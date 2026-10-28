// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec079_browser_session_test

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ledgerPassword signs in to the ledger's sign-in host.
const ledgerPassword = "ledger-pw-9431"

// ledgerPage writes through every channel a page has, one button each. Its
// sign-in goes to another host, localhost, on the same server.
const ledgerPage = `<!doctype html><html><head><title>Ledger</title></head><body>
<h1>Ledger</h1>
<p id="status">ready</p>
<label>Password <input id="password" type="password"></label>
<button onclick="post('/api/entries', JSON.stringify({amount: 1000, memo: 'taxi'}), 'application/json')">Save entry</button>
<form method="post" action="/entries"><input name="amount" value="1000"><button type="submit">Submit form</button></form>
<a href="/entries/1/delete">Delete entry</a>
<button onclick="worker()">Start worker</button>
<button onclick="navigator.sendBeacon('/beacon', 'x'); say('beacon sent')">Send beacon</button>
<button onclick="socket()">Open socket</button>
<button onclick="signIn()">Sign in</button>
<button onclick="setTimeout(() => post('/api/autosave', 'draft=1', 'application/x-www-form-urlencoded'), 4000)">Autosave later</button>
<button onclick="post('/Default.aspx', '__EVENTTARGET=ctl00%24Save&amount=1', 'application/x-www-form-urlencoded')">Postback</button>
<button onclick="post('/graphql', JSON.stringify({operationName: 'CreateEntry', query: 'mutation CreateEntry { id }'}), 'application/json')">Create with GraphQL</button>
<button onclick="sync()">Sync later</button>
<script>
function say(text) { document.getElementById('status').textContent = text; }
function post(path, body, type) { return fetch(path, {method: 'POST', headers: {'Content-Type': type}, body}).then(r => say('saved ' + r.status), () => say('not saved')); }
function worker() { const w = new Worker('/worker.js'); w.onmessage = e => say(e.data); }
function socket() { const s = new WebSocket(location.origin.replace('http', 'ws') + '/ws'); s.onopen = () => say('socket open'); s.onerror = () => say('socket refused'); }
function signIn() {
  const body = new URLSearchParams({user: 'alice', password: document.getElementById('password').value});
  fetch('http://localhost:' + location.port + '/session', {method: 'POST', body}).then(r => say('signed in ' + r.status), () => say('sign-in failed'));
}
async function sync() { const reg = await navigator.serviceWorker.register('/sw.js'); await navigator.serviceWorker.ready; await reg.sync.register('entries'); say('sync registered'); document.body.dataset.sync = 'registered'; }
</script></body></html>`

const ledgerWorker = `fetch('/worker', {method: 'POST', body: 'x'}).then(() => postMessage('worker saved'), () => postMessage('worker not saved'));`

const ledgerServiceWorker = `self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('sync', e => e.waitUntil(fetch('/sync', {method: 'POST', body: 'x'})));`

// ledger counts the writes it receives by path, and the WebSockets it
// accepts.
type ledger struct {
	url     string
	mu      sync.Mutex
	writes  map[string]int
	sockets int
}

func startLedger(t *testing.T) *ledger {
	t.Helper()
	l := &ledger{writes: map[string]int{}}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	l.url = "http://" + listener.Addr().String()
	server := &http.Server{Handler: http.HandlerFunc(l.serve), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return l
}

func (l *ledger) serve(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	write := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions
	switch r.URL.Path {
	case "/ws":
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		l.mu.Lock()
		l.sockets++
		l.mu.Unlock()
		_ = conn.CloseNow()
		return
	case "/worker.js":
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = io.WriteString(w, ledgerWorker)
		return
	case "/sw.js":
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = io.WriteString(w, ledgerServiceWorker)
		return
	case "/entries/1/delete":
		// A read that deletes, which only a policy can tell.
		write = true
	}
	if write {
		l.mu.Lock()
		l.writes[r.URL.Path]++
		l.mu.Unlock()
	}
	w.Header().Set("Content-Type", "text/html")
	_, _ = io.WriteString(w, ledgerPage)
}

func (l *ledger) written() (map[string]int, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string]int{}
	maps.Copy(out, l.writes)
	return out, l.sockets
}

type gateEnv struct {
	t      *testing.T
	dagu   *harness.Runner
	env    []string
	home   string
	ledger *ledger
	policy string
}

func newGateEnv(t *testing.T, extra ...string) *gateEnv {
	t.Helper()
	requireChrome(t)
	browserSlot <- struct{}{}
	t.Cleanup(func() { <-browserSlot })
	home := t.TempDir()
	policy := filepath.Join(t.TempDir(), "write-policy.yaml")
	require.NoError(t, os.WriteFile(policy, []byte(`sign_in:
  - host: localhost
    path: /session
    secret: LEDGER_PASSWORD
writes:
  - host: 127.0.0.1
    path: /entries/*
`), 0o600))
	env := append([]string{"DAGU_HOME=" + home, "LEDGER_PASSWORD=" + ledgerPassword, "DAGU_BROWSER_SANDBOX=false"}, extra...)
	if runtime.GOOS == "windows" {
		env = append(env, "USERPROFILE="+os.Getenv("USERPROFILE"), "APPDATA="+os.Getenv("APPDATA"))
	}
	return &gateEnv{t: t, dagu: harness.NewRunner(t).WithCommandTimeout(browserCommandTimeout), env: env, home: home, ledger: startLedger(t), policy: policy}
}

func (g *gateEnv) session(stdin string, args ...string) *harness.Result {
	g.t.Helper()
	return g.dagu.RunWithStdin(g.env, strings.NewReader(stdin), append([]string{"browser", "session"}, args...)...)
}

// gatedResult is what a session command prints, as far as the write gate
// is concerned.
type gatedResult struct {
	ID        string  `json:"id"`
	CDPURL    string  `json:"cdp_url"`
	Status    string  `json:"status"`
	Outline   string  `json:"outline"`
	WriteGate string  `json:"write_gate"`
	Writes    []write `json:"writes"`
	Error     *struct {
		Code string `json:"code"`
	} `json:"error"`
}

type write struct {
	Kind      string `json:"kind"`
	Method    string `json:"method"`
	URL       string `json:"url"`
	Class     string `json:"class"`
	Outcome   string `json:"outcome"`
	Signature string `json:"signature"`
	Body      string `json:"body"`
}

func decode(t *testing.T, result *harness.Result) gatedResult {
	t.Helper()
	var out gatedResult
	require.NoError(t, json.Unmarshal([]byte(result.Stdout()), &out), result.Stdout())
	return out
}

// open opens a session on the ledger with args and closes it when the test
// ends.
func (g *gateEnv) open(args ...string) gatedResult {
	g.t.Helper()
	result := g.session("", append([]string{"open", g.ledger.url + "/"}, args...)...)
	result.ExpectExitCode(0)
	opened := decode(g.t, result)
	g.t.Cleanup(func() { g.session("", "close", opened.ID, "--force") })
	return opened
}

var controlID = regexp.MustCompile(`\[(\d+-\d+)\] (?:button|link|textbox) "([^"]+)"`)

// id returns the ID the outline shows for the control named name.
func id(t *testing.T, outline, name string) string {
	t.Helper()
	for _, m := range controlID.FindAllStringSubmatch(outline, -1) {
		if m[2] == name {
			return m[1]
		}
	}
	t.Fatalf("no control %q in the outline:\n%s", name, outline)
	return ""
}

// press loads the ledger and clicks the control named name, returning what
// both commands reported.
func (g *gateEnv) press(sessionID, name string) []write {
	g.t.Helper()
	loaded := g.session(fmt.Sprintf(`{"goto": %q}`, g.ledger.url+"/"), "do", sessionID)
	loaded.ExpectExitCode(0)
	clicked := g.session(fmt.Sprintf(`{"click": %q}`, g.find(sessionID, name)), "do", sessionID)
	clicked.ExpectExitCode(0)
	return append(decode(g.t, loaded).Writes, decode(g.t, clicked).Writes...)
}

// find returns the ID of the control named name on the session's page.
func (g *gateEnv) find(sessionID, name string) string {
	g.t.Helper()
	described := g.session("", "describe", sessionID, "--find", name)
	described.ExpectExitCode(0)
	return id(g.t, decode(g.t, described).Outline, name)
}

func byPath(writes []write) map[string]write {
	out := map[string]write{}
	for _, w := range writes {
		path := w.URL
		if i := strings.Index(path, "://"); i >= 0 {
			path = path[i+3:]
			path = path[strings.Index(path, "/"):]
		}
		out[path] = w
	}
	return out
}

// A rehearsing session blocks every write the ledger's page sends, through
// every channel, and reports each; only the sign-in the policy lists is
// sent, without its password in the report.
func TestSessionRehearsesEveryWrite(t *testing.T) {
	t.Parallel()

	g := newGateEnv(t)
	opened := g.open("--write-gate", "rehearse", "--write-policy", g.policy)
	assert.Equal(t, "rehearse", opened.WriteGate)

	var writes []write
	for _, name := range []string{"Save entry", "Submit form", "Delete entry", "Start worker", "Send beacon", "Open socket", "Postback", "Create with GraphQL", "Sync later"} {
		writes = append(writes, g.press(opened.ID, name)...)
	}
	// The sync registers only once its service worker is ready, after the
	// click reports back; a page left before then registers nothing.
	synced := g.session(`{"wait": {"selector": "body[data-sync=registered]"}}`, "do", opened.ID)
	synced.ExpectExitCode(0)
	writes = append(writes, decode(t, synced).Writes...)
	g.session(fmt.Sprintf(`{"goto": %q}`, g.ledger.url+"/"), "do", opened.ID).ExpectExitCode(0)
	typed := g.session(`{"type": {"into": "`+g.find(opened.ID, "Password")+`", "text": "%pw%"}, "variables": {"pw": {"env": "LEDGER_PASSWORD"}}}`, "do", opened.ID)
	typed.ExpectExitCode(0)
	signedIn := g.session(`{"click": "`+g.find(opened.ID, "Sign in")+`"}`, "do", opened.ID)
	signedIn.ExpectExitCode(0)
	writes = append(writes, decode(t, signedIn).Writes...)
	// Workers write after the click reports back; the next command reports
	// what they sent.
	waited := g.session(`{"wait": {"duration": "2s"}}`, "do", opened.ID)
	waited.ExpectExitCode(0)
	writes = append(writes, decode(t, waited).Writes...)

	sent, sockets := g.ledger.written()
	assert.Equal(t, map[string]int{"/session": 1}, sent, "only the listed sign-in reaches the site")
	assert.Zero(t, sockets, "the page's socket is refused")
	seen := byPath(writes)
	for path, want := range map[string]string{
		"/api/entries":      "write blocked",
		"/entries":          "write blocked",
		"/entries/1/delete": "write blocked",
		"/worker":           "write blocked",
		"/beacon":           "noise blocked",
		"/Default.aspx":     "write blocked",
		"/graphql":          "write blocked",
		"/sync":             "write blocked",
		"/session":          "sign_in sent",
	} {
		got, ok := seen[path]
		if assert.True(t, ok, "%s is reported; got %+v", path, writes) {
			assert.Equal(t, want, got.Class+" "+got.Outcome, path)
		}
	}
	assert.Contains(t, seen["/Default.aspx"].Signature, "__EVENTTARGET=ctl00$Save")
	assert.Contains(t, seen["/graphql"].Signature, "operationName=CreateEntry")
	assert.NotContains(t, seen["/session"].Body, ledgerPassword)
	signedIn.ExpectStdoutNotContains(ledgerPassword)
}

// A write the page sends while the session waits for its next command is
// blocked then and reported by that command.
func TestSessionReportsAWriteMadeWhileIdle(t *testing.T) {
	t.Parallel()

	g := newGateEnv(t)
	opened := g.open("--write-gate", "rehearse")
	// The autosave comes after the click's downloads settled, while the
	// session waits.
	g.press(opened.ID, "Autosave later")
	time.Sleep(4 * time.Second)
	sent, _ := g.ledger.written()
	assert.Empty(t, sent)

	next := g.session(`{"wait": {"duration": "1ms"}}`, "do", opened.ID)
	next.ExpectExitCode(0)
	autosave, ok := byPath(decode(t, next).Writes)["/api/autosave"]
	require.True(t, ok, next.Stdout())
	assert.Equal(t, "blocked", autosave.Outcome)
}

// While a person holds the browser the gate sends what they do, still
// reporting it; handed back, it blocks again.
func TestSessionSendsWhatAPersonDoes(t *testing.T) {
	t.Parallel()

	g := newGateEnv(t)
	opened := g.open("--write-gate", "rehearse")
	g.session("", "gate", "--person", opened.ID).ExpectExitCode(0)
	held := byPath(g.press(opened.ID, "Save entry"))
	g.session("", "gate", "--guard", opened.ID).ExpectExitCode(0)
	guarded := byPath(g.press(opened.ID, "Save entry"))

	sent, _ := g.ledger.written()
	assert.Equal(t, map[string]int{"/api/entries": 1}, sent)
	assert.Equal(t, "person sent", held["/api/entries"].Class+" "+held["/api/entries"].Outcome)
	assert.Equal(t, "write blocked", guarded["/api/entries"].Class+" "+guarded["/api/entries"].Outcome)
}

// The host's floor holds whatever a session asks for.
func TestSessionWriteGateFloorBeatsOff(t *testing.T) {
	t.Parallel()

	g := newGateEnv(t, "DAGU_BROWSER_WRITE_GATE=rehearse")
	opened := g.open("--write-gate", "off")
	assert.Equal(t, "rehearse", opened.WriteGate)
	g.press(opened.ID, "Save entry")
	sent, _ := g.ledger.written()
	assert.Empty(t, sent)
}

// A browser whose gate is gone is closed, since its writes would go
// undecided.
func TestSessionGateKillClosesTheBrowser(t *testing.T) {
	t.Parallel()

	g := newGateEnv(t)
	opened := g.open("--write-gate", "rehearse")
	matches, err := filepath.Glob(filepath.Join(g.home, "data", "browser", "interactive", opened.ID+".json"))
	require.NoError(t, err)
	require.Len(t, matches, 1)
	data, err := os.ReadFile(matches[0])
	require.NoError(t, err)
	var record struct {
		WriteGatePID int `json:"writeGatePid"`
	}
	require.NoError(t, json.Unmarshal(data, &record))
	require.NotZero(t, record.WriteGatePID)
	gate, err := os.FindProcess(record.WriteGatePID)
	require.NoError(t, err)
	require.NoError(t, gate.Kill())

	require.Eventually(t, func() bool {
		listed := g.session("", "list")
		var sessions struct {
			Sessions []struct {
				ID    string `json:"id"`
				State string `json:"state"`
			} `json:"sessions"`
		}
		if json.Unmarshal([]byte(listed.Stdout()), &sessions) != nil {
			return false
		}
		for _, s := range sessions.Sessions {
			if s.ID == opened.ID {
				return s.State == "ended"
			}
		}
		return false
	}, 20*time.Second, 200*time.Millisecond, "the session ends")
	if response, err := http.Get(opened.CDPURL + "/json/version"); err == nil { //nolint:noctx // a probe of a local browser
		_ = response.Body.Close()
		t.Error("the browser still answers")
	}
	after := decode(t, g.session(`{"wait": {"duration": "1ms"}}`, "do", opened.ID))
	require.NotNil(t, after.Error, "the session cannot run another command: %+v", after)
	assert.Equal(t, "session_ended", after.Error.Code)
}

// A session whose gate records blocks a write until the command that sends
// it allows its signature, and the step it exports keeps that write as a
// commit point to approve.
func TestSessionRecordSendsAnAllowedWrite(t *testing.T) {
	t.Parallel()

	g := newGateEnv(t)
	opened := g.open("--write-gate", "record")
	assert.Equal(t, "record", opened.WriteGate)
	save := fmt.Sprintf(`{"click": %q}`, g.find(opened.ID, "Save entry"))

	blocked := g.session(save, "do", opened.ID)
	blocked.ExpectNonZeroExitCode()
	refused := decode(t, blocked)
	require.NotNil(t, refused.Error)
	assert.Equal(t, "write_blocked", refused.Error.Code)
	require.Len(t, refused.Writes, 1)
	signature := refused.Writes[0].Signature
	sent, _ := g.ledger.written()
	assert.Empty(t, sent)

	allowed := g.session(save, "do", opened.ID, "--allow-write", signature)
	allowed.ExpectExitCode(0)
	approved := byPath(decode(t, allowed).Writes)["/api/entries"]
	assert.Equal(t, "approved sent", approved.Class+" "+approved.Outcome)
	sent, _ = g.ledger.written()
	assert.Equal(t, map[string]int{"/api/entries": 1}, sent)

	exported := g.session("", "export", opened.ID, "--dag", "books", "--step", "enter")
	exported.ExpectExitCode(0)
	var export struct {
		CommitPoints int `json:"commit_points"`
	}
	require.NoError(t, json.Unmarshal([]byte(exported.Stdout()), &export), exported.Stdout())
	assert.Equal(t, 1, export.CommitPoints)
	listed := g.dagu.RunWithEnv(g.env, "browser", "writes", "list", "books")
	listed.ExpectExitCode(0)
	assert.Contains(t, listed.Stdout(), signature)
}
