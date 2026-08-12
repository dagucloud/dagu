// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
	"github.com/dagucloud/dagu/v2/internal/proc"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureQueueDispatchRetryTarget_MissingRunReturnsNotQueued(t *testing.T) {
	t.Parallel()

	store := filedagrun.NewRepository(filepath.Join(t.TempDir(), "dag-runs"), dagrun.RepositoryOptions{LatestStatusToday: true})
	err := ensureQueueDispatchRetryTarget(
		context.Background(),
		store,
		ir.NewDAGRunRef("retry-test", "missing-run"),
		ir.DAGRunRef{},
	)
	require.Error(t, err)

	var notQueuedErr *queue.DAGRunNotQueuedError
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
	store := filedagrun.NewRepository(filepath.Join(t.TempDir(), "dag-runs"), dagrun.RepositoryOptions{LatestStatusToday: true})
	dag := &ir.DAG{
		Name: "retry-test",
		Steps: []ir.Step{
			{Name: "step", Command: "echo hi"},
		},
	}

	_, err := store.CreateAttempt(ctx, dag, time.Now(), "run-1", dagrun.CreateAttemptOptions{})
	require.NoError(t, err)

	err = ensureQueueDispatchRetryTarget(
		ctx,
		store,
		ir.NewDAGRunRef(dag.Name, "run-1"),
		ir.DAGRunRef{},
	)
	require.Error(t, err)

	var notQueuedErr *queue.DAGRunNotQueuedError
	require.ErrorAs(t, err, &notQueuedErr)
	assert.False(t, notQueuedErr.HasStatus)
}

func TestRestoreRetryExecutionContext_BackfillsStoredWorkingDirSnapshot(t *testing.T) {
	t.Parallel()

	dagDir := t.TempDir()
	workDir := t.TempDir()
	dag := &ir.DAG{
		Name:       "retry-test",
		Location:   filepath.Join(dagDir, "retry-test.yaml"),
		WorkingDir: workDir,
	}
	status := &ir.DAGRunStatus{}

	restoreRetryExecutionContext(dag, status, nil)

	assert.Equal(t, workDir, status.WorkingDir)
	assert.Equal(t, workDir, dag.WorkingDir)
	assert.True(t, dag.WorkingDirExplicit)
}

func TestRestoreRetryExecutionContext_BackfillsAttemptWorkDirSnapshot(t *testing.T) {
	t.Parallel()

	dagDir := t.TempDir()
	attemptWorkDir := t.TempDir()
	dag := &ir.DAG{
		Name:       "retry-test",
		Location:   filepath.Join(dagDir, "retry-test.yaml"),
		WorkingDir: dagDir,
	}
	status := &ir.DAGRunStatus{}
	attempt := &testutil.MockAttempt{}
	attempt.On("WorkDir").Return(attemptWorkDir).Once()

	restoreRetryExecutionContext(dag, status, attempt)

	assert.Equal(t, attemptWorkDir, status.WorkingDir)
	assert.Equal(t, attemptWorkDir, dag.WorkingDir)
	assert.True(t, dag.WorkingDirExplicit)
	attempt.AssertExpectations(t)
}

func TestWaitForRetrySourceRelease_WaitsForTerminalRunProcToStop(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{Name: "retry-test"}
	store := &retryReleaseProcStore{heartbeats: []*proc.ProcHeartbeat{
		retryReleaseHeartbeat(dag.Name, "run-1", "attempt-1", true),
		retryReleaseHeartbeat(dag.Name, "run-1", "attempt-1", true),
		nil,
	}}
	status := &ir.DAGRunStatus{
		Name:      dag.Name,
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
		Status:    ir.Succeeded,
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
	assert.Equal(t, ir.NewDAGRunRef(dag.Name, "run-1"), store.dagRun)
}

func TestWaitForRetrySourceRelease_SkipsActiveStatus(t *testing.T) {
	t.Parallel()

	store := &retryReleaseProcStore{
		heartbeats: []*proc.ProcHeartbeat{
			retryReleaseHeartbeat("retry-test", "run-1", "attempt-1", true),
		},
	}
	dag := &ir.DAG{Name: "retry-test"}
	status := &ir.DAGRunStatus{
		Name:     dag.Name,
		DAGRunID: "run-1",
		Status:   ir.Running,
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

	dag := &ir.DAG{Name: "retry-test"}
	store := &retryReleaseProcStore{
		alwaysHeartbeat: retryReleaseHeartbeat(dag.Name, "run-1", "attempt-1", true),
	}
	status := &ir.DAGRunStatus{
		Name:      dag.Name,
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
		Status:    ir.Failed,
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

	dag := &ir.DAG{Name: "retry-test"}
	store := &retryReleaseProcStore{heartbeats: []*proc.ProcHeartbeat{
		retryReleaseHeartbeat(dag.Name, "run-1", "attempt-2", true),
	}}
	status := &ir.DAGRunStatus{
		Name:      dag.Name,
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
		Status:    ir.Failed,
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
	proc.ProcStore

	heartbeats      []*proc.ProcHeartbeat
	alwaysHeartbeat *proc.ProcHeartbeat
	calls           int
	groupName       string
	dagRun          ir.DAGRunRef
}

func (s *retryReleaseProcStore) LatestHeartbeat(_ context.Context, groupName string, dagRun ir.DAGRunRef) (*proc.ProcHeartbeat, error) {
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

func retryReleaseHeartbeat(dagName, runID, attemptID string, fresh bool) *proc.ProcHeartbeat {
	return &proc.ProcHeartbeat{
		DAGRun:    ir.NewDAGRunRef(dagName, runID),
		AttemptID: attemptID,
		Fresh:     fresh,
	}
}
