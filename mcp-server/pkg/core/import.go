package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/enowdev/enowx-rag/mcp-server/pkg/rag"
)

// ImportPlanProject is what importing one project of a SQLite snapshot into
// the running store will do.
type ImportPlanProject struct {
	ProjectID  string `json:"project_id"`
	Points     int    `json:"points"`
	Chars      int64  `json:"chars"`
	EmbedModel string `json:"embed_model"` // most common model in the snapshot
	EmbedDim   int    `json:"embed_dim"`
	// ReuseVectors: every point's vector fits the running store (compatible
	// model, same size) and the store accepts vectors — nothing is re-embedded.
	ReuseVectors bool `json:"reuse_vectors"`
	ReusePoints  int  `json:"reuse_points"`
	Exists       bool `json:"exists"` // the project already has points here
}

// PlanImport summarizes a snapshot against the running store.
func (s *Service) PlanImport(ctx context.Context, snap *rag.SQLiteSnapshot) ([]ImportPlanProject, error) {
	projects, err := snap.Projects(ctx)
	if err != nil {
		return nil, err
	}
	existing := map[string]bool{}
	if ids, err := s.projectIDs(ctx); err == nil {
		for _, id := range ids {
			existing[id] = true
		}
	}
	model, dim := s.EmbedIdentity()
	_, canImport := s.provider.(rag.VectorImporter)
	out := make([]ImportPlanProject, 0, len(projects))
	for _, p := range projects {
		pl := ImportPlanProject{ProjectID: p.ProjectID, Points: p.Points, Chars: p.Chars, Exists: existing[p.ProjectID]}
		if len(p.Embeds) > 0 {
			pl.EmbedModel, pl.EmbedDim = p.Embeds[0].Model, p.Embeds[0].Dim
		}
		if canImport && model != "" {
			for _, st := range p.Embeds {
				if reusable(st.Model, st.Dim, model, dim) {
					pl.ReusePoints += st.Count
				}
			}
		}
		pl.ReuseVectors = pl.ReusePoints == pl.Points && pl.Points > 0
		out = append(out, pl)
	}
	return out, nil
}

// reusable reports whether a vector made by srcModel at srcDim can be stored
// as-is by a store embedding with model at dim.
func reusable(srcModel string, srcDim int, model string, dim int) bool {
	return srcModel != "" && rag.EmbedModelsCompatible(srcModel, model) && srcDim == dim && dim > 0
}

// ImportOptions select what ImportSnapshot imports and how.
type ImportOptions struct {
	Projects []string          // empty = all
	Mode     string            // "merge" (upsert, default) or "replace" (delete the project first)
	Rename   map[string]string // source project → destination project
}

// ImportResult is the outcome for one project.
type ImportResult struct {
	Project    string `json:"project"`
	Dest       string `json:"dest"`
	Points     int    `json:"points"`
	Reused     int    `json:"reused"`     // stored with their original vectors
	Reembedded int    `json:"reembedded"` // embedded again (other model/size)
}

// ImportSnapshot copies projects from a SQLite store snapshot into the running
// store (e.g. pgvector): vectors are reused when the model and size match,
// otherwise the stored text is embedded again. Points keep their IDs and
// metadata, so later syncs from the original folders stay incremental.
func (s *Service) ImportSnapshot(ctx context.Context, snap *rag.SQLiteSnapshot, opts ImportOptions,
	progress func(project string, done, total int)) ([]ImportResult, error) {
	plan, err := snap.Projects(ctx)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, p := range opts.Projects {
		want[p] = true
	}
	model, dim := s.EmbedIdentity()
	importer, _ := s.provider.(rag.VectorImporter)
	var results []ImportResult
	for _, p := range plan {
		if len(want) > 0 && !want[p.ProjectID] {
			continue
		}
		dest := p.ProjectID
		if r := strings.TrimSpace(opts.Rename[p.ProjectID]); r != "" {
			dest = r
		}
		if opts.Mode == "replace" {
			if err := s.provider.DeleteCollection(ctx, dest); err != nil {
				return results, fmt.Errorf("replace %s: %w", dest, err)
			}
		}
		if err := s.provider.CreateCollection(ctx, dest); err != nil {
			return results, fmt.Errorf("create %s: %w", dest, err)
		}
		res := ImportResult{Project: p.ProjectID, Dest: dest}
		if progress != nil {
			progress(dest, 0, p.Points)
		}
		err := snap.EachBatch(ctx, p.ProjectID, 100, func(docs []rag.Document, vecs [][]float32) error {
			var keepDocs, embedDocs []rag.Document
			var keepVecs [][]float32
			for i, d := range docs {
				srcDim := len(vecs[i])
				if importer != nil && reusable(d.Meta["embed_model"], srcDim, model, dim) {
					keepDocs = append(keepDocs, d)
					keepVecs = append(keepVecs, vecs[i])
				} else {
					embedDocs = append(embedDocs, d)
				}
			}
			if len(keepDocs) > 0 {
				if err := importer.IndexVectors(ctx, dest, keepDocs, keepVecs); err != nil {
					return fmt.Errorf("import %s: %w", dest, err)
				}
				res.Reused += len(keepDocs)
			}
			if len(embedDocs) > 0 {
				if err := s.provider.Index(ctx, dest, embedDocs); err != nil {
					return fmt.Errorf("import %s (embedding %d chunks with %s): %w", dest, len(embedDocs), model, err)
				}
				res.Reembedded += len(embedDocs)
			}
			res.Points += len(docs)
			if progress != nil {
				progress(dest, res.Points, p.Points)
			}
			return nil
		})
		results = append(results, res)
		if err != nil {
			return results, err
		}
		s.events.Publish(Event{Type: "project_imported", Timestamp: time.Now(), Data: map[string]any{
			"project_id": dest, "source_project": p.ProjectID, "count": res.Points, "reused": res.Reused, "reembedded": res.Reembedded}})
	}
	return results, nil
}

// ImportSummary is a one-line account of an import ("Imported 120 chunks
// into 2 projects: 100 kept their vectors, 20 were embedded again").
func ImportSummary(results []ImportResult) string {
	var points, reused, reembedded int
	for _, r := range results {
		points += r.Points
		reused += r.Reused
		reembedded += r.Reembedded
	}
	s := fmt.Sprintf("Imported %d chunks into %d project", points, len(results))
	if len(results) != 1 {
		s += "s"
	}
	switch {
	case reembedded == 0:
		return s + ", all with their vectors (nothing re-embedded)"
	case reused == 0:
		return s + fmt.Sprintf(", all %d embedded again", reembedded)
	}
	return s + fmt.Sprintf(": %d kept their vectors, %d were embedded again", reused, reembedded)
}
