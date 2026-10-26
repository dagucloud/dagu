// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package writegate_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/browserhost/writegate"
	"github.com/dagucloud/dagu/v2/internal/test/chrometest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// site serves the pages the tests drive and counts the writes it receives
// by path.
type site struct {
	URL    string
	mu     sync.Mutex
	writes map[string]int
	reads  map[string]int
	socket atomic.Int64
}

const sitePage = `<!doctype html><html><head><title>Ledger</title><script>
function post(path, opts) { return fetch(path, Object.assign({method: 'POST', body: 'amount=1000'}, opts || {})).then(r => 'ok ' + r.status, e => 'error ' + e); }
function socket() { return new Promise(res => { const s = new WebSocket(location.origin.replace('http', 'ws') + '/ws'); s.onopen = () => res('open'); s.onerror = () => res('error'); setTimeout(() => res('timeout'), 3000); }); }
</script></head><body><h1>Ledger</h1>
<form id="form" method="post" action="/w/form"><input name="title" value="hello"></form></body></html>`

const siteWorker = `fetch('/w/worker', {method: 'POST', body: 'x'}).then(r => postMessage('ok ' + r.status), e => postMessage('error ' + e));`

// siteStartWorker opens a socket in its first statement.
const siteStartWorker = `new WebSocket(location.origin.replace('http', 'ws') + '/ws?from=start');
self.addEventListener('install', () => self.skipWaiting());`

const siteServiceWorker = `self.addEventListener('install', e => self.skipWaiting());
self.addEventListener('activate', e => e.waitUntil(clients.claim()));
self.addEventListener('message', e => e.waitUntil((async () => {
  const s = new WebSocket(location.origin.replace('http', 'ws') + '/ws?from=sw');
  const opened = await new Promise(res => { s.onopen = () => res('open'); s.onerror = () => res('error'); setTimeout(() => res('timeout'), 3000); });
  const write = await fetch('/w/service-worker', {method: 'POST', body: 'x'}).then(r => 'ok ' + r.status, e => 'error ' + e);
  e.source.postMessage(opened + ' ' + write);
})()));`

func startSite(t *testing.T) *site {
	t.Helper()
	s := &site{writes: map[string]int{}, reads: map[string]int{}}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s.URL = "http://" + listener.Addr().String()
	server := &http.Server{Handler: http.HandlerFunc(s.serve), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return s
}

func (s *site) serve(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	switch {
	case r.URL.Path == "/ws":
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		s.socket.Add(1)
		_ = conn.CloseNow()
		return
	case strings.HasPrefix(r.URL.Path, "/w/"):
		s.mu.Lock()
		if writegate.IsWrite(r.Method) {
			s.writes[r.URL.Path]++
		} else {
			s.reads[r.URL.Path]++
		}
		s.mu.Unlock()
		_, _ = io.WriteString(w, `{"ok":true}`)
	case r.URL.Path == "/worker.js":
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = io.WriteString(w, siteWorker)
	case r.URL.Path == "/sw.js":
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = io.WriteString(w, siteServiceWorker)
	case r.URL.Path == "/sw-start.js":
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = io.WriteString(w, siteStartWorker)
	default:
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, sitePage)
	}
}

func (s *site) written() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int{}
	maps.Copy(out, s.writes)
	return out
}

// browser is a headless Chrome started for a test, as the browser runtime
// starts one: unpacked extensions may be loaded over DevTools.
type browser struct {
	cmd     *exec.Cmd
	cdpURL  string
	profile string
}

func startBrowser(t *testing.T) *browser {
	t.Helper()
	return startBrowserOn(t, profileDir(t))
}

// profileDir returns a new browser profile directory. The browser's helper
// processes can write to it for a moment after the browser is ended, so it
// is removed with retries; call it before starting the browser, so it is
// removed after.
func profileDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "writegate-profile-")
	require.NoError(t, err)
	t.Cleanup(func() {
		for range 50 {
			if os.RemoveAll(dir) == nil {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
	})
	return dir
}

// startBrowserOn starts the browser on the profile directory.
func startBrowserOn(t *testing.T, profile string) *browser {
	t.Helper()
	path := chrometest.Require(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	cmd := exec.Command(path, "--headless", "--no-sandbox", "--no-first-run", "--no-default-browser-check",
		"--enable-unsafe-extension-debugging", "--remote-allow-origins=*", "--password-store=basic", "--use-mock-keychain",
		fmt.Sprintf("--remote-debugging-port=%d", port), "--user-data-dir="+profile, "about:blank")
	require.NoError(t, cmd.Start())
	b := &browser{cmd: cmd, cdpURL: fmt.Sprintf("http://127.0.0.1:%d", port), profile: profile}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	require.Eventually(t, func() bool { return browserhost.Probe(t.Context(), b.cdpURL) == nil }, 30*time.Second, 50*time.Millisecond)
	return b
}

// guard runs a gate in mode rehearse on the browser until the test ends,
// and returns where its controls are.
func guard(t *testing.T, b *browser, policy writegate.Policy, secrets map[string]string) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	return guardIn(t, b, writegate.ModeRehearse, policy, secrets)
}

// guardIn runs a gate in mode on the browser until the test ends.
func guardIn(t *testing.T, b *browser, mode string, policy writegate.Policy, secrets map[string]string) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	readyCh := make(chan writegate.Ready, 1)
	done := make(chan error, 1)
	token := "test-token"
	go func() {
		done <- writegate.Serve(ctx, writegate.Config{
			Token: token, CDPURL: b.cdpURL, Mode: mode, Policy: policy, Secrets: secrets,
			ExtensionDir: filepath.Join(b.profile, "DaguWriteGate"),
		}, func(r writegate.Ready) { readyCh <- r })
	}()
	t.Cleanup(cancel)
	select {
	case r := <-readyCh:
		return fmt.Sprintf("http://127.0.0.1:%d/%s", r.Port, token), cancel, done
	case err := <-done:
		t.Fatalf("the gate did not start: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the gate did not start in time")
	}
	return "", cancel, done
}

// page is a DevTools session on the browser's first page.
type page struct {
	t    *testing.T
	conn *websocket.Conn
	sid  string
	next int64
	mu   sync.Mutex
}

func openPage(t *testing.T, b *browser) *page {
	t.Helper()
	wsURL, err := browserhost.BrowserID(t.Context(), b.cdpURL)
	require.NoError(t, err)
	conn, response, err := websocket.Dial(t.Context(), wsURL, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	require.NoError(t, err)
	conn.SetReadLimit(-1)
	t.Cleanup(func() { _ = conn.CloseNow() })
	p := &page{t: t, conn: conn}
	var targets struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
		} `json:"targetInfos"`
	}
	p.call("", "Target.getTargets", nil, &targets)
	for _, target := range targets.TargetInfos {
		if target.Type == "page" {
			var attached struct {
				SessionID string `json:"sessionId"`
			}
			p.call("", "Target.attachToTarget", map[string]any{"targetId": target.TargetID, "flatten": true}, &attached)
			p.sid = attached.SessionID
			return p
		}
	}
	t.Fatal("no page")
	return nil
}

func (p *page) call(sid, method string, params map[string]any, result any) {
	p.t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.next++
	command := map[string]any{"id": p.next, "method": method, "params": params}
	if params == nil {
		command["params"] = map[string]any{}
	}
	if sid != "" {
		command["sessionId"] = sid
	}
	data, _ := json.Marshal(command)
	ctx, cancel := context.WithTimeout(p.t.Context(), time.Minute)
	defer cancel()
	require.NoError(p.t, p.conn.Write(ctx, websocket.MessageText, data))
	for {
		_, data, err := p.conn.Read(ctx)
		require.NoError(p.t, err, method)
		var m struct {
			ID     int64           `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &m) != nil || m.ID != p.next {
			continue
		}
		require.Nil(p.t, m.Error, "%s: %v", method, m.Error)
		if result != nil {
			require.NoError(p.t, json.Unmarshal(m.Result, result))
		}
		return
	}
}

// eval runs an expression in the page and returns its value as text.
func (p *page) eval(expression string) string {
	p.t.Helper()
	var r struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
	}
	p.call(p.sid, "Runtime.evaluate", map[string]any{"expression": expression, "awaitPromise": true, "returnByValue": true}, &r)
	return fmt.Sprint(r.Result.Value)
}

// goTo loads url and waits until its document is ready.
func (p *page) goTo(url string) {
	p.t.Helper()
	p.call(p.sid, "Page.navigate", map[string]any{"url": url}, nil)
	require.Eventually(p.t, func() bool {
		return p.eval(`location.href === `+quote(url)+` && document.readyState === 'complete'`) == "true"
	}, 30*time.Second, 50*time.Millisecond)
}

func quote(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

func events(t *testing.T, controls string) []writegate.Event {
	t.Helper()
	list, err := writegate.Events(t.Context(), controls, 0)
	require.NoError(t, err)
	return list
}

func find(list []writegate.Event, path string) (writegate.Event, bool) {
	for _, e := range list {
		if strings.Contains(e.URL, path) {
			return e, true
		}
	}
	return writegate.Event{}, false
}

func TestGateBlocksWritesItRehearses(t *testing.T) {
	t.Parallel()

	s := startSite(t)
	b := startBrowser(t)
	controls, _, _ := guard(t, b, writegate.Policy{}, nil)
	p := openPage(t, b)
	p.goTo(s.URL + "/")

	assert.Contains(t, p.eval(`post('/w/fetch', {headers: {'Content-Type': 'application/x-www-form-urlencoded'}, body: 'amount=1000&password=hunter2'})`), "error")
	assert.Equal(t, "ok 200", p.eval(`fetch('/w/read').then(r => 'ok ' + r.status)`))
	assert.Equal(t, "true", p.eval(`navigator.sendBeacon('/w/beacon', 'x')`))
	assert.Contains(t, p.eval(`new Promise(res => { const w = new Worker('/worker.js'); w.onmessage = e => res(e.data); })`), "error")
	p.eval(`document.getElementById('form').submit(); 1`)
	require.Eventually(t, func() bool { return p.eval(`document.title`) == "Not sent" }, 10*time.Second, 50*time.Millisecond)

	assert.Empty(t, s.written(), "no write reaches the site")
	list := events(t, controls)
	fetchWrite, ok := find(list, "/w/fetch")
	require.True(t, ok, "%+v", list)
	assert.Equal(t, writegate.ClassWrite, fetchWrite.Class)
	assert.Equal(t, writegate.OutcomeBlocked, fetchWrite.Outcome)
	assert.True(t, fetchWrite.Consequential())
	assert.Equal(t, []string{"amount", "password"}, fetchWrite.Fields)
	assert.NotContains(t, fetchWrite.Body, "hunter2")
	assert.Contains(t, fetchWrite.Body, "amount=1000")
	assert.Equal(t, "POST "+s.URL+"/w/fetch? {amount,password}", fetchWrite.Signature)
	beacon, ok := find(list, "/w/beacon")
	require.True(t, ok)
	assert.Equal(t, writegate.ClassNoise, beacon.Class)
	assert.False(t, beacon.Consequential())
	worker, ok := find(list, "/w/worker")
	require.True(t, ok)
	assert.True(t, worker.Consequential())
	form, ok := find(list, "/w/form")
	require.True(t, ok)
	assert.Equal(t, "Document", form.ResourceType)
	_, read := find(list, "/w/read")
	assert.False(t, read, "a read is not an event")
}

// A page loaded before the gate started is paused at its own target, so
// its writes are decided too.
func TestGateCoversAPageLoadedBeforeIt(t *testing.T) {
	t.Parallel()

	s := startSite(t)
	b := startBrowser(t)
	p := openPage(t, b)
	p.goTo(s.URL + "/")
	controls, _, _ := guard(t, b, writegate.Policy{}, nil)

	assert.Contains(t, p.eval(`post('/w/loaded-before')`), "error")
	assert.Equal(t, "ok 200", p.eval(`fetch('/w/read').then(r => 'ok ' + r.status)`))
	assert.Empty(t, s.written())
	list := events(t, controls)
	require.Len(t, list, 1, "%+v", list)
	assert.True(t, list[0].Consequential())
}

func TestGateSendsSignInAndWhatAPersonDoes(t *testing.T) {
	t.Parallel()

	s := startSite(t)
	b := startBrowser(t)
	policy := writegate.Policy{SignIn: []writegate.Rule{{Host: "127.0.0.1", Path: "/w/session", Secret: "PORTAL_PASSWORD"}}}
	controls, _, _ := guard(t, b, policy, map[string]string{"PORTAL_PASSWORD": "s3cret pw"})
	p := openPage(t, b)
	p.goTo(s.URL + "/")

	assert.Equal(t, "ok 200", p.eval(`post('/w/session', {body: 'user=a&password=s3cret+pw'})`))
	assert.Contains(t, p.eval(`post('/w/session', {body: 'user=a&password=guess'})`), "error", "a sign-in without the secret is a write")
	require.NoError(t, writegate.SetPerson(t.Context(), controls, true))
	assert.Equal(t, "ok 200", p.eval(`post('/w/person')`))
	require.NoError(t, writegate.SetPerson(t.Context(), controls, false))
	assert.Contains(t, p.eval(`post('/w/after')`), "error")

	assert.Equal(t, map[string]int{"/w/session": 1, "/w/person": 1}, s.written())
	var classes []string
	for _, e := range events(t, controls) {
		classes = append(classes, e.Class+" "+e.Outcome)
		assert.NotContains(t, e.Body, "s3cret")
	}
	assert.Equal(t, []string{"sign_in sent", "write blocked", "person sent", "write blocked"}, classes)
}

func TestGateRefusesSockets(t *testing.T) {
	t.Parallel()

	s := startSite(t)
	b := startBrowser(t)
	controls, _, _ := guard(t, b, writegate.Policy{}, nil)
	p := openPage(t, b)
	p.goTo(s.URL + "/")

	assert.Equal(t, "error", p.eval(`socket()`))
	assert.Zero(t, s.socket.Load(), "a page's socket is refused")

	// A service worker opens its socket past the extension's rule, so the
	// gate reports it.
	reply := p.eval(`(async () => {
  await navigator.serviceWorker.register('/sw.js');
  await navigator.serviceWorker.ready;
  if (!navigator.serviceWorker.controller) await new Promise(res => navigator.serviceWorker.addEventListener('controllerchange', res, {once: true}));
  return await new Promise(res => { navigator.serviceWorker.addEventListener('message', e => res(e.data), {once: true}); navigator.serviceWorker.controller.postMessage('go'); });
})()`)
	assert.Contains(t, reply, "error", "the service worker's write is blocked: %s", reply)
	assert.Empty(t, s.written())
	require.Eventually(t, func() bool {
		_, ok := find(events(t, controls), "/ws?from=sw")
		return ok
	}, 5*time.Second, 50*time.Millisecond)
	socket, _ := find(events(t, controls), "/ws?from=sw")
	assert.Equal(t, writegate.KindSocket, socket.Kind)
	assert.Equal(t, "service_worker", socket.Target)
}

// A service worker waits for the gate to watch it, so a socket it opens in
// its first statement is reported.
func TestGateSeesASocketAWorkerOpensAtStart(t *testing.T) {
	t.Parallel()

	s := startSite(t)
	b := startBrowser(t)
	controls, _, _ := guard(t, b, writegate.Policy{}, nil)
	p := openPage(t, b)
	p.goTo(s.URL + "/")

	p.eval(`navigator.serviceWorker.register('/sw-start.js').then(() => navigator.serviceWorker.ready).then(() => 'ready')`)
	require.Eventually(t, func() bool {
		_, ok := find(events(t, controls), "/ws?from=start")
		return ok
	}, 10*time.Second, 50*time.Millisecond)
	socket, _ := find(events(t, controls), "/ws?from=start")
	assert.Equal(t, writegate.KindSocket, socket.Kind)
}

func TestGateCutsLargeBodies(t *testing.T) {
	t.Parallel()

	s := startSite(t)
	b := startBrowser(t)
	controls, _, _ := guard(t, b, writegate.Policy{}, nil)
	p := openPage(t, b)
	p.goTo(s.URL + "/")

	// Its message is several times the 16 MiB a capped connection reads.
	assert.Contains(t, p.eval(`post('/w/large', {body: new Blob(['x'.repeat(20 << 20)])})`), "error")
	assert.Contains(t, p.eval(`post('/w/next')`), "error", "the gate still decides after a large body")
	assert.Empty(t, s.written())
	large, ok := find(events(t, controls), "/w/large")
	require.True(t, ok)
	assert.Equal(t, int64(20<<20), large.BodyBytes)
	assert.LessOrEqual(t, len(large.Body), 3*writegate.DefaultBodyLimit)
}

func TestGateClosesTheBrowserItLoses(t *testing.T) {
	t.Parallel()

	b := startBrowser(t)
	_, stop, done := guard(t, b, writegate.Policy{}, nil)
	stop()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("the gate did not end")
	}
	assert.ErrorIs(t, browserhost.Probe(context.Background(), b.cdpURL), browserhost.ErrUnreachable)
}

// The extension is loaded for one browser; a browser started later on the
// same profile without a gate keeps its sockets.
func TestGateExtensionLeavesTheProfileAsItWas(t *testing.T) {
	t.Parallel()

	profile := profileDir(t)
	b := startBrowserOn(t, profile)
	_, _, _ = guard(t, b, writegate.Policy{}, nil)
	require.Contains(t, extensionNames(t, b), "Dagu write gate")
	require.NoError(t, browserhost.CloseBrowser(t.Context(), b.cdpURL))
	_, _ = b.cmd.Process.Wait()

	again := startBrowserOn(t, profile)
	assert.NotContains(t, extensionNames(t, again), "Dagu write gate")
}

func extensionNames(t *testing.T, b *browser) []string {
	t.Helper()
	var listed struct {
		Extensions []struct {
			Name string `json:"name"`
		} `json:"extensions"`
	}
	openPage(t, b).call("", "Extensions.getExtensions", nil, &listed)
	names := []string{}
	for _, e := range listed.Extensions {
		names = append(names, e.Name)
	}
	return names
}

// In record mode the gate sends only the writes the operation running may
// send, while it runs, and keeps each with its answer.
func TestGateRecordSendsWhatTheOperationMay(t *testing.T) {
	t.Parallel()

	s := startSite(t)
	b := startBrowser(t)
	controls, _, _ := guardIn(t, b, writegate.ModeRecord, writegate.Policy{}, nil)
	p := openPage(t, b)
	p.goTo(s.URL + "/")

	assert.Contains(t, p.eval(`post('/w/save')`), "error", "no operation runs, so nothing is sent")
	blocked, ok := find(events(t, controls), "/w/save")
	require.True(t, ok)
	require.NoError(t, writegate.Begin(t.Context(), controls, []string{blocked.Signature}, time.Minute))
	assert.Equal(t, "ok 200", p.eval(`post('/w/save')`))
	assert.Contains(t, p.eval(`post('/w/other')`), "error", "a write the operation may not send is blocked")
	require.NoError(t, writegate.End(t.Context(), controls))
	assert.Contains(t, p.eval(`post('/w/save')`), "error", "the operation ended")
	require.NoError(t, writegate.Begin(t.Context(), controls, []string{blocked.Signature}, time.Second))
	time.Sleep(1500 * time.Millisecond)
	assert.Contains(t, p.eval(`post('/w/save')`), "error", "the lease ended")

	assert.Equal(t, map[string]int{"/w/save": 1}, s.written())
	var outcomes []string
	var sent writegate.Event
	for _, e := range events(t, controls) {
		outcomes = append(outcomes, strings.TrimPrefix(e.URL, s.URL)+" "+e.Class+" "+e.Outcome)
		if e.Outcome == writegate.OutcomeSent {
			sent = e
		}
	}
	assert.Equal(t, []string{
		"/w/save write blocked", "/w/save approved sent", "/w/other write blocked", "/w/save write blocked", "/w/save write blocked",
	}, outcomes)
	assert.Equal(t, 200, sent.Status)
	assert.Equal(t, `{"ok":true}`, sent.Response)
}

// A page armed on its own target, and then on the browser target once it
// loads another document, has each write kept once.
func TestGateKeepsEachWriteOnce(t *testing.T) {
	t.Parallel()

	s := startSite(t)
	b := startBrowser(t)
	p := openPage(t, b)
	p.goTo(s.URL + "/")
	policy := writegate.Policy{SignIn: []writegate.Rule{{Host: "127.0.0.1", Path: "/w/session"}}}
	controls, _, _ := guard(t, b, policy, nil)
	p.goTo(s.URL + "/?again")

	assert.Equal(t, "ok 200", p.eval(`post('/w/session')`))
	assert.Contains(t, p.eval(`post('/w/save')`), "error")
	require.Eventually(t, func() bool { return len(events(t, controls)) >= 2 }, 5*time.Second, 50*time.Millisecond)
	time.Sleep(300 * time.Millisecond)
	list := events(t, controls)
	require.Len(t, list, 2, "%+v", list)
	assert.Equal(t, writegate.ClassSignIn, list[0].Class)
	assert.Equal(t, 200, list[0].Status)
	assert.True(t, list[1].Consequential())
}
