package rag

import (
	"context"
	"errors"
	"hash/fnv"
	"math"
	"testing"
)

// sizedEmbedder returns deterministic vectors of its size, named by model.
type sizedEmbedder struct {
	model string
	dim   int
	err   error
	calls int
}

func (e *sizedEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	e.calls++
	if e.err != nil {
		return nil, e.err
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		h := fnv.New32a()
		h.Write([]byte(t))
		seed := float64(h.Sum32()%1000) / 1000
		v := make([]float32, e.dim)
		for j := range v {
			v[j] = float32(math.Sin(seed*float64(j+1)) + 1.5)
		}
		out[i] = v
	}
	return out, nil
}
func (e *sizedEmbedder) VectorSize() int   { return e.dim }
func (e *sizedEmbedder) ModelName() string { return e.model }

const hostTestTable = "project_memory_hosttest"

func openHostPG(t *testing.T, emb EmbeddingClient) *PGVectorProvider {
	t.Helper()
	p, err := NewPGVectorProvider(context.Background(), pgvectorDSN, emb, hostTestTable)
	if err != nil {
		t.Skipf("PostgreSQL not available: %v", err)
	}
	return p
}

func freshHostPG(t *testing.T, emb EmbeddingClient) *PGVectorProvider {
	t.Helper()
	p := openHostPG(t, emb)
	if _, err := p.pool.Exec(context.Background(), "DROP TABLE IF EXISTS "+hostTestTable); err != nil {
		t.Fatal(err)
	}
	p.Close()
	p = openHostPG(t, emb)
	t.Cleanup(func() { p.Close() })
	return p
}

// A model with another vector size resizes the column only after a real
// embedding confirms the size; a failing embedder (missing key) never does.
func TestPGVectorResizesOnlyAfterProbe(t *testing.T) {
	ctx := context.Background()
	p := freshHostPG(t, &sizedEmbedder{model: "m-a", dim: 4})
	if err := p.Index(ctx, "proj", []Document{{ID: "a", Content: "alpha"}, {ID: "b", Content: "beta"}}); err != nil {
		t.Fatal(err)
	}

	// Failing embedder at another size: the column and the vectors stay.
	broken := openHostPG(t, &sizedEmbedder{model: "m-b", dim: 6, err: errors.New("no key")})
	if d, _ := broken.columnDim(ctx); d != 4 {
		t.Fatalf("column dim = %d after a failing probe, want 4", d)
	}
	broken.Close()

	// Working embedder at another size: resized, old vectors cleared.
	p2 := openHostPG(t, &sizedEmbedder{model: "m-b", dim: 6})
	defer p2.Close()
	if d, _ := p2.columnDim(ctx); d != 6 {
		t.Fatalf("column dim = %d, want 6", d)
	}
	if _, err := p2.SemanticSearch(ctx, "proj", "alpha", 5); err != nil {
		t.Fatalf("search with cleared vectors: %v", err)
	}
	stats, err := p2.EmbedStats(ctx, "proj")
	if err != nil || len(stats) != 1 || stats[0].Model != "m-a" || stats[0].Dim != 4 || stats[0].Count != 2 {
		t.Fatalf("EmbedStats = %+v (%v), want 2 points of m-a/4 waiting", stats, err)
	}
}

// ListPointIDs honors metadata filters (sync must not touch another
// directory's chunks).
func TestPGVectorListPointsMetaFilter(t *testing.T) {
	ctx := context.Background()
	p := freshHostPG(t, &sizedEmbedder{model: "m", dim: 4})
	docs := []Document{
		{ID: "a1", Content: "x", Meta: map[string]string{"source_dir": "a", "source_file": "f1"}},
		{ID: "a2", Content: "y", Meta: map[string]string{"source_dir": "a", "source_file": "f2"}},
		{ID: "b1", Content: "z", Meta: map[string]string{"source_dir": "b", "source_file": "f1"}},
	}
	if err := p.Index(ctx, "proj", docs); err != nil {
		t.Fatal(err)
	}
	ids, err := p.ListPointIDs(ctx, "proj", map[string]string{"source_dir": "a"})
	if err != nil || len(ids) != 2 {
		t.Fatalf("ListPointIDs(source_dir=a) = %v (%v), want a1 a2", ids, err)
	}
	pts, err := p.ListPoints(ctx, "proj", map[string]string{"source_dir": "b", "source_file": "f1"})
	if err != nil || len(pts) != 1 || pts[0].ID != "b1" {
		t.Fatalf("ListPoints(b, f1) = %+v (%v), want b1", pts, err)
	}
	if n, _ := p.CountFiles(ctx, "proj"); n != 2 {
		t.Errorf("CountFiles = %d, want 2", n)
	}
}

// Vectors survive an export/import round trip; payload updates keep them.
func TestPGVectorVectorRoundTrip(t *testing.T) {
	ctx := context.Background()
	p := freshHostPG(t, &sizedEmbedder{model: "m", dim: 4})
	vec := []float32{0.123456789, -1.5, 3.25e-7, 42}
	doc := Document{ID: "v1", Content: "hello world", Meta: map[string]string{"embed_model": "m", "source_file": "a.go"}}
	if err := p.IndexVectors(ctx, "proj", []Document{doc}, [][]float32{vec}); err != nil {
		t.Fatal(err)
	}
	if err := p.IndexVectors(ctx, "proj", []Document{doc}, [][]float32{{1, 2}}); err == nil {
		t.Fatal("IndexVectors accepted a vector of the wrong size")
	}
	if err := p.UpdatePayload(ctx, "proj", []Document{{ID: "v1", Content: "hello again", Meta: map[string]string{"start_line": "3"}}}); err != nil {
		t.Fatal(err)
	}
	docs, vecs, err := p.ExportPointsWithVectors(ctx, "proj")
	if err != nil || len(docs) != 1 {
		t.Fatalf("export = %d (%v)", len(docs), err)
	}
	if docs[0].Content != "hello again" || docs[0].Meta["start_line"] != "3" || docs[0].Meta["source_file"] != "a.go" {
		t.Errorf("payload = %q %v", docs[0].Content, docs[0].Meta)
	}
	for i := range vec {
		if vecs[0][i] != vec[i] {
			t.Fatalf("vector[%d] = %v, want %v", i, vecs[0][i], vec[i])
		}
	}
	info, err := p.StoreInfo(ctx)
	if err != nil || info.Rows != 1 || info.ServerVersion == "" || info.ExtensionVersion == "" || info.Dim != 4 {
		t.Fatalf("StoreInfo = %+v (%v)", info, err)
	}
}

func TestProbePostgres(t *testing.T) {
	ctx := context.Background()
	if _, err := NewPGVectorProvider(ctx, pgvectorDSN, &sizedEmbedder{model: "m", dim: 4}, hostTestTable); err != nil {
		t.Skipf("PostgreSQL not available: %v", err)
	}
	pr, err := ProbePostgres(ctx, pgvectorDSN, true)
	if err != nil || pr.ExtensionVersion == "" || pr.ServerVersion == "" {
		t.Fatalf("probe = %+v (%v)", pr, err)
	}
	if _, err := ProbePostgres(ctx, "postgres://nobody@127.0.0.1:1/x?connect_timeout=2", false); err == nil {
		t.Fatal("probe of a closed port succeeded")
	}
}
