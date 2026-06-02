// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package subflow_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/dagucloud/dagu/internal/subflow"
)

func TestLocalCancelRequestsStoredChildAttemptWhenInactive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := exec.NewDAGRunRef("root", "root-run")
	attempt := new(exec.MockDAGRunAttempt)
	attempt.On("Abort", ctx).Return(nil).Once()
	store := &localDAGRunStore{subAttempt: attempt}
	runner := subflow.NewLocal(runtime.Manager{}, nil, subflow.WithLocalDAGRunStore(store))

	err := runner.Cancel(ctx, executor.SubWorkflowCancelRequest{
		DAG:        &core.DAG{Name: "child"},
		RootDAGRun: root,
		RunID:      "child-run",
	})

	require.NoError(t, err)
	require.Equal(t, root, store.findRoot)
	require.Equal(t, "child-run", store.findRunID)
	attempt.AssertExpectations(t)
}

func TestLocalCancelIgnoresMissingStoredChildAttemptWhenInactive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := exec.NewDAGRunRef("root", "root-run")
	store := &localDAGRunStore{}
	runner := subflow.NewLocal(runtime.Manager{}, nil, subflow.WithLocalDAGRunStore(store))

	err := runner.Cancel(ctx, executor.SubWorkflowCancelRequest{
		DAG:        &core.DAG{Name: "child"},
		RootDAGRun: root,
		RunID:      "child-run",
	})

	require.NoError(t, err)
	require.Equal(t, root, store.findRoot)
	require.Equal(t, "child-run", store.findRunID)
}

func TestLocalCancelReturnsStoredChildAttemptLookupError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := exec.NewDAGRunRef("root", "root-run")
	findErr := errors.New("store unavailable")
	store := &localDAGRunStore{findErr: findErr}
	runner := subflow.NewLocal(runtime.Manager{}, nil, subflow.WithLocalDAGRunStore(store))

	err := runner.Cancel(ctx, executor.SubWorkflowCancelRequest{
		DAG:        &core.DAG{Name: "child"},
		RootDAGRun: root,
		RunID:      "child-run",
	})

	require.ErrorIs(t, err, findErr)
	require.ErrorContains(t, err, "failed to find child workflow attempt")
	require.Equal(t, root, store.findRoot)
	require.Equal(t, "child-run", store.findRunID)
}

func TestLocalRetryRejectsMissingRunDatabase(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := exec.NewDAGRunRef("root", "root-run")
	runner := subflow.NewLocal(runtime.Manager{}, nil)

	result, err := runner.Retry(ctx, executor.SubWorkflowRetryRequest{
		SubWorkflowRequest: executor.SubWorkflowRequest{
			DAG:        &core.DAG{Name: "child"},
			RootDAGRun: root,
			RunID:      "child-run",
		},
		StepName: "step-1",
	})

	require.Nil(t, result)
	require.ErrorContains(t, err, "child workflow status database is not configured")
}

func TestLocalRetryReadsStoredChildAttemptStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := exec.NewDAGRunRef("root", "root-run")
	readErr := errors.New("read status failed")
	attempt := new(exec.MockDAGRunAttempt)
	attempt.On("ReadStatus", ctx).Return(nil, readErr).Once()
	store := &localDAGRunStore{subAttempt: attempt}
	runner := subflow.NewLocal(runtime.Manager{}, nil, subflow.WithLocalDAGRunStore(store))

	result, err := runner.Retry(ctx, executor.SubWorkflowRetryRequest{
		SubWorkflowRequest: executor.SubWorkflowRequest{
			DAG:        &core.DAG{Name: "child"},
			RootDAGRun: root,
			RunID:      "child-run",
		},
		StepName: "step-1",
	})

	require.Nil(t, result)
	require.ErrorIs(t, err, readErr)
	require.ErrorContains(t, err, "failed to read child workflow status")
	require.Equal(t, root, store.findRoot)
	require.Equal(t, "child-run", store.findRunID)
	attempt.AssertExpectations(t)
}

type localDAGRunStore struct {
	subAttempt exec.DAGRunAttempt
	findErr    error
	findRoot   exec.DAGRunRef
	findRunID  string
}

func (s *localDAGRunStore) CreateAttempt(context.Context, *core.DAG, time.Time, string, exec.NewDAGRunAttemptOptions) (exec.DAGRunAttempt, error) {
	return nil, nil
}

func (s *localDAGRunStore) RecentAttempts(context.Context, string, int) []exec.DAGRunAttempt {
	return nil
}

func (s *localDAGRunStore) LatestAttempt(context.Context, string) (exec.DAGRunAttempt, error) {
	return nil, exec.ErrDAGRunIDNotFound
}

func (s *localDAGRunStore) ListStatuses(context.Context, ...exec.ListDAGRunStatusesOption) ([]*exec.DAGRunStatus, error) {
	return nil, nil
}

func (s *localDAGRunStore) ListStatusesPage(context.Context, ...exec.ListDAGRunStatusesOption) (exec.DAGRunStatusPage, error) {
	return exec.DAGRunStatusPage{}, nil
}

func (s *localDAGRunStore) CompareAndSwapLatestAttemptStatus(
	context.Context,
	exec.DAGRunRef,
	string,
	core.Status,
	func(*exec.DAGRunStatus) error,
	...exec.CompareAndSwapStatusOption,
) (*exec.DAGRunStatus, bool, error) {
	return nil, false, nil
}

func (s *localDAGRunStore) FindAttempt(context.Context, exec.DAGRunRef) (exec.DAGRunAttempt, error) {
	return nil, exec.ErrDAGRunIDNotFound
}

func (s *localDAGRunStore) FindSubAttempt(_ context.Context, root exec.DAGRunRef, childRunID string) (exec.DAGRunAttempt, error) {
	s.findRoot = root
	s.findRunID = childRunID
	if s.findErr != nil {
		return nil, s.findErr
	}
	if s.subAttempt == nil {
		return nil, exec.ErrDAGRunIDNotFound
	}
	return s.subAttempt, nil
}

func (s *localDAGRunStore) CreateSubAttempt(context.Context, exec.DAGRunRef, string) (exec.DAGRunAttempt, error) {
	return nil, nil
}

func (s *localDAGRunStore) RemoveOldDAGRuns(context.Context, string, int, ...exec.RemoveOldDAGRunsOption) ([]string, error) {
	return nil, nil
}

func (s *localDAGRunStore) RenameDAGRuns(context.Context, string, string) error {
	return nil
}

func (s *localDAGRunStore) RemoveDAGRun(context.Context, exec.DAGRunRef, ...exec.RemoveDAGRunOption) error {
	return nil
}
