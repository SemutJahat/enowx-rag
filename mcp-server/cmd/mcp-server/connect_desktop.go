package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/enowdev/enowx-rag/mcp-server/pkg/httpapi"
)

// claudeDesktopRunning reports whether the Claude desktop app runs now. The
// app keeps claude_desktop_config.json in memory and writes it back while it
// runs, so an edit made meanwhile is undone, and it starts that file's
// servers for its Code sessions too.
var claudeDesktopRunning = func() bool {
	switch runtime.GOOS {
	case "darwin":
		// ps, not pgrep: pgrep skips its own ancestors, and connect may run
		// inside a Claude Code session of the app itself.
		out, err := exec.Command("ps", "-axco", "comm=").Output()
		return err == nil && psListed(out, "Claude")
	case "windows":
		out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq Claude.exe", "/NH").Output()
		return err == nil && tasklistListed(out, "Claude.exe")
	}
	return false
}

// psListed reports whether `ps -co comm=` output has a process named exactly
// name ("Claude", not "Claude Helper").
func psListed(out []byte, name string) bool {
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == name {
			return true
		}
	}
	return false
}

// tasklistListed reports whether `tasklist /NH` output has a line for the
// image name (its first column).
func tasklistListed(out []byte, image string) bool {
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) > 0 && strings.EqualFold(f[0], image) {
			return true
		}
	}
	return false
}

// withoutRunningDesktop drops Claude Desktop from targets while its app runs
// and returns the warning to print, or "" when nothing was dropped.
func withoutRunningDesktop(targets []httpapi.ClientInfo, running bool, rerun string) ([]httpapi.ClientInfo, string) {
	if !running {
		return targets, ""
	}
	kept := make([]httpapi.ClientInfo, 0, len(targets))
	dropped := false
	for _, c := range targets {
		if c.ID == "claude-desktop" {
			dropped = true
			continue
		}
		kept = append(kept, c)
	}
	if !dropped {
		return targets, ""
	}
	return kept, fmt.Sprintf(`! Claude Desktop: skipped, the Claude app is running. It keeps its config in
  memory and writes it back, so a change made now would be undone. Quit Claude
  completely (Cmd+Q; on Windows, File → Exit), then run from a terminal outside it:

    %s
`, rerun)
}
