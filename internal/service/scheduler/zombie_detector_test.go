// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewZombieDetector(t *testing.T) {
	t.Parallel()

	dagRunStore := &mockDAGRunStore{}
	procStore := &mockProcStore{}

	detector := NewZombieDetector(dagRunStore, procStore, 0, 0)
	require.NotNil(t, detector)
	assert.Equal(t, 45*time.Second, detector.interval)
	assert.Equal(t, 3, detector.failureThreshold)

	detector = NewZombieDetector(dagRunStore, procStore, 60*time.Second, 5)
	require.NotNil(t, detector)
	assert.Equal(t, 60*time.Second, detector.interval)
	assert.Equal(t, 5, detector.failureThreshold)
}

func TestZombieDetectorDetectAndCleanZombies_NoEntries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagRunStore := &mockDAGRunStore{}
	procStore := &mockProcStore{}
	detector := NewZombieDetector(dagRunStore, procStore, time.Second, 1)

	procStore.On("ListAllEntries", ctx).Return([]exec.ProcEntry{}, nil).Once()

	detector.detectAndCleanZombies(ctx)

	procStore.AssertExpectations(t)
	dagRunStore.AssertExpectations(t)
}

func TestZombieDetectorDetectAndCleanZombies_FreshEntrySkipsRepair(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagRunStore := &mockDAGRunStore{}
	procStore := &mockProcStore{}
	detector := NewZombieDetector(dagRunStore, procStore, time.Second, 1)

	entry := testRootProcEntry("queue", "test-dag", "run-1", "attempt-1", true)
	procStore.On("ListAllEntries", ctx).Return([]exec.ProcEntry{entry}, nil).Once()

	detector.detectAndCleanZombies(ctx)

	procStore.AssertExpectations(t)
	dagRunStore.AssertExpectations(t)
}

func TestZombieDetectorDetectAndCleanZombies_StaleEntryRepairsMatchingAttempt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagRunStore := &mockDAGRunStore{}
	procStore := &mockProcStore{}
	detector := NewZombieDetector(dagRunStore, procStore, time.Second, 1)

	dag := &core.DAG{
		Name: "test-dag",
		Steps: []core.Step{
			{Name: "step1"},
		},
	}
	entry := testRootProcEntry(dag.ProcGroup(), dag.Name, "run-1", "attempt-1", false)
	status := &exec.DAGRunStatus{
		Name:      dag.Name,
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
		Status:    core.Running,
		Nodes:     exec.NewNodesFromSteps(dag.Steps),
	}
	status.Nodes[0].Status = core.NodeRunning
	attempt := &exec.MockDAGRunAttempt{}

	procStore.On("ListAllEntries", ctx).Return([]exec.ProcEntry{entry}, nil).Once()
	dagRunStore.On("FindAttempt", mock.Anything, exec.NewDAGRunRef(dag.Name, "run-1")).Return(attempt, nil).Once()
	attempt.On("ReadStatus", mock.Anything).Return(status, nil).Twice()
	attempt.On("ReadDAG", mock.Anything).Return(dag, nil).Once()
	attempt.On("Open", mock.Anything).Return(nil).Once()
	attempt.On("Write", mock.Anything, mock.MatchedBy(func(s exec.DAGRunStatus) bool {
		return s.Status == core.Failed &&
			s.AttemptID == status.AttemptID &&
			len(s.Nodes) == 1 &&
			s.Nodes[0].Status == core.NodeFailed
	})).Return(nil).Once()
	attempt.On("Close", mock.Anything).Return(nil).Once()
	procStore.On("RemoveIfStale", mock.Anything, entry).Return(nil).Once()

	detector.detectAndCleanZombies(ctx)

	procStore.AssertExpectations(t)
	dagRunStore.AssertExpectations(t)
	attempt.AssertExpectations(t)
}

func TestZombieDetectorDetectAndCleanZombies_StaleEntryWithFreshSiblingRemovesOnlyStale(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagRunStore := &mockDAGRunStore{}
	procStore := &mockProcStore{}
	detector := NewZombieDetector(dagRunStore, procStore, time.Second, 1)

	staleEntry := testRootProcEntry("queue", "test-dag", "run-1", "attempt-1", false)
	freshEntry := testRootProcEntry("queue", "test-dag", "run-1", "attempt-2", true)

	procStore.On("ListAllEntries", ctx).Return([]exec.ProcEntry{staleEntry, freshEntry}, nil).Once()
	procStore.On("RemoveIfStale", mock.Anything, staleEntry).Return(nil).Once()

	detector.detectAndCleanZombies(ctx)

	procStore.AssertExpectations(t)
	dagRunStore.AssertExpectations(t)
}

func TestZombieDetectorDetectAndCleanZombies_SubDAGUsesRootScopedLookup(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagRunStore := &mockDAGRunStore{}
	procStore := &mockProcStore{}
	detector := NewZombieDetector(dagRunStore, procStore, time.Second, 1)

	dag := &core.DAG{
		Name: "child",
		Steps: []core.Step{
			{Name: "child-step"},
		},
	}
	entry := exec.ProcEntry{
		GroupName: dag.ProcGroup(),
		FilePath:  "/tmp/stale-sub.proc",
		Meta: exec.ProcMeta{
			StartedAt:    time.Now().Add(-time.Minute).Unix(),
			Name:         dag.Name,
			DAGRunID:     "sub-1",
			AttemptID:    "attempt-1",
			RootName:     "root",
			RootDAGRunID: "root-1",
		},
		Fresh: false,
	}
	status := &exec.DAGRunStatus{
		Name:      dag.Name,
		DAGRunID:  "sub-1",
		AttemptID: "attempt-1",
		Status:    core.Running,
		Nodes:     exec.NewNodesFromSteps(dag.Steps),
	}
	status.Nodes[0].Status = core.NodeRunning
	attempt := &exec.MockDAGRunAttempt{}

	procStore.On("ListAllEntries", ctx).Return([]exec.ProcEntry{entry}, nil).Once()
	dagRunStore.On("FindSubAttempt", mock.Anything, exec.NewDAGRunRef("root", "root-1"), "sub-1").Return(attempt, nil).Once()
	attempt.On("ReadStatus", mock.Anything).Return(status, nil).Twice()
	attempt.On("ReadDAG", mock.Anything).Return(dag, nil).Once()
	attempt.On("Open", mock.Anything).Return(nil).Once()
	attempt.On("Write", mock.Anything, mock.MatchedBy(func(s exec.DAGRunStatus) bool {
		return s.Status == core.Failed && s.AttemptID == status.AttemptID
	})).Return(nil).Once()
	attempt.On("Close", mock.Anything).Return(nil).Once()
	procStore.On("RemoveIfStale", mock.Anything, entry).Return(nil).Once()

	detector.detectAndCleanZombies(ctx)

	procStore.AssertExpectations(t)
	dagRunStore.AssertExpectations(t)
	attempt.AssertExpectations(t)
}

func TestZombieDetectorDetectAndCleanZombies_AttemptCounterDoesNotCarryAcrossRetries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagRunStore := &mockDAGRunStore{}
	procStore := &mockProcStore{}
	detector := NewZombieDetector(dagRunStore, procStore, time.Second, 2)

	firstAttempt := testRootProcEntry("queue", "test-dag", "run-1", "attempt-1", false)
	secondAttempt := testRootProcEntry("queue", "test-dag", "run-1", "attempt-2", false)

	procStore.On("ListAllEntries", ctx).Return([]exec.ProcEntry{firstAttempt}, nil).Once()
	detector.detectAndCleanZombies(ctx)

	procStore.On("ListAllEntries", ctx).Return([]exec.ProcEntry{secondAttempt}, nil).Once()
	detector.detectAndCleanZombies(ctx)

	dagRunStore.AssertNotCalled(t, "FindAttempt", mock.Anything, mock.Anything)
	procStore.AssertExpectations(t)
}

func TestZombieDetectorDetectAndCleanZombies_OrphanedStaleEntryIsRemoved(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagRunStore := &mockDAGRunStore{}
	procStore := &mockProcStore{}
	detector := NewZombieDetector(dagRunStore, procStore, time.Second, 1)

	entry := testRootProcEntry("queue", "test-dag", "run-1", "attempt-1", false)

	procStore.On("ListAllEntries", ctx).Return([]exec.ProcEntry{entry}, nil).Once()
	dagRunStore.On("FindAttempt", mock.Anything, exec.NewDAGRunRef("test-dag", "run-1")).Return(nil, exec.ErrDAGRunIDNotFound).Once()
	procStore.On("RemoveIfStale", mock.Anything, entry).Return(nil).Once()

	detector.detectAndCleanZombies(ctx)

	procStore.AssertExpectations(t)
	dagRunStore.AssertExpectations(t)
}

func TestZombieDetectorDetectAndCleanZombies_StaleEntryWithMissingStatusIsRemoved(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagRunStore := &mockDAGRunStore{}
	procStore := &mockProcStore{}
	detector := NewZombieDetector(dagRunStore, procStore, time.Second, 1)

	entry := testRootProcEntry("queue", "test-dag", "run-1", "attempt-1", false)

	procStore.On("ListAllEntries", ctx).Return([]exec.ProcEntry{entry}, nil).Once()
	dagRunStore.On("FindAttempt", mock.Anything, exec.NewDAGRunRef("test-dag", "run-1")).Return(nil, exec.ErrNoStatusData).Once()
	procStore.On("RemoveIfStale", mock.Anything, entry).Return(nil).Once()

	detector.detectAndCleanZombies(ctx)

	procStore.AssertExpectations(t)
	dagRunStore.AssertExpectations(t)
}

func TestZombieDetectorDetectAndCleanZombies_StaleEntryWithCorruptedStatusIsRemoved(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagRunStore := &mockDAGRunStore{}
	procStore := &mockProcStore{}
	detector := NewZombieDetector(dagRunStore, procStore, time.Second, 1)

	entry := testRootProcEntry("queue", "test-dag", "run-1", "attempt-1", false)

	procStore.On("ListAllEntries", ctx).Return([]exec.ProcEntry{entry}, nil).Once()
	dagRunStore.On("FindAttempt", mock.Anything, exec.NewDAGRunRef("test-dag", "run-1")).Return(nil, exec.ErrCorruptedStatusFile).Once()
	procStore.On("RemoveIfStale", mock.Anything, entry).Return(nil).Once()

	detector.detectAndCleanZombies(ctx)

	procStore.AssertExpectations(t)
	dagRunStore.AssertExpectations(t)
}

func testRootProcEntry(groupName, dagName, dagRunID, attemptID string, fresh bool) exec.ProcEntry {
	return exec.ProcEntry{
		GroupName: groupName,
		FilePath:  "/tmp/" + dagRunID + "_" + attemptID + ".proc",
		Meta: exec.ProcMeta{
			StartedAt:    time.Now().Add(-time.Minute).Unix(),
			Name:         dagName,
			DAGRunID:     dagRunID,
			AttemptID:    attemptID,
			RootName:     dagName,
			RootDAGRunID: dagRunID,
		},
		LastHeartbeatAt: time.Now().Add(-2 * time.Minute).Unix(),
		Fresh:           fresh,
	}
}

var _ exec.DAGRunStore = (*mockDAGRunStore)(nil)

type mockDAGRunStore struct {
	mock.Mock
}

func (m *mockDAGRunStore) CreateAttempt(ctx context.Context, dag *core.DAG, ts time.Time, dagRunID string, opts exec.NewDAGRunAttemptOptions) (exec.DAGRunAttempt, error) {
	args := m.Called(ctx, dag, ts, dagRunID, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(exec.DAGRunAttempt), args.Error(1)
}

func (m *mockDAGRunStore) RecentAttempts(ctx context.Context, name string, itemLimit int) []exec.DAGRunAttempt {
	args := m.Called(ctx, name, itemLimit)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).([]exec.DAGRunAttempt)
}

func (m *mockDAGRunStore) LatestAttempt(ctx context.Context, name string) (exec.DAGRunAttempt, error) {
	args := m.Called(ctx, name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(exec.DAGRunAttempt), args.Error(1)
}

func (m *mockDAGRunStore) ListStatuses(ctx context.Context, opts ...exec.ListDAGRunStatusesOption) ([]*exec.DAGRunStatus, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*exec.DAGRunStatus), args.Error(1)
}

func (m *mockDAGRunStore) ListStatusesPage(ctx context.Context, opts ...exec.ListDAGRunStatusesOption) (exec.DAGRunStatusPage, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return exec.DAGRunStatusPage{}, args.Error(1)
	}
	return args.Get(0).(exec.DAGRunStatusPage), args.Error(1)
}

func (m *mockDAGRunStore) CompareAndSwapLatestAttemptStatus(
	ctx context.Context,
	dagRun exec.DAGRunRef,
	expectedAttemptID string,
	expectedStatus core.Status,
	_ func(*exec.DAGRunStatus) error,
	_ ...exec.CompareAndSwapStatusOption,
) (*exec.DAGRunStatus, bool, error) {
	args := m.Called(ctx, dagRun, expectedAttemptID, expectedStatus, mock.Anything)
	if args.Get(0) == nil {
		return nil, args.Bool(1), args.Error(2)
	}
	return args.Get(0).(*exec.DAGRunStatus), args.Bool(1), args.Error(2)
}

func (m *mockDAGRunStore) FindAttempt(ctx context.Context, dagRun exec.DAGRunRef) (exec.DAGRunAttempt, error) {
	args := m.Called(ctx, dagRun)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(exec.DAGRunAttempt), args.Error(1)
}

func (m *mockDAGRunStore) FindSubAttempt(ctx context.Context, dagRun exec.DAGRunRef, subDAGRunID string) (exec.DAGRunAttempt, error) {
	args := m.Called(ctx, dagRun, subDAGRunID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(exec.DAGRunAttempt), args.Error(1)
}

func (m *mockDAGRunStore) CreateSubAttempt(ctx context.Context, rootRef exec.DAGRunRef, subDAGRunID string) (exec.DAGRunAttempt, error) {
	args := m.Called(ctx, rootRef, subDAGRunID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(exec.DAGRunAttempt), args.Error(1)
}

func (m *mockDAGRunStore) RemoveOldDAGRuns(ctx context.Context, name string, retentionDays int, opts ...exec.RemoveOldDAGRunsOption) ([]string, error) {
	args := m.Called(ctx, name, retentionDays, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

func (m *mockDAGRunStore) RenameDAGRuns(ctx context.Context, oldName, newName string) error {
	args := m.Called(ctx, oldName, newName)
	return args.Error(0)
}

func (m *mockDAGRunStore) RemoveDAGRun(ctx context.Context, dagRun exec.DAGRunRef, _ ...exec.RemoveDAGRunOption) error {
	args := m.Called(ctx, dagRun)
	return args.Error(0)
}

var _ exec.ProcStore = (*mockProcStore)(nil)

type mockProcStore struct {
	mock.Mock
}

func (m *mockProcStore) Lock(_ context.Context, _ string) error { return nil }

func (m *mockProcStore) Unlock(_ context.Context, _ string) {}

func (m *mockProcStore) Acquire(ctx context.Context, groupName string, meta exec.ProcMeta) (exec.ProcHandle, error) {
	args := m.Called(ctx, groupName, meta)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(exec.ProcHandle), args.Error(1)
}

func (m *mockProcStore) CountAlive(ctx context.Context, groupName string) (int, error) {
	args := m.Called(ctx, groupName)
	return args.Int(0), args.Error(1)
}

func (m *mockProcStore) CountAliveByDAGName(ctx context.Context, groupName, dagName string) (int, error) {
	args := m.Called(ctx, groupName, dagName)
	return args.Int(0), args.Error(1)
}

func (m *mockProcStore) IsRunAlive(ctx context.Context, groupName string, dagRun exec.DAGRunRef) (bool, error) {
	args := m.Called(ctx, groupName, dagRun)
	return args.Bool(0), args.Error(1)
}

func (m *mockProcStore) IsAttemptAlive(ctx context.Context, groupName string, dagRun exec.DAGRunRef, attemptID string) (bool, error) {
	args := m.Called(ctx, groupName, dagRun, attemptID)
	return args.Bool(0), args.Error(1)
}

func (m *mockProcStore) ListAlive(ctx context.Context, groupName string) ([]exec.DAGRunRef, error) {
	args := m.Called(ctx, groupName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]exec.DAGRunRef), args.Error(1)
}

func (m *mockProcStore) ListAllAlive(ctx context.Context) (map[string][]exec.DAGRunRef, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string][]exec.DAGRunRef), args.Error(1)
}

func (m *mockProcStore) ListEntries(ctx context.Context, groupName string) ([]exec.ProcEntry, error) {
	args := m.Called(ctx, groupName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]exec.ProcEntry), args.Error(1)
}

func (m *mockProcStore) LatestFreshEntryByDAGName(ctx context.Context, groupName, dagName string) (*exec.ProcEntry, error) {
	args := m.Called(ctx, groupName, dagName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	if entry, ok := args.Get(0).(*exec.ProcEntry); ok {
		return entry, args.Error(1)
	}
	entry := args.Get(0).(exec.ProcEntry)
	return &entry, args.Error(1)
}

func (m *mockProcStore) ListAllEntries(ctx context.Context) ([]exec.ProcEntry, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]exec.ProcEntry), args.Error(1)
}

func (m *mockProcStore) RemoveIfStale(ctx context.Context, entry exec.ProcEntry) error {
	args := m.Called(ctx, entry)
	return args.Error(0)
}
