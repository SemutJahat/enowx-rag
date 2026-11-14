package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enowdev/enowx-rag/mcp-server/pkg/config"
	"github.com/enowdev/enowx-rag/mcp-server/pkg/core"
	"github.com/enowdev/enowx-rag/mcp-server/pkg/rag"
)

// fakeEmbeddings serves an OpenAI-compatible /embeddings endpoint returning
// dim-sized vectors, so a migration can re-embed without a network.
func fakeEmbeddings(t *testing.T, dim int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		data := make([]map[string]any, len(req.Input))
		for i := range req.Input {
			v := make([]float32, dim)
			v[i%dim] = 1
			data[i] = map[string]any{"index": i, "embedding": v}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "usage": map[string]any{"total_tokens": len(req.Input)}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// runningSQLite is the running store: SQLite at path, holding project "p".
func runningSQLite(t *testing.T, path, embedURL string) *rag.SQLiteProvider {
	t.Helper()
	p, err := rag.NewSQLiteProvider(path, rag.NewOpenAIEmbeddingClient("", "old-model", embedURL, 4))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	docs := []rag.Document{
		{ID: "app/a.go#1", Content: "File: a.go:1-3\n\nfunc A() {}", Meta: map[string]string{"source_file": "a.go", "source_dir": "app"}},
		{ID: "note-1", Content: "decision: keep it simple", Meta: map[string]string{"type": "decision"}},
	}
	if err := p.Index(context.Background(), "p", docs); err != nil {
		t.Fatal(err)
	}
	return p
}

// postMigrate sends body to /api/migrate and, when it is accepted, waits for
// the migration to finish. It returns the HTTP status and the final event.
func postMigrate(t *testing.T, svc *core.Service, router http.Handler, body string) (int, string) {
	t.Helper()
	events := svc.Events().Subscribe()
	defer svc.Events().Unsubscribe(events)
	req := httptest.NewRequest(http.MethodPost, "/api/migrate", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		return w.Code, w.Body.String()
	}
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev := <-events:
			switch ev.Type {
			case "migration_completed":
				return w.Code, ev.Type
			case "migration_failed":
				return w.Code, fmt.Sprintf("%s: %v", ev.Type, ev.Data)
			}
		case <-timeout:
			t.Fatal("migration did not finish")
		}
	}
}

// A migration can write into a SQLite store: the points are re-embedded by
// the destination embedder and stamped with its model.
func TestMigrateEndpointToSQLite(t *testing.T) {
	embedURL := fakeEmbeddings(t, 4).URL
	src := runningSQLite(t, filepath.Join(t.TempDir(), "rag.db"), embedURL)
	svc, router := newTestServer(t, src, nil)

	dest := filepath.Join(t.TempDir(), "dest.db")
	body := fmt.Sprintf(`{"source_project":"p","dest_project":"p2","vector_store":"sqlite","sqlite_path":%q,
		"embedder":"openai","openai_base_url":%q,"openai_model":"new-model","openai_dim":8}`, dest, fakeEmbeddings(t, 8).URL)
	code, final := postMigrate(t, svc, router, body)
	if code != http.StatusAccepted || final != "migration_completed" {
		t.Fatalf("migrate = %d %s", code, final)
	}

	p, err := rag.NewSQLiteProvider(dest, rag.NewOpenAIEmbeddingClient("", "new-model", embedURL, 8))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	docs, err := p.ExportPoints(context.Background(), "p2")
	if err != nil || len(docs) != 2 {
		t.Fatalf("destination holds %d points (%v), want 2", len(docs), err)
	}
	for _, d := range docs {
		if d.Meta["embed_model"] != "new-model" || d.Meta["embed_dim"] != "8" {
			t.Errorf("%s stamped %s/%s, want new-model/8", d.ID, d.Meta["embed_model"], d.Meta["embed_dim"])
		}
	}
}

// After the embedding model changed, a reindex reports the points it
// re-embedded (the memory note included, which no file backs).
func TestReindexReportsReembedded(t *testing.T) {
	embedURL := fakeEmbeddings(t, 4).URL
	path := filepath.Join(t.TempDir(), "rag.db")
	runningSQLite(t, path, embedURL).Close()
	p, err := rag.NewSQLiteProvider(path, rag.NewOpenAIEmbeddingClient("", "new-model", embedURL, 4))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	_, router := newTestServer(t, p, nil)

	body := fmt.Sprintf(`{"directory":%q}`, t.TempDir())
	req := httptest.NewRequest(http.MethodPost, "/api/projects/p/reindex", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if w.Code != http.StatusOK || resp["points_reembedded"] != float64(2) {
		t.Fatalf("reindex = %d %s, want 200 with points_reembedded 2", w.Code, w.Body.String())
	}
}

// A typed "~/" path is the home folder; an empty one the running store's file.
func TestSQLiteDestPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	running := &config.Config{VectorStore: "sqlite", SQLitePath: "/data/rag.db"}
	for in, want := range map[string]string{
		"":                "/data/rag.db",
		"  ":              "/data/rag.db",
		"~/stores/new.db": filepath.Join(home, "stores", "new.db"),
		"/abs/other.db":   "/abs/other.db",
	} {
		if got := sqliteDestPath(in, running); got != want {
			t.Errorf("sqliteDestPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// Without sqlite_path the destination is the running store's file (from the
// config). Migrating a project into itself there is refused; into another
// project name of that file it works.
func TestMigrateEndpointSQLiteDefaultsToRunningStore(t *testing.T) {
	defer helperClearRagEnv(t)()
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := filepath.Join(home, ".enowx-rag", "rag.db")
	if err := os.MkdirAll(filepath.Dir(store), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".enowx-rag", "config.yaml"), []byte("vector_store: sqlite\nembedder: openai\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	embedURL := fakeEmbeddings(t, 4).URL
	src := runningSQLite(t, store, embedURL)
	svc, router := newTestServer(t, src, nil)

	spec := `"vector_store":"sqlite","embedder":"openai","openai_base_url":%q,"openai_model":"new-model","openai_dim":4`
	same := fmt.Sprintf(`{"source_project":"p","dest_project":"p",`+spec+`}`, embedURL)
	if code, msg := postMigrate(t, svc, router, same); code != http.StatusBadRequest || !strings.Contains(msg, "must differ") {
		t.Fatalf("migrate into itself = %d %s, want 400 must differ", code, msg)
	}

	other := fmt.Sprintf(`{"source_project":"p","dest_project":"p-new",`+spec+`}`, embedURL)
	code, final := postMigrate(t, svc, router, other)
	if code != http.StatusAccepted || final != "migration_completed" {
		t.Fatalf("migrate = %d %s", code, final)
	}
	docs, err := src.ExportPoints(context.Background(), "p-new")
	if err != nil || len(docs) != 2 {
		t.Fatalf("running store holds %d points in p-new (%v), want 2", len(docs), err)
	}
}
