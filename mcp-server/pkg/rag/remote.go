package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// RemoteProvider is client mode (vector_store: remote): this process scans
// and chunks code locally, and an enowx-rag server (`--serve`, e.g. on a VPS
// with pgvector) embeds, stores, searches and reranks it over the server's
// HTTP API. The local machine needs no database and no embedding API key; the
// server's dashboard sees every project, sync and query.
type RemoteProvider struct {
	base   string
	token  string
	client *http.Client
	host   string

	mu     sync.Mutex
	counts map[string]int
	at     time.Time
}

var (
	_ Provider          = (*RemoteProvider)(nil)
	_ PayloadUpdater    = (*RemoteProvider)(nil)
	_ DelegatedSearcher = (*RemoteProvider)(nil)
	_ SyncReporter      = (*RemoteProvider)(nil)
)

// NewRemoteProvider connects to the server at baseURL and checks that it is
// reachable and accepts the token, so a wrong URL or token fails at startup
// with a clear message instead of on the first tool call.
func NewRemoteProvider(ctx context.Context, baseURL, token string) (*RemoteProvider, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("remote store: remote_url is required (the enowx-rag server, e.g. https://rag.example.com)")
	}
	if u, err := url.Parse(baseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("remote store: remote_url %q must be an http(s) URL like https://rag.example.com", baseURL)
	}
	host, _ := os.Hostname()
	p := &RemoteProvider{base: baseURL, token: token, host: host, client: &http.Client{Timeout: 5 * time.Minute}}
	if err := p.Ping(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

// Ping checks the server is up and the token is accepted.
func (p *RemoteProvider) Ping(ctx context.Context) error {
	var stats struct {
		TotalProjects int `json:"total_projects"`
	}
	if err := p.do(ctx, http.MethodGet, "/api/stats", nil, &stats); err != nil {
		return fmt.Errorf("remote store %s: %w", p.base, err)
	}
	return nil
}

// BaseURL is the server this provider talks to.
func (p *RemoteProvider) BaseURL() string { return p.base }

func (p *RemoteProvider) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.base+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	req.Header.Set("X-Enowx-Client", p.host)
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach the enowx-rag server: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("the server rejected the token (HTTP %d) — set remote_token / RAG_REMOTE_TOKEN to the server's admin token", resp.StatusCode)
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
			Hint  string `json:"hint"`
		}
		msg := strings.TrimSpace(string(raw))
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			msg = e.Error
			if e.Hint != "" {
				msg += " — " + e.Hint
			}
		}
		if resp.StatusCode == http.StatusNotFound && strings.Contains(msg, "API endpoint not found") {
			msg += " (the server runs an older enowx-rag: upgrade it to use client mode)"
		}
		return fmt.Errorf("server: %s %s: HTTP %d: %s", method, path, resp.StatusCode, msg)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("server: %s %s: decode: %w", method, path, err)
		}
	}
	return nil
}

func projPath(projectID, rest string) string {
	return "/api/projects/" + url.PathEscape(projectID) + rest
}

type remoteDoc struct {
	ID      string            `json:"id"`
	Content string            `json:"content"`
	Meta    map[string]string `json:"meta,omitempty"`
}

func toRemoteDocs(docs []Document) []remoteDoc {
	out := make([]remoteDoc, len(docs))
	for i, d := range docs {
		out[i] = remoteDoc{ID: d.ID, Content: d.Content, Meta: d.Meta}
	}
	return out
}

func (p *RemoteProvider) CreateCollection(ctx context.Context, projectID string) error {
	return p.do(ctx, http.MethodPost, projPath(projectID, ""), map[string]any{}, nil)
}

func (p *RemoteProvider) DeleteCollection(ctx context.Context, projectID string) error {
	p.invalidate()
	return p.do(ctx, http.MethodDelete, projPath(projectID, ""), nil, nil)
}

func (p *RemoteProvider) Index(ctx context.Context, projectID string, docs []Document) error {
	if len(docs) == 0 {
		return nil
	}
	p.invalidate()
	return p.do(ctx, http.MethodPost, projPath(projectID, "/documents"),
		map[string]any{"documents": toRemoteDocs(docs), "quiet": true}, nil)
}

func (p *RemoteProvider) UpdatePayload(ctx context.Context, projectID string, docs []Document) error {
	if len(docs) == 0 {
		return nil
	}
	return p.do(ctx, http.MethodPost, projPath(projectID, "/documents/payload"),
		map[string]any{"documents": toRemoteDocs(docs)}, nil)
}

func (p *RemoteProvider) SemanticSearch(ctx context.Context, projectID, query string, limit int) ([]Result, error) {
	return p.SearchFull(ctx, projectID, query, limit, limit, false, false, false)
}

// SearchFull runs the whole search on the server: retrieval, hybrid fusion,
// reranking (with the server's Voyage key) and metrics.
func (p *RemoteProvider) SearchFull(ctx context.Context, projectID, query string, k, recall int, hybrid, rerank, compress bool) ([]Result, error) {
	var out struct {
		Results []Result `json:"results"`
	}
	err := p.do(ctx, http.MethodPost, "/api/search", map[string]any{
		"project_id": projectID, "query": query, "k": k, "recall": recall,
		"hybrid": hybrid, "rerank": rerank, "compress": compress,
	}, &out)
	if err != nil && strings.Contains(err.Error(), "HTTP 404") && strings.Contains(err.Error(), "project not found") {
		return []Result{}, nil
	}
	return out.Results, err
}

func (p *RemoteProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	return nil, fmt.Errorf("embedding happens on the enowx-rag server in client mode")
}

func (p *RemoteProvider) DeletePoints(ctx context.Context, projectID string, pointIDs []string) error {
	if len(pointIDs) == 0 {
		return nil
	}
	p.invalidate()
	return p.do(ctx, http.MethodPost, projPath(projectID, "/points/delete"), map[string]any{"ids": pointIDs}, nil)
}

func (p *RemoteProvider) ListPointIDs(ctx context.Context, projectID string, metaFilter map[string]string) ([]string, error) {
	pts, err := p.ListPoints(ctx, projectID, metaFilter)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(pts))
	for i, pt := range pts {
		ids[i] = pt.ID
	}
	return ids, nil
}

func (p *RemoteProvider) ListPoints(ctx context.Context, projectID string, metaFilter map[string]string) ([]PointInfo, error) {
	q := url.Values{}
	for k, v := range metaFilter {
		q.Set("meta."+k, v)
	}
	path := projPath(projectID, "/points")
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out []PointInfo
	if err := p.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ReportSync tells the server a directory finished syncing.
func (p *RemoteProvider) ReportSync(ctx context.Context, projectID, dir string, stats map[string]int, took time.Duration) error {
	return p.do(ctx, http.MethodPost, projPath(projectID, "/sources"), map[string]any{
		"host": p.host, "dir": dir, "result": stats, "duration_ms": took.Milliseconds(),
	}, nil)
}

func (p *RemoteProvider) invalidate() {
	p.mu.Lock()
	p.counts = nil
	p.mu.Unlock()
}

// projectCounts lists the server's projects with chunk counts, cached briefly
// so ListProjects doesn't make one request per project.
func (p *RemoteProvider) projectCounts(ctx context.Context) (map[string]int, error) {
	p.mu.Lock()
	if p.counts != nil && time.Since(p.at) < 3*time.Second {
		c := p.counts
		p.mu.Unlock()
		return c, nil
	}
	p.mu.Unlock()
	var list []struct {
		ProjectID  string `json:"project_id"`
		ChunkCount int    `json:"chunk_count"`
	}
	if err := p.do(ctx, http.MethodGet, "/api/projects", nil, &list); err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(list))
	for _, it := range list {
		counts[it.ProjectID] = it.ChunkCount
	}
	p.mu.Lock()
	p.counts, p.at = counts, time.Now()
	p.mu.Unlock()
	return counts, nil
}

// ListProjectIDs implements core.ProjectLister.
func (p *RemoteProvider) ListProjectIDs(ctx context.Context) ([]string, error) {
	counts, err := p.projectCounts(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(counts))
	for id := range counts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// CountPoints implements core.ProjectCounter.
func (p *RemoteProvider) CountPoints(ctx context.Context, projectID string) (int, error) {
	counts, err := p.projectCounts(ctx)
	if err != nil {
		return 0, err
	}
	return counts[projectID], nil
}

// RemoteStats returns the server's stats and metrics (rag_stats in client mode).
func (p *RemoteProvider) RemoteStats(ctx context.Context) (map[string]any, error) {
	var stats, metrics map[string]any
	if err := p.do(ctx, http.MethodGet, "/api/stats", nil, &stats); err != nil {
		return nil, err
	}
	if err := p.do(ctx, http.MethodGet, "/api/metrics", nil, &metrics); err != nil {
		return nil, err
	}
	delete(stats, "projects")
	stats["metrics"] = metrics
	stats["server"] = p.base
	return stats, nil
}

func (p *RemoteProvider) Close() error { return nil }
