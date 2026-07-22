// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	persisfile "github.com/dagucloud/dagu/internal/persis/file"
	persisstore "github.com/dagucloud/dagu/internal/persis/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	retryQueueTestKey1     = "0190f4c0-0000-7000-8000-000000000001"
	retryQueueTestKey2     = "0190f4c0-0001-7000-8000-000000000002"
	retryQueueTestKey3     = "0190f4c0-0002-7000-8000-000000000003"
	retryQueueTestKey4     = "0190f4c0-0003-7000-8000-000000000004"
	retryQueueTestKey5     = "0190f4c0-0004-7000-8000-000000000005"
	retryQueueTestKey6     = "0190f4c0-0005-7000-8000-000000000006"
	retryQueueTestKeyStale = "0180f4c0-0000-7000-8000-000000000007"
)

func TestQueueProcessorReconcileRetryQueueIntentsPublishesUnpublishedIntent(t *testing.T) {
	t.Parallel()

	dag := &core.DAG{Name: "retry-recovery-dag"}
	status := retryQueueTestStatus(dag.Name, "run-1", retryQueueTestKey1, false)
	store := newRetryScannerStore(dag, status)
	queueStore := newRetryQueueTestQueueStore(t)
	processor := &QueueProcessor{dagRunStore: store, queueStore: queueStore}

	require.NoError(t, processor.reconcileRetryQueueIntents(t.Context()))

	latest := store.mustStatus(status.DAGRun())
	require.NotNil(t, latest)
	assert.True(t, latest.RetryQueuePublished)
	assert.Equal(t, status.RetryQueueKey, latest.RetryQueueKey)

	items, err := queueStore.List(t.Context(), dag.ProcGroup())
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, status.DAGRun(), requireRetryQueueRef(t, items[0]))
	assert.Equal(t, status.RetryQueueKey, exec.QueuedItemEnqueueKey(items[0]))
}

func TestQueueProcessorReconcileRetryQueueIntentsSkipsCompletedIntents(t *testing.T) {
	t.Parallel()

	dag := &core.DAG{Name: "retry-completed-intents-dag"}
	published := retryQueueTestStatus(dag.Name, "run-published", retryQueueTestKey2, true)
	withoutKey := retryQueueTestStatus(dag.Name, "run-without-key", "", false)
	store := newRetryScannerStore(dag, published, withoutKey)
	queueStore := newRetryQueueTestQueueStore(t)
	processor := &QueueProcessor{dagRunStore: store, queueStore: queueStore}

	require.NoError(t, processor.reconcileRetryQueueIntents(t.Context()))

	items, err := queueStore.All(t.Context())
	require.NoError(t, err)
	assert.Empty(t, items)
	assert.True(t, store.mustStatus(published.DAGRun()).RetryQueuePublished)
	assert.Empty(t, store.mustStatus(withoutKey.DAGRun()).RetryQueueKey)
}

func TestQueueProcessorReconcileRetryQueueIntentsContinuesAfterItemFailure(t *testing.T) {
	t.Parallel()

	dag := &core.DAG{Name: "retry-partial-recovery-dag"}
	failed := retryQueueTestStatus(dag.Name, "run-failed", retryQueueTestKey3, false)
	succeeded := retryQueueTestStatus(dag.Name, "run-succeeded", retryQueueTestKey4, false)
	store := newRetryScannerStore(dag, failed, succeeded)
	queueStore := newRetryQueueTestQueueStore(t)
	failingQueueStore := &selectiveRetryQueueFailureStore{
		QueueStore: queueStore,
		failed:     failed.DAGRun(),
	}
	processor := &QueueProcessor{dagRunStore: store, queueStore: failingQueueStore}

	err := processor.reconcileRetryQueueIntents(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), failed.DAGRun().String())
	assert.ErrorContains(t, err, "queue unavailable")

	assert.False(t, store.mustStatus(failed.DAGRun()).RetryQueuePublished)
	assert.True(t, store.mustStatus(succeeded.DAGRun()).RetryQueuePublished)
	items, listErr := queueStore.List(t.Context(), dag.ProcGroup())
	require.NoError(t, listErr)
	require.Len(t, items, 1)
	assert.Equal(t, succeeded.DAGRun(), requireRetryQueueRef(t, items[0]))
	assert.Equal(t, succeeded.RetryQueueKey, exec.QueuedItemEnqueueKey(items[0]))
}

func TestQueueProcessorRetryQueueItemLifecycle(t *testing.T) {
	t.Parallel()

	t.Run("CurrentIntentIsRetainedWhileRunBecomesAlive", func(t *testing.T) {
		t.Parallel()

		dag := &core.DAG{Name: "retry-active-item-dag"}
		status := retryQueueTestStatus(dag.Name, "run-1", retryQueueTestKey5, true)
		store := newRetryScannerStore(dag, status)
		queueStore := newRetryQueueTestQueueStore(t)
		require.NoError(t, queueStore.EnsureEnqueued(
			t.Context(), dag.ProcGroup(), exec.QueuePriorityLow, status.DAGRun(), status.RetryQueueKey,
		))

		procStore := &mockProcStore{}
		procStore.On("CountAlive", mock.Anything, dag.ProcGroup()).Return(0, nil).Once()
		procStore.On("IsRunAlive", mock.Anything, dag.ProcGroup(), status.DAGRun()).Return(true, nil).Once()
		processor := newRetryQueueTestProcessor(queueStore, store, procStore, dag.ProcGroup())

		processor.ProcessQueueItems(t.Context(), dag.ProcGroup())

		items, err := queueStore.List(t.Context(), dag.ProcGroup())
		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.Equal(t, status.RetryQueueKey, exec.QueuedItemEnqueueKey(items[0]))
		procStore.AssertExpectations(t)
	})

	t.Run("StaleIntentDeletesOnlyItsOwnItem", func(t *testing.T) {
		t.Parallel()

		dag := &core.DAG{Name: "retry-stale-item-dag"}
		status := retryQueueTestStatus(dag.Name, "run-1", retryQueueTestKey6, true)
		store := newRetryScannerStore(dag, status)
		queueStore := newRetryQueueTestQueueStore(t)
		require.NoError(t, queueStore.EnsureEnqueued(
			t.Context(), dag.ProcGroup(), exec.QueuePriorityLow, status.DAGRun(), retryQueueTestKeyStale,
		))
		require.NoError(t, queueStore.EnsureEnqueued(
			t.Context(), dag.ProcGroup(), exec.QueuePriorityLow, status.DAGRun(), status.RetryQueueKey,
		))

		procStore := &mockProcStore{}
		procStore.On("CountAlive", mock.Anything, dag.ProcGroup()).Return(0, nil).Once()
		procStore.On("IsRunAlive", mock.Anything, dag.ProcGroup(), status.DAGRun()).Return(false, nil).Once()
		processor := newRetryQueueTestProcessor(queueStore, store, procStore, dag.ProcGroup())

		processor.ProcessQueueItems(t.Context(), dag.ProcGroup())

		items, err := queueStore.List(t.Context(), dag.ProcGroup())
		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.Equal(t, status.DAGRun(), requireRetryQueueRef(t, items[0]))
		assert.Equal(t, status.RetryQueueKey, exec.QueuedItemEnqueueKey(items[0]))
		procStore.AssertExpectations(t)
	})

	t.Run("CorruptedStatusRemovesItem", func(t *testing.T) {
		t.Parallel()

		dag := &core.DAG{Name: "retry-corrupt-status-dag"}
		status := retryQueueTestStatus(dag.Name, "run-1", retryQueueTestKey1, true)
		store := newRetryScannerStore(dag, status)
		store.attempts[status.DAGRun().String()].readStatusErr = exec.ErrCorruptedStatusFile
		queueStore := newRetryQueueTestQueueStore(t)
		require.NoError(t, queueStore.EnsureEnqueued(
			t.Context(), dag.ProcGroup(), exec.QueuePriorityLow, status.DAGRun(), status.RetryQueueKey,
		))

		procStore := &mockProcStore{}
		procStore.On("CountAlive", mock.Anything, dag.ProcGroup()).Return(0, nil).Once()
		procStore.On("IsRunAlive", mock.Anything, dag.ProcGroup(), status.DAGRun()).Return(false, nil).Once()
		processor := newRetryQueueTestProcessor(queueStore, store, procStore, dag.ProcGroup())

		processor.ProcessQueueItems(t.Context(), dag.ProcGroup())

		items, err := queueStore.List(t.Context(), dag.ProcGroup())
		require.NoError(t, err)
		assert.Empty(t, items)
		procStore.AssertExpectations(t)
	})
}

type selectiveRetryQueueFailureStore struct {
	exec.QueueStore
	failed exec.DAGRunRef
}

func (s *selectiveRetryQueueFailureStore) EnsureEnqueued(
	ctx context.Context,
	name string,
	priority exec.QueuePriority,
	dagRun exec.DAGRunRef,
	key string,
) error {
	if dagRun == s.failed {
		return errors.New("queue unavailable")
	}
	return s.QueueStore.EnsureEnqueued(ctx, name, priority, dagRun, key)
}

func retryQueueTestStatus(dagName, runID, queueKey string, published bool) *exec.DAGRunStatus {
	return &exec.DAGRunStatus{
		Name:                dagName,
		DAGRunID:            runID,
		AttemptID:           "attempt-" + runID,
		AttemptKey:          "attempt-key-" + runID,
		Status:              core.Queued,
		QueuedAt:            time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		RetryQueueKey:       queueKey,
		RetryQueuePublished: published,
	}
}

func newRetryQueueTestQueueStore(t *testing.T) *persisstore.QueueStore {
	t.Helper()
	return persisstore.NewQueueStore(persisfile.NewCollection(filepath.Join(t.TempDir(), "queue")))
}

func newRetryQueueTestProcessor(
	queueStore exec.QueueStore,
	dagRunStore exec.DAGRunStore,
	procStore exec.ProcStore,
	queueName string,
) *QueueProcessor {
	return NewQueueProcessor(
		queueStore,
		dagRunStore,
		procStore,
		nil,
		config.Queues{Config: []config.QueueConfig{{Name: queueName, MaxActiveRuns: 1}}},
	)
}

func requireRetryQueueRef(t *testing.T, item exec.QueuedItemData) exec.DAGRunRef {
	t.Helper()
	ref, err := item.Data()
	require.NoError(t, err)
	require.NotNil(t, ref)
	return *ref
}
