// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package writegate

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/cmn/procutil"
)

const (
	// DefaultBodyLimit is how much of a write's body the gate keeps.
	DefaultBodyLimit = 64 << 10
	// commandTimeout bounds each command the gate sends while it starts.
	commandTimeout = 30 * time.Second
	// closeTimeout bounds closing the browser once the gate lost it.
	closeTimeout = 10 * time.Second
	// readHeaderTimeout bounds a control request's headers.
	readHeaderTimeout = 10 * time.Second
	// accessDenied is the reason a blocked request fails with. A page sees
	// a network error, and the browser does not count it among the
	// requests allowed_domains blocked.
	accessDenied = "AccessDenied"
)

// Control paths, under the token.
const (
	eventsPath  = "events"
	personPath  = "person"
	beginPath   = "begin"
	endPath     = "end"
	afterParam  = "after"
	personParam = "on"
)

// maxLease bounds how long an operation's allowance lasts, so writes an
// operation that never ended may send stop being sent.
const maxLease = 30 * time.Minute

// notSentPage answers a page load the gate blocked, so the page shows
// where it stopped instead of a network error.
const notSentPage = `<!doctype html><html><head><meta charset="utf-8"><title>Not sent</title></head>` +
	`<body><h1>Not sent</h1><p>The browser held back this form; nothing was saved.</p></body></html>`

// Config is what a gate decides, and for which browser.
type Config struct {
	// Token is a random value that prefixes the control paths.
	Token string `json:"token"`
	// CDPURL is the DevTools address of the browser, such as
	// http://127.0.0.1:9333.
	CDPURL string `json:"cdp_url"`
	Mode   string `json:"mode"`
	Policy Policy `json:"policy"`
	// Secrets holds the values of the secrets the policy's rules name, by
	// name.
	Secrets map[string]string `json:"secrets,omitempty"`
	// ExtensionDir is where the socket-blocking extension is written.
	ExtensionDir string `json:"extension_dir"`
	// BodyLimit is how much of a write's body the gate keeps; zero means
	// DefaultBodyLimit.
	BodyLimit int `json:"body_limit,omitempty"`
}

func (c Config) validate() error {
	switch {
	case c.Token == "":
		return errNoToken
	case c.CDPURL == "":
		return errors.New("the write gate config names no browser")
	case c.ExtensionDir == "":
		return errors.New("the write gate config names no extension directory")
	}
	if _, err := ParseMode(c.Mode); err != nil {
		return err
	}
	if c.Mode == ModeOff {
		return errors.New("a write gate in mode off has nothing to decide")
	}
	return c.Policy.Validate()
}

// Ready describes a gate that decides the browser's requests.
type Ready struct {
	// Port is the port of the control paths.
	Port int `json:"port"`
}

// Serve guards the browser at cfg.CDPURL until the browser is gone or ctx
// ends. It calls ready once every request the browser makes is decided,
// before any page loads. Losing the browser's connection while the browser
// still runs closes the browser, as does ctx ending.
func Serve(ctx context.Context, cfg Config, ready func(Ready)) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	if err := writeExtension(cfg.ExtensionDir); err != nil {
		return err
	}
	wsURL, err := browserhost.BrowserID(ctx, cfg.CDPURL)
	if err != nil {
		return fmt.Errorf("reach the browser: %w", err)
	}
	// The process is ended when the browser does not close when asked.
	browserPID, _ := browserhost.BrowserProcessID(ctx, cfg.CDPURL)
	browserStartedAt, _ := procutil.StartTime(browserPID)
	conn, response, err := websocket.Dial(ctx, wsURL, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		return fmt.Errorf("connect to the browser: %w", err)
	}
	// A message carries a request's whole body, however large; it is read
	// as a stream and the body cut.
	conn.SetReadLimit(-1)
	limit := cfg.BodyLimit
	if limit <= 0 {
		limit = DefaultBodyLimit
	}
	readCtx, stopRead := context.WithCancel(context.WithoutCancel(ctx))
	defer stopRead()
	g := &gate{
		conn:      conn,
		ctx:       readCtx,
		mode:      cfg.Mode,
		rules:     newRules(cfg.Policy, cfg.Secrets),
		ledger:    &ledger{},
		bodyLimit: limit,
		waiters:   map[int64]chan reply{},
		callbacks: map[int64]func(reply){},
		watched:   map[string]string{},
		sending:   map[string]Event{},
		decided:   map[string]bool{},
		done:      make(chan struct{}),
	}
	go g.read()
	listener, err := g.arm(ctx, cfg.ExtensionDir)
	if err != nil {
		_ = conn.CloseNow()
		<-g.done
		return err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	server := &http.Server{Handler: http.StripPrefix("/"+cfg.Token, g.controls()), ReadHeaderTimeout: readHeaderTimeout}
	go func() { _ = server.Serve(listener) }()
	ready(Ready{Port: port})

	select {
	case <-g.done:
	case <-ctx.Done():
	}
	_ = server.Close()
	_ = conn.CloseNow()
	<-g.done
	// Requests the browser makes from now on go unseen, so a browser that
	// still runs is closed.
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
	defer cancel()
	if err := browserhost.CloseBrowser(closeCtx, cfg.CDPURL); err != nil {
		if endProcess(browserPID, browserStartedAt) {
			return nil
		}
		return fmt.Errorf("close the browser the write gate lost: %w", err)
	}
	return nil
}

// endProcess ends pid when it still is the process that started at
// startedAt.
func endProcess(pid int, startedAt int64) bool {
	matched, _, ok := procutil.MatchesStartTime(pid, startedAt)
	if !ok || !matched {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	defer func() { _ = process.Release() }()
	return process.Kill() == nil
}

// gate decides the requests of one browser on its own DevTools
// connection.
type gate struct {
	conn      *websocket.Conn
	ctx       context.Context
	mode      string
	rules     *rules
	ledger    *ledger
	bodyLimit int
	person    atomic.Bool
	nextID    atomic.Int64

	mu      sync.Mutex
	waiters map[int64]chan reply
	// callbacks handle the replies to commands sent while deciding, which
	// the gate never waits on.
	callbacks map[int64]func(reply)
	// watched maps the sessions of the service workers and shared workers
	// the gate watches for sockets to their kind of target.
	watched map[string]string
	// sending holds the writes sent until their answers arrive, by
	// request.
	sending map[string]Event
	// decided holds the requests a page target let through, by network ID,
	// so the browser target does not keep them again.
	decided map[string]bool
	// allowed are the signatures the operation running may send, until
	// allowedUntil.
	allowed      map[string]bool
	allowedUntil time.Time
	done         chan struct{}
}

type reply struct {
	result json.RawMessage
	err    error
}

// message is a command reply or an event read from the browser.
type message struct {
	ID        int64           `json:"id"`
	SessionID string          `json:"sessionId"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params"`
	Result    json.RawMessage `json:"result"`
	Error     *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// arm makes the browser pause every request and refuse sockets, and
// listens for the control paths.
func (g *gate) arm(ctx context.Context, extensionDir string) (net.Listener, error) {
	commands := []struct {
		method string
		params map[string]any
	}{
		{"Fetch.enable", map[string]any{"patterns": []map[string]any{{"urlPattern": "*", "requestStage": "Request"}}}},
		{"Extensions.loadUnpacked", map[string]any{"path": extensionDir}},
		// A worker waits for the gate to watch it before it runs, so a socket
		// it opens at once is still seen.
		{"Target.setAutoAttach", map[string]any{
			"autoAttach": true, "waitForDebuggerOnStart": true, "flatten": true,
			"filter": []map[string]any{{"type": targetServiceWorker}, {"type": targetSharedWorker}, {"exclude": true}},
		}},
	}
	for _, command := range commands {
		if _, err := g.call(ctx, "", command.method, command.params); err != nil {
			return nil, err
		}
	}
	if err := g.awaitSocketRule(ctx); err != nil {
		return nil, err
	}
	// The browser target covers what loads from now on. A page loaded
	// before is paused at its own target too.
	if err := g.armLoadedPages(ctx); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for the write gate controls: %w", err)
	}
	return listener, nil
}

const (
	targetPage          = "page"
	targetIframe        = "iframe"
	targetServiceWorker = "service_worker"
	targetSharedWorker  = "shared_worker"
)

func (g *gate) armLoadedPages(ctx context.Context) error {
	raw, err := g.call(ctx, "", "Target.getTargets", nil)
	if err != nil {
		return err
	}
	var targets struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
			URL      string `json:"url"`
		} `json:"targetInfos"`
	}
	if err := json.Unmarshal(raw, &targets); err != nil {
		return err
	}
	for _, target := range targets.TargetInfos {
		if (target.Type != targetPage && target.Type != targetIframe) || !webURL(target.URL) {
			continue
		}
		raw, err := g.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": target.TargetID, "flatten": true})
		if err != nil {
			continue
		}
		var attached struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(raw, &attached) != nil {
			continue
		}
		if _, err := g.call(ctx, attached.SessionID, "Fetch.enable", map[string]any{"patterns": []map[string]any{{"urlPattern": "*", "requestStage": "Request"}}}); err != nil {
			return err
		}
	}
	return nil
}

func webURL(raw string) bool {
	return strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://")
}

// call sends a command and waits for its reply.
func (g *gate) call(ctx context.Context, sessionID, method string, params map[string]any) (json.RawMessage, error) {
	id := g.nextID.Add(1)
	wait := make(chan reply, 1)
	g.mu.Lock()
	g.waiters[id] = wait
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(g.waiters, id)
		g.mu.Unlock()
	}()
	if err := g.write(id, sessionID, method, params); err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	select {
	case r := <-wait:
		if r.err != nil {
			return nil, fmt.Errorf("%s: %w", method, r.err)
		}
		return r.result, nil
	case <-g.done:
		return nil, fmt.Errorf("%s: the browser closed the connection", method)
	case <-ctx.Done():
		return nil, fmt.Errorf("%s: %w", method, ctx.Err())
	}
}

// send sends a command without waiting for its reply.
func (g *gate) send(sessionID, method string, params map[string]any) {
	_ = g.write(g.nextID.Add(1), sessionID, method, params)
}

func (g *gate) write(id int64, sessionID, method string, params map[string]any) error {
	if params == nil {
		params = map[string]any{}
	}
	command := map[string]any{"id": id, "method": method, "params": params}
	if sessionID != "" {
		command["sessionId"] = sessionID
	}
	data, err := json.Marshal(command)
	if err != nil {
		return err
	}
	return g.conn.Write(g.ctx, websocket.MessageText, data)
}

// read decides each paused request as it arrives, until the connection
// ends.
func (g *gate) read() {
	defer close(g.done)
	for {
		_, r, err := g.conn.Reader(g.ctx)
		if err != nil {
			return
		}
		data, err := cutMessage(r, g.bodyLimit)
		if err != nil {
			return
		}
		var m message
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.Method {
		case "":
			r := reply{result: m.Result}
			if m.Error != nil {
				r.err = errors.New(m.Error.Message)
			}
			g.mu.Lock()
			wait, callback := g.waiters[m.ID], g.callbacks[m.ID]
			delete(g.callbacks, m.ID)
			g.mu.Unlock()
			switch {
			case wait != nil:
				wait <- r
			case callback != nil:
				callback(r)
			}
		case "Fetch.requestPaused":
			g.paused(m.SessionID, m.Params)
		case "Target.attachedToTarget":
			g.attached(m.Params)
		case "Target.detachedFromTarget":
			var e struct {
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal(m.Params, &e) == nil {
				g.mu.Lock()
				delete(g.watched, e.SessionID)
				g.mu.Unlock()
			}
		case "Network.webSocketCreated":
			g.socket(m.SessionID, m.Params)
		}
	}
}

// pausedRequest is a Fetch.requestPaused event, with its body cut. One
// paused at the response stage has a status or an error.
type pausedRequest struct {
	RequestID           string `json:"requestId"`
	NetworkID           string `json:"networkId"`
	ResourceType        string `json:"resourceType"`
	ResponseStatusCode  int    `json:"responseStatusCode"`
	ResponseErrorReason string `json:"responseErrorReason"`
	Request             struct {
		URL          string            `json:"url"`
		Method       string            `json:"method"`
		Headers      map[string]string `json:"headers"`
		PostData     string            `json:"postData"`
		PostDataSize int64             `json:"postDataSize"`
	} `json:"request"`
	ResponseHeaders []header `json:"responseHeaders"`
}

type header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// paused decides a paused request at once. The browser target sees the
// requests of every document loaded after the gate; a page target, armed
// for a page loaded before it, sees its page's requests first, and a
// request it lets through is seen again by the browser target only when a
// later document of the page sent it, so it is kept once.
func (g *gate) paused(sessionID string, params json.RawMessage) {
	var p pausedRequest
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	if p.ResponseStatusCode != 0 || p.ResponseErrorReason != "" {
		g.answered(sessionID, p)
		return
	}
	if sessionID == "" && p.NetworkID != "" {
		g.mu.Lock()
		decided := g.decided[p.NetworkID]
		delete(g.decided, p.NetworkID)
		g.mu.Unlock()
		if decided {
			g.send(sessionID, "Fetch.continueRequest", map[string]any{"requestId": p.RequestID})
			return
		}
	}
	req := request{
		Method: p.Request.Method, URL: p.Request.URL, ResourceType: p.ResourceType,
		Body: []byte(p.Request.PostData), BodyBytes: max(p.Request.PostDataSize, int64(len(p.Request.PostData))),
	}
	for name, value := range p.Request.Headers {
		if strings.EqualFold(name, "Content-Type") {
			req.ContentType = value
		}
	}
	class, write := g.rules.classify(req, g.person.Load())
	if !write {
		g.send(sessionID, "Fetch.continueRequest", map[string]any{"requestId": p.RequestID})
		return
	}
	event := g.describe(req, class)
	if class == ClassWrite && Sends(g.mode) && g.allows(event.Signature) {
		event.Class = ClassApproved
	}
	if class == ClassSignIn || class == ClassPerson || event.Class == ClassApproved {
		if sessionID != "" && p.NetworkID != "" {
			g.mu.Lock()
			g.decided[p.NetworkID] = true
			g.mu.Unlock()
		}
		// What an approved write was answered with tells whether it saved.
		event.Outcome = OutcomeSent
		g.mu.Lock()
		g.sending[sessionID+"\x00"+p.RequestID] = event
		g.mu.Unlock()
		g.send(sessionID, "Fetch.continueRequest", map[string]any{"requestId": p.RequestID, "interceptResponse": true})
		return
	}
	// The event is kept before the page learns the outcome, so whoever saw
	// the outcome finds the event.
	event.Outcome = OutcomeBlocked
	g.ledger.add(event)
	if req.ResourceType == resourceDocument {
		g.send(sessionID, "Fetch.fulfillRequest", map[string]any{
			"requestId": p.RequestID, "responseCode": http.StatusOK,
			"responseHeaders": []map[string]string{{"name": "Content-Type", "value": "text/html; charset=utf-8"}},
			"body":            base64.StdEncoding.EncodeToString([]byte(notSentPage)),
		})
	} else {
		g.send(sessionID, "Fetch.failRequest", map[string]any{"requestId": p.RequestID, "errorReason": accessDenied})
	}
}

// describe returns the event of a write.
func (g *gate) describe(req request, class string) Event {
	d := g.rules.describe(req)
	return Event{
		Kind: KindWrite, Method: req.Method, URL: d.URL, ResourceType: req.ResourceType,
		Class: class, Signature: d.Signature, ContentType: req.ContentType,
		Fields: d.Fields, Body: d.Body, BodyBytes: req.BodyBytes,
	}
}

// answered keeps a sent write with the answer it got, paused at the
// response stage, and lets the answer through as soon as its body is read.
// As with a blocked write, the event is kept before the page gets the
// answer.
func (g *gate) answered(sessionID string, p pausedRequest) {
	key := sessionID + "\x00" + p.RequestID
	g.mu.Lock()
	event, ok := g.sending[key]
	delete(g.sending, key)
	g.mu.Unlock()
	if p.ResponseErrorReason != "" {
		if ok {
			event.ResponseError = p.ResponseErrorReason
			g.ledger.add(event)
		}
		g.send(sessionID, "Fetch.continueRequest", map[string]any{"requestId": p.RequestID})
		return
	}
	if !ok {
		g.send(sessionID, "Fetch.continueResponse", map[string]any{"requestId": p.RequestID})
		return
	}
	event.Status = p.ResponseStatusCode
	if !readableResponse(p.ResponseHeaders) {
		g.ledger.add(event)
		g.send(sessionID, "Fetch.continueResponse", map[string]any{"requestId": p.RequestID})
		return
	}
	id := g.nextID.Add(1)
	g.mu.Lock()
	g.callbacks[id] = func(r reply) {
		var body struct {
			Body          string `json:"body"`
			Base64Encoded bool   `json:"base64Encoded"`
		}
		if r.err == nil && json.Unmarshal(r.result, &body) == nil {
			event.Response = g.rules.describeResponse(body.Body, body.Base64Encoded, contentType(p.ResponseHeaders))
		}
		g.ledger.add(event)
		g.send(sessionID, "Fetch.continueResponse", map[string]any{"requestId": p.RequestID})
	}
	g.mu.Unlock()
	if err := g.write(id, sessionID, "Fetch.getResponseBody", map[string]any{"requestId": p.RequestID}); err != nil {
		g.mu.Lock()
		delete(g.callbacks, id)
		g.mu.Unlock()
		g.ledger.add(event)
		g.send(sessionID, "Fetch.continueResponse", map[string]any{"requestId": p.RequestID})
	}
}

// allows reports whether the operation running may send a write of
// signature.
func (g *gate) allows(signature string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.allowed[signature] && time.Now().Before(g.allowedUntil)
}

// allowance is what an operation may send while it runs.
type allowance struct {
	Signatures []string `json:"signatures"`
	// Seconds bounds how long the operation may send them.
	Seconds int `json:"seconds"`
}

func contentType(headers []header) string {
	return headerValue(headers, "Content-Type")
}

func headerValue(headers []header, name string) string {
	for _, h := range headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

// readableResponse reports whether an answer is text the page reads whole,
// so the gate may read it first. A stream or a download reaches the page
// as it arrives, and its event keeps no answer.
func readableResponse(headers []header) bool {
	disposition, _, _ := mime.ParseMediaType(headerValue(headers, "Content-Disposition"))
	if disposition == "attachment" {
		return false
	}
	mediaType, _, _ := mime.ParseMediaType(contentType(headers))
	switch {
	case mediaType == "text/event-stream", strings.HasSuffix(mediaType, "ndjson"):
		return false
	case strings.HasPrefix(mediaType, "text/"), strings.Contains(mediaType, "json"), strings.Contains(mediaType, "xml"),
		mediaType == "application/x-www-form-urlencoded":
		return true
	}
	return false
}

// attached watches a service worker or a shared worker of a site for the
// sockets it opens, then lets it run; a worker of anything else, such as an
// extension, just runs.
func (g *gate) attached(params json.RawMessage) {
	var e struct {
		SessionID  string `json:"sessionId"`
		TargetInfo struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"targetInfo"`
	}
	if json.Unmarshal(params, &e) != nil {
		return
	}
	if webURL(e.TargetInfo.URL) {
		g.mu.Lock()
		g.watched[e.SessionID] = e.TargetInfo.Type
		g.mu.Unlock()
		// The worker runs the commands in order, so it is watched before it
		// runs.
		g.send(e.SessionID, "Network.enable", map[string]any{"maxPostDataSize": 0})
	}
	g.send(e.SessionID, "Runtime.runIfWaitingForDebugger", nil)
}

// socket records a WebSocket a watched worker opened.
func (g *gate) socket(sessionID string, params json.RawMessage) {
	g.mu.Lock()
	target, ok := g.watched[sessionID]
	g.mu.Unlock()
	var e struct {
		URL string `json:"url"`
	}
	if !ok || json.Unmarshal(params, &e) != nil {
		return
	}
	address := e.URL
	if u, err := url.Parse(e.URL); err == nil {
		address = g.rules.masker.MaskString(maskQuery(u).String())
	}
	g.ledger.add(Event{Kind: KindSocket, URL: address, Target: target})
}

// controls serves the control paths: the events after a sequence number,
// and whether a person holds the browser.
func (g *gate) controls() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /"+eventsPath, func(w http.ResponseWriter, r *http.Request) {
		after, _ := strconv.ParseInt(r.URL.Query().Get(afterParam), 10, 64)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(g.ledger.after(after))
	})
	mux.HandleFunc("POST /"+beginPath, func(w http.ResponseWriter, r *http.Request) {
		var a allowance
		if err := json.NewDecoder(r.Body).Decode(&a); err != nil || a.Seconds <= 0 {
			http.Error(w, "give the signatures and a positive number of seconds", http.StatusBadRequest)
			return
		}
		allowed := map[string]bool{}
		for _, signature := range a.Signatures {
			allowed[signature] = true
		}
		g.mu.Lock()
		g.allowed, g.allowedUntil = allowed, time.Now().Add(min(time.Duration(a.Seconds)*time.Second, maxLease))
		g.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /"+endPath, func(w http.ResponseWriter, _ *http.Request) {
		g.mu.Lock()
		g.allowed, g.allowedUntil = nil, time.Time{}
		g.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /"+personPath, func(w http.ResponseWriter, r *http.Request) {
		on, err := strconv.ParseBool(r.URL.Query().Get(personParam))
		if err != nil {
			http.Error(w, "on must be true or false", http.StatusBadRequest)
			return
		}
		g.person.Store(on)
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}
