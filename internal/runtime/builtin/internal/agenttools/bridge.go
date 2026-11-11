// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agenttools

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The tool bridge of Spec 091 gives the tools of the effective catalog (a DAG
// entry) to a CLI agent. It is an MCP server over the
// streamable HTTP transport that exists for one attempt.

const (
	// BridgeServerName is the name of the server of the bridge in the MCP config
	// of the CLI. A tool of the bridge is mcp__dagu__<name>.
	BridgeServerName = "dagu"
	// BridgePath is the path of the MCP endpoint.
	BridgePath = "/mcp"
	// DefaultMaxToolCalls is the default of max_tool_calls.
	DefaultMaxToolCalls = ir.DefaultAgentMaxToolCalls

	// secretBytes is the size of the secret of one attempt.
	secretBytes = 32
)

// BridgeOptions configures the bridge of one attempt.
type BridgeOptions struct {
	// Registry is the effective catalog. It must offer at least one tool.
	Registry *ToolRegistry
	// WorkDir is the working directory of the step. A child run starts from it.
	WorkDir string
	// ResultMaxBytes cuts one result. Zero means no limit.
	ResultMaxBytes int
	// MaxToolCalls is the number of calls that one attempt can make. A value
	// of zero or less selects DefaultMaxToolCalls.
	MaxToolCalls int
	// DAGRunID and Step name what the secret is bound to.
	DAGRunID string
	Step     string
}

// Bridge serves the catalog of one attempt as an MCP server on the loopback
// address. It refuses every request that does not carry the secret of the
// attempt.
type Bridge struct {
	opts      BridgeOptions
	maxCalls  int
	executor  *ToolExecutor
	names     []string
	known     map[string]struct{}
	secret    string
	secretSum [sha256.Size]byte

	ctx    context.Context
	cancel context.CancelFunc

	server   *mcpsdk.Server
	http     *http.Server
	listener net.Listener
	served   chan struct{}
	url      string

	// sem lets one call run at a time. A channel queues its waiting senders in
	// the order of arrival.
	sem chan struct{}

	mu        sync.Mutex
	attempted int
	executed  int
	subRuns   []ir.SubDAGRun

	closed    atomic.Bool
	closeOnce sync.Once
	closeErr  error
}

// StartBridge starts the bridge on a random free port of 127.0.0.1. The secret
// is 32 random bytes. The context is the context of the step: a child run of a
// tool starts under it, and the bridge ends the run when it closes.
func StartBridge(ctx context.Context, opts BridgeOptions) (*Bridge, error) {
	if opts.Registry == nil || !opts.Registry.HasTools() {
		return nil, errors.New("the tool bridge needs a catalog with at least one tool")
	}
	secret, err := newBridgeSecret()
	if err != nil {
		return nil, err
	}

	maxCalls := opts.MaxToolCalls
	if maxCalls <= 0 {
		maxCalls = DefaultMaxToolCalls
	}
	executor := NewToolExecutor(opts.Registry, opts.WorkDir)
	executor.SetResultLimit(opts.ResultMaxBytes)

	b := &Bridge{
		opts:      opts,
		maxCalls:  maxCalls,
		executor:  executor,
		names:     opts.Registry.names(),
		known:     make(map[string]struct{}),
		secret:    secret,
		secretSum: sha256.Sum256([]byte(secret)),
		served:    make(chan struct{}),
		sem:       make(chan struct{}, 1),
	}
	b.ctx, b.cancel = context.WithCancel(ctx)

	b.server = mcpsdk.NewServer(&mcpsdk.Implementation{Name: BridgeServerName, Version: "v2"}, nil)
	b.server.AddReceivingMiddleware(b.routeCalls)
	for _, tool := range opts.Registry.ToLLMTools() {
		if err := b.addTool(tool); err != nil {
			b.cancel()
			return nil, err
		}
		b.known[tool.Function.Name] = struct{}{}
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.cancel()
		return nil, fmt.Errorf("the tool bridge cannot listen on 127.0.0.1: %w", err)
	}
	b.listener = listener
	b.url = "http://" + listener.Addr().String() + BridgePath

	mcpHandler := mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return b.server },
		nil)
	b.http = &http.Server{
		Handler:           b.authenticate(mcpHandler),
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return b.ctx },
	}
	go func() {
		defer close(b.served)
		if err := b.http.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Warn(b.ctx, "The tool bridge stopped serving", slog.String("error", err.Error()))
		}
	}()

	// The line names what the secret is bound to, and never the secret.
	logger.Info(ctx, "Tool bridge started",
		slog.String("address", listener.Addr().String()),
		slog.String("dag_run_id", opts.DAGRunID),
		slog.String("step", opts.Step),
		slog.Int("tools", len(b.names)),
		slog.Int("max_tool_calls", maxCalls),
	)
	return b, nil
}

// newBridgeSecret returns 32 bytes of the system random generator, encoded for
// an HTTP header.
func newBridgeSecret() (string, error) {
	raw := make([]byte, secretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("the tool bridge cannot make its secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// addTool puts a tool of the catalog into the MCP server. The server panics for
// a schema that is not an object, so the panic becomes an error.
func (b *Bridge) addTool(tool llmpkg.Tool) (err error) {
	name := tool.Function.Name
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("the tool %q cannot join the tool bridge: %v", name, recovered)
		}
	}()
	b.server.AddTool(&mcpsdk.Tool{
		Name:        name,
		Description: tool.Function.Description,
		InputSchema: tool.Function.Parameters,
	}, b.handleCall)
	return nil
}

// Names lists the tool names of the catalog in order.
func (r *ToolRegistry) Names() []string { return append([]string(nil), r.names()...) }

// URL returns the address of the MCP endpoint. It holds no secret.
func (b *Bridge) URL() string { return b.url }

// Secret returns the secret of the attempt. The caller writes it to the MCP
// config of the CLI and to nothing else: it must not enter a log, a record or an
// error.
func (b *Bridge) Secret() string { return b.secret }

// ToolNames lists the tools of the bridge in the order of the catalog.
func (b *Bridge) ToolNames() []string { return append([]string(nil), b.names...) }

// MaxToolCalls returns the limit of calls for the attempt.
func (b *Bridge) MaxToolCalls() int { return b.maxCalls }

// Calls returns the number of calls that ran a tool.
func (b *Bridge) Calls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.executed
}

// SubRuns returns the child runs that the calls started, for the UI.
func (b *Bridge) SubRuns() []ir.SubDAGRun {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]ir.SubDAGRun(nil), b.subRuns...)
}

// Kill stops the child runs that a call started.
func (b *Bridge) Kill(sig os.Signal) error { return b.executor.Kill(sig) }

// Close stops the bridge: it ends the open calls and their child runs, closes
// the listener and the open connections, and invalidates the secret. It waits
// until the server has stopped, so no listener remains when it returns. It can
// run more than once.
func (b *Bridge) Close() error {
	b.closeOnce.Do(func() {
		b.closed.Store(true)
		b.cancel()
		_ = b.executor.Kill(syscall.SIGTERM)
		if err := b.http.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			b.closeErr = err
		}
		for session := range b.server.Sessions() {
			_ = session.Close()
		}
		<-b.served
	})
	return b.closeErr
}

// authenticate refuses a request that does not carry the secret of the attempt.
// It compares in constant time.
func (b *Bridge) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !b.authorized(r.Header.Get("Authorization")) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="dagu"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != BridgePath {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (b *Bridge) authorized(header string) bool {
	if b.closed.Load() {
		return false
	}
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		return false
	}
	sum := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(sum[:], b.secretSum[:]) == 1
}

// routeCalls sends every tools/call to handleCall, so that a name outside the
// catalog gets a result with isError true and not a protocol error.
func (b *Bridge) routeCalls(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
	return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
		if method == "tools/call" {
			if call, ok := req.(*mcpsdk.CallToolRequest); ok {
				return b.handleCall(ctx, call)
			}
		}
		return next(ctx, method, req)
	}
}

// handleCall runs one tool call. A refused call, an unknown tool and a failed
// child run give a result with isError true; the attempt does not fail.
func (b *Bridge) handleCall(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	name := req.Params.Name

	b.mu.Lock()
	b.attempted++
	over := b.attempted > b.maxCalls
	b.mu.Unlock()
	if over {
		return errorResult(CallLimitText(b.maxCalls)), nil
	}
	if _, ok := b.known[name]; !ok {
		return errorResult(fmt.Sprintf("tool %q not found", name)), nil
	}

	select {
	case b.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.ctx.Done():
		return errorResult("the tool bridge is closed"), nil
	}
	defer func() { <-b.sem }()

	b.mu.Lock()
	b.executed++
	number := b.executed
	b.mu.Unlock()

	// The call runs under the context of the step. A request that the client
	// cancels ends the call too.
	callCtx, cancel := context.WithCancel(b.ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()

	arguments := ""
	if len(req.Params.Arguments) > 0 {
		arguments = string(req.Params.Arguments)
	}
	results := b.executor.ExecuteToolCalls(callCtx, []llmpkg.ToolCall{{
		ID:       fmt.Sprintf("bridge-%d", number),
		Type:     "function",
		Function: llmpkg.ToolCallFunction{Name: name, Arguments: arguments},
	}})
	if len(results) != 1 {
		return errorResult("the tool call gave no result"), nil
	}
	call := results[0]
	if call.SubRun.DAGRunID != "" {
		b.mu.Lock()
		b.subRuns = append(b.subRuns, call.SubRun)
		b.mu.Unlock()
	}

	text := call.Result.Content
	if call.Result.Error != "" {
		text = call.Result.Error
	}
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: text}},
		IsError: call.Failed,
	}, nil
}

func errorResult(text string) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: text}},
		IsError: true,
	}
}
