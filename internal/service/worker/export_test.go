// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"errors"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/proto/convert"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

type captureCoordinatorClientForTest struct {
	coordinator.Client
	status *exec.DAGRunStatus
	err    error
}

func (c *captureCoordinatorClientForTest) ReportStatus(_ context.Context, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
	c.status, c.err = convert.ProtoToDAGRunStatus(req.Status)
	return &coordinatorv1.ReportStatusResponse{Accepted: true}, nil
}

func (c *captureCoordinatorClientForTest) ReportStatusTo(ctx context.Context, _ exec.HostInfo, req *coordinatorv1.ReportStatusRequest) (*coordinatorv1.ReportStatusResponse, error) {
	return c.ReportStatus(ctx, req)
}

type captureStatusPusherForTest struct {
	status *exec.DAGRunStatus
}

func (p *captureStatusPusherForTest) Push(_ context.Context, status exec.DAGRunStatus) error {
	copied := status
	p.status = &copied
	return nil
}

// ReportTaskLoadFailureStatusForTest returns the status emitted for a task load failure.
func ReportTaskLoadFailureStatusForTest(ctx context.Context, task *coordinatorv1.Task, root, parent exec.DAGRunRef, loadErr error, profileName string) (*exec.DAGRunStatus, error) {
	client := &captureCoordinatorClientForTest{}
	handler := &remoteTaskHandler{
		workerID:          "worker-test",
		coordinatorClient: client,
	}
	handler.reportTaskLoadFailure(ctx, task, root, parent, exec.HostInfo{}, loadErr, profileName)
	if client.err != nil {
		return nil, client.err
	}
	if client.status == nil {
		return nil, errors.New("load failure status was not reported")
	}
	return client.status, nil
}

// ReportTaskInitFailureStatusForTest returns the status emitted for a task init failure.
func ReportTaskInitFailureStatusForTest(ctx context.Context, task *coordinatorv1.Task, root, parent exec.DAGRunRef, initErr error, profileName string) (*exec.DAGRunStatus, error) {
	pusher := &captureStatusPusherForTest{}
	handler := &remoteTaskHandler{}
	handler.reportTaskInitFailure(ctx, task, root, parent, pusher, initErr, profileName)
	if pusher.status == nil {
		return nil, errors.New("init failure status was not reported")
	}
	return pusher.status, nil
}
