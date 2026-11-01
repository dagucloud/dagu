// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec079_browser_session holds black-box conformance tests for
// browser sessions. Each session command runs as its own dagu process
// against a real Chrome, a local portal, and a scripted OpenAI-compatible
// model, so no external service is needed.
package spec079_browser_session_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// password is the portal's password, which no model request may hold.
const password = "s3cret-portal-pw"

// loginPage signs in through a request and moves to the orders a moment
// after, so the click reports back before the page changes.
const loginPage = `<!doctype html><html><head><title>Sign in</title></head><body>
<h1>Order portal</h1>
<form onsubmit="return false">
<label>Username <input name="user"></label>
<label>Password <input name="password" type="password"></label>
<button type="button" onclick="signIn()">Sign in</button>
</form>
<p id="status"></p>
<script>
function signIn() {
  const body = new URLSearchParams({user: document.querySelector('[name=user]').value, password: document.querySelector('[name=password]').value});
  fetch('/session', {method: 'POST', body}).then((res) => {
    if (res.ok) { setTimeout(() => { location.href = '/orders'; }, 100); }
    else { document.getElementById('status').textContent = 'Wrong password'; }
  });
}
</script>
</body></html>`

// ordersPage filters its rows by status when Apply is pressed.
const ordersPage = `<!doctype html><html><head><title>Orders</title></head><body>
<h1>Orders</h1>
<label>Status <select id="status"><option>All</option><option>Open</option><option>Shipped</option></select></label>
<button type="button" onclick="apply()">Apply</button>
<table><thead><tr><th>Order</th><th>Status</th></tr></thead><tbody>
<tr><td>A-100</td><td>Open</td></tr>
<tr><td>A-101</td><td>Shipped</td></tr>
<tr><td>A-102</td><td>Shipped</td></tr>
<tr><td>A-103</td><td>Open</td></tr>
</tbody></table>
<script>
function apply() {
  const status = document.getElementById('status').value;
  for (const row of document.querySelectorAll('tbody tr')) {
    row.style.display = status === 'All' || row.cells[1].textContent === status ? '' : 'none';
  }
}
</script>
</body></html>`

// statementsPage sends the file its labeled file input gets to the server
// and says which arrived.
const statementsPage = `<!doctype html><html><head><title>Statements</title></head><body>
<h1>Statements</h1>
<label>Statement <input id="statement" type="file"></label>
<p id="status">No statement yet</p>
<script>
document.getElementById('statement').addEventListener('change', async (event) => {
  const form = new FormData();
  form.append('file', event.target.files[0]);
  const response = await fetch('/statements', {method: 'POST', body: form});
  document.getElementById('status').textContent = await response.text();
});
</script>
</body></html>`

// browserCommandTimeout bounds a command that starts or drives a browser.
const browserCommandTimeout = 2 * time.Minute

// browserSlot lets one test run browsers at a time, as in the browser
// actions' conformance tests.
var browserSlot = make(chan struct{}, 1)

const (
	kindAct     = "act"
	kindExtract = "extract"
)

var (
	controlPattern   = regexp.MustCompile(`\[(\d+-\d+)\] (button|link|textbox|select|combobox): ([^\n\[]+)`)
	variablePattern  = regexp.MustCompile(`%[A-Za-z_][A-Za-z0-9_]*%`)
	quotedPattern    = regexp.MustCompile(`'([^']+)'`)
	orderPattern     = regexp.MustCompile(`A-1\d\d`)
	pageMarkerPrefix = regexp.MustCompile(`(?s)^(.*?)(Accessibility Tree:|DOM:)(.*)$`)
)

// scriptedModel answers like a model reading the page it is sent: an act
// fills, selects in, or clicks the control its instruction names, and an
// extract reads the order numbers on the page. A request that holds the
// password is refused and counted.
type scriptedModel struct {
	mu     sync.Mutex
	counts map[string]int
	leaks  int
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

func (m *scriptedModel) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if strings.Contains(string(body), password) {
		m.mu.Lock()
		m.leaks++
		m.mu.Unlock()
		http.Error(w, "the password reached the model", http.StatusBadRequest)
		return
	}
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
	if err := json.Unmarshal(body, &req); err != nil || len(req.Tools) == 0 {
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
		kind, answer = "observe", `{"elements":[]}`
	case strings.Contains(schema, "elementId"):
		kind, answer = kindAct, actAnswer(instruction, page)
	case strings.Contains(schema, `"completed"`):
		kind, answer = "metadata", `{"completed":true,"progress":"done"}`
	default:
		kind = kindExtract
		orders := []string{}
		for _, order := range orderPattern.FindAllString(page, -1) {
			if !slices.Contains(orders, order) {
				orders = append(orders, order)
			}
		}
		data, _ := json.Marshal(map[string]any{"orders": orders})
		answer = string(data)
	}
	m.mu.Lock()
	m.counts[kind]++
	m.mu.Unlock()

	arguments, _ := json.Marshal(answer)
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"respond","arguments":`+string(arguments)+`}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`)
}

// actAnswer picks the control an instruction names: it fills a field with
// the instruction's variable, picks the quoted option of a select, or
// clicks a button or link.
func actAnswer(instruction, page string) string {
	for _, control := range controlPattern.FindAllStringSubmatch(page, -1) {
		id, role, name := control[1], control[2], strings.TrimSpace(control[3])
		if !strings.Contains(instruction, name) {
			continue
		}
		method, arguments := "click", []string{}
		switch role {
		case "textbox":
			method, arguments = "fill", []string{variablePattern.FindString(instruction)}
		case "select", "combobox":
			option := ""
			if quoted := quotedPattern.FindStringSubmatch(instruction); quoted != nil {
				option = quoted[1]
			}
			method, arguments = "selectOptionFromDropdown", []string{option}
		}
		action, _ := json.Marshal(map[string]any{"elementId": id, "description": role + " " + name, "method": method, "arguments": arguments})
		return `{"action":` + string(action) + `,"twoStep":false}`
	}
	return `{"action":null,"twoStep":false}`
}

// statements keeps the names and contents of the files the portal's
// statements page sent.
type statements struct {
	mu       sync.Mutex
	received []string
}

func (s *statements) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.received)
}

// startPortal serves a portal whose orders need a signed-in session, and a
// statements page whose uploads it keeps.
func startPortal(t *testing.T) (string, *statements) {
	t.Helper()
	kept := &statements{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/statements":
			if r.Method != http.MethodPost {
				w.Header().Set("Content-Type", "text/html")
				_, _ = io.WriteString(w, statementsPage)
				return
			}
			file, header, err := r.FormFile("file")
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			content, _ := io.ReadAll(file)
			_ = file.Close()
			kept.mu.Lock()
			kept.received = append(kept.received, header.Filename+": "+string(content))
			kept.mu.Unlock()
			_, _ = io.WriteString(w, "Received "+header.Filename)
		case "/session":
			if r.ParseForm() != nil || r.PostForm.Get("password") != password {
				http.Error(w, "wrong password", http.StatusUnauthorized)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "signed-in", Path: "/"})
		case "/guarded":
			// Cloudflare marks every answer that is one of its checks.
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Cf-Mitigated", "challenge")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `<title>Just a moment...</title><p>Checking your browser</p>`)
		case "/orders":
			if cookie, err := r.Cookie("sid"); err != nil || cookie.Value != "signed-in" {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, ordersPage)
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, loginPage)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL, kept
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

// sessionEnv runs dagu commands that share one Dagu home, as one person's
// commands would.
type sessionEnv struct {
	t          *testing.T
	dagu       *harness.Runner
	model      *scriptedModel
	env        []string
	portal     string
	statements *statements
}

func newSessionEnv(t *testing.T) *sessionEnv {
	t.Helper()
	requireChrome(t)
	browserSlot <- struct{}{}
	t.Cleanup(func() { <-browserSlot })
	model, modelURL := startModel(t)
	portal, kept := startPortal(t)
	// The harness sets CI, where the browser runtime turns off the sandbox,
	// so the tests turn it off explicitly instead of being refused.
	env := []string{
		"DAGU_HOME=" + t.TempDir(), "SHOP_URL=" + portal, "LLM_BASE_URL=" + modelURL,
		"SHOP_PASSWORD=" + password, "DAGU_BROWSER_SANDBOX=false",
	}
	if runtime.GOOS == "windows" {
		// Chrome on Windows needs the real profile folders to start.
		env = append(env, "USERPROFILE="+os.Getenv("USERPROFILE"), "APPDATA="+os.Getenv("APPDATA"))
	}
	return &sessionEnv{t: t, dagu: harness.NewRunner(t).WithCommandTimeout(browserCommandTimeout), model: model, env: env, portal: portal, statements: kept}
}

// session runs dagu browser session with args, reading stdin.
func (s *sessionEnv) session(stdin string, args ...string) *harness.Result {
	s.t.Helper()
	return s.dagu.RunWithStdin(s.env, strings.NewReader(stdin), append([]string{"browser", "session"}, args...)...)
}

// open opens a session on the portal's sign-in page and closes it when the
// test ends.
func (s *sessionEnv) open(args ...string) string {
	s.t.Helper()
	result := s.session("", append([]string{"open", s.portal + "/login"}, args...)...)
	result.ExpectExitCode(0)
	var opened struct {
		ID      string `json:"id"`
		Outline string `json:"outline"`
	}
	require.NoError(s.t, json.Unmarshal([]byte(result.Stdout()), &opened), result.Stdout())
	assert.Contains(s.t, opened.Outline, `textbox "Password"`)
	s.t.Cleanup(func() {
		s.dagu.RunWithStdin(s.env, strings.NewReader(""), "browser", "session", "close", opened.ID, "--force")
	})
	return opened.ID
}

var modelFlags = []string{"--provider", "local", "--model", "test-model", "--base-url", "${LLM_BASE_URL}"}

// printed decodes what a session command printed.
func printed(t *testing.T, result *harness.Result) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.Stdout()), &out), result.Stdout())
	return out
}

func errorCode(t *testing.T, result *harness.Result) string {
	t.Helper()
	failure, _ := printed(t, result)["error"].(map[string]any)
	code, _ := failure["code"].(string)
	return code
}

// A session works the portal one operation per command, each its own
// process, and exports the step; that step's first run replays every act
// without asking the model.
func TestSessionWorkAndExport(t *testing.T) {
	t.Parallel()

	s := newSessionEnv(t)
	id := s.open(modelFlags...)
	for _, op := range []string{
		`{"act": "Type %user% into the Username field", "variables": {"user": "alice"}}`,
		`{"act": "Type %password% into the Password field", "variables": {"password": {"env": "SHOP_PASSWORD"}}}`,
		`{"act": "Click the Sign in button"}`,
		`{"expect": {"url": "/orders", "within": "10s"}}`,
	} {
		s.session(op, "do", id).ExpectExitCode(0)
	}
	failed := s.session(`{"act": "Click the Delete everything button"}`, "do", id)
	failed.ExpectNonZeroExitCode()
	assert.Equal(t, "failed", printed(t, failed)["status"], "a failed operation leaves the session usable")
	for _, op := range []string{
		`{"act": "Select 'Shipped' in the Status select"}`,
		`{"act": "Click the Apply button"}`,
		`{"extract": {"instruction": "The order numbers shown", "schema": {"type": "object", "properties": {"orders": {"type": "array", "items": {"type": "string"}}}, "required": ["orders"]}}}`,
	} {
		result := s.session(op, "do", id)
		result.ExpectExitCode(0)
		if strings.Contains(op, "extract") {
			assert.Equal(t, map[string]any{"orders": []any{"A-101", "A-102"}}, printed(t, result)["outputs"])
		}
	}
	described := s.session("", "describe", id, "--find", "A-101", "--format", "text")
	described.ExpectExitCode(0)
	assert.Contains(t, described.Stdout(), "row: A-101 | Shipped")

	exported := s.session("", "export", id, "--dag", "orders", "--step", "fetch")
	exported.ExpectExitCode(0)
	var export struct {
		Step       map[string]any `json:"step"`
		Recordings int            `json:"recordings"`
	}
	require.NoError(t, json.Unmarshal([]byte(strings.ReplaceAll(exported.Stdout(), s.portal, "${SHOP_URL}")), &export))
	assert.Equal(t, 5, export.Recordings)
	assert.Equal(t, fixtureStep(t, "orders.yaml", "fetch"), export.Step)
	for _, result := range []*harness.Result{exported, described, failed} {
		result.ExpectStdoutNotContains(password)
	}
	s.session("", "close", id).ExpectExitCode(0)
	listed := s.session("", "list")
	listed.ExpectExitCode(0)
	assert.JSONEq(t, `{"sessions": [], "capabilities": ["trusted_cas", "write_gate"]}`, listed.Stdout())

	acts := s.model.count(kindAct)
	s.dagu.RunWithEnv(s.env, "start", "orders.yaml").ExpectExitCode(0)
	s.dagu.ExpectFileContains("orders.out", "A-101", "A-102")
	assert.Equal(t, acts, s.model.count(kindAct), "the first run replays every act the session recorded")
	assert.Zero(t, s.model.leaks, "the password never reached the model")
}

// An upload into a file input the outline shows by its ID runs without the
// model, and the exported step's first run replays it.
func TestSessionUploadAndExport(t *testing.T) {
	t.Parallel()

	s := newSessionEnv(t)
	statement := filepath.Join(t.TempDir(), "statement.pdf")
	require.NoError(t, os.WriteFile(statement, []byte("statement for March"), 0o600))
	opened := s.session("", append([]string{"open", s.portal + "/statements"}, modelFlags...)...)
	opened.ExpectExitCode(0)
	var session struct {
		ID      string `json:"id"`
		Outline string `json:"outline"`
	}
	require.NoError(t, json.Unmarshal([]byte(opened.Stdout()), &session), opened.Stdout())
	t.Cleanup(func() { s.session("", "close", session.ID, "--force") })
	input := regexp.MustCompile(`\[(\d+-\d+)\] file input "Statement"`).FindStringSubmatch(session.Outline)
	require.NotNil(t, input, session.Outline)

	upload, err := json.Marshal(map[string]any{"upload": map[string]any{"into": input[1], "files": statement}})
	require.NoError(t, err)
	uploaded := s.session(string(upload), "do", session.ID)
	uploaded.ExpectExitCode(0)
	result := printed(t, uploaded)
	assert.Equal(t, `the "Statement" file input`, result["into"])
	assert.Equal(t, true, result["recorded"])
	s.session(`{"expect": {"text": "Received statement.pdf", "within": "10s"}}`, "do", session.ID).ExpectExitCode(0)

	exported := s.session("", "export", session.ID, "--dag", "statements", "--step", "attach")
	exported.ExpectExitCode(0)
	var export struct {
		Step       map[string]any `json:"step"`
		Recordings int            `json:"recordings"`
		Warnings   []string       `json:"warnings"`
	}
	// The export is JSON, which escapes the separators of a Windows path.
	quoted, err := json.Marshal(statement)
	require.NoError(t, err)
	replaced := strings.NewReplacer(s.portal, "${SHOP_URL}", strings.Trim(string(quoted), `"`), "${STATEMENT}").Replace(exported.Stdout())
	require.NoError(t, json.Unmarshal([]byte(replaced), &export))
	assert.Equal(t, 1, export.Recordings)
	assert.Equal(t, fixtureStep(t, "statements.yaml", "attach"), export.Step)
	require.Len(t, export.Warnings, 1)
	assert.Contains(t, export.Warnings[0], "uploads ${STATEMENT}, the files the session was given")
	s.session("", "close", session.ID).ExpectExitCode(0)

	s.dagu.RunWithEnv(append(s.env, "STATEMENT="+statement), "start", "statements.yaml").ExpectExitCode(0)
	assert.Equal(t, []string{"statement.pdf: statement for March", "statement.pdf: statement for March"}, s.statements.all())
	assert.Zero(t, s.model.count(kindAct)+s.model.count(kindExtract)+s.model.count("observe"), "nothing asked the model")
}

// fixtureStep reads the step with id from a fixture, as JSON values.
func fixtureStep(t *testing.T, fixture, id string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", fixture))
	require.NoError(t, err)
	var dag struct {
		Steps []map[string]any `yaml:"steps"`
	}
	require.NoError(t, yaml.Unmarshal(data, &dag))
	for _, step := range dag.Steps {
		if step["id"] == id {
			encoded, err := json.Marshal(step)
			require.NoError(t, err)
			var decoded map[string]any
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			return decoded
		}
	}
	t.Fatalf("no step %s in %s", id, fixture)
	return nil
}

// A step cannot use a profile a session holds, and can once the session
// is closed.
func TestSessionHoldsItsProfile(t *testing.T) {
	t.Parallel()

	s := newSessionEnv(t)
	id := s.open("--profile", "portal")
	refused := s.dagu.RunWithEnv(s.env, "start", "profile_step.yaml")
	refused.ExpectNonZeroExitCode()
	refused.ExpectStderrContains(`browser profile "portal" is held by browser session ` + id)

	s.session("", "close", id).ExpectExitCode(0)
	s.dagu.RunWithEnv(s.env, "start", "profile_step.yaml").ExpectExitCode(0)
}

// A session idle past its timeout ends without any other command: its
// browser closes, and its history can still be exported.
func TestSessionEndsWhenIdle(t *testing.T) {
	t.Parallel()

	s := newSessionEnv(t)
	// The timeout starts at open, so it has to outlast the do that follows,
	// which on a loaded runner takes seconds to start and reach the browser.
	id := s.open("--idle-timeout", "15s")
	s.session(`{"expect": {"text": "Order portal"}}`, "do", id).ExpectExitCode(0)

	require.Eventually(t, func() bool {
		listed := s.session("", "list")
		return strings.Contains(listed.Stdout(), `"state": "ended"`)
	}, time.Minute, time.Second)
	ended := s.session(`{"expect": {"text": "Order portal"}}`, "do", id)
	ended.ExpectNonZeroExitCode()
	assert.Equal(t, "session_ended", errorCode(t, ended))
	s.session("", "export", id, "--dag", "orders", "--step", "check", "--dry-run").ExpectExitCode(0)
}

// A page a site answered with a bot check is reported by every command that
// finds the browser on it, including one that reattached after it loaded.
func TestSessionReportsBotCheck(t *testing.T) {
	t.Parallel()

	s := newSessionEnv(t)
	result := s.session("", "open", s.portal+"/guarded")
	result.ExpectExitCode(0)
	opened := printed(t, result)
	id, _ := opened["id"].(string)
	t.Cleanup(func() {
		s.dagu.RunWithStdin(s.env, strings.NewReader(""), "browser", "session", "close", id, "--force")
	})
	refused, _ := opened["refused"].(map[string]any)
	assert.Equal(t, "cloudflare", refused["bot_check"], result.Stdout())
	assert.EqualValues(t, http.StatusForbidden, refused["status"])

	described := s.session("", "describe", id)
	described.ExpectExitCode(0)
	refused, _ = printed(t, described)["refused"].(map[string]any)
	assert.Equal(t, "cloudflare", refused["bot_check"], described.Stdout())
}

// A session opened without a model refuses the operations that ask one and
// runs the rest.
func TestSessionWithoutModel(t *testing.T) {
	t.Parallel()

	s := newSessionEnv(t)
	id := s.open()
	refused := s.session(`{"act": "Click the Sign in button"}`, "do", id)
	refused.ExpectNonZeroExitCode()
	assert.Equal(t, "model_required", errorCode(t, refused))
	s.session(`{"expect": {"text": "Order portal"}}`, "do", id).ExpectExitCode(0)
	s.session("", "describe", id).ExpectExitCode(0)
	assert.Zero(t, s.model.count(kindAct)+s.model.count(kindExtract))
}

// Commands refuse what a session cannot use before any browser starts.
func TestSessionInputErrors(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	for _, tc := range []struct {
		name, stdin, code string
		args              []string
	}{
		{name: "unknown session", stdin: `{"goto": "https://example.com"}`, code: "session_not_found", args: []string{"do", "ab2cd3ef4g"}},
		{name: "ask", stdin: `{"ask": {"prompt": "Code?", "as": "code"}}`, code: "invalid_input", args: []string{"do", "ab2cd3ef4g"}},
		{name: "no operation", stdin: "", code: "invalid_input", args: []string{"do", "ab2cd3ef4g"}},
		{name: "page outside the allowed domains", code: "invalid_input", args: []string{"open", "https://evil.example.org", "--allowed-domain", "portal.example.com"}},
		{name: "step ID", code: "invalid_input", args: []string{"export", "ab2cd3ef4g", "--dag", "orders", "--step", "fetch-orders"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := dagu.RunWithStdin(nil, strings.NewReader(tc.stdin), append([]string{"browser", "session"}, tc.args...)...)
			result.ExpectNonZeroExitCode()
			assert.Equal(t, tc.code, errorCode(t, result))
		})
	}
}
