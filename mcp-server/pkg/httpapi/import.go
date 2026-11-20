package httpapi

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/enowdev/enowx-rag/mcp-server/pkg/config"
	"github.com/enowdev/enowx-rag/mcp-server/pkg/core"
	"github.com/enowdev/enowx-rag/mcp-server/pkg/rag"
	"github.com/go-chi/chi/v5"
)

// Import flow: a client uploads a snapshot of its local SQLite store
// (POST /api/import/upload, raw or gzip bytes), sees what importing would do
// (vectors reused or re-embedded), then starts the import as a job
// (POST /api/import/start). Uploads expire after uploadTTL.

const (
	uploadTTL      = time.Hour
	maxUploadBytes = 4 << 30 // 4 GiB
)

type upload struct {
	ID       string
	Path     string
	Size     int64
	Created  time.Time
	Projects []core.ImportPlanProject
}

type uploadStore struct {
	mu    sync.Mutex
	dir   string
	items map[string]*upload
}

func newUploadStore() *uploadStore {
	return &uploadStore{dir: filepath.Join(config.Dir(), "uploads"), items: map[string]*upload{}}
}

// sweep removes expired uploads (and stray files left by a previous run).
func (u *uploadStore) sweep() {
	u.mu.Lock()
	defer u.mu.Unlock()
	for id, it := range u.items {
		if time.Since(it.Created) > uploadTTL {
			_ = os.Remove(it.Path)
			delete(u.items, id)
		}
	}
	entries, err := os.ReadDir(u.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		id := strings.TrimSuffix(e.Name(), ".db")
		if _, live := u.items[id]; live {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > uploadTTL {
			_ = os.Remove(filepath.Join(u.dir, e.Name()))
		}
	}
}

func (u *uploadStore) get(id string) (*upload, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	it, ok := u.items[id]
	if ok && time.Since(it.Created) > uploadTTL {
		_ = os.Remove(it.Path)
		delete(u.items, id)
		return nil, false
	}
	return it, ok
}

func (u *uploadStore) put(it *upload) {
	u.mu.Lock()
	u.items[it.ID] = it
	u.mu.Unlock()
}

func (u *uploadStore) remove(id string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	it, ok := u.items[id]
	if ok {
		_ = os.Remove(it.Path)
		delete(u.items, id)
	}
	return ok
}

// ImportUpload handles POST /api/import/upload: the body is a SQLite store
// file (rag.db), raw or gzip-compressed. Returns what importing would do.
func (h *Handlers) ImportUpload(w http.ResponseWriter, r *http.Request) {
	h.uploads.sweep()
	if err := os.MkdirAll(h.uploads.dir, 0o700); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot create the upload folder: "+err.Error())
		return
	}
	id := strings.Replace(newJobID(), "j_", "u_", 1)
	path := filepath.Join(h.uploads.dir, id+".db")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(path)
		}
	}()

	body := bufio.NewReader(http.MaxBytesReader(w, r.Body, maxUploadBytes))
	var src io.Reader = body
	// gzip by header or by magic bytes (curl --data-binary @rag.db.gz).
	if magic, _ := body.Peek(2); strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") ||
		(len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b) {
		zr, err := gzip.NewReader(body)
		if err != nil {
			f.Close()
			writeErr(w, http.StatusBadRequest, "the gzip data is invalid: "+err.Error())
			return
		}
		defer zr.Close()
		src = zr
	}
	n, err := io.Copy(f, io.LimitReader(src, maxUploadBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, "upload failed: "+err.Error())
		return
	}
	if n > maxUploadBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "the store is larger than 4 GiB")
		return
	}
	if n == 0 {
		writeErrHint(w, http.StatusBadRequest, "the upload is empty", "send the rag.db file as the request body")
		return
	}

	snap, err := rag.OpenSQLiteSnapshot(path)
	if err != nil {
		writeErrHint(w, http.StatusBadRequest, "this is not an enowx-rag SQLite store: "+err.Error(),
			"upload ~/.enowx-rag/rag.db (or the file sqlite_path points to)")
		return
	}
	plan, err := h.svc.PlanImport(r.Context(), snap)
	snap.Close()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "cannot read the store: "+err.Error())
		return
	}
	if len(plan) == 0 {
		writeErrHint(w, http.StatusBadRequest, "the store has no projects", "index something locally first, or nothing needs importing")
		return
	}
	keep = true
	h.uploads.put(&upload{ID: id, Path: path, Size: n, Created: time.Now(), Projects: plan})
	writeJSON(w, http.StatusOK, map[string]any{
		"upload_id":    id,
		"size_bytes":   n,
		"expires_in_s": int(uploadTTL.Seconds()),
		"projects":     plan,
		"estimate":     h.importEstimate(plan),
	})
}

// importEstimate summarizes the cost of importing plan: how many chunks keep
// their vectors and how many must be embedded again (Voyage tokens ≈ chars/4).
func (h *Handlers) importEstimate(plan []core.ImportPlanProject) map[string]any {
	model, dim := h.svc.EmbedIdentity()
	var points, reuse int
	var reembedChars int64
	for _, p := range plan {
		points += p.Points
		reuse += p.ReusePoints
		if p.Points > 0 {
			reembedChars += p.Chars * int64(p.Points-p.ReusePoints) / int64(p.Points)
		}
	}
	tokens := reembedChars / 4
	est := map[string]any{
		"points":         points,
		"reuse_points":   reuse,
		"reembed_points": points - reuse,
		"reembed_tokens": tokens,
		"model":          model,
		"dim":            dim,
	}
	switch {
	case points == reuse:
		est["note"] = fmt.Sprintf("Every vector fits %s at %d dimensions: nothing is re-embedded, the import costs no Voyage tokens.", model, dim)
	case reuse == 0:
		est["note"] = fmt.Sprintf("These vectors were made by another model or size: all %d chunks are embedded again with %s (≈%s tokens).", points, model, humanCount(tokens))
	default:
		est["note"] = fmt.Sprintf("%d chunks keep their vectors; %d are embedded again with %s (≈%s tokens).", reuse, points-reuse, model, humanCount(tokens))
	}
	if info, ok := rag.LookupVoyageModel(model); ok && info.PricePerMTok > 0 && tokens > 0 {
		est["reembed_cost_usd"] = float64(tokens) / 1e6 * info.PricePerMTok
	}
	return est
}

func humanCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

// ImportStart handles POST /api/import/start {upload_id, projects, mode, rename}.
func (h *Handlers) ImportStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UploadID string            `json:"upload_id"`
		Projects []string          `json:"projects"`
		Mode     string            `json:"mode"`
		Rename   map[string]string `json:"rename"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	up, ok := h.uploads.get(req.UploadID)
	if !ok {
		writeErrHint(w, http.StatusNotFound, "upload not found or expired", "upload the store again")
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "merge"
	}
	if mode != "merge" && mode != "replace" {
		writeErr(w, http.StatusBadRequest, "mode must be merge or replace")
		return
	}
	known := map[string]bool{}
	for _, p := range up.Projects {
		known[p.ProjectID] = true
	}
	for _, p := range req.Projects {
		if !known[p] {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("project %q is not in the upload", p))
			return
		}
	}
	if job := h.jobs.Running("import", ""); job != nil {
		writeJSON(w, http.StatusAccepted, map[string]any{"job": job, "note": "An import is already running — following it."})
		return
	}
	label := "all projects"
	if len(req.Projects) > 0 {
		label = strings.Join(req.Projects, ", ")
	}
	opts := core.ImportOptions{Projects: req.Projects, Mode: mode, Rename: req.Rename}
	job := h.jobs.Start("import", "", "Importing "+label, func(ctx context.Context, update JobUpdate) (map[string]any, error) {
		snap, err := rag.OpenSQLiteSnapshot(up.Path)
		if err != nil {
			return nil, err
		}
		defer func() {
			snap.Close()
			h.uploads.remove(up.ID)
		}()
		total := 0
		for _, p := range up.Projects {
			if len(req.Projects) == 0 || inList(req.Projects, p.ProjectID) {
				total += p.Points
			}
		}
		base := 0
		last := ""
		lastDone := 0
		results, err := h.svc.ImportSnapshot(ctx, snap, opts, func(project string, done, _ int) {
			if project != last {
				base += lastDone
				last, lastDone = project, 0
			}
			lastDone = done
			update(base+done, total, fmt.Sprintf("Importing %s: %d chunks", project, done))
		})
		if err == nil {
			update(total, total, core.ImportSummary(results))
		}
		return map[string]any{"projects": results}, err
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"job": job})
}

func inList(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ImportDiscard handles DELETE /api/import/upload/{id}.
func (h *Handlers) ImportDiscard(w http.ResponseWriter, r *http.Request) {
	if !h.uploads.remove(chi.URLParam(r, "id")) {
		writeErr(w, http.StatusNotFound, "upload not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "discarded"})
}
