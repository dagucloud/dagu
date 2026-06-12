// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"testing"

	openapiv1 "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/backoff"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var _ coordinator.Client = (*stubCoordinatorClient)(nil)

type stubCoordinatorClient struct {
	workers []*coordinatorv1.WorkerInfo
	err     error
}

func (s *stubCoordinatorClient) Dispatch(context.Context, *exec.DispatchTask) error {
	return nil
}

func (s *stubCoordinatorClient) Cleanup(context.Context) error {
	return nil
}

func (s *stubCoordinatorClient) GetDAGRunStatus(context.Context, string, string, *exec.DAGRunRef) (*exec.DAGRunStatusResult, error) {
	return nil, nil
}

func (s *stubCoordinatorClient) RequestCancel(context.Context, string, string, *exec.DAGRunRef) error {
	return nil
}

func (s *stubCoordinatorClient) Poll(context.Context, backoff.RetryPolicy, *coordinatorv1.PollRequest) (*coordinatorv1.Task, error) {
	return nil, nil
}

func (s *stubCoordinatorClient) GetWorkers(context.Context) ([]*coordinatorv1.WorkerInfo, error) {
	return s.workers, s.err
}

func (s *stubCoordinatorClient) Heartbeat(context.Context, *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error) {
	return nil, nil
}

func (s *stubCoordinatorClient) AckTaskClaimTo(context.Context, exec.HostInfo, *coordinatorv1.AckTaskClaimRequest) (*coordinatorv1.AckTaskClaimResponse, error) {
	return &coordinatorv1.AckTaskClaimResponse{Accepted: true}, nil
}

func (s *stubCoordinatorClient) RunHeartbeatTo(context.Context, exec.HostInfo, *coordinatorv1.RunHeartbeatRequest) (*coordinatorv1.RunHeartbeatResponse, error) {
	return &coordinatorv1.RunHeartbeatResponse{}, nil
}

func (s *stubCoordinatorClient) ReportStatus(context.Context, *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
	return nil, nil
}

func (s *stubCoordinatorClient) ReportStatusTo(ctx context.Context, _ exec.HostInfo, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
	return s.ReportStatus(ctx, req)
}

func (s *stubCoordinatorClient) StreamLogs(context.Context) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
	return nil, errors.New("not implemented")
}

func (s *stubCoordinatorClient) StreamLogsTo(ctx context.Context, _ exec.HostInfo) (coordinatorv1.CoordinatorService_StreamLogsClient, error) {
	return s.StreamLogs(ctx)
}

func (s *stubCoordinatorClient) StreamArtifacts(context.Context) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
	return nil, errors.New("not implemented")
}

func (s *stubCoordinatorClient) StreamArtifactsTo(ctx context.Context, _ exec.HostInfo) (coordinatorv1.CoordinatorService_StreamArtifactsClient, error) {
	return s.StreamArtifacts(ctx)
}

func (s *stubCoordinatorClient) Metrics() coordinator.Metrics {
	return coordinator.Metrics{}
}

func (s *stubCoordinatorClient) GetDAG(_ context.Context, _ string) (string, error) {
	return "", errors.New("not implemented")
}

func TestAPIGetWorkers_ReturnsPartialResultsWithErrors(t *testing.T) {
	t.Parallel()

	api := &API{
		coordinatorCli: &stubCoordinatorClient{
			workers: []*coordinatorv1.WorkerInfo{
				{
					WorkerId:        "worker-1",
					LastHeartbeatAt: 1710000000,
				},
			},
			err: errors.New("partial failure getting workers: coordinator unavailable"),
		},
	}

	resp, err := api.GetWorkers(context.Background(), openapiv1.GetWorkersRequestObject{})
	require.NoError(t, err)

	okResp, ok := resp.(openapiv1.GetWorkers200JSONResponse)
	require.True(t, ok)
	require.Len(t, okResp.Workers, 1)
	require.Equal(t, "worker-1", okResp.Workers[0].Id)
	require.Equal(t, []string{"partial failure getting workers: coordinator unavailable"}, okResp.Errors)
}

func TestAPIGetWorkers_ReturnsUnavailableWhenNoResults(t *testing.T) {
	t.Parallel()

	api := &API{
		coordinatorCli: &stubCoordinatorClient{
			err: status.Error(codes.Unavailable, "coordinator unavailable"),
		},
	}

	resp, err := api.GetWorkers(context.Background(), openapiv1.GetWorkersRequestObject{})
	require.NoError(t, err)

	_, ok := resp.(openapiv1.GetWorkers503JSONResponse)
	require.True(t, ok)
}
