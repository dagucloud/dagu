// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aigateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
)

// Spec 094, "The relay". The relay is a small HTTP server of the worker process.
// It listens on 127.0.0.1, exists for one attempt of one step, and forwards each
// request through a Client. It holds no key.

const (
	// relaySecretBytes is the size of the secret of one attempt.
	relaySecretBytes = 32
	// relayWriteTimeout bounds one write to the client. A client that reads
	// nothing for this time loses the connection.
	relayWriteTimeout = 5 * time.Minute
)

// RelayOptions configures the relay of one attempt.
type RelayOptions struct {
	// Client carries the requests: the coordinator over gRPC, or the gateway of
	// the local process.
	Client Client
	// Step is the step that the secret is bound to.
	Step StepRef
	// Endpoint is the name of the endpoint that the step names. It is empty for a
	// step that names MCP servers only. The relay then serves no model request.
	Endpoint string
	// Protocol is the protocol of the endpoint, from the start check. It selects
	// the list of allowed requests and the error format.
	Protocol aiendpoint.Protocol
	// MCPServers are the names of the MCP server entries that the step names. The
	// relay serves /mcp/<name> for these names only.
	MCPServers []string
	// Log receives the line that tells a refusal. It can be nil.
	Log io.Writer
}

// Relay serves the requests of one attempt. It refuses each request that does not
// carry its secret.
type Relay struct {
	opts      RelayOptions
	secret    string
	secretSum [sha256.Size]byte

	ctx    context.Context
	cancel context.CancelFunc

	http     *http.Server
	listener net.Listener
	served   chan struct{}
	port     string

	closed    atomic.Bool
	closeOnce sync.Once
	logMu     sync.Mutex

	refusalMu sync.Mutex
	refusal   *Failure
}

// LastRefusal returns the last failure that the relay or the coordinator gave to
// a request, or nil. A request that the client canceled is not a refusal. The
// step uses it to give the reason of a failed attempt.
func (r *Relay) LastRefusal() *Failure {
	r.refusalMu.Lock()
	defer r.refusalMu.Unlock()
	return r.refusal
}

func (r *Relay) noteRefusal(failure *Failure) {
	r.refusalMu.Lock()
	defer r.refusalMu.Unlock()
	r.refusal = failure
}

// StartRelay starts the relay on a random free port of 127.0.0.1. The secret is
// 32 random bytes of the system random generator. The context is the context of
// the step: the relay ends when it ends.
func StartRelay(ctx context.Context, opts RelayOptions) (*Relay, error) {
	if opts.Client == nil {
		return nil, errors.New("the AI relay needs a client")
	}
	raw := make([]byte, relaySecretBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("the AI relay cannot make its secret: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(raw)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("the AI relay cannot listen on 127.0.0.1: %w", err)
	}
	_, port, _ := net.SplitHostPort(listener.Addr().String())

	r := &Relay{
		opts:      opts,
		secret:    secret,
		secretSum: sha256.Sum256([]byte(secret)),
		listener:  listener,
		served:    make(chan struct{}),
		port:      port,
	}
	r.ctx, r.cancel = context.WithCancel(ctx)
	r.http = &http.Server{
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return r.ctx },
	}
	go func() {
		defer close(r.served)
		if err := r.http.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Warn(r.ctx, "The AI relay stopped serving", slog.String("error", err.Error()))
		}
	}()
	// The relay ends with the context of the step.
	go func() {
		<-r.ctx.Done()
		_ = r.Close()
	}()
	// The line names what the secret is bound to, and never the secret.
	logger.Info(ctx, "AI relay started",
		slog.String("address", listener.Addr().String()),
		slog.String("dag", opts.Step.DAGName),
		slog.String("step", opts.Step.StepName),
		slog.String("endpoint", opts.Endpoint),
		slog.String("mcp_servers", strings.Join(opts.MCPServers, ",")),
	)
	return r, nil
}

// URL returns the base URL of the relay for the client: http://127.0.0.1:<port>.
// It holds no secret. A client of the protocol openai adds /v1.
func (r *Relay) URL() string { return "http://127.0.0.1:" + r.port }

// Secret returns the secret of the attempt. The caller gives it to the client as
// its API key, and to a file of mode 0400. It must not enter a log or an error.
func (r *Relay) Secret() string { return r.secret }

// Close stops the relay: it ends the open requests, closes the listener and the
// open connections, and invalidates the secret. It waits until the server has
// stopped. It can run more than once.
func (r *Relay) Close() error {
	var err error
	r.closeOnce.Do(func() {
		r.closed.Store(true)
		r.cancel()
		if closeErr := r.http.Close(); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
			err = closeErr
		}
		<-r.served
	})
	return err
}

// logf writes one line to the step log.
func (r *Relay) logf(format string, args ...any) {
	if r.opts.Log == nil {
		return
	}
	r.logMu.Lock()
	defer r.logMu.Unlock()
	_, _ = fmt.Fprintf(r.opts.Log, "ai relay: "+format+"\n", args...)
}

// ServeHTTP implements http.Handler. It applies the checks of Spec 094, "The
// relay" and "Allowed requests": the form of the request and the Host header, the
// secret, then the path, the method and the query.
func (r *Relay) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// A request with no secret can come from any local process. Its refusal is
	// logged, but it does not become the reason of the attempt.
	if failure := r.checkForm(req); failure != nil {
		r.logRefusal(req, failure)
		r.writeFailureFor(w, req, failure)
		return
	}
	if !r.authorized(req) {
		failure := Fail(ReasonRelayUnauthorized, "the request has no valid relay secret")
		w.Header().Set("WWW-Authenticate", `Bearer realm="dagu"`)
		r.logRefusal(req, failure)
		r.writeFailureFor(w, req, failure)
		return
	}
	target, head, failure := r.route(req)
	if failure != nil {
		r.refuse(w, req, failure)
		return
	}

	sink := &httpSink{w: w, rc: http.NewResponseController(w)}
	err := r.opts.Client.Relay(req.Context(), r.opts.Step, target, head, req.Body, sink)
	if err == nil {
		return
	}
	failure = FailureOf(err)
	if failure.Reason == ReasonCanceled {
		return
	}
	r.logf("refuse %s %s: %s", req.Method, logSafe(head.Path), failure.Reason)
	r.noteRefusal(failure)
	if sink.headSent {
		// The response is cut. The client must see a broken connection, not a
		// clean end.
		panic(http.ErrAbortHandler)
	}
	r.writeFailureFor(w, req, failure)
}

// route applies the list of allowed requests to a request. A path under /mcp/ is a
// request for a MCP server entry that the step names. Another path is a model
// request, which needs the endpoint of the step.
func (r *Relay) route(req *http.Request) (Target, HTTPRequest, *Failure) {
	if IsMCPPath(req.URL.Path) {
		name, failure := AllowMCP(req.Method, req.URL.Path, req.URL.RawQuery)
		if failure != nil {
			return Target{}, HTTPRequest{}, failure
		}
		if !slices.Contains(r.opts.MCPServers, name) {
			return Target{}, HTTPRequest{}, Fail(ReasonMCPServerForbidden, "the step does not name the MCP server %q", name)
		}
		header, _, failure := FilterMCPRequestHeaders(req.Header)
		if failure != nil {
			return Target{}, HTTPRequest{}, failure
		}
		return MCPTarget(name), HTTPRequest{
			Method: req.Method, Path: req.URL.Path, Header: header, ContentLength: req.ContentLength,
		}, nil
	}
	if r.opts.Endpoint == "" {
		return Target{}, HTTPRequest{}, Fail(ReasonPathNotAllowed, "the step names no endpoint, so the relay serves no model request")
	}
	route, failure := Allow(r.opts.Protocol, req.Method, req.URL.Path, req.URL.RawQuery)
	if failure != nil {
		return Target{}, HTTPRequest{}, failure
	}
	header, _, failure := FilterRequestHeaders(req.Header)
	if failure != nil {
		return Target{}, HTTPRequest{}, failure
	}
	return EndpointTarget(r.opts.Endpoint), HTTPRequest{
		Method: req.Method, Path: route.Path, RawQuery: route.Query, Header: header, ContentLength: req.ContentLength,
	}, nil
}

// checkForm refuses a request that is not in origin form, a CONNECT request and a
// Host header other than 127.0.0.1:<port> or localhost:<port>.
func (r *Relay) checkForm(req *http.Request) *Failure {
	if req.Method == http.MethodConnect {
		return Fail(ReasonHostNotAllowed, "the method CONNECT is not allowed")
	}
	if req.URL.IsAbs() || req.URL.Host != "" || !strings.HasPrefix(req.RequestURI, "/") {
		return Fail(ReasonHostNotAllowed, "the relay serves the origin form only")
	}
	switch strings.ToLower(req.Host) {
	case "127.0.0.1:" + r.port, "localhost:" + r.port:
		return nil
	}
	return Fail(ReasonHostNotAllowed, "the Host header is not the address of the relay")
}

// authorized reports whether the request carries the secret, in the header that
// the client uses for its API key. The comparison takes the same time for each
// wrong value.
func (r *Relay) authorized(req *http.Request) bool {
	if r.closed.Load() {
		return false
	}
	candidates := make([]string, 0, 2)
	if value := req.Header.Values("X-Api-Key"); len(value) == 1 {
		candidates = append(candidates, value[0])
	}
	if value := req.Header.Values("Authorization"); len(value) == 1 {
		if scheme, token, ok := strings.Cut(value[0], " "); ok && strings.EqualFold(scheme, "Bearer") {
			candidates = append(candidates, strings.TrimSpace(token))
		}
	}
	if len(candidates) != 1 {
		// A request with the secret in two headers, or in none, is refused.
		return false
	}
	sum := sha256.Sum256([]byte(candidates[0]))
	return subtle.ConstantTimeCompare(sum[:], r.secretSum[:]) == 1
}

func (r *Relay) refuse(w http.ResponseWriter, req *http.Request, failure *Failure) {
	r.logRefusal(req, failure)
	r.noteRefusal(failure)
	r.writeFailureFor(w, req, failure)
}

// logRefusal writes the line of a refusal to the step log.
func (r *Relay) logRefusal(req *http.Request, failure *Failure) {
	r.logf("refuse %s %s: %s", req.Method, logSafe(req.URL.Path), failure.Reason)
}

// logSafe replaces each control character of a path that a client sent, so the
// path cannot add a line to the step log.
func logSafe(path string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, path)
}

// writeFailureFor writes a failure in the error format of the request: a JSON-RPC
// error for a MCP path, and the error format of the protocol for a model request.
func (r *Relay) writeFailureFor(w http.ResponseWriter, req *http.Request, failure *Failure) {
	if IsMCPPath(req.URL.Path) {
		contentType, body := MCPErrorBody(failure)
		r.writeBody(w, failure, contentType, body)
		return
	}
	r.writeFailure(w, failure)
}

func (r *Relay) writeFailure(w http.ResponseWriter, failure *Failure) {
	contentType, body := ErrorBody(r.opts.Protocol, failure)
	r.writeBody(w, failure, contentType, body)
}

func (r *Relay) writeBody(w http.ResponseWriter, failure *Failure, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	// Claude Code repeats a request that gets a 401 with no such header until its time
	// ends. The provider sets the header on its own answers, and the relay does so on its
	// refusals that no later request can change.
	if status := failure.Reason.Status(); status >= http.StatusBadRequest && !failure.Reason.Retryable() {
		w.Header().Set("X-Should-Retry", "false")
	}
	w.WriteHeader(failure.Reason.Status())
	_, _ = w.Write(body) //nolint:gosec // G705: the body is JSON that ErrorBody builds, and the content type is application/json
}

// httpSink writes the response of the gateway to the client, and flushes each
// chunk at once.
type httpSink struct {
	w        http.ResponseWriter
	rc       *http.ResponseController
	headSent bool
}

func (s *httpSink) Head(_ context.Context, status int, header http.Header) error {
	maps.Copy(s.w.Header(), header)
	s.w.WriteHeader(status)
	s.headSent = true
	return s.rc.Flush()
}

func (s *httpSink) Write(_ context.Context, chunk []byte) error {
	_ = s.rc.SetWriteDeadline(time.Now().Add(relayWriteTimeout))
	if _, err := s.w.Write(chunk); err != nil {
		return err
	}
	return s.rc.Flush()
}
