// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runstate_test

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

func TestHistoryStoreBeginAttemptUsesPreparedAttempt(t *testing.T) {
	ctx := context.Background()
	dag := &ir.DAG{Name: "parent"}
	attempt := newRecordingAttempt("attempt-1")
	store := &recordingDAGRunBackend{}

	stateStore := runstate.NewHistoryStore(testDAGRunRepository(store), runstate.WithPreparedAttempt(attempt))
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

func TestHistoryStoreBeginAttemptRejectsPreparedAttemptIDMismatch(t *testing.T) {
	ctx := context.Background()
	attempt := newRecordingAttempt("prepared-attempt")
	store := &recordingDAGRunBackend{}

	stateStore := runstate.NewHistoryStore(testDAGRunRepository(store), runstate.WithPreparedAttempt(attempt))
	got, err := stateStore.BeginAttempt(ctx, runstate.BeginAttemptRequest{
		DAG:       &ir.DAG{Name: "parent"},
		RunID:     "run-1",
		AttemptID: "requested-attempt",
	})

	require.ErrorContains(t, err, "prepared attempt ID")
	require.Nil(t, got)
	require.Zero(t, store.createCalls)
}

func TestHistoryStoreBeginAttemptUsesNoopAttemptWhenStoreMissing(t *testing.T) {
	ctx := context.Background()

	stateStore := runstate.NewHistoryStore(nil)
	attempt, err := stateStore.BeginAttempt(ctx, runstate.BeginAttemptRequest{
		DAG:       &ir.DAG{Name: "parent"},
		RunID:     "run-1",
		AttemptID: "attempt-1",
	})

	require.NoError(t, err)
	require.Equal(t, "attempt-1", attempt.ID())
}

func TestHistoryStoreBeginAttemptCreatesAttemptAndAppliesRetention(t *testing.T) {
	ctx := context.Background()
	dag := &ir.DAG{Name: "parent", HistRetentionRuns: 3}
	store := &recordingDAGRunBackend{
		createAttempt: newRecordingAttempt("attempt-2"),
	}

	stateStore := runstate.NewHistoryStore(testDAGRunRepository(store))
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

func TestHistoryStoreBeginAttemptOmitsRootDAGRunForRootAttempt(t *testing.T) {
	ctx := context.Background()
	store := &recordingDAGRunBackend{
		createAttempt: newRecordingAttempt("attempt-1"),
	}

	stateStore := runstate.NewHistoryStore(testDAGRunRepository(store))
	got, err := stateStore.BeginAttempt(ctx, runstate.BeginAttemptRequest{
		DAG:        &ir.DAG{Name: "parent"},
		RunID:      "root-run",
		RootDAGRun: ir.NewDAGRunRef("parent", "root-run"),
	})

	require.NoError(t, err)
	require.Equal(t, "attempt-1", got.ID())
	require.True(t, store.createOpts.RootDAGRun.Zero())
}

func TestHistoryStoreBeginAttemptIgnoresRetentionCleanupFailure(t *testing.T) {
	ctx := context.Background()
	dag := &ir.DAG{Name: "parent", HistRetentionDays: 7}
	store := &recordingDAGRunBackend{
		createAttempt: newRecordingAttempt("attempt-1"),
		removeOldErr:  errors.New("cleanup failed"),
	}

	stateStore := runstate.NewHistoryStore(testDAGRunRepository(store))
	got, err := stateStore.BeginAttempt(ctx, runstate.BeginAttemptRequest{
		DAG:   dag,
		RunID: "run-1",
	})

	require.NoError(t, err)
	require.Equal(t, "attempt-1", got.ID())
	require.Equal(t, 1, store.createCalls)
	require.Len(t, store.removeOldCalls, 1)
	require.Equal(t, persis.NewUTC(historyStoreNow.AddDate(0, 0, -7)), store.removeOldCalls[0].OlderThan)
}

func TestHistoryStoreOpenChildAttemptReturnsAttemptState(t *testing.T) {
	ctx := context.Background()
	status := &ir.DAGRunStatus{Name: "child", DAGRunID: "child-run", Status: ir.Succeeded}
	attempt := newRecordingAttempt("child-attempt")
	attempt.status = status
	store := &recordingDAGRunBackend{
		subAttempt: attempt,
	}

	stateStore := runstate.NewHistoryStore(testDAGRunRepository(store))
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
	store := &recordingDAGRunBackend{
		createAttempt: attempt,
	}
	stateStore := runstate.NewHistoryStore(testDAGRunRepository(store))
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

type recordingDAGRunBackend struct {
	testutil.DAGRunBackendStub
	createAttempt  dagrun.Attempt
	subAttempt     dagrun.Attempt
	createCalls    int
	createRunID    string
	createOpts     persis.DAGRunCreateAttemptOptions
	removeOldErr   error
	removeOldCalls []persis.DAGRunRetentionRequest
}

func (s *recordingDAGRunBackend) CreateAttempt(_ context.Context, req persis.DAGRunCreateAttemptRequest) (dagrun.Attempt, error) {
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

func (s *recordingDAGRunBackend) FindSubAttempt(context.Context, ir.DAGRunRef, string) (dagrun.Attempt, error) {
	if s.subAttempt == nil {
		return nil, dagrun.ErrDAGRunIDNotFound
	}
	return s.subAttempt, nil
}

func (s *recordingDAGRunBackend) RemoveOldDAGRuns(_ context.Context, req persis.DAGRunRetentionRequest) ([]ir.DAGRunRef, error) {
	s.removeOldCalls = append(s.removeOldCalls, req)
	return nil, s.removeOldErr
}

var historyStoreNow = time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)

func testDAGRunRepository(backend persis.DAGRunStore) *persis.DAGRunRepository {
	return persis.NewDAGRunRepository(backend, nil, persis.DAGRunRepositoryOptions{
		Now: func() time.Time { return historyStoreNow },
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
