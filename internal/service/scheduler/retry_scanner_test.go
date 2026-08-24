// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	queuedomain "github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestRetryScannerEvaluateRetryDecision(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 14, 14, 0, 0, 0, time.UTC)
	baseDAG := &ir.DAG{
		Name:        "retry-dag",
		RetryPolicy: &ir.DAGRetryPolicy{Limit: 3, Interval: time.Minute, Backoff: 0, MaxInterval: 10 * time.Minute},
	}
	baseStatus := &ir.DAGRunStatus{
		Name:           "retry-dag",
		DAGRunID:       "run-1",
		AttemptID:      "att-1",
		Status:         ir.Failed,
		AutoRetryCount: 0,
		FinishedAt:     now.Add(-2 * time.Minute).Format(time.RFC3339),
		ScheduleTime:   now.Add(-10 * time.Minute).Format(time.RFC3339),
	}

	tests := []struct {
		name      string
		status    *ir.DAGRunStatus
		metadata  dagRetryMetadata
		enqueue   bool
		reason    string
		nextRetry time.Time
		delay     time.Duration
	}{
		{
			name:     "RetryExhaustedSkips",
			status:   withAutoRetryCount(baseStatus, 3),
			metadata: mustRetryMetadataFromDAG(t, baseDAG),
			reason:   "retry_exhausted",
		},
		{
			name:   "RetryLimitZeroSkips",
			status: cloneRetryStatus(baseStatus),
			metadata: dagRetryMetadata{
				limit:       0,
				interval:    time.Minute,
				maxInterval: 10 * time.Minute,
			},
			reason: "retry_policy_missing",
		},
		{
			name:      "MissingFinishedAtFallsBackToCreatedAt",
			status:    withCreatedAt(withFinishedAt(baseStatus, ""), now.Add(-2*time.Minute).UnixMilli()),
			metadata:  mustRetryMetadataFromDAG(t, baseDAG),
			enqueue:   true,
			nextRetry: now.Add(-time.Minute),
			delay:     time.Minute,
		},
		{
			name:     "MissingRetryReferenceTimeSkips",
			status:   withStartedAt(withCreatedAt(withFinishedAt(baseStatus, ""), 0), ""),
			metadata: mustRetryMetadataFromDAG(t, baseDAG),
			reason:   "missing_retry_reference_time",
		},
		{
			name:      "BackoffNotElapsedSkips",
			status:    withFinishedAt(baseStatus, now.Add(-30*time.Second).Format(time.RFC3339)),
			metadata:  mustRetryMetadataFromDAG(t, baseDAG),
			reason:    "backoff_not_elapsed",
			nextRetry: now.Add(30 * time.Second),
			delay:     time.Minute,
		},
		{
			name:      "EligibleFailureEnqueues",
			status:    cloneRetryStatus(baseStatus),
			metadata:  mustRetryMetadataFromDAG(t, baseDAG),
			enqueue:   true,
			nextRetry: now.Add(-time.Minute),
			delay:     time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			scanner, err := NewRetryScanner(
				nil,
				nil,
				nil,
				24*time.Hour,
				func() time.Time { return now },
			)
			require.NoError(t, err)

			got := scanner.evaluateRetryDecision(
				context.Background(),
				tt.status,
				tt.metadata,
				now,
			)

			assert.Equal(t, tt.enqueue, got.enqueue)
			assert.Equal(t, tt.reason, got.reason)
			assert.Equal(t, tt.nextRetry, got.nextRetryAt)
			assert.Equal(t, tt.delay, got.computedDelay)
		})
	}
}

func TestDAGRetryDelay(t *testing.T) {
	t.Parallel()

	t.Run("FixedIntervalBackoffStaysConstant", func(t *testing.T) {
		t.Parallel()

		delay := dagRetryDelay(time.Minute, 0, 10*time.Minute, 3)

		assert.Equal(t, time.Minute, delay)
	})

	t.Run("ExponentialBackoffIsCapped", func(t *testing.T) {
		t.Parallel()

		delay := dagRetryDelay(time.Minute, 2.0, 3*time.Minute, 3)

		assert.Equal(t, 3*time.Minute, delay)
	})
}

func TestNewRetryScanner(t *testing.T) {
	t.Parallel()

	t.Run("AllowsNilDependencies", func(t *testing.T) {
		t.Parallel()

		scanner, err := NewRetryScanner(nil, nil, nil, 0, time.Now)
		require.NoError(t, err)
		require.NotNil(t, scanner)
	})

	t.Run("Valid", func(t *testing.T) {
		t.Parallel()

		scanner, err := NewRetryScanner(nil, nil, nil, 0, time.Now)
		require.NoError(t, err)
		require.NotNil(t, scanner)
	})
}

func TestSuspendFlagName(t *testing.T) {
	t.Parallel()

	t.Run("UsesFilenameStem", func(t *testing.T) {
		t.Parallel()

		got := suspendFlagName(nil, &ir.DAG{
			Name:     "logical-name",
			Location: "/tmp/example-dag.yaml",
		}, "")

		assert.Equal(t, "example-dag", got)
	})

	t.Run("FallsBackToDAGNameWhenLocationMissing", func(t *testing.T) {
		t.Parallel()

		got := suspendFlagName(nil, &ir.DAG{
			Name: "logical-name",
		}, "")

		assert.Equal(t, "logical-name", got)
	})
}

func TestRetryScannerScanEnqueuesRetry(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 14, 14, 0, 0, 0, time.UTC)
	dag := &ir.DAG{
		Name:     "retry-dag",
		Location: "/tmp/retry-dag.yaml",
		RetryPolicy: &ir.DAGRetryPolicy{
			Limit:       3,
			Interval:    time.Minute,
			Backoff:     0,
			MaxInterval: 10 * time.Minute,
		},
	}
	status := &ir.DAGRunStatus{
		Name:           dag.Name,
		DAGRunID:       "run-1",
		AttemptID:      "att-1",
		Status:         ir.Failed,
		AutoRetryCount: 1,
		FinishedAt:     now.Add(-3 * time.Minute).Format(time.RFC3339),
		ScheduleTime:   now.Add(-10 * time.Minute).Format(time.RFC3339),
	}
	store := newRetryScannerStore(dag, status)
	queueStore := &testutil.MockQueueStore{}
	queueStore.On("Enqueue", mock.Anything, dag.ProcGroup(), queuedomain.QueuePriorityLow, status.DAGRun()).
		Return(nil).
		Once()

	scanner, err := NewRetryScanner(
		store.repository(),
		queueStore,
		nil,
		24*time.Hour,
		func() time.Time { return now },
	)
	require.NoError(t, err)

	err = scanner.scan(context.Background())
	require.NoError(t, err)

	latest := store.mustStatus(status.DAGRun())
	assert.Equal(t, ir.Queued, latest.Status)
	assert.Equal(t, ir.TriggerTypeRetry, latest.TriggerType)
	assert.NotEmpty(t, latest.QueuedAt)
	assert.Equal(t, 2, latest.AutoRetryCount)
	assert.Equal(t, 0, store.latestAttemptCalls)
	assert.Len(t, store.listCalls, 1)

	queueStore.AssertExpectations(t)
}

func TestRetryScannerScanSkipsDisabledRetryPolicy(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 14, 14, 0, 0, 0, time.UTC)
	dag := &ir.DAG{
		Name:     "retry-disabled-dag",
		Location: "/tmp/retry-disabled-dag.yaml",
		RetryPolicy: &ir.DAGRetryPolicy{
			Limit:       0,
			Interval:    time.Minute,
			Backoff:     0,
			MaxInterval: 10 * time.Minute,
		},
	}
	status := &ir.DAGRunStatus{
		Name:           dag.Name,
		DAGRunID:       "run-1",
		AttemptID:      "att-1",
		Status:         ir.Failed,
		AutoRetryCount: 0,
		FinishedAt:     now.Add(-3 * time.Minute).Format(time.RFC3339),
		ScheduleTime:   now.Add(-10 * time.Minute).Format(time.RFC3339),
	}
	store := newRetryScannerStore(dag, status)
	queueStore := &testutil.MockQueueStore{}

	scanner, err := NewRetryScanner(
		store.repository(),
		queueStore,
		nil,
		24*time.Hour,
		func() time.Time { return now },
	)
	require.NoError(t, err)

	err = scanner.scan(context.Background())
	require.NoError(t, err)

	latest := store.mustStatus(status.DAGRun())
	assert.Equal(t, ir.Failed, latest.Status)
	assert.Equal(t, 0, latest.AutoRetryCount)
	assert.Equal(t, 0, latest.AutoRetryLimit)
	assert.Equal(t, 0, store.latestAttemptCalls)
	assert.Len(t, store.listCalls, 1)
	assert.Equal(t, 0, store.findAttemptCalls)
	queueStore.AssertNotCalled(t, "Enqueue", mock.Anything, dag.ProcGroup(), queuedomain.QueuePriorityLow, status.DAGRun())
}

func TestRetryScannerScanEnqueuesRetryWithoutLiveTargets(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 14, 14, 0, 0, 0, time.UTC)
	dag := &ir.DAG{
		Name:     "retry-dag",
		Location: "/tmp/retry-dag.yaml",
		RetryPolicy: &ir.DAGRetryPolicy{
			Limit:       3,
			Interval:    time.Minute,
			Backoff:     0,
			MaxInterval: 10 * time.Minute,
		},
	}
	status := &ir.DAGRunStatus{
		Name:           dag.Name,
		DAGRunID:       "run-1",
		AttemptID:      "att-1",
		Status:         ir.Failed,
		AutoRetryCount: 0,
		FinishedAt:     now.Add(-2 * time.Minute).Format(time.RFC3339),
		ScheduleTime:   now.Add(-10 * time.Minute).Format(time.RFC3339),
	}
	store := newRetryScannerStore(dag, status)
	queueStore := &testutil.MockQueueStore{}
	queueStore.On("Enqueue", mock.Anything, dag.ProcGroup(), queuedomain.QueuePriorityLow, status.DAGRun()).
		Return(nil).
		Once()

	scanner, err := NewRetryScanner(
		store.repository(),
		queueStore,
		nil,
		24*time.Hour,
		func() time.Time { return now },
	)
	require.NoError(t, err)

	err = scanner.scan(context.Background())
	require.NoError(t, err)
	assert.Equal(t, ir.Queued, store.mustStatus(status.DAGRun()).Status)
	assert.Len(t, store.listCalls, 1)
	queueStore.AssertExpectations(t)
}

func TestRetryScannerScanRetriesOlderFailedRunEvenWhenNewerRunExists(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 15, 0, 10, 0, 0, time.UTC)
	dag := &ir.DAG{
		Name:     "retry-dag",
		Location: "/tmp/retry-dag.yaml",
		RetryPolicy: &ir.DAGRetryPolicy{
			Limit:       3,
			Interval:    time.Minute,
			Backoff:     0,
			MaxInterval: 10 * time.Minute,
		},
	}
	failed := &ir.DAGRunStatus{
		Name:           dag.Name,
		DAGRunID:       "run-1",
		AttemptID:      "att-1",
		Status:         ir.Failed,
		AutoRetryCount: 0,
		FinishedAt:     time.Date(2026, 3, 15, 0, 2, 0, 0, time.UTC).Format(time.RFC3339),
		ScheduleTime:   time.Date(2026, 3, 14, 23, 50, 0, 0, time.UTC).Format(time.RFC3339),
	}
	active := &ir.DAGRunStatus{
		Name:         dag.Name,
		DAGRunID:     "run-2",
		AttemptID:    "att-2",
		Status:       ir.Running,
		ScheduleTime: time.Date(2026, 3, 14, 23, 59, 0, 0, time.UTC).Format(time.RFC3339),
	}

	store := newRetryScannerStore(dag, failed, active)
	queueStore := &testutil.MockQueueStore{}
	queueStore.On("Enqueue", mock.Anything, dag.ProcGroup(), queuedomain.QueuePriorityLow, failed.DAGRun()).
		Return(nil).
		Once()

	scanner, err := NewRetryScanner(
		store.repository(),
		queueStore,
		nil,
		24*time.Hour,
		func() time.Time { return now },
	)
	require.NoError(t, err)

	err = scanner.scan(context.Background())
	require.NoError(t, err)

	assert.Equal(t, ir.Queued, store.mustStatus(failed.DAGRun()).Status)
	assert.Equal(t, ir.Running, store.mustStatus(active.DAGRun()).Status)
	assert.Equal(t, 0, store.latestAttemptCalls)
	assert.Len(t, store.listCalls, 1)
	queueStore.AssertExpectations(t)
}

func TestRetryScannerScanUsesPersistedRetryPolicy(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 14, 14, 0, 0, 0, time.UTC)
	retryDAG := &ir.DAG{
		Name:     "retry-dag",
		Location: "/tmp/retry-dag.yaml",
		RetryPolicy: &ir.DAGRetryPolicy{
			Limit:       3,
			Interval:    time.Minute,
			Backoff:     0,
			MaxInterval: 10 * time.Minute,
		},
	}
	noRetryDAG := &ir.DAG{Name: "plain-dag", Location: "/tmp/plain-dag.yaml"}
	retryStatus := &ir.DAGRunStatus{
		Name:           retryDAG.Name,
		DAGRunID:       "run-1",
		AttemptID:      "att-1",
		Status:         ir.Failed,
		AutoRetryCount: 0,
		FinishedAt:     now.Add(-2 * time.Minute).Format(time.RFC3339),
		ScheduleTime:   now.Add(-10 * time.Minute).Format(time.RFC3339),
	}
	plainStatus := &ir.DAGRunStatus{
		Name:           noRetryDAG.Name,
		DAGRunID:       "run-2",
		AttemptID:      "att-2",
		Status:         ir.Failed,
		AutoRetryCount: 0,
		FinishedAt:     now.Add(-2 * time.Minute).Format(time.RFC3339),
		ScheduleTime:   now.Add(-10 * time.Minute).Format(time.RFC3339),
	}
	store := newRetryScannerStoreWithEntries(
		retryScannerStoreEntry{dag: retryDAG, status: retryStatus},
		retryScannerStoreEntry{dag: noRetryDAG, status: plainStatus},
	)
	queueStore := &testutil.MockQueueStore{}
	queueStore.On("Enqueue", mock.Anything, retryDAG.ProcGroup(), queuedomain.QueuePriorityLow, retryStatus.DAGRun()).
		Return(nil).
		Once()

	scanner, err := NewRetryScanner(
		store.repository(),
		queueStore,
		nil,
		24*time.Hour,
		func() time.Time { return now },
	)
	require.NoError(t, err)

	err = scanner.scan(context.Background())
	require.NoError(t, err)

	assert.Equal(t, 0, store.latestAttemptCalls)
	assert.Len(t, store.listCalls, 1)
	assert.Equal(t, 1, store.mustStatus(retryStatus.DAGRun()).AutoRetryCount)
	assert.Equal(t, ir.Failed, store.mustStatus(plainStatus.DAGRun()).Status)
	queueStore.AssertExpectations(t)
}

func TestRetryScannerScanSkipsSuspendedPersistedRetries(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 14, 14, 0, 0, 0, time.UTC)
	dag := &ir.DAG{
		Name:     "retry-dag",
		Location: "/tmp/retry-dag.yaml",
		RetryPolicy: &ir.DAGRetryPolicy{
			Limit:       3,
			Interval:    time.Minute,
			Backoff:     0,
			MaxInterval: 10 * time.Minute,
		},
	}
	status := &ir.DAGRunStatus{
		Name:           dag.Name,
		DAGRunID:       "run-1",
		AttemptID:      "att-1",
		Status:         ir.Failed,
		AutoRetryCount: 0,
		FinishedAt:     now.Add(-2 * time.Minute).Format(time.RFC3339),
		ScheduleTime:   now.Add(-10 * time.Minute).Format(time.RFC3339),
	}
	store := newRetryScannerStore(dag, status)
	suspendedFlag := dag.SuspendFlagName()
	require.NotEmpty(t, suspendedFlag)

	scanner, err := NewRetryScanner(
		store.repository(),
		&testutil.MockQueueStore{},
		func(_ context.Context, name string) (bool, error) { return name == suspendedFlag, nil },
		24*time.Hour,
		func() time.Time { return now },
	)
	require.NoError(t, err)

	require.NoError(t, scanner.scan(context.Background()))

	assert.Equal(t, ir.Failed, store.mustStatus(status.DAGRun()).Status)
	assert.Equal(t, 0, store.mustStatus(status.DAGRun()).AutoRetryCount)
	assert.Equal(t, 0, store.latestAttemptCalls)
	assert.Len(t, store.listCalls, 1)
	assert.Equal(t, 0, store.findAttemptCalls)
}

func TestRetryScannerLeavesRetryPendingWhenSuspensionReadFails(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 14, 14, 0, 0, 0, time.UTC)
	dag := &ir.DAG{
		Name:     "retry-dag",
		Location: "/tmp/retry-dag.yaml",
		RetryPolicy: &ir.DAGRetryPolicy{
			Limit:       3,
			Interval:    time.Minute,
			MaxInterval: 10 * time.Minute,
		},
	}
	status := &ir.DAGRunStatus{
		Name:           dag.Name,
		DAGRunID:       "run-1",
		AttemptID:      "att-1",
		Status:         ir.Failed,
		FinishedAt:     now.Add(-2 * time.Minute).Format(time.RFC3339),
		ScheduleTime:   now.Add(-10 * time.Minute).Format(time.RFC3339),
		AutoRetryLimit: 3,
	}
	store := newRetryScannerStore(dag, status)
	readErr := errors.New("read suspend flag")
	scanner, err := NewRetryScanner(
		store.repository(),
		&testutil.MockQueueStore{},
		func(context.Context, string) (bool, error) { return false, readErr },
		24*time.Hour,
		func() time.Time { return now },
	)
	require.NoError(t, err)

	err = scanner.processFailedRun(context.Background(), status, now)
	require.ErrorIs(t, err, readErr)
	assert.Equal(t, ir.Failed, store.mustStatus(status.DAGRun()).Status)
	assert.Equal(t, 0, store.mustStatus(status.DAGRun()).AutoRetryCount)
}

func TestRetryScannerScanSkipsSuspendedLegacyStatuses(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 14, 14, 0, 0, 0, time.UTC)
	dag := &ir.DAG{
		Name:     "retry-dag",
		Location: "/tmp/retry-dag.yaml",
		RetryPolicy: &ir.DAGRetryPolicy{
			Limit:       3,
			Interval:    time.Minute,
			Backoff:     0,
			MaxInterval: 10 * time.Minute,
		},
	}
	status := &ir.DAGRunStatus{
		Name:           dag.Name,
		DAGRunID:       "run-legacy",
		AttemptID:      "att-legacy",
		Status:         ir.Failed,
		AutoRetryCount: 0,
		FinishedAt:     now.Add(-2 * time.Minute).Format(time.RFC3339),
		ScheduleTime:   now.Add(-10 * time.Minute).Format(time.RFC3339),
	}
	store := newRetryScannerStoreWithEntries(retryScannerStoreEntry{dag: dag, status: status})
	legacy := store.attempts[status.DAGRun().String()]
	require.NotNil(t, legacy)
	legacy.status.ProcGroup = ""
	legacy.status.DefinitionID = ""
	legacy.status.SuspendFlagName = ""
	legacy.status.AutoRetryLimit = 0
	legacy.status.AutoRetryInterval = 0
	legacy.status.AutoRetryBackoff = 0
	legacy.status.AutoRetryMaxInterval = 0

	suspendedFlag := dag.SuspendFlagName()
	require.NotEmpty(t, suspendedFlag)

	scanner, err := NewRetryScanner(
		store.repository(),
		&testutil.MockQueueStore{},
		func(_ context.Context, name string) (bool, error) { return name == suspendedFlag, nil },
		24*time.Hour,
		func() time.Time { return now },
	)
	require.NoError(t, err)

	require.NoError(t, scanner.scan(context.Background()))

	assert.Equal(t, 1, store.findAttemptCalls)
	assert.Equal(t, 0, store.latestAttemptCalls)
	assert.Len(t, store.listCalls, 1)
	assert.Equal(t, ir.Failed, store.mustStatus(status.DAGRun()).Status)
}

func TestRetryScannerScanFallsBackToDAGNameWhenSuspendSnapshotMissing(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 14, 14, 0, 0, 0, time.UTC)
	dag := &ir.DAG{
		Name:     "retry-dag",
		Location: "/tmp/retry-dag.yaml",
		RetryPolicy: &ir.DAGRetryPolicy{
			Limit:       3,
			Interval:    time.Minute,
			Backoff:     0,
			MaxInterval: 10 * time.Minute,
		},
	}
	status := &ir.DAGRunStatus{
		Name:           dag.Name,
		DAGRunID:       "run-1",
		AttemptID:      "att-1",
		Status:         ir.Failed,
		AutoRetryCount: 0,
		FinishedAt:     now.Add(-2 * time.Minute).Format(time.RFC3339),
		ScheduleTime:   now.Add(-10 * time.Minute).Format(time.RFC3339),
	}
	store := newRetryScannerStore(dag, status)
	store.attempts[status.DAGRun().String()].status.DefinitionID = ""
	store.attempts[status.DAGRun().String()].status.SuspendFlagName = ""

	var checked string
	scanner, err := NewRetryScanner(
		store.repository(),
		&testutil.MockQueueStore{},
		func(_ context.Context, name string) (bool, error) {
			checked = name
			return name == dag.Name, nil
		},
		24*time.Hour,
		func() time.Time { return now },
	)
	require.NoError(t, err)

	require.NoError(t, scanner.scan(context.Background()))

	assert.Equal(t, dag.Name, checked)
	assert.Equal(t, ir.Failed, store.mustStatus(status.DAGRun()).Status)
	assert.Equal(t, 0, store.findAttemptCalls)
}

func TestRetryScannerScanIsIdempotentForQueuedRun(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 14, 14, 0, 0, 0, time.UTC)
	dag := &ir.DAG{
		Name:     "retry-dag",
		Location: "/tmp/retry-dag.yaml",
		RetryPolicy: &ir.DAGRetryPolicy{
			Limit:       3,
			Interval:    time.Minute,
			Backoff:     0,
			MaxInterval: 10 * time.Minute,
		},
	}
	status := &ir.DAGRunStatus{
		Name:           dag.Name,
		DAGRunID:       "run-1",
		AttemptID:      "att-1",
		Status:         ir.Failed,
		AutoRetryCount: 0,
		FinishedAt:     now.Add(-2 * time.Minute).Format(time.RFC3339),
		ScheduleTime:   now.Add(-10 * time.Minute).Format(time.RFC3339),
	}
	store := newRetryScannerStore(dag, status)
	queueStore := &testutil.MockQueueStore{}
	queueStore.On("Enqueue", mock.Anything, dag.ProcGroup(), queuedomain.QueuePriorityLow, status.DAGRun()).
		Return(nil).
		Once()

	scanner, err := NewRetryScanner(
		store.repository(),
		queueStore,
		nil,
		24*time.Hour,
		func() time.Time { return now },
	)
	require.NoError(t, err)

	require.NoError(t, scanner.scan(context.Background()))
	require.NoError(t, scanner.scan(context.Background()))

	assert.Equal(t, ir.Queued, store.mustStatus(status.DAGRun()).Status)
	assert.Equal(t, 0, store.latestAttemptCalls)
	assert.Len(t, store.listCalls, 2)
	queueStore.AssertExpectations(t)
}

type retryScannerStore struct {
	testutil.DAGRunStoreStub
	attempts           map[string]*retryScannerAttempt
	latestByName       map[string]*retryScannerAttempt
	latestAttemptCalls int
	listCalls          []persis.DAGRunStatusQuery
	findAttemptCalls   int
}

type retryScannerStoreEntry struct {
	dag    *ir.DAG
	status *ir.DAGRunStatus
}

func newRetryScannerStore(dag *ir.DAG, statuses ...*ir.DAGRunStatus) *retryScannerStore {
	entries := make([]retryScannerStoreEntry, 0, len(statuses))
	for _, status := range statuses {
		if status == nil {
			continue
		}
		entries = append(entries, retryScannerStoreEntry{dag: dag, status: status})
	}
	return newRetryScannerStoreWithEntries(entries...)
}

func newRetryScannerStoreWithEntries(entries ...retryScannerStoreEntry) *retryScannerStore {
	attempts := make(map[string]*retryScannerAttempt, len(entries))
	latestByName := make(map[string]*retryScannerAttempt)
	for _, entry := range entries {
		if entry.status == nil {
			continue
		}
		status := cloneRetryStatus(entry.status)
		applyRetrySnapshot(status, entry.dag)
		attempts[entry.status.DAGRun().String()] = &retryScannerAttempt{
			id:     status.AttemptID,
			status: status,
			dag:    entry.dag,
		}
		latestByName[status.Name] = attempts[entry.status.DAGRun().String()]
	}
	return &retryScannerStore{attempts: attempts, latestByName: latestByName}
}

func (s *retryScannerStore) repository() *persis.DAGRunRepository {
	return persis.NewDAGRunRepository(s, nil, persis.DAGRunRepositoryOptions{})
}

func (s *retryScannerStore) CreateAttempt(context.Context, persis.DAGRunCreateAttemptRequest) (dagrun.Attempt, error) {
	return nil, errors.New("unexpected CreateAttempt call")
}

func (s *retryScannerStore) RecentStatuses(context.Context, string, int) ([]ir.DAGRunStatus, error) {
	return nil, nil
}

func (s *retryScannerStore) LatestAttempt(_ context.Context, query persis.DAGRunLatestAttemptQuery) (dagrun.Attempt, error) {
	s.latestAttemptCalls++
	attempt, ok := s.latestByName[query.Name]
	if !ok {
		return nil, dagrun.ErrDAGRunIDNotFound
	}
	return attempt, nil
}

func (s *retryScannerStore) QueryStatuses(_ context.Context, cfg persis.DAGRunStatusQuery) (persis.DAGRunStatusPage, error) {
	s.listCalls = append(s.listCalls, cfg)

	var ret []*ir.DAGRunStatus
	for _, attempt := range s.attempts {
		status := attempt.status
		if status == nil {
			continue
		}
		if cfg.ExactName != "" && status.Name != cfg.ExactName {
			continue
		}
		if len(cfg.Statuses) > 0 && !containsStatus(cfg.Statuses, status.Status) {
			continue
		}
		ret = append(ret, cloneRetryStatus(status))
	}
	return persis.DAGRunStatusPage{Items: ret}, nil
}

func (s *retryScannerStore) CompareAndSwapLatestAttemptStatus(
	_ context.Context,
	req persis.DAGRunCompareAndSwapStatusRequest,
) (*ir.DAGRunStatus, bool, error) {
	attempt, ok := s.attempts[req.DAGRun.String()]
	if !ok {
		return nil, false, nil
	}
	current := cloneRetryStatus(attempt.status)
	if current.AttemptID != req.ExpectedAttemptID || current.Status != req.ExpectedStatus {
		return current, false, nil
	}
	if err := req.Mutate(current); err != nil {
		return nil, false, err
	}
	attempt.status = cloneRetryStatus(current)
	return cloneRetryStatus(attempt.status), true, nil
}

func (s *retryScannerStore) FindAttempt(_ context.Context, dagRun ir.DAGRunRef) (dagrun.Attempt, error) {
	s.findAttemptCalls++
	attempt, ok := s.attempts[dagRun.String()]
	if !ok {
		return nil, dagrun.ErrDAGRunIDNotFound
	}
	return attempt, nil
}

func (s *retryScannerStore) mustStatus(ref ir.DAGRunRef) *ir.DAGRunStatus {
	attempt, ok := s.attempts[ref.String()]
	if !ok {
		return nil
	}
	return cloneRetryStatus(attempt.status)
}

type retryScannerAttempt struct {
	id     string
	status *ir.DAGRunStatus
	dag    *ir.DAG
}

func (a *retryScannerAttempt) ID() string { return a.id }
func (a *retryScannerAttempt) Open(context.Context) error {
	return errors.New("unexpected Open call")
}
func (a *retryScannerAttempt) Write(context.Context, ir.DAGRunStatus) error {
	return errors.New("unexpected Write call")
}
func (a *retryScannerAttempt) Close(context.Context) error { return nil }
func (a *retryScannerAttempt) ReadStatus(context.Context) (*ir.DAGRunStatus, error) {
	return cloneRetryStatus(a.status), nil
}
func (a *retryScannerAttempt) ReadStatusUncached(ctx context.Context) (*ir.DAGRunStatus, error) {
	return a.ReadStatus(ctx)
}
func (a *retryScannerAttempt) ReadDAG(context.Context) (*ir.DAG, error) { return a.dag, nil }
func (a *retryScannerAttempt) SetDAG(*ir.DAG)                           {}
func (a *retryScannerAttempt) Abort(context.Context) error              { return nil }
func (a *retryScannerAttempt) IsAborting(context.Context) (bool, error) { return false, nil }
func (a *retryScannerAttempt) Hide(context.Context) error               { return nil }
func (a *retryScannerAttempt) Hidden() bool                             { return false }
func (a *retryScannerAttempt) WriteOutputs(context.Context, *ir.DAGRunOutputs) error {
	return nil
}
func (a *retryScannerAttempt) ReadOutputs(context.Context) (*ir.DAGRunOutputs, error) {
	return nil, nil
}
func (a *retryScannerAttempt) WriteStepMessages(context.Context, string, []ir.LLMMessage) error {
	return nil
}
func (a *retryScannerAttempt) ReadStepMessages(context.Context, string) ([]ir.LLMMessage, error) {
	return nil, nil
}
func cloneRetryStatus(status *ir.DAGRunStatus) *ir.DAGRunStatus {
	if status == nil {
		return nil
	}
	cloned := *status
	if status.Nodes != nil {
		cloned.Nodes = append([]*ir.Node(nil), status.Nodes...)
	}
	return &cloned
}

func containsStatus(statuses []ir.Status, want ir.Status) bool {
	return slices.Contains(statuses, want)
}

func withAutoRetryCount(status *ir.DAGRunStatus, retryCount int) *ir.DAGRunStatus {
	cloned := cloneRetryStatus(status)
	cloned.AutoRetryCount = retryCount
	return cloned
}

func withFinishedAt(status *ir.DAGRunStatus, finishedAt string) *ir.DAGRunStatus {
	cloned := cloneRetryStatus(status)
	cloned.FinishedAt = finishedAt
	return cloned
}

func withCreatedAt(status *ir.DAGRunStatus, createdAt int64) *ir.DAGRunStatus {
	cloned := cloneRetryStatus(status)
	cloned.CreatedAt = createdAt
	return cloned
}

func withStartedAt(status *ir.DAGRunStatus, startedAt string) *ir.DAGRunStatus {
	cloned := cloneRetryStatus(status)
	cloned.StartedAt = startedAt
	return cloned
}

func applyRetrySnapshot(status *ir.DAGRunStatus, dag *ir.DAG) {
	if status == nil || dag == nil {
		return
	}
	status.ProcGroup = dag.ProcGroup()
	status.DefinitionID = dag.SuspendFlagName()
	if dag.RetryPolicy != nil {
		status.AutoRetryLimit = dag.RetryPolicy.Limit
		status.AutoRetryInterval = dag.RetryPolicy.Interval
		status.AutoRetryBackoff = dag.RetryPolicy.Backoff
		status.AutoRetryMaxInterval = dag.RetryPolicy.MaxInterval
	}
}

func mustRetryMetadataFromDAG(t *testing.T, dag *ir.DAG) dagRetryMetadata {
	t.Helper()
	metadata, ok := retryMetadataFromDAG(dag)
	require.True(t, ok)
	return metadata
}
