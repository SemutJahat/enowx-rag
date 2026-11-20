// Package ragbuild constructs rag.Provider + embedder instances from an
// explicit, self-contained spec (no env vars, no global config). This lets both
// the process startup (cmd/mcp-server) and the migration orchestrator build
// providers — including a *destination* provider with a different store, model,
// dimension, or pgvector table — without depending on the main package.
package ragbuild

import (
	"context"
	"fmt"
	"strings"

	"github.com/enowdev/enowx-rag/mcp-server/pkg/rag"
)

// Spec fully describes one provider + embedder to build.
type Spec struct {
	VectorStore string // "sqlite" | "qdrant" | "chroma" | "pgvector" | "remote"
	Embedder    string // "voyage" | "openai" | "tei"

	// Vector store connection.
	QdrantURL     string
	QdrantAPIKey  string
	ChromaURL     string
	PGVectorDSN   string
	PGVectorTable string // pgvector table name; defaults to "project_memory"
	SQLitePath    string // the built-in store's file

	// Embedder settings.
	VoyageAPIKey  string
	VoyageModel   string
	VoyageDim     int
	OpenAIAPIKey  string
	OpenAIModel   string
	OpenAIBaseURL string
	OpenAIDim     int
	TEIURL        string

	// Client mode (VectorStore "remote"): the enowx-rag server and its token.
	RemoteURL   string
	RemoteToken string

	// AllowUnconfigured opens the store with a stand-in embedder when the
	// embedder can't be built yet (e.g. no Voyage key on a fresh server): the
	// store can be browsed, embedding calls fail with what to set.
	AllowUnconfigured bool
}

// BuildEmbedder constructs the embedding client described by the spec.
func BuildEmbedder(spec Spec) (rag.EmbeddingClient, error) {
	switch strings.ToLower(spec.Embedder) {
	case "tei":
		return rag.NewTEIEmbeddingClient(spec.TEIURL), nil
	case "voyage":
		if spec.VoyageAPIKey == "" {
			return nil, fmt.Errorf("the Voyage API key is not set (voyage.api_key / RAG_VOYAGE_API_KEY)")
		}
		return rag.NewVoyageEmbeddingClient(spec.VoyageAPIKey, spec.VoyageModel, spec.VoyageDim), nil
	case "openai":
		if spec.OpenAIModel == "" {
			return nil, fmt.Errorf("openai_model is required for the openai embedder")
		}
		return rag.NewOpenAIEmbeddingClient(spec.OpenAIAPIKey, spec.OpenAIModel, spec.OpenAIBaseURL, spec.OpenAIDim), nil
	default:
		return nil, fmt.Errorf("unsupported embedder: %s", spec.Embedder)
	}
}

// BuildProvider constructs the vector-store provider described by the spec,
// wiring in the embedder.
func BuildProvider(ctx context.Context, spec Spec) (rag.Provider, error) {
	if strings.EqualFold(spec.VectorStore, "remote") {
		return rag.NewRemoteProvider(ctx, spec.RemoteURL, spec.RemoteToken)
	}
	embedder, err := BuildEmbedder(spec)
	if err != nil {
		if !spec.AllowUnconfigured {
			return nil, err
		}
		embedder = &rag.UnconfiguredEmbedder{Model: spec.VoyageModel, Dim: spec.VoyageDim,
			Reason: err.Error() + " — add it in the dashboard (Setup or Settings → API keys) or set RAG_VOYAGE_API_KEY"}
	}
	switch strings.ToLower(spec.VectorStore) {
	case "sqlite":
		if spec.SQLitePath == "" {
			return nil, fmt.Errorf("sqlite_path is required for the sqlite store")
		}
		return rag.NewSQLiteProvider(spec.SQLitePath, embedder)
	case "qdrant":
		return rag.NewQdrantProvider(ctx, spec.QdrantURL, spec.QdrantAPIKey, embedder)
	case "chroma":
		return rag.NewChromaProvider(spec.ChromaURL, embedder), nil
	case "pgvector":
		table := spec.PGVectorTable
		if table == "" {
			table = "project_memory"
		}
		return rag.NewPGVectorProvider(ctx, spec.PGVectorDSN, embedder, table)
	default:
		return nil, fmt.Errorf("unsupported vector store: %s", spec.VectorStore)
	}
}
