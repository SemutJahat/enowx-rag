package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/enowdev/enowx-rag/mcp-server/pkg/core"
)

// fakeCloudflare stands in for a server behind Cloudflare: it refuses
// requests larger than limit with Cloudflare's HTML 413, fails one part with
// a 502 page, and loses the answer to one part that did arrive (the client
// must then resume from what the server holds).
type fakeCloudflare struct {
	limit int64

	mu         sync.Mutex
	data       []byte
	parts      int
	failedOnce bool
	lostOnce   bool
	userAgents []string
	finished   []byte
}

func (f *fakeCloudflare) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.userAgents = append(f.userAgents, r.UserAgent())
	cfPage := func(code int) {
		w.Header().Set("Server", "cloudflare")
		w.Header().Set("Cf-Ray", "8f00000000000001-SIN")
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(code)
		fmt.Fprintf(w, "<html><body>%d</body></html>", code)
	}
	if r.ContentLength > f.limit {
		cfPage(http.StatusRequestEntityTooLarge)
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/import/parts":
		json.NewEncoder(w).Encode(map[string]any{"upload_id": "u_test", "part_bytes": 4 << 20, "max_part_bytes": 256 << 20})
	case r.Method == http.MethodPut && r.URL.Path == "/api/import/parts/u_test":
		off, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
		if off != int64(len(f.data)) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]any{"error": "wrong offset", "received": len(f.data)})
			return
		}
		f.parts++
		if f.parts == 2 && !f.failedOnce {
			f.failedOnce = true
			io.Copy(io.Discard, r.Body)
			cfPage(http.StatusBadGateway)
			return
		}
		b, _ := io.ReadAll(r.Body)
		f.data = append(f.data, b...)
		if f.parts == 4 && !f.lostOnce {
			// The part arrived, but the answer is lost on the way back.
			f.lostOnce = true
			cfPage(http.StatusGatewayTimeout)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"received": len(f.data)})
	case r.Method == http.MethodPost && r.URL.Path == "/api/import/parts/u_test/finish":
		if r.URL.Query().Get("size") != strconv.Itoa(len(f.data)) {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]any{"error": "size mismatch", "received": len(f.data)})
			return
		}
		f.finished = f.data
		json.NewEncoder(w).Encode(map[string]any{"upload_id": "u_done", "size_bytes": len(f.data)})
	case r.Method == http.MethodDelete:
		json.NewEncoder(w).Encode(map[string]any{"status": "discarded"})
	default:
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{"error": "API endpoint not found"})
	}
}

// push uploads in parts, splits parts the proxy refuses as too large,
// retries a failed part and resumes after a lost answer; the server ends up
// with exactly the compressed store.
func TestUploadSnapshotThroughSizeLimitedProxy(t *testing.T) {
	src := filepath.Join(t.TempDir(), "snap.db")
	raw := make([]byte, 6<<20) // random: gzip can't shrink it
	rand.Read(raw)
	if err := os.WriteFile(src, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &fakeCloudflare{limit: 3 << 20} // the server suggests 4 MiB parts; the proxy takes 3
	ts := httptest.NewServer(fake)
	defer ts.Close()

	plan, err := uploadSnapshot(context.Background(), newAPIClient(ts.URL, "tok"), src, 0)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if plan.UploadID != "u_done" {
		t.Fatalf("plan = %+v", plan)
	}
	zr, err := gzip.NewReader(bytes.NewReader(fake.finished))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(zr)
	if !bytes.Equal(got, raw) {
		t.Fatalf("the server got %d bytes that differ from the %d-byte store", len(got), len(raw))
	}
	if !fake.failedOnce || !fake.lostOnce {
		t.Errorf("the 502 (%v) and the lost answer (%v) were not exercised", fake.failedOnce, fake.lostOnce)
	}
	for _, ua := range fake.userAgents {
		if !strings.HasPrefix(ua, "enowx-rag/") {
			t.Fatalf("User-Agent %q", ua)
		}
	}
	if _, err := os.Stat(src + ".gz"); !os.IsNotExist(err) {
		t.Errorf("the compressed temp file is left behind")
	}
}

// `push --project X` shows and counts only X: a project after X in the plan
// must not appear as if it were imported (or replaced).
func TestPrintPlanShowsOnlySelectedProjects(t *testing.T) {
	plan := &importPlan{Projects: []core.ImportPlanProject{
		{ProjectID: "alpha", Points: 10, ReuseVectors: true, ReusePoints: 10, EmbedModel: "voyage-4", EmbedDim: 1024, Exists: true},
		{ProjectID: "beta-with-a-rather-long-project-name", Points: 5, ReuseVectors: true, ReusePoints: 5, EmbedModel: "voyage-4", EmbedDim: 1024, Exists: true},
		{ProjectID: "gamma", Points: 7, EmbedModel: "other", EmbedDim: 768},
	}}

	var buf bytes.Buffer
	reembed, missing := printPlan(&buf, plan, []string{"alpha"}, "replace")
	out := buf.String()
	if !strings.Contains(out, "alpha") || strings.Contains(out, "beta") || strings.Contains(out, "gamma") {
		t.Errorf("plan for alpha shows other projects:\n%s", out)
	}
	if reembed != 0 || len(missing) != 0 {
		t.Errorf("reembed = %d (gamma counted?), missing = %v", reembed, missing)
	}

	buf.Reset()
	if _, missing = printPlan(&buf, plan, []string{"alpha", "nope"}, "merge"); len(missing) != 1 || missing[0] != "nope" {
		t.Errorf("missing = %v, want [nope]", missing)
	}

	buf.Reset()
	reembed, missing = printPlan(&buf, plan, nil, "merge")
	out = buf.String()
	if strings.Count(out, "(project exists: merge)") != 2 || !strings.Contains(out, "7 re-embedded") || reembed != 7 || len(missing) != 0 {
		t.Errorf("full plan: reembed = %d, missing = %v\n%s", reembed, missing, out)
	}
	// Columns line up even with a name longer than the default width.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	col := strings.Index(lines[0], "CHUNKS") + len("CHUNKS")
	for _, l := range lines[1:] {
		if len(l) < col || l[col-1] == ' ' {
			t.Errorf("CHUNKS column misaligned:\n%s", out)
			break
		}
	}
}

// A Cloudflare WAF block is reported as such, never as a bad token.
func TestCheckServerBehindCloudflareBlock(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "cloudflare")
		w.Header().Set("Cf-Ray", "8f00000000000002-SIN")
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, "<html>Sorry, you have been blocked</html>")
	}))
	defer ts.Close()
	_, err := newAPIClient(ts.URL, "tok").checkServer(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Cloudflare blocked") || strings.Contains(err.Error(), "token (401)") ||
		strings.Contains(err.Error(), "is the URL right") {
		t.Fatalf("err = %v", err)
	}
	if statusOf(err) != http.StatusForbidden {
		t.Errorf("status = %d", statusOf(err))
	}
}
