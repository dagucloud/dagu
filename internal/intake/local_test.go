// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"context"
	"errors"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/proc"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareLocalExecutionAcquiresProcWithPreparedAttempt(t *testing.T) {
	t.Parallel()

	attempt := &queueAttempt{id: "attempt-1"}
	procRepository := &localProcRepository{handle: &localProcHandle{}}
	dag := newLocalDAG()
	root := ir.NewDAGRunRef("root-dag", "root-run")

	prepared, err := PrepareLocalExecution(context.Background(), LocalRequest{
		ProcRepository: procRepository,
		DAG:            dag,
		DAGRunID:       "run-1",
		Root:           root,
		TriggerType:    ir.TriggerTypeManual,
		BuildAttempt: func(context.Context) (dagrun.Attempt, error) {
			return attempt, nil
		},
	})

	require.NoError(t, err)
	require.NotNil(t, prepared)
	assert.Same(t, attempt, prepared.Attempt)
	assert.Same(t, dag, attempt.dag)
	assert.True(t, procRepository.locked)
	assert.True(t, procRepository.unlocked)
	assert.Equal(t, dag.ProcGroup(), procRepository.groupName)
	assert.Equal(t, "run-1", procRepository.meta.DAGRunID)
	assert.Equal(t, "attempt-1", procRepository.meta.AttemptID)
	assert.Equal(t, root.Name, procRepository.meta.RootName)
	assert.Equal(t, root.ID, procRepository.meta.RootDAGRunID)
}

func TestPrepareLocalExecutionRecordsFailedStatusWhenProcAcquireFails(t *testing.T) {
	t.Parallel()

	attempt := &queueAttempt{id: "attempt-1"}
	procRepository := &localProcRepository{
		handle:     &localProcHandle{},
		acquireErr: errors.New("already running"),
	}
	dag := newLocalDAG()

	_, err := PrepareLocalExecution(context.Background(), LocalRequest{
		ProcRepository: procRepository,
		DAG:            dag,
		DAGRunID:       "run-1",
		DefinitionID:   "daily.yaml",
		TriggerType:    ir.TriggerTypeManual,
		BuildAttempt: func(context.Context) (dagrun.Attempt, error) {
			return attempt, nil
		},
	})

	require.ErrorIs(t, err, ErrProcAcquisitionFailed)
	require.NotNil(t, attempt.status)
	assert.Equal(t, ir.Failed, attempt.status.Status)
	assert.Equal(t, "attempt-1", attempt.status.AttemptID)
	assert.Equal(t, "daily.yaml", attempt.status.DefinitionID)
	assert.Equal(t, "local", attempt.status.WorkerID)
	assert.Contains(t, attempt.status.Error, "already running")
	assert.True(t, attempt.closed)
	assert.True(t, procRepository.unlocked)
}

func TestPrepareLocalExecutionReturnsFailureRecordingErrorWhenRecordFails(t *testing.T) {
	t.Parallel()

	recordErr := errors.New("status write failed")
	attempt := &queueAttempt{id: "attempt-1", writeErr: recordErr}
	procRepository := &localProcRepository{
		handle:     &localProcHandle{},
		acquireErr: errors.New("already running"),
	}
	dag := newLocalDAG()

	_, err := PrepareLocalExecution(context.Background(), LocalRequest{
		ProcRepository: procRepository,
		DAG:            dag,
		DAGRunID:       "run-1",
		TriggerType:    ir.TriggerTypeManual,
		BuildAttempt: func(context.Context) (dagrun.Attempt, error) {
			return attempt, nil
		},
	})

	require.ErrorIs(t, err, ErrProcAcquisitionFailed)
	assert.ErrorIs(t, err, recordErr)
	assert.Contains(t, err.Error(), "already running")
	assert.Contains(t, err.Error(), "failed to record prepared local execution failure")
	assert.True(t, procRepository.unlocked)
}

func newLocalDAG() *ir.DAG {
	dag := &ir.DAG{
		Name:   "test-dag",
		LogDir: "logs",
	}
	ir.InitializeDefaults(dag)
	return dag
}

type localProcRepository struct {
	handle     proc.ProcHandle
	acquireErr error
	locked     bool
	unlocked   bool
	groupName  string
	meta       proc.ProcMeta
	fresh      []proc.ProcEntry
}

func (s *localProcRepository) FreshEntriesByDAGName(context.Context, string, string) ([]proc.ProcEntry, error) {
	return s.fresh, nil
}

func (s *localProcRepository) WithLock(_ context.Context, groupName string, fn func() error) error {
	s.locked = true
	s.groupName = groupName
	err := fn()
	s.unlocked = true
	return err
}

func (s *localProcRepository) Acquire(_ context.Context, groupName string, meta proc.ProcMeta) (proc.ProcHandle, error) {
	s.groupName = groupName
	s.meta = meta
	if s.acquireErr != nil {
		return nil, s.acquireErr
	}
	return s.handle, nil
}

type localProcHandle struct{}

func (h *localProcHandle) Stop(context.Context) error { return nil }

// A DAG with a restart_policy has one run at a time. The check is made under the
// process lock, so that of two starts that race, one sees the other.
func TestPrepareLocalExecutionAlwaysOnDAG(t *testing.T) {
	t.Parallel()

	rootEntry := func(runID string) proc.ProcEntry {
		return proc.ProcEntry{Meta: proc.ProcMeta{Name: "test-dag", DAGRunID: runID, RootName: "test-dag", RootDAGRunID: runID}, Fresh: true}
	}
	subEntry := proc.ProcEntry{Meta: proc.ProcMeta{Name: "test-dag", DAGRunID: "sub", RootName: "parent-dag", RootDAGRunID: "parent"}, Fresh: true}

	for _, tt := range []struct {
		name    string
		policy  ir.RestartPolicy
		parent  ir.DAGRunRef
		fresh   []proc.ProcEntry
		refused bool
	}{
		{name: "AnotherRunAlive", policy: ir.RestartPolicyAlways, fresh: []proc.ProcEntry{rootEntry("run-2")}, refused: true},
		{name: "NoRunAlive", policy: ir.RestartPolicyAlways},
		{name: "OwnRunAlive", policy: ir.RestartPolicyAlways, fresh: []proc.ProcEntry{rootEntry("run-1")}},
		{name: "SubDAGRunAlive", policy: ir.RestartPolicyAlways, fresh: []proc.ProcEntry{subEntry}},
		{name: "StartedAsSubDAG", policy: ir.RestartPolicyAlways, parent: ir.NewDAGRunRef("parent-dag", "parent"), fresh: []proc.ProcEntry{rootEntry("run-2")}},
		{name: "NoPolicy", fresh: []proc.ProcEntry{rootEntry("run-2")}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			procRepository := &localProcRepository{handle: &localProcHandle{}, fresh: tt.fresh}
			dag := newLocalDAG()
			dag.RestartPolicy = tt.policy
			built := false

			prepared, err := PrepareLocalExecution(context.Background(), LocalRequest{
				ProcRepository: procRepository,
				DAG:            dag,
				DAGRunID:       "run-1",
				Parent:         tt.parent,
				TriggerType:    ir.TriggerTypeManual,
				BuildAttempt: func(context.Context) (dagrun.Attempt, error) {
					built = true
					return &queueAttempt{id: "attempt-1"}, nil
				},
			})

			if tt.refused {
				require.ErrorIs(t, err, runtime.ErrAlwaysOnRunActive)
				assert.False(t, built, "a refused run leaves no attempt")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, prepared)
		})
	}
}
