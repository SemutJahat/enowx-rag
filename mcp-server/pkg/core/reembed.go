package core

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/enowdev/enowx-rag/mcp-server/pkg/rag"
)

const (
	// reembedBatch bounds how many points go to the provider per Index call.
	reembedBatch = 100
	// reembedTimeout bounds a re-embed started by a search. It is resumable:
	// the points done keep their new vectors, the rest are done next time.
	reembedTimeout = 2 * time.Minute
	// reembedRetry is how long a failed re-embed waits before it is tried
	// again, so a store that can't take the new vectors (a fixed-size Qdrant
	// collection or pgvector table) isn't re-embedded on every search.
	reembedRetry = time.Minute
)

// embedChecks remembers which projects were found current for an embedder in
// this process, so the stored points are read once, not on every search.
type embedChecks struct {
	mu     sync.Mutex
	locks  map[string]*sync.Mutex // project → one check at a time
	done   map[string]bool        // project\x00model\x00dim → every point current
	failed map[string]time.Time   // same key → when the last run failed
}

func (c *embedChecks) lock(project string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.locks == nil {
		c.locks = map[string]*sync.Mutex{}
	}
	l, ok := c.locks[project]
	if !ok {
		l = &sync.Mutex{}
		c.locks[project] = l
	}
	return l
}

// due reports whether key still needs a check: never found current, and not
// failed within reembedRetry.
func (c *embedChecks) due(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.done[key] && time.Since(c.failed[key]) >= reembedRetry
}

func (c *embedChecks) finish(key string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		if c.failed == nil {
			c.failed = map[string]time.Time{}
		}
		c.failed[key] = time.Now()
		return
	}
	if c.done == nil {
		c.done = map[string]bool{}
	}
	c.done[key] = true
	delete(c.failed, key)
}

// refreshEmbeddings re-embeds, from their stored text, the points of project
// that another embedding model or vector size wrote than the provider writes
// now: after the model in the config changed, code chunks and memory notes
// alike, keeping their IDs and metadata. Without it the old vectors stay: a
// re-index skips unchanged chunks, and a search compares the query against
// vectors of another model (wrong results) or skips them (other size).
//
// It returns how many points it re-embedded. A project is checked once per
// embedder in this process; a failed run is retried after reembedRetry.
// Providers that can't report their embedder or export points are left alone.
func (s *Service) refreshEmbeddings(ctx context.Context, project string) (int, error) {
	ider, ok := s.provider.(rag.EmbedIdentifier)
	if !ok {
		return 0, nil
	}
	exp, ok := s.provider.(rag.Exporter)
	if !ok {
		return 0, nil
	}
	model, dim := ider.EmbedIdentity()
	key := project + "\x00" + model + "\x00" + strconv.Itoa(dim)

	l := s.embedChecks.lock(project)
	l.Lock()
	defer l.Unlock()
	if !s.embedChecks.due(key) {
		return 0, nil
	}
	n, err := s.reembedStale(ctx, exp, project, model, dim)
	s.embedChecks.finish(key, err)
	if n > 0 {
		fmt.Fprintf(os.Stderr, "re-embedded %d points of %s for %s (dim %d)\n", n, project, model, dim)
	}
	return n, err
}

func (s *Service) reembedStale(ctx context.Context, exp rag.Exporter, project, model string, dim int) (int, error) {
	docs, err := exp.ExportPoints(ctx, project)
	if err != nil {
		return 0, fmt.Errorf("check embeddings of %s: %w", project, err)
	}
	stale := embeddedElsewhere(docs, model, dim)
	if len(stale) == 0 {
		return 0, nil
	}
	// The size an embedder reports can be a guess (TEI falls back to a
	// default when its /info is unreachable): before re-embedding, measure
	// it on a real vector, so a wrong guess doesn't re-embed everything.
	probe, err := s.provider.Embed(ctx, "embedding size probe")
	if err != nil {
		return 0, fmt.Errorf("check embeddings of %s: %w", project, err)
	}
	if len(probe) != dim {
		dim = len(probe)
		stale = embeddedElsewhere(docs, model, dim)
	}
	done := 0
	for start := 0; start < len(stale); start += reembedBatch {
		batch := stale[start:min(start+reembedBatch, len(stale))]
		if err := s.provider.Index(ctx, project, batch); err != nil {
			return done, fmt.Errorf("re-embed %d points of %s for %s (dim %d): %w; "+
				"a store with a fixed vector size (a Qdrant collection, a pgvector table) "+
				"needs a Migration to a new collection or table", len(stale)-done, project, model, dim, err)
		}
		done += len(batch)
	}
	return done, nil
}

// embeddedElsewhere returns the docs whose metadata names another embedding
// model or vector size than model and dim. Points that don't say (written
// before enowx-rag recorded it) are left alone; dim 0 means unknown.
func embeddedElsewhere(docs []rag.Document, model string, dim int) []rag.Document {
	want := strconv.Itoa(dim)
	var out []rag.Document
	for _, d := range docs {
		m, sz := d.Meta["embed_model"], d.Meta["embed_dim"]
		if (m != "" && m != model) || (sz != "" && dim > 0 && sz != want) {
			out = append(out, d)
		}
	}
	return out
}
