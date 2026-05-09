// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnqueueDAGRunClosesStatusBeforeQueuePublish(t *testing.T) {
	f := newEnqueueDAGRunFixture(t, nil)

	require.NoError(t, enqueueDAGRun(f.ctx, f.dag, "run-1", core.TriggerTypeManual, ""))
	assert.True(t, f.queueStore.enqueued)
	require.NotNil(t, f.attempt.status)
	assert.Equal(t, core.Queued, f.attempt.status.Status)
}

func TestEnqueueDAGRunPublishesQueueWhenCloseFails(t *testing.T) {
	f := newEnqueueDAGRunFixture(t, errors.New("sync failed"))

	require.NoError(t, enqueueDAGRun(f.ctx, f.dag, "run-1", core.TriggerTypeManual, ""))
	assert.True(t, f.queueStore.enqueued)
	assert.True(t, f.attempt.closed, "attempt should be closed even when Close returns an error")
	require.NotNil(t, f.attempt.status)
	assert.Equal(t, core.Queued, f.attempt.status.Status)
}

type enqueueDAGRunFixture struct {
	attempt    *enqueueTrackingAttempt
	queueStore *enqueueObservingQueueStore
	ctx        *Context
	dag        *core.DAG
}

func newEnqueueDAGRunFixture(t *testing.T, closeErr error) enqueueDAGRunFixture {
	t.Helper()

	th := test.Setup(t)
	th.Config.Queues.Enabled = true

	attempt := &enqueueTrackingAttempt{
		id:       "attempt-1",
		closeErr: closeErr,
	}
	runStore := &enqueueTrackingDAGRunStore{attempt: attempt}
	queueStore := &enqueueObservingQueueStore{attempt: attempt}
	dag := th.DAG(t, `steps:
  - name: "step"
    run: "true"
`).DAG

	ctx := &Context{
		Context:     th.Context,
		Config:      th.Config,
		DAGRunStore: runStore,
		QueueStore:  queueStore,
	}

	return enqueueDAGRunFixture{
		attempt:    attempt,
		queueStore: queueStore,
		ctx:        ctx,
		dag:        dag,
	}
}

type enqueueTrackingDAGRunStore struct {
	attempt *enqueueTrackingAttempt
}

func (s *enqueueTrackingDAGRunStore) CreateAttempt(context.Context, *core.DAG, time.Time, string, exec.NewDAGRunAttemptOptions) (exec.DAGRunAttempt, error) {
	return s.attempt, nil
}

func (s *enqueueTrackingDAGRunStore) RecentAttempts(context.Context, string, int) []exec.DAGRunAttempt {
	return nil
}

func (s *enqueueTrackingDAGRunStore) LatestAttempt(context.Context, string) (exec.DAGRunAttempt, error) {
	return nil, exec.ErrDAGRunIDNotFound
}

func (s *enqueueTrackingDAGRunStore) ListStatuses(context.Context, ...exec.ListDAGRunStatusesOption) ([]*exec.DAGRunStatus, error) {
	return nil, nil
}

func (s *enqueueTrackingDAGRunStore) ListStatusesPage(context.Context, ...exec.ListDAGRunStatusesOption) (exec.DAGRunStatusPage, error) {
	return exec.DAGRunStatusPage{}, nil
}

func (s *enqueueTrackingDAGRunStore) CompareAndSwapLatestAttemptStatus(context.Context, exec.DAGRunRef, string, core.Status, func(*exec.DAGRunStatus) error) (*exec.DAGRunStatus, bool, error) {
	return nil, false, nil
}

func (s *enqueueTrackingDAGRunStore) FindAttempt(context.Context, exec.DAGRunRef) (exec.DAGRunAttempt, error) {
	return nil, exec.ErrDAGRunIDNotFound
}

func (s *enqueueTrackingDAGRunStore) FindSubAttempt(context.Context, exec.DAGRunRef, string) (exec.DAGRunAttempt, error) {
	return nil, exec.ErrDAGRunIDNotFound
}

func (s *enqueueTrackingDAGRunStore) CreateSubAttempt(context.Context, exec.DAGRunRef, string) (exec.DAGRunAttempt, error) {
	return nil, errors.New("not implemented")
}

func (s *enqueueTrackingDAGRunStore) RemoveOldDAGRuns(context.Context, string, int, ...exec.RemoveOldDAGRunsOption) ([]string, error) {
	return nil, nil
}

func (s *enqueueTrackingDAGRunStore) RenameDAGRuns(context.Context, string, string) error {
	return nil
}

func (s *enqueueTrackingDAGRunStore) RemoveDAGRun(context.Context, exec.DAGRunRef, ...exec.RemoveDAGRunOption) error {
	return nil
}

type enqueueTrackingAttempt struct {
	id       string
	dag      *core.DAG
	open     bool
	closed   bool
	closeErr error
	status   *exec.DAGRunStatus
}

func (a *enqueueTrackingAttempt) ID() string {
	return a.id
}

func (a *enqueueTrackingAttempt) Open(context.Context) error {
	a.open = true
	a.closed = false
	return nil
}

func (a *enqueueTrackingAttempt) Write(_ context.Context, status exec.DAGRunStatus) error {
	if !a.open {
		return errors.New("attempt is not open")
	}
	a.status = &status
	return nil
}

func (a *enqueueTrackingAttempt) Close(context.Context) error {
	a.open = false
	a.closed = true
	return a.closeErr
}

func (a *enqueueTrackingAttempt) ReadStatus(context.Context) (*exec.DAGRunStatus, error) {
	return a.status, nil
}

func (a *enqueueTrackingAttempt) ReadDAG(context.Context) (*core.DAG, error) {
	return a.dag, nil
}

func (a *enqueueTrackingAttempt) SetDAG(dag *core.DAG) {
	a.dag = dag
}

func (a *enqueueTrackingAttempt) Abort(context.Context) error {
	return nil
}

func (a *enqueueTrackingAttempt) IsAborting(context.Context) (bool, error) {
	return false, nil
}

func (a *enqueueTrackingAttempt) Hide(context.Context) error {
	return nil
}

func (a *enqueueTrackingAttempt) Hidden() bool {
	return false
}

func (a *enqueueTrackingAttempt) WriteOutputs(context.Context, *exec.DAGRunOutputs) error {
	return nil
}

func (a *enqueueTrackingAttempt) ReadOutputs(context.Context) (*exec.DAGRunOutputs, error) {
	return nil, nil
}

func (a *enqueueTrackingAttempt) WriteStepMessages(context.Context, string, []exec.LLMMessage) error {
	return nil
}

func (a *enqueueTrackingAttempt) ReadStepMessages(context.Context, string) ([]exec.LLMMessage, error) {
	return nil, nil
}

func (a *enqueueTrackingAttempt) WorkDir() string {
	return ""
}

type enqueueObservingQueueStore struct {
	attempt  *enqueueTrackingAttempt
	enqueued bool
}

func (s *enqueueObservingQueueStore) Enqueue(context.Context, string, exec.QueuePriority, exec.DAGRunRef) error {
	if !s.attempt.closed {
		return errors.New("status attempt was not closed before queue enqueue")
	}
	if s.attempt.status == nil || s.attempt.status.Status != core.Queued {
		return errors.New("queued status was not written before queue enqueue")
	}
	s.enqueued = true
	return nil
}

func (s *enqueueObservingQueueStore) DequeueByName(context.Context, string) (exec.QueuedItemData, error) {
	return nil, exec.ErrQueueEmpty
}

func (s *enqueueObservingQueueStore) DequeueByDAGRunID(context.Context, string, exec.DAGRunRef) ([]exec.QueuedItemData, error) {
	return nil, exec.ErrQueueItemNotFound
}

func (s *enqueueObservingQueueStore) DeleteByItemIDs(context.Context, string, []string) (int, error) {
	return 0, nil
}

func (s *enqueueObservingQueueStore) Len(context.Context, string) (int, error) {
	return 0, nil
}

func (s *enqueueObservingQueueStore) List(context.Context, string) ([]exec.QueuedItemData, error) {
	return nil, nil
}

func (s *enqueueObservingQueueStore) ListCursor(context.Context, string, string, int) (exec.CursorResult[exec.QueuedItemData], error) {
	return exec.CursorResult[exec.QueuedItemData]{}, nil
}

func (s *enqueueObservingQueueStore) All(context.Context) ([]exec.QueuedItemData, error) {
	return nil, nil
}

func (s *enqueueObservingQueueStore) ListByDAGName(context.Context, string, string) ([]exec.QueuedItemData, error) {
	return nil, nil
}

func (s *enqueueObservingQueueStore) QueueList(context.Context) ([]string, error) {
	return nil, nil
}

func (s *enqueueObservingQueueStore) QueueWatcher(context.Context) exec.QueueWatcher {
	return nil
}
