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
			name:   "AlreadyQueued",
			dag:    &core.DAG{Name: "test-dag"},
			status: &exec.DAGRunStatus{Status: core.Queued},
			store:  &stubDAGRunStore{},
			setupQueue: func(qs *exec.MockQueueStore) {
				qs.AssertNotCalled(t, "Enqueue", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
			},
			assertStore: func(t *testing.T, store *stubDAGRunStore) {
				assert.Equal(t, 0, store.casCalls)
			},
		},
		{
			name: "Success",
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
				assert.Equal(t, 2, store.status.AutoRetryCount)
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
			name: "BackfillsMissingRootFromCallerStatus",
			dag:  &core.DAG{Name: "child-dag"},
			status: &exec.DAGRunStatus{
				Name:      "child-dag",
				DAGRunID:  "run-root",
				AttemptID: "att-root",
				Status:    core.Failed,
				Root:      exec.NewDAGRunRef("root-dag", "root-run"),
			},
			store: &stubDAGRunStore{
				status: &exec.DAGRunStatus{
					Name:      "child-dag",
					DAGRunID:  "run-root",
					AttemptID: "att-root",
					Status:    core.Failed,
				},
			},
			setupQueue: func(qs *exec.MockQueueStore) {
				qs.On("Enqueue", mock.Anything, "child-dag", exec.QueuePriorityLow, exec.NewDAGRunRef("child-dag", "run-root")).
					Return(nil)
			},
			assertStore: func(t *testing.T, store *stubDAGRunStore) {
				require.NotNil(t, store.status)
				assert.Equal(t, exec.NewDAGRunRef("root-dag", "root-run"), store.status.Root)
			},
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
				status:       &exec.DAGRunStatus{Name: "test-dag", DAGRunID: "run-3", AttemptID: "att-new", Status: core.Queued},
				firstSwapped: false,
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
				status:       &exec.DAGRunStatus{Name: "test-dag", DAGRunID: "run-3b", AttemptID: "att-other", Status: core.Running},
				firstSwapped: false,
			},
			assertErr: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, exec.ErrRetryStaleLatest)
			},
			assertStore: func(t *testing.T, store *stubDAGRunStore) {
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
			},
			store: &stubDAGRunStore{
				status: &exec.DAGRunStatus{
					Name:           "test-dag",
					DAGRunID:       "run-4",
					AttemptID:      "att-4",
					Status:         core.Failed,
					AutoRetryCount: 1,
				},
				secondSwapped: true,
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
				assert.Equal(t, 2, store.casCalls)
			},
			wantErr: "enqueue retry",
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
				secondSwapped: true,
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

type stubDAGRunStore struct {
	status        *exec.DAGRunStatus
	firstErr      error
	secondErr     error
	firstSwapped  bool
	secondSwapped bool
	casCalls      int
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
	_ context.Context,
	_ exec.DAGRunRef,
	expectedAttemptID string,
	expectedStatus core.Status,
	mutate func(*exec.DAGRunStatus) error,
	_ ...exec.CompareAndSwapStatusOption,
) (*exec.DAGRunStatus, bool, error) {
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

	swapped := true
	if s.casCalls == 1 && !s.firstSwapped {
		swapped = expectedAttemptID == s.status.AttemptID && expectedStatus == s.status.Status
	}

	if s.casCalls == 1 && !s.firstSwapped && s.status.Status == core.Queued {
		return s.cloneStatus(), false, nil
	}
	if s.casCalls == 2 && !s.secondSwapped {
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
	return nil, errors.New("unexpected call")
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
	if s.status == nil {
		return nil
	}
	cloned := *s.status
	return &cloned
}
