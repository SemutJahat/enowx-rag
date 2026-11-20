package rag

import (
	"context"
	"errors"
)

// UnconfiguredEmbedder stands in for an embedder that can't be built yet
// (e.g. the Voyage API key is missing on a fresh server). It reports the
// configured model and size, so the store opens unchanged and the dashboard
// can show and browse what is stored, while every embedding call fails with
// a message saying what to set.
type UnconfiguredEmbedder struct {
	Model  string
	Dim    int
	Reason string
}

// ErrEmbedderNotConfigured wraps every UnconfiguredEmbedder failure.
var ErrEmbedderNotConfigured = errors.New("embedder not configured")

func (u *UnconfiguredEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return nil, errors.Join(ErrEmbedderNotConfigured, errors.New(u.Reason))
}

func (u *UnconfiguredEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	return nil, errors.Join(ErrEmbedderNotConfigured, errors.New(u.Reason))
}

func (u *UnconfiguredEmbedder) VectorSize() int {
	if u.Dim > 0 {
		return u.Dim
	}
	return 1024
}

func (u *UnconfiguredEmbedder) ModelName() string { return u.Model }
