package core

import (
	"context"
	"hash/fnv"
	"os"
	"path/filepath"
	"testing"

	"github.com/enowdev/enowx-rag/mcp-server/pkg/rag"
)

type importEmbedder struct {
	model string
	dim   int
	calls int
}

func (e *importEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	e.calls += len(texts)
	out := make([][]float32, len(texts))
	for i, t := range texts {
		h := fnv.New32a()
		h.Write([]byte(t))
		v := make([]float32, e.dim)
		for j := range v {
			v[j] = float32((h.Sum32()>>uint(j%24))%97) + 1
		}
		out[i] = v
	}
	return out, nil
}
func (e *importEmbedder) VectorSize() int   { return e.dim }
func (e *importEmbedder) ModelName() string { return e.model }

// sourceStore writes a SQLite store with two projects embedded by model/dim.
func sourceStore(t *testing.T, model string, dim int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rag.db")
	src, err := rag.NewSQLiteProvider(path, &importEmbedder{model: model, dim: dim})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, proj := range []string{"alpha", "beta"} {
		docs := []rag.Document{
			{ID: proj + "-1", Content: "func one() {}", Meta: map[string]string{"source_file": "a.go", "source_dir": "/src/" + proj}},
			{ID: proj + "-2", Content: "func two() {}", Meta: map[string]string{"source_file": "b.go", "source_dir": "/src/" + proj}},
			{ID: proj + "-3", Content: "a note", Meta: map[string]string{"type": "decision"}},
		}
		if err := src.Index(ctx, proj, docs); err != nil {
			t.Fatal(err)
		}
	}
	src.Close()
	return path
}

func importInto(t *testing.T, dest rag.Provider, srcPath string, opts ImportOptions) ([]ImportPlanProject, []ImportResult) {
	t.Helper()
	svc := NewService(dest, nil, nil)
	snap, err := rag.OpenSQLiteSnapshot(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	ctx := context.Background()
	plan, err := svc.PlanImport(ctx, snap)
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.ImportSnapshot(ctx, snap, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	return plan, res
}

// Same model and size: vectors are reused, nothing is embedded.
func TestImportSnapshotReusesVectors(t *testing.T) {
	src := sourceStore(t, "voyage-4-lite", 8)
	emb := &importEmbedder{model: "voyage-4", dim: 8} // same Voyage 4 space
	dest, err := rag.NewSQLiteProvider(filepath.Join(t.TempDir(), "dest.db"), emb)
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	plan, res := importInto(t, dest, src, ImportOptions{Projects: []string{"alpha"}, Rename: map[string]string{"alpha": "alpha2"}})
	if len(plan) != 2 || !plan[0].ReuseVectors || plan[0].Points != 3 {
		t.Fatalf("plan = %+v, want 2 projects reusing vectors", plan)
	}
	if len(res) != 1 || res[0].Dest != "alpha2" || res[0].Reused != 3 || res[0].Reembedded != 0 {
		t.Fatalf("result = %+v, want alpha → alpha2 with 3 reused", res)
	}
	if emb.calls != 0 {
		t.Errorf("embedder called for %d texts, want 0", emb.calls)
	}
	n, _ := dest.CountPoints(context.Background(), "alpha2")
	if n != 3 {
		t.Errorf("alpha2 has %d points, want 3", n)
	}
}

// Another model: the stored text is embedded again with the running model.
func TestImportSnapshotReembedsOtherModel(t *testing.T) {
	src := sourceStore(t, "voyage-code-3", 8)
	emb := &importEmbedder{model: "voyage-4", dim: 8}
	dest, err := rag.NewSQLiteProvider(filepath.Join(t.TempDir(), "dest.db"), emb)
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	plan, res := importInto(t, dest, src, ImportOptions{Mode: "replace"})
	if plan[0].ReuseVectors {
		t.Fatalf("plan = %+v, want re-embedding", plan)
	}
	total := 0
	for _, r := range res {
		total += r.Reembedded
	}
	if total != 6 || emb.calls != 6 {
		t.Fatalf("re-embedded %d (embedder saw %d), want 6", total, emb.calls)
	}
	pts, _ := dest.ListPoints(context.Background(), "beta", map[string]string{"source_dir": "/src/beta"})
	if len(pts) != 2 {
		t.Errorf("beta keeps %d points of /src/beta, want 2 (metadata kept)", len(pts))
	}
}

// SQLite → pgvector (needs RAG_TEST_PGVECTOR_DSN).
func TestImportSnapshotIntoPGVector(t *testing.T) {
	dsn := os.Getenv("RAG_TEST_PGVECTOR_DSN")
	if dsn == "" {
		t.Skip("set RAG_TEST_PGVECTOR_DSN to run")
	}
	src := sourceStore(t, "voyage-4", 8)
	emb := &importEmbedder{model: "voyage-4", dim: 8}
	ctx := context.Background()
	dest, err := rag.NewPGVectorProvider(ctx, dsn, emb, "project_memory_importtest")
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	_ = dest.DeleteCollection(ctx, "alpha")
	_ = dest.DeleteCollection(ctx, "beta")
	_, res := importInto(t, dest, src, ImportOptions{})
	for _, r := range res {
		if r.Reused != 3 {
			t.Fatalf("result = %+v, want 3 reused per project", res)
		}
	}
	got, err := dest.SemanticSearch(ctx, "alpha", "func one() {}", 1)
	if err != nil || len(got) != 1 || got[0].ID != "alpha-1" {
		t.Fatalf("search after import = %+v (%v), want alpha-1 first", got, err)
	}
	if emb.calls > 1 { // the query embedding only
		t.Errorf("embedder embedded %d texts during the import, want only the query", emb.calls)
	}
}
