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
	"github.com/dagucloud/dagu/internal/persis/file/dagrun"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureQueueDispatchRetryTarget_MissingRunReturnsNotQueued(t *testing.T) {
	t.Parallel()

	store := dagrun.New(filepath.Join(t.TempDir(), "dag-runs"))
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

func TestRetryCommandDoesNotExposeProfileFlag(t *testing.T) {
	t.Parallel()

	cmd := Retry()
	assert.Nil(t, cmd.Flags().Lookup(profileFlag.name))
}

func TestEnsureQueueDispatchRetryTarget_MissingStatusReturnsNotQueued(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := dagrun.New(filepath.Join(t.TempDir(), "dag-runs"))
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

	dag := &core.DAG{Name: "retry-test"}
	store := &retryReleaseProcStore{heartbeats: []*exec.ProcHeartbeat{
		retryReleaseHeartbeat(dag.Name, "run-1", "attempt-1", true),
		retryReleaseHeartbeat(dag.Name, "run-1", "attempt-1", true),
		nil,
	}}
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
}

func TestWaitForRetrySourceRelease_SkipsActiveStatus(t *testing.T) {
	t.Parallel()

	store := &retryReleaseProcStore{
		heartbeats: []*exec.ProcHeartbeat{
			retryReleaseHeartbeat("retry-test", "run-1", "attempt-1", true),
		},
	}
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

	dag := &core.DAG{Name: "retry-test"}
	store := &retryReleaseProcStore{
		alwaysHeartbeat: retryReleaseHeartbeat(dag.Name, "run-1", "attempt-1", true),
	}
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

	dag := &core.DAG{Name: "retry-test"}
	store := &retryReleaseProcStore{heartbeats: []*exec.ProcHeartbeat{
		retryReleaseHeartbeat(dag.Name, "run-1", "attempt-2", true),
	}}
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

	heartbeats      []*exec.ProcHeartbeat
	alwaysHeartbeat *exec.ProcHeartbeat
	calls           int
	groupName       string
	dagRun          exec.DAGRunRef
}

func (s *retryReleaseProcStore) LatestHeartbeat(_ context.Context, groupName string, dagRun exec.DAGRunRef) (*exec.ProcHeartbeat, error) {
	s.calls++
	s.groupName = groupName
	s.dagRun = dagRun
	if s.alwaysHeartbeat != nil {
		heartbeat := *s.alwaysHeartbeat
		return &heartbeat, nil
	}
	if len(s.heartbeats) == 0 {
		return nil, nil
	}
	heartbeat := s.heartbeats[0]
	s.heartbeats = s.heartbeats[1:]
	if heartbeat == nil {
		return nil, nil
	}
	copy := *heartbeat
	return &copy, nil
}

func retryReleaseHeartbeat(dagName, runID, attemptID string, fresh bool) *exec.ProcHeartbeat {
	return &exec.ProcHeartbeat{
		DAGRun:    exec.NewDAGRunRef(dagName, runID),
		AttemptID: attemptID,
		Fresh:     fresh,
	}
}
