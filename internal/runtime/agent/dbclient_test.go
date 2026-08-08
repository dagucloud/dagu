// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dagstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var _ dagstore.DAGStore = (*mockDAGStore)(nil)

// mockDAGStore implements models.DAGStore
type mockDAGStore struct {
	mock.Mock
}

func (m *mockDAGStore) Create(ctx context.Context, fileName string, spec []byte) error {
	args := m.Called(ctx, fileName, spec)
	return args.Error(0)
}

func (m *mockDAGStore) Delete(ctx context.Context, fileName string) error {
	args := m.Called(ctx, fileName)
	return args.Error(0)
}

func (m *mockDAGStore) List(ctx context.Context, params dagstore.ListDAGsOptions) (pagination.PaginatedResult[*ir.DAG], []string, error) {
	args := m.Called(ctx, params)
	return args.Get(0).(pagination.PaginatedResult[*ir.DAG]), args.Get(1).([]string), args.Error(2)
}

func (m *mockDAGStore) GetMetadata(ctx context.Context, fileName string) (*ir.DAG, error) {
	args := m.Called(ctx, fileName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ir.DAG), args.Error(1)
}

func (m *mockDAGStore) GetDetails(ctx context.Context, fileName string, opts dagstore.DAGLoadOptions) (*ir.DAG, error) {
	args := m.Called(ctx, fileName, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ir.DAG), args.Error(1)
}

func (m *mockDAGStore) Grep(ctx context.Context, pattern string) ([]*dagstore.GrepDAGsResult, []string, error) {
	args := m.Called(ctx, pattern)
	return args.Get(0).([]*dagstore.GrepDAGsResult), args.Get(1).([]string), args.Error(2)
}

func (m *mockDAGStore) SearchCursor(ctx context.Context, opts dagstore.SearchDAGsOptions) (*pagination.CursorResult[dagstore.SearchDAGResult], []string, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return nil, args.Get(1).([]string), args.Error(2)
	}
	return args.Get(0).(*pagination.CursorResult[dagstore.SearchDAGResult]), args.Get(1).([]string), args.Error(2)
}

func (m *mockDAGStore) SearchMatches(ctx context.Context, fileName string, opts dagstore.SearchDAGMatchesOptions) (*pagination.CursorResult[*dagstore.Match], error) {
	args := m.Called(ctx, fileName, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*pagination.CursorResult[*dagstore.Match]), args.Error(1)
}

func (m *mockDAGStore) Rename(ctx context.Context, oldID, newID string) error {
	args := m.Called(ctx, oldID, newID)
	return args.Error(0)
}

func (m *mockDAGStore) GetSpec(ctx context.Context, fileName string) (string, error) {
	args := m.Called(ctx, fileName)
	return args.Get(0).(string), args.Error(1)
}

func (m *mockDAGStore) UpdateSpec(ctx context.Context, fileName string, spec []byte) error {
	args := m.Called(ctx, fileName, spec)
	return args.Error(0)
}

func (m *mockDAGStore) LoadSpec(ctx context.Context, source []byte, _ string, opts dagstore.DAGLoadOptions) (*ir.DAG, error) {
	args := m.Called(ctx, source, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ir.DAG), args.Error(1)
}

func (m *mockDAGStore) LabelList(ctx context.Context) ([]string, []string, error) {
	args := m.Called(ctx)
	return args.Get(0).([]string), args.Get(1).([]string), args.Error(2)
}

func (m *mockDAGStore) ToggleSuspend(ctx context.Context, fileName string, suspend bool) error {
	args := m.Called(ctx, fileName, suspend)
	return args.Error(0)
}

func (m *mockDAGStore) IsSuspended(ctx context.Context, fileName string) bool {
	args := m.Called(ctx, fileName)
	return args.Bool(0)
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

func (m *mockDAGRunStore) ListStatuses(ctx context.Context, opts ...dagrun.ListDAGRunStatusesOption) ([]*dagrun.DAGRunStatus, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*dagrun.DAGRunStatus), args.Error(1)
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
	mutate func(*dagrun.DAGRunStatus) error,
	_ ...dagrun.CompareAndSwapStatusOption,
) (*dagrun.DAGRunStatus, bool, error) {
	args := m.Called(ctx, dagRun, expectedAttemptID, expectedStatus, mutate)
	if args.Get(0) == nil {
		return nil, args.Bool(1), args.Error(2)
	}
	return args.Get(0).(*dagrun.DAGRunStatus), args.Bool(1), args.Error(2)
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

	// Helper to create a mock DAG store with pre-set GetDetails expectations.
	setupMockDS := func(name string, dag *ir.DAG, err error) *mockDAGStore {
		m := new(mockDAGStore)
		m.On("GetDetails", mock.Anything, name, mock.Anything).Return(dag, err)
		return m
	}

	tests := []struct {
		name              string
		ds                dagstore.DAGStore // nil means no local store
		remoteLoader      RemoteDAGLoader   // nil means no remote loader
		expectDAG         *ir.DAG
		expectError       bool
		expectErrContains string
	}{
		{
			name:         "local hit returns dag",
			ds:           setupMockDS("test-dag", testDAG, nil),
			remoteLoader: nil,
			expectDAG:    testDAG,
			expectError:  false,
		},
		{
			name: "local not-found + remote hit",
			ds:   setupMockDS("test-dag", nil, dagstore.ErrDAGNotFound),
			remoteLoader: func(ctx context.Context, name string) (*ir.DAG, error) {
				return testDAG, nil
			},
			expectDAG:   testDAG,
			expectError: false,
		},
		{
			name: "local not-found + remote returns nil",
			ds:   setupMockDS("test-dag", nil, dagstore.ErrDAGNotFound),
			remoteLoader: func(ctx context.Context, name string) (*ir.DAG, error) {
				return nil, nil
			},
			expectError:       true,
			expectErrContains: "DAG is not found",
		},
		{
			name: "local not-found + remote returns error",
			ds:   setupMockDS("test-dag", nil, dagstore.ErrDAGNotFound),
			remoteLoader: func(ctx context.Context, name string) (*ir.DAG, error) {
				return nil, errors.New("remote unavailable")
			},
			expectError:       true,
			expectErrContains: "DAG is not found",
		},
		{
			name:              "local not-found + no remote loader",
			ds:                setupMockDS("test-dag", nil, dagstore.ErrDAGNotFound),
			remoteLoader:      nil,
			expectError:       true,
			expectErrContains: "DAG is not found",
		},
		{
			name: "local non-not-found error propagates immediately",
			ds:   setupMockDS("test-dag", nil, errors.New("permission denied")),
			remoteLoader: func(ctx context.Context, name string) (*ir.DAG, error) {
				return testDAG, nil // should NOT be called
			},
			expectError:       true,
			expectErrContains: "permission denied",
		},
		{
			name: "nil ds + remote hit",
			ds:   nil,
			remoteLoader: func(ctx context.Context, name string) (*ir.DAG, error) {
				return testDAG, nil
			},
			expectDAG:   testDAG,
			expectError: false,
		},
		{
			name: "nil ds + remote returns nil dag",
			ds:   nil,
			remoteLoader: func(ctx context.Context, name string) (*ir.DAG, error) {
				return nil, nil
			},
			expectError:       true,
			expectErrContains: "not found locally or remotely",
		},
		{
			name: "nil ds + remote returns error",
			ds:   nil,
			remoteLoader: func(ctx context.Context, name string) (*ir.DAG, error) {
				return nil, errors.New("remote unavailable")
			},
			expectError:       true,
			expectErrContains: "remote DAG load failed",
		},
		{
			name:              "nil ds + no remote loader",
			ds:                nil,
			remoteLoader:      nil,
			expectError:       true,
			expectErrContains: "no local DAG store and no remote loader",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mockDRS := new(mockDAGRunStore)
			client := newDBClient(mockDRS, tt.ds, tt.remoteLoader)

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
			if mockDS, ok := tt.ds.(*mockDAGStore); ok {
				mockDS.AssertExpectations(t)
			}
		})
	}
}
