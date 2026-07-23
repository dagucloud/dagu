// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestEnqueueRetry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		dag         *core.DAG
		status      *exec.DAGRunStatus
		opts        exec.EnqueueRetryOptions
		store       *stubDAGRunStore
		setupQueue  func(qs *exec.MockQueueStore)
		assertErr   func(t *testing.T, err error)
		assertStore func(t *testing.T, store *stubDAGRunStore)
		wantErr     string
	}{
		{
			name:   "RejectsAlreadyQueued",
			dag:    &core.DAG{Name: "test-dag"},
			status: &exec.DAGRunStatus{Status: core.Queued},
			store:  &stubDAGRunStore{},
			assertErr: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, exec.ErrRetryStaleLatest)
			},
		},
		{
			name: "SuccessPreservesProfile",
			dag:  &core.DAG{Name: "test-dag"},
			status: &exec.DAGRunStatus{
				Name:           "test-dag",
				DAGRunID:       "run-1",
				AttemptID:      "att-1",
				Status:         core.Failed,
				AutoRetryCount: 2,
			},
			store: &stubDAGRunStore{
				status: &exec.DAGRunStatus{
					Name:           "test-dag",
					DAGRunID:       "run-1",
					AttemptID:      "att-1",
					Status:         core.Failed,
					AutoRetryCount: 2,
					Log:            "/tmp/test-dag/run-1.log",
					WorkingDir:     "/tmp/test-dag/run-1",
					ProfileName:    "old-profile",
					ProfileResolvedAt: time.Date(
						2026, 3, 14, 14, 30, 0, 0, time.UTC,
					).Format(time.RFC3339),
				},
			},
			setupQueue: func(qs *exec.MockQueueStore) {
				qs.On("Enqueue", mock.Anything, "test-dag", exec.QueuePriorityLow, exec.NewDAGRunRef("test-dag", "run-1")).
					Return(nil)
			},
			assertStore: func(t *testing.T, store *stubDAGRunStore) {
				require.NotNil(t, store.status)
				assert.Equal(t, core.Queued, store.status.Status)
				assert.Equal(t, core.TriggerTypeRetry, store.status.TriggerType)
				assert.NotEmpty(t, store.status.QueuedAt)
				assert.Empty(t, store.status.Conditions)
				assert.Equal(t, 2, store.status.AutoRetryCount)
				assert.Equal(t, "old-profile", store.status.ProfileName)
				assert.Equal(t, "/tmp/test-dag/run-1.log", store.status.Log)
				assert.Equal(t, "/tmp/test-dag/run-1", store.status.WorkingDir)
				assert.NotEmpty(t, store.status.ProfileResolvedAt)
				assert.Equal(t, 1, store.casCalls)
			},
		},
		{
			name: "AutoRetryIncrementsCount",
			dag:  &core.DAG{Name: "test-dag"},
			status: &exec.DAGRunStatus{
				Name:           "test-dag",
				DAGRunID:       "run-auto",
				AttemptID:      "att-auto",
				Status:         core.Failed,
				AutoRetryCount: 2,
			},
			opts: exec.EnqueueRetryOptions{AutoRetry: true},
			store: &stubDAGRunStore{
				status: &exec.DAGRunStatus{
					Name:           "test-dag",
					DAGRunID:       "run-auto",
					AttemptID:      "att-auto",
					Status:         core.Failed,
					AutoRetryCount: 2,
				},
			},
			setupQueue: func(qs *exec.MockQueueStore) {
				qs.On("Enqueue", mock.Anything, "test-dag", exec.QueuePriorityLow, exec.NewDAGRunRef("test-dag", "run-auto")).
					Return(nil)
			},
			assertStore: func(t *testing.T, store *stubDAGRunStore) {
				require.NotNil(t, store.status)
				assert.Equal(t, 3, store.status.AutoRetryCount)
			},
		},
		{
			name: "UsesPersistedProcGroupWhenDAGIsNil",
			status: &exec.DAGRunStatus{
				Name:           "test-dag",
				DAGRunID:       "run-fast-path",
				AttemptID:      "att-fast-path",
				Status:         core.Failed,
				AutoRetryCount: 1,
				ProcGroup:      "input-queue",
			},
			store: &stubDAGRunStore{
				status: &exec.DAGRunStatus{
					Name:           "test-dag",
					DAGRunID:       "run-fast-path",
					AttemptID:      "att-fast-path",
					Status:         core.Failed,
					AutoRetryCount: 1,
					ProcGroup:      "custom-queue",
					Log:            "/tmp/test-dag/run-fast-path.log",
				},
			},
			setupQueue: func(qs *exec.MockQueueStore) {
				qs.On("Enqueue", mock.Anything, "custom-queue", exec.QueuePriorityLow, exec.NewDAGRunRef("test-dag", "run-fast-path")).
					Return(nil)
			},
			assertStore: func(t *testing.T, store *stubDAGRunStore) {
				require.NotNil(t, store.status)
				assert.Equal(t, core.Queued, store.status.Status)
				assert.Equal(t, "custom-queue", store.status.ProcGroup)
			},
		},
		{
			name: "PreservesLatestStatusChanges",
			dag:  &core.DAG{Name: "test-dag"},
			status: &exec.DAGRunStatus{
				Name:      "test-dag",
				DAGRunID:  "run-latest",
				AttemptID: "att-latest",
				Status:    core.Waiting,
				Nodes: []*exec.Node{
					{Status: core.NodeWaiting},
				},
			},
			store: &stubDAGRunStore{
				status: &exec.DAGRunStatus{
					Name:      "test-dag",
					DAGRunID:  "run-latest",
					AttemptID: "att-latest",
					Status:    core.Waiting,
					Nodes: []*exec.Node{
						{Status: core.NodeSucceeded},
					},
				},
			},
			setupQueue: func(qs *exec.MockQueueStore) {
				qs.On("Enqueue", mock.Anything, "test-dag", exec.QueuePriorityLow, exec.NewDAGRunRef("test-dag", "run-latest")).
					Return(nil)
			},
			assertStore: func(t *testing.T, store *stubDAGRunStore) {
				require.NotNil(t, store.status)
				require.Len(t, store.status.Nodes, 1)
				assert.Equal(t, core.NodeSucceeded, store.status.Nodes[0].Status)
			},
		},
		{
			name: "RejectsSubDAGRetry",
			dag:  &core.DAG{Name: "child-dag"},
			status: &exec.DAGRunStatus{
				Name:      "child-dag",
				DAGRunID:  "child-run",
				AttemptID: "child-attempt",
				Status:    core.Failed,
				Root:      exec.NewDAGRunRef("root-dag", "root-run"),
			},
			store: &stubDAGRunStore{
				status: &exec.DAGRunStatus{
					Name:      "child-dag",
					DAGRunID:  "child-run",
					AttemptID: "child-attempt",
					Status:    core.Failed,
				},
			},
			assertStore: func(t *testing.T, store *stubDAGRunStore) {
				require.NotNil(t, store.status)
				assert.Equal(t, core.Failed, store.status.Status)
				assert.Zero(t, store.casCalls)
			},
			wantErr: "queued sub-DAG retries are not supported",
		},
		{
			name: "PersistQueuedStatusFails",
			dag:  &core.DAG{Name: "test-dag"},
			status: &exec.DAGRunStatus{
				Name:      "test-dag",
				DAGRunID:  "run-2",
				AttemptID: "att-2",
				Status:    core.Failed,
			},
			store: &stubDAGRunStore{
				status:   &exec.DAGRunStatus{Name: "test-dag", DAGRunID: "run-2", AttemptID: "att-2", Status: core.Failed},
				firstErr: errors.New("cas error"),
			},
			wantErr: "persist queued retry status",
		},
		{
			name: "CompareAndSwapLosesRaceToQueued",
			dag:  &core.DAG{Name: "test-dag"},
			status: &exec.DAGRunStatus{
				Name:      "test-dag",
				DAGRunID:  "run-3",
				AttemptID: "att-3",
				Status:    core.Failed,
			},
			store: &stubDAGRunStore{
				status: &exec.DAGRunStatus{Name: "test-dag", DAGRunID: "run-3", AttemptID: "att-new", Status: core.Queued},
			},
			assertErr: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, exec.ErrRetryStaleLatest)
			},
			assertStore: func(t *testing.T, store *stubDAGRunStore) {
				assert.Equal(t, 1, store.casCalls)
			},
		},
		{
			name: "CompareAndSwapLosesRaceToDifferentLatestStatus",
			dag:  &core.DAG{Name: "test-dag"},
			status: &exec.DAGRunStatus{
				Name:      "test-dag",
				DAGRunID:  "run-3b",
				AttemptID: "att-3b",
				Status:    core.Failed,
			},
			store: &stubDAGRunStore{
				status: &exec.DAGRunStatus{Name: "test-dag", DAGRunID: "run-3b", AttemptID: "att-other", Status: core.Running},
			},
			assertErr: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, exec.ErrRetryStaleLatest)
			},
			assertStore: func(t *testing.T, store *stubDAGRunStore) {
				assert.Equal(t, 1, store.casCalls)
			},
		},
		{
			name: "RejectsReplacementWithReusedAttemptID",
			dag:  &core.DAG{Name: "test-dag"},
			status: &exec.DAGRunStatus{
				Name:       "test-dag",
				DAGRunID:   "run-replaced",
				AttemptID:  "att-reused",
				AttemptKey: "attempt-key-old",
				Status:     core.Failed,
			},
			store: &stubDAGRunStore{
				status: &exec.DAGRunStatus{
					Name:       "test-dag",
					DAGRunID:   "run-replaced",
					AttemptID:  "att-reused",
					AttemptKey: "attempt-key-new",
					Status:     core.Failed,
				},
			},
			assertErr: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, exec.ErrRetryStaleLatest)
			},
			assertStore: func(t *testing.T, store *stubDAGRunStore) {
				assert.Equal(t, "attempt-key-new", store.status.AttemptKey)
				assert.Equal(t, core.Failed, store.status.Status)
				assert.Equal(t, 1, store.casCalls)
			},
		},
		{
			name: "EnqueueFailsAndRollsBack",
			dag:  &core.DAG{Name: "test-dag"},
			status: &exec.DAGRunStatus{
				Name:           "test-dag",
				DAGRunID:       "run-4",
				AttemptID:      "att-4",
				Status:         core.Failed,
				AutoRetryCount: 1,
				Root:           exec.NewDAGRunRef("test-dag", "run-4"),
			},
			store: &stubDAGRunStore{
				status: &exec.DAGRunStatus{
					Name:           "test-dag",
					DAGRunID:       "run-4",
					AttemptID:      "att-4",
					Status:         core.Failed,
					AutoRetryCount: 1,
				},
			},
			opts: exec.EnqueueRetryOptions{AutoRetry: true},
			setupQueue: func(qs *exec.MockQueueStore) {
				qs.On("Enqueue", mock.Anything, "test-dag", exec.QueuePriorityLow, exec.NewDAGRunRef("test-dag", "run-4")).
					Return(errors.New("enqueue error"))
			},
			assertStore: func(t *testing.T, store *stubDAGRunStore) {
				require.NotNil(t, store.status)
				assert.Equal(t, core.Failed, store.status.Status)
				assert.Empty(t, store.status.QueuedAt)
				assert.Equal(t, core.TriggerTypeUnknown, store.status.TriggerType)
				assert.Equal(t, 1, store.status.AutoRetryCount)
				assert.True(t, store.status.Root.Zero())
				assert.Equal(t, 2, store.casCalls)
			},
			wantErr: "enqueue retry",
		},
		{
			name: "ReportsEnqueueAndRollbackFailures",
			dag:  &core.DAG{Name: "test-dag"},
			status: &exec.DAGRunStatus{
				Name:      "test-dag",
				DAGRunID:  "run-rollback-error",
				AttemptID: "att-rollback-error",
				Status:    core.Failed,
			},
			store: &stubDAGRunStore{
				status: &exec.DAGRunStatus{
					Name:      "test-dag",
					DAGRunID:  "run-rollback-error",
					AttemptID: "att-rollback-error",
					Status:    core.Failed,
				},
				secondErr: errors.New("rollback error"),
			},
			setupQueue: func(qs *exec.MockQueueStore) {
				qs.On("Enqueue", mock.Anything, "test-dag", exec.QueuePriorityLow, exec.NewDAGRunRef("test-dag", "run-rollback-error")).
					Return(errors.New("enqueue error"))
			},
			assertErr: func(t *testing.T, err error) {
				assert.ErrorContains(t, err, "enqueue error")
				assert.ErrorContains(t, err, "rollback queued retry status: rollback error")
			},
		},
		{
			name: "EmptyProcGroupRollsBackQueuedStatus",
			status: &exec.DAGRunStatus{
				DAGRunID:       "run-empty-group",
				AttemptID:      "att-empty-group",
				Status:         core.Failed,
				AutoRetryCount: 1,
			},
			store: &stubDAGRunStore{
				status: &exec.DAGRunStatus{
					DAGRunID:       "run-empty-group",
					AttemptID:      "att-empty-group",
					Status:         core.Failed,
					AutoRetryCount: 1,
				},
			},
			assertStore: func(t *testing.T, store *stubDAGRunStore) {
				require.NotNil(t, store.status)
				assert.Equal(t, core.Failed, store.status.Status)
				assert.Empty(t, store.status.QueuedAt)
				assert.Equal(t, core.TriggerTypeUnknown, store.status.TriggerType)
				assert.Equal(t, 1, store.status.AutoRetryCount)
				assert.Equal(t, 2, store.casCalls)
			},
			wantErr: "proc group is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			qs := &exec.MockQueueStore{}
			if tt.setupQueue != nil {
				tt.setupQueue(qs)
			}

			err := exec.EnqueueRetry(ctx, tt.store, qs, tt.dag, tt.status, tt.opts)
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

			if tt.assertStore != nil {
				tt.assertStore(t, tt.store)
			}
			qs.AssertExpectations(t)
		})
	}
}

func TestEnqueueRetryRollsBackAfterCallerCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	status := &exec.DAGRunStatus{
		Name:      "test-dag",
		DAGRunID:  "run-canceled",
		AttemptID: "att-canceled",
		Status:    core.Failed,
	}
	store := &stubDAGRunStore{
		status: cloneDAGRunStatus(status),
	}
	queueStore := &exec.MockQueueStore{}
	queueStore.On(
		"Enqueue",
		mock.Anything,
		"test-dag",
		exec.QueuePriorityLow,
		status.DAGRun(),
	).Run(func(mock.Arguments) {
		cancel()
	}).Return(context.Canceled)

	err := exec.EnqueueRetry(
		ctx,
		store,
		queueStore,
		&core.DAG{Name: "test-dag"},
		status,
		exec.EnqueueRetryOptions{},
	)

	require.ErrorIs(t, err, context.Canceled)
	require.NotNil(t, store.status)
	assert.Equal(t, core.Failed, store.status.Status)
	queueStore.AssertExpectations(t)
}

func TestEnqueueRetryDoesNotRollbackReplacementAttempt(t *testing.T) {
	t.Parallel()

	status := &exec.DAGRunStatus{
		Name:       "test-dag",
		DAGRunID:   "run-replaced-during-enqueue",
		AttemptID:  "att-reused",
		AttemptKey: "attempt-key-old",
		Status:     core.Failed,
	}
	store := &stubDAGRunStore{
		status: cloneDAGRunStatus(status),
	}
	queueStore := &exec.MockQueueStore{}
	queueStore.On(
		"Enqueue",
		mock.Anything,
		"test-dag",
		exec.QueuePriorityLow,
		status.DAGRun(),
	).Run(func(mock.Arguments) {
		store.status.AttemptKey = "attempt-key-new"
	}).Return(errors.New("enqueue error"))

	err := exec.EnqueueRetry(
		t.Context(),
		store,
		queueStore,
		&core.DAG{Name: "test-dag"},
		status,
		exec.EnqueueRetryOptions{},
	)

	require.Error(t, err)
	assert.ErrorContains(t, err, "enqueue error")
	assert.ErrorContains(t, err, "rollback queued retry status: DAG-run state changed")
	assert.Equal(t, "attempt-key-new", store.status.AttemptKey)
	assert.Equal(t, core.Queued, store.status.Status)
	queueStore.AssertExpectations(t)
}

type stubDAGRunStore struct {
	status    *exec.DAGRunStatus
	firstErr  error
	secondErr error
	casCalls  int
}

func (s *stubDAGRunStore) CreateAttempt(context.Context, *core.DAG, time.Time, string, exec.NewDAGRunAttemptOptions) (exec.DAGRunAttempt, error) {
	return nil, errors.New("unexpected call")
}

func (s *stubDAGRunStore) RecentAttempts(context.Context, string, int) []exec.DAGRunAttempt {
	return nil
}

func (s *stubDAGRunStore) LatestAttempt(context.Context, string) (exec.DAGRunAttempt, error) {
	return nil, errors.New("unexpected call")
}

func (s *stubDAGRunStore) ListStatuses(context.Context, ...exec.ListDAGRunStatusesOption) ([]*exec.DAGRunStatus, error) {
	return nil, nil
}

func (s *stubDAGRunStore) ListStatusesPage(context.Context, ...exec.ListDAGRunStatusesOption) (exec.DAGRunStatusPage, error) {
	return exec.DAGRunStatusPage{}, nil
}

func (s *stubDAGRunStore) CompareAndSwapLatestAttemptStatus(
	ctx context.Context,
	_ exec.DAGRunRef,
	expectedAttemptID string,
	expectedStatus core.Status,
	mutate func(*exec.DAGRunStatus) error,
	opts ...exec.CompareAndSwapStatusOption,
) (*exec.DAGRunStatus, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
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

	swapped := expectedAttemptID == s.status.AttemptID && expectedStatus == s.status.Status
	cfg := exec.NewCompareAndSwapStatusOptions(opts...)
	if cfg.ExpectedAttemptKey != "" && cfg.ExpectedAttemptKey != s.status.AttemptKey {
		swapped = false
	}
	if !swapped {
		return s.cloneStatus(), false, nil
	}

	updated := s.cloneStatus()
	if err := mutate(updated); err != nil {
		return nil, false, err
	}
	s.status = updated
	return s.cloneStatus(), true, nil
}

func (s *stubDAGRunStore) FindAttempt(context.Context, exec.DAGRunRef) (exec.DAGRunAttempt, error) {
	if s.status == nil {
		return nil, exec.ErrDAGRunIDNotFound
	}
	return &exec.MockDAGRunAttempt{Status: s.cloneStatus()}, nil
}

func (s *stubDAGRunStore) FindSubAttempt(context.Context, exec.DAGRunRef, string) (exec.DAGRunAttempt, error) {
	return nil, errors.New("unexpected call")
}

func (s *stubDAGRunStore) CreateSubAttempt(context.Context, exec.DAGRunRef, string) (exec.DAGRunAttempt, error) {
	return nil, errors.New("unexpected call")
}

func (s *stubDAGRunStore) RemoveOldDAGRuns(context.Context, string, int, ...exec.RemoveOldDAGRunsOption) ([]string, error) {
	return nil, nil
}

func (s *stubDAGRunStore) RenameDAGRuns(context.Context, string, string) error {
	return nil
}

func (s *stubDAGRunStore) RemoveDAGRun(context.Context, exec.DAGRunRef, ...exec.RemoveDAGRunOption) error {
	return nil
}

func (s *stubDAGRunStore) cloneStatus() *exec.DAGRunStatus {
	return cloneDAGRunStatus(s.status)
}

func cloneDAGRunStatus(status *exec.DAGRunStatus) *exec.DAGRunStatus {
	if status == nil {
		return nil
	}
	cloned := *status
	return &cloned
}
