// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnqueueRunWritesQueuedStatusBeforeQueuePublish(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t)

	queued, err := EnqueueRun(f.ctx, QueueRequest{
		DAGRunRepository: f.dagRunRepository,
		QueueStore:       f.queueStore,
		DAG:              f.dag,
		DAGRunID:         "run-1",
		LogBaseDir:       f.logDir,
		ArtifactBaseDir:  f.artifactDir,
		TriggerType:      ir.TriggerTypeManual,
		TriggerActor:     "alice",
		ProfileName:      "prod",
		DefinitionID:     "ops/daily",
		Now:              fixedQueueNow,
	})

	require.NoError(t, err)
	require.NotNil(t, queued)
	assert.True(t, f.queueStore.enqueued)
	assert.Equal(t, queue.QueuePriorityLow, f.queueStore.priority)
	require.NotNil(t, f.attempt.status)
	assert.Equal(t, ir.Queued, f.attempt.status.Status)
	assert.Equal(t, "attempt-1", f.attempt.status.AttemptID)
	assert.Equal(t, "2026-05-19T01:02:03Z", f.attempt.status.QueuedAt)
	assert.Equal(t, ir.TriggerTypeManual, f.attempt.status.TriggerType)
	assert.Equal(t, "alice", f.attempt.status.TriggerActor)
	assert.Equal(t, "prod", f.attempt.status.ProfileName)
	assert.Equal(t, "ops/daily", f.attempt.status.DefinitionID)
	assert.Equal(t, f.attempt.status.Log, queued.LogFile)
	assert.Equal(t, f.attempt.status.ArchiveDir, queued.ArtifactDir)
}

func TestEnqueueRunRollsBackCreatedAttemptWhenQueuePublishFails(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t)
	f.queueStore.err = errors.New("queue offline")

	_, err := EnqueueRun(f.ctx, QueueRequest{
		DAGRunRepository: f.dagRunRepository,
		QueueStore:       f.queueStore,
		DAG:              f.dag,
		DAGRunID:         "run-1",
		LogBaseDir:       f.logDir,
		ArtifactBaseDir:  f.artifactDir,
		Now:              fixedQueueNow,
	})

	require.Error(t, err)
	assert.True(t, f.runStore.removed)
	assert.Equal(t, ir.NewDAGRunRef("test-dag", "run-1"), f.runStore.removedRef)
}

func TestEnqueueRunCanProceedWhenAttemptCloseFails(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t)
	f.attempt.closeErr = errors.New("sync failed")

	queued, err := EnqueueRun(f.ctx, QueueRequest{
		DAGRunRepository:        f.dagRunRepository,
		QueueStore:              f.queueStore,
		DAG:                     f.dag,
		DAGRunID:                "run-1",
		LogBaseDir:              f.logDir,
		ArtifactBaseDir:         f.artifactDir,
		ProceedOnStatusCloseErr: true,
		Now:                     fixedQueueNow,
	})

	require.NoError(t, err)
	require.NotNil(t, queued)
	assert.True(t, f.queueStore.enqueued)
	assert.True(t, f.attempt.closed)
	assert.False(t, f.runStore.removed)
	assert.ErrorIs(t, queued.StatusCloseErr, f.attempt.closeErr)
}

func fixedQueueNow() time.Time {
	return time.Date(2026, 5, 19, 1, 2, 3, 0, time.UTC)
}

type queueFixture struct {
	ctx              context.Context
	logDir           string
	artifactDir      string
	dag              *ir.DAG
	attempt          *queueAttempt
	runStore         *queueRunStore
	dagRunRepository *persis.DAGRunRepository
	queueStore       *queueStore
}

func newQueueFixture(t *testing.T) queueFixture {
	t.Helper()

	tmp := t.TempDir()
	attempt := &queueAttempt{id: "attempt-1"}
	dag := &ir.DAG{
		Name:   "test-dag",
		LogDir: "logs",
		Artifacts: &ir.ArtifactsConfig{
			Enabled: true,
			Dir:     "artifacts",
		},
	}
	ir.InitializeDefaults(dag)

	runStore := &queueRunStore{attempt: attempt}
	return queueFixture{
		ctx:              context.Background(),
		logDir:           filepath.Join(tmp, "logs"),
		artifactDir:      filepath.Join(tmp, "artifacts"),
		dag:              dag,
		attempt:          attempt,
		runStore:         runStore,
		dagRunRepository: persis.NewDAGRunRepository(runStore, nil, persis.DAGRunRepositoryOptions{}),
		queueStore:       &queueStore{attempt: attempt},
	}
}

type queueRunStore struct {
	testutil.DAGRunStoreStub
	attempt    *queueAttempt
	removed    bool
	removedRef ir.DAGRunRef
}

func (s *queueRunStore) CreateAttempt(context.Context, persis.DAGRunCreateAttemptRequest) (dagrun.Attempt, error) {
	return s.attempt, nil
}

func (s *queueRunStore) RemoveDAGRun(_ context.Context, req persis.DAGRunRemoveRequest) error {
	s.removed = true
	s.removedRef = req.DAGRun
	return nil
}

type queueAttempt struct {
	id       string
	dag      *ir.DAG
	open     bool
	closed   bool
	openErr  error
	writeErr error
	closeErr error
	status   *ir.DAGRunStatus
}

func (a *queueAttempt) ID() string { return a.id }

func (a *queueAttempt) Open(context.Context) error {
	if a.openErr != nil {
		return a.openErr
	}
	a.open = true
	a.closed = false
	return nil
}

func (a *queueAttempt) Write(_ context.Context, status ir.DAGRunStatus) error {
	if !a.open {
		return errors.New("attempt is not open")
	}
	if a.writeErr != nil {
		return a.writeErr
	}
	a.status = &status
	return nil
}

func (a *queueAttempt) Close(context.Context) error {
	a.open = false
	a.closed = true
	return a.closeErr
}

func (a *queueAttempt) ReadStatus(context.Context) (*ir.DAGRunStatus, error) {
	return a.status, nil
}
func (a *queueAttempt) ReadStatusUncached(ctx context.Context) (*ir.DAGRunStatus, error) {
	return a.ReadStatus(ctx)
}
func (a *queueAttempt) ReadDAG(context.Context) (*ir.DAG, error) { return a.dag, nil }
func (a *queueAttempt) SetDAG(dag *ir.DAG)                       { a.dag = dag }
func (a *queueAttempt) Abort(context.Context) error              { return nil }
func (a *queueAttempt) IsAborting(context.Context) (bool, error) { return false, nil }
func (a *queueAttempt) Hide(context.Context) error               { return nil }
func (a *queueAttempt) Hidden() bool                             { return false }
func (a *queueAttempt) WriteOutputs(context.Context, *ir.DAGRunOutputs) error {
	return nil
}
func (a *queueAttempt) ReadOutputs(context.Context) (*ir.DAGRunOutputs, error) {
	return nil, nil
}
func (a *queueAttempt) WriteStepMessages(context.Context, string, []ir.LLMMessage) error {
	return nil
}
func (a *queueAttempt) ReadStepMessages(context.Context, string) ([]ir.LLMMessage, error) {
	return nil, nil
}

type queueStore struct {
	attempt  *queueAttempt
	enqueued bool
	priority queue.QueuePriority
	err      error
}

func (s *queueStore) Enqueue(_ context.Context, _ string, priority queue.QueuePriority, _ ir.DAGRunRef) error {
	if s.err != nil {
		return s.err
	}
	if !s.attempt.closed {
		return errors.New("status attempt was not closed before queue enqueue")
	}
	if s.attempt.status == nil || s.attempt.status.Status != ir.Queued {
		return errors.New("queued status was not written before queue enqueue")
	}
	s.enqueued = true
	s.priority = priority
	return nil
}

func (s *queueStore) DequeueByDAGRunID(context.Context, string, ir.DAGRunRef) ([]queue.QueuedItemData, error) {
	return nil, queue.ErrQueueItemNotFound
}
func (s *queueStore) DeleteByItemIDs(context.Context, string, []string) (int, error) {
	return 0, nil
}
func (s *queueStore) Len(context.Context, string) (int, error) { return 0, nil }
func (s *queueStore) List(context.Context, string) ([]queue.QueuedItemData, error) {
	return nil, nil
}
func (s *queueStore) GetByItemID(context.Context, string, string) (queue.QueuedItemData, error) {
	return nil, queue.ErrQueueItemNotFound
}
func (s *queueStore) ListCursor(context.Context, string, string, int) (pagination.CursorResult[queue.QueuedItemData], error) {
	return pagination.CursorResult[queue.QueuedItemData]{}, nil
}
func (s *queueStore) Revision(context.Context, string) (int64, error)     { return 0, nil }
func (s *queueStore) All(context.Context) ([]queue.QueuedItemData, error) { return nil, nil }
func (s *queueStore) ListByDAGName(context.Context, string, string) ([]queue.QueuedItemData, error) {
	return nil, nil
}
func (s *queueStore) QueueList(context.Context) ([]string, error) { return nil, nil }
func (s *queueStore) QueueWatcher(context.Context) queue.QueueWatcher {
	return nil
}
