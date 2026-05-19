// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnqueueRunWritesQueuedStatusBeforeQueuePublish(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t)

	queued, err := EnqueueRun(f.ctx, QueueRequest{
		DAGRunStore:     f.runStore,
		QueueStore:      f.queueStore,
		DAG:             f.dag,
		DAGRunID:        "run-1",
		LogBaseDir:      f.logDir,
		ArtifactBaseDir: f.artifactDir,
		TriggerType:     core.TriggerTypeManual,
		Now:             fixedQueueNow,
	})

	require.NoError(t, err)
	require.NotNil(t, queued)
	assert.True(t, f.queueStore.enqueued)
	assert.Equal(t, exec.QueuePriorityLow, f.queueStore.priority)
	require.NotNil(t, f.attempt.status)
	assert.Equal(t, core.Queued, f.attempt.status.Status)
	assert.Equal(t, "attempt-1", f.attempt.status.AttemptID)
	assert.Equal(t, "2026-05-19T01:02:03Z", f.attempt.status.QueuedAt)
	assert.Equal(t, core.TriggerTypeManual, f.attempt.status.TriggerType)
	assert.Equal(t, f.attempt.status.Log, queued.LogFile)
	assert.Equal(t, f.attempt.status.ArchiveDir, queued.ArtifactDir)
}

func TestEnqueueRunRollsBackCreatedAttemptWhenQueuePublishFails(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t)
	f.queueStore.err = errors.New("queue offline")

	_, err := EnqueueRun(f.ctx, QueueRequest{
		DAGRunStore:     f.runStore,
		QueueStore:      f.queueStore,
		DAG:             f.dag,
		DAGRunID:        "run-1",
		LogBaseDir:      f.logDir,
		ArtifactBaseDir: f.artifactDir,
		Now:             fixedQueueNow,
	})

	require.Error(t, err)
	assert.True(t, f.runStore.removed)
	assert.Equal(t, exec.NewDAGRunRef("test-dag", "run-1"), f.runStore.removedRef)
}

func TestEnqueueRunCanProceedWhenAttemptCloseFails(t *testing.T) {
	t.Parallel()

	f := newQueueFixture(t)
	f.attempt.closeErr = errors.New("sync failed")

	queued, err := EnqueueRun(f.ctx, QueueRequest{
		DAGRunStore:             f.runStore,
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
	ctx         context.Context
	logDir      string
	artifactDir string
	dag         *core.DAG
	attempt     *queueAttempt
	runStore    *queueRunStore
	queueStore  *queueStore
}

func newQueueFixture(t *testing.T) queueFixture {
	t.Helper()

	tmp := t.TempDir()
	attempt := &queueAttempt{id: "attempt-1"}
	dag := &core.DAG{
		Name:   "test-dag",
		LogDir: "logs",
		Artifacts: &core.ArtifactsConfig{
			Enabled: true,
			Dir:     "artifacts",
		},
	}
	core.InitializeDefaults(dag)

	return queueFixture{
		ctx:         context.Background(),
		logDir:      filepath.Join(tmp, "logs"),
		artifactDir: filepath.Join(tmp, "artifacts"),
		dag:         dag,
		attempt:     attempt,
		runStore:    &queueRunStore{attempt: attempt},
		queueStore:  &queueStore{attempt: attempt},
	}
}

type queueRunStore struct {
	attempt    *queueAttempt
	removed    bool
	removedRef exec.DAGRunRef
}

func (s *queueRunStore) CreateAttempt(context.Context, *core.DAG, time.Time, string, exec.NewDAGRunAttemptOptions) (exec.DAGRunAttempt, error) {
	return s.attempt, nil
}

func (s *queueRunStore) RecentAttempts(context.Context, string, int) []exec.DAGRunAttempt {
	return nil
}

func (s *queueRunStore) LatestAttempt(context.Context, string) (exec.DAGRunAttempt, error) {
	return nil, exec.ErrDAGRunIDNotFound
}

func (s *queueRunStore) ListStatuses(context.Context, ...exec.ListDAGRunStatusesOption) ([]*exec.DAGRunStatus, error) {
	return nil, nil
}

func (s *queueRunStore) ListStatusesPage(context.Context, ...exec.ListDAGRunStatusesOption) (exec.DAGRunStatusPage, error) {
	return exec.DAGRunStatusPage{}, nil
}

func (s *queueRunStore) CompareAndSwapLatestAttemptStatus(context.Context, exec.DAGRunRef, string, core.Status, func(*exec.DAGRunStatus) error, ...exec.CompareAndSwapStatusOption) (*exec.DAGRunStatus, bool, error) {
	return nil, false, nil
}

func (s *queueRunStore) FindAttempt(context.Context, exec.DAGRunRef) (exec.DAGRunAttempt, error) {
	return nil, exec.ErrDAGRunIDNotFound
}

func (s *queueRunStore) FindSubAttempt(context.Context, exec.DAGRunRef, string) (exec.DAGRunAttempt, error) {
	return nil, exec.ErrDAGRunIDNotFound
}

func (s *queueRunStore) CreateSubAttempt(context.Context, exec.DAGRunRef, string) (exec.DAGRunAttempt, error) {
	return nil, errors.New("not implemented")
}

func (s *queueRunStore) RemoveOldDAGRuns(context.Context, string, int, ...exec.RemoveOldDAGRunsOption) ([]string, error) {
	return nil, nil
}

func (s *queueRunStore) RenameDAGRuns(context.Context, string, string) error {
	return nil
}

func (s *queueRunStore) RemoveDAGRun(_ context.Context, ref exec.DAGRunRef, _ ...exec.RemoveDAGRunOption) error {
	s.removed = true
	s.removedRef = ref
	return nil
}

type queueAttempt struct {
	id       string
	dag      *core.DAG
	open     bool
	closed   bool
	openErr  error
	writeErr error
	closeErr error
	status   *exec.DAGRunStatus
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

func (a *queueAttempt) Write(_ context.Context, status exec.DAGRunStatus) error {
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

func (a *queueAttempt) ReadStatus(context.Context) (*exec.DAGRunStatus, error) { return a.status, nil }
func (a *queueAttempt) ReadDAG(context.Context) (*core.DAG, error)             { return a.dag, nil }
func (a *queueAttempt) SetDAG(dag *core.DAG)                                   { a.dag = dag }
func (a *queueAttempt) Abort(context.Context) error                            { return nil }
func (a *queueAttempt) IsAborting(context.Context) (bool, error)               { return false, nil }
func (a *queueAttempt) Hide(context.Context) error                             { return nil }
func (a *queueAttempt) Hidden() bool                                           { return false }
func (a *queueAttempt) WriteOutputs(context.Context, *exec.DAGRunOutputs) error {
	return nil
}
func (a *queueAttempt) ReadOutputs(context.Context) (*exec.DAGRunOutputs, error) {
	return nil, nil
}
func (a *queueAttempt) WriteStepMessages(context.Context, string, []exec.LLMMessage) error {
	return nil
}
func (a *queueAttempt) ReadStepMessages(context.Context, string) ([]exec.LLMMessage, error) {
	return nil, nil
}
func (a *queueAttempt) WorkDir() string { return "" }

type queueStore struct {
	attempt  *queueAttempt
	enqueued bool
	priority exec.QueuePriority
	err      error
}

func (s *queueStore) Enqueue(_ context.Context, _ string, priority exec.QueuePriority, _ exec.DAGRunRef) error {
	if s.err != nil {
		return s.err
	}
	if !s.attempt.closed {
		return errors.New("status attempt was not closed before queue enqueue")
	}
	if s.attempt.status == nil || s.attempt.status.Status != core.Queued {
		return errors.New("queued status was not written before queue enqueue")
	}
	s.enqueued = true
	s.priority = priority
	return nil
}

func (s *queueStore) DequeueByName(context.Context, string) (exec.QueuedItemData, error) {
	return nil, exec.ErrQueueEmpty
}
func (s *queueStore) DequeueByDAGRunID(context.Context, string, exec.DAGRunRef) ([]exec.QueuedItemData, error) {
	return nil, exec.ErrQueueItemNotFound
}
func (s *queueStore) DeleteByItemIDs(context.Context, string, []string) (int, error) {
	return 0, nil
}
func (s *queueStore) Len(context.Context, string) (int, error) { return 0, nil }
func (s *queueStore) List(context.Context, string) ([]exec.QueuedItemData, error) {
	return nil, nil
}
func (s *queueStore) ListCursor(context.Context, string, string, int) (exec.CursorResult[exec.QueuedItemData], error) {
	return exec.CursorResult[exec.QueuedItemData]{}, nil
}
func (s *queueStore) All(context.Context) ([]exec.QueuedItemData, error) { return nil, nil }
func (s *queueStore) ListByDAGName(context.Context, string, string) ([]exec.QueuedItemData, error) {
	return nil, nil
}
func (s *queueStore) QueueList(context.Context) ([]string, error) { return nil, nil }
func (s *queueStore) QueueWatcher(context.Context) exec.QueueWatcher {
	return nil
}
