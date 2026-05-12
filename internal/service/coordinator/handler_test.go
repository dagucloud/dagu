// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/filedagrun"
	"github.com/dagucloud/dagu/internal/persis/filedistributed"
	"github.com/dagucloud/dagu/internal/proto/convert"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// mockDAGRunStore is a test implementation of execution.DAGRunStore
type mockDAGRunStore struct {
	attempts            map[string]*mockDAGRunAttempt
	subAttempts         map[string]*mockDAGRunAttempt // key: rootID:subID
	createAttemptErr    error
	createSubAttemptErr error
	listStatusesCalls   int
	mu                  sync.Mutex
}

func newMockDAGRunStore() *mockDAGRunStore {
	return &mockDAGRunStore{
		attempts:    make(map[string]*mockDAGRunAttempt),
		subAttempts: make(map[string]*mockDAGRunAttempt),
	}
}

func (m *mockDAGRunStore) addSubAttempt(rootRef exec.DAGRunRef, subDAGRunID string, status *exec.DAGRunStatus) *mockDAGRunAttempt {
	m.mu.Lock()
	defer m.mu.Unlock()
	attempt := &mockDAGRunAttempt{
		status: status,
	}
	key := rootRef.ID + ":" + subDAGRunID
	m.subAttempts[key] = attempt
	return attempt
}

func (m *mockDAGRunStore) addAttempt(ref exec.DAGRunRef, status *exec.DAGRunStatus) *mockDAGRunAttempt {
	m.mu.Lock()
	defer m.mu.Unlock()
	attempt := &mockDAGRunAttempt{
		status: status,
	}
	m.attempts[ref.ID] = attempt
	return attempt
}

func (m *mockDAGRunStore) addAbortingAttempt(ref exec.DAGRunRef, status *exec.DAGRunStatus) *mockDAGRunAttempt {
	m.mu.Lock()
	defer m.mu.Unlock()
	attempt := &mockDAGRunAttempt{
		status:   status,
		aborting: true,
	}
	m.attempts[ref.ID] = attempt
	return attempt
}

func (m *mockDAGRunStore) FindAttempt(_ context.Context, dagRun exec.DAGRunRef) (exec.DAGRunAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if attempt, ok := m.attempts[dagRun.ID]; ok {
		return attempt, nil
	}
	return nil, exec.ErrDAGRunIDNotFound
}

// Implement other required interface methods (unused in tests)
// These methods return sentinel errors or panic to make test failures obvious if accidentally called.
func (m *mockDAGRunStore) CreateAttempt(_ context.Context, dag *core.DAG, _ time.Time, dagRunID string, _ exec.NewDAGRunAttemptOptions) (exec.DAGRunAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createAttemptErr != nil {
		return nil, m.createAttemptErr
	}
	attempt := &mockDAGRunAttempt{
		status: &exec.DAGRunStatus{Name: dag.Name, DAGRunID: dagRunID},
	}
	m.attempts[dagRunID] = attempt
	return attempt, nil
}
func (m *mockDAGRunStore) RecentAttempts(_ context.Context, _ string, _ int) []exec.DAGRunAttempt {
	return nil // Empty slice is valid
}
func (m *mockDAGRunStore) LatestAttempt(_ context.Context, _ string) (exec.DAGRunAttempt, error) {
	return nil, exec.ErrDAGRunIDNotFound
}
func (m *mockDAGRunStore) ListStatuses(_ context.Context, opts ...exec.ListDAGRunStatusesOption) ([]*exec.DAGRunStatus, error) {
	var options exec.ListDAGRunStatusesOptions
	for _, opt := range opts {
		opt(&options)
	}

	statusFilter := make(map[core.Status]struct{}, len(options.Statuses))
	for _, st := range options.Statuses {
		statusFilter[st] = struct{}{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.listStatusesCalls++

	var statuses []*exec.DAGRunStatus
	appendStatus := func(status *exec.DAGRunStatus) {
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

	return statuses, nil
}

func (m *mockDAGRunStore) ListStatusesCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listStatusesCalls
}

func (m *mockDAGRunStore) ListStatusesPage(ctx context.Context, opts ...exec.ListDAGRunStatusesOption) (exec.DAGRunStatusPage, error) {
	items, err := m.ListStatuses(ctx, opts...)
	if err != nil {
		return exec.DAGRunStatusPage{}, err
	}
	return exec.DAGRunStatusPage{Items: items}, nil
}
func (m *mockDAGRunStore) CompareAndSwapLatestAttemptStatus(
	ctx context.Context,
	dagRun exec.DAGRunRef,
	expectedAttemptID string,
	expectedStatus core.Status,
	mutate func(*exec.DAGRunStatus) error,
) (*exec.DAGRunStatus, bool, error) {
	return m.CompareAndSwapAttemptStatus(ctx, exec.DAGRunAttemptRef{
		DAGRun:    dagRun,
		AttemptID: expectedAttemptID,
	}, expectedStatus, mutate)
}

func (m *mockDAGRunStore) CompareAndSwapAttemptStatus(
	_ context.Context,
	ref exec.DAGRunAttemptRef,
	expectedStatus core.Status,
	mutate func(*exec.DAGRunStatus) error,
) (*exec.DAGRunStatus, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var (
		attempt *mockDAGRunAttempt
		ok      bool
	)
	if ref.IsSubDAG() {
		key := ref.Root.ID + ":" + ref.DAGRun.ID
		attempt, ok = m.subAttempts[key]
	} else {
		attempt, ok = m.attempts[ref.DAGRun.ID]
	}
	if !ok || attempt.status == nil {
		return nil, false, nil
	}

	current := *attempt.status
	if ref.AttemptID != "" && current.AttemptID != ref.AttemptID {
		return &current, false, nil
	}
	if ref.AttemptKey != "" && current.AttemptKey != "" && current.AttemptKey != ref.AttemptKey {
		return &current, false, nil
	}
	if current.Status != expectedStatus {
		return &current, false, nil
	}
	if err := mutate(&current); err != nil {
		return nil, false, err
	}
	attempt.status = &current
	attempt.written = true
	return &current, true, nil
}
func (m *mockDAGRunStore) FindSubAttempt(_ context.Context, rootRef exec.DAGRunRef, subDAGRunID string) (exec.DAGRunAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := rootRef.ID + ":" + subDAGRunID
	if attempt, ok := m.subAttempts[key]; ok {
		return attempt, nil
	}
	return nil, exec.ErrDAGRunIDNotFound
}
func (m *mockDAGRunStore) CreateSubAttempt(_ context.Context, rootRef exec.DAGRunRef, subDAGRunID string) (exec.DAGRunAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createSubAttemptErr != nil {
		return nil, m.createSubAttemptErr
	}
	key := rootRef.ID + ":" + subDAGRunID
	attempt := &mockDAGRunAttempt{
		status: &exec.DAGRunStatus{},
	}
	m.subAttempts[key] = attempt
	return attempt, nil
}
func (m *mockDAGRunStore) RemoveOldDAGRuns(_ context.Context, _ string, _ int, _ ...exec.RemoveOldDAGRunsOption) ([]string, error) {
	return nil, nil
}
func (m *mockDAGRunStore) RenameDAGRuns(_ context.Context, _, _ string) error { return nil }
func (m *mockDAGRunStore) RemoveDAGRun(_ context.Context, _ exec.DAGRunRef, _ ...exec.RemoveDAGRunOption) error {
	return nil
}

// mockDAGRunAttempt is a test implementation of execution.DAGRunAttempt
type mockDAGRunAttempt struct {
	dag                    *core.DAG
	status                 *exec.DAGRunStatus
	opened                 bool
	closed                 bool
	written                bool
	aborting               bool
	openError              error
	readStatusError        error
	writeError             error
	stepMessages           map[string][]exec.LLMMessage // stepName -> messages
	writeStepMessagesError error                        // injected error for WriteStepMessages
	mu                     sync.Mutex
}

func (m *mockDAGRunAttempt) ID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status != nil && m.status.AttemptID != "" {
		return m.status.AttemptID
	}
	return "test-attempt"
}
func (m *mockDAGRunAttempt) Open(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.openError != nil {
		return m.openError
	}
	m.opened = true
	return nil
}
func (m *mockDAGRunAttempt) Write(_ context.Context, s exec.DAGRunStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.writeError != nil {
		return m.writeError
	}
	m.status = &s
	m.written = true
	return nil
}
func (m *mockDAGRunAttempt) Close(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}
func (m *mockDAGRunAttempt) ReadStatus(_ context.Context) (*exec.DAGRunStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.readStatusError != nil {
		return nil, m.readStatusError
	}
	if m.status == nil {
		return nil, exec.ErrNoStatusData
	}
	cloned := *m.status
	return &cloned, nil
}
func (m *mockDAGRunAttempt) ReadDAG(_ context.Context) (*core.DAG, error) { return m.dag, nil }
func (m *mockDAGRunAttempt) SetDAG(_ *core.DAG)                           {}
func (m *mockDAGRunAttempt) Abort(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.aborting = true
	return nil
}
func (m *mockDAGRunAttempt) IsAborting(_ context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.aborting, nil
}
func (m *mockDAGRunAttempt) Hide(_ context.Context) error { return nil }
func (m *mockDAGRunAttempt) Hidden() bool                 { return false }
func (m *mockDAGRunAttempt) WriteOutputs(_ context.Context, _ *exec.DAGRunOutputs) error {
	return nil
}
func (m *mockDAGRunAttempt) ReadOutputs(_ context.Context) (*exec.DAGRunOutputs, error) {
	return nil, nil
}
func (m *mockDAGRunAttempt) WriteStepMessages(_ context.Context, stepName string, messages []exec.LLMMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.writeStepMessagesError != nil {
		return m.writeStepMessagesError
	}
	if m.stepMessages == nil {
		m.stepMessages = make(map[string][]exec.LLMMessage)
	}
	m.stepMessages[stepName] = messages
	return nil
}
func (m *mockDAGRunAttempt) ReadStepMessages(_ context.Context, stepName string) ([]exec.LLMMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stepMessages == nil {
		return nil, nil
	}
	return m.stepMessages[stepName], nil
}

func (m *mockDAGRunAttempt) WorkDir() string { return "" }

// GetStepMessages returns the messages written for a step (for test assertions)
func (m *mockDAGRunAttempt) GetStepMessages(stepName string) []exec.LLMMessage {
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
	attempt := &mockDAGRunAttempt{
		dag: &core.DAG{
			Name: "test-dag",
			Artifacts: &core.ArtifactsConfig{
				Enabled: true,
			},
		},
	}
	incoming := &exec.DAGRunStatus{
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
	attempt := &mockDAGRunAttempt{
		dag: &core.DAG{
			Name: "../weird/..-dag--name",
			Artifacts: &core.ArtifactsConfig{
				Enabled: true,
			},
		},
	}
	incoming := &exec.DAGRunStatus{
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
	incoming := &exec.DAGRunStatus{DAGRunID: "run-123"}
	latestStatus := &exec.DAGRunStatus{ArchiveDir: existingArchiveDir}

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
	attempt := &mockDAGRunAttempt{
		dag: &core.DAG{
			Name: "test-dag",
			Artifacts: &core.ArtifactsConfig{
				Enabled: true,
				Dir:     "${EMPTY_ARTIFACT_DIR}",
			},
		},
	}
	incoming := &exec.DAGRunStatus{
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
	attempt := &mockDAGRunAttempt{
		dag: &core.DAG{
			Name: "test-dag",
			Artifacts: &core.ArtifactsConfig{
				Enabled: true,
				Dir:     baseDir,
			},
		},
	}
	incoming := &exec.DAGRunStatus{
		DAGRunID:   "run-123",
		ArchiveDir: "/tmp/worker/dag-run_20260412_000000Z_run-123",
	}

	err := handler.transformArtifactPaths(context.Background(), attempt, nil, incoming)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(baseDir, "test-dag", "dag-run_20260412_000000Z_run-123"), incoming.ArchiveDir)
}

// Thread-safe getters for test assertions
func (m *mockDAGRunAttempt) WasOpened() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.opened
}

func (m *mockDAGRunAttempt) WasWritten() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.written
}

func (m *mockDAGRunAttempt) WasClosed() bool {
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
			Definition:       "name: test-dag\nsteps:\n  - name: step1\n    command: echo hello",
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
			Definition:     "name: test-dag\nsteps:\n  - name: step1\n    command: echo hello",
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
		attempt := &mockDAGRunAttempt{}

		err := h.writeInitialStatus(
			context.Background(),
			attempt,
			"test-dag",
			"run-123",
			"attempt-key",
			"2026-03-13T10:00:00Z",
			exec.DAGRunRef{},
			nil,
		)
		require.NoError(t, err)

		status, err := attempt.ReadStatus(context.Background())
		require.NoError(t, err)
		require.Equal(t, "2026-03-13T10:00:00Z", status.ScheduleTime)
	})

	t.Run("DispatchFailsWhenAttemptPreparationFails", func(t *testing.T) {
		t.Parallel()
		core.RegisterExecutorCapabilities("command", core.ExecutorCapabilities{Command: true})

		baseDir := filepath.Join(t.TempDir(), "distributed")
		dispatchStore := filedistributed.NewDispatchTaskStore(baseDir)
		heartbeatStore := filedistributed.NewWorkerHeartbeatStore(baseDir)
		require.NoError(t, heartbeatStore.Upsert(context.Background(), exec.WorkerHeartbeatRecord{
			WorkerID:        "worker-1",
			LastHeartbeatAt: time.Now().UTC().UnixMilli(),
		}))

		store := newMockDAGRunStore()
		store.createAttemptErr = errors.New("prepare failed")
		h := NewHandler(HandlerConfig{
			DAGRunStore:          store,
			DispatchTaskStore:    dispatchStore,
			WorkerHeartbeatStore: heartbeatStore,
		})

		_, err := h.Dispatch(context.Background(), &coordinatorv1.DispatchRequest{
			Task: &coordinatorv1.Task{
				DagRunId:   "run-123",
				Target:     "test-dag",
				Definition: "name: test-dag\nsteps:\n  - name: step1\n    type: command\n    command: echo hello",
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

	t.Run("DispatchMarksNewAttemptFailedWhenEnqueueFails", func(t *testing.T) {
		t.Parallel()
		core.RegisterExecutorCapabilities("command", core.ExecutorCapabilities{Command: true})

		heartbeatStore := filedistributed.NewWorkerHeartbeatStore(filepath.Join(t.TempDir(), "distributed"))
		require.NoError(t, heartbeatStore.Upsert(context.Background(), exec.WorkerHeartbeatRecord{
			WorkerID:        "worker-1",
			LastHeartbeatAt: time.Now().UTC().UnixMilli(),
		}))

		store := filedagrun.New(filepath.Join(t.TempDir(), "dag-runs"))
		h := NewHandler(HandlerConfig{
			DAGRunStore:          store,
			DispatchTaskStore:    &failingDispatchTaskStore{enqueueErr: errors.New("disk full")},
			WorkerHeartbeatStore: heartbeatStore,
		})

		_, err := h.Dispatch(context.Background(), &coordinatorv1.DispatchRequest{
			Task: &coordinatorv1.Task{
				DagRunId:   "run-123",
				Target:     "test-dag",
				Definition: "name: test-dag\nsteps:\n  - name: step1\n    type: command\n    command: echo hello",
				QueueName:  "test-queue",
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to enqueue task")

		attempt, findErr := store.FindAttempt(context.Background(), exec.DAGRunRef{Name: "test-dag", ID: "run-123"})
		require.NoError(t, findErr)
		runStatus, readErr := attempt.ReadStatus(context.Background())
		require.NoError(t, readErr)
		require.Equal(t, core.Failed, runStatus.Status)
		require.Contains(t, runStatus.Error, "failed to hand off distributed task")

		h.attemptsMu.RLock()
		require.Empty(t, h.openAttempts)
		h.attemptsMu.RUnlock()
	})

	t.Run("DispatchLeavesReusedQueuedAttemptQueuedWhenEnqueueFails", func(t *testing.T) {
		t.Parallel()
		core.RegisterExecutorCapabilities("command", core.ExecutorCapabilities{Command: true})

		heartbeatStore := filedistributed.NewWorkerHeartbeatStore(filepath.Join(t.TempDir(), "distributed"))
		require.NoError(t, heartbeatStore.Upsert(context.Background(), exec.WorkerHeartbeatRecord{
			WorkerID:        "worker-1",
			LastHeartbeatAt: time.Now().UTC().UnixMilli(),
		}))

		store := newMockDAGRunStore()
		ref := exec.DAGRunRef{Name: "test-dag", ID: "run-123"}
		attempt := store.addAttempt(ref, &exec.DAGRunStatus{
			Name:      "test-dag",
			DAGRunID:  "run-123",
			AttemptID: "attempt-existing",
			Status:    core.Queued,
		})

		h := NewHandler(HandlerConfig{
			DAGRunStore:          store,
			DispatchTaskStore:    &failingDispatchTaskStore{enqueueErr: errors.New("disk full")},
			WorkerHeartbeatStore: heartbeatStore,
		})

		_, err := h.Dispatch(context.Background(), &coordinatorv1.DispatchRequest{
			Task: &coordinatorv1.Task{
				DagRunId:   "run-123",
				Target:     "test-dag",
				Definition: "name: test-dag\nsteps:\n  - name: step1\n    type: command\n    command: echo hello",
				QueueName:  "test-queue",
			},
		})
		require.Error(t, err)

		runStatus, readErr := attempt.ReadStatus(context.Background())
		require.NoError(t, readErr)
		require.Equal(t, core.Queued, runStatus.Status)
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

	core.RegisterExecutorCapabilities("command", core.ExecutorCapabilities{Command: true})

	baseDir := filepath.Join(t.TempDir(), "distributed")
	dispatchStore := filedistributed.NewDispatchTaskStore(baseDir)
	heartbeatStore := filedistributed.NewWorkerHeartbeatStore(baseDir)
	require.NoError(t, heartbeatStore.Upsert(context.Background(), exec.WorkerHeartbeatRecord{
		WorkerID:        "worker-1",
		LastHeartbeatAt: time.Now().UTC().UnixMilli(),
	}))

	store := newMockDAGRunStore()
	ref := exec.DAGRunRef{Name: "test-dag", ID: "run-123"}
	store.addAttempt(ref, &exec.DAGRunStatus{
		Name:      "test-dag",
		DAGRunID:  "run-123",
		AttemptID: "attempt-current",
		Status:    core.Aborted,
	})

	previousStatus, err := convert.DAGRunStatusToProto(&exec.DAGRunStatus{
		Name:      "test-dag",
		DAGRunID:  "run-123",
		AttemptID: "attempt-queued",
		Status:    core.Queued,
	})
	require.NoError(t, err)

	h := NewHandler(HandlerConfig{
		DAGRunStore:          store,
		DispatchTaskStore:    dispatchStore,
		WorkerHeartbeatStore: heartbeatStore,
	})

	_, err = h.Dispatch(context.Background(), &coordinatorv1.DispatchRequest{
		Task: &coordinatorv1.Task{
			Operation:      coordinatorv1.Operation_OPERATION_RETRY,
			DagRunId:       "run-123",
			Target:         "test-dag",
			Definition:     "name: test-dag\nsteps:\n  - name: step1\n    type: command\n    command: echo hello",
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
	require.Equal(t, core.Aborted, runStatus.Status)
}

type failingDispatchTaskStore struct {
	enqueueErr error
}

func (s *failingDispatchTaskStore) Enqueue(context.Context, *coordinatorv1.Task) error {
	return s.enqueueErr
}

func (s *failingDispatchTaskStore) ClaimNext(context.Context, exec.DispatchTaskClaim) (*exec.ClaimedDispatchTask, error) {
	return nil, nil
}

func (s *failingDispatchTaskStore) GetClaim(context.Context, string) (*exec.ClaimedDispatchTask, error) {
	return nil, exec.ErrDispatchTaskNotFound
}

func (s *failingDispatchTaskStore) DeleteClaim(context.Context, string) error {
	return nil
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
		h := NewHandler(HandlerConfig{DAGRunStore: store, StaleLeaseThreshold: 10 * time.Second})
		ctx := context.Background()

		initialLease := time.Now().Add(-10 * time.Second).UnixMilli()
		ref := exec.DAGRunRef{Name: "test-dag", ID: "run-123"}
		attempt := store.addAttempt(ref, &exec.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			AttemptKey: "root-attempt-key",
			Status:     core.Running,
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

	t.Run("HeartbeatRefreshesLeaseForRunningSubDAG", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunStore: store, StaleLeaseThreshold: 10 * time.Second})
		ctx := context.Background()

		initialLease := time.Now().Add(-10 * time.Second).UnixMilli()
		rootRef := exec.DAGRunRef{Name: "root-dag", ID: "root-123"}
		attempt := store.addSubAttempt(rootRef, "sub-456", &exec.DAGRunStatus{
			Name:       "sub-dag",
			DAGRunID:   "sub-456",
			AttemptID:  "attempt-2",
			AttemptKey: "sub-attempt-key",
			Status:     core.Running,
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

		leaseStore := filedistributed.NewDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
		h := NewHandler(HandlerConfig{
			DAGRunLeaseStore: leaseStore,
			Owner:            exec.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		})
		ctx := context.Background()

		initial := time.Now().Add(-10 * time.Second).UTC()
		require.NoError(t, leaseStore.Upsert(ctx, exec.DAGRunLease{
			AttemptKey:      "attempt-key-1",
			DAGRun:          exec.NewDAGRunRef("test-dag", "run-123"),
			Root:            exec.NewDAGRunRef("test-dag", "run-123"),
			AttemptID:       "attempt-1",
			QueueName:       "test-dag",
			WorkerID:        "worker-1",
			Owner:           exec.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
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

	t.Run("RunHeartbeatRepairsStaleLeaseFailureForOwnedAttempt", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		baseDir := filepath.Join(t.TempDir(), "distributed")
		leaseStore := filedistributed.NewDAGRunLeaseStore(baseDir)
		activeStore := filedistributed.NewActiveDistributedRunStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunStore:               store,
			DAGRunLeaseStore:          leaseStore,
			ActiveDistributedRunStore: activeStore,
			Owner:                     exec.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		})
		ctx := context.Background()

		ref := exec.NewDAGRunRef("test-dag", "run-123")
		reason := staleDistributedLeaseReason("worker-1")
		attempt := store.addAttempt(ref, &exec.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			Root:       ref,
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     core.Failed,
			WorkerID:   "worker-1",
			FinishedAt: "2026-04-20T00:00:01Z",
			Error:      reason,
			Nodes: []*exec.Node{
				{
					Step:       core.Step{Name: "long-step"},
					StartedAt:  "2026-04-20T00:00:00Z",
					FinishedAt: "2026-04-20T00:00:01Z",
					Status:     core.NodeFailed,
					Error:      reason,
				},
				{
					Step:       core.Step{Name: "completed-step"},
					StartedAt:  "2026-04-20T00:00:00Z",
					FinishedAt: "2026-04-20T00:00:01Z",
					Status:     core.NodeSucceeded,
				},
				{
					Step:   core.Step{Name: "pending-step"},
					Status: core.NodeFailed,
					Error:  reason,
				},
			},
		})

		initial := time.Now().Add(-time.Second).UTC()
		require.NoError(t, leaseStore.Upsert(ctx, exec.DAGRunLease{
			AttemptKey:      "attempt-key-1",
			DAGRun:          ref,
			Root:            ref,
			AttemptID:       "attempt-1",
			QueueName:       "test-dag",
			WorkerID:        "worker-1",
			Owner:           exec.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
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
		assert.Equal(t, core.Running, status.Status)
		assert.Empty(t, status.Error)
		assert.Empty(t, status.FinishedAt)
		require.Len(t, status.Nodes, 3)
		assert.Equal(t, core.NodeRunning, status.Nodes[0].Status)
		assert.Equal(t, "2026-04-20T00:00:00Z", status.Nodes[0].StartedAt)
		assert.Empty(t, status.Nodes[0].FinishedAt)
		assert.Empty(t, status.Nodes[0].Error)
		assert.Equal(t, core.NodeSucceeded, status.Nodes[1].Status)
		assert.Equal(t, core.NodeNotStarted, status.Nodes[2].Status)
		assert.Equal(t, "-", status.Nodes[2].StartedAt)
		assert.Equal(t, "-", status.Nodes[2].FinishedAt)
		assert.Empty(t, status.Nodes[2].Error)

		record, err := activeStore.Get(ctx, "attempt-key-1")
		require.NoError(t, err)
		assert.Equal(t, "attempt-1", record.AttemptID)
		assert.Equal(t, "worker-1", record.WorkerID)
		assert.Equal(t, core.Running, record.Status)
	})

	t.Run("RunHeartbeatDoesNotRepairUnrelatedFailure", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		leaseStore := filedistributed.NewDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
		h := NewHandler(HandlerConfig{
			DAGRunStore:      store,
			DAGRunLeaseStore: leaseStore,
			Owner:            exec.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		})
		ctx := context.Background()

		ref := exec.NewDAGRunRef("test-dag", "run-123")
		attempt := store.addAttempt(ref, &exec.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			Root:       ref,
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     core.Failed,
			WorkerID:   "worker-1",
			Error:      "exit status 1",
			Nodes: []*exec.Node{
				{Status: core.NodeFailed, Error: "exit status 1"},
			},
		})

		initial := time.Now().Add(-time.Second).UTC()
		require.NoError(t, leaseStore.Upsert(ctx, exec.DAGRunLease{
			AttemptKey:      "attempt-key-1",
			DAGRun:          ref,
			Root:            ref,
			AttemptID:       "attempt-1",
			QueueName:       "test-dag",
			WorkerID:        "worker-1",
			Owner:           exec.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
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
		assert.Equal(t, core.Failed, status.Status)
		assert.Equal(t, "exit status 1", status.Error)
		assert.Equal(t, core.NodeFailed, status.Nodes[0].Status)
		assert.Equal(t, "exit status 1", status.Nodes[0].Error)
		assert.False(t, attempt.WasWritten())
	})

	t.Run("RunHeartbeatCancelsTaskWhenLeaseMissing", func(t *testing.T) {
		t.Parallel()

		leaseStore := filedistributed.NewDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
		h := NewHandler(HandlerConfig{
			DAGRunLeaseStore: leaseStore,
			Owner:            exec.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
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

	t.Run("RunHeartbeatRejectsNonOwnerCoordinator", func(t *testing.T) {
		t.Parallel()

		leaseStore := filedistributed.NewDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
		h := NewHandler(HandlerConfig{
			DAGRunLeaseStore: leaseStore,
			Owner:            exec.CoordinatorEndpoint{ID: "coord-a"},
		})
		ctx := context.Background()

		_, err := h.RunHeartbeat(ctx, &coordinatorv1.RunHeartbeatRequest{
			WorkerId:           "worker-1",
			OwnerCoordinatorId: "coord-b",
		})
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.FailedPrecondition, st.Code())
	})

	t.Run("HeartbeatSkipsLeaseRefreshOnAttemptKeyMismatch", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunStore: store, StaleLeaseThreshold: 10 * time.Second})
		ctx := context.Background()

		initialLease := time.Now().Add(-10 * time.Second).UnixMilli()
		ref := exec.DAGRunRef{Name: "test-dag", ID: "run-123"}
		attempt := store.addAttempt(ref, &exec.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			AttemptKey: "current-attempt-key",
			Status:     core.Running,
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
		h := NewHandler(HandlerConfig{DAGRunStore: store, StaleLeaseThreshold: 10 * time.Second})
		ctx := context.Background()

		initialLease := time.Now().Add(-10 * time.Second).UnixMilli()
		ref := exec.DAGRunRef{Name: "test-dag", ID: "run-123"}
		attempt := store.addAttempt(ref, &exec.DAGRunStatus{
			Name:      "test-dag",
			DAGRunID:  "run-123",
			AttemptID: "attempt-1",
			Status:    core.Running,
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
		h := NewHandler(HandlerConfig{DAGRunStore: store})
		ctx := context.Background()

		// Create a running DAG run
		ref := exec.DAGRunRef{Name: "test-dag", ID: "run-123"}
		initialStatus := &exec.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   core.Running,
			Nodes: []*exec.Node{
				{Status: core.NodeRunning},
				{Status: core.NodeSucceeded},
			},
		}
		attempt := store.addAttempt(ref, initialStatus)

		// Mark the run as failed
		h.markRunFailed(ctx, "test-dag", "run-123", "worker crashed")

		// Verify the status was updated
		require.True(t, attempt.WasOpened())
		require.True(t, attempt.WasWritten())
		require.True(t, attempt.WasClosed())

		// Check the status
		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, core.Failed, status.Status)
		require.Equal(t, "worker crashed", status.Error)
		require.NotEmpty(t, status.FinishedAt)

		// Check that running node was marked as failed
		require.Equal(t, core.NodeFailed, status.Nodes[0].Status)
		require.Equal(t, "worker crashed", status.Nodes[0].Error)
		// Succeeded node should remain unchanged
		require.Equal(t, core.NodeSucceeded, status.Nodes[1].Status)
	})

	t.Run("MarkRunFailedSkipsCompletedRuns", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunStore: store})
		ctx := context.Background()

		// Create an already completed DAG run
		ref := exec.DAGRunRef{Name: "test-dag", ID: "run-123"}
		initialStatus := &exec.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   core.Succeeded,
		}
		attempt := store.addAttempt(ref, initialStatus)

		// Try to mark the run as failed
		h.markRunFailed(ctx, "test-dag", "run-123", "worker crashed")

		// Verify no writes occurred (status should remain Succeeded)
		require.False(t, attempt.WasWritten())
		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, core.Succeeded, status.Status)
	})

	t.Run("MarkWorkerTasksFailedWithNoStore", func(t *testing.T) {
		t.Parallel()

		// Handler without dagRunStore
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
		h := NewHandler(HandlerConfig{DAGRunStore: store})
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
		h := NewHandler(HandlerConfig{DAGRunStore: store})
		ctx := context.Background()

		// Create a running DAG run
		ref := exec.DAGRunRef{Name: "test-dag", ID: "run-123"}
		initialStatus := &exec.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   core.Running,
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
		require.Equal(t, core.Failed, status.Status)
		require.Contains(t, status.Error, "stale-worker")
		require.Contains(t, status.Error, "unresponsive")
	})

	t.Run("DetectAndCleanupZombies", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunStore: store})
		ctx := context.Background()

		// Create two running DAG runs
		ref1 := exec.DAGRunRef{Name: "dag1", ID: "run-1"}
		status1 := &exec.DAGRunStatus{
			Name:     "dag1",
			DAGRunID: "run-1",
			Status:   core.Running,
		}
		attempt1 := store.addAttempt(ref1, status1)

		ref2 := exec.DAGRunRef{Name: "dag2", ID: "run-2"}
		status2 := &exec.DAGRunStatus{
			Name:     "dag2",
			DAGRunID: "run-2",
			Status:   core.Running,
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
		require.Equal(t, core.Failed, s1.Status)
		require.Equal(t, core.Failed, s2.Status)

		// Verify the stale worker was removed
		h.mu.Lock()
		_, exists := h.heartbeats["crashed-worker"]
		h.mu.Unlock()
		require.False(t, exists)
	})

	t.Run("StartZombieDetectorRunsPeriodically", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunStore: store})

		// Create a running DAG run
		ref := exec.DAGRunRef{Name: "test-dag", ID: "run-123"}
		status := &exec.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   core.Running,
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
		require.Equal(t, core.Failed, s.Status)
	})

	t.Run("DetectStaleLeasesOnlyFailsRunningDistributedRuns", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{
			DAGRunStore:             store,
			StaleLeaseThreshold:     time.Second,
			StaleHeartbeatThreshold: time.Second,
		})
		ctx := context.Background()

		staleLease := time.Now().Add(-5 * time.Second).UnixMilli()
		runningAttempt := store.addAttempt(exec.DAGRunRef{Name: "running-dag", ID: "run-1"}, &exec.DAGRunStatus{
			Name:     "running-dag",
			DAGRunID: "run-1",
			Status:   core.Running,
			WorkerID: "worker1",
			LeaseAt:  staleLease,
		})
		waitingAttempt := store.addAttempt(exec.DAGRunRef{Name: "waiting-dag", ID: "run-2"}, &exec.DAGRunStatus{
			Name:     "waiting-dag",
			DAGRunID: "run-2",
			Status:   core.Waiting,
			WorkerID: "worker1",
			LeaseAt:  staleLease,
		})
		queuedAttempt := store.addAttempt(exec.DAGRunRef{Name: "queued-dag", ID: "run-3"}, &exec.DAGRunStatus{
			Name:     "queued-dag",
			DAGRunID: "run-3",
			Status:   core.Queued,
			WorkerID: "worker1",
			LeaseAt:  staleLease,
		})

		h.detectStaleLeases(ctx)

		runningStatus, err := runningAttempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, core.Failed, runningStatus.Status)

		waitingStatus, err := waitingAttempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, core.Waiting, waitingStatus.Status)

		queuedStatus, err := queuedAttempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, core.Queued, queuedStatus.Status)
	})

	t.Run("DetectStaleSharedLeaseFailsLatestMatchingAttemptAndDeletesLease", func(t *testing.T) {
		t.Parallel()

		testCases := []struct {
			name       string
			status     core.Status
			nodeStatus core.NodeStatus
			workerID   string
		}{
			{name: "Running", status: core.Running, nodeStatus: core.NodeRunning, workerID: "worker-1"},
			{name: "NotStarted", status: core.NotStarted, nodeStatus: core.NodeNotStarted, workerID: "worker-1"},
			{name: "NotStartedWithoutPersistedWorkerID", status: core.NotStarted, nodeStatus: core.NodeNotStarted},
			{name: "Queued", status: core.Queued, nodeStatus: core.NodeNotStarted, workerID: "worker-1"},
			{name: "QueuedWithoutPersistedWorkerID", status: core.Queued, nodeStatus: core.NodeNotStarted},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				store := newMockDAGRunStore()
				leaseStore := filedistributed.NewDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
				h := NewHandler(HandlerConfig{
					DAGRunStore:         store,
					DAGRunLeaseStore:    leaseStore,
					StaleLeaseThreshold: time.Second,
				})
				ctx := context.Background()

				ref := exec.DAGRunRef{Name: "lease-dag", ID: "run-lease"}
				attempt := store.addAttempt(ref, &exec.DAGRunStatus{
					Name:       "lease-dag",
					DAGRunID:   "run-lease",
					AttemptID:  "attempt-1",
					AttemptKey: "lease-key-1",
					Status:     tc.status,
					WorkerID:   tc.workerID,
					Nodes: []*exec.Node{
						{Status: tc.nodeStatus},
					},
				})

				staleAt := time.Now().Add(-10 * time.Second).UTC()
				require.NoError(t, leaseStore.Upsert(ctx, exec.DAGRunLease{
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
				assert.Equal(t, core.Failed, status.Status)
				assert.Equal(t, staleDistributedLeaseReason("worker-1"), status.Error)
				assert.Equal(t, core.NodeFailed, status.Nodes[0].Status)
				assert.Equal(t, staleDistributedLeaseReason("worker-1"), status.Nodes[0].Error)

				_, err = leaseStore.Get(ctx, "lease-key-1")
				assert.ErrorIs(t, err, exec.ErrDAGRunLeaseNotFound)
			})
		}
	})

	t.Run("DetectStaleLeasesFailsLeasedRunWithoutStatusScan", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		store := newMockDAGRunStore()
		baseDir := filepath.Join(t.TempDir(), "distributed")
		leaseStore := filedistributed.NewDAGRunLeaseStore(baseDir)
		activeStore := filedistributed.NewActiveDistributedRunStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunStore:               store,
			DAGRunLeaseStore:          leaseStore,
			ActiveDistributedRunStore: activeStore,
			StaleLeaseThreshold:       time.Second,
		})

		ref := exec.DAGRunRef{Name: "lease-dag", ID: "run-lease"}
		attempt := store.addAttempt(ref, &exec.DAGRunStatus{
			Name:       "lease-dag",
			DAGRunID:   "run-lease",
			AttemptID:  "attempt-1",
			AttemptKey: "lease-key-1",
			Status:     core.Running,
			WorkerID:   "worker-1",
			Nodes: []*exec.Node{
				{Status: core.NodeRunning},
			},
		})

		staleAt := time.Now().Add(-10 * time.Second).UTC()
		require.NoError(t, leaseStore.Upsert(ctx, exec.DAGRunLease{
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
		assert.Equal(t, core.Failed, status.Status)
		assert.Equal(t, staleDistributedLeaseReason("worker-1"), status.Error)
		assert.Equal(t, core.NodeFailed, status.Nodes[0].Status)

		_, err = leaseStore.Get(ctx, "lease-key-1")
		assert.ErrorIs(t, err, exec.ErrDAGRunLeaseNotFound)
	})

	t.Run("DetectStaleLeasesFailsSubDAGLeasedRunWhenFreshWorkerHeartbeatDropsAttempt", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		store := newMockDAGRunStore()
		baseDir := filepath.Join(t.TempDir(), "distributed")
		leaseStore := filedistributed.NewDAGRunLeaseStore(baseDir)
		heartbeatStore := filedistributed.NewWorkerHeartbeatStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunStore:             store,
			DAGRunLeaseStore:        leaseStore,
			WorkerHeartbeatStore:    heartbeatStore,
			StaleHeartbeatThreshold: time.Minute,
			StaleLeaseThreshold:     time.Second,
		})

		root := exec.NewDAGRunRef("root-dag", "root-run")
		subRun := exec.NewDAGRunRef("sub-dag", "sub-run")
		attemptKey := "sub-attempt-key"
		attemptID := "sub-attempt-id"
		attempt := store.addSubAttempt(root, subRun.ID, &exec.DAGRunStatus{
			Name:       subRun.Name,
			DAGRunID:   subRun.ID,
			Root:       root,
			AttemptID:  attemptID,
			AttemptKey: attemptKey,
			Status:     core.Running,
			WorkerID:   "worker-1",
			Nodes: []*exec.Node{
				{Status: core.NodeRunning},
			},
		})

		staleAt := time.Now().Add(-10 * time.Second).UTC()
		require.NoError(t, leaseStore.Upsert(ctx, exec.DAGRunLease{
			AttemptKey:      attemptKey,
			DAGRun:          subRun,
			Root:            root,
			AttemptID:       attemptID,
			QueueName:       subRun.Name,
			WorkerID:        "worker-1",
			LastHeartbeatAt: staleAt.UnixMilli(),
			ClaimedAt:       staleAt.UnixMilli(),
		}))
		require.NoError(t, heartbeatStore.Upsert(ctx, exec.WorkerHeartbeatRecord{
			WorkerID:        "worker-1",
			LastHeartbeatAt: time.Now().UTC().UnixMilli(),
			Stats: &coordinatorv1.WorkerStats{
				RunningTasks: []*coordinatorv1.RunningTask{},
			},
		}))

		h.detectStaleLeases(ctx)

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		require.Equal(t, core.Failed, status.Status)
		require.Equal(t, staleDistributedLeaseReason("worker-1"), status.Error)
		require.Equal(t, core.NodeFailed, status.Nodes[0].Status)
		require.Equal(t, staleDistributedLeaseReason("worker-1"), status.Nodes[0].Error)
	})

	t.Run("DetectStaleLeasesKeepsLeasedRunWhenFreshWorkerHeartbeatStillReportsAttempt", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		store := newMockDAGRunStore()
		baseDir := filepath.Join(t.TempDir(), "distributed")
		leaseStore := filedistributed.NewDAGRunLeaseStore(baseDir)
		heartbeatStore := filedistributed.NewWorkerHeartbeatStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunStore:             store,
			WorkerHeartbeatStore:    heartbeatStore,
			DAGRunLeaseStore:        leaseStore,
			StaleHeartbeatThreshold: time.Minute,
			StaleLeaseThreshold:     time.Second,
		})

		ref := exec.DAGRunRef{Name: "lease-dag", ID: "run-lease"}
		attemptKey := "lease-key-1"
		attempt := store.addAttempt(ref, &exec.DAGRunStatus{
			Name:       "lease-dag",
			DAGRunID:   "run-lease",
			AttemptID:  "attempt-1",
			AttemptKey: attemptKey,
			Status:     core.Running,
			WorkerID:   "worker-1",
			Nodes: []*exec.Node{
				{Status: core.NodeRunning},
			},
		})

		staleAt := time.Now().Add(-10 * time.Second).UTC()
		require.NoError(t, leaseStore.Upsert(ctx, exec.DAGRunLease{
			AttemptKey:      attemptKey,
			DAGRun:          ref,
			Root:            ref,
			AttemptID:       "attempt-1",
			QueueName:       "lease-dag",
			WorkerID:        "worker-1",
			LastHeartbeatAt: staleAt.UnixMilli(),
			ClaimedAt:       staleAt.UnixMilli(),
		}))
		require.NoError(t, heartbeatStore.Upsert(ctx, exec.WorkerHeartbeatRecord{
			WorkerID:        "worker-1",
			LastHeartbeatAt: time.Now().UTC().UnixMilli(),
			Stats: &coordinatorv1.WorkerStats{
				RunningTasks: []*coordinatorv1.RunningTask{
					{
						DagRunId:       "run-lease",
						DagName:        "lease-dag",
						RootDagRunId:   "run-lease",
						RootDagRunName: "lease-dag",
						AttemptKey:     attemptKey,
					},
				},
			},
		}))

		h.detectStaleLeases(ctx)

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, core.Running, status.Status)
		assert.Equal(t, core.NodeRunning, status.Nodes[0].Status)

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
		leaseStore := filedistributed.NewDAGRunLeaseStore(baseDir)
		heartbeatStore := filedistributed.NewWorkerHeartbeatStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunStore:             store,
			WorkerHeartbeatStore:    heartbeatStore,
			DAGRunLeaseStore:        leaseStore,
			StaleHeartbeatThreshold: time.Minute,
			StaleLeaseThreshold:     time.Second,
		})

		ref := exec.DAGRunRef{Name: "lease-dag", ID: "run-lease"}
		attemptKey := "lease-key-1"
		attempt := store.addAttempt(ref, &exec.DAGRunStatus{
			Name:       "lease-dag",
			DAGRunID:   "run-lease",
			AttemptID:  "attempt-1",
			AttemptKey: attemptKey,
			Status:     core.Running,
			WorkerID:   "worker-1",
			Nodes: []*exec.Node{
				{Status: core.NodeRunning},
			},
		})

		require.NoError(t, heartbeatStore.Upsert(ctx, exec.WorkerHeartbeatRecord{
			WorkerID:        "worker-1",
			LastHeartbeatAt: time.Now().UTC().UnixMilli(),
			Stats: &coordinatorv1.WorkerStats{
				RunningTasks: []*coordinatorv1.RunningTask{
					{
						DagRunId:       "run-lease",
						DagName:        "lease-dag",
						RootDagRunId:   "run-lease",
						RootDagRunName: "lease-dag",
						AttemptKey:     attemptKey,
					},
				},
			},
		}))

		h.detectStaleLeases(ctx)

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, core.Running, status.Status)
		assert.Equal(t, core.NodeRunning, status.Nodes[0].Status)

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
		leaseStore := filedistributed.NewDAGRunLeaseStore(baseDir)
		heartbeatStore := filedistributed.NewWorkerHeartbeatStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunStore:             store,
			WorkerHeartbeatStore:    heartbeatStore,
			DAGRunLeaseStore:        leaseStore,
			StaleHeartbeatThreshold: time.Minute,
			StaleLeaseThreshold:     time.Second,
		})

		ref := exec.DAGRunRef{Name: "lease-dag", ID: "run-lease"}
		attemptKey := "lease-key-1"
		attempt := store.addAttempt(ref, &exec.DAGRunStatus{
			Name:       "lease-dag",
			DAGRunID:   "run-lease",
			AttemptID:  "attempt-1",
			AttemptKey: attemptKey,
			Status:     core.Running,
			WorkerID:   "worker-1",
			Nodes: []*exec.Node{
				{Status: core.NodeRunning},
			},
		})

		staleAt := time.Now().Add(-10 * time.Second).UTC()
		require.NoError(t, leaseStore.Upsert(ctx, exec.DAGRunLease{
			AttemptKey:      attemptKey,
			DAGRun:          ref,
			Root:            ref,
			AttemptID:       "attempt-1",
			QueueName:       "lease-dag",
			WorkerID:        "worker-1",
			LastHeartbeatAt: staleAt.UnixMilli(),
			ClaimedAt:       staleAt.UnixMilli(),
		}))
		require.NoError(t, heartbeatStore.Upsert(ctx, exec.WorkerHeartbeatRecord{
			WorkerID:        "worker-1",
			LastHeartbeatAt: time.Now().UTC().UnixMilli(),
			Stats: &coordinatorv1.WorkerStats{
				RunningTasks: []*coordinatorv1.RunningTask{},
			},
		}))

		h.detectStaleLeases(ctx)

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, core.Failed, status.Status)
		assert.Equal(t, staleDistributedLeaseReason("worker-1"), status.Error)
		assert.Equal(t, core.NodeFailed, status.Nodes[0].Status)

		_, err = leaseStore.Get(ctx, attemptKey)
		assert.ErrorIs(t, err, exec.ErrDAGRunLeaseNotFound)
	})

	t.Run("DetectStaleLeasesFailsOrphanedDistributedStatusWithoutLease", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		leaseStore := filedistributed.NewDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
		h := NewHandler(HandlerConfig{
			DAGRunStore:         store,
			DAGRunLeaseStore:    leaseStore,
			StaleLeaseThreshold: time.Second,
		})
		ctx := context.Background()

		ref := exec.DAGRunRef{Name: "lease-dag", ID: "run-lease"}
		attempt := store.addAttempt(ref, &exec.DAGRunStatus{
			Name:       "lease-dag",
			DAGRunID:   "run-lease",
			AttemptID:  "attempt-1",
			AttemptKey: "lease-key-1",
			Status:     core.Running,
			WorkerID:   "worker-1",
			Nodes: []*exec.Node{
				{Status: core.NodeRunning},
			},
		})

		h.detectStaleLeases(ctx)

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, core.Failed, status.Status)
		assert.Equal(t, staleDistributedLeaseReason("worker-1"), status.Error)
		assert.Equal(t, core.NodeFailed, status.Nodes[0].Status)
	})

	t.Run("DetectStaleLeasesRestoresMissingLeaseFromActiveIndexWhenFreshWorkerHeartbeatStillReportsAttempt", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		store := newMockDAGRunStore()
		baseDir := filepath.Join(t.TempDir(), "distributed")
		leaseStore := filedistributed.NewDAGRunLeaseStore(baseDir)
		heartbeatStore := filedistributed.NewWorkerHeartbeatStore(baseDir)
		activeStore := filedistributed.NewActiveDistributedRunStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunStore:               store,
			WorkerHeartbeatStore:      heartbeatStore,
			DAGRunLeaseStore:          leaseStore,
			ActiveDistributedRunStore: activeStore,
			StaleHeartbeatThreshold:   time.Minute,
			StaleLeaseThreshold:       time.Second,
		})

		ref := exec.DAGRunRef{Name: "lease-dag", ID: "run-lease"}
		attemptKey := "lease-key-1"
		attempt := store.addAttempt(ref, &exec.DAGRunStatus{
			Name:       "lease-dag",
			DAGRunID:   "run-lease",
			AttemptID:  "attempt-1",
			AttemptKey: attemptKey,
			Status:     core.Running,
			WorkerID:   "worker-1",
			Nodes: []*exec.Node{
				{Status: core.NodeRunning},
			},
		})
		staleAt := time.Now().Add(-10 * time.Second).UTC()
		require.NoError(t, activeStore.Upsert(ctx, exec.ActiveDistributedRun{
			AttemptKey: attemptKey,
			DAGRun:     ref,
			Root:       ref,
			AttemptID:  "attempt-1",
			WorkerID:   "worker-1",
			Status:     core.Running,
			UpdatedAt:  staleAt.UnixMilli(),
		}))
		require.NoError(t, heartbeatStore.Upsert(ctx, exec.WorkerHeartbeatRecord{
			WorkerID:        "worker-1",
			LastHeartbeatAt: time.Now().UTC().UnixMilli(),
			Stats: &coordinatorv1.WorkerStats{
				RunningTasks: []*coordinatorv1.RunningTask{
					{
						DagRunId:       "run-lease",
						DagName:        "lease-dag",
						RootDagRunId:   "run-lease",
						RootDagRunName: "lease-dag",
						AttemptKey:     attemptKey,
					},
				},
			},
		}))

		h.detectStaleLeases(ctx)

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, core.Running, status.Status)
		assert.Equal(t, core.NodeRunning, status.Nodes[0].Status)

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
		leaseStore := filedistributed.NewDAGRunLeaseStore(baseDir)
		activeStore := filedistributed.NewActiveDistributedRunStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunStore:               store,
			DAGRunLeaseStore:          leaseStore,
			ActiveDistributedRunStore: activeStore,
			StaleLeaseThreshold:       time.Minute,
		})

		ref := exec.DAGRunRef{Name: "lease-dag", ID: "run-lease"}
		attemptKey := "lease-key-1"
		attempt := store.addAttempt(ref, &exec.DAGRunStatus{
			Name:       "lease-dag",
			DAGRunID:   "run-lease",
			AttemptID:  "attempt-1",
			AttemptKey: attemptKey,
			Status:     core.Running,
			WorkerID:   "worker-1",
			Nodes: []*exec.Node{
				{Status: core.NodeRunning},
			},
		})
		freshAt := time.Now().UTC()
		require.NoError(t, leaseStore.Upsert(ctx, exec.DAGRunLease{
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
		assert.Equal(t, core.Running, status.Status)

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
		leaseStore := filedistributed.NewDAGRunLeaseStore(baseDir)
		activeStore := filedistributed.NewActiveDistributedRunStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunStore:               store,
			DAGRunLeaseStore:          leaseStore,
			ActiveDistributedRunStore: activeStore,
			StaleLeaseThreshold:       time.Second,
		})

		ref := exec.DAGRunRef{Name: "lease-dag", ID: "run-lease"}
		attempt := store.addAttempt(ref, &exec.DAGRunStatus{
			Name:       "lease-dag",
			DAGRunID:   "run-lease",
			AttemptID:  "attempt-1",
			AttemptKey: "lease-key-1",
			Status:     core.Running,
			WorkerID:   "worker-1",
			Nodes: []*exec.Node{
				{Status: core.NodeRunning},
			},
		})
		require.NoError(t, activeStore.Upsert(ctx, exec.ActiveDistributedRun{
			AttemptKey: "lease-key-1",
			DAGRun:     ref,
			Root:       ref,
			AttemptID:  "attempt-1",
			WorkerID:   "worker-1",
			Status:     core.Running,
		}))

		h.detectStaleLeases(ctx)

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, core.Failed, status.Status)
		assert.Equal(t, staleDistributedLeaseReason("worker-1"), status.Error)

		records, err := activeStore.ListAll(ctx)
		require.NoError(t, err)
		assert.Empty(t, records)
	})

	t.Run("DetectStaleLeasesDoesNotScanStatusesWhenActiveIndexMissesRun", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()
		store := newMockDAGRunStore()
		baseDir := filepath.Join(t.TempDir(), "distributed")
		leaseStore := filedistributed.NewDAGRunLeaseStore(baseDir)
		activeStore := filedistributed.NewActiveDistributedRunStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunStore:               store,
			DAGRunLeaseStore:          leaseStore,
			ActiveDistributedRunStore: activeStore,
			StaleLeaseThreshold:       time.Second,
		})

		ref := exec.DAGRunRef{Name: "lease-dag", ID: "run-missing-index"}
		attempt := store.addAttempt(ref, &exec.DAGRunStatus{
			Name:       "lease-dag",
			DAGRunID:   "run-missing-index",
			AttemptID:  "attempt-1",
			AttemptKey: "lease-key-missing-index",
			Status:     core.Running,
			WorkerID:   "worker-1",
			Nodes: []*exec.Node{
				{Status: core.NodeRunning},
			},
		})

		h.detectStaleLeases(ctx)

		status, err := attempt.ReadStatus(ctx)
		require.NoError(t, err)
		assert.Equal(t, core.Running, status.Status)
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
		leaseStore := filedistributed.NewDAGRunLeaseStore(baseDir)
		activeStore := filedistributed.NewActiveDistributedRunStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunStore:               store,
			DAGRunLeaseStore:          leaseStore,
			ActiveDistributedRunStore: activeStore,
			StaleLeaseThreshold:       time.Second,
		})

		ref := exec.DAGRunRef{Name: "lease-dag", ID: "run-corrupted"}
		attemptKey := "lease-key-corrupted"
		attempt := store.addAttempt(ref, &exec.DAGRunStatus{
			Name:       "lease-dag",
			DAGRunID:   "run-corrupted",
			AttemptID:  "attempt-1",
			AttemptKey: attemptKey,
			Status:     core.Running,
			WorkerID:   "worker-1",
		})
		attempt.readStatusError = exec.ErrCorruptedStatusFile

		staleAt := time.Now().Add(-10 * time.Second).UTC()
		require.NoError(t, leaseStore.Upsert(ctx, exec.DAGRunLease{
			AttemptKey:      attemptKey,
			DAGRun:          ref,
			Root:            ref,
			AttemptID:       "attempt-1",
			QueueName:       "lease-dag",
			WorkerID:        "worker-1",
			LastHeartbeatAt: staleAt.UnixMilli(),
			ClaimedAt:       staleAt.UnixMilli(),
		}))
		require.NoError(t, activeStore.Upsert(ctx, exec.ActiveDistributedRun{
			AttemptKey: attemptKey,
			DAGRun:     ref,
			Root:       ref,
			AttemptID:  "attempt-1",
			WorkerID:   "worker-1",
			Status:     core.Running,
		}))

		h.detectStaleLeases(ctx)

		assert.Zero(t, store.ListStatusesCallCount())
		_, err := leaseStore.Get(ctx, attemptKey)
		assert.ErrorIs(t, err, exec.ErrDAGRunLeaseNotFound)
		_, err = activeStore.Get(ctx, attemptKey)
		assert.ErrorIs(t, err, exec.ErrActiveRunNotFound)
	})
}

func TestHandler_ReportStatus(t *testing.T) {
	t.Parallel()

	t.Run("ValidStatusReport", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunStore: store})
		ctx := context.Background()

		// Create an attempt for the DAG run
		ref := exec.DAGRunRef{Name: "test-dag", ID: "run-123"}
		store.addAttempt(ref, &exec.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   core.Running,
		})

		// Report status
		protoStatus, convErr := convert.DAGRunStatusToProto(&exec.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   core.Running,
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
		h := NewHandler(HandlerConfig{DAGRunStore: store})
		ctx := context.Background()

		ref := exec.DAGRunRef{Name: "test-dag", ID: "run-123"}
		attempt := store.addAttempt(ref, &exec.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   core.Running,
		})

		protoStatus, convErr := convert.DAGRunStatusToProto(&exec.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   core.Running,
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

	t.Run("RejectsLateStatusForLeaseCleanedAttempt", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		leaseStore := filedistributed.NewDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
		h := NewHandler(HandlerConfig{
			DAGRunStore:      store,
			DAGRunLeaseStore: leaseStore,
			Owner:            exec.CoordinatorEndpoint{ID: "coord-a"},
		})
		ctx := context.Background()

		ref := exec.DAGRunRef{Name: "test-dag", ID: "run-123"}
		attempt := store.addAttempt(ref, &exec.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     core.Failed,
		})

		protoStatus, convErr := convert.DAGRunStatusToProto(&exec.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     core.Running,
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
		assert.Equal(t, core.Failed, current.Status)

		_, err = leaseStore.Get(ctx, "attempt-key-1")
		assert.ErrorIs(t, err, exec.ErrDAGRunLeaseNotFound)
	})

	t.Run("RejectsSupersededAttemptStatus", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunStore: store})
		ctx := context.Background()

		ref := exec.DAGRunRef{Name: "test-dag", ID: "run-123"}
		attempt := store.addAttempt(ref, &exec.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-2",
			AttemptKey: "attempt-key-2",
			Status:     core.Running,
		})

		protoStatus, convErr := convert.DAGRunStatusToProto(&exec.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     core.Running,
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
		leaseStore := filedistributed.NewDAGRunLeaseStore(baseDir)
		activeStore := filedistributed.NewActiveDistributedRunStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunStore:               store,
			DAGRunLeaseStore:          leaseStore,
			ActiveDistributedRunStore: activeStore,
			Owner:                     exec.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		})
		ctx := context.Background()

		ref := exec.DAGRunRef{Name: "test-dag", ID: "run-123"}
		attempt := store.addAttempt(ref, &exec.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			WorkerID:   "worker-1",
			Status:     core.Failed,
		})
		require.NoError(t, leaseStore.Upsert(ctx, exec.DAGRunLease{
			AttemptKey:      "attempt-key-1",
			DAGRun:          ref,
			Root:            ref,
			AttemptID:       "attempt-1",
			QueueName:       "queue-a",
			WorkerID:        "worker-1",
			ClaimedAt:       time.Now().UTC().UnixMilli(),
			LastHeartbeatAt: time.Now().UTC().UnixMilli(),
		}))
		require.NoError(t, activeStore.Upsert(ctx, exec.ActiveDistributedRun{
			AttemptKey: "attempt-key-1",
			DAGRun:     ref,
			Root:       ref,
			AttemptID:  "attempt-1",
			WorkerID:   "worker-1",
			Status:     core.Running,
		}))

		protoStatus, convErr := convert.DAGRunStatusToProto(&exec.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			WorkerID:   "worker-1",
			Status:     core.Failed,
			Error:      "duplicate terminal payload",
			Nodes: []*exec.Node{
				{
					Step:   core.Step{Name: "chat-step"},
					Status: core.NodeFailed,
					ChatMessages: []exec.LLMMessage{
						{Role: exec.RoleAssistant, Content: "final summary"},
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

		messages := attempt.GetStepMessages("chat-step")
		require.Len(t, messages, 1)
		assert.Equal(t, "final summary", messages[0].Content)

		_, err = leaseStore.Get(ctx, "attempt-key-1")
		assert.ErrorIs(t, err, exec.ErrDAGRunLeaseNotFound)

		_, err = activeStore.Get(ctx, "attempt-key-1")
		assert.ErrorIs(t, err, exec.ErrActiveRunNotFound)
	})

	t.Run("MissingStatusReturnsError", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunStore: store})
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

	t.Run("NilDAGRunStoreReturnsError", func(t *testing.T) {
		t.Parallel()

		h := NewHandler(HandlerConfig{}) // No dagRunStore
		ctx := context.Background()

		protoStatus, convErr := convert.DAGRunStatusToProto(&exec.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   core.Running,
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
		h := NewHandler(HandlerConfig{DAGRunStore: store})
		ctx := context.Background()

		// Create an attempt for the DAG run
		ref := exec.DAGRunRef{Name: "chat-dag", ID: "chat-run-123"}
		attempt := store.addAttempt(ref, &exec.DAGRunStatus{
			Name:     "chat-dag",
			DAGRunID: "chat-run-123",
			Status:   core.Running,
		})

		// Create status with ChatMessages
		statusWithMessages := &exec.DAGRunStatus{
			Name:     "chat-dag",
			DAGRunID: "chat-run-123",
			Status:   core.Running,
			Nodes: []*exec.Node{
				{
					Step:   core.Step{Name: "chat-step"},
					Status: core.NodeSucceeded,
					ChatMessages: []exec.LLMMessage{
						{Role: exec.RoleUser, Content: "Hello!"},
						{Role: exec.RoleAssistant, Content: "Hi there!", Metadata: &exec.LLMMessageMetadata{
							Provider:    "openai",
							Model:       "gpt-4",
							TotalTokens: 10,
						}},
					},
				},
				{
					Step:   core.Step{Name: "no-messages-step"},
					Status: core.NodeSucceeded,
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
		assert.Equal(t, exec.RoleUser, chatStepMessages[0].Role)
		assert.Equal(t, "Hello!", chatStepMessages[0].Content)
		assert.Equal(t, exec.RoleAssistant, chatStepMessages[1].Role)
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
		h := NewHandler(HandlerConfig{DAGRunStore: store})
		ctx := context.Background()

		// Create an existing attempt
		ref := exec.DAGRunRef{Name: "handler-dag", ID: "handler-run-123"}
		attempt := store.addAttempt(ref, &exec.DAGRunStatus{
			Name:     "handler-dag",
			DAGRunID: "handler-run-123",
			Status:   core.Running,
		})

		// Create status with handler nodes that have empty step names
		statusWithHandlers := &exec.DAGRunStatus{
			Name:     "handler-dag",
			DAGRunID: "handler-run-123",
			Status:   core.Succeeded,
			// OnInit handler with empty step name - should use "on_init" fallback
			OnInit: &exec.Node{
				Step:   core.Step{}, // Empty name
				Status: core.NodeSucceeded,
				ChatMessages: []exec.LLMMessage{
					{Role: exec.RoleAssistant, Content: "Init completed"},
				},
			},
			// OnSuccess handler with explicit name - should use explicit name
			OnSuccess: &exec.Node{
				Step:   core.Step{Name: "my-success-handler"},
				Status: core.NodeSucceeded,
				ChatMessages: []exec.LLMMessage{
					{Role: exec.RoleAssistant, Content: "Success!"},
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
		h := NewHandler(HandlerConfig{DAGRunStore: store})
		ctx := context.Background()

		// Create an existing attempt with error injection
		ref := exec.DAGRunRef{Name: "error-dag", ID: "error-run-123"}
		attempt := store.addAttempt(ref, &exec.DAGRunStatus{
			Name:     "error-dag",
			DAGRunID: "error-run-123",
			Status:   core.Running,
		})

		// Inject error for WriteStepMessages
		attempt.writeStepMessagesError = errors.New("simulated write failure")

		// Create status with ChatMessages
		statusWithMessages := &exec.DAGRunStatus{
			Name:     "error-dag",
			DAGRunID: "error-run-123",
			Status:   core.Succeeded,
			Nodes: []*exec.Node{
				{
					Step:   core.Step{Name: "chat-step"},
					Status: core.NodeSucceeded,
					ChatMessages: []exec.LLMMessage{
						{Role: exec.RoleUser, Content: "Hello!"},
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
		leaseStore := filedistributed.NewDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
		h := NewHandler(HandlerConfig{
			DAGRunStore:      store,
			DAGRunLeaseStore: leaseStore,
			Owner:            exec.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		})
		ctx := context.Background()

		ref := exec.DAGRunRef{Name: "test-dag", ID: "run-123"}
		attempt := store.addAttempt(ref, &exec.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   core.NotStarted,
		})

		protoStatus, convErr := convert.DAGRunStatusToProto(&exec.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			ProcGroup:  "test-queue",
			Status:     core.Running,
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
		leaseStore := filedistributed.NewDAGRunLeaseStore(filepath.Join(t.TempDir(), "distributed"))
		h := NewHandler(HandlerConfig{
			DAGRunStore:      store,
			DAGRunLeaseStore: leaseStore,
			Owner:            exec.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		})
		ctx := context.Background()

		ref := exec.DAGRunRef{Name: "test-dag", ID: "run-123"}
		store.addAttempt(ref, &exec.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   core.NotStarted,
		})

		require.NoError(t, leaseStore.Upsert(ctx, exec.DAGRunLease{
			AttemptKey:      "attempt-key-1",
			DAGRun:          ref,
			Root:            ref,
			AttemptID:       "attempt-1",
			QueueName:       "queue-a",
			WorkerID:        "worker-1",
			ClaimedAt:       time.Now().Add(-time.Second).UTC().UnixMilli(),
			LastHeartbeatAt: time.Now().Add(-time.Second).UTC().UnixMilli(),
		}))

		protoStatus, convErr := convert.DAGRunStatusToProto(&exec.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     core.Running,
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
		leaseStore := filedistributed.NewDAGRunLeaseStore(baseDir)
		activeStore := filedistributed.NewActiveDistributedRunStore(baseDir)
		h := NewHandler(HandlerConfig{
			DAGRunStore:               store,
			DAGRunLeaseStore:          leaseStore,
			ActiveDistributedRunStore: activeStore,
			Owner:                     exec.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		})
		ctx := context.Background()

		ref := exec.DAGRunRef{Name: "test-dag", ID: "run-123"}
		store.addAttempt(ref, &exec.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     core.NotStarted,
			WorkerID:   "worker-1",
		})

		runningProto, convErr := convert.DAGRunStatusToProto(&exec.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     core.Running,
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
		assert.Equal(t, core.Running, record.Status)

		succeededProto, convErr := convert.DAGRunStatusToProto(&exec.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     core.Succeeded,
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
		assert.ErrorIs(t, err, exec.ErrActiveRunNotFound)
	})

	t.Run("AckTaskClaimCreatesLeaseAndDeletesClaim", func(t *testing.T) {
		t.Parallel()

		baseDir := filepath.Join(t.TempDir(), "distributed")
		dispatchStore := filedistributed.NewDispatchTaskStore(baseDir)
		leaseStore := filedistributed.NewDAGRunLeaseStore(baseDir)
		activeStore := filedistributed.NewActiveDistributedRunStore(baseDir)
		h := NewHandler(HandlerConfig{
			DispatchTaskStore:         dispatchStore,
			DAGRunLeaseStore:          leaseStore,
			ActiveDistributedRunStore: activeStore,
			Owner:                     exec.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		})
		ctx := context.Background()

		task := &coordinatorv1.Task{
			DagRunId:       "run-123",
			Target:         "test-dag",
			AttemptId:      "attempt-1",
			AttemptKey:     "attempt-key-1",
			QueueName:      "queue-a",
			RootDagRunName: "test-dag",
			RootDagRunId:   "run-123",
		}
		require.NoError(t, dispatchStore.Enqueue(ctx, task))

		claimed, err := dispatchStore.ClaimNext(ctx, exec.DispatchTaskClaim{
			WorkerID: "worker-1",
			PollerID: "poller-1",
			Owner:    exec.CoordinatorEndpoint{ID: "coord-a", Host: "127.0.0.1", Port: 1234},
		})
		require.NoError(t, err)
		require.NotNil(t, claimed)

		resp, err := h.AckTaskClaim(ctx, &coordinatorv1.AckTaskClaimRequest{
			ClaimToken: claimed.ClaimToken,
			WorkerId:   "worker-1",
		})
		require.NoError(t, err)
		require.True(t, resp.Accepted)

		lease, err := leaseStore.Get(ctx, "attempt-key-1")
		require.NoError(t, err)
		assert.Equal(t, "queue-a", lease.QueueName)
		assert.Equal(t, "worker-1", lease.WorkerID)
		assert.Equal(t, "coord-a", lease.Owner.ID)

		record, err := activeStore.Get(ctx, "attempt-key-1")
		require.NoError(t, err)
		assert.Equal(t, "test-dag", record.DAGRun.Name)
		assert.Equal(t, "run-123", record.DAGRun.ID)
		assert.Equal(t, "attempt-1", record.AttemptID)
		assert.Equal(t, "worker-1", record.WorkerID)
		assert.Equal(t, core.Queued, record.Status)

		_, err = dispatchStore.GetClaim(ctx, claimed.ClaimToken)
		assert.ErrorIs(t, err, exec.ErrDispatchTaskNotFound)
	})
}

func TestHandler_GetDAGRunStatus(t *testing.T) {
	t.Parallel()

	t.Run("TopLevelDAGLookup", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunStore: store})
		ctx := context.Background()

		// Create an attempt with status
		ref := exec.DAGRunRef{Name: "test-dag", ID: "run-123"}
		store.addAttempt(ref, &exec.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   core.Running,
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
		h := NewHandler(HandlerConfig{DAGRunStore: store})
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

	t.Run("NilDAGRunStoreReturnsError", func(t *testing.T) {
		t.Parallel()

		h := NewHandler(HandlerConfig{}) // No dagRunStore
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
		h := NewHandler(HandlerConfig{DAGRunStore: store})
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
		h := NewHandler(HandlerConfig{DAGRunStore: store})
		ctx := context.Background()

		// Create and cache an attempt
		ref := exec.DAGRunRef{Name: "test-dag", ID: "run-123"}
		attempt := store.addAttempt(ref, &exec.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   core.Running,
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

		h := NewHandler(HandlerConfig{}) // No dagRunStore
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
		h := NewHandler(HandlerConfig{DAGRunStore: store})
		ctx := context.Background()

		result := h.getCancelledRunsForWorker(ctx, nil)
		require.Nil(t, result)
	})

	t.Run("ReturnsNilWithEmptyRunningTasks", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunStore: store})
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

	t.Run("WithDAGRunStore", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunStore: store})

		require.Same(t, store, h.dagRunStore)
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
		h := NewHandler(HandlerConfig{DAGRunStore: store, LogDir: logDir})

		require.Same(t, store, h.dagRunStore)
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
}

func TestHandler_GetCancelledRunsForWorker_Full(t *testing.T) {
	t.Parallel()

	t.Run("ReturnsCancelledRuns", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunStore: store})
		ctx := context.Background()

		// Create an attempt that is aborting (cancelled)
		ref := exec.DAGRunRef{Name: "test-dag", ID: "run-123"}
		store.addAbortingAttempt(ref, &exec.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   core.Running, // Status doesn't matter, IsAborting is what's checked
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
		h := NewHandler(HandlerConfig{DAGRunStore: store})
		ctx := context.Background()

		// Create an attempt that is running (not cancelled)
		ref := exec.DAGRunRef{Name: "test-dag", ID: "run-456"}
		store.addAttempt(ref, &exec.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-456",
			Status:   core.Running,
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
		h := NewHandler(HandlerConfig{DAGRunStore: store})
		ctx := context.Background()

		ref := exec.DAGRunRef{Name: "test-dag", ID: "run-789"}
		store.addAttempt(ref, &exec.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-789",
			AttemptID:  "attempt-2",
			AttemptKey: "attempt-key-2",
			Status:     core.Running,
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
		h := NewHandler(HandlerConfig{DAGRunStore: store})
		ctx := context.Background()

		ref := exec.DAGRunRef{Name: "test-dag", ID: "run-999"}
		store.addAttempt(ref, &exec.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-999",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     core.Failed,
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
		h := NewHandler(HandlerConfig{DAGRunStore: store})
		ctx := context.Background()

		ref := exec.DAGRunRef{Name: "test-dag", ID: "run-success"}
		store.addAttempt(ref, &exec.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-success",
			AttemptID:  "attempt-1",
			AttemptKey: "attempt-key-1",
			Status:     core.Succeeded,
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
		h := NewHandler(HandlerConfig{DAGRunStore: store})
		ctx := context.Background()

		rootRef := exec.DAGRunRef{Name: "parent-dag", ID: "root-run"}
		attempt := store.addSubAttempt(rootRef, "child-run", &exec.DAGRunStatus{
			Name:      "child-dag",
			DAGRunID:  "child-run",
			AttemptID: "attempt-1",
			Status:    core.NotStarted,
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
		require.Equal(t, core.Aborted, attempt.status.Status)
		require.NotEmpty(t, attempt.status.FinishedAt)
		require.Equal(t, context.Canceled.Error(), attempt.status.Error)
	})

	t.Run("LeavesActiveSubAttemptForWorkerShutdown", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunStore: store})
		ctx := context.Background()

		rootRef := exec.DAGRunRef{Name: "parent-dag", ID: "root-run"}
		attempt := store.addSubAttempt(rootRef, "child-run", &exec.DAGRunStatus{
			Name:      "child-dag",
			DAGRunID:  "child-run",
			AttemptID: "attempt-1",
			Status:    core.Running,
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
		require.Equal(t, core.Running, attempt.status.Status)
	})
}

func TestHandler_GetOrOpenSubAttempt(t *testing.T) {
	t.Parallel()

	t.Run("OpensSubAttemptOnFirstAccess", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunStore: store})
		ctx := context.Background()

		// Add a sub-attempt
		rootRef := exec.DAGRunRef{Name: "parent-dag", ID: "root-123"}
		subDAGRunID := "sub-456"
		store.addSubAttempt(rootRef, subDAGRunID, &exec.DAGRunStatus{
			Name:     "child-dag",
			DAGRunID: subDAGRunID,
			Status:   core.Running,
		})

		// Get the sub-attempt
		attempt, err := h.getOrOpenSubAttempt(ctx, rootRef, subDAGRunID)
		require.NoError(t, err)
		require.NotNil(t, attempt)

		// Verify it was opened
		mockAttempt := attempt.(*mockDAGRunAttempt)
		assert.True(t, mockAttempt.WasOpened())
	})

	t.Run("ReturnsCachedAttemptOnSecondAccess", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunStore: store})
		ctx := context.Background()

		// Add a sub-attempt
		rootRef := exec.DAGRunRef{Name: "parent-dag", ID: "root-789"}
		subDAGRunID := "sub-101"
		store.addSubAttempt(rootRef, subDAGRunID, &exec.DAGRunStatus{
			Name:     "child-dag",
			DAGRunID: subDAGRunID,
			Status:   core.Running,
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
		h := NewHandler(HandlerConfig{DAGRunStore: store})
		ctx := context.Background()

		rootRef := exec.DAGRunRef{Name: "parent-dag", ID: "root-999"}

		// Try to get a non-existent sub-attempt
		_, err := h.getOrOpenSubAttempt(ctx, rootRef, "non-existent")
		assert.Error(t, err)
	})
}
