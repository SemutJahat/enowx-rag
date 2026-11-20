package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	_ PayloadUpdater = (*PGVectorProvider)(nil)
	_ VectorImporter = (*PGVectorProvider)(nil)
	_ VectorExporter = (*PGVectorProvider)(nil)
	_ StoreInfoer    = (*PGVectorProvider)(nil)
	_ EmbedStatser   = (*PGVectorProvider)(nil)
	_ FileCounter    = (*PGVectorProvider)(nil)
)

// UpdatePayload rewrites content and merges metadata of existing points,
// keeping their vectors (see PayloadUpdater): a moved but unchanged chunk is
// not embedded again.
func (p *PGVectorProvider) UpdatePayload(ctx context.Context, projectID string, docs []Document) error {
	if len(docs) == 0 {
		return nil
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := fmt.Sprintf(`UPDATE %s SET content = $3, metadata = COALESCE(metadata, '{}'::jsonb) || $4::jsonb
WHERE project_id = $1 AND id = $2`, p.table)
	for _, d := range docs {
		patch, _ := json.Marshal(pgMeta(d.Meta, 0))
		tag, err := tx.Exec(ctx, q, projectID, pgText(d.ID), pgText(d.Content), string(patch))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("update payload: point %q not found", d.ID)
		}
	}
	return tx.Commit(ctx)
}

// IndexVectors stores points with vectors embedded elsewhere (see
// VectorImporter), e.g. imported from a local SQLite store.
func (p *PGVectorProvider) IndexVectors(ctx context.Context, projectID string, docs []Document, vectors [][]float32) error {
	if len(docs) != len(vectors) {
		return fmt.Errorf("index vectors: %d documents but %d vectors", len(docs), len(vectors))
	}
	batch := &pgx.Batch{}
	for i, d := range docs {
		if len(vectors[i]) != p.dim {
			return fmt.Errorf("index vectors: point %q has %d dimensions, the table stores %d", d.ID, len(vectors[i]), p.dim)
		}
		id := pgText(d.ID)
		if id == "" {
			id = uuid.NewString()
		}
		meta := pgMeta(d.Meta, 1)
		meta["embed_dim"] = strconv.Itoa(len(vectors[i]))
		batch.Queue(fmt.Sprintf(`
INSERT INTO %s (id, project_id, content, metadata, embedding)
VALUES ($1, $2, $3, $4, $5::vector)
ON CONFLICT (project_id, id) DO UPDATE SET
	content = EXCLUDED.content, metadata = EXCLUDED.metadata, embedding = EXCLUDED.embedding
`, p.table), id, projectID, pgText(d.Content), meta, pgVectorLiteral(vectors[i]))
	}
	return p.pool.SendBatch(ctx, batch).Close()
}

// pgText makes s storable in PostgreSQL, which refuses text that is not valid
// UTF-8 or holds a NUL byte, both of which SQLite keeps (a file indexed in
// Windows-1252, say): invalid bytes become U+FFFD and NULs are dropped.
func pgText(s string) string {
	if utf8.ValidString(s) && strings.IndexByte(s, 0) < 0 {
		return s
	}
	return strings.ReplaceAll(strings.ToValidUTF8(s, "�"), "\x00", "")
}

// pgMeta copies metadata with pgText applied to keys and values, leaving room
// for extra entries.
func pgMeta(m map[string]string, extra int) map[string]string {
	out := make(map[string]string, len(m)+extra)
	for k, v := range m {
		out[pgText(k)] = pgText(v)
	}
	return out
}

// ExportPointsWithVectors returns every point with its stored vector (nil for
// a point waiting to be re-embedded).
func (p *PGVectorProvider) ExportPointsWithVectors(ctx context.Context, projectID string) ([]Document, [][]float32, error) {
	rows, err := p.pool.Query(ctx, fmt.Sprintf(
		`SELECT id, content, metadata, embedding::text FROM %s WHERE project_id = $1 ORDER BY id`, p.table), projectID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var docs []Document
	var vecs [][]float32
	for rows.Next() {
		var id, content string
		var meta map[string]string
		var emb *string
		if err := rows.Scan(&id, &content, &meta, &emb); err != nil {
			return nil, nil, err
		}
		if meta == nil {
			meta = map[string]string{}
		}
		var v []float32
		if emb != nil {
			if v, err = parsePGVector(*emb); err != nil {
				return nil, nil, err
			}
		}
		docs = append(docs, Document{ID: id, Content: content, Meta: meta})
		vecs = append(vecs, v)
	}
	return docs, vecs, rows.Err()
}

// StoreInfo reports the PostgreSQL and pgvector versions, the table's size and
// row count, and the round-trip latency.
func (p *PGVectorProvider) StoreInfo(ctx context.Context) (StoreInfo, error) {
	info := StoreInfo{Type: "pgvector", Table: p.table, Dim: p.dim, Target: DescribeDSN(p.dsn)}
	start := time.Now()
	if err := p.pool.QueryRow(ctx, `SELECT current_setting('server_version')`).Scan(&info.ServerVersion); err != nil {
		return info, err
	}
	info.LatencyMs = time.Since(start).Milliseconds()
	info.ServerVersion = "PostgreSQL " + info.ServerVersion
	_ = p.pool.QueryRow(ctx, `SELECT extversion FROM pg_extension WHERE extname = 'vector'`).Scan(&info.ExtensionVersion)
	_ = p.pool.QueryRow(ctx, `SELECT COALESCE(pg_total_relation_size(to_regclass($1)), 0)`, p.table).Scan(&info.SizeBytes)
	if err := p.pool.QueryRow(ctx, fmt.Sprintf(
		`SELECT COUNT(*), COALESCE(SUM(LENGTH(content)), 0) FROM %s`, p.table)).Scan(&info.Rows, &info.ContentChars); err != nil {
		return info, err
	}
	return info, nil
}

// EmbedStats counts points per embed_model / embed_dim (projectID "" = all).
func (p *PGVectorProvider) EmbedStats(ctx context.Context, projectID string) ([]EmbedStat, error) {
	q := fmt.Sprintf(`SELECT COALESCE(metadata->>'embed_model', ''), COALESCE(metadata->>'embed_dim', ''), COUNT(*)
FROM %s WHERE ($1 = '' OR project_id = $1) GROUP BY 1, 2 ORDER BY 3 DESC`, p.table)
	rows, err := p.pool.Query(ctx, q, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EmbedStat
	for rows.Next() {
		var st EmbedStat
		var dim string
		if err := rows.Scan(&st.Model, &dim, &st.Count); err != nil {
			return nil, err
		}
		st.Dim, _ = strconv.Atoi(dim)
		out = append(out, st)
	}
	return out, rows.Err()
}

// CountFiles counts a project's distinct source files.
func (p *PGVectorProvider) CountFiles(ctx context.Context, projectID string) (int, error) {
	var n int
	err := p.pool.QueryRow(ctx, fmt.Sprintf(
		`SELECT COUNT(DISTINCT metadata->>'source_file') FROM %s WHERE project_id = $1`, p.table), projectID).Scan(&n)
	return n, err
}

// DescribeDSN is a PostgreSQL connection string without its password:
// "user@host:port/db".
func DescribeDSN(dsn string) string {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return "(unparsable connection string)"
	}
	user := cfg.User
	if user != "" {
		user += "@"
	}
	return fmt.Sprintf("%s%s:%d/%s", user, cfg.Host, cfg.Port, cfg.Database)
}

// PGProbe is what ProbePostgres found.
type PGProbe struct {
	Target             string `json:"target"` // user@host:port/db, never the password
	ServerVersion      string `json:"server_version,omitempty"`
	ExtensionVersion   string `json:"extension_version,omitempty"`   // installed pgvector
	ExtensionAvailable string `json:"extension_available,omitempty"` // pgvector the server could install
	LatencyMs          int64  `json:"latency_ms"`
}

// ProbePostgres connects to dsn and reports the PostgreSQL and pgvector
// versions. With create it also runs CREATE EXTENSION IF NOT EXISTS vector
// (what the store does at startup). Errors carry a hint on what to fix.
func ProbePostgres(ctx context.Context, dsn string, create bool) (PGProbe, error) {
	pr := PGProbe{Target: DescribeDSN(dsn)}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return pr, fmt.Errorf("the connection string is invalid (%v) — use postgres://user:password@host:5432/dbname", err)
	}
	start := time.Now()
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return pr, pgConnectError(err, cfg.Host, cfg.Port, cfg.Database)
	}
	defer conn.Close(context.Background())
	if err := conn.QueryRow(ctx, `SELECT current_setting('server_version')`).Scan(&pr.ServerVersion); err != nil {
		return pr, err
	}
	pr.LatencyMs = time.Since(start).Milliseconds()
	pr.ServerVersion = "PostgreSQL " + pr.ServerVersion
	_ = conn.QueryRow(ctx, `SELECT default_version FROM pg_available_extensions WHERE name = 'vector'`).Scan(&pr.ExtensionAvailable)
	if create {
		if _, err := conn.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
			if pr.ExtensionAvailable == "" {
				return pr, fmt.Errorf("%s has no pgvector extension: install it (the pgvector/pgvector:pg17 Docker image has it, or apt install postgresql-17-pgvector) — %v", pr.ServerVersion, err)
			}
			return pr, fmt.Errorf("cannot enable pgvector (%v): run CREATE EXTENSION vector; once as a superuser in database %s", err, cfg.Database)
		}
	}
	_ = conn.QueryRow(ctx, `SELECT extversion FROM pg_extension WHERE extname = 'vector'`).Scan(&pr.ExtensionVersion)
	return pr, nil
}

func pgConnectError(err error, host string, port uint16, db string) error {
	msg := err.Error()
	low := strings.ToLower(msg)
	var hint string
	switch {
	case strings.Contains(low, "password authentication failed") || strings.Contains(low, "no password supplied"):
		hint = "wrong user or password in the connection string"
	case strings.Contains(low, "does not exist") && strings.Contains(low, "database"):
		hint = fmt.Sprintf("create the database first (createdb %s) or fix its name", db)
	case strings.Contains(low, "connection refused"):
		hint = fmt.Sprintf("nothing listens on %s:%d — is PostgreSQL running? In docker compose use the service name (e.g. postgres) as the host", host, port)
	case strings.Contains(low, "no such host") || strings.Contains(low, "lookup"):
		hint = fmt.Sprintf("the host %q cannot be resolved", host)
	case strings.Contains(low, "timeout") || strings.Contains(low, "deadline"):
		hint = fmt.Sprintf("%s:%d does not answer — check the firewall and the host", host, port)
	case strings.Contains(low, "ssl") || strings.Contains(low, "tls"):
		hint = "add ?sslmode=disable for a server without TLS, or sslmode=require for one with it"
	}
	if hint != "" {
		return fmt.Errorf("cannot connect to PostgreSQL at %s:%d: %s (%s)", host, port, hint, msg)
	}
	return fmt.Errorf("cannot connect to PostgreSQL at %s:%d: %s", host, port, msg)
}
