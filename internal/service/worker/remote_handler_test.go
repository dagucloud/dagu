// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/dagrun"

	"github.com/dagucloud/dagu/v2/internal/cmn/backoff"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/proto/convert"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	dagruntime "github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/service/worker/coordreport"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/dagucloud/dagu/v2/internal/test"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
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
		assert.Equal(t, serviceregistry.HostInfo{}, owner)
	})

	t.Run("AcceptsCompleteMetadata", func(t *testing.T) {
		t.Parallel()

		owner, err := taskOwner(&coordinatorv1.Task{
			OwnerCoordinatorId:   "coord-1",
			OwnerCoordinatorHost: "127.0.0.1",
			OwnerCoordinatorPort: 4321,
		})

		require.NoError(t, err)
		assert.Equal(t, serviceregistry.HostInfo{ID: "coord-1", Host: "127.0.0.1", Port: 4321}, owner)
	})

	t.Run("AcceptsEndpointWithoutProcessID", func(t *testing.T) {
		t.Parallel()

		owner, err := taskOwner(&coordinatorv1.Task{
			OwnerCoordinatorHost: "coordinator",
			OwnerCoordinatorPort: 50055,
		})

		require.NoError(t, err)
		assert.Equal(t, serviceregistry.HostInfo{Host: "coordinator", Port: 50055}, owner)
	})
}

func TestPollerAckTaskClaimRejectsPartialOwnerMetadata(t *testing.T) {
	t.Parallel()

	called := false
	client := newMockRemoteCoordinatorClient()
	client.AckTaskClaimFunc = func(context.Context, serviceregistry.HostInfo, *coordinatorv1.AckTaskClaimRequest) (*coordinatorv1.AckTaskClaimResponse, error) {
		called = true
		return &coordinatorv1.AckTaskClaimResponse{Accepted: true}, nil
	}

	poller := NewPoller("worker-1", client, nil, 0, nil)
	err := poller.ackTaskClaim(context.Background(), &coordinatorv1.Task{
		ClaimToken:           "claim-1",
		OwnerCoordinatorHost: "127.0.0.1",
		OwnerCoordinatorId:   "coord-1",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "incomplete owner coordinator metadata")
	assert.False(t, called)
}

func TestPollerAckTaskClaimRetriesTransientFailure(t *testing.T) {
	t.Parallel()

	client := newMockRemoteCoordinatorClient()
	calls := 0
	client.AckTaskClaimFunc = func(_ context.Context, _ serviceregistry.HostInfo, req *coordinatorv1.AckTaskClaimRequest) (*coordinatorv1.AckTaskClaimResponse, error) {
		calls++
		require.Equal(t, "attempt-key-1", req.AttemptKey)
		if calls == 1 {
			return nil, status.Error(codes.Unavailable, "coordinator restarting")
		}
		return &coordinatorv1.AckTaskClaimResponse{Accepted: true}, nil
	}

	poller := NewPoller("worker-1", client, nil, 0, nil)
	err := poller.ackTaskClaim(t.Context(), &coordinatorv1.Task{
		AttemptKey:           "attempt-key-1",
		ClaimToken:           "claim-1",
		OwnerCoordinatorHost: "coordinator",
		OwnerCoordinatorPort: 50055,
		OwnerCoordinatorId:   "coord-a",
	})

	require.NoError(t, err)
	require.Equal(t, 2, calls)
}

type mockStreamLogsClient struct {
	chunks           []*coordinatorv1.LogChunk
	mu               sync.Mutex
	sendErr          error
	closeErr         error
	closeAndRecvFunc func() (*coordinatorv1.StreamLogsResponse, error)
	response         *coordinatorv1.StreamLogsResponse
	ctx              context.Context
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
	if m.closeAndRecvFunc != nil {
		return m.closeAndRecvFunc()
	}
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

func (m *mockStreamLogsClient) snapshotChunks() []*coordinatorv1.LogChunk {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*coordinatorv1.LogChunk(nil), m.chunks...)
}

type recordingStatusPusher struct {
	push func(context.Context, ir.DAGRunStatus) error
}

func (p *recordingStatusPusher) Push(ctx context.Context, status ir.DAGRunStatus) error {
	return p.push(ctx, status)
}

type schedulerLogStatusFinalizerFunc func(context.Context, ir.DAGRunStatus) (bool, error)

func (f schedulerLogStatusFinalizerFunc) finalizeSchedulerLogForStatus(ctx context.Context, status ir.DAGRunStatus) (bool, error) {
	return f(ctx, status)
}

type schedulerLogContextCloserFunc func(context.Context) error

func (f schedulerLogContextCloserFunc) Close() error {
	return f(context.Background())
}

func (f schedulerLogContextCloserFunc) CloseWithContext(ctx context.Context) error {
	return f(ctx)
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
    run: |
      if (-not $env:DAG_RUN_ARTIFACTS_DIR) { throw 'DAG_RUN_ARTIFACTS_DIR not set' }
      New-Item -ItemType Directory -Path $env:DAG_RUN_ARTIFACTS_DIR -Force | Out-Null
      [System.IO.File]::WriteAllText((Join-Path $env:DAG_RUN_ARTIFACTS_DIR 'out.txt'), 'artifact')
      exit 1
    with:
      shell: powershell
`, name, stepName)
		}

		return fmt.Sprintf(`name: %s
artifacts:
  enabled: true
steps:
  - name: %s
    run: |
      if (-not $env:DAG_RUN_ARTIFACTS_DIR) { throw 'DAG_RUN_ARTIFACTS_DIR not set' }
      New-Item -ItemType Directory -Path $env:DAG_RUN_ARTIFACTS_DIR -Force | Out-Null
      [System.IO.File]::WriteAllText((Join-Path $env:DAG_RUN_ARTIFACTS_DIR 'out.txt'), 'artifact')
    with:
      shell: powershell
`, name, stepName)
	}

	if fail {
		return fmt.Sprintf(`name: %s
artifacts:
  enabled: true
steps:
  - name: %s
    run: |
      printf "artifact" > "$DAG_RUN_ARTIFACTS_DIR/out.txt"
      exit 1
    with:
      shell: /bin/sh
`, name, stepName)
	}

	return fmt.Sprintf(`name: %s
artifacts:
  enabled: true
steps:
  - name: %s
    run: |
      printf "artifact" > "$DAG_RUN_ARTIFACTS_DIR/out.txt"
    with:
      shell: /bin/sh
`, name, stepName)
}

func (m *mockStreamArtifactsClient) SendMsg(any) error {
	return nil
}

func (m *mockStreamArtifactsClient) RecvMsg(any) error {
	return nil
}

func (m *mockStreamArtifactsClient) snapshotChunks() []*coordinatorv1.ArtifactChunk {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*coordinatorv1.ArtifactChunk(nil), m.chunks...)
}

func countSchedulerFinalChunks(streams []*mockStreamLogsClient) int {
	return countLogChunks(streams, func(chunk *coordinatorv1.LogChunk) bool {
		return chunk.StreamType == coordinatorv1.LogStreamType_LOG_STREAM_TYPE_SCHEDULER && chunk.IsFinal
	})
}

func hasLogChunk(streams []*mockStreamLogsClient, dagRunID, dagName, attemptID string, root ir.DAGRunRef, stepName string) bool {
	return hasLogChunkMatching(streams, func(chunk *coordinatorv1.LogChunk) bool {
		return logChunkHasMetadata(chunk, dagRunID, dagName, attemptID, root) && chunk.StepName == stepName
	})
}

func hasStepLogDataChunk(streams []*mockStreamLogsClient, dagRunID, dagName, attemptID string, root ir.DAGRunRef, stepName string, streamType coordinatorv1.LogStreamType) bool {
	return hasLogChunkMatching(streams, func(chunk *coordinatorv1.LogChunk) bool {
		return logChunkHasMetadata(chunk, dagRunID, dagName, attemptID, root) &&
			chunk.StepName == stepName &&
			chunk.StreamType == streamType &&
			len(chunk.Data) > 0
	})
}

func hasSchedulerDataChunk(streams []*mockStreamLogsClient, dagRunID, dagName, attemptID string, root ir.DAGRunRef) bool {
	return hasLogChunkMatching(streams, func(chunk *coordinatorv1.LogChunk) bool {
		return logChunkHasMetadata(chunk, dagRunID, dagName, attemptID, root) &&
			chunk.StreamType == coordinatorv1.LogStreamType_LOG_STREAM_TYPE_SCHEDULER &&
			!chunk.IsFinal &&
			len(chunk.Data) > 0
	})
}

func hasSchedulerFinalChunk(streams []*mockStreamLogsClient, dagRunID, dagName, attemptID string, root ir.DAGRunRef) bool {
	return hasLogChunkMatching(streams, func(chunk *coordinatorv1.LogChunk) bool {
		return logChunkHasMetadata(chunk, dagRunID, dagName, attemptID, root) &&
			chunk.StreamType == coordinatorv1.LogStreamType_LOG_STREAM_TYPE_SCHEDULER &&
			chunk.IsFinal
	})
}

func hasLogChunkMatching(streams []*mockStreamLogsClient, match func(*coordinatorv1.LogChunk) bool) bool {
	return countLogChunks(streams, match) > 0
}

func countLogChunks(streams []*mockStreamLogsClient, match func(*coordinatorv1.LogChunk) bool) int {
	var count int
	for _, stream := range streams {
		for _, chunk := range stream.snapshotChunks() {
			if match(chunk) {
				count++
			}
		}
	}
	return count
}

func logChunkHasMetadata(chunk *coordinatorv1.LogChunk, dagRunID, dagName, attemptID string, root ir.DAGRunRef) bool {
	return chunk.DagRunId == dagRunID &&
		chunk.DagName == dagName &&
		chunk.AttemptId == attemptID &&
		chunk.RootDagRunName == root.Name &&
		chunk.RootDagRunId == root.ID
}

func hasArtifactChunk(streams []*mockStreamArtifactsClient, dagRunID, dagName, attemptID string, root ir.DAGRunRef, relPath string) bool {
	for _, stream := range streams {
		for _, chunk := range stream.snapshotChunks() {
			if chunk.DagRunId == dagRunID &&
				chunk.DagName == dagName &&
				chunk.AttemptId == attemptID &&
				chunk.RootDagRunName == root.Name &&
				chunk.RootDagRunId == root.ID &&
				chunk.RelativePath == relPath {
				return true
			}
		}
	}
	return false
}

type mockRemoteCoordinatorClient struct {
	AckTaskClaimFunc      func(ctx context.Context, owner serviceregistry.HostInfo, req *coordinatorv1.AckTaskClaimRequest) (*coordinatorv1.AckTaskClaimResponse, error)
	RunHeartbeatFunc      func(ctx context.Context, owner serviceregistry.HostInfo, req *coordinatorv1.RunHeartbeatRequest) (*coordinatorv1.RunHeartbeatResponse, error)
	ReportStatusFunc      func(ctx context.Context, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error)
	ReportStatusToFunc    func(ctx context.Context, owner serviceregistry.HostInfo, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error)
	StreamLogsFunc        func(ctx context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error)
	StreamLogsToFunc      func(ctx context.Context, owner serviceregistry.HostInfo) (coordinatorv1.CoordinatorService_StreamLogsClient, error)
	StreamArtifactsFunc   func(ctx context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error)
	StreamArtifactsToFunc func(ctx context.Context, owner serviceregistry.HostInfo) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error)
	GetDAGRunStatusFunc   func(ctx context.Context, dagName, dagRunID string, rootRef *ir.DAGRunRef) (*dispatch.DAGRunStatusResult, error)
	GetDAGFunc            func(ctx context.Context, name string) (string, error)
	DispatchFunc          func(ctx context.Context, task *dispatch.DispatchTask) error
	PollFunc              func(ctx context.Context, policy backoff.RetryPolicy, req *coordinatorv1.PollRequest) (*coordinatorv1.Task, error)
	HeartbeatFunc         func(ctx context.Context, req *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error)
	GetWorkersFunc        func(ctx context.Context) ([]*coordinatorv1.WorkerInfo, error)
	CleanupFunc           func(ctx context.Context) error
	MetricsFunc           func() coordinator.Metrics
	RequestCancelFunc     func(ctx context.Context, dagName, dagRunID string, rootRef *ir.DAGRunRef) error
}

type workspaceRemoteCoordinatorClient struct {
	*mockRemoteCoordinatorClient
	data []byte
}

func (c *workspaceRemoteCoordinatorClient) PutWorkspaceBundle(context.Context, workspacebundle.Descriptor, []byte) error {
	return nil
}

func (c *workspaceRemoteCoordinatorClient) GetWorkspaceBundle(context.Context, string) ([]byte, error) {
	return c.data, nil
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
		GetDAGRunStatusFunc: func(_ context.Context, _, _ string, _ *ir.DAGRunRef) (*dispatch.DAGRunStatusResult, error) {
			return &dispatch.DAGRunStatusResult{Found: false}, nil
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

func (m *mockRemoteCoordinatorClient) AckTaskClaimTo(ctx context.Context, owner serviceregistry.HostInfo, req *coordinatorv1.AckTaskClaimRequest) (*coordinatorv1.AckTaskClaimResponse, error) {
	if m.AckTaskClaimFunc != nil {
		return m.AckTaskClaimFunc(ctx, owner, req)
	}
	return &coordinatorv1.AckTaskClaimResponse{Accepted: true}, nil
}

func (m *mockRemoteCoordinatorClient) RunHeartbeatTo(ctx context.Context, owner serviceregistry.HostInfo, req *coordinatorv1.RunHeartbeatRequest) (*coordinatorv1.RunHeartbeatResponse, error) {
	if m.RunHeartbeatFunc != nil {
		return m.RunHeartbeatFunc(ctx, owner, req)
	}
	return &coordinatorv1.RunHeartbeatResponse{}, nil
}

func (m *mockRemoteCoordinatorClient) ReportStatusTo(ctx context.Context, owner serviceregistry.HostInfo, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
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

func (m *mockRemoteCoordinatorClient) StreamLogsTo(ctx context.Context, owner serviceregistry.HostInfo) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
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

func (m *mockRemoteCoordinatorClient) StreamArtifactsTo(ctx context.Context, owner serviceregistry.HostInfo) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
	if m.StreamArtifactsToFunc != nil {
		return m.StreamArtifactsToFunc(ctx, owner)
	}
	return m.StreamArtifacts(ctx)
}

func (m *mockRemoteCoordinatorClient) GetDAGRunStatus(ctx context.Context, dagName, dagRunID string, rootRef *ir.DAGRunRef) (*dispatch.DAGRunStatusResult, error) {
	if m.GetDAGRunStatusFunc != nil {
		return m.GetDAGRunStatusFunc(ctx, dagName, dagRunID, rootRef)
	}
	return &dispatch.DAGRunStatusResult{Found: false}, nil
}

func (m *mockRemoteCoordinatorClient) GetDAG(ctx context.Context, name string) (string, error) {
	if m.GetDAGFunc != nil {
		return m.GetDAGFunc(ctx, name)
	}
	return "", nil
}

func (m *mockRemoteCoordinatorClient) Dispatch(ctx context.Context, req dispatch.DispatchRequest) error {
	if m.DispatchFunc != nil {
		return m.DispatchFunc(ctx, req.Task)
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

func (m *mockRemoteCoordinatorClient) RequestCancel(ctx context.Context, dagName, dagRunID string, rootRef *ir.DAGRunRef) error {
	if m.RequestCancelFunc != nil {
		return m.RequestCancelFunc(ctx, dagName, dagRunID, rootRef)
	}
	return nil
}

type mockRemoteStateCoordinatorClient struct {
	*mockRemoteCoordinatorClient
	handler *coordinator.Handler
}

func newMockRemoteStateCoordinatorClient(stateStore dagrun.StateStore) *mockRemoteStateCoordinatorClient {
	return &mockRemoteStateCoordinatorClient{
		mockRemoteCoordinatorClient: newMockRemoteCoordinatorClient(),
		handler: coordinator.NewHandler(coordinator.HandlerConfig{
			StateStore: stateStore,
		}),
	}
}

func (m *mockRemoteStateCoordinatorClient) GetState(ctx context.Context, req *coordinatorv1.GetStateRequest) (*coordinatorv1.GetStateResponse, error) {
	return m.handler.GetState(ctx, req)
}

func (m *mockRemoteStateCoordinatorClient) PutState(ctx context.Context, req *coordinatorv1.PutStateRequest) (*coordinatorv1.PutStateResponse, error) {
	return m.handler.PutState(ctx, req)
}

func (m *mockRemoteStateCoordinatorClient) DeleteState(ctx context.Context, req *coordinatorv1.DeleteStateRequest) (*coordinatorv1.DeleteStateResponse, error) {
	return m.handler.DeleteState(ctx, req)
}

func (m *mockRemoteStateCoordinatorClient) ListState(ctx context.Context, req *coordinatorv1.ListStateRequest) (*coordinatorv1.ListStateResponse, error) {
	return m.handler.ListState(ctx, req)
}

func TestNewRemoteTaskHandler(t *testing.T) {
	t.Parallel()

	t.Run("AllFieldsSet", func(t *testing.T) {
		t.Parallel()

		client := newMockRemoteCoordinatorClient()
		cfg := &config.Config{}

		handler := NewRemoteTaskHandler(RemoteTaskHandlerConfig{
			WorkerID:          "worker-1",
			CoordinatorClient: client,
			Config:            cfg,
		})

		require.NotNil(t, handler)

		// Verify it's the correct type
		rh, ok := handler.(*remoteTaskHandler)
		require.True(t, ok, "should return *remoteTaskHandler")
		assert.Equal(t, "worker-1", rh.workerID)
		assert.Equal(t, client, rh.coordinatorClient)
		assert.Equal(t, cfg, rh.config)
	})

	t.Run("NilOptionalFields", func(t *testing.T) {
		t.Parallel()

		client := newMockRemoteCoordinatorClient()

		handler := NewRemoteTaskHandler(RemoteTaskHandlerConfig{
			WorkerID:          "worker-2",
			CoordinatorClient: client,
			// DAGRepository is nil
			// ServiceRegistry is nil
		})

		require.NotNil(t, handler)

		rh, ok := handler.(*remoteTaskHandler)
		require.True(t, ok)
		assert.Nil(t, rh.dagRepository)
		assert.Nil(t, rh.serviceRegistry)
	})

	t.Run("StateStoreSet", func(t *testing.T) {
		t.Parallel()

		stateStore := store.NewDAGStateStore(testutil.NewMemoryBackend().Collection("dag_state"))
		handler := NewRemoteTaskHandler(RemoteTaskHandlerConfig{
			WorkerID:          "worker-state",
			CoordinatorClient: newMockRemoteCoordinatorClient(),
			StateStore:        stateStore,
		})

		rh, ok := handler.(*remoteTaskHandler)
		require.True(t, ok)
		assert.Equal(t, stateStore, rh.stateStore)
	})

	t.Run("StateStoreDefaultsToCoordinatorStateClient", func(t *testing.T) {
		t.Parallel()

		stateStore := store.NewDAGStateStore(testutil.NewMemoryBackend().Collection("dag_state"))
		client := newMockRemoteStateCoordinatorClient(stateStore)
		handler := NewRemoteTaskHandler(RemoteTaskHandlerConfig{
			WorkerID:          "worker-state-client",
			CoordinatorClient: client,
		})

		rh, ok := handler.(*remoteTaskHandler)
		require.True(t, ok)
		require.NotNil(t, rh.stateStore)

		ref := dagrun.StateRef{Scope: dagrun.StateScopeDAG, Namespace: "daily-agent", Key: "cursor"}
		_, err := rh.stateStore.Put(context.Background(), ref, json.RawMessage(`{"last_id":123}`), dagrun.StatePutOptions{})
		require.NoError(t, err)

		got, err := stateStore.Get(context.Background(), ref)
		require.NoError(t, err)
		assert.JSONEq(t, `{"last_id":123}`, string(got.Value))
	})
}

func TestCreateRemoteHandlers(t *testing.T) {
	t.Parallel()

	handler := &remoteTaskHandler{
		workerID:          "test-worker",
		coordinatorClient: newMockRemoteCoordinatorClient(),
	}
	handlers := handler.createRemoteHandlers(remoteRun{
		task: &coordinatorv1.Task{
			DagRunId:   "run-1",
			AttemptId:  "attempt-1",
			AttemptKey: "attempt-key-1",
		},
		root: ir.DAGRunRef{Name: "root-dag", ID: "root-123"},
	}, "test-dag")

	require.NotNil(t, handlers.status)
	require.NotNil(t, handlers.logs)
	require.NotNil(t, handlers.artifacts)
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
    run: echo inline
`

		task := &coordinatorv1.Task{
			Target:     "inline-dag", // Target is the DAG name, not filename
			Definition: dagDefinition,
		}

		loaded, err := handler.loadDAG(context.Background(), task)

		require.NoError(t, err)
		dag := loaded.dag
		cleanup := loaded.cleanup
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

		loaded, err := handler.loadDAG(context.Background(), task)

		require.Error(t, err)
		require.Nil(t, loaded)
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
		require.Contains(t, err.Error(), "retry requires previous_status in task")
	})

	t.Run("QueuedCatchupPreservesTriggerType", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `name: remote-catchup-dag
steps:
  - name: step1
    run: echo remote catchup
`)

		runID := "remote-catchup-run"
		status := ir.NewStatusBuilder(dag.DAG).Create(
			runID,
			ir.Queued,
			0,
			time.Time{},
			ir.WithAttemptID("queued-attempt"),
			ir.WithTriggerType(ir.TriggerTypeCatchUp),
			ir.WithQueuedAt(stringutil.FormatTime(time.Now())),
			ir.WithScheduleTime(stringutil.FormatTime(time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC))),
		)

		previousStatus, convErr := convert.DAGRunStatusToProto(&status)
		require.NoError(t, convErr)

		var (
			mu       sync.Mutex
			reported []*ir.DAGRunStatus
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
			dagRepository:     th.DAGRepository,
			dagRunMgr:         th.DAGRunMgr,
			serviceRegistry:   th.ServiceRegistry,
			peerConfig:        config.Peer{Insecure: true},
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
		require.Equal(t, ir.Succeeded, final.Status)
		require.Equal(t, ir.TriggerTypeCatchUp, final.TriggerType)
	})

	t.Run("RemoteWorkerWithEmbeddedStatus", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()
		dagFile := filepath.Join(tempDir, "retry.yaml")
		dagContent := `name: retry-dag
steps:
  - name: step1
    run: echo retry
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

		// Create a previous status proto
		previousStatus, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
			Name:   "retry-dag",
			Status: ir.Succeeded,
			Nodes:  []*ir.Node{},
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

		// This will fail at agent creation since full dependencies are not configured,
		// but it verifies the retry path uses the embedded previous status.
		err = handler.handleRetry(context.Background(), task)

		// The error should NOT be about missing status source
		require.Error(t, err)
		require.NotContains(t, err.Error(), "retry requires previous_status in task")
	})
}

func TestRetryTaskProfileNameUsesStoredStatus(t *testing.T) {
	t.Parallel()

	status := &ir.DAGRunStatus{ProfileName: "prod"}
	assert.Equal(t, "prod", retryTaskProfileName(status))
	assert.Empty(t, retryTaskProfileName(nil))
}

func TestTaskExtraEnvs(t *testing.T) {
	t.Parallel()

	assert.Nil(t, taskExtraEnvs(nil))
	assert.Nil(t, taskExtraEnvs(&coordinatorv1.Task{}))
	assert.Equal(t, []string{runenv.EnvKeyExternalStepRetry + "=1"}, taskExtraEnvs(&coordinatorv1.Task{
		ExternalStepRetry: true,
	}))
	assert.Equal(t, []string{
		runenv.EnvKeyExternalStepRetry + "=1",
		ir.ParallelItemVariable + "=item-1",
	}, taskExtraEnvs(&coordinatorv1.Task{
		ExternalStepRetry: true,
		ParallelItem:      "item-1",
	}))
	assert.Equal(t, []string{
		ir.ParallelItemVariable + "=item-1",
	}, taskExtraEnvs(&coordinatorv1.Task{
		ParallelItem: "item-1",
	}))
}

func TestHandleStart_ExternalStepRetryQueuesPendingRetry(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	dag := th.DAG(t, `name: remote-external-retry
steps:
  - name: flaky
    run: exit 1
    retry_policy:
      limit: 1
      interval_sec: 30
`)

	var (
		mu       sync.Mutex
		reported []*ir.DAGRunStatus
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
		dagRepository:     th.DAGRepository,
		dagRunMgr:         th.DAGRunMgr,
		serviceRegistry:   th.ServiceRegistry,
		peerConfig:        config.Peer{Insecure: true},
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

	err := handler.handleStart(th.Context, task, false)
	require.NoError(t, err)
	mu.Lock()
	require.NotEmpty(t, reported)

	final := reported[len(reported)-1]
	mu.Unlock()
	require.Equal(t, ir.Queued, final.Status)
	require.Equal(t, []ir.PendingStepRetry{
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
    run: echo start
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

	// OPERATION_RETRY requires previous_status in the task.
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
	require.Contains(t, err.Error(), "retry requires previous_status in task")
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
	require.Contains(t, err.Error(), "retry requires previous_status in task")
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
    run: echo cleanup
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
    run: echo queued
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
    run: echo exec
`
	err := os.WriteFile(dagFile, []byte(dagContent), 0644)
	require.NoError(t, err)

	previousStatus, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
		Name:   "exec-retry-dag",
		Status: ir.Succeeded,
		Nodes:  []*ir.Node{},
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
	previousStatus, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
		Name:   "loaddag-error-dag",
		Status: ir.Succeeded,
		Nodes:  []*ir.Node{},
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
	previousStatus, convErr := convert.DAGRunStatusToProto(&ir.DAGRunStatus{
		Name:   "def-cleanup-dag",
		Status: ir.Succeeded,
		Nodes:  []*ir.Node{},
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
    run: echo cleanup
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

func TestLoadDAGSelectsInlineTargetFromWorkspace(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "input.txt"), []byte("input"), 0o600))
	definition := []byte(`name: root
steps:
  - name: root-step
    run: echo root
---
name: child
steps:
  - name: child-step
    run: cat input.txt
    dependencies: input.txt
`)
	desc, archive, err := workspacebundle.PackDirectory(root, workspacebundle.PackOptions{
		DAGPath:  "dag.yaml",
		DAGData:  definition,
		Includes: []string{"input.txt"},
	})
	require.NoError(t, err)
	client := &workspaceRemoteCoordinatorClient{
		mockRemoteCoordinatorClient: newMockRemoteCoordinatorClient(),
		data:                        archive,
	}
	handler := &remoteTaskHandler{coordinatorClient: client, config: &config.Config{}}
	task := &coordinatorv1.Task{
		Target:                     "child",
		DagRunId:                   "run-1",
		WorkspaceBundleDigest:      desc.Digest,
		WorkspaceBundleSize:        desc.Size,
		WorkspaceBundleDagPath:     desc.DAGPath,
		WorkspaceBundleOriginalRef: desc.OriginalRef,
		WorkspaceBundleResolvedRef: desc.ResolvedRef,
	}

	loaded, err := handler.loadDAG(context.Background(), task)
	require.NoError(t, err)
	defer loaded.cleanup()
	require.NotNil(t, loaded.workspaceSeed)
	require.Len(t, loaded.dag.Steps, 1)
	assert.Equal(t, "child-step", loaded.dag.Steps[0].Name)
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
    run: echo cleanup
`

	task := &coordinatorv1.Task{
		Target:     "cleanup-logged.yaml",
		Definition: dagDefinition,
	}

	loaded, err := handler.loadDAG(context.Background(), task)

	require.NoError(t, err)
	require.NotNil(t, loaded.dag)
	require.NotNil(t, loaded.cleanup)

	// Call cleanup - this exercises the cleanup path even though
	// we can't easily make it fail
	loaded.cleanup()

	// Calling cleanup again should not panic (handles IsNotExist)
	require.NotPanics(t, func() {
		loaded.cleanup()
	})
}

func TestExecuteDAGRun_CreateAgentEnvError(t *testing.T) {
	t.Parallel()

	// Test that executeDAGRun returns error when createAgentEnv fails
	// Use null byte in workerID to trigger MkdirAll error
	dagContent := `name: exec-env-error-dag
steps:
  - name: step1
    run: echo test
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
	loaded, loadErr := handler.loadDAG(context.Background(), task)
	require.NoError(t, loadErr)
	require.NotNil(t, loaded.dag)
	defer loaded.cleanup()
	dag := loaded.dag

	// Create remote handlers
	root := ir.DAGRunRef{Name: "root", ID: "root-1"}
	parent := ir.DAGRunRef{Name: "parent", ID: "parent-1"}
	run := remoteRun{
		task: &coordinatorv1.Task{
			DagRunId:   "run-error",
			AttemptId:  "attempt-error",
			AttemptKey: "attempt-key-error",
		},
		root:   root,
		parent: parent,
	}
	run.handlers = handler.createRemoteHandlers(run, dag.Name)

	// Call executeDAGRun directly - should fail at createAgentEnv
	err := handler.executeDAGRun(context.Background(), dag, run)

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
    run: echo "hello from remote handler"
`
	dag := th.DAG(t, dagContent)

	client := newMockRemoteCoordinatorClient()
	var (
		reportedMu sync.Mutex
		finalActor string
	)
	client.ReportStatusFunc = func(_ context.Context, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
		status, err := convert.ProtoToDAGRunStatus(req.Status)
		require.NoError(t, err)
		if status.Status == ir.Succeeded {
			reportedMu.Lock()
			finalActor = status.TriggerActor
			reportedMu.Unlock()
		}
		return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
	}

	// Create handler with full dependencies from test helper
	handler := &remoteTaskHandler{
		workerID:          "integration-test-worker",
		coordinatorClient: client,
		dagRepository:     th.DAGRepository,
		dagRunMgr:         th.DAGRunMgr,
		serviceRegistry:   th.ServiceRegistry,
		peerConfig:        config.Peer{Insecure: true},
		config:            th.Config,
	}

	// For a top-level run, root ID should match the dagRunID
	dagRunID := "run-success-1"
	root := ir.DAGRunRef{Name: dag.Name, ID: dagRunID}
	handlers := runHandlers{
		status:    coordreport.NewStatusPusher(client, "integration-test-worker", ""),
		logs:      coordreport.NewLogStreamer(client, "integration-test-worker", dagRunID, dag.Name, "", root),
		artifacts: coordreport.NewArtifactUploader(client, "integration-test-worker", dagRunID, dag.Name, "", root),
	}

	// Call executeDAGRun - this should succeed and log completion
	// For top-level runs, pass empty parent and ensure root matches dagRunID
	err := handler.executeDAGRun(th.Context, dag.DAG, remoteRun{
		task:     &coordinatorv1.Task{DagRunId: dagRunID, TriggerActor: "alice"},
		root:     root,
		handlers: handlers,
	})

	// Should succeed for simple echo command
	require.NoError(t, err, "executeDAGRun should succeed for simple echo command")
	reportedMu.Lock()
	defer reportedMu.Unlock()
	require.Equal(t, "alice", finalActor)
}

func TestRemoteRunReporter_FinalizesSchedulerLogByClosingLiveWriterOnce(t *testing.T) {
	const (
		dagName   = "final-scheduler-log"
		dagRunID  = "run-final-scheduler-log"
		attemptID = "attempt-final-scheduler-log"
	)

	logFilePath := filepath.Join(t.TempDir(), "scheduler.log")
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, logFile.Close())
	}()

	var (
		streams   []*mockStreamLogsClient
		streamsMu sync.Mutex
		events    []string
		eventsMu  sync.Mutex
	)
	record := func(event string) {
		eventsMu.Lock()
		defer eventsMu.Unlock()
		events = append(events, event)
	}

	client := newMockRemoteCoordinatorClient()
	client.StreamLogsFunc = func(context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
		stream := newMockStreamLogsClient()
		streamsMu.Lock()
		streams = append(streams, stream)
		streamsMu.Unlock()
		return stream, nil
	}

	reporter := newRemoteRunReporter(client, "worker-1", remoteRunMetadata{
		dagRunID:  dagRunID,
		dagName:   dagName,
		attemptID: attemptID,
		root:      ir.NewDAGRunRef(dagName, dagRunID),
	}, serviceregistry.HostInfo{})
	require.NotNil(t, reporter.EnableSchedulerFinalizer(logFilePath))

	schedulerWriter := reporter.NewSchedulerLogWriter(context.Background(), logFile)
	_, err = schedulerWriter.Write([]byte(strings.Repeat("x", 40*1024)))
	require.NoError(t, err)

	statusPusher := &finalSchedulerLogStatusPusher{
		finalizer: reporter,
		pusher: &recordingStatusPusher{
			push: func(ctx context.Context, status ir.DAGRunStatus) error {
				require.Equal(t, dagRunID, status.DAGRunID)
				require.Equal(t, ir.Succeeded, status.Status)
				require.NoError(t, ctx.Err(), "terminal status should be pushed with a live context")
				record("terminal-status")
				return nil
			},
		},
	}

	require.NoError(t, statusPusher.Push(context.Background(), ir.DAGRunStatus{
		Root:      ir.NewDAGRunRef(dagName, dagRunID),
		Name:      dagName,
		DAGRunID:  dagRunID,
		AttemptID: attemptID,
		Status:    ir.Succeeded,
		Log:       logFilePath,
	}))
	streamsMu.Lock()
	finalsBeforeClose := countSchedulerFinalChunks(streams)
	streamsMu.Unlock()
	require.Equal(t, 1, finalsBeforeClose, "scheduler finalization should send exactly one final marker before terminal status")

	require.NoError(t, schedulerWriter.Close())
	streamsMu.Lock()
	finalsAfterClose := countSchedulerFinalChunks(streams)
	streamCountAfterFinalization := len(streams)
	streamsMu.Unlock()

	require.NoError(t, reporter.StreamSchedulerLog(context.Background(), logFilePath))
	streamsMu.Lock()
	streamCountAfterCachedReplay := len(streams)
	streamsMu.Unlock()

	require.Equal(t, finalsBeforeClose, finalsAfterClose, "deferred close should not send a duplicate final scheduler marker")
	require.Equal(t, streamCountAfterFinalization, streamCountAfterCachedReplay, "cached finalization should not reopen the stream")
	require.Equal(t, []string{"terminal-status"}, events)
}

func TestRemoteRunReporter_RetainsClaimKeyWhenReusedWithPartialMetadata(t *testing.T) {
	const (
		dagName   = "claim-key-reuse"
		dagRunID  = "run-claim-key-reuse"
		attemptID = "attempt-claim-key-reuse"
		claimKey  = "claim-key"
	)

	logStream := newMockStreamLogsClient()
	artifactStream := newMockStreamArtifactsClient()
	client := newMockRemoteCoordinatorClient()
	client.StreamLogsFunc = func(context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
		return logStream, nil
	}
	client.StreamArtifactsFunc = func(context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
		return artifactStream, nil
	}

	root := ir.NewDAGRunRef(dagName, dagRunID)
	full := remoteRunMetadata{
		dagRunID:  dagRunID,
		dagName:   dagName,
		attemptID: attemptID,
		claimKey:  claimKey,
		root:      root,
	}
	partial := full
	partial.claimKey = ""
	reporter := newRemoteRunReporter(client, "worker-1", full, serviceregistry.HostInfo{})

	streamer := reporter.logStreamerFor(full)
	require.Same(t, streamer, reporter.logStreamerFor(partial))
	writer := streamer.NewStepWriter(context.Background(), "step", runctx.StreamTypeStdout)
	_, err := writer.Write([]byte("output"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	uploader := reporter.artifactUploaderFor(full)
	require.Same(t, uploader, reporter.artifactUploaderFor(partial))
	artifactDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(artifactDir, "out.txt"), []byte("artifact"), 0o600))
	require.NoError(t, uploader.UploadDir(context.Background(), artifactDir))

	logChunks := logStream.snapshotChunks()
	require.NotEmpty(t, logChunks)
	for _, chunk := range logChunks {
		assert.Equal(t, claimKey, chunk.AttemptKey)
	}
	artifactChunks := artifactStream.snapshotChunks()
	require.NotEmpty(t, artifactChunks)
	for _, chunk := range artifactChunks {
		assert.Equal(t, claimKey, chunk.AttemptKey)
	}
}

func TestFinalSchedulerLogStatusPusher_BoundsSchedulerLogFinalization(t *testing.T) {
	const (
		dagName  = "bounded-scheduler-log"
		dagRunID = "run-bounded-scheduler-log"
	)

	finalizer := newSchedulerLogFinalizer()
	finalizer.timeout = 20 * time.Millisecond
	entry := finalizer.register(remoteRunMetadata{
		dagRunID: dagRunID,
		dagName:  dagName,
		root:     ir.NewDAGRunRef(dagName, dagRunID),
	}, filepath.Join(t.TempDir(), "scheduler.log"))
	require.NotNil(t, entry)

	entered := make(chan struct{})
	done := make(chan error, 1)
	var once sync.Once
	entry.trackWriter(schedulerLogContextCloserFunc(func(ctx context.Context) error {
		once.Do(func() { close(entered) })
		<-ctx.Done()
		err := ctx.Err()
		done <- err
		return err
	}))

	statusPushed := make(chan struct{}, 1)
	statusPusher := &finalSchedulerLogStatusPusher{
		finalizer: schedulerLogStatusFinalizerFunc(func(ctx context.Context, _ ir.DAGRunStatus) (bool, error) {
			return entry.finalizeLog(ctx)
		}),
		pusher: &recordingStatusPusher{
			push: func(ctx context.Context, status ir.DAGRunStatus) error {
				require.Equal(t, dagRunID, status.DAGRunID)
				require.Equal(t, ir.Succeeded, status.Status)
				require.NoError(t, ctx.Err(), "terminal status should use a live context")
				statusPushed <- struct{}{}
				return nil
			},
		},
	}

	start := time.Now()
	require.NoError(t, statusPusher.Push(context.Background(), ir.DAGRunStatus{
		Root:     ir.NewDAGRunRef(dagName, dagRunID),
		Name:     dagName,
		DAGRunID: dagRunID,
		Status:   ir.Succeeded,
	}))
	require.Less(t, time.Since(start), time.Second)

	select {
	case <-entered:
	default:
		t.Fatal("scheduler log finalizer did not close the tracked writer")
	}
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	default:
		t.Fatal("scheduler log finalizer did not bound the close with a deadline")
	}
	select {
	case <-statusPushed:
	default:
		t.Fatal("terminal status was not pushed after scheduler log finalization timed out")
	}
}

func TestSchedulerLogFinalizerEntry_FinalizeBeforeWriterDoesNotConsumeOnce(t *testing.T) {
	const (
		dagName  = "late-scheduler-writer"
		dagRunID = "run-late-scheduler-writer"
	)

	finalizer := newSchedulerLogFinalizer()
	entry := finalizer.register(remoteRunMetadata{
		dagRunID: dagRunID,
		dagName:  dagName,
		root:     ir.NewDAGRunRef(dagName, dagRunID),
	}, filepath.Join(t.TempDir(), "scheduler.log"))
	require.NotNil(t, entry)

	ran, err := entry.finalizeLog(context.Background())
	require.NoError(t, err)
	require.False(t, ran)

	var closes int
	entry.trackWriter(schedulerLogContextCloserFunc(func(context.Context) error {
		closes++
		return nil
	}))

	ran, err = entry.finalizeLog(context.Background())
	require.NoError(t, err)
	require.True(t, ran)
	require.Equal(t, 1, closes)

	ran, err = entry.finalizeLog(context.Background())
	require.NoError(t, err)
	require.False(t, ran)
	require.Equal(t, 1, closes)
}

func TestRemoteRunReporter_SchedulerWriterCloseUsesLiveStream(t *testing.T) {
	const (
		dagName   = "local-scheduler-log"
		dagRunID  = "run-local-scheduler-log"
		attemptID = "attempt-local-scheduler-log"
	)

	logFilePath := filepath.Join(t.TempDir(), "scheduler.log")
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, logFile.Close())
	}()

	streamOpened := make(chan struct{}, 1)
	closeEntered := make(chan struct{}, 1)
	unblockClose := make(chan struct{})
	var unblockOnce sync.Once
	unblock := func() {
		unblockOnce.Do(func() {
			close(unblockClose)
		})
	}
	defer unblock()

	client := newMockRemoteCoordinatorClient()
	client.StreamLogsFunc = func(context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
		streamOpened <- struct{}{}
		stream := newMockStreamLogsClient()
		stream.closeAndRecvFunc = func() (*coordinatorv1.StreamLogsResponse, error) {
			closeEntered <- struct{}{}
			<-unblockClose
			return &coordinatorv1.StreamLogsResponse{}, nil
		}
		return stream, nil
	}

	reporter := newRemoteRunReporter(client, "worker-1", remoteRunMetadata{
		dagRunID:  dagRunID,
		dagName:   dagName,
		attemptID: attemptID,
		root:      ir.NewDAGRunRef(dagName, dagRunID),
	}, serviceregistry.HostInfo{})
	require.NotNil(t, reporter.EnableSchedulerFinalizer(logFilePath))

	schedulerWriter := reporter.NewSchedulerLogWriter(context.Background(), logFile)
	_, err = schedulerWriter.Write([]byte(strings.Repeat("x", 40*1024)))
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		done <- schedulerWriter.Close()
	}()

	select {
	case <-closeEntered:
		unblock()
		require.NoError(t, <-done)
	case <-time.After(time.Second):
		unblock()
		require.NoError(t, <-done)
		t.Fatal("scheduler writer close did not close a live stream")
	}

	select {
	case <-streamOpened:
	default:
		t.Fatal("remote scheduler writer did not open a live scheduler log stream")
	}
	select {
	case <-closeEntered:
		t.Fatal("remote scheduler writer closed a live scheduler log stream more than once")
	default:
	}
}

func TestRemoteRunReporter_MirrorsStepOutputIntoFinalSchedulerLog(t *testing.T) {
	const (
		dagName   = "mirrored-scheduler-log"
		dagRunID  = "run-mirrored-scheduler-log"
		attemptID = "attempt-mirrored-scheduler-log"
	)

	logFilePath := filepath.Join(t.TempDir(), "scheduler.log")
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, logFile.Close())
	}()

	var (
		streams   []*mockStreamLogsClient
		streamsMu sync.Mutex
	)
	client := newMockRemoteCoordinatorClient()
	client.StreamLogsFunc = func(context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
		stream := newMockStreamLogsClient()
		streamsMu.Lock()
		streams = append(streams, stream)
		streamsMu.Unlock()
		return stream, nil
	}

	reporter := newRemoteRunReporter(client, "worker-1", remoteRunMetadata{
		dagRunID:  dagRunID,
		dagName:   dagName,
		attemptID: attemptID,
		root:      ir.NewDAGRunRef(dagName, dagRunID),
	}, serviceregistry.HostInfo{})
	require.NotNil(t, reporter.EnableSchedulerFinalizer(logFilePath))

	schedulerWriter := reporter.NewSchedulerLogWriter(context.Background(), logFile)
	_, err = schedulerWriter.Write([]byte("scheduler-start\n"))
	require.NoError(t, err)

	stdoutWriter := reporter.NewStepWriter(context.Background(), "step-one", runctx.StreamTypeStdout)
	_, err = stdoutWriter.Write([]byte("mirrored-stdout\n"))
	require.NoError(t, err)
	require.NoError(t, stdoutWriter.Close())

	stderrWriter := reporter.NewStepWriter(context.Background(), "step-one", runctx.StreamTypeStderr)
	_, err = stderrWriter.Write([]byte("mirrored-stderr\n"))
	require.NoError(t, err)
	require.NoError(t, stderrWriter.Close())

	require.NoError(t, schedulerWriter.Close())
	require.NoError(t, reporter.StreamSchedulerLog(context.Background(), logFilePath))

	streamsMu.Lock()
	defer streamsMu.Unlock()
	require.NotEmpty(t, streams)
	var schedulerLog []byte
	for _, stream := range streams {
		for _, chunk := range stream.snapshotChunks() {
			if chunk.StreamType != coordinatorv1.LogStreamType_LOG_STREAM_TYPE_SCHEDULER || chunk.IsFinal {
				continue
			}
			schedulerLog = append(schedulerLog, chunk.Data...)
		}
	}
	require.Contains(t, string(schedulerLog), "scheduler-start")
	require.Contains(t, string(schedulerLog), "mirrored-stdout")
	require.Contains(t, string(schedulerLog), "mirrored-stderr")
}

func TestRemoteRunReporter_UsesRuntimeContextForChildLogsAndArtifactsWithoutMutatingRoot(t *testing.T) {
	const (
		rootName     = "root-dag"
		rootRunID    = "root-run-metadata"
		rootAttempt  = "root-attempt"
		childName    = "child-dag"
		childRunID   = "child-run-metadata"
		childAttempt = "child-attempt"
	)

	var (
		logStreams      []*mockStreamLogsClient
		logStreamsMu    sync.Mutex
		artifactStreams []*mockStreamArtifactsClient
		artifactMu      sync.Mutex
		events          []string
		eventsMu        sync.Mutex
	)
	record := func(event string) {
		eventsMu.Lock()
		defer eventsMu.Unlock()
		events = append(events, event)
	}

	client := newMockRemoteCoordinatorClient()
	client.StreamLogsFunc = func(context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
		stream := newMockStreamLogsClient()
		logStreamsMu.Lock()
		logStreams = append(logStreams, stream)
		logStreamsMu.Unlock()
		return stream, nil
	}
	client.StreamArtifactsFunc = func(context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
		stream := newMockStreamArtifactsClient()
		artifactMu.Lock()
		artifactStreams = append(artifactStreams, stream)
		artifactMu.Unlock()
		return stream, nil
	}

	rootRef := ir.NewDAGRunRef(rootName, rootRunID)
	reporter := newRemoteRunReporter(client, "worker-1", remoteRunMetadata{
		dagRunID:  rootRunID,
		dagName:   rootName,
		attemptID: rootAttempt,
		root:      rootRef,
	}, serviceregistry.HostInfo{})
	require.NotNil(t, reporter.EnableSchedulerFinalizer(filepath.Join(t.TempDir(), "scheduler.log")))

	rootWriter := reporter.NewStepWriter(context.Background(), "root-step", runctx.StreamTypeStdout)
	_, err := rootWriter.Write([]byte("root output"))
	require.NoError(t, err)
	require.NoError(t, rootWriter.Close())

	childDAG := &ir.DAG{Name: childName}
	childCtx := dagruntime.NewContext(context.Background(), childDAG, childRunID, "", dagruntime.WithAttemptID(childAttempt), dagruntime.WithRootDAGRun(rootRef))
	childWriter := reporter.NewStepWriter(childCtx, "child-step", runctx.StreamTypeStdout)
	_, err = childWriter.Write([]byte("child output"))
	require.NoError(t, err)
	require.NoError(t, childWriter.Close())

	childLogFile := filepath.Join(t.TempDir(), childRunID+".log")
	childLog, err := os.OpenFile(childLogFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, childLog.Close())
	}()
	childSchedulerWriter := reporter.NewSchedulerLogWriter(childCtx, childLog)
	_, err = childSchedulerWriter.Write([]byte("child scheduler\n"))
	require.NoError(t, err)
	statusPusher := &finalSchedulerLogStatusPusher{
		finalizer: reporter,
		pusher: &recordingStatusPusher{
			push: func(ctx context.Context, status ir.DAGRunStatus) error {
				require.NoError(t, ctx.Err())
				require.Equal(t, childRunID, status.DAGRunID)
				require.Equal(t, childAttempt, status.AttemptID)
				logStreamsMu.Lock()
				defer logStreamsMu.Unlock()
				require.True(t, hasSchedulerFinalChunk(logStreams, childRunID, childName, childAttempt, rootRef), "child scheduler log should be finalized before child terminal status")
				record("child-terminal-status")
				return nil
			},
		},
	}
	require.NoError(t, statusPusher.Push(childCtx, ir.DAGRunStatus{
		Root:      rootRef,
		Name:      childName,
		DAGRunID:  childRunID,
		AttemptID: childAttempt,
		Status:    ir.Succeeded,
		Log:       childLogFile,
	}))

	artifactDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(artifactDir, "out.txt"), []byte("artifact"), 0o600))
	require.NoError(t, reporter.Finalize(childCtx, childAttempt, artifactDir))

	logStreamsMu.Lock()
	defer logStreamsMu.Unlock()
	require.True(t, hasLogChunk(logStreams, rootRunID, rootName, rootAttempt, rootRef, "root-step"), "root step logs should keep root runtime metadata")
	require.True(t, hasLogChunk(logStreams, childRunID, childName, childAttempt, rootRef, "child-step"), "child step logs should use child runtime metadata")
	require.True(t, hasSchedulerFinalChunk(logStreams, childRunID, childName, childAttempt, rootRef), "child scheduler final marker should use child runtime metadata")

	artifactMu.Lock()
	defer artifactMu.Unlock()
	require.True(t, hasArtifactChunk(artifactStreams, childRunID, childName, childAttempt, rootRef, "out.txt"), "child artifacts should use child runtime metadata")
	require.Equal(t, []string{"child-terminal-status"}, events)
}

func TestHandleStart_InitFailureFinalizesSchedulerLogBeforeTerminalStatus(t *testing.T) {
	const dagRunID = "run-init-failure-final-scheduler-log"

	dagContent := `name: init-failure-final-log
tools:
  packages:
    - name: jq
      package: jqlang/jq
      version: jq-1.7.1
      commands: [jq]
steps:
  - name: echo-step
    run: echo "should not run"
`

	var (
		streamsMu sync.Mutex
		streams   []*mockStreamLogsClient
	)

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()

	client := newMockRemoteCoordinatorClient()
	client.StreamLogsFunc = func(ctx context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
		stream := newMockStreamLogsClient()
		stream.ctx = ctx
		stream.closeAndRecvFunc = func() (*coordinatorv1.StreamLogsResponse, error) {
			cancelRun()
			return &coordinatorv1.StreamLogsResponse{}, nil
		}

		streamsMu.Lock()
		streams = append(streams, stream)
		streamsMu.Unlock()

		return stream, nil
	}

	var terminalStatusSeen bool
	client.ReportStatusFunc = func(ctx context.Context, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
		status, err := convert.ProtoToDAGRunStatus(req.Status)
		require.NoError(t, err)
		if status.Status != ir.Failed {
			return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
		}
		terminalStatusSeen = true
		require.NoError(t, ctx.Err(), "init failure terminal status should use a live context")

		streamsMu.Lock()
		defer streamsMu.Unlock()
		foundSchedulerReplay := false
		for _, stream := range streams {
			for _, chunk := range stream.snapshotChunks() {
				if chunk.StreamType == coordinatorv1.LogStreamType_LOG_STREAM_TYPE_SCHEDULER && chunk.IsFinal {
					foundSchedulerReplay = true
				}
			}
		}
		require.True(t, foundSchedulerReplay, "scheduler log should be finalized before init failure status is reported")

		return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
	}

	handler := &remoteTaskHandler{
		workerID:          "integration-test-worker",
		coordinatorClient: client,
		config: &config.Config{
			Paths: config.PathsConfig{
				DAGsDir: t.TempDir(),
			},
		},
	}

	err := handler.handleStart(runCtx, &coordinatorv1.Task{
		Target:       "init-failure-final-log",
		Definition:   dagContent,
		DagRunId:     dagRunID,
		RootDagRunId: dagRunID,
		AttemptId:    "attempt-init-failure",
	}, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "tools dir is required")
	require.True(t, terminalStatusSeen)
}

func TestExecuteDAGRun_FinalSchedulerLogStreamsBeforeTerminalStatus(t *testing.T) {
	th := test.Setup(t)

	dagContent := `name: remote-handler-final-scheduler-log
steps:
  - name: echo-step
    run: echo "final scheduler stream"
`
	dag := th.DAG(t, dagContent)

	type capturedStream struct {
		openErr error
		stream  *mockStreamLogsClient
	}

	var (
		streamsMu sync.Mutex
		streams   []capturedStream
	)

	client := newMockRemoteCoordinatorClient()
	client.StreamLogsFunc = func(ctx context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
		stream := newMockStreamLogsClient()
		stream.ctx = ctx

		streamsMu.Lock()
		streams = append(streams, capturedStream{
			openErr: ctx.Err(),
			stream:  stream,
		})
		streamsMu.Unlock()

		return stream, nil
	}

	runCtx, cancelRun := context.WithCancel(th.Context)
	defer cancelRun()

	var terminalStatusSeen bool
	var terminalStatusReports int
	client.ReportStatusFunc = func(_ context.Context, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
		status, err := convert.ProtoToDAGRunStatus(req.Status)
		require.NoError(t, err)

		if status.Status != ir.Succeeded {
			return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
		}
		terminalStatusSeen = true
		terminalStatusReports++

		streamsMu.Lock()
		defer streamsMu.Unlock()

		var schedulerStreamOpenErr error
		foundSchedulerFinal := false
		for _, captured := range streams {
			for _, chunk := range captured.stream.snapshotChunks() {
				if chunk.StreamType == coordinatorv1.LogStreamType_LOG_STREAM_TYPE_SCHEDULER && chunk.IsFinal {
					require.Equal(t, status.AttemptID, chunk.AttemptId)
					schedulerStreamOpenErr = captured.openErr
					foundSchedulerFinal = true
				}
			}
		}

		require.True(t, foundSchedulerFinal, "scheduler log should be finalized before terminal status is reported")
		require.NoError(t, schedulerStreamOpenErr, "scheduler stream should open with a live context")

		cancelRun()

		return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
	}

	handler := &remoteTaskHandler{
		workerID:          "integration-test-worker",
		coordinatorClient: client,
		dagRepository:     th.DAGRepository,
		dagRunMgr:         th.DAGRunMgr,
		serviceRegistry:   th.ServiceRegistry,
		peerConfig:        config.Peer{Insecure: true},
		config:            th.Config,
	}

	dagRunID := "run-final-scheduler-log"
	root := ir.DAGRunRef{Name: dag.Name, ID: dagRunID}
	run := remoteRun{
		task: &coordinatorv1.Task{DagRunId: dagRunID},
		root: root,
	}
	run.handlers = handler.createRemoteHandlers(run, dag.Name)

	err := handler.executeDAGRun(runCtx, dag.DAG, run)
	require.NoError(t, err)
	require.True(t, terminalStatusSeen)
	require.Equal(t, 1, terminalStatusReports)
}

func TestExecuteDAGRun_DispatchesRemoteSubDAGThroughCoordinatorClient(t *testing.T) {
	th := test.Setup(t)

	rootDAG := th.DAG(t, `name: worker-parent
steps:
  - name: run-child
    action: dag.run
    with:
      dag: worker-child
`)
	childDAG := `name: worker-child
worker_selector:
  os: windows
steps:
  - name: child-step
    run: echo child
`

	root := ir.NewDAGRunRef(rootDAG.Name, "run-remote-subdag-dispatch")
	var dispatchedTask *dispatch.DispatchTask
	client := newMockRemoteCoordinatorClient()
	client.GetDAGFunc = func(_ context.Context, name string) (string, error) {
		require.Equal(t, "worker-child", name)
		return childDAG, nil
	}
	client.DispatchFunc = func(_ context.Context, task *dispatch.DispatchTask) error {
		require.Equal(t, "worker-child", task.Target)
		require.Equal(t, map[string]string{"os": "windows"}, task.WorkerSelector)
		require.Equal(t, root.Name, task.RootDAGRunName)
		require.Equal(t, root.ID, task.RootDAGRunID)
		require.Equal(t, root.Name, task.ParentDAGRunName)
		require.Equal(t, root.ID, task.ParentDAGRunID)
		dispatchedTask = task
		return nil
	}
	client.GetDAGRunStatusFunc = func(_ context.Context, dagName, dagRunID string, rootRef *ir.DAGRunRef) (*dispatch.DAGRunStatusResult, error) {
		if dispatchedTask == nil {
			return &dispatch.DAGRunStatusResult{Found: false}, nil
		}
		require.Equal(t, dispatchedTask.Target, dagName)
		require.Equal(t, dispatchedTask.DAGRunID, dagRunID)
		require.Equal(t, &root, rootRef)
		return &dispatch.DAGRunStatusResult{
			Found: true,
			Status: &ir.DAGRunStatus{
				Name:     dagName,
				DAGRunID: dagRunID,
				Status:   ir.Succeeded,
			},
		}, nil
	}

	handler := &remoteTaskHandler{
		workerID:          "integration-test-worker",
		coordinatorClient: client,
		dagRunMgr:         th.DAGRunMgr,
		config:            th.Config,
	}

	run := remoteRun{
		task: &coordinatorv1.Task{DagRunId: root.ID},
		root: root,
	}
	run.handlers = handler.createRemoteHandlers(run, rootDAG.Name)

	err := handler.executeDAGRun(th.Context, rootDAG.DAG, run)
	require.NoError(t, err)
	require.NotNil(t, dispatchedTask)
}

func TestExecuteDAGRun_LocalSubDAGKeepsRemoteLoaderForNestedDispatch(t *testing.T) {
	th := test.Setup(t)

	rootDAG := th.DAG(t, `name: worker-root
steps:
  - name: run-stage
    action: dag.run
    with:
      dag: worker-stage
`)
	stageDAG := `name: worker-stage
steps:
  - name: run-child
    action: dag.run
    with:
      dag: worker-child
`
	childDAG := `name: worker-child
worker_selector:
  os: windows
steps:
  - name: child-step
    run: echo child
`

	root := ir.NewDAGRunRef(rootDAG.Name, "run-nested-remote-subdag")
	var dispatchedTask *dispatch.DispatchTask
	client := newMockRemoteCoordinatorClient()
	client.GetDAGFunc = func(_ context.Context, name string) (string, error) {
		switch name {
		case "worker-stage":
			return stageDAG, nil
		case "worker-child":
			return childDAG, nil
		default:
			return "", fmt.Errorf("unexpected DAG %q", name)
		}
	}
	client.DispatchFunc = func(_ context.Context, task *dispatch.DispatchTask) error {
		require.Equal(t, "worker-child", task.Target)
		require.Equal(t, map[string]string{"os": "windows"}, task.WorkerSelector)
		require.Equal(t, root.Name, task.RootDAGRunName)
		require.Equal(t, root.ID, task.RootDAGRunID)
		require.Equal(t, "worker-stage", task.ParentDAGRunName)
		require.NotEmpty(t, task.ParentDAGRunID)
		require.NotEqual(t, root.ID, task.ParentDAGRunID)
		dispatchedTask = task
		return nil
	}
	client.GetDAGRunStatusFunc = func(_ context.Context, dagName, dagRunID string, rootRef *ir.DAGRunRef) (*dispatch.DAGRunStatusResult, error) {
		if dispatchedTask == nil {
			return &dispatch.DAGRunStatusResult{Found: false}, nil
		}
		require.Equal(t, dispatchedTask.Target, dagName)
		require.Equal(t, dispatchedTask.DAGRunID, dagRunID)
		require.Equal(t, &root, rootRef)
		return &dispatch.DAGRunStatusResult{
			Found: true,
			Status: &ir.DAGRunStatus{
				Name:     dagName,
				DAGRunID: dagRunID,
				Status:   ir.Succeeded,
			},
		}, nil
	}

	handler := &remoteTaskHandler{
		workerID:          "integration-test-worker",
		coordinatorClient: client,
		dagRunMgr:         th.DAGRunMgr,
		config:            th.Config,
	}

	run := remoteRun{
		task: &coordinatorv1.Task{DagRunId: root.ID},
		root: root,
	}
	run.handlers = handler.createRemoteHandlers(run, rootDAG.Name)

	err := handler.executeDAGRun(th.Context, rootDAG.DAG, run)
	require.NoError(t, err)
	require.NotNil(t, dispatchedTask)
}

func TestExecuteDAGRun_FinalSchedulerLogUsesChildMetadataForLocalSubDAG(t *testing.T) {
	th := test.Setup(t)

	childCommand := `printf "child stdout\n"
printf "child stderr\n" >&2`
	childShell := "/bin/sh"
	if runtime.GOOS == "windows" {
		childCommand = `[Console]::Out.WriteLine('child stdout')
[Console]::Error.WriteLine('child stderr')`
		childShell = "powershell"
	}
	indentedChildCommand := "      " + strings.ReplaceAll(childCommand, "\n", "\n      ")

	const childDAGName = "worker-child-final-log"

	dagContent := fmt.Sprintf(`name: worker-subdag-final-log
steps:
  - name: run-child
    action: dag.run
    with:
      dag: %s
---
name: %s
worker_selector: local
steps:
  - name: child-step
    run: |
%s
    with:
      shell: %s
`, childDAGName, childDAGName, indentedChildCommand, childShell)
	dag := th.DAG(t, dagContent)

	var (
		streamsMu sync.Mutex
		streams   []*mockStreamLogsClient

		childTerminalSeen bool
		childRunID        string
		childAttemptID    string
	)

	client := newMockRemoteCoordinatorClient()
	client.StreamLogsFunc = func(ctx context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
		stream := newMockStreamLogsClient()
		stream.ctx = ctx

		streamsMu.Lock()
		streams = append(streams, stream)
		streamsMu.Unlock()

		return stream, nil
	}

	root := ir.NewDAGRunRef(dag.Name, "run-local-subdag-final-scheduler-log")
	client.ReportStatusFunc = func(_ context.Context, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
		status, err := convert.ProtoToDAGRunStatus(req.Status)
		require.NoError(t, err)

		if status.Name != childDAGName || status.Status != ir.Succeeded {
			return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
		}

		childTerminalSeen = true
		childRunID = status.DAGRunID
		childAttemptID = status.AttemptID

		require.NotEmpty(t, childRunID)
		require.NotEqual(t, root.ID, childRunID)
		require.NotEmpty(t, childAttemptID)
		require.Equal(t, root, status.Root)
		require.Equal(t, ir.NewDAGRunRef(dag.Name, root.ID), status.Parent)

		streamsMu.Lock()
		defer streamsMu.Unlock()
		require.True(t, hasSchedulerDataChunk(streams, childRunID, status.Name, childAttemptID, root), "child scheduler log data should be streamed before child terminal status")
		require.True(t, hasSchedulerFinalChunk(streams, childRunID, status.Name, childAttemptID, root), "child scheduler final marker should be sent before child terminal status")

		return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
	}

	handler := &remoteTaskHandler{
		workerID:          "integration-test-worker",
		coordinatorClient: client,
		dagRepository:     th.DAGRepository,
		dagRunMgr:         th.DAGRunMgr,
		serviceRegistry:   th.ServiceRegistry,
		peerConfig:        config.Peer{Insecure: true},
		config:            th.Config,
	}

	require.NoError(t, os.MkdirAll(th.Config.Paths.LogDir, 0o750))

	run := remoteRun{
		task: &coordinatorv1.Task{DagRunId: root.ID},
		root: root,
	}
	run.handlers = handler.createRemoteHandlers(run, dag.Name)
	err := handler.executeDAGRun(th.Context, dag.DAG, run)
	require.NoError(t, err)
	require.True(t, childTerminalSeen)

	streamsMu.Lock()
	defer streamsMu.Unlock()
	require.True(t, hasStepLogDataChunk(streams, childRunID, childDAGName, childAttemptID, root, "child-step", coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDOUT), "child stdout chunks should use child metadata")
	require.True(t, hasStepLogDataChunk(streams, childRunID, childDAGName, childAttemptID, root, "child-step", coordinatorv1.LogStreamType_LOG_STREAM_TYPE_STDERR), "child stderr chunks should use child metadata")
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
		dagRepository:     th.DAGRepository,
		dagRunMgr:         th.DAGRunMgr,
		serviceRegistry:   th.ServiceRegistry,
		peerConfig:        config.Peer{Insecure: true},
		config:            th.Config,
	}

	dagRunID := "run-failure-artifacts-1"
	root := ir.DAGRunRef{Name: dag.Name, ID: dagRunID}
	err := handler.executeDAGRun(th.Context, dag.DAG, remoteRun{
		task: &coordinatorv1.Task{DagRunId: dagRunID},
		root: root,
		handlers: runHandlers{
			status:    coordreport.NewStatusPusher(client, "integration-test-worker", ""),
			logs:      coordreport.NewLogStreamer(client, "integration-test-worker", dagRunID, dag.Name, "", root),
			artifacts: coordreport.NewArtifactUploader(client, "integration-test-worker", dagRunID, dag.Name, "", root),
		},
	})
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

	var reported []ir.DAGRunStatus
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
		dagRepository:     th.DAGRepository,
		dagRunMgr:         th.DAGRunMgr,
		serviceRegistry:   th.ServiceRegistry,
		peerConfig:        config.Peer{Insecure: true},
		config:            th.Config,
	}

	dagRunID := "run-upload-failure-1"
	root := ir.DAGRunRef{Name: dag.Name, ID: dagRunID}
	err := handler.executeDAGRun(th.Context, dag.DAG, remoteRun{
		task: &coordinatorv1.Task{DagRunId: dagRunID},
		root: root,
		handlers: runHandlers{
			status:    coordreport.NewStatusPusher(client, "integration-test-worker", ""),
			logs:      coordreport.NewLogStreamer(client, "integration-test-worker", dagRunID, dag.Name, "", root),
			artifacts: coordreport.NewArtifactUploader(client, "integration-test-worker", dagRunID, dag.Name, "", root),
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upload artifacts")
	reportedMu.Lock()
	require.NotEmpty(t, reported)

	final := reported[len(reported)-1]
	reportedMu.Unlock()
	assert.Equal(t, ir.Failed, final.Status)
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

	var reported []ir.DAGRunStatus
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
		dagRepository:     th.DAGRepository,
		dagRunMgr:         th.DAGRunMgr,
		serviceRegistry:   th.ServiceRegistry,
		peerConfig:        config.Peer{Insecure: true},
		config:            th.Config,
	}

	dagRunID := "run-failure-upload-failure-1"
	root := ir.DAGRunRef{Name: dag.Name, ID: dagRunID}
	err := handler.executeDAGRun(th.Context, dag.DAG, remoteRun{
		task: &coordinatorv1.Task{DagRunId: dagRunID},
		root: root,
		handlers: runHandlers{
			status:    coordreport.NewStatusPusher(client, "integration-test-worker", ""),
			logs:      coordreport.NewLogStreamer(client, "integration-test-worker", dagRunID, dag.Name, "", root),
			artifacts: coordreport.NewArtifactUploader(client, "integration-test-worker", dagRunID, dag.Name, "", root),
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upload artifacts")
	reportedMu.Lock()
	require.NotEmpty(t, reported)

	final := reported[len(reported)-1]
	reportedMu.Unlock()
	assert.Equal(t, ir.Failed, final.Status)
	assert.Contains(t, final.Error, "failed to upload artifacts")
}
