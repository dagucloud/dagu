// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package embed_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/dagucloud/dagu"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

func embeddedTimeout(timeout time.Duration) time.Duration {
	if runtime.GOOS == "windows" {
		return timeout * 3
	}
	return timeout
}

func TestEmbeddedLocalRunYAML(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), embeddedTimeout(20*time.Second))
	defer cancel()

	engine, err := dagu.New(ctx, dagu.Options{HomeDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, engine.Close(context.Background()))
	})

	run, err := engine.RunYAML(ctx, []byte(`
name: embedded-intg-local
type: graph
steps:
  - name: first
    run: echo first
  - name: second
    run: echo second
    depends: [first]
`))
	require.NoError(t, err)

	status, err := run.Wait(ctx)
	require.NoError(t, err)
	require.Equal(t, "embedded-intg-local", status.Name)
	require.Equal(t, "succeeded", status.Status)
	require.NotEmpty(t, status.RunID)
	require.NotEmpty(t, status.AttemptID)
	require.NotEmpty(t, status.LogFile)
	require.FileExists(t, status.LogFile)

	saved, err := engine.Status(ctx, run.Ref())
	require.NoError(t, err)
	require.Equal(t, status.RunID, saved.RunID)
	require.Equal(t, "succeeded", saved.Status)
}

func TestEmbeddedCustomExecutorRunYAML(t *testing.T) {
	const executorType = "embedded_intg_echo"

	dagu.RegisterExecutor(
		executorType,
		func(_ context.Context, step dagu.Step) (dagu.Executor, error) {
			return &echoExecutor{step: step}, nil
		},
		dagu.WithExecutorCapabilities(dagu.ExecutorCapabilities{Command: true}),
	)
	t.Cleanup(func() {
		dagu.UnregisterExecutor(executorType)
	})

	ctx, cancel := context.WithTimeout(context.Background(), embeddedTimeout(20*time.Second))
	defer cancel()

	engine, err := dagu.New(ctx, dagu.Options{HomeDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, engine.Close(context.Background()))
	})

	run, err := engine.RunYAML(ctx, []byte(`
name: embedded-intg-custom-executor
type: graph
steps:
  - name: go-step
    action: embedded_intg_echo
    with:
      message: called from YAML
`))
	require.NoError(t, err)

	status, err := run.Wait(ctx)
	require.NoError(t, err)
	require.Equal(t, "embedded-intg-custom-executor", status.Name)
	require.Equal(t, "succeeded", status.Status)
}

func TestEmbeddedDistributedSharedNothingRunYAML(t *testing.T) {
	coord := test.SetupCoordinator(t, test.WithStatusPersistence())

	ctx, cancel := context.WithTimeout(context.Background(), embeddedTimeout(45*time.Second))
	defer cancel()

	engine, err := dagu.New(ctx, dagu.Options{
		HomeDir:     t.TempDir(),
		DefaultMode: dagu.ExecutionModeDistributed,
		Distributed: &dagu.DistributedOptions{
			Coordinators:    []string{coord.Address()},
			TLS:             dagu.TLSOptions{Insecure: true},
			WorkerSelector:  map[string]string{"pool": "embedded-intg"},
			PollInterval:    100 * time.Millisecond,
			MaxStatusErrors: 20,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, engine.Close(context.Background()))
	})

	worker, err := engine.NewWorker(dagu.WorkerOptions{
		ID:            "embedded-intg-worker",
		MaxActiveRuns: 2,
		Labels:        map[string]string{"pool": "embedded-intg"},
		HealthPort:    0,
	})
	require.NoError(t, err)

	workerCtx, stopWorker := context.WithCancel(ctx)
	workerErrCh := make(chan error, 1)
	go func() {
		workerErrCh <- worker.Start(workerCtx)
	}()
	t.Cleanup(func() {
		stopWorker()
		_ = worker.Stop(context.Background())
	})

	require.NoError(t, worker.WaitReady(ctx))

	run, err := engine.RunYAML(ctx, []byte(`
name: embedded-intg-distributed
type: graph
steps:
  - name: worker-step
    run: echo distributed
`))
	require.NoError(t, err)

	status, err := run.Wait(ctx)
	require.NoError(t, err)
	require.Equal(t, "embedded-intg-distributed", status.Name)
	require.Equal(t, "succeeded", status.Status)
	require.Equal(t, "embedded-intg-worker", status.WorkerID)

	stopWorker()
	require.NoError(t, worker.Stop(context.Background()))
	select {
	case err := <-workerErrCh:
		require.NoError(t, err)
	case <-time.After(embeddedTimeout(5 * time.Second)):
		t.Fatal("embedded worker did not stop")
	}
}

type echoExecutor struct {
	step   dagu.Step
	stdout io.Writer
}

func (e *echoExecutor) SetStdout(out io.Writer) {
	e.stdout = out
}

func (e *echoExecutor) SetStderr(io.Writer) {}

func (e *echoExecutor) Kill(os.Signal) error {
	return nil
}

func (e *echoExecutor) Run(context.Context) error {
	out := e.stdout
	if out == nil {
		out = io.Discard
	}
	command := e.step.Command
	if command == "" && len(e.step.Commands) > 0 {
		command = e.step.Commands[0].CmdWithArgs
	}
	if command == "" {
		if message, ok := e.step.ExecutorConfig.Config["message"].(string); ok {
			command = message
		}
	}
	_, err := fmt.Fprintf(out, "embedded step ran %s: %s\n", e.step.Name, command)
	return err
}
