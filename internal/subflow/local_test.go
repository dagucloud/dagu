// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package subflow_test

import (
	"context"
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

type localDAGRunStore struct {
	subAttempt exec.DAGRunAttempt
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
