// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestEnqueueRetry(t *testing.T) {
	t.Parallel()

	triggerActor := "alice"
	tests := []struct {
		name        string
		dag         *ir.DAG
		status      *dagrun.DAGRunStatus
		opts        exec.EnqueueRetryOptions
		store       *stubDAGRunStore
		setupQueue  func(qs *exec.MockQueueStore)
		assertErr   func(t *testing.T, err error)
		assertStore func(t *testing.T, store *stubDAGRunStore)
		wantQueued  bool
		wantErr     string
	}{
		{
			name:   "AlreadyQueued",
			dag:    &ir.DAG{Name: "test-dag"},
			status: &dagrun.DAGRunStatus{Status: ir.Queued},
			store:  &stubDAGRunStore{},
			setupQueue: func(qs *exec.MockQueueStore) {
				qs.AssertNotCalled(t, "Enqueue", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
			},
		},
		{
			name: "SuccessRecordsTriggerActor",
			dag:  &ir.DAG{Name: "test-dag"},
			status: &dagrun.DAGRunStatus{
				Name:           "test-dag",
				DAGRunID:       "run-1",
				AttemptID:      "att-1",
				Status:         ir.Failed,
				AutoRetryCount: 2,
			},
			opts: exec.EnqueueRetryOptions{TriggerActor: &triggerActor},
			store: &stubDAGRunStore{
				status: &dagrun.DAGRunStatus{
					Name:           "test-dag",
					DAGRunID:       "run-1",
					AttemptID:      "att-1",
					Status:         ir.Failed,
					AutoRetryCount: 2,
					Log:            "/tmp/test-dag/run-1.log",
					WorkingDir:     "/tmp/test-dag/run-1",
					ProfileName:    "old-profile",
					TriggerActor:   "bob",
					ProfileResolvedAt: time.Date(
						2026, 3, 14, 14, 30, 0, 0, time.UTC,
					).Format(time.RFC3339),
				},
			},
			setupQueue: func(qs *exec.MockQueueStore) {
				qs.On("Enqueue", mock.Anything, "test-dag", exec.QueuePriorityLow, dagrun.NewDAGRunRef("test-dag", "run-1")).
					Return(nil)
			},
			assertStore: func(t *testing.T, store *stubDAGRunStore) {
				require.NotNil(t, store.status)
				assert.Equal(t, ir.Queued, store.status.Status)
				assert.Equal(t, ir.TriggerTypeRetry, store.status.TriggerType)
				assert.Equal(t, "alice", store.status.TriggerActor)
				assert.NotEmpty(t, store.status.QueuedAt)
				assert.Empty(t, store.status.Conditions)
				assert.Equal(t, 2, store.status.AutoRetryCount)
				assert.Equal(t, "old-profile", store.status.ProfileName)
				assert.Equal(t, "/tmp/test-dag/run-1.log", store.status.Log)
				assert.Equal(t, "/tmp/test-dag/run-1", store.status.WorkingDir)
				assert.NotEmpty(t, store.status.ProfileResolvedAt)
			},
			wantQueued: true,
		},
		{
			name: "AutoRetryIncrementsCount",
			dag:  &ir.DAG{Name: "test-dag"},
			status: &dagrun.DAGRunStatus{
				Name:           "test-dag",
				DAGRunID:       "run-auto",
				AttemptID:      "att-auto",
				Status:         ir.Failed,
				AutoRetryCount: 2,
			},
			opts: exec.EnqueueRetryOptions{AutoRetry: true},
			store: &stubDAGRunStore{
				status: &dagrun.DAGRunStatus{
					Name:           "test-dag",
					DAGRunID:       "run-auto",
					AttemptID:      "att-auto",
					Status:         ir.Failed,
					AutoRetryCount: 2,
					TriggerActor:   "bob",
				},
			},
			setupQueue: func(qs *exec.MockQueueStore) {
				qs.On("Enqueue", mock.Anything, "test-dag", exec.QueuePriorityLow, dagrun.NewDAGRunRef("test-dag", "run-auto")).
					Return(nil)
			},
			assertStore: func(t *testing.T, store *stubDAGRunStore) {
				require.NotNil(t, store.status)
				assert.Equal(t, 3, store.status.AutoRetryCount)
				assert.Equal(t, "bob", store.status.TriggerActor)
			},
			wantQueued: true,
		},
		{
			name: "UsesPersistedProcGroupWhenDAGIsNil",
			status: &dagrun.DAGRunStatus{
				Name:           "test-dag",
				DAGRunID:       "run-fast-path",
				AttemptID:      "att-fast-path",
				Status:         ir.Failed,
				AutoRetryCount: 1,
				ProcGroup:      "input-queue",
			},
			store: &stubDAGRunStore{
				status: &dagrun.DAGRunStatus{
					Name:           "test-dag",
					DAGRunID:       "run-fast-path",
					AttemptID:      "att-fast-path",
					Status:         ir.Failed,
					AutoRetryCount: 1,
					ProcGroup:      "custom-queue",
					Log:            "/tmp/test-dag/run-fast-path.log",
				},
			},
			setupQueue: func(qs *exec.MockQueueStore) {
				qs.On("Enqueue", mock.Anything, "custom-queue", exec.QueuePriorityLow, dagrun.NewDAGRunRef("test-dag", "run-fast-path")).
					Return(nil)
			},
			assertStore: func(t *testing.T, store *stubDAGRunStore) {
				require.NotNil(t, store.status)
				assert.Equal(t, ir.Queued, store.status.Status)
				assert.Equal(t, "custom-queue", store.status.ProcGroup)
			},
			wantQueued: true,
		},
		{
			name: "BackfillsMissingRootFromCallerStatus",
			dag:  &ir.DAG{Name: "child-dag"},
			status: &dagrun.DAGRunStatus{
				Name:      "child-dag",
				DAGRunID:  "run-root",
				AttemptID: "att-root",
				Status:    ir.Failed,
				Root:      dagrun.NewDAGRunRef("root-dag", "root-run"),
			},
			store: &stubDAGRunStore{
				status: &dagrun.DAGRunStatus{
					Name:      "child-dag",
					DAGRunID:  "run-root",
					AttemptID: "att-root",
					Status:    ir.Failed,
				},
			},
			setupQueue: func(qs *exec.MockQueueStore) {
				qs.On("Enqueue", mock.Anything, "child-dag", exec.QueuePriorityLow, dagrun.NewDAGRunRef("child-dag", "run-root")).
					Return(nil)
			},
			assertStore: func(t *testing.T, store *stubDAGRunStore) {
				require.NotNil(t, store.status)
				assert.Equal(t, dagrun.NewDAGRunRef("root-dag", "root-run"), store.status.Root)
			},
			wantQueued: true,
		},
		{
			name: "PersistQueuedStatusFails",
			dag:  &ir.DAG{Name: "test-dag"},
			status: &dagrun.DAGRunStatus{
				Name:      "test-dag",
				DAGRunID:  "run-2",
				AttemptID: "att-2",
				Status:    ir.Failed,
			},
			store: &stubDAGRunStore{
				status:   &dagrun.DAGRunStatus{Name: "test-dag", DAGRunID: "run-2", AttemptID: "att-2", Status: ir.Failed},
				firstErr: errors.New("cas error"),
			},
			wantErr: "persist queued retry status",
		},
		{
			name: "CompareAndSwapLosesRaceToSameAttemptQueued",
			dag:  &ir.DAG{Name: "test-dag"},
			status: &dagrun.DAGRunStatus{
				Name:       "test-dag",
				DAGRunID:   "run-3",
				AttemptID:  "att-3",
				AttemptKey: "key-3",
				Status:     ir.Failed,
			},
			store: &stubDAGRunStore{
				status: &dagrun.DAGRunStatus{
					Name: "test-dag", DAGRunID: "run-3", AttemptID: "att-3", AttemptKey: "key-3", Status: ir.Queued,
				},
			},
		},
		{
			name: "CompareAndSwapLosesRaceToDifferentLatestStatus",
			dag:  &ir.DAG{Name: "test-dag"},
			status: &dagrun.DAGRunStatus{
				Name:      "test-dag",
				DAGRunID:  "run-3b",
				AttemptID: "att-3b",
				Status:    ir.Failed,
			},
			store: &stubDAGRunStore{
				status: &dagrun.DAGRunStatus{Name: "test-dag", DAGRunID: "run-3b", AttemptID: "att-other", Status: ir.Running},
			},
			assertErr: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, exec.ErrRetryStaleLatest)
			},
		},
		{
			name: "EnqueueFailsAndRollsBack",
			dag:  &ir.DAG{Name: "test-dag"},
			status: &dagrun.DAGRunStatus{
				Name:           "test-dag",
				DAGRunID:       "run-4",
				AttemptID:      "att-4",
				Status:         ir.Failed,
				AutoRetryCount: 1,
			},
			store: &stubDAGRunStore{
				status: &dagrun.DAGRunStatus{
					Name:           "test-dag",
					DAGRunID:       "run-4",
					AttemptID:      "att-4",
					Status:         ir.Failed,
					AutoRetryCount: 1,
					TriggerActor:   "bob",
				},
			},
			opts: exec.EnqueueRetryOptions{AutoRetry: true, TriggerActor: &triggerActor},
			setupQueue: func(qs *exec.MockQueueStore) {
				qs.On("Enqueue", mock.Anything, "test-dag", exec.QueuePriorityLow, dagrun.NewDAGRunRef("test-dag", "run-4")).
					Return(errors.New("enqueue error"))
			},
			assertStore: func(t *testing.T, store *stubDAGRunStore) {
				require.NotNil(t, store.status)
				assert.Equal(t, ir.Failed, store.status.Status)
				assert.Empty(t, store.status.QueuedAt)
				assert.Equal(t, ir.TriggerTypeUnknown, store.status.TriggerType)
				assert.Equal(t, 1, store.status.AutoRetryCount)
				assert.Equal(t, "bob", store.status.TriggerActor)
			},
			wantErr: "enqueue retry",
		},
		{
			name: "EnqueueAndRollbackFail",
			dag:  &ir.DAG{Name: "test-dag"},
			status: &dagrun.DAGRunStatus{
				Name:      "test-dag",
				DAGRunID:  "run-rollback-failure",
				AttemptID: "att-rollback-failure",
				Status:    ir.Waiting,
			},
			store: &stubDAGRunStore{
				status: &dagrun.DAGRunStatus{
					Name:      "test-dag",
					DAGRunID:  "run-rollback-failure",
					AttemptID: "att-rollback-failure",
					Status:    ir.Waiting,
				},
				secondErr: errors.New("rollback error"),
			},
			setupQueue: func(qs *exec.MockQueueStore) {
				qs.On("Enqueue", mock.Anything, "test-dag", exec.QueuePriorityLow, dagrun.NewDAGRunRef("test-dag", "run-rollback-failure")).
					Return(errors.New("enqueue error"))
			},
			assertStore: func(t *testing.T, store *stubDAGRunStore) {
				require.NotNil(t, store.status)
				assert.Equal(t, ir.Queued, store.status.Status)
			},
			wantErr: "enqueue retry: enqueue error; rollback queued retry status: rollback error",
		},
		{
			name: "EmptyProcGroupRollsBackQueuedStatus",
			status: &dagrun.DAGRunStatus{
				DAGRunID:       "run-empty-group",
				AttemptID:      "att-empty-group",
				Status:         ir.Failed,
				AutoRetryCount: 1,
			},
			store: &stubDAGRunStore{
				status: &dagrun.DAGRunStatus{
					DAGRunID:       "run-empty-group",
					AttemptID:      "att-empty-group",
					Status:         ir.Failed,
					AutoRetryCount: 1,
					TriggerActor:   "bob",
				},
			},
			opts: exec.EnqueueRetryOptions{TriggerActor: &triggerActor},
			assertStore: func(t *testing.T, store *stubDAGRunStore) {
				require.NotNil(t, store.status)
				assert.Equal(t, ir.Failed, store.status.Status)
				assert.Empty(t, store.status.QueuedAt)
				assert.Equal(t, ir.TriggerTypeUnknown, store.status.TriggerType)
				assert.Equal(t, 1, store.status.AutoRetryCount)
				assert.Equal(t, "bob", store.status.TriggerActor)
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

			queued, err := exec.EnqueueRetry(ctx, tt.store, qs, tt.dag, tt.status, tt.opts)
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

			if tt.assertStore != nil {
				tt.assertStore(t, tt.store)
			}
			qs.AssertExpectations(t)
		})
	}
}

type stubDAGRunStore struct {
	status    *dagrun.DAGRunStatus
	firstErr  error
	secondErr error
	casCalls  int
}

func (s *stubDAGRunStore) CreateAttempt(context.Context, *ir.DAG, time.Time, string, dagrun.NewDAGRunAttemptOptions) (dagrun.DAGRunAttempt, error) {
	return nil, errors.New("unexpected call")
}

func (s *stubDAGRunStore) RecentAttempts(context.Context, string, int) []dagrun.DAGRunAttempt {
	return nil
}

func (s *stubDAGRunStore) LatestAttempt(context.Context, string) (dagrun.DAGRunAttempt, error) {
	return nil, errors.New("unexpected call")
}

func (s *stubDAGRunStore) ListStatuses(context.Context, ...dagrun.ListDAGRunStatusesOption) ([]*dagrun.DAGRunStatus, error) {
	return nil, nil
}

func (s *stubDAGRunStore) ListStatusesPage(context.Context, ...dagrun.ListDAGRunStatusesOption) (dagrun.DAGRunStatusPage, error) {
	return dagrun.DAGRunStatusPage{}, nil
}

func (s *stubDAGRunStore) CompareAndSwapLatestAttemptStatus(
	_ context.Context,
	_ dagrun.DAGRunRef,
	expectedAttemptID string,
	expectedStatus ir.Status,
	mutate func(*dagrun.DAGRunStatus) error,
	_ ...dagrun.CompareAndSwapStatusOption,
) (*dagrun.DAGRunStatus, bool, error) {
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

	if expectedAttemptID != s.status.AttemptID || expectedStatus != s.status.Status {
		return s.cloneStatus(), false, nil
	}

	updated := s.cloneStatus()
	if err := mutate(updated); err != nil {
		return nil, false, err
	}
	s.status = updated
	return s.cloneStatus(), true, nil
}

func (s *stubDAGRunStore) FindAttempt(context.Context, dagrun.DAGRunRef) (dagrun.DAGRunAttempt, error) {
	if s.status == nil {
		return nil, dagrun.ErrDAGRunIDNotFound
	}
	return &testutil.MockDAGRunAttempt{Status: s.cloneStatus()}, nil
}

func (s *stubDAGRunStore) FindSubAttempt(context.Context, dagrun.DAGRunRef, string) (dagrun.DAGRunAttempt, error) {
	return nil, errors.New("unexpected call")
}

func (s *stubDAGRunStore) CreateSubAttempt(context.Context, dagrun.DAGRunRef, string) (dagrun.DAGRunAttempt, error) {
	return nil, errors.New("unexpected call")
}

func (s *stubDAGRunStore) RemoveOldDAGRuns(context.Context, string, int, ...dagrun.RemoveOldDAGRunsOption) ([]string, error) {
	return nil, nil
}

func (s *stubDAGRunStore) RemoveDAGRun(context.Context, dagrun.DAGRunRef, ...dagrun.RemoveDAGRunOption) error {
	return nil
}

func (s *stubDAGRunStore) cloneStatus() *dagrun.DAGRunStatus {
	return cloneDAGRunStatus(s.status)
}

func cloneDAGRunStatus(status *dagrun.DAGRunStatus) *dagrun.DAGRunStatus {
	if status == nil {
		return nil
	}
	cloned := *status
	return &cloned
}
