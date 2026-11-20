package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/enowdev/enowx-rag/mcp-server/pkg/config"
	"github.com/enowdev/enowx-rag/mcp-server/pkg/core"
	"github.com/enowdev/enowx-rag/mcp-server/pkg/rag"
)

// --- Request / response types for setup wizard endpoints ---

// setupConfigRequest is the JSON body sent by the wizard for both /test and
// /apply. It mirrors config.Config but uses flat JSON field names that the
// React wizard sends.
type setupConfigRequest struct {
	VectorStore   string `json:"vector_store"`
	Embedder      string `json:"embedder"`
	VoyageAPIKey  string `json:"voyage_api_key"`
	VoyageModel   string `json:"voyage_model"`
	VoyageDim     int    `json:"voyage_dim"`
	OpenAIAPIKey  string `json:"openai_api_key"`
	OpenAIModel   string `json:"openai_model"`
	OpenAIBaseURL string `json:"openai_base_url"`
	OpenAIDim     int    `json:"openai_dim"`
	PGVectorDSN   string `json:"pgvector_dsn"`
	SQLitePath    string `json:"sqlite_path"`
	QdrantURL     string `json:"qdrant_url"`
	QdrantAPIKey  string `json:"qdrant_api_key"`
	ChromaURL     string `json:"chroma_url"`
	TEIURL        string `json:"tei_url"`
	// RerankerModel: absent = rerank-3, "" or "off" = no reranking.
	RerankerModel    *string `json:"reranker_model"`
	AutoSyncInterval string  `json:"auto_sync_interval"`
	Restart          bool    `json:"restart"`
}

// withRunning fills what the request leaves empty from the running
// configuration (environment variables included), so a wizard on a server
// whose .env already holds the key or the DSN doesn't have to repeat it.
func (r *setupConfigRequest) withRunning() *setupConfigRequest {
	run := runningConfig()
	c := *r
	if c.VectorStore == "" {
		c.VectorStore = run.VectorStore
	}
	if c.Embedder == "" {
		c.Embedder = run.Embedder
	}
	if c.VoyageAPIKey == "" || isMasked(c.VoyageAPIKey) {
		c.VoyageAPIKey = run.Voyage.APIKey
	}
	if c.PGVectorDSN == "" || isMasked(c.PGVectorDSN) {
		c.PGVectorDSN = run.PGVectorDSN
	}
	if c.OpenAIAPIKey == "" || isMasked(c.OpenAIAPIKey) {
		c.OpenAIAPIKey = run.OpenAI.APIKey
	}
	if c.QdrantAPIKey == "" || isMasked(c.QdrantAPIKey) {
		c.QdrantAPIKey = run.QdrantAPIKey
	}
	if c.SQLitePath == "" {
		c.SQLitePath = run.SQLitePath
	}
	return &c
}

func (r *setupConfigRequest) rerankerModel() string {
	if r.RerankerModel == nil {
		return rag.DefaultRerankModel
	}
	m := strings.TrimSpace(*r.RerankerModel)
	switch strings.ToLower(m) {
	case "", "off", "none", "false", "disabled", "0":
		return ""
	}
	return m
}

// toConfig merges the wizard request into base (the config file as written).
// Settings an environment variable fixes are left out (the variable wins
// anyway) and reported in skipped.
func (r *setupConfigRequest) toConfig(base *config.Config) (cfg *config.Config, skipped []string) {
	cfg = base
	locked := map[string]bool{}
	for _, k := range config.EnvLocked() {
		locked[k] = true
	}
	run := runningConfig()
	// What the environment sets for the non-secret settings: writing the
	// same value keeps config.yaml consistent (it would otherwise keep a
	// default such as vector_store: qdrant). Secrets are never copied.
	envValue := map[string]string{
		"vector_store": run.VectorStore, "embedder": run.Embedder, "voyage_model": run.Voyage.Model,
		"voyage_dim": fmt.Sprint(run.Voyage.Dim), "reranker_model": run.RerankerModel,
		"auto_sync_interval": run.AutoSyncInterval, "sqlite_path": run.SQLitePath,
		"qdrant_url": run.QdrantURL, "chroma_url": run.ChromaURL, "tei_url": run.TEIURL,
		"openai_model": run.OpenAI.Model, "openai_base_url": run.OpenAI.BaseURL,
	}
	set := func(key, val string, apply func()) {
		if locked[key] {
			if env, public := envValue[key]; public && strings.EqualFold(env, val) {
				apply()
				return
			}
			skipped = append(skipped, key)
			return
		}
		apply()
	}
	setStr := func(key string, dst *string, v string) {
		if v == "" || isMasked(v) {
			return
		}
		set(key, v, func() { *dst = v })
	}
	setStr("vector_store", &cfg.VectorStore, r.VectorStore)
	setStr("embedder", &cfg.Embedder, r.Embedder)
	setStr("voyage_api_key", &cfg.Voyage.APIKey, r.VoyageAPIKey)
	model := r.VoyageModel
	if model == "" && cfg.Voyage.Model == "" {
		model = "voyage-4"
	}
	setStr("voyage_model", &cfg.Voyage.Model, model)
	dim := r.VoyageDim
	if dim == 0 && cfg.Voyage.Dim == 0 {
		dim = 1024
		if info, ok := rag.LookupVoyageModel(cfg.Voyage.Model); ok {
			dim = info.DefaultDim
		}
	}
	if dim > 0 {
		set("voyage_dim", fmt.Sprint(dim), func() { cfg.Voyage.Dim = dim })
	}
	setStr("openai_api_key", &cfg.OpenAI.APIKey, r.OpenAIAPIKey)
	setStr("openai_model", &cfg.OpenAI.Model, r.OpenAIModel)
	setStr("openai_base_url", &cfg.OpenAI.BaseURL, r.OpenAIBaseURL)
	if r.OpenAIDim > 0 {
		set("openai_dim", fmt.Sprint(r.OpenAIDim), func() { cfg.OpenAI.Dim = r.OpenAIDim })
	}
	setStr("pgvector_dsn", &cfg.PGVectorDSN, r.PGVectorDSN)
	setStr("sqlite_path", &cfg.SQLitePath, r.SQLitePath)
	setStr("qdrant_url", &cfg.QdrantURL, r.QdrantURL)
	setStr("qdrant_api_key", &cfg.QdrantAPIKey, r.QdrantAPIKey)
	setStr("chroma_url", &cfg.ChromaURL, r.ChromaURL)
	setStr("tei_url", &cfg.TEIURL, r.TEIURL)
	if r.RerankerModel != nil || cfg.RerankerModel == "" {
		rr := r.rerankerModel()
		if rr == "" {
			rr = "off"
		}
		set("reranker_model", rr, func() { cfg.RerankerModel = rr })
	}
	setStr("auto_sync_interval", &cfg.AutoSyncInterval, r.AutoSyncInterval)
	return cfg, skipped
}

// fileConfigForWrite is the base for saving settings: the config file as
// written (no environment overrides, so env values never leak into it), or
// the defaults when there is no file yet.
func fileConfigForWrite() (*config.Config, error) {
	if _, err := os.Stat(config.Path()); os.IsNotExist(err) {
		return config.Default(), nil
	}
	return config.LoadFile()
}

// componentResult is the per-component health check result returned by
// POST /api/setup/test.
type componentResult struct {
	OK        bool   `json:"ok"`
	Message   string `json:"message"`
	LatencyMs int64  `json:"latency_ms"`
}

// setupTestResponse is the JSON body returned by POST /api/setup/test.
type setupTestResponse struct {
	VectorStore componentResult  `json:"vector_store"`
	Embedder    componentResult  `json:"embedder"`
	Reranker    *componentResult `json:"reranker,omitempty"`
}

// setupStatusResponse is the JSON body returned by GET /api/setup/status.
type setupStatusResponse struct {
	Configured       bool     `json:"configured"`
	ConfigPath       string   `json:"config_path"`
	ConfigFileExists bool     `json:"config_file_exists"`
	Missing          []string `json:"missing"`
	EnvLocked        []string `json:"env_locked"`
	AuthRequired     bool     `json:"auth_required"`
	RestartSupported bool     `json:"restart_supported"`
	// RestartPending: the settings are complete but this process started
	// before they were (it runs without a working embedder) — restart it.
	RestartPending bool `json:"restart_pending"`
}

// --- Handlers ---

// SetupTest handles POST /api/setup/test.
// It tests the vector store (for pgvector: connect, enable the extension,
// versions), the embedder (a real embedding) and the reranker, returning
// per-component ok/message/latency_ms. Empty fields fall back to the running
// configuration (environment variables included).
func (h *Handlers) SetupTest(w http.ResponseWriter, r *http.Request) {
	var in setupConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req := in.withRunning()
	if req.VectorStore == "" {
		writeErr(w, http.StatusBadRequest, "vector_store is required")
		return
	}
	if req.Embedder == "" {
		writeErr(w, http.StatusBadRequest, "embedder is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	resp := setupTestResponse{
		VectorStore: testVectorStore(ctx, req),
		Embedder:    testEmbedder(ctx, req),
	}
	if strings.EqualFold(req.Embedder, "voyage") && req.VoyageAPIKey != "" {
		if model := req.rerankerModel(); model != "" {
			if res, ok := testVoyageReranker(ctx, req.VoyageAPIKey, model).(map[string]any); ok {
				cr := componentResult{}
				cr.OK, _ = res["ok"].(bool)
				cr.Message, _ = res["message"].(string)
				cr.LatencyMs, _ = res["latency_ms"].(int64)
				resp.Reranker = &cr
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// SetupApply handles POST /api/setup/apply.
// It merges the submitted settings into ~/.enowx-rag/config.yaml (0600) —
// settings fixed by environment variables are not written — and, with
// restart, restarts the server so they take effect.
func (h *Handlers) SetupApply(w http.ResponseWriter, r *http.Request) {
	var req setupConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	run := runningConfig()
	if req.VectorStore == "" && run.VectorStore == "" {
		writeErr(w, http.StatusBadRequest, "vector_store is required")
		return
	}
	if req.Embedder == "" && run.Embedder == "" {
		writeErr(w, http.StatusBadRequest, "embedder is required")
		return
	}
	if req.AutoSyncInterval != "" {
		if _, err := core.ParseAutoSyncInterval(req.AutoSyncInterval); err != nil {
			writeErr(w, http.StatusBadRequest, "auto_sync_interval: "+err.Error())
			return
		}
	}
	base, err := fileConfigForWrite()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	cfg, skipped := req.toConfig(base)
	if err := config.Save(cfg); err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Sprintf("failed to save config: %v", err))
		return
	}
	warnings := []string{}
	for _, k := range skipped {
		warnings = append(warnings, fmt.Sprintf("%s is set by %s in the environment, which wins over config.yaml", k, config.EnvVarFor(k)))
	}
	restarting := false
	if req.Restart {
		restarting = h.scheduleRestart()
		if !restarting {
			warnings = append(warnings, "this server cannot restart itself: restart it to use the new settings")
		}
	}
	// Do not echo the config back — it contains secrets (e.g. Voyage API key).
	writeJSON(w, http.StatusOK, map[string]any{
		"status":           "saved",
		"path":             config.Path(),
		"restart_required": !restarting,
		"restarting":       restarting,
		"warnings":         warnings,
	})
}

// SetupStatus handles GET /api/setup/status (open).
// configured is true when the settings the chosen store and embedder need
// are all present — from config.yaml or from environment variables (a Docker
// deploy with a complete .env never sees the wizard). missing lists what's
// still needed, so the wizard only asks for that.
func (h *Handlers) SetupStatus(w http.ResponseWriter, r *http.Request) {
	_, err := os.Stat(config.Path())
	fileExists := err == nil
	run := runningConfig()
	missing := missingSettings(run)
	envLocked := config.EnvLocked()
	configured := len(missing) == 0 && (fileExists || len(envLocked) > 0)
	writeJSON(w, http.StatusOK, setupStatusResponse{
		Configured:       configured,
		ConfigPath:       config.Path(),
		ConfigFileExists: fileExists,
		Missing:          missing,
		EnvLocked:        envLocked,
		AuthRequired:     config.EffectiveAdminToken() != "",
		RestartSupported: h.opts.Restart != nil,
		RestartPending:   configured && h.opts.Runtime.Embedder != "" && !h.opts.Runtime.EmbedReady,
	})
}

// missingSettings lists the required settings cfg lacks for its store and
// embedder.
func missingSettings(cfg *config.Config) []string {
	missing := []string{}
	switch strings.ToLower(cfg.VectorStore) {
	case "pgvector":
		if cfg.PGVectorDSN == "" {
			missing = append(missing, "pgvector_dsn")
		}
	case "qdrant":
		if cfg.QdrantURL == "" {
			missing = append(missing, "qdrant_url")
		}
	case "chroma":
		if cfg.ChromaURL == "" {
			missing = append(missing, "chroma_url")
		}
	case "remote":
		if cfg.RemoteURL == "" {
			missing = append(missing, "remote_url")
		}
		return missing // the server embeds
	}
	switch strings.ToLower(cfg.Embedder) {
	case "", "voyage":
		if cfg.Voyage.APIKey == "" {
			missing = append(missing, "voyage_api_key")
		}
	case "openai":
		if cfg.OpenAI.Model == "" {
			missing = append(missing, "openai_model")
		}
	case "tei":
		if cfg.TEIURL == "" {
			missing = append(missing, "tei_url")
		}
	}
	return missing
}

// --- Connectivity test helpers ---

// testVectorStore checks the configured vector store for real.
func testVectorStore(ctx context.Context, req *setupConfigRequest) componentResult {
	start := time.Now()

	switch strings.ToLower(req.VectorStore) {
	case "pgvector":
		if req.PGVectorDSN == "" {
			return componentResult{OK: false, Message: "pgvector_dsn is required (postgres://user:password@host:5432/dbname)"}
		}
		pr, err := rag.ProbePostgres(ctx, req.PGVectorDSN, true)
		if err != nil {
			return componentResult{OK: false, Message: err.Error(), LatencyMs: time.Since(start).Milliseconds()}
		}
		return componentResult{
			OK:        true,
			Message:   fmt.Sprintf("connected to %s at %s, pgvector %s ready", pr.ServerVersion, pr.Target, pr.ExtensionVersion),
			LatencyMs: time.Since(start).Milliseconds(),
		}

	case "sqlite":
		path := req.SQLitePath
		if path == "" {
			path = (&config.Config{}).StorePath()
		}
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return componentResult{OK: false, Message: fmt.Sprintf("cannot create %s: %v", dir, err)}
		}
		probe, err := os.CreateTemp(dir, ".write-test-*")
		if err != nil {
			return componentResult{OK: false, Message: fmt.Sprintf("%s is not writable: %v", dir, err)}
		}
		probe.Close()
		os.Remove(probe.Name())
		return componentResult{OK: true, Message: "built-in SQLite store at " + path, LatencyMs: time.Since(start).Milliseconds()}

	case "qdrant":
		url := req.QdrantURL
		if url == "" {
			return componentResult{OK: false, Message: "qdrant_url is required", LatencyMs: 0}
		}
		ok, msg := httpHealthCheck(ctx, strings.TrimRight(url, "/")+"/healthz")
		return componentResult{OK: ok, Message: msg, LatencyMs: time.Since(start).Milliseconds()}

	case "chroma":
		url := req.ChromaURL
		if url == "" {
			return componentResult{OK: false, Message: "chroma_url is required", LatencyMs: 0}
		}
		ok, msg := httpHealthCheck(ctx, strings.TrimRight(url, "/")+"/api/v1/heartbeat")
		return componentResult{OK: ok, Message: msg, LatencyMs: time.Since(start).Milliseconds()}

	default:
		return componentResult{OK: false, Message: fmt.Sprintf("unsupported vector store: %s", req.VectorStore), LatencyMs: 0}
	}
}

// testEmbedder embeds a test sentence with the configured embedder.
func testEmbedder(ctx context.Context, req *setupConfigRequest) componentResult {
	start := time.Now()

	switch strings.ToLower(req.Embedder) {
	case "voyage":
		if req.VoyageAPIKey == "" {
			return componentResult{OK: false, Message: "voyage_api_key is required (it starts with \"pa-\"; get one at dashboard.voyageai.com)"}
		}
		model := req.VoyageModel
		if model == "" {
			model = "voyage-4"
		}
		res := testVoyageModel(ctx, req.VoyageAPIKey, model, req.VoyageDim)
		ok, _ := res["ok"].(bool)
		msg, _ := res["message"].(string)
		return componentResult{OK: ok, Message: msg, LatencyMs: time.Since(start).Milliseconds()}

	case "tei":
		teiURL := req.TEIURL
		if teiURL == "" {
			return componentResult{OK: false, Message: "tei_url is required", LatencyMs: 0}
		}
		ok, msg := testTEIEmbed(ctx, teiURL)
		return componentResult{OK: ok, Message: msg, LatencyMs: time.Since(start).Milliseconds()}

	case "openai":
		if req.OpenAIModel == "" {
			return componentResult{OK: false, Message: "openai_model is required", LatencyMs: 0}
		}
		ok, msg := testOpenAIEmbed(ctx, req.OpenAIAPIKey, req.OpenAIModel, req.OpenAIBaseURL)
		return componentResult{OK: ok, Message: msg, LatencyMs: time.Since(start).Milliseconds()}

	default:
		return componentResult{OK: false, Message: fmt.Sprintf("unsupported embedder: %s", req.Embedder), LatencyMs: 0}
	}
}

// httpHealthCheck sends a GET request to the given URL and returns
// (true, "connected to <url>") on HTTP 200, or (false, "<error>") on failure.
func httpHealthCheck(ctx context.Context, url string) (bool, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, fmt.Sprintf("invalid URL %s: %s", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, fmt.Sprintf("cannot connect to %s — %s", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("%s returned HTTP %d", url, resp.StatusCode)
	}
	return true, fmt.Sprintf("connected to %s", url)
}

// testOpenAIEmbed sends a minimal embedding request to an OpenAI-compatible
// endpoint to verify reachability, model, and (if provided) the API key. It
// reuses the rag provider so URL normalization matches production behavior.
func testOpenAIEmbed(ctx context.Context, apiKey, model, baseURL string) (bool, string) {
	client := rag.NewOpenAIEmbeddingClient(apiKey, model, baseURL, 0)
	vecs, err := client.Embed(ctx, []string{"test"})
	if err != nil {
		if strings.Contains(err.Error(), "401") {
			return false, "OpenAI API key is invalid or missing"
		}
		return false, fmt.Sprintf("cannot reach embeddings endpoint — %s", err)
	}
	dim := 0
	if len(vecs) == 1 {
		dim = len(vecs[0])
	}
	return true, fmt.Sprintf("OpenAI-compatible endpoint connected (model: %s, dim: %d)", model, dim)
}

// testTEIEmbed sends a minimal embedding request to the TEI server to
// verify that the service is reachable and responding.
func testTEIEmbed(ctx context.Context, teiURL string) (bool, string) {
	url := strings.TrimRight(teiURL, "/") + "/embed"
	body := `{"inputs":["test"],"options":{"wait_for_model":true}}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return false, fmt.Sprintf("failed to create request: %s", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, fmt.Sprintf("cannot connect to TEI at %s — %s", teiURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("TEI at %s returned HTTP %d", teiURL, resp.StatusCode)
	}
	return true, fmt.Sprintf("TEI connected at %s", teiURL)
}

// parsePGDSN extracts the host and port from a PostgreSQL connection string.
// Supports both URL format (postgresql://host:port/db) and keyword format
// (host=... port=...). Returns ("localhost", "5432") as default if parsing
// fails.
func parsePGDSN(dsn string) (host, port string) {
	host = "localhost"
	port = "5432"

	// URL format: postgresql://user@host:port/db
	if strings.HasPrefix(dsn, "postgresql://") || strings.HasPrefix(dsn, "postgres://") {
		// Strip scheme
		rest := dsn
		if idx := strings.Index(dsn, "://"); idx >= 0 {
			rest = dsn[idx+3:]
		}
		// Strip credentials (user:pass@)
		if atIdx := strings.LastIndex(rest, "@"); atIdx >= 0 {
			rest = rest[atIdx+1:]
		}
		// Strip path
		if slashIdx := strings.Index(rest, "/"); slashIdx >= 0 {
			rest = rest[:slashIdx]
		}
		// Now rest is host:port or just host
		if colonIdx := strings.LastIndex(rest, ":"); colonIdx >= 0 {
			host = rest[:colonIdx]
			port = rest[colonIdx+1:]
		} else {
			host = rest
		}
		if host == "" {
			host = "localhost"
		}
		return host, port
	}

	// Keyword format: host=... port=... dbname=...
	// Split by spaces and look for host= and port= keys.
	for _, part := range strings.Fields(dsn) {
		if strings.HasPrefix(part, "host=") {
			h := strings.TrimPrefix(part, "host=")
			if h != "" {
				host = h
			}
		}
		if strings.HasPrefix(part, "port=") {
			p := strings.TrimPrefix(part, "port=")
			if p != "" {
				port = p
			}
		}
	}

	return host, port
}

// tcpDial attempts a TCP connection to the given host:port with the context's
// deadline. It returns nil on success, or an error describing the failure.
func tcpDial(ctx context.Context, host, port string) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}
