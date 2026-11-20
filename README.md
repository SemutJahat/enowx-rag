# enowx-rag

[![CI](https://github.com/enowdev/enowx-rag/actions/workflows/ci.yml/badge.svg)](https://github.com/enowdev/enowx-rag/actions/workflows/ci.yml)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go 1.26+](https://img.shields.io/badge/Go-1.26+-00ADD8.svg?logo=go)](mcp-server/go.mod)

Per-project RAG memory MCP server. Each project gets its own vector collection, so an LLM can index context about a codebase and retrieve it quickly.

![enowx-rag dashboard — Overview](docs/screenshots/overview.png)

One binary, three ways to run it:

- **Local** (default): `enowx-rag` — your MCP client launches it over stdio; everything stays on this machine.
- **Server**: `enowx-rag --serve` — a dashboard for monitoring, a REST API, and **MCP over HTTP at `/mcp`**. It embeds (Voyage), stores (PostgreSQL + pgvector), reranks and records metrics, e.g. on a VPS.
- **Client**: `enowx-rag connect --remote https://rag.example.com --token …` — your machine reads and chunks the code, the server embeds and stores it. Existing local memory moves over with `enowx-rag push`.

### Run it on a VPS

```bash
curl -fsSL https://raw.githubusercontent.com/enowdev/enowx-rag/main/deploy/install-vps.sh | sudo sh
```

Docker, PostgreSQL + pgvector, the server and optional HTTPS, with generated secrets; the dashboard's setup wizard then only asks for the Voyage models. See [Deploy on a VPS](#deploy-on-a-vps-postgresql--pgvector).

---

## Why enowx-rag?

LLM coding agents forget everything between sessions and only see the files you
paste. enowx-rag gives an agent a **persistent, per-project memory** it can
search — so it recalls your architecture decisions, gotchas, and conventions
without you re-explaining them every time.

- **Per-project isolation.** Each project is its own vector collection
  (`project_<id>`), so an agent working in one repo never retrieves context
  leaked from another. Index a codebase once; query it in isolation.
- **Retrieval that actually finds things.** Dense semantic search, optional
  **hybrid** (dense + lexical RRF), **reranking** (Voyage `rerank-3`), and
  near-duplicate **compression** — tunable per query.
- **Persistent memory, durable metrics.** Design decisions and facts survive
  across sessions and restarts; query latency (p50/p95), token usage, and
  retrieval breakdown are persisted to a local SQLite DB (pure-Go, no cgo).
- **Bring your own stack.** Vector stores: **a built-in SQLite file** (nothing to install), **Qdrant, pgvector, Chroma**.
  Embedders: **Voyage AI, any OpenAI-compatible `/v1/embeddings` API, or
  self-hosted TEI**. Change the model in Settings and the chunks are
  re-embedded from their stored text — no re-scraping your codebase.
- **Local or hosted.** Local **MCP stdio** for your editor, or one server on a
  VPS (PostgreSQL + pgvector, bearer token, HTTPS) shared by all your machines
  in client mode, with a dashboard to watch it.
- **No lock-in, no toolchain.** A single self-contained binary with the
  dashboard embedded. Install via `curl | sh`, Homebrew, npm, `go install`, or
  a direct download. CGO-free, so it runs anywhere.

### Dashboard preview

The embedded dashboard (`enowx-rag --serve`) — real data from a live instance:

| Overview | Chunks |
|---|---|
| [![Overview](docs/screenshots/overview.png)](docs/screenshots/overview.png) | [![Chunks](docs/screenshots/chunks.png)](docs/screenshots/chunks.png) |
| **Docs** | **Settings** |
| [![Docs](docs/screenshots/docs.png)](docs/screenshots/docs.png) | [![Settings](docs/screenshots/settings.png)](docs/screenshots/settings.png) |

The dashboard is for hosting and monitoring: **Overview** (health, store,
models, projects and where each was last synced from, jobs, live activity),
**Chunks**, **Import** (bring a local `rag.db`), **Connect** (commands for your
machines), **Settings** (Voyage embed/rerank models with live tests and re-embed
cost, database, auto-sync, token) and **Docs**.

---

## Install

enowx-rag ships as a single self-contained binary (the dashboard is embedded) —
no runtime dependencies, no toolchain required. Pick whichever fits:

**Install script (macOS/Linux)** — downloads the prebuilt binary for your platform:

```bash
curl -fsSL https://raw.githubusercontent.com/enowdev/enowx-rag/main/install.sh | sh
```

Pin a version or install location with env vars:
`ENOWX_VERSION=v0.1.0 ENOWX_INSTALL_DIR=~/.local/bin`.

**Homebrew (macOS/Linux):**

```bash
brew install enowdev/tap/enowx-rag
```

**npm** (installs the native binary via a thin wrapper; handy if you already use Node):

```bash
npm install -g enowx-rag
```

**Go** (if you have Go 1.26+; builds from source, the dashboard is embedded from a committed build):

```bash
go install github.com/enowdev/enowx-rag/mcp-server/cmd/mcp-server@latest
```

**Prebuilt binaries** for macOS/Linux (amd64/arm64) and Windows (amd64) are
attached to every [GitHub Release](https://github.com/enowdev/enowx-rag/releases).

Verify with `enowx-rag version`, then run `enowx-rag --serve` and open
`http://localhost:7777`.

---

## Quick Start (one-command deploy)

### From source

```bash
git clone https://github.com/enowdev/enowx-rag.git
cd enowx-rag
make build && ./enowx-rag --serve
```

This builds the React SPA, compiles the Go binary (with the SPA embedded), and starts the HTTP server on port 7777. Open `http://localhost:7777` in your browser.

Set your embedding provider before starting:

```bash
export RAG_VOYAGE_API_KEY=your-voyage-api-key
make build && ./enowx-rag --serve
```

### With Docker (all-in-one)

```bash
export RAG_VOYAGE_API_KEY=your-voyage-api-key
docker compose -f docker-compose.all-in-one.yml up -d
```

The enowx-rag container serves both the API and UI on port 7777. Qdrant starts automatically as the vector store. See [Docker all-in-one](#docker-all-in-one) below for details.

---

## Deploy on a VPS (PostgreSQL + pgvector)

### One command

```bash
curl -fsSL https://raw.githubusercontent.com/enowdev/enowx-rag/main/deploy/install-vps.sh | sudo sh
# or without questions:
curl -fsSL https://raw.githubusercontent.com/enowdev/enowx-rag/main/deploy/install-vps.sh \
  | sudo ENOWX_DOMAIN=rag.example.com VOYAGE_API_KEY=pa-... sh
```

[`deploy/install-vps.sh`](deploy/install-vps.sh):

1. installs Docker if it's missing and clones the repo into `/opt/enowx-rag`;
2. writes `/opt/enowx-rag/.env` (mode 600) with a random PostgreSQL password and admin token;
3. starts [`docker-compose.vps.yml`](docker-compose.vps.yml): **postgres** (`pgvector/pgvector:pg17`, not published), **enowx-rag**, and with `ENOWX_DOMAIN` set **caddy** for HTTPS (Let's Encrypt);
4. waits for `/api/health` and prints the dashboard URL, the token, and the commands for your machines.

Re-run it to update (`.env` is kept). On a VPS with less than 2 GB of RAM it
embeds the committed dashboard instead of building it with npm
(`ENOWX_WEB_BUILD=prebuilt`).

By hand:

```bash
git clone https://github.com/enowdev/enowx-rag && cd enowx-rag
cp deploy/vps.env.example .env      # set POSTGRES_PASSWORD and RAG_ADMIN_TOKEN
docker compose -f docker-compose.vps.yml up -d --build
docker compose -f docker-compose.vps.yml --profile https up -d --build   # HTTPS (ENOWX_DOMAIN)
```

Without Docker, run the binary under systemd:
[`deploy/enowx-rag.service`](deploy/enowx-rag.service) (pgvector DSN and token in
`/etc/enowx-rag.env`, your own HTTPS proxy in front).

### Dokploy (or another Docker host with its own proxy)

Dokploy runs `docker-compose.vps.yml` as a Compose service behind its own
Traefik with HTTPS, so the Caddy profile stays off:

1. **Create Service → Compose**, provider Git/GitHub: this repository, branch
   `main`, compose path `./docker-compose.vps.yml`.
2. **Environment**: `POSTGRES_PASSWORD` (letters and digits,
   `openssl rand -hex 24`), `RAG_ADMIN_TOKEN` (`openssl rand -hex 32`);
   optionally `RAG_VOYAGE_API_KEY`, `ENOWX_WEB_BUILD=prebuilt` on a small
   server, `ENOWX_PORT` when 7777 is taken on the host. Keep **Create
   Environment File** on.
3. **Domains**: your host name, service `enowx-rag`, container port `7777`,
   HTTPS on (Let's Encrypt, or certificate provider **None** when Cloudflare
   connects with an origin certificate already added under Settings →
   Certificates).
4. **Preview Compose**: `enowx-rag` should list both `dokploy-network` and
   `default` under `networks` (Dokploy v0.31 keeps the stack's own network, so
   it still reaches `postgres`, which stays off Dokploy's shared network).
   Only if it lists `dokploy-network` alone (older Dokploy) turn on
   **Advanced → Isolated Deployments**.
5. **Deploy** and open the domain: the setup wizard asks for the Voyage key.
6. **Auto-deploy** (optional): turn on **Autodeploy**, then add the service's
   **Webhook URL** (Deployments tab) to the repository's GitHub webhooks
   (content type `application/json`, event *push*). Every push to the
   configured branch then redeploys. If the Dokploy panel's host is behind a
   Cloudflare IP allowlist, exempt `/api/deploy/` for GitHub's webhook ranges
   (`hooks` in `https://api.github.com/meta`), or GitHub gets a 403.

Data lives in the named volumes `pg-data` and `enowx-data` and survives
redeploys. The dashboard is also published on the host's `127.0.0.1:7777`
only (handy for an SSH tunnel).

### Behind Cloudflare

- Proxied record, SSL/TLS **Full (strict)**, a certificate for the name on the
  server (a Cloudflare Origin Certificate works).
- Free and Pro plans accept at most **100 MB per request**: `push` and the
  dashboard's Import send stores in 32 MiB parts (a part a proxy still refuses
  is split, a failed one retried), so any size fits.
- Cloudflare waits at most 100 s for an answer; imports, re-index and
  re-embedding run as jobs, and the live event stream sends a heartbeat every
  30 s.
- Keep Bot Fight Mode, challenges and Cloudflare Access away from `/api` and
  `/mcp` (MCP clients can't solve them); clients send
  `User-Agent: enowx-rag/<version>` for a WAF skip rule.
- A request blocked by Cloudflare or another proxy is reported as such (with
  the Ray ID) by `connect`, `push`, client mode and the dashboard, not as a bad
  token.

### Onboarding

Open the dashboard and sign in with the admin token. The setup wizard:

1. **Database** — detected from the environment (PostgreSQL and pgvector versions shown); nothing to type.
2. **Models** — Voyage embedding model and dimension, rerank model: price, context length, and a live test of your key (dimension returned, latency, tokens — or the error with a hint).
3. **Review & apply** — each component is tested for real; the server writes `config.yaml` and restarts itself.
4. **Connect** — the commands for your machines.

Settings given in `.env` are read-only in the dashboard and secrets are never
written to `config.yaml`.

### Connect your machines (client mode)

On every machine where you code:

```bash
curl -fsSL https://raw.githubusercontent.com/enowdev/enowx-rag/main/install.sh | sh
enowx-rag connect --remote https://rag.example.com --token <RAG_ADMIN_TOKEN>
```

`connect` checks the URL and token, then adds enowx-rag to every MCP client it
finds (Claude Code, Cursor, Claude Desktop, Cline, Windsurf, Codex, Zed,
Continue; `--client` picks, `--print` only prints), merging into existing
configs with a `.bak` copy. The entry runs the local binary with
`RAG_VECTOR_STORE=remote`, `RAG_REMOTE_URL` and `RAG_REMOTE_TOKEN`: the machine
reads and chunks files (honoring `.gitignore`, never sending secrets), the
server embeds, stores, reranks and records metrics. No Voyage key or database on
the machine.

Quit the Claude desktop app before connecting it. While it runs it writes its
own copy of `claude_desktop_config.json` back over the file, so `connect` skips
Claude Desktop then and prints the command to run once the app is closed.

### Bring existing memory (SQLite → pgvector)

```bash
enowx-rag push --remote https://rag.example.com --token <RAG_ADMIN_TOKEN>
```

`push` snapshots `~/.enowx-rag/rag.db` (safe while agents use it), uploads it
gzip-compressed in 32 MiB parts (fits behind Cloudflare's 100 MB request
limit; failed parts are retried), shows per project how many chunks keep their vectors, imports
them as a server job and registers each project's folder so auto-sync carries
on. Vectors from the server's model and dimension are copied as they are (the
voyage-4 family shares one embedding space, so those are reused across its
models); the rest are re-embedded from text, after showing the token estimate
(`--yes` skips the question). Options: `--project ID` (repeatable),
`--mode replace`, `--file other.db`, `--part-mb N`. The dashboard's **Import** page does the
same from the browser.

A server configured for pgvector that finds a SQLite store next to its config
imports it on start and renames it `rag.db.imported-<time>`
(`RAG_AUTO_IMPORT_SQLITE=false` turns that off).

### Security

- The admin token reads and deletes every project: keep it secret.
- Use HTTPS (`ENOWX_DOMAIN`) on the internet; over plain HTTP the token is sent in clear text.
- Docker publishes ports past ufw/firewalld; without the HTTPS profile, restrict the port at your provider's firewall.

---

## Two modes of operation

### MCP stdio mode (default)

Run without `--serve` to use the MCP stdio transport. This is the mode you configure in your AI coding tool (Claude Code, Cursor, Cline, etc.):

```bash
./enowx-rag
```

MCP tools available: `rag_create_project`, `rag_delete_project`, `rag_index`, `rag_index_project`, `rag_semantic_search`, `rag_retrieve_context`, `rag_list_projects`, `rag_project_exists`, `rag_list_points`, `rag_delete_points`, `rag_stats`. Logs go to stderr (stdout is the MCP protocol stream).

### HTTP serve mode

Run with `--serve` to start the HTTP API + embedded UI:

```bash
# Default port 7777
./enowx-rag --serve

# Custom port
./enowx-rag --serve --addr :8080

# With admin token auth (protects /api/* endpoints)
RAG_ADMIN_TOKEN=your-secret ./enowx-rag --serve
```

| Flag | Default | Description |
| --- | --- | --- |
| `--serve` | `false` | Run as HTTP server instead of stdio MCP |
| `--addr` | `:7777` | HTTP listen address (only used with `--serve`) |

---

## REST API endpoints

When running in `--serve` mode, the following REST endpoints are available:

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/api/health` | Liveness, version, boot id (always open) |
| `GET` | `/api/server` | Store, embedder, reranker, auto-sync, stale chunks, running jobs |
| `GET` | `/api/models` | Voyage embedding and rerank catalog (dimensions, price, context) |
| `POST` | `/api/models/test` | Test a Voyage model or reranker with a key 🔒 |
| `POST` | `/api/server/restart` | Restart in place (re-reads config.yaml) 🔒 |
| `GET` | `/api/projects` | List all projects with chunk counts and last sync source |
| `GET` | `/api/projects/{id}` | Get project detail (404 if not found) |
| `POST` | `/api/projects/{id}` | Create a project |
| `DELETE` | `/api/projects/{id}` | Delete a project collection |
| `GET` | `/api/projects/{id}/points` | List chunks (supports `?source_file=`, `?offset=`, `?limit=`) |
| `POST` | `/api/projects/{id}/points/delete` | Delete chunks (`{"ids": [...]}`) |
| `DELETE` | `/api/projects/{id}/points/{pointId}` | Delete a single chunk |
| `POST` | `/api/projects/{id}/reindex` | Re-index as a job, no path needed (`{"mode": "auto"\|"files"\|"reembed", "directory"?}`) |
| `POST` | `/api/projects/{id}/documents` | Index documents (client mode, scripts) |
| `POST` | `/api/projects/{id}/sources` | A machine reports a sync (client mode) |
| `POST` | `/api/search` | Search (body: `{"project_id", "query", "k", "recall", "hybrid", "rerank", "compress"}`) |
| `GET` | `/api/stats` | Aggregate stats (total projects, chunks, embed model) |
| `GET` | `/api/metrics` | Query metrics: latency (avg/p50/p95), token usage, backend, `persistent` flag |
| `GET` | `/api/activity` | Recent events (syncs, imports, jobs, restarts), persisted |
| `GET` | `/api/events` | SSE stream of the same events, live |
| `GET` | `/api/jobs`, `/api/jobs/{id}` | Background jobs (re-index, import, startup) |
| `POST` | `/api/import/upload` | Upload a SQLite store (raw or gzip); returns the import plan and cost |
| `POST` | `/api/import/parts` | Start an upload in parts (for proxies that cap request size, e.g. Cloudflare's 100 MB) |
| `PUT` | `/api/import/parts/{id}?offset=N` | Next part from byte N → `{received}` (409 `{received}` on a wrong offset) |
| `POST` | `/api/import/parts/{id}/finish?size=N` | Read the upload; same answer as `/api/import/upload` |
| `POST` | `/api/import/start` | Start the import (`{"upload_id", "projects"?, "mode"?: "merge"\|"replace"}`) |
| `GET` | `/api/setup/status` | Configured? Missing settings, env-locked keys (always open) |
| `GET` | `/api/setup/detect` | What the server finds on its own (database, key, old SQLite store) 🔒 |
| `POST` | `/api/setup/test` | Test store, embedder and reranker 🔒 |
| `POST` | `/api/setup/apply` | Save `config.yaml`, optionally restart 🔒 |
| `GET` / `POST` | `/api/setup/config` | Current config (secrets masked) / change settings with re-embed warnings 🔒 |
| `GET` | `/api/setup/mcp-snippet` | Config to paste into an MCP client (`?client_id=&mode=client\|remote`) |
| `POST` | `/api/migrate` | Move/re-embed a project into another store (API only) 🔒 |
| `GET` | `/api/docs`, `/api/docs/{section}` | The documentation shown in the dashboard, as markdown |

With an admin token set, every endpoint except `/api/health` and
`/api/setup/status` needs `Authorization: Bearer <token>`. 🔒 endpoints also
need it when no token is set, unless called from localhost. Errors are JSON
`{"error", "hint"?}`.

**Agent-driven setup.** The dashboard's Docs → Agent setup page has a prompt to
paste into your coding agent: it reads `GET /api/docs/setup` and installs the
binary, runs `enowx-rag connect`, adds the AGENTS.md block and indexes the
project, skipping what's already there.

**Query metrics** are recorded for every search and exposed at `/api/metrics`:
latency percentiles, Voyage token usage (embed + rerank), and — for hybrid
searches on pgvector — the dense/lexical retrieval breakdown. Metrics are
persisted durably to a local SQLite file (`~/.enowx-rag/metrics.db`, pure-Go,
no external service), so they survive restarts on any backend. If the file can't
be opened, metrics fall back to in-memory (`"persistent": false`).

Example:

```bash
curl http://localhost:7777/api/projects
curl -X POST http://localhost:7777/api/search \
  -H 'Content-Type: application/json' \
  -d '{"project_id": "my-project", "query": "how does auth work", "k": 5, "hybrid": true, "rerank": true}'
```

---

## Optional admin token auth

When the `RAG_ADMIN_TOKEN` environment variable is set, all `/api/*` endpoints require an `Authorization: Bearer <token>` header. The SPA and static assets are always served without auth.

```bash
# Enable auth
export RAG_ADMIN_TOKEN=my-secret-token
./enowx-rag --serve

# API requests must include the token
curl -H "Authorization: Bearer my-secret-token" http://localhost:7777/api/projects
```

When `RAG_ADMIN_TOKEN` is **not set**, no authentication is required (default behavior). This makes it easy to run locally without auth and enable it only when exposing the server to the internet.

---

## Docker all-in-one

For a server, prefer [`docker-compose.vps.yml`](#deploy-on-a-vps-postgresql--pgvector) (PostgreSQL + pgvector, required token, optional HTTPS). The `docker-compose.all-in-one.yml` file starts the enowx-rag server (API + UI) alongside Qdrant in a single command:

```bash
export RAG_VOYAGE_API_KEY=your-voyage-api-key
docker compose -f docker-compose.all-in-one.yml up -d
```

- enowx-rag container serves API + SPA on port **7777**
- Qdrant starts automatically on port 6333
- PostgreSQL (pgvector) and TEI are available as commented-out services in the compose file
- Set `RAG_ADMIN_TOKEN` in the compose environment to enable auth
- Settings saved in the dashboard persist in the `enowx-data` volume

To use pgvector instead of Qdrant, uncomment the `postgres` service and switch `RAG_VECTOR_STORE` to `pgvector` in the compose file.

---

## Quick setup for AI agents (copy-paste this to your AI agent)

There are two setup paths. Pick the one that matches your situation:

- **Option A — Full setup from scratch:** you don't have enowx-rag installed yet (clone, build, choose embedder, install the MCP server in your tools). Use this the first time.
- **Option B — Onboard a new project:** the enowx-rag MCP server is already installed and running in your tools, and you just want to wire a *new* project into RAG memory (create its collection, add AGENTS.md/CLAUDE.md, install the skill). Use this for every project after the first.

Copy the matching prompt below and paste it into Claude Code, Cline, Cursor, OpenCode, Codex, Factory Droid, Roo, Zed, Windsurf, or Continue.

### Option A — Full setup from scratch

```
I want to set up enowx-rag, a per-project RAG memory MCP server.

Read the setup guide at https://raw.githubusercontent.com/enowdev/enowx-rag/main/README.md and the skill at https://raw.githubusercontent.com/enowdev/enowx-rag/main/skill/enowx-rag.md

Then follow these steps:
1. Clone the repo to my machine: git clone https://github.com/enowdev/enowx-rag.git
2. Build the MCP server: cd enowx-rag/mcp-server && go build ./cmd/mcp-server
3. Ask me which embedding option I prefer:
   - **Voyage AI** (recommended): no local setup needed, ask for my Voyage API key (free at voyageai.com, 200M free tokens with voyage-4).
   - **Self-hosted TEI**: start Qdrant + TEI with docker compose. Ask for Qdrant URL if I already have one running.
4. Install the MCP server in every coding tool I use. Use the correct config format per tool (the README has all formats from official docs).
5. For the project I'm currently working in, create or merge AGENTS.md and CLAUDE.md with RAG memory instructions. Use project ID derived from the folder name. Call rag_create_project to create the collection.
6. Do not replace existing AGENTS.md or CLAUDE.md. Append the RAG section with a --- separator.
```

### Option B — Onboard a new project (enowx-rag already installed)

Use this when the enowx-rag MCP server is already installed and configured in your coding tool. It skips clone/build/backend and only wires the current project into RAG memory.

```
I already have the enowx-rag MCP server installed and running. I want to onboard THIS project into RAG memory.

Read the skill at https://raw.githubusercontent.com/enowdev/enowx-rag/main/skill/enowx-rag.md for the AGENTS.md/CLAUDE.md templates.

Then follow these steps:
1. Confirm the enowx-rag MCP tools are available (e.g. rag_create_project, rag_retrieve_context). If they are NOT available, stop and tell me to run the full setup (Option A) first.
2. Derive a PROJECT_ID from this project's folder name (or ask me).
3. Call rag_create_project with that PROJECT_ID to create its collection (safe to call if it already exists).
4. Create or merge AGENTS.md and CLAUDE.md in this project root using the skill's templates, with PROJECT_ID filled in. Do NOT replace existing files — append the RAG section below existing content, separated by a --- divider.
5. Install the enowx-rag skill for this project. Prefer a project-local skill directory used by my tool (e.g. .agents/skills/enowx-rag/, .factory/skills/enowx-rag/, or the tool's skill folder) — do not assume ~/.factory. Ask me if you are unsure which directory my tool uses.
6. Call rag_index_project on this project directory to seed the collection with the current files.
```

---

## What agents should do

1. Install the Factory Droid skill.
2. Build the Go MCP server.
3. Start the RAG backend (if local mode).
4. Install the MCP server in every coding tool the user uses.
5. Generate `AGENTS.md` and `CLAUDE.md` in the target project so all agents automatically use the RAG memory.

---

## 1. Install the Factory Droid skill

Copy the skill file into the Droid skill directory:

```bash
mkdir -p ~/.factory/skills/enowx-rag
cp skill/enowx-rag.md ~/.factory/skills/enowx-rag/skill.md
```

If the skill must be reloaded, restart the Droid session or refresh skills.

---

## 2. Build the MCP server

```bash
cd mcp-server
go build ./cmd/mcp-server
```

This produces the binary `mcp-server/mcp-server` inside your clone; use its absolute path as the MCP `command` below.

---

## 3. Start the RAG backend (local mode)

If the user does not already have a Qdrant + TEI backend, start the local one:

```bash
cd mcp-server
docker compose up -d qdrant tei-embedding
```

Verify:

```bash
curl -f http://localhost:6333/healthz
curl -f http://localhost:8081/health
```

If using an existing backend, skip this step and set the correct URLs in the tool config below.

---

## 4. Install the MCP server in every coding tool

Each tool has a different config format and file location. Use the correct one per tool.

### Quick reference table

| Tool | Format | Key | Config file |
| --- | --- | --- | --- |
| Claude Code | JSON | `mcpServers` | `~/.claude.json` or `.mcp.json` in project root |
| Claude Desktop | JSON | `mcpServers` | `~/Library/Application Support/Claude/claude_desktop_config.json` |
| Cline | JSON | `mcpServers` | `~/.cline/mcp.json` or IDE MCP settings |
| Cursor | JSON | `mcpServers` | `~/.cursor/mcp.json` or `.cursor/mcp.json` in project root |
| OpenCode | JSON | `mcp` | `~/.opencode/settings.json` or `opencode.json` in project root |
| Codex (OpenAI) | TOML | `[mcp_servers.*]` | `~/.codex/config.toml` |
| Factory Droid | CLI | — | `droid mcp add enowx-rag /path/to/mcp-server` |
| Roo Code | JSON | `mcpServers` | global `mcp_settings.json` or `.roo/mcp.json` in project root |
| Zed | JSON | `context_servers` | `~/.config/zed/settings.json` |
| Windsurf | JSON | `mcpServers` | `~/.codeium/windsurf/mcp_config.json` |
| Continue | YAML | `mcpServers` (list) | `~/.continue/config.yaml` |

### Claude Code

**CLI (Voyage AI):**
```bash
claude mcp add --transport stdio enowx-rag \
  --env RAG_VECTOR_STORE=qdrant \
  --env RAG_QDRANT_URL=http://localhost:6333 \
  --env RAG_VOYAGE_API_KEY=your-voyage-api-key \
  -- /path/to/enowx-rag/mcp-server/mcp-server
```

**CLI (self-hosted TEI):**
```bash
claude mcp add --transport stdio enowx-rag \
  --env RAG_VECTOR_STORE=qdrant \
  --env RAG_QDRANT_URL=http://localhost:6333 \
  --env RAG_TEI_URL=http://localhost:8081 \
  -- /path/to/enowx-rag/mcp-server/mcp-server
```

**JSON (`~/.claude.json` or `.mcp.json`) — Voyage AI:**
```json
{
  "mcpServers": {
    "enowx-rag": {
      "command": "/path/to/enowx-rag/mcp-server/mcp-server",
      "env": {
        "RAG_VECTOR_STORE": "qdrant",
        "RAG_QDRANT_URL": "http://localhost:6333",
        "RAG_VOYAGE_API_KEY": "your-voyage-api-key",
        "RAG_VOYAGE_MODEL": "voyage-4"
      }
    }
  }
}
```

**JSON — self-hosted TEI:**
```json
{
  "mcpServers": {
    "enowx-rag": {
      "command": "/path/to/enowx-rag/mcp-server/mcp-server",
      "env": {
        "RAG_VECTOR_STORE": "qdrant",
        "RAG_QDRANT_URL": "http://localhost:6333",
        "RAG_TEI_URL": "http://localhost:8081"
      }
    }
  }
}
```

All examples below use Voyage AI (recommended). Replace `RAG_VOYAGE_API_KEY` with `RAG_EMBEDDER=tei` + `RAG_TEI_URL` if using self-hosted TEI.

### Claude Desktop

**File:** `~/Library/Application Support/Claude/claude_desktop_config.json`

```json
{
  "mcpServers": {
    "enowx-rag": {
      "command": "/path/to/enowx-rag/mcp-server/mcp-server",
      "env": {
        "RAG_VECTOR_STORE": "qdrant",
        "RAG_QDRANT_URL": "http://localhost:6333",
        "RAG_VOYAGE_API_KEY": "your-voyage-api-key",
        "RAG_VOYAGE_MODEL": "voyage-4"
      }
    }
  }
}
```

### Cline

**File:** `~/.cline/mcp.json` (CLI) or open Cline panel > MCP Servers > Configure (IDE)

```json
{
  "mcpServers": {
    "enowx-rag": {
      "command": "/path/to/enowx-rag/mcp-server/mcp-server",
      "env": {
        "RAG_VECTOR_STORE": "qdrant",
        "RAG_QDRANT_URL": "http://localhost:6333",
        "RAG_VOYAGE_API_KEY": "your-voyage-api-key",
        "RAG_VOYAGE_MODEL": "voyage-4"
      },
      "disabled": false,
      "autoApprove": []
    }
  }
}
```

### Cursor

**File:** `~/.cursor/mcp.json` (global) or `.cursor/mcp.json` (project)

```json
{
  "mcpServers": {
    "enowx-rag": {
      "type": "stdio",
      "command": "/path/to/enowx-rag/mcp-server/mcp-server",
      "env": {
        "RAG_VECTOR_STORE": "qdrant",
        "RAG_QDRANT_URL": "http://localhost:6333",
        "RAG_VOYAGE_API_KEY": "your-voyage-api-key",
        "RAG_VOYAGE_MODEL": "voyage-4"
      }
    }
  }
}
```

### OpenCode

**File:** `~/.opencode/settings.json` or `opencode.json` in project root

OpenCode uses `mcp` key (not `mcpServers`), `command` as array, and `environment` (not `env`).

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "enowx-rag": {
      "type": "local",
      "command": ["/path/to/enowx-rag/mcp-server/mcp-server"],
      "enabled": true,
      "environment": {
        "RAG_VECTOR_STORE": "qdrant",
        "RAG_QDRANT_URL": "http://localhost:6333",
        "RAG_VOYAGE_API_KEY": "your-voyage-api-key",
        "RAG_VOYAGE_MODEL": "voyage-4"
      }
    }
  }
}
```

### Codex (OpenAI)

**File:** `~/.codex/config.toml` (TOML, not JSON)

**CLI:**
```bash
codex mcp add enowx-rag \
  --env RAG_VECTOR_STORE=qdrant \
  --env RAG_QDRANT_URL=http://localhost:6333 \
  --env RAG_VOYAGE_API_KEY=your-voyage-api-key \
  -- /path/to/enowx-rag/mcp-server/mcp-server
```

**TOML:**
```toml
[mcp_servers.enowx-rag]
command = "/path/to/enowx-rag/mcp-server/mcp-server"

[mcp_servers.enowx-rag.env]
RAG_VECTOR_STORE = "qdrant"
RAG_QDRANT_URL = "http://localhost:6333"
RAG_VOYAGE_API_KEY = "your-voyage-api-key"
RAG_VOYAGE_MODEL = "voyage-4"
```

### Factory Droid

```bash
droid mcp add enowx-rag /path/to/enowx-rag/mcp-server/mcp-server
```

### Roo Code

**File:** global `mcp_settings.json` (via Roo Code > MCP > Edit Global MCP) or `.roo/mcp.json` (project)

```json
{
  "mcpServers": {
    "enowx-rag": {
      "command": "/path/to/enowx-rag/mcp-server/mcp-server",
      "env": {
        "RAG_VECTOR_STORE": "qdrant",
        "RAG_QDRANT_URL": "http://localhost:6333",
        "RAG_VOYAGE_API_KEY": "your-voyage-api-key",
        "RAG_VOYAGE_MODEL": "voyage-4"
      },
      "alwaysAllow": [],
      "disabled": false
    }
  }
}
```

### Zed

**File:** `~/.config/zed/settings.json`

Zed uses `context_servers` key (not `mcpServers`). Can also be added via UI: Command Palette > `agent: add context server`.

```json
{
  "context_servers": {
    "enowx-rag": {
      "command": "/path/to/enowx-rag/mcp-server/mcp-server",
      "args": [],
      "env": {
        "RAG_VECTOR_STORE": "qdrant",
        "RAG_QDRANT_URL": "http://localhost:6333",
        "RAG_VOYAGE_API_KEY": "your-voyage-api-key",
        "RAG_VOYAGE_MODEL": "voyage-4"
      }
    }
  }
}
```

### Windsurf

**File:** `~/.codeium/windsurf/mcp_config.json`

Can also be added via MCP Marketplace UI in Windsurf.

```json
{
  "mcpServers": {
    "enowx-rag": {
      "command": "/path/to/enowx-rag/mcp-server/mcp-server",
      "env": {
        "RAG_VECTOR_STORE": "qdrant",
        "RAG_QDRANT_URL": "http://localhost:6333",
        "RAG_VOYAGE_API_KEY": "your-voyage-api-key",
        "RAG_VOYAGE_MODEL": "voyage-4"
      }
    }
  }
}
```

### Continue

**File:** `~/.continue/config.yaml` (YAML, not JSON)

Continue uses `mcpServers` as a list of objects with `name`, `command`, `env`.

```yaml
mcpServers:
  - name: enowx-rag
    command: /path/to/enowx-rag/mcp-server/mcp-server
    env:
      RAG_VECTOR_STORE: qdrant
      RAG_QDRANT_URL: http://localhost:6333
      RAG_VOYAGE_API_KEY: your-voyage-api-key
      RAG_VOYAGE_MODEL: voyage-4
```

Replace `/path/to/enowx-rag/mcp-server/mcp-server` with the actual absolute path to the built binary.

---

## 5. Enable RAG memory for the target project

### Per-project collection

Each project gets its own isolated collection in the vector store: `project_<PROJECT_ID>`. Call `rag_create_project` with the project ID to create it. Multiple projects share the same MCP server and backend, but each has isolated memory.

### AGENTS.md and CLAUDE.md

Create or update these two files in the root of the target project. Replace `PROJECT_ID` with the actual project name or slug.

**Important: merge, do not replace.** If the file already exists, append the RAG section below existing content separated by `---`. Use the templates in `skill/templates/AGENTS.md` and `skill/templates/CLAUDE.md`.

### `AGENTS.md`

```markdown
# Project agent instructions

This project uses the `enowx-rag` MCP server for per-project memory.

## Before coding

1. Call `rag_retrieve_context` with the project ID `PROJECT_ID` and the user's query.
2. Read the returned context. If relevant, use it to shape your answer or plan.

## After coding

1. Summarize what you changed.
2. Call `rag_index` with useful new facts, design decisions, gotchas, or patterns under project ID `PROJECT_ID`.
3. No need to re-index files after each change: once indexed with `rag_index_project`, the directory is re-synced automatically before searches (`RAG_AUTO_SYNC_INTERVAL`, default 60s). Call `rag_index_project` again only to force an immediate sync.

Keep chunks concise (one idea per chunk). Use metadata tags like `type:architecture`, `type:decision`, `type:api`, `type:bugfix`, `type:howto`, or `type:snippet`.
```

### `CLAUDE.md`

```markdown
# Claude instructions for this project

## RAG memory workflow

### Before making significant changes

1. Call `rag_retrieve_context` with the project ID `PROJECT_ID` and the user's query.
2. Read the returned context. If it is empty or irrelevant, continue as normal.

### After completing work

1. Summarize what you changed and why.
2. Call `rag_index` with useful new facts, design decisions, gotchas, or patterns under project ID `PROJECT_ID`.

Use project ID: `PROJECT_ID`

Each project has its own collection: `project_PROJECT_ID`. Do not mix project memories.
```

---

## Layout

```
enowx-rag/
├── AGENTS.md                          # Universal agent install guide (this repo)
├── CLAUDE.md                          # Claude-family quick reference (this repo)
├── README.md                          # This file
├── Makefile                           # Build pipeline: make web, make build, make dev-*
├── docker-compose.all-in-one.yml      # All-in-one: enowx-rag + Qdrant (optional PG, TEI)
├── docker-compose.vps.yml             # VPS: enowx-rag + PostgreSQL/pgvector (+ Caddy HTTPS)
├── deploy/                            # install-vps.sh, vps.env.example, systemd unit
├── mcp-server/                        # Go server (MCP stdio + HTTP serve modes)
│   ├── cmd/mcp-server/main.go         # Entry point: stdio, --serve, startup jobs, restart
│   ├── cmd/mcp-server/client_cmds.go  # connect / push subcommands (client mode)
│   ├── pkg/rag                        # Provider interface + Qdrant, Chroma, pgvector, Voyage, TEI
│   ├── pkg/core                       # Service layer (shared by MCP + HTTP)
│   ├── pkg/config                     # Config file (~/.enowx-rag/config.yaml)
│   ├── pkg/httpapi                    # Chi router, REST handlers, SSE, admin auth
│   ├── pkg/indexer                    # File indexer with content hashing
│   ├── web/                           # React SPA (Vite + React + TypeScript + Tailwind)
│   │   ├── dist/                      # Build output (embedded via go:embed)
│   │   └── embed.go                   # //go:embed all:dist
│   ├── Dockerfile                     # Multi-stage: SPA (or prebuilt) + static Go binary, non-root
│   └── docker-compose.yml             # Backend-only compose (Qdrant + TEI)
└── skill/                             # Factory Droid skill
    ├── enowx-rag.md
    └── templates/
        └── AGENTS.md
```

## Providers

| Vector store | Embedder | Status |
| --- | --- | --- |
| Qdrant | TEI (self-hosted) | Supported |
| Qdrant | Voyage AI | Supported |
| pgvector | TEI (self-hosted) | Supported |
| pgvector | Voyage AI | Supported — recommended (hybrid search + retrieval breakdown) |
| Chroma | TEI / Voyage AI | Experimental — see note below |

**Feature support by backend:**

| Feature | Qdrant | pgvector | Chroma |
| --- | :---: | :---: | :---: |
| Index / semantic search / delete | ✅ | ✅ | ⚠️ |
| Project list + stats (dashboard) | ✅ | ✅ | ⚠️ |
| Hybrid search (dense + lexical RRF) | — | ✅ | — |
| Retrieval breakdown (dense/lexical) | — | ✅ | — |
| Token metrics (Voyage) | ✅ | ✅ | ✅ |

> **Chroma is experimental.** The provider targets Chroma's legacy `/api/v1`
> REST API and has only been tested against mocks, not a live server. Chroma
> ≥ 0.6 moved to `/api/v2` (tenant/database paths, UUID-addressed collections),
> so the current provider may not work against modern Chroma. Use **Qdrant** or
> **pgvector** for a supported setup. Contributions to port Chroma to `/api/v2`
> are welcome.

## Migration

**Local memory to a server:** `enowx-rag push` or the dashboard's **Import**
page (see [Deploy on a VPS](#deploy-on-a-vps-postgresql--pgvector)).

**Changing the model or dimension of the store you run on needs no migration.**
Change it in **Settings** (or `config.yaml`) and restart. Settings tells you
first whether chunks must be re-embedded (none within the voyage-4 family at
the same dimension), how many, and the estimated tokens and cost. After the
restart a background job re-embeds them from their stored text; pgvector
resizes its vector column first, only after a test embedding with the new
settings succeeded, and searches skip chunks that aren't re-embedded yet.

**Between stores** (API only): `POST /api/migrate` re-embeds a project's stored
text into another store with credentials you pass — e.g. Qdrant → pgvector, or
an import from **Qdrant Cloud** (verified); **Pinecone**, **Weaviate** and
**Chroma Cloud** connectors are *experimental* (mock-tested only). It runs as a
job with progress events and never deletes the source.

## Embedding options

### Option A: Voyage AI (recommended, hosted)

No GPU required. Get a free API key at [voyageai.com](https://voyageai.com). `voyage-4` has 200M free tokens.

Set `RAG_EMBEDDER=voyage` (or just set `RAG_VOYAGE_API_KEY` — it auto-detects):

```json
"env": {
  "RAG_VECTOR_STORE": "qdrant",
  "RAG_QDRANT_URL": "http://localhost:6333",
  "RAG_EMBEDDER": "voyage",
  "RAG_VOYAGE_API_KEY": "your-voyage-api-key",
  "RAG_VOYAGE_MODEL": "voyage-4"
}
```

### Option B: OpenAI-compatible (any `/v1/embeddings` API)

Works with OpenAI, Together, Jina, Mistral, DeepInfra, LiteLLM, a local Ollama, and anything else that speaks the OpenAI embeddings protocol. Point `RAG_OPENAI_BASE_URL` at the provider and set the model. Leave the API key empty for a local/no-auth endpoint. `RAG_OPENAI_DIM` is optional (`0` = auto-detect; models like `text-embedding-3-*` honor it).

```json
"env": {
  "RAG_VECTOR_STORE": "qdrant",
  "RAG_QDRANT_URL": "http://localhost:6333",
  "RAG_EMBEDDER": "openai",
  "RAG_OPENAI_BASE_URL": "https://api.openai.com/v1",
  "RAG_OPENAI_API_KEY": "sk-...",
  "RAG_OPENAI_MODEL": "text-embedding-3-small"
}
```

Local Ollama example (no API key needed):

```json
"env": {
  "RAG_EMBEDDER": "openai",
  "RAG_OPENAI_BASE_URL": "http://localhost:11434/v1",
  "RAG_OPENAI_MODEL": "nomic-embed-text"
}
```

### Option C: TEI (self-hosted)

Runs a local Text Embeddings Inference container — serve **any** local embedding model (BGE, GTE, E5, nomic-embed, …); enowx-rag only needs its URL. Requires Docker and ~1 GB of RAM.

```bash
cd mcp-server && docker compose up -d qdrant tei-embedding
```

```json
"env": {
  "RAG_VECTOR_STORE": "qdrant",
  "RAG_QDRANT_URL": "http://localhost:6333",
  "RAG_EMBEDDER": "tei",
  "RAG_TEI_URL": "http://localhost:8081"
}
```

## Environment variables

All configuration is via environment variables (or config file at `~/.enowx-rag/config.yaml`). Priority: **env var > config file > default**.

| Variable | Default | Description |
| --- | --- | --- |
| `RAG_VECTOR_STORE` | `qdrant` | Vector store: `pgvector`, `sqlite` (built-in), `qdrant`, `chroma`, or `remote` (client mode). A server with no configuration and no Qdrant starts on `sqlite` so the setup wizard can run |
| `RAG_EMBEDDER` | `voyage` | Embedding provider: `voyage`, `openai` (any OpenAI-compatible API), or `tei`. Without a Voyage key the server still starts; indexing and search answer with an error saying to set it (no silent fallback to TEI) |
| `RAG_QDRANT_URL` | `http://localhost:6333` | Qdrant REST URL |
| `RAG_QDRANT_API_KEY` | *(empty)* | Optional Qdrant API key (for Qdrant Cloud) |
| `RAG_CHROMA_URL` | `http://localhost:8000` | Chroma REST URL |
| `RAG_PGVECTOR_DSN` | *(empty)* | PostgreSQL connection string (e.g., `postgresql://user@localhost:5432/dbname`). Required when `RAG_VECTOR_STORE=pgvector` |
| `RAG_SQLITE_PATH` | `~/.enowx-rag/rag.db` | File of the built-in SQLite store |
| `RAG_REMOTE_URL` | *(empty)* | Client mode: URL of the enowx-rag server (`RAG_VECTOR_STORE=remote`) |
| `RAG_REMOTE_TOKEN` | *(empty)* | Client mode: the server's admin token |
| `RAG_AUTO_IMPORT_SQLITE` | `true` | A pgvector server imports a SQLite store found next to its config once at startup, then renames the file |
| `RAG_TEI_URL` | `http://localhost:8081` | Text Embeddings Inference URL. Used when `RAG_EMBEDDER=tei` |
| `RAG_VOYAGE_API_KEY` | *(empty)* | Voyage AI API key (required when `RAG_EMBEDDER=voyage`). Get a free key at [voyageai.com](https://voyageai.com) (200M free tokens with voyage-4) |
| `RAG_VOYAGE_MODEL` | `voyage-4` | Voyage AI embedding model name (catalog with prices in Settings or `GET /api/models`) |
| `RAG_VOYAGE_BASE_URL` | `https://api.voyageai.com/v1` | Voyage-compatible API base (gateways, tests) |
| `RAG_OPENAI_BASE_URL` | `https://api.openai.com/v1` | OpenAI-compatible embeddings base URL (used when `RAG_EMBEDDER=openai`). Point at OpenAI, Together, Jina, a local Ollama (`http://localhost:11434/v1`), etc. |
| `RAG_OPENAI_API_KEY` | *(empty)* | API key for the OpenAI-compatible endpoint. Leave empty for local/no-auth endpoints |
| `RAG_OPENAI_MODEL` | *(empty)* | Embedding model name (required when `RAG_EMBEDDER=openai`), e.g. `text-embedding-3-small`, `nomic-embed-text` |
| `RAG_OPENAI_DIM` | `0` | Output dimension for the OpenAI embedder. `0` = auto-detect; models like `text-embedding-3-*` honor an explicit value |
| `RAG_VECTOR_DIM` | `1024` | Voyage embedding dimension: 256, 512, 1024 or 2048 (voyage-4 family, voyage-code-3/4) |
| `RAG_RERANKER_MODEL` | `rerank-3` | Voyage rerank model (`rerank-3`, `rerank-3-lite`, …); `off` disables reranking. Needs the Voyage key |
| `RAG_ADMIN_TOKEN` | *(empty)* | Admin token. When set, the dashboard, `/api/*` (except `/api/health`, `/api/setup/status`) and `/mcp` require `Authorization: Bearer <token>`. Required by `docker-compose.vps.yml` |
| `RAG_AUTO_SYNC_INTERVAL` | `60s` | Directories indexed with `rag_index_project` are re-synced (incrementally) before a search once their index is older than this. A duration (`90s`, `5m`), a number of seconds, or `off` to disable; a tiny value like `1s` syncs before practically every search. Also settable as `auto_sync_interval` in `~/.enowx-rag/config.yaml` (the env var wins). The directories are recorded in `~/.enowx-rag/project_dirs.json` |
| `RAG_CORS_ORIGIN` | *(empty)* | Optional CORS origin for the SSE event stream (`/api/events`). When set (e.g. `*` or `https://app.example.com`), it becomes the `Access-Control-Allow-Origin` header. When unset, no CORS header is sent and the stream stays same-origin only |

## Tools

- `rag_create_project` — create a project collection
- `rag_delete_project` — delete a project collection
- `rag_index` — index individual documents into a project
- `rag_index_project` — scan a directory and sync all code/text files: new and edited code is indexed, code removed from a file and removed files are purged. Honors `.gitignore`; skips node_modules, .git, vendor, dist, build and secrets. Run it once per directory: the directory is remembered and re-synced automatically before searches (see `RAG_AUTO_SYNC_INTERVAL`).
- `rag_semantic_search` — semantic search across project memory
- `rag_retrieve_context` — compact context string for LLM consumption

## Notes

- One binary: **MCP stdio** (default, no flags), **HTTP serve** (`--serve`), and **client mode** (stdio with `RAG_VECTOR_STORE=remote`). All use the same core service layer.
- The dashboard's restart (after saving settings) re-executes the binary in place (same PID); on Windows restart the process yourself.
- In stdio mode, logs go to stderr (stdout is the MCP protocol stream).
- In serve mode, the HTTP server provides a REST API, SSE event stream, and an embedded React dashboard UI.
- `make build` produces a single self-contained binary with the SPA embedded via `embed.FS`.
- Each project gets its own collection/index: `project_<project_id>`.
- Config priority: environment variable > config file (`~/.enowx-rag/config.yaml`) > built-in defaults.
- Optional admin token auth (`RAG_ADMIN_TOKEN`) protects `/api/*` endpoints when set. No auth when unset.

