package rag

import "strings"

// VoyageModelInfo describes one Voyage AI embedding model, for the dashboard's
// model picker and for request shaping (which models take output_dimension).
// Figures are from docs.voyageai.com (embeddings + pricing pages, checked
// 2026-11). Prices are USD per 1M tokens; FreeMTok is the free allowance in
// millions of tokens.
type VoyageModelInfo struct {
	ID            string  `json:"id"`
	Family        string  `json:"family"`
	Status        string  `json:"status"` // latest | older | deprecated
	Recommended   bool    `json:"recommended"`
	ContextTokens int     `json:"context_tokens"`
	DefaultDim    int     `json:"default_dim"`
	Dims          []int   `json:"dims"` // one entry = fixed size (no output_dimension)
	PricePerMTok  float64 `json:"price_per_mtok"`
	FreeMTok      int     `json:"free_mtok"`
	BestFor       string  `json:"best_for"`
	Notes         string  `json:"notes,omitempty"`
}

// VoyageRerankInfo describes one Voyage AI reranker model.
type VoyageRerankInfo struct {
	ID            string  `json:"id"`
	Status        string  `json:"status"` // latest | older
	Recommended   bool    `json:"recommended"`
	ContextTokens int     `json:"context_tokens"`
	QueryTokens   int     `json:"query_tokens"`
	PricePerMTok  float64 `json:"price_per_mtok"`
	FreeMTok      int     `json:"free_mtok"`
	BestFor       string  `json:"best_for"`
}

var flexDims = []int{256, 512, 1024, 2048}

// voyageEmbedModels is the embedding catalog, newest first.
var voyageEmbedModels = []VoyageModelInfo{
	{ID: "voyage-4-large", Family: "voyage-4", Status: "latest", ContextTokens: 32000, DefaultDim: 1024, Dims: flexDims,
		PricePerMTok: 0.12, FreeMTok: 200, BestFor: "Best general-purpose and multilingual retrieval quality",
		Notes: "Shares the Voyage 4 embedding space: index with voyage-4-large, switch to voyage-4 / voyage-4-lite later without re-embedding."},
	{ID: "voyage-4", Family: "voyage-4", Status: "latest", Recommended: true, ContextTokens: 32000, DefaultDim: 1024, Dims: flexDims,
		PricePerMTok: 0.06, FreeMTok: 200, BestFor: "General-purpose and multilingual retrieval — the balanced default",
		Notes: "Shares the Voyage 4 embedding space with voyage-4-large and voyage-4-lite."},
	{ID: "voyage-4-lite", Family: "voyage-4", Status: "latest", ContextTokens: 32000, DefaultDim: 1024, Dims: flexDims,
		PricePerMTok: 0.02, FreeMTok: 200, BestFor: "Lowest latency and cost",
		Notes: "Shares the Voyage 4 embedding space with voyage-4-large and voyage-4."},
	{ID: "voyage-code-4", Family: "voyage-code-4", Status: "latest", ContextTokens: 32000, DefaultDim: 1024, Dims: flexDims,
		PricePerMTok: 0.12, FreeMTok: 200, BestFor: "Code retrieval and coding agents",
		Notes: "Its own embedding space: switching to or from it re-embeds every chunk."},
	{ID: "voyage-finance-2", Family: "voyage-finance-2", Status: "latest", ContextTokens: 32000, DefaultDim: 1024, Dims: []int{1024},
		PricePerMTok: 0.12, FreeMTok: 50, BestFor: "Finance retrieval and RAG"},
	{ID: "voyage-law-2", Family: "voyage-law-2", Status: "latest", ContextTokens: 16000, DefaultDim: 1024, Dims: []int{1024},
		PricePerMTok: 0.12, FreeMTok: 50, BestFor: "Legal retrieval and RAG"},
	{ID: "voyage-3-large", Family: "voyage-3-large", Status: "older", ContextTokens: 32000, DefaultDim: 1024, Dims: flexDims,
		PricePerMTok: 0.18, BestFor: "Previous-generation general and multilingual retrieval"},
	{ID: "voyage-3.5", Family: "voyage-3.5", Status: "older", ContextTokens: 32000, DefaultDim: 1024, Dims: flexDims,
		PricePerMTok: 0.06, BestFor: "Previous-generation general and multilingual retrieval"},
	{ID: "voyage-3.5-lite", Family: "voyage-3.5-lite", Status: "older", ContextTokens: 32000, DefaultDim: 1024, Dims: flexDims,
		PricePerMTok: 0.02, BestFor: "Previous-generation latency and cost"},
	{ID: "voyage-code-3", Family: "voyage-code-3", Status: "older", ContextTokens: 32000, DefaultDim: 1024, Dims: flexDims,
		PricePerMTok: 0.18, BestFor: "Previous-generation code retrieval"},
	{ID: "voyage-3", Family: "voyage-3", Status: "older", ContextTokens: 32000, DefaultDim: 1024, Dims: []int{1024},
		PricePerMTok: 0.06, BestFor: "General and multilingual retrieval"},
	{ID: "voyage-3-lite", Family: "voyage-3-lite", Status: "older", ContextTokens: 32000, DefaultDim: 512, Dims: []int{512},
		PricePerMTok: 0.02, BestFor: "Latency and cost"},
	{ID: "voyage-multilingual-2", Family: "voyage-multilingual-2", Status: "older", ContextTokens: 32000, DefaultDim: 1024, Dims: []int{1024},
		PricePerMTok: 0.12, BestFor: "Multilingual retrieval and RAG"},
	{ID: "voyage-code-2", Family: "voyage-code-2", Status: "older", ContextTokens: 16000, DefaultDim: 1536, Dims: []int{1536},
		PricePerMTok: 0.12, FreeMTok: 50, BestFor: "Code retrieval (first generation)"},
	{ID: "voyage-2", Family: "voyage-2", Status: "deprecated", ContextTokens: 4000, DefaultDim: 1024, Dims: []int{1024},
		PricePerMTok: 0.10, BestFor: "Legacy — move to voyage-4"},
}

// voyageRerankModels is the reranker catalog, newest first.
var voyageRerankModels = []VoyageRerankInfo{
	{ID: "rerank-3", Status: "latest", Recommended: true, ContextTokens: 32000, QueryTokens: 8000,
		PricePerMTok: 0.05, FreeMTok: 200, BestFor: "Highest accuracy — recommended for most applications"},
	{ID: "rerank-3-lite", Status: "latest", ContextTokens: 32000, QueryTokens: 8000,
		PricePerMTok: 0.02, FreeMTok: 200, BestFor: "Fast and cost-effective, for latency-sensitive search"},
	{ID: "rerank-2.5", Status: "older", ContextTokens: 32000, QueryTokens: 8000,
		PricePerMTok: 0.05, FreeMTok: 200, BestFor: "Previous generation: quality, instruction-following, multilingual"},
	{ID: "rerank-2.5-lite", Status: "older", ContextTokens: 32000, QueryTokens: 8000,
		PricePerMTok: 0.02, FreeMTok: 200, BestFor: "Previous generation: latency and quality"},
	{ID: "rerank-2", Status: "older", ContextTokens: 16000, QueryTokens: 4000,
		PricePerMTok: 0.05, FreeMTok: 200, BestFor: "Second generation, quality"},
	{ID: "rerank-2-lite", Status: "older", ContextTokens: 8000, QueryTokens: 2000,
		PricePerMTok: 0.02, FreeMTok: 200, BestFor: "Second generation, latency"},
}

// VoyageCompatNote explains which models share an embedding space.
const VoyageCompatNote = "All Voyage 4 series embeddings (voyage-4-large, voyage-4, voyage-4-lite, voyage-4-nano) " +
	"share one embedding space: switching between them at the same dimension keeps the stored vectors. " +
	"Any other model or dimension change re-embeds every chunk from its stored text (automatic, costs tokens)."

// VoyageCatalogSource names where the catalog figures come from.
const VoyageCatalogSource = "docs.voyageai.com/docs/embeddings, /docs/reranker, /docs/pricing (checked 2026-11)"

// VoyageEmbedModels returns a copy of the embedding catalog.
func VoyageEmbedModels() []VoyageModelInfo {
	return append([]VoyageModelInfo(nil), voyageEmbedModels...)
}

// VoyageRerankModels returns a copy of the reranker catalog.
func VoyageRerankModels() []VoyageRerankInfo {
	return append([]VoyageRerankInfo(nil), voyageRerankModels...)
}

// LookupVoyageModel returns the catalog entry for an embedding model.
func LookupVoyageModel(id string) (VoyageModelInfo, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, m := range voyageEmbedModels {
		if m.ID == id {
			return m, true
		}
	}
	return VoyageModelInfo{}, false
}

// LookupVoyageReranker returns the catalog entry for a reranker model.
func LookupVoyageReranker(id string) (VoyageRerankInfo, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, m := range voyageRerankModels {
		if m.ID == id {
			return m, true
		}
	}
	return VoyageRerankInfo{}, false
}

// FixedDim reports whether the model has a single output size (the API rejects
// output_dimension for it) and that size.
func (m VoyageModelInfo) FixedDim() (int, bool) {
	if len(m.Dims) == 1 {
		return m.Dims[0], true
	}
	return 0, false
}

// SupportsDim reports whether dim is one of the model's output sizes.
func (m VoyageModelInfo) SupportsDim(dim int) bool {
	for _, d := range m.Dims {
		if d == dim {
			return true
		}
	}
	return false
}

// voyageFamily is the embedding space a model writes into: models of one
// family produce comparable vectors. Unknown models are their own family.
func voyageFamily(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if m, ok := LookupVoyageModel(model); ok {
		return m.Family
	}
	if model == "voyage-4-nano" {
		return "voyage-4"
	}
	return model
}

// EmbedModelsCompatible reports whether vectors written by model a can be
// searched with queries embedded by model b (same embedding space). Equal
// names always are; Voyage 4 series models are with each other.
func EmbedModelsCompatible(a, b string) bool {
	if strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) {
		return true
	}
	return voyageFamily(a) == voyageFamily(b) && voyageFamily(a) == "voyage-4"
}
