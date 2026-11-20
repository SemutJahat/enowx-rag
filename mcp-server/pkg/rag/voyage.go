package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

const (
	voyageAPIURL   = "https://api.voyageai.com/v1/embeddings"
	voyageMaxBatch = 128
)

// VoyageEmbeddingClient calls the Voyage AI embeddings API.
type VoyageEmbeddingClient struct {
	APIKey  string
	Model   string
	Dim     int // 0 = default 1024
	client  *http.Client
	baseURL string       // override for testing; defaults to voyageAPIURL
	tokens  atomic.Int64 // cumulative total_tokens reported by the API
	// fixedDim: the model has one output size, so output_dimension is not sent.
	fixedDim bool
}

// NewVoyageEmbeddingClient creates a Voyage AI embedding client.
// model defaults to "voyage-4" if empty.
// dim sets the output dimension (Matryoshka). 0 defaults to 1024.
func NewVoyageEmbeddingClient(apiKey, model string, dim int) *VoyageEmbeddingClient {
	if model == "" {
		model = "voyage-4"
	}
	if dim == 0 {
		dim = 1024
		if info, ok := LookupVoyageModel(model); ok {
			dim = info.DefaultDim
		}
	}
	fixed := false
	if info, ok := LookupVoyageModel(model); ok {
		// Fixed-size models (voyage-3, voyage-law-2, …) reject output_dimension:
		// they always return their own size, which is what gets stored.
		if d, ok := info.FixedDim(); ok {
			dim, fixed = d, true
		}
	}
	return &VoyageEmbeddingClient{
		APIKey:   apiKey,
		Model:    model,
		Dim:      dim,
		fixedDim: fixed,
		baseURL:  voyageEndpoint("embeddings"),
		client:   &http.Client{Timeout: 120 * time.Second},
	}
}

// voyageEndpoint is the URL of a Voyage API endpoint ("embeddings",
// "rerank"): RAG_VOYAGE_BASE_URL (e.g. a Voyage-compatible gateway,
// "https://gw.example.com/v1") when set, else "" for the official API.
func voyageEndpoint(name string) string {
	if base := strings.TrimRight(os.Getenv("RAG_VOYAGE_BASE_URL"), "/"); base != "" {
		return base + "/" + name
	}
	return ""
}

type voyageRequest struct {
	Input           []string `json:"input"`
	Model           string   `json:"model"`
	InputType       string   `json:"input_type,omitempty"`
	OutputDimension int      `json:"output_dimension,omitempty"`
}

type voyageResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Usage struct {
		TotalTokens int64 `json:"total_tokens"`
	} `json:"usage"`
}

// Embed returns one embedding per input text.
// Splits into batches of voyageMaxBatch automatically.
func (c *VoyageEmbeddingClient) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return c.embedWithType(ctx, texts, "document")
}

// EmbedQuery embeds a single query text with input_type=query for better retrieval.
func (c *VoyageEmbeddingClient) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	vecs, err := c.embedWithType(ctx, []string{text}, "query")
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("empty embedding response")
	}
	return vecs[0], nil
}

func (c *VoyageEmbeddingClient) embedWithType(ctx context.Context, texts []string, inputType string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	out := make([][]float32, len(texts))
	for i := 0; i < len(texts); i += voyageMaxBatch {
		end := i + voyageMaxBatch
		if end > len(texts) {
			end = len(texts)
		}
		vecs, err := c.embedBatch(ctx, texts[i:end], inputType)
		if err != nil {
			return nil, err
		}
		copy(out[i:end], vecs)
	}
	return out, nil
}

func (c *VoyageEmbeddingClient) embedBatch(ctx context.Context, texts []string, inputType string) ([][]float32, error) {
	req := voyageRequest{Input: texts, Model: c.Model, InputType: inputType}
	if !c.fixedDim {
		req.OutputDimension = c.Dim
	}
	body, _ := json.Marshal(req)

	url := c.baseURL
	if url == "" {
		url = voyageAPIURL
	}

	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.client.Do(hreq)
	if err != nil {
		return nil, fmt.Errorf("voyage request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, voyageError("embeddings", c.Model, resp.StatusCode, b)
	}

	var result voyageResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode voyage response: %w", err)
	}
	c.tokens.Add(result.Usage.TotalTokens)

	vecs := make([][]float32, len(texts))
	for _, d := range result.Data {
		if d.Index < len(vecs) {
			vecs[d.Index] = d.Embedding
		}
	}
	return vecs, nil
}

// ModelName returns the configured model name. Implements ModelNamer.
func (c *VoyageEmbeddingClient) ModelName() string {
	return c.Model
}

// VectorSize returns the configured dimension, defaulting to 1024 when Dim is 0.
func (c *VoyageEmbeddingClient) VectorSize() int {
	if c.Dim > 0 {
		return c.Dim
	}
	return 1024
}

// TokensUsed returns the cumulative total_tokens reported by the Voyage
// embeddings API since this client was created. Implements TokenCounter.
func (c *VoyageEmbeddingClient) TokensUsed() int64 {
	return c.tokens.Load()
}

var _ TokenCounter = (*VoyageEmbeddingClient)(nil)
