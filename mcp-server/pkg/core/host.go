package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/enowdev/enowx-rag/mcp-server/pkg/indexer"
	"github.com/enowdev/enowx-rag/mcp-server/pkg/rag"
)

// maxQueryText bounds the query text kept in metrics and events.
const maxQueryText = 300

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

type clientKey struct{}

// WithClient tags ctx with who is calling (a client host, "mcp-http",
// "dashboard"), recorded with each query for the monitoring dashboard.
func WithClient(ctx context.Context, client string) context.Context {
	if client == "" {
		return ctx
	}
	return context.WithValue(ctx, clientKey{}, client)
}

// ClientFrom is the caller set by WithClient, else this host's name.
func ClientFrom(ctx context.Context) string {
	if v, ok := ctx.Value(clientKey{}).(string); ok && v != "" {
		return v
	}
	return localHost()
}

var hostOnce struct {
	sync.Once
	name string
}

func localHost() string {
	hostOnce.Do(func() {
		hostOnce.name, _ = os.Hostname()
		if hostOnce.name == "" {
			hostOnce.name = "local"
		}
	})
	return hostOnce.name
}

// persistedEvents are the event types kept in the activity log (progress
// ticks and per-query events are not: queries live in query_metrics).
var persistedEvents = map[string]bool{
	"index_completed": true, "index_failed": true, "sync_reported": true, "project_imported": true, "project_created": true, "project_deleted": true,
	"points_deleted": true, "documents_indexed": true, "job_started": true, "job_completed": true,
	"job_failed": true, "server_started": true, "server_restarting": true, "config_changed": true,
}

// SetEventStore persists the activity log to store (see EventStore).
func (s *Service) SetEventStore(store EventStore) {
	s.eventStore = store
	if store == nil {
		s.events.SetSink(nil)
		return
	}
	s.events.SetSink(func(ev Event) {
		if !persistedEvents[ev.Type] {
			return
		}
		go func() { _ = store.PersistEvent(context.Background(), ev) }()
	})
}

// RecentEvents returns the persisted activity log, newest first (empty
// without an event store).
func (s *Service) RecentEvents(ctx context.Context, limit int) ([]Event, error) {
	if s.eventStore == nil {
		return []Event{}, nil
	}
	return s.eventStore.RecentEvents(ctx, limit)
}

// Insights returns the metrics store's 24h timeline, per-project usage and
// recent queries (zero values without a store that computes them).
func (s *Service) Insights(ctx context.Context) Insights {
	empty := Insights{Timeline24h: []TimelinePoint{}, PerProject: []ProjectUsage{}, RecentQueries: []RecentQuery{}}
	ip, ok := s.metricsStore.(InsightsProvider)
	if !ok {
		return empty
	}
	in, err := ip.Insights(ctx, time.Now())
	if err != nil {
		return empty
	}
	return in
}

// ---- Sources: where each project's code lives ----

// Source is one directory synced into a project, by this server or by a
// client-mode machine.
type Source struct {
	Host       string         `json:"host"`
	Dir        string         `json:"dir"`
	LastSync   time.Time      `json:"last_sync"`
	LastResult map[string]int `json:"last_result,omitempty"`
	DurationMs int64          `json:"duration_ms,omitempty"`
	OnServer   bool           `json:"on_server"` // computed: dir is readable here
}

// SourceRegistry remembers the sources of each project in a JSON file, so the
// dashboard can say where a project's code lives and the re-index button
// knows what to sync without asking for a path.
type SourceRegistry struct {
	mu   sync.Mutex
	path string
}

// NewSourceRegistry uses the JSON file at path (made on first write).
func NewSourceRegistry(path string) *SourceRegistry { return &SourceRegistry{path: path} }

func (r *SourceRegistry) load() map[string][]Source {
	out := map[string][]Source{}
	raw, err := os.ReadFile(r.path)
	if err == nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func (r *SourceRegistry) save(all map[string][]Source) error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(all, "", "  ")
	tmp, err := os.CreateTemp(filepath.Dir(r.path), ".project_sources-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), r.path)
}

// Record notes a finished sync of host:dir into project.
func (r *SourceRegistry) Record(project string, src Source) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	all := r.load()
	list := all[project]
	replaced := false
	for i := range list {
		if list[i].Host == src.Host && list[i].Dir == src.Dir {
			list[i] = src
			replaced = true
		}
	}
	if !replaced {
		list = append(list, src)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].LastSync.After(list[j].LastSync) })
	all[project] = list
	return r.save(all)
}

// Remove forgets a project's sources.
func (r *SourceRegistry) Remove(project string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	all := r.load()
	if _, ok := all[project]; !ok {
		return nil
	}
	delete(all, project)
	return r.save(all)
}

// Sources returns project's sources, with OnServer set for directories this
// server can read.
func (r *SourceRegistry) Sources(project string) []Source {
	r.mu.Lock()
	list := append([]Source(nil), r.load()[project]...)
	r.mu.Unlock()
	for i := range list {
		list[i].OnServer = dirExists(list[i].Dir) && (list[i].Host == localHost() || list[i].Host == "")
	}
	return list
}

func dirExists(dir string) bool {
	if dir == "" {
		return false
	}
	st, err := os.Stat(dir)
	return err == nil && st.IsDir()
}

// SetSourceRegistry makes the Service remember project sources.
func (s *Service) SetSourceRegistry(r *SourceRegistry) { s.sources = r }

// RecordSource records a sync a client-mode machine reported, and publishes
// it to the activity log.
func (s *Service) RecordSource(project, host, dir string, stats map[string]int, took time.Duration) {
	if s.sources != nil {
		if err := s.sources.Record(project, Source{Host: host, Dir: dir, LastSync: time.Now().UTC(),
			LastResult: stats, DurationMs: took.Milliseconds()}); err != nil {
			fmt.Fprintf(os.Stderr, "sources: %v\n", err)
		}
	}
	s.events.Publish(Event{Type: "sync_reported", Timestamp: time.Now(), Data: map[string]any{
		"project_id": project, "host": host, "directory": dir, "duration_ms": took.Milliseconds(), "result": stats}})
}

// ProjectSources returns the known sources of project (nil without a registry).
func (s *Service) ProjectSources(project string) []Source {
	if s.sources == nil {
		return nil
	}
	return s.sources.Sources(project)
}

// reportSync records a finished IndexProject: locally in the source registry,
// and to the server when the provider is a client-mode remote.
func (s *Service) reportSync(ctx context.Context, project, dir string, res *indexer.SyncResult, took time.Duration) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	stats := map[string]int{"indexed": res.Indexed, "updated": res.Updated, "deleted": res.Deleted,
		"files_scanned": res.FilesScanned, "skipped": res.Skipped}
	if s.sources != nil {
		_ = s.sources.Record(project, Source{Host: localHost(), Dir: abs, LastSync: time.Now().UTC(),
			LastResult: stats, DurationMs: took.Milliseconds()})
	}
	if rep, ok := s.provider.(rag.SyncReporter); ok {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		if err := rep.ReportSync(rctx, project, abs, stats, took); err != nil {
			fmt.Fprintf(os.Stderr, "report sync of %s to server: %v\n", project, err)
		}
	}
}

// ServerDirs are the directories of project this server can sync from disk:
// the auto-sync registry's and the recorded sources that exist here.
func (s *Service) ServerDirs(project string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(d string) {
		if d != "" && !seen[d] && dirExists(d) {
			seen[d] = true
			out = append(out, d)
		}
	}
	if s.syncer != nil {
		for _, d := range s.syncer.registry.Dirs(project) {
			add(d)
		}
	}
	for _, src := range s.ProjectSources(project) {
		if src.OnServer {
			add(src.Dir)
		}
	}
	return out
}

// ---- Embedding health ----

// EmbedIdentity is the model and size the provider embeds with now ("", 0
// when it can't tell, e.g. client mode).
func (s *Service) EmbedIdentity() (string, int) {
	if id, ok := s.provider.(rag.EmbedIdentifier); ok {
		return id.EmbedIdentity()
	}
	return "", 0
}

// StalePoints counts points embedded by an incompatible model or another
// size than the provider writes now (they are re-embedded automatically), per
// project. Providers that can't count cheaply report nothing.
func (s *Service) StalePoints(ctx context.Context) (int, map[string]int) {
	es, ok := s.provider.(rag.EmbedStatser)
	if !ok {
		return 0, nil
	}
	model, dim := s.EmbedIdentity()
	if model == "" {
		return 0, nil
	}
	ids, err := s.projectIDs(ctx)
	if err != nil {
		return 0, nil
	}
	total := 0
	per := map[string]int{}
	for _, id := range ids {
		stats, err := es.EmbedStats(ctx, id)
		if err != nil {
			continue
		}
		n := 0
		for _, st := range stats {
			if staleStat(st, model, dim) {
				n += st.Count
			}
		}
		if n > 0 {
			per[id] = n
			total += n
		}
	}
	return total, per
}

func staleStat(st rag.EmbedStat, model string, dim int) bool {
	if st.Model != "" && !rag.EmbedModelsCompatible(st.Model, model) {
		return true
	}
	return st.Dim > 0 && dim > 0 && st.Dim != dim
}

func (s *Service) projectIDs(ctx context.Context) ([]string, error) {
	lister, ok := s.provider.(ProjectLister)
	if !ok {
		return nil, fmt.Errorf("provider cannot list projects")
	}
	return lister.ListProjectIDs(ctx)
}

// RefreshStale re-embeds, project by project, every point another model or
// size wrote (after the model in the config changed), reporting progress.
// It returns how many points it re-embedded.
func (s *Service) RefreshStale(ctx context.Context, progress func(project string, done, total int)) (int, error) {
	exp, ok := s.provider.(rag.Exporter)
	if !ok {
		return 0, nil
	}
	_, per := s.StalePoints(ctx)
	model, dim := s.EmbedIdentity()
	projects := make([]string, 0, len(per))
	for p := range per {
		projects = append(projects, p)
	}
	sort.Strings(projects)
	done := 0
	for _, p := range projects {
		l := s.embedChecks.lock(p)
		l.Lock()
		n, err := s.reembedStaleProgress(ctx, exp, p, model, dim, func(d, t int) {
			if progress != nil {
				progress(p, d, t)
			}
		})
		key := p + "\x00" + model + "\x00" + strconv.Itoa(dim)
		s.embedChecks.finish(key, err)
		l.Unlock()
		done += n
		if err != nil {
			return done, err
		}
	}
	return done, nil
}

// ReembedProject embeds every point of project again from its stored text
// with the current model, keeping IDs and metadata: the path-free re-index of
// a server whose code lives on client machines.
func (s *Service) ReembedProject(ctx context.Context, project string, progress func(done, total int)) (int, error) {
	exp, ok := s.provider.(rag.Exporter)
	if !ok {
		return 0, fmt.Errorf("this vector store cannot export points to re-embed them")
	}
	docs, err := exp.ExportPoints(ctx, project)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", project, err)
	}
	if progress != nil {
		progress(0, len(docs))
	}
	done := 0
	for start := 0; start < len(docs); start += reembedBatch {
		batch := docs[start:min(start+reembedBatch, len(docs))]
		if err := ctx.Err(); err != nil {
			return done, err
		}
		if err := s.provider.Index(ctx, project, batch); err != nil {
			return done, fmt.Errorf("re-embed %s: %w", project, err)
		}
		done += len(batch)
		if progress != nil {
			progress(done, len(docs))
		}
	}
	s.events.Publish(Event{Type: "index_completed", Timestamp: time.Now(), Data: map[string]any{
		"project_id": project, "reembedded": done, "host": localHost(), "mode": "reembed"}})
	return done, nil
}

// ---- Project details for the dashboard ----

// ProjectDetail is a project as the monitoring dashboard lists it.
type ProjectDetail struct {
	ProjectID   string     `json:"project_id"`
	ChunkCount  int        `json:"chunk_count"`
	FileCount   int        `json:"file_count"`
	Sources     []Source   `json:"sources"`
	QueryCount  int64      `json:"query_count"`
	LastQueryAt *time.Time `json:"last_query_at,omitempty"`
	StalePoints int        `json:"stale_points"`
}

// ProjectDetails lists projects with file counts, sources, query usage and
// how many of their points still wait to be re-embedded.
func (s *Service) ProjectDetails(ctx context.Context) ([]ProjectDetail, error) {
	stats, err := s.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	usage := map[string]ProjectUsage{}
	for _, u := range s.Insights(ctx).PerProject {
		usage[u.ProjectID] = u
	}
	_, stale := s.StalePoints(ctx)
	fc, _ := s.provider.(rag.FileCounter)
	out := make([]ProjectDetail, 0, len(stats))
	for _, st := range stats {
		d := ProjectDetail{ProjectID: st.ProjectID, ChunkCount: st.ChunkCount, Sources: s.ProjectSources(st.ProjectID),
			StalePoints: stale[st.ProjectID]}
		if d.Sources == nil {
			d.Sources = []Source{}
		}
		if fc != nil {
			d.FileCount, _ = fc.CountFiles(ctx, st.ProjectID)
		}
		if u, ok := usage[st.ProjectID]; ok {
			d.QueryCount, d.LastQueryAt = u.Count, u.LastAt
		}
		out = append(out, d)
	}
	return out, nil
}
