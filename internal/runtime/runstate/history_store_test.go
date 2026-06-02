// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runstate_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime/runstate"
)

func TestHistoryStoreBeginAttemptUsesPreparedAttempt(t *testing.T) {
	ctx := context.Background()
	dag := &core.DAG{Name: "parent"}
	attempt := newRecordingAttempt("attempt-1")
	store := &recordingDAGRunStore{}

	stateStore := runstate.NewHistoryStore(store, runstate.WithPreparedAttempt(attempt))
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
	store := &recordingDAGRunStore{}

	stateStore := runstate.NewHistoryStore(store, runstate.WithPreparedAttempt(attempt))
	got, err := stateStore.BeginAttempt(ctx, runstate.BeginAttemptRequest{
		DAG:       &core.DAG{Name: "parent"},
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
		DAG:       &core.DAG{Name: "parent"},
		RunID:     "run-1",
		AttemptID: "attempt-1",
	})

	require.NoError(t, err)
	require.Equal(t, "attempt-1", attempt.ID())
}

func TestHistoryStoreBeginAttemptCreatesAttemptAndAppliesRetention(t *testing.T) {
	ctx := context.Background()
	dag := &core.DAG{Name: "parent", HistRetentionRuns: 3}
	store := &recordingDAGRunStore{
		createAttempt: newRecordingAttempt("attempt-2"),
	}

	stateStore := runstate.NewHistoryStore(store)
	got, err := stateStore.BeginAttempt(ctx, runstate.BeginAttemptRequest{
		DAG:        dag,
		RunID:      "run-2",
		AttemptID:  "attempt-2",
		Retry:      true,
		RootDAGRun: exec.NewDAGRunRef("root", "root-run"),
	})

	require.NoError(t, err)
	require.Equal(t, "attempt-2", got.ID())
	require.Equal(t, 1, store.createCalls)
	require.Equal(t, "run-2", store.createRunID)
	require.True(t, store.createOpts.Retry)
	require.Equal(t, "attempt-2", store.createOpts.AttemptID)
	require.NotNil(t, store.createOpts.RootDAGRun)
	require.Equal(t, exec.NewDAGRunRef("root", "root-run"), *store.createOpts.RootDAGRun)
	require.Len(t, store.removeOldCalls, 1)
	require.Equal(t, 0, store.removeOldCalls[0].retentionDays)
	require.NotNil(t, store.removeOldCalls[0].opts.RetentionRuns)
	require.Equal(t, 3, *store.removeOldCalls[0].opts.RetentionRuns)
}

func TestHistoryStoreBeginAttemptOmitsRootDAGRunForRootAttempt(t *testing.T) {
	ctx := context.Background()
	store := &recordingDAGRunStore{
		createAttempt: newRecordingAttempt("attempt-1"),
	}

	stateStore := runstate.NewHistoryStore(store)
	got, err := stateStore.BeginAttempt(ctx, runstate.BeginAttemptRequest{
		DAG:        &core.DAG{Name: "parent"},
		RunID:      "root-run",
		RootDAGRun: exec.NewDAGRunRef("parent", "root-run"),
	})

	require.NoError(t, err)
	require.Equal(t, "attempt-1", got.ID())
	require.Nil(t, store.createOpts.RootDAGRun)
}

func TestHistoryStoreBeginAttemptIgnoresRetentionCleanupFailure(t *testing.T) {
	ctx := context.Background()
	dag := &core.DAG{Name: "parent", HistRetentionDays: 7}
	store := &recordingDAGRunStore{
		createAttempt: newRecordingAttempt("attempt-1"),
		removeOldErr:  errors.New("cleanup failed"),
	}

	stateStore := runstate.NewHistoryStore(store)
	got, err := stateStore.BeginAttempt(ctx, runstate.BeginAttemptRequest{
		DAG:   dag,
		RunID: "run-1",
	})

	require.NoError(t, err)
	require.Equal(t, "attempt-1", got.ID())
	require.Equal(t, 1, store.createCalls)
	require.Len(t, store.removeOldCalls, 1)
	require.Equal(t, 7, store.removeOldCalls[0].retentionDays)
}

func TestHistoryStoreOpenChildAttemptReturnsAttemptState(t *testing.T) {
	ctx := context.Background()
	status := &exec.DAGRunStatus{Name: "child", DAGRunID: "child-run", Status: core.Succeeded}
	attempt := newRecordingAttempt("child-attempt")
	attempt.status = status
	store := &recordingDAGRunStore{
		subAttempt: attempt,
	}

	stateStore := runstate.NewHistoryStore(store)
	child, err := stateStore.OpenChildAttempt(ctx, exec.NewDAGRunRef("root", "root-run"), "child-run")
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
	store := &recordingDAGRunStore{
		createAttempt: attempt,
	}
	stateStore := runstate.NewHistoryStore(store)
	stateAttempt, err := stateStore.BeginAttempt(ctx, runstate.BeginAttemptRequest{
		DAG:   &core.DAG{Name: "parent"},
		RunID: "run-1",
	})
	require.NoError(t, err)

	status := exec.DAGRunStatus{Name: "parent", DAGRunID: "run-1", Status: core.Running}
	outputs := &exec.DAGRunOutputs{Outputs: map[string]string{"result": "ok"}}
	messages := []exec.LLMMessage{{Role: exec.RoleAssistant, Content: "done"}}

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

type recordingDAGRunStore struct {
	createAttempt  exec.DAGRunAttempt
	subAttempt     exec.DAGRunAttempt
	createCalls    int
	createRunID    string
	createOpts     exec.NewDAGRunAttemptOptions
	removeOldErr   error
	removeOldCalls []removeOldCall
}

type removeOldCall struct {
	retentionDays int
	opts          exec.RemoveOldDAGRunsOptions
}

func (s *recordingDAGRunStore) CreateAttempt(_ context.Context, dag *core.DAG, _ time.Time, dagRunID string, opts exec.NewDAGRunAttemptOptions) (exec.DAGRunAttempt, error) {
	s.createCalls++
	s.createRunID = dagRunID
	s.createOpts = opts
	if s.createAttempt == nil {
		return newRecordingAttempt(opts.AttemptID), nil
	}
	s.createAttempt.SetDAG(dag)
	return s.createAttempt, nil
}

func (s *recordingDAGRunStore) RecentAttempts(context.Context, string, int) []exec.DAGRunAttempt {
	return nil
}

func (s *recordingDAGRunStore) LatestAttempt(context.Context, string) (exec.DAGRunAttempt, error) {
	return nil, exec.ErrDAGRunIDNotFound
}

func (s *recordingDAGRunStore) ListStatuses(context.Context, ...exec.ListDAGRunStatusesOption) ([]*exec.DAGRunStatus, error) {
	return nil, nil
}

func (s *recordingDAGRunStore) ListStatusesPage(context.Context, ...exec.ListDAGRunStatusesOption) (exec.DAGRunStatusPage, error) {
	return exec.DAGRunStatusPage{}, nil
}

func (s *recordingDAGRunStore) CompareAndSwapLatestAttemptStatus(context.Context, exec.DAGRunRef, string, core.Status, func(*exec.DAGRunStatus) error, ...exec.CompareAndSwapStatusOption) (*exec.DAGRunStatus, bool, error) {
	return nil, false, nil
}

func (s *recordingDAGRunStore) FindAttempt(context.Context, exec.DAGRunRef) (exec.DAGRunAttempt, error) {
	return nil, exec.ErrDAGRunIDNotFound
}

func (s *recordingDAGRunStore) FindSubAttempt(context.Context, exec.DAGRunRef, string) (exec.DAGRunAttempt, error) {
	if s.subAttempt == nil {
		return nil, exec.ErrDAGRunIDNotFound
	}
	return s.subAttempt, nil
}

func (s *recordingDAGRunStore) CreateSubAttempt(context.Context, exec.DAGRunRef, string) (exec.DAGRunAttempt, error) {
	return nil, nil
}

func (s *recordingDAGRunStore) RemoveOldDAGRuns(_ context.Context, _ string, retentionDays int, opts ...exec.RemoveOldDAGRunsOption) ([]string, error) {
	var options exec.RemoveOldDAGRunsOptions
	for _, opt := range opts {
		opt(&options)
	}
	s.removeOldCalls = append(s.removeOldCalls, removeOldCall{retentionDays: retentionDays, opts: options})
	return nil, s.removeOldErr
}

func (s *recordingDAGRunStore) RenameDAGRuns(context.Context, string, string) error {
	return nil
}

func (s *recordingDAGRunStore) RemoveDAGRun(context.Context, exec.DAGRunRef, ...exec.RemoveDAGRunOption) error {
	return nil
}

type recordingAttempt struct {
	id             string
	dag            *core.DAG
	status         *exec.DAGRunStatus
	writtenStatus  exec.DAGRunStatus
	writtenOutputs *exec.DAGRunOutputs
	messages       map[string][]exec.LLMMessage
	openCalls      int
	closeCalls     int
	abortCalls     int
}

func newRecordingAttempt(id string) *recordingAttempt {
	return &recordingAttempt{id: id, messages: make(map[string][]exec.LLMMessage)}
}

func (a *recordingAttempt) ID() string { return a.id }

func (a *recordingAttempt) Open(context.Context) error {
	a.openCalls++
	return nil
}

func (a *recordingAttempt) Write(_ context.Context, status exec.DAGRunStatus) error {
	a.writtenStatus = status
	return nil
}

func (a *recordingAttempt) Close(context.Context) error {
	a.closeCalls++
	return nil
}

func (a *recordingAttempt) ReadStatus(context.Context) (*exec.DAGRunStatus, error) {
	return a.status, nil
}

func (a *recordingAttempt) ReadDAG(context.Context) (*core.DAG, error) {
	return a.dag, nil
}

func (a *recordingAttempt) SetDAG(dag *core.DAG) {
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

func (a *recordingAttempt) WriteOutputs(_ context.Context, outputs *exec.DAGRunOutputs) error {
	a.writtenOutputs = outputs
	return nil
}

func (a *recordingAttempt) ReadOutputs(context.Context) (*exec.DAGRunOutputs, error) {
	return nil, nil
}

func (a *recordingAttempt) WriteStepMessages(_ context.Context, stepName string, messages []exec.LLMMessage) error {
	a.messages[stepName] = append([]exec.LLMMessage(nil), messages...)
	return nil
}

func (a *recordingAttempt) ReadStepMessages(_ context.Context, stepName string) ([]exec.LLMMessage, error) {
	return append([]exec.LLMMessage(nil), a.messages[stepName]...), nil
}

func (a *recordingAttempt) WorkDir() string { return "" }
