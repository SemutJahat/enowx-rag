package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/enowdev/enowx-rag/mcp-server/pkg/config"
	"github.com/enowdev/enowx-rag/mcp-server/pkg/core"
	"github.com/enowdev/enowx-rag/mcp-server/pkg/rag"
)

const maskDots = "••••"

// maskSecret returns a masked view of a secret: first 4 + last 3 chars, middle
// as dots. Empty stays empty; short secrets are fully dotted.
func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return maskDots
	}
	return s[:4] + maskDots + s[len(s)-3:]
}

// isMasked reports whether v is a masked value sent back unchanged.
func isMasked(v string) bool { return strings.Contains(v, maskDots) }

var dsnPasswordKV = regexp.MustCompile(`(password=)('[^']*'|\S+)`)

// maskDSN hides the password of a PostgreSQL connection string.
func maskDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return rag.DescribeDSN(dsn)
		}
		if _, has := u.User.Password(); has {
			u.User = url.UserPassword(u.User.Username(), "PASSWORD")
			return strings.Replace(u.String(), ":PASSWORD@", ":"+maskDots+"@", 1)
		}
		return dsn
	}
	return dsnPasswordKV.ReplaceAllString(dsn, "${1}"+maskDots)
}

// SetupConfig handles GET /api/setup/config — the configuration in effect
// (environment variables included) with secrets masked, plus which settings
// the environment fixes. Safe for display.
func (h *Handlers) SetupConfig(w http.ResponseWriter, r *http.Request) {
	cfg := runningConfig()
	reranker := cfg.RerankerModel
	if !cfg.RerankerEnabled() {
		reranker = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"vector_store":       cfg.VectorStore,
		"embedder":           cfg.Embedder,
		"qdrant_url":         cfg.QdrantURL,
		"qdrant_api_key":     maskSecret(cfg.QdrantAPIKey),
		"chroma_url":         cfg.ChromaURL,
		"pgvector_dsn":       maskDSN(cfg.PGVectorDSN),
		"sqlite_path":        cfg.StorePath(),
		"tei_url":            cfg.TEIURL,
		"voyage_model":       cfg.Voyage.Model,
		"voyage_dim":         cfg.Voyage.Dim,
		"voyage_api_key":     maskSecret(cfg.Voyage.APIKey),
		"openai_base_url":    cfg.OpenAI.BaseURL,
		"openai_model":       cfg.OpenAI.Model,
		"openai_api_key":     maskSecret(cfg.OpenAI.APIKey),
		"reranker_model":     reranker,
		"auto_sync_interval": cfg.AutoSyncInterval,
		"admin_token_set":    config.EffectiveAdminToken() != "",
		"admin_token":        maskSecret(config.EffectiveAdminToken()),
		"env_locked":         config.EnvLocked(),
	})
}

// SetupConfigReveal handles GET /api/setup/config/reveal — the FULL secrets.
// Gated by LocalOrAdminMiddleware (localhost or a valid admin token) so it is
// not open to anonymous callers on an exposed daemon.
func (h *Handlers) SetupConfigReveal(w http.ResponseWriter, r *http.Request) {
	cfg := runningConfig()
	writeJSON(w, http.StatusOK, map[string]any{
		"voyage_api_key": cfg.Voyage.APIKey,
		"qdrant_api_key": cfg.QdrantAPIKey,
		"openai_api_key": cfg.OpenAI.APIKey,
		"pgvector_dsn":   cfg.PGVectorDSN,
		"admin_token":    config.EffectiveAdminToken(),
	})
}

// configUpdate is the body of POST /api/setup/config: only the fields present
// change. Masked values sent back unchanged are ignored.
type configUpdate struct {
	VoyageAPIKey     *string `json:"voyage_api_key"`
	VoyageModel      *string `json:"voyage_model"`
	VoyageDim        *int    `json:"voyage_dim"`
	RerankerModel    *string `json:"reranker_model"`
	AutoSyncInterval *string `json:"auto_sync_interval"`
	QdrantAPIKey     *string `json:"qdrant_api_key"`
	OpenAIAPIKey     *string `json:"openai_api_key"`
	OpenAIModel      *string `json:"openai_model"`
	OpenAIBaseURL    *string `json:"openai_base_url"`
	PGVectorDSN      *string `json:"pgvector_dsn"`
	AdminToken       *string `json:"admin_token"`
	Restart          bool    `json:"restart"`
}

// SetupConfigUpdate handles POST /api/setup/config — a partial update merged
// into config.yaml (never copying environment values into it). Changing a
// setting an environment variable fixes is refused (409). The response says
// whether a restart is needed and warns about consequences (re-embedding,
// unknown models, …); with restart, the server restarts itself.
func (h *Handlers) SetupConfigUpdate(w http.ResponseWriter, r *http.Request) {
	var req configUpdate
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	locked := map[string]bool{}
	for _, k := range config.EnvLocked() {
		locked[k] = true
	}
	run := runningConfig()
	cfg, err := fileConfigForWrite()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	warnings := []string{}
	changed := map[string]bool{}

	// str applies a string field; it returns false after writing a 409.
	str := func(key string, v *string, dst *string, current string) bool {
		if v == nil || isMasked(*v) {
			return true
		}
		val := strings.TrimSpace(*v)
		if val == current {
			return true
		}
		if locked[key] {
			writeErrHint(w, http.StatusConflict, fmt.Sprintf("%s is set by the environment variable %s", key, config.EnvVarFor(key)),
				fmt.Sprintf("change %s where the server is started (.env / docker-compose) and restart it", config.EnvVarFor(key)))
			return false
		}
		*dst = val
		changed[key] = true
		return true
	}

	// Voyage model and size: validate against the catalog.
	newModel := run.Voyage.Model
	if req.VoyageModel != nil && strings.TrimSpace(*req.VoyageModel) != "" {
		newModel = strings.TrimSpace(*req.VoyageModel)
	}
	info, known := rag.LookupVoyageModel(newModel)
	if req.VoyageModel != nil && !known && strings.EqualFold(run.Embedder, "voyage") {
		warnings = append(warnings, fmt.Sprintf("%s is not in the Voyage catalog this server knows — make sure the name is right", newModel))
	}
	newDim := run.Voyage.Dim
	if req.VoyageDim != nil && *req.VoyageDim > 0 {
		newDim = *req.VoyageDim
	}
	if known {
		if d, fixed := info.FixedDim(); fixed {
			newDim = d
		} else if !info.SupportsDim(newDim) {
			if req.VoyageDim != nil {
				writeErrHint(w, http.StatusBadRequest, fmt.Sprintf("%s does not support %d dimensions", newModel, newDim),
					fmt.Sprintf("pick one of %v", info.Dims))
				return
			}
			newDim = info.DefaultDim
		}
	}

	if !str("voyage_api_key", req.VoyageAPIKey, &cfg.Voyage.APIKey, run.Voyage.APIKey) ||
		!str("voyage_model", req.VoyageModel, &cfg.Voyage.Model, run.Voyage.Model) {
		return
	}
	if newDim != run.Voyage.Dim && (req.VoyageDim != nil || changed["voyage_model"]) {
		if locked["voyage_dim"] {
			writeErrHint(w, http.StatusConflict, "voyage_dim is set by the environment variable "+config.EnvVarFor("voyage_dim"),
				"change it where the server is started and restart it")
			return
		}
		cfg.Voyage.Dim = newDim
		changed["voyage_dim"] = true
	}
	if req.RerankerModel != nil {
		rr := strings.TrimSpace(*req.RerankerModel)
		switch strings.ToLower(rr) {
		case "", "off", "none", "false", "disabled", "0":
			rr = "off"
		default:
			if _, ok := rag.LookupVoyageReranker(rr); !ok {
				warnings = append(warnings, fmt.Sprintf("%s is not in the Voyage rerank catalog this server knows — make sure the name is right", rr))
			}
		}
		cur := run.RerankerModel
		if !run.RerankerEnabled() {
			cur = "off"
		}
		if !str("reranker_model", &rr, &cfg.RerankerModel, cur) {
			return
		}
	}
	if req.AutoSyncInterval != nil && strings.TrimSpace(*req.AutoSyncInterval) != "" {
		if _, err := core.ParseAutoSyncInterval(*req.AutoSyncInterval); err != nil {
			writeErr(w, http.StatusBadRequest, "auto_sync_interval: "+err.Error())
			return
		}
	}
	if !str("auto_sync_interval", req.AutoSyncInterval, &cfg.AutoSyncInterval, run.AutoSyncInterval) ||
		!str("qdrant_api_key", req.QdrantAPIKey, &cfg.QdrantAPIKey, run.QdrantAPIKey) ||
		!str("openai_api_key", req.OpenAIAPIKey, &cfg.OpenAI.APIKey, run.OpenAI.APIKey) ||
		!str("openai_model", req.OpenAIModel, &cfg.OpenAI.Model, run.OpenAI.Model) ||
		!str("openai_base_url", req.OpenAIBaseURL, &cfg.OpenAI.BaseURL, run.OpenAI.BaseURL) ||
		!str("pgvector_dsn", req.PGVectorDSN, &cfg.PGVectorDSN, run.PGVectorDSN) ||
		!str("admin_token", req.AdminToken, &cfg.AdminToken, config.EffectiveAdminToken()) {
		return
	}

	if len(changed) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"status": "unchanged", "restart_required": false, "restarting": false, "warnings": warnings})
		return
	}
	if err := config.Save(cfg); err != nil {
		writeErr(w, http.StatusInternalServerError, "save config: "+err.Error())
		return
	}

	// Consequences worth knowing before the restart.
	if changed["voyage_model"] || changed["voyage_dim"] {
		oldModel, oldDim := h.svc.EmbedIdentity()
		if oldModel != "" && (!rag.EmbedModelsCompatible(oldModel, newModel) || oldDim != newDim) {
			points := h.totalPoints(r.Context())
			warnings = append(warnings, fmt.Sprintf(
				"stored chunks were embedded with %s at %d dimensions: after the restart all %d are re-embedded with %s at %d in the background",
				oldModel, oldDim, points, newModel, newDim))
		} else if oldModel != "" {
			warnings = append(warnings, fmt.Sprintf("%s shares its embedding space with %s at the same size: stored vectors stay valid, nothing is re-embedded", newModel, oldModel))
		}
	}
	if changed["pgvector_dsn"] {
		warnings = append(warnings, "the new database starts empty: chunks in the old one are not moved (use Import or push to fill it)")
	}
	if changed["admin_token"] {
		warnings = append(warnings, "the admin token changed: log in again with the new one, and update it on client machines (enowx-rag connect)")
	}

	// The admin token is read on every request; everything else at startup.
	restartRequired := false
	for k := range changed {
		if k != "admin_token" {
			restartRequired = true
		}
	}
	restarting := false
	if req.Restart && restartRequired {
		restarting = h.scheduleRestart()
		if !restarting {
			warnings = append(warnings, "this server cannot restart itself: restart it to use the new settings")
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":           "saved",
		"restart_required": restartRequired && !restarting,
		"restarting":       restarting,
		"warnings":         warnings,
	})
}

func (h *Handlers) totalPoints(ctx context.Context) int {
	if st, ok := h.svc.Provider().(rag.EmbedStatser); ok {
		stats, err := st.EmbedStats(ctx, "")
		if err == nil {
			n := 0
			for _, s := range stats {
				n += s.Count
			}
			return n
		}
	}
	return 0
}

// SetupGenToken handles POST /api/setup/gen-token — generate a strong random
// admin token, save it to config.yaml, and return it ONCE (so the caller can
// copy it). Gated. The env var still takes precedence at runtime if set.
func (h *Handlers) SetupGenToken(w http.ResponseWriter, r *http.Request) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		writeErr(w, http.StatusInternalServerError, "generate token: "+err.Error())
		return
	}
	token := hex.EncodeToString(buf)
	cfg, err := fileConfigForWrite()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	cfg.AdminToken = token
	if err := config.Save(cfg); err != nil {
		writeErr(w, http.StatusInternalServerError, "save config: "+err.Error())
		return
	}
	envOverride := config.EffectiveAdminToken() != token // true if env var shadows it
	writeJSON(w, http.StatusOK, map[string]any{
		"token":        token,
		"saved":        true,
		"env_override": envOverride,
		"note":         "Copy this now — it is stored in config.yaml (0600). If RAG_ADMIN_TOKEN is set in the environment, that value takes precedence at runtime.",
	})
}

// SetupDetect handles GET /api/setup/detect: what onboarding can fill in by
// itself — the store the environment points at (probed for real), whether a
// Voyage key is already there, a local SQLite store worth importing, and the
// recommended settings.
func (h *Handlers) SetupDetect(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	run := runningConfig()
	locked := map[string]bool{}
	for _, k := range config.EnvLocked() {
		locked[k] = true
	}

	store := map[string]any{"type": run.VectorStore, "from_env": locked["vector_store"]}
	suggestedStore := run.VectorStore
	switch strings.ToLower(run.VectorStore) {
	case "pgvector":
		if run.PGVectorDSN == "" {
			store["ok"] = false
			store["message"] = "pgvector needs a connection string (RAG_PGVECTOR_DSN or the field below)"
			break
		}
		store["dsn_masked"] = maskDSN(run.PGVectorDSN)
		store["dsn_from_env"] = locked["pgvector_dsn"]
		pr, err := rag.ProbePostgres(ctx, run.PGVectorDSN, false)
		store["server_version"] = pr.ServerVersion
		store["extension_version"] = pr.ExtensionVersion
		if err != nil {
			store["ok"] = false
			store["message"] = err.Error()
		} else {
			store["ok"] = true
			ext := "pgvector " + pr.ExtensionVersion
			if pr.ExtensionVersion == "" {
				ext = "pgvector " + pr.ExtensionAvailable + " available (enabled on first start)"
				if pr.ExtensionAvailable == "" {
					store["ok"] = false
					ext = "pgvector is NOT installed in this PostgreSQL"
				}
			}
			store["message"] = fmt.Sprintf("%s at %s — %s", pr.ServerVersion, pr.Target, ext)
		}
	case "sqlite", "":
		store["type"] = "sqlite"
		store["ok"] = true
		store["message"] = "built-in SQLite store at " + run.StorePath()
		suggestedStore = "sqlite"
	default:
		store["ok"] = true
		store["message"] = run.VectorStore
	}

	out := map[string]any{
		"store":               store,
		"voyage_key_from_env": locked["voyage_api_key"],
		"voyage_key_set":      run.Voyage.APIKey != "",
		"suggested": map[string]any{
			"vector_store":   suggestedStore,
			"embedder":       "voyage",
			"voyage_model":   "voyage-4",
			"voyage_dim":     1024,
			"reranker_model": rag.DefaultRerankModel,
		},
	}
	// A local SQLite store with points while the server uses another store:
	// offer to import it.
	if !strings.EqualFold(run.VectorStore, "sqlite") {
		if path := run.StorePath(); fileHasData(path) {
			if snap, err := rag.OpenSQLiteSnapshot(path); err == nil {
				projects, _ := snap.Projects(ctx)
				snap.Close()
				n := 0
				for _, p := range projects {
					n += p.Points
				}
				if n > 0 {
					out["sqlite_store"] = map[string]any{"path": path, "points": n, "projects": len(projects)}
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func fileHasData(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Size() > 0
}
