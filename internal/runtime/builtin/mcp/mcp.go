// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	cmnconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var closeTimeout = 5 * time.Second

var _ executor.Executor = (*caller)(nil)

type caller struct {
	mu     sync.Mutex
	stdout io.Writer
	stderr io.Writer
	cfg    config
	cancel context.CancelFunc
	killed bool
}

func newCaller(_ context.Context, step ir.Step) (executor.Executor, error) {
	cfg, err := decodeConfig(step, false)
	if err != nil {
		return nil, err
	}
	return &caller{stdout: os.Stdout, stderr: os.Stderr, cfg: cfg}, nil
}

func (c *caller) SetStdout(out io.Writer) { c.stdout = out }
func (c *caller) SetStderr(out io.Writer) { c.stderr = out }

func (c *caller) Kill(_ os.Signal) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.killed = true
	if c.cancel != nil {
		c.cancel()
	}
	return nil
}

// Run performs exactly one connect, initialize, tools/call and close
// sequence. It never retries; retry policy belongs to the step.
func (c *caller) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c.mu.Lock()
	c.cancel = cancel
	killed := c.killed
	c.mu.Unlock()
	if killed {
		cancel()
	}

	redact := newRedactor(c.cfg.Headers, c.cfg.URL)
	res, cerr := c.call(ctx, redact)
	if cerr == nil {
		if err := writeRedacted(c.stdout, newOutput(res, c.cfg.IncludeRaw), redact); err != nil {
			return fmt.Errorf("mcp.call: writing output: %w", err)
		}
		if res.IsError {
			return &callError{kind: kindTool, message: redact.apply(toolMessage(res))}
		}
		return nil
	}
	_ = writeJSON(c.stdout, errorOutput{Error: errorBody{Kind: cerr.kind, Message: cerr.message}})
	return cerr
}

func (c *caller) call(ctx context.Context, redact redactor) (*sdk.CallToolResult, *callError) {
	base := http.DefaultTransport.(*http.Transport).Clone()
	defer base.CloseIdleConnections()
	obs := &observingTransport{base: base, headers: c.cfg.Headers}

	transport := &sdk.StreamableClientTransport{
		Endpoint: c.cfg.URL,
		HTTPClient: &http.Client{
			Transport: obs,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "dagu", Version: cmnconfig.Version}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, classify(ctx, err, obs, redact)
	}
	defer closeSession(session)

	params := &sdk.CallToolParams{Name: c.cfg.Tool}
	if c.cfg.Arguments != nil {
		params.Arguments = c.cfg.Arguments
	}
	res, err := session.CallTool(ctx, params)
	if err != nil {
		return nil, classify(ctx, err, obs, redact)
	}
	return res, nil
}

func closeSession(s *sdk.ClientSession) {
	done := make(chan struct{})
	go func() {
		_ = s.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(closeTimeout):
	}
}

func classify(ctx context.Context, err error, obs *observingTransport, redact redactor) *callError {
	msg := redact.apply(err.Error())
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return &callError{kindTimeout, "step timed out"}
	case errors.Is(ctx.Err(), context.Canceled):
		return &callError{kindCanceled, "call canceled"}
	}
	if status := obs.authStatus(); status != 0 {
		return &callError{kindAuth, "server rejected credentials (HTTP " + strconv.Itoa(status) + ")"}
	}
	if obs.redirectStatus() != 0 {
		return &callError{kindProtocol, "server responded with HTTP " + strconv.Itoa(obs.redirectStatus()) + "; redirects are not followed, configure the final URL"}
	}
	// The SDK wraps network failures in a JSON-RPC error, so check them first.
	if obs.networkError() != nil || errors.Is(err, sdk.ErrConnectionClosed) {
		return &callError{kindTransport, msg}
	}
	return &callError{kindProtocol, msg}
}

func toolMessage(res *sdk.CallToolResult) string {
	for _, content := range res.Content {
		if text, ok := content.(*sdk.TextContent); ok && text.Text != "" {
			return text.Text
		}
	}
	return "tool reported an error"
}

func init() {
	executor.RegisterExecutor(executorType, newCaller, validateStep, registry.ExecutorCapabilities{})
}
