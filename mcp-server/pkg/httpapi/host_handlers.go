package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/enowdev/enowx-rag/mcp-server/pkg/config"
	"github.com/enowdev/enowx-rag/mcp-server/pkg/core"
	"github.com/enowdev/enowx-rag/mcp-server/pkg/rag"
	"github.com/go-chi/chi/v5"
)

// writeErrHint writes an error with a hint saying what to do about it.
func writeErrHint(w http.ResponseWriter, code int, msg, hint string) {
	writeJSON(w, code, map[string]string{"error": msg, "hint": hint})
}

// requestClient names the caller for metrics: a client-mode machine sends its
// host name in X-Enowx-Client; anything else is the dashboard/API.
func requestClient(r *http.Request) string {
	if c := strings.TrimSpace(r.Header.Get("X-Enowx-Client")); c != "" {
		if len(c) > 64 {
			c = c[:64]
		}
		return c
	}
	return "api"
}

// Health handles GET /api/health (open): liveness and the boot id the
// dashboard watches to know a restart finished.
func (h *Handlers) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"version":       h.opts.Version,
		"boot_id":       h.opts.BootID,
		"uptime_s":      int64(time.Since(h.opts.StartedAt).Seconds()),
		"auth_required": config.EffectiveAdminToken() != "",
	})
}

// ServerInfo handles GET /api/server: everything the monitoring dashboard
// shows about this server — the store and its health, the models in use,
// auth, auto-sync and background work.
func (h *Handlers) ServerInfo(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	rt := h.opts.Runtime
	host, _ := os.Hostname()

	store := map[string]any{"type": rt.VectorStore, "ok": true}
	var contentChars int64
	if si, ok := h.svc.Provider().(rag.StoreInfoer); ok {
		info, err := si.StoreInfo(ctx)
		raw, _ := json.Marshal(info)
		_ = json.Unmarshal(raw, &store)
		contentChars = info.ContentChars
		if err != nil {
			store["ok"] = false
			store["error"] = err.Error()
		}
	} else if rp, ok := h.svc.Provider().(*rag.RemoteProvider); ok {
		store["target"] = rp.BaseURL()
	}

	embedder := map[string]any{"type": rt.Embedder, "model": rt.EmbedModel, "dim": rt.EmbedDim,
		"key_set": rt.EmbedKeySet, "ready": rt.EmbedReady}
	if rt.EmbedProblem != "" {
		embedder["problem"] = rt.EmbedProblem
	}
	if rt.Embedder == "voyage" {
		if info, ok := rag.LookupVoyageModel(rt.EmbedModel); ok {
			embedder["info"] = info
		}
	}
	reranker := map[string]any{"enabled": rt.RerankerModel != "", "model": rt.RerankerModel}
	if rt.RerankerModel != "" {
		if info, ok := rag.LookupVoyageReranker(rt.RerankerModel); ok {
			reranker["info"] = info
		}
	} else if rt.RerankerReason != "" {
		reranker["reason"] = rt.RerankerReason
	}
	stale, _ := h.svc.StalePoints(ctx)

	writeJSON(w, http.StatusOK, map[string]any{
		"version":           h.opts.Version,
		"boot_id":           h.opts.BootID,
		"started_at":        h.opts.StartedAt.UTC(),
		"uptime_s":          int64(time.Since(h.opts.StartedAt).Seconds()),
		"hostname":          host,
		"os":                runtime.GOOS + "/" + runtime.GOARCH,
		"go_version":        runtime.Version(),
		"auth_required":     config.EffectiveAdminToken() != "",
		"config_path":       config.Path(),
		"env_locked":        config.EnvLocked(),
		"restart_supported": h.opts.Restart != nil,
		"store":             store,
		"embedder":          embedder,
		"reranker":          reranker,
		"auto_sync":         map[string]any{"enabled": rt.AutoSync != "", "interval": rt.AutoSync},
		"content_chars":     contentChars,
		"stale_points":      stale,
		"jobs_running":      h.jobs.RunningCount(),
	})
}

// Models handles GET /api/models: the Voyage embedding and rerank catalog
// with context sizes, dimensions, prices and what each is best for.
func (h *Handlers) Models(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"embedding":   rag.VoyageEmbedModels(),
		"rerank":      rag.VoyageRerankModels(),
		"compat_note": rag.VoyageCompatNote,
		"source":      rag.VoyageCatalogSource,
	})
}

// ModelsTest handles POST /api/models/test: embeds a sentence with the given
// model and size, and reranks two documents with the given reranker, using
// the given key or the saved one. Reports the real vector size, latency and
// tokens, or Voyage's error with a hint.
func (h *Handlers) ModelsTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		VoyageAPIKey string `json:"voyage_api_key"`
		EmbedModel   string `json:"embed_model"`
		Dim          int    `json:"dim"`
		RerankModel  string `json:"rerank_model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	key := strings.TrimSpace(req.VoyageAPIKey)
	if key == "" {
		key = runningConfig().Voyage.APIKey
	}
	if key == "" {
		writeErrHint(w, http.StatusBadRequest, "no Voyage API key", "enter a key (it starts with \"pa-\") — get one at dashboard.voyageai.com")
		return
	}
	if req.EmbedModel == "" {
		req.EmbedModel = "voyage-4"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, map[string]any{
		"embed":  testVoyageModel(ctx, key, req.EmbedModel, req.Dim),
		"rerank": testVoyageReranker(ctx, key, req.RerankModel),
	})
}

func testVoyageModel(ctx context.Context, key, model string, dim int) map[string]any {
	client := rag.NewVoyageEmbeddingClient(key, model, dim)
	start := time.Now()
	vecs, err := client.Embed(ctx, []string{"enowx-rag connection test"})
	lat := time.Since(start).Milliseconds()
	if err != nil {
		return map[string]any{"ok": false, "message": err.Error(), "latency_ms": lat}
	}
	got := 0
	if len(vecs) == 1 {
		got = len(vecs[0])
	}
	return map[string]any{"ok": got > 0, "latency_ms": lat, "dim": got, "tokens": client.TokensUsed(),
		"message": fmt.Sprintf("%s returned a %d-dimension vector in %d ms", model, got, lat)}
}

func testVoyageReranker(ctx context.Context, key, model string) any {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "", "off", "none":
		return nil
	}
	rr := rag.NewVoyageReranker(key, model)
	start := time.Now()
	hits, err := rr.Rerank(ctx, "how do I configure the database connection",
		[]string{"The weather is nice and sunny today.", "To configure the database, set RAG_PGVECTOR_DSN to the PostgreSQL connection string."}, 2)
	lat := time.Since(start).Milliseconds()
	if err != nil {
		return map[string]any{"ok": false, "message": err.Error(), "latency_ms": lat}
	}
	ok := len(hits) > 0 && hits[0].Index == 1
	msg := fmt.Sprintf("%s ranked the relevant document first in %d ms", model, lat)
	if !ok {
		msg = fmt.Sprintf("%s answered in %d ms but ranked unexpectedly", model, lat)
	}
	return map[string]any{"ok": len(hits) > 0, "message": msg, "latency_ms": lat}
}

// CreateProject handles POST /api/projects/{id} (idempotent).
func (h *Handlers) CreateProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if strings.TrimSpace(id) == "" {
		writeErr(w, http.StatusBadRequest, "project id is required")
		return
	}
	if err := h.svc.CreateProject(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "project_id": id})
}

type docsBody struct {
	Documents []struct {
		ID      string            `json:"id"`
		Content string            `json:"content"`
		Meta    map[string]string `json:"meta"`
	} `json:"documents"`
	Quiet bool `json:"quiet"`
}

func (b docsBody) docs() []rag.Document {
	out := make([]rag.Document, len(b.Documents))
	for i, d := range b.Documents {
		out[i] = rag.Document{ID: d.ID, Content: d.Content, Meta: d.Meta}
	}
	return out
}

// maxDocsPerRequest bounds one documents request (the indexer sends 100).
const maxDocsPerRequest = 1000

// IndexDocuments handles POST /api/projects/{id}/documents: embed and store
// documents (client-mode sync batches set quiet to skip per-batch events).
func (h *Handlers) IndexDocuments(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body docsBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if len(body.Documents) > maxDocsPerRequest {
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("at most %d documents per request", maxDocsPerRequest))
		return
	}
	docs := body.docs()
	var err error
	if body.Quiet {
		err = h.svc.IndexDocumentsQuiet(r.Context(), id, docs)
	} else {
		err = h.svc.IndexDocuments(r.Context(), id, docs)
	}
	if err != nil {
		writeEmbedErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "indexed", "count": len(docs)})
}

// writeEmbedErr reports a failed write, with a hint when embedding isn't set up.
func writeEmbedErr(w http.ResponseWriter, err error) {
	if strings.Contains(err.Error(), rag.ErrEmbedderNotConfigured.Error()) {
		writeErrHint(w, http.StatusServiceUnavailable, err.Error(), "finish the server setup: add the Voyage API key in the dashboard")
		return
	}
	writeErr(w, http.StatusInternalServerError, err.Error())
}

// UpdatePayload handles POST /api/projects/{id}/documents/payload: refresh
// content and metadata of existing points without re-embedding.
func (h *Handlers) UpdatePayload(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body docsBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	up, ok := h.svc.Provider().(rag.PayloadUpdater)
	if !ok {
		writeErrHint(w, http.StatusNotImplemented, "this vector store cannot update points in place", "the client re-indexes these chunks instead")
		return
	}
	if err := up.UpdatePayload(r.Context(), id, body.docs()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "updated", "count": len(body.Documents)})
}

// DeletePointsBulk handles POST /api/projects/{id}/points/delete {ids: [...]}.
func (h *Handlers) DeletePointsBulk(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := h.svc.DeletePoints(r.Context(), id, body.IDs); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "project_id": id, "count": len(body.IDs)})
}

// ReportSource handles POST /api/projects/{id}/sources: a client-mode
// machine finished syncing a directory.
func (h *Handlers) ReportSource(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Host       string         `json:"host"`
		Dir        string         `json:"dir"`
		Result     map[string]int `json:"result"`
		DurationMs int64          `json:"duration_ms"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Dir == "" {
		writeErr(w, http.StatusBadRequest, "body needs dir (and host, result, duration_ms)")
		return
	}
	if body.Host == "" {
		body.Host = requestClient(r)
	}
	h.svc.RecordSource(id, body.Host, body.Dir, body.Result, time.Duration(body.DurationMs)*time.Millisecond)
	writeJSON(w, http.StatusOK, map[string]string{"status": "recorded"})
}

// ReindexProject handles POST /api/projects/{id}/reindex — no path needed.
//
// mode "auto" (default) syncs every remembered directory of the project that
// exists on this server; when there is none (the code lives on client
// machines), it re-embeds the stored chunks with the current model instead.
// "files" only syncs directories (an explicit "directory" may be given),
// "reembed" only re-embeds. Runs as a job: 202 + the job to follow.
func (h *Handlers) ReindexProject(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "id")
	if project == "" {
		writeErr(w, http.StatusBadRequest, "project id is required")
		return
	}
	var req struct {
		Mode      string `json:"mode"`
		Directory string `json:"directory"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "auto"
	}
	if mode != "auto" && mode != "files" && mode != "reembed" {
		writeErr(w, http.StatusBadRequest, "mode must be auto, files or reembed")
		return
	}
	if job := h.jobs.Running("", project); job != nil {
		writeJSON(w, http.StatusAccepted, map[string]any{"job": job, "mode": job.Kind,
			"note": "A job for this project is already running — following it."})
		return
	}

	var dirs []string
	if req.Directory != "" {
		dirs = []string{req.Directory}
	} else if mode != "reembed" {
		dirs = h.svc.ServerDirs(project)
	}

	if mode == "files" && len(dirs) == 0 {
		writeErrHint(w, http.StatusBadRequest, "no source directory of this project exists on this server",
			sourcesHint(h.svc.ProjectSources(project)))
		return
	}

	if len(dirs) > 0 && mode != "reembed" {
		note := fmt.Sprintf("Syncing %s from disk: new and changed code is embedded, removed code is deleted.", strings.Join(dirs, ", "))
		job := h.jobs.Start("reindex", project, note, func(ctx context.Context, update JobUpdate) (map[string]any, error) {
			total := map[string]int{}
			for i, dir := range dirs {
				update(i, len(dirs), "Syncing "+dir)
				res, err := h.svc.IndexProject(ctx, project, dir)
				if err != nil {
					return map[string]any{"synced": total}, err
				}
				total["indexed"] += res.Indexed
				total["updated"] += res.Updated
				total["deleted"] += res.Deleted
				total["files_scanned"] += res.FilesScanned
				total["skipped"] += res.Skipped
				total["reembedded"] += res.Reembedded
			}
			update(len(dirs), len(dirs), fmt.Sprintf("Synced %d director%s: %d new, %d updated, %d removed, %d unchanged",
				len(dirs), plural(len(dirs), "y", "ies"), total["indexed"], total["updated"], total["deleted"], total["skipped"]))
			return map[string]any{"synced": total, "directories": dirs}, nil
		})
		writeJSON(w, http.StatusAccepted, map[string]any{"job": job, "mode": "files", "note": note})
		return
	}

	model, _ := h.svc.EmbedIdentity()
	count := 0
	if c, ok := h.svc.Provider().(core.ProjectCounter); ok {
		count, _ = c.CountPoints(r.Context(), project)
	}
	note := fmt.Sprintf("Re-embedding %d stored chunks with %s.", count, model)
	if srcs := h.svc.ProjectSources(project); len(srcs) > 0 && mode == "auto" {
		note = fmt.Sprintf("The code of %s lives on %s, not on this server: re-embedding its %d stored chunks with %s. "+
			"File changes sync automatically from that machine before each search.", project, describeSources(srcs), count, model)
	}
	job := h.jobs.Start("reembed", project, note, func(ctx context.Context, update JobUpdate) (map[string]any, error) {
		n, err := h.svc.ReembedProject(ctx, project, func(done, total int) {
			update(done, total, fmt.Sprintf("Re-embedding %d/%d chunks with %s", done, total, model))
		})
		if err == nil {
			// The job's last message is what the dashboard keeps showing.
			update(n, n, fmt.Sprintf("Re-embedded %d %s with %s", n, plural(n, "chunk", "chunks"), model))
		}
		return map[string]any{"reembedded": n, "model": model}, err
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"job": job, "mode": "reembed", "note": note})
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func describeSources(srcs []core.Source) string {
	parts := make([]string, 0, len(srcs))
	for i, s := range srcs {
		if i == 3 {
			parts = append(parts, fmt.Sprintf("+%d more", len(srcs)-3))
			break
		}
		parts = append(parts, s.Host+":"+s.Dir)
	}
	return strings.Join(parts, ", ")
}

func sourcesHint(srcs []core.Source) string {
	if len(srcs) == 0 {
		return "no machine has synced this project yet — run enowx-rag in client mode where the code lives, or use mode \"reembed\""
	}
	return "its code lives on " + describeSources(srcs) + " — it syncs from there automatically; use mode \"reembed\" to re-embed the stored chunks here"
}

// Activity handles GET /api/activity?limit=N: the persisted activity log.
func (h *Handlers) Activity(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	evs, err := h.svc.RecentEvents(r.Context(), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, evs)
}

// ServerRestart handles POST /api/server/restart.
func (h *Handlers) ServerRestart(w http.ResponseWriter, r *http.Request) {
	if h.opts.Restart == nil {
		writeErrHint(w, http.StatusNotImplemented, "this server cannot restart itself",
			"restart it yourself (docker compose restart enowx-rag, or systemctl restart enowx-rag)")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"restarting": true})
	h.scheduleRestart()
}

// scheduleRestart restarts the process shortly, after the response is sent.
func (h *Handlers) scheduleRestart() bool {
	if h.opts.Restart == nil {
		return false
	}
	h.svc.Events().Publish(core.Event{Type: "server_restarting", Timestamp: time.Now(), Data: map[string]any{"boot_id": h.opts.BootID}})
	go func() {
		time.Sleep(600 * time.Millisecond)
		if err := h.opts.Restart(); err != nil {
			fmt.Fprintf(os.Stderr, "restart failed: %v\n", err)
		}
	}()
	return true
}
