// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordedLogWriters is the log writer factory of a worker that streams its
// logs to a coordinator. It keeps what each writer receives.
type recordedLogWriters struct {
	mu      sync.Mutex
	writers []*recordedLogWriter
}

func (f *recordedLogWriters) NewStepWriter(_ context.Context, stepName string, streamType int) io.WriteCloser {
	f.mu.Lock()
	defer f.mu.Unlock()
	w := &recordedLogWriter{step: stepName, streamType: streamType}
	f.writers = append(f.writers, w)
	return w
}

func (f *recordedLogWriters) all() []*recordedLogWriter {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*recordedLogWriter(nil), f.writers...)
}

type recordedLogWriter struct {
	step       string
	streamType int

	mu      sync.Mutex
	data    bytes.Buffer
	flushes int
	closed  bool
}

func (w *recordedLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, io.ErrClosedPipe
	}
	return w.data.Write(p)
}

func (w *recordedLogWriter) FlushIfDue() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushes++
	return nil
}

func (w *recordedLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	return nil
}

func (w *recordedLogWriter) snapshot() (data string, flushes int, closed bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.data.String(), w.flushes, w.closed
}

func remoteStreamRun(t *testing.T, run ir.AgentRun, streams ...string) (*fakeClaudeRun, *recordedLogWriters) {
	t.Helper()
	factory := &recordedLogWriters{}
	result := newFakeClaudeRunWith(t, []runtime.ContextOption{runtime.WithLogWriterFactory(factory)}, run, nil, streams...)
	return result, factory
}

// On a worker that streams its logs to a coordinator, the raw stream of the CLI
// goes to the log writer of the type agent stream, and no file stays on the
// worker (Spec 091, "Outputs").
func TestAgentRunStreamGoesToTheLogWriter(t *testing.T) {
	t.Parallel()

	run, factory := remoteStreamRun(t, ir.AgentRun{Prompt: "Say done."}, streamSuccess)
	require.NoError(t, run.run())
	assert.Equal(t, "done\n", run.stdout.String())

	writers := factory.all()
	require.Len(t, writers, 1)
	data, _, closed := writers[0].snapshot()
	assert.Equal(t, runctx.StreamTypeAgentStream, writers[0].streamType)
	assert.Equal(t, "review", writers[0].step)
	assert.Equal(t, streamSuccess, data)
	assert.True(t, closed, "the writer is closed when the step ends")

	_, err := os.Stat(filepath.Join(run.dir, "step.out"+ir.AgentStreamSuffix))
	assert.True(t, os.IsNotExist(err), "no stream file stays on the worker")
}

// A run that has no log writer factory writes the file next to the stdout log.
func TestAgentRunStreamFileWithoutLogWriter(t *testing.T) {
	t.Parallel()

	run := newFakeClaudeRun(t, ir.AgentRun{Prompt: "Say done."}, nil, streamSuccess)
	require.NoError(t, run.run())
	data, err := os.ReadFile(filepath.Join(run.dir, "step.out"+ir.AgentStreamSuffix))
	require.NoError(t, err)
	assert.Equal(t, streamSuccess, string(data))
}

// The writer of the coordinator gets the stream after the masking of the
// secrets of the attempt, as the file does, and it gets the end of a last line
// that has no line end.
func TestAgentRunStreamSinkMasksSecrets(t *testing.T) {
	t.Parallel()

	factory := &recordedLogWriters{}
	ctx := newHarnessTestContextWith(t, &ir.DAG{Name: "agent-run"}, ir.Step{Name: "review"},
		[]runtime.ContextOption{runtime.WithLogWriterFactory(factory)})
	a := &agentHarness{harnessExecutor: &harnessExecutor{step: ir.Step{Name: "review"}}}
	defer a.closeStream()

	sink, release := a.openStreamSink(ctx)
	require.NotNil(t, sink)
	masked := masking.NewMaskingWriter(sink, secretMasker([]string{"bridge-secret-value"}))
	_, err := masked.Write([]byte("{\"token\":\"bridge-secret-value\"}\n{\"tail\":\"bridge-secret-value\"}"))
	require.NoError(t, err)
	require.NoError(t, masked.Flush())
	release()

	writers := factory.all()
	require.Len(t, writers, 1)
	data, _, _ := writers[0].snapshot()
	assert.NotContains(t, data, "bridge-secret-value")
	assert.Contains(t, data, `{"token":"`)
	assert.Contains(t, data, `{"tail":"`)
}

// The run flushes the writer while the CLI runs, so a reader of the coordinator
// sees the stream as it grows, as for the stdout log.
func TestAgentRunStreamFlushesWhileTheCLIRuns(t *testing.T) {
	t.Parallel()

	run, factory := remoteStreamRun(t, ir.AgentRun{Prompt: "x"}, streamSuccess)
	run.agent.streamFlushInterval = 5 * time.Millisecond
	require.NoError(t, os.WriteFile(filepath.Join(run.dir, "wait.1"), nil, 0o600))

	done := make(chan error, 1)
	go func() { done <- run.run() }()

	require.Eventually(t, func() bool {
		writers := factory.all()
		if len(writers) != 1 {
			return false
		}
		_, flushes, _ := writers[0].snapshot()
		return flushes > 0
	}, 10*time.Second, 5*time.Millisecond, "the writer is flushed before the CLI ends")
	require.NoError(t, os.WriteFile(filepath.Join(run.dir, "go.1"), nil, 0o600))
	require.NoError(t, <-done)
}
