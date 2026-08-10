// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordreport

import (
	"context"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/backoff"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/proto/convert"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

const (
	finalDeliveryRetryInitialInterval = 100 * time.Millisecond
	finalDeliveryRetryMaxInterval     = 2 * time.Second
	finalDeliveryRetryTimeout         = 3 * time.Minute
)

func finalDeliveryRetryPolicy() backoff.RetryPolicy {
	policy := backoff.NewExponentialBackoffPolicy(finalDeliveryRetryInitialInterval)
	policy.MaxInterval = finalDeliveryRetryMaxInterval
	return backoff.WithJitter(policy, backoff.Jitter)
}

var _ runtime.StatusPusher = (*StatusPusher)(nil)

// StatusPusher sends status updates to coordinator via gRPC
type StatusPusher struct {
	client     coordinator.Client
	workerID   string
	owner      serviceregistry.HostInfo
	claimKey   string
	sourceFile string
	labels     string
}

// NewTaskStatusPusher creates a StatusPusher bound to a dispatched task.
func NewTaskStatusPusher(client coordinator.Client, workerID string, task *coordinatorv1.Task, owner ...serviceregistry.HostInfo) *StatusPusher {
	if task == nil {
		return NewStatusPusher(client, workerID, "", owner...)
	}
	pusher := NewStatusPusher(client, workerID, task.AttemptKey, owner...)
	pusher.sourceFile = task.SourceFile
	pusher.labels = task.Labels
	return pusher
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

// NewStatusPusher creates a StatusPusher bound to a worker claim.
func NewStatusPusher(client coordinator.Client, workerID, claimKey string, owner ...serviceregistry.HostInfo) *StatusPusher {
	var target serviceregistry.HostInfo
	if len(owner) > 0 {
		target = owner[0]
	}
	return &StatusPusher{
		client:   client,
		workerID: workerID,
		owner:    target,
		claimKey: claimKey,
	}
}

// Push sends a status update to the coordinator
func (p *StatusPusher) Push(ctx context.Context, status ir.DAGRunStatus) error {
	if p.claimKey != "" {
		status.ClaimKey = p.claimKey
	}
	protoStatus, err := convert.DAGRunStatusToProto(&status)
	if err != nil {
		return fmt.Errorf("failed to convert status to proto: %w", err)
	}
	req := &coordinatorv1.ReportStatusRequest{
		WorkerId:           p.workerID,
		Status:             protoStatus,
		OwnerCoordinatorId: p.owner.ID,
		SourceFile:         p.sourceFile,
		Labels:             p.labels,
	}

	var resp *coordinatorv1.ReportStatusResponse
	report := func(ctx context.Context) error {
		if p.owner.Host != "" {
			resp, err = p.client.ReportStatusTo(ctx, p.owner, req)
		} else {
			resp, err = p.client.ReportStatus(ctx, req)
		}
		return err
	}
	if status.Status != ir.NotStarted && !status.Status.IsActive() {
		retryCtx, cancel := context.WithTimeout(ctx, finalDeliveryRetryTimeout)
		defer cancel()
		err = backoff.Retry(retryCtx, report, finalDeliveryRetryPolicy(), isRetryableStatusReportError)
	} else {
		err = report(ctx)
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

func isRetryableStatusReportError(err error) bool {
	code := grpcstatus.Code(err)
	return code == codes.Unavailable || code == codes.DeadlineExceeded
}
