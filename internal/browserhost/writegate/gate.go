// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package writegate

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
	afterParam  = "after"
	personParam = "on"
)

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
		rules:     newRules(cfg.Policy, cfg.Secrets),
		ledger:    &ledger{},
		bodyLimit: limit,
		waiters:   map[int64]chan reply{},
		watched:   map[string]string{},
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
	rules     *rules
	ledger    *ledger
	bodyLimit int
	person    atomic.Bool
	nextID    atomic.Int64

	mu      sync.Mutex
	waiters map[int64]chan reply
	// watched maps the sessions of the service workers and shared workers
	// the gate watches for sockets to their kind of target.
	watched map[string]string
	done    chan struct{}
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
		{"Target.setAutoAttach", map[string]any{
			"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true,
			"filter": []map[string]any{{"type": targetServiceWorker}, {"type": targetSharedWorker}, {"exclude": true}},
		}},
	}
	for _, command := range commands {
		if _, err := g.call(ctx, "", command.method, command.params); err != nil {
			return nil, err
		}
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
			g.mu.Lock()
			wait := g.waiters[m.ID]
			g.mu.Unlock()
			if wait != nil {
				r := reply{result: m.Result}
				if m.Error != nil {
					r.err = errors.New(m.Error.Message)
				}
				wait <- r
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

// pausedRequest is a Fetch.requestPaused event, with its body cut.
type pausedRequest struct {
	RequestID    string `json:"requestId"`
	ResourceType string `json:"resourceType"`
	Request      struct {
		URL          string            `json:"url"`
		Method       string            `json:"method"`
		Headers      map[string]string `json:"headers"`
		PostData     string            `json:"postData"`
		PostDataSize int64             `json:"postDataSize"`
	} `json:"request"`
}

// paused decides a paused request at once. The browser target sees every
// request; a page target, armed for a page loaded before the gate, sees
// its page's first, so a request that target lets through is kept only
// when the browser target decides it.
func (g *gate) paused(sessionID string, params json.RawMessage) {
	var p pausedRequest
	if err := json.Unmarshal(params, &p); err != nil {
		return
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
	if !write || class == ClassSignIn || class == ClassPerson {
		g.send(sessionID, "Fetch.continueRequest", map[string]any{"requestId": p.RequestID})
		if write && sessionID == "" {
			g.keep(req, class, OutcomeSent)
		}
		return
	}
	if req.ResourceType == resourceDocument {
		g.send(sessionID, "Fetch.fulfillRequest", map[string]any{
			"requestId": p.RequestID, "responseCode": http.StatusOK,
			"responseHeaders": []map[string]string{{"name": "Content-Type", "value": "text/html; charset=utf-8"}},
			"body":            base64.StdEncoding.EncodeToString([]byte(notSentPage)),
		})
	} else {
		g.send(sessionID, "Fetch.failRequest", map[string]any{"requestId": p.RequestID, "errorReason": accessDenied})
	}
	g.keep(req, class, OutcomeBlocked)
}

// keep records a decided write.
func (g *gate) keep(req request, class, outcome string) {
	d := g.rules.describe(req)
	g.ledger.add(Event{
		Kind: KindWrite, Method: req.Method, URL: d.URL, ResourceType: req.ResourceType,
		Class: class, Outcome: outcome, Signature: d.Signature, ContentType: req.ContentType,
		Fields: d.Fields, Body: d.Body, BodyBytes: req.BodyBytes,
	})
}

// attached watches a service worker or a shared worker of a site for the
// sockets it opens.
func (g *gate) attached(params json.RawMessage) {
	var e struct {
		SessionID  string `json:"sessionId"`
		TargetInfo struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"targetInfo"`
	}
	if json.Unmarshal(params, &e) != nil || !webURL(e.TargetInfo.URL) {
		return
	}
	g.mu.Lock()
	g.watched[e.SessionID] = e.TargetInfo.Type
	g.mu.Unlock()
	g.send(e.SessionID, "Network.enable", map[string]any{"maxPostDataSize": 0})
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
