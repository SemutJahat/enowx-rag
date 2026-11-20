# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **Uploads in parts**: `push` and the dashboard's Import send stores in 32 MiB
  parts (`POST /api/import/parts`, `PUT …/{id}?offset=N`, `POST …/finish`), so
  stores larger than a proxy's request limit (Cloudflare Free/Pro: 100 MB) go
  through. A part a proxy refuses as too large is split, a failed part is
  retried, and a lost answer is resumed from what the server holds
  (`push --part-mb N` sets the size).
- **Proxy-aware errors**: answers from Cloudflare or another proxy in front of
  the server (WAF 403, 413, 52x, a login page) are explained as such, with the
  Ray ID, in `connect`, `push`, client mode and the dashboard, instead of
  "the token was rejected". Clients send `User-Agent: enowx-rag/<version>`.
- **Dokploy and Cloudflare docs** (README and the in-app Deploy page).
- **VPS deployment**: `deploy/install-vps.sh` (Docker, generated secrets,
  optional HTTPS through Caddy, health wait), `docker-compose.vps.yml`
  (PostgreSQL 17 + pgvector, not published; token required),
  `deploy/vps.env.example` and a systemd unit. The Docker image is static
  (CGO off), runs as a non-root user with `/data` as its volume, has a health
  check on `/api/health`, and can embed the committed dashboard
  (`WEB=prebuilt`) on small machines.
- **Client mode** (`vector_store: remote`, `RAG_REMOTE_URL`,
  `RAG_REMOTE_TOKEN`): the machine scans and chunks, the server embeds,
  stores, reranks and records metrics; syncs are reported to the server, so
  the dashboard shows which machine and folder each project came from.
  `enowx-rag connect --remote URL --token T` configures every MCP client found
  (merge + `.bak`; `--client`, `--print`).
- **Import local memory**: `enowx-rag push` (consistent snapshot, gzip upload
  with progress, per-project plan, job, folder registration) and the
  dashboard's **Import** page (`POST /api/import/upload`, `/api/import/start`).
  Vectors that fit the server's model and dimension are reused (the voyage-4
  family shares one space); others are re-embedded after a token estimate.
- **Automatic SQLite → pgvector**: a pgvector server imports a SQLite store
  found next to its config once at startup and renames it
  `rag.db.imported-<time>` (`RAG_AUTO_IMPORT_SQLITE=false` turns it off).
- **Onboarding wizard rebuilt**: Welcome → Database (detected from the
  environment, versions shown) → Models → Review & apply (real tests of store,
  embedder and reranker) → Connect → Done. Apply saves `config.yaml` and the
  server restarts itself in place (same PID; picks up an upgraded binary).
- **Voyage settings that explain themselves**: model catalog
  (`GET /api/models`) with dimensions, price, context length and family;
  live tests (`POST /api/models/test`) that report the returned dimension,
  latency and tokens, or the error with a hint; Settings warns before saving
  whether chunks must be re-embedded, how many, and the cost.
- **Re-index without a path**: `POST /api/projects/{id}/reindex` runs as a
  job; `auto` re-syncs the known folder when the server can read it and
  otherwise re-embeds the stored chunks.
- Background jobs (`GET /api/jobs`), a persisted activity log
  (`GET /api/activity`: syncs, imports, jobs, restarts), `GET /api/server`,
  open `GET /api/health`, `GET /api/setup/detect`, JSON errors with a `hint`.
- `RAG_VOYAGE_BASE_URL` for Voyage-compatible gateways and tests.
- In-app docs: *Deploy on a VPS* and *Connect machines & import*; links and
  snippets honor `X-Forwarded-Proto` / `X-Forwarded-Host` behind a proxy.
- **Auto-sync before search**: a directory indexed once with
  `rag_index_project` is recorded (`~/.enowx-rag/project_dirs.json`) and
  re-synced incrementally before a search when its index is older than
  `RAG_AUTO_SYNC_INTERVAL` or `auto_sync_interval` in config.yaml (default
  `60s`, `off` disables). Agents no longer
  need to call `rag_index_project` after every task; the agent templates say
  so. A recorded directory that no longer exists is skipped, not synced, and
  a project deleted elsewhere is not recreated. The registry file is shared
  safely by several processes (re-read before every read and change).
- **Big classes split at their methods**: a declaration too long for one
  chunk is cut at its members (Python, Rust, Ruby, Java/C#, Kotlin, Swift,
  PHP, JS/TS class methods; decorators and annotations kept with their
  method, closures kept inside it), each chunk's symbol `Class.method`,
  before falling back to blank lines.
- `rag.PayloadUpdater` (implemented by the SQLite store): rewrites a point's
  content and metadata, keeping its vector.
- **Built-in store (`vector_store: sqlite`)**: one SQLite file (pure Go, no
  cgo; `sqlite_path`, default `~/.enowx-rag/rag.db`, or `RAG_SQLITE_PATH`),
  vectors searched by cosine, text in FTS5 for hybrid search (RRF), so
  enowx-rag runs with nothing else to install. Fits projects of up to a few
  hundred thousand chunks.
- **Chunks cut by code structure**: at top-level declarations (Go, Python,
  JS/TS, Rust, Java/Kotlin/Swift/C#/PHP, Ruby, Markdown headings, SQL,
  protobuf), the comments above each kept with it, small ones packed
  together; each chunk carries `start_line`, `end_line` and `symbol`, and
  starts `File: path:start-end (symbol)` (`chunk_version` v3, so existing
  indexes re-embed once).
- `Indexer.Files` lets a program embedding enowx-rag pass the files to index.
- **Migration into the built-in SQLite store**: the Migration page and
  `POST /api/migrate` take `vector_store: sqlite` with an optional
  `sqlite_path` (empty: the running store's file, `~/` expanded). Migrating a
  project into itself in the running file is refused.
- `rag.EmbedIdentifier` (all built-in stores): the embedding model and vector
  size a store stamps on the points it writes now.

### Fixed
- **Import into pgvector stopped at text that is not UTF-8**: SQLite keeps any
  bytes, so a file indexed in Windows-1252 (a test fixture, say) or holding a
  NUL byte made PostgreSQL refuse the batch (`invalid byte sequence for
  encoding "UTF8"`, SQLSTATE 22021) and the import job ended there. The
  pgvector store now replaces invalid bytes with U+FFFD and drops NULs in
  content, metadata and IDs, when indexing and importing alike.
- **Changing the embedding model left the old vectors in place**: a re-index
  skipped unchanged chunks whatever model embedded them, so after a model
  change searches compared the query with another model's vectors (wrong
  results, no error) or, at another size, skipped them (nothing found), and
  memory notes were never re-embedded at all. The first search or sync of a
  project now re-embeds, from their stored text, the points another model or
  size wrote, chunks and notes alike, once per process
  (`points_reembedded` in the `rag_index_project` result). The size an
  embedder reports is checked on a real vector first, so a wrong guess (TEI
  without `/info`) doesn't re-embed a project. A store with a fixed size
  (Qdrant collection, pgvector table) reports an error pointing to Migration.
- **SQLite: searches missed what other sessions wrote**: each process (every
  Claude session runs its own server over the shared `rag.db`) read a
  project's vectors once and dropped them only after its own writes. Points
  another process indexed were never found by vector search there, and once
  another process deleted a point, every search there failed with
  `sql: no rows in result set` until a restart. The store now watches
  SQLite's `data_version` on a connection it holds open and reads the
  vectors again after anyone commits; a point deleted mid-search is left out
  instead of failing the search.
- **Migrated points kept the old model's label**: every store let a
  document's metadata override the `embed_model`/`embed_dim` it stamps, so a
  migration wrote the source model's name on the destination's vectors.
  Stores now always stamp the embedder they used.
- **Stale chunks after editing a file**: a sync deleted chunks only when
  their file was gone, so code removed from a file that still exists stayed
  in the index and was served as current. A sync now deletes every chunk of
  the directory it did not produce again (also replacing chunks written by
  older chunk layouts).
- **Re-embedding everything below an edit**: chunk IDs were their position
  in the file (`#chunkN`), so adding a function shifted the IDs of all the
  chunks after it. IDs are now derived from the chunk's content
  (`dir/file#<hash>`, `chunk_version` v4; existing indexes re-embed once);
  only small declarations are packed together, so an edit changes only the
  chunks around it; a chunk that only moved gets its line numbers refreshed
  without re-embedding where the store supports it (SQLite), re-indexed
  otherwise.
- `rag_index_project` on a directory that does not exist is an error instead
  of an empty scan that deleted everything indexed from it.
- A sync only ever deletes chunks under its own directory's ID prefix, even
  when the store returns other points from `ListPoints` (pgvector ignores the
  `source_dir` filter, so syncing one directory deleted another's chunks).
- Indexing a second directory with the same folder name as one already in
  the project (`/x/app`, `/y/app`) is refused instead of silently replacing
  its chunks.
- A file that exists but can't be read keeps its chunks instead of losing
  them to the stale cleanup.
- **Chroma**: points are written with `/upsert` instead of `/add`, which
  skipped IDs already stored, so edited chunks were never updated.

### Security
- Secrets are never indexed: `.env*`, keys and certificates, `credentials`
  and `*secret*` files by name, and any chunk holding a private key block or a
  cloud or API token.

### Removed
- The Playground, Migration and Setup pages (search tests and moves between
  stores stay available as `POST /api/search` and `POST /api/migrate`).
- `POST /api/setup/install-mcp`, `GET /api/setup/clients` and
  `POST /api/setup/write-agents-md`: they wrote files on the server's machine,
  which is wrong for a remote server. `enowx-rag connect` writes the client
  config on the machine that runs it.

### Changed
- **pgvector follows the embedding dimension**: after a model or dimension
  change, the vector column is resized only once a test embedding with the new
  settings succeeds; a startup job re-embeds the affected chunks and searches
  skip chunks without a vector meanwhile.
- **The dashboard is for hosting and monitoring**: Overview (health, store,
  models, projects with their sync source, jobs, activity), Chunks, Import,
  Connect, Settings, Docs.
- Settings set by environment variables are read-only in the dashboard;
  secrets are never written to `config.yaml`, other env values only when they
  match.
- Without a Voyage key the server starts; indexing and search fail with an
  error that says to set it. The silent fallback to TEI is gone.
- The default rerank model is `rerank-3` (`off` disables reranking).
- `CreateProject` emits `project_created` only for new projects; imports emit
  `project_imported`, machine syncs `sync_reported`.
- The Go module is `github.com/enowdev/enowx-rag/mcp-server`, as its folder
  says, so `go install …/mcp-server/cmd/mcp-server@latest` and importing its
  packages from another program work.
- A git repository is indexed from `git ls-files` (tracked and untracked,
  `.gitignore` honored) instead of a walk with a fixed ignore list.
- **One-line install + prebuilt binaries**: releases now ship cross-compiled,
  CGO-free binaries for macOS/Linux (amd64/arm64) and Windows (amd64) via
  GoReleaser (`.github/workflows/release.yml` on tag `v*`). Install without a
  toolchain through the `install.sh` script (`curl -fsSL …/install.sh | sh`),
  Homebrew (`brew install enowdev/tap/enowx-rag`), npm (`npm install -g
  enowx-rag`, a thin wrapper that fetches the native binary), `go install
  …/cmd/mcp-server@latest` (the dashboard is embedded from a committed build),
  or by downloading a release archive directly. Adds `enowx-rag version`
  (`--version`), with the version stamped at build time via ldflags.
- **Settings page** to manage API keys and the admin token from the dashboard:
  view keys masked (`GET /api/setup/config`), reveal full keys
  (`/config/reveal`, localhost or admin token), update a key
  (`POST /api/setup/config`, merged into config.yaml 0600), and generate an
  admin token (`POST /api/setup/gen-token`, shown once, saved to config). The
  admin token now takes effect from either `RAG_ADMIN_TOKEN` (precedence) or the
  saved config, read per-request so a generated token works without a restart.
- **More MCP tools** (6 → 11): `rag_list_projects` (discover projects + chunk
  counts), `rag_project_exists`, `rag_list_points` (inspect indexed chunks),
  `rag_delete_points` (remove stale chunks without a full re-index), and
  `rag_stats` (projects/chunks/embed-model/latency/tokens). Thin wrappers over
  existing core.Service methods; available over stdio and remote HTTP.
- **MCP over HTTP (remote daemon)**: `enowx-rag --serve` now also exposes the MCP
  server at `/mcp` (Streamable HTTP transport, stateless), so agents can use
  enowx-rag as a centralized remote daemon — e.g. on a VPS — instead of a local
  stdio process. It's gated by the same `RAG_ADMIN_TOKEN` bearer as `/api`.
  Connect a client with `{ "url": ".../mcp", "headers": { "Authorization":
  "Bearer <token>" } }`. Local stdio mode is unchanged.
- **Agent setup**: point an AI agent at `GET /api/docs/setup` and it configures
  enowx-rag for a project, idempotently. `GET /api/setup/probe` reports what's
  already installed (MCP per client, skill, AGENTS.md block) so finished steps
  are skipped; `POST /api/setup/write-agents-md` merges an enowx-rag section into
  the project's AGENTS.md via `<!-- enowx-rag:start/end -->` markers (create /
  append / update, preserving the user's own content). The Install step shows a
  short copy-paste prompt.
- **Migration** page + engine: re-embed a project's stored text into a new
  destination to change embedding model/dimension or move between vector stores
  (Qdrant/pgvector/Chroma). Raw vectors aren't copied (they're model-specific);
  the text — stored alongside every chunk — is re-embedded by the destination.
  `POST /api/migrate` runs asynchronously with live SSE progress; the UI shows a
  progress bar and offers to delete the source after success.
- **Cloud import**: pull from an external vector DB and re-embed. Qdrant Cloud is
  verified (reuses the tested Qdrant provider); Pinecone, Weaviate, and Chroma
  Cloud connectors are **experimental** (built from vendor docs, mock-tested
  only — not verified against a live account) and labelled as such in the UI.
- OpenAI-compatible embedder (`RAG_EMBEDDER=openai`): works with any
  `/v1/embeddings` API — OpenAI, Together, Jina, Mistral, a local Ollama,
  LiteLLM, etc. — via `RAG_OPENAI_BASE_URL` / `RAG_OPENAI_MODEL` /
  `RAG_OPENAI_API_KEY` / `RAG_OPENAI_DIM`. Surfaced in the wizard's Embedding
  step, which also clarifies that TEI serves any local model.
- Query metrics: latency (avg/p50/p95), Voyage token usage, and dense/lexical
  retrieval breakdown, exposed at `GET /api/metrics` and shown on the dashboard.
  Persisted durably to `~/.enowx-rag/metrics.db` (pure-Go SQLite, no cgo), so
  metrics survive restarts on any backend.
- `compress` search option: deterministic near-duplicate result dedup.
- `enowx-rag setup [--run]` CLI subcommand to generate/run the backend
  docker-compose (never over HTTP).
- Efficient project chunk counts (`points/count` / `COUNT(*)` / `/count`),
  making `/api/projects` and `/api/stats` fast on large Qdrant/pgvector.
- Qdrant & Chroma now implement project listing, so the dashboard works on all
  backends, not only pgvector.

### Changed
- MCP `rag_semantic_search` / `rag_retrieve_context` now accept and apply
  `hybrid`/`rerank`/`recall`/`compress` (hybrid & rerank default on).
- Dashboard shows only real, backend-sourced data — removed all mock/hardcoded
  metrics, dead controls, and the fake auto-setup simulation.

### Security
- `/api/setup/apply` and `/api/setup/test` require localhost or a valid admin
  token; `/api/setup/apply` no longer echoes the saved config (API key) back.
- Removed the deprecated `middleware.RealIP` (X-Forwarded-For spoofing risk).

### Notes
- The Chroma provider is **experimental** (legacy `/api/v1`, mock-tested only);
  use Qdrant or pgvector for a supported setup.

## [0.1.0] - 2026-07-12

First tagged release. A per-project RAG memory skill and MCP server for AI
coding agents, distributed as a single self-contained binary.

### Added

- **MCP server** (stdio) exposing per-project RAG tools: create/delete project,
  index, retrieve context, semantic search.
- **Vector store providers**: Qdrant, Chroma, and pgvector, behind a common
  `rag.Provider` interface (one project = one collection).
- **Embedding backends**: Voyage AI (with Matryoshka `output_dimension` and
  `input_type=query`/`document` split) and self-hosted TEI.
- **Reranking**: Voyage `rerank-2.5` integrated into search (retrieve → rerank →
  top-k), disable-able and with graceful fallback.
- **Hybrid search** on pgvector: dense + lexical full-text fused with
  Reciprocal Rank Fusion (RRF, k=60) over a GIN `tsvector` index.
- **HTTP + web UI** (`--serve`): chi-based REST API, Server-Sent Events activity
  stream, and an embedded React dashboard (Overview, Playground, Chunks) served
  from the single binary via `embed.FS`.
- **Onboarding wizard**: guided setup for vector store, embedder, and connection
  testing.
- **Incremental indexing**: `content_hash` / `chunk_version` metadata so
  unchanged chunks are skipped on re-index; `embed_model`/`embed_dim` recorded.
- **Auth**: optional `RAG_ADMIN_TOKEN` protecting `/api/*` with constant-time
  comparison.
- **Packaging**: Makefile build pipeline and an all-in-one Docker Compose.

### Security

- SSE event stream (`/api/events`) is same-origin only by default; cross-origin
  access must be explicitly enabled via `RAG_CORS_ORIGIN`.
- Reranked result sets are defensively truncated to `k` regardless of the
  rerank API response.

[Unreleased]: https://github.com/enowdev/enowx-rag/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/enowdev/enowx-rag/releases/tag/v0.1.0
