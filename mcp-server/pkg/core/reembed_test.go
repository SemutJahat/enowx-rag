package core

import (
	"context"
	"errors"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/enowdev/enowx-rag/mcp-server/pkg/indexer"
	"github.com/enowdev/enowx-rag/mcp-server/pkg/rag"
)

// modelEmbedder hashes words into dim buckets, seeded by the model name, so
// two models give unrelated vectors: switching it stands for changing the
// embedding model in the config. texts counts every text it embeds.
type modelEmbedder struct {
	name  string
	dim   int
	texts *atomic.Int64
}

func newModelEmbedder(name string, dim int) modelEmbedder {
	return modelEmbedder{name: name, dim: dim, texts: &atomic.Int64{}}
}

func (e modelEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	e.texts.Add(int64(len(texts)))
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, e.dim)
		for _, w := range strings.FieldsFunc(strings.ToLower(t), func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
		}) {
			h := fnv.New32a()
			h.Write([]byte(e.name + "/" + w))
			v[h.Sum32()%uint32(e.dim)]++
		}
		out[i] = v
	}
	return out, nil
}

func (e modelEmbedder) VectorSize() int   { return e.dim }
func (e modelEmbedder) ModelName() string { return e.name }

// modelChangeFixture indexes a folder and a memory note under model-a, then
// reopens the same store with emb, as after editing the config and
// restarting. It returns the new service, the folder and the point count.
func modelChangeFixture(t *testing.T, emb rag.EmbeddingClient) (*Service, string, int) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "rag.db")
	dir := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "auth.go"), "package app\n\nfunc zebraLogin(user string) bool {\n\treturn user != \"\"\n}\n")

	old, err := rag.NewSQLiteProvider(path, newModelEmbedder("model-a", 64))
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(old, nil, indexer.NewIndexer(old, 1000))
	if _, err := svc.IndexProject(ctx, "p", dir); err != nil {
		t.Fatal(err)
	}
	note := rag.Document{ID: "note-1", Content: "decision: penguin workers own the retry queue", Meta: map[string]string{"type": "decision"}}
	if err := svc.IndexDocuments(ctx, "p", []rag.Document{note}); err != nil {
		t.Fatal(err)
	}
	docs, err := old.ExportPoints(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	old.Close()

	p, err := rag.NewSQLiteProvider(path, emb)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return NewService(p, nil, indexer.NewIndexer(p, 1000)), dir, len(docs)
}

func assertAllStamped(t *testing.T, svc *Service, model, dim string) {
	t.Helper()
	docs, err := svc.ExportProject(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		if d.Meta["embed_model"] != model || d.Meta["embed_dim"] != dim {
			t.Errorf("%s still embedded by %s/%s, want %s/%s", d.ID, d.Meta["embed_model"], d.Meta["embed_dim"], model, dim)
		}
	}
}

func searchFinds(t *testing.T, svc *Service, query, want string) bool {
	t.Helper()
	res, err := svc.Search(context.Background(), "p", query, SearchOpts{K: 3})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if strings.Contains(r.Content, want) {
			return true
		}
	}
	return false
}

// After the embedding model and size change, the first search re-embeds the
// project's chunks and memory notes from their stored text, so both are found
// again, with no auto-sync and no rag_index_project call. It happens once.
func TestModelChange_SearchReembedsChunksAndNotes(t *testing.T) {
	emb := newModelEmbedder("model-b", 32)
	svc, _, points := modelChangeFixture(t, emb)

	if !searchFinds(t, svc, "penguin retry queue", "penguin workers") {
		t.Error("memory note not found after the model change")
	}
	// Every point once, one probe measuring the new vector size, the query.
	if got := emb.texts.Load(); got != int64(points)+2 {
		t.Errorf("embedded %d texts on the first search, want %d points + probe + query", got, points)
	}
	assertAllStamped(t, svc, "model-b", "32")

	emb.texts.Store(0)
	if !searchFinds(t, svc, "zebraLogin user", "zebraLogin") {
		t.Error("code chunk not found after the model change")
	}
	if got := emb.texts.Load(); got != 1 {
		t.Errorf("second search embedded %d texts, want only its query", got)
	}
}

// A model change at the same vector size is caught by the model name: the old
// vectors would still be compared, giving wrong results without an error.
func TestModelChange_SameSizeIsReembedded(t *testing.T) {
	svc, _, _ := modelChangeFixture(t, newModelEmbedder("model-b", 64))
	if !searchFinds(t, svc, "penguin retry queue", "penguin workers") {
		t.Error("memory note not found after the model change")
	}
	assertAllStamped(t, svc, "model-b", "64")
}

// rag_index_project after a model change reports the re-embedded points, and
// does not embed the unchanged chunks a second time.
func TestModelChange_IndexProjectReportsReembedded(t *testing.T) {
	emb := newModelEmbedder("model-b", 32)
	svc, dir, points := modelChangeFixture(t, emb)

	res, err := svc.IndexProject(context.Background(), "p", dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Reembedded != points || res.Indexed != 0 {
		t.Errorf("reembedded %d indexed %d, want reembedded %d indexed 0", res.Reembedded, res.Indexed, points)
	}
	if got := emb.texts.Load(); got != int64(points)+1 {
		t.Errorf("embedded %d texts, want %d points + probe", got, points)
	}
	assertAllStamped(t, svc, "model-b", "32")
}

// misreportingEmbedder reports a vector size it doesn't produce, as TEI does
// when its /info can't be reached (it falls back to a default).
type misreportingEmbedder struct{ modelEmbedder }

func (misreportingEmbedder) VectorSize() int { return 16 }

// A wrong reported size alone doesn't re-embed the project: the size is
// measured on a real vector before anything is re-embedded.
func TestMisreportedSize_NothingReembedded(t *testing.T) {
	emb := misreportingEmbedder{newModelEmbedder("model-a", 64)}
	svc, dir, _ := modelChangeFixture(t, emb)

	res, err := svc.IndexProject(context.Background(), "p", dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Reembedded != 0 || emb.texts.Load() != 1 {
		t.Errorf("reembedded %d, embedded %d texts over a misreported size; want 0 and only the probe",
			res.Reembedded, emb.texts.Load())
	}
	assertAllStamped(t, svc, "model-a", "64")
}

// failingIndex is a store whose writes fail, like a Qdrant collection or a
// pgvector table refusing vectors of another size.
type failingIndex struct {
	*rag.SQLiteProvider
	calls *atomic.Int64
}

func (f failingIndex) Index(context.Context, string, []rag.Document) error {
	f.calls.Add(1)
	return errors.New("wrong vector size")
}

// A re-embed that fails doesn't fail the search, and isn't tried again on
// the next search; rag_index_project reports it.
func TestReembedFailure_SearchStillAnswers(t *testing.T) {
	emb := newModelEmbedder("model-b", 64)
	svc, dir, _ := modelChangeFixture(t, emb)
	store := failingIndex{svc.Provider().(*rag.SQLiteProvider), &atomic.Int64{}}
	svc = NewService(store, nil, indexer.NewIndexer(store, 1000))

	if _, err := svc.Search(context.Background(), "p", "penguin", SearchOpts{K: 3}); err != nil {
		t.Fatalf("search failed with the re-embed: %v", err)
	}
	if _, err := svc.Search(context.Background(), "p", "penguin", SearchOpts{K: 3}); err != nil {
		t.Fatal(err)
	}
	if n := store.calls.Load(); n != 1 {
		t.Errorf("re-embed tried %d times across two searches, want 1", n)
	}
	svc = NewService(store, nil, indexer.NewIndexer(store, 1000)) // a fresh process
	if _, err := svc.IndexProject(context.Background(), "p", dir); err == nil || !strings.Contains(err.Error(), "Migration") {
		t.Errorf("IndexProject error = %v, want the re-embed failure with a Migration hint", err)
	}
}

// With the model unchanged, nothing is re-embedded: a restart costs nothing.
func TestSameModel_NothingReembedded(t *testing.T) {
	emb := newModelEmbedder("model-a", 64)
	svc, dir, _ := modelChangeFixture(t, emb)

	if !searchFinds(t, svc, "penguin retry queue", "penguin workers") {
		t.Error("memory note not found")
	}
	res, err := svc.IndexProject(context.Background(), "p", dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Reembedded != 0 || emb.texts.Load() != 1 {
		t.Errorf("reembedded %d, embedded %d texts; want 0 and only the query", res.Reembedded, emb.texts.Load())
	}
}
