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

// tokenConnections hands out one token for every connection.
type tokenConnections struct{ token string }

func (c tokenConnections) Connection(_ context.Context, name string) (*connection.Connection, error) {
	return &connection.Connection{Name: name}, nil
}

func (c tokenConnections) Token(context.Context, *connection.Connection, string, time.Duration) (string, time.Duration, error) {
	return c.token, time.Hour, nil
}

func (c tokenConnections) Rejected(context.Context, *connection.Connection, string, string, time.Duration) (string, time.Duration, error) {
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

// The token of a step's connection, taken before the step's output is set
// up, and a token the step learns while it runs are masked in its log.
func TestConnectionTokensMaskedInStepLog(t *testing.T) {
	t.Parallel()

	executorType := "test-connection-token-printer"
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
		runtime.WithConnections(tokenConnections{token: "issued-token"}))
	node := runtime.NewNode(ir.Step{
		Name:           "print",
		ExecutorConfig: ir.ExecutorConfig{Type: executorType},
	}, runtime.NodeState{})
	require.NoError(t, node.Prepare(ctx, t.TempDir(), "run-1"))
	require.NoError(t, runtime.NewStepExecutor().Execute(ctx, node))
	require.NoError(t, node.Teardown())

	log, err := os.ReadFile(node.StdoutFile())
	require.NoError(t, err)
	require.NotContains(t, string(log), "issued-token")
	require.NotContains(t, string(log), "refreshed-token")
}
