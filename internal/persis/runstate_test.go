// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/runtime/runstate"
	"github.com/dagucloud/dagu/v2/internal/testutil"
)

func TestRunStateStoreBeginAttemptUsesPreparedAttempt(t *testing.T) {
	ctx := context.Background()
	dag := &ir.DAG{Name: "parent"}
	attempt := newRecordingAttempt("attempt-1")
	store := &recordingRunStateBackend{}

	stateStore := persis.NewRunStateStore(testDAGRunRepository(store), attempt)
	got, err := stateStore.BeginAttempt(ctx, runstate.BeginAttemptRequest{
		DAG:       dag,
		RunID:     "run-1",
		AttemptID: "attempt-1",
	})

	require.NoError(t, err)
	require.Equal(t, "attempt-1", got.ID())
	require.Same(t, dag, attempt.dag)
	require.Zero(t, store.createCalls)
}

func TestRunStateStoreBeginAttemptRejectsPreparedAttemptIDMismatch(t *testing.T) {
	ctx := context.Background()
	attempt := newRecordingAttempt("prepared-attempt")
	store := &recordingRunStateBackend{}

	stateStore := persis.NewRunStateStore(testDAGRunRepository(store), attempt)
	got, err := stateStore.BeginAttempt(ctx, runstate.BeginAttemptRequest{
		DAG:       &ir.DAG{Name: "parent"},
		RunID:     "run-1",
		AttemptID: "requested-attempt",
	})

	require.ErrorContains(t, err, "prepared attempt ID")
	require.Nil(t, got)
	require.Zero(t, store.createCalls)
}

func TestNoopRunStateAttemptUsesRequestedID(t *testing.T) {
	attempt := runstate.NewNoopAttempt(runstate.BeginAttemptRequest{
		DAG:       &ir.DAG{Name: "parent"},
		RunID:     "run-1",
		AttemptID: "attempt-1",
	})

	require.Equal(t, "attempt-1", attempt.ID())
}

func TestRunStateStoreBeginAttemptCreatesAttemptAndAppliesRetention(t *testing.T) {
	ctx := context.Background()
	dag := &ir.DAG{Name: "parent", HistRetentionRuns: 3}
	store := &recordingRunStateBackend{
		createAttempt: newRecordingAttempt("attempt-2"),
	}

	stateStore := persis.NewRunStateStore(testDAGRunRepository(store), nil)
	got, err := stateStore.BeginAttempt(ctx, runstate.BeginAttemptRequest{
		DAG:        dag,
		RunID:      "run-2",
		AttemptID:  "attempt-2",
		Retry:      true,
		RootDAGRun: ir.NewDAGRunRef("root", "root-run"),
	})

	require.NoError(t, err)
	require.Equal(t, "attempt-2", got.ID())
	require.Equal(t, 1, store.createCalls)
	require.Equal(t, "run-2", store.createRunID)
	require.True(t, store.createOpts.Retry)
	require.Equal(t, "attempt-2", store.createOpts.AttemptID)
	require.Equal(t, ir.NewDAGRunRef("root", "root-run"), store.createOpts.RootDAGRun)
	require.Len(t, store.removeOldCalls, 1)
	require.Equal(t, 3, store.removeOldCalls[0].KeepRuns)
}

func TestRunStateStoreBeginAttemptOmitsRootDAGRunForRootAttempt(t *testing.T) {
	ctx := context.Background()
	store := &recordingRunStateBackend{
		createAttempt: newRecordingAttempt("attempt-1"),
	}

	stateStore := persis.NewRunStateStore(testDAGRunRepository(store), nil)
	got, err := stateStore.BeginAttempt(ctx, runstate.BeginAttemptRequest{
		DAG:        &ir.DAG{Name: "parent"},
		RunID:      "root-run",
		RootDAGRun: ir.NewDAGRunRef("parent", "root-run"),
	})

	require.NoError(t, err)
	require.Equal(t, "attempt-1", got.ID())
	require.True(t, store.createOpts.RootDAGRun.Zero())
}

func TestRunStateStoreBeginAttemptIgnoresRetentionCleanupFailure(t *testing.T) {
	ctx := context.Background()
	dag := &ir.DAG{Name: "parent", HistRetentionDays: 7}
	store := &recordingRunStateBackend{
		createAttempt: newRecordingAttempt("attempt-1"),
		removeOldErr:  errors.New("cleanup failed"),
	}

	stateStore := persis.NewRunStateStore(testDAGRunRepository(store), nil)
	got, err := stateStore.BeginAttempt(ctx, runstate.BeginAttemptRequest{
		DAG:   dag,
		RunID: "run-1",
	})

	require.NoError(t, err)
	require.Equal(t, "attempt-1", got.ID())
	require.Equal(t, 1, store.createCalls)
	require.Len(t, store.removeOldCalls, 1)
	require.Equal(t, persis.NewUTC(runStateStoreNow.AddDate(0, 0, -7)), store.removeOldCalls[0].OlderThan)
}

func TestRunStateStoreOpenChildAttemptReturnsAttemptState(t *testing.T) {
	ctx := context.Background()
	status := &ir.DAGRunStatus{Name: "child", DAGRunID: "child-run", Status: ir.Succeeded}
	attempt := newRecordingAttempt("child-attempt")
	attempt.status = status
	store := &recordingRunStateBackend{
		subAttempt: attempt,
	}

	stateStore := persis.NewRunStateStore(testDAGRunRepository(store), nil)
	child, err := stateStore.OpenChildAttempt(ctx, ir.NewDAGRunRef("root", "root-run"), "child-run")
	require.NoError(t, err)

	got, err := child.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, status, got)

	require.NoError(t, child.RequestCancel(ctx))
	require.Equal(t, 1, attempt.abortCalls)
}

func TestAttemptDelegatesStateOperations(t *testing.T) {
	ctx := context.Background()
	attempt := newRecordingAttempt("attempt-1")
	store := &recordingRunStateBackend{
		createAttempt: attempt,
	}
	stateStore := persis.NewRunStateStore(testDAGRunRepository(store), nil)
	stateAttempt, err := stateStore.BeginAttempt(ctx, runstate.BeginAttemptRequest{
		DAG:   &ir.DAG{Name: "parent"},
		RunID: "run-1",
	})
	require.NoError(t, err)

	status := ir.DAGRunStatus{Name: "parent", DAGRunID: "run-1", Status: ir.Running}
	outputs := &ir.DAGRunOutputs{Outputs: map[string]string{"result": "ok"}}
	messages := []ir.LLMMessage{{Role: ir.LLMRoleAssistant, Content: "done"}}

	require.NoError(t, stateAttempt.Open(ctx))
	require.NoError(t, stateAttempt.RecordStatus(ctx, status))
	require.NoError(t, stateAttempt.RecordOutputs(ctx, outputs))
	require.NoError(t, stateAttempt.WriteStepMessages(ctx, "step-1", messages))
	gotMessages, err := stateAttempt.ReadStepMessages(ctx, "step-1")
	require.NoError(t, err)
	cancelled, err := stateAttempt.CancelRequested(ctx)
	require.NoError(t, err)
	require.False(t, cancelled)
	require.NoError(t, stateAttempt.Close(ctx))

	require.Equal(t, 1, attempt.openCalls)
	require.Equal(t, status, attempt.writtenStatus)
	require.Equal(t, outputs, attempt.writtenOutputs)
	require.Equal(t, messages, gotMessages)
	require.Equal(t, 1, attempt.closeCalls)
}

type recordingRunStateBackend struct {
	testutil.DAGRunStoreStub
	createAttempt  dagrun.Attempt
	subAttempt     dagrun.Attempt
	createCalls    int
	createRunID    string
	createOpts     persis.DAGRunCreateAttemptOptions
	removeOldErr   error
	removeOldCalls []persis.DAGRunRetentionRequest
}

func (s *recordingRunStateBackend) CreateAttempt(_ context.Context, req persis.DAGRunCreateAttemptRequest) (dagrun.Attempt, error) {
	s.createCalls++
	s.createRunID = req.DAGRunID
	s.createOpts = persis.DAGRunCreateAttemptOptions{
		RootDAGRun: req.RootDAGRun,
		Retry:      req.Retry,
		AttemptID:  req.AttemptID,
	}
	if s.createAttempt == nil {
		return newRecordingAttempt(req.AttemptID), nil
	}
	s.createAttempt.SetDAG(req.DAG)
	return s.createAttempt, nil
}

func (s *recordingRunStateBackend) FindSubAttempt(context.Context, ir.DAGRunRef, string) (dagrun.Attempt, error) {
	if s.subAttempt == nil {
		return nil, dagrun.ErrDAGRunIDNotFound
	}
	return s.subAttempt, nil
}

func (s *recordingRunStateBackend) RemoveOldDAGRuns(_ context.Context, req persis.DAGRunRetentionRequest) ([]ir.DAGRunRef, error) {
	s.removeOldCalls = append(s.removeOldCalls, req)
	return nil, s.removeOldErr
}

var runStateStoreNow = time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)

func testDAGRunRepository(backend persis.DAGRunStore) *persis.DAGRunRepository {
	return persis.NewDAGRunRepository(backend, nil, persis.DAGRunRepositoryOptions{
		Now: func() time.Time { return runStateStoreNow },
	})
}

type recordingAttempt struct {
	id             string
	dag            *ir.DAG
	status         *ir.DAGRunStatus
	writtenStatus  ir.DAGRunStatus
	writtenOutputs *ir.DAGRunOutputs
	messages       map[string][]ir.LLMMessage
	openCalls      int
	closeCalls     int
	abortCalls     int
}

func newRecordingAttempt(id string) *recordingAttempt {
	return &recordingAttempt{id: id, messages: make(map[string][]ir.LLMMessage)}
}

func (a *recordingAttempt) ID() string { return a.id }

func (a *recordingAttempt) Open(context.Context) error {
	a.openCalls++
	return nil
}

func (a *recordingAttempt) Write(_ context.Context, status ir.DAGRunStatus) error {
	a.writtenStatus = status
	return nil
}

func (a *recordingAttempt) Close(context.Context) error {
	a.closeCalls++
	return nil
}

func (a *recordingAttempt) ReadStatus(context.Context) (*ir.DAGRunStatus, error) {
	return a.status, nil
}

func (a *recordingAttempt) ReadStatusUncached(ctx context.Context) (*ir.DAGRunStatus, error) {
	return a.ReadStatus(ctx)
}

func (a *recordingAttempt) ReadDAG(context.Context) (*ir.DAG, error) {
	return a.dag, nil
}

func (a *recordingAttempt) SetDAG(dag *ir.DAG) {
	a.dag = dag
}

func (a *recordingAttempt) Abort(context.Context) error {
	a.abortCalls++
	return nil
}

func (a *recordingAttempt) IsAborting(context.Context) (bool, error) {
	return false, nil
}

func (a *recordingAttempt) Hide(context.Context) error { return nil }

func (a *recordingAttempt) Hidden() bool { return false }

func (a *recordingAttempt) WriteOutputs(_ context.Context, outputs *ir.DAGRunOutputs) error {
	a.writtenOutputs = outputs
	return nil
}

func (a *recordingAttempt) ReadOutputs(context.Context) (*ir.DAGRunOutputs, error) {
	return nil, nil
}

func (a *recordingAttempt) WriteStepMessages(_ context.Context, stepName string, messages []ir.LLMMessage) error {
	a.messages[stepName] = append([]ir.LLMMessage(nil), messages...)
	return nil
}

func (a *recordingAttempt) ReadStepMessages(_ context.Context, stepName string) ([]ir.LLMMessage, error) {
	return append([]ir.LLMMessage(nil), a.messages[stepName]...), nil
}
