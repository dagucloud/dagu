// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordreport

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/proto/convert"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

var _ runtime.StatusPusher = (*StatusPusher)(nil)

// StatusPusher sends status updates to coordinator via gRPC
type StatusPusher struct {
	client   coordinator.Client
	workerID string
	owner    exec.HostInfo
}

// AttemptRejectedError indicates the coordinator explicitly rejected a status
// update because the worker's claimed attempt is no longer authoritative.
type AttemptRejectedError struct {
	Reason string
}

func (e *AttemptRejectedError) Error() string {
	if e == nil || e.Reason == "" {
		return "status rejected"
	}
	return fmt.Sprintf("status rejected: %s", e.Reason)
}

// AttemptRejectedReason returns the coordinator rejection reason.
func (e *AttemptRejectedError) AttemptRejectedReason() string {
	if e == nil {
		return ""
	}
	return e.Reason
}

// NewStatusPusher creates a new StatusPusher
func NewStatusPusher(client coordinator.Client, workerID string, owner ...exec.HostInfo) *StatusPusher {
	var target exec.HostInfo
	if len(owner) > 0 {
		target = owner[0]
	}
	return &StatusPusher{
		client:   client,
		workerID: workerID,
		owner:    target,
	}
}

// Push sends a status update to the coordinator
func (p *StatusPusher) Push(ctx context.Context, status exec.DAGRunStatus) error {
	protoStatus, err := convert.DAGRunStatusToProto(&status)
	if err != nil {
		return fmt.Errorf("failed to convert status to proto: %w", err)
	}
	req := &coordinatorv1.ReportStatusRequest{
		WorkerId:           p.workerID,
		Status:             protoStatus,
		OwnerCoordinatorId: p.owner.ID,
	}

	var resp *coordinatorv1.ReportStatusResponse
	if p.owner.Host != "" {
		resp, err = p.client.ReportStatusTo(ctx, p.owner, req)
	} else {
		resp, err = p.client.ReportStatus(ctx, req)
	}
	if err != nil {
		return fmt.Errorf("failed to report status: %w", err)
	}

	if resp == nil {
		return fmt.Errorf("received nil response from coordinator")
	}

	if !resp.Accepted {
		return &AttemptRejectedError{Reason: resp.Error}
	}

	return nil
}
