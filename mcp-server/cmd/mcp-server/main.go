package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/enowdev/enowx-rag/mcp-server/pkg/config"
	"github.com/enowdev/enowx-rag/mcp-server/pkg/core"
	"github.com/enowdev/enowx-rag/mcp-server/pkg/httpapi"
	"github.com/enowdev/enowx-rag/mcp-server/pkg/indexer"
	"github.com/enowdev/enowx-rag/mcp-server/pkg/rag"
	"github.com/enowdev/enowx-rag/mcp-server/pkg/ragbuild"
	"github.com/enowdev/enowx-rag/mcp-server/web"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// version is the build version. It defaults to "dev" for `go run`/source
// builds and is overridden at release time via the linker:
//
//	-ldflags "-X main.version=v0.1.0"
//
// GoReleaser sets it from the git tag. `go install ...@vX.Y.Z` also reports a
// meaningful version through runtime/debug build info (see resolvedVersion).
var version = "dev"

// resolvedVersion returns the ldflags-injected version when set, otherwise it
// falls back to the module version recorded in the binary's build info. That
// fallback makes `go install github.com/enowdev/enowx-rag/...@v0.1.0` report
// "v0.1.0" even though it doesn't pass our -ldflags.
func resolvedVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return version
}

// RuntimeConfig holds the resolved configuration used to build the service
// layer. It is populated from three sources with strict priority:
// environment variables > config file (~/.enowx-rag/config.yaml) > defaults.
type RuntimeConfig struct {
	VectorStore   string
	Embedder      string
	QdrantURL     string
	QdrantAPIKey  string
	ChromaURL     string
	PGVectorDSN   string
	SQLitePath    string
	TEIBaseURL    string
	VoyageAPIKey  string
	VoyageModel   string
	OpenAIAPIKey  string
	OpenAIModel   string
	OpenAIBaseURL string
	OpenAIDim     int
	RerankerModel string
	VectorDim     int
	// AutoSyncInterval is the raw auto-sync setting (config file
	// auto_sync_interval, or RAG_AUTO_SYNC_INTERVAL); see core.ParseAutoSyncInterval.
	AutoSyncInterval string
	// RerankerOn: a reranker model is set and not "off".
	RerankerOn bool
	// Client mode (VectorStore "remote").
	RemoteURL   string
	RemoteToken string
	// AutoImport: import the SQLite store into another store at startup.
	AutoImport bool
	// Configured: the settings come from config.yaml or the environment
	// (not just built-in defaults).
	Configured bool
}

// resolveConfig builds a RuntimeConfig from the three-tier priority:
// env var > config file > default. It uses pkg/config.Resolve() which
// handles the file loading and env override logic.
func resolveConfig() (*RuntimeConfig, error) {
	cfg, err := config.Resolve()
	if err != nil {
		return nil, fmt.Errorf("resolve config: %w", err)
	}

	rc := &RuntimeConfig{
		VectorStore:   cfg.VectorStore,
		Embedder:      cfg.Embedder,
		QdrantURL:     cfg.QdrantURL,
		QdrantAPIKey:  cfg.QdrantAPIKey,
		ChromaURL:     cfg.ChromaURL,
		PGVectorDSN:   cfg.PGVectorDSN,
		SQLitePath:    cfg.StorePath(),
		TEIBaseURL:    cfg.TEIURL,
		VoyageAPIKey:  cfg.Voyage.APIKey,
		VoyageModel:   cfg.Voyage.Model,
		OpenAIAPIKey:  cfg.OpenAI.APIKey,
		OpenAIModel:   cfg.OpenAI.Model,
		OpenAIBaseURL: cfg.OpenAI.BaseURL,
		OpenAIDim:     cfg.OpenAI.Dim,
		RerankerModel: cfg.RerankerModel,
		VectorDim:     cfg.Voyage.Dim,

		AutoSyncInterval: cfg.AutoSyncInterval,
		RerankerOn:       cfg.RerankerEnabled(),
		RemoteURL:        cfg.RemoteURL,
		RemoteToken:      cfg.RemoteToken,
		AutoImport:       cfg.AutoImportEnabled(),
	}
	if _, err := os.Stat(config.Path()); err == nil || len(config.EnvLocked()) > 0 {
		rc.Configured = true
	}
	// A missing Voyage key no longer switches to TEI behind the user's back
	// (with pgvector that could resize the vector column): the store opens
	// with a stand-in embedder and every embedding call says what to set.
	return rc, nil
}

func buildProvider(ctx context.Context, cfg *RuntimeConfig) (rag.Provider, error) {
	return ragbuild.BuildProvider(ctx, cfg.spec())
}

// spec is the ragbuild description of cfg's store and embedder.
func (cfg *RuntimeConfig) spec() ragbuild.Spec {
	return ragbuild.Spec{
		VectorStore:   cfg.VectorStore,
		Embedder:      cfg.Embedder,
		QdrantURL:     cfg.QdrantURL,
		QdrantAPIKey:  cfg.QdrantAPIKey,
		ChromaURL:     cfg.ChromaURL,
		PGVectorDSN:   cfg.PGVectorDSN,
		SQLitePath:    cfg.SQLitePath,
		VoyageAPIKey:  cfg.VoyageAPIKey,
		VoyageModel:   cfg.VoyageModel,
		VoyageDim:     cfg.VectorDim,
		OpenAIAPIKey:  cfg.OpenAIAPIKey,
		OpenAIModel:   cfg.OpenAIModel,
		OpenAIBaseURL: cfg.OpenAIBaseURL,
		OpenAIDim:     cfg.OpenAIDim,
		TEIURL:        cfg.TEIBaseURL,
		RemoteURL:     cfg.RemoteURL,
		RemoteToken:   cfg.RemoteToken,

		AllowUnconfigured: true,
	}
}

// embedModelName is the model the configured embedder uses, for stats.
func (cfg *RuntimeConfig) embedModelName() string {
	switch strings.ToLower(cfg.Embedder) {
	case "openai":
		return cfg.OpenAIModel
	case "tei":
		return "tei"
	}
	return cfg.VoyageModel
}

// buildService wraps a provider, optional reranker, and indexer into a
// core.Service that both MCP and HTTP handlers call.
func buildService(provider rag.Provider, reranker rag.Reranker, idx *indexer.Indexer) *core.Service {
	return core.NewService(provider, reranker, idx)
}

// Tool inputs.
type CreateProjectInput struct {
	ProjectID string `json:"project_id" jsonschema:"Unique identifier for the project/collection"`
}

type DeleteProjectInput struct {
	ProjectID string `json:"project_id" jsonschema:"Unique identifier for the project/collection"`
}

type IndexProjectInput struct {
	ProjectID string `json:"project_id" jsonschema:"Project identifier"`
	Documents []struct {
		ID      string            `json:"id" jsonschema:"Optional document ID; generated if empty"`
		Content string            `json:"content" jsonschema:"Text to index"`
		Meta    map[string]string `json:"meta" jsonschema:"Optional metadata key-value pairs"`
	} `json:"documents" jsonschema:"Documents to index into the project memory"`
}

type SemanticSearchInput struct {
	ProjectID string `json:"project_id" jsonschema:"Project identifier"`
	Query     string `json:"query" jsonschema:"Query text"`
	Limit     int    `json:"limit" jsonschema:"Number of results to return (default 5)"`
	Recall    int    `json:"recall" jsonschema:"Candidates retrieved before reranking (default 40)"`
	Hybrid    *bool  `json:"hybrid" jsonschema:"Combine dense + lexical search when the backend supports it (default true)"`
	Rerank    *bool  `json:"rerank" jsonschema:"Rerank candidates with the reranker when configured (default true)"`
	Compress  bool   `json:"compress" jsonschema:"Drop near-duplicate results (default false)"`
}

type RetrieveContextInput struct {
	ProjectID string `json:"project_id" jsonschema:"Project identifier"`
	Query     string `json:"query" jsonschema:"Question or topic to retrieve context for"`
	Limit     int    `json:"limit" jsonschema:"Number of chunks to retrieve (default 5)"`
	Recall    int    `json:"recall" jsonschema:"Candidates retrieved before reranking (default 40)"`
	Hybrid    *bool  `json:"hybrid" jsonschema:"Combine dense + lexical search when the backend supports it (default true)"`
	Rerank    *bool  `json:"rerank" jsonschema:"Rerank candidates with the reranker when configured (default true)"`
	Compress  bool   `json:"compress" jsonschema:"Drop near-duplicate results (default false)"`
}

// searchOptsFromMCP builds SearchOpts from MCP tool inputs. Hybrid and Rerank
// default to true (nil pointer) so the built-in hybrid/rerank features are used
// by default from MCP clients; they only take effect when the backend/reranker
// supports them (otherwise Search falls back gracefully).
func searchOptsFromMCP(limit, recall int, hybrid, rerank *bool, compress bool) core.SearchOpts {
	b := func(p *bool) bool { return p == nil || *p }
	return core.SearchOpts{
		K:        limit,
		Recall:   recall,
		Hybrid:   b(hybrid),
		Rerank:   b(rerank),
		Compress: compress,
	}
}

type ScanProjectInput struct {
	ProjectID string `json:"project_id" jsonschema:"Project identifier"`
	Directory string `json:"directory" jsonschema:"Absolute path to the project directory to scan and index"`
}

// EmptyInput is used by tools that take no arguments (e.g. rag_list_projects).
type EmptyInput struct{}

type ProjectIDInput struct {
	ProjectID string `json:"project_id" jsonschema:"Project identifier"`
}

type ListPointsInput struct {
	ProjectID  string `json:"project_id" jsonschema:"Project identifier"`
	SourceFile string `json:"source_file" jsonschema:"Optional: only list chunks from this source file"`
}

type DeletePointsInput struct {
	ProjectID string   `json:"project_id" jsonschema:"Project identifier"`
	PointIDs  []string `json:"point_ids" jsonschema:"IDs of the chunks/points to delete"`
}

func main() {
	rag.SetClientVersion(resolvedVersion())
	// Subcommand dispatch (must run before flag.Parse, which only handles the
	// default mode's flags). `enowx-rag setup [--run]` generates/runs the
	// docker-compose backend from the command line — never over HTTP.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "setup":
			runSetup(os.Args[2:])
			return
		case "push":
			os.Exit(runPush(os.Args[2:]))
		case "connect":
			os.Exit(runConnect(os.Args[2:]))
		case "version", "--version", "-v":
			// `enowx-rag version` / `--version` / `-v` prints the build version.
			fmt.Printf("enowx-rag %s\n", resolvedVersion())
			return
		}
	}

	serve := flag.Bool("serve", false, "run HTTP+UI server instead of stdio MCP")
	addr := flag.String("addr", ":7777", "HTTP listen address (only used with --serve)")
	flag.Usage = usage
	flag.Parse()

	cfg, err := resolveConfig()
	if err != nil {
		log.Fatalf("failed to resolve config: %v", err)
	}

	provider, err := openStore(cfg, *serve)
	if err != nil {
		log.Fatalf("failed to open the %s store: %v", cfg.VectorStore, err)
	}
	defer provider.Close()
	clientMode := strings.EqualFold(cfg.VectorStore, "remote")

	// Build the service layer that wraps provider + indexer.
	idx := indexer.NewIndexer(provider, 1500)

	// The reranker shares the Voyage API key used for embeddings. In client
	// mode the server reranks. Without a reranker, Search with Rerank=true
	// falls back to semantic order.
	var reranker rag.Reranker
	rerankerModel, rerankerReason := "", ""
	switch {
	case clientMode:
		rerankerReason = "client mode: the server reranks"
	case !cfg.RerankerOn:
		rerankerReason = "turned off (reranker_model: off)"
	case cfg.VoyageAPIKey == "":
		rerankerReason = "needs the Voyage API key"
	default:
		rerankerModel = cfg.RerankerModel
		reranker = rag.NewVoyageReranker(cfg.VoyageAPIKey, cfg.RerankerModel)
	}

	svc := buildService(provider, reranker, idx)
	svc.SetEmbedModel(cfg.embedModelName())
	svc.SetBackend(cfg.VectorStore)

	// Auto-sync: directories indexed with rag_index_project are re-synced
	// (incrementally) before a search once their index is older than the
	// auto-sync interval (config auto_sync_interval or RAG_AUTO_SYNC_INTERVAL;
	// default 60s; "off" disables), so agents don't have to re-run
	// rag_index_project after every change.
	autoSync := ""
	if every, err := core.ParseAutoSyncInterval(cfg.AutoSyncInterval); err != nil {
		fmt.Fprintf(os.Stderr, "auto-sync: disabled: %v\n", err)
	} else if every > 0 {
		if reg, err := core.NewDirRegistry(config.ProjectDirsPath()); err != nil {
			fmt.Fprintf(os.Stderr, "auto-sync: disabled: %v\n", err)
		} else {
			svc.EnableAutoSync(reg, every)
			autoSync = every.String()
		}
	}
	// Where each project's code lives (this machine or client machines).
	svc.SetSourceRegistry(core.NewSourceRegistry(filepath.Join(config.Dir(), "project_sources.json")))

	// Durable metrics + activity log: a local SQLite file (pure-Go, no cgo)
	// that works with any vector-store backend. If it can't be opened, fall
	// back to in-memory metrics rather than failing startup.
	metricsPath := config.MetricsDBPath()
	if err := os.MkdirAll(filepath.Dir(metricsPath), 0o700); err == nil {
		if store, err := core.NewSQLiteMetricsStore(metricsPath); err != nil {
			fmt.Fprintf(os.Stderr, "metrics: durable store unavailable, using in-memory: %v\n", err)
		} else {
			svc.SetMetricsStore(store)
			if *serve {
				svc.SetEventStore(store)
			}
			defer store.Close()
		}
	}

	rt := httpapi.Runtime{
		VectorStore:    cfg.VectorStore,
		Embedder:       cfg.Embedder,
		RerankerModel:  rerankerModel,
		RerankerReason: rerankerReason,
		AutoSync:       autoSync,
		EmbedReady:     true,
	}
	if id, ok := provider.(rag.EmbedIdentifier); ok {
		rt.EmbedModel, rt.EmbedDim = id.EmbedIdentity()
	}
	switch strings.ToLower(cfg.Embedder) {
	case "voyage":
		rt.EmbedKeySet = cfg.VoyageAPIKey != ""
	case "openai":
		rt.EmbedKeySet = cfg.OpenAIAPIKey != ""
	default:
		rt.EmbedKeySet = true
	}
	if clientMode {
		rt.Embedder, rt.EmbedModel = "remote", ""
	} else if _, err := ragbuild.BuildEmbedder(cfg.spec()); err != nil {
		rt.EmbedReady, rt.EmbedProblem = false, err.Error()
	}

	if *serve {
		runHTTP(svc, *addr, cfg, rt, provider)
		return
	}
	runStdio(svc, cfg, rt)
}

func usage() {
	fmt.Fprintf(os.Stderr, `enowx-rag %s — per-project RAG memory (MCP server)

Usage:
  enowx-rag                     MCP server over stdio (what MCP clients launch)
  enowx-rag --serve [--addr :7777]
                                HTTP API + dashboard + MCP at /mcp (e.g. on a VPS)
  enowx-rag connect --remote URL --token TOKEN [--client ID]
                                use an enowx-rag server from this machine (client mode)
  enowx-rag push --remote URL --token TOKEN [--project ID]...
                                import this machine's SQLite memory into the server
  enowx-rag setup [--run]       generate/run a docker-compose backend
  enowx-rag version

Flags:
`, resolvedVersion())
	flag.PrintDefaults()
}

// openStore builds the vector store. A server (--serve) waits for a store
// that is still starting (PostgreSQL next to it in docker compose), and when
// nothing was configured yet and the default store isn't there, it opens the
// built-in SQLite store so the dashboard can run the onboarding wizard.
func openStore(cfg *RuntimeConfig, serve bool) (rag.Provider, error) {
	attempts := 1
	if serve && cfg.Configured && !strings.EqualFold(cfg.VectorStore, "sqlite") {
		attempts = 12 // ~1 minute
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			fmt.Fprintf(os.Stderr, "%s store not ready (%v) — retrying in 5s (%d/%d)\n", cfg.VectorStore, lastErr, i, attempts-1)
			time.Sleep(5 * time.Second)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		p, err := buildProvider(ctx, cfg)
		cancel()
		if err == nil {
			return p, nil
		}
		lastErr = err
	}
	if serve && !cfg.Configured {
		fmt.Fprintf(os.Stderr, "no configuration yet and the default %s store is unavailable (%v): starting with the built-in SQLite store — finish the setup in the dashboard\n", cfg.VectorStore, lastErr)
		fallback := *cfg
		fallback.VectorStore = "sqlite"
		cfg.VectorStore = "sqlite"
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return buildProvider(ctx, &fallback)
	}
	return nil, lastErr
}

// newMCPServer builds an *mcp.Server with all enowx-rag tools registered. It is
// shared by both transports: stdio (single session) and the streamable HTTP
// handler (many concurrent sessions).
func newMCPServer(svc *core.Service) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "enowx-rag", Version: resolvedVersion()}, &mcp.ServerOptions{
		Instructions: "Per-project RAG memory: create collections, index project documents, and retrieve/semantic-search context. Stores in SQLite, pgvector, Qdrant or Chroma (or a remote enowx-rag server), embeds with Voyage AI.",
	})
	registerMCPTools(server, svc)
	return server
}

// runStdio starts the MCP server in stdio mode. This is the default mode
// when --serve is not passed. Logs go to stderr (stdout is MCP protocol).
func runStdio(svc *core.Service, cfg *RuntimeConfig, rt httpapi.Runtime) {
	server := newMCPServer(svc)

	// Log tool configuration to stderr so it doesn't interfere with stdio transport.
	if strings.EqualFold(cfg.VectorStore, "remote") {
		fmt.Fprintf(os.Stderr, "enowx-rag mcp-server ready: client mode → %s (local folders are scanned here, embedded and stored there)\n", cfg.RemoteURL)
	} else {
		fmt.Fprintf(os.Stderr, "enowx-rag mcp-server ready: vector_store=%s embedder=%s\n", cfg.VectorStore, cfg.Embedder)
	}
	if !rt.EmbedReady {
		fmt.Fprintf(os.Stderr, "warning: %s\n", rt.EmbedProblem)
	}
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}

// runHTTP starts the HTTP API + SPA server on the given address, and also mounts
// the MCP server over HTTP at /mcp so agents can use enowx-rag as a remote
// daemon (behind RAG_ADMIN_TOKEN when set).
func runHTTP(svc *core.Service, addr string, cfg *RuntimeConfig, rt httpapi.Runtime, provider rag.Provider) {
	// Extract the dist subdirectory from the embedded filesystem.
	distFS, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		log.Fatalf("failed to get embedded dist: %v", err)
	}

	// MCP over HTTP: one server instance shared across all sessions. Stateless
	// mode suits a request/response RAG tool server and is easy to scale.
	mcpSrv := newMCPServer(svc)
	mcpHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return mcpSrv },
		&mcp.StreamableHTTPOptions{Stateless: true},
	)

	srv := &http.Server{Addr: addr, ReadHeaderTimeout: 15 * time.Second}
	var restart func() error
	if canRestart {
		restart = func() error {
			fmt.Fprintln(os.Stderr, "restarting to apply the saved configuration…")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = srv.Shutdown(ctx)
			cancel()
			_ = provider.Close()
			err := execSelf()
			// Only reached when exec failed: the listener and store are
			// closed, so exit and let Docker/systemd start it again.
			fmt.Fprintf(os.Stderr, "restart failed: %v — exiting so the supervisor restarts the server\n", err)
			os.Exit(1)
			return err
		}
	}
	startedAt := time.Now()
	opts := httpapi.Options{
		Version:     resolvedVersion(),
		BootID:      fmt.Sprintf("%x", startedAt.UnixNano()),
		StartedAt:   startedAt,
		Restart:     restart,
		Runtime:     rt,
		StartupJobs: startupJobs(svc, cfg, rt),
	}
	srv.Handler = httpapi.NewRouterWithOptions(svc, distFS, mcpHandler, opts)

	printBanner(addr, cfg, rt)
	svc.Events().Publish(core.Event{Type: "server_started", Timestamp: time.Now(), Data: map[string]any{
		"version": opts.Version, "vector_store": rt.VectorStore, "embed_model": rt.EmbedModel, "embed_dim": rt.EmbedDim,
		"reranker": rt.RerankerModel, "embed_ready": rt.EmbedReady}})
	// SIGTERM (docker stop, systemctl stop) / Ctrl-C: finish requests, close
	// the store (a SQLite store checkpoints its WAL), exit.
	stopped := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		fmt.Fprintln(os.Stderr, "shutting down…")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
		close(stopped)
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("HTTP server error: %v", err)
	}
	// Closed by a signal (return: main's defers close the store) or for a
	// restart (execSelf replaces the process; block meanwhile).
	<-stopped
}

// printBanner tells whoever starts the server what it runs with and what is
// still missing.
func printBanner(addr string, cfg *RuntimeConfig, rt httpapi.Runtime) {
	authNote := "OPEN — set RAG_ADMIN_TOKEN before exposing it"
	if config.EffectiveAdminToken() != "" {
		authNote = "admin token required"
	}
	w := os.Stderr
	fmt.Fprintf(w, "enowx-rag %s serving on %s (dashboard, /api, MCP at /mcp) — %s\n", resolvedVersion(), addr, authNote)
	fmt.Fprintf(w, "  store:    %s\n", describeStore(cfg))
	if rt.EmbedReady {
		fmt.Fprintf(w, "  embedder: %s %s (%d dims)\n", cfg.Embedder, rt.EmbedModel, rt.EmbedDim)
	} else {
		fmt.Fprintf(w, "  embedder: NOT READY — %s\n", rt.EmbedProblem)
	}
	if rt.RerankerModel != "" {
		fmt.Fprintf(w, "  reranker: %s\n", rt.RerankerModel)
	} else {
		fmt.Fprintf(w, "  reranker: off (%s)\n", rt.RerankerReason)
	}
}

func describeStore(cfg *RuntimeConfig) string {
	switch strings.ToLower(cfg.VectorStore) {
	case "pgvector":
		return "pgvector " + rag.DescribeDSN(cfg.PGVectorDSN)
	case "sqlite":
		return "sqlite " + cfg.SQLitePath
	case "qdrant":
		return "qdrant " + cfg.QdrantURL
	case "chroma":
		return "chroma " + cfg.ChromaURL
	case "remote":
		return "remote " + cfg.RemoteURL
	}
	return cfg.VectorStore
}

// startupJobs are what a server does by itself after starting:
//  1. the one-time import of the local SQLite store into the configured store
//     (switching sqlite → pgvector converts the memory automatically);
//  2. re-embedding points another model or size wrote (after a model change),
//     including projects whose code lives on client machines.
func startupJobs(svc *core.Service, cfg *RuntimeConfig, rt httpapi.Runtime) []httpapi.StartupJob {
	var jobs []httpapi.StartupJob
	if cfg.AutoImport && !strings.EqualFold(cfg.VectorStore, "sqlite") && !strings.EqualFold(cfg.VectorStore, "remote") {
		path := cfg.SQLitePath
		if st, err := os.Stat(path); err == nil && st.Size() > 0 {
			jobs = append(jobs, httpapi.StartupJob{
				Kind:    "import",
				Message: "Importing the SQLite store " + path + " into " + cfg.VectorStore,
				Skip: func() bool {
					skip, why := skipAutoImport(svc, path, rt)
					if why != "" {
						fmt.Fprintf(os.Stderr, "auto-import: %s\n", why)
					}
					return skip
				},
				Run: func(ctx context.Context, update httpapi.JobUpdate) (map[string]any, error) {
					return autoImport(ctx, svc, path, update)
				},
			})
		}
	}
	if rt.EmbedReady && !strings.EqualFold(cfg.VectorStore, "remote") {
		jobs = append(jobs, httpapi.StartupJob{
			Kind:    "refresh",
			Message: "Re-embedding chunks written by another embedding model or size",
			Skip: func() bool {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				n, _ := svc.StalePoints(ctx)
				return n == 0
			},
			Run: func(ctx context.Context, update httpapi.JobUpdate) (map[string]any, error) {
				model, dim := svc.EmbedIdentity()
				total, _ := svc.StalePoints(ctx)
				done := 0
				last, lastN := "", 0
				n, err := svc.RefreshStale(ctx, func(project string, d, _ int) {
					if project != last {
						done += lastN
						last, lastN = project, 0
					}
					lastN = d
					update(done+d, total, fmt.Sprintf("Re-embedding %s with %s (%d dims)", project, model, dim))
				})
				return map[string]any{"reembedded": n, "model": model, "dim": dim}, err
			},
		})
	}
	return jobs
}

// skipAutoImport decides whether the SQLite store at path should be imported
// now: it must hold points, and every point must either keep its vector or
// be embeddable (a missing key would fail halfway — wait for it).
func skipAutoImport(svc *core.Service, path string, rt httpapi.Runtime) (bool, string) {
	snap, err := rag.OpenSQLiteSnapshot(path)
	if err != nil {
		return true, fmt.Sprintf("skipped, %s is not readable as a store: %v", path, err)
	}
	defer snap.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	plan, err := svc.PlanImport(ctx, snap)
	if err != nil {
		return true, "skipped: " + err.Error()
	}
	points, reuse := 0, 0
	for _, p := range plan {
		points += p.Points
		reuse += p.ReusePoints
	}
	if points == 0 {
		return true, ""
	}
	if reuse < points && !rt.EmbedReady {
		return true, fmt.Sprintf("waiting: %d of %d chunks in %s must be embedded again and the embedder isn't ready (%s)", points-reuse, points, path, rt.EmbedProblem)
	}
	return false, ""
}

// autoImport imports every project of the SQLite store at path (merge) and
// renames the file so it is imported only once.
func autoImport(ctx context.Context, svc *core.Service, path string, update httpapi.JobUpdate) (map[string]any, error) {
	snap, err := rag.OpenSQLiteSnapshot(path)
	if err != nil {
		return nil, err
	}
	projects, err := snap.Projects(ctx)
	if err != nil {
		snap.Close()
		return nil, err
	}
	total := 0
	for _, p := range projects {
		total += p.Points
	}
	base, last, lastN := 0, "", 0
	results, err := svc.ImportSnapshot(ctx, snap, core.ImportOptions{Mode: "merge"}, func(project string, done, _ int) {
		if project != last {
			base += lastN
			last, lastN = project, 0
		}
		lastN = done
		update(base+done, total, fmt.Sprintf("Importing %s from SQLite: %d chunks", project, done))
	})
	snap.Close()
	if err != nil {
		return map[string]any{"projects": results}, err
	}
	update(total, total, "From SQLite: "+core.ImportSummary(results))
	done := fmt.Sprintf("%s.imported-%s", path, time.Now().Format("20060102-150405"))
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); err == nil {
			_ = os.Rename(path+suffix, done+suffix)
		}
	}
	fmt.Fprintf(os.Stderr, "auto-import: imported %d chunks from %s (kept as %s)\n", total, path, done)
	return map[string]any{"projects": results, "kept_as": done}, nil
}

// registerMCPTools registers all MCP tools on the server, each as a thin
// wrapper over the core.Service methods. No direct provider/indexer calls
// are made inside the closures.
func registerMCPTools(server *mcp.Server, svc *core.Service) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "rag_create_project",
		Description: "Create a new RAG collection for a project. Safe to call if collection already exists.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in CreateProjectInput) (*mcp.CallToolResult, any, error) {
		if err := svc.CreateProject(ctx, in.ProjectID); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"status": "ok", "project_id": in.ProjectID}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "rag_delete_project",
		Description: "Delete the RAG collection for a project and all its indexed memory.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in DeleteProjectInput) (*mcp.CallToolResult, any, error) {
		if err := svc.DeleteProject(ctx, in.ProjectID); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"status": "deleted", "project_id": in.ProjectID}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "rag_index",
		Description: "Index documents into a project collection. Each document is embedded and stored as a retrievable chunk.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in IndexProjectInput) (*mcp.CallToolResult, any, error) {
		docs := make([]rag.Document, len(in.Documents))
		for i, d := range in.Documents {
			docs[i] = rag.Document{ID: d.ID, Content: d.Content, Meta: d.Meta}
		}
		if err := svc.IndexDocuments(ctx, in.ProjectID, docs); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"status": "indexed", "count": len(docs)}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "rag_semantic_search",
		Description: "Semantic search over a project collection. Returns the most relevant chunks with similarity scores.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in SemanticSearchInput) (*mcp.CallToolResult, any, error) {
		opts := searchOptsFromMCP(in.Limit, in.Recall, in.Hybrid, in.Rerank, in.Compress)
		res, err := svc.Search(ctx, in.ProjectID, in.Query, opts)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"results": res}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "rag_retrieve_context",
		Description: "Retrieve a compact context string for a project. Fetches top chunks and concatenates them for LLM context.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in RetrieveContextInput) (*mcp.CallToolResult, any, error) {
		opts := searchOptsFromMCP(in.Limit, in.Recall, in.Hybrid, in.Rerank, in.Compress)
		context, chunks, err := svc.RetrieveContext(ctx, in.ProjectID, in.Query, opts)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"context": context, "chunks": chunks}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "rag_index_project",
		Description: "Scan a project directory and index all code/text files into RAG, syncing it: new and changed code is indexed, code removed from files and removed files are deleted. Honors .gitignore; skips node_modules, .git, vendor, dist, build and secret files. Run it once per project directory: the directory is remembered and re-synced automatically before searches (RAG_AUTO_SYNC_INTERVAL, default 60s). Run it again to force an immediate sync.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in ScanProjectInput) (*mcp.CallToolResult, any, error) {
		// Use a long-lived context so large projects don't time out under the
		// MCP framework's short request deadline. 30 minutes is enough for
		// even very large monorepos.
		indexCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		result, err := svc.IndexProject(indexCtx, in.ProjectID, in.Directory)
		if err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{
			"status":         "synced",
			"project_id":     in.ProjectID,
			"chunks_indexed": result.Indexed,
			"chunks_updated": result.Updated,
			"points_deleted": result.Deleted,
			"files_scanned":  result.FilesScanned,
			// Points embedded again because another embedding model or
			// vector size wrote them (the model in the config changed).
			"points_reembedded": result.Reembedded,
			"stale_error":       result.StaleError,
		}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "rag_list_projects",
		Description: "List all RAG projects with their chunk counts. Use this to discover what memory is available before searching.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in EmptyInput) (*mcp.CallToolResult, any, error) {
		stats, err := svc.ListProjects(ctx)
		if err != nil {
			return nil, nil, err
		}
		if stats == nil {
			stats = []core.ProjectStat{}
		}
		return nil, map[string]any{"projects": stats}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "rag_project_exists",
		Description: "Check whether a project has any indexed memory. Useful before searching or indexing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in ProjectIDInput) (*mcp.CallToolResult, any, error) {
		return nil, map[string]any{"project_id": in.ProjectID, "exists": svc.ProjectExists(ctx, in.ProjectID)}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "rag_list_points",
		Description: "List indexed chunks in a project (id, source file, content preview), optionally filtered by source file. Use to inspect what is stored.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in ListPointsInput) (*mcp.CallToolResult, any, error) {
		filter := map[string]string{}
		if in.SourceFile != "" {
			filter["source_file"] = in.SourceFile
		}
		points, err := svc.ListPoints(ctx, in.ProjectID, filter)
		if err != nil {
			return nil, nil, err
		}
		if points == nil {
			points = []rag.PointInfo{}
		}
		return nil, map[string]any{"points": points, "count": len(points)}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "rag_delete_points",
		Description: "Delete specific chunks/points from a project by their IDs (e.g. to remove stale entries without a full re-index).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in DeletePointsInput) (*mcp.CallToolResult, any, error) {
		if err := svc.DeletePoints(ctx, in.ProjectID, in.PointIDs); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"status": "deleted", "project_id": in.ProjectID, "count": len(in.PointIDs)}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "rag_stats",
		Description: "Get aggregate RAG statistics: projects, total chunks, embedding model, query latency, and token usage.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in EmptyInput) (*mcp.CallToolResult, any, error) {
		stats, err := svc.ListProjects(ctx)
		if err != nil {
			return nil, nil, err
		}
		totalChunks := 0
		for _, s := range stats {
			totalChunks += s.ChunkCount
		}
		if rp, ok := svc.Provider().(*rag.RemoteProvider); ok {
			remote, err := rp.RemoteStats(ctx)
			if err != nil {
				return nil, nil, err
			}
			remote["mode"] = "client"
			remote["server"] = rp.BaseURL()
			return nil, remote, nil
		}
		m := svc.MetricsSnapshot(ctx)
		return nil, map[string]any{
			"total_projects": len(stats),
			"total_chunks":   totalChunks,
			"embed_model":    svc.EmbedModel(),
			"metrics":        m,
		}, nil
	})
}
