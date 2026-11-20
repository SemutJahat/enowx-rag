package httpapi

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/enowdev/enowx-rag/mcp-server/pkg/config"
)

// mcpServerEntry builds the command + env for the enowx-rag MCP server from the
// currently saved config. The command is this binary's own path so the client
// launches the exact server the user configured.
// mcpServerEntry builds the MCP config entry. When remoteURL is non-empty it
// returns a remote entry (url + optional bearer token); otherwise a local stdio
// entry (this binary's path + env from the saved config).
func mcpServerEntry(remoteURL, token string) (mcpEntry, error) {
	if remoteURL != "" {
		return mcpEntry{RemoteURL: remoteURL, Token: token}, nil
	}
	return localServerEntry()
}

// clientModeEntry is the stdio entry for a machine that indexes its local
// folders into the enowx-rag server at serverURL (client mode).
func clientModeEntry(command, serverURL, token string) mcpEntry {
	if command == "" {
		command = "enowx-rag"
	}
	env := map[string]string{"RAG_VECTOR_STORE": "remote", "RAG_REMOTE_URL": serverURL}
	if token != "" {
		env["RAG_REMOTE_TOKEN"] = token
	}
	return mcpEntry{Command: command, Env: env}
}

// localServerEntry is the stdio entry running this binary with the saved config.
func localServerEntry() (mcpEntry, error) {
	exe, err := os.Executable()
	if err != nil {
		return mcpEntry{}, fmt.Errorf("resolve binary path: %w", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return mcpEntry{}, fmt.Errorf("load config: %w", err)
	}

	env := map[string]string{
		"RAG_VECTOR_STORE": cfg.VectorStore,
		"RAG_EMBEDDER":     cfg.Embedder,
	}
	switch cfg.VectorStore {
	case "qdrant":
		env["RAG_QDRANT_URL"] = cfg.QdrantURL
		if cfg.QdrantAPIKey != "" {
			env["RAG_QDRANT_API_KEY"] = cfg.QdrantAPIKey
		}
	case "chroma":
		env["RAG_CHROMA_URL"] = cfg.ChromaURL
	case "pgvector":
		if cfg.PGVectorDSN != "" {
			env["RAG_PGVECTOR_DSN"] = cfg.PGVectorDSN
		}
	}
	switch cfg.Embedder {
	case "voyage":
		if cfg.Voyage.APIKey != "" {
			env["RAG_VOYAGE_API_KEY"] = cfg.Voyage.APIKey
		}
		if cfg.Voyage.Model != "" {
			env["RAG_VOYAGE_MODEL"] = cfg.Voyage.Model
		}
	case "tei":
		env["RAG_TEI_URL"] = cfg.TEIURL
	case "openai":
		if cfg.OpenAI.APIKey != "" {
			env["RAG_OPENAI_API_KEY"] = cfg.OpenAI.APIKey
		}
		if cfg.OpenAI.Model != "" {
			env["RAG_OPENAI_MODEL"] = cfg.OpenAI.Model
		}
		if cfg.OpenAI.BaseURL != "" {
			env["RAG_OPENAI_BASE_URL"] = cfg.OpenAI.BaseURL
		}
		if cfg.OpenAI.Dim > 0 {
			env["RAG_OPENAI_DIM"] = fmt.Sprintf("%d", cfg.OpenAI.Dim)
		}
	}

	return mcpEntry{Command: exe, Env: env}, nil
}

// SetupMCPSnippet handles GET /api/setup/mcp-snippet?client_id=...
// It returns the config file path and a ready-to-paste snippet, without writing
// anything. Used for the manual tab and unknown ("Other") clients.
func (h *Handlers) SetupMCPSnippet(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("client_id")
	client, ok := mcpClientByID(id)
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown client_id")
		return
	}
	// mode=remote&remote_url=...&token=... produces a remote (url + headers)
	// snippet; otherwise a local stdio snippet.
	// mode=client produces a client-mode stdio entry pointing at this server
	// (server_url defaults to the URL the request came in on).
	remoteURL := r.URL.Query().Get("remote_url")
	var entry mcpEntry
	switch r.URL.Query().Get("mode") {
	case "client":
		server := r.URL.Query().Get("server_url")
		if server == "" {
			server = requestBase(r)
		}
		token := r.URL.Query().Get("token")
		if token == "" {
			token = "YOUR-ADMIN-TOKEN"
		}
		entry = clientModeEntry("enowx-rag", server, token)
	default:
		if r.URL.Query().Get("mode") == "remote" && remoteURL == "" {
			remoteURL = requestBase(r) + "/mcp"
		}
		var err error
		entry, err = mcpServerEntry(remoteURL, r.URL.Query().Get("token"))
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	path, content := client.snippet(entry)
	writeJSON(w, http.StatusOK, map[string]any{
		"client":  client.Label,
		"path":    path,
		"format":  string(client.Format),
		"content": content,
	})
}

// SetupSkillGuide handles GET /api/setup/skill-guide.
// Returns manual install instructions for the skill (command + target paths).
// The skill is not auto-installed because only some clients support skills.
func (h *Handlers) SetupSkillGuide(w http.ResponseWriter, r *http.Request) {
	type target struct {
		Client string `json:"client"`
		Dir    string `json:"dir"`
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"note":        "Skills are supported by some clients only (e.g. Claude Code, Factory). Install manually into your client's skills directory.",
		"source_file": "skill/enowx-rag.md (in the enowx-rag repo)",
		"targets": []target{
			{Client: "Claude Code", Dir: "~/.claude/skills/enowx-rag/"},
			{Client: "Factory", Dir: "~/.factory/skills/enowx-rag/"},
		},
		"commands": []string{
			"mkdir -p ~/.claude/skills/enowx-rag",
			"cp /path/to/enowx-rag/skill/enowx-rag.md ~/.claude/skills/enowx-rag/skill.md",
		},
	})
}

// ClientInfo describes an MCP client the connect command can configure.
type ClientInfo struct {
	ID         string
	Label      string
	Present    bool   // its config file or folder exists: the client is used here
	HasEnowx   bool   // its config already has an enowx-rag entry
	GlobalPath string // the config file a global install writes
}

// MCPClients lists the MCP clients enowx-rag knows how to configure.
func MCPClients() []ClientInfo {
	out := make([]ClientInfo, 0, len(mcpClients))
	for _, c := range mcpClients {
		path, _ := c.resolvePath("global", "")
		present := false
		if _, err := os.Stat(path); err == nil {
			present = true
		} else if _, err := os.Stat(filepath.Dir(path)); err == nil && filepath.Dir(path) != filepath.Dir(expandHome("~/x")) {
			present = true // e.g. ~/.cursor exists, mcp.json not yet
		}
		out = append(out, ClientInfo{ID: c.ID, Label: c.Label, Present: present, HasEnowx: c.isInstalled(), GlobalPath: path})
	}
	return out
}

// InstallClientMode writes a client-mode enowx-rag entry (command, with
// RAG_VECTOR_STORE=remote pointing at serverURL) into a client's global MCP
// config, merging with what is there and keeping a .bak of the old file.
func InstallClientMode(clientID, command, serverURL, token string) (path string, backedUp bool, err error) {
	c, ok := mcpClientByID(clientID)
	if !ok {
		return "", false, fmt.Errorf("unknown client %q", clientID)
	}
	path, err = c.resolvePath("global", "")
	if err != nil {
		return "", false, err
	}
	backedUp, err = c.install(path, clientModeEntry(command, serverURL, token))
	return path, backedUp, err
}

// ClientModeSnippet is the config text to paste into a client's MCP config by
// hand, and where it goes.
func ClientModeSnippet(clientID, command, serverURL, token string) (path, content string, err error) {
	c, ok := mcpClientByID(clientID)
	if !ok {
		return "", "", fmt.Errorf("unknown client %q", clientID)
	}
	path, content = c.snippet(clientModeEntry(command, serverURL, token))
	return path, content, nil
}
