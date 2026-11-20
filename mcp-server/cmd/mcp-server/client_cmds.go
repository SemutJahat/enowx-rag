package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/enowdev/enowx-rag/mcp-server/pkg/config"
	"github.com/enowdev/enowx-rag/mcp-server/pkg/core"
	"github.com/enowdev/enowx-rag/mcp-server/pkg/httpapi"
	_ "modernc.org/sqlite"
)

// Client-side commands for using an enowx-rag server (e.g. on a VPS):
//
//	enowx-rag connect --remote URL --token T   configure MCP clients (client mode)
//	enowx-rag push    --remote URL --token T   import the local SQLite memory

// remoteFlags are the flags both commands share; they default to
// RAG_REMOTE_URL / RAG_REMOTE_TOKEN (or remote_url / remote_token in config.yaml).
type remoteFlags struct {
	url, token *string
}

func addRemoteFlags(fs *flag.FlagSet) remoteFlags {
	cfg, _ := config.Resolve()
	defURL, defToken := "", ""
	if cfg != nil {
		defURL, defToken = cfg.RemoteURL, cfg.RemoteToken
	}
	if defToken == "" {
		defToken = os.Getenv("RAG_ADMIN_TOKEN")
	}
	return remoteFlags{
		url:   fs.String("remote", defURL, "URL of the enowx-rag server, e.g. https://rag.example.com (env RAG_REMOTE_URL)"),
		token: fs.String("token", defToken, "the server's admin token (env RAG_REMOTE_TOKEN)"),
	}
}

func (rf remoteFlags) check() (string, string, error) {
	u := strings.TrimRight(strings.TrimSpace(*rf.url), "/")
	if u == "" {
		return "", "", errors.New("--remote is required (the URL of your enowx-rag server, e.g. https://rag.example.com)")
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = "https://" + u
	}
	if _, err := url.Parse(u); err != nil {
		return "", "", fmt.Errorf("--remote %q is not a URL: %v", u, err)
	}
	return strings.TrimSuffix(u, "/mcp"), strings.TrimSpace(*rf.token), nil
}

// apiClient talks to the server's HTTP API.
type apiClient struct {
	base, token, host string
	http              *http.Client
}

func newAPIClient(base, token string) *apiClient {
	host, _ := os.Hostname()
	return &apiClient{base: base, token: token, host: host, http: &http.Client{Timeout: 0}}
}

func (c *apiClient) do(ctx context.Context, method, path string, body io.Reader, hdr map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("X-Enowx-Client", c.host)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach %s: %v", c.base, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("the server rejected the token (401) — use the admin token you log in to the dashboard with")
	}
	if resp.StatusCode == http.StatusNotFound && strings.HasPrefix(path, "/api/import") {
		return fmt.Errorf("the server at %s has no import API — update it to this version of enowx-rag", c.base)
	}
	if resp.StatusCode >= 300 {
		var e struct{ Error, Hint string }
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			if e.Hint != "" {
				return fmt.Errorf("%s (%s)", e.Error, e.Hint)
			}
			return errors.New(e.Error)
		}
		return fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// checkServer verifies the server answers and the token works; it returns the
// server's version.
func (c *apiClient) checkServer(ctx context.Context) (string, error) {
	var health struct {
		Version      string `json:"version"`
		AuthRequired bool   `json:"auth_required"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/health", nil, nil, &health); err != nil {
		return "", fmt.Errorf("%v — is the URL right and the server running?", err)
	}
	if health.AuthRequired && c.token == "" {
		return health.Version, errors.New("the server requires a token: pass --token")
	}
	if err := c.do(ctx, http.MethodGet, "/api/stats", nil, nil, nil); err != nil {
		return health.Version, err
	}
	return health.Version, nil
}

// ---- connect ----

func runConnect(args []string) int {
	fs := flag.NewFlagSet("connect", flag.ExitOnError)
	rf := addRemoteFlags(fs)
	clients := fs.String("client", "", "comma-separated clients to configure (default: every client found here); ids: "+clientIDs())
	printOnly := fs.Bool("print", false, "only print the config to paste, write nothing")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: enowx-rag connect --remote URL --token TOKEN [--client claude-code,cursor] [--print]

Configures your MCP clients to use the enowx-rag server in client mode: this
machine scans and chunks your project folders, the server embeds, stores and
searches them (and its dashboard shows everything). Existing client configs
are merged, with a .bak backup.

`)
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	base, token, err := rf.check()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	api := newAPIClient(base, token)
	version, err := api.checkServer(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	fmt.Printf("✓ %s answers (enowx-rag %s) and accepts the token\n", base, version)

	exe, err := os.Executable()
	if err != nil {
		exe = "enowx-rag"
	} else if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	var targets []httpapi.ClientInfo
	all := httpapi.MCPClients()
	if *clients != "" {
		want := map[string]bool{}
		for _, id := range strings.Split(*clients, ",") {
			want[strings.TrimSpace(id)] = true
		}
		for _, c := range all {
			if want[c.ID] {
				targets = append(targets, c)
				delete(want, c.ID)
			}
		}
		for id := range want {
			fmt.Fprintf(os.Stderr, "error: unknown client %q (known: %s)\n", id, clientIDs())
			return 2
		}
	} else {
		for _, c := range all {
			if c.Present {
				targets = append(targets, c)
			}
		}
	}

	if *printOnly || len(targets) == 0 {
		if len(targets) == 0 {
			fmt.Println("No MCP client config found on this machine. Add this to your client's MCP config:")
			targets = []httpapi.ClientInfo{{ID: "claude-desktop", Label: "any client (mcpServers JSON)"}}
		}
		for _, c := range targets {
			path, content, err := httpapi.ClientModeSnippet(c.ID, exe, base, token)
			if err != nil {
				continue
			}
			fmt.Printf("\n# %s — %s\n%s\n", c.Label, path, content)
		}
		return 0
	}

	failed := 0
	for _, c := range targets {
		path, backedUp, err := httpapi.InstallClientMode(c.ID, exe, base, token)
		if err != nil {
			fmt.Fprintf(os.Stderr, "✗ %s: %v\n", c.Label, err)
			failed++
			continue
		}
		note := ""
		if backedUp {
			note = " (previous config saved as .bak)"
		}
		fmt.Printf("✓ %s: %s%s\n", c.Label, path, note)
	}
	fmt.Printf(`
Restart your MCP clients to pick it up. Agents keep using the same tools
(rag_index_project with a local folder works: it is scanned here, stored on
the server). To bring this machine's existing memory to the server:

  enowx-rag push --remote %s --token <TOKEN>
`, base)
	if failed > 0 {
		return 1
	}
	return 0
}

func clientIDs() string {
	ids := []string{}
	for _, c := range httpapi.MCPClients() {
		ids = append(ids, c.ID)
	}
	return strings.Join(ids, ", ")
}

// ---- push ----

type importPlan struct {
	UploadID string                   `json:"upload_id"`
	Size     int64                    `json:"size_bytes"`
	Projects []core.ImportPlanProject `json:"projects"`
	Estimate struct {
		Points        int     `json:"points"`
		ReusePoints   int     `json:"reuse_points"`
		ReembedPoints int     `json:"reembed_points"`
		ReembedTokens int64   `json:"reembed_tokens"`
		CostUSD       float64 `json:"reembed_cost_usd"`
		Note          string  `json:"note"`
	} `json:"estimate"`
}

type jobState struct {
	ID      string         `json:"id"`
	State   string         `json:"state"`
	Message string         `json:"message"`
	Done    int            `json:"done"`
	Total   int            `json:"total"`
	Result  map[string]any `json:"result"`
	Error   string         `json:"error"`
}

func runPush(args []string) int {
	fs := flag.NewFlagSet("push", flag.ExitOnError)
	rf := addRemoteFlags(fs)
	file := fs.String("file", "", "the SQLite store to push (default: this machine's store, ~/.enowx-rag/rag.db)")
	var projects multiFlag
	fs.Var(&projects, "project", "push only this project (repeatable; default: all)")
	mode := fs.String("mode", "merge", "merge (add/update chunks) or replace (delete the server's copy of each project first)")
	yes := fs.Bool("yes", false, "don't ask before chunks are embedded again (costs Voyage tokens)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: enowx-rag push --remote URL --token TOKEN [--project ID]... [--mode merge|replace] [--yes]

Imports this machine's SQLite memory into the enowx-rag server (any store,
e.g. pgvector). Vectors are reused when the server embeds with a compatible
model at the same size; otherwise the server embeds the chunks again.
Point IDs and metadata are kept, so later syncs stay incremental.

`)
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	base, token, err := rf.check()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	if *mode != "merge" && *mode != "replace" {
		fmt.Fprintln(os.Stderr, "error: --mode must be merge or replace")
		return 2
	}
	src := *file
	if src == "" {
		cfg, _ := config.Resolve()
		if cfg == nil {
			cfg = config.Default()
		}
		src = cfg.StorePath()
	}
	if st, err := os.Stat(src); err != nil || st.Size() == 0 {
		fmt.Fprintf(os.Stderr, "error: no SQLite store at %s (pass --file)\n", src)
		return 1
	}

	ctx := context.Background()
	api := newAPIClient(base, token)
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	version, err := api.checkServer(cctx)
	cancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	fmt.Printf("✓ %s (enowx-rag %s)\n", base, version)

	// A consistent copy, even while an MCP server writes to the store.
	snap, err := snapshotSQLite(src)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: snapshot:", err)
		return 1
	}
	defer os.Remove(snap)

	plan, err := uploadSnapshot(ctx, api, snap)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: upload:", err)
		return 1
	}
	discard := func() {
		_ = api.do(context.Background(), http.MethodDelete, "/api/import/upload/"+plan.UploadID, nil, nil, nil)
	}

	selected := map[string]bool{}
	for _, p := range projects {
		selected[p] = true
	}
	fmt.Printf("\n%-28s %8s  %-22s %s\n", "PROJECT", "CHUNKS", "EMBEDDED WITH", "ON THE SERVER")
	reembed := 0
	for _, p := range plan.Projects {
		if len(selected) > 0 && !selected[p.ProjectID] {
			continue
		}
		delete(selected, p.ProjectID)
		how := "vectors reused"
		if !p.ReuseVectors {
			how = fmt.Sprintf("%d re-embedded", p.Points-p.ReusePoints)
			reembed += p.Points - p.ReusePoints
		}
		exists := ""
		if p.Exists {
			exists = " (project exists: " + *mode + ")"
		}
		fmt.Printf("%-28s %8d  %-22s %s%s\n", p.ProjectID, p.Points, fmt.Sprintf("%s/%d", p.EmbedModel, p.EmbedDim), how, exists)
	}
	for missing := range selected {
		fmt.Fprintf(os.Stderr, "error: project %q is not in %s\n", missing, src)
		discard()
		return 1
	}
	fmt.Println()
	if plan.Estimate.Note != "" {
		fmt.Println(plan.Estimate.Note)
	}
	if reembed > 0 && !*yes {
		if !isTerminal(os.Stdin) {
			fmt.Fprintln(os.Stderr, "error: chunks must be embedded again (Voyage tokens); re-run with --yes to go ahead")
			discard()
			return 1
		}
		fmt.Print("Go ahead? [y/N] ")
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			fmt.Println("Cancelled.")
			discard()
			return 1
		}
	}

	var started struct {
		Job jobState `json:"job"`
	}
	body, _ := json.Marshal(map[string]any{"upload_id": plan.UploadID, "projects": []string(projects), "mode": *mode})
	if err := api.do(ctx, http.MethodPost, "/api/import/start", bytes.NewReader(body),
		map[string]string{"Content-Type": "application/json"}, &started); err != nil {
		fmt.Fprintln(os.Stderr, "error: start import:", err)
		return 1
	}
	job, err := followJob(ctx, api, started.Job.ID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "\nerror:", err)
		return 1
	}
	fmt.Printf("\n✓ %s\n", job.Message)

	// Tell the server where each project's code lives on this machine, so
	// its dashboard shows the sources.
	if reg, err := core.NewDirRegistry(config.ProjectDirsPath()); err == nil {
		for _, p := range plan.Projects {
			if len(projects) > 0 && !projects.has(p.ProjectID) {
				continue
			}
			for _, dir := range reg.Dirs(p.ProjectID) {
				b, _ := json.Marshal(map[string]any{"host": api.host, "dir": dir, "result": map[string]int{"imported": p.Points}})
				_ = api.do(ctx, http.MethodPost, "/api/projects/"+url.PathEscape(p.ProjectID)+"/sources", bytes.NewReader(b),
					map[string]string{"Content-Type": "application/json"}, nil)
			}
		}
	}
	fmt.Printf(`
Next: make this machine's agents use the server (client mode) so new work
lands there too:

  enowx-rag connect --remote %s --token <TOKEN>
`, base)
	return 0
}

// snapshotSQLite copies the store at src into a temporary file with VACUUM
// INTO: a consistent, compact copy even while another process writes.
func snapshotSQLite(src string) (string, error) {
	db, err := sql.Open("sqlite", "file:"+src+"?_pragma=busy_timeout(10000)")
	if err != nil {
		return "", err
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM points`).Scan(&n); err != nil {
		return "", fmt.Errorf("%s is not an enowx-rag store: %v", src, err)
	}
	if n == 0 {
		return "", fmt.Errorf("%s holds no chunks", src)
	}
	tmp, err := os.CreateTemp("", "enowx-rag-push-*.db")
	if err != nil {
		return "", err
	}
	path := tmp.Name()
	tmp.Close()
	os.Remove(path) // VACUUM INTO wants a new file
	if _, err := db.Exec(`VACUUM INTO ?`, path); err != nil {
		os.Remove(path)
		return "", err
	}
	fmt.Printf("✓ snapshot of %s: %d chunks\n", src, n)
	return path, nil
}

// uploadSnapshot gzips and uploads the snapshot, printing progress.
func uploadSnapshot(ctx context.Context, api *apiClient, path string) (*importPlan, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, _ := f.Stat()
	pr, pw := io.Pipe()
	go func() {
		zw := gzip.NewWriter(pw)
		_, err := io.Copy(zw, &progressReader{r: f, total: st.Size(), label: "uploading"})
		if cerr := zw.Close(); err == nil {
			err = cerr
		}
		pw.CloseWithError(err)
	}()
	var plan importPlan
	err = api.do(ctx, http.MethodPost, "/api/import/upload", pr,
		map[string]string{"Content-Type": "application/octet-stream", "Content-Encoding": "gzip"}, &plan)
	fmt.Println()
	if err != nil {
		return nil, err
	}
	return &plan, nil
}

// followJob polls a server job until it finishes, printing its progress.
func followJob(ctx context.Context, api *apiClient, id string) (jobState, error) {
	var job jobState
	for {
		if err := api.do(ctx, http.MethodGet, "/api/jobs/"+id, nil, nil, &job); err != nil {
			return job, err
		}
		line := job.Message
		if job.Total > 0 {
			line = fmt.Sprintf("[%3d%%] %s", job.Done*100/job.Total, job.Message)
		}
		fmt.Printf("\r\033[K%s", line)
		switch job.State {
		case "done":
			return job, nil
		case "failed":
			return job, errors.New(job.Error)
		}
		time.Sleep(time.Second)
	}
}

type progressReader struct {
	r            io.Reader
	total, done  int64
	label        string
	lastPrinted  time.Time
	printedFinal bool
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	if time.Since(p.lastPrinted) > 200*time.Millisecond || (err == io.EOF && !p.printedFinal) {
		p.lastPrinted = time.Now()
		p.printedFinal = err == io.EOF
		pct := int64(100)
		if p.total > 0 {
			pct = p.done * 100 / p.total
		}
		fmt.Printf("\r\033[K%s %.1f MB (%d%%)", p.label, float64(p.done)/1e6, pct)
	}
	return n, err
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }
func (m multiFlag) has(v string) bool {
	for _, x := range m {
		if x == v {
			return true
		}
	}
	return false
}

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
