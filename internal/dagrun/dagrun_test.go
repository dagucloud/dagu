// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepositoryNormalizesStatusQueries(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 12, 15, 4, 5, 0, time.UTC)
	backend := &recordingDAGRunBackend{}
	repository := dagrun.NewRepository(backend, nil, dagrun.RepositoryOptions{
		Now: func() time.Time { return now },
	})

	_, err := repository.ListStatuses(context.Background())
	require.NoError(t, err)
	assert.Equal(t, dagrun.NewUTC(now.Truncate(24*time.Hour)), backend.statusQuery.From)
	assert.Equal(t, 1000, backend.statusQuery.Limit)

	for _, limit := range []int{-1, 2000} {
		_, err = repository.ListStatuses(context.Background(), dagrun.WithLimit(limit))
		require.NoError(t, err)
		assert.Equal(t, 1000, backend.statusQuery.Limit)
	}

	_, err = repository.ListStatuses(context.Background(), dagrun.WithAllHistory(), dagrun.WithoutLimit())
	require.NoError(t, err)
	assert.True(t, backend.statusQuery.From.IsZero())
	assert.Zero(t, backend.statusQuery.Limit)

	from := dagrun.NewUTC(now.Add(-24 * time.Hour))
	to := dagrun.NewUTC(now)
	statuses := []ir.Status{ir.Succeeded, ir.Failed}
	filter := &workspace.WorkspaceFilter{Enabled: true, Workspaces: []string{"ops"}}
	_, err = repository.ListStatuses(context.Background(),
		dagrun.WithFrom(from),
		dagrun.WithTo(to),
		dagrun.WithStatuses(statuses),
		dagrun.WithExactName("test-dag"),
		dagrun.WithName("partial-name"),
		dagrun.WithDAGRunID("run-123"),
		dagrun.WithLabels([]string{"env=prod"}),
		dagrun.WithWorkspaceFilter(filter),
		dagrun.WithLimit(25),
		dagrun.WithCursor("cursor"),
	)
	require.NoError(t, err)
	assert.Equal(t, dagrun.StatusQuery{
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

	location, err := time.LoadLocation("Asia/Tokyo")
	require.NoError(t, err)
	now := time.Date(2026, 8, 12, 1, 2, 3, 0, time.UTC)
	backend := &recordingDAGRunBackend{}
	repository := dagrun.NewRepository(backend, nil, dagrun.RepositoryOptions{
		LatestStatusToday: true,
		Location:          location,
		Now:               func() time.Time { return now },
	})

	_, err = repository.LatestAttempt(context.Background(), "daily")
	require.NoError(t, err)
	assert.Equal(t, "daily", backend.latestQuery.Name)
	assert.Equal(t, dagrun.NewUTC(time.Date(2026, 8, 12, 0, 0, 0, 0, location)), backend.latestQuery.NotBefore)

	_, err = repository.LatestAttemptAllHistory(context.Background(), "daily")
	require.NoError(t, err)
	assert.Equal(t, "daily", backend.latestQuery.Name)
	assert.True(t, backend.latestQuery.NotBefore.IsZero())

	_, err = repository.RemoveOldDAGRuns(context.Background(), "daily", 7)
	require.NoError(t, err)
	assert.Equal(t, "daily", backend.retentionRequest.Name)
	assert.Equal(t, dagrun.NewUTC(now.AddDate(0, 0, -7)), backend.retentionRequest.OlderThan)

	_, err = repository.RemoveOldDAGRuns(context.Background(), "daily", 0, dagrun.WithRetentionRuns(3))
	require.NoError(t, err)
	assert.Equal(t, 3, backend.retentionRequest.KeepRuns)
	assert.True(t, backend.retentionRequest.OlderThan.IsZero())
}

func TestRepositoryCreatesChildAttemptThroughBackend(t *testing.T) {
	t.Parallel()

	backend := &recordingDAGRunBackend{}
	repository := dagrun.NewRepository(backend, nil, dagrun.RepositoryOptions{})
	dag := &ir.DAG{Name: "child"}
	root := ir.NewDAGRunRef("root", "root-run")
	timestamp := time.Date(2026, 8, 12, 1, 2, 3, 0, time.UTC)

	attempt, err := repository.CreateAttempt(context.Background(), dag, timestamp, "child-run", dagrun.CreateAttemptOptions{
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

	_, err = repository.CreateAttempt(context.Background(), dag, timestamp, "child-run", dagrun.CreateAttemptOptions{
		RootDAGRun: ir.DAGRunRef{Name: "root"},
	})
	require.ErrorIs(t, err, dagrun.ErrDAGRunIDEmpty)
}

func TestRepositoryNormalizesCompareAndSwapRequest(t *testing.T) {
	t.Parallel()

	backend := &recordingDAGRunBackend{
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
	repository := dagrun.NewRepository(backend, nil, dagrun.RepositoryOptions{})
	ref := ir.NewDAGRunRef("daily", "run-1")

	updated, swapped, err := repository.CompareAndSwapLatestAttemptStatus(
		context.Background(),
		ref,
		"attempt-1",
		ir.Queued,
		func(status *ir.DAGRunStatus) error {
			status.Status = ir.Failed
			return nil
		},
	)
	require.NoError(t, err)
	require.True(t, swapped)
	require.NotNil(t, updated)
	assert.Equal(t, ref, backend.compareAndSwapRequest.RootDAGRun)
	assert.Equal(t, ir.Failed, updated.Status)
	assert.Empty(t, updated.Conditions)
}

func TestRepositoryRecentStatusesHidesBackendErrors(t *testing.T) {
	t.Parallel()

	backend := &recordingDAGRunBackend{recentStatusesErr: errors.New("list failed")}
	repository := dagrun.NewRepository(backend, nil, dagrun.RepositoryOptions{})

	assert.Nil(t, repository.RecentStatuses(context.Background(), "daily", 10))
}

func TestRepositoryCleansWorkspacesAfterRunMetadata(t *testing.T) {
	t.Parallel()

	workspaceErr := errors.New("workspace unavailable")
	backend := &recordingDAGRunBackend{
		removedRefs: []ir.DAGRunRef{
			ir.NewDAGRunRef("daily", "run-1"),
			ir.NewDAGRunRef("daily", "run-2"),
		},
	}
	workspaces := &recordingWorkspaceStore{removeErr: workspaceErr}
	repository := dagrun.NewRepository(backend, workspaces, dagrun.RepositoryOptions{})

	removed, err := repository.RemoveOldDAGRuns(context.Background(), "daily", 7)
	assert.Equal(t, []string{"run-1", "run-2"}, removed)
	require.ErrorIs(t, err, workspaceErr)
	assert.Equal(t, []dagrun.WorkspaceRef{
		{RootDAGRun: ir.NewDAGRunRef("daily", "run-1"), DAGRun: ir.NewDAGRunRef("daily", "run-1")},
		{RootDAGRun: ir.NewDAGRunRef("daily", "run-2"), DAGRun: ir.NewDAGRunRef("daily", "run-2")},
	}, workspaces.removed)
}

func TestRepositoryRetentionDryRunDoesNotRemoveWorkspaces(t *testing.T) {
	t.Parallel()

	backend := &recordingDAGRunBackend{
		removedRefs: []ir.DAGRunRef{ir.NewDAGRunRef("daily", "run-1")},
	}
	workspaces := &recordingWorkspaceStore{}
	repository := dagrun.NewRepository(backend, workspaces, dagrun.RepositoryOptions{})

	removed, err := repository.RemoveOldDAGRuns(context.Background(), "daily", 7, dagrun.WithDryRun())
	require.NoError(t, err)
	assert.Equal(t, []string{"run-1"}, removed)
	assert.Empty(t, workspaces.removed)
}

func TestNormalizeStateValueCompactsJSON(t *testing.T) {
	value, err := dagrun.NormalizeStateValue([]byte(`{ "b": 2, "a": 1 }`))
	require.NoError(t, err)
	assert.Equal(t, `{"a":1,"b":2}`, string(value))

	_, err = dagrun.NormalizeStateValue([]byte(`{`))
	require.ErrorIs(t, err, dagrun.ErrInvalidStateValue)
}

func TestNormalizeStateValueRejectsNormalizedValueOverLimit(t *testing.T) {
	raw := []byte(`"` + strings.Repeat("<", dagrun.MaxStateValueBytes/6+1) + `"`)
	assert.Less(t, len(raw), dagrun.MaxStateValueBytes)

	_, err := dagrun.NormalizeStateValue(raw)
	require.ErrorIs(t, err, dagrun.ErrStateValueTooLarge)
}

func TestNormalizeStateValuePreservesNumericPrecision(t *testing.T) {
	value, err := dagrun.NormalizeStateValue([]byte(`{"id":9007199254740993,"decimal":1.2300}`))
	require.NoError(t, err)
	assert.Equal(t, `{"decimal":1.2300,"id":9007199254740993}`, string(value))
}

type recordingDAGRunBackend struct {
	testutil.DAGRunBackendStub
	createRequest         dagrun.CreateAttemptRequest
	latestQuery           dagrun.LatestAttemptQuery
	statusQuery           dagrun.StatusQuery
	retentionRequest      dagrun.RetentionRequest
	recentStatuses        []ir.DAGRunStatus
	recentStatusesErr     error
	compareAndSwapRequest dagrun.CompareAndSwapStatusRequest
	compareAndSwapStatus  *ir.DAGRunStatus
	removedRefs           []ir.DAGRunRef
}

func (s *recordingDAGRunBackend) CreateAttempt(_ context.Context, req dagrun.CreateAttemptRequest) (dagrun.Attempt, error) {
	s.createRequest = req
	return dagrun.NewNoopAttempt(req.AttemptID, req.DAG), nil
}

func (s *recordingDAGRunBackend) RecentStatuses(context.Context, string, int) ([]ir.DAGRunStatus, error) {
	return s.recentStatuses, s.recentStatusesErr
}

func (s *recordingDAGRunBackend) LatestAttempt(_ context.Context, query dagrun.LatestAttemptQuery) (dagrun.Attempt, error) {
	s.latestQuery = query
	return dagrun.NewNoopAttempt("latest", nil), nil
}

func (s *recordingDAGRunBackend) QueryStatuses(_ context.Context, query dagrun.StatusQuery) (dagrun.StatusPage, error) {
	s.statusQuery = query
	return dagrun.StatusPage{}, nil
}

func (s *recordingDAGRunBackend) CompareAndSwapLatestAttemptStatus(
	_ context.Context,
	req dagrun.CompareAndSwapStatusRequest,
) (*ir.DAGRunStatus, bool, error) {
	s.compareAndSwapRequest = req
	if err := req.Mutate(s.compareAndSwapStatus); err != nil {
		return nil, false, err
	}
	return s.compareAndSwapStatus, true, nil
}

func (s *recordingDAGRunBackend) RemoveOldDAGRuns(_ context.Context, req dagrun.RetentionRequest) ([]ir.DAGRunRef, error) {
	s.retentionRequest = req
	return s.removedRefs, nil
}

type recordingWorkspaceStore struct {
	removed   []dagrun.WorkspaceRef
	removeErr error
}

func (*recordingWorkspaceStore) Materialize(context.Context, dagrun.WorkspaceRef) (string, error) {
	return "", nil
}

func (*recordingWorkspaceStore) Snapshot(context.Context, dagrun.WorkspaceRef, string) error {
	return nil
}

func (s *recordingWorkspaceStore) Remove(_ context.Context, ref dagrun.WorkspaceRef) error {
	s.removed = append(s.removed, ref)
	return s.removeErr
}
