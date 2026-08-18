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

func TestRepositoryCleansWorkDirsAfterRunMetadata(t *testing.T) {
	t.Parallel()

	workDirErr := errors.New("work directory unavailable")
	backend := &recordingDAGRunStore{
		removedRefs: []ir.DAGRunRef{
			ir.NewDAGRunRef("daily", "run-1"),
			ir.NewDAGRunRef("daily", "run-2"),
		},
	}
	workDirs := &recordingWorkDirStore{removeErr: workDirErr}
	repository := persis.NewDAGRunRepository(backend, workDirs, persis.DAGRunRepositoryOptions{})

	removed, err := repository.RemoveOldDAGRuns(context.Background(), "daily", 7, persis.DAGRunRetentionOptions{})
	assert.Equal(t, []string{"run-1", "run-2"}, removed)
	require.ErrorIs(t, err, workDirErr)
	assert.Equal(t, []dagrun.WorkDirRef{
		{RootDAGRun: ir.NewDAGRunRef("daily", "run-1"), DAGRun: ir.NewDAGRunRef("daily", "run-1")},
		{RootDAGRun: ir.NewDAGRunRef("daily", "run-2"), DAGRun: ir.NewDAGRunRef("daily", "run-2")},
	}, workDirs.removed)
}

func TestRepositoryRetentionDryRunDoesNotRemoveWorkDirs(t *testing.T) {
	t.Parallel()

	backend := &recordingDAGRunStore{
		removedRefs: []ir.DAGRunRef{ir.NewDAGRunRef("daily", "run-1")},
	}
	workDirs := &recordingWorkDirStore{}
	repository := persis.NewDAGRunRepository(backend, workDirs, persis.DAGRunRepositoryOptions{})

	removed, err := repository.RemoveOldDAGRuns(context.Background(), "daily", 7, persis.DAGRunRetentionOptions{DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"run-1"}, removed)
	assert.Empty(t, workDirs.removed)
}

func TestRepositoryEnqueuesAgentSessionsBeforeRemovingDAGRun(t *testing.T) {
	t.Parallel()

	ref := ir.NewDAGRunRef("daily", "run-1")
	events := []string{}
	backend := &recordingDAGRunStore{
		attempt: &testutil.MockAttempt{Status: &ir.DAGRunStatus{
			AgentSessions: []ir.AgentSessionResource{{
				Provider:      "opencode",
				SessionID:     "session-1",
				Directory:     "/workspace",
				OwnerWorkerID: "worker-1",
				StepName:      "agent",
				Generation:    2,
			}},
		}},
		events: &events,
	}
	enqueuer := &recordingDAGRunRemovalEnqueuer{events: &events}
	repository := persis.NewDAGRunRepository(backend, nil, persis.DAGRunRepositoryOptions{
		RemovalEnqueuer: enqueuer,
	})

	err := repository.RemoveDAGRun(context.Background(), ref, persis.DAGRunRemoveOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"enqueue", "remove"}, events)
	assert.Equal(t, ref, enqueuer.root)
	assert.Equal(t, []ir.AgentSessionResource{{
		Provider:      "opencode",
		SessionID:     "session-1",
		Directory:     "/workspace",
		OwnerWorkerID: "worker-1",
		StepName:      "agent",
		Generation:    2,
	}}, enqueuer.resources)
	assert.Equal(t, persis.DAGRunRemoveRequest{DAGRun: ref}, backend.removeRequest)
}

func TestRepositoryRetentionRemovesExactlyQueuedDAGRuns(t *testing.T) {
	t.Parallel()

	ref := ir.NewDAGRunRef("daily", "run-1")
	events := []string{}
	backend := &recordingDAGRunStore{
		removedRefs: []ir.DAGRunRef{ref},
		attempt:     &testutil.MockAttempt{Status: &ir.DAGRunStatus{}},
		events:      &events,
	}
	repository := persis.NewDAGRunRepository(backend, nil, persis.DAGRunRepositoryOptions{
		RemovalEnqueuer: &recordingDAGRunRemovalEnqueuer{events: &events},
	})

	removed, err := repository.RemoveOldDAGRuns(context.Background(), "daily", 7, persis.DAGRunRetentionOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"run-1"}, removed)
	require.Len(t, backend.retentionRequests, 1)
	assert.True(t, backend.retentionRequests[0].DryRun)
	assert.Equal(t, []persis.DAGRunRemoveRequest{{DAGRun: ref, RejectActive: true}}, backend.removeRequests)
	assert.Equal(t, []string{"enqueue", "remove"}, events)
}

type recordingDAGRunStore struct {
	testutil.DAGRunStoreStub
	createRequest         persis.DAGRunCreateAttemptRequest
	latestQuery           persis.DAGRunLatestAttemptQuery
	statusQuery           persis.DAGRunStatusQuery
	retentionRequest      persis.DAGRunRetentionRequest
	retentionRequests     []persis.DAGRunRetentionRequest
	recentStatuses        []ir.DAGRunStatus
	recentStatusesErr     error
	compareAndSwapRequest persis.DAGRunCompareAndSwapStatusRequest
	compareAndSwapStatus  *ir.DAGRunStatus
	removedRefs           []ir.DAGRunRef
	attempt               dagrun.Attempt
	removeRequest         persis.DAGRunRemoveRequest
	removeRequests        []persis.DAGRunRemoveRequest
	events                *[]string
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
	s.retentionRequests = append(s.retentionRequests, req)
	return s.removedRefs, nil
}

func (s *recordingDAGRunStore) FindAttempt(context.Context, ir.DAGRunRef) (dagrun.Attempt, error) {
	return s.attempt, nil
}

func (s *recordingDAGRunStore) RemoveDAGRun(_ context.Context, req persis.DAGRunRemoveRequest) error {
	s.removeRequest = req
	s.removeRequests = append(s.removeRequests, req)
	if s.events != nil {
		*s.events = append(*s.events, "remove")
	}
	return nil
}

type recordingDAGRunRemovalEnqueuer struct {
	root      ir.DAGRunRef
	resources []ir.AgentSessionResource
	events    *[]string
}

func (e *recordingDAGRunRemovalEnqueuer) EnqueueDAGRunRemoval(
	_ context.Context,
	root ir.DAGRunRef,
	resources []ir.AgentSessionResource,
) error {
	e.root = root
	e.resources = resources
	*e.events = append(*e.events, "enqueue")
	return nil
}

type recordingWorkDirStore struct {
	removed   []dagrun.WorkDirRef
	removeErr error
}

func (*recordingWorkDirStore) Materialize(context.Context, dagrun.WorkDirRef) (string, error) {
	return "", nil
}

func (*recordingWorkDirStore) Snapshot(context.Context, dagrun.WorkDirRef, string) error {
	return nil
}

func (s *recordingWorkDirStore) Remove(_ context.Context, ref dagrun.WorkDirRef) error {
	s.removed = append(s.removed, ref)
	return s.removeErr
}
