// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/cmn/backoff"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/proto/convert"
	"github.com/dagucloud/dagu/internal/runtime/remote"
	"github.com/dagucloud/dagu/internal/runtime/transform"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	"github.com/dagucloud/dagu/internal/test"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

var _ TaskHandler = (*remoteTaskHandler)(nil)

func TestSanitizeTaskLoadError(t *testing.T) {
	t.Parallel()

	t.Run("worker temp path is removed", func(t *testing.T) {
		t.Parallel()

		err := fmt.Errorf("failed to load DAG from /tmp/dagu-worker/task.yaml: parameter validation failed: region is required")
		assert.Equal(
			t,
			`failed to load DAG "child-dag": parameter validation failed: region is required`,
			sanitizeTaskLoadError("child-dag", err),
		)
	})

	t.Run("non loader error is preserved", func(t *testing.T) {
		t.Parallel()

		err := fmt.Errorf("plain error")
		assert.Equal(t, "plain error", sanitizeTaskLoadError("child-dag", err))
	})
}

func TestTaskOwner(t *testing.T) {
	t.Parallel()

	t.Run("RejectsPartialMetadata", func(t *testing.T) {
		t.Parallel()

		owner, err := taskOwner(&coordinatorv1.Task{
			OwnerCoordinatorId: "coord-1",
		})

		require.Error(t, err)
		assert.Equal(t, exec.HostInfo{}, owner)
	})

	t.Run("AcceptsCompleteMetadata", func(t *testing.T) {
		t.Parallel()

		owner, err := taskOwner(&coordinatorv1.Task{
			OwnerCoordinatorId:   "coord-1",
			OwnerCoordinatorHost: "127.0.0.1",
			OwnerCoordinatorPort: 4321,
		})

		require.NoError(t, err)
		assert.Equal(t, exec.HostInfo{ID: "coord-1", Host: "127.0.0.1", Port: 4321}, owner)
	})
}

func TestPollerAckTaskClaimRejectsPartialOwnerMetadata(t *testing.T) {
	t.Parallel()

	called := false
	client := newMockRemoteCoordinatorClient()
	client.AckTaskClaimFunc = func(context.Context, exec.HostInfo, *coordinatorv1.AckTaskClaimRequest) (*coordinatorv1.AckTaskClaimResponse, error) {
		called = true
		return &coordinatorv1.AckTaskClaimResponse{Accepted: true}, nil
	}

	poller := NewPoller("worker-1", client, nil, 0, nil)
	err := poller.ackTaskClaim(context.Background(), &coordinatorv1.Task{
		ClaimToken:           "claim-1",
		OwnerCoordinatorHost: "127.0.0.1",
		OwnerCoordinatorPort: 4321,
		OwnerCoordinatorId:   "",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "incomplete owner coordinator metadata")
	assert.False(t, called)
}

type mockStreamLogsClient struct {
	chunks   []*coordinatorv1.LogChunk
	mu       sync.Mutex
	sendErr  error
	closeErr error
	response *coordinatorv1.StreamLogsResponse
	ctx      context.Context
}

func newMockStreamLogsClient() *mockStreamLogsClient {
	return &mockStreamLogsClient{
		chunks: make([]*coordinatorv1.LogChunk, 0),
		ctx:    context.Background(),
		response: &coordinatorv1.StreamLogsResponse{
			ChunksReceived: 0,
			BytesWritten:   0,
		},
	}
}

func (m *mockStreamLogsClient) Send(chunk *coordinatorv1.LogChunk) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sendErr != nil {
		return m.sendErr
	}
	m.chunks = append(m.chunks, chunk)
	return nil
}

func (m *mockStreamLogsClient) CloseAndRecv() (*coordinatorv1.StreamLogsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closeErr != nil {
		return nil, m.closeErr
	}
	if m.response != nil {
		m.response.ChunksReceived = uint64(len(m.chunks))
	}
	return m.response, nil
}

func (m *mockStreamLogsClient) Header() (metadata.MD, error) {
	return nil, nil
}

func (m *mockStreamLogsClient) Trailer() metadata.MD {
	return nil
}

func (m *mockStreamLogsClient) CloseSend() error {
	return nil
}

func (m *mockStreamLogsClient) Context() context.Context {
	return m.ctx
}

func (m *mockStreamLogsClient) SendMsg(any) error {
	return nil
}

func (m *mockStreamLogsClient) RecvMsg(any) error {
	return nil
}

type mockStreamArtifactsClient struct {
	chunks   []*coordinatorv1.ArtifactChunk
	mu       sync.Mutex
	sendErr  error
	closeErr error
	response *coordinatorv1.StreamArtifactsResponse
	ctx      context.Context
}

func newMockStreamArtifactsClient() *mockStreamArtifactsClient {
	return &mockStreamArtifactsClient{
		chunks: make([]*coordinatorv1.ArtifactChunk, 0),
		ctx:    context.Background(),
		response: &coordinatorv1.StreamArtifactsResponse{
			ChunksReceived: 0,
			BytesWritten:   0,
		},
	}
}

func (m *mockStreamArtifactsClient) Send(chunk *coordinatorv1.ArtifactChunk) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sendErr != nil {
		return m.sendErr
	}
	m.chunks = append(m.chunks, chunk)
	return nil
}

func (m *mockStreamArtifactsClient) CloseAndRecv() (*coordinatorv1.StreamArtifactsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closeErr != nil {
		return nil, m.closeErr
	}
	if m.response != nil {
		m.response.ChunksReceived = uint64(len(m.chunks))
	}
	return m.response, nil
}

func (m *mockStreamArtifactsClient) Header() (metadata.MD, error) {
	return nil, nil
}

func (m *mockStreamArtifactsClient) Trailer() metadata.MD {
	return nil
}

func (m *mockStreamArtifactsClient) CloseSend() error {
	return nil
}

func (m *mockStreamArtifactsClient) Context() context.Context {
	return m.ctx
}

func artifactUploadTestDAGContent(name, stepName string, fail bool) string {
	if runtime.GOOS == "windows" {
		if fail {
			return fmt.Sprintf(`name: %s
artifacts:
  enabled: true
steps:
  - name: %s
    shell: powershell
    command: |
      if (-not $env:DAG_RUN_ARTIFACTS_DIR) { throw 'DAG_RUN_ARTIFACTS_DIR not set' }
      New-Item -ItemType Directory -Path $env:DAG_RUN_ARTIFACTS_DIR -Force | Out-Null
      [System.IO.File]::WriteAllText((Join-Path $env:DAG_RUN_ARTIFACTS_DIR 'out.txt'), 'artifact')
      exit 1
`, name, stepName)
		}

		return fmt.Sprintf(`name: %s
artifacts:
  enabled: true
steps:
  - name: %s
    shell: powershell
    command: |
      if (-not $env:DAG_RUN_ARTIFACTS_DIR) { throw 'DAG_RUN_ARTIFACTS_DIR not set' }
      New-Item -ItemType Directory -Path $env:DAG_RUN_ARTIFACTS_DIR -Force | Out-Null
      [System.IO.File]::WriteAllText((Join-Path $env:DAG_RUN_ARTIFACTS_DIR 'out.txt'), 'artifact')
`, name, stepName)
	}

	if fail {
		return fmt.Sprintf(`name: %s
artifacts:
  enabled: true
steps:
  - name: %s
    shell: /bin/sh
    command: |
      printf "artifact" > "$DAG_RUN_ARTIFACTS_DIR/out.txt"
      exit 1
`, name, stepName)
	}

	return fmt.Sprintf(`name: %s
artifacts:
  enabled: true
steps:
  - name: %s
    shell: /bin/sh
    command: |
      printf "artifact" > "$DAG_RUN_ARTIFACTS_DIR/out.txt"
`, name, stepName)
}

func (m *mockStreamArtifactsClient) SendMsg(any) error {
	return nil
}

func (m *mockStreamArtifactsClient) RecvMsg(any) error {
	return nil
}

type mockRemoteCoordinatorClient struct {
	AckTaskClaimFunc      func(ctx context.Context, owner exec.HostInfo, req *coordinatorv1.AckTaskClaimRequest) (*coordinatorv1.AckTaskClaimResponse, error)
	RunHeartbeatFunc      func(ctx context.Context, owner exec.HostInfo, req *coordinatorv1.RunHeartbeatRequest) (*coordinatorv1.RunHeartbeatResponse, error)
	ReportStatusFunc      func(ctx context.Context, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error)
	ReportStatusToFunc    func(ctx context.Context, owner exec.HostInfo, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error)
	StreamLogsFunc        func(ctx context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error)
	StreamLogsToFunc      func(ctx context.Context, owner exec.HostInfo) (coordinatorv1.CoordinatorService_StreamLogsClient, error)
	StreamArtifactsFunc   func(ctx context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error)
	StreamArtifactsToFunc func(ctx context.Context, owner exec.HostInfo) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error)
	GetDAGRunStatusFunc   func(ctx context.Context, dagName, dagRunID string, rootRef *exec.DAGRunRef) (*coordinatorv1.GetDAGRunStatusResponse, error)
	DispatchFunc          func(ctx context.Context, task *coordinatorv1.Task) error
	PollFunc              func(ctx context.Context, policy backoff.RetryPolicy, req *coordinatorv1.PollRequest) (*coordinatorv1.Task, error)
	HeartbeatFunc         func(ctx context.Context, req *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error)
	GetWorkersFunc        func(ctx context.Context) ([]*coordinatorv1.WorkerInfo, error)
	CleanupFunc           func(ctx context.Context) error
	MetricsFunc           func() coordinator.Metrics
	RequestCancelFunc     func(ctx context.Context, dagName, dagRunID string, rootRef *exec.DAGRunRef) error
}

func newMockRemoteCoordinatorClient() *mockRemoteCoordinatorClient {
	return &mockRemoteCoordinatorClient{
		ReportStatusFunc: func(_ context.Context, _ *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
			return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
		},
		StreamLogsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
			return newMockStreamLogsClient(), nil
		},
		StreamArtifactsFunc: func(_ context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
			return newMockStreamArtifactsClient(), nil
		},
		GetDAGRunStatusFunc: func(_ context.Context, _, _ string, _ *exec.DAGRunRef) (*coordinatorv1.GetDAGRunStatusResponse, error) {
			return &coordinatorv1.GetDAGRunStatusResponse{Found: false}, nil
		},
		MetricsFunc: func() coordinator.Metrics {
			return coordinator.Metrics{IsConnected: true}
		},
	}
}

func (m *mockRemoteCoordinatorClient) ReportStatus(ctx context.Context, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
	if m.ReportStatusFunc != nil {
		return m.ReportStatusFunc(ctx, req)
	}
	return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
}

func (m *mockRemoteCoordinatorClient) AckTaskClaimTo(ctx context.Context, owner exec.HostInfo, req *coordinatorv1.AckTaskClaimRequest) (*coordinatorv1.AckTaskClaimResponse, error) {
	if m.AckTaskClaimFunc != nil {
		return m.AckTaskClaimFunc(ctx, owner, req)
	}
	return &coordinatorv1.AckTaskClaimResponse{Accepted: true}, nil
}

func (m *mockRemoteCoordinatorClient) RunHeartbeatTo(ctx context.Context, owner exec.HostInfo, req *coordinatorv1.RunHeartbeatRequest) (*coordinatorv1.RunHeartbeatResponse, error) {
	if m.RunHeartbeatFunc != nil {
		return m.RunHeartbeatFunc(ctx, owner, req)
	}
	return &coordinatorv1.RunHeartbeatResponse{}, nil
}

func (m *mockRemoteCoordinatorClient) ReportStatusTo(ctx context.Context, owner exec.HostInfo, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
	if m.ReportStatusToFunc != nil {
		return m.ReportStatusToFunc(ctx, owner, req)
	}
	return m.ReportStatus(ctx, req)
}

func (m *mockRemoteCoordinatorClient) StreamLogs(ctx context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
	if m.StreamLogsFunc != nil {
		return m.StreamLogsFunc(ctx)
	}
	return newMockStreamLogsClient(), nil
}

func (m *mockRemoteCoordinatorClient) StreamLogsTo(ctx context.Context, owner exec.HostInfo) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
	if m.StreamLogsToFunc != nil {
		return m.StreamLogsToFunc(ctx, owner)
	}
	return m.StreamLogs(ctx)
}

func (m *mockRemoteCoordinatorClient) StreamArtifacts(ctx context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
	if m.StreamArtifactsFunc != nil {
		return m.StreamArtifactsFunc(ctx)
	}
	return newMockStreamArtifactsClient(), nil
}

func (m *mockRemoteCoordinatorClient) StreamArtifactsTo(ctx context.Context, owner exec.HostInfo) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
	if m.StreamArtifactsToFunc != nil {
		return m.StreamArtifactsToFunc(ctx, owner)
	}
	return m.StreamArtifacts(ctx)
}

func (m *mockRemoteCoordinatorClient) GetDAGRunStatus(ctx context.Context, dagName, dagRunID string, rootRef *exec.DAGRunRef) (*coordinatorv1.GetDAGRunStatusResponse, error) {
	if m.GetDAGRunStatusFunc != nil {
		return m.GetDAGRunStatusFunc(ctx, dagName, dagRunID, rootRef)
	}
	return &coordinatorv1.GetDAGRunStatusResponse{Found: false}, nil
}

func (m *mockRemoteCoordinatorClient) Dispatch(ctx context.Context, task *coordinatorv1.Task) error {
	if m.DispatchFunc != nil {
		return m.DispatchFunc(ctx, task)
	}
	return nil
}

func (m *mockRemoteCoordinatorClient) Poll(ctx context.Context, policy backoff.RetryPolicy, req *coordinatorv1.PollRequest) (*coordinatorv1.Task, error) {
	if m.PollFunc != nil {
		return m.PollFunc(ctx, policy, req)
	}
	return nil, nil
}

func (m *mockRemoteCoordinatorClient) Heartbeat(ctx context.Context, req *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error) {
	if m.HeartbeatFunc != nil {
		return m.HeartbeatFunc(ctx, req)
	}
	return &coordinatorv1.HeartbeatResponse{}, nil
}

func (m *mockRemoteCoordinatorClient) GetWorkers(ctx context.Context) ([]*coordinatorv1.WorkerInfo, error) {
	if m.GetWorkersFunc != nil {
		return m.GetWorkersFunc(ctx)
	}
	return nil, nil
}

func (m *mockRemoteCoordinatorClient) Cleanup(ctx context.Context) error {
	if m.CleanupFunc != nil {
		return m.CleanupFunc(ctx)
	}
	return nil
}

func (m *mockRemoteCoordinatorClient) Metrics() coordinator.Metrics {
	if m.MetricsFunc != nil {
		return m.MetricsFunc()
	}
	return coordinator.Metrics{IsConnected: true}
}

func (m *mockRemoteCoordinatorClient) RequestCancel(ctx context.Context, dagName, dagRunID string, rootRef *exec.DAGRunRef) error {
	if m.RequestCancelFunc != nil {
		return m.RequestCancelFunc(ctx, dagName, dagRunID, rootRef)
	}
	return nil
}

// mockRemoteDAGRunAttempt implements execution.DAGRunAttempt for testing
type mockRemoteDAGRunAttempt struct {
	id       string
	status   *exec.DAGRunStatus
	dag      *core.DAG
	readErr  error
	openErr  error
	writeErr error
	closeErr error
	opened   bool
	closed   bool
	written  bool
	aborting bool
	hidden   bool
	mu       sync.Mutex
}

func (m *mockRemoteDAGRunAttempt) ID() string {
	return m.id
}

func (m *mockRemoteDAGRunAttempt) Open(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.openErr != nil {
		return m.openErr
	}
	m.opened = true
	return nil
}

func (m *mockRemoteDAGRunAttempt) Write(_ context.Context, status exec.DAGRunStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.writeErr != nil {
		return m.writeErr
	}
	m.written = true
	m.status = &status
	return nil
}

func (m *mockRemoteDAGRunAttempt) Close(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closeErr != nil {
		return m.closeErr
	}
	m.closed = true
	return nil
}

func (m *mockRemoteDAGRunAttempt) ReadStatus(_ context.Context) (*exec.DAGRunStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.readErr != nil {
		return nil, m.readErr
	}
	return m.status, nil
}

func (m *mockRemoteDAGRunAttempt) ReadDAG(_ context.Context) (*core.DAG, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dag, nil
}

func (m *mockRemoteDAGRunAttempt) SetDAG(dag *core.DAG) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dag = dag
}

func (m *mockRemoteDAGRunAttempt) Abort(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.aborting = true
	return nil
}

func (m *mockRemoteDAGRunAttempt) IsAborting(_ context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.aborting, nil
}

func (m *mockRemoteDAGRunAttempt) Hide(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hidden = true
	return nil
}

func (m *mockRemoteDAGRunAttempt) Hidden() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hidden
}

func (m *mockRemoteDAGRunAttempt) WriteOutputs(_ context.Context, _ *exec.DAGRunOutputs) error {
	return nil
}

func (m *mockRemoteDAGRunAttempt) ReadOutputs(_ context.Context) (*exec.DAGRunOutputs, error) {
	return nil, nil
}

func (m *mockRemoteDAGRunAttempt) WriteStepMessages(_ context.Context, _ string, _ []exec.LLMMessage) error {
	return nil
}

func (m *mockRemoteDAGRunAttempt) ReadStepMessages(_ context.Context, _ string) ([]exec.LLMMessage, error) {
	return nil, nil
}

func (m *mockRemoteDAGRunAttempt) WorkDir() string {
	return ""
}

// mockRemoteDAGRunStore implements execution.DAGRunStore for testing
type mockRemoteDAGRunStore struct {
	attempts    map[string]exec.DAGRunAttempt
	subAttempts map[string]exec.DAGRunAttempt
	findErr     error
	createErr   error
	mu          sync.Mutex
}

func newMockRemoteDAGRunStore() *mockRemoteDAGRunStore {
	return &mockRemoteDAGRunStore{
		attempts:    make(map[string]exec.DAGRunAttempt),
		subAttempts: make(map[string]exec.DAGRunAttempt),
	}
}

func (m *mockRemoteDAGRunStore) SetAttempt(ref exec.DAGRunRef, attempt exec.DAGRunAttempt) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attempts[ref.ID] = attempt
}

func (m *mockRemoteDAGRunStore) SetSubAttempt(rootRef exec.DAGRunRef, subID string, attempt exec.DAGRunAttempt) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", rootRef.ID, subID)
	m.subAttempts[key] = attempt
}

func (m *mockRemoteDAGRunStore) FindAttempt(_ context.Context, ref exec.DAGRunRef) (exec.DAGRunAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.findErr != nil {
		return nil, m.findErr
	}
	attempt, ok := m.attempts[ref.ID]
	if !ok {
		return nil, exec.ErrDAGRunIDNotFound
	}
	return attempt, nil
}

func (m *mockRemoteDAGRunStore) FindSubAttempt(_ context.Context, rootRef exec.DAGRunRef, subDAGRunID string) (exec.DAGRunAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.findErr != nil {
		return nil, m.findErr
	}
	key := fmt.Sprintf("%s:%s", rootRef.ID, subDAGRunID)
	attempt, ok := m.subAttempts[key]
	if !ok {
		return nil, exec.ErrDAGRunIDNotFound
	}
	return attempt, nil
}

func (m *mockRemoteDAGRunStore) CreateSubAttempt(_ context.Context, rootRef exec.DAGRunRef, subDAGRunID string) (exec.DAGRunAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createErr != nil {
		return nil, m.createErr
	}
	key := fmt.Sprintf("%s:%s", rootRef.ID, subDAGRunID)
	attempt := &mockRemoteDAGRunAttempt{
		id:     subDAGRunID,
		status: &exec.DAGRunStatus{},
	}
	m.subAttempts[key] = attempt
	return attempt, nil
}

func (m *mockRemoteDAGRunStore) CreateAttempt(_ context.Context, _ *core.DAG, _ time.Time, _ string, _ exec.NewDAGRunAttemptOptions) (exec.DAGRunAttempt, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	panic("CreateAttempt not implemented in mock")
}

func (m *mockRemoteDAGRunStore) RecentAttempts(_ context.Context, _ string, _ int) []exec.DAGRunAttempt {
	return nil
}

func (m *mockRemoteDAGRunStore) LatestAttempt(_ context.Context, _ string) (exec.DAGRunAttempt, error) {
	return nil, exec.ErrDAGRunIDNotFound
}

func (m *mockRemoteDAGRunStore) ListStatuses(_ context.Context, _ ...exec.ListDAGRunStatusesOption) ([]*exec.DAGRunStatus, error) {
	return nil, nil
}

func (m *mockRemoteDAGRunStore) ListStatusesPage(_ context.Context, _ ...exec.ListDAGRunStatusesOption) (exec.DAGRunStatusPage, error) {
	return exec.DAGRunStatusPage{}, nil
}

func (m *mockRemoteDAGRunStore) CompareAndSwapLatestAttemptStatus(
	_ context.Context,
	_ exec.DAGRunRef,
	_ string,
	_ core.Status,
	_ func(*exec.DAGRunStatus) error,
) (*exec.DAGRunStatus, bool, error) {
	return nil, false, nil
}

func (m *mockRemoteDAGRunStore) CompareAndSwapAttemptStatus(
	_ context.Context,
	_ exec.DAGRunAttemptRef,
	_ core.Status,
	_ func(*exec.DAGRunStatus) error,
) (*exec.DAGRunStatus, bool, error) {
	return nil, false, nil
}

func (m *mockRemoteDAGRunStore) RemoveOldDAGRuns(_ context.Context, _ string, _ int, _ ...exec.RemoveOldDAGRunsOption) ([]string, error) {
	return nil, nil
}

func (m *mockRemoteDAGRunStore) RenameDAGRuns(_ context.Context, _, _ string) error {
	return nil
}

func (m *mockRemoteDAGRunStore) RemoveDAGRun(_ context.Context, _ exec.DAGRunRef, _ ...exec.RemoveDAGRunOption) error {
	return nil
}

func TestNewRemoteTaskHandler(t *testing.T) {
	t.Parallel()

	t.Run("AllFieldsSet", func(t *testing.T) {
		t.Parallel()

		client := newMockRemoteCoordinatorClient()
		store := newMockRemoteDAGRunStore()
		cfg := &config.Config{}

		handler := NewRemoteTaskHandler(RemoteTaskHandlerConfig{
			WorkerID:          "worker-1",
			CoordinatorClient: client,
			DAGRunStore:       store,
			Config:            cfg,
		})

		require.NotNil(t, handler)

		// Verify it's the correct type
		rh, ok := handler.(*remoteTaskHandler)
		require.True(t, ok, "should return *remoteTaskHandler")
		assert.Equal(t, "worker-1", rh.workerID)
		assert.Equal(t, client, rh.coordinatorClient)
		assert.Equal(t, store, rh.dagRunStore)
		assert.Equal(t, cfg, rh.config)
	})

	t.Run("NilOptionalFields", func(t *testing.T) {
		t.Parallel()

		client := newMockRemoteCoordinatorClient()

		handler := NewRemoteTaskHandler(RemoteTaskHandlerConfig{
			WorkerID:          "worker-2",
			CoordinatorClient: client,
			// DAGRunStore is nil - valid for fully remote mode
			// DAGStore is nil
			// ServiceRegistry is nil
		})

		require.NotNil(t, handler)

		rh, ok := handler.(*remoteTaskHandler)
		require.True(t, ok)
		assert.Nil(t, rh.dagRunStore)
		assert.Nil(t, rh.dagStore)
		assert.Nil(t, rh.serviceRegistry)
	})
}

func TestCreateRemoteHandlers(t *testing.T) {
	t.Parallel()

	t.Run("CreatesStatusPusher", func(t *testing.T) {
		t.Parallel()

		client := newMockRemoteCoordinatorClient()
		handler := &remoteTaskHandler{
			workerID:          "test-worker",
			coordinatorClient: client,
		}

		root := exec.DAGRunRef{Name: "root-dag", ID: "root-123"}
		statusPusher, _, _ := handler.createRemoteHandlers("run-1", "test-dag", root)

		require.NotNil(t, statusPusher)
	})

	t.Run("CreatesLogStreamer", func(t *testing.T) {
		t.Parallel()

		client := newMockRemoteCoordinatorClient()
		handler := &remoteTaskHandler{
			workerID:          "test-worker",
			coordinatorClient: client,
		}

		root := exec.DAGRunRef{Name: "root-dag", ID: "root-123"}
		_, logStreamer, _ := handler.createRemoteHandlers("run-1", "test-dag", root)

		require.NotNil(t, logStreamer)
	})

	t.Run("PassesCorrectParameters", func(t *testing.T) {
		t.Parallel()

		client := newMockRemoteCoordinatorClient()
		handler := &remoteTaskHandler{
			workerID:          "worker-abc",
			coordinatorClient: client,
		}

		root := exec.DAGRunRef{Name: "my-root", ID: "root-xyz"}
		statusPusher, logStreamer, artifactUploader := handler.createRemoteHandlers("my-run-id", "my-dag", root)

		// Both should be created
		require.NotNil(t, statusPusher)
		require.NotNil(t, logStreamer)
		require.NotNil(t, artifactUploader)
	})
}

func TestAgentStoresFromSnapshot_HydratesSnapshotStores(t *testing.T) {
	t.Parallel()

	handler := &remoteTaskHandler{}
	payload, err := agent.MarshalSnapshot(&agent.Snapshot{
		Config: &agent.Config{
			Enabled:        true,
			DefaultModelID: "model-default",
		},
		Models: []*agent.ModelConfig{
			{
				ID:       "model-default",
				Name:     "Default",
				Provider: "openai",
				Model:    "gpt-5.4",
				APIKey:   "test-key",
			},
		},
		Souls: []*agent.Soul{
			{ID: "helper", Name: "Helper", Content: "be precise"},
		},
		Memory: &agent.MemorySnapshot{
			Global: "global memory",
			PerDAG: map[string]string{"snapshot-dag": "dag memory"},
		},
	})
	require.NoError(t, err)

	stores, err := handler.agentStoresFromSnapshot(payload)
	require.NoError(t, err)
	require.NotNil(t, stores.configStore)
	require.NotNil(t, stores.modelStore)
	require.NotNil(t, stores.soulStore)
	require.NotNil(t, stores.memoryStore)
	assert.Nil(t, stores.oauthManager)

	cfg, err := stores.configStore.Load(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "model-default", cfg.DefaultModelID)
	model, err := stores.modelStore.GetByID(context.Background(), "model-default")
	require.NoError(t, err)
	assert.Equal(t, "gpt-5.4", model.Model)
	soul, err := stores.soulStore.GetByID(context.Background(), "helper")
	require.NoError(t, err)
	assert.Equal(t, "Helper", soul.Name)
	globalMemory, err := stores.memoryStore.LoadGlobalMemory(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "global memory", globalMemory)
}

func TestHandleStart_InvalidSnapshotReportsInitFailure(t *testing.T) {
	t.Parallel()

	var reported *coordinatorv1.ReportStatusRequest
	client := newMockRemoteCoordinatorClient()
	client.ReportStatusFunc = func(_ context.Context, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
		reported = req
		return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
	}

	handler := NewRemoteTaskHandler(RemoteTaskHandlerConfig{
		WorkerID:          "worker-1",
		CoordinatorClient: client,
		Config:            &config.Config{},
	})

	task := &coordinatorv1.Task{
		Operation:      coordinatorv1.Operation_OPERATION_START,
		Target:         "snapshot-dag",
		RootDagRunName: "snapshot-dag",
		RootDagRunId:   "run-1",
		DagRunId:       "run-1",
		Definition: `
steps:
  - name: main
    command: echo hello
`,
		AgentSnapshot: []byte("not-a-valid-snapshot"),
	}

	err := handler.Handle(context.Background(), task)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hydrate agent snapshot")
	require.NotNil(t, reported)
	require.NotNil(t, reported.Status)

	status, convErr := convert.ProtoToDAGRunStatus(reported.Status)
	require.NoError(t, convErr)
	assert.Equal(t, core.Failed, status.Status)
	assert.Equal(t, "snapshot-dag", status.Name)
	assert.Equal(t, "run-1", status.DAGRunID)
	assert.Contains(t, status.Error, "hydrate agent snapshot")
}

func TestCreateAgentEnv(t *testing.T) {
	t.Parallel()

	t.Run("CreatesLogDirectory", func(t *testing.T) {
		t.Parallel()

		handler := &remoteTaskHandler{
			workerID: "test-worker-env",
		}

		ctx := context.Background()
		env, err := handler.createAgentEnv(ctx, nil, "test-run-123")

		require.NoError(t, err)
		require.NotNil(t, env)
		defer env.cleanup()

		// Verify directory exists
		info, statErr := os.Stat(env.logDir)
		require.NoError(t, statErr)
		require.True(t, info.IsDir())
	})

	t.Run("PathIncludesWorkerID", func(t *testing.T) {
		t.Parallel()

		handler := &remoteTaskHandler{
			workerID: "unique-worker-id",
		}

		ctx := context.Background()
		env, err := handler.createAgentEnv(ctx, nil, "run-456")

		require.NoError(t, err)
		defer env.cleanup()

		// Path should include workerID
		require.Contains(t, env.logDir, "unique-worker-id")
		require.Contains(t, env.logDir, "run-456")
	})

	t.Run("PathIncludesDagRunID", func(t *testing.T) {
		t.Parallel()

		handler := &remoteTaskHandler{
			workerID: "worker-x",
		}

		ctx := context.Background()
		env, err := handler.createAgentEnv(ctx, nil, "specific-run-id")

		require.NoError(t, err)
		defer env.cleanup()

		require.Contains(t, env.logDir, "specific-run-id")
	})

	t.Run("SetsLogFilePath", func(t *testing.T) {
		t.Parallel()

		handler := &remoteTaskHandler{
			workerID: "worker-logfile",
		}

		ctx := context.Background()
		env, err := handler.createAgentEnv(ctx, nil, "run-log")

		require.NoError(t, err)
		defer env.cleanup()

		// logFile should be within logDir
		require.Contains(t, env.logFile, env.logDir)
		require.Contains(t, env.logFile, "scheduler.log")
	})

	t.Run("CleanupRemovesDirectory", func(t *testing.T) {
		t.Parallel()

		handler := &remoteTaskHandler{
			workerID: "worker-cleanup",
		}

		ctx := context.Background()
		env, err := handler.createAgentEnv(ctx, nil, "run-cleanup")

		require.NoError(t, err)

		// Verify directory exists
		logDir := env.logDir
		_, statErr := os.Stat(logDir)
		require.NoError(t, statErr)

		// Call cleanup
		env.cleanup()

		// Verify directory is removed
		_, statErr = os.Stat(logDir)
		require.True(t, os.IsNotExist(statErr), "directory should be removed after cleanup")
	})

	t.Run("CleanupHandlesNonExistentDirectory", func(t *testing.T) {
		t.Parallel()

		handler := &remoteTaskHandler{
			workerID: "worker-nonexist",
		}

		ctx := context.Background()
		env, err := handler.createAgentEnv(ctx, nil, "run-nonexist")

		require.NoError(t, err)

		// Remove directory manually first
		_ = os.RemoveAll(env.logDir)

		// Cleanup should not panic
		require.NotPanics(t, func() {
			env.cleanup()
		})
	})

	t.Run("CreatesNestedDirectoryStructure", func(t *testing.T) {
		t.Parallel()

		handler := &remoteTaskHandler{
			workerID: "worker-nested",
		}

		ctx := context.Background()
		env, err := handler.createAgentEnv(ctx, nil, "run-nested")

		require.NoError(t, err)
		defer env.cleanup()

		// Should contain the expected path structure
		expectedPath := filepath.Join(os.TempDir(), "dagu", "worker-logs", "worker-nested", "run-nested")
		assert.Equal(t, expectedPath, env.logDir)
	})
}

func TestLoadDAG(t *testing.T) {
	t.Parallel()

	t.Run("FromDefinition", func(t *testing.T) {
		t.Parallel()

		handler := &remoteTaskHandler{
			config: &config.Config{
				Paths: config.PathsConfig{
					DAGsDir: t.TempDir(),
				},
			},
		}

		dagDefinition := `name: inline-dag
steps:
  - name: inline-step
    command: echo inline
`

		task := &coordinatorv1.Task{
			Target:     "inline-dag", // Target is the DAG name, not filename
			Definition: dagDefinition,
		}

		dag, cleanup, err := handler.loadDAG(context.Background(), task)

		require.NoError(t, err)
		require.NotNil(t, dag)
		assert.Equal(t, "inline-dag", dag.Name) // Name comes from task.Target when Definition is provided
		require.NotNil(t, cleanup, "cleanup should be set for inline definitions")

		// Call cleanup to remove temp file
		cleanup()
	})

	t.Run("CleanupOnSpecLoadError", func(t *testing.T) {
		t.Parallel()

		handler := &remoteTaskHandler{
			config: &config.Config{
				Paths: config.PathsConfig{
					DAGsDir: t.TempDir(),
				},
			},
		}

		// Invalid YAML that will fail to parse
		invalidDefinition := `invalid: yaml: content: [[[`

		task := &coordinatorv1.Task{
			Target:     "invalid.yaml",
			Definition: invalidDefinition,
		}

		dag, cleanup, err := handler.loadDAG(context.Background(), task)

		require.Error(t, err)
		require.Nil(t, dag)
		assert.Nil(t, cleanup, "cleanup should be nil after error (already cleaned up)")
		require.Contains(t, err.Error(), "failed to load DAG")
	})
}

func TestHandle(t *testing.T) {
	t.Parallel()

	t.Run("UnsupportedOperation", func(t *testing.T) {
		t.Parallel()

		handler := &remoteTaskHandler{
			workerID:          "test-worker",
			coordinatorClient: newMockRemoteCoordinatorClient(),
			config:            &config.Config{},
		}

		task := &coordinatorv1.Task{
			Operation: coordinatorv1.Operation_OPERATION_UNSPECIFIED,
		}

		err := handler.Handle(context.Background(), task)

		require.Error(t, err)
		require.Contains(t, err.Error(), "unsupported operation")
	})

	t.Run("UnknownOperationValue", func(t *testing.T) {
		t.Parallel()

		handler := &remoteTaskHandler{
			workerID:          "test-worker",
			coordinatorClient: newMockRemoteCoordinatorClient(),
			config:            &config.Config{},
		}

		task := &coordinatorv1.Task{
			Operation: coordinatorv1.Operation(999), // Unknown value
		}

		err := handler.Handle(context.Background(), task)

		require.Error(t, err)
		require.Contains(t, err.Error(), "unsupported operation")
	})
}

func TestHandleRetry(t *testing.T) {
	t.Parallel()

	t.Run("NoStatusSource", func(t *testing.T) {
		t.Parallel()

		handler := &remoteTaskHandler{
			workerID:          "test-worker",
			coordinatorClient: newMockRemoteCoordinatorClient(),
			config:            &config.Config{},
		}

		task := &coordinatorv1.Task{
			Operation:      coordinatorv1.Operation_OPERATION_RETRY,
			Step:           "step1",
			PreviousStatus: nil, // Missing - should error
			RootDagRunName: "root",
			RootDagRunId:   "root-123",
			DagRunId:       "run-123",
		}

		err := handler.handleRetry(context.Background(), task)

		require.Error(t, err)
		require.Contains(t, err.Error(), "retry requires previous_status in task for shared-nothing mode")
	})

	t.Run("QueuedCatchupPreservesTriggerType", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `name: remote-catchup-dag
steps:
  - name: step1
    command: echo remote catchup
`)

		runID := "remote-catchup-run"
		status := transform.NewStatusBuilder(dag.DAG).Create(
			runID,
			core.Queued,
			0,
			time.Time{},
			transform.WithAttemptID("queued-attempt"),
			transform.WithTriggerType(core.TriggerTypeCatchUp),
			transform.WithQueuedAt(stringutil.FormatTime(time.Now())),
			transform.WithScheduleTime(stringutil.FormatTime(time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC))),
		)

		previousStatus, convErr := convert.DAGRunStatusToProto(&status)
		require.NoError(t, convErr)

		var (
			mu       sync.Mutex
			reported []*exec.DAGRunStatus
		)
		client := newMockRemoteCoordinatorClient()
		client.ReportStatusFunc = func(_ context.Context, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
			got, err := convert.ProtoToDAGRunStatus(req.Status)
			require.NoError(t, err)
			mu.Lock()
			reported = append(reported, got)
			mu.Unlock()
			return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
		}

		handler := &remoteTaskHandler{
			workerID:          "test-worker",
			coordinatorClient: client,
			dagRunStore:       nil,
			dagStore:          th.DAGStore,
			dagRunMgr:         th.DAGRunMgr,
			serviceRegistry:   th.ServiceRegistry,
			config:            th.Config,
		}

		task := &coordinatorv1.Task{
			Operation:      coordinatorv1.Operation_OPERATION_RETRY,
			Target:         dag.Name,
			Definition:     string(dag.YamlData),
			PreviousStatus: previousStatus,
			RootDagRunName: dag.Name,
			RootDagRunId:   runID,
			DagRunId:       runID,
		}

		err := handler.handleRetry(th.Context, task)
		require.NoError(t, err)

		mu.Lock()
		defer mu.Unlock()
		require.NotEmpty(t, reported)

		final := reported[len(reported)-1]
		require.Equal(t, core.Succeeded, final.Status)
		require.Equal(t, core.TriggerTypeCatchUp, final.TriggerType)
	})

	t.Run("SharedNothingModeWithEmbeddedStatus", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()
		dagFile := filepath.Join(tempDir, "retry.yaml")
		dagContent := `name: retry-dag
steps:
  - name: step1
    command: echo retry
`
		err := os.WriteFile(dagFile, []byte(dagContent), 0644)
		require.NoError(t, err)

		handler := &remoteTaskHandler{
			workerID:          "test-worker",
			coordinatorClient: newMockRemoteCoordinatorClient(),
			dagRunStore:       nil, // No local store - fully shared-nothing
			config: &config.Config{
				Paths: config.PathsConfig{
					DAGsDir: tempDir,
				},
			},
		}

		// Create a previous status proto
		previousStatus, convErr := convert.DAGRunStatusToProto(&exec.DAGRunStatus{
			Name:   "retry-dag",
			Status: core.Succeeded,
			Nodes:  []*exec.Node{},
		})
		require.NoError(t, convErr)

		task := &coordinatorv1.Task{
			Operation:      coordinatorv1.Operation_OPERATION_RETRY,
			Step:           "step1",
			Target:         dagFile,
			PreviousStatus: previousStatus,
			RootDagRunName: "root",
			RootDagRunId:   "root-123",
			DagRunId:       "run-123",
		}

		// This will fail at agent creation since we don't have full dependencies,
		// but it proves the shared-nothing path is taken
		err = handler.handleRetry(context.Background(), task)

		// The error should NOT be about missing status source
		require.Error(t, err)
		require.NotContains(t, err.Error(), "retry requires either previous_status")
	})
}

func TestTaskExtraEnvs(t *testing.T) {
	t.Parallel()

	assert.Nil(t, taskExtraEnvs(nil))
	assert.Nil(t, taskExtraEnvs(&coordinatorv1.Task{}))
	assert.Equal(t, []string{exec.EnvKeyExternalStepRetry + "=1"}, taskExtraEnvs(&coordinatorv1.Task{
		ExternalStepRetry: true,
	}))
}

func TestHandleStart_ExternalStepRetryQueuesPendingRetry(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	dag := th.DAG(t, `name: remote-external-retry
steps:
  - name: flaky
    command: exit 1
    retry_policy:
      limit: 1
      interval_sec: 30
`)

	var (
		mu       sync.Mutex
		reported []*exec.DAGRunStatus
	)
	client := newMockRemoteCoordinatorClient()
	client.ReportStatusFunc = func(_ context.Context, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
		status, err := convert.ProtoToDAGRunStatus(req.Status)
		require.NoError(t, err)
		mu.Lock()
		reported = append(reported, status)
		mu.Unlock()
		return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
	}

	handler := &remoteTaskHandler{
		workerID:          "test-worker",
		coordinatorClient: client,
		dagRunStore:       nil,
		dagStore:          th.DAGStore,
		dagRunMgr:         th.DAGRunMgr,
		serviceRegistry:   th.ServiceRegistry,
		config:            th.Config,
	}

	task := &coordinatorv1.Task{
		Operation:         coordinatorv1.Operation_OPERATION_START,
		Target:            dag.Name,
		Definition:        string(dag.YamlData),
		RootDagRunName:    dag.Name,
		RootDagRunId:      "run-queued",
		DagRunId:          "run-queued",
		ExternalStepRetry: true,
	}

	started := time.Now()
	err := handler.handleStart(th.Context, task, false)
	require.NoError(t, err)
	require.Less(t, time.Since(started), 5*time.Second)
	mu.Lock()
	require.NotEmpty(t, reported)

	final := reported[len(reported)-1]
	mu.Unlock()
	require.Equal(t, core.Queued, final.Status)
	require.Equal(t, []exec.PendingStepRetry{
		{StepName: "flaky", Interval: 30 * time.Second},
	}, final.PendingStepRetries)
}

func TestHandleStart(t *testing.T) {
	t.Parallel()

	t.Run("LoadDAGError", func(t *testing.T) {
		t.Parallel()

		handler := &remoteTaskHandler{
			workerID:          "test-worker",
			coordinatorClient: newMockRemoteCoordinatorClient(),
			config: &config.Config{
				Paths: config.PathsConfig{
					DAGsDir: t.TempDir(),
				},
			},
		}

		// Test with invalid YAML definition
		task := &coordinatorv1.Task{
			Target:     "invalid-dag",
			Definition: `invalid: yaml: content: [[[`,
		}

		err := handler.handleStart(context.Background(), task, false)

		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to load DAG")
	})
}

func TestRemoteHandler_UniqueLogDirs(t *testing.T) {
	t.Parallel()

	// Verify that different dagRunIDs produce unique log directories
	handler := &remoteTaskHandler{
		workerID: "collision-worker",
	}

	ctx := context.Background()

	env1, err1 := handler.createAgentEnv(ctx, nil, "run-aaa")
	require.NoError(t, err1)
	defer env1.cleanup()

	env2, err2 := handler.createAgentEnv(ctx, nil, "run-bbb")
	require.NoError(t, err2)
	defer env2.cleanup()

	assert.NotEqual(t, env1.logDir, env2.logDir, "different dagRunIDs should produce different log directories")
}

func TestHandle_OperationStart(t *testing.T) {
	t.Parallel()

	// This test verifies the OPERATION_START path is taken
	tempDir := t.TempDir()
	dagFile := filepath.Join(tempDir, "start.yaml")
	dagContent := `name: start-dag
steps:
  - name: step1
    command: echo start
`
	err := os.WriteFile(dagFile, []byte(dagContent), 0644)
	require.NoError(t, err)

	handler := &remoteTaskHandler{
		workerID:          "test-worker",
		coordinatorClient: newMockRemoteCoordinatorClient(),
		config: &config.Config{
			Paths: config.PathsConfig{
				DAGsDir: tempDir,
			},
		},
	}

	task := &coordinatorv1.Task{
		Operation:      coordinatorv1.Operation_OPERATION_START,
		Target:         dagFile,
		DagRunId:       "run-start-1",
		RootDagRunName: "root",
		RootDagRunId:   "root-1",
	}

	// This will fail at agent creation (no parent DAG run), but proves the path is taken
	err = handler.Handle(context.Background(), task)
	require.Error(t, err)
	// The error should be from execution, not unsupported operation
	require.NotContains(t, err.Error(), "unsupported operation")
}

func TestHandle_OperationRetryWithoutStatusSource(t *testing.T) {
	t.Parallel()

	// OPERATION_RETRY requires previous_status in the task for shared-nothing mode.
	// All retry callers embed status via WithPreviousStatus().
	handler := &remoteTaskHandler{
		workerID:          "test-worker",
		coordinatorClient: newMockRemoteCoordinatorClient(),
		config:            &config.Config{},
	}

	task := &coordinatorv1.Task{
		Operation:      coordinatorv1.Operation_OPERATION_RETRY,
		Step:           "",
		Target:         "test-dag",
		DagRunId:       "run-retry-1",
		RootDagRunName: "root",
		RootDagRunId:   "root-1",
		PreviousStatus: nil, // Missing - should error
	}

	// Without PreviousStatus, retry should fail with a clear error
	err := handler.Handle(context.Background(), task)
	require.Error(t, err)
	require.Contains(t, err.Error(), "retry requires previous_status in task for shared-nothing mode")
}

func TestHandle_OperationRetryWithStep(t *testing.T) {
	t.Parallel()

	// When OPERATION_RETRY is used with a step, it should call handleRetry
	handler := &remoteTaskHandler{
		workerID:          "test-worker",
		coordinatorClient: newMockRemoteCoordinatorClient(),
		config:            &config.Config{},
	}

	task := &coordinatorv1.Task{
		Operation:      coordinatorv1.Operation_OPERATION_RETRY,
		Step:           "step1", // With step = handleRetry path
		Target:         "test-dag",
		DagRunId:       "run-retry-1",
		RootDagRunName: "root",
		RootDagRunId:   "root-1",
		PreviousStatus: nil, // No embedded status
	}

	err := handler.Handle(context.Background(), task)
	require.Error(t, err)
	// Should fail with "retry requires" error from handleRetry
	require.Contains(t, err.Error(), "retry requires previous_status in task for shared-nothing mode")
}

func TestHandleStart_SuccessPathWithCleanup(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	// Test with inline definition to exercise cleanup path
	handler := &remoteTaskHandler{
		workerID:          "test-worker",
		coordinatorClient: newMockRemoteCoordinatorClient(),
		config: &config.Config{
			Paths: config.PathsConfig{
				DAGsDir: tempDir,
			},
		},
	}

	dagDefinition := `name: cleanup-test-dag
steps:
  - name: step1
    command: echo cleanup
`

	task := &coordinatorv1.Task{
		Target:         "cleanup-test.yaml",
		Definition:     dagDefinition, // Inline definition triggers temp file + cleanup
		DagRunId:       "run-cleanup-1",
		RootDagRunName: "root",
		RootDagRunId:   "root-cleanup-1",
	}

	// Will fail at execution but exercises DAG loading with cleanup
	err := handler.handleStart(context.Background(), task, false)
	require.Error(t, err)
	// Error should be from execution, not DAG loading
	require.NotContains(t, err.Error(), "failed to load DAG")
}

func TestHandleStart_QueuedRunFlag(t *testing.T) {
	t.Parallel()

	dagContent := `name: queued-flag-dag
steps:
  - name: step1
    command: echo queued
`

	handler := &remoteTaskHandler{
		workerID:          "test-worker",
		coordinatorClient: newMockRemoteCoordinatorClient(),
		config: &config.Config{
			Paths: config.PathsConfig{
				DAGsDir: t.TempDir(),
			},
		},
	}

	task := &coordinatorv1.Task{
		Target:         "queued-flag",
		Definition:     dagContent,
		DagRunId:       "run-queued-flag",
		RootDagRunName: "root",
		RootDagRunId:   "root-queued",
	}

	// Test with queuedRun=true
	err := handler.handleStart(context.Background(), task, true)
	require.Error(t, err)
	// Should fail at execution, not DAG loading
	require.NotContains(t, err.Error(), "failed to load DAG")
}

func TestExecuteDAGRun_WithRetryConfig(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	dagFile := filepath.Join(tempDir, "exec-retry.yaml")
	dagContent := `name: exec-retry-dag
steps:
  - name: step1
    command: echo exec
`
	err := os.WriteFile(dagFile, []byte(dagContent), 0644)
	require.NoError(t, err)

	previousStatus, convErr := convert.DAGRunStatusToProto(&exec.DAGRunStatus{
		Name:   "exec-retry-dag",
		Status: core.Succeeded,
		Nodes:  []*exec.Node{},
	})
	require.NoError(t, convErr)

	handler := &remoteTaskHandler{
		workerID:          "test-worker",
		coordinatorClient: newMockRemoteCoordinatorClient(),
		config: &config.Config{
			Paths: config.PathsConfig{
				DAGsDir: tempDir,
			},
		},
	}

	task := &coordinatorv1.Task{
		Operation:      coordinatorv1.Operation_OPERATION_RETRY,
		Step:           "step1",
		Target:         dagFile,
		PreviousStatus: previousStatus,
		RootDagRunName: "root",
		RootDagRunId:   "root-exec",
		DagRunId:       "run-exec",
	}

	// This exercises the retry path through handleRetry
	err = handler.handleRetry(context.Background(), task)
	require.Error(t, err)
	// Error from execution, not status lookup
	require.NotContains(t, err.Error(), "retry requires")
}

func TestRemoteHandler_DifferentWorkersDifferentPaths(t *testing.T) {
	t.Parallel()

	handler1 := &remoteTaskHandler{workerID: "worker-alpha"}
	handler2 := &remoteTaskHandler{workerID: "worker-beta"}

	ctx := context.Background()

	env1, err1 := handler1.createAgentEnv(ctx, nil, "same-run-id")
	require.NoError(t, err1)
	defer env1.cleanup()

	env2, err2 := handler2.createAgentEnv(ctx, nil, "same-run-id")
	require.NoError(t, err2)
	defer env2.cleanup()

	assert.NotEqual(t, env1.logDir, env2.logDir, "different workerIDs should produce different log directories even for same dagRunID")
	assert.Contains(t, env1.logDir, "worker-alpha")
	assert.Contains(t, env2.logDir, "worker-beta")
}

func TestHandleRetry_LoadDAGErrorPath(t *testing.T) {
	t.Parallel()

	// Test the path where handleRetry fails at loadDAG after getting status
	previousStatus, convErr := convert.DAGRunStatusToProto(&exec.DAGRunStatus{
		Name:   "loaddag-error-dag",
		Status: core.Succeeded,
		Nodes:  []*exec.Node{},
	})
	require.NoError(t, convErr)

	handler := &remoteTaskHandler{
		workerID:          "test-worker",
		coordinatorClient: newMockRemoteCoordinatorClient(),
		config: &config.Config{
			Paths: config.PathsConfig{
				DAGsDir: t.TempDir(),
			},
		},
	}

	task := &coordinatorv1.Task{
		Operation:      coordinatorv1.Operation_OPERATION_RETRY,
		Step:           "step1",
		Target:         "/nonexistent/path/dag.yaml", // Will fail to load
		PreviousStatus: previousStatus,               // Has embedded status
		RootDagRunName: "root",
		RootDagRunId:   "root-loaddag",
		DagRunId:       "run-loaddag",
	}

	err := handler.handleRetry(context.Background(), task)

	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to load DAG")
}

func TestHandleRetry_WithDefinitionAndCleanup(t *testing.T) {
	t.Parallel()

	// Test handleRetry with inline definition to trigger cleanup path
	previousStatus, convErr := convert.DAGRunStatusToProto(&exec.DAGRunStatus{
		Name:   "def-cleanup-dag",
		Status: core.Succeeded,
		Nodes:  []*exec.Node{},
	})
	require.NoError(t, convErr)

	handler := &remoteTaskHandler{
		workerID:          "test-worker",
		coordinatorClient: newMockRemoteCoordinatorClient(),
		config: &config.Config{
			Paths: config.PathsConfig{
				DAGsDir: t.TempDir(),
			},
		},
	}

	dagDefinition := `name: def-cleanup-dag
steps:
  - name: step1
    command: echo cleanup
`

	task := &coordinatorv1.Task{
		Operation:      coordinatorv1.Operation_OPERATION_RETRY,
		Step:           "step1",
		Target:         "def-cleanup.yaml",
		Definition:     dagDefinition, // Inline definition triggers cleanup
		PreviousStatus: previousStatus,
		RootDagRunName: "root",
		RootDagRunId:   "root-cleanup",
		DagRunId:       "run-cleanup",
	}

	err := handler.handleRetry(context.Background(), task)

	// Should fail at execution but exercise cleanup path
	require.Error(t, err)
	require.NotContains(t, err.Error(), "failed to load DAG")
}

func TestCreateAgentEnv_MkdirAllError(t *testing.T) {
	// Note: This test may be skipped on some platforms where the path is valid
	// The null byte in paths should cause MkdirAll to fail on most systems
	t.Parallel()

	// Using a null byte in the path should cause an error on most systems
	handler := &remoteTaskHandler{
		workerID: "worker\x00invalid",
	}

	ctx := context.Background()
	env, err := handler.createAgentEnv(ctx, nil, "run-invalid")

	// On most systems, a null byte in the path should cause an error
	// If the system allows it (unlikely), the test passes anyway
	if err != nil {
		require.Contains(t, err.Error(), "failed to create log directory")
		require.Nil(t, env)
	} else {
		// If somehow it succeeded, clean up
		if env != nil {
			env.cleanup()
		}
	}
}

func TestLoadDAG_CleanupErrorLogged(t *testing.T) {
	t.Parallel()

	// Test that cleanup errors in loadDAG are logged but don't affect the return
	// We can't easily trigger an os.Remove error that's not IsNotExist,
	// but we can verify the cleanup function handles normal removal correctly

	handler := &remoteTaskHandler{
		config: &config.Config{
			Paths: config.PathsConfig{
				DAGsDir: t.TempDir(),
			},
		},
	}

	dagDefinition := `name: cleanup-logged-dag
steps:
  - name: step1
    command: echo cleanup
`

	task := &coordinatorv1.Task{
		Target:     "cleanup-logged.yaml",
		Definition: dagDefinition,
	}

	dag, cleanup, err := handler.loadDAG(context.Background(), task)

	require.NoError(t, err)
	require.NotNil(t, dag)
	require.NotNil(t, cleanup)

	// Call cleanup - this exercises the cleanup path even though
	// we can't easily make it fail
	cleanup()

	// Calling cleanup again should not panic (handles IsNotExist)
	require.NotPanics(t, func() {
		cleanup()
	})
}

func TestExecuteDAGRun_CreateAgentEnvError(t *testing.T) {
	t.Parallel()

	// Test that executeDAGRun returns error when createAgentEnv fails
	// Use null byte in workerID to trigger MkdirAll error
	dagContent := `name: exec-env-error-dag
steps:
  - name: step1
    command: echo test
`

	client := newMockRemoteCoordinatorClient()

	// Create handler with invalid workerID containing null byte
	handler := &remoteTaskHandler{
		workerID:          "worker\x00error", // Null byte causes MkdirAll to fail
		coordinatorClient: client,
		config: &config.Config{
			Paths: config.PathsConfig{
				DAGsDir: t.TempDir(),
			},
		},
	}

	// Load DAG with definition (required for distributed execution)
	task := &coordinatorv1.Task{
		Target:     "exec-env-error",
		Definition: dagContent,
	}
	dag, cleanup, loadErr := handler.loadDAG(context.Background(), task)
	require.NoError(t, loadErr)
	require.NotNil(t, dag)
	defer cleanup()

	// Create remote handlers
	root := exec.DAGRunRef{Name: "root", ID: "root-1"}
	parent := exec.DAGRunRef{Name: "parent", ID: "parent-1"}
	statusPusher, logStreamer, artifactUploader := handler.createRemoteHandlers("run-error", dag.Name, root)

	// Call executeDAGRun directly - should fail at createAgentEnv
	err := handler.executeDAGRun(context.Background(), dag, "run-error", "", "", root, parent, statusPusher, logStreamer, artifactUploader, false, nil, nil, nil)

	// On systems where null byte in path fails, we should get an error
	if err != nil {
		require.Contains(t, err.Error(), "failed to create log directory")
	}
}

func TestExecuteDAGRun_SuccessfulExecution(t *testing.T) {
	// This test covers the success path (lines 310-313) by running a complete execution
	// with full test infrastructure

	// Use test.Setup for full dependencies
	th := test.Setup(t)

	// Create a simple DAG that will succeed
	dagContent := `name: remote-handler-success
steps:
  - name: echo-step
    command: echo "hello from remote handler"
`
	dag := th.DAG(t, dagContent)

	client := newMockRemoteCoordinatorClient()

	// Create handler with full dependencies from test helper
	handler := &remoteTaskHandler{
		workerID:          "integration-test-worker",
		coordinatorClient: client,
		dagRunStore:       th.DAGRunStore,
		dagStore:          th.DAGStore,
		dagRunMgr:         th.DAGRunMgr,
		serviceRegistry:   th.ServiceRegistry,
		config:            th.Config,
	}

	// For a top-level run, root ID should match the dagRunID
	dagRunID := "run-success-1"
	root := exec.DAGRunRef{Name: dag.Name, ID: dagRunID}
	statusPusher := remote.NewStatusPusher(client, "integration-test-worker")
	logStreamer := remote.NewLogStreamer(client, "integration-test-worker", dagRunID, dag.Name, "", root)
	artifactUploader := remote.NewArtifactUploader(client, "integration-test-worker", dagRunID, dag.Name, "", root)

	// Call executeDAGRun - this should succeed and log completion
	// For top-level runs, pass empty parent and ensure root matches dagRunID
	err := handler.executeDAGRun(th.Context, dag.DAG, dagRunID, "", "", root, exec.DAGRunRef{}, statusPusher, logStreamer, artifactUploader, false, nil, nil, nil)

	// Should succeed for simple echo command
	require.NoError(t, err, "executeDAGRun should succeed for simple echo command")
}

func TestExecuteDAGRun_FailedExecutionStillUploadsArtifacts(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)

	dagContent := artifactUploadTestDAGContent("remote-handler-failure-artifacts", "fail-step", true)
	dag := th.DAG(t, dagContent)

	stream := newMockStreamArtifactsClient()
	client := newMockRemoteCoordinatorClient()
	client.StreamArtifactsFunc = func(context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
		return stream, nil
	}

	handler := &remoteTaskHandler{
		workerID:          "integration-test-worker",
		coordinatorClient: client,
		dagRunStore:       th.DAGRunStore,
		dagStore:          th.DAGStore,
		dagRunMgr:         th.DAGRunMgr,
		serviceRegistry:   th.ServiceRegistry,
		config:            th.Config,
	}

	dagRunID := "run-failure-artifacts-1"
	root := exec.DAGRunRef{Name: dag.Name, ID: dagRunID}
	statusPusher := remote.NewStatusPusher(client, "integration-test-worker")
	logStreamer := remote.NewLogStreamer(client, "integration-test-worker", dagRunID, dag.Name, "", root)
	artifactUploader := remote.NewArtifactUploader(client, "integration-test-worker", dagRunID, dag.Name, "", root)

	err := handler.executeDAGRun(th.Context, dag.DAG, dagRunID, "", "", root, exec.DAGRunRef{}, statusPusher, logStreamer, artifactUploader, false, nil, nil, nil)
	require.Error(t, err)

	var sawData bool
	var sawFinal bool
	for _, chunk := range stream.chunks {
		if chunk.RelativePath != "out.txt" {
			continue
		}
		if len(chunk.Data) > 0 {
			sawData = true
		}
		if chunk.IsFinal {
			sawFinal = true
		}
	}
	assert.True(t, sawData, "failed runs should still upload artifact contents")
	assert.True(t, sawFinal, "failed runs should still finalize artifact uploads")
}

func TestExecuteDAGRun_ArtifactUploadFailureMarksRunFailed(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)

	dagContent := artifactUploadTestDAGContent("remote-handler-upload-failure", "write-artifact", false)
	dag := th.DAG(t, dagContent)

	stream := newMockStreamArtifactsClient()
	stream.response = &coordinatorv1.StreamArtifactsResponse{
		Error: "coordinator write failed",
	}

	var reported []exec.DAGRunStatus
	var reportedMu sync.Mutex
	client := newMockRemoteCoordinatorClient()
	client.StreamArtifactsFunc = func(context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
		return stream, nil
	}
	client.ReportStatusFunc = func(_ context.Context, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
		status, err := convert.ProtoToDAGRunStatus(req.Status)
		require.NoError(t, err)
		reportedMu.Lock()
		reported = append(reported, *status)
		reportedMu.Unlock()
		return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
	}

	handler := &remoteTaskHandler{
		workerID:          "integration-test-worker",
		coordinatorClient: client,
		dagRunStore:       th.DAGRunStore,
		dagStore:          th.DAGStore,
		dagRunMgr:         th.DAGRunMgr,
		serviceRegistry:   th.ServiceRegistry,
		config:            th.Config,
	}

	dagRunID := "run-upload-failure-1"
	root := exec.DAGRunRef{Name: dag.Name, ID: dagRunID}
	statusPusher := remote.NewStatusPusher(client, "integration-test-worker")
	logStreamer := remote.NewLogStreamer(client, "integration-test-worker", dagRunID, dag.Name, "", root)
	artifactUploader := remote.NewArtifactUploader(client, "integration-test-worker", dagRunID, dag.Name, "", root)

	err := handler.executeDAGRun(th.Context, dag.DAG, dagRunID, "", "", root, exec.DAGRunRef{}, statusPusher, logStreamer, artifactUploader, false, nil, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upload artifacts")
	reportedMu.Lock()
	require.NotEmpty(t, reported)

	final := reported[len(reported)-1]
	reportedMu.Unlock()
	assert.Equal(t, core.Failed, final.Status)
	assert.Contains(t, final.Error, "failed to upload artifacts")
}

func TestExecuteDAGRun_FailedExecutionWithArtifactUploadFailurePreservesFailedStatus(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)

	dagContent := artifactUploadTestDAGContent("remote-handler-failure-upload-failure", "fail-step", true)
	dag := th.DAG(t, dagContent)

	stream := newMockStreamArtifactsClient()
	stream.response = &coordinatorv1.StreamArtifactsResponse{
		Error: "coordinator write failed",
	}

	var reported []exec.DAGRunStatus
	var reportedMu sync.Mutex
	client := newMockRemoteCoordinatorClient()
	client.StreamArtifactsFunc = func(context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
		return stream, nil
	}
	client.ReportStatusFunc = func(_ context.Context, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
		status, err := convert.ProtoToDAGRunStatus(req.Status)
		require.NoError(t, err)
		reportedMu.Lock()
		reported = append(reported, *status)
		reportedMu.Unlock()
		return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
	}

	handler := &remoteTaskHandler{
		workerID:          "integration-test-worker",
		coordinatorClient: client,
		dagRunStore:       th.DAGRunStore,
		dagStore:          th.DAGStore,
		dagRunMgr:         th.DAGRunMgr,
		serviceRegistry:   th.ServiceRegistry,
		config:            th.Config,
	}

	dagRunID := "run-failure-upload-failure-1"
	root := exec.DAGRunRef{Name: dag.Name, ID: dagRunID}
	statusPusher := remote.NewStatusPusher(client, "integration-test-worker")
	logStreamer := remote.NewLogStreamer(client, "integration-test-worker", dagRunID, dag.Name, "", root)
	artifactUploader := remote.NewArtifactUploader(client, "integration-test-worker", dagRunID, dag.Name, "", root)

	err := handler.executeDAGRun(th.Context, dag.DAG, dagRunID, "", "", root, exec.DAGRunRef{}, statusPusher, logStreamer, artifactUploader, false, nil, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upload artifacts")
	reportedMu.Lock()
	require.NotEmpty(t, reported)

	final := reported[len(reported)-1]
	reportedMu.Unlock()
	assert.Equal(t, core.Failed, final.Status)
	assert.Contains(t, final.Error, "failed to upload artifacts")
}
