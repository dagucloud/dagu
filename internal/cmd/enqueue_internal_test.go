// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnqueueDAGRunClosesStatusBeforeQueuePublish(t *testing.T) {
	f := newEnqueueDAGRunFixture(t, nil)

	require.NoError(t, enqueueDAGRun(f.ctx, f.dag, "run-1", runOptions{triggerType: ir.TriggerTypeManual}))
	assert.True(t, f.queueStore.enqueued)
	require.NotNil(t, f.attempt.status)
	assert.Equal(t, ir.Queued, f.attempt.status.Status)
}

func TestEnqueueDAGRunPublishesQueueWhenCloseFails(t *testing.T) {
	f := newEnqueueDAGRunFixture(t, errors.New("sync failed"))

	require.NoError(t, enqueueDAGRun(f.ctx, f.dag, "run-1", runOptions{triggerType: ir.TriggerTypeManual}))
	assert.True(t, f.queueStore.enqueued)
	assert.True(t, f.attempt.closed, "attempt should be closed even when Close returns an error")
	require.NotNil(t, f.attempt.status)
	assert.Equal(t, ir.Queued, f.attempt.status.Status)
}

type enqueueDAGRunFixture struct {
	attempt    *enqueueTrackingAttempt
	queueStore *enqueueObservingQueueStore
	ctx        *Context
	dag        *ir.DAG
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
		Context: th.Context,
		Config:  th.Config,
		Persistence: Persistence{
			DAGRunRepository: persis.NewDAGRunRepository(runStore, nil, persis.DAGRunRepositoryOptions{}),
			QueueStore:       queueStore,
		},
	}

	return enqueueDAGRunFixture{
		attempt:    attempt,
		queueStore: queueStore,
		ctx:        ctx,
		dag:        dag,
	}
}

type enqueueTrackingDAGRunStore struct {
	testutil.DAGRunStoreStub
	attempt *enqueueTrackingAttempt
}

func (s *enqueueTrackingDAGRunStore) CreateAttempt(context.Context, persis.DAGRunCreateAttemptRequest) (dagrun.Attempt, error) {
	return s.attempt, nil
}

func (s *enqueueTrackingDAGRunStore) FindAttempt(context.Context, ir.DAGRunRef) (dagrun.Attempt, error) {
	return nil, dagrun.ErrDAGRunIDNotFound
}

type enqueueTrackingAttempt struct {
	id       string
	dag      *ir.DAG
	open     bool
	closed   bool
	closeErr error
	status   *ir.DAGRunStatus
}

func (a *enqueueTrackingAttempt) ID() string {
	return a.id
}

func (a *enqueueTrackingAttempt) Open(context.Context) error {
	a.open = true
	a.closed = false
	return nil
}

func (a *enqueueTrackingAttempt) Write(_ context.Context, status ir.DAGRunStatus) error {
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

func (a *enqueueTrackingAttempt) ReadStatus(context.Context) (*ir.DAGRunStatus, error) {
	return a.status, nil
}

func (a *enqueueTrackingAttempt) ReadStatusUncached(ctx context.Context) (*ir.DAGRunStatus, error) {
	return a.ReadStatus(ctx)
}

func (a *enqueueTrackingAttempt) ReadDAG(context.Context) (*ir.DAG, error) {
	return a.dag, nil
}

func (a *enqueueTrackingAttempt) SetDAG(dag *ir.DAG) {
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

func (a *enqueueTrackingAttempt) WriteOutputs(context.Context, *ir.DAGRunOutputs) error {
	return nil
}

func (a *enqueueTrackingAttempt) ReadOutputs(context.Context) (*ir.DAGRunOutputs, error) {
	return nil, nil
}

func (a *enqueueTrackingAttempt) WriteStepMessages(context.Context, string, []ir.LLMMessage) error {
	return nil
}

func (a *enqueueTrackingAttempt) ReadStepMessages(context.Context, string) ([]ir.LLMMessage, error) {
	return nil, nil
}

type enqueueObservingQueueStore struct {
	attempt  *enqueueTrackingAttempt
	enqueued bool
}

func (s *enqueueObservingQueueStore) Enqueue(context.Context, string, queue.QueuePriority, ir.DAGRunRef) error {
	if !s.attempt.closed {
		return errors.New("status attempt was not closed before queue enqueue")
	}
	if s.attempt.status == nil || s.attempt.status.Status != ir.Queued {
		return errors.New("queued status was not written before queue enqueue")
	}
	s.enqueued = true
	return nil
}

func (s *enqueueObservingQueueStore) DequeueByDAGRunID(context.Context, string, ir.DAGRunRef) ([]queue.QueuedItemData, error) {
	return nil, queue.ErrQueueItemNotFound
}

func (s *enqueueObservingQueueStore) DeleteByItemIDs(context.Context, string, []string) (int, error) {
	return 0, nil
}

func (s *enqueueObservingQueueStore) Len(context.Context, string) (int, error) {
	return 0, nil
}

func (s *enqueueObservingQueueStore) List(context.Context, string) ([]queue.QueuedItemData, error) {
	return nil, nil
}

func (s *enqueueObservingQueueStore) GetByItemID(context.Context, string, string) (queue.QueuedItemData, error) {
	return nil, queue.ErrQueueItemNotFound
}

func (s *enqueueObservingQueueStore) ListCursor(context.Context, string, string, int) (pagination.CursorResult[queue.QueuedItemData], error) {
	return pagination.CursorResult[queue.QueuedItemData]{}, nil
}

func (s *enqueueObservingQueueStore) Revision(context.Context, string) (int64, error) {
	return 0, nil
}

func (s *enqueueObservingQueueStore) All(context.Context) ([]queue.QueuedItemData, error) {
	return nil, nil
}

func (s *enqueueObservingQueueStore) ListByDAGName(context.Context, string, string) ([]queue.QueuedItemData, error) {
	return nil, nil
}

func (s *enqueueObservingQueueStore) QueueList(context.Context) ([]string, error) {
	return nil, nil
}

func (s *enqueueObservingQueueStore) QueueWatcher(context.Context) queue.QueueWatcher {
	return nil
}
