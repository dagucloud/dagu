// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/agentsession"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/testutil"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persistestutil "github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/proto/convert"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func coordinatorTestTimeout(timeout time.Duration) time.Duration {
	if runtime.GOOS == "windows" {
		return timeout * 5
	}
	return timeout
}

type corruptLeaseStore struct {
	dispatch.DAGRunLeaseStore
	attemptKey string
}

func (s corruptLeaseStore) Get(ctx context.Context, attemptKey string) (*dispatch.DAGRunLease, error) {
	if attemptKey == s.attemptKey {
		return nil, persis.ErrCorrupt
	}
	return s.DAGRunLeaseStore.Get(ctx, attemptKey)
}

func TestDispatchBindErrorCode(t *testing.T) {
	t.Parallel()

	assert.Equal(t, codes.FailedPrecondition, dispatchBindErrorCode(dispatch.ErrDispatchAdmissionNotFound))
	assert.Equal(t, codes.FailedPrecondition, dispatchBindErrorCode(dispatch.ErrDispatchAdmissionConflict))
	assert.Equal(t, codes.Internal, dispatchBindErrorCode(errors.New("disk full")))
}

func TestRunLockSet(t *testing.T) {
	t.Parallel()

	var locks runLockSet
	first := locks.lock("run-a")

	woken := make(chan *runLock, 1)
	go func() {
		woken <- locks.lock("run-a")
	}()
	waitForRunLockRefs(t, &locks, "run-a", 2)

	other := make(chan *runLock, 1)
	go func() {
		other <- locks.lock("run-b")
	}()
	select {
	case held := <-other:
		locks.unlock("run-b", held)
	case <-time.After(coordinatorTestTimeout(time.Second)):
		require.FailNow(t, "different run lock was blocked")
	}

	locks.unlock("run-a", first)
	var second *runLock
	select {
	case second = <-woken:
	case <-time.After(coordinatorTestTimeout(time.Second)):
		require.FailNow(t, "waiting run lock was not acquired")
	}

	third := make(chan *runLock, 1)
	go func() {
		third <- locks.lock("run-a")
	}()
	waitForRunLockRefs(t, &locks, "run-a", 2)
	select {
	case held := <-third:
		locks.unlock("run-a", held)
		require.FailNow(t, "same run lock was acquired concurrently")
	default:
	}

	locks.unlock("run-a", second)
	select {
	case held := <-third:
		locks.unlock("run-a", held)
	case <-time.After(coordinatorTestTimeout(time.Second)):
		require.FailNow(t, "second waiting run lock was not acquired")
	}
}

func waitForRunLockRefs(t *testing.T, locks *runLockSet, dagRunID string, want int) {
	t.Helper()

	require.Eventually(t, func() bool {
		locks.mu.Lock()
		defer locks.mu.Unlock()

		entry := locks.entries[dagRunID]
		return entry != nil && entry.refs == want
	}, coordinatorTestTimeout(time.Second), 10*time.Millisecond)
}

type mockDAGRunStore struct {
	testutil.DAGRunStoreStub
	repository          *persis.DAGRunRepository
	attempts            map[string]*mockAttempt
	subAttempts         map[string]*mockAttempt // key: rootID:subID
	findAttemptErr      error
	createAttemptErr    error
	createSubAttemptErr error
	attemptWriteErr     error
	listStatusesCalls   int
	compareAndSwapCalls int
	mu                  sync.Mutex
}

func newMockDAGRunStore() *mockDAGRunStore {
	backend := &mockDAGRunStore{
		attempts:    make(map[string]*mockAttempt),
		subAttempts: make(map[string]*mockAttempt),
	}
	backend.repository = persis.NewDAGRunRepository(backend, nil, persis.DAGRunRepositoryOptions{})
	return backend
}

func TestHandlerAgentSessionCleanup(t *testing.T) {
	t.Parallel()

	backend := persistestutil.NewMemoryBackend()
	queue := agentsession.NewCleanupQueue(backend.Collection("cleanups"))
	root := ir.NewDAGRunRef("build", "run-removed")
	require.NoError(t, queue.EnqueueDAGRunRemoval(t.Context(), root, []ir.AgentSessionResource{{
		Provider: "opencode", SessionID: "session-1", Directory: "/workspace", OwnerWorkerID: "worker-a",
	}}))
	store := newMockDAGRunStore()
	handler := NewHandler(HandlerConfig{
		DAGRunRepository:         store.repository,
		AgentSessionCleanupQueue: queue,
		Owner:                    dispatch.CoordinatorEndpoint{ID: "coordinator-a", Host: "127.0.0.1", Port: 50055},
	})

	claimed, err := handler.ClaimAgentSessionCleanup(t.Context(), &coordinatorv1.ClaimAgentSessionCleanupRequest{WorkerId: "worker-a"})
	require.NoError(t, err)
	require.True(t, claimed.Found)
	assert.Equal(t, "session-1", claimed.SessionId)
	assert.Equal(t, "coordinator-a", claimed.OwnerCoordinatorId)

	_, err = handler.CompleteAgentSessionCleanup(t.Context(), &coordinatorv1.CompleteAgentSessionCleanupRequest{
		WorkerId: "worker-b", JobId: claimed.JobId, ClaimToken: claimed.ClaimToken,
	})
	require.Error(t, err)
	_, err = handler.CompleteAgentSessionCleanup(t.Context(), &coordinatorv1.CompleteAgentSessionCleanupRequest{
		WorkerId: "worker-a", JobId: claimed.JobId, ClaimToken: claimed.ClaimToken,
	})
	require.NoError(t, err)
	claimed, err = handler.ClaimAgentSessionCleanup(t.Context(), &coordinatorv1.ClaimAgentSessionCleanupRequest{WorkerId: "worker-a"})
	require.NoError(t, err)
	assert.False(t, claimed.Found)
}

func registerCommandExecutorCapsForCoordinatorTest() {
	caps := registry.ExecutorCapabilities{
		Command:          true,
		MultipleCommands: true,
		Script:           true,
		Shell:            true,
	}
	registry.RegisterExecutorCapabilities("", caps)
	registry.RegisterExecutorCapabilities("shell", caps)
	registry.RegisterExecutorCapabilities("command", caps)
}

func (m *mockDAGRunStore) addSubAttempt(rootRef ir.DAGRunRef, subDAGRunID string, status *ir.DAGRunStatus) *mockAttempt {
	m.mu.Lock()
	defer m.mu.Unlock()
	attempt := &mockAttempt{
		status: status,
	}
	key := rootRef.ID + ":" + subDAGRunID
	m.subAttempts[key] = attempt
	return attempt
}

func (m *mockDAGRunStore) addAttempt(ref ir.DAGRunRef, status *ir.DAGRunStatus) *mockAttempt {
	m.mu.Lock()
	defer m.mu.Unlock()
	attempt := &mockAttempt{
		status: status,
	}
	m.attempts[ref.ID] = attempt
	return attempt
}

func (m *mockDAGRunStore) addAbortingAttempt(ref ir.DAGRunRef, status *ir.DAGRunStatus) *mockAttempt {
	m.mu.Lock()
	defer m.mu.Unlock()
	attempt := &mockAttempt{
		status:   status,
		aborting: true,
	}
	m.attempts[ref.ID] = attempt
	return attempt
}

func (m *mockDAGRunStore) FindAttempt(_ context.Context, dagRun ir.DAGRunRef) (dagrun.Attempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.findAttemptErr != nil {
		return nil, m.findAttemptErr
	}
	if attempt, ok := m.attempts[dagRun.ID]; ok {
		return attempt, nil
	}
	return nil, dagrun.ErrDAGRunIDNotFound
}

func (m *mockDAGRunStore) CreateAttempt(_ context.Context, req persis.DAGRunCreateAttemptRequest) (dagrun.Attempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !req.RootDAGRun.Zero() {
		if m.createSubAttemptErr != nil {
			return nil, m.createSubAttemptErr
		}
		key := req.RootDAGRun.ID + ":" + req.DAGRunID
		attempt := &mockAttempt{
			dag:        req.DAG,
			status:     &ir.DAGRunStatus{Name: req.DAG.Name, DAGRunID: req.DAGRunID},
			writeError: m.attemptWriteErr,
		}
		m.subAttempts[key] = attempt
		return attempt, nil
	}
	if m.createAttemptErr != nil {
		return nil, m.createAttemptErr
	}
	attempt := &mockAttempt{
		dag:        req.DAG,
		status:     &ir.DAGRunStatus{Name: req.DAG.Name, DAGRunID: req.DAGRunID},
		writeError: m.attemptWriteErr,
	}
	m.attempts[req.DAGRunID] = attempt
	return attempt, nil
}
func (m *mockDAGRunStore) RecentStatuses(_ context.Context, _ string, _ int) ([]ir.DAGRunStatus, error) {
	return nil, nil
}
func (m *mockDAGRunStore) LatestAttempt(_ context.Context, _ persis.DAGRunLatestAttemptQuery) (dagrun.Attempt, error) {
	return nil, dagrun.ErrDAGRunIDNotFound
}
func (m *mockDAGRunStore) QueryStatuses(ctx context.Context, options persis.DAGRunStatusQuery) (persis.DAGRunStatusPage, error) {
	if err := ctx.Err(); err != nil {
		return persis.DAGRunStatusPage{}, err
	}

	statusFilter := make(map[ir.Status]struct{}, len(options.Statuses))
	for _, st := range options.Statuses {
		statusFilter[st] = struct{}{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.listStatusesCalls++

	var statuses []*ir.DAGRunStatus
	appendStatus := func(status *ir.DAGRunStatus) {
		if status == nil {
			return
		}
		if len(statusFilter) > 0 {
			if _, ok := statusFilter[status.Status]; !ok {
				return
			}
		}
		if options.DAGRunID != "" && status.DAGRunID != options.DAGRunID {
			return
		}
		if options.ExactName != "" && status.Name != options.ExactName {
			return
		}
		if options.Name != "" && status.Name != options.Name {
			return
		}

		cloned := *status
		statuses = append(statuses, &cloned)
	}

	for _, attempt := range m.attempts {
		appendStatus(attempt.status)
	}
	for _, attempt := range m.subAttempts {
		appendStatus(attempt.status)
	}

	return persis.DAGRunStatusPage{Items: statuses}, nil
}

func (m *mockDAGRunStore) ListStatusesCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listStatusesCalls
}

func (m *mockDAGRunStore) CompareAndSwapCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.compareAndSwapCalls
}

func (m *mockDAGRunStore) CompareAndSwapLatestAttemptStatus(
	ctx context.Context,
	req persis.DAGRunCompareAndSwapStatusRequest,
) (*ir.DAGRunStatus, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.compareAndSwapCalls++

	root := req.RootDAGRun
	isSubDAG := root.ID != "" && (root.ID != req.DAGRun.ID || root.Name != req.DAGRun.Name)

	var (
		attempt *mockAttempt
		ok      bool
	)
	if isSubDAG {
		key := root.ID + ":" + req.DAGRun.ID
		attempt, ok = m.subAttempts[key]
	} else {
		attempt, ok = m.attempts[req.DAGRun.ID]
	}
	if !ok || attempt.status == nil {
		return nil, false, nil
	}

	current := *attempt.status
	if req.ExpectedAttemptID != "" && current.AttemptID != req.ExpectedAttemptID {
		return &current, false, nil
	}
	if req.ExpectedAttemptKey != "" && current.AttemptKey != req.ExpectedAttemptKey {
		return &current, false, nil
	}
	if current.Status != req.ExpectedStatus {
		return &current, false, nil
	}
	if err := req.Mutate(&current); err != nil {
		return nil, false, err
	}
	attempt.status = &current
	attempt.written = true
	return &current, true, nil
}
func (m *mockDAGRunStore) FindSubAttempt(_ context.Context, rootRef ir.DAGRunRef, subDAGRunID string) (dagrun.Attempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := rootRef.ID + ":" + subDAGRunID
	if attempt, ok := m.subAttempts[key]; ok {
		return attempt, nil
	}
	return nil, dagrun.ErrDAGRunIDNotFound
}
func (m *mockDAGRunStore) RemoveOldDAGRuns(_ context.Context, _ persis.DAGRunRetentionRequest) ([]ir.DAGRunRef, error) {
	return nil, nil
}
func (m *mockDAGRunStore) RemoveDAGRun(_ context.Context, _ persis.DAGRunRemoveRequest) error {
	return nil
}

// mockAttempt is a test implementation of execution.Attempt
type mockAttempt struct {
	dag                    *ir.DAG
	status                 *ir.DAGRunStatus
	opened                 bool
	closed                 bool
	written                bool
	aborting               bool
	openError              error
	readStatusError        error
	writeError             error
	writeStarted           chan struct{}
	releaseWrite           chan struct{}
	stepMessages           map[string][]ir.LLMMessage // stepName -> messages
	writeStepMessagesError error                      // injected error for WriteStepMessages
	requireOpenForRead     bool
	isOpen                 bool
	mu                     sync.Mutex
}

func (m *mockAttempt) ID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status != nil && m.status.AttemptID != "" {
		return m.status.AttemptID
	}
	return "test-attempt"
}
func (m *mockAttempt) Open(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.openError != nil {
		return m.openError
	}
	m.opened = true
	m.isOpen = true
	return nil
}
func (m *mockAttempt) Write(_ context.Context, s ir.DAGRunStatus) error {
	m.mu.Lock()
	writeStarted := m.writeStarted
	releaseWrite := m.releaseWrite
	if writeStarted != nil {
		m.writeStarted = nil
	}
	m.mu.Unlock()

	if writeStarted != nil {
		close(writeStarted)
		if releaseWrite != nil {
			<-releaseWrite
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.writeError != nil {
		return m.writeError
	}
	m.status = &s
	m.written = true
	return nil
}
func (m *mockAttempt) Close(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	m.isOpen = false
	return nil
}
func (m *mockAttempt) ReadStatus(_ context.Context) (*ir.DAGRunStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.requireOpenForRead && !m.isOpen {
		return nil, errors.New("attempt is closed")
	}
	if m.readStatusError != nil {
		return nil, m.readStatusError
	}
	if m.status == nil {
		return nil, dagrun.ErrNoStatusData
	}
	cloned := *m.status
	return &cloned, nil
}
func (m *mockAttempt) ReadStatusUncached(ctx context.Context) (*ir.DAGRunStatus, error) {
	return m.ReadStatus(ctx)
}
func (m *mockAttempt) ReadDAG(_ context.Context) (*ir.DAG, error) { return m.dag, nil }
func (m *mockAttempt) SetDAG(dag *ir.DAG) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dag = dag
}
func (m *mockAttempt) Abort(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.aborting = true
	return nil
}
func (m *mockAttempt) IsAborting(_ context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.aborting, nil
}
func (m *mockAttempt) Hide(_ context.Context) error { return nil }
func (m *mockAttempt) Hidden() bool                 { return false }
func (m *mockAttempt) WriteOutputs(_ context.Context, _ *ir.DAGRunOutputs) error {
	return nil
}
func (m *mockAttempt) ReadOutputs(_ context.Context) (*ir.DAGRunOutputs, error) {
	return nil, nil
}
func (m *mockAttempt) WriteStepMessages(_ context.Context, stepName string, messages []ir.LLMMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.writeStepMessagesError != nil {
		return m.writeStepMessagesError
	}
	if m.stepMessages == nil {
		m.stepMessages = make(map[string][]ir.LLMMessage)
	}
	m.stepMessages[stepName] = messages
	return nil
}
func (m *mockAttempt) ReadStepMessages(_ context.Context, stepName string) ([]ir.LLMMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stepMessages == nil {
		return nil, nil
	}
	return m.stepMessages[stepName], nil
}

// GetStepMessages returns the messages written for a step (for test assertions)
func (m *mockAttempt) GetStepMessages(stepName string) []ir.LLMMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stepMessages == nil {
		return nil
	}
	return m.stepMessages[stepName]
}

func TestTransformArtifactPathsCreatesDirectory(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	handler := &Handler{artifactDir: baseDir}
	attempt := &mockAttempt{
		dag: &ir.DAG{
			Name: "test-dag",
			Artifacts: &ir.ArtifactsConfig{
				Enabled: true,
			},
		},
	}
	incoming := &ir.DAGRunStatus{
		DAGRunID:   "run-123",
		ArchiveDir: "/tmp/worker/dag-run_20260412_000000Z_run-123",
	}

	err := handler.transformArtifactPaths(context.Background(), attempt, nil, incoming)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(baseDir, "test-dag", "dag-run_20260412_000000Z_run-123"), incoming.ArchiveDir)

	info, statErr := os.Stat(incoming.ArchiveDir)
	require.NoError(t, statErr)
	assert.True(t, info.IsDir())
}

func TestTransformArtifactPathsSanitizesDAGName(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	handler := &Handler{artifactDir: baseDir}
	attempt := &mockAttempt{
		dag: &ir.DAG{
			Name: "../weird/..-dag--name",
			Artifacts: &ir.ArtifactsConfig{
				Enabled: true,
			},
		},
	}
	incoming := &ir.DAGRunStatus{
		DAGRunID:   "run-123",
		ArchiveDir: "/tmp/worker/dag-run_20260412_000000Z_run-123",
	}

	err := handler.transformArtifactPaths(context.Background(), attempt, nil, incoming)
	require.NoError(t, err)

	expected := filepath.Join(baseDir, fileutil.SafeName(attempt.dag.Name), "dag-run_20260412_000000Z_run-123")
	assert.Equal(t, expected, incoming.ArchiveDir)

	info, statErr := os.Stat(incoming.ArchiveDir)
	require.NoError(t, statErr)
	assert.True(t, info.IsDir())
}

func TestTransformArtifactPathsPreservesLatestArchiveDir(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	existingArchiveDir := filepath.Join(baseDir, "test-dag", "dag-run_20260412_000000Z_run-123")
	handler := &Handler{artifactDir: baseDir}
	incoming := &ir.DAGRunStatus{DAGRunID: "run-123"}
	latestStatus := &ir.DAGRunStatus{ArchiveDir: existingArchiveDir}

	err := handler.transformArtifactPaths(context.Background(), nil, latestStatus, incoming)
	require.NoError(t, err)
	assert.Equal(t, existingArchiveDir, incoming.ArchiveDir)

	info, statErr := os.Stat(existingArchiveDir)
	require.NoError(t, statErr)
	assert.True(t, info.IsDir())
}

func TestTransformArtifactPathsRejectsEmptyExpandedBaseDir(t *testing.T) {
	t.Setenv("EMPTY_ARTIFACT_DIR", "")

	handler := &Handler{artifactDir: t.TempDir()}
	attempt := &mockAttempt{
		dag: &ir.DAG{
			Name: "test-dag",
			Artifacts: &ir.ArtifactsConfig{
				Enabled: true,
				Dir:     "${EMPTY_ARTIFACT_DIR}",
			},
		},
	}
	incoming := &ir.DAGRunStatus{
		DAGRunID:   "run-123",
		ArchiveDir: "/tmp/worker/dag-run_20260412_000000Z_run-123",
	}

	err := handler.transformArtifactPaths(context.Background(), attempt, nil, incoming)
	require.EqualError(t, err, "artifact directory is empty after expansion")
}

func TestTransformArtifactPathsUsesDAGSpecificDirWithoutGlobalArtifactDir(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	handler := &Handler{}
	attempt := &mockAttempt{
		dag: &ir.DAG{
			Name: "test-dag",
			Artifacts: &ir.ArtifactsConfig{
				Enabled: true,
				Dir:     baseDir,
			},
		},
	}
	incoming := &ir.DAGRunStatus{
		DAGRunID:   "run-123",
		ArchiveDir: "/tmp/worker/dag-run_20260412_000000Z_run-123",
	}

	err := handler.transformArtifactPaths(context.Background(), attempt, nil, incoming)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(baseDir, "test-dag", "dag-run_20260412_000000Z_run-123"), incoming.ArchiveDir)
}

func TestCreateAttemptForTaskCarriesDAGLabels(t *testing.T) {
	registerCommandExecutorCapsForCoordinatorTest()

	h := NewHandler(HandlerConfig{DAGRunRepository: newMockDAGRunStore().repository})
	task := &coordinatorv1.Task{
		DagRunId:   "run-123",
		Target:     "daily",
		SourceFile: "/dags/daily-file.yaml",
		Definition: "name: daily\nlabels: [workspace=ops, team=platform]\nsteps:\n  - name: step1\n    run: echo hello",
	}
	prepared, err := h.createAttemptForTask(context.Background(), task)
	require.NoError(t, err)
	require.NotNil(t, prepared)

	assert.Equal(t, "workspace=ops,team=platform", task.Labels)
	status, err := prepared.attempt.ReadStatus(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"workspace=ops", "team=platform"}, status.Labels)
}

func TestCreateAttemptForTaskReturnsStorageErrors(t *testing.T) {
	registerCommandExecutorCapsForCoordinatorTest()

	newTask := func(runID string) *coordinatorv1.Task {
		return &coordinatorv1.Task{
			DagRunId:   runID,
			Target:     "daily",
			Definition: "name: daily\nsteps:\n  - name: step1\n    run: echo hello",
		}
	}

	t.Run("FindAttempt", func(t *testing.T) {
		storeErr := errors.New("storage unavailable")
		store := newMockDAGRunStore()
		store.findAttemptErr = storeErr

		_, err := NewHandler(HandlerConfig{DAGRunRepository: store.repository}).createAttemptForTask(context.Background(), newTask("run-find"))
		require.ErrorIs(t, err, storeErr)
	})

	t.Run("ReadStatus", func(t *testing.T) {
		storeErr := errors.New("status read failed")
		store := newMockDAGRunStore()
		attempt := store.addAttempt(ir.DAGRunRef{Name: "daily", ID: "run-read"}, &ir.DAGRunStatus{Status: ir.Running})
		attempt.readStatusError = storeErr

		_, err := NewHandler(HandlerConfig{DAGRunRepository: store.repository}).createAttemptForTask(context.Background(), newTask("run-read"))
		require.ErrorIs(t, err, storeErr)
	})
}

func TestAttemptInitializationClosesAfterWriteFailure(t *testing.T) {
	registerCommandExecutorCapsForCoordinatorTest()

	t.Run("RootAttempt", func(t *testing.T) {
		writeErr := errors.New("status write failed")
		store := newMockDAGRunStore()
		store.attemptWriteErr = writeErr
		task := &coordinatorv1.Task{
			DagRunId:   "run-root",
			Target:     "daily",
			Definition: "name: daily\nsteps:\n  - name: step1\n    run: echo hello",
		}

		_, err := NewHandler(HandlerConfig{DAGRunRepository: store.repository}).createAttemptForTask(context.Background(), task)
		require.ErrorIs(t, err, writeErr)
		assert.True(t, store.attempts[task.DagRunId].WasClosed())
	})

	t.Run("SubAttempt", func(t *testing.T) {
		writeErr := errors.New("status write failed")
		store := newMockDAGRunStore()
		store.attemptWriteErr = writeErr
		task := &coordinatorv1.Task{
			DagRunId:       "run-child",
			Target:         "child",
			Definition:     "name: child\nsteps:\n  - name: step1\n    run: echo hello",
			RootDagRunName: "daily",
			RootDagRunId:   "run-root",
		}

		_, err := NewHandler(HandlerConfig{DAGRunRepository: store.repository}).createSubAttemptForTask(context.Background(), task)
		require.ErrorIs(t, err, writeErr)
		assert.True(t, store.subAttempts[task.RootDagRunId+":"+task.DagRunId].WasClosed())
	})
}

// Thread-safe getters for test assertions
func (m *mockAttempt) WasOpened() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.opened
}

func (m *mockAttempt) WasWritten() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.written
}

func (m *mockAttempt) WasClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

func TestHandler_Poll(t *testing.T) {
	t.Parallel()

	t.Run("PollWithoutPollerID", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(HandlerConfig{})
		ctx := context.Background()

		_, err := h.Poll(ctx, &coordinatorv1.PollRequest{
			WorkerId: "worker1",
		})

		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.InvalidArgument, st.Code())
		require.Contains(t, st.Message(), "poller_id is required")
	})

	t.Run("PollAndDispatch", func(t *testing.T) {
		t.Parallel()

		h := NewHandler(HandlerConfig{})
		ctx := context.Background()

		// Start polling in a goroutine
		pollDone := make(chan *coordinatorv1.PollResponse)
		pollErr := make(chan error)
		go func() {
			resp, err := h.Poll(ctx, &coordinatorv1.PollRequest{
				WorkerId: "worker1",
				PollerId: "poller1",
			})
			if err != nil {
				pollErr <- err
			} else {
				pollDone <- resp
			}
		}()

		// Wait for poller to register
		require.Eventually(t, func() bool {
			h.mu.Lock()
			defer h.mu.Unlock()
			return len(h.waitingPollers) == 1
		}, time.Second, 10*time.Millisecond)

		// Dispatch a task
		task := &coordinatorv1.Task{
			RootDagRunName:   "test-dag",
			RootDagRunId:     "run-123",
			ParentDagRunName: "",
			ParentDagRunId:   "",
			DagRunId:         "run-123",
			Definition:       "name: test-dag\nsteps:\n  - name: step1\n    run: echo hello",
		}

		_, err := h.Dispatch(ctx, &coordinatorv1.DispatchRequest{
			Task: task,
		})
		require.NoError(t, err)

		// Check that the poller received the task
		select {
		case resp := <-pollDone:
			require.NotNil(t, resp)
			require.NotNil(t, resp.Task)
			require.Equal(t, "test-dag", resp.Task.RootDagRunName)
			require.Equal(t, "run-123", resp.Task.RootDagRunId)
		case err := <-pollErr:
			t.Fatalf("Poll failed: %v", err)
		case <-time.After(1 * time.Second):
			t.Fatal("Poll timed out")
		}
	})

	t.Run("DispatchWithNoWaitingPollers", func(t *testing.T) {
		t.Parallel()

		h := NewHandler(HandlerConfig{})
		ctx := context.Background()

		task := &coordinatorv1.Task{
			RootDagRunName: "test-dag",
			RootDagRunId:   "run-123",
			DagRunId:       "run-123",
			Definition:     "name: test-dag\nsteps:\n  - name: step1\n    run: echo hello",
		}

		_, err := h.Dispatch(ctx, &coordinatorv1.DispatchRequest{
			Task: task,
		})

		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.Unavailable, st.Code())
		require.Contains(t, st.Message(), "no available workers")
	})

	t.Run("WriteInitialStatusPreservesScheduleTime", func(t *testing.T) {
		t.Parallel()

		h := NewHandler(HandlerConfig{})
		attempt := &mockAttempt{}

		err := h.writeInitialStatus(
			context.Background(),
			attempt,
			&coordinatorv1.Task{
				DagRunId:     "run-123",
				AttemptKey:   "attempt-key",
				TriggerActor: "alice",
				ScheduleTime: "2026-03-13T10:00:00Z",
			},
			"test-dag",
			ir.DAGRunRef{},
			nil,
		)
		require.NoError(t, err)

		status, err := attempt.ReadStatus(context.Background())
		require.NoError(t, err)
		require.Equal(t, "2026-03-13T10:00:00Z", status.ScheduleTime)
		require.Equal(t, "alice", status.TriggerActor)
	})

	t.Run("DispatchFailsWhenAttemptPreparationFails", func(t *testing.T) {
		t.Parallel()
		registerCommandExecutorCapsForCoordinatorTest()

		baseDir := filepath.Join(t.TempDir(), "distributed")
		dispatchStore := newTestDispatchTaskStore(baseDir)
		heartbeatStore := newTestWorkerHeartbeatStore(baseDir)
		require.NoError(t, heartbeatStore.Upsert(context.Background(), dispatch.WorkerHeartbeatRecord{
			WorkerID:        "worker-1",
			LastHeartbeatAt: time.Now().UTC().UnixMilli(),
		}))

		store := newMockDAGRunStore()
		store.createAttemptErr = errors.New("prepare failed")
		h := NewHandler(HandlerConfig{
			DAGRunRepository:     store.repository,
			DispatchTaskStore:    dispatchStore,
			WorkerHeartbeatStore: heartbeatStore,
		})

		_, err := h.Dispatch(context.Background(), &coordinatorv1.DispatchRequest{
			Task: &coordinatorv1.Task{
				DagRunId:   "run-123",
				Target:     "test-dag",
				Definition: "name: test-dag\nsteps:\n  - name: step1\n    run: echo hello",
				QueueName:  "test-queue",
			},
		})
		require.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.Internal, st.Code())
		require.Contains(t, st.Message(), "failed to prepare attempt")

		count, countErr := dispatchStore.CountOutstandingByQueue(context.Background(), "test-queue", time.Second)
		require.NoError(t, countErr)
		assert.Zero(t, count)
	})

	t.Run("DispatchBindsAdmissionReservationIdempotently", func(t *testing.T) {
		t.Parallel()
		registerCommandExecutorCapsForCoordinatorTest()

		ctx := context.Background()
		baseDir := filepath.Join(t.TempDir(), "distributed")
		dispatchStore, leaseStore, activeStore := newTestDispatchAdmissionTaskStore(baseDir)
		heartbeatStore := newTestWorkerHeartbeatStore(baseDir)
		require.NoError(t, heartbeatStore.Upsert(ctx, dispatch.WorkerHeartbeatRecord{
			WorkerID:        "worker-1",
			LastHeartbeatAt: time.Now().UTC().UnixMilli(),
		}))

		dagRunRepository := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{
			DAGRunRepository:          dagRunRepository.repository,
			DispatchTaskStore:         dispatchStore,
			WorkerHeartbeatStore:      heartbeatStore,
			DAGRunLeaseStore:          leaseStore,
			ActiveDistributedRunStore: activeStore,
		})

		runRef := ir.NewDAGRunRef("test-dag", "run-123")
		attemptID := "test-attempt"
		attemptKey := ir.GenerateAttemptKey(runRef.Name, runRef.ID, runRef.Name, runRef.ID, attemptID)
		decision, err := dispatchStore.ReserveAdmission(ctx, dispatch.DispatchAdmissionRequest{
			QueueName:      "test-queue",
			MaxConcurrency: 1,
			AttemptKey:     attemptKey,
			AttemptID:      attemptID,
			DAGRun:         runRef,
			StaleThreshold: time.Minute,
		})
		require.NoError(t, err)
		require.True(t, decision.Reserved)

		req := &coordinatorv1.DispatchRequest{
			AdmissionReservationToken: decision.ReservationToken,
			Task: &coordinatorv1.Task{
				Operation:  coordinatorv1.Operation_OPERATION_RETRY,
				DagRunId:   runRef.ID,
				Target:     runRef.Name,
				Definition: "name: test-dag\nsteps:\n  - name: step1\n    run: echo hello",
				QueueName:  "test-queue",
			},
		}
		_, err = h.Dispatch(ctx, req)
		require.NoError(t, err)

		claimed, err := dispatchStore.ClaimNext(ctx, dispatch.DispatchTaskClaim{
			WorkerID:     "worker-1",
			PollerID:     "poller-1",
			ClaimTimeout: time.Minute,
		})
		require.NoError(t, err)
		require.NotNil(t, claimed)

		_, err = h.Dispatch(ctx, req)
		require.NoError(t, err)
		require.NoError(t, dispatchStore.DeleteClaim(ctx, claimed.ClaimToken))

		claimedAgain, err := dispatchStore.ClaimNext(ctx, dispatch.DispatchTaskClaim{
			WorkerID:     "worker-1",
			PollerID:     "poller-2",
			ClaimTimeout: time.Minute,
		})
		require.NoError(t, err)
		assert.Nil(t, claimedAgain)
	})

	t.Run("DispatchMarksNewAttemptFailedWhenEnqueueFails", func(t *testing.T) {
		t.Parallel()
		registerCommandExecutorCapsForCoordinatorTest()

		heartbeatStore := newTestWorkerHeartbeatStore(filepath.Join(t.TempDir(), "distributed"))
		require.NoError(t, heartbeatStore.Upsert(context.Background(), dispatch.WorkerHeartbeatRecord{
			WorkerID:        "worker-1",
			LastHeartbeatAt: time.Now().UTC().UnixMilli(),
		}))

		repository := testutil.NewFileDAGRunRepository(filepath.Join(t.TempDir(), "dag-runs"), persis.DAGRunRepositoryOptions{LatestStatusToday: true})
		h := NewHandler(HandlerConfig{
			DAGRunRepository:     repository,
			DispatchTaskStore:    &failingDispatchTaskStore{enqueueErr: errors.New("disk full")},
			WorkerHeartbeatStore: heartbeatStore,
		})

		_, err := h.Dispatch(context.Background(), &coordinatorv1.DispatchRequest{
			Task: &coordinatorv1.Task{
				DagRunId:   "run-123",
				Target:     "test-dag",
				Definition: "name: test-dag\nsteps:\n  - name: step1\n    run: echo hello",
				QueueName:  "test-queue",
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to enqueue task")

		attempt, findErr := repository.FindAttempt(context.Background(), ir.DAGRunRef{Name: "test-dag", ID: "run-123"})
		require.NoError(t, findErr)
		runStatus, readErr := attempt.ReadStatus(context.Background())
		require.NoError(t, readErr)
		require.Equal(t, ir.Failed, runStatus.Status)
		require.Contains(t, runStatus.Error, "failed to hand off distributed task")

		h.attemptsMu.RLock()
		require.Empty(t, h.openAttempts)
		h.attemptsMu.RUnlock()
	})

	t.Run("DispatchLeavesReusedQueuedAttemptQueuedWhenEnqueueFails", func(t *testing.T) {
		t.Parallel()
		registerCommandExecutorCapsForCoordinatorTest()

		heartbeatStore := newTestWorkerHeartbeatStore(filepath.Join(t.TempDir(), "distributed"))
		require.NoError(t, heartbeatStore.Upsert(context.Background(), dispatch.WorkerHeartbeatRecord{
			WorkerID:        "worker-1",
			LastHeartbeatAt: time.Now().UTC().UnixMilli(),
		}))

		store := newMockDAGRunStore()
		ref := ir.DAGRunRef{Name: "test-dag", ID: "run-123"}
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:      "test-dag",
			DAGRunID:  "run-123",
			AttemptID: "attempt-existing",
			Status:    ir.Queued,
		})

		h := NewHandler(HandlerConfig{
			DAGRunRepository:     store.repository,
			DispatchTaskStore:    &failingDispatchTaskStore{enqueueErr: errors.New("disk full")},
			WorkerHeartbeatStore: heartbeatStore,
		})

		_, err := h.Dispatch(context.Background(), &coordinatorv1.DispatchRequest{
			Task: &coordinatorv1.Task{
				DagRunId:   "run-123",
				Target:     "test-dag",
				Definition: "name: test-dag\nsteps:\n  - name: step1\n    run: echo hello",
				QueueName:  "test-queue",
			},
		})
		require.Error(t, err)

		runStatus, readErr := attempt.ReadStatus(context.Background())
		require.NoError(t, readErr)
		require.Equal(t, ir.Queued, runStatus.Status)
		assert.Equal(t, "attempt-existing", runStatus.AttemptID)
		assert.True(t, attempt.WasClosed())

		h.attemptsMu.RLock()
		require.Empty(t, h.openAttempts)
		h.attemptsMu.RUnlock()
	})

	t.Run("PollContextCancellation", func(t *testing.T) {
		t.Parallel()

		h := NewHandler(HandlerConfig{})
		ctx, cancel := context.WithCancel(context.Background())

		// Start polling
		pollDone := make(chan error)
		go func() {
			_, err := h.Poll(ctx, &coordinatorv1.PollRequest{
				WorkerId: "worker1",
				PollerId: "poller1",
			})
			pollDone <- err
		}()

		// Wait for poller to register
		require.Eventually(t, func() bool {
			h.mu.Lock()
			defer h.mu.Unlock()
			return len(h.waitingPollers) == 1
		}, time.Second, 10*time.Millisecond)

		// Cancel the context
		cancel()

		// Check that Poll returns with context error
		select {
		case err := <-pollDone:
			require.Error(t, err)
			require.Equal(t, context.Canceled, err)
		case <-time.After(1 * time.Second):
			t.Fatal("Poll did not return after context cancellation")
		}
	})
}

func TestHandler_DispatchRejectsStaleQueueDispatchRetry(t *testing.T) {
	t.Parallel()

	registerCommandExecutorCapsForCoordinatorTest()

	baseDir := filepath.Join(t.TempDir(), "distributed")
	dispatchStore := newTestDispatchTaskStore(baseDir)
	heartbeatStore := newTestWorkerHeartbeatStore(baseDir)
	require.NoError(t, heartbeatStore.Upsert(context.Background(), dispatch.WorkerHeartbeatRecord{
		WorkerID:        "worker-1",
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}))

	store := newMockDAGRunStore()
	ref := ir.DAGRunRef{Name: "test-dag", ID: "run-123"}
	store.addAttempt(ref, &ir.DAGRunStatus{
		Name:      "test-dag",
		DAGRunID:  "run-123",
		AttemptID: "attempt-current",
		Status:    ir.Aborted,
	})

	previousStatus, err := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
		Name:      "test-dag",
		DAGRunID:  "run-123",
		AttemptID: "attempt-queued",
		Status:    ir.Queued,
	})
	require.NoError(t, err)

	h := NewHandler(HandlerConfig{
		DAGRunRepository:     store.repository,
		DispatchTaskStore:    dispatchStore,
		WorkerHeartbeatStore: heartbeatStore,
	})

	_, err = h.Dispatch(context.Background(), &coordinatorv1.DispatchRequest{
		Task: &coordinatorv1.Task{
			Operation:      coordinatorv1.Operation_OPERATION_RETRY,
			DagRunId:       "run-123",
			Target:         "test-dag",
			Definition:     "name: test-dag\nsteps:\n  - name: step1\n    run: echo hello",
			QueueName:      "test-queue",
			PreviousStatus: previousStatus,
		},
	})
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, st.Code())
	require.Contains(t, st.Message(), "stale queue dispatch")

	count, countErr := dispatchStore.CountOutstandingByQueue(context.Background(), "test-queue", time.Second)
	require.NoError(t, countErr)
	assert.Zero(t, count)

	require.Len(t, store.attempts, 1)
	attempt, findErr := store.FindAttempt(context.Background(), ref)
	require.NoError(t, findErr)
	runStatus, readErr := attempt.ReadStatus(context.Background())
	require.NoError(t, readErr)
	require.Equal(t, "attempt-current", runStatus.AttemptID)
	require.Equal(t, ir.Aborted, runStatus.Status)
}

type failingDispatchTaskStore struct {
	enqueueErr error
}

func (s *failingDispatchTaskStore) Enqueue(context.Context, *dispatch.DispatchTask) error {
	return s.enqueueErr
}

func (s *failingDispatchTaskStore) ClaimNext(context.Context, dispatch.DispatchTaskClaim) (*dispatch.ClaimedDispatchTask, error) {
	return nil, nil
}

func (s *failingDispatchTaskStore) GetClaim(context.Context, string) (*dispatch.ClaimedDispatchTask, error) {
	return nil, dispatch.ErrDispatchTaskNotFound
}

func (s *failingDispatchTaskStore) ReleaseClaim(context.Context, string) error {
	return nil
}

func (s *failingDispatchTaskStore) DeleteClaim(context.Context, string) error {
	return nil
}

func (s *failingDispatchTaskStore) ListBundleDigests(context.Context) ([]string, error) {
	return nil, nil
}

func (s *failingDispatchTaskStore) CountOutstandingByQueue(context.Context, string, time.Duration) (int, error) {
	return 0, nil
}

func (s *failingDispatchTaskStore) HasOutstandingAttempt(context.Context, string, time.Duration) (bool, error) {
	return false, nil
}

func TestHandler_Heartbeat(t *testing.T) {
	t.Parallel()

	t.Run("ValidHeartbeat", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(HandlerConfig{})
		ctx := context.Background()

		req := &coordinatorv1.HeartbeatRequest{
			WorkerId: "worker1",
			Labels:   map[string]string{"type": "compute"},
			Stats: &coordinatorv1.WorkerStats{
				TotalPollers: 5,
				BusyPollers:  2,
				RunningTasks: []*coordinatorv1.RunningTask{
					{
						DagRunId:  "run-123",
						DagName:   "test.yaml",
						StartedAt: time.Now().Unix(),
					},
				},
			},
		}

		resp, err := h.Heartbeat(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("MissingWorkerID", func(t *testing.T) {
		t.Parallel()

		h := NewHandler(HandlerConfig{})
		ctx := context.Background()

		req := &coordinatorv1.HeartbeatRequest{
			Labels: map[string]string{"type": "compute"},
		}

		_, err := h.Heartbeat(ctx, req)
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("HeartbeatUpdatesWorkerInfo", func(t *testing.T) {
		t.Parallel()

		h := NewHandler(HandlerConfig{})
		ctx := context.Background()

		// Send heartbeat
		req := &coordinatorv1.HeartbeatRequest{
			WorkerId: "worker1",
			Labels:   map[string]string{"type": "compute", "region": "us-east"},
			Stats: &coordinatorv1.WorkerStats{
				TotalPollers: 10,
				BusyPollers:  3,
			},
		}

		_, err := h.Heartbeat(ctx, req)
		require.NoError(t, err)

		// Get workers should return the heartbeat data
		resp, err := h.GetWorkers(ctx, &coordinatorv1.GetWorkersRequest{})
		require.NoError(t, err)
		require.Len(t, resp.Workers, 1)

		worker := resp.Workers[0]
		require.Equal(t, "worker1", worker.WorkerId)
		require.Equal(t, map[string]string{"type": "compute", "region": "us-east"}, worker.Labels)
		require.Equal(t, int32(10), worker.TotalPollers)
		require.Equal(t, int32(3), worker.BusyPollers)
		require.Greater(t, worker.LastHeartbeatAt, int64(0))
	})

	t.Run("HeartbeatRefreshesLeaseForRunningRootTask", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository, StaleLeaseThreshold: 10 * time.Second})
		ctx := context.Background()

		initialLease := time.Now().Add(-10 * time.Second).UnixMilli()
		ref := ir.DAGRunRef{Name: "test-dag", ID: "run-123"}
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			AttemptKey: "root-attempt-key",
			Status:     ir.Running,
			WorkerID:   "worker1",
			LeaseAt:    initialLease,
		})

		_, err := h.Heartbeat(ctx, &coordinatorv1.HeartbeatRequest{
			WorkerId: "worker1",
			Stats: &coordinatorv1.WorkerStats{
				RunningTasks: []*coordinatorv1.RunningTask{
					{DagRunId: "run-123", DagName: "test-dag", AttemptKey: "root-attempt-key"},
				},
			},
		})
		require.NoError(t, err)

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		require.True(t, attempt.WasWritten())
		assert.Greater(t, status.LeaseAt, initialLease)
	})

	t.Run("HeartbeatKeepsAttemptOpenDuringRefresh", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository, StaleLeaseThreshold: 10 * time.Second})
		ctx := context.Background()

		ref := ir.NewDAGRunRef("test-dag", "run-123")
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       ref.Name,
			DAGRunID:   ref.ID,
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.Running,
			WorkerID:   "worker-1",
			LeaseAt:    time.Now().Add(-time.Minute).UnixMilli(),
		})
		attempt.requireOpenForRead = true
		require.NoError(t, attempt.Open(ctx))
		h.openAttempts[ref.ID] = attempt

		held := h.runLocks.lock(ref.ID)
		done := make(chan struct{})
		go func() {
			h.refreshLeaseForRunningTask(ctx, "worker-1", &coordinatorv1.RunningTask{
				DagRunId:   ref.ID,
				DagName:    ref.Name,
				AttemptKey: "attempt-key-1",
			}, time.Now())
			close(done)
		}()
		waitForRunLockRefs(t, &h.runLocks, ref.ID, 2)

		h.closeCachedAttemptForRun(ctx, ctx, ref.ID, attempt.ID())
		h.runLocks.unlock(ref.ID, held)
		select {
		case <-done:
		case <-time.After(coordinatorTestTimeout(time.Second)):
			require.FailNow(t, "heartbeat lease refresh did not finish")
		}

		assert.True(t, attempt.WasWritten())
	})

	t.Run("HeartbeatRefreshesLeaseForRunningSubDAG", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository, StaleLeaseThreshold: 10 * time.Second})
		ctx := context.Background()

		initialLease := time.Now().Add(-10 * time.Second).UnixMilli()
		rootRef := ir.DAGRunRef{Name: "root-dag", ID: "root-123"}
		attempt := store.addSubAttempt(rootRef, "sub-456", &ir.DAGRunStatus{
			Name:       "sub-dag",
			DAGRunID:   "sub-456",
			AttemptID:  "attempt-2",
			AttemptKey: "sub-attempt-key",
			Status:     ir.Running,
			WorkerID:   "worker1",
			LeaseAt:    initialLease,
		})

		_, err := h.Heartbeat(ctx, &coordinatorv1.HeartbeatRequest{
			WorkerId: "worker1",
			Stats: &coordinatorv1.WorkerStats{
				RunningTasks: []*coordinatorv1.RunningTask{
					{
						DagRunId:       "sub-456",
						DagName:        "sub-dag",
						RootDagRunName: "root-dag",
						RootDagRunId:   "root-123",
						AttemptKey:     "sub-attempt-key",
					},
				},
			},
		})
		require.NoError(t, err)

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		require.True(t, attempt.WasWritten())
		assert.Greater(t, status.LeaseAt, initialLease)
	})

	t.Run("RunHeartbeatTouchesSharedLease", func(t *testing.T) {
		t.Parallel()

		leaseStore := newTestDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
		h := NewHandler(HandlerConfig{
			DAGRunLeaseStore: leaseStore,
			Owner:            dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		})
		ctx := context.Background()

		initial := time.Now().Add(-10 * time.Second).UTC()
		require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
			AttemptKey:      "attempt-key-1",
			DAGRun:          ir.NewDAGRunRef("test-dag", "run-123"),
			Root:            ir.NewDAGRunRef("test-dag", "run-123"),
			AttemptID:       "attempt-1",
			QueueName:       "test-dag",
			WorkerID:        "worker-1",
			Owner:           dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
			ClaimedAt:       initial.UnixMilli(),
			LastHeartbeatAt: initial.UnixMilli(),
		}))

		_, err := h.RunHeartbeat(ctx, &coordinatorv1.RunHeartbeatRequest{
			WorkerId:           "worker-1",
			OwnerCoordinatorId: "coord-a",
			RunningTasks: []*coordinatorv1.RunningTask{
				{AttemptKey: "attempt-key-1", DagRunId: "run-123", DagName: "test-dag"},
			},
		})
		require.NoError(t, err)

		lease, err := leaseStore.Get(ctx, "attempt-key-1")
		require.NoError(t, err)
		assert.Greater(t, lease.LastHeartbeatAt, initial.UnixMilli())
		assert.Equal(t, initial.UnixMilli(), lease.ClaimedAt)
	})

	t.Run("RunHeartbeatCoalescesFreshSharedLease", func(t *testing.T) {
		t.Parallel()

		leaseStore := newTestDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
		h := NewHandler(HandlerConfig{
			DAGRunLeaseStore: leaseStore,
			Owner:            dispatch.CoordinatorEndpoint{ID: "coord-a"},
		})
		ctx := context.Background()
		initial := time.Now().UTC()
		require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
			AttemptKey:      "attempt-key-1",
			DAGRun:          ir.NewDAGRunRef("test-dag", "run-123"),
			AttemptID:       "attempt-1",
			WorkerID:        "worker-1",
			LastHeartbeatAt: initial.UnixMilli(),
		}))

		_, err := h.RunHeartbeat(ctx, &coordinatorv1.RunHeartbeatRequest{
			WorkerId:           "worker-1",
			OwnerCoordinatorId: "coord-a",
			RunningTasks: []*coordinatorv1.RunningTask{
				{AttemptKey: "attempt-key-1", DagRunId: "run-123", DagName: "test-dag"},
			},
		})
		require.NoError(t, err)

		lease, err := leaseStore.Get(ctx, "attempt-key-1")
		require.NoError(t, err)
		assert.Equal(t, initial.UnixMilli(), lease.LastHeartbeatAt)
	})

	t.Run("RunHeartbeatIsolatesCorruptLease", func(t *testing.T) {
		t.Parallel()

		baseStore := newTestDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
		leaseStore := corruptLeaseStore{DAGRunLeaseStore: baseStore, attemptKey: "corrupt-key"}
		h := NewHandler(HandlerConfig{
			DAGRunLeaseStore: leaseStore,
			Owner:            dispatch.CoordinatorEndpoint{ID: "coord-a"},
		})
		ctx := context.Background()
		initial := time.Now().Add(-10 * time.Second).UTC()
		require.NoError(t, baseStore.Upsert(ctx, dispatch.DAGRunLease{
			AttemptKey:      "valid-key",
			DAGRun:          ir.NewDAGRunRef("test-dag", "run-123"),
			AttemptID:       "attempt-1",
			WorkerID:        "worker-1",
			LastHeartbeatAt: initial.UnixMilli(),
		}))

		resp, err := h.RunHeartbeat(ctx, &coordinatorv1.RunHeartbeatRequest{
			WorkerId:           "worker-1",
			OwnerCoordinatorId: "coord-a",
			RunningTasks: []*coordinatorv1.RunningTask{
				{AttemptKey: "corrupt-key", DagRunId: "run-123", DagName: "test-dag"},
				{AttemptKey: "valid-key", DagRunId: "run-123", DagName: "test-dag"},
			},
		})
		require.NoError(t, err)
		require.Len(t, resp.CancelledRuns, 1)
		assert.Equal(t, "corrupt-key", resp.CancelledRuns[0].AttemptKey)

		lease, err := baseStore.Get(ctx, "valid-key")
		require.NoError(t, err)
		assert.Greater(t, lease.LastHeartbeatAt, initial.UnixMilli())
	})

	t.Run("RunHeartbeatRepairsStaleLeaseFailureForOwnedAttempt", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		baseDir := filepath.Join(t.TempDir(), "distributed")
		leaseStore := newTestDAGRunLeaseStore(baseDir)
		activeStore := newTestActiveDistributedRunStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunRepository:          store.repository,
			DAGRunLeaseStore:          leaseStore,
			ActiveDistributedRunStore: activeStore,
			Owner:                     dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		})
		ctx := context.Background()

		ref := ir.NewDAGRunRef("test-dag", "run-123")
		reason := dispatch.DistributedLeaseExpiredReason("worker-1")
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			Root:       ref,
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.Failed,
			WorkerID:   "worker-1",
			FinishedAt: "2026-04-20T00:00:01Z",
			Error:      reason,
			Nodes: []*ir.Node{
				{
					Step:       ir.Step{Name: "long-step"},
					StartedAt:  "2026-04-20T00:00:00Z",
					FinishedAt: "2026-04-20T00:00:01Z",
					Status:     ir.NodeFailed,
					Error:      reason,
				},
				{
					Step:       ir.Step{Name: "completed-step"},
					StartedAt:  "2026-04-20T00:00:00Z",
					FinishedAt: "2026-04-20T00:00:01Z",
					Status:     ir.NodeSucceeded,
				},
				{
					Step:   ir.Step{Name: "pending-step"},
					Status: ir.NodeFailed,
					Error:  reason,
				},
			},
		})

		initial := time.Now().Add(-time.Second).UTC()
		require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
			AttemptKey:      "attempt-key-1",
			DAGRun:          ref,
			Root:            ref,
			AttemptID:       "attempt-1",
			QueueName:       "test-dag",
			WorkerID:        "worker-1",
			Owner:           dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
			ClaimedAt:       initial.UnixMilli(),
			LastHeartbeatAt: initial.UnixMilli(),
		}))

		resp, err := h.RunHeartbeat(ctx, &coordinatorv1.RunHeartbeatRequest{
			WorkerId:           "worker-1",
			OwnerCoordinatorId: "coord-a",
			RunningTasks: []*coordinatorv1.RunningTask{
				{AttemptKey: "attempt-key-1", DagRunId: "run-123", DagName: "test-dag"},
			},
		})
		require.NoError(t, err)
		require.Empty(t, resp.CancelledRuns)

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, ir.Running, status.Status)
		assert.Empty(t, status.Error)
		assert.Empty(t, status.FinishedAt)
		require.Len(t, status.Nodes, 3)
		assert.Equal(t, ir.NodeRunning, status.Nodes[0].Status)
		assert.Equal(t, "2026-04-20T00:00:00Z", status.Nodes[0].StartedAt)
		assert.Empty(t, status.Nodes[0].FinishedAt)
		assert.Empty(t, status.Nodes[0].Error)
		assert.Equal(t, ir.NodeSucceeded, status.Nodes[1].Status)
		assert.Equal(t, ir.NodeNotStarted, status.Nodes[2].Status)
		assert.Equal(t, "-", status.Nodes[2].StartedAt)
		assert.Equal(t, "-", status.Nodes[2].FinishedAt)
		assert.Empty(t, status.Nodes[2].Error)

		record, err := activeStore.Get(ctx, "attempt-key-1")
		require.NoError(t, err)
		assert.Equal(t, "attempt-1", record.AttemptID)
		assert.Equal(t, "worker-1", record.WorkerID)
		assert.Equal(t, ir.Running, record.Status)
	})

	t.Run("RunHeartbeatStaleRepairSurvivesCallerCancellation", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		leaseStore := newTestDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
		h := NewHandler(HandlerConfig{
			DAGRunRepository: store.repository,
			DAGRunLeaseStore: leaseStore,
			Owner:            dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		})
		ctx := context.Background()

		ref := ir.NewDAGRunRef("test-dag", "run-123")
		reason := dispatch.DistributedLeaseExpiredReason("worker-1")
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			Root:       ref,
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.Failed,
			WorkerID:   "worker-1",
			FinishedAt: "2026-04-20T00:00:01Z",
			Error:      reason,
		})

		observedAt := time.Now().UTC()
		require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
			AttemptKey:      "attempt-key-1",
			DAGRun:          ref,
			Root:            ref,
			AttemptID:       "attempt-1",
			QueueName:       "test-dag",
			WorkerID:        "worker-1",
			Owner:           dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
			ClaimedAt:       observedAt.UnixMilli(),
			LastHeartbeatAt: observedAt.UnixMilli(),
		}))

		cancelledCtx, cancel := context.WithCancel(ctx)
		cancel()
		h.repairStaleLeaseFailureFromRunHeartbeat(cancelledCtx, "worker-1", &coordinatorv1.RunningTask{
			AttemptKey: "attempt-key-1",
			DagRunId:   "run-123",
			DagName:    "test-dag",
		}, observedAt)

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, ir.Running, status.Status)
		assert.Empty(t, status.Error)
		assert.Empty(t, status.FinishedAt)
	})

	t.Run("RunHeartbeatSkipsStaleRepairCASForActiveAttempt", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		leaseStore := newTestDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
		h := NewHandler(HandlerConfig{
			DAGRunRepository: store.repository,
			DAGRunLeaseStore: leaseStore,
			Owner:            dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		})
		ctx := context.Background()

		ref := ir.NewDAGRunRef("test-dag", "run-123")
		store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			Root:       ref,
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.Running,
			WorkerID:   "worker-1",
		})

		initial := time.Now().Add(-time.Second).UTC()
		require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
			AttemptKey:      "attempt-key-1",
			DAGRun:          ref,
			Root:            ref,
			AttemptID:       "attempt-1",
			QueueName:       "test-dag",
			WorkerID:        "worker-1",
			Owner:           dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
			ClaimedAt:       initial.UnixMilli(),
			LastHeartbeatAt: initial.UnixMilli(),
		}))

		resp, err := h.RunHeartbeat(ctx, &coordinatorv1.RunHeartbeatRequest{
			WorkerId:           "worker-1",
			OwnerCoordinatorId: "coord-a",
			RunningTasks: []*coordinatorv1.RunningTask{
				{AttemptKey: "attempt-key-1", DagRunId: "run-123", DagName: "test-dag"},
			},
		})
		require.NoError(t, err)
		require.Empty(t, resp.CancelledRuns)
		assert.Equal(t, 0, store.CompareAndSwapCallCount())
	})

	t.Run("RunHeartbeatDoesNotRepairUnrelatedFailure", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		leaseStore := newTestDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
		h := NewHandler(HandlerConfig{
			DAGRunRepository: store.repository,
			DAGRunLeaseStore: leaseStore,
			Owner:            dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		})
		ctx := context.Background()

		ref := ir.NewDAGRunRef("test-dag", "run-123")
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			Root:       ref,
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.Failed,
			WorkerID:   "worker-1",
			Error:      "exit status 1",
			Nodes: []*ir.Node{
				{Status: ir.NodeFailed, Error: "exit status 1"},
			},
		})

		initial := time.Now().Add(-time.Second).UTC()
		require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
			AttemptKey:      "attempt-key-1",
			DAGRun:          ref,
			Root:            ref,
			AttemptID:       "attempt-1",
			QueueName:       "test-dag",
			WorkerID:        "worker-1",
			Owner:           dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
			ClaimedAt:       initial.UnixMilli(),
			LastHeartbeatAt: initial.UnixMilli(),
		}))

		resp, err := h.RunHeartbeat(ctx, &coordinatorv1.RunHeartbeatRequest{
			WorkerId:           "worker-1",
			OwnerCoordinatorId: "coord-a",
			RunningTasks: []*coordinatorv1.RunningTask{
				{AttemptKey: "attempt-key-1", DagRunId: "run-123", DagName: "test-dag"},
			},
		})
		require.NoError(t, err)
		require.Len(t, resp.CancelledRuns, 1)
		assert.Equal(t, "attempt-key-1", resp.CancelledRuns[0].AttemptKey)

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, ir.Failed, status.Status)
		assert.Equal(t, "exit status 1", status.Error)
		assert.Equal(t, ir.NodeFailed, status.Nodes[0].Status)
		assert.Equal(t, "exit status 1", status.Nodes[0].Error)
		assert.False(t, attempt.WasWritten())
	})

	t.Run("RunHeartbeatCancelsTaskWhenLeaseMissing", func(t *testing.T) {
		t.Parallel()

		leaseStore := newTestDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
		h := NewHandler(HandlerConfig{
			DAGRunLeaseStore: leaseStore,
			Owner:            dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		})
		ctx := context.Background()

		resp, err := h.RunHeartbeat(ctx, &coordinatorv1.RunHeartbeatRequest{
			WorkerId:           "worker-1",
			OwnerCoordinatorId: "coord-a",
			RunningTasks: []*coordinatorv1.RunningTask{
				{AttemptKey: "missing-attempt-key", DagRunId: "run-123", DagName: "test-dag"},
			},
		})
		require.NoError(t, err)
		require.Len(t, resp.CancelledRuns, 1)
		assert.Equal(t, "missing-attempt-key", resp.CancelledRuns[0].AttemptKey)
	})

	t.Run("RunHeartbeatAcceptsAttemptFromDifferentCoordinatorEndpoint", func(t *testing.T) {
		t.Parallel()

		leaseStore := newTestDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
		initialHeartbeat := time.Now().UTC().Add(-time.Minute)
		require.NoError(t, leaseStore.Upsert(t.Context(), dispatch.DAGRunLease{
			AttemptKey:      "attempt-key-1",
			DAGRun:          ir.NewDAGRunRef("test-dag", "run-123"),
			Root:            ir.NewDAGRunRef("test-dag", "run-123"),
			AttemptID:       "attempt-1",
			WorkerID:        "worker-1",
			Owner:           dispatch.CoordinatorEndpoint{ID: "coord-b", Host: "coordinator-b", Port: 50055},
			LastHeartbeatAt: initialHeartbeat.UnixMilli(),
		}))
		h := NewHandler(HandlerConfig{
			DAGRunLeaseStore: leaseStore,
			Owner:            dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "coordinator-a", Port: 50055},
		})
		ctx := context.Background()

		resp, err := h.RunHeartbeat(ctx, &coordinatorv1.RunHeartbeatRequest{
			WorkerId:           "worker-1",
			OwnerCoordinatorId: "coord-b",
			RunningTasks: []*coordinatorv1.RunningTask{{
				AttemptKey: "attempt-key-1",
				DagRunId:   "run-123",
				DagName:    "test-dag",
			}},
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Empty(t, resp.CancelledRuns)

		lease, err := leaseStore.Get(t.Context(), "attempt-key-1")
		require.NoError(t, err)
		assert.Greater(t, lease.LastHeartbeatAt, initialHeartbeat.UnixMilli())
		assert.Equal(t, "coord-b", lease.Owner.ID)
	})

	t.Run("HeartbeatSkipsLeaseRefreshOnAttemptKeyMismatch", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository, StaleLeaseThreshold: 10 * time.Second})
		ctx := context.Background()

		initialLease := time.Now().Add(-10 * time.Second).UnixMilli()
		ref := ir.DAGRunRef{Name: "test-dag", ID: "run-123"}
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			AttemptKey: "current-attempt-key",
			Status:     ir.Running,
			WorkerID:   "worker1",
			LeaseAt:    initialLease,
		})

		_, err := h.Heartbeat(ctx, &coordinatorv1.HeartbeatRequest{
			WorkerId: "worker1",
			Stats: &coordinatorv1.WorkerStats{
				RunningTasks: []*coordinatorv1.RunningTask{
					{DagRunId: "run-123", DagName: "test-dag", AttemptKey: "stale-attempt-key"},
				},
			},
		})
		require.NoError(t, err)

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, initialLease, status.LeaseAt)
		assert.False(t, attempt.WasWritten())
	})

	t.Run("HeartbeatSkipsLeaseRefreshOnWorkerMismatch", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository, StaleLeaseThreshold: 10 * time.Second})
		ctx := context.Background()

		initialLease := time.Now().Add(-10 * time.Second).UnixMilli()
		ref := ir.DAGRunRef{Name: "test-dag", ID: "run-123"}
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:      "test-dag",
			DAGRunID:  "run-123",
			AttemptID: "attempt-1",
			Status:    ir.Running,
			WorkerID:  "worker-a",
			LeaseAt:   initialLease,
		})

		_, err := h.Heartbeat(ctx, &coordinatorv1.HeartbeatRequest{
			WorkerId: "worker-b",
			Stats: &coordinatorv1.WorkerStats{
				RunningTasks: []*coordinatorv1.RunningTask{
					{DagRunId: "run-123", DagName: "test-dag"},
				},
			},
		})
		require.NoError(t, err)

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, initialLease, status.LeaseAt)
		assert.False(t, attempt.WasWritten())
	})

	t.Run("StaleHeartbeatCleanup", func(t *testing.T) {
		t.Parallel()

		h := NewHandler(HandlerConfig{})
		ctx := context.Background()

		// Manually add a stale heartbeat
		h.mu.Lock()
		h.heartbeats["old-worker"] = &heartbeatInfo{
			workerID:        "old-worker",
			labels:          map[string]string{"type": "old"},
			lastHeartbeatAt: time.Now().Add(-40 * time.Second), // 40 seconds old
		}
		h.mu.Unlock()

		// Send a new heartbeat from different worker
		req := &coordinatorv1.HeartbeatRequest{
			WorkerId: "new-worker",
			Labels:   map[string]string{"type": "new"},
			Stats: &coordinatorv1.WorkerStats{
				TotalPollers: 5,
			},
		}

		_, err := h.Heartbeat(ctx, req)
		require.NoError(t, err)

		// Trigger zombie detection (this is now done periodically, not on heartbeat)
		h.detectAndCleanupZombies(ctx)

		// Old worker should be cleaned up
		resp, err := h.GetWorkers(ctx, &coordinatorv1.GetWorkersRequest{})
		require.NoError(t, err)
		require.Len(t, resp.Workers, 1)
		require.Equal(t, "new-worker", resp.Workers[0].WorkerId)
	})
}

func TestHandler_GetWorkers(t *testing.T) {
	t.Parallel()

	t.Run("NoWorkers", func(t *testing.T) {
		t.Parallel()
		h := NewHandler(HandlerConfig{})
		ctx := context.Background()

		resp, err := h.GetWorkers(ctx, &coordinatorv1.GetWorkersRequest{})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Empty(t, resp.Workers)
	})

	t.Run("WorkersFromHeartbeats", func(t *testing.T) {
		t.Parallel()

		h := NewHandler(HandlerConfig{})
		ctx := context.Background()

		// Send heartbeats from multiple workers
		workers := []struct {
			id           string
			totalPollers int32
			busyPollers  int32
			labels       map[string]string
		}{
			{"worker1", 5, 2, map[string]string{"type": "compute"}},
			{"worker2", 10, 7, map[string]string{"type": "storage"}},
			{"worker3", 3, 0, map[string]string{"type": "network"}},
		}

		for _, w := range workers {
			_, err := h.Heartbeat(ctx, &coordinatorv1.HeartbeatRequest{
				WorkerId: w.id,
				Labels:   w.labels,
				Stats: &coordinatorv1.WorkerStats{
					TotalPollers: w.totalPollers,
					BusyPollers:  w.busyPollers,
				},
			})
			require.NoError(t, err)
		}

		// Get workers
		resp, err := h.GetWorkers(ctx, &coordinatorv1.GetWorkersRequest{})
		require.NoError(t, err)
		require.Len(t, resp.Workers, 3)

		// Verify worker data
		workerMap := make(map[string]*coordinatorv1.WorkerInfo)
		for _, w := range resp.Workers {
			workerMap[w.WorkerId] = w
		}

		for _, expected := range workers {
			actual, ok := workerMap[expected.id]
			require.True(t, ok, "Worker %s not found", expected.id)
			require.Equal(t, expected.labels, actual.Labels)
			require.Equal(t, expected.totalPollers, actual.TotalPollers)
			require.Equal(t, expected.busyPollers, actual.BusyPollers)
			require.Greater(t, actual.LastHeartbeatAt, int64(0))
		}
	})

	t.Run("RunningTasksInHeartbeat", func(t *testing.T) {
		t.Parallel()

		h := NewHandler(HandlerConfig{})
		ctx := context.Background()

		// Send heartbeat with running tasks
		runningTasks := []*coordinatorv1.RunningTask{
			{
				DagRunId:  "run-123",
				DagName:   "etl-pipeline.yaml",
				StartedAt: time.Now().Add(-5 * time.Minute).Unix(),
			},
			{
				DagRunId:  "run-124",
				DagName:   "backup-job.yaml",
				StartedAt: time.Now().Add(-1 * time.Minute).Unix(),
			},
		}

		_, err := h.Heartbeat(ctx, &coordinatorv1.HeartbeatRequest{
			WorkerId: "worker1",
			Labels:   map[string]string{"type": "compute"},
			Stats: &coordinatorv1.WorkerStats{
				TotalPollers: 5,
				BusyPollers:  2,
				RunningTasks: runningTasks,
			},
		})
		require.NoError(t, err)

		// Get workers and verify running tasks
		resp, err := h.GetWorkers(ctx, &coordinatorv1.GetWorkersRequest{})
		require.NoError(t, err)
		require.Len(t, resp.Workers, 1)

		worker := resp.Workers[0]
		require.Equal(t, int32(2), worker.BusyPollers)
		require.Len(t, worker.RunningTasks, 2)

		// Verify task details
		for i, task := range worker.RunningTasks {
			require.Equal(t, runningTasks[i].DagRunId, task.DagRunId)
			require.Equal(t, runningTasks[i].DagName, task.DagName)
			require.Equal(t, runningTasks[i].StartedAt, task.StartedAt)
		}
	})

}

func TestHandler_ZombieDetection(t *testing.T) {
	t.Parallel()

	t.Run("MarkRunFailedUpdatesStatus", func(t *testing.T) {
		t.Parallel()
		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		// Create a running DAG run
		ref := ir.DAGRunRef{Name: "test-dag", ID: "run-123"}
		initialStatus := &ir.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   ir.Running,
			Nodes: []*ir.Node{
				{Status: ir.NodeRunning},
				{Status: ir.NodeSucceeded},
			},
		}
		attempt := store.addAttempt(ref, initialStatus)
		require.NoError(t, attempt.Open(ctx))
		h.openAttempts[ref.ID] = attempt

		// Mark the run as failed
		h.markRunFailed(ctx, "test-dag", "run-123", "worker crashed")

		// Verify the status was updated
		require.True(t, attempt.WasOpened())
		require.True(t, attempt.WasWritten())
		require.True(t, attempt.WasClosed())
		_, cached := h.openAttempts[ref.ID]
		assert.False(t, cached)

		// Check the status
		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, ir.Failed, status.Status)
		require.Equal(t, "worker crashed", status.Error)
		require.NotEmpty(t, status.FinishedAt)

		// Check that running node was marked as failed
		require.Equal(t, ir.NodeFailed, status.Nodes[0].Status)
		require.Equal(t, "worker crashed", status.Nodes[0].Error)
		// Succeeded node should remain unchanged
		require.Equal(t, ir.NodeSucceeded, status.Nodes[1].Status)
	})

	t.Run("MarkRunFailedSkipsCompletedRuns", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		// Create an already completed DAG run
		ref := ir.DAGRunRef{Name: "test-dag", ID: "run-123"}
		initialStatus := &ir.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   ir.Succeeded,
		}
		attempt := store.addAttempt(ref, initialStatus)

		// Try to mark the run as failed
		h.markRunFailed(ctx, "test-dag", "run-123", "worker crashed")

		// Verify no writes occurred (status should remain Succeeded)
		require.False(t, attempt.WasWritten())
		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, ir.Succeeded, status.Status)
	})

	t.Run("MarkWorkerTasksFailedWithNoStore", func(t *testing.T) {
		t.Parallel()

		h := NewHandler(HandlerConfig{})
		ctx := context.Background()

		info := &heartbeatInfo{
			workerID: "worker1",
			stats: &coordinatorv1.WorkerStats{
				RunningTasks: []*coordinatorv1.RunningTask{
					{DagRunId: "run-123", DagName: "test-dag"},
				},
			},
		}

		// Should not panic, just skip
		h.markWorkerTasksFailed(ctx, info)
	})

	t.Run("MarkWorkerTasksFailedWithNoStats", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		info := &heartbeatInfo{
			workerID: "worker1",
			stats:    nil, // No stats
		}

		// Should not panic, just skip
		h.markWorkerTasksFailed(ctx, info)
	})

	t.Run("StaleHeartbeatMarksTasksAsFailed", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		// Create a running DAG run
		ref := ir.DAGRunRef{Name: "test-dag", ID: "run-123"}
		initialStatus := &ir.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   ir.Running,
		}
		attempt := store.addAttempt(ref, initialStatus)

		// Add a stale heartbeat with running tasks
		h.mu.Lock()
		h.heartbeats["stale-worker"] = &heartbeatInfo{
			workerID:        "stale-worker",
			lastHeartbeatAt: time.Now().Add(-40 * time.Second), // 40 seconds old
			stats: &coordinatorv1.WorkerStats{
				RunningTasks: []*coordinatorv1.RunningTask{
					{DagRunId: "run-123", DagName: "test-dag"},
				},
			},
		}
		h.mu.Unlock()

		// Trigger zombie detection (this is now done periodically, not on heartbeat)
		h.detectAndCleanupZombies(ctx)

		// Verify the stale worker's task was marked as failed
		require.True(t, attempt.WasWritten())
		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, ir.Failed, status.Status)
		require.Contains(t, status.Error, "stale-worker")
		require.Contains(t, status.Error, "unresponsive")
	})

	t.Run("DetectAndCleanupZombies", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		// Create two running DAG runs
		ref1 := ir.DAGRunRef{Name: "dag1", ID: "run-1"}
		status1 := &ir.DAGRunStatus{
			Name:     "dag1",
			DAGRunID: "run-1",
			Status:   ir.Running,
		}
		attempt1 := store.addAttempt(ref1, status1)

		ref2 := ir.DAGRunRef{Name: "dag2", ID: "run-2"}
		status2 := &ir.DAGRunStatus{
			Name:     "dag2",
			DAGRunID: "run-2",
			Status:   ir.Running,
		}
		attempt2 := store.addAttempt(ref2, status2)

		// Add a stale heartbeat with both running tasks
		h.mu.Lock()
		h.heartbeats["crashed-worker"] = &heartbeatInfo{
			workerID:        "crashed-worker",
			lastHeartbeatAt: time.Now().Add(-40 * time.Second),
			stats: &coordinatorv1.WorkerStats{
				RunningTasks: []*coordinatorv1.RunningTask{
					{DagRunId: "run-1", DagName: "dag1"},
					{DagRunId: "run-2", DagName: "dag2"},
				},
			},
		}
		h.mu.Unlock()

		// Run zombie detection
		h.detectAndCleanupZombies(ctx)

		// Verify both tasks were marked as failed
		require.True(t, attempt1.WasWritten())
		require.True(t, attempt2.WasWritten())

		s1, _ := attempt1.ReadStatus(ctx)
		s2, _ := attempt2.ReadStatus(ctx)
		require.Equal(t, ir.Failed, s1.Status)
		require.Equal(t, ir.Failed, s2.Status)

		// Verify the stale worker was removed
		h.mu.Lock()
		_, exists := h.heartbeats["crashed-worker"]
		h.mu.Unlock()
		require.False(t, exists)
	})

	t.Run("StartZombieDetectorRunsPeriodically", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})

		// Create a running DAG run
		ref := ir.DAGRunRef{Name: "test-dag", ID: "run-123"}
		status := &ir.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   ir.Running,
		}
		attempt := store.addAttempt(ref, status)

		// Add a stale heartbeat
		h.mu.Lock()
		h.heartbeats["zombie-worker"] = &heartbeatInfo{
			workerID:        "zombie-worker",
			lastHeartbeatAt: time.Now().Add(-40 * time.Second),
			stats: &coordinatorv1.WorkerStats{
				RunningTasks: []*coordinatorv1.RunningTask{
					{DagRunId: "run-123", DagName: "test-dag"},
				},
			},
		}
		h.mu.Unlock()

		// Start zombie detector with short interval for testing
		ctx := t.Context()

		h.StartZombieDetector(ctx, 50*time.Millisecond)

		// Wait for detector to mark task as failed
		require.Eventually(t, func() bool {
			return attempt.WasWritten()
		}, time.Second, 10*time.Millisecond)

		// Verify the task was marked as failed
		s, _ := attempt.ReadStatus(ctx)
		require.Equal(t, ir.Failed, s.Status)
	})

	t.Run("StartZombieDetectorDefersSharedLeaseCleanup", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		leaseStore := newTestDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
		threshold := coordinatorTestTimeout(300 * time.Millisecond)
		h := NewHandler(HandlerConfig{
			DAGRunRepository:    store.repository,
			DAGRunLeaseStore:    leaseStore,
			StaleLeaseThreshold: threshold,
			Owner: dispatch.CoordinatorEndpoint{
				ID: "coord-b", Host: "127.0.0.1", Port: 4321,
			},
		})
		ctx, cancel := context.WithCancel(t.Context())
		defer func() {
			cancel()
			h.WaitZombieDetector()
		}()

		ref := ir.NewDAGRunRef("test-dag", "run-123")
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       ref.Name,
			DAGRunID:   ref.ID,
			Root:       ref,
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.Running,
			WorkerID:   "worker-1",
		})
		staleAt := time.Now().Add(-threshold - time.Second).UTC()
		require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
			AttemptKey: "attempt-key-1",
			DAGRun:     ref,
			Root:       ref,
			AttemptID:  "attempt-1",
			QueueName:  ref.Name,
			WorkerID:   "worker-1",
			Owner: dispatch.CoordinatorEndpoint{
				ID: "coord-a", Host: "127.0.0.1", Port: 1234,
			},
			ClaimedAt:       staleAt.UnixMilli(),
			LastHeartbeatAt: staleAt.UnixMilli(),
		}))

		h.StartZombieDetector(ctx, threshold/10)
		deadline := time.Now().Add(threshold / 2)
		for time.Now().Before(deadline) {
			require.False(t, attempt.WasWritten(), "stale shared lease was cleaned up before the threshold elapsed")
			lease, err := leaseStore.Get(ctx, "attempt-key-1")
			require.NoError(t, err)
			require.Equal(t, staleAt.UnixMilli(), lease.LastHeartbeatAt)
			time.Sleep(threshold / 20)
		}
	})

	t.Run("DetectStaleLeasesOnlyFailsRunningDistributedRuns", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{
			DAGRunRepository:        store.repository,
			StaleLeaseThreshold:     time.Second,
			StaleHeartbeatThreshold: time.Second,
		})
		ctx := context.Background()

		staleLease := time.Now().Add(-5 * time.Second).UnixMilli()
		runningAttempt := store.addAttempt(ir.DAGRunRef{Name: "running-dag", ID: "run-1"}, &ir.DAGRunStatus{
			Name:     "running-dag",
			DAGRunID: "run-1",
			Status:   ir.Running,
			WorkerID: "worker1",
			LeaseAt:  staleLease,
		})
		waitingAttempt := store.addAttempt(ir.DAGRunRef{Name: "waiting-dag", ID: "run-2"}, &ir.DAGRunStatus{
			Name:     "waiting-dag",
			DAGRunID: "run-2",
			Status:   ir.Waiting,
			WorkerID: "worker1",
			LeaseAt:  staleLease,
		})
		queuedAttempt := store.addAttempt(ir.DAGRunRef{Name: "queued-dag", ID: "run-3"}, &ir.DAGRunStatus{
			Name:     "queued-dag",
			DAGRunID: "run-3",
			Status:   ir.Queued,
			WorkerID: "worker1",
			LeaseAt:  staleLease,
		})

		h.detectStaleLeases(ctx)

		runningStatus, err := runningAttempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, ir.Failed, runningStatus.Status)

		waitingStatus, err := waitingAttempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, ir.Waiting, waitingStatus.Status)

		queuedStatus, err := queuedAttempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, ir.Queued, queuedStatus.Status)
	})

	t.Run("DetectStaleSharedLeaseFailsLatestMatchingAttemptAndDeletesLease", func(t *testing.T) {
		t.Parallel()

		testCases := []struct {
			name       string
			status     ir.Status
			nodeStatus ir.NodeStatus
			workerID   string
		}{
			{name: "Running", status: ir.Running, nodeStatus: ir.NodeRunning, workerID: "worker-1"},
			{name: "NotStarted", status: ir.NotStarted, nodeStatus: ir.NodeNotStarted, workerID: "worker-1"},
			{name: "NotStartedWithoutPersistedWorkerID", status: ir.NotStarted, nodeStatus: ir.NodeNotStarted},
			{name: "Queued", status: ir.Queued, nodeStatus: ir.NodeNotStarted, workerID: "worker-1"},
			{name: "QueuedWithoutPersistedWorkerID", status: ir.Queued, nodeStatus: ir.NodeNotStarted},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				store := newMockDAGRunStore()
				leaseStore := newTestDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
				h := NewHandler(HandlerConfig{
					DAGRunRepository:    store.repository,
					DAGRunLeaseStore:    leaseStore,
					StaleLeaseThreshold: time.Second,
				})
				ctx := context.Background()

				ref := ir.DAGRunRef{Name: "lease-dag", ID: "run-lease"}
				attempt := store.addAttempt(ref, &ir.DAGRunStatus{
					Name:       "lease-dag",
					DAGRunID:   "run-lease",
					AttemptID:  "attempt-1",
					AttemptKey: "lease-key-1",
					Status:     tc.status,
					WorkerID:   tc.workerID,
					Nodes: []*ir.Node{
						{Status: tc.nodeStatus},
					},
				})

				staleAt := time.Now().Add(-10 * time.Second).UTC()
				require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
					AttemptKey:      "lease-key-1",
					DAGRun:          ref,
					Root:            ref,
					AttemptID:       "attempt-1",
					QueueName:       "lease-dag",
					WorkerID:        "worker-1",
					LastHeartbeatAt: staleAt.UnixMilli(),
					ClaimedAt:       staleAt.UnixMilli(),
				}))

				h.detectStaleLeases(ctx)

				status, err := attempt.ReadStatus(ctx)
				require.NoError(t, err)
				assert.Equal(t, ir.Failed, status.Status)
				assert.Equal(t, dispatch.DistributedLeaseExpiredReason("worker-1"), status.Error)
				assert.Equal(t, ir.NodeFailed, status.Nodes[0].Status)
				assert.Equal(t, dispatch.DistributedLeaseExpiredReason("worker-1"), status.Nodes[0].Error)

				_, err = leaseStore.Get(ctx, "lease-key-1")
				assert.ErrorIs(t, err, dispatch.ErrDAGRunLeaseNotFound)
			})
		}
	})

	t.Run("DetectStaleLeasesFailsLeasedRunWithoutStatusScan", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		store := newMockDAGRunStore()
		baseDir := filepath.Join(t.TempDir(), "distributed")
		leaseStore := newTestDAGRunLeaseStore(baseDir)
		activeStore := newTestActiveDistributedRunStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunRepository:          store.repository,
			DAGRunLeaseStore:          leaseStore,
			ActiveDistributedRunStore: activeStore,
			StaleLeaseThreshold:       time.Second,
		})

		ref := ir.DAGRunRef{Name: "lease-dag", ID: "run-lease"}
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       "lease-dag",
			DAGRunID:   "run-lease",
			AttemptID:  "attempt-1",
			AttemptKey: "lease-key-1",
			Status:     ir.Running,
			WorkerID:   "worker-1",
			Nodes: []*ir.Node{
				{Status: ir.NodeRunning},
			},
		})

		staleAt := time.Now().Add(-10 * time.Second).UTC()
		require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
			AttemptKey:      "lease-key-1",
			DAGRun:          ref,
			Root:            ref,
			AttemptID:       "attempt-1",
			QueueName:       "lease-dag",
			WorkerID:        "worker-1",
			LastHeartbeatAt: staleAt.UnixMilli(),
			ClaimedAt:       staleAt.UnixMilli(),
		}))

		h.detectStaleLeases(ctx)

		assert.Zero(t, store.ListStatusesCallCount())

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, ir.Failed, status.Status)
		assert.Equal(t, dispatch.DistributedLeaseExpiredReason("worker-1"), status.Error)
		assert.Equal(t, ir.NodeFailed, status.Nodes[0].Status)

		_, err = leaseStore.Get(ctx, "lease-key-1")
		assert.ErrorIs(t, err, dispatch.ErrDAGRunLeaseNotFound)
	})

	t.Run("DetectStaleLeasesClosesCachedAttemptBeforeFailure", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		store := newMockDAGRunStore()
		baseDir := filepath.Join(t.TempDir(), "distributed")
		leaseStore := newTestDAGRunLeaseStore(baseDir)
		activeStore := newTestActiveDistributedRunStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunRepository:          store.repository,
			DAGRunLeaseStore:          leaseStore,
			ActiveDistributedRunStore: activeStore,
			StaleLeaseThreshold:       time.Second,
		})

		ref := ir.DAGRunRef{Name: "lease-dag", ID: "run-lease"}
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       "lease-dag",
			DAGRunID:   "run-lease",
			AttemptID:  "attempt-1",
			AttemptKey: "lease-key-1",
			Status:     ir.Running,
			WorkerID:   "worker-1",
			Nodes: []*ir.Node{
				{Status: ir.NodeRunning},
			},
		})
		h.openAttempts[ref.ID] = attempt

		staleAt := time.Now().Add(-10 * time.Second).UTC()
		require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
			AttemptKey:      "lease-key-1",
			DAGRun:          ref,
			Root:            ref,
			AttemptID:       "attempt-1",
			QueueName:       "lease-dag",
			WorkerID:        "worker-1",
			LastHeartbeatAt: staleAt.UnixMilli(),
			ClaimedAt:       staleAt.UnixMilli(),
		}))

		h.detectStaleLeases(ctx)

		require.True(t, attempt.WasClosed())
		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, ir.Failed, status.Status)

		h.attemptsMu.RLock()
		_, cached := h.openAttempts[ref.ID]
		h.attemptsMu.RUnlock()
		assert.False(t, cached)
	})

	t.Run("DetectStaleLeasesWaitsForInFlightStatusReport", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		store := newMockDAGRunStore()
		baseDir := filepath.Join(t.TempDir(), "distributed")
		leaseStore := newTestDAGRunLeaseStore(baseDir)
		heartbeatStore := newTestWorkerHeartbeatStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunRepository:        store.repository,
			DAGRunLeaseStore:        leaseStore,
			WorkerHeartbeatStore:    heartbeatStore,
			StaleHeartbeatThreshold: time.Minute,
			StaleLeaseThreshold:     time.Second,
			Owner:                   dispatch.CoordinatorEndpoint{ID: "coord-a"},
		})

		ref := ir.DAGRunRef{Name: "lease-dag", ID: "run-lease"}
		writeStarted := make(chan struct{})
		releaseWrite := make(chan struct{})
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       "lease-dag",
			DAGRunID:   "run-lease",
			AttemptID:  "attempt-1",
			AttemptKey: "lease-key-1",
			Status:     ir.Running,
			WorkerID:   "worker-1",
			Nodes: []*ir.Node{
				{Status: ir.NodeRunning},
			},
		})
		attempt.writeStarted = writeStarted
		attempt.releaseWrite = releaseWrite

		staleAt := time.Now().Add(-10 * time.Second).UTC()
		require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
			AttemptKey:      "lease-key-1",
			DAGRun:          ref,
			Root:            ref,
			AttemptID:       "attempt-1",
			QueueName:       "lease-dag",
			WorkerID:        "worker-1",
			LastHeartbeatAt: staleAt.UnixMilli(),
			ClaimedAt:       staleAt.UnixMilli(),
		}))
		require.NoError(t, heartbeatStore.Upsert(ctx, dispatch.WorkerHeartbeatRecord{
			WorkerID:        "worker-1",
			LastHeartbeatAt: staleAt.UnixMilli(),
		}))

		protoStatus, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:       ref.Name,
			DAGRunID:   ref.ID,
			AttemptID:  "attempt-1",
			AttemptKey: "lease-key-1",
			ProcGroup:  "lease-dag",
			Status:     ir.Running,
			WorkerID:   "worker-1",
		})
		require.NoError(t, convErr)

		reportDone := make(chan error, 1)
		go func() {
			resp, err := h.ReportStatus(ctx, &coordinatorv1.ReportStatusRequest{
				Status:             protoStatus,
				WorkerId:           "worker-1",
				OwnerCoordinatorId: "coord-a",
			})
			if err != nil {
				reportDone <- err
				return
			}
			if resp == nil || !resp.Accepted {
				reportDone <- errors.New("status report was not accepted")
				return
			}
			reportDone <- nil
		}()

		statusReportTimeout := coordinatorTestTimeout(time.Second)
		select {
		case <-writeStarted:
		case <-time.After(statusReportTimeout):
			require.FailNow(t, "timed out waiting for status report to reach write")
		}

		detectDone := make(chan struct{})
		go func() {
			defer close(detectDone)
			h.detectStaleLeases(ctx)
		}()

		select {
		case <-detectDone:
			require.FailNow(t, "stale-lease repair completed while status report held the run mutex")
		case <-time.After(50 * time.Millisecond):
		}

		close(releaseWrite)
		select {
		case err := <-reportDone:
			require.NoError(t, err)
		case <-time.After(statusReportTimeout):
			require.FailNow(t, "timed out waiting for status report")
		}
		select {
		case <-detectDone:
		case <-time.After(statusReportTimeout):
			require.FailNow(t, "timed out waiting for stale-lease repair")
		}

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, ir.Running, status.Status)
		lease, err := leaseStore.Get(ctx, "lease-key-1")
		require.NoError(t, err)
		assert.Greater(t, lease.LastHeartbeatAt, staleAt.UnixMilli())
	})

	t.Run("DetectStaleLeasesFailsSubDAGLeasedRunWhenFreshWorkerHeartbeatDropsAttempt", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		store := newMockDAGRunStore()
		baseDir := filepath.Join(t.TempDir(), "distributed")
		leaseStore := newTestDAGRunLeaseStore(baseDir)
		heartbeatStore := newTestWorkerHeartbeatStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunRepository:        store.repository,
			DAGRunLeaseStore:        leaseStore,
			WorkerHeartbeatStore:    heartbeatStore,
			StaleHeartbeatThreshold: time.Minute,
			StaleLeaseThreshold:     time.Second,
		})

		root := ir.NewDAGRunRef("root-dag", "root-run")
		subRun := ir.NewDAGRunRef("sub-dag", "sub-run")
		attemptKey := "sub-attempt-key"
		attemptID := "sub-attempt-id"
		attempt := store.addSubAttempt(root, subRun.ID, &ir.DAGRunStatus{
			Name:       subRun.Name,
			DAGRunID:   subRun.ID,
			Root:       root,
			AttemptID:  attemptID,
			AttemptKey: attemptKey,
			Status:     ir.Running,
			WorkerID:   "worker-1",
			Nodes: []*ir.Node{
				{Status: ir.NodeRunning},
			},
		})

		staleAt := time.Now().Add(-10 * time.Second).UTC()
		require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
			AttemptKey:      attemptKey,
			DAGRun:          subRun,
			Root:            root,
			AttemptID:       attemptID,
			QueueName:       subRun.Name,
			WorkerID:        "worker-1",
			LastHeartbeatAt: staleAt.UnixMilli(),
			ClaimedAt:       staleAt.UnixMilli(),
		}))
		require.NoError(t, heartbeatStore.Upsert(ctx, dispatch.WorkerHeartbeatRecord{
			WorkerID:        "worker-1",
			LastHeartbeatAt: time.Now().UTC().UnixMilli(),
			Stats: &dispatch.WorkerStats{
				RunningTasks: []*dispatch.RunningTask{},
			},
		}))

		h.detectStaleLeases(ctx)

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, ir.Failed, status.Status)
		require.Equal(t, dispatch.DistributedLeaseExpiredReason("worker-1"), status.Error)
		require.Equal(t, ir.NodeFailed, status.Nodes[0].Status)
		require.Equal(t, dispatch.DistributedLeaseExpiredReason("worker-1"), status.Nodes[0].Error)

		_, err = leaseStore.Get(ctx, attemptKey)
		assert.ErrorIs(t, err, dispatch.ErrDAGRunLeaseNotFound)
	})

	t.Run("DetectStaleLeasesKeepsLeasedRunWhenFreshWorkerHeartbeatStillReportsAttempt", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		store := newMockDAGRunStore()
		baseDir := filepath.Join(t.TempDir(), "distributed")
		leaseStore := newTestDAGRunLeaseStore(baseDir)
		heartbeatStore := newTestWorkerHeartbeatStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunRepository:        store.repository,
			WorkerHeartbeatStore:    heartbeatStore,
			DAGRunLeaseStore:        leaseStore,
			StaleHeartbeatThreshold: time.Minute,
			StaleLeaseThreshold:     time.Second,
		})

		ref := ir.DAGRunRef{Name: "lease-dag", ID: "run-lease"}
		attemptKey := "lease-key-1"
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       "lease-dag",
			DAGRunID:   "run-lease",
			AttemptID:  "attempt-1",
			AttemptKey: attemptKey,
			Status:     ir.Running,
			WorkerID:   "worker-1",
			Nodes: []*ir.Node{
				{Status: ir.NodeRunning},
			},
		})

		staleAt := time.Now().Add(-10 * time.Second).UTC()
		require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
			AttemptKey:      attemptKey,
			DAGRun:          ref,
			Root:            ref,
			AttemptID:       "attempt-1",
			QueueName:       "lease-dag",
			WorkerID:        "worker-1",
			LastHeartbeatAt: staleAt.UnixMilli(),
			ClaimedAt:       staleAt.UnixMilli(),
		}))
		require.NoError(t, heartbeatStore.Upsert(ctx, dispatch.WorkerHeartbeatRecord{
			WorkerID:        "worker-1",
			LastHeartbeatAt: time.Now().UTC().UnixMilli(),
			Stats: &dispatch.WorkerStats{
				RunningTasks: []*dispatch.RunningTask{
					{
						DAGRunID:       "run-lease",
						DAGName:        "lease-dag",
						RootDAGRunID:   "run-lease",
						RootDAGRunName: "lease-dag",
						AttemptKey:     attemptKey,
					},
				},
			},
		}))

		h.detectStaleLeases(ctx)

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, ir.Running, status.Status)
		assert.Equal(t, ir.NodeRunning, status.Nodes[0].Status)

		lease, err := leaseStore.Get(ctx, attemptKey)
		require.NoError(t, err)
		assert.Equal(t, attemptKey, lease.AttemptKey)
		assert.Equal(t, "worker-1", lease.WorkerID)
		assert.Greater(t, lease.LastHeartbeatAt, staleAt.UnixMilli())
	})

	t.Run("DetectStaleLeasesRestoresMissingLeaseWhenFreshWorkerHeartbeatStillReportsOrphanedRun", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		store := newMockDAGRunStore()
		baseDir := filepath.Join(t.TempDir(), "distributed")
		leaseStore := newTestDAGRunLeaseStore(baseDir)
		heartbeatStore := newTestWorkerHeartbeatStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunRepository:        store.repository,
			WorkerHeartbeatStore:    heartbeatStore,
			DAGRunLeaseStore:        leaseStore,
			StaleHeartbeatThreshold: time.Minute,
			StaleLeaseThreshold:     time.Second,
		})

		ref := ir.DAGRunRef{Name: "lease-dag", ID: "run-lease"}
		attemptKey := "lease-key-1"
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       "lease-dag",
			DAGRunID:   "run-lease",
			AttemptID:  "attempt-1",
			AttemptKey: attemptKey,
			Status:     ir.Running,
			WorkerID:   "worker-1",
			Nodes: []*ir.Node{
				{Status: ir.NodeRunning},
			},
		})

		require.NoError(t, heartbeatStore.Upsert(ctx, dispatch.WorkerHeartbeatRecord{
			WorkerID:        "worker-1",
			LastHeartbeatAt: time.Now().UTC().UnixMilli(),
			Stats: &dispatch.WorkerStats{
				RunningTasks: []*dispatch.RunningTask{
					{
						DAGRunID:       "run-lease",
						DAGName:        "lease-dag",
						RootDAGRunID:   "run-lease",
						RootDAGRunName: "lease-dag",
						AttemptKey:     attemptKey,
					},
				},
			},
		}))

		h.detectStaleLeases(ctx)

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, ir.Running, status.Status)
		assert.Equal(t, ir.NodeRunning, status.Nodes[0].Status)

		lease, err := leaseStore.Get(ctx, attemptKey)
		require.NoError(t, err)
		assert.Equal(t, attemptKey, lease.AttemptKey)
		assert.Equal(t, "worker-1", lease.WorkerID)
	})

	t.Run("DetectStaleLeasesFailsLeasedRunWhenFreshWorkerHeartbeatDropsAttempt", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		store := newMockDAGRunStore()
		baseDir := filepath.Join(t.TempDir(), "distributed")
		leaseStore := newTestDAGRunLeaseStore(baseDir)
		heartbeatStore := newTestWorkerHeartbeatStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunRepository:        store.repository,
			WorkerHeartbeatStore:    heartbeatStore,
			DAGRunLeaseStore:        leaseStore,
			StaleHeartbeatThreshold: time.Minute,
			StaleLeaseThreshold:     time.Second,
		})

		ref := ir.DAGRunRef{Name: "lease-dag", ID: "run-lease"}
		attemptKey := "lease-key-1"
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       "lease-dag",
			DAGRunID:   "run-lease",
			AttemptID:  "attempt-1",
			AttemptKey: attemptKey,
			Status:     ir.Running,
			WorkerID:   "worker-1",
			Nodes: []*ir.Node{
				{Status: ir.NodeRunning},
			},
		})

		staleAt := time.Now().Add(-10 * time.Second).UTC()
		require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
			AttemptKey:      attemptKey,
			DAGRun:          ref,
			Root:            ref,
			AttemptID:       "attempt-1",
			QueueName:       "lease-dag",
			WorkerID:        "worker-1",
			LastHeartbeatAt: staleAt.UnixMilli(),
			ClaimedAt:       staleAt.UnixMilli(),
		}))
		require.NoError(t, heartbeatStore.Upsert(ctx, dispatch.WorkerHeartbeatRecord{
			WorkerID:        "worker-1",
			LastHeartbeatAt: time.Now().UTC().UnixMilli(),
			Stats: &dispatch.WorkerStats{
				RunningTasks: []*dispatch.RunningTask{},
			},
		}))

		h.detectStaleLeases(ctx)

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, ir.Failed, status.Status)
		assert.Equal(t, dispatch.DistributedLeaseExpiredReason("worker-1"), status.Error)
		assert.Equal(t, ir.NodeFailed, status.Nodes[0].Status)

		_, err = leaseStore.Get(ctx, attemptKey)
		assert.ErrorIs(t, err, dispatch.ErrDAGRunLeaseNotFound)
	})

	t.Run("DetectStaleLeasesFailsOrphanedDistributedStatusWithoutLease", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		leaseStore := newTestDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
		h := NewHandler(HandlerConfig{
			DAGRunRepository:    store.repository,
			DAGRunLeaseStore:    leaseStore,
			StaleLeaseThreshold: time.Second,
		})
		ctx := context.Background()

		ref := ir.DAGRunRef{Name: "lease-dag", ID: "run-lease"}
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       "lease-dag",
			DAGRunID:   "run-lease",
			AttemptID:  "attempt-1",
			AttemptKey: "lease-key-1",
			Status:     ir.Running,
			WorkerID:   "worker-1",
			Nodes: []*ir.Node{
				{Status: ir.NodeRunning},
			},
		})

		h.detectStaleLeases(ctx)

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, ir.Failed, status.Status)
		assert.Equal(t, dispatch.DistributedLeaseExpiredReason("worker-1"), status.Error)
		assert.Equal(t, ir.NodeFailed, status.Nodes[0].Status)
	})

	t.Run("DetectStaleLeasesRestoresMissingLeaseFromActiveIndexWhenFreshWorkerHeartbeatStillReportsAttempt", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		store := newMockDAGRunStore()
		baseDir := filepath.Join(t.TempDir(), "distributed")
		leaseStore := newTestDAGRunLeaseStore(baseDir)
		heartbeatStore := newTestWorkerHeartbeatStore(baseDir)
		activeStore := newTestActiveDistributedRunStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunRepository:          store.repository,
			WorkerHeartbeatStore:      heartbeatStore,
			DAGRunLeaseStore:          leaseStore,
			ActiveDistributedRunStore: activeStore,
			StaleHeartbeatThreshold:   time.Minute,
			StaleLeaseThreshold:       time.Second,
		})

		ref := ir.DAGRunRef{Name: "lease-dag", ID: "run-lease"}
		attemptKey := "lease-key-1"
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       "lease-dag",
			DAGRunID:   "run-lease",
			AttemptID:  "attempt-1",
			AttemptKey: attemptKey,
			Status:     ir.Running,
			WorkerID:   "worker-1",
			Nodes: []*ir.Node{
				{Status: ir.NodeRunning},
			},
		})
		staleAt := time.Now().Add(-10 * time.Second).UTC()
		require.NoError(t, activeStore.Upsert(ctx, dispatch.ActiveDistributedRun{
			AttemptKey: attemptKey,
			DAGRun:     ref,
			Root:       ref,
			AttemptID:  "attempt-1",
			WorkerID:   "worker-1",
			Status:     ir.Running,
			UpdatedAt:  staleAt.UnixMilli(),
		}))
		require.NoError(t, heartbeatStore.Upsert(ctx, dispatch.WorkerHeartbeatRecord{
			WorkerID:        "worker-1",
			LastHeartbeatAt: time.Now().UTC().UnixMilli(),
			Stats: &dispatch.WorkerStats{
				RunningTasks: []*dispatch.RunningTask{
					{
						DAGRunID:       "run-lease",
						DAGName:        "lease-dag",
						RootDAGRunID:   "run-lease",
						RootDAGRunName: "lease-dag",
						AttemptKey:     attemptKey,
					},
				},
			},
		}))

		h.detectStaleLeases(ctx)

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, ir.Running, status.Status)
		assert.Equal(t, ir.NodeRunning, status.Nodes[0].Status)

		lease, err := leaseStore.Get(ctx, attemptKey)
		require.NoError(t, err)
		assert.Equal(t, attemptKey, lease.AttemptKey)
		assert.Equal(t, "worker-1", lease.WorkerID)

		record, err := activeStore.Get(ctx, attemptKey)
		require.NoError(t, err)
		assert.Equal(t, attemptKey, record.AttemptKey)
		assert.Equal(t, "worker-1", record.WorkerID)
	})

	t.Run("DetectStaleLeasesRebuildsActiveIndexFromLeasesWithoutStatusScan", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		store := newMockDAGRunStore()
		baseDir := filepath.Join(t.TempDir(), "distributed")
		leaseStore := newTestDAGRunLeaseStore(baseDir)
		activeStore := newTestActiveDistributedRunStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunRepository:          store.repository,
			DAGRunLeaseStore:          leaseStore,
			ActiveDistributedRunStore: activeStore,
			StaleLeaseThreshold:       time.Minute,
		})

		ref := ir.DAGRunRef{Name: "lease-dag", ID: "run-lease"}
		attemptKey := "lease-key-1"
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       "lease-dag",
			DAGRunID:   "run-lease",
			AttemptID:  "attempt-1",
			AttemptKey: attemptKey,
			Status:     ir.Running,
			WorkerID:   "worker-1",
			Nodes: []*ir.Node{
				{Status: ir.NodeRunning},
			},
		})
		freshAt := time.Now().UTC()
		require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
			AttemptKey:      attemptKey,
			DAGRun:          ref,
			Root:            ref,
			AttemptID:       "attempt-1",
			QueueName:       "lease-dag",
			WorkerID:        "worker-1",
			LastHeartbeatAt: freshAt.UnixMilli(),
			ClaimedAt:       freshAt.UnixMilli(),
		}))

		h.detectStaleLeases(ctx)

		assert.Zero(t, store.ListStatusesCallCount())

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, ir.Running, status.Status)

		record, err := activeStore.Get(ctx, attemptKey)
		require.NoError(t, err)
		assert.Equal(t, ref, record.DAGRun)
		assert.Equal(t, "attempt-1", record.AttemptID)
		assert.Equal(t, "worker-1", record.WorkerID)
	})

	t.Run("DetectIndexedDistributedStatusesFailsActiveEntryWhenLeaseMissing", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		store := newMockDAGRunStore()
		baseDir := filepath.Join(t.TempDir(), "distributed")
		leaseStore := newTestDAGRunLeaseStore(baseDir)
		activeStore := newTestActiveDistributedRunStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunRepository:          store.repository,
			DAGRunLeaseStore:          leaseStore,
			ActiveDistributedRunStore: activeStore,
			StaleLeaseThreshold:       time.Second,
		})

		ref := ir.DAGRunRef{Name: "lease-dag", ID: "run-lease"}
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       "lease-dag",
			DAGRunID:   "run-lease",
			AttemptID:  "attempt-1",
			AttemptKey: "lease-key-1",
			Status:     ir.Running,
			WorkerID:   "worker-1",
			Nodes: []*ir.Node{
				{Status: ir.NodeRunning},
			},
		})
		require.NoError(t, activeStore.Upsert(ctx, dispatch.ActiveDistributedRun{
			AttemptKey: "lease-key-1",
			DAGRun:     ref,
			Root:       ref,
			AttemptID:  "attempt-1",
			WorkerID:   "worker-1",
			Status:     ir.Running,
		}))

		h.detectStaleLeases(ctx)

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, ir.Failed, status.Status)
		assert.Equal(t, dispatch.DistributedLeaseExpiredReason("worker-1"), status.Error)

		records, err := activeStore.ListAll(ctx)
		require.NoError(t, err)
		assert.Empty(t, records)
	})

	t.Run("DetectStaleLeasesDoesNotScanStatusesWhenActiveIndexMissesRun", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		store := newMockDAGRunStore()
		baseDir := filepath.Join(t.TempDir(), "distributed")
		leaseStore := newTestDAGRunLeaseStore(baseDir)
		activeStore := newTestActiveDistributedRunStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunRepository:          store.repository,
			DAGRunLeaseStore:          leaseStore,
			ActiveDistributedRunStore: activeStore,
			StaleLeaseThreshold:       time.Second,
		})

		ref := ir.DAGRunRef{Name: "lease-dag", ID: "run-missing-index"}
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       "lease-dag",
			DAGRunID:   "run-missing-index",
			AttemptID:  "attempt-1",
			AttemptKey: "lease-key-missing-index",
			Status:     ir.Running,
			WorkerID:   "worker-1",
			Nodes: []*ir.Node{
				{Status: ir.NodeRunning},
			},
		})

		h.detectStaleLeases(ctx)

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, ir.Running, status.Status)
		assert.Zero(t, store.ListStatusesCallCount())

		records, err := activeStore.ListAll(ctx)
		require.NoError(t, err)
		assert.Empty(t, records)
	})

	t.Run("DetectStaleLeasesDeletesTrackingForCorruptedStatus", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		store := newMockDAGRunStore()
		baseDir := filepath.Join(t.TempDir(), "distributed")
		leaseStore := newTestDAGRunLeaseStore(baseDir)
		activeStore := newTestActiveDistributedRunStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunRepository:          store.repository,
			DAGRunLeaseStore:          leaseStore,
			ActiveDistributedRunStore: activeStore,
			StaleLeaseThreshold:       time.Second,
		})

		ref := ir.DAGRunRef{Name: "lease-dag", ID: "run-corrupted"}
		attemptKey := "lease-key-corrupted"
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       "lease-dag",
			DAGRunID:   "run-corrupted",
			AttemptID:  "attempt-1",
			AttemptKey: attemptKey,
			Status:     ir.Running,
			WorkerID:   "worker-1",
		})
		attempt.readStatusError = dagrun.ErrCorruptedStatusData

		staleAt := time.Now().Add(-10 * time.Second).UTC()
		require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
			AttemptKey:      attemptKey,
			DAGRun:          ref,
			Root:            ref,
			AttemptID:       "attempt-1",
			QueueName:       "lease-dag",
			WorkerID:        "worker-1",
			LastHeartbeatAt: staleAt.UnixMilli(),
			ClaimedAt:       staleAt.UnixMilli(),
		}))
		require.NoError(t, activeStore.Upsert(ctx, dispatch.ActiveDistributedRun{
			AttemptKey: attemptKey,
			DAGRun:     ref,
			Root:       ref,
			AttemptID:  "attempt-1",
			WorkerID:   "worker-1",
			Status:     ir.Running,
		}))

		h.detectStaleLeases(ctx)

		assert.Zero(t, store.ListStatusesCallCount())
		_, err := leaseStore.Get(ctx, attemptKey)
		assert.ErrorIs(t, err, dispatch.ErrDAGRunLeaseNotFound)
		_, err = activeStore.Get(ctx, attemptKey)
		assert.ErrorIs(t, err, dispatch.ErrActiveRunNotFound)
	})
}

func TestHandler_ReportStatus(t *testing.T) {
	t.Parallel()

	t.Run("ValidStatusReport", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		// Create an attempt for the DAG run
		ref := ir.DAGRunRef{Name: "test-dag", ID: "run-123"}
		store.addAttempt(ref, &ir.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   ir.Running,
		})

		// Report status
		protoStatus, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   ir.Running,
		})
		require.NoError(t, convErr)

		req := &coordinatorv1.ReportStatusRequest{
			Status: protoStatus,
		}

		resp, err := h.ReportStatus(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.True(t, resp.Accepted)
	})

	t.Run("CoordinatorStampsLeaseAtOnReportStatus", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		ref := ir.DAGRunRef{Name: "test-dag", ID: "run-123"}
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   ir.Running,
		})

		protoStatus, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   ir.Running,
			LeaseAt:  1,
		})
		require.NoError(t, convErr)

		_, err := h.ReportStatus(ctx, &coordinatorv1.ReportStatusRequest{Status: protoStatus})
		require.NoError(t, err)

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Greater(t, status.LeaseAt, int64(1))
		assert.WithinDuration(t, time.Now(), time.UnixMilli(status.LeaseAt), 2*time.Second)
	})

	t.Run("PreservesPersistedLabels", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		ref := ir.DAGRunRef{Name: "test-dag", ID: "run-123"}
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:      ref.Name,
			DAGRunID:  ref.ID,
			AttemptID: "attempt-1",
			Status:    ir.Running,
			Labels:    []string{"workspace=ops"},
		})

		protoStatus, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:      ref.Name,
			DAGRunID:  ref.ID,
			AttemptID: "attempt-1",
			Status:    ir.Failed,
		})
		require.NoError(t, convErr)

		resp, err := h.ReportStatus(ctx, &coordinatorv1.ReportStatusRequest{Status: protoStatus})
		require.NoError(t, err)
		require.True(t, resp.Accepted)

		persisted, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, []string{"workspace=ops"}, persisted.Labels)
	})

	t.Run("WaitingStatusClosesCachedAttemptBeforePersisting", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		ref := ir.DAGRunRef{Name: "test-dag", ID: "run-123"}
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:      ref.Name,
			DAGRunID:  ref.ID,
			AttemptID: "attempt-1",
			Status:    ir.Running,
		})
		runningProto, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:      ref.Name,
			DAGRunID:  ref.ID,
			AttemptID: "attempt-1",
			Status:    ir.Running,
		})
		require.NoError(t, convErr)
		runningResp, err := h.ReportStatus(ctx, &coordinatorv1.ReportStatusRequest{Status: runningProto})
		require.NoError(t, err)
		require.True(t, runningResp.Accepted)

		protoStatus, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:       ref.Name,
			DAGRunID:   ref.ID,
			AttemptID:  "attempt-1",
			Status:     ir.Waiting,
			FinishedAt: time.Now().UTC().Format(time.RFC3339),
		})
		require.NoError(t, convErr)

		resp, err := h.ReportStatus(ctx, &coordinatorv1.ReportStatusRequest{Status: protoStatus})
		require.NoError(t, err)
		require.True(t, resp.Accepted)
		assert.True(t, attempt.WasClosed())
		assert.Equal(t, 1, store.CompareAndSwapCallCount())

		h.attemptsMu.RLock()
		_, cached := h.openAttempts[ref.ID]
		h.attemptsMu.RUnlock()
		assert.False(t, cached)
	})

	t.Run("RejectsWaitingStatusThatRegressesCompletedHumanTask", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ref := ir.NewDAGRunRef("test-dag", "run-123")
		completedAt := time.Now().UTC().Format(time.RFC3339)
		outputs := `{"environment":"production"}`
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       ref.Name,
			DAGRunID:   ref.ID,
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.Waiting,
			Nodes: []*ir.Node{{
				Step:                   ir.Step{ID: "review", HumanTask: &ir.HumanTaskConfig{Prompt: "Review"}},
				Status:                 ir.NodeSucceeded,
				FinishedAt:             completedAt,
				StepOutputsValue:       &outputs,
				HumanTaskInput:         []byte(`{"environment":"production"}`),
				HumanTaskCompletedBy:   "operator",
				HumanTaskCompletedByID: "user-1",
			}},
		})
		incoming, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:       ref.Name,
			DAGRunID:   ref.ID,
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.Waiting,
			Nodes: []*ir.Node{{
				Step:   ir.Step{ID: "review", HumanTask: &ir.HumanTaskConfig{Prompt: "Review"}},
				Status: ir.NodeWaiting,
			}},
		})
		require.NoError(t, convErr)

		resp, err := h.ReportStatus(t.Context(), &coordinatorv1.ReportStatusRequest{Status: incoming})
		require.NoError(t, err)
		require.False(t, resp.Accepted)
		assert.Equal(t, remoteAttemptRejectedManualAction, resp.Error)

		persisted, err := attempt.ReadStatus(t.Context())
		require.NoError(t, err)
		require.Len(t, persisted.Nodes, 1)
		assert.Equal(t, ir.NodeSucceeded, persisted.Nodes[0].Status)
		assert.JSONEq(t, `{"environment":"production"}`, string(persisted.Nodes[0].HumanTaskInput))
		assert.Equal(t, "operator", persisted.Nodes[0].HumanTaskCompletedBy)
		assert.Equal(t, "user-1", persisted.Nodes[0].HumanTaskCompletedByID)
		require.NotNil(t, persisted.Nodes[0].StepOutputsValue)
		assert.JSONEq(t, outputs, *persisted.Nodes[0].StepOutputsValue)
	})

	t.Run("RejectsWaitingStatusThatRegressesCompletedApproval", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ref := ir.NewDAGRunRef("test-dag", "run-123")
		approvedAt := time.Now().UTC().Format(time.RFC3339)
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       ref.Name,
			DAGRunID:   ref.ID,
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.Waiting,
			Nodes: []*ir.Node{{
				Step:         ir.Step{Name: "review", Approval: &ir.ApprovalConfig{}},
				Status:       ir.NodeSucceeded,
				ApprovedAt:   approvedAt,
				ApprovedBy:   "operator",
				ApprovedByID: "user-1",
				ApprovalInputs: map[string]string{
					"environment": "production",
				},
			}},
		})
		incoming, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:       ref.Name,
			DAGRunID:   ref.ID,
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.Waiting,
			Nodes: []*ir.Node{{
				Step:   ir.Step{Name: "review", Approval: &ir.ApprovalConfig{}},
				Status: ir.NodeWaiting,
			}},
		})
		require.NoError(t, convErr)

		resp, err := h.ReportStatus(t.Context(), &coordinatorv1.ReportStatusRequest{Status: incoming})
		require.NoError(t, err)
		require.False(t, resp.Accepted)
		assert.Equal(t, remoteAttemptRejectedManualAction, resp.Error)

		persisted, err := attempt.ReadStatus(t.Context())
		require.NoError(t, err)
		require.Len(t, persisted.Nodes, 1)
		assert.Equal(t, ir.NodeSucceeded, persisted.Nodes[0].Status)
		assert.Equal(t, approvedAt, persisted.Nodes[0].ApprovedAt)
		assert.Equal(t, "operator", persisted.Nodes[0].ApprovedBy)
		assert.Equal(t, "user-1", persisted.Nodes[0].ApprovedByID)
		assert.Equal(t, map[string]string{"environment": "production"}, persisted.Nodes[0].ApprovalInputs)
	})

	t.Run("RejectsWaitingStatusThatRegressesPushBack", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ref := ir.NewDAGRunRef("test-dag", "run-123")
		pushedBackAt := time.Now().UTC().Format(time.RFC3339)
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       ref.Name,
			DAGRunID:   ref.ID,
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.Waiting,
			Nodes: []*ir.Node{{
				Step:                   ir.Step{ID: "prepare", Name: "prepare"},
				StartedAt:              "-",
				FinishedAt:             "-",
				Status:                 ir.NodeNotStarted,
				ApprovalIteration:      1,
				PushBackInputs:         map[string]string{"FEEDBACK": "revise"},
				PushBackPreviousStdout: "/tmp/prepare.out",
				PushBackHistory: []ir.PushBackEntry{{
					Iteration: 1,
					By:        "operator",
					ByID:      "user-1",
					At:        pushedBackAt,
					Inputs:    map[string]string{"FEEDBACK": "revise"},
				}},
			}},
		})
		incoming, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:       ref.Name,
			DAGRunID:   ref.ID,
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.Waiting,
			Nodes: []*ir.Node{{
				Step:       ir.Step{ID: "prepare", Name: "prepare"},
				StartedAt:  pushedBackAt,
				FinishedAt: pushedBackAt,
				Status:     ir.NodeSucceeded,
				Stdout:     "/tmp/prepare.out",
			}},
		})
		require.NoError(t, convErr)

		resp, err := h.ReportStatus(t.Context(), &coordinatorv1.ReportStatusRequest{Status: incoming})
		require.NoError(t, err)
		require.False(t, resp.Accepted)
		assert.Equal(t, remoteAttemptRejectedManualAction, resp.Error)

		persisted, err := attempt.ReadStatus(t.Context())
		require.NoError(t, err)
		require.Len(t, persisted.Nodes, 1)
		assert.Equal(t, ir.NodeNotStarted, persisted.Nodes[0].Status)
		assert.Equal(t, 1, persisted.Nodes[0].ApprovalIteration)
		assert.Equal(t, map[string]string{"FEEDBACK": "revise"}, persisted.Nodes[0].PushBackInputs)
		assert.Equal(t, "/tmp/prepare.out", persisted.Nodes[0].PushBackPreviousStdout)
		require.Len(t, persisted.Nodes[0].PushBackHistory, 1)
		assert.Equal(t, "user-1", persisted.Nodes[0].PushBackHistory[0].ByID)
	})

	t.Run("AcceptsWaitingStatusThatPreservesPushBack", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ref := ir.NewDAGRunRef("test-dag", "run-123")
		pushedBackAt := time.Now().UTC().Format(time.RFC3339)
		history := []ir.PushBackEntry{{
			Iteration: 1,
			By:        "operator",
			ByID:      "user-1",
			At:        pushedBackAt,
			Inputs:    map[string]string{"FEEDBACK": "revise"},
		}}
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       ref.Name,
			DAGRunID:   ref.ID,
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.Running,
			Nodes: []*ir.Node{{
				Step:                   ir.Step{ID: "prepare", Name: "prepare"},
				StartedAt:              "-",
				FinishedAt:             "-",
				Status:                 ir.NodeNotStarted,
				ApprovalIteration:      1,
				PushBackInputs:         map[string]string{"FEEDBACK": "revise"},
				PushBackHistory:        history,
				PushBackPreviousStdout: "/tmp/prepare.out",
			}},
		})
		incoming, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:       ref.Name,
			DAGRunID:   ref.ID,
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.Waiting,
			Nodes: []*ir.Node{{
				Step:                   ir.Step{ID: "prepare", Name: "prepare"},
				StartedAt:              pushedBackAt,
				FinishedAt:             pushedBackAt,
				Status:                 ir.NodeSucceeded,
				ApprovalIteration:      1,
				PushBackInputs:         map[string]string{"FEEDBACK": "revise"},
				PushBackHistory:        history,
				PushBackPreviousStdout: "/tmp/prepare.out",
			}},
		})
		require.NoError(t, convErr)

		resp, err := h.ReportStatus(t.Context(), &coordinatorv1.ReportStatusRequest{Status: incoming})
		require.NoError(t, err)
		require.True(t, resp.Accepted)

		persisted, err := attempt.ReadStatus(t.Context())
		require.NoError(t, err)
		require.Len(t, persisted.Nodes, 1)
		assert.Equal(t, ir.NodeSucceeded, persisted.Nodes[0].Status)
		assert.Equal(t, 1, persisted.Nodes[0].ApprovalIteration)
		assert.Equal(t, history, persisted.Nodes[0].PushBackHistory)
	})

	t.Run("AcceptsWaitingStatusThatPreservesCompletedHumanTask", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ref := ir.NewDAGRunRef("test-dag", "run-123")
		completedAt := time.Now().UTC().Format(time.RFC3339)
		completedNode := &ir.Node{
			Step:                   ir.Step{ID: "review", HumanTask: &ir.HumanTaskConfig{Prompt: "Review"}},
			Status:                 ir.NodeSucceeded,
			FinishedAt:             completedAt,
			HumanTaskInput:         []byte(`{"approved":true}`),
			HumanTaskCompletedBy:   "operator",
			HumanTaskCompletedByID: "user-1",
		}
		store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       ref.Name,
			DAGRunID:   ref.ID,
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.Queued,
			Nodes:      []*ir.Node{completedNode},
		})
		incoming, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:       ref.Name,
			DAGRunID:   ref.ID,
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.Waiting,
			Nodes: []*ir.Node{
				completedNode,
				{
					Step:   ir.Step{ID: "publish", HumanTask: &ir.HumanTaskConfig{Prompt: "Publish"}},
					Status: ir.NodeWaiting,
				},
			},
		})
		require.NoError(t, convErr)

		resp, err := h.ReportStatus(t.Context(), &coordinatorv1.ReportStatusRequest{Status: incoming})
		require.NoError(t, err)
		require.True(t, resp.Accepted)
	})

	t.Run("RejectsLateStatusForLeaseCleanedAttempt", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		leaseStore := newTestDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
		h := NewHandler(HandlerConfig{
			DAGRunRepository: store.repository,
			DAGRunLeaseStore: leaseStore,
			Owner:            dispatch.CoordinatorEndpoint{ID: "coord-a"},
		})
		ctx := context.Background()

		ref := ir.DAGRunRef{Name: "test-dag", ID: "run-123"}
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.Failed,
		})

		protoStatus, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.Running,
		})
		require.NoError(t, convErr)

		resp, err := h.ReportStatus(ctx, &coordinatorv1.ReportStatusRequest{
			Status:             protoStatus,
			WorkerId:           "worker-1",
			OwnerCoordinatorId: "coord-a",
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.False(t, resp.Accepted)
		assert.Equal(t, remoteAttemptRejectedLeaseInactive, resp.Error)
		assert.False(t, attempt.WasWritten())

		current, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, ir.Failed, current.Status)

		_, err = leaseStore.Get(ctx, "attempt-key-1")
		assert.ErrorIs(t, err, dispatch.ErrDAGRunLeaseNotFound)
	})

	t.Run("RejectsStatusWithoutRequestWorkerIdentity", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		leaseStore := newTestDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
		h := NewHandler(HandlerConfig{
			DAGRunRepository: store.repository,
			DAGRunLeaseStore: leaseStore,
			Owner:            dispatch.CoordinatorEndpoint{ID: "coord-a"},
		})
		ctx := context.Background()

		ref := ir.NewDAGRunRef("test-dag", "run-123")
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       ref.Name,
			DAGRunID:   ref.ID,
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			WorkerID:   "worker-1",
			Status:     ir.Running,
		})
		require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
			AttemptKey: "attempt-key-1",
			DAGRun:     ref,
			Root:       ref,
			AttemptID:  "attempt-1",
			WorkerID:   "worker-1",
		}))
		incoming, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:      ref.Name,
			DAGRunID:  ref.ID,
			AttemptID: "attempt-1",
			WorkerID:  "worker-1",
			Status:    ir.Succeeded,
		})
		require.NoError(t, convErr)

		_, err := h.ReportStatus(ctx, &coordinatorv1.ReportStatusRequest{Status: incoming})
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.False(t, attempt.WasWritten())
		_, err = leaseStore.Get(ctx, "attempt-key-1")
		require.NoError(t, err)
	})

	t.Run("BootstrapsMissingSubAttemptFromRemoteStatus", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		baseDir := filepath.Join(t.TempDir(), "distributed")
		leaseStore := newTestDAGRunLeaseStore(baseDir)
		activeStore := newTestActiveDistributedRunStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunRepository:          store.repository,
			DAGRunLeaseStore:          leaseStore,
			ActiveDistributedRunStore: activeStore,
			Owner:                     dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		})
		ctx := context.Background()

		rootRef := ir.DAGRunRef{Name: "root-dag", ID: "root-run-123"}
		rootAttemptID := "root-attempt-1"
		rootAttemptKey := ir.GenerateAttemptKey(rootRef.Name, rootRef.ID, rootRef.Name, rootRef.ID, rootAttemptID)
		store.addAttempt(rootRef, &ir.DAGRunStatus{
			Name:       rootRef.Name,
			DAGRunID:   rootRef.ID,
			Root:       rootRef,
			AttemptID:  rootAttemptID,
			AttemptKey: rootAttemptKey,
			Status:     ir.Running,
			WorkerID:   "worker-1",
		})
		require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
			AttemptKey: rootAttemptKey,
			DAGRun:     rootRef,
			Root:       rootRef,
			AttemptID:  rootAttemptID,
			WorkerID:   "worker-1",
			Owner:      dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		}))

		runningProto, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:      "child-dag",
			DAGRunID:  "child-run-123",
			Root:      rootRef,
			AttemptID: "child-attempt-1",
			ProcGroup: "child-queue",
			Status:    ir.Running,
			WorkerID:  "worker-1",
		})
		require.NoError(t, convErr)

		resp, err := h.ReportStatus(ctx, &coordinatorv1.ReportStatusRequest{
			Status:             runningProto,
			WorkerId:           "worker-1",
			OwnerCoordinatorId: "coord-a",
			SourceFile:         "/dags/child-file.yaml",
			Labels:             "workspace=ops, team=platform",
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.True(t, resp.Accepted)

		attempt, err := store.FindSubAttempt(ctx, rootRef, "child-run-123")
		require.NoError(t, err)
		storedAttempt, ok := attempt.(*mockAttempt)
		require.True(t, ok)
		current, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)

		attemptKey := ir.GenerateAttemptKey(rootRef.Name, rootRef.ID, "child-dag", "child-run-123", "child-attempt-1")
		assert.Equal(t, "child-dag", current.Name)
		assert.Equal(t, "child-run-123", current.DAGRunID)
		assert.Equal(t, rootRef, current.Root)
		assert.Equal(t, "child-attempt-1", current.AttemptID)
		assert.Equal(t, attemptKey, current.AttemptKey)
		assert.Equal(t, rootAttemptKey, current.ClaimKey)
		assert.Equal(t, ir.Running, current.Status)
		assert.Equal(t, "worker-1", current.WorkerID)
		assert.Equal(t, []string{"workspace=ops", "team=platform"}, current.Labels)
		dag, err := attempt.ReadDAG(ctx)
		require.NoError(t, err)
		assert.Equal(t, "child-dag", dag.Name)
		assert.Equal(t, "/dags/child-file.yaml", dag.SourceFile)

		lease, err := leaseStore.Get(ctx, rootAttemptKey)
		require.NoError(t, err)
		assert.Equal(t, rootAttemptID, lease.AttemptID)
		assert.Equal(t, "worker-1", lease.WorkerID)
		assert.Equal(t, "coord-a", lease.Owner.ID)

		record, err := activeStore.Get(ctx, attemptKey)
		require.NoError(t, err)
		assert.Equal(t, "child-attempt-1", record.AttemptID)
		assert.Equal(t, "worker-1", record.WorkerID)
		assert.Equal(t, ir.Running, record.Status)

		succeededProto, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:       "child-dag",
			DAGRunID:   "child-run-123",
			Root:       rootRef,
			AttemptID:  "child-attempt-1",
			AttemptKey: attemptKey,
			Status:     ir.Succeeded,
			WorkerID:   "worker-1",
		})
		require.NoError(t, convErr)

		resp, err = h.ReportStatus(ctx, &coordinatorv1.ReportStatusRequest{
			Status:             succeededProto,
			WorkerId:           "worker-1",
			OwnerCoordinatorId: "coord-a",
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.True(t, resp.Accepted)

		_, err = leaseStore.Get(ctx, rootAttemptKey)
		require.NoError(t, err)

		_, err = activeStore.Get(ctx, attemptKey)
		assert.ErrorIs(t, err, dispatch.ErrActiveRunNotFound)
		assert.True(t, storedAttempt.WasClosed())

		h.attemptsMu.RLock()
		_, cached := h.openAttempts["child-run-123"]
		h.attemptsMu.RUnlock()
		assert.False(t, cached)
	})

	t.Run("ClosesBootstrappedAttemptAfterWriteError", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		store.attemptWriteErr = errors.New("status write failed")
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		rootRef := ir.NewDAGRunRef("root-dag", "root-run-123")
		store.addAttempt(rootRef, &ir.DAGRunStatus{
			Name:     rootRef.Name,
			DAGRunID: rootRef.ID,
			Status:   ir.Running,
		})
		incoming, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:      "child-dag",
			DAGRunID:  "child-run-123",
			Root:      rootRef,
			AttemptID: "child-attempt-1",
			WorkerID:  "worker-1",
			Status:    ir.Failed,
		})
		require.NoError(t, convErr)

		_, err := h.ReportStatus(ctx, &coordinatorv1.ReportStatusRequest{
			Status:   incoming,
			WorkerId: "worker-1",
		})
		require.Error(t, err)
		assert.Equal(t, codes.Internal, status.Code(err))

		attempt := store.subAttempts[rootRef.ID+":child-run-123"]
		require.NotNil(t, attempt)
		assert.True(t, attempt.WasClosed())

		h.attemptsMu.RLock()
		_, cached := h.openAttempts["child-run-123"]
		h.attemptsMu.RUnlock()
		assert.False(t, cached)
	})

	t.Run("RejectsMissingSubAttemptWithoutRootLease", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		leaseStore := newTestDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
		h := NewHandler(HandlerConfig{
			DAGRunRepository: store.repository,
			DAGRunLeaseStore: leaseStore,
			Owner:            dispatch.CoordinatorEndpoint{ID: "coord-a"},
		})
		ctx := context.Background()

		rootRef := ir.NewDAGRunRef("root-dag", "root-run-123")
		store.addAttempt(rootRef, &ir.DAGRunStatus{
			Name:       rootRef.Name,
			DAGRunID:   rootRef.ID,
			Root:       rootRef,
			AttemptID:  "root-attempt-1",
			AttemptKey: "root-attempt-key-1",
			WorkerID:   "worker-1",
			Status:     ir.Running,
		})
		incoming, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:      "child-dag",
			DAGRunID:  "child-run-123",
			Root:      rootRef,
			AttemptID: "child-attempt-1",
			WorkerID:  "worker-1",
			Status:    ir.Running,
		})
		require.NoError(t, convErr)

		resp, err := h.ReportStatus(ctx, &coordinatorv1.ReportStatusRequest{
			Status:   incoming,
			WorkerId: "worker-1",
		})
		require.NoError(t, err)
		require.False(t, resp.Accepted)
		assert.Equal(t, remoteAttemptRejectedLeaseInactive, resp.Error)
		_, err = store.FindSubAttempt(ctx, rootRef, "child-run-123")
		assert.ErrorIs(t, err, dagrun.ErrDAGRunIDNotFound)
	})

	t.Run("BootstrapsMissingSubAttemptRecomputesLogPathsAfterAttemptID", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		baseDir := filepath.Join(t.TempDir(), "distributed")
		logDir := filepath.Join(t.TempDir(), "logs")
		leaseStore := newTestDAGRunLeaseStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunRepository: store.repository,
			DAGRunLeaseStore: leaseStore,
			LogDir:           logDir,
			Owner:            dispatch.CoordinatorEndpoint{ID: "coord-a"},
		})
		ctx := context.Background()

		rootRef := ir.DAGRunRef{Name: "root-dag", ID: "root-run-123"}
		rootAttemptID := "root-attempt-1"
		rootAttemptKey := ir.GenerateAttemptKey(rootRef.Name, rootRef.ID, rootRef.Name, rootRef.ID, rootAttemptID)
		store.addAttempt(rootRef, &ir.DAGRunStatus{
			Name:       rootRef.Name,
			DAGRunID:   rootRef.ID,
			Root:       rootRef,
			AttemptID:  rootAttemptID,
			AttemptKey: rootAttemptKey,
			Status:     ir.Running,
			WorkerID:   "worker-1",
		})
		require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
			AttemptKey: rootAttemptKey,
			DAGRun:     rootRef,
			Root:       rootRef,
			AttemptID:  rootAttemptID,
			WorkerID:   "worker-1",
			Owner:      dispatch.CoordinatorEndpoint{ID: "coord-a"},
		}))

		runningProto, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:      "child-dag",
			DAGRunID:  "child-run-123",
			Root:      rootRef,
			ProcGroup: "child-queue",
			Status:    ir.Running,
			WorkerID:  "worker-1",
			Nodes: []*ir.Node{
				{
					Step:   ir.Step{Name: "child-step"},
					Status: ir.NodeRunning,
				},
			},
		})
		require.NoError(t, convErr)

		resp, err := h.ReportStatus(ctx, &coordinatorv1.ReportStatusRequest{
			Status:             runningProto,
			WorkerId:           "worker-1",
			OwnerCoordinatorId: "coord-a",
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.True(t, resp.Accepted)

		attempt, err := store.FindSubAttempt(ctx, rootRef, "child-run-123")
		require.NoError(t, err)
		current, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)

		assert.Equal(t, "test-attempt", current.AttemptID)
		assert.Equal(t, rootAttemptKey, current.ClaimKey)
		expectedDir := filepath.Join(logDir, "root-dag", "root-run-123", "test-attempt")
		assert.Equal(t, filepath.Join(expectedDir, "scheduler.log"), current.Log)
		require.Len(t, current.Nodes, 1)
		assert.Equal(t, filepath.Join(expectedDir, "child-step.stdout.log"), current.Nodes[0].Stdout)
		assert.Equal(t, filepath.Join(expectedDir, "child-step.stderr.log"), current.Nodes[0].Stderr)
	})

	t.Run("DoesNotBootstrapMissingSubAttemptWithoutRootAttempt", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		rootRef := ir.DAGRunRef{Name: "root-dag", ID: "missing-root-run"}
		protoStatus, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:     "child-dag",
			DAGRunID: "child-run-123",
			Root:     rootRef,
			Status:   ir.Running,
			WorkerID: "worker-1",
		})
		require.NoError(t, convErr)

		resp, err := h.ReportStatus(ctx, &coordinatorv1.ReportStatusRequest{
			Status:   protoStatus,
			WorkerId: "worker-1",
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.False(t, resp.Accepted)
		assert.Equal(t, remoteAttemptRejectedLeaseInactive, resp.Error)

		_, err = store.FindSubAttempt(ctx, rootRef, "child-run-123")
		assert.ErrorIs(t, err, dagrun.ErrDAGRunIDNotFound)
	})

	t.Run("DoesNotBootstrapMissingSubAttemptUnderTerminalRoot", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		rootRef := ir.DAGRunRef{Name: "root-dag", ID: "root-run-123"}
		store.addAttempt(rootRef, &ir.DAGRunStatus{
			Name:     rootRef.Name,
			DAGRunID: rootRef.ID,
			Root:     rootRef,
			Status:   ir.Succeeded,
		})

		protoStatus, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:     "child-dag",
			DAGRunID: "child-run-123",
			Root:     rootRef,
			Status:   ir.Running,
			WorkerID: "worker-1",
		})
		require.NoError(t, convErr)

		resp, err := h.ReportStatus(ctx, &coordinatorv1.ReportStatusRequest{
			Status:   protoStatus,
			WorkerId: "worker-1",
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.False(t, resp.Accepted)
		assert.Equal(t, remoteAttemptRejectedLeaseInactive, resp.Error)

		_, err = store.FindSubAttempt(ctx, rootRef, "child-run-123")
		assert.ErrorIs(t, err, dagrun.ErrDAGRunIDNotFound)
	})

	t.Run("DoesNotBootstrapMissingSubAttemptWithMismatchedWorkerID", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		rootRef := ir.DAGRunRef{Name: "root-dag", ID: "root-run-123"}
		store.addAttempt(rootRef, &ir.DAGRunStatus{
			Name:     rootRef.Name,
			DAGRunID: rootRef.ID,
			Root:     rootRef,
			Status:   ir.Running,
		})

		protoStatus, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:     "child-dag",
			DAGRunID: "child-run-123",
			Root:     rootRef,
			Status:   ir.Running,
			WorkerID: "worker-1",
		})
		require.NoError(t, convErr)

		resp, err := h.ReportStatus(ctx, &coordinatorv1.ReportStatusRequest{
			Status:   protoStatus,
			WorkerId: "worker-2",
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.False(t, resp.Accepted)
		assert.Equal(t, remoteAttemptRejectedLeaseInactive, resp.Error)

		_, err = store.FindSubAttempt(ctx, rootRef, "child-run-123")
		assert.ErrorIs(t, err, dagrun.ErrDAGRunIDNotFound)
	})

	t.Run("AcceptsCancelledTerminalStatusAfterLeaseFailure", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		baseDir := filepath.Join(t.TempDir(), "distributed")
		leaseStore := newTestDAGRunLeaseStore(baseDir)
		activeStore := newTestActiveDistributedRunStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunRepository:          store.repository,
			DAGRunLeaseStore:          leaseStore,
			ActiveDistributedRunStore: activeStore,
			Owner:                     dispatch.CoordinatorEndpoint{ID: "coord-a"},
		})
		ctx := context.Background()

		ref := ir.DAGRunRef{Name: "test-dag", ID: "run-123"}
		attempt := store.addAbortingAttempt(ref, &ir.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			WorkerID:   "worker-1",
			Status:     ir.Failed,
		})

		protoStatus, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			WorkerID:   "worker-1",
			Status:     ir.Aborted,
			Error:      context.Canceled.Error(),
		})
		require.NoError(t, convErr)

		resp, err := h.ReportStatus(ctx, &coordinatorv1.ReportStatusRequest{
			Status:             protoStatus,
			WorkerId:           "worker-1",
			OwnerCoordinatorId: "coord-a",
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.True(t, resp.Accepted)
		assert.True(t, attempt.WasWritten())

		current, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, ir.Aborted, current.Status)
		assert.Equal(t, context.Canceled.Error(), current.Error)

		_, err = leaseStore.Get(ctx, "attempt-key-1")
		assert.ErrorIs(t, err, dispatch.ErrDAGRunLeaseNotFound)

		_, err = activeStore.Get(ctx, "attempt-key-1")
		assert.ErrorIs(t, err, dispatch.ErrActiveRunNotFound)
	})

	t.Run("RejectsSupersededAttemptStatus", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		ref := ir.DAGRunRef{Name: "test-dag", ID: "run-123"}
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-2",
			AttemptKey: "attempt-key-2",
			Status:     ir.Running,
		})

		protoStatus, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.Running,
		})
		require.NoError(t, convErr)

		resp, err := h.ReportStatus(ctx, &coordinatorv1.ReportStatusRequest{Status: protoStatus})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.False(t, resp.Accepted)
		assert.Equal(t, remoteAttemptRejectedSuperseded, resp.Error)
		assert.False(t, attempt.WasWritten())

		current, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, "attempt-2", current.AttemptID)
		assert.Equal(t, "attempt-key-2", current.AttemptKey)
	})

	t.Run("AcceptsDuplicateTerminalStatusAndPersistsFollowUpData", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		baseDir := filepath.Join(t.TempDir(), "distributed")
		leaseStore := newTestDAGRunLeaseStore(baseDir)
		activeStore := newTestActiveDistributedRunStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunRepository:          store.repository,
			DAGRunLeaseStore:          leaseStore,
			ActiveDistributedRunStore: activeStore,
			Owner:                     dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		})
		ctx := context.Background()

		ref := ir.DAGRunRef{Name: "test-dag", ID: "run-123"}
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			WorkerID:   "worker-1",
			Status:     ir.Failed,
		})
		require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
			AttemptKey:      "attempt-key-1",
			DAGRun:          ref,
			Root:            ref,
			AttemptID:       "attempt-1",
			QueueName:       "queue-a",
			WorkerID:        "worker-1",
			ClaimedAt:       time.Now().UTC().UnixMilli(),
			LastHeartbeatAt: time.Now().UTC().UnixMilli(),
		}))
		require.NoError(t, activeStore.Upsert(ctx, dispatch.ActiveDistributedRun{
			AttemptKey: "attempt-key-1",
			DAGRun:     ref,
			Root:       ref,
			AttemptID:  "attempt-1",
			WorkerID:   "worker-1",
			Status:     ir.Running,
		}))

		protoStatus, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			WorkerID:   "worker-1",
			Status:     ir.Failed,
			Error:      "duplicate terminal payload",
			Nodes: []*ir.Node{
				{
					Step:   ir.Step{Name: "chat-step"},
					Status: ir.NodeFailed,
					ChatMessages: []ir.LLMMessage{
						{Role: ir.LLMRoleAssistant, Content: "final summary"},
					},
				},
			},
		})
		require.NoError(t, convErr)

		resp, err := h.ReportStatus(ctx, &coordinatorv1.ReportStatusRequest{
			Status:             protoStatus,
			OwnerCoordinatorId: "coord-a",
			WorkerId:           "worker-1",
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.True(t, resp.Accepted)
		assert.True(t, attempt.WasWritten())

		current, readErr := attempt.ReadStatus(ctx)
		require.NoError(t, readErr)
		assert.Equal(t, "duplicate terminal payload", current.Error)
		assert.True(t, attempt.WasClosed())

		h.attemptsMu.RLock()
		_, cached := h.openAttempts[ref.ID]
		h.attemptsMu.RUnlock()
		assert.False(t, cached)

		messages := attempt.GetStepMessages("chat-step")
		require.Len(t, messages, 1)
		assert.Equal(t, "final summary", messages[0].Content)

		_, err = leaseStore.Get(ctx, "attempt-key-1")
		assert.ErrorIs(t, err, dispatch.ErrDAGRunLeaseNotFound)

		_, err = activeStore.Get(ctx, "attempt-key-1")
		assert.ErrorIs(t, err, dispatch.ErrActiveRunNotFound)
	})

	t.Run("ClosesAttemptAfterStatusWriteError", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		ref := ir.NewDAGRunRef("test-dag", "run-123")
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       ref.Name,
			DAGRunID:   ref.ID,
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.Running,
		})
		attempt.writeError = errors.New("status write failed")
		incoming, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:       ref.Name,
			DAGRunID:   ref.ID,
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.Failed,
		})
		require.NoError(t, convErr)

		_, err := h.ReportStatus(ctx, &coordinatorv1.ReportStatusRequest{Status: incoming})
		require.Error(t, err)
		assert.Equal(t, codes.Internal, status.Code(err))
		assert.True(t, attempt.WasClosed())

		h.attemptsMu.RLock()
		_, cached := h.openAttempts[ref.ID]
		h.attemptsMu.RUnlock()
		assert.False(t, cached)
	})

	t.Run("MissingStatusReturnsError", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		req := &coordinatorv1.ReportStatusRequest{
			Status: nil,
		}

		_, err := h.ReportStatus(ctx, req)
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("NilDAGRunRepositoryReturnsError", func(t *testing.T) {
		t.Parallel()

		h := NewHandler(HandlerConfig{}) // No dagRunRepository
		ctx := context.Background()

		protoStatus, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   ir.Running,
		})
		require.NoError(t, convErr)

		req := &coordinatorv1.ReportStatusRequest{
			Status: protoStatus,
		}

		_, err := h.ReportStatus(ctx, req)
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.FailedPrecondition, st.Code())
	})

	t.Run("ChatMessagesPersistence", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		// Create an attempt for the DAG run
		ref := ir.DAGRunRef{Name: "chat-dag", ID: "chat-run-123"}
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:     "chat-dag",
			DAGRunID: "chat-run-123",
			Status:   ir.Running,
		})

		// Create status with ChatMessages
		statusWithMessages := &ir.DAGRunStatus{
			Name:     "chat-dag",
			DAGRunID: "chat-run-123",
			Status:   ir.Running,
			Nodes: []*ir.Node{
				{
					Step:   ir.Step{Name: "chat-step"},
					Status: ir.NodeSucceeded,
					ChatMessages: []ir.LLMMessage{
						{Role: ir.LLMRoleUser, Content: "Hello!"},
						{Role: ir.LLMRoleAssistant, Content: "Hi there!", Metadata: &ir.LLMMessageMetadata{
							Provider:    "openai",
							Model:       "gpt-4",
							TotalTokens: 10,
						}},
					},
				},
				{
					Step:   ir.Step{Name: "no-messages-step"},
					Status: ir.NodeSucceeded,
					// No ChatMessages
				},
			},
		}

		protoStatus, convErr := convert.DAGRunStatusToProto(statusWithMessages)
		require.NoError(t, convErr)

		req := &coordinatorv1.ReportStatusRequest{
			Status: protoStatus,
		}

		resp, err := h.ReportStatus(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.True(t, resp.Accepted)

		// Verify ChatMessages were persisted via WriteStepMessages
		chatStepMessages := attempt.GetStepMessages("chat-step")
		require.Len(t, chatStepMessages, 2)
		assert.Equal(t, ir.LLMRoleUser, chatStepMessages[0].Role)
		assert.Equal(t, "Hello!", chatStepMessages[0].Content)
		assert.Equal(t, ir.LLMRoleAssistant, chatStepMessages[1].Role)
		assert.Equal(t, "Hi there!", chatStepMessages[1].Content)
		require.NotNil(t, chatStepMessages[1].Metadata)
		assert.Equal(t, "openai", chatStepMessages[1].Metadata.Provider)
		assert.Equal(t, "gpt-4", chatStepMessages[1].Metadata.Model)
		assert.Equal(t, 10, chatStepMessages[1].Metadata.TotalTokens)

		// Verify no messages were written for step without ChatMessages
		noMsgStepMessages := attempt.GetStepMessages("no-messages-step")
		assert.Nil(t, noMsgStepMessages)
	})

	t.Run("ChatMessagesPersistence_HandlerNodesFallbackNames", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		// Create an existing attempt
		ref := ir.DAGRunRef{Name: "handler-dag", ID: "handler-run-123"}
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:     "handler-dag",
			DAGRunID: "handler-run-123",
			Status:   ir.Running,
		})

		// Create status with handler nodes that have empty step names
		statusWithHandlers := &ir.DAGRunStatus{
			Name:     "handler-dag",
			DAGRunID: "handler-run-123",
			Status:   ir.Succeeded,
			// OnInit handler with empty step name - should use "on_init" fallback
			OnInit: &ir.Node{
				Step:   ir.Step{}, // Empty name
				Status: ir.NodeSucceeded,
				ChatMessages: []ir.LLMMessage{
					{Role: ir.LLMRoleAssistant, Content: "Init completed"},
				},
			},
			// OnSuccess handler with explicit name - should use explicit name
			OnSuccess: &ir.Node{
				Step:   ir.Step{Name: "my-success-handler"},
				Status: ir.NodeSucceeded,
				ChatMessages: []ir.LLMMessage{
					{Role: ir.LLMRoleAssistant, Content: "Success!"},
				},
			},
		}

		protoStatus, convErr := convert.DAGRunStatusToProto(statusWithHandlers)
		require.NoError(t, convErr)

		req := &coordinatorv1.ReportStatusRequest{
			Status: protoStatus,
		}

		resp, err := h.ReportStatus(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.True(t, resp.Accepted)

		// Verify OnInit messages were persisted with fallback name "on_init"
		onInitMessages := attempt.GetStepMessages("on_init")
		require.Len(t, onInitMessages, 1)
		assert.Equal(t, "Init completed", onInitMessages[0].Content)

		// Verify OnSuccess messages were persisted with explicit name
		onSuccessMessages := attempt.GetStepMessages("my-success-handler")
		require.Len(t, onSuccessMessages, 1)
		assert.Equal(t, "Success!", onSuccessMessages[0].Content)
	})

	t.Run("ChatMessagesPersistence_WriteErrorDoesNotFailStatus", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		// Create an existing attempt with error injection
		ref := ir.DAGRunRef{Name: "error-dag", ID: "error-run-123"}
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:     "error-dag",
			DAGRunID: "error-run-123",
			Status:   ir.Running,
		})

		// Inject error for WriteStepMessages
		attempt.writeStepMessagesError = errors.New("simulated write failure")

		// Create status with ChatMessages
		statusWithMessages := &ir.DAGRunStatus{
			Name:     "error-dag",
			DAGRunID: "error-run-123",
			Status:   ir.Succeeded,
			Nodes: []*ir.Node{
				{
					Step:   ir.Step{Name: "chat-step"},
					Status: ir.NodeSucceeded,
					ChatMessages: []ir.LLMMessage{
						{Role: ir.LLMRoleUser, Content: "Hello!"},
					},
				},
			},
		}

		protoStatus, convErr := convert.DAGRunStatusToProto(statusWithMessages)
		require.NoError(t, convErr)

		req := &coordinatorv1.ReportStatusRequest{
			Status: protoStatus,
		}

		// ReportStatus should succeed even when WriteStepMessages fails
		resp, err := h.ReportStatus(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.True(t, resp.Accepted)

		// Verify the main status was still written
		require.True(t, attempt.WasWritten())
	})

	t.Run("ReportStatusSyncsSharedLeaseWithoutStampingLeaseAt", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		leaseStore := newTestDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
		h := NewHandler(HandlerConfig{
			DAGRunRepository: store.repository,
			DAGRunLeaseStore: leaseStore,
			Owner:            dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		})
		ctx := context.Background()

		ref := ir.DAGRunRef{Name: "test-dag", ID: "run-123"}
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   ir.NotStarted,
		})

		protoStatus, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			ProcGroup:  "test-queue",
			Status:     ir.Running,
			WorkerID:   "worker-1",
			LeaseAt:    1,
		})
		require.NoError(t, convErr)

		_, err := h.ReportStatus(ctx, &coordinatorv1.ReportStatusRequest{
			Status:             protoStatus,
			OwnerCoordinatorId: "coord-a",
			WorkerId:           "worker-1",
		})
		require.NoError(t, err)

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, int64(1), status.LeaseAt)

		lease, err := leaseStore.Get(ctx, "attempt-key-1")
		require.NoError(t, err)
		assert.Equal(t, "attempt-1", lease.AttemptID)
		assert.Equal(t, "test-queue", lease.QueueName)
		assert.Equal(t, "worker-1", lease.WorkerID)
		assert.Equal(t, "coord-a", lease.Owner.ID)
	})

	t.Run("ReportStatusPreservesExistingLeaseQueueName", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		leaseStore := newTestDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
		h := NewHandler(HandlerConfig{
			DAGRunRepository: store.repository,
			DAGRunLeaseStore: leaseStore,
			Owner:            dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		})
		ctx := context.Background()

		ref := ir.DAGRunRef{Name: "test-dag", ID: "run-123"}
		store.addAttempt(ref, &ir.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   ir.NotStarted,
		})

		require.NoError(t, leaseStore.Upsert(ctx, dispatch.DAGRunLease{
			AttemptKey:      "attempt-key-1",
			DAGRun:          ref,
			Root:            ref,
			AttemptID:       "attempt-1",
			QueueName:       "queue-a",
			WorkerID:        "worker-1",
			ClaimedAt:       time.Now().Add(-time.Second).UTC().UnixMilli(),
			LastHeartbeatAt: time.Now().Add(-time.Second).UTC().UnixMilli(),
		}))

		protoStatus, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.Running,
			WorkerID:   "worker-1",
		})
		require.NoError(t, convErr)

		_, err := h.ReportStatus(ctx, &coordinatorv1.ReportStatusRequest{
			Status:             protoStatus,
			OwnerCoordinatorId: "coord-a",
			WorkerId:           "worker-1",
		})
		require.NoError(t, err)

		lease, err := leaseStore.Get(ctx, "attempt-key-1")
		require.NoError(t, err)
		assert.Equal(t, "queue-a", lease.QueueName)
	})

	t.Run("ReportStatusSyncsActiveDistributedRunIndex", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		baseDir := filepath.Join(t.TempDir(), "distributed")
		leaseStore := newTestDAGRunLeaseStore(baseDir)
		activeStore := newTestActiveDistributedRunStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunRepository:          store.repository,
			DAGRunLeaseStore:          leaseStore,
			ActiveDistributedRunStore: activeStore,
			Owner:                     dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		})
		ctx := context.Background()

		ref := ir.DAGRunRef{Name: "test-dag", ID: "run-123"}
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.NotStarted,
			WorkerID:   "worker-1",
		})

		runningProto, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.Running,
			WorkerID:   "worker-1",
		})
		require.NoError(t, convErr)

		resp, err := h.ReportStatus(ctx, &coordinatorv1.ReportStatusRequest{
			Status:             runningProto,
			OwnerCoordinatorId: "coord-a",
			WorkerId:           "worker-1",
		})
		require.NoError(t, err)
		require.True(t, resp.Accepted)

		record, err := activeStore.Get(ctx, "attempt-key-1")
		require.NoError(t, err)
		require.NotNil(t, record)
		assert.Equal(t, "attempt-1", record.AttemptID)
		assert.Equal(t, "worker-1", record.WorkerID)
		assert.Equal(t, ir.Running, record.Status)

		succeededProto, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.Succeeded,
			WorkerID:   "worker-1",
		})
		require.NoError(t, convErr)

		resp, err = h.ReportStatus(ctx, &coordinatorv1.ReportStatusRequest{
			Status:             succeededProto,
			OwnerCoordinatorId: "coord-a",
			WorkerId:           "worker-1",
		})
		require.NoError(t, err)
		require.True(t, resp.Accepted)

		_, err = activeStore.Get(ctx, "attempt-key-1")
		assert.ErrorIs(t, err, dispatch.ErrActiveRunNotFound)
		assert.True(t, attempt.WasClosed())

		h.attemptsMu.RLock()
		_, cached := h.openAttempts[ref.ID]
		h.attemptsMu.RUnlock()
		assert.False(t, cached)
	})

	t.Run("AckTaskClaimCreatesLeaseAndDeletesClaim", func(t *testing.T) {
		t.Parallel()

		baseDir := filepath.Join(t.TempDir(), "distributed")
		dispatchStore := newTestDispatchTaskStore(baseDir)
		leaseStore := newTestDAGRunLeaseStore(baseDir)
		activeStore := newTestActiveDistributedRunStore(baseDir)
		h := NewHandler(HandlerConfig{
			DispatchTaskStore:         dispatchStore,
			DAGRunLeaseStore:          leaseStore,
			ActiveDistributedRunStore: activeStore,
			Owner:                     dispatch.CoordinatorEndpoint{ID: "coord-b", Host: "coordinator-b", Port: 5678},
		})
		ctx := context.Background()

		task := &dispatch.DispatchTask{
			DAGRunID:       "run-123",
			Target:         "test-dag",
			AttemptID:      "attempt-1",
			AttemptKey:     "attempt-key-1",
			QueueName:      "queue-a",
			RootDAGRunName: "test-dag",
			RootDAGRunID:   "run-123",
		}
		require.NoError(t, dispatchStore.Enqueue(ctx, task))

		claimed, err := dispatchStore.ClaimNext(ctx, dispatch.DispatchTaskClaim{
			WorkerID: "worker-1",
			PollerID: "poller-1",
			Owner:    dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "coordinator-a", Port: 1234},
		})
		require.NoError(t, err)
		require.NotNil(t, claimed)

		ackRequest := &coordinatorv1.AckTaskClaimRequest{
			ClaimToken: claimed.ClaimToken,
			WorkerId:   "worker-1",
			AttemptKey: "attempt-key-1",
		}
		resp, err := h.AckTaskClaim(ctx, ackRequest)
		require.NoError(t, err)
		require.True(t, resp.Accepted)

		lease, err := leaseStore.Get(ctx, "attempt-key-1")
		require.NoError(t, err)
		assert.Equal(t, "queue-a", lease.QueueName)
		assert.Equal(t, "worker-1", lease.WorkerID)
		assert.Equal(t, dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "coordinator-a", Port: 1234}, lease.Owner)
		assert.Equal(t, claimed.ClaimToken, lease.ClaimToken)

		record, err := activeStore.Get(ctx, "attempt-key-1")
		require.NoError(t, err)
		assert.Equal(t, "test-dag", record.DAGRun.Name)
		assert.Equal(t, "run-123", record.DAGRun.ID)
		assert.Equal(t, "attempt-1", record.AttemptID)
		assert.Equal(t, "worker-1", record.WorkerID)
		assert.Equal(t, ir.Queued, record.Status)

		_, err = dispatchStore.GetClaim(ctx, claimed.ClaimToken)
		assert.ErrorIs(t, err, dispatch.ErrDispatchTaskNotFound)

		resp, err = h.AckTaskClaim(ctx, ackRequest)
		require.NoError(t, err)
		require.True(t, resp.Accepted)
	})

	t.Run("AckTaskClaimUsesClaimedWorkerWhenRequestWorkerMissing", func(t *testing.T) {
		t.Parallel()

		baseDir := filepath.Join(t.TempDir(), "distributed")
		dispatchStore := newTestDispatchTaskStore(baseDir)
		leaseStore := newTestDAGRunLeaseStore(baseDir)
		activeStore := newTestActiveDistributedRunStore(baseDir)
		h := NewHandler(HandlerConfig{
			DispatchTaskStore:         dispatchStore,
			DAGRunLeaseStore:          leaseStore,
			ActiveDistributedRunStore: activeStore,
			Owner:                     dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		})
		ctx := context.Background()

		task := &dispatch.DispatchTask{
			DAGRunID:   "run-123",
			Target:     "test-dag",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			QueueName:  "queue-a",
		}
		require.NoError(t, dispatchStore.Enqueue(ctx, task))

		claimed, err := dispatchStore.ClaimNext(ctx, dispatch.DispatchTaskClaim{
			WorkerID: "worker-1",
			PollerID: "poller-1",
			Owner:    dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		})
		require.NoError(t, err)
		require.NotNil(t, claimed)

		resp, err := h.AckTaskClaim(ctx, &coordinatorv1.AckTaskClaimRequest{
			ClaimToken: claimed.ClaimToken,
		})
		require.NoError(t, err)
		require.True(t, resp.Accepted)

		lease, err := leaseStore.Get(ctx, "attempt-key-1")
		require.NoError(t, err)
		assert.Equal(t, "worker-1", lease.WorkerID)

		record, err := activeStore.Get(ctx, "attempt-key-1")
		require.NoError(t, err)
		assert.Equal(t, "worker-1", record.WorkerID)
	})

	t.Run("AckTaskClaimRejectsConflictingLease", func(t *testing.T) {
		t.Parallel()

		baseDir := filepath.Join(t.TempDir(), "distributed")
		dispatchStore := newTestDispatchTaskStore(baseDir)
		leaseStore := newTestDAGRunLeaseStore(baseDir)
		activeStore := newTestActiveDistributedRunStore(baseDir)
		owner := dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "coordinator", Port: 1234}
		h := NewHandler(HandlerConfig{
			DispatchTaskStore:         dispatchStore,
			DAGRunLeaseStore:          leaseStore,
			ActiveDistributedRunStore: activeStore,
			Owner:                     owner,
		})
		ctx := context.Background()

		record := dispatch.DAGRunLease{
			AttemptKey: "attempt-key-1",
			DAGRun:     ir.NewDAGRunRef("test-dag", "run-123"),
			WorkerID:   "worker-1",
			Owner:      owner,
			ClaimToken: "claim-1",
		}
		require.NoError(t, leaseStore.Upsert(ctx, record))
		require.NoError(t, dispatchStore.Enqueue(ctx, &dispatch.DispatchTask{
			DAGRunID:   "run-123",
			Target:     "test-dag",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
		}))

		claimed, err := dispatchStore.ClaimNext(ctx, dispatch.DispatchTaskClaim{
			WorkerID: "worker-2",
			PollerID: "poller-2",
			Owner:    owner,
		})
		require.NoError(t, err)
		require.NotNil(t, claimed)

		resp, err := h.AckTaskClaim(ctx, &coordinatorv1.AckTaskClaimRequest{
			ClaimToken: claimed.ClaimToken,
			WorkerId:   "worker-2",
			AttemptKey: "attempt-key-1",
		})
		require.NoError(t, err)
		require.False(t, resp.Accepted)
		assert.Contains(t, resp.Error, "conflicts with the active lease")

		lease, err := leaseStore.Get(ctx, "attempt-key-1")
		require.NoError(t, err)
		assert.Equal(t, "worker-1", lease.WorkerID)
		assert.Equal(t, "claim-1", lease.ClaimToken)
	})
}

func TestHandler_GetDAGRunStatus(t *testing.T) {
	t.Parallel()

	t.Run("TopLevelDAGLookup", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		// Create an attempt with status
		ref := ir.DAGRunRef{Name: "test-dag", ID: "run-123"}
		store.addAttempt(ref, &ir.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   ir.Running,
		})

		req := &coordinatorv1.GetDAGRunStatusRequest{
			DagName:  "test-dag",
			DagRunId: "run-123",
		}

		resp, err := h.GetDAGRunStatus(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.True(t, resp.Found)
		require.NotNil(t, resp.Status)
	})

	t.Run("NotFoundReturnsFalse", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		req := &coordinatorv1.GetDAGRunStatusRequest{
			DagName:  "nonexistent-dag",
			DagRunId: "run-999",
		}

		resp, err := h.GetDAGRunStatus(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.False(t, resp.Found)
	})

	t.Run("LookupFailureReturnsInternalError", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		store.findAttemptErr = errors.New("storage unavailable")
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})

		_, err := h.GetDAGRunStatus(context.Background(), &coordinatorv1.GetDAGRunStatusRequest{
			DagName:  "test-dag",
			DagRunId: "run-123",
		})
		require.Error(t, err)
		assert.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("ReadFailureReturnsInternalError", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		attempt := store.addAttempt(ir.DAGRunRef{Name: "test-dag", ID: "run-123"}, &ir.DAGRunStatus{})
		attempt.readStatusError = errors.New("status read failed")
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})

		_, err := h.GetDAGRunStatus(context.Background(), &coordinatorv1.GetDAGRunStatusRequest{
			DagName:  "test-dag",
			DagRunId: "run-123",
		})
		require.Error(t, err)
		assert.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("NilDAGRunRepositoryReturnsError", func(t *testing.T) {
		t.Parallel()

		h := NewHandler(HandlerConfig{}) // No dagRunRepository
		ctx := context.Background()

		req := &coordinatorv1.GetDAGRunStatusRequest{
			DagName:  "test-dag",
			DagRunId: "run-123",
		}

		_, err := h.GetDAGRunStatus(ctx, req)
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.FailedPrecondition, st.Code())
	})

	t.Run("MissingRequiredFieldsReturnsError", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		// Missing DagName
		req := &coordinatorv1.GetDAGRunStatusRequest{
			DagRunId: "run-123",
		}

		_, err := h.GetDAGRunStatus(ctx, req)
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.InvalidArgument, st.Code())
	})
}

func TestHandler_StreamLogs(t *testing.T) {
	t.Parallel()

	t.Run("EmptyLogDirReturnsError", func(t *testing.T) {
		t.Parallel()

		h := NewHandler(HandlerConfig{}) // No logDir
		// StreamLogs requires a mock stream, but we can test the precondition check
		// by checking that logDir is empty
		require.Empty(t, h.logDir)
	})

	t.Run("WithLogDirConfigured", func(t *testing.T) {
		t.Parallel()

		logDir := t.TempDir()
		h := NewHandler(HandlerConfig{LogDir: logDir})
		require.Equal(t, logDir, h.logDir)
	})
}

func TestMatchesSelector(t *testing.T) {
	t.Parallel()

	t.Run("EmptySelectorMatchesAll", func(t *testing.T) {
		t.Parallel()

		workerLabels := map[string]string{"type": "compute", "region": "us-east"}
		selector := map[string]string{}

		require.True(t, matchesSelector(workerLabels, selector))
	})

	t.Run("NilSelectorMatchesAll", func(t *testing.T) {
		t.Parallel()

		workerLabels := map[string]string{"type": "compute"}

		require.True(t, matchesSelector(workerLabels, nil))
	})

	t.Run("ExactMatch", func(t *testing.T) {
		t.Parallel()

		workerLabels := map[string]string{"type": "compute", "region": "us-east"}
		selector := map[string]string{"type": "compute", "region": "us-east"}

		require.True(t, matchesSelector(workerLabels, selector))
	})

	t.Run("PartialSelectorMatch", func(t *testing.T) {
		t.Parallel()

		workerLabels := map[string]string{"type": "compute", "region": "us-east", "tier": "high"}
		selector := map[string]string{"type": "compute"}

		require.True(t, matchesSelector(workerLabels, selector))
	})

	t.Run("PartialSelectorNoMatch", func(t *testing.T) {
		t.Parallel()

		workerLabels := map[string]string{"type": "compute"}
		selector := map[string]string{"type": "storage"}

		require.False(t, matchesSelector(workerLabels, selector))
	})

	t.Run("MissingLabelNoMatch", func(t *testing.T) {
		t.Parallel()

		workerLabels := map[string]string{"type": "compute"}
		selector := map[string]string{"type": "compute", "region": "us-east"}

		require.False(t, matchesSelector(workerLabels, selector))
	})

	t.Run("EmptyWorkerLabelsWithSelectorNoMatch", func(t *testing.T) {
		t.Parallel()

		workerLabels := map[string]string{}
		selector := map[string]string{"type": "compute"}

		require.False(t, matchesSelector(workerLabels, selector))
	})

	t.Run("NilWorkerLabelsWithSelectorNoMatch", func(t *testing.T) {
		t.Parallel()

		selector := map[string]string{"type": "compute"}

		require.False(t, matchesSelector(nil, selector))
	})
}

func TestCalculateHealthStatus(t *testing.T) {
	t.Parallel()

	t.Run("LessThan5SecondsIsHealthy", func(t *testing.T) {
		t.Parallel()

		status := calculateHealthStatus(0 * time.Second)
		require.Equal(t, coordinatorv1.WorkerHealthStatus_WORKER_HEALTH_STATUS_HEALTHY, status)

		status = calculateHealthStatus(4 * time.Second)
		require.Equal(t, coordinatorv1.WorkerHealthStatus_WORKER_HEALTH_STATUS_HEALTHY, status)
	})

	t.Run("Between5And15SecondsIsWarning", func(t *testing.T) {
		t.Parallel()

		status := calculateHealthStatus(5 * time.Second)
		require.Equal(t, coordinatorv1.WorkerHealthStatus_WORKER_HEALTH_STATUS_WARNING, status)

		status = calculateHealthStatus(10 * time.Second)
		require.Equal(t, coordinatorv1.WorkerHealthStatus_WORKER_HEALTH_STATUS_WARNING, status)

		status = calculateHealthStatus(14 * time.Second)
		require.Equal(t, coordinatorv1.WorkerHealthStatus_WORKER_HEALTH_STATUS_WARNING, status)
	})

	t.Run("GreaterThan15SecondsIsUnhealthy", func(t *testing.T) {
		t.Parallel()

		status := calculateHealthStatus(15 * time.Second)
		require.Equal(t, coordinatorv1.WorkerHealthStatus_WORKER_HEALTH_STATUS_UNHEALTHY, status)

		status = calculateHealthStatus(30 * time.Second)
		require.Equal(t, coordinatorv1.WorkerHealthStatus_WORKER_HEALTH_STATUS_UNHEALTHY, status)

		status = calculateHealthStatus(60 * time.Second)
		require.Equal(t, coordinatorv1.WorkerHealthStatus_WORKER_HEALTH_STATUS_UNHEALTHY, status)
	})
}

func TestHandler_Close(t *testing.T) {
	t.Parallel()

	t.Run("ClosesOpenAttempts", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		// Create and cache an attempt
		ref := ir.DAGRunRef{Name: "test-dag", ID: "run-123"}
		attempt := store.addAttempt(ref, &ir.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   ir.Running,
		})

		// Manually add to open attempts cache
		h.attemptsMu.Lock()
		h.openAttempts["run-123"] = attempt
		h.attemptsMu.Unlock()

		// Close handler
		h.Close(ctx)

		// Verify attempt was closed
		require.True(t, attempt.WasClosed())

		// Verify cache is cleared
		h.attemptsMu.RLock()
		require.Empty(t, h.openAttempts)
		h.attemptsMu.RUnlock()
	})
}

func TestHandler_GetCancelledRunsForWorker(t *testing.T) {
	t.Parallel()

	t.Run("ReturnsNilWithNilStore", func(t *testing.T) {
		t.Parallel()

		h := NewHandler(HandlerConfig{}) // No dagRunRepository
		ctx := context.Background()

		stats := &coordinatorv1.WorkerStats{
			RunningTasks: []*coordinatorv1.RunningTask{
				{DagRunId: "run-123", DagName: "test-dag"},
			},
		}

		result := h.getCancelledRunsForWorker(ctx, stats)
		require.Nil(t, result)
	})

	t.Run("ReturnsNilWithNilStats", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		result := h.getCancelledRunsForWorker(ctx, nil)
		require.Nil(t, result)
	})

	t.Run("ReturnsNilWithEmptyRunningTasks", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		stats := &coordinatorv1.WorkerStats{
			RunningTasks: []*coordinatorv1.RunningTask{},
		}

		result := h.getCancelledRunsForWorker(ctx, stats)
		require.Nil(t, result)
	})
}

func TestHandlerOptions(t *testing.T) {
	t.Parallel()

	t.Run("WithDAGRunRepository", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})

		require.Same(t, store.repository, h.dagRunRepository)
	})

	t.Run("WithLogDir", func(t *testing.T) {
		t.Parallel()

		logDir := "/var/log/test"
		h := NewHandler(HandlerConfig{LogDir: logDir})

		require.Equal(t, logDir, h.logDir)
	})

	t.Run("MultipleOptions", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		logDir := "/var/log/test"
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository, LogDir: logDir})

		require.Same(t, store.repository, h.dagRunRepository)
		require.Equal(t, logDir, h.logDir)
	})
}

// mockStreamLogsServer implements coordinatorv1.CoordinatorService_StreamLogsServer for testing
type mockStreamLogsServer struct {
	chunks   []*coordinatorv1.LogChunk
	idx      int
	response *coordinatorv1.StreamLogsResponse
	ctx      context.Context
}

func (m *mockStreamLogsServer) Recv() (*coordinatorv1.LogChunk, error) {
	if m.idx >= len(m.chunks) {
		return nil, io.EOF
	}
	chunk := m.chunks[m.idx]
	m.idx++
	return chunk, nil
}

func (m *mockStreamLogsServer) SendAndClose(resp *coordinatorv1.StreamLogsResponse) error {
	m.response = resp
	return nil
}

func (m *mockStreamLogsServer) SetHeader(_ metadata.MD) error  { return nil }
func (m *mockStreamLogsServer) SendHeader(_ metadata.MD) error { return nil }
func (m *mockStreamLogsServer) SetTrailer(_ metadata.MD)       {}
func (m *mockStreamLogsServer) Context() context.Context       { return m.ctx }
func (m *mockStreamLogsServer) SendMsg(_ any) error            { return nil }
func (m *mockStreamLogsServer) RecvMsg(_ any) error            { return nil }

func TestHandler_StreamLogs_Full(t *testing.T) {
	t.Parallel()

	t.Run("ReturnsErrorWhenLogDirEmpty", func(t *testing.T) {
		t.Parallel()

		h := NewHandler(HandlerConfig{}) // No logDir
		stream := &mockStreamLogsServer{
			chunks: []*coordinatorv1.LogChunk{},
			ctx:    context.Background(),
		}

		err := h.StreamLogs(stream)
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.FailedPrecondition, st.Code())
		assert.Contains(t, st.Message(), "logDir is empty")
	})

	t.Run("WritesLogsToFileSystem", func(t *testing.T) {
		t.Parallel()

		logDir := t.TempDir()
		h := NewHandler(HandlerConfig{LogDir: logDir})

		chunks := []*coordinatorv1.LogChunk{
			{
				DagName:    "test-dag",
				DagRunId:   "run-123",
				AttemptId:  "attempt-1",
				StepName:   "step1",
				StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
				Data:       []byte("test log data\n"),
			},
			{
				DagName:    "test-dag",
				DagRunId:   "run-123",
				AttemptId:  "attempt-1",
				StepName:   "step1",
				StreamType: coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
				IsFinal:    true,
			},
		}

		stream := &mockStreamLogsServer{
			chunks: chunks,
			ctx:    context.Background(),
		}

		err := h.StreamLogs(stream)
		require.NoError(t, err)
		require.NotNil(t, stream.response)
		assert.Equal(t, uint64(2), stream.response.ChunksReceived)
		assert.Equal(t, uint64(14), stream.response.BytesWritten)
	})

	t.Run("AcceptsPreviousOwnerAtDifferentCoordinatorEndpoint", func(t *testing.T) {
		t.Parallel()

		logDir := t.TempDir()
		leaseStore := newTestDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
		attemptKey := ir.GenerateAttemptKey("test-dag", "run-123", "test-dag", "run-123", "attempt-1")
		require.NoError(t, leaseStore.Upsert(t.Context(), dispatch.DAGRunLease{
			AttemptKey:      attemptKey,
			DAGRun:          ir.NewDAGRunRef("test-dag", "run-123"),
			Root:            ir.NewDAGRunRef("test-dag", "run-123"),
			AttemptID:       "attempt-1",
			WorkerID:        "worker-1",
			Owner:           dispatch.CoordinatorEndpoint{ID: "coord-a", Host: "coordinator", Port: 50055},
			LastHeartbeatAt: time.Now().UTC().UnixMilli(),
		}))
		h := NewHandler(HandlerConfig{
			LogDir:           logDir,
			DAGRunLeaseStore: leaseStore,
			Owner:            dispatch.CoordinatorEndpoint{ID: "coord-b", Host: "coordinator-b", Port: 50056},
		})
		stream := &mockStreamLogsServer{
			ctx: t.Context(),
			chunks: []*coordinatorv1.LogChunk{
				{
					WorkerId:           "worker-1",
					DagName:            "test-dag",
					DagRunId:           "run-123",
					AttemptId:          "attempt-1",
					StepName:           "step1",
					StreamType:         coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
					Data:               []byte("continued\n"),
					OwnerCoordinatorId: "coord-a",
				},
				{
					WorkerId:           "worker-1",
					DagName:            "test-dag",
					DagRunId:           "run-123",
					AttemptId:          "attempt-1",
					StepName:           "step1",
					StreamType:         coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
					IsFinal:            true,
					OwnerCoordinatorId: "coord-a",
				},
			},
		}

		require.NoError(t, h.StreamLogs(stream))
		content, err := os.ReadFile(filepath.Join(logDir, "test-dag", "run-123", "attempt-1", "step1.stdout.log"))
		require.NoError(t, err)
		assert.Equal(t, "continued\n", string(content))
	})

	t.Run("AcceptsChildLogsAuthorizedByRootClaim", func(t *testing.T) {
		t.Parallel()

		logDir := t.TempDir()
		leaseStore := newTestDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
		root := ir.NewDAGRunRef("root-dag", "root-run")
		claimKey := ir.GenerateAttemptKey(root.Name, root.ID, root.Name, root.ID, "root-attempt")
		require.NoError(t, leaseStore.Upsert(t.Context(), dispatch.DAGRunLease{
			AttemptKey:      claimKey,
			DAGRun:          root,
			Root:            root,
			AttemptID:       "root-attempt",
			WorkerID:        "worker-1",
			Owner:           dispatch.CoordinatorEndpoint{Host: "coordinator", Port: 50055},
			LastHeartbeatAt: time.Now().UTC().UnixMilli(),
		}))
		h := NewHandler(HandlerConfig{
			LogDir:           logDir,
			DAGRunLeaseStore: leaseStore,
			Owner:            dispatch.CoordinatorEndpoint{Host: "coordinator", Port: 50055},
		})
		stream := &mockStreamLogsServer{
			ctx: t.Context(),
			chunks: []*coordinatorv1.LogChunk{
				{
					WorkerId:       "worker-1",
					DagName:        "child-dag",
					DagRunId:       "child-run",
					AttemptId:      "child-attempt",
					RootDagRunName: root.Name,
					RootDagRunId:   root.ID,
					AttemptKey:     claimKey,
					StepName:       "step1",
					StreamType:     coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
					Data:           []byte("child output\n"),
				},
				{
					WorkerId:       "worker-1",
					DagName:        "child-dag",
					DagRunId:       "child-run",
					AttemptId:      "child-attempt",
					RootDagRunName: root.Name,
					RootDagRunId:   root.ID,
					AttemptKey:     claimKey,
					StepName:       "step1",
					StreamType:     coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT,
					IsFinal:        true,
				},
			},
		}

		require.NoError(t, h.StreamLogs(stream))
		content, err := os.ReadFile(filepath.Join(logDir, root.Name, root.ID, "child-attempt", "step1.stdout.log"))
		require.NoError(t, err)
		assert.Equal(t, "child output\n", string(content))
	})

}

func TestHandler_GetCancelledRunsForWorker_Full(t *testing.T) {
	t.Parallel()

	t.Run("ReturnsCancelledRuns", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		// Create an attempt that is aborting (cancelled)
		ref := ir.DAGRunRef{Name: "test-dag", ID: "run-123"}
		store.addAbortingAttempt(ref, &ir.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   ir.Running, // Status doesn't matter, IsAborting is what's checked
		})

		expectedAttemptKey := "test-attempt-key-123"
		stats := &coordinatorv1.WorkerStats{
			RunningTasks: []*coordinatorv1.RunningTask{
				{DagRunId: "run-123", DagName: "test-dag", AttemptKey: expectedAttemptKey},
			},
		}

		result := h.getCancelledRunsForWorker(ctx, stats)
		require.Len(t, result, 1)
		assert.Equal(t, expectedAttemptKey, result[0].AttemptKey)
	})

	t.Run("DoesNotReturnRunningTasks", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		// Create an attempt that is running (not cancelled)
		ref := ir.DAGRunRef{Name: "test-dag", ID: "run-456"}
		store.addAttempt(ref, &ir.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-456",
			Status:   ir.Running,
		})

		stats := &coordinatorv1.WorkerStats{
			RunningTasks: []*coordinatorv1.RunningTask{
				{DagRunId: "run-456", DagName: "test-dag"},
			},
		}

		result := h.getCancelledRunsForWorker(ctx, stats)
		assert.Empty(t, result)
	})

	t.Run("ReturnsCancelledRunsForSupersededAttempts", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		ref := ir.DAGRunRef{Name: "test-dag", ID: "run-789"}
		store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-789",
			AttemptID:  "attempt-2",
			AttemptKey: "attempt-key-2",
			Status:     ir.Running,
		})

		stats := &coordinatorv1.WorkerStats{
			RunningTasks: []*coordinatorv1.RunningTask{
				{DagRunId: "run-789", DagName: "test-dag", AttemptKey: "attempt-key-1"},
			},
		}

		result := h.getCancelledRunsForWorker(ctx, stats)
		require.Len(t, result, 1)
		assert.Equal(t, "attempt-key-1", result[0].AttemptKey)
	})

	t.Run("ReturnsCancelledRunsForTerminalAttempts", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		ref := ir.DAGRunRef{Name: "test-dag", ID: "run-999"}
		store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-999",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.Failed,
		})

		stats := &coordinatorv1.WorkerStats{
			RunningTasks: []*coordinatorv1.RunningTask{
				{DagRunId: "run-999", DagName: "test-dag", AttemptKey: "attempt-key-1"},
			},
		}

		result := h.getCancelledRunsForWorker(ctx, stats)
		require.Len(t, result, 1)
		assert.Equal(t, "attempt-key-1", result[0].AttemptKey)
	})

	t.Run("DoesNotReturnCancelledRunsForSuccessfulTerminalAttempts", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		ref := ir.DAGRunRef{Name: "test-dag", ID: "run-success"}
		store.addAttempt(ref, &ir.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-success",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     ir.Succeeded,
		})

		stats := &coordinatorv1.WorkerStats{
			RunningTasks: []*coordinatorv1.RunningTask{
				{DagRunId: "run-success", DagName: "test-dag", AttemptKey: "attempt-key-1"},
			},
		}

		result := h.getCancelledRunsForWorker(ctx, stats)
		assert.Empty(t, result)
	})
}

func TestHandler_RequestCancel(t *testing.T) {
	t.Parallel()

	t.Run("FinalizesNotStartedSubAttempt", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		rootRef := ir.DAGRunRef{Name: "parent-dag", ID: "root-run"}
		attempt := store.addSubAttempt(rootRef, "child-run", &ir.DAGRunStatus{
			Name:      "child-dag",
			DAGRunID:  "child-run",
			AttemptID: "attempt-1",
			Status:    ir.NotStarted,
		})

		resp, err := h.RequestCancel(ctx, &coordinatorv1.RequestCancelRequest{
			DagName:        "child-dag",
			DagRunId:       "child-run",
			RootDagRunName: rootRef.Name,
			RootDagRunId:   rootRef.ID,
		})
		require.NoError(t, err)
		require.True(t, resp.Accepted)

		attempt.mu.Lock()
		defer attempt.mu.Unlock()
		require.True(t, attempt.aborting)
		require.True(t, attempt.opened)
		require.True(t, attempt.closed)
		require.True(t, attempt.written)
		require.NotNil(t, attempt.status)
		require.Equal(t, ir.Aborted, attempt.status.Status)
		require.NotEmpty(t, attempt.status.FinishedAt)
		require.Equal(t, context.Canceled.Error(), attempt.status.Error)
	})

	t.Run("LeavesActiveSubAttemptForWorkerShutdown", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		rootRef := ir.DAGRunRef{Name: "parent-dag", ID: "root-run"}
		attempt := store.addSubAttempt(rootRef, "child-run", &ir.DAGRunStatus{
			Name:      "child-dag",
			DAGRunID:  "child-run",
			AttemptID: "attempt-1",
			Status:    ir.Running,
		})

		resp, err := h.RequestCancel(ctx, &coordinatorv1.RequestCancelRequest{
			DagName:        "child-dag",
			DagRunId:       "child-run",
			RootDagRunName: rootRef.Name,
			RootDagRunId:   rootRef.ID,
		})
		require.NoError(t, err)
		require.True(t, resp.Accepted)

		attempt.mu.Lock()
		defer attempt.mu.Unlock()
		require.True(t, attempt.aborting)
		require.False(t, attempt.opened)
		require.False(t, attempt.closed)
		require.False(t, attempt.written)
		require.NotNil(t, attempt.status)
		require.Equal(t, ir.Running, attempt.status.Status)
	})
}

func TestHandler_GetOrOpenSubAttempt(t *testing.T) {
	t.Parallel()

	t.Run("OpensSubAttemptOnFirstAccess", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		// Add a sub-attempt
		rootRef := ir.DAGRunRef{Name: "parent-dag", ID: "root-123"}
		subDAGRunID := "sub-456"
		storedAttempt := store.addSubAttempt(rootRef, subDAGRunID, &ir.DAGRunStatus{
			Name:     "child-dag",
			DAGRunID: subDAGRunID,
			Status:   ir.Running,
		})

		// Get the sub-attempt
		attempt, err := h.getOrOpenSubAttempt(ctx, rootRef, subDAGRunID)
		require.NoError(t, err)
		require.NotNil(t, attempt)

		// Verify it was opened
		assert.True(t, storedAttempt.WasOpened())
	})

	t.Run("ReturnsCachedAttemptOnSecondAccess", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		// Add a sub-attempt
		rootRef := ir.DAGRunRef{Name: "parent-dag", ID: "root-789"}
		subDAGRunID := "sub-101"
		store.addSubAttempt(rootRef, subDAGRunID, &ir.DAGRunStatus{
			Name:     "child-dag",
			DAGRunID: subDAGRunID,
			Status:   ir.Running,
		})

		// Get the sub-attempt twice
		attempt1, err := h.getOrOpenSubAttempt(ctx, rootRef, subDAGRunID)
		require.NoError(t, err)

		attempt2, err := h.getOrOpenSubAttempt(ctx, rootRef, subDAGRunID)
		require.NoError(t, err)

		// Both should be the same instance
		assert.Same(t, attempt1, attempt2)
	})

	t.Run("ReturnsErrorWhenSubAttemptNotFound", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository})
		ctx := context.Background()

		rootRef := ir.DAGRunRef{Name: "parent-dag", ID: "root-999"}

		// Try to get a non-existent sub-attempt
		_, err := h.getOrOpenSubAttempt(ctx, rootRef, "non-existent")
		assert.Error(t, err)
	})
}
