package httpapi

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enowdev/enowx-rag/mcp-server/pkg/core"
	"github.com/enowdev/enowx-rag/mcp-server/pkg/rag"
)

// A store uploaded in parts (as behind Cloudflare's 100 MB request limit)
// gives the same plan as a single upload; a part at the wrong offset is
// refused with what the server holds, so a client can resume.
func TestImportInParts(t *testing.T) {
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
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(raw)
	zw.Close()
	gz := buf.Bytes()

	call := func(method, path string, body []byte) (int, map[string]any) {
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.RemoteAddr = "127.0.0.1:1"
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}

	code, start := call(http.MethodPost, "/api/import/parts", nil)
	id, _ := start["upload_id"].(string)
	if code != http.StatusOK || id == "" || start["part_bytes"].(float64) <= 0 {
		t.Fatalf("start = %d %v", code, start)
	}

	third := len(gz)/3 + 1
	var offset int
	for i := 0; offset < len(gz); i++ {
		end := min(offset+third, len(gz))
		if i == 1 {
			// A part that skips ahead is refused with what the server holds.
			code, out := call(http.MethodPut, fmt.Sprintf("/api/import/parts/%s?offset=%d", id, offset+1), gz[offset+1:end])
			if code != http.StatusConflict || int(out["received"].(float64)) != offset {
				t.Fatalf("skipping part = %d %v, want 409 with received=%d", code, out, offset)
			}
			// Finishing early is refused too.
			if code, _ := call(http.MethodPost, fmt.Sprintf("/api/import/parts/%s/finish?size=%d", id, len(gz)), nil); code != http.StatusConflict {
				t.Fatalf("early finish = %d, want 409", code)
			}
		}
		code, out := call(http.MethodPut, fmt.Sprintf("/api/import/parts/%s?offset=%d", id, offset), gz[offset:end])
		if code != http.StatusOK || int(out["received"].(float64)) != end {
			t.Fatalf("part %d = %d %v, want received=%d", i, code, out, end)
		}
		offset = end
	}

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/import/parts/%s/finish?size=%d", id, len(gz)), nil)
	req.RemoteAddr = "127.0.0.1:1"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	var plan struct {
		UploadID string                   `json:"upload_id"`
		Projects []core.ImportPlanProject `json:"projects"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &plan)
	if w.Code != http.StatusOK || plan.UploadID == "" || len(plan.Projects) != 1 || !plan.Projects[0].ReuseVectors {
		t.Fatalf("finish = %d %s, want one project reusing its vectors", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(newUploadStore().dir, id+".part")); !os.IsNotExist(err) {
		t.Errorf("the .part file is still there after finishing (err=%v)", err)
	}
	// The partial is gone; the finished upload can be started as usual.
	if code, _ := call(http.MethodPut, fmt.Sprintf("/api/import/parts/%s?offset=0", id), []byte("x")); code != http.StatusNotFound {
		t.Errorf("part after finish = %d, want 404", code)
	}
	code, _ = call(http.MethodPost, "/api/import/start", []byte(`{"upload_id":"`+plan.UploadID+`"}`))
	if code != http.StatusAccepted {
		t.Errorf("start import = %d, want 202", code)
	}

	// Discarding an unfinished upload removes it.
	_, start = call(http.MethodPost, "/api/import/parts", nil)
	id2 := start["upload_id"].(string)
	call(http.MethodPut, "/api/import/parts/"+id2+"?offset=0", gz[:10])
	if code, _ := call(http.MethodDelete, "/api/import/parts/"+id2, nil); code != http.StatusOK {
		t.Errorf("discard = %d, want 200", code)
	}
	if code, out := call(http.MethodPost, "/api/import/parts/"+id2+"/finish", nil); code != http.StatusNotFound {
		t.Errorf("finish after discard = %d %v, want 404", code, out)
	}
	if code, out := call(http.MethodPut, "/api/import/parts/"+id2+"?offset=x", nil); code != http.StatusNotFound || !strings.Contains(fmt.Sprint(out["error"]), "not found") {
		t.Errorf("part after discard = %d %v, want 404", code, out)
	}
}
