package rag

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
)

var (
	_ VectorImporter = (*SQLiteProvider)(nil)
	_ VectorExporter = (*SQLiteProvider)(nil)
	_ StoreInfoer    = (*SQLiteProvider)(nil)
	_ EmbedStatser   = (*SQLiteProvider)(nil)
	_ FileCounter    = (*SQLiteProvider)(nil)
)

// ExportPointsWithVectors returns every point of the project with its vector.
func (p *SQLiteProvider) ExportPointsWithVectors(ctx context.Context, projectID string) ([]Document, [][]float32, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT id, content, meta, vector FROM points WHERE project = ? ORDER BY id`, projectID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	return scanSQLitePoints(rows)
}

func scanSQLitePoints(rows *sql.Rows) ([]Document, [][]float32, error) {
	var docs []Document
	var vecs [][]float32
	for rows.Next() {
		var d Document
		var raw string
		var blob []byte
		if err := rows.Scan(&d.ID, &d.Content, &raw, &blob); err != nil {
			return nil, nil, err
		}
		d.Meta = map[string]string{}
		_ = json.Unmarshal([]byte(raw), &d.Meta)
		docs = append(docs, d)
		vecs = append(vecs, decodeVector(blob))
	}
	return docs, vecs, rows.Err()
}

// IndexVectors stores points with vectors embedded elsewhere.
func (p *SQLiteProvider) IndexVectors(ctx context.Context, projectID string, docs []Document, vectors [][]float32) error {
	if len(docs) != len(vectors) {
		return fmt.Errorf("index vectors: %d documents but %d vectors", len(docs), len(vectors))
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO projects(project) VALUES (?)`, projectID); err != nil {
		return err
	}
	for i, d := range docs {
		id := d.ID
		if id == "" {
			id = uuid.NewString()
		}
		meta := map[string]string{"doc_id": id}
		for k, v := range d.Meta {
			meta[k] = v
		}
		meta["embed_dim"] = strconv.Itoa(len(vectors[i]))
		raw, _ := json.Marshal(meta)
		if _, err := tx.ExecContext(ctx, `INSERT INTO points(project, id, content, meta, vector) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(project, id) DO UPDATE SET content = excluded.content, meta = excluded.meta, vector = excluded.vector`,
			projectID, id, d.Content, string(raw), encodeVector(vectors[i])); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM points_fts WHERE project = ? AND id = ?`, projectID, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO points_fts(content, project, id) VALUES (?, ?, ?)`, d.Content, projectID, id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	p.forget(projectID)
	return nil
}

// StoreInfo reports the file, its size and the stored rows.
func (p *SQLiteProvider) StoreInfo(ctx context.Context) (StoreInfo, error) {
	info := StoreInfo{Type: "sqlite", Dim: p.embedder.VectorSize()}
	start := time.Now()
	var file string
	rows, err := p.db.QueryContext(ctx, `PRAGMA database_list`)
	if err == nil {
		for rows.Next() {
			var seq int
			var name, path string
			if rows.Scan(&seq, &name, &path) == nil && name == "main" {
				file = path
			}
		}
		rows.Close()
	}
	info.Target = file
	if st, err := os.Stat(file); err == nil {
		info.SizeBytes = st.Size()
	}
	var version string
	_ = p.db.QueryRowContext(ctx, `SELECT sqlite_version()`).Scan(&version)
	info.ServerVersion = "SQLite " + version
	if err := p.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(LENGTH(content)), 0) FROM points`).Scan(&info.Rows, &info.ContentChars); err != nil {
		return info, err
	}
	info.LatencyMs = time.Since(start).Milliseconds()
	return info, nil
}

// EmbedStats counts points per embed_model / embed_dim (projectID "" = all).
func (p *SQLiteProvider) EmbedStats(ctx context.Context, projectID string) ([]EmbedStat, error) {
	return sqliteEmbedStats(ctx, p.db, projectID)
}

func sqliteEmbedStats(ctx context.Context, db *sql.DB, projectID string) ([]EmbedStat, error) {
	rows, err := db.QueryContext(ctx, `SELECT COALESCE(json_extract(meta, '$.embed_model'), ''), COALESCE(json_extract(meta, '$.embed_dim'), ''), COUNT(*)
FROM points WHERE (? = '' OR project = ?) GROUP BY 1, 2 ORDER BY 3 DESC`, projectID, projectID)
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
func (p *SQLiteProvider) CountFiles(ctx context.Context, projectID string) (int, error) {
	var n int
	err := p.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT json_extract(meta, '$.source_file')) FROM points WHERE project = ?`, projectID).Scan(&n)
	return n, err
}

// SQLiteSnapshot reads an enowx-rag SQLite store file (rag.db) without
// changing it: the source of an import into another store (e.g. a local
// store uploaded to a server running pgvector).
type SQLiteSnapshot struct {
	db *sql.DB
}

// SnapshotProject summarizes one project of a snapshot.
type SnapshotProject struct {
	ProjectID string      `json:"project_id"`
	Points    int         `json:"points"`
	Chars     int64       `json:"chars"`
	Embeds    []EmbedStat `json:"embeds"` // by model / size, most points first
}

// OpenSQLiteSnapshot opens path read-only and checks it is an enowx-rag store.
func OpenSQLiteSnapshot(path string) (*SQLiteSnapshot, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("sqlite store: %w", err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("sqlite store: open: %w", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'points'`).Scan(&n); err != nil {
		db.Close()
		return nil, fmt.Errorf("not a SQLite database: %w", err)
	}
	if n == 0 {
		db.Close()
		return nil, fmt.Errorf("not an enowx-rag store: it has no \"points\" table (expected ~/.enowx-rag/rag.db)")
	}
	return &SQLiteSnapshot{db: db}, nil
}

// Close releases the file.
func (s *SQLiteSnapshot) Close() error { return s.db.Close() }

// Projects lists the snapshot's projects with point counts and embedders.
func (s *SQLiteSnapshot) Projects(ctx context.Context) ([]SnapshotProject, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT project, COUNT(*), COALESCE(SUM(LENGTH(content)), 0) FROM points GROUP BY project ORDER BY project`)
	if err != nil {
		return nil, err
	}
	var out []SnapshotProject
	for rows.Next() {
		var sp SnapshotProject
		if err := rows.Scan(&sp.ProjectID, &sp.Points, &sp.Chars); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, sp)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		st, err := sqliteEmbedStats(ctx, s.db, out[i].ProjectID)
		if err != nil {
			return nil, err
		}
		out[i].Embeds = st
	}
	return out, nil
}

// EachBatch calls fn with the project's points (and vectors) in batches of
// size, in id order, so a large store is never held in memory at once.
func (s *SQLiteSnapshot) EachBatch(ctx context.Context, projectID string, size int, fn func(docs []Document, vecs [][]float32) error) error {
	if size <= 0 {
		size = 100
	}
	after := ""
	for {
		rows, err := s.db.QueryContext(ctx, `SELECT id, content, meta, vector FROM points WHERE project = ? AND id > ? ORDER BY id LIMIT ?`,
			projectID, after, size)
		if err != nil {
			return err
		}
		docs, vecs, err := scanSQLitePoints(rows)
		rows.Close()
		if err != nil {
			return err
		}
		if len(docs) == 0 {
			return nil
		}
		if err := fn(docs, vecs); err != nil {
			return err
		}
		after = docs[len(docs)-1].ID
		if len(docs) < size {
			return nil
		}
	}
}
