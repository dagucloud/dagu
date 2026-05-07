// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/filedagrun"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureQueueDispatchRetryTarget_MissingRunReturnsNotQueued(t *testing.T) {
	t.Parallel()

	store := filedagrun.New(filepath.Join(t.TempDir(), "dag-runs"))
	err := ensureQueueDispatchRetryTarget(
		context.Background(),
		store,
		exec.NewDAGRunRef("retry-test", "missing-run"),
		exec.DAGRunRef{},
	)
	require.Error(t, err)

	var notQueuedErr *exec.DAGRunNotQueuedError
	require.ErrorAs(t, err, &notQueuedErr)
	assert.False(t, notQueuedErr.HasStatus)
}

func TestEnsureQueueDispatchRetryTarget_MissingStatusReturnsNotQueued(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := filedagrun.New(filepath.Join(t.TempDir(), "dag-runs"))
	dag := &core.DAG{
		Name: "retry-test",
		Steps: []core.Step{
			{Name: "step", Command: "echo hi"},
		},
	}

	_, err := store.CreateAttempt(ctx, dag, time.Now(), "run-1", exec.NewDAGRunAttemptOptions{})
	require.NoError(t, err)

	err = ensureQueueDispatchRetryTarget(
		ctx,
		store,
		exec.NewDAGRunRef(dag.Name, "run-1"),
		exec.DAGRunRef{},
	)
	require.Error(t, err)

	var notQueuedErr *exec.DAGRunNotQueuedError
	require.ErrorAs(t, err, &notQueuedErr)
	assert.False(t, notQueuedErr.HasStatus)
}

func TestRestoreRetryExecutionContext_BackfillsStoredWorkingDirSnapshot(t *testing.T) {
	t.Parallel()

	dagDir := t.TempDir()
	workDir := t.TempDir()
	dag := &core.DAG{
		Name:       "retry-test",
		Location:   filepath.Join(dagDir, "retry-test.yaml"),
		WorkingDir: workDir,
	}
	status := &exec.DAGRunStatus{}

	restoreRetryExecutionContext(dag, status, nil)

	assert.Equal(t, workDir, status.WorkingDir)
	assert.Equal(t, workDir, dag.WorkingDir)
	assert.True(t, dag.WorkingDirExplicit)
}

func TestRestoreRetryExecutionContext_BackfillsAttemptWorkDirSnapshot(t *testing.T) {
	t.Parallel()

	dagDir := t.TempDir()
	attemptWorkDir := t.TempDir()
	dag := &core.DAG{
		Name:       "retry-test",
		Location:   filepath.Join(dagDir, "retry-test.yaml"),
		WorkingDir: dagDir,
	}
	status := &exec.DAGRunStatus{}
	attempt := &exec.MockDAGRunAttempt{}
	attempt.On("WorkDir").Return(attemptWorkDir).Once()

	restoreRetryExecutionContext(dag, status, attempt)

	assert.Equal(t, attemptWorkDir, status.WorkingDir)
	assert.Equal(t, attemptWorkDir, dag.WorkingDir)
	assert.True(t, dag.WorkingDirExplicit)
	attempt.AssertExpectations(t)
}
