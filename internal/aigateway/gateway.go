// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aigateway

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
)

// EndpointSource gives the gateway the endpoints and their keys. The type
// *aiendpoint.Service implements it.
type EndpointSource interface {
	ResolveEndpoint(ctx context.Context, name, dagName, workspace string) (aiendpoint.Endpoint, error)
	ReadKey(ctx context.Context, ep aiendpoint.Endpoint) (string, error)
}

var _ EndpointSource = (*aiendpoint.Service)(nil)

// ModelChecker is the optional part of an EndpointSource that checks the model of
// a step against the models list of its endpoint. The type *aiendpoint.Service
// implements it. A source without it makes no model check.
type ModelChecker interface {
	CheckModel(ctx context.Context, ep aiendpoint.Endpoint, provider, model string) error
}

var _ ModelChecker = (*aiendpoint.Service)(nil)

// MCPSource gives the gateway the MCP server entries and their credentials. The
// type *aiendpoint.MCPService implements it.
type MCPSource interface {
	ResolveMCPServer(ctx context.Context, name, dagName, workspace string) (aiendpoint.MCPServer, error)
	ReadCredentials(ctx context.Context, server aiendpoint.MCPServer) (aiendpoint.MCPCredentials, error)
}

var _ MCPSource = (*aiendpoint.MCPService)(nil)

// Subject is the run and the step that make a request. The coordinator takes the
// values from the DAG that it holds, never from the request. The local mode
// takes them from the step that runs in the process.
type Subject struct {
	RunID      string
	DAGName    string
	Workspace  string
	StepName   string
	WorkerID   string
	AttemptKey string
}

// CheckResult is what the start check gives to the worker.
type CheckResult struct {
	Protocol aiendpoint.Protocol
	// ChatType is the provider type of chat.completion for the endpoint.
	ChatType string
}

// Request is one HTTP request of a client, after the identity checks.
type Request struct {
	Subject
	// Target is the endpoint or the MCP server entry that the step names.
	Target Target
	// MCPAllow is the key allow of the step for a MCP target: the tools that a
	// tools/call can name. The coordinator takes it from the DAG that it holds.
	MCPAllow []string
	Method   string
	// Path and RawQuery are the path and the query that the client sent to the relay.
	Path          string
	RawQuery      string
	Header        http.Header
	ContentLength int64 // -1 when unknown
	Body          io.Reader
}

// ResponseSink receives the response. The gateway calls Head once, then Write for
// each chunk. A call returns when the context ends. An error of a call means
// that the client is gone.
type ResponseSink interface {
	Head(ctx context.Context, status int, header http.Header) error
	Write(ctx context.Context, chunk []byte) error
}

// AuditRecord is the content of the audit line of one request. It never holds a
// key, a prefix, a header value, a body or a model name.
type AuditRecord struct {
	RunID         string
	DAGName       string
	StepName      string
	WorkerID      string
	TargetKind    string
	Name          string
	Scope         string
	Method        string
	Route         string
	Status        int
	Reason        Reason
	RequestBytes  int64
	ResponseBytes int64
	DurationMs    int64
	// FirstByteMs is the time from the start of the request to the first byte of the
	// response that the sink took. It is 0 when no byte went to the sink. Next to
	// DurationMs it shows if the response streams: a stream has a first byte long
	// before its end.
	FirstByteMs int64
	// started is the time of the start of the request.
	started time.Time
	// Fingerprint is the fingerprint of the key that the request used.
	Fingerprint string
	// RPCMethod and Tool are the JSON-RPC methods and the tool names of a MCP
	// request, joined with a comma. They are empty for a model request.
	RPCMethod string
	Tool      string
}

// AuditFunc receives each audit record. The log line of the gateway is always
// written, whatever the function does.
type AuditFunc func(ctx context.Context, record AuditRecord)

// Config configures a Gateway.
type Config struct {
	Endpoints EndpointSource
	// MCPServers gives the MCP server entries. It can be nil: a request for a MCP
	// target then fails with the reason endpoint_unavailable.
	MCPServers MCPSource
	Limits     Limits
	// ProxyURL is the value of ai.upstream.proxy_url. It is empty for a direct
	// call. The gateway never reads the variables HTTP_PROXY, HTTPS_PROXY or
	// NO_PROXY.
	ProxyURL string
	Audit    AuditFunc
	// Transport replaces the transport of the calls to the provider. A test uses
	// it. The redirect rule and the headers do not depend on it.
	Transport http.RoundTripper
}

// Gateway is the code of the coordinator that checks a request, adds the key,
// calls the provider and streams the response. The local mode runs it in the
// process of the run.
type Gateway struct {
	endpoints EndpointSource
	mcp       MCPSource
	limits    Limits
	client    *http.Client
	audit     AuditFunc
	streams   *streamCounter
	requests  atomic.Int64
}

// New creates a Gateway. It checks the proxy URL.
func New(cfg Config) (*Gateway, error) {
	if cfg.Endpoints == nil {
		return nil, errors.New("AI gateway: the endpoint source cannot be nil")
	}
	transport := cfg.Transport
	if transport == nil {
		var err error
		transport, err = newTransport(cfg.Limits, cfg.ProxyURL)
		if err != nil {
			return nil, err
		}
	}
	return &Gateway{
		endpoints: cfg.Endpoints,
		mcp:       cfg.MCPServers,
		limits:    cfg.Limits,
		client:    newUpstreamClient(transport),
		audit:     cfg.Audit,
		streams:   newStreamCounter(cfg.Limits.MaxStreams, cfg.Limits.MaxStreamsPerRun),
	}, nil
}

// newUpstreamClient wraps a transport in the client of the calls to the provider.
// A redirect never leaves the host of the base URL: the client does not follow
// it, and the caller reports an error.
func newUpstreamClient(transport http.RoundTripper) *http.Client {
	return &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// NewUpstreamClient returns the HTTP client that the gateway uses for the calls
// to a provider: the proxy of ai.upstream.proxy_url only, TLS verification on, no
// compression and no redirect. The models list uses it too, so both reach the
// provider in the same way.
func NewUpstreamClient(limits Limits, proxyURL string) (*http.Client, error) {
	transport, err := newTransport(limits, proxyURL)
	if err != nil {
		return nil, err
	}
	return newUpstreamClient(transport), nil
}

// newTransport builds the transport of the calls to the provider: TLS
// verification on, no compression, the proxy of the configuration only.
func newTransport(limits Limits, proxyURL string) (*http.Transport, error) {
	dialer := &net.Dialer{Timeout: limits.ConnectTimeout, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		// An explicit nil: the transport never reads HTTP_PROXY or NO_PROXY.
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   limits.ConnectTimeout,
		ResponseHeaderTimeout: limits.ResponseHeaderTimeout,
		DisableCompression:    true,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
	}
	if proxyURL != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, errors.New("AI gateway: ai.upstream.proxy_url is not an http or https URL")
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	return transport, nil
}

// Limits returns the limits of the gateway.
func (g *Gateway) Limits() Limits { return g.limits }

// OpenStreams returns the number of streams that are open now.
func (g *Gateway) OpenStreams() int { return g.streams.open() }

// Check applies the checks 5 to 9 of Spec 094, "Identity checks", that do not
// depend on the transport: the target exists in a scope that the DAG sees, the
// DAG may use it and, for an endpoint, the protocol matches the client and the
// endpoint has a key. It reads no key value.
func (g *Gateway) Check(ctx context.Context, sub Subject, target Target, provider string) (CheckResult, *Failure) {
	return g.CheckModel(ctx, sub, target, provider, "")
}

// CheckModel is Check with the model rule of Spec 093, "Use in a step". When model
// is not empty, it must pass the models list of the endpoint of an endpoint target.
func (g *Gateway) CheckModel(ctx context.Context, sub Subject, target Target, provider, model string) (CheckResult, *Failure) {
	if target.Kind == TargetMCP {
		return CheckResult{}, g.checkMCP(ctx, sub, target.Name)
	}
	ep, err := g.endpoints.ResolveEndpoint(ctx, target.Name, sub.DAGName, sub.Workspace)
	if err != nil {
		return CheckResult{}, FailureOf(err)
	}
	if provider != "" {
		if err := ep.CheckProtocol(provider); err != nil {
			return CheckResult{}, FailureOf(err)
		}
	}
	if err := ep.CheckKey(); err != nil {
		return CheckResult{}, FailureOf(err)
	}
	if checker, ok := g.endpoints.(ModelChecker); ok && model != "" {
		if err := checker.CheckModel(ctx, ep, provider, model); err != nil {
			return CheckResult{}, FailureOf(err)
		}
	}
	result := CheckResult{Protocol: ep.Protocol}
	if row, ok := aiendpoint.LookupPreset(ep.Preset, ep.Protocol); ok {
		result.ChatType = row.ChatType
	}
	return result, nil
}

// AuditRefusal writes the audit line of a relay request that the checks before Do
// refused, so that a refusal leaves a line as a call does. The subject can lack the
// run ID when the refusal came from a check of the identity.
func (g *Gateway) AuditRefusal(ctx context.Context, sub Subject, target Target, method string, failure *Failure) {
	record := AuditRecord{
		RunID: sub.RunID, DAGName: sub.DAGName, StepName: sub.StepName, WorkerID: sub.WorkerID,
		TargetKind: "endpoint", Name: target.Name, Method: method,
		Status: failure.Reason.Status(), Reason: failure.Reason,
	}
	if target.Kind == TargetMCP {
		record.TargetKind, record.Route = "mcp", RouteMCP
	}
	g.writeAudit(ctx, record)
}

// Do carries one request to the provider. The caller made the identity checks.
// Do resolves the endpoint again for each request, so a deleted endpoint, a new
// allow list or a new key apply to the next request. It returns nil when the
// whole response went to the sink. It returns a failure otherwise. A failure
// after the call of Head means that the response is cut.
func (g *Gateway) Do(ctx context.Context, req Request, sink ResponseSink) (failure *Failure) {
	if req.Target.Kind == TargetMCP {
		return g.doMCP(ctx, req, sink)
	}
	start := time.Now()
	record := AuditRecord{
		RunID: req.RunID, DAGName: req.DAGName, StepName: req.StepName, WorkerID: req.WorkerID,
		TargetKind: "endpoint", Name: req.Target.Name, Method: req.Method, started: start,
	}
	defer func() {
		record.DurationMs = time.Since(start).Milliseconds()
		if failure != nil {
			record.Reason = failure.Reason
			if record.Status == 0 {
				record.Status = failure.Reason.Status()
			}
		}
		g.writeAudit(ctx, record)
	}()

	ep, err := g.endpoints.ResolveEndpoint(ctx, req.Target.Name, req.DAGName, req.Workspace)
	if err != nil {
		return FailureOf(err)
	}
	record.Scope = ep.Scope.String()
	record.Fingerprint = ep.KeyFingerprint

	route, failure := Allow(ep.Protocol, req.Method, req.Path, req.RawQuery)
	if failure != nil {
		return failure
	}
	record.Route = route.Name
	header, headerBytes, failure := FilterRequestHeaders(req.Header)
	if failure != nil {
		return failure
	}
	if headerBytes > int(g.limits.MaxHeaderBytes) {
		return Fail(ReasonRequestTooLarge, "the request headers have more than %d bytes", g.limits.MaxHeaderBytes)
	}
	if req.ContentLength > g.limits.MaxRequestBytes {
		return Fail(ReasonRequestTooLarge, "the request body has more than %d bytes", g.limits.MaxRequestBytes)
	}

	release, failure := g.streams.acquire(req.AttemptKey)
	if failure != nil {
		return failure
	}
	defer release()

	// The total timeout includes the read of the body.
	callCtx, cancel := context.WithTimeout(ctx, g.limits.TotalTimeout)
	defer cancel()

	// The whole body is read before the call, so the call has a content-length.
	body, failure := g.readBody(ctx, callCtx, req.Body)
	if failure != nil {
		return failure
	}
	record.RequestBytes = int64(len(body))

	key, err := g.endpoints.ReadKey(callCtx, ep)
	if err != nil {
		return FailureOf(err)
	}
	httpReq, failure := buildUpstreamRequest(callCtx, ep, route, req.Method, header, body, key)
	if failure != nil {
		return failure
	}

	return g.roundTrip(ctx, callCtx, cancel, httpReq, sink, &record)
}

// roundTrip sends the call to the upstream and streams its response to the sink.
// It does not follow a redirect: a status 3xx is an upstream_error. It sets the
// status and the number of response bytes in the audit record.
func (g *Gateway) roundTrip(ctx, callCtx context.Context, cancel context.CancelFunc, httpReq *http.Request, sink ResponseSink, record *AuditRecord) *Failure {
	resp, err := g.client.Do(httpReq)
	if err != nil {
		return g.classify(ctx, callCtx, err)
	}
	defer func() { _ = resp.Body.Close() }()
	record.Status = resp.StatusCode
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		record.Status = 0
		return Fail(ReasonUpstreamError, "the provider answered with the redirect status %d, which the gateway does not follow", resp.StatusCode)
	}
	if err := sink.Head(callCtx, resp.StatusCode, FilterResponseHeaders(resp.Header)); err != nil {
		return g.classify(ctx, callCtx, err)
	}

	total, failure := g.copyResponse(ctx, callCtx, cancel, resp.Body, &firstByteSink{ResponseSink: sink, record: record})
	record.ResponseBytes = total
	return failure
}

// firstByteSink notes in the audit record the time of the first chunk that the
// wrapped sink took.
type firstByteSink struct {
	ResponseSink
	record *AuditRecord
	seen   bool
}

func (s *firstByteSink) Write(ctx context.Context, chunk []byte) error {
	err := s.ResponseSink.Write(ctx, chunk)
	if err == nil && !s.seen && len(chunk) > 0 {
		s.seen = true
		s.record.FirstByteMs = time.Since(s.record.started).Milliseconds()
	}
	return err
}

// copyResponse reads the body of the provider in chunks and gives each chunk to
// the sink at once. A watchdog ends the call when no byte moves for the idle
// timeout. It returns the number of bytes that the sink took.
func (g *Gateway) copyResponse(ctx, callCtx context.Context, cancel context.CancelFunc, body io.Reader, sink ResponseSink) (int64, *Failure) {
	var idle atomic.Bool
	watchdog := time.AfterFunc(g.limits.IdleTimeout, func() {
		idle.Store(true)
		cancel()
	})
	defer watchdog.Stop()

	buf := make([]byte, ChunkBytes)
	var total int64
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			watchdog.Reset(g.limits.IdleTimeout)
			if total+int64(n) > g.limits.MaxResponseBytes {
				return total, Fail(ReasonResponseTooLarge, "the response has more than %d bytes", g.limits.MaxResponseBytes)
			}
			if err := sink.Write(callCtx, buf[:n]); err != nil {
				if idle.Load() {
					return total, Fail(ReasonUpstreamTimeout, "no byte moved for %s", g.limits.IdleTimeout)
				}
				return total, g.classify(ctx, callCtx, err)
			}
			total += int64(n)
			watchdog.Reset(g.limits.IdleTimeout)
		}
		if readErr == io.EOF {
			return total, nil
		}
		if readErr != nil {
			if idle.Load() {
				return total, Fail(ReasonUpstreamTimeout, "no byte moved for %s", g.limits.IdleTimeout)
			}
			return total, g.classify(ctx, callCtx, readErr)
		}
	}
}

// classify maps an error of the call to a failure. A parent context that ended
// means that the client, the attempt or the lease ended the call.
func (g *Gateway) classify(parent, call context.Context, err error) *Failure {
	switch {
	case parent.Err() != nil:
		return Fail(ReasonCanceled, "the request was canceled")
	case errors.Is(call.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		return Fail(ReasonUpstreamTimeout, "the request took more than %s", g.limits.TotalTimeout)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return Fail(ReasonUpstreamTimeout, "the call to the provider timed out")
	}
	// The client gets a fixed text: the error can name addresses of the network
	// of the coordinator. The log of the coordinator keeps the error.
	logger.Warn(parent, "AI gateway: the call to the upstream failed", tag.Error(err))
	return Fail(ReasonUpstreamError, "%s", upstreamCause(err))
}

// upstreamCause returns the fixed text of an error of the call to the upstream.
func upstreamCause(err error) string {
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return "the upstream refused the connection"
	}
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return "the host name of the upstream does not resolve"
	}
	if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		return "the TLS certificate of the upstream is not valid"
	}
	return "the call to the upstream failed"
}

// readBody reads the whole request body within the total timeout of the call.
// A body that moves no byte for the idle timeout gives upstream_timeout, and a
// body of more than max_request_bytes gives request_too_large. When it returns
// early, the read goes on in the background until the body source ends; it
// writes to no shared state.
func (g *Gateway) readBody(parent, call context.Context, r io.Reader) ([]byte, *Failure) {
	if r == nil {
		return nil, nil
	}
	max := g.limits.MaxRequestBytes
	type result struct {
		data []byte
		err  error
	}
	moved := make(chan struct{}, 1)
	done := make(chan result, 1)
	go func() {
		var buf bytes.Buffer
		limited := io.LimitReader(r, max+1)
		chunk := make([]byte, ChunkBytes)
		for {
			n, err := limited.Read(chunk)
			if n > 0 {
				buf.Write(chunk[:n])
				select {
				case moved <- struct{}{}:
				default:
				}
			}
			if errors.Is(err, io.EOF) {
				done <- result{data: buf.Bytes()}
				return
			}
			if err != nil {
				done <- result{err: err}
				return
			}
		}
	}()
	idle := time.NewTimer(g.limits.IdleTimeout)
	defer idle.Stop()
	for {
		select {
		case res := <-done:
			if res.err != nil {
				if call.Err() != nil {
					return nil, g.classify(parent, call, call.Err())
				}
				return nil, Fail(ReasonRelayUnavailable, "the request body could not be read: %s", res.err.Error())
			}
			if int64(len(res.data)) > max {
				return nil, Fail(ReasonRequestTooLarge, "the request body has more than %d bytes", max)
			}
			return res.data, nil
		case <-moved:
			idle.Reset(g.limits.IdleTimeout)
		case <-idle.C:
			return nil, Fail(ReasonUpstreamTimeout, "no byte of the request body came for %s", g.limits.IdleTimeout)
		case <-call.Done():
			return nil, g.classify(parent, call, call.Err())
		}
	}
}

// buildUpstreamRequest builds the call from the base URL of the endpoint, the
// route and the headers of the allowed list. It sets the credential of the form
// of the endpoint, and removes each credential of the client.
func buildUpstreamRequest(ctx context.Context, ep aiendpoint.Endpoint, route Route, method string, header http.Header, body []byte, key string) (*http.Request, *Failure) {
	target, err := url.Parse(ep.BaseURL)
	if err != nil || target.Host == "" {
		return nil, Fail(ReasonEndpointUnavailable, "the base URL of the endpoint is not valid")
	}
	target.Path = UpstreamPath(ep.Protocol, target.Path, route.Path)
	target.RawPath = ""
	target.RawQuery = route.Query
	var reader io.Reader
	if method != http.MethodGet && method != http.MethodHead {
		reader = bytes.NewReader(body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return nil, Fail(ReasonUpstreamError, "the call to the provider cannot be built")
	}
	httpReq.Header = header.Clone()
	httpReq.Header.Set("Accept-Encoding", "identity")
	aiendpoint.SetCredential(httpReq.Header, ep.Auth, key)
	return httpReq, nil
}

// writeAudit writes the audit line of a request. The line has the fields of Spec
// 094, "Audit", and never a key.
func (g *Gateway) writeAudit(ctx context.Context, r AuditRecord) {
	g.requests.Add(1)
	attrs := []slog.Attr{
		slog.String("action", "ai_relay_request"),
		slog.String("run_id", r.RunID),
		slog.String("dag", r.DAGName),
		slog.String("step", r.StepName),
		slog.String("worker_id", r.WorkerID),
		slog.String("target_kind", r.TargetKind),
		slog.String("target", r.Name),
		slog.String("scope", r.Scope),
		slog.String("method", r.Method),
		slog.String("route", r.Route),
		slog.Int("status", r.Status),
		slog.String("reason", string(r.Reason)),
		slog.Int64("request_bytes", r.RequestBytes),
		slog.Int64("response_bytes", r.ResponseBytes),
		slog.Int64("first_byte_ms", r.FirstByteMs),
		slog.Int64("duration_ms", r.DurationMs),
		slog.String("key_fingerprint", r.Fingerprint),
	}
	if r.RPCMethod != "" {
		attrs = append(attrs, slog.String("rpc_method", r.RPCMethod))
	}
	if r.Tool != "" {
		attrs = append(attrs, slog.String("tool", r.Tool))
	}
	logger.Info(ctx, "AI relay request", attrs...)
	if g.audit != nil {
		g.audit(ctx, r)
	}
}
