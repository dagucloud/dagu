// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/procutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/proc"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNewZombieDetector(t *testing.T) {
	t.Parallel()

	dagRunRepository := &mockDAGRunStore{}
	procRepository := &mockProcRepository{}

	detector := NewZombieDetector(dagRunRepository.repository(), procRepository, 0, 0)
	require.NotNil(t, detector)
	assert.Equal(t, 45*time.Second, detector.interval)
	assert.Equal(t, 3, detector.failureThreshold)

	detector = NewZombieDetector(dagRunRepository.repository(), procRepository, 60*time.Second, 5)
	require.NotNil(t, detector)
	assert.Equal(t, 60*time.Second, detector.interval)
	assert.Equal(t, 5, detector.failureThreshold)
}

func TestZombieDetectorDetectAndCleanZombies_NoEntries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagRunRepository := &mockDAGRunStore{}
	procRepository := &mockProcRepository{}
	detector := NewZombieDetector(dagRunRepository.repository(), procRepository, time.Second, 1)

	procRepository.On("ListAllEntries", ctx).Return([]proc.ProcEntry{}, nil).Once()

	detector.detectAndCleanZombies(ctx)

	procRepository.AssertExpectations(t)
	dagRunRepository.AssertExpectations(t)
}

func TestZombieDetectorDetectAndCleanZombies_FreshEntrySkipsRepair(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagRunRepository := &mockDAGRunStore{}
	procRepository := &mockProcRepository{}
	detector := NewZombieDetector(dagRunRepository.repository(), procRepository, time.Second, 1)

	entry := testRootProcEntry("queue", "test-dag", "run-1", "attempt-1", true)
	procRepository.On("ListAllEntries", ctx).Return([]proc.ProcEntry{entry}, nil).Once()

	detector.detectAndCleanZombies(ctx)

	procRepository.AssertExpectations(t)
	dagRunRepository.AssertExpectations(t)
}

func TestZombieDetectorDetectAndCleanZombies_StaleEntryRepairsMatchingAttempt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagRunRepository := &mockDAGRunStore{}
	procRepository := &mockProcRepository{}
	detector := NewZombieDetector(dagRunRepository.repository(), procRepository, time.Second, 1)

	dag := &ir.DAG{
		Name: "test-dag",
		Steps: []ir.Step{
			{Name: "step1"},
		},
	}
	entry := testRootProcEntry(dag.ProcGroup(), dag.Name, "run-1", "attempt-1", false)
	status := &ir.DAGRunStatus{
		Name:      dag.Name,
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
		Status:    ir.Running,
		Nodes:     ir.NewNodesFromSteps(dag.Steps),
	}
	status.Nodes[0].Status = ir.NodeRunning
	attempt := &testutil.MockAttempt{}

	procRepository.On("ListAllEntries", ctx).Return([]proc.ProcEntry{entry}, nil).Once()
	dagRunRepository.On("FindAttempt", mock.Anything, ir.NewDAGRunRef(dag.Name, "run-1")).Return(attempt, nil).Once()
	attempt.On("ReadStatus", mock.Anything).Return(status, nil).Twice()
	attempt.On("ReadDAG", mock.Anything).Return(dag, nil).Once()
	attempt.On("Open", mock.Anything).Return(nil).Once()
	attempt.On("Write", mock.Anything, mock.MatchedBy(func(s ir.DAGRunStatus) bool {
		return s.Status == ir.Failed &&
			s.AttemptID == status.AttemptID &&
			len(s.Nodes) == 1 &&
			s.Nodes[0].Status == ir.NodeFailed
	})).Return(nil).Once()
	attempt.On("Close", mock.Anything).Return(nil).Once()
	procRepository.On("RemoveIfStale", mock.Anything, entry).Return(nil).Once()

	detector.detectAndCleanZombies(ctx)

	procRepository.AssertExpectations(t)
	dagRunRepository.AssertExpectations(t)
	attempt.AssertExpectations(t)
}

func TestZombieDetectorDetectAndCleanZombies_StaleEntryWithAliveLocalPIDSkipsRepair(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagRunRepository := &mockDAGRunStore{}
	procRepository := &mockProcRepository{}
	detector := NewZombieDetector(dagRunRepository.repository(), procRepository, time.Second, 1)

	dag := &ir.DAG{
		Name: "test-dag",
		Steps: []ir.Step{
			{Name: "step1"},
		},
	}
	entry := testRootProcEntry(dag.ProcGroup(), dag.Name, "run-1", "attempt-1", false)
	pidStartedAt, ok := procutil.StartTime(os.Getpid())
	require.True(t, ok)
	status := &ir.DAGRunStatus{
		Name:         dag.Name,
		DAGRunID:     "run-1",
		AttemptID:    "attempt-1",
		Status:       ir.Running,
		WorkerID:     "local",
		PID:          ir.PID(os.Getpid()),
		PIDStartedAt: pidStartedAt,
		Nodes:        ir.NewNodesFromSteps(dag.Steps),
	}
	status.Nodes[0].Status = ir.NodeRunning
	attempt := &testutil.MockAttempt{}

	procRepository.On("ListAllEntries", ctx).Return([]proc.ProcEntry{entry}, nil).Once()
	dagRunRepository.On("FindAttempt", mock.Anything, ir.NewDAGRunRef(dag.Name, "run-1")).Return(attempt, nil).Once()
	attempt.On("ReadStatus", mock.Anything).Return(status, nil).Once()

	detector.detectAndCleanZombies(ctx)

	procRepository.AssertExpectations(t)
	dagRunRepository.AssertExpectations(t)
	attempt.AssertExpectations(t)
	attempt.AssertNotCalled(t, "ReadDAG", mock.Anything)
	attempt.AssertNotCalled(t, "Write", mock.Anything, mock.Anything)
	procRepository.AssertNotCalled(t, "RemoveIfStale", mock.Anything, mock.Anything)
}

func TestZombieDetectorDetectAndCleanZombies_StaleEntryWithFreshSiblingRemovesOnlyStale(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagRunRepository := &mockDAGRunStore{}
	procRepository := &mockProcRepository{}
	detector := NewZombieDetector(dagRunRepository.repository(), procRepository, time.Second, 1)

	staleEntry := testRootProcEntry("queue", "test-dag", "run-1", "attempt-1", false)
	freshEntry := testRootProcEntry("queue", "test-dag", "run-1", "attempt-2", true)

	procRepository.On("ListAllEntries", ctx).Return([]proc.ProcEntry{staleEntry, freshEntry}, nil).Once()
	procRepository.On("RemoveIfStale", mock.Anything, staleEntry).Return(nil).Once()

	detector.detectAndCleanZombies(ctx)

	procRepository.AssertExpectations(t)
	dagRunRepository.AssertExpectations(t)
}

func TestZombieDetectorDetectAndCleanZombies_SubDAGUsesRootScopedLookup(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagRunRepository := &mockDAGRunStore{}
	procRepository := &mockProcRepository{}
	detector := NewZombieDetector(dagRunRepository.repository(), procRepository, time.Second, 1)

	dag := &ir.DAG{
		Name: "child",
		Steps: []ir.Step{
			{Name: "child-step"},
		},
	}
	entry := proc.ProcEntry{
		GroupName: dag.ProcGroup(),
		Meta: proc.ProcMeta{
			StartedAt:    time.Now().Add(-time.Minute).Unix(),
			Name:         dag.Name,
			DAGRunID:     "sub-1",
			AttemptID:    "attempt-1",
			RootName:     "root",
			RootDAGRunID: "root-1",
		},
		Fresh: false,
	}
	status := &ir.DAGRunStatus{
		Name:      dag.Name,
		DAGRunID:  "sub-1",
		AttemptID: "attempt-1",
		Status:    ir.Running,
		Nodes:     ir.NewNodesFromSteps(dag.Steps),
	}
	status.Nodes[0].Status = ir.NodeRunning
	attempt := &testutil.MockAttempt{}

	procRepository.On("ListAllEntries", ctx).Return([]proc.ProcEntry{entry}, nil).Once()
	dagRunRepository.On("FindSubAttempt", mock.Anything, ir.NewDAGRunRef("root", "root-1"), "sub-1").Return(attempt, nil).Once()
	attempt.On("ReadStatus", mock.Anything).Return(status, nil).Twice()
	attempt.On("ReadDAG", mock.Anything).Return(dag, nil).Once()
	attempt.On("Open", mock.Anything).Return(nil).Once()
	attempt.On("Write", mock.Anything, mock.MatchedBy(func(s ir.DAGRunStatus) bool {
		return s.Status == ir.Failed && s.AttemptID == status.AttemptID
	})).Return(nil).Once()
	attempt.On("Close", mock.Anything).Return(nil).Once()
	procRepository.On("RemoveIfStale", mock.Anything, entry).Return(nil).Once()

	detector.detectAndCleanZombies(ctx)

	procRepository.AssertExpectations(t)
	dagRunRepository.AssertExpectations(t)
	attempt.AssertExpectations(t)
}

func TestZombieDetectorDetectAndCleanZombies_AttemptCounterDoesNotCarryAcrossRetries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagRunRepository := &mockDAGRunStore{}
	procRepository := &mockProcRepository{}
	detector := NewZombieDetector(dagRunRepository.repository(), procRepository, time.Second, 2)

	firstAttempt := testRootProcEntry("queue", "test-dag", "run-1", "attempt-1", false)
	secondAttempt := testRootProcEntry("queue", "test-dag", "run-1", "attempt-2", false)

	procRepository.On("ListAllEntries", ctx).Return([]proc.ProcEntry{firstAttempt}, nil).Once()
	detector.detectAndCleanZombies(ctx)

	procRepository.On("ListAllEntries", ctx).Return([]proc.ProcEntry{secondAttempt}, nil).Once()
	detector.detectAndCleanZombies(ctx)

	dagRunRepository.AssertNotCalled(t, "FindAttempt", mock.Anything, mock.Anything)
	procRepository.AssertExpectations(t)
}

func TestZombieDetectorDetectAndCleanZombies_OrphanedStaleEntryIsRemoved(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagRunRepository := &mockDAGRunStore{}
	procRepository := &mockProcRepository{}
	detector := NewZombieDetector(dagRunRepository.repository(), procRepository, time.Second, 1)

	entry := testRootProcEntry("queue", "test-dag", "run-1", "attempt-1", false)

	procRepository.On("ListAllEntries", ctx).Return([]proc.ProcEntry{entry}, nil).Once()
	dagRunRepository.On("FindAttempt", mock.Anything, ir.NewDAGRunRef("test-dag", "run-1")).Return(nil, dagrun.ErrDAGRunIDNotFound).Once()
	procRepository.On("RemoveIfStale", mock.Anything, entry).Return(nil).Once()

	detector.detectAndCleanZombies(ctx)

	procRepository.AssertExpectations(t)
	dagRunRepository.AssertExpectations(t)
}

func TestZombieDetectorDetectAndCleanZombies_StaleEntryWithMissingStatusIsRemoved(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagRunRepository := &mockDAGRunStore{}
	procRepository := &mockProcRepository{}
	detector := NewZombieDetector(dagRunRepository.repository(), procRepository, time.Second, 1)

	entry := testRootProcEntry("queue", "test-dag", "run-1", "attempt-1", false)

	procRepository.On("ListAllEntries", ctx).Return([]proc.ProcEntry{entry}, nil).Once()
	dagRunRepository.On("FindAttempt", mock.Anything, ir.NewDAGRunRef("test-dag", "run-1")).Return(nil, dagrun.ErrNoStatusData).Once()
	procRepository.On("RemoveIfStale", mock.Anything, entry).Return(nil).Once()

	detector.detectAndCleanZombies(ctx)

	procRepository.AssertExpectations(t)
	dagRunRepository.AssertExpectations(t)
}

func TestZombieDetectorDetectAndCleanZombies_StaleEntryWithCorruptedStatusIsRemoved(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dagRunRepository := &mockDAGRunStore{}
	procRepository := &mockProcRepository{}
	detector := NewZombieDetector(dagRunRepository.repository(), procRepository, time.Second, 1)

	entry := testRootProcEntry("queue", "test-dag", "run-1", "attempt-1", false)

	procRepository.On("ListAllEntries", ctx).Return([]proc.ProcEntry{entry}, nil).Once()
	dagRunRepository.On("FindAttempt", mock.Anything, ir.NewDAGRunRef("test-dag", "run-1")).Return(nil, dagrun.ErrCorruptedStatusData).Once()
	procRepository.On("RemoveIfStale", mock.Anything, entry).Return(nil).Once()

	detector.detectAndCleanZombies(ctx)

	procRepository.AssertExpectations(t)
	dagRunRepository.AssertExpectations(t)
}

func testRootProcEntry(groupName, dagName, dagRunID, attemptID string, fresh bool) proc.ProcEntry {
	return proc.ProcEntry{
		GroupName: groupName,
		Meta: proc.ProcMeta{
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

type mockDAGRunStore struct {
	testutil.DAGRunStoreStub
	mock.Mock
}

func (m *mockDAGRunStore) repository() *persis.DAGRunRepository {
	return persis.NewDAGRunRepository(m, nil, persis.DAGRunRepositoryOptions{})
}

func (m *mockDAGRunStore) CompareAndSwapLatestAttemptStatus(
	ctx context.Context,
	req persis.DAGRunCompareAndSwapStatusRequest,
) (*ir.DAGRunStatus, bool, error) {
	status := &ir.DAGRunStatus{
		Name:      req.DAGRun.Name,
		DAGRunID:  req.DAGRun.ID,
		AttemptID: req.ExpectedAttemptID,
		Status:    req.ExpectedStatus,
	}
	if err := req.Mutate(status); err != nil {
		return nil, false, err
	}
	args := m.MethodCalled(
		"CompareAndSwapLatestAttemptStatus",
		ctx,
		req.DAGRun,
		req.ExpectedAttemptID,
		req.ExpectedStatus,
		status,
	)
	if args.Get(0) == nil {
		return nil, args.Bool(1), args.Error(2)
	}
	return args.Get(0).(*ir.DAGRunStatus), args.Bool(1), args.Error(2)
}

func (m *mockDAGRunStore) FindAttempt(ctx context.Context, dagRun ir.DAGRunRef) (dagrun.Attempt, error) {
	args := m.Called(ctx, dagRun)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(dagrun.Attempt), args.Error(1)
}

func (m *mockDAGRunStore) FindSubAttempt(ctx context.Context, dagRun ir.DAGRunRef, subDAGRunID string) (dagrun.Attempt, error) {
	args := m.Called(ctx, dagRun, subDAGRunID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(dagrun.Attempt), args.Error(1)
}

var _ processRepository = (*mockProcRepository)(nil)

type mockProcRepository struct {
	mock.Mock
}

func (m *mockProcRepository) CountAlive(ctx context.Context, groupName string) (int, error) {
	args := m.Called(ctx, groupName)
	return args.Int(0), args.Error(1)
}

func (m *mockProcRepository) CountAliveByDAGName(ctx context.Context, groupName, dagName string) (int, error) {
	args := m.Called(ctx, groupName, dagName)
	return args.Int(0), args.Error(1)
}

func (m *mockProcRepository) IsRunAlive(ctx context.Context, groupName string, dagRun ir.DAGRunRef) (bool, error) {
	args := m.Called(ctx, groupName, dagRun)
	return args.Bool(0), args.Error(1)
}

func (m *mockProcRepository) ListAllEntries(ctx context.Context) ([]proc.ProcEntry, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]proc.ProcEntry), args.Error(1)
}

func (m *mockProcRepository) RemoveIfStale(ctx context.Context, entry proc.ProcEntry) error {
	args := m.Called(ctx, entry)
	return args.Error(0)
}
