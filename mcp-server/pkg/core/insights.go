package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// migrateMetricsSchema adds what newer versions record to a metrics database
// made by an older one: who ran each query, and the persisted activity log.
func migrateMetricsSchema(db *sql.DB) error {
	cols := map[string]bool{}
	rows, err := db.Query(`PRAGMA table_info(query_metrics)`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		cols[name] = true
	}
	rows.Close()
	for _, c := range []string{"project", "query", "client"} {
		if !cols[c] {
			if _, err := db.Exec(fmt.Sprintf(`ALTER TABLE query_metrics ADD COLUMN %s TEXT NOT NULL DEFAULT ''`, c)); err != nil {
				return err
			}
		}
	}
	for _, q := range []string{
		`CREATE INDEX IF NOT EXISTS idx_query_metrics_ts ON query_metrics(ts)`,
		`CREATE TABLE IF NOT EXISTS activity (
			id      INTEGER PRIMARY KEY AUTOINCREMENT,
			ts      INTEGER NOT NULL,
			type    TEXT    NOT NULL,
			project TEXT    NOT NULL DEFAULT '',
			data    TEXT    NOT NULL DEFAULT '{}'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_activity_ts ON activity(ts)`,
	} {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

// Insights are the durable views the monitoring dashboard shows next to the
// latency summary.
type Insights struct {
	Queries24h    int64           `json:"queries_24h"`
	Timeline24h   []TimelinePoint `json:"timeline_24h"`
	PerProject    []ProjectUsage  `json:"per_project"`
	RecentQueries []RecentQuery   `json:"recent_queries"`
}

// TimelinePoint is one hour of query traffic.
type TimelinePoint struct {
	T            time.Time `json:"t"`
	Count        int64     `json:"count"`
	AvgLatencyMs float64   `json:"avg_latency_ms"`
}

// ProjectUsage is how often a project was searched.
type ProjectUsage struct {
	ProjectID string     `json:"project_id"`
	Count     int64      `json:"count"`
	LastAt    *time.Time `json:"last_at,omitempty"`
}

// RecentQuery is one persisted query.
type RecentQuery struct {
	TS        time.Time `json:"ts"`
	Project   string    `json:"project"`
	Query     string    `json:"query"`
	LatencyMs float64   `json:"latency_ms"`
	Results   int       `json:"results"`
	Reranked  bool      `json:"reranked"`
	Hybrid    bool      `json:"hybrid"`
	Client    string    `json:"client"`
}

// InsightsProvider is implemented by metrics stores that can compute Insights.
type InsightsProvider interface {
	Insights(ctx context.Context, now time.Time) (Insights, error)
}

// EventStore is implemented by metrics stores that persist the activity log.
type EventStore interface {
	PersistEvent(ctx context.Context, ev Event) error
	RecentEvents(ctx context.Context, limit int) ([]Event, error)
}

var (
	_ InsightsProvider = (*SQLiteMetricsStore)(nil)
	_ EventStore       = (*SQLiteMetricsStore)(nil)
)

// Insights computes the last 24 hours' hourly timeline, per-project usage and
// the 20 most recent queries.
func (s *SQLiteMetricsStore) Insights(ctx context.Context, now time.Time) (Insights, error) {
	out := Insights{Timeline24h: []TimelinePoint{}, PerProject: []ProjectUsage{}, RecentQueries: []RecentQuery{}}
	end := now.Truncate(time.Hour).Add(time.Hour)
	start := end.Add(-24 * time.Hour)
	buckets := make([]TimelinePoint, 24)
	for i := range buckets {
		buckets[i].T = start.Add(time.Duration(i) * time.Hour).UTC()
	}
	rows, err := s.db.QueryContext(ctx, `SELECT (ts - ?) / 3600, COUNT(*), AVG(latency_ms) FROM query_metrics
WHERE ts >= ? AND ts < ? GROUP BY 1`, start.Unix(), start.Unix(), end.Unix())
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var i int
		var n int64
		var avg float64
		if err := rows.Scan(&i, &n, &avg); err != nil {
			rows.Close()
			return out, err
		}
		if i >= 0 && i < 24 {
			buckets[i].Count, buckets[i].AvgLatencyMs = n, avg
			out.Queries24h += n
		}
	}
	rows.Close()
	out.Timeline24h = buckets

	rows, err = s.db.QueryContext(ctx, `SELECT project, COUNT(*), MAX(ts) FROM query_metrics WHERE project != '' GROUP BY project ORDER BY 2 DESC`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var pu ProjectUsage
		var last int64
		if err := rows.Scan(&pu.ProjectID, &pu.Count, &last); err != nil {
			rows.Close()
			return out, err
		}
		t := time.Unix(last, 0).UTC()
		pu.LastAt = &t
		out.PerProject = append(out.PerProject, pu)
	}
	rows.Close()

	rows, err = s.db.QueryContext(ctx, `SELECT ts, project, query, latency_ms, results, reranked, hybrid, client
FROM query_metrics WHERE project != '' ORDER BY id DESC LIMIT 20`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var rq RecentQuery
		var ts int64
		var reranked, hybrid int
		if err := rows.Scan(&ts, &rq.Project, &rq.Query, &rq.LatencyMs, &rq.Results, &reranked, &hybrid, &rq.Client); err != nil {
			return out, err
		}
		rq.TS = time.Unix(ts, 0).UTC()
		rq.Reranked, rq.Hybrid = reranked == 1, hybrid == 1
		out.RecentQueries = append(out.RecentQueries, rq)
	}
	return out, rows.Err()
}

// activityKeep bounds the persisted activity log.
const activityKeep = 5000

// PersistEvent appends ev to the activity log (trimmed to the newest entries).
func (s *SQLiteMetricsStore) PersistEvent(ctx context.Context, ev Event) error {
	raw, _ := json.Marshal(ev.Data)
	project := ""
	if m, ok := ev.Data.(map[string]any); ok {
		if p, ok := m["project_id"].(string); ok {
			project = p
		}
	}
	ts := ev.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO activity(ts, type, project, data) VALUES (?, ?, ?, ?)`,
		ts.UnixMilli(), ev.Type, project, string(raw))
	if err != nil {
		return err
	}
	if id, err := res.LastInsertId(); err == nil && id%200 == 0 {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM activity WHERE id <= ?`, id-activityKeep)
	}
	return nil
}

// RecentEvents returns the newest persisted events, newest first.
func (s *SQLiteMetricsStore) RecentEvents(ctx context.Context, limit int) ([]Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT ts, type, data FROM activity ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var ts int64
		var ev Event
		var raw string
		if err := rows.Scan(&ts, &ev.Type, &raw); err != nil {
			return nil, err
		}
		ev.Timestamp = time.UnixMilli(ts).UTC()
		var data map[string]any
		if json.Unmarshal([]byte(raw), &data) == nil {
			ev.Data = data
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}
