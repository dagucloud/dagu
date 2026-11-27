// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"cmp"
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
	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
	"github.com/dagucloud/dagu/v2/internal/connection/mcpclient"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var closeTimeout = 5 * time.Second

// newBaseTransport returns the transport that one call's requests go
// through.
var newBaseTransport = func() *http.Transport {
	return http.DefaultTransport.(*http.Transport).Clone()
}

var (
	_ executor.Executor          = (*caller)(nil)
	_ executor.SecretMaskerAware = (*caller)(nil)
)

type caller struct {
	mu     sync.Mutex
	stdout io.Writer
	stderr io.Writer
	cfg    config
	// files stores binary content when save_binary is set; nil otherwise.
	files  *agentstep.ArtifactStore
	cancel context.CancelFunc
	killed bool
	// maskSecrets masks a token in the step's logs from the moment the
	// call takes it; nil outside a step.
	maskSecrets func(...string)
}

func newCaller(ctx context.Context, step ir.Step) (executor.Executor, error) {
	cfg, err := decodeConfig(step, false)
	if err != nil {
		return nil, err
	}
	c := &caller{stdout: os.Stdout, stderr: os.Stderr, cfg: cfg}
	if cfg.SaveBinary {
		env := runtime.GetEnv(ctx)
		dir := ""
		if env.Scope != nil {
			dir, _ = env.Scope.Get(runenv.EnvKeyDAGRunArtifactsDir)
		}
		if dir == "" {
			return nil, errors.New("mcp.call: with.save_binary requires artifacts, which are disabled for this DAG")
		}
		stepKey := cmp.Or(step.ID, step.Name)
		c.files = agentstep.NewArtifactStore(dir, binarySubdir, stepKey)
	}
	return c, nil
}

func (c *caller) SetStdout(out io.Writer) { c.stdout = out }
func (c *caller) SetStderr(out io.Writer) { c.stderr = out }

func (c *caller) SetSecretMasker(mask func(...string)) { c.maskSecrets = mask }

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
// sequence. It never retries; retry policy belongs to the step. Through a
// connection, a request whose token the server refused is sent once more
// with the token the source returns.
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

	res, redact, cerr := c.call(ctx)
	if cerr == nil {
		out, err := newOutput(res, c.cfg.IncludeRaw, c.files)
		if err != nil {
			return err
		}
		if err := writeRedacted(c.stdout, out, redact); err != nil {
			return fmt.Errorf("mcp.call: writing output: %w", err)
		}
		if res.IsError {
			return &callError{kind: kindTool, message: redact.apply(toolMessage(res))}
		}
		return nil
	}
	_ = writeJSON(c.stdout, errorOutput{Error: cerr.body()})
	return cerr
}

// call returns the tool's result or the call's error, and the redactor of
// the call's secrets, which include each token the call took.
func (c *caller) call(ctx context.Context) (*sdk.CallToolResult, redactor, *callError) {
	redact := newRedactor(c.cfg.Headers, c.cfg.URL)
	base := newBaseTransport()
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
		DisableStandaloneSSE: true,
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "dagu", Version: cmnconfig.Version}, nil)
	var cc *connectionCall
	if c.cfg.Connection != "" {
		var cerr *callError
		cc, cerr = c.connect(ctx, base)
		redact = redact.with(cc.seen()...)
		if cerr != nil {
			cerr.message = redact.apply(cerr.message)
			return nil, redact, cerr
		}
		obs.base = cc.bearer
		transport.Endpoint = cc.endpoint
		transport.OAuthHandler = cc.bearer
		client = mcpclient.NewClient(cc.elicited)
	}

	res, err := c.callTool(ctx, client, transport)
	if cc != nil {
		redact = redact.with(cc.seen()...)
		if cerr := cc.confirmationError(redact); cerr != nil {
			return nil, redact, cerr
		}
	}
	if err != nil {
		return nil, redact, classify(ctx, err, obs, cc, redact)
	}
	return res, redact, nil
}

func (c *caller) callTool(ctx context.Context, client *sdk.Client, transport sdk.Transport) (*sdk.CallToolResult, error) {
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, err
	}
	defer closeSession(session)

	params := &sdk.CallToolParams{Name: c.cfg.Tool}
	if c.cfg.Arguments != nil {
		params.Arguments = c.cfg.Arguments
	}
	return session.CallTool(ctx, params)
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

// classify returns the error of a failed call. cc is nil for a call without
// a connection.
func classify(ctx context.Context, err error, obs *observingTransport, cc *connectionCall, redact redactor) *callError {
	msg := redact.apply(err.Error())
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return &callError{kind: kindTimeout, message: "call timed out"}
	case errors.Is(ctx.Err(), context.Canceled):
		return &callError{kind: kindCanceled, message: "call canceled"}
	}
	if cc != nil {
		if cerr := cc.refusal(err, redact); cerr != nil {
			return cerr
		}
		if errors.Is(err, broker.ErrReconnectRequired) {
			cerr := authError(cc.name, err)
			cerr.message = redact.apply(cerr.message)
			return cerr
		}
	} else if status := obs.authStatus(); status != 0 {
		return &callError{kind: kindAuth, message: "server rejected credentials (HTTP " + strconv.Itoa(status) + ")"}
	}
	if obs.redirectStatus() != 0 {
		return &callError{kind: kindProtocol, message: "server responded with HTTP " + strconv.Itoa(obs.redirectStatus()) + "; redirects are not followed, configure the final URL"}
	}
	if limit := obs.rateLimit(); limit != nil {
		return limit.callError(redact)
	}
	if status := obs.unavailableStatus(); status != 0 {
		return &callError{kind: kindTransport, message: "server responded with HTTP " + strconv.Itoa(status) + " " + http.StatusText(status)}
	}
	// The SDK wraps network failures in a JSON-RPC error, so check them first.
	if obs.networkError() != nil || errors.Is(err, sdk.ErrConnectionClosed) {
		return &callError{kind: kindTransport, message: msg}
	}
	return &callError{kind: kindProtocol, message: msg}
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
	executor.RegisterExecutor(executorType, newCaller, validateStep, registry.ExecutorCapabilities{
		Connection: stepConnection,
	})
}
