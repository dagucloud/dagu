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

func TestWaitForRetrySourceRelease_WaitsForTerminalRunProcToStop(t *testing.T) {
	t.Parallel()

	store := &retryReleaseProcStore{attemptAlive: []bool{true, true, false}}
	dag := &core.DAG{Name: "retry-test"}
	status := &exec.DAGRunStatus{
		Name:      dag.Name,
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
		Status:    core.Succeeded,
	}

	err := waitForRetrySourceReleaseFor(
		&Context{Context: context.Background(), ProcStore: store},
		dag,
		status,
		time.Second,
		time.Millisecond,
	)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, store.calls, 3)
	assert.Equal(t, dag.ProcGroup(), store.groupName)
	assert.Equal(t, exec.NewDAGRunRef(dag.Name, "run-1"), store.dagRun)
	assert.Equal(t, "attempt-1", store.attemptID)
}

func TestWaitForRetrySourceRelease_SkipsActiveStatus(t *testing.T) {
	t.Parallel()

	store := &retryReleaseProcStore{alive: []bool{true}}
	dag := &core.DAG{Name: "retry-test"}
	status := &exec.DAGRunStatus{
		Name:     dag.Name,
		DAGRunID: "run-1",
		Status:   core.Running,
	}

	err := waitForRetrySourceReleaseFor(
		&Context{Context: context.Background(), ProcStore: store},
		dag,
		status,
		time.Second,
		time.Millisecond,
	)
	require.NoError(t, err)
	assert.Zero(t, store.calls)
}

func TestWaitForRetrySourceRelease_TimesOutWhileProcAlive(t *testing.T) {
	t.Parallel()

	store := &retryReleaseProcStore{attemptAlwaysAlive: true}
	dag := &core.DAG{Name: "retry-test"}
	status := &exec.DAGRunStatus{
		Name:      dag.Name,
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
		Status:    core.Failed,
	}

	err := waitForRetrySourceReleaseFor(
		&Context{Context: context.Background(), ProcStore: store},
		dag,
		status,
		5*time.Millisecond,
		time.Millisecond,
	)
	require.Error(t, err)
	assert.ErrorContains(t, err, "still finalizing")
	assert.NotZero(t, store.calls)
}

func TestWaitForRetrySourceReleaseRejectsDifferentActiveAttempt(t *testing.T) {
	t.Parallel()

	store := &retryReleaseProcStore{
		attemptAlive: []bool{false},
		alive:        []bool{true},
	}
	dag := &core.DAG{Name: "retry-test"}
	status := &exec.DAGRunStatus{
		Name:      dag.Name,
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
		Status:    core.Failed,
	}

	err := waitForRetrySourceReleaseFor(
		&Context{Context: context.Background(), ProcStore: store},
		dag,
		status,
		time.Second,
		time.Millisecond,
	)
	require.Error(t, err)
	assert.ErrorContains(t, err, "another active attempt")
}

type retryReleaseProcStore struct {
	exec.ProcStore

	alive              []bool
	attemptAlive       []bool
	attemptAlwaysAlive bool
	alwaysAlive        bool
	calls              int
	groupName          string
	dagRun             exec.DAGRunRef
	attemptID          string
}

func (s *retryReleaseProcStore) IsRunAlive(_ context.Context, groupName string, dagRun exec.DAGRunRef) (bool, error) {
	s.calls++
	s.groupName = groupName
	s.dagRun = dagRun
	if s.alwaysAlive {
		return true, nil
	}
	if len(s.alive) == 0 {
		return false, nil
	}
	alive := s.alive[0]
	s.alive = s.alive[1:]
	return alive, nil
}

func (s *retryReleaseProcStore) IsAttemptAlive(_ context.Context, groupName string, dagRun exec.DAGRunRef, attemptID string) (bool, error) {
	s.calls++
	s.groupName = groupName
	s.dagRun = dagRun
	s.attemptID = attemptID
	if s.attemptAlwaysAlive {
		return true, nil
	}
	if len(s.attemptAlive) == 0 {
		return false, nil
	}
	alive := s.attemptAlive[0]
	s.attemptAlive = s.attemptAlive[1:]
	return alive, nil
}
