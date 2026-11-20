package rag

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

// PostgreSQL refuses text that is not valid UTF-8 or holds a NUL byte, which
// SQLite stores as is: a file indexed in Windows-1252 (as found in test
// fixtures) must not stop indexing or an import into pgvector.
func TestPGVectorStoresInvalidUTF8(t *testing.T) {
	skipIfNoPostgres(t)
	ctx := context.Background()
	p, err := NewPGVectorProvider(ctx, pgvectorDSN, &mockPlainEmbedder{vectorSize: 3}, "project_memory_test")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	projectID := "test_invalid_utf8"
	cleanupTestTable(t, projectID)
	defer cleanupTestTable(t, projectID)

	latin1 := "<title>Berita Caf\xe9</title>\x00end"
	meta := map[string]string{"source_file": "feeds/caf\xe9.xml", "note\x00": "a\x00b"}

	if err := p.IndexVectors(ctx, projectID, []Document{{ID: "imported", Content: latin1, Meta: meta}},
		[][]float32{{0.1, 0.2, 0.3}}); err != nil {
		t.Fatalf("IndexVectors: %v", err)
	}
	if err := p.Index(ctx, projectID, []Document{{ID: "indexed", Content: latin1, Meta: meta}}); err != nil {
		t.Fatalf("Index: %v", err)
	}
	if err := p.UpdatePayload(ctx, projectID, []Document{{ID: "imported", Content: latin1 + " moved", Meta: meta}}); err != nil {
		t.Fatalf("UpdatePayload: %v", err)
	}

	docs, err := p.ExportPoints(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("%d points stored, want 2", len(docs))
	}
	for _, d := range docs {
		if !utf8.ValidString(d.Content) || strings.ContainsRune(d.Content, 0) || !strings.Contains(d.Content, "Berita Caf") {
			t.Errorf("%s: content %q", d.ID, d.Content)
		}
		if got := d.Meta["source_file"]; !utf8.ValidString(got) || !strings.HasPrefix(got, "feeds/caf") {
			t.Errorf("%s: source_file %q", d.ID, got)
		}
		if d.Meta["note"] != "ab" {
			t.Errorf("%s: meta %v", d.ID, d.Meta)
		}
	}
}
