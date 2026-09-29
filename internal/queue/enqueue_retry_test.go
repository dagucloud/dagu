// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package queue_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestEnqueueRetry(t *testing.T) {
	t.Parallel()

	triggerActor := "alice"
	tests := []struct {
		name          string
		dag           *ir.DAG
		status        *ir.DAGRunStatus
		opts          queue.EnqueueRetryOptions
		backend       *stubDAGRunStore
		setupQueue    func(qs *testutil.MockQueueStore)
		assertErr     func(t *testing.T, err error)
		assertBackend func(t *testing.T, backend *stubDAGRunStore)
		wantQueued    bool
		wantErr       string
	}{
		{
			name:    "AlreadyQueued",
			dag:     &ir.DAG{Name: "test-dag"},
			status:  &ir.DAGRunStatus{Status: ir.Queued},
			backend: &stubDAGRunStore{},
			setupQueue: func(qs *testutil.MockQueueStore) {
				qs.AssertNotCalled(t, "Enqueue", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
			},
		},
		{
			name: "SuccessRecordsTriggerActor",
			dag:  &ir.DAG{Name: "test-dag"},
			status: &ir.DAGRunStatus{
				Name:           "test-dag",
				DAGRunID:       "run-1",
				AttemptID:      "att-1",
				Status:         ir.Failed,
				AutoRetryCount: 2,
			},
			opts: queue.EnqueueRetryOptions{TriggerActor: &triggerActor},
			backend: &stubDAGRunStore{
				status: &ir.DAGRunStatus{
					Name:           "test-dag",
					DAGRunID:       "run-1",
					AttemptID:      "att-1",
					Status:         ir.Failed,
					AutoRetryCount: 2,
					Log:            "/tmp/test-dag/run-1.log",
					WorkingDir:     "/tmp/test-dag/run-1",
					ProfileName:    "old-profile",
					TriggerActor:   "bob",
					WorkerID:       "worker-1",
					PID:            1234,
					PIDStartedAt:   5678,
					LeaseAt:        9012,
					ProfileResolvedAt: time.Date(
						2026, 3, 14, 14, 30, 0, 0, time.UTC,
					).Format(time.RFC3339),
				},
			},
			setupQueue: func(qs *testutil.MockQueueStore) {
				qs.On("Enqueue", mock.Anything, "test-dag", queue.QueuePriorityLow, ir.NewDAGRunRef("test-dag", "run-1")).
					Return(nil)
			},
			assertBackend: func(t *testing.T, backend *stubDAGRunStore) {
				require.NotNil(t, backend.status)
				assert.Equal(t, ir.Queued, backend.status.Status)
				assert.Equal(t, ir.TriggerTypeRetry, backend.status.TriggerType)
				assert.Equal(t, "alice", backend.status.TriggerActor)
				assert.NotEmpty(t, backend.status.QueuedAt)
				assert.Empty(t, backend.status.Conditions)
				assert.Equal(t, 2, backend.status.AutoRetryCount)
				assert.Equal(t, "old-profile", backend.status.ProfileName)
				assert.Equal(t, "/tmp/test-dag/run-1.log", backend.status.Log)
				assert.Equal(t, "/tmp/test-dag/run-1", backend.status.WorkingDir)
				assert.NotEmpty(t, backend.status.ProfileResolvedAt)
				assert.Empty(t, backend.status.WorkerID)
				assert.Zero(t, backend.status.PID)
				assert.Zero(t, backend.status.PIDStartedAt)
				assert.Zero(t, backend.status.LeaseAt)
			},
			wantQueued: true,
		},
		{
			name: "AutoRetryIncrementsCount",
			dag:  &ir.DAG{Name: "test-dag"},
			status: &ir.DAGRunStatus{
				Name:           "test-dag",
				DAGRunID:       "run-auto",
				AttemptID:      "att-auto",
				Status:         ir.Failed,
				AutoRetryCount: 2,
			},
			opts: queue.EnqueueRetryOptions{AutoRetry: true},
			backend: &stubDAGRunStore{
				status: &ir.DAGRunStatus{
					Name:           "test-dag",
					DAGRunID:       "run-auto",
					AttemptID:      "att-auto",
					Status:         ir.Failed,
					AutoRetryCount: 2,
					TriggerActor:   "bob",
				},
			},
			setupQueue: func(qs *testutil.MockQueueStore) {
				qs.On("Enqueue", mock.Anything, "test-dag", queue.QueuePriorityLow, ir.NewDAGRunRef("test-dag", "run-auto")).
					Return(nil)
			},
			assertBackend: func(t *testing.T, backend *stubDAGRunStore) {
				require.NotNil(t, backend.status)
				assert.Equal(t, 3, backend.status.AutoRetryCount)
				assert.Equal(t, "bob", backend.status.TriggerActor)
			},
			wantQueued: true,
		},
		{
			name: "UsesPersistedProcGroupWhenDAGIsNil",
			status: &ir.DAGRunStatus{
				Name:           "test-dag",
				DAGRunID:       "run-fast-path",
				AttemptID:      "att-fast-path",
				Status:         ir.Failed,
				AutoRetryCount: 1,
				ProcGroup:      "input-queue",
			},
			backend: &stubDAGRunStore{
				status: &ir.DAGRunStatus{
					Name:           "test-dag",
					DAGRunID:       "run-fast-path",
					AttemptID:      "att-fast-path",
					Status:         ir.Failed,
					AutoRetryCount: 1,
					ProcGroup:      "custom-queue",
					Log:            "/tmp/test-dag/run-fast-path.log",
				},
			},
			setupQueue: func(qs *testutil.MockQueueStore) {
				qs.On("Enqueue", mock.Anything, "custom-queue", queue.QueuePriorityLow, ir.NewDAGRunRef("test-dag", "run-fast-path")).
					Return(nil)
			},
			assertBackend: func(t *testing.T, backend *stubDAGRunStore) {
				require.NotNil(t, backend.status)
				assert.Equal(t, ir.Queued, backend.status.Status)
				assert.Equal(t, "custom-queue", backend.status.ProcGroup)
			},
			wantQueued: true,
		},
		{
			name: "BackfillsMissingRootFromCallerStatus",
			dag:  &ir.DAG{Name: "child-dag"},
			status: &ir.DAGRunStatus{
				Name:      "child-dag",
				DAGRunID:  "run-root",
				AttemptID: "att-root",
				Status:    ir.Failed,
				Root:      ir.NewDAGRunRef("root-dag", "root-run"),
			},
			backend: &stubDAGRunStore{
				status: &ir.DAGRunStatus{
					Name:      "child-dag",
					DAGRunID:  "run-root",
					AttemptID: "att-root",
					Status:    ir.Failed,
				},
			},
			setupQueue: func(qs *testutil.MockQueueStore) {
				qs.On("Enqueue", mock.Anything, "child-dag", queue.QueuePriorityLow, ir.NewDAGRunRef("child-dag", "run-root")).
					Return(nil)
			},
			assertBackend: func(t *testing.T, backend *stubDAGRunStore) {
				require.NotNil(t, backend.status)
				assert.Equal(t, ir.NewDAGRunRef("root-dag", "root-run"), backend.status.Root)
			},
			wantQueued: true,
		},
		{
			name: "PersistQueuedStatusFails",
			dag:  &ir.DAG{Name: "test-dag"},
			status: &ir.DAGRunStatus{
				Name:      "test-dag",
				DAGRunID:  "run-2",
				AttemptID: "att-2",
				Status:    ir.Failed,
			},
			backend: &stubDAGRunStore{
				status:   &ir.DAGRunStatus{Name: "test-dag", DAGRunID: "run-2", AttemptID: "att-2", Status: ir.Failed},
				firstErr: errors.New("cas error"),
			},
			wantErr: "persist queued retry status",
		},
		{
			name: "CompareAndSwapLosesRaceToSameAttemptQueued",
			dag:  &ir.DAG{Name: "test-dag"},
			status: &ir.DAGRunStatus{
				Name:       "test-dag",
				DAGRunID:   "run-3",
				AttemptID:  "att-3",
				AttemptKey: "key-3",
				Status:     ir.Failed,
			},
			backend: &stubDAGRunStore{
				status: &ir.DAGRunStatus{
					Name: "test-dag", DAGRunID: "run-3", AttemptID: "att-3", AttemptKey: "key-3", Status: ir.Queued,
				},
			},
		},
		{
			name: "CompareAndSwapLosesRaceToDifferentLatestStatus",
			dag:  &ir.DAG{Name: "test-dag"},
			status: &ir.DAGRunStatus{
				Name:      "test-dag",
				DAGRunID:  "run-3b",
				AttemptID: "att-3b",
				Status:    ir.Failed,
			},
			backend: &stubDAGRunStore{
				status: &ir.DAGRunStatus{Name: "test-dag", DAGRunID: "run-3b", AttemptID: "att-other", Status: ir.Running},
			},
			assertErr: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, queue.ErrRetryStaleLatest)
			},
		},
		{
			name: "EnqueueFailsAndRollsBack",
			dag:  &ir.DAG{Name: "test-dag"},
			status: &ir.DAGRunStatus{
				Name:           "test-dag",
				DAGRunID:       "run-4",
				AttemptID:      "att-4",
				Status:         ir.Failed,
				AutoRetryCount: 1,
			},
			backend: &stubDAGRunStore{
				status: &ir.DAGRunStatus{
					Name:           "test-dag",
					DAGRunID:       "run-4",
					AttemptID:      "att-4",
					Status:         ir.Failed,
					AutoRetryCount: 1,
					TriggerActor:   "bob",
					WorkerID:       "worker-1",
					PID:            1234,
					PIDStartedAt:   5678,
					LeaseAt:        9012,
				},
			},
			opts: queue.EnqueueRetryOptions{AutoRetry: true, TriggerActor: &triggerActor},
			setupQueue: func(qs *testutil.MockQueueStore) {
				qs.On("Enqueue", mock.Anything, "test-dag", queue.QueuePriorityLow, ir.NewDAGRunRef("test-dag", "run-4")).
					Return(errors.New("enqueue error"))
			},
			assertBackend: func(t *testing.T, backend *stubDAGRunStore) {
				require.NotNil(t, backend.status)
				assert.Equal(t, ir.Failed, backend.status.Status)
				assert.Empty(t, backend.status.QueuedAt)
				assert.Equal(t, ir.TriggerTypeUnknown, backend.status.TriggerType)
				assert.Equal(t, 1, backend.status.AutoRetryCount)
				assert.Equal(t, "bob", backend.status.TriggerActor)
				assert.Equal(t, "worker-1", backend.status.WorkerID)
				assert.Equal(t, ir.PID(1234), backend.status.PID)
				assert.Equal(t, int64(5678), backend.status.PIDStartedAt)
				assert.Equal(t, int64(9012), backend.status.LeaseAt)
			},
			wantErr: "enqueue retry",
		},
		{
			name: "EnqueueAndRollbackFail",
			dag:  &ir.DAG{Name: "test-dag"},
			status: &ir.DAGRunStatus{
				Name:      "test-dag",
				DAGRunID:  "run-rollback-failure",
				AttemptID: "att-rollback-failure",
				Status:    ir.Waiting,
			},
			backend: &stubDAGRunStore{
				status: &ir.DAGRunStatus{
					Name:      "test-dag",
					DAGRunID:  "run-rollback-failure",
					AttemptID: "att-rollback-failure",
					Status:    ir.Waiting,
				},
				secondErr: errors.New("rollback error"),
			},
			setupQueue: func(qs *testutil.MockQueueStore) {
				qs.On("Enqueue", mock.Anything, "test-dag", queue.QueuePriorityLow, ir.NewDAGRunRef("test-dag", "run-rollback-failure")).
					Return(errors.New("enqueue error"))
			},
			assertBackend: func(t *testing.T, backend *stubDAGRunStore) {
				require.NotNil(t, backend.status)
				assert.Equal(t, ir.Queued, backend.status.Status)
			},
			wantErr: "enqueue retry: enqueue error; rollback queued retry status: rollback error",
		},
		{
			name: "EmptyProcGroupRollsBackQueuedStatus",
			status: &ir.DAGRunStatus{
				DAGRunID:       "run-empty-group",
				AttemptID:      "att-empty-group",
				Status:         ir.Failed,
				AutoRetryCount: 1,
			},
			backend: &stubDAGRunStore{
				status: &ir.DAGRunStatus{
					DAGRunID:       "run-empty-group",
					AttemptID:      "att-empty-group",
					Status:         ir.Failed,
					AutoRetryCount: 1,
					TriggerActor:   "bob",
				},
			},
			opts: queue.EnqueueRetryOptions{TriggerActor: &triggerActor},
			assertBackend: func(t *testing.T, backend *stubDAGRunStore) {
				require.NotNil(t, backend.status)
				assert.Equal(t, ir.Failed, backend.status.Status)
				assert.Empty(t, backend.status.QueuedAt)
				assert.Equal(t, ir.TriggerTypeUnknown, backend.status.TriggerType)
				assert.Equal(t, 1, backend.status.AutoRetryCount)
				assert.Equal(t, "bob", backend.status.TriggerActor)
			},
			wantErr: "proc group is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			qs := &testutil.MockQueueStore{}
			if tt.setupQueue != nil {
				tt.setupQueue(qs)
			}

			repository := persis.NewDAGRunRepository(tt.backend, nil, persis.DAGRunRepositoryOptions{})
			queued, err := queue.EnqueueRetry(ctx, repository, qs, tt.dag, tt.status, tt.opts)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				if tt.assertErr != nil {
					require.Error(t, err)
					tt.assertErr(t, err)
				} else {
					require.NoError(t, err)
				}
			}
			assert.Equal(t, tt.wantQueued, queued)

			if tt.assertBackend != nil {
				tt.assertBackend(t, tt.backend)
			}
			qs.AssertExpectations(t)
		})
	}
}

func TestEnqueueRetryRejectsUnconfirmedSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		processes queue.RunProcesses
	}{
		{
			name:      "LivenessUnavailable",
			processes: retryRunProcesses{err: errors.New("proc store unavailable")},
		},
		{
			name:      "SourceStillAlive",
			processes: retryRunProcesses{alive: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status := &ir.DAGRunStatus{
				Name:      "test-dag",
				DAGRunID:  "run-source",
				AttemptID: "att-source",
				Status:    ir.Failed,
				ProcGroup: "test-queue",
			}
			backend := &stubDAGRunStore{status: cloneDAGRunStatus(status)}
			queueStore := &recordingRetryQueueStore{}
			repository := persis.NewDAGRunRepository(backend, nil, persis.DAGRunRepositoryOptions{})
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
			defer cancel()

			queued, err := queue.EnqueueRetry(
				ctx,
				repository,
				queueStore,
				&ir.DAG{Name: "test-dag", Queue: "test-queue"},
				status,
				queue.EnqueueRetryOptions{Processes: tt.processes},
			)

			require.Error(t, err)
			assert.False(t, queued)
			assert.Equal(t, ir.Failed, backend.status.Status)
			assert.Zero(t, queueStore.enqueueCalls)
		})
	}
}

func TestEnqueueRetrySourceReleasePolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		autoRetry  bool
		wantQueued bool
		wantStatus ir.Status
		wantCalls  int
	}{
		{
			name:       "AutoRetryProbesOnce",
			autoRetry:  true,
			wantStatus: ir.Failed,
			wantCalls:  1,
		},
		{
			name:       "UserRetryWaits",
			wantQueued: true,
			wantStatus: ir.Queued,
			wantCalls:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status := &ir.DAGRunStatus{
				Name:      "test-dag",
				DAGRunID:  "run-source",
				AttemptID: "att-source",
				Status:    ir.Failed,
				ProcGroup: "test-queue",
			}
			backend := &stubDAGRunStore{status: cloneDAGRunStatus(status)}
			queueStore := &recordingRetryQueueStore{}
			processes := &sequenceRetryRunProcesses{alive: []bool{true, false}}
			repository := persis.NewDAGRunRepository(backend, nil, persis.DAGRunRepositoryOptions{})

			queued, err := queue.EnqueueRetry(
				t.Context(),
				repository,
				queueStore,
				&ir.DAG{Name: "test-dag", Queue: "test-queue"},
				status,
				queue.EnqueueRetryOptions{AutoRetry: tt.autoRetry, Processes: processes},
			)

			require.NoError(t, err)
			assert.Equal(t, tt.wantQueued, queued)
			assert.Equal(t, tt.wantStatus, backend.status.Status)
			assert.Equal(t, tt.wantCalls, processes.calls)
			if tt.wantQueued {
				assert.Equal(t, 1, queueStore.enqueueCalls)
			} else {
				assert.Zero(t, queueStore.enqueueCalls)
			}
		})
	}
}

type retryRunProcesses struct {
	alive bool
	err   error
}

func (p retryRunProcesses) IsAttemptAlive(context.Context, string, ir.DAGRunRef, string) (bool, error) {
	return p.alive, p.err
}

type sequenceRetryRunProcesses struct {
	alive []bool
	calls int
}

func (p *sequenceRetryRunProcesses) IsAttemptAlive(context.Context, string, ir.DAGRunRef, string) (bool, error) {
	alive := p.alive[min(p.calls, len(p.alive)-1)]
	p.calls++
	return alive, nil
}

type recordingRetryQueueStore struct {
	queue.QueueStore
	enqueueCalls int
}

func (s *recordingRetryQueueStore) Enqueue(context.Context, string, queue.QueuePriority, ir.DAGRunRef) error {
	s.enqueueCalls++
	return nil
}

type stubDAGRunStore struct {
	testutil.DAGRunStoreStub
	status    *ir.DAGRunStatus
	firstErr  error
	secondErr error
	casCalls  int
}

func (s *stubDAGRunStore) CompareAndSwapLatestAttemptStatus(
	_ context.Context,
	req persis.DAGRunCompareAndSwapStatusRequest,
) (*ir.DAGRunStatus, bool, error) {
	s.casCalls++
	if s.casCalls == 1 && s.firstErr != nil {
		return nil, false, s.firstErr
	}
	if s.casCalls == 2 && s.secondErr != nil {
		return nil, false, s.secondErr
	}

	if s.status == nil {
		return nil, false, nil
	}

	if req.ExpectedAttemptID != s.status.AttemptID || req.ExpectedStatus != s.status.Status {
		return s.cloneStatus(), false, nil
	}

	updated := s.cloneStatus()
	if err := req.Mutate(updated); err != nil {
		return nil, false, err
	}
	s.status = updated
	return s.cloneStatus(), true, nil
}

func (s *stubDAGRunStore) FindAttempt(context.Context, ir.DAGRunRef) (dagrun.Attempt, error) {
	if s.status == nil {
		return nil, dagrun.ErrDAGRunIDNotFound
	}
	return &testutil.MockAttempt{Status: s.cloneStatus()}, nil
}

func (s *stubDAGRunStore) cloneStatus() *ir.DAGRunStatus {
	return cloneDAGRunStatus(s.status)
}

func cloneDAGRunStatus(status *ir.DAGRunStatus) *ir.DAGRunStatus {
	if status == nil {
		return nil
	}
	cloned := *status
	return &cloned
}
