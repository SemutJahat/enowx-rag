package main

import (
	"testing"

	"github.com/enowdev/enowx-rag/mcp-server/pkg/config"
)

// A client-mode machine keeps no local database: the server records metrics.
func TestLocalMetricsPath(t *testing.T) {
	if got := localMetricsPath(true); got != "" {
		t.Fatalf("client mode: localMetricsPath = %q, want no local metrics file", got)
	}
	if got, want := localMetricsPath(false), config.MetricsDBPath(); got != want {
		t.Fatalf("local mode: localMetricsPath = %q, want %q", got, want)
	}
}
