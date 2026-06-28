// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/stretchr/testify/require"
)

func TestRunnerClosesStepWritersWhenFailedNodeReportsProgress(t *testing.T) {
	t.Parallel()

	logWriters := newTrackedLogWriterFactory()
	helper := setupRunner(t)
	plan := helper.newPlan(t, failStep("fail"))
	dag := &core.DAG{Name: "test_dag", WorkingDir: plan.workDir}
	logFilePath := filepath.Join(helper.cfg.LogDir, fmt.Sprintf("%s_%s.log", dag.Name, helper.cfg.DAGRunID))
	ctx := runtime.NewContext(
		helper.Context,
		dag,
		helper.cfg.DAGRunID,
		logFilePath,
		runtime.WithLogWriterFactory(logWriters),
	)

	progressCh := make(chan *runtime.Node)
	progressDone := make(chan struct{})
	go func() {
		for range progressCh {
		}
		close(progressDone)
	}()

	err := helper.runner.Run(ctx, plan.Plan, progressCh)
	close(progressCh)
	<-progressDone

	require.Error(t, err)
	logWriters.requireClosed(t, exec.StreamTypeStdout)
	logWriters.requireClosed(t, exec.StreamTypeStderr)
}

type trackedLogWriterFactory struct {
	mu      sync.Mutex
	writers map[int]*trackedWriteCloser
}

func newTrackedLogWriterFactory() *trackedLogWriterFactory {
	return &trackedLogWriterFactory{writers: make(map[int]*trackedWriteCloser)}
}

func (f *trackedLogWriterFactory) NewStepWriter(_ context.Context, _ string, streamType int) io.WriteCloser {
	writer := &trackedWriteCloser{}

	f.mu.Lock()
	f.writers[streamType] = writer
	f.mu.Unlock()

	return writer
}

func (f *trackedLogWriterFactory) requireClosed(t *testing.T, streamType int) {
	t.Helper()

	f.mu.Lock()
	writer := f.writers[streamType]
	f.mu.Unlock()

	require.NotNil(t, writer)
	require.True(t, writer.closed())
}

type trackedWriteCloser struct {
	mu       sync.Mutex
	isClosed bool
}

func (w *trackedWriteCloser) Write(p []byte) (int, error) {
	return len(p), nil
}

func (w *trackedWriteCloser) Close() error {
	w.mu.Lock()
	w.isClosed = true
	w.mu.Unlock()
	return nil
}

func (w *trackedWriteCloser) closed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.isClosed
}
