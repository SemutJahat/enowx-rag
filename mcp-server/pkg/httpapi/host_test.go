package httpapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enowdev/enowx-rag/mcp-server/pkg/core"
	"github.com/enowdev/enowx-rag/mcp-server/pkg/indexer"
	"github.com/enowdev/enowx-rag/mcp-server/pkg/rag"
)

// Upload a local SQLite store (gzip), see the plan, import it as a job.
func TestImportUploadAndStart(t *testing.T) {
	embedURL := fakeEmbeddings(t, 4).URL
	srcPath := filepath.Join(t.TempDir(), "local.db")
	runningSQLite(t, srcPath, embedURL).Close()

	dest, err := rag.NewSQLiteProvider(filepath.Join(t.TempDir(), "server.db"), rag.NewOpenAIEmbeddingClient("", "old-model", embedURL, 4))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dest.Close() })
	_, router := newTestServer(t, dest, nil)

	raw, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(raw)
	zw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/import/upload", &gz)
	req.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	var plan struct {
		UploadID string                   `json:"upload_id"`
		Projects []core.ImportPlanProject `json:"projects"`
		Estimate map[string]any           `json:"estimate"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &plan)
	if w.Code != http.StatusOK || plan.UploadID == "" || len(plan.Projects) != 1 || !plan.Projects[0].ReuseVectors {
		t.Fatalf("upload = %d %s, want one project reusing its vectors", w.Code, w.Body.String())
	}
	if !strings.Contains(plan.Estimate["note"].(string), "nothing is re-embedded") {
		t.Errorf("estimate note = %v", plan.Estimate["note"])
	}

	body := `{"upload_id":"` + plan.UploadID + `","projects":["p"],"rename":{"p":"p-remote"}}`
	req = httptest.NewRequest(http.MethodPost, "/api/import/start", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1"
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	var started struct{ Job Job }
	_ = json.Unmarshal(w.Body.Bytes(), &started)
	if w.Code != http.StatusAccepted {
		t.Fatalf("start = %d %s", w.Code, w.Body.String())
	}
	job := waitJob(t, router, started.Job.ID)
	if job.State != "done" || job.Done != 2 {
		t.Fatalf("import job = %+v, want done with 2 chunks", job)
	}
	if n, _ := dest.CountPoints(context.Background(), "p-remote"); n != 2 {
		t.Errorf("p-remote holds %d points, want 2", n)
	}

	// The upload is gone once imported; garbage is refused.
	req = httptest.NewRequest(http.MethodPost, "/api/import/start", strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1"
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("second start = %d, want 404", w.Code)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/import/upload", strings.NewReader("not a database"))
	req.RemoteAddr = "127.0.0.1:1"
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("garbage upload = %d, want 400", w.Code)
	}
}

// Client mode: a local service with a RemoteProvider indexes a local folder
// into the server, searches through it, and the server records the source.
func TestClientModeRoundTrip(t *testing.T) {
	embedURL := fakeEmbeddings(t, 4).URL
	store, err := rag.NewSQLiteProvider(filepath.Join(t.TempDir(), "server.db"), rag.NewOpenAIEmbeddingClient("", "m", embedURL, 4))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	serverSvc, router := newTestServer(t, store, nil)
	serverSvc.SetSourceRegistry(core.NewSourceRegistry(filepath.Join(t.TempDir(), "sources.json")))
	t.Setenv("RAG_ADMIN_TOKEN", "tok")
	srv := httptest.NewServer(router)
	defer srv.Close()

	if _, err := rag.NewRemoteProvider(context.Background(), srv.URL, "wrong"); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("wrong token: %v, want a token error", err)
	}
	remote, err := rag.NewRemoteProvider(context.Background(), srv.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	local := core.NewService(remote, nil, indexer.NewIndexer(remote, 1500))

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {\n\tprintln(\"hello remote\")\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	res, err := local.IndexProject(ctx, "cm", dir)
	if err != nil || res.Indexed == 0 {
		t.Fatalf("IndexProject = %+v (%v)", res, err)
	}
	if n, _ := store.CountPoints(ctx, "cm"); n != res.Indexed {
		t.Fatalf("server holds %d points, want %d", n, res.Indexed)
	}
	// A second sync sees nothing new (incremental through the API).
	res2, err := local.IndexProject(ctx, "cm", dir)
	if err != nil || res2.Indexed != 0 || res2.Deleted != 0 {
		t.Fatalf("second sync = %+v (%v), want no changes", res2, err)
	}
	hits, err := local.Search(ctx, "cm", "hello remote", core.SearchOpts{K: 3})
	if err != nil || len(hits) == 0 {
		t.Fatalf("search through the server = %v (%v)", hits, err)
	}
	srcs := serverSvc.ProjectSources("cm")
	if len(srcs) != 1 || srcs[0].Dir != dir {
		t.Fatalf("server sources = %+v, want %s", srcs, dir)
	}

	// The server can't sync a folder that only exists on the client; a
	// path-free reindex re-embeds instead and says where the code lives.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/projects/cm/reindex", nil)
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"mode":"reembed"`) {
		t.Fatalf("reindex = %d %s, want a reembed job", w.Code, w.Body.String())
	}
	var resp struct{ Job Job }
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	t.Setenv("RAG_ADMIN_TOKEN", "")
	if job := waitJob(t, router, resp.Job.ID); job.State != "done" {
		t.Fatalf("reembed job = %+v", job)
	}
}
