package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enowdev/enowx-rag/mcp-server/pkg/httpapi"
)

// connect leaves Claude Desktop's config alone while the app runs, still
// configures the other clients, and exits 1; with the app closed it writes it.
func TestConnectSkipsClaudeDesktopWhileItRuns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/health":
			fmt.Fprint(w, `{"ok":true,"version":"test","auth_required":true}`)
		case "/api/stats":
			fmt.Fprint(w, `{"total_projects":0}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	orig := claudeDesktopRunning
	defer func() { claudeDesktopRunning = orig }()

	for _, tc := range []struct {
		running     bool
		wantCode    int
		wantDesktop bool
	}{{running: true, wantCode: 1, wantDesktop: false}, {running: false, wantCode: 0, wantDesktop: true}} {
		home := t.TempDir()
		t.Setenv("HOME", home)
		claudeDesktopRunning = func() bool { return tc.running }

		code := runConnect([]string{"--remote", srv.URL, "--token", "t0k", "--client", "claude-desktop,cursor"})
		if code != tc.wantCode {
			t.Errorf("app running=%v: exit %d, want %d", tc.running, code, tc.wantCode)
		}
		_, err := os.Stat(filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json"))
		if gotDesktop := err == nil; gotDesktop != tc.wantDesktop {
			t.Errorf("app running=%v: Claude Desktop config written = %v, want %v", tc.running, gotDesktop, tc.wantDesktop)
		}
		if _, err := os.Stat(filepath.Join(home, ".cursor", "mcp.json")); err != nil {
			t.Errorf("app running=%v: Cursor config not written: %v", tc.running, err)
		}
	}
}

func ids(cs []httpapi.ClientInfo) string {
	var out []string
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return strings.Join(out, ",")
}

// While the Claude app runs it writes its in-memory config back over
// claude_desktop_config.json, so connect must not write it then.
func TestWithoutRunningDesktop(t *testing.T) {
	both := []httpapi.ClientInfo{{ID: "claude-code"}, {ID: "claude-desktop", Label: "Claude Desktop"}}
	rerun := "enowx-rag connect --remote https://rag.example.com --token <TOKEN> --client claude-desktop"

	got, warn := withoutRunningDesktop(both, true, rerun)
	if ids(got) != "claude-code" {
		t.Fatalf("app running: targets = %q, want claude-code only", ids(got))
	}
	for _, want := range []string{"Claude Desktop", "running", "Quit", rerun} {
		if !strings.Contains(warn, want) {
			t.Errorf("app running: warning lacks %q:\n%s", want, warn)
		}
	}

	if got, warn := withoutRunningDesktop(both, false, rerun); ids(got) != "claude-code,claude-desktop" || warn != "" {
		t.Errorf("app not running: targets = %q, warning %q; want both, no warning", ids(got), warn)
	}
	codeOnly := []httpapi.ClientInfo{{ID: "claude-code"}}
	if got, warn := withoutRunningDesktop(codeOnly, true, rerun); ids(got) != "claude-code" || warn != "" {
		t.Errorf("desktop not selected: targets = %q, warning %q; want claude-code, no warning", ids(got), warn)
	}
}

func TestPSListed(t *testing.T) {
	ps := []byte("launchd\nClaude Helper\nClaude Helper (Renderer)\nclaude\n  Claude  \nzsh\n")
	if !psListed(ps, "Claude") {
		t.Error("Claude is listed (with padding) but not found")
	}
	if psListed([]byte("Claude Helper\nClaude Helper (Renderer)\nclaude\n"), "Claude") {
		t.Error("only helpers and the claude CLI run, but the app was reported running")
	}
}

func TestTasklistListed(t *testing.T) {
	// tasklist on Windows prints the image name first on its line.
	tl := []byte("\r\nClaude.exe                   4242 Console    1    210,000 K\r\n")
	if !tasklistListed(tl, "Claude.exe") {
		t.Error("Claude.exe in tasklist output not found")
	}
	if tasklistListed([]byte("INFO: No tasks are running which match the specified criteria.\r\n"), "Claude.exe") {
		t.Error("tasklist reported no match, but the app was reported running")
	}
}
