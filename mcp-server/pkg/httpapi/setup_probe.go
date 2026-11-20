package httpapi

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// agentsMarkerStart / agentsMarkerEnd delimit the enowx-rag block inside a
// project's AGENTS.md so it can be merged idempotently without touching the
// user's own content.
const (
	agentsMarkerStart = "<!-- enowx-rag:start -->"
	agentsMarkerEnd   = "<!-- enowx-rag:end -->"
)

// skillDirs are the known per-client skill directories. Only some clients have
// a skill system; the skill is "installed" if present in any of them.
var skillDirs = []string{"~/.claude/skills/enowx-rag", "~/.factory/skills/enowx-rag"}

// SetupProbe handles GET /api/setup/probe?client=<id>&dir=<project-dir>.
// It reports what is already set up so an agent can skip finished steps:
//   - mcp: whether the enowx-rag server is in the client's config (per client,
//     or all clients when `client` is omitted)
//   - skill: whether the skill is installed in a known skill directory
//   - agents_md: whether the project's AGENTS.md exists and already contains the
//     enowx-rag block
func (h *Handlers) SetupProbe(w http.ResponseWriter, r *http.Request) {
	clientID := r.URL.Query().Get("client")
	dir := r.URL.Query().Get("dir")

	// MCP status: one client, or a map of all clients.
	mcp := map[string]bool{}
	if clientID != "" {
		if c, ok := mcpClientByID(clientID); ok {
			mcp[c.ID] = c.isInstalled()
		}
	} else {
		for _, c := range mcpClients {
			mcp[c.ID] = c.isInstalled()
		}
	}

	// Skill status.
	skillInstalled := false
	skillDir := ""
	for _, d := range skillDirs {
		p := expandHome(d)
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			skillInstalled = true
			skillDir = p
			break
		}
	}

	// AGENTS.md status for the given project directory.
	agentsExists := false
	agentsHasBlock := false
	agentsPath := ""
	if dir != "" {
		agentsPath = filepath.Join(expandHome(dir), "AGENTS.md")
		if data, err := os.ReadFile(agentsPath); err == nil {
			agentsExists = true
			agentsHasBlock = strings.Contains(string(data), agentsMarkerStart)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"mcp": mcp,
		"skill": map[string]any{
			"installed": skillInstalled,
			"dir":       skillDir,
		},
		"agents_md": map[string]any{
			"path":      agentsPath,
			"exists":    agentsExists,
			"has_block": agentsHasBlock,
		},
	})
}

// agentsBlock builds the enowx-rag AGENTS.md block for a project, wrapped in the
// idempotent markers so it can be merged in and out cleanly.
func agentsBlock(projectID string) string {
	return fmt.Sprintf(`%s
## enowx-rag memory (project: %s)

This project uses the enowx-rag MCP server for per-project RAG memory.

- **First time**: if the project was never indexed, call `+"`rag_index_project`"+` once with the project directory. After that the server re-syncs it automatically before searches (`+"`RAG_AUTO_SYNC_INTERVAL`"+`, default 60s); call it again only to force a sync.
- **Before coding**: call `+"`rag_retrieve_context`"+` with project ID `+"`%s`"+` and the user's query; use any relevant context.
- **After coding**: call `+"`rag_index`"+` with new facts/decisions/gotchas (code changes are synced automatically; the reasons behind them are not). Keep chunks focused; tag with `+"`type:architecture|decision|api|bugfix|howto|snippet`"+`.
- Each project has its own collection; do not mix project memories.
%s`, agentsMarkerStart, projectID, projectID, agentsMarkerEnd)
}
