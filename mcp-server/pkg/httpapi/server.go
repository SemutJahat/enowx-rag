// Package httpapi provides the HTTP API layer for enowx-rag. It exposes
// REST endpoints and an SSE event stream over a chi router, serving both
// the API and an embedded React SPA from a single binary.
package httpapi

import (
	"context"
	"io/fs"
	"net/http"
	"time"

	"github.com/enowdev/enowx-rag/mcp-server/pkg/core"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Options carry what the process knows about itself to the HTTP layer.
type Options struct {
	Version   string
	BootID    string // changes on every process start (restart detection)
	StartedAt time.Time
	// Restart re-executes the server so a saved config takes effect; nil when
	// the platform can't (the dashboard then asks for a manual restart).
	Restart func() error
	// Runtime describes what this process actually runs with (config.yaml may
	// have been edited since it started).
	Runtime Runtime
	// StartupJobs run one after another in the background once the router
	// exists (e.g. the SQLite → pgvector import, then re-embedding stale
	// points), visible in the dashboard like any job.
	StartupJobs []StartupJob
}

// StartupJob is a job the server runs by itself after starting. Skip, when
// set, is asked right before it would run (after the jobs before it).
type StartupJob struct {
	Kind    string
	Project string
	Message string
	Skip    func() bool
	Run     func(ctx context.Context, update JobUpdate) (map[string]any, error)
}

// Runtime is the running configuration, for /api/server.
type Runtime struct {
	VectorStore    string
	Embedder       string
	EmbedModel     string
	EmbedDim       int
	EmbedKeySet    bool
	EmbedReady     bool   // false when the embedder is a stand-in (key missing)
	EmbedProblem   string // why it isn't ready
	RerankerModel  string // "" = off
	RerankerReason string // why reranking is off
	AutoSync       string // interval, "" = off
}

// NewRouter creates a chi router with all API routes and SPA fallback.
// svc is the core service layer shared with MCP stdio mode.
// ui is the embedded filesystem containing the SPA dist (index.html + assets).
// mcpHandler, when non-nil, is mounted at /mcp so agents can use enowx-rag as a
// remote MCP server; it is gated by the same RAG_ADMIN_TOKEN as /api.
func NewRouter(svc *core.Service, ui fs.FS, mcpHandler http.Handler) http.Handler {
	return NewRouterWithOptions(svc, ui, mcpHandler, Options{})
}

// NewRouterWithOptions is NewRouter with process information (version,
// restart support, running configuration).
func NewRouterWithOptions(svc *core.Service, ui fs.FS, mcpHandler http.Handler, opts Options) http.Handler {
	if opts.StartedAt.IsZero() {
		opts.StartedAt = time.Now()
	}
	if opts.BootID == "" {
		opts.BootID = newJobID()[2:]
	}
	if opts.Version == "" {
		opts.Version = "dev"
	}
	h := &Handlers{svc: svc, opts: opts, jobs: NewJobs(svc.Events()), uploads: newUploadStore()}
	if len(opts.StartupJobs) > 0 {
		go func() {
			for _, sj := range opts.StartupJobs {
				if sj.Skip != nil && sj.Skip() {
					continue
				}
				h.jobs.RunSync(sj.Kind, sj.Project, sj.Message, sj.Run)
			}
		}()
	}

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	// NOTE: middleware.RealIP is intentionally NOT used. It rewrites
	// r.RemoteAddr from client-controlled X-Forwarded-For / X-Real-IP headers,
	// which would let a remote caller spoof a loopback address and bypass
	// LocalOrAdminMiddleware's localhost check on the setup endpoints.

	r.Route("/api", func(r chi.Router) {
		// Always open: liveness (Docker healthcheck, restart detection) and
		// whether onboarding is needed. Neither reveals configuration secrets.
		r.Get("/health", h.Health)
		r.Get("/setup/status", h.SetupStatus)

		r.Group(func(r chi.Router) {
			// When an admin token is configured (RAG_ADMIN_TOKEN or config.yaml),
			// every other endpoint requires Authorization: Bearer <token>.
			r.Use(AdminTokenMiddleware)

			r.Get("/server", h.ServerInfo)
			r.Get("/models", h.Models)

			r.Get("/projects", h.ListProjects)
			r.Get("/projects/{id}", h.GetProject)
			r.Post("/projects/{id}", h.CreateProject)
			r.Delete("/projects/{id}", h.DeleteProject)
			r.Get("/projects/{id}/points", h.ListPoints)
			r.Post("/projects/{id}/points/delete", h.DeletePointsBulk)
			r.Delete("/projects/{id}/points/{pointId}", h.DeletePoint)
			r.Post("/projects/{id}/reindex", h.ReindexProject)
			// Client mode (vector_store: remote) writes through these.
			r.Post("/projects/{id}/documents", h.IndexDocuments)
			r.Post("/projects/{id}/documents/payload", h.UpdatePayload)
			r.Post("/projects/{id}/sources", h.ReportSource)

			r.Post("/search", h.Search)
			r.Get("/stats", h.Stats)
			r.Get("/metrics", h.Metrics)
			r.Get("/activity", h.Activity)
			r.Get("/events", h.SSE)
			r.Get("/jobs", h.ListJobs)
			r.Get("/jobs/{id}", h.GetJob)

			// Import a local SQLite store (rag.db) into this server's store.
			r.Post("/import/upload", h.ImportUpload)
			r.Post("/import/start", h.ImportStart)
			r.Delete("/import/upload/{id}", h.ImportDiscard)

			r.Get("/setup/config", h.SetupConfig) // masked config (no full secrets)
			r.Get("/setup/mcp-snippet", h.SetupMCPSnippet)
			r.Get("/setup/skill-guide", h.SetupSkillGuide)
			r.Get("/setup/probe", h.SetupProbe)
			r.Get("/docs", h.DocsList)
			r.Get("/docs/setup", h.SetupDocs) // alias for the agent-setup section
			r.Get("/docs/{section}", h.DocsSection)

			// Endpoints that accept credentials, reveal secrets, write
			// config.yaml or restart the process: loopback or a valid admin
			// token only (a remote caller can't use them without the token even
			// when no token is configured).
			r.Group(func(r chi.Router) {
				r.Use(LocalOrAdminMiddleware)
				r.Get("/setup/detect", h.SetupDetect)
				r.Post("/setup/test", h.SetupTest)
				r.Post("/setup/apply", h.SetupApply)
				r.Get("/setup/config/reveal", h.SetupConfigReveal)
				r.Post("/setup/config", h.SetupConfigUpdate)
				r.Post("/setup/gen-token", h.SetupGenToken)
				r.Post("/models/test", h.ModelsTest)
				r.Post("/server/restart", h.ServerRestart)
				// migrate writes into a destination store with user-supplied
				// credentials (API only; the dashboard uses Import).
				r.Post("/migrate", h.Migrate)
			})
		})

		// Unknown /api/ routes return 404 JSON (not SPA fallback)
		r.NotFound(h.NotFound)
	})

	// MCP over HTTP at /mcp — remote MCP transport. Gated by the same admin
	// token as /api. Must be registered before the SPA catch-all below, or the
	// catch-all would swallow /mcp requests.
	if mcpHandler != nil {
		r.Group(func(r chi.Router) {
			r.Use(AdminTokenMiddleware)
			r.Handle("/mcp", mcpHandler)
			r.Handle("/mcp/*", mcpHandler)
		})
	}

	// SPA fallback: serve embedded dist files, fall back to index.html
	// for client-side routes.
	if ui != nil {
		spa := &SPAHandler{root: ui}
		r.Handle("/*", spa)
	}

	return r
}

// SPAHandler serves static files from an embedded filesystem. For paths
// that don't match a real file, it falls back to index.html to enable
// client-side routing (SPA mode).
type SPAHandler struct {
	root fs.FS
}

func (s *SPAHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "" || path == "/" {
		path = "/index.html"
	}

	// Try to serve the file directly from the embedded FS.
	data, err := fs.ReadFile(s.root, path[1:]) // strip leading "/"
	if err == nil {
		w.Header().Set("Content-Type", contentTypeFor(path))
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(http.StatusOK)
		w.Write(data)
		return
	}

	// Fall back to index.html for client-side routes.
	indexData, err := fs.ReadFile(s.root, "index.html")
	if err != nil {
		http.Error(w, "index.html not found in embedded dist", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	w.Write(indexData)
}

// contentTypeFor returns the Content-Type header value for common asset types.
func contentTypeFor(path string) string {
	switch {
	case endsWith(path, ".html"):
		return "text/html; charset=utf-8"
	case endsWith(path, ".js"):
		return "application/javascript"
	case endsWith(path, ".mjs"):
		return "application/javascript"
	case endsWith(path, ".css"):
		return "text/css"
	case endsWith(path, ".json"):
		return "application/json"
	case endsWith(path, ".svg"):
		return "image/svg+xml"
	case endsWith(path, ".png"):
		return "image/png"
	case endsWith(path, ".jpg"), endsWith(path, ".jpeg"):
		return "image/jpeg"
	case endsWith(path, ".gif"):
		return "image/gif"
	case endsWith(path, ".ico"):
		return "image/x-icon"
	case endsWith(path, ".woff"):
		return "font/woff"
	case endsWith(path, ".woff2"):
		return "font/woff2"
	case endsWith(path, ".ttf"):
		return "font/ttf"
	case endsWith(path, ".map"):
		return "application/json"
	case endsWith(path, ".txt"):
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

func endsWith(s, suffix string) bool {
	if len(suffix) > len(s) {
		return false
	}
	return s[len(s)-len(suffix):] == suffix
}
