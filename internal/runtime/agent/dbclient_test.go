// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type mockDAGLoader struct {
	mock.Mock
}

func (m *mockDAGLoader) GetDetails(ctx context.Context, fileName string, opts persis.DAGLoadOptions) (*ir.DAG, error) {
	args := m.Called(ctx, fileName, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ir.DAG), args.Error(1)
}

var _ dagrun.DAGRunStore = (*mockDAGRunStore)(nil)

// mockDAGRunStore implements models.DAGRunStore
type mockDAGRunStore struct {
	mock.Mock
}

// RemoveDAGRun implements models.DAGRunStore.
func (m *mockDAGRunStore) RemoveDAGRun(ctx context.Context, dagRun ir.DAGRunRef, _ ...dagrun.RemoveDAGRunOption) error {
	panic("unimplemented")
}

func (m *mockDAGRunStore) CreateAttempt(ctx context.Context, dag *ir.DAG, ts time.Time, dagRunID string, opts dagrun.NewDAGRunAttemptOptions) (dagrun.DAGRunAttempt, error) {
	args := m.Called(ctx, dag, ts, dagRunID, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(dagrun.DAGRunAttempt), args.Error(1)
}

func (m *mockDAGRunStore) RecentAttempts(ctx context.Context, name string, itemLimit int) []dagrun.DAGRunAttempt {
	args := m.Called(ctx, name, itemLimit)
	return args.Get(0).([]dagrun.DAGRunAttempt)
}

func (m *mockDAGRunStore) LatestAttempt(ctx context.Context, name string) (dagrun.DAGRunAttempt, error) {
	args := m.Called(ctx, name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(dagrun.DAGRunAttempt), args.Error(1)
}

func (m *mockDAGRunStore) ListStatuses(ctx context.Context, opts ...dagrun.ListDAGRunStatusesOption) ([]*ir.DAGRunStatus, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*ir.DAGRunStatus), args.Error(1)
}

func (m *mockDAGRunStore) ListStatusesPage(ctx context.Context, opts ...dagrun.ListDAGRunStatusesOption) (dagrun.DAGRunStatusPage, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return dagrun.DAGRunStatusPage{}, args.Error(1)
	}
	return args.Get(0).(dagrun.DAGRunStatusPage), args.Error(1)
}

func (m *mockDAGRunStore) CompareAndSwapLatestAttemptStatus(
	ctx context.Context,
	dagRun ir.DAGRunRef,
	expectedAttemptID string,
	expectedStatus ir.Status,
	mutate func(*ir.DAGRunStatus) error,
	_ ...dagrun.CompareAndSwapStatusOption,
) (*ir.DAGRunStatus, bool, error) {
	args := m.Called(ctx, dagRun, expectedAttemptID, expectedStatus, mutate)
	if args.Get(0) == nil {
		return nil, args.Bool(1), args.Error(2)
	}
	return args.Get(0).(*ir.DAGRunStatus), args.Bool(1), args.Error(2)
}

func (m *mockDAGRunStore) FindAttempt(ctx context.Context, dagRun ir.DAGRunRef) (dagrun.DAGRunAttempt, error) {
	args := m.Called(ctx, dagRun)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(dagrun.DAGRunAttempt), args.Error(1)
}

func (m *mockDAGRunStore) FindSubAttempt(ctx context.Context, rootDAGRun ir.DAGRunRef, dagRunID string) (dagrun.DAGRunAttempt, error) {
	args := m.Called(ctx, rootDAGRun, dagRunID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(dagrun.DAGRunAttempt), args.Error(1)
}

func (m *mockDAGRunStore) CreateSubAttempt(ctx context.Context, rootRef ir.DAGRunRef, subDAGRunID string) (dagrun.DAGRunAttempt, error) {
	args := m.Called(ctx, rootRef, subDAGRunID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(dagrun.DAGRunAttempt), args.Error(1)
}

func (m *mockDAGRunStore) RemoveOldDAGRuns(ctx context.Context, name string, retentionDays int, opts ...dagrun.RemoveOldDAGRunsOption) ([]string, error) {
	args := m.Called(ctx, name, retentionDays, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

func TestDBClient_GetDAG(t *testing.T) {
	testDAG := &ir.DAG{Name: "test-dag"}

	setupMockLoader := func(name string, dag *ir.DAG, err error) *mockDAGLoader {
		m := new(mockDAGLoader)
		m.On("GetDetails", mock.Anything, name, mock.Anything).Return(dag, err)
		return m
	}

	tests := []struct {
		name              string
		dagLoader         dagDetailsLoader // nil means no local loader
		remoteLoader      RemoteDAGLoader  // nil means no remote loader
		expectDAG         *ir.DAG
		expectError       bool
		expectErrContains string
	}{
		{
			name:         "local hit returns dag",
			dagLoader:    setupMockLoader("test-dag", testDAG, nil),
			remoteLoader: nil,
			expectDAG:    testDAG,
			expectError:  false,
		},
		{
			name:      "local not-found + remote hit",
			dagLoader: setupMockLoader("test-dag", nil, persis.ErrDAGNotFound),
			remoteLoader: func(ctx context.Context, name string) (*ir.DAG, error) {
				return testDAG, nil
			},
			expectDAG:   testDAG,
			expectError: false,
		},
		{
			name:      "local not-found + remote returns nil",
			dagLoader: setupMockLoader("test-dag", nil, persis.ErrDAGNotFound),
			remoteLoader: func(ctx context.Context, name string) (*ir.DAG, error) {
				return nil, nil
			},
			expectError:       true,
			expectErrContains: "DAG is not found",
		},
		{
			name:      "local not-found + remote returns error",
			dagLoader: setupMockLoader("test-dag", nil, persis.ErrDAGNotFound),
			remoteLoader: func(ctx context.Context, name string) (*ir.DAG, error) {
				return nil, errors.New("remote unavailable")
			},
			expectError:       true,
			expectErrContains: "DAG is not found",
		},
		{
			name:              "local not-found + no remote loader",
			dagLoader:         setupMockLoader("test-dag", nil, persis.ErrDAGNotFound),
			remoteLoader:      nil,
			expectError:       true,
			expectErrContains: "DAG is not found",
		},
		{
			name:      "local non-not-found error propagates immediately",
			dagLoader: setupMockLoader("test-dag", nil, errors.New("permission denied")),
			remoteLoader: func(ctx context.Context, name string) (*ir.DAG, error) {
				return testDAG, nil // should NOT be called
			},
			expectError:       true,
			expectErrContains: "permission denied",
		},
		{
			name:      "nil local loader + remote hit",
			dagLoader: nil,
			remoteLoader: func(ctx context.Context, name string) (*ir.DAG, error) {
				return testDAG, nil
			},
			expectDAG:   testDAG,
			expectError: false,
		},
		{
			name:      "nil local loader + remote returns nil dag",
			dagLoader: nil,
			remoteLoader: func(ctx context.Context, name string) (*ir.DAG, error) {
				return nil, nil
			},
			expectError:       true,
			expectErrContains: "not found locally or remotely",
		},
		{
			name:      "nil local loader + remote returns error",
			dagLoader: nil,
			remoteLoader: func(ctx context.Context, name string) (*ir.DAG, error) {
				return nil, errors.New("remote unavailable")
			},
			expectError:       true,
			expectErrContains: "remote DAG load failed",
		},
		{
			name:              "nil local loader + no remote loader",
			dagLoader:         nil,
			remoteLoader:      nil,
			expectError:       true,
			expectErrContains: "no local DAG store and no remote loader",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mockDRS := new(mockDAGRunStore)
			client := newDBClient(mockDRS, tt.dagLoader, tt.remoteLoader)

			dag, err := client.GetDAG(ctx, "test-dag")

			if tt.expectError {
				require.Error(t, err)
				if tt.expectErrContains != "" {
					assert.Contains(t, err.Error(), tt.expectErrContains)
				}
				assert.Nil(t, dag)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectDAG, dag)
			}

			// Assert mock expectations for the DAG store (when a mock is used).
			if loader, ok := tt.dagLoader.(*mockDAGLoader); ok {
				loader.AssertExpectations(t)
			}
		})
	}
}
