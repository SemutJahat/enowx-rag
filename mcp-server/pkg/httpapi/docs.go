package httpapi

import (
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
)

// docSection is one documentation page. Body is markdown, rendered by the Docs
// UI and readable by agents via GET /api/docs/{id}.
type docSection struct {
	ID    string
	Title string
	Body  func(base, exe string) string
}

// docSections is the ordered documentation table of contents. Bodies are
// functions so they can embed the live server base URL and binary path.
var docSections = []docSection{
	{ID: "overview", Title: "Overview", Body: docOverview},
	{ID: "quickstart", Title: "Quick start", Body: docQuickstart},
	{ID: "vps", Title: "Deploy on a VPS", Body: docVPS},
	{ID: "client-mode", Title: "Connect machines & import", Body: docClientMode},
	{ID: "mcp-tools", Title: "MCP tools", Body: docMCPTools},
	{ID: "api-reference", Title: "API reference", Body: docAPIReference},
	{ID: "embedders", Title: "Embedders (Voyage)", Body: docEmbedders},
	{ID: "vector-stores", Title: "Vector stores", Body: docVectorStores},
	{ID: "search", Title: "Search (hybrid / rerank / compress)", Body: docSearch},
	{ID: "migration", Title: "Migration (API)", Body: docMigration},
	{ID: "metrics", Title: "Metrics", Body: docMetrics},
	{ID: "remote", Title: "MCP over HTTP", Body: docRemote},
	{ID: "agent-setup", Title: "Agent setup", Body: docAgentSetup},
}

// requestBase is the URL clients reach this server at, for links and config
// snippets. Behind a reverse proxy (the Caddy service in
// docker-compose.vps.yml) the original scheme and host come from
// X-Forwarded-Proto / X-Forwarded-Host. These only shape text shown back to
// the same caller, never access decisions.
func requestBase(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := strings.ToLower(firstHeaderValue(r, "X-Forwarded-Proto")); p == "http" || p == "https" {
		scheme = p
	}
	host := r.Host
	if fh := firstHeaderValue(r, "X-Forwarded-Host"); fh != "" && !strings.ContainsAny(fh, " /\\\t@") {
		host = fh
	}
	return scheme + "://" + host
}

// firstHeaderValue returns the first comma-separated value of a header
// (proxies append to X-Forwarded-* lists), trimmed.
func firstHeaderValue(r *http.Request, name string) string {
	v := r.Header.Get(name)
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// DocsList handles GET /api/docs — the documentation table of contents.
func (h *Handlers) DocsList(w http.ResponseWriter, r *http.Request) {
	type item struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	out := make([]item, 0, len(docSections))
	for _, s := range docSections {
		out = append(out, item{ID: s.ID, Title: s.Title})
	}
	writeJSON(w, http.StatusOK, out)
}

// DocsSection handles GET /api/docs/{section} — one section's markdown.
func (h *Handlers) DocsSection(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "section")
	base := requestBase(r)
	exe, _ := os.Executable()
	for _, s := range docSections {
		if s.ID == id {
			w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(s.Body(base, exe)))
			return
		}
	}
	writeErr(w, http.StatusNotFound, "unknown docs section")
}

// SetupDocs handles GET /api/docs/setup — kept as an alias for the agent-setup
// section so existing links and the copy-paste prompt keep working.
func (h *Handlers) SetupDocs(w http.ResponseWriter, r *http.Request) {
	base := requestBase(r)
	exe, _ := os.Executable()
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(docAgentSetup(base, exe)))
}

// --- Section bodies ---

// Bodies are markdown with placeholders: {{BASE}} (the URL clients reach this
// server at), {{EXE}} (this binary) and {{AGENTS_BLOCK}} (the AGENTS.md block,
// as an indented code block). The dashboard renders one paragraph per line.
func docText(base, exe, md string) string {
	return strings.NewReplacer(
		"{{BASE}}", base,
		"{{EXE}}", exe,
		"{{AGENTS_BLOCK}}", indentBlock(agentsBlock("PROJECT_ID")),
	).Replace(md)
}

func indentBlock(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "    " + l
	}
	return strings.Join(lines, "\n")
}

func docOverview(base, exe string) string {
	return docText(base, exe, `# enowx-rag

Per-project RAG memory for AI coding agents. Each project gets its own collection: agents index a codebase and the decisions behind it, and retrieve the relevant parts before they code.

## Three ways to run (one binary)

| Mode | How it runs | Where the work happens |
| --- | --- | --- |
| Local | `+"`"+`enowx-rag`+"`"+`, launched by your MCP client | everything on this machine |
| Server | `+"`"+`enowx-rag --serve`+"`"+` (e.g. on a VPS) | dashboard, REST API and MCP at `+"`"+`/mcp`+"`"+`; embeds, stores, reranks |
| Client | `+"`"+`enowx-rag`+"`"+` with `+"`"+`RAG_VECTOR_STORE=remote`+"`"+` (written by `+"`"+`enowx-rag connect`+"`"+`) | this machine reads and chunks files; the server embeds and stores them |

The usual setup: one **server** on a VPS with PostgreSQL + pgvector, and every machine you code on in **client mode**. See *Deploy on a VPS* and *Connect machines & import*.

## The dashboard

The server's dashboard is for hosting and monitoring:

- **Overview** — health, store, models, projects (and the machine and folder each was last synced from), running jobs, live activity.
- **Chunks** — browse and delete a project's chunks.
- **Import** — upload a local SQLite store (`+"`"+`rag.db`+"`"+`); its projects are converted into this server's store, vectors reused where the model matches.
- **Connect** — the commands and config to use this server from a machine.
- **Settings** — Voyage embedding and rerank models (live test, re-embed cost), database, auto-sync, admin token.
- **Docs** — these pages.

## Building blocks

- **Vector stores**: PostgreSQL + pgvector (recommended for a server), the built-in SQLite store (local), Qdrant, Chroma (experimental).
- **Embedders**: Voyage AI (recommended), any OpenAI-compatible API, or self-hosted TEI.
- **Search**: dense, hybrid (dense + lexical), Voyage reranking, near-duplicate compression.`)
}

func docQuickstart(base, exe string) string {
	return docText(base, exe, `# Quick start

## 1. The server (VPS)

On a fresh Linux VPS:

    curl -fsSL https://raw.githubusercontent.com/enowdev/enowx-rag/main/deploy/install-vps.sh | sudo sh

It installs Docker, starts PostgreSQL + pgvector and enowx-rag, generates the database password and the admin token, and prints the dashboard URL. Open it and sign in with the token: the setup wizard finds the database by itself, you pick the Voyage models (and paste the key if the installer didn't get one). Details in *Deploy on a VPS*.

## 2. Each machine you code on

    curl -fsSL https://raw.githubusercontent.com/enowdev/enowx-rag/main/install.sh | sh
    enowx-rag connect --remote {{BASE}} --token <ADMIN_TOKEN>

`+"`"+`connect`+"`"+` adds enowx-rag in client mode to every MCP client it finds (Claude Code, Cursor, …); restart the client. Bring the memory this machine already has:

    enowx-rag push --remote {{BASE}} --token <ADMIN_TOKEN>

## 3. Index a project

Ask your agent to call `+"`"+`rag_index_project`+"`"+` once with the project folder. The folder is remembered: before a search whose index is older than `+"`"+`auto_sync_interval`+"`"+` (default 60s, `+"`"+`off`+"`"+` to disable) new and edited code is indexed and removed code is deleted.

The dashboard's **Re-index** button needs no path. It re-syncs the project's folder when this server can read it, and otherwise re-embeds the stored chunks with the current model (the code itself is synced by the machines that index it).

## Local only (no server)

Let your MCP client run the binary with no flags, with `+"`"+`RAG_VECTOR_STORE=sqlite`+"`"+` (the built-in store, `+"`"+`~/.enowx-rag/rag.db`+"`"+`, no service to run) and `+"`"+`RAG_VOYAGE_API_KEY`+"`"+` in its environment. Or run `+"`"+`enowx-rag --serve`+"`"+` once and finish the setup wizard: the settings land in `+"`"+`~/.enowx-rag/config.yaml`+"`"+`, which the stdio server reads too. Binary: {{EXE}}`)
}

func docVPS(base, exe string) string {
	return docText(base, exe, `# Deploy on a VPS

## One command

    curl -fsSL https://raw.githubusercontent.com/enowdev/enowx-rag/main/deploy/install-vps.sh | sudo sh

Without questions: `+"`"+`curl -fsSL …/install-vps.sh | sudo ENOWX_DOMAIN=rag.example.com VOYAGE_API_KEY=pa-... sh`+"`"+`

The installer:

1. installs Docker if it's missing and clones the repo into `+"`"+`/opt/enowx-rag`+"`"+`;
2. writes `+"`"+`/opt/enowx-rag/.env`+"`"+` (mode 600) with a random PostgreSQL password and admin token;
3. starts `+"`"+`docker-compose.vps.yml`+"`"+`: **postgres** (pgvector, not published), **enowx-rag**, and with `+"`"+`ENOWX_DOMAIN`+"`"+` set **caddy** for HTTPS (Let's Encrypt);
4. waits for `+"`"+`/api/health`+"`"+`, then prints the URL, the token and the commands for your machines.

Re-run it to update; `+"`"+`.env`+"`"+` is kept. On a VPS with less than 2 GB of RAM it embeds the committed dashboard instead of building it with npm.

## By hand

    git clone https://github.com/enowdev/enowx-rag && cd enowx-rag
    cp deploy/vps.env.example .env      # set POSTGRES_PASSWORD and RAG_ADMIN_TOKEN
    docker compose -f docker-compose.vps.yml up -d --build
    docker compose -f docker-compose.vps.yml --profile https up -d --build   # HTTPS (ENOWX_DOMAIN)

Without Docker: run `+"`"+`enowx-rag --serve`+"`"+` under systemd (`+"`"+`deploy/enowx-rag.service`+"`"+`) with `+"`"+`RAG_VECTOR_STORE=pgvector`+"`"+`, `+"`"+`RAG_PGVECTOR_DSN`+"`"+` and `+"`"+`RAG_ADMIN_TOKEN`+"`"+` in its environment, behind your own HTTPS proxy.

## Onboarding

The first visit opens the setup wizard:

1. **Database** — detected from the environment (the compose file sets pgvector), with the PostgreSQL and pgvector versions; nothing to type.
2. **Models** — Voyage embedding model and dimension, and the rerank model, with price, context length and a live test of the key.
3. **Review & apply** — each component is tested for real; the server saves `+"`"+`config.yaml`+"`"+` and restarts itself.
4. **Connect** — the commands for your machines.

An old SQLite store (`+"`"+`rag.db`+"`"+`) in the data volume is imported into pgvector when the server starts, then renamed `+"`"+`rag.db.imported-<time>`+"`"+`.

## Settings from the environment

Settings given as environment variables (the compose `+"`"+`.env`+"`"+`) are read-only in the dashboard, and secrets are never written to `+"`"+`config.yaml`+"`"+`. Change them in `+"`"+`.env`+"`"+`, then `+"`"+`docker compose -f docker-compose.vps.yml up -d`+"`"+`.

| Variable | Meaning |
| --- | --- |
| `+"`"+`RAG_ADMIN_TOKEN`+"`"+` | required; gates the dashboard, `+"`"+`/api`+"`"+` and `+"`"+`/mcp`+"`"+` |
| `+"`"+`POSTGRES_PASSWORD`+"`"+` | database password (letters and digits; the DSN is built from it) |
| `+"`"+`RAG_VOYAGE_API_KEY`+"`"+` | optional; otherwise set it in the dashboard |
| `+"`"+`ENOWX_DOMAIN`+"`"+` | DNS name for HTTPS through Caddy |
| `+"`"+`ENOWX_BIND`+"`"+`, `+"`"+`ENOWX_PORT`+"`"+` | where the dashboard port is published (127.0.0.1 behind Caddy) |
| `+"`"+`ENOWX_WEB_BUILD`+"`"+` | `+"`"+`build`+"`"+` (npm inside Docker) or `+"`"+`prebuilt`+"`"+` (committed dashboard) |

## Security

- The admin token can read and delete every project: keep it secret.
- Use HTTPS (`+"`"+`ENOWX_DOMAIN`+"`"+`) when the server is reachable from the internet; over plain HTTP the token travels in clear text.
- Docker publishes ports past ufw/firewalld: without the HTTPS profile, restrict the port at your provider's firewall.
- PostgreSQL is reachable only inside the compose network.`)
}

func docClientMode(base, exe string) string {
	return docText(base, exe, `# Connect machines & import

## Client mode

In client mode your machine keeps doing what only it can do (reading the project folder, chunking, skipping secrets and `+"`"+`.gitignore`+"`"+`d files) and sends the chunks to the server, which embeds, stores, reranks and records metrics. No Voyage key or database is needed on the machine, and the dashboard shows which machine and folder each project was last synced from.

    enowx-rag connect --remote {{BASE}} --token <ADMIN_TOKEN>

- Configures every MCP client it finds; `+"`"+`--client claude-code,cursor`+"`"+` picks them, `+"`"+`--print`+"`"+` only prints the config to paste.
- Merges into existing config files and keeps a `+"`"+`.bak`+"`"+` copy.
- Checks the URL and token first and says what's wrong (unreachable, bad token).

The config it writes runs the local binary with:

    RAG_VECTOR_STORE=remote
    RAG_REMOTE_URL={{BASE}}
    RAG_REMOTE_TOKEN=<ADMIN_TOKEN>

The dashboard's **Connect** page shows the same commands and snippets per client.

## Import local memory (SQLite → server)

    enowx-rag push --remote {{BASE}} --token <ADMIN_TOKEN>

1. Takes a consistent snapshot of `+"`"+`~/.enowx-rag/rag.db`+"`"+` (safe while agents use it).
2. Uploads it gzip-compressed, with progress.
3. Shows the plan per project: chunks, and how many keep their vectors.
4. Imports into the server's store as a job and follows it.
5. Tells the server where each project's folder is, so auto-sync carries on.

Vectors made by the server's embedding model at its dimension are copied as they are; the voyage-4 family (voyage-4-large, voyage-4, voyage-4-lite) shares one embedding space, so those are reused across models too. Other chunks are embedded again from their text; `+"`"+`push`+"`"+` shows the token estimate and asks first (`+"`"+`--yes`+"`"+` skips the question).

Options: `+"`"+`--file other.db`+"`"+`, `+"`"+`--project ID`+"`"+` (repeatable), `+"`"+`--mode replace`+"`"+` (delete the server's copy of each project first; default `+"`"+`merge`+"`"+`).

From the dashboard: **Import** → choose `+"`"+`rag.db`+"`"+` → review the plan → start. With curl:

    curl -H "Authorization: Bearer $TOKEN" --data-binary @rag.db {{BASE}}/api/import/upload
    curl -H "Authorization: Bearer $TOKEN" -d '{"upload_id":"u_…","mode":"merge"}' {{BASE}}/api/import/start

## Automatic conversion on the server

When a server configured for pgvector starts and finds a SQLite store (`+"`"+`rag.db`+"`"+`) next to its config, it imports it into pgvector the same way and renames the file `+"`"+`rag.db.imported-<time>`+"`"+` (with its `+"`"+`-wal`+"`"+`/`+"`"+`-shm`+"`"+`). Switching an existing install from SQLite to pgvector is therefore: change the store in Settings, save & restart.`)
}

func docMCPTools(base, exe string) string {
	return docText(base, exe, `# MCP tools

enowx-rag exposes these MCP tools in every mode (local, client, and remote over `+"`"+`/mcp`+"`"+`). Project IDs isolate collections.

## Write / index
## `+"`"+`rag_create_project`+"`"+`
Create a project collection. Input: `+"`"+`project_id`+"`"+`.

## `+"`"+`rag_index`+"`"+`
Index documents you pass directly. Input: `+"`"+`project_id`+"`"+`, `+"`"+`documents`+"`"+` (each `+"`"+`{id?, content, meta?}`+"`"+`). Use it to save facts and decisions.

## `+"`"+`rag_index_project`+"`"+`
Scan a folder and sync its code and text files: new and edited code is indexed, removed code and files deleted, incrementally. Honors `+"`"+`.gitignore`+"`"+`, skips node_modules/.git/vendor/dist/build and secrets. Input: `+"`"+`project_id`+"`"+`, `+"`"+`directory`+"`"+`. Run it once per folder: it is re-synced automatically before searches (`+"`"+`RAG_AUTO_SYNC_INTERVAL`+"`"+`, default 60s). In client mode the folder is read on your machine, not on the server.

## Read / search
## `+"`"+`rag_semantic_search`+"`"+`
Search a project. Input: `+"`"+`project_id`+"`"+`, `+"`"+`query`+"`"+`, `+"`"+`limit`+"`"+`, and optionally `+"`"+`recall`+"`"+`, `+"`"+`hybrid`+"`"+`, `+"`"+`rerank`+"`"+`, `+"`"+`compress`+"`"+` (hybrid and rerank default on). Returns chunks with scores.

## `+"`"+`rag_retrieve_context`+"`"+`
Like search, but returns a compact context string plus the chunks — convenient for feeding an LLM. Same options as `+"`"+`rag_semantic_search`+"`"+`.

## Inspect / manage
## `+"`"+`rag_list_projects`+"`"+`
All projects with chunk counts. No input.

## `+"`"+`rag_project_exists`+"`"+`
Whether a project has indexed memory. Input: `+"`"+`project_id`+"`"+`.

## `+"`"+`rag_list_points`+"`"+`
Chunks in a project (id, source file, preview), optionally filtered by `+"`"+`source_file`+"`"+`. Input: `+"`"+`project_id`+"`"+`, `+"`"+`source_file?`+"`"+`.

## `+"`"+`rag_delete_points`+"`"+`
Delete chunks by ID. Input: `+"`"+`project_id`+"`"+`, `+"`"+`point_ids`+"`"+`.

## `+"`"+`rag_delete_project`+"`"+`
Delete a project and all its data. Input: `+"`"+`project_id`+"`"+`.

## `+"`"+`rag_stats`+"`"+`
Projects, total chunks, embed model, latency, tokens (from the server in client mode). No input.

**Typical loop:** `+"`"+`rag_index_project`+"`"+` once per folder → `+"`"+`rag_retrieve_context`+"`"+` before coding (file changes sync automatically) → do the work → `+"`"+`rag_index`+"`"+` the new facts and decisions.`)
}

func docAPIReference(base, exe string) string {
	return docText(base, exe, `# API reference

All endpoints are under {{BASE}}/api. When an admin token is set (`+"`"+`RAG_ADMIN_TOKEN`+"`"+` or `+"`"+`config.yaml`+"`"+`) every endpoint except `+"`"+`/api/health`+"`"+` and `+"`"+`/api/setup/status`+"`"+` needs `+"`"+`Authorization: Bearer <token>`+"`"+`. Endpoints marked 🔒 also need the token when none is configured, unless called from localhost. Errors are JSON `+"`"+`{error, hint?}`+"`"+`.

## Server
- `+"`"+`GET /api/health`+"`"+` — liveness, version, boot id (open)
- `+"`"+`GET /api/server`+"`"+` — store, embedder, reranker, auto-sync, stale chunks, running jobs
- `+"`"+`GET /api/models`+"`"+` — Voyage embedding and rerank catalog (dimensions, price, context)
- `+"`"+`POST /api/models/test`+"`"+` 🔒 — test a Voyage model or reranker with a key
- `+"`"+`POST /api/server/restart`+"`"+` 🔒 — restart the process (re-reads config.yaml)

## Projects
- `+"`"+`GET /api/projects`+"`"+` — projects with chunk counts and last sync source
- `+"`"+`GET /api/projects/{id}`+"`"+` — one project
- `+"`"+`POST /api/projects/{id}`+"`"+` — create
- `+"`"+`DELETE /api/projects/{id}`+"`"+` — delete with all its chunks
- `+"`"+`GET /api/projects/{id}/points`+"`"+` — chunks (`+"`"+`?source_file=&offset=&limit=`+"`"+`)
- `+"`"+`POST /api/projects/{id}/points/delete`+"`"+` — delete chunks `+"`"+`{ids}`+"`"+`
- `+"`"+`DELETE /api/projects/{id}/points/{pointId}`+"`"+` — delete one chunk
- `+"`"+`POST /api/projects/{id}/reindex`+"`"+` — `+"`"+`{mode?: auto|files|reembed, directory?}`+"`"+`; a job. `+"`"+`auto`+"`"+` re-syncs the known folder when the server can read it, else re-embeds
- `+"`"+`POST /api/projects/{id}/documents`+"`"+` — index documents `+"`"+`{documents: [{id?, content, meta?}]}`+"`"+`
- `+"`"+`POST /api/projects/{id}/documents/payload`+"`"+` — update chunk metadata (client mode)
- `+"`"+`POST /api/projects/{id}/sources`+"`"+` — report a sync from a machine (client mode)

## Search, metrics, activity
- `+"`"+`POST /api/search`+"`"+` — `+"`"+`{project_id, query, k, recall, hybrid, rerank, compress}`+"`"+`
- `+"`"+`GET /api/stats`+"`"+` — totals + embed model
- `+"`"+`GET /api/metrics`+"`"+` — latency, tokens, retrieval breakdown (see *Metrics*)
- `+"`"+`GET /api/activity`+"`"+` — recent events (persisted)
- `+"`"+`GET /api/events`+"`"+` — SSE stream of the same events, live
- `+"`"+`GET /api/jobs`+"`"+`, `+"`"+`GET /api/jobs/{id}`+"`"+` — background jobs (re-index, import, startup)

## Import
- `+"`"+`POST /api/import/upload`+"`"+` — body: a SQLite store, raw or gzip; returns the plan + cost estimate
- `+"`"+`POST /api/import/start`+"`"+` — `+"`"+`{upload_id, projects?, mode?: merge|replace, rename?}`+"`"+`; a job
- `+"`"+`DELETE /api/import/upload/{id}`+"`"+` — discard an upload (they expire after an hour)

## Setup & settings
- `+"`"+`GET /api/setup/status`+"`"+` — configured? what's missing, env-locked keys (open)
- `+"`"+`GET /api/setup/detect`+"`"+` 🔒 — what the server can find on its own (database, key, old SQLite store)
- `+"`"+`POST /api/setup/test`+"`"+` 🔒 — test store, embedder and reranker
- `+"`"+`POST /api/setup/apply`+"`"+` 🔒 — save config.yaml (`+"`"+`restart: true`+"`"+` restarts)
- `+"`"+`GET /api/setup/config`+"`"+` — current config, secrets masked
- `+"`"+`GET /api/setup/config/reveal`+"`"+` 🔒 — with secrets
- `+"`"+`POST /api/setup/config`+"`"+` 🔒 — change settings; returns warnings (re-embed cost, restart)
- `+"`"+`POST /api/setup/gen-token`+"`"+` 🔒 — generate and save an admin token
- `+"`"+`GET /api/setup/mcp-snippet?client_id=&mode=client|remote`+"`"+` — config to paste into a client

## Migration
- `+"`"+`POST /api/migrate`+"`"+` 🔒 — re-embed or move a project to another store (see *Migration*)

## Docs
- `+"`"+`GET /api/docs`+"`"+` — this table of contents
- `+"`"+`GET /api/docs/{section}`+"`"+` — one section as markdown`)
}

func docEmbedders(base, exe string) string {
	return docText(base, exe, `# Embedders (Voyage)

## Voyage AI (`+"`"+`voyage`+"`"+`, recommended)

Set the key and models in **Settings → Models** (or the wizard). Every choice shows the dimensions it supports, the price per million tokens and the context length, and **Test** calls Voyage with your key: you see the dimension it returned, the latency and the tokens used, or the error with a hint (wrong key, quota, model name).

| Model | Use |
| --- | --- |
| `+"`"+`voyage-4-large`+"`"+` | best quality |
| `+"`"+`voyage-4`+"`"+` | default; quality and cost balanced |
| `+"`"+`voyage-4-lite`+"`"+` | cheapest, fastest |
| `+"`"+`voyage-code-4`+"`"+` | tuned for code |

Embedding dimension: 1024 (default), 256, 512 or 2048. Smaller vectors are cheaper to store and search, at some quality cost.

**Rerank** (`+"`"+`rerank-3`+"`"+` default, `+"`"+`rerank-3-lite`+"`"+` faster, or off) re-orders the search candidates by relevance; its test ranks sample documents to show it works.

Environment: `+"`"+`RAG_VOYAGE_API_KEY`+"`"+`, `+"`"+`RAG_VOYAGE_MODEL`+"`"+`, `+"`"+`RAG_VECTOR_DIM`+"`"+`, `+"`"+`RAG_RERANKER_MODEL`+"`"+` (`+"`"+`off`+"`"+` disables). `+"`"+`RAG_VOYAGE_BASE_URL`+"`"+` points at a Voyage-compatible gateway.

## Changing the model or dimension

Vectors of different models or dimensions can't be compared. Settings tells you before you save:

- within the voyage-4 family at the same dimension: **no re-embed** (shared embedding space);
- otherwise: how many chunks are embedded again, the estimated tokens and cost.

After the restart, a background job re-embeds the affected chunks from their stored text (pgvector resizes its column first, only once a test embedding with the new settings succeeded). Searches skip chunks that aren't re-embedded yet.

## Other embedders

- **OpenAI-compatible** (`+"`"+`openai`+"`"+`): any `+"`"+`/v1/embeddings`+"`"+` API — OpenAI, Together, Jina, Mistral, Ollama, LiteLLM. `+"`"+`RAG_OPENAI_BASE_URL`+"`"+`, `+"`"+`RAG_OPENAI_MODEL`+"`"+`, `+"`"+`RAG_OPENAI_API_KEY`+"`"+`, `+"`"+`RAG_OPENAI_DIM`+"`"+` (0 = detect).
- **TEI** (`+"`"+`tei`+"`"+`): self-hosted Text Embeddings Inference. `+"`"+`RAG_TEI_URL`+"`"+`.

The server starts without a key; until one is set, indexing and search answer with an error that says to set it.`)
}

func docVectorStores(base, exe string) string {
	return docText(base, exe, `# Vector stores

Set with `+"`"+`vector_store`+"`"+` in config.yaml or `+"`"+`RAG_VECTOR_STORE`+"`"+`.

## PostgreSQL + pgvector (`+"`"+`pgvector`+"`"+`, recommended for a server)
`+"`"+`RAG_PGVECTOR_DSN`+"`"+`. One table for all projects, an HNSW index, hybrid search (dense + full-text) and the retrieval breakdown. A new embedding dimension resizes the vector column after a successful test embedding, then the chunks are re-embedded in the background. The wizard's test reports the PostgreSQL and pgvector versions and whether the extension can be created.

## Built-in SQLite (`+"`"+`sqlite`+"`"+`, local)
No service to run: `+"`"+`~/.enowx-rag/rag.db`+"`"+` (or `+"`"+`sqlite_path`+"`"+`). Best for a single machine. A server configured for pgvector imports it on start.

## Remote (`+"`"+`remote`+"`"+`, client mode)
Not a store on this machine: chunks go to an enowx-rag server (`+"`"+`RAG_REMOTE_URL`+"`"+`, `+"`"+`RAG_REMOTE_TOKEN`+"`"+`). See *Connect machines & import*.

## Qdrant (`+"`"+`qdrant`+"`"+`)
`+"`"+`RAG_QDRANT_URL`+"`"+`, optional `+"`"+`RAG_QDRANT_API_KEY`+"`"+`. One collection per project.

## Chroma (`+"`"+`chroma`+"`"+`)
**Experimental**: legacy `+"`"+`/api/v1`+"`"+` API, mock-tested only.

| Feature | pgvector | SQLite | Qdrant | Chroma |
| --- | :---: | :---: | :---: | :---: |
| Index / search / delete | ✅ | ✅ | ✅ | ⚠️ |
| Hybrid search | ✅ | ✅ | — | — |
| Retrieval breakdown | ✅ | — | — | — |
| Resize on new dimension | ✅ | ✅ | — | — |`)
}

func docSearch(base, exe string) string {
	return docText(base, exe, `# Search: hybrid, rerank, compress

Options on `+"`"+`POST /api/search`+"`"+` and the search MCP tools.

## Recall vs. K
`+"`"+`recall`+"`"+` (default 40) candidates are retrieved, then narrowed to `+"`"+`k`+"`"+` (default 5) results. Reranking works best with recall > k.

## Hybrid (`+"`"+`hybrid`+"`"+`)
Combines dense vector similarity with lexical full-text search using Reciprocal Rank Fusion (RRF, k=60). Stores without it fall back to dense search. Good for keyword-heavy queries.

## Rerank (`+"`"+`rerank`+"`"+`)
Re-orders the candidates with the Voyage rerank model (`+"`"+`rerank-3`+"`"+` by default) when one is configured: retrieve `+"`"+`recall`+"`"+` → rerank → keep the top `+"`"+`k`+"`"+`. Falls back to the semantic order if the reranker fails.

## Compress (`+"`"+`compress`+"`"+`)
Drops near-duplicate results (same content hash or identical text) after ranking — deterministic, no LLM.

## Retrieval breakdown
On pgvector hybrid searches, the dashboard shows how many results came from the dense and the lexical ranking (see *Metrics*).`)
}

func docMigration(base, exe string) string {
	return docText(base, exe, `# Migration (API)

To bring local memory to a server, use *Connect machines & import* (`+"`"+`enowx-rag push`+"`"+` or the dashboard's **Import** page). To change the embedding model or dimension of the store you run on, change it in Settings: the chunks are re-embedded in place.

`+"`"+`POST /api/migrate`+"`"+` 🔒 remains for moving a project between stores with credentials you pass (e.g. Qdrant → pgvector, or a Qdrant Cloud import). It re-embeds from the text stored with every chunk, runs as a job with progress events (`+"`"+`migration_started/progress/completed/failed`+"`"+`), and never deletes the source.

Body (between stores): `+"`"+`{source_project, dest_project, vector_store, embedder, ...connection/model/dim}`+"`"+`. Body (cloud import): add `+"`"+`cloud_source: {provider, url, api_key, index, text_field}`+"`"+` (Qdrant Cloud is verified; Pinecone, Weaviate and Chroma Cloud are experimental).`)
}

func docMetrics(base, exe string) string {
	return docText(base, exe, `# Metrics

Every search records metrics, exposed at `+"`"+`GET {{BASE}}/api/metrics`+"`"+` and shown on the dashboard Overview.

## What's tracked
- **Latency** — average, p50, p95 over recent queries.
- **Token usage** — embed and rerank tokens reported by Voyage (0 for backends that don't report them, e.g. TEI).
- **Retrieval breakdown** — dense vs. lexical counts on pgvector hybrid searches.
- **Activity** — syncs reported by machines, imports, re-index and startup jobs, restarts (`+"`"+`GET /api/activity`+"`"+`, live over `+"`"+`GET /api/events`+"`"+`).

In client mode the server records the metrics of the searches it runs for your machines.

## Persistence
Metrics and activity live in a local SQLite file (`+"`"+`~/.enowx-rag/metrics.db`+"`"+`, `+"`"+`/data/.enowx-rag/metrics.db`+"`"+` in Docker), so they survive restarts on any store. If it can't be opened, metrics fall back to memory (`+"`"+`"persistent": false`+"`"+`).`)
}

func docRemote(base, exe string) string {
	return docText(base, exe, `# MCP over HTTP

Besides client mode (see *Connect machines & import*), an MCP client can talk to the server directly at `+"`"+`/mcp`+"`"+` (Streamable HTTP, stateless). Then the server also reads the project folders, so `+"`"+`rag_index_project`+"`"+` only works for folders on the server; use client mode for code on your machine.

    {
      "mcpServers": {
        "enowx-rag": {
          "url": "{{BASE}}/mcp",
          "headers": { "Authorization": "Bearer <ADMIN_TOKEN>" }
        }
      }
    }

`+"`"+`GET /api/setup/mcp-snippet?client_id=<id>&mode=remote`+"`"+` returns this in the format a given client expects.

The admin token gates `+"`"+`/mcp`+"`"+` and `+"`"+`/api`+"`"+` alike. Without a token there is no auth: only acceptable on a trusted local network.`)
}

func docAgentSetup(base, exe string) string {
	return docText(base, exe, `# Agent setup

Set up enowx-rag memory for the current project, on the machine where the code is. Skip any step that is already done. The enowx-rag server is {{BASE}}.

## 1. Install the binary (skip if `+"`"+`enowx-rag version`+"`"+` works)

    curl -fsSL https://raw.githubusercontent.com/enowdev/enowx-rag/main/install.sh | sh

## 2. Connect your MCP client (skip if its config already has an `+"`"+`enowx-rag`+"`"+` entry)

    enowx-rag connect --remote {{BASE}} --token <ADMIN_TOKEN> --client <CLIENT_ID>

CLIENT_ID is one of: claude-code, claude-desktop, cursor, cline, windsurf, codex, zed, continue. Ask the user for the admin token if you don't have it; never write it into the project. The client must be restarted to load the server.

## 3. Install the skill (optional; Claude Code and Factory)

    mkdir -p ~/.claude/skills/enowx-rag
    curl -fsSL https://raw.githubusercontent.com/enowdev/enowx-rag/main/skill/enowx-rag.md -o ~/.claude/skills/enowx-rag/skill.md

## 4. Add the enowx-rag block to the project's AGENTS.md (skip if it has `+"`"+`<!-- enowx-rag:start -->`+"`"+`)

Append this, with PROJECT_ID replaced by a short slug for the project (e.g. the repository name):

{{AGENTS_BLOCK}}

## 5. Index the project

Call the `+"`"+`rag_index_project`+"`"+` MCP tool with PROJECT_ID and the project's absolute folder. Existing local memory: `+"`"+`enowx-rag push --remote {{BASE}} --token <ADMIN_TOKEN>`+"`"+`.`)
}
