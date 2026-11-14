package rag

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// staleMeta is the metadata of a point first embedded by another model: what a
// migration or a re-embed hands back to Index.
func staleMeta() map[string]string {
	return map[string]string{"source_file": "a.go", "embed_model": "old-model", "embed_dim": "7"}
}

func newModelEmbedder() *mockModelNamerEmbedder {
	return &mockModelNamerEmbedder{mockPlainEmbedder: mockPlainEmbedder{vectorSize: 3}, modelName: "new-model"}
}

// A provider stamps the model and size it actually embedded with, even when
// the document's metadata still names the model that embedded it before.
// Otherwise a migrated or re-embedded point stays labeled with a model that
// never produced its vector.
func TestIndexStampsTheEmbedderItUsed(t *testing.T) {
	ctx := context.Background()

	t.Run("sqlite", func(t *testing.T) {
		p, err := NewSQLiteProvider(filepath.Join(t.TempDir(), "rag.db"), newModelEmbedder())
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		if err := p.Index(ctx, "proj", []Document{{ID: "a", Content: "hello", Meta: staleMeta()}}); err != nil {
			t.Fatal(err)
		}
		docs, err := p.ExportPoints(ctx, "proj")
		if err != nil || len(docs) != 1 {
			t.Fatalf("export: %v %v", docs, err)
		}
		if docs[0].Meta["embed_model"] != "new-model" || docs[0].Meta["embed_dim"] != "3" {
			t.Errorf("stamped %q dim %q, want new-model dim 3", docs[0].Meta["embed_model"], docs[0].Meta["embed_dim"])
		}
		if docs[0].Meta["source_file"] != "a.go" {
			t.Errorf("caller metadata lost: %v", docs[0].Meta)
		}
	})

	t.Run("qdrant", func(t *testing.T) {
		var payload map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Points []struct {
					Payload map[string]any `json:"payload"`
				} `json:"points"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body.Points) > 0 {
				payload = body.Points[0].Payload
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"status": "completed"}})
		}))
		defer srv.Close()
		p, err := NewQdrantProvider(ctx, srv.URL, "", newModelEmbedder())
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		if err := p.Index(ctx, "proj", []Document{{ID: "a", Content: "hello", Meta: staleMeta()}}); err != nil {
			t.Fatal(err)
		}
		if payload["embed_model"] != "new-model" || payload["embed_dim"] != float64(3) {
			t.Errorf("stamped %v dim %v, want new-model dim 3", payload["embed_model"], payload["embed_dim"])
		}
	})

	t.Run("chroma", func(t *testing.T) {
		var meta map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Metadatas []map[string]any `json:"metadatas"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body.Metadatas) > 0 {
				meta = body.Metadatas[0]
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "c1", "name": "project_proj"})
		}))
		defer srv.Close()
		p := NewChromaProvider(srv.URL, newModelEmbedder())
		defer p.Close()
		if err := p.Index(ctx, "proj", []Document{{ID: "a", Content: "hello", Meta: staleMeta()}}); err != nil {
			t.Fatal(err)
		}
		if meta["embed_model"] != "new-model" || meta["embed_dim"] != float64(3) {
			t.Errorf("stamped %v dim %v, want new-model dim 3", meta["embed_model"], meta["embed_dim"])
		}
	})
}
