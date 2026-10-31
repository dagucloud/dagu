// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec072_browser holds black-box conformance tests for the browser
// actions. They drive a real Chrome against a local page with a scripted
// OpenAI-compatible model, so no external service is needed.
package spec072_browser_test

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

const shopPage = `<!doctype html><html><head><title>Checkout</title></head><body>
<h1>Widget Checkout</h1>
<p id="status">Cart has 2 items</p>
<button onclick="document.getElementById('status').textContent='Order confirmed: A-100'">Place order</button>
<a href="/report.csv">Download report</a>
</body></html>`

// rowsPage greets with an alert while it loads and asks to confirm before
// it deletes the row.
const rowsPage = `<!doctype html><html><head><title>Rows</title></head><body>
<h1>Rows</h1>
<script>alert('Welcome back')</script>
<p id="status">1 row</p>
<button onclick="if (confirm('Delete the row?')) document.getElementById('status').textContent='Row deleted'">Delete row</button>
</body></html>`

// signinPage loads its script and images from hosts the blocked_requests
// fixture leaves out of allowed_domains, so it never shows "Signed in".
const signinPage = `<!doctype html><html><head><title>Sign in</title>
<script src="http://sso.blocked.test/login.js"></script></head><body>
<img src="http://cdn.blocked.test/logo.png"><img src="http://cdn.blocked.test/banner.png">
<p id="status">Signing in</p>
</body></html>`

const reportBody = "id,total\n1,10\n2,20\n"

// receiptsPage sends the files its hidden file input gets to the server,
// and says which arrived. Its button opens the file picker.
const receiptsPage = `<!doctype html><html><head><title>Receipts</title></head><body>
<h1>Receipts</h1>
<input id="files" type="file" multiple hidden>
<button onclick="document.getElementById('files').click()">Choose files</button>
<p id="status">No files yet</p>
<script>
let uploads = 0;
document.getElementById('files').addEventListener('change', async (event) => {
  const form = new FormData();
  for (const file of event.target.files) form.append('file', file);
  const response = await fetch(location.pathname, {method: 'POST', body: form});
  uploads++;
  document.getElementById('status').textContent = 'Upload ' + uploads + ': ' + (await response.text());
});
</script>
</body></html>`

// layoutPage has a Save button. With ?banner=1 a Dismiss button comes
// first, at the location Save had.
const layoutPage = `<!doctype html><html><head><title>Draft</title></head><body>
<p id="status">Draft</p>
<div>%s<button onclick="document.getElementById('status').textContent='Saved'">Save</button></div>
</body></html>`

// agentPage says whether the user agent its request came with, given as a
// JSON string, is the one its scripts see and names no headless browser,
// and whether the screen holds the browser's window.
const agentPage = `<!doctype html><html><head><title>Agent</title></head><body>
<p id="agent"></p>
<p id="screen"></p>
<script>
const sent = %s;
document.getElementById('agent').textContent = sent === navigator.userAgent && !sent.includes('Headless')
  ? 'user agent: headed' : 'user agent: ' + sent + ' / ' + navigator.userAgent;
document.getElementById('screen').textContent = screen.width >= outerWidth && screen.height >= outerHeight
  ? 'screen holds window' : 'screen ' + screen.width + 'x' + screen.height + ', window ' + outerWidth + 'x' + outerHeight;
</script>
</body></html>`

const layoutBanner = `<button onclick="document.getElementById('status').textContent='Banner closed'">Dismiss</button>`

// bidsPage is a site built of frames, as older ones are. Its Search button
// writes a "searching" window in place and posts into the results frame,
// whose answer comes after bidsAnswerDelay and closes the window as it
// loads. A search with no region is answered at once with an error that
// leaves the window open, as such sites do. Choose region opens a window
// to tick regions in, which stays until Register gives them to the search
// and closes it.
const bidsPage = `<!doctype html><html><head><title>Bids</title><script>
function openSearching() {
  top.searching = window.open("/bids/blank", "searching", "width=320,height=120");
  top.searching.document.write("<title>Searching</title><p>Searching, please wait</p><input name='indicator'>");
  top.searching.document.close();
}
function closeSearching() { if (top.searching) top.searching.close(); }
</script></head>
<frameset cols="200,*"><frame src="/bids/menu"><frame name="results" src="/bids/home"></frameset></html>`

const bidsMenuPage = `<!doctype html><html><body>
<form id="search" method="post" action="/bids/results" target="results"><input type="hidden" name="region" id="region"></form>
<p id="chosen">All regions</p>
<button onclick="top.openSearching(); document.getElementById('search').submit()">Search</button>
<button onclick="window.open('/bids/regions', 'regions', 'width=800,height=600')">Choose region</button>
<script>function setRegion(name) { document.getElementById('region').value = name; document.getElementById('chosen').textContent = name; }</script>
</body></html>`

const bidsRegionsPage = `<!doctype html><html><head><title>Regions</title></head><body>
<label><input type="checkbox" value="Chiba">Chiba</label>
<label><input type="checkbox" value="Funabashi">Funabashi</label>
<button onclick="opener.setRegion([...document.querySelectorAll('input:checked')].map(box => box.value).join(', ')); window.close()">Register</button>
</body></html>`

const bidsErrorPage = `<!doctype html><html><body><p>Input error: choose a region</p></body></html>`

const bidsResultsPage = `<!doctype html><html><body onload="top.closeSearching()">
<h1>Road repair</h1>
<p>Road repair in %s</p>
<button onclick="this.textContent = 'Opened'">Open</button>
</body></html>`

// bidsAnswerDelay outlasts the five seconds a page gets to settle after an
// action.
const bidsAnswerDelay = 7 * time.Second

// browserCommandTimeout bounds a command that starts a browser. Starting
// one takes tens of seconds when the conformance job loads the runner.
const browserCommandTimeout = 2 * time.Minute

// browserSlot lets one test run browsers at a time. The browser runtime gives
// its extension a fixed minute to start, which overlapping browser startups
// on a loaded CI runner can exceed together.
var browserSlot = make(chan struct{}, 1)

// Model request kinds, told apart by the response schema the browser
// runtime asks for.
const (
	kindAct       = "act"
	kindObserve   = "observe"
	kindStatement = "statement"
	kindExtract   = "extract"
	kindMetadata  = "metadata"
)

var (
	buttonPattern    = regexp.MustCompile(`\[(\d+-\d+)\] (?:button|link|checkbox): ([^\n]+)`)
	quotedPattern    = regexp.MustCompile(`'([^']+)'`)
	orderPattern     = regexp.MustCompile(`Order confirmed: (\S+)`)
	headingPattern   = regexp.MustCompile(`heading: ([^\n]+)`)
	pageMarkerPrefix = regexp.MustCompile(`(?s)^(.*?)(Accessibility Tree:|DOM:)(.*)$`)
)

// scriptedModel answers like a model reading the page it is sent: it picks
// the button named in an act instruction, judges quoted text against the
// page, and extracts values from the page text.
type scriptedModel struct {
	mu     sync.Mutex
	counts map[string]int
}

func startModel(t *testing.T) (*scriptedModel, string) {
	t.Helper()
	model := &scriptedModel{counts: map[string]int{}}
	server := httptest.NewServer(http.HandlerFunc(model.serve))
	t.Cleanup(server.Close)
	return model, server.URL
}

func (m *scriptedModel) count(kind string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counts[kind]
}

func (m *scriptedModel) total() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	total := 0
	for _, count := range m.counts {
		total += count
	}
	return total
}

func (m *scriptedModel) serve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Parameters json.RawMessage `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Tools) == 0 {
		http.Error(w, "expected a tool request", http.StatusBadRequest)
		return
	}
	user := ""
	for _, message := range req.Messages {
		if message.Role == "user" {
			user = message.Content
		}
	}
	instruction, page := user, ""
	if match := pageMarkerPrefix.FindStringSubmatch(user); match != nil {
		instruction, page = match[1], match[3]
	}
	schema := string(req.Tools[0].Function.Parameters)

	var kind, answer string
	switch {
	case strings.Contains(schema, `"elements"`):
		kind = kindObserve
		answer = `{"elements":[]}`
		for _, button := range buttonPattern.FindAllStringSubmatch(page, -1) {
			if strings.Contains(instruction, strings.TrimSpace(button[2])) {
				answer = `{"elements":[{"elementId":"` + button[1] + `","description":"button","method":"click","arguments":[]}]}`
				break
			}
		}
	case strings.Contains(schema, "elementId"):
		kind = kindAct
		answer = `{"action":null,"twoStep":false}`
		for _, button := range buttonPattern.FindAllStringSubmatch(page, -1) {
			if strings.Contains(instruction, strings.TrimSpace(button[2])) {
				answer = `{"action":{"elementId":"` + button[1] + `","description":"button","method":"click","arguments":[]},"twoStep":false}`
				break
			}
		}
	case strings.Contains(schema, `"completed"`):
		kind = kindMetadata
		answer = `{"completed":true,"progress":"done"}`
	case strings.Contains(schema, `"answer"`):
		kind = kindStatement
		holds := false
		if quoted := quotedPattern.FindStringSubmatch(instruction); quoted != nil {
			holds = strings.Contains(page, quoted[1])
		}
		answer = `{"answer":` + map[bool]string{true: "true", false: "false"}[holds] + `,"reason":"judged from the page text"}`
	case strings.Contains(schema, "order_number"):
		kind = kindExtract
		value := ""
		if match := orderPattern.FindStringSubmatch(page); match != nil {
			value = match[1]
		}
		answer = `{"order_number":"` + value + `"}`
	default:
		kind = kindExtract
		value := ""
		if match := headingPattern.FindStringSubmatch(page); match != nil {
			value = strings.TrimSpace(match[1])
		}
		answer = `{"heading":"` + value + `"}`
	}
	m.mu.Lock()
	m.counts[kind]++
	m.mu.Unlock()

	arguments, _ := json.Marshal(answer)
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"respond","arguments":`+string(arguments)+`}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`)
}

func startShop(t *testing.T) string {
	t.Helper()
	items := &itemPages{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rows" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, rowsPage)
			return
		}
		if r.URL.Path == "/signin" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, signinPage)
			return
		}
		if r.URL.Path == "/layout" {
			banner := ""
			if r.URL.Query().Get("banner") == "1" {
				banner = layoutBanner
			}
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprintf(w, layoutPage, banner)
			return
		}
		if r.URL.Path == "/guarded" {
			// Cloudflare marks every answer that is one of its checks.
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Cf-Mitigated", "challenge")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `<title>Just a moment...</title><p>Checking your browser</p>`)
			return
		}
		if r.URL.Path == "/passing" {
			if _, err := r.Cookie("passed"); err == nil {
				w.Header().Set("Content-Type", "text/html")
				_, _ = io.WriteString(w, shopPage)
				return
			}
			// A check that needs no person marks the browser and loads the
			// page again.
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Cf-Mitigated", "challenge")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `<title>Just a moment...</title><p>Checking your browser</p>
<script>setTimeout(() => { document.cookie = "passed=1; path=/"; location.reload(); }, 1000);</script>`)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/bids") {
			serveBids(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/items/") {
			items.serve(w, r)
			return
		}
		if r.URL.Path == "/agent" {
			sent, _ := json.Marshal(r.UserAgent())
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprintf(w, agentPage, sent)
			return
		}
		if r.URL.Path == "/report.csv" {
			w.Header().Set("Content-Type", "text/csv")
			w.Header().Set("Content-Disposition", `attachment; filename="report.csv"`)
			_, _ = io.WriteString(w, reportBody)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, shopPage)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// itemPages serves a page per foreach item at /items/page?item=N, and at
// /items/turn?item=N answers 200 once the page of item N-1 has been
// served, so an item's turn comes while the item before it shows its page.
type itemPages struct {
	mu     sync.Mutex
	served map[string]bool
}

func (p *itemPages) serve(w http.ResponseWriter, r *http.Request) {
	item := r.URL.Query().Get("item")
	p.mu.Lock()
	defer p.mu.Unlock()
	switch r.URL.Path {
	case "/items/page":
		if p.served == nil {
			p.served = map[string]bool{}
		}
		p.served[item] = true
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprintf(w, "<!doctype html><title>Item %s</title><p>Item %s</p>", html.EscapeString(item), html.EscapeString(item))
	case "/items/turn":
		index, err := strconv.Atoi(item)
		if err != nil || index > 0 && !p.served[strconv.Itoa(index-1)] {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}
}

// serveBids serves bidsPage and its frames and windows.
func serveBids(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	switch r.URL.Path {
	case "/bids":
		_, _ = io.WriteString(w, bidsPage)
	case "/bids/menu":
		_, _ = io.WriteString(w, bidsMenuPage)
	case "/bids/regions":
		_, _ = io.WriteString(w, bidsRegionsPage)
	case "/bids/home":
		_, _ = io.WriteString(w, "<p>Choose a region and search</p>")
	case "/bids/results":
		region := r.PostFormValue("region")
		if region == "" {
			_, _ = io.WriteString(w, bidsErrorPage)
			return
		}
		time.Sleep(bidsAnswerDelay)
		_, _ = fmt.Fprintf(w, bidsResultsPage, html.EscapeString(region))
	}
}

// requireChrome skips when no Chrome is installed, except in CI, where the
// runners provide one.
func requireChrome(t *testing.T) {
	t.Helper()
	if os.Getenv("CHROME_PATH") != "" {
		return
	}
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}
	case "windows":
		for _, root := range []string{os.Getenv("PROGRAMFILES"), os.Getenv("PROGRAMFILES(X86)"), os.Getenv("LOCALAPPDATA")} {
			if root != "" {
				candidates = append(candidates, filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"))
			}
		}
	default:
		for _, name := range []string{"google-chrome-stable", "google-chrome", "chromium-browser", "chromium"} {
			if path, err := exec.LookPath(name); err == nil {
				candidates = append(candidates, path)
			}
		}
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return
		}
	}
	if os.Getenv("CI") != "" {
		t.Fatal("Chrome is required in CI; set CHROME_PATH")
	}
	t.Skip("Chrome is not installed; set CHROME_PATH to run browser conformance tests")
}

// receivedFile is a file the receipts page sent.
type receivedFile struct {
	Name, Type, Content string
}

// receiptsServer serves the receipts page and keeps the files it sends.
type receiptsServer struct {
	mu       sync.Mutex
	received [][]receivedFile
}

func startReceipts(t *testing.T) (*receiptsServer, string) {
	t.Helper()
	receipts := &receiptsServer{}
	server := httptest.NewServer(http.HandlerFunc(receipts.serve))
	t.Cleanup(server.Close)
	return receipts, server.URL + "/receipts"
}

func (s *receiptsServer) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, receiptsPage)
		return
	}
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var files []receivedFile
	var names []string
	for _, header := range r.MultipartForm.File["file"] {
		file, err := header.Open()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		content, _ := io.ReadAll(file)
		_ = file.Close()
		files = append(files, receivedFile{Name: header.Filename, Type: header.Header.Get("Content-Type"), Content: string(content)})
		names = append(names, header.Filename)
	}
	s.mu.Lock()
	s.received = append(s.received, files)
	s.mu.Unlock()
	_, _ = io.WriteString(w, strings.Join(names, ", "))
}

func (s *receiptsServer) uploads() [][]receivedFile {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.received)
}

type browserEnv struct {
	dagu  *harness.Runner
	model *scriptedModel
	env   []string
}

func newBrowserEnv(t *testing.T) *browserEnv {
	t.Helper()
	requireChrome(t)
	browserSlot <- struct{}{}
	t.Cleanup(func() { <-browserSlot })
	model, modelURL := startModel(t)
	// The harness sets CI, where the browser runtime turns off the sandbox,
	// so the tests turn it off explicitly instead of being refused.
	env := []string{"SHOP_URL=" + startShop(t), "LLM_BASE_URL=" + modelURL, "DAGU_BROWSER_SANDBOX=false"}
	if runtime.GOOS == "windows" {
		// The harness points the profile folders at empty temporary paths;
		// Chrome on Windows needs the real ones to start.
		env = append(env, "USERPROFILE="+os.Getenv("USERPROFILE"), "APPDATA="+os.Getenv("APPDATA"))
	}
	return &browserEnv{
		dagu:  harness.NewRunner(t).WithCommandTimeout(browserCommandTimeout),
		model: model,
		env:   env,
	}
}

func TestBrowserRunCheckout(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	b.dagu.RunWithEnv(b.env, "start", "checkout.yaml").ExpectExitCode(0)

	b.dagu.ExpectTextFileContent("order.out", "A-100\n")
	b.dagu.ExpectFileContains("shots.out", "confirmation.png", "final.png")
	require.Equal(t, 1, b.model.count(kindAct))
}

// A file the page downloads is saved in the run artifacts before the step
// ends.
func TestBrowserDownload(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	b.dagu.RunWithEnv(b.env, "start", "download.yaml").ExpectExitCode(0)
	b.dagu.ExpectTextFileContent("report.out", reportBody)
}

// A goto to a file the site answers as a download ends as soon as the
// download begins, rather than waiting for a page that never comes, and
// the file is saved in the run artifacts.
func TestBrowserGotoDownload(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	b.dagu.RunWithEnv(b.env, "start", "download_goto.yaml").ExpectExitCode(0)
	b.dagu.ExpectTextFileContent("report.out", reportBody)
}

// A page that opens dialogs keeps working: the alert shown while it loads
// and the confirm raised by an act are both accepted.
func TestBrowserDialogs(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	b.dagu.RunWithEnv(b.env, "start", "dialogs.yaml").ExpectExitCode(0)
	require.Equal(t, 1, b.model.count(kindAct))
}

// A browser without a window sends the user agent the same browser sends
// with one, learned on the first run and kept for the next, and reports a
// screen that holds its window.
func TestBrowserLooksHeaded(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	for range 2 {
		result := b.dagu.RunWithEnv(b.env, "start", "looks_headed.yaml")
		result.ExpectExitCode(0)
		result.ExpectStderrNotContains("warning:")
	}
}

// A step that fails on a page the site answered with a bot check says so.
func TestBrowserNamesBotCheck(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	result := b.dagu.RunWithEnv(b.env, "start", "bot_check.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("answered with a Cloudflare bot check (HTTP 403) in place of the page")
}

// A bot check that passes on its own is waited out, so the step reads the
// page it stood in for.
func TestBrowserWaitsForBotCheckToPass(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	b.dagu.RunWithEnv(b.env, "start", "bot_check_passes.yaml").ExpectExitCode(0)
}

// With the sandbox on, a browser step fails before starting a browser where
// the browser runtime would turn the sandbox off, here because CI is set.
func TestBrowserSandboxNotSilentlyOff(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	env := slices.DeleteFunc(slices.Clone(b.env), func(entry string) bool {
		return strings.HasPrefix(entry, "DAGU_BROWSER_SANDBOX=")
	})
	result := b.dagu.RunWithEnv(env, "start", "extract.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("because CI is set", "DAGU_BROWSER_SANDBOX=false")
	require.Zero(t, b.model.total())
}

// A search that opens a "searching" window, which its answer closes, is
// waited out even when the answer takes longer than a page gets to settle,
// so the when, expect, extract, and act after it work on the results, not
// on the window. A window that stays, as one to tick regions in and
// register, is worked on as a page.
func TestBrowserSearchingWindow(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	b.dagu.RunWithEnv(b.env, "start", "searching_window.yaml").ExpectExitCode(0)
	b.dagu.ExpectTextFileContent("heading.out", "Road repair\n")
}

// A step that fails on a searching window the site left open over its
// error names the page behind the window and captures it.
func TestBrowserNamesThePageBehindAWindowLeftOpen(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	result := b.dagu.RunWithEnv(b.env, "start", "searching_window_error.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains(`the page read is a window that the page "Bids" opened and that is still open in front of it`, "failure-opener.png shows that page")
}

// The items of a foreach each drive a browser of their own: an item whose
// browser step starts while another item's browser is open leaves that
// browser alone, though both run the same step of the same DAG run.
func TestBrowserForeachItemsKeepTheirBrowsers(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	b.dagu.RunWithEnv(b.env, "start", "foreach_items.yaml").ExpectExitCode(0)
	data, err := os.ReadFile(b.dagu.ProjectPath("each.out"))
	require.NoError(t, err)
	var aggregate struct {
		Summary struct {
			Succeeded int `json:"succeeded"`
		} `json:"summary"`
		Items []struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(data, &aggregate))
	require.Equal(t, 2, aggregate.Summary.Succeeded, "items: %+v", aggregate.Items)
}

func TestBrowserExtract(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	b.dagu.RunWithEnv(b.env, "start", "extract.yaml").ExpectExitCode(0)
	b.dagu.ExpectTextFileContent("heading.out", "Widget Checkout\n")
}

// A second run of the same step replays the recorded click without asking
// the model.
func TestBrowserReplayCache(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	env := append(b.env, "DAGU_HOME="+t.TempDir())
	b.dagu.RunWithEnv(env, "start", "click.yaml").ExpectExitCode(0)
	require.Equal(t, 1, b.model.count(kindAct))

	b.dagu.RunWithEnv(env, "start", "click.yaml").ExpectExitCode(0)
	require.Equal(t, 1, b.model.count(kindAct), "the replayed act makes no model request")
}

// A step under ai: never runs without a model: it fails while nothing is
// recorded, and replays what a run of the step under on_miss recorded.
func TestBrowserNeverReplaysWithoutModel(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	env := append(b.env, "DAGU_HOME="+t.TempDir())
	// The name makes never.yaml the click DAG, whose replay cache it reads.
	missed := b.dagu.RunWithEnv(env, "start", "--name", "click", "never.yaml")
	missed.ExpectNonZeroExitCode()
	missed.ExpectStderrContains("the act cannot run without AI: there is no recording of it on this host")
	require.Zero(t, b.model.total())

	b.dagu.RunWithEnv(env, "start", "click.yaml").ExpectExitCode(0)
	requests := b.model.total()
	require.Equal(t, 1, b.model.count(kindAct))

	b.dagu.RunWithEnv(env, "start", "--name", "click", "never.yaml").ExpectExitCode(0)
	require.Equal(t, requests, b.model.total(), "the replay makes no model request")
}

// A replay acts only on the element it recorded. When the layout puts
// another element at the recorded location, the act asks the model again
// instead of clicking that element.
func TestBrowserReplayHealsShiftedElement(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	env := append(b.env, "DAGU_HOME="+t.TempDir())
	b.dagu.RunWithEnv(env, "start", "layout.yaml").ExpectExitCode(0)
	require.Equal(t, 1, b.model.count(kindAct))

	b.dagu.RunWithEnv(env, "start", "layout.yaml", "--", "BANNER=1").ExpectExitCode(0)
	require.Equal(t, 2, b.model.count(kindAct), "the act heals on the shifted page")

	b.dagu.RunWithEnv(env, "start", "layout.yaml", "--", "BANNER=1").ExpectExitCode(0)
	require.Equal(t, 2, b.model.count(kindAct), "the healed act replays")
}

// Clearing a step's cache, or removing all of the DAG's history, makes the
// next run ask the model again.
func TestBrowserReplayCacheClear(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	env := append(b.env, "DAGU_HOME="+t.TempDir())
	b.dagu.RunWithEnv(env, "start", "click.yaml").ExpectExitCode(0)
	require.Equal(t, 1, b.model.count(kindAct))

	cleared := b.dagu.RunWithEnv(env, "browser", "cache", "clear", "click", "--step", "checkout")
	cleared.ExpectExitCode(0)
	require.Contains(t, cleared.Stdout(), `Removed browser replay cache for step "checkout" of DAG "click"`)
	b.dagu.RunWithEnv(env, "start", "click.yaml").ExpectExitCode(0)
	require.Equal(t, 2, b.model.count(kindAct), "the cleared act asks the model again")

	b.dagu.RunWithEnv(env, "rm", "--history", "--force", "click").ExpectExitCode(0)
	b.dagu.RunWithEnv(env, "start", "click.yaml").ExpectExitCode(0)
	require.Equal(t, 3, b.model.count(kindAct), "removing all history clears the cache")
}

// Files an earlier step lists reach the site through the file picker the
// named button opens, and through the page's only file input without the
// model. A second run uploads through the recorded button without the model.
func TestBrowserUpload(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	receipts, receiptsURL := startReceipts(t)
	env := append(b.env, "RECEIPTS_URL="+receiptsURL, "DAGU_HOME="+t.TempDir())
	b.dagu.RunWithEnv(env, "start", "upload.yaml").ExpectExitCode(0)

	invoices := []receivedFile{
		{Name: "a.pdf", Type: "application/pdf", Content: "invoice A"},
		{Name: "b.pdf", Type: "application/pdf", Content: "invoice B"},
	}
	require.Equal(t, [][]receivedFile{invoices, invoices}, receipts.uploads())
	require.Equal(t, 1, b.model.count(kindObserve))
	require.Zero(t, b.model.count(kindAct))

	b.dagu.RunWithEnv(env, "start", "upload.yaml").ExpectExitCode(0)
	require.Len(t, receipts.uploads(), 4)
	require.Equal(t, 1, b.model.count(kindObserve), "the recorded upload makes no model request")
}

func TestBrowserSecretInInstruction(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	result := b.dagu.RunWithEnv(append(b.env, "SHOP_TOKEN=tok-12345"), "start", "secret_instruction.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("contains the value of secret SHOP_TOKEN")
	result.ExpectStderrNotContains("tok-12345")
	require.Zero(t, b.model.total())
}

func TestBrowserAllowedDomains(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	result := b.dagu.RunWithEnv(b.env, "start", "allowed_domains.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("outside browser.allowed_domains")
	require.Zero(t, b.model.total())
}

// A page that cannot load hosts outside allowed_domains fails a later
// operation, and the failure names the blocked hosts.
func TestBrowserBlockedRequests(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	result := b.dagu.RunWithEnv(b.env, "start", "blocked_requests.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("expectation not met", "browser.allowed_domains blocked",
		"cdn.blocked.test (", "sso.blocked.test (")
}

func TestBrowserValidation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ fixture, message string }{
		{"missing_llm.yaml", "browser actions need a model"},
		{"duplicate_output.yaml", `output "order" is already extracted by do[0]`},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			result := harness.NewRunner(t).Run("validate", tc.fixture)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.message)
		})
	}
}

// A link field without format: uri gets the link's element id when the step
// runs, so validate warns about it and still succeeds.
func TestBrowserLinkFieldWarning(t *testing.T) {
	t.Parallel()

	result := harness.NewRunner(t).Run("validate", "link_field.yaml")
	result.ExpectExitCode(0)
	result.ExpectStderrContains("listings[].link", "has no format: uri")
}
