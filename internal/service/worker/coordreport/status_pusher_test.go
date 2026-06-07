// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordreport_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/backoff"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/proto/convert"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	"github.com/dagucloud/dagu/internal/service/worker/coordreport"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Compile-time interface check
var _ coordinator.Client = (*mockCoordinatorClient)(nil)

// mockCoordinatorClient is a minimal mock for testing StatusPusher.
// Only ReportStatus is used by StatusPusher; other methods panic if called.
type mockCoordinatorClient struct {
	reportStatusFunc     func(context.Context, *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error)
	reportStatusToFunc   func(context.Context, exec.HostInfo, *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error)
	reportStatusToCalled bool
}

func (m *mockCoordinatorClient) ReportStatus(ctx context.Context, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
	if m.reportStatusFunc != nil {
		return m.reportStatusFunc(ctx, req)
	}
	return nil, errors.New("ReportStatus not configured")
}

// Stub methods for interface compliance - panic if called unexpectedly
func (m *mockCoordinatorClient) Dispatch(_ context.Context, _ *exec.DispatchTask) error {
	panic("Dispatch not implemented in mock")
}

func (m *mockCoordinatorClient) Poll(_ context.Context, _ backoff.RetryPolicy, _ *coordinatorv1.PollRequest) (*coordinatorv1.Task, error) {
	panic("Poll not implemented in mock")
}

func (m *mockCoordinatorClient) GetWorkers(_ context.Context) ([]*coordinatorv1.WorkerInfo, error) {
	panic("GetWorkers not implemented in mock")
}

func (m *mockCoordinatorClient) Heartbeat(_ context.Context, _ *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error) {
	panic("Heartbeat not implemented in mock")
}

func (m *mockCoordinatorClient) AckTaskClaimTo(_ context.Context, _ exec.HostInfo, _ *coordinatorv1.AckTaskClaimRequest) (*coordinatorv1.AckTaskClaimResponse, error) {
	panic("AckTaskClaimTo not implemented in mock")
}

func (m *mockCoordinatorClient) RunHeartbeatTo(_ context.Context, _ exec.HostInfo, _ *coordinatorv1.RunHeartbeatRequest) (*coordinatorv1.RunHeartbeatResponse, error) {
	panic("RunHeartbeatTo not implemented in mock")
}

func (m *mockCoordinatorClient) StreamLogs(_ context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
	panic("StreamLogs not implemented in mock")
}

func (m *mockCoordinatorClient) ReportStatusTo(ctx context.Context, owner exec.HostInfo, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
	m.reportStatusToCalled = true
	if m.reportStatusToFunc != nil {
		return m.reportStatusToFunc(ctx, owner, req)
	}
	return m.ReportStatus(ctx, req)
}

func (m *mockCoordinatorClient) StreamLogsTo(ctx context.Context, _ exec.HostInfo) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
	return m.StreamLogs(ctx)
}

func (m *mockCoordinatorClient) StreamArtifacts(_ context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
	panic("StreamArtifacts not implemented in mock")
}

func (m *mockCoordinatorClient) StreamArtifactsTo(ctx context.Context, _ exec.HostInfo) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
	return m.StreamArtifacts(ctx)
}

func (m *mockCoordinatorClient) Metrics() coordinator.Metrics {
	return coordinator.Metrics{}
}

func (m *mockCoordinatorClient) Cleanup(_ context.Context) error {
	return nil
}

func (m *mockCoordinatorClient) GetDAGRunStatus(_ context.Context, _, _ string, _ *exec.DAGRunRef) (*exec.DAGRunStatusResult, error) {
	panic("GetDAGRunStatus not implemented in mock")
}

func (m *mockCoordinatorClient) GetDAG(_ context.Context, _ string) (string, error) {
	panic("GetDAG not implemented in mock")
}

func (m *mockCoordinatorClient) RequestCancel(_ context.Context, _, _ string, _ *exec.DAGRunRef) error {
	panic("RequestCancel not implemented in mock")
}

func TestNewStatusPusher(t *testing.T) {
	t.Parallel()

	client := &mockCoordinatorClient{}
	pusher := coordreport.NewStatusPusher(client, "worker-123")

	require.NotNil(t, pusher)
	snapshot := coordreport.SnapshotStatusPusher(pusher)
	assert.Equal(t, "worker-123", snapshot.WorkerID)
	assert.Equal(t, client, snapshot.Client)
}

func TestPush(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()

		var capturedReq *coordinatorv1.ReportStatusRequest
		client := &mockCoordinatorClient{
			reportStatusFunc: func(_ context.Context, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
				capturedReq = req
				return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
			},
		}

		pusher := coordreport.NewStatusPusher(client, "worker-1")
		status := exec.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   core.Running,
		}

		err := pusher.Push(context.Background(), status)

		require.NoError(t, err)
		require.NotNil(t, capturedReq)
		assert.Equal(t, "worker-1", capturedReq.WorkerId)
		assert.NotNil(t, capturedReq.Status)
		assert.NotEmpty(t, capturedReq.Status.JsonData)
		// Verify the JSON contains the expected data
		s, convErr := convert.ProtoToDAGRunStatus(capturedReq.Status)
		require.NoError(t, convErr)
		require.NotNil(t, s)
		assert.Equal(t, "run-123", s.DAGRunID)
	})

	t.Run("Rejected", func(t *testing.T) {
		t.Parallel()

		client := &mockCoordinatorClient{
			reportStatusFunc: func(_ context.Context, _ *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
				return &coordinatorv1.ReportStatusResponse{
					Accepted: false,
					Error:    "duplicate status",
				}, nil
			},
		}

		pusher := coordreport.NewStatusPusher(client, "worker-1")
		status := exec.DAGRunStatus{Name: "test-dag", DAGRunID: "run-123"}

		err := pusher.Push(context.Background(), status)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "status rejected")
		assert.Contains(t, err.Error(), "duplicate status")
		var rejectedErr *coordreport.AttemptRejectedError
		require.ErrorAs(t, err, &rejectedErr)
		assert.Equal(t, "duplicate status", rejectedErr.Reason)
	})

	t.Run("RejectedNoMessage", func(t *testing.T) {
		t.Parallel()

		client := &mockCoordinatorClient{
			reportStatusFunc: func(_ context.Context, _ *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
				return &coordinatorv1.ReportStatusResponse{Accepted: false, Error: ""}, nil
			},
		}

		pusher := coordreport.NewStatusPusher(client, "worker-1")
		err := pusher.Push(context.Background(), exec.DAGRunStatus{})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "status rejected")
		var rejectedErr *coordreport.AttemptRejectedError
		require.ErrorAs(t, err, &rejectedErr)
		assert.Empty(t, rejectedErr.Reason)
	})

	t.Run("NilResponse", func(t *testing.T) {
		t.Parallel()

		client := &mockCoordinatorClient{
			reportStatusFunc: func(_ context.Context, _ *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
				return nil, nil
			},
		}

		pusher := coordreport.NewStatusPusher(client, "worker-1")
		err := pusher.Push(context.Background(), exec.DAGRunStatus{})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil response")
	})

	t.Run("ClientError", func(t *testing.T) {
		t.Parallel()

		client := &mockCoordinatorClient{
			reportStatusFunc: func(_ context.Context, _ *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
				return nil, errors.New("connection refused")
			},
		}

		pusher := coordreport.NewStatusPusher(client, "worker-1")
		err := pusher.Push(context.Background(), exec.DAGRunStatus{})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to report status")
		assert.Contains(t, err.Error(), "connection refused")
	})

	t.Run("ContextCancelled", func(t *testing.T) {
		t.Parallel()

		client := &mockCoordinatorClient{
			reportStatusFunc: func(ctx context.Context, _ *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
				return nil, ctx.Err()
			},
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		pusher := coordreport.NewStatusPusher(client, "worker-1")
		err := pusher.Push(ctx, exec.DAGRunStatus{})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "context canceled")
	})

	t.Run("ComplexStatus", func(t *testing.T) {
		t.Parallel()

		var capturedReq *coordinatorv1.ReportStatusRequest
		client := &mockCoordinatorClient{
			reportStatusFunc: func(_ context.Context, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
				capturedReq = req
				return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
			},
		}

		status := exec.DAGRunStatus{
			Name:       "complex-dag",
			DAGRunID:   "run-456",
			AttemptID:  "attempt-1",
			Status:     core.Succeeded,
			WorkerID:   "other-worker",
			PID:        12345,
			StartedAt:  "2024-01-01T00:00:00Z",
			FinishedAt: "2024-01-01T00:05:00Z",
			Params:     "key=value",
			Root:       exec.DAGRunRef{Name: "root", ID: "root-id"},
			Parent:     exec.DAGRunRef{Name: "parent", ID: "parent-id"},
			Nodes: []*exec.Node{
				{
					Step:   core.Step{Name: "step-1"},
					Status: core.NodeSucceeded,
				},
			},
		}

		pusher := coordreport.NewStatusPusher(client, "worker-1")
		err := pusher.Push(context.Background(), status)

		require.NoError(t, err)
		require.NotNil(t, capturedReq)
		require.NotNil(t, capturedReq.Status)

		// Verify complex fields were converted via JSON
		s, convErr := convert.ProtoToDAGRunStatus(capturedReq.Status)
		require.NoError(t, convErr)
		require.NotNil(t, s)
		assert.Equal(t, "complex-dag", s.Name)
		assert.Equal(t, "attempt-1", s.AttemptID)
		assert.Equal(t, core.Succeeded, s.Status)
		assert.False(t, s.Root.Zero())
		assert.False(t, s.Parent.Zero())
		assert.Len(t, s.Nodes, 1)
	})

	t.Run("OwnerScopedPushUsesReportStatusTo", func(t *testing.T) {
		t.Parallel()

		client := &mockCoordinatorClient{
			reportStatusToFunc: func(_ context.Context, owner exec.HostInfo, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
				assert.Equal(t, exec.HostInfo{ID: "coord-1", Host: "127.0.0.1", Port: 4321}, owner)
				assert.Equal(t, "coord-1", req.OwnerCoordinatorId)
				return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
			},
		}

		pusher := coordreport.NewStatusPusher(client, "worker-1", exec.HostInfo{ID: "coord-1", Host: "127.0.0.1", Port: 4321})
		err := pusher.Push(context.Background(), exec.DAGRunStatus{Name: "test-dag", DAGRunID: "run-owner"})

		require.NoError(t, err)
		assert.True(t, client.reportStatusToCalled)
	})
}
