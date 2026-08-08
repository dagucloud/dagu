// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"context"
	"errors"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareLocalExecutionAcquiresProcWithPreparedAttempt(t *testing.T) {
	t.Parallel()

	attempt := &queueAttempt{id: "attempt-1"}
	procStore := &localProcStore{handle: &localProcHandle{}}
	dag := newLocalDAG()
	root := dagrun.NewDAGRunRef("root-dag", "root-run")

	prepared, err := PrepareLocalExecution(context.Background(), LocalRequest{
		ProcStore:   procStore,
		DAG:         dag,
		DAGRunID:    "run-1",
		Root:        root,
		TriggerType: ir.TriggerTypeManual,
		BuildAttempt: func(context.Context) (dagrun.DAGRunAttempt, error) {
			return attempt, nil
		},
	})

	require.NoError(t, err)
	require.NotNil(t, prepared)
	assert.Same(t, attempt, prepared.Attempt)
	assert.Same(t, dag, attempt.dag)
	assert.True(t, procStore.locked)
	assert.True(t, procStore.unlocked)
	assert.Equal(t, dag.ProcGroup(), procStore.groupName)
	assert.Equal(t, "run-1", procStore.meta.DAGRunID)
	assert.Equal(t, "attempt-1", procStore.meta.AttemptID)
	assert.Equal(t, root.Name, procStore.meta.RootName)
	assert.Equal(t, root.ID, procStore.meta.RootDAGRunID)
}

func TestPrepareLocalExecutionRecordsFailedStatusWhenProcAcquireFails(t *testing.T) {
	t.Parallel()

	attempt := &queueAttempt{id: "attempt-1"}
	procStore := &localProcStore{
		handle:     &localProcHandle{},
		acquireErr: errors.New("already running"),
	}
	dag := newLocalDAG()

	_, err := PrepareLocalExecution(context.Background(), LocalRequest{
		ProcStore:   procStore,
		DAG:         dag,
		DAGRunID:    "run-1",
		TriggerType: ir.TriggerTypeManual,
		BuildAttempt: func(context.Context) (dagrun.DAGRunAttempt, error) {
			return attempt, nil
		},
	})

	require.ErrorIs(t, err, ErrProcAcquisitionFailed)
	require.NotNil(t, attempt.status)
	assert.Equal(t, ir.Failed, attempt.status.Status)
	assert.Equal(t, "attempt-1", attempt.status.AttemptID)
	assert.Equal(t, "local", attempt.status.WorkerID)
	assert.Contains(t, attempt.status.Error, "already running")
	assert.True(t, attempt.closed)
	assert.True(t, procStore.unlocked)
}

func TestPrepareLocalExecutionReturnsFailureRecordingErrorWhenRecordFails(t *testing.T) {
	t.Parallel()

	recordErr := errors.New("status write failed")
	attempt := &queueAttempt{id: "attempt-1", writeErr: recordErr}
	procStore := &localProcStore{
		handle:     &localProcHandle{},
		acquireErr: errors.New("already running"),
	}
	dag := newLocalDAG()

	_, err := PrepareLocalExecution(context.Background(), LocalRequest{
		ProcStore:   procStore,
		DAG:         dag,
		DAGRunID:    "run-1",
		TriggerType: ir.TriggerTypeManual,
		BuildAttempt: func(context.Context) (dagrun.DAGRunAttempt, error) {
			return attempt, nil
		},
	})

	require.ErrorIs(t, err, ErrProcAcquisitionFailed)
	assert.ErrorIs(t, err, recordErr)
	assert.Contains(t, err.Error(), "already running")
	assert.Contains(t, err.Error(), "failed to record prepared local execution failure")
	assert.True(t, procStore.unlocked)
}

func newLocalDAG() *ir.DAG {
	dag := &ir.DAG{
		Name:   "test-dag",
		LogDir: "logs",
	}
	ir.InitializeDefaults(dag)
	return dag
}

type localProcStore struct {
	handle     exec.ProcHandle
	acquireErr error
	locked     bool
	unlocked   bool
	groupName  string
	meta       exec.ProcMeta
}

func (s *localProcStore) Lock(_ context.Context, groupName string) error {
	s.locked = true
	s.groupName = groupName
	return nil
}

func (s *localProcStore) Unlock(context.Context, string) {
	s.unlocked = true
}

func (s *localProcStore) Acquire(_ context.Context, groupName string, meta exec.ProcMeta) (exec.ProcHandle, error) {
	s.groupName = groupName
	s.meta = meta
	if s.acquireErr != nil {
		return nil, s.acquireErr
	}
	return s.handle, nil
}

type localProcHandle struct{}

func (h *localProcHandle) Stop(context.Context) error { return nil }
