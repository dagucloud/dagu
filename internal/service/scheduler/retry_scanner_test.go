// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestRetryScannerEvaluateRetryDecision(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 14, 14, 0, 0, 0, time.UTC)
	baseDAG := &core.DAG{
		Name:        "retry-dag",
		RetryPolicy: &core.DAGRetryPolicy{Limit: 3, Interval: time.Minute, Backoff: 0, MaxInterval: 10 * time.Minute},
	}
	baseStatus := &exec.DAGRunStatus{
		Name:           "retry-dag",
		DAGRunID:       "run-1",
		AttemptID:      "att-1",
		Status:         core.Failed,
		AutoRetryCount: 0,
		FinishedAt:     now.Add(-2 * time.Minute).Format(time.RFC3339),
		ScheduleTime:   now.Add(-10 * time.Minute).Format(time.RFC3339),
	}

	tests := []struct {
		name      string
		status    *exec.DAGRunStatus
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

func TestDAGSuspendFlagName(t *testing.T) {
	t.Parallel()

	t.Run("UsesFilenameStem", func(t *testing.T) {
		t.Parallel()

		got := dagSuspendFlagName(&core.DAG{
			Name:     "logical-name",
			Location: "/tmp/example-dag.yaml",
		})

		assert.Equal(t, "example-dag", got)
	})

	t.Run("FallsBackToDAGNameWhenLocationMissing", func(t *testing.T) {
		t.Parallel()

		got := dagSuspendFlagName(&core.DAG{
			Name: "logical-name",
		})

		assert.Equal(t, "logical-name", got)
	})
}

func TestRetryScannerScanEnqueuesRetry(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 14, 14, 0, 0, 0, time.UTC)
	dag := &core.DAG{
		Name:     "retry-dag",
		Location: "/tmp/retry-dag.yaml",
		RetryPolicy: &core.DAGRetryPolicy{
			Limit:       3,
			Interval:    time.Minute,
			Backoff:     0,
			MaxInterval: 10 * time.Minute,
		},
	}
	status := &exec.DAGRunStatus{
		Name:           dag.Name,
		DAGRunID:       "run-1",
		AttemptID:      "att-1",
		Status:         core.Failed,
		AutoRetryCount: 1,
		FinishedAt:     now.Add(-3 * time.Minute).Format(time.RFC3339),
		ScheduleTime:   now.Add(-10 * time.Minute).Format(time.RFC3339),
	}
	store := newRetryScannerStore(dag, status)
	queueStore := &exec.MockQueueStore{}
	queueStore.On("Enqueue", mock.Anything, dag.ProcGroup(), exec.QueuePriorityLow, status.DAGRun()).
		Return(nil).
		Once()

	scanner, err := NewRetryScanner(
		store,
		queueStore,
		nil,
		24*time.Hour,
		func() time.Time { return now },
	)
	require.NoError(t, err)

	err = scanner.scan(context.Background())
	require.NoError(t, err)

	latest := store.mustStatus(status.DAGRun())
	assert.Equal(t, core.Queued, latest.Status)
	assert.Equal(t, core.TriggerTypeRetry, latest.TriggerType)
	assert.NotEmpty(t, latest.QueuedAt)
	assert.Equal(t, 2, latest.AutoRetryCount)
	assert.Equal(t, 0, store.latestAttemptCalls)
	assert.Len(t, store.listCalls, 1)
	assert.Equal(t, 0, store.findAttemptCalls)

	queueStore.AssertExpectations(t)
}

func TestRetryScannerScanSkipsDisabledRetryPolicy(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 14, 14, 0, 0, 0, time.UTC)
	dag := &core.DAG{
		Name:     "retry-disabled-dag",
		Location: "/tmp/retry-disabled-dag.yaml",
		RetryPolicy: &core.DAGRetryPolicy{
			Limit:       0,
			Interval:    time.Minute,
			Backoff:     0,
			MaxInterval: 10 * time.Minute,
		},
	}
	status := &exec.DAGRunStatus{
		Name:           dag.Name,
		DAGRunID:       "run-1",
		AttemptID:      "att-1",
		Status:         core.Failed,
		AutoRetryCount: 0,
		FinishedAt:     now.Add(-3 * time.Minute).Format(time.RFC3339),
		ScheduleTime:   now.Add(-10 * time.Minute).Format(time.RFC3339),
	}
	store := newRetryScannerStore(dag, status)
	queueStore := &exec.MockQueueStore{}

	scanner, err := NewRetryScanner(
		store,
		queueStore,
		nil,
		24*time.Hour,
		func() time.Time { return now },
	)
	require.NoError(t, err)

	err = scanner.scan(context.Background())
	require.NoError(t, err)

	latest := store.mustStatus(status.DAGRun())
	assert.Equal(t, core.Failed, latest.Status)
	assert.Equal(t, 0, latest.AutoRetryCount)
	assert.Equal(t, 0, latest.AutoRetryLimit)
	assert.Equal(t, 0, store.latestAttemptCalls)
	assert.Len(t, store.listCalls, 1)
	assert.Equal(t, 0, store.findAttemptCalls)
	queueStore.AssertNotCalled(t, "Enqueue", mock.Anything, dag.ProcGroup(), exec.QueuePriorityLow, status.DAGRun())
}

func TestRetryScannerScanEnqueuesRetryWithoutLiveTargets(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 14, 14, 0, 0, 0, time.UTC)
	dag := &core.DAG{
		Name:     "retry-dag",
		Location: "/tmp/retry-dag.yaml",
		RetryPolicy: &core.DAGRetryPolicy{
			Limit:       3,
			Interval:    time.Minute,
			Backoff:     0,
			MaxInterval: 10 * time.Minute,
		},
	}
	status := &exec.DAGRunStatus{
		Name:           dag.Name,
		DAGRunID:       "run-1",
		AttemptID:      "att-1",
		Status:         core.Failed,
		AutoRetryCount: 0,
		FinishedAt:     now.Add(-2 * time.Minute).Format(time.RFC3339),
		ScheduleTime:   now.Add(-10 * time.Minute).Format(time.RFC3339),
	}
	store := newRetryScannerStore(dag, status)
	queueStore := &exec.MockQueueStore{}
	queueStore.On("Enqueue", mock.Anything, dag.ProcGroup(), exec.QueuePriorityLow, status.DAGRun()).
		Return(nil).
		Once()

	scanner, err := NewRetryScanner(
		store,
		queueStore,
		nil,
		24*time.Hour,
		func() time.Time { return now },
	)
	require.NoError(t, err)

	err = scanner.scan(context.Background())
	require.NoError(t, err)
	assert.Equal(t, core.Queued, store.mustStatus(status.DAGRun()).Status)
	assert.Len(t, store.listCalls, 1)
	assert.Equal(t, 0, store.findAttemptCalls)
	queueStore.AssertExpectations(t)
}

func TestRetryScannerScanRetriesOlderFailedRunEvenWhenNewerRunExists(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 15, 0, 10, 0, 0, time.UTC)
	dag := &core.DAG{
		Name:     "retry-dag",
		Location: "/tmp/retry-dag.yaml",
		RetryPolicy: &core.DAGRetryPolicy{
			Limit:       3,
			Interval:    time.Minute,
			Backoff:     0,
			MaxInterval: 10 * time.Minute,
		},
	}
	failed := &exec.DAGRunStatus{
		Name:           dag.Name,
		DAGRunID:       "run-1",
		AttemptID:      "att-1",
		Status:         core.Failed,
		AutoRetryCount: 0,
		FinishedAt:     time.Date(2026, 3, 15, 0, 2, 0, 0, time.UTC).Format(time.RFC3339),
		ScheduleTime:   time.Date(2026, 3, 14, 23, 50, 0, 0, time.UTC).Format(time.RFC3339),
	}
	active := &exec.DAGRunStatus{
		Name:         dag.Name,
		DAGRunID:     "run-2",
		AttemptID:    "att-2",
		Status:       core.Running,
		ScheduleTime: time.Date(2026, 3, 14, 23, 59, 0, 0, time.UTC).Format(time.RFC3339),
	}

	store := newRetryScannerStore(dag, failed, active)
	queueStore := &exec.MockQueueStore{}
	queueStore.On("Enqueue", mock.Anything, dag.ProcGroup(), exec.QueuePriorityLow, failed.DAGRun()).
		Return(nil).
		Once()

	scanner, err := NewRetryScanner(
		store,
		queueStore,
		nil,
		24*time.Hour,
		func() time.Time { return now },
	)
	require.NoError(t, err)

	err = scanner.scan(context.Background())
	require.NoError(t, err)

	assert.Equal(t, core.Queued, store.mustStatus(failed.DAGRun()).Status)
	assert.Equal(t, core.Running, store.mustStatus(active.DAGRun()).Status)
	assert.Equal(t, 0, store.latestAttemptCalls)
	assert.Len(t, store.listCalls, 1)
	assert.Equal(t, 0, store.findAttemptCalls)
	queueStore.AssertExpectations(t)
}

func TestRetryScannerScanUsesPersistedRetryPolicy(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 14, 14, 0, 0, 0, time.UTC)
	retryDAG := &core.DAG{
		Name:     "retry-dag",
		Location: "/tmp/retry-dag.yaml",
		RetryPolicy: &core.DAGRetryPolicy{
			Limit:       3,
			Interval:    time.Minute,
			Backoff:     0,
			MaxInterval: 10 * time.Minute,
		},
	}
	noRetryDAG := &core.DAG{Name: "plain-dag", Location: "/tmp/plain-dag.yaml"}
	retryStatus := &exec.DAGRunStatus{
		Name:           retryDAG.Name,
		DAGRunID:       "run-1",
		AttemptID:      "att-1",
		Status:         core.Failed,
		AutoRetryCount: 0,
		FinishedAt:     now.Add(-2 * time.Minute).Format(time.RFC3339),
		ScheduleTime:   now.Add(-10 * time.Minute).Format(time.RFC3339),
	}
	plainStatus := &exec.DAGRunStatus{
		Name:           noRetryDAG.Name,
		DAGRunID:       "run-2",
		AttemptID:      "att-2",
		Status:         core.Failed,
		AutoRetryCount: 0,
		FinishedAt:     now.Add(-2 * time.Minute).Format(time.RFC3339),
		ScheduleTime:   now.Add(-10 * time.Minute).Format(time.RFC3339),
	}
	store := newRetryScannerStoreWithEntries(
		retryScannerStoreEntry{dag: retryDAG, status: retryStatus},
		retryScannerStoreEntry{dag: noRetryDAG, status: plainStatus},
	)
	queueStore := &exec.MockQueueStore{}
	queueStore.On("Enqueue", mock.Anything, retryDAG.ProcGroup(), exec.QueuePriorityLow, retryStatus.DAGRun()).
		Return(nil).
		Once()

	scanner, err := NewRetryScanner(
		store,
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
	assert.Equal(t, core.Failed, store.mustStatus(plainStatus.DAGRun()).Status)
	assert.Equal(t, 0, store.findAttemptCalls)
	queueStore.AssertExpectations(t)
}

func TestRetryScannerScanSkipsSuspendedPersistedRetries(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 14, 14, 0, 0, 0, time.UTC)
	dag := &core.DAG{
		Name:     "retry-dag",
		Location: "/tmp/retry-dag.yaml",
		RetryPolicy: &core.DAGRetryPolicy{
			Limit:       3,
			Interval:    time.Minute,
			Backoff:     0,
			MaxInterval: 10 * time.Minute,
		},
	}
	status := &exec.DAGRunStatus{
		Name:           dag.Name,
		DAGRunID:       "run-1",
		AttemptID:      "att-1",
		Status:         core.Failed,
		AutoRetryCount: 0,
		FinishedAt:     now.Add(-2 * time.Minute).Format(time.RFC3339),
		ScheduleTime:   now.Add(-10 * time.Minute).Format(time.RFC3339),
	}
	store := newRetryScannerStore(dag, status)
	suspendedFlag := dag.SuspendFlagName()
	require.NotEmpty(t, suspendedFlag)

	scanner, err := NewRetryScanner(
		store,
		&exec.MockQueueStore{},
		func(_ context.Context, name string) bool { return name == suspendedFlag },
		24*time.Hour,
		func() time.Time { return now },
	)
	require.NoError(t, err)

	require.NoError(t, scanner.scan(context.Background()))

	assert.Equal(t, core.Failed, store.mustStatus(status.DAGRun()).Status)
	assert.Equal(t, 0, store.mustStatus(status.DAGRun()).AutoRetryCount)
	assert.Equal(t, 0, store.latestAttemptCalls)
	assert.Len(t, store.listCalls, 1)
	assert.Equal(t, 0, store.findAttemptCalls)
}

func TestRetryScannerScanSkipsSuspendedLegacyStatuses(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 14, 14, 0, 0, 0, time.UTC)
	dag := &core.DAG{
		Name:     "retry-dag",
		Location: "/tmp/retry-dag.yaml",
		RetryPolicy: &core.DAGRetryPolicy{
			Limit:       3,
			Interval:    time.Minute,
			Backoff:     0,
			MaxInterval: 10 * time.Minute,
		},
	}
	status := &exec.DAGRunStatus{
		Name:           dag.Name,
		DAGRunID:       "run-legacy",
		AttemptID:      "att-legacy",
		Status:         core.Failed,
		AutoRetryCount: 0,
		FinishedAt:     now.Add(-2 * time.Minute).Format(time.RFC3339),
		ScheduleTime:   now.Add(-10 * time.Minute).Format(time.RFC3339),
	}
	store := newRetryScannerStoreWithEntries(retryScannerStoreEntry{dag: dag, status: status})
	legacy := store.attempts[status.DAGRun().String()]
	require.NotNil(t, legacy)
	legacy.status.ProcGroup = ""
	legacy.status.SuspendFlagName = ""
	legacy.status.AutoRetryLimit = 0
	legacy.status.AutoRetryInterval = 0
	legacy.status.AutoRetryBackoff = 0
	legacy.status.AutoRetryMaxInterval = 0

	suspendedFlag := dag.SuspendFlagName()
	require.NotEmpty(t, suspendedFlag)

	scanner, err := NewRetryScanner(
		store,
		&exec.MockQueueStore{},
		func(_ context.Context, name string) bool { return name == suspendedFlag },
		24*time.Hour,
		func() time.Time { return now },
	)
	require.NoError(t, err)

	require.NoError(t, scanner.scan(context.Background()))

	assert.Equal(t, 1, store.findAttemptCalls)
	assert.Equal(t, 0, store.latestAttemptCalls)
	assert.Len(t, store.listCalls, 1)
	assert.Equal(t, core.Failed, store.mustStatus(status.DAGRun()).Status)
}

func TestRetryScannerScanFallsBackToDAGNameWhenSuspendSnapshotMissing(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 14, 14, 0, 0, 0, time.UTC)
	dag := &core.DAG{
		Name:     "retry-dag",
		Location: "/tmp/retry-dag.yaml",
		RetryPolicy: &core.DAGRetryPolicy{
			Limit:       3,
			Interval:    time.Minute,
			Backoff:     0,
			MaxInterval: 10 * time.Minute,
		},
	}
	status := &exec.DAGRunStatus{
		Name:           dag.Name,
		DAGRunID:       "run-1",
		AttemptID:      "att-1",
		Status:         core.Failed,
		AutoRetryCount: 0,
		FinishedAt:     now.Add(-2 * time.Minute).Format(time.RFC3339),
		ScheduleTime:   now.Add(-10 * time.Minute).Format(time.RFC3339),
	}
	store := newRetryScannerStore(dag, status)
	store.attempts[status.DAGRun().String()].status.SuspendFlagName = ""

	var checked string
	scanner, err := NewRetryScanner(
		store,
		&exec.MockQueueStore{},
		func(_ context.Context, name string) bool {
			checked = name
			return name == dag.Name
		},
		24*time.Hour,
		func() time.Time { return now },
	)
	require.NoError(t, err)

	require.NoError(t, scanner.scan(context.Background()))

	assert.Equal(t, dag.Name, checked)
	assert.Equal(t, core.Failed, store.mustStatus(status.DAGRun()).Status)
	assert.Equal(t, 0, store.findAttemptCalls)
}

func TestRetryScannerScanIsIdempotentForQueuedRun(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 14, 14, 0, 0, 0, time.UTC)
	dag := &core.DAG{
		Name:     "retry-dag",
		Location: "/tmp/retry-dag.yaml",
		RetryPolicy: &core.DAGRetryPolicy{
			Limit:       3,
			Interval:    time.Minute,
			Backoff:     0,
			MaxInterval: 10 * time.Minute,
		},
	}
	status := &exec.DAGRunStatus{
		Name:           dag.Name,
		DAGRunID:       "run-1",
		AttemptID:      "att-1",
		Status:         core.Failed,
		AutoRetryCount: 0,
		FinishedAt:     now.Add(-2 * time.Minute).Format(time.RFC3339),
		ScheduleTime:   now.Add(-10 * time.Minute).Format(time.RFC3339),
	}
	store := newRetryScannerStore(dag, status)
	queueStore := &exec.MockQueueStore{}
	queueStore.On("Enqueue", mock.Anything, dag.ProcGroup(), exec.QueuePriorityLow, status.DAGRun()).
		Return(nil).
		Once()

	scanner, err := NewRetryScanner(
		store,
		queueStore,
		nil,
		24*time.Hour,
		func() time.Time { return now },
	)
	require.NoError(t, err)

	require.NoError(t, scanner.scan(context.Background()))
	require.NoError(t, scanner.scan(context.Background()))

	assert.Equal(t, core.Queued, store.mustStatus(status.DAGRun()).Status)
	assert.Equal(t, 0, store.latestAttemptCalls)
	assert.Len(t, store.listCalls, 2)
	assert.Equal(t, 0, store.findAttemptCalls)
	queueStore.AssertExpectations(t)
}

type retryScannerStore struct {
	attempts           map[string]*retryScannerAttempt
	latestByName       map[string]*retryScannerAttempt
	latestAttemptCalls int
	listCalls          []exec.ListDAGRunStatusesOptions
	findAttemptCalls   int
}

type retryScannerStoreEntry struct {
	dag    *core.DAG
	status *exec.DAGRunStatus
}

func newRetryScannerStore(dag *core.DAG, statuses ...*exec.DAGRunStatus) *retryScannerStore {
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

func (s *retryScannerStore) CreateAttempt(context.Context, *core.DAG, time.Time, string, exec.NewDAGRunAttemptOptions) (exec.DAGRunAttempt, error) {
	return nil, errors.New("unexpected CreateAttempt call")
}

func (s *retryScannerStore) RecentAttempts(context.Context, string, int) []exec.DAGRunAttempt {
	return nil
}

func (s *retryScannerStore) LatestAttempt(_ context.Context, name string) (exec.DAGRunAttempt, error) {
	s.latestAttemptCalls++
	attempt, ok := s.latestByName[name]
	if !ok {
		return nil, exec.ErrDAGRunIDNotFound
	}
	return attempt, nil
}

func (s *retryScannerStore) ListStatuses(_ context.Context, opts ...exec.ListDAGRunStatusesOption) ([]*exec.DAGRunStatus, error) {
	var cfg exec.ListDAGRunStatusesOptions
	for _, opt := range opts {
		opt(&cfg)
	}
	s.listCalls = append(s.listCalls, cfg)

	var ret []*exec.DAGRunStatus
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
	return ret, nil
}

func (s *retryScannerStore) ListStatusesPage(ctx context.Context, opts ...exec.ListDAGRunStatusesOption) (exec.DAGRunStatusPage, error) {
	items, err := s.ListStatuses(ctx, opts...)
	if err != nil {
		return exec.DAGRunStatusPage{}, err
	}
	return exec.DAGRunStatusPage{Items: items}, nil
}

func (s *retryScannerStore) CompareAndSwapLatestAttemptStatus(
	_ context.Context,
	dagRun exec.DAGRunRef,
	expectedAttemptID string,
	expectedStatus core.Status,
	mutate func(*exec.DAGRunStatus) error,
	_ ...exec.CompareAndSwapStatusOption,
) (*exec.DAGRunStatus, bool, error) {
	attempt, ok := s.attempts[dagRun.String()]
	if !ok {
		return nil, false, nil
	}
	current := cloneRetryStatus(attempt.status)
	if current.AttemptID != expectedAttemptID || current.Status != expectedStatus {
		return current, false, nil
	}
	if err := mutate(current); err != nil {
		return nil, false, err
	}
	attempt.status = cloneRetryStatus(current)
	return cloneRetryStatus(attempt.status), true, nil
}

func (s *retryScannerStore) FindAttempt(_ context.Context, dagRun exec.DAGRunRef) (exec.DAGRunAttempt, error) {
	s.findAttemptCalls++
	attempt, ok := s.attempts[dagRun.String()]
	if !ok {
		return nil, exec.ErrDAGRunIDNotFound
	}
	return attempt, nil
}

func (s *retryScannerStore) FindSubAttempt(context.Context, exec.DAGRunRef, string) (exec.DAGRunAttempt, error) {
	return nil, errors.New("unexpected FindSubAttempt call")
}

func (s *retryScannerStore) CreateSubAttempt(context.Context, exec.DAGRunRef, string) (exec.DAGRunAttempt, error) {
	return nil, errors.New("unexpected CreateSubAttempt call")
}

func (s *retryScannerStore) RemoveOldDAGRuns(context.Context, string, int, ...exec.RemoveOldDAGRunsOption) ([]string, error) {
	return nil, nil
}

func (s *retryScannerStore) RenameDAGRuns(context.Context, string, string) error { return nil }
func (s *retryScannerStore) RemoveDAGRun(context.Context, exec.DAGRunRef, ...exec.RemoveDAGRunOption) error {
	return nil
}

func (s *retryScannerStore) mustStatus(ref exec.DAGRunRef) *exec.DAGRunStatus {
	attempt, ok := s.attempts[ref.String()]
	if !ok {
		return nil
	}
	return cloneRetryStatus(attempt.status)
}

type retryScannerAttempt struct {
	id     string
	status *exec.DAGRunStatus
	dag    *core.DAG
}

func (a *retryScannerAttempt) ID() string { return a.id }
func (a *retryScannerAttempt) Open(context.Context) error {
	return errors.New("unexpected Open call")
}
func (a *retryScannerAttempt) Write(context.Context, exec.DAGRunStatus) error {
	return errors.New("unexpected Write call")
}
func (a *retryScannerAttempt) Close(context.Context) error { return nil }
func (a *retryScannerAttempt) ReadStatus(context.Context) (*exec.DAGRunStatus, error) {
	return cloneRetryStatus(a.status), nil
}
func (a *retryScannerAttempt) ReadDAG(context.Context) (*core.DAG, error) { return a.dag, nil }
func (a *retryScannerAttempt) SetDAG(*core.DAG)                           {}
func (a *retryScannerAttempt) Abort(context.Context) error                { return nil }
func (a *retryScannerAttempt) IsAborting(context.Context) (bool, error)   { return false, nil }
func (a *retryScannerAttempt) Hide(context.Context) error                 { return nil }
func (a *retryScannerAttempt) Hidden() bool                               { return false }
func (a *retryScannerAttempt) WriteOutputs(context.Context, *exec.DAGRunOutputs) error {
	return nil
}
func (a *retryScannerAttempt) ReadOutputs(context.Context) (*exec.DAGRunOutputs, error) {
	return nil, nil
}
func (a *retryScannerAttempt) WriteStepMessages(context.Context, string, []exec.LLMMessage) error {
	return nil
}
func (a *retryScannerAttempt) ReadStepMessages(context.Context, string) ([]exec.LLMMessage, error) {
	return nil, nil
}
func (a *retryScannerAttempt) WorkDir() string { return "" }

func cloneRetryStatus(status *exec.DAGRunStatus) *exec.DAGRunStatus {
	if status == nil {
		return nil
	}
	cloned := *status
	if status.Nodes != nil {
		cloned.Nodes = append([]*exec.Node(nil), status.Nodes...)
	}
	return &cloned
}

func containsStatus(statuses []core.Status, want core.Status) bool {
	return slices.Contains(statuses, want)
}

func withAutoRetryCount(status *exec.DAGRunStatus, retryCount int) *exec.DAGRunStatus {
	cloned := cloneRetryStatus(status)
	cloned.AutoRetryCount = retryCount
	return cloned
}

func withFinishedAt(status *exec.DAGRunStatus, finishedAt string) *exec.DAGRunStatus {
	cloned := cloneRetryStatus(status)
	cloned.FinishedAt = finishedAt
	return cloned
}

func withCreatedAt(status *exec.DAGRunStatus, createdAt int64) *exec.DAGRunStatus {
	cloned := cloneRetryStatus(status)
	cloned.CreatedAt = createdAt
	return cloned
}

func withStartedAt(status *exec.DAGRunStatus, startedAt string) *exec.DAGRunStatus {
	cloned := cloneRetryStatus(status)
	cloned.StartedAt = startedAt
	return cloned
}

func applyRetrySnapshot(status *exec.DAGRunStatus, dag *core.DAG) {
	if status == nil || dag == nil {
		return
	}
	status.ProcGroup = dag.ProcGroup()
	status.SuspendFlagName = dag.SuspendFlagName()
	if dag.RetryPolicy != nil {
		status.AutoRetryLimit = dag.RetryPolicy.Limit
		status.AutoRetryInterval = dag.RetryPolicy.Interval
		status.AutoRetryBackoff = dag.RetryPolicy.Backoff
		status.AutoRetryMaxInterval = dag.RetryPolicy.MaxInterval
	}
}

func mustRetryMetadataFromDAG(t *testing.T, dag *core.DAG) dagRetryMetadata {
	t.Helper()
	metadata, ok := retryMetadataFromDAG(dag)
	require.True(t, ok)
	return metadata
}
