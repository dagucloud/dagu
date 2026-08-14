// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepositoryNormalizesStatusQueries(t *testing.T) {
	t.Parallel()

	location := time.FixedZone("UTC-7", -7*60*60)
	now := time.Date(2026, 8, 12, 5, 4, 3, 0, time.UTC)
	backend := &recordingDAGRunStore{}
	repository := persis.NewDAGRunRepository(backend, nil, persis.DAGRunRepositoryOptions{
		Location: location,
		Now:      func() time.Time { return now },
	})

	_, err := repository.ListStatuses(context.Background(), persis.DAGRunListOptions{})
	require.NoError(t, err)
	assert.Equal(t, persis.NewUTC(time.Date(2026, 8, 11, 0, 0, 0, 0, location)), backend.statusQuery.From)
	assert.Equal(t, 1000, backend.statusQuery.Limit)

	for _, limit := range []int{-1, 2000} {
		_, err = repository.ListStatuses(context.Background(), persis.DAGRunListOptions{Limit: limit})
		require.NoError(t, err)
		assert.Equal(t, 1000, backend.statusQuery.Limit)
	}

	_, err = repository.ListStatuses(context.Background(), persis.DAGRunListOptions{AllHistory: true, Unbounded: true})
	require.NoError(t, err)
	assert.True(t, backend.statusQuery.From.IsZero())
	assert.Zero(t, backend.statusQuery.Limit)

	from := persis.NewUTC(now.Add(-24 * time.Hour))
	to := persis.NewUTC(now)
	statuses := []ir.Status{ir.Succeeded, ir.Failed}
	filter := &workspace.WorkspaceFilter{Enabled: true, Workspaces: []string{"ops"}}
	_, err = repository.ListStatuses(context.Background(), persis.DAGRunListOptions{From: from, To: to, Statuses: statuses, ExactName: "test-dag", Name: "partial-name", DAGRunID: "run-123", Labels: []string{"env=prod"}, WorkspaceFilter: filter, Limit: 25, Cursor: "cursor"})
	require.NoError(t, err)
	assert.Equal(t, persis.DAGRunStatusQuery{
		DAGRunID:        "run-123",
		Name:            "partial-name",
		ExactName:       "test-dag",
		From:            from,
		To:              to,
		Statuses:        statuses,
		Limit:           25,
		Cursor:          "cursor",
		Labels:          []string{"env=prod"},
		WorkspaceFilter: filter,
	}, backend.statusQuery)
}

func TestRepositoryNormalizesLatestAndRetentionRequests(t *testing.T) {
	t.Parallel()

	location := time.FixedZone("JST", 9*60*60)
	now := time.Date(2026, 8, 12, 1, 2, 3, 0, time.UTC)
	backend := &recordingDAGRunStore{}
	repository := persis.NewDAGRunRepository(backend, nil, persis.DAGRunRepositoryOptions{
		LatestStatusToday: true,
		Location:          location,
		Now:               func() time.Time { return now },
	})

	_, err := repository.LatestAttempt(context.Background(), "daily", persis.DAGRunLatestAttemptOptions{})
	require.NoError(t, err)
	assert.Equal(t, "daily", backend.latestQuery.Name)
	assert.Equal(t, persis.NewUTC(time.Date(2026, 8, 12, 0, 0, 0, 0, location)), backend.latestQuery.NotBefore)

	_, err = repository.LatestAttempt(context.Background(), "daily", persis.DAGRunLatestAttemptOptions{AllHistory: true})
	require.NoError(t, err)
	assert.Equal(t, "daily", backend.latestQuery.Name)
	assert.True(t, backend.latestQuery.NotBefore.IsZero())

	_, err = repository.RemoveOldDAGRuns(context.Background(), "daily", 7, persis.DAGRunRetentionOptions{})
	require.NoError(t, err)
	assert.Equal(t, "daily", backend.retentionRequest.Name)
	assert.Equal(t, persis.NewUTC(now.AddDate(0, 0, -7)), backend.retentionRequest.OlderThan)

	retentionRuns := 3
	_, err = repository.RemoveOldDAGRuns(context.Background(), "daily", 0, persis.DAGRunRetentionOptions{
		RetentionRuns: &retentionRuns,
	})
	require.NoError(t, err)
	assert.Equal(t, 3, backend.retentionRequest.KeepRuns)
	assert.True(t, backend.retentionRequest.OlderThan.IsZero())
}

func TestRepositoryCreatesChildAttemptThroughBackend(t *testing.T) {
	t.Parallel()

	backend := &recordingDAGRunStore{}
	repository := persis.NewDAGRunRepository(backend, nil, persis.DAGRunRepositoryOptions{})
	dag := &ir.DAG{Name: "child"}
	root := ir.NewDAGRunRef("root", "root-run")
	timestamp := time.Date(2026, 8, 12, 1, 2, 3, 0, time.UTC)

	attempt, err := repository.CreateAttempt(context.Background(), dag, timestamp, "child-run", persis.DAGRunCreateAttemptOptions{
		RootDAGRun: root,
		Retry:      true,
		AttemptID:  "attempt-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "attempt-1", attempt.ID())
	assert.Same(t, dag, backend.createRequest.DAG)
	assert.Equal(t, timestamp, backend.createRequest.Timestamp)
	assert.Equal(t, "child-run", backend.createRequest.DAGRunID)
	assert.Equal(t, root, backend.createRequest.RootDAGRun)
	assert.True(t, backend.createRequest.Retry)
	assert.Equal(t, "attempt-1", backend.createRequest.AttemptID)

	_, err = repository.CreateAttempt(context.Background(), dag, timestamp, "child-run", persis.DAGRunCreateAttemptOptions{
		RootDAGRun: ir.DAGRunRef{Name: "root"},
	})
	require.ErrorIs(t, err, dagrun.ErrDAGRunIDEmpty)
}

func TestRepositoryNormalizesCompareAndSwapRequest(t *testing.T) {
	t.Parallel()

	backend := &recordingDAGRunStore{
		compareAndSwapStatus: &ir.DAGRunStatus{
			Name:      "daily",
			DAGRunID:  "run-1",
			AttemptID: "attempt-1",
			Status:    ir.Queued,
			Conditions: []ir.DAGRunCondition{
				ir.NewDAGRunCondition("Runnable", "False", "Blocked", "Waiting", time.Now()),
			},
		},
	}
	repository := persis.NewDAGRunRepository(backend, nil, persis.DAGRunRepositoryOptions{})
	ref := ir.NewDAGRunRef("daily", "run-1")

	updated, swapped, err := repository.CompareAndSwapLatestAttemptStatus(
		context.Background(),
		ref,
		"attempt-1",
		ir.Queued,
		func(status *ir.DAGRunStatus) error {
			status.Status = ir.Failed
			return nil
		}, persis.DAGRunCompareAndSwapOptions{},
	)
	require.NoError(t, err)
	require.True(t, swapped)
	require.NotNil(t, updated)
	assert.Equal(t, ref, backend.compareAndSwapRequest.RootDAGRun)
	assert.Equal(t, ir.Failed, updated.Status)
	assert.Empty(t, updated.Conditions)
}

func TestRepositoryRecentStatusesReturnsStoreErrors(t *testing.T) {
	t.Parallel()

	backend := &recordingDAGRunStore{recentStatusesErr: errors.New("list failed")}
	repository := persis.NewDAGRunRepository(backend, nil, persis.DAGRunRepositoryOptions{})

	statuses, err := repository.RecentStatuses(context.Background(), "daily", 10)
	assert.Nil(t, statuses)
	require.EqualError(t, err, "list failed")
}

func TestRepositoryCleansWorkspacesAfterRunMetadata(t *testing.T) {
	t.Parallel()

	workspaceErr := errors.New("workspace unavailable")
	backend := &recordingDAGRunStore{
		removedRefs: []ir.DAGRunRef{
			ir.NewDAGRunRef("daily", "run-1"),
			ir.NewDAGRunRef("daily", "run-2"),
		},
	}
	workspaces := &recordingDAGRunWorkspaceStore{removeErr: workspaceErr}
	repository := persis.NewDAGRunRepository(backend, workspaces, persis.DAGRunRepositoryOptions{})

	removed, err := repository.RemoveOldDAGRuns(context.Background(), "daily", 7, persis.DAGRunRetentionOptions{})
	assert.Equal(t, []string{"run-1", "run-2"}, removed)
	require.ErrorIs(t, err, workspaceErr)
	assert.Equal(t, []dagrun.DAGRunWorkspaceRef{
		{RootDAGRun: ir.NewDAGRunRef("daily", "run-1"), DAGRun: ir.NewDAGRunRef("daily", "run-1")},
		{RootDAGRun: ir.NewDAGRunRef("daily", "run-2"), DAGRun: ir.NewDAGRunRef("daily", "run-2")},
	}, workspaces.removed)
}

func TestRepositoryRetentionDryRunDoesNotRemoveWorkspaces(t *testing.T) {
	t.Parallel()

	backend := &recordingDAGRunStore{
		removedRefs: []ir.DAGRunRef{ir.NewDAGRunRef("daily", "run-1")},
	}
	workspaces := &recordingDAGRunWorkspaceStore{}
	repository := persis.NewDAGRunRepository(backend, workspaces, persis.DAGRunRepositoryOptions{})

	removed, err := repository.RemoveOldDAGRuns(context.Background(), "daily", 7, persis.DAGRunRetentionOptions{DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"run-1"}, removed)
	assert.Empty(t, workspaces.removed)
}

type recordingDAGRunStore struct {
	testutil.DAGRunStoreStub
	createRequest         persis.DAGRunCreateAttemptRequest
	latestQuery           persis.DAGRunLatestAttemptQuery
	statusQuery           persis.DAGRunStatusQuery
	retentionRequest      persis.DAGRunRetentionRequest
	recentStatuses        []ir.DAGRunStatus
	recentStatusesErr     error
	compareAndSwapRequest persis.DAGRunCompareAndSwapStatusRequest
	compareAndSwapStatus  *ir.DAGRunStatus
	removedRefs           []ir.DAGRunRef
}

func (s *recordingDAGRunStore) CreateAttempt(_ context.Context, req persis.DAGRunCreateAttemptRequest) (dagrun.Attempt, error) {
	s.createRequest = req
	return dagrun.NewNoopAttempt(req.AttemptID, req.DAG), nil
}

func (s *recordingDAGRunStore) RecentStatuses(context.Context, string, int) ([]ir.DAGRunStatus, error) {
	return s.recentStatuses, s.recentStatusesErr
}

func (s *recordingDAGRunStore) LatestAttempt(_ context.Context, query persis.DAGRunLatestAttemptQuery) (dagrun.Attempt, error) {
	s.latestQuery = query
	return dagrun.NewNoopAttempt("latest", nil), nil
}

func (s *recordingDAGRunStore) QueryStatuses(_ context.Context, query persis.DAGRunStatusQuery) (persis.DAGRunStatusPage, error) {
	s.statusQuery = query
	return persis.DAGRunStatusPage{}, nil
}

func (s *recordingDAGRunStore) CompareAndSwapLatestAttemptStatus(
	_ context.Context,
	req persis.DAGRunCompareAndSwapStatusRequest,
) (*ir.DAGRunStatus, bool, error) {
	s.compareAndSwapRequest = req
	if err := req.Mutate(s.compareAndSwapStatus); err != nil {
		return nil, false, err
	}
	return s.compareAndSwapStatus, true, nil
}

func (s *recordingDAGRunStore) RemoveOldDAGRuns(_ context.Context, req persis.DAGRunRetentionRequest) ([]ir.DAGRunRef, error) {
	s.retentionRequest = req
	return s.removedRefs, nil
}

type recordingDAGRunWorkspaceStore struct {
	removed   []dagrun.DAGRunWorkspaceRef
	removeErr error
}

func (*recordingDAGRunWorkspaceStore) Materialize(context.Context, dagrun.DAGRunWorkspaceRef) (string, error) {
	return "", nil
}

func (*recordingDAGRunWorkspaceStore) Snapshot(context.Context, dagrun.DAGRunWorkspaceRef, string) error {
	return nil
}

func (s *recordingDAGRunWorkspaceStore) Remove(_ context.Context, ref dagrun.DAGRunWorkspaceRef) error {
	s.removed = append(s.removed, ref)
	return s.removeErr
}
