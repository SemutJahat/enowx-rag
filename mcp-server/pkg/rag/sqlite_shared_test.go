package rag

import (
	"context"
	"path/filepath"
	"testing"
)

// openShared opens two providers on one store file, as two Claude sessions
// each run their own server over ~/.enowx-rag/rag.db.
func openShared(t *testing.T) (a, b *SQLiteProvider) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rag.db")
	var err error
	if a, err = NewSQLiteProvider(path, wordsEmbedder{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	if b, err = NewSQLiteProvider(path, wordsEmbedder{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return a, b
}

// cachedFirst is the address of p's first cached vector of proj: the same
// address after a search means the vectors were reused, not read again.
func cachedFirst(t *testing.T, p *SQLiteProvider) *cached {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.cache["proj"]) == 0 {
		t.Fatal("no vectors cached after a search")
	}
	return &p.cache["proj"][0]
}

func ids(rs []Result) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}

// After one process has read a project's vectors, a search there still sees
// the points another process writes, and doesn't fail on (or return) the
// points another process deletes.
func TestSQLiteSearchSeesOtherProcessWrites(t *testing.T) {
	ctx := context.Background()
	a, b := openShared(t)
	if err := a.Index(ctx, "proj", []Document{{ID: "old", Content: "the http server routes requests"}}); err != nil {
		t.Fatal(err)
	}
	if got, err := b.SemanticSearch(ctx, "proj", "http server", 3); err != nil || len(got) != 1 {
		t.Fatalf("first search: %v %v", ids(got), err)
	}

	if err := a.Index(ctx, "proj", []Document{{ID: "new", Content: "walrus config loader parses yaml"}}); err != nil {
		t.Fatal(err)
	}
	got, err := b.SemanticSearch(ctx, "proj", "walrus config loader", 3)
	if err != nil || len(got) == 0 || got[0].ID != "new" {
		t.Fatalf("after the other process wrote a point: %v %v, want new first", ids(got), err)
	}

	if err := a.DeletePoints(ctx, "proj", []string{"old"}); err != nil {
		t.Fatal(err)
	}
	for _, search := range []func(context.Context, string, string, int) ([]Result, error){b.SemanticSearch, b.SemanticSearchHybrid} {
		got, err := search(ctx, "proj", "http server routes", 3)
		if err != nil {
			t.Fatalf("search after the other process deleted a point: %v", err)
		}
		for _, r := range got {
			if r.ID == "old" {
				t.Errorf("deleted point still returned: %v", ids(got))
			}
		}
	}
}

// A point another process deletes between the vector ranking and reading
// the results' text is left out, instead of failing the whole search.
func TestSQLiteFillSkipsVanishedPoints(t *testing.T) {
	ctx := context.Background()
	a, _ := openShared(t)
	if err := a.Index(ctx, "proj", []Document{{ID: "kept", Content: "the http server routes requests"}}); err != nil {
		t.Fatal(err)
	}
	got, err := a.fill(ctx, "proj", []Result{{ID: "gone", Score: 0.9}, {ID: "kept", Score: 0.5}})
	if err != nil || len(got) != 1 || got[0].ID != "kept" || got[0].Content == "" {
		t.Fatalf("fill = %+v %v, want only kept, with its text", got, err)
	}
}

// If the connection watching for commits breaks, searches stay right (the
// vectors are read again) and a new watch connection brings the cache back.
func TestSQLiteWatchConnectionRecovers(t *testing.T) {
	ctx := context.Background()
	a, b := openShared(t)
	if err := a.Index(ctx, "proj", []Document{{ID: "x", Content: "the http server routes requests"}}); err != nil {
		t.Fatal(err)
	}
	b.watch.Close()
	if got, err := b.SemanticSearch(ctx, "proj", "http server", 3); err != nil || len(got) != 1 {
		t.Fatalf("search with a broken watch connection: %v %v", ids(got), err)
	}
	if _, err := b.SemanticSearch(ctx, "proj", "http server", 3); err != nil {
		t.Fatal(err)
	}
	first := cachedFirst(t, b)
	if _, err := b.SemanticSearch(ctx, "proj", "routes requests", 3); err != nil {
		t.Fatal(err)
	}
	if again := cachedFirst(t, b); again != first {
		t.Error("cache not reused after the watch connection was replaced")
	}
}

// While nobody writes, the vectors read for a project are reused, not read
// again on every search.
func TestSQLiteSearchReusesVectorsWhenUnchanged(t *testing.T) {
	ctx := context.Background()
	a, b := openShared(t)
	if err := a.Index(ctx, "proj", []Document{{ID: "x", Content: "the http server routes requests"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SemanticSearch(ctx, "proj", "http server", 3); err != nil {
		t.Fatal(err)
	}
	first := cachedFirst(t, b)
	if _, err := b.SemanticSearch(ctx, "proj", "routes requests", 3); err != nil {
		t.Fatal(err)
	}
	if again := cachedFirst(t, b); again != first {
		t.Error("vectors read again though nothing was written")
	}
}
