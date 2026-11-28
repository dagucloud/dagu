// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	runtimeexec "github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

// tokenConnections hands out one token for every connection. When release
// is set, a token request closes asked and waits for release.
type tokenConnections struct {
	token   string
	account string
	// err fails every token request.
	err     error
	asked   chan struct{}
	release chan struct{}
}

func (c *tokenConnections) Connection(_ context.Context, name string) (*connection.Connection, error) {
	return &connection.Connection{Name: name}, nil
}

func (c *tokenConnections) Token(context.Context, *connection.Connection, string, time.Duration) (string, time.Duration, error) {
	if c.release != nil {
		close(c.asked)
		<-c.release
	}
	return c.token, time.Hour, c.err
}

func (c *tokenConnections) Rejected(context.Context, *connection.Connection, string, string, time.Duration) (string, time.Duration, error) {
	return c.token, time.Hour, nil
}

func (c *tokenConnections) Account(context.Context, *connection.Connection) (string, error) {
	return c.account, nil
}

// tokenPrinter prints the token of its connection, then a token it learns
// while it runs.
type tokenPrinter struct {
	stdout io.Writer
	mask   func(...string)
}

func (e *tokenPrinter) SetStdout(out io.Writer)              { e.stdout = out }
func (e *tokenPrinter) SetStderr(io.Writer)                  {}
func (e *tokenPrinter) Kill(os.Signal) error                 { return nil }
func (e *tokenPrinter) SetSecretMasker(mask func(...string)) { e.mask = mask }
func (e *tokenPrinter) Run(context.Context) error {
	e.mask("refreshed-token")
	_, err := fmt.Fprintln(e.stdout, "issued-token refreshed-token")
	return err
}

// connectionNode returns a node whose step uses connection work and runs
// exec, in a run whose connections are conns.
func connectionNode(t *testing.T, conns runtime.Connections, exec runtimeexec.Executor) (context.Context, *runtime.Node) {
	t.Helper()
	executorType := "test-connection-token-printer-" + t.Name()
	runtimeexec.RegisterExecutor(executorType, func(context.Context, ir.Step) (runtimeexec.Executor, error) {
		return exec, nil
	}, nil, registry.ExecutorCapabilities{
		Connection: func(ir.Step) string { return "work" },
	})
	t.Cleanup(func() {
		runtimeexec.UnregisterExecutor(executorType)
		registry.UnregisterExecutorCapabilities(executorType)
	})
	ctx := runtime.NewContext(context.Background(), &ir.DAG{Name: "connection-dag"}, "run-1", "dag.log",
		runtime.WithConnections(conns))
	node := runtime.NewNode(ir.Step{
		Name:           "print",
		ExecutorConfig: ir.ExecutorConfig{Type: executorType},
	}, runtime.NodeState{})
	return ctx, node
}

// The token of a step's connection, taken before the step's output is set
// up, and a token the step learns while it runs are masked in its log.
func TestConnectionTokensMaskedInStepLog(t *testing.T) {
	t.Parallel()

	ctx, node := connectionNode(t, &tokenConnections{token: "issued-token"}, &tokenPrinter{})
	require.NoError(t, node.Prepare(ctx, t.TempDir(), "run-1"))
	require.NoError(t, runtime.NewStepExecutor().Execute(ctx, node))
	require.NoError(t, node.Teardown())

	log, err := os.ReadFile(node.StdoutFile())
	require.NoError(t, err)
	require.NotContains(t, string(log), "issued-token")
	require.NotContains(t, string(log), "refreshed-token")
}

// A stop of a step that waits for its connection's token, which can take a
// refresh or a coordinator request, does not wait for the token.
func TestStopWhileConnectionTokenPending(t *testing.T) {
	t.Parallel()

	conns := &tokenConnections{token: "issued-token", asked: make(chan struct{}), release: make(chan struct{})}
	ctx, node := connectionNode(t, conns, &tokenPrinter{})
	prepared := make(chan error, 1)
	go func() { prepared <- node.Prepare(ctx, t.TempDir(), "run-1") }()
	<-conns.asked

	stopped := make(chan struct{})
	go func() {
		node.Cancel()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		close(conns.release)
		t.Fatal("the stop waited for the token")
	}
	close(conns.release)
	require.NoError(t, <-prepared)
	require.NoError(t, node.Teardown())
}

// tokenTaker asks the run's connections for the token of connection work,
// and fails as the request does.
type tokenTaker struct{}

func (tokenTaker) SetStdout(io.Writer)  {}
func (tokenTaker) SetStderr(io.Writer)  {}
func (tokenTaker) Kill(os.Signal) error { return nil }
func (tokenTaker) Run(ctx context.Context) error {
	conns := runtime.GetDAGContext(ctx).Connections
	conn, err := conns.Connection(ctx, "work")
	if err != nil {
		return err
	}
	_, _, err = conns.Token(ctx, conn, "", time.Minute)
	return err
}

// A step records its connection and account, and why the connection gave
// it no token, which decides whether a failed run was caused by a broken
// connection.
func TestConnectionRecordedOnNode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want ir.ConnectionFailure
	}{
		{name: "Used"},
		{name: "Pending", err: broker.ErrReconnectRequired, want: ir.ConnectionReconnectRequired},
		{
			name: "NeedsReconnect",
			err:  fmt.Errorf("%w: %w", broker.ErrReconnectRequired, broker.ErrNeedsAttention),
			want: ir.ConnectionNeedsAttention,
		},
		{name: "RefreshFailed", err: broker.ErrRefreshFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			conns := &tokenConnections{token: "issued-token", account: "ops@example.com", err: tt.err}
			ctx, node := connectionNode(t, conns, tokenTaker{})
			require.NoError(t, node.Prepare(ctx, t.TempDir(), "run-1"))
			err := runtime.NewStepExecutor().Execute(ctx, node)
			require.ErrorIs(t, err, tt.err)
			require.NoError(t, node.Teardown())

			require.Equal(t, &ir.NodeConnection{Name: "work", Account: "ops@example.com", Failure: tt.want}, node.State().Connection)
		})
	}
}
