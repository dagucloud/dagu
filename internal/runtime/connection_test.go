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
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	runtimeexec "github.com/dagucloud/dagu/v2/internal/runtime/executor"
)

// tokenConnections hands out one token for every connection. When release
// is set, a token request closes asked and waits for release.
type tokenConnections struct {
	token   string
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
	return c.token, time.Hour, nil
}

func (c *tokenConnections) Rejected(context.Context, *connection.Connection, string, string, time.Duration) (string, time.Duration, error) {
	return c.token, time.Hour, nil
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

// connectionNode returns a node whose step uses connection work, in a run
// whose connections are conns.
func connectionNode(t *testing.T, conns runtime.Connections) (context.Context, *runtime.Node) {
	t.Helper()
	executorType := "test-connection-token-printer-" + t.Name()
	runtimeexec.RegisterExecutor(executorType, func(context.Context, ir.Step) (runtimeexec.Executor, error) {
		return &tokenPrinter{}, nil
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

	ctx, node := connectionNode(t, &tokenConnections{token: "issued-token"})
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
	ctx, node := connectionNode(t, conns)
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
