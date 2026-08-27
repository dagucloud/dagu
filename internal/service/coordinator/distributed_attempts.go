// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

type attemptOwnershipConfig struct {
	Owner               dispatch.CoordinatorEndpoint
	LeaseStore          dispatch.DAGRunLeaseStore
	ActiveRunStore      dispatch.ActiveDistributedRunStore
	StaleLeaseThreshold time.Duration
	Now                 func() time.Time
}

type attemptOwnership struct {
	owner               dispatch.CoordinatorEndpoint
	leaseStore          dispatch.DAGRunLeaseStore
	activeRunStore      dispatch.ActiveDistributedRunStore
	staleLeaseThreshold time.Duration
	now                 func() time.Time
}

func newAttemptOwnership(cfg attemptOwnershipConfig) *attemptOwnership {
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &attemptOwnership{
		owner:               cfg.Owner,
		leaseStore:          cfg.LeaseStore,
		activeRunStore:      cfg.ActiveRunStore,
		staleLeaseThreshold: cfg.StaleLeaseThreshold,
		now:                 now,
	}
}

func (h *Handler) attemptOwnership() *attemptOwnership {
	return newAttemptOwnership(attemptOwnershipConfig{
		Owner:               h.owner,
		LeaseStore:          h.dagRunLeaseStore,
		ActiveRunStore:      h.activeDistributedRunStore,
		StaleLeaseThreshold: h.staleLeaseThreshold,
	})
}

func (o *attemptOwnership) statusDecision(
	ctx context.Context,
	latest *ir.DAGRunStatus,
	incoming *ir.DAGRunStatus,
	opts statusDecisionOptions,
) (accepted bool, rejectionReason string) {
	if latest == nil || incoming == nil {
		return false, remoteAttemptRejectedLeaseInactive
	}
	if !sameAttemptStatus(latest, incoming) {
		return false, remoteAttemptRejectedSuperseded
	}
	if !isTerminalRunStatus(latest.Status) {
		return true, ""
	}
	claimKey := opts.ClaimKey
	if claimKey == "" {
		claimKey = latest.EffectiveClaimKey()
	}
	if o.leaseInactive(ctx, claimKey) && (incoming.Status.IsActive() || incoming.Status == ir.NotStarted) {
		return false, remoteAttemptRejectedLeaseInactive
	}
	if latest.Status == incoming.Status {
		return true, ""
	}
	if opts.CancellationRequested && latest.Status == ir.Failed && incoming.Status == ir.Aborted {
		return true, ""
	}
	return false, remoteAttemptRejectedTerminal
}

type statusDecisionOptions struct {
	CancellationRequested bool
	ClaimKey              string
}

func (o *attemptOwnership) leaseInactive(ctx context.Context, attemptKey string) bool {
	if o.leaseStore == nil || attemptKey == "" {
		return false
	}
	lease, err := o.leaseStore.Get(ctx, attemptKey)
	switch {
	case err == nil:
		return !lease.IsFresh(o.now(), o.staleLeaseThreshold)
	case errors.Is(err, dispatch.ErrDAGRunLeaseNotFound):
		return true
	default:
		logger.Warn(ctx, "Failed to read distributed lease for status validation",
			tag.AttemptKey(attemptKey),
			tag.Error(err),
		)
		return false
	}
}

func (o *attemptOwnership) syncFromStatus(
	ctx context.Context,
	workerID string,
	status *ir.DAGRunStatus,
	fallbackAttemptID string,
) {
	o.syncLeaseFromStatus(ctx, workerID, status, fallbackAttemptID)
	o.syncActiveRunFromStatus(ctx, workerID, status, fallbackAttemptID)
}

func (o *attemptOwnership) syncLeaseFromStatus(
	ctx context.Context,
	workerID string,
	status *ir.DAGRunStatus,
	fallbackAttemptID string,
) {
	if o.leaseStore == nil || status == nil {
		return
	}

	switch status.Status {
	case ir.Running, ir.NotStarted, ir.Queued:
		o.upsertLeaseFromStatus(ctx, workerID, status, fallbackAttemptID)
	case ir.Failed, ir.Aborted, ir.Succeeded,
		ir.PartiallySucceeded, ir.Waiting, ir.Rejected:
		attemptKey := dispatch.AttemptKeyForStatus(status, fallbackAttemptID)
		if attemptKey == "" {
			return
		}
		if err := o.leaseStore.Delete(ctx, attemptKey); err != nil {
			logger.Warn(ctx, "Failed to delete distributed run lease",
				tag.RunID(status.DAGRunID),
				tag.Error(err),
			)
		}
	}
}

func (o *attemptOwnership) upsertLeaseFromStatus(
	ctx context.Context,
	workerID string,
	status *ir.DAGRunStatus,
	fallbackAttemptID string,
) {
	if o.leaseStore == nil || status == nil {
		return
	}

	attemptKey := dispatch.AttemptKeyForStatus(status, fallbackAttemptID)
	if attemptKey == "" {
		return
	}
	claimKey := status.EffectiveClaimKey()
	if claimKey == "" {
		claimKey = attemptKey
	}
	if claimKey != attemptKey {
		return
	}

	attemptID := status.AttemptID
	if attemptID == "" {
		attemptID = fallbackAttemptID
	}
	if attemptID == "" {
		return
	}

	if workerID == "" {
		workerID = status.WorkerID
	}
	if !dispatch.IsRemoteWorkerID(workerID) {
		return
	}

	queueName := queueNameForStatus(status)
	now := o.now()
	lease := dispatch.DAGRunLease{
		AttemptKey: attemptKey,
		DAGRun: ir.DAGRunRef{
			Name: status.Name,
			ID:   status.DAGRunID,
		},
		Root:            status.Root,
		AttemptID:       attemptID,
		QueueName:       queueName,
		WorkerID:        workerID,
		Owner:           o.owner,
		ClaimedAt:       now.UnixMilli(),
		LastHeartbeatAt: now.UnixMilli(),
	}
	if existing, err := o.leaseStore.Get(ctx, attemptKey); err == nil && existing != nil {
		lease.ClaimedAt = existing.ClaimedAt
		lease.WorkspaceBundleDigest = existing.WorkspaceBundleDigest
		if existing.Owner != (dispatch.CoordinatorEndpoint{}) {
			lease.Owner = existing.Owner
		}
		if status.ProcGroup == "" && existing.QueueName != "" {
			lease.QueueName = existing.QueueName
		}
	}
	if err := o.leaseStore.Upsert(ctx, lease); err != nil {
		logger.Warn(ctx, "Failed to upsert distributed run lease",
			tag.RunID(status.DAGRunID),
			tag.Error(err),
		)
	}
}

func (o *attemptOwnership) restoreConfirmedFromStatus(
	ctx context.Context,
	workerID string,
	status *ir.DAGRunStatus,
	fallbackAttemptID string,
) {
	if status == nil {
		return
	}

	switch status.Status {
	case ir.Running, ir.NotStarted, ir.Queued:
		o.upsertLeaseFromStatus(ctx, workerID, status, fallbackAttemptID)
		o.upsertActiveFromStatus(ctx, status, workerID, fallbackAttemptID)
	case ir.Failed, ir.Aborted, ir.Succeeded,
		ir.PartiallySucceeded, ir.Waiting, ir.Rejected:
	}
}

func (o *attemptOwnership) syncActiveRunFromStatus(
	ctx context.Context,
	workerID string,
	status *ir.DAGRunStatus,
	fallbackAttemptID string,
) {
	if o.activeRunStore == nil || status == nil {
		return
	}

	attemptKey := dispatch.AttemptKeyForStatus(status, fallbackAttemptID)
	if attemptKey == "" {
		return
	}

	switch status.Status {
	case ir.Running, ir.NotStarted, ir.Queued:
		o.upsertActiveFromStatus(ctx, status, workerID, fallbackAttemptID)
	case ir.Failed, ir.Aborted, ir.Succeeded,
		ir.PartiallySucceeded, ir.Waiting, ir.Rejected:
		if err := o.activeRunStore.Delete(ctx, attemptKey); err != nil {
			logger.Warn(ctx, "Failed to delete active distributed run",
				tag.RunID(status.DAGRunID),
				tag.AttemptKey(attemptKey),
				tag.Error(err),
			)
		}
	}
}

func (o *attemptOwnership) upsertActiveFromStatus(
	ctx context.Context,
	runStatus *ir.DAGRunStatus,
	workerID string,
	fallbackAttemptID string,
) {
	if o.activeRunStore == nil || runStatus == nil {
		return
	}

	attemptKey := dispatch.AttemptKeyForStatus(runStatus, fallbackAttemptID)
	if attemptKey == "" {
		return
	}

	attemptID := runStatus.AttemptID
	if attemptID == "" {
		attemptID = fallbackAttemptID
	}
	if workerID == "" {
		workerID = runStatus.WorkerID
	}
	if !dispatch.IsRemoteWorkerID(workerID) {
		return
	}

	record := dispatch.ActiveDistributedRun{
		AttemptKey: attemptKey,
		DAGRun:     runStatus.DAGRun(),
		Root:       runStatus.Root,
		AttemptID:  attemptID,
		WorkerID:   workerID,
		Status:     runStatus.Status,
		UpdatedAt:  o.now().UnixMilli(),
	}
	if err := o.activeRunStore.Upsert(ctx, record); err != nil {
		logger.Warn(ctx, "Failed to upsert active distributed run",
			tag.RunID(runStatus.DAGRunID),
			tag.AttemptKey(attemptKey),
			tag.Error(err),
		)
	}
}

func (o *attemptOwnership) recordTaskClaim(
	ctx context.Context,
	task *coordinatorv1.Task,
	workerID string,
) error {
	now := o.now()
	if err := o.leaseStore.Upsert(ctx, o.leaseFromTask(task, workerID, now)); err != nil {
		return err
	}
	o.upsertActiveFromTask(ctx, task, workerID, now)
	return nil
}

func (o *attemptOwnership) upsertActiveFromTask(
	ctx context.Context,
	task *coordinatorv1.Task,
	workerID string,
	now time.Time,
) {
	if o.activeRunStore == nil || task == nil || task.AttemptKey == "" {
		return
	}
	if !dispatch.IsRemoteWorkerID(workerID) {
		return
	}

	root := ir.DAGRunRef{Name: task.RootDagRunName, ID: task.RootDagRunId}
	if root.Zero() {
		root = ir.DAGRunRef{Name: task.Target, ID: task.DagRunId}
	}

	record := dispatch.ActiveDistributedRun{
		AttemptKey: task.AttemptKey,
		DAGRun: ir.DAGRunRef{
			Name: task.Target,
			ID:   task.DagRunId,
		},
		Root:      root,
		AttemptID: task.AttemptId,
		WorkerID:  workerID,
		Status:    ir.Queued,
		UpdatedAt: now.UnixMilli(),
	}
	if err := o.activeRunStore.Upsert(ctx, record); err != nil {
		logger.Warn(ctx, "Failed to upsert active distributed run from task claim",
			tag.RunID(task.DagRunId),
			tag.AttemptKey(task.AttemptKey),
			tag.Error(err),
		)
	}
}

func (o *attemptOwnership) leaseFromTask(
	task *coordinatorv1.Task,
	workerID string,
	now time.Time,
) dispatch.DAGRunLease {
	owner := dispatch.CoordinatorEndpoint{
		ID:   task.OwnerCoordinatorId,
		Host: task.OwnerCoordinatorHost,
		Port: int(task.OwnerCoordinatorPort),
	}
	if owner.ID == "" || owner.Host == "" || owner.Port <= 0 {
		owner = o.owner
	}
	root := ir.DAGRunRef{Name: task.RootDagRunName, ID: task.RootDagRunId}
	if root.Zero() {
		root = ir.DAGRunRef{Name: task.Target, ID: task.DagRunId}
	}
	queueName := task.QueueName
	if queueName == "" {
		queueName = task.Target
	}
	return dispatch.DAGRunLease{
		AttemptKey: task.AttemptKey,
		DAGRun: ir.DAGRunRef{
			Name: task.Target,
			ID:   task.DagRunId,
		},
		Root:                  root,
		AttemptID:             task.AttemptId,
		QueueName:             queueName,
		WorkerID:              workerID,
		Owner:                 owner,
		ClaimToken:            task.ClaimToken,
		WorkspaceBundleDigest: task.WorkspaceBundleDigest,
		ClaimedAt:             now.UnixMilli(),
		LastHeartbeatAt:       now.UnixMilli(),
	}
}

func (o *attemptOwnership) deleteTracking(
	ctx context.Context,
	storeCtx context.Context,
	dagRun ir.DAGRunRef,
	attemptKey string,
	leaseMessage string,
	activeRunMessage string,
) {
	o.deleteLease(ctx, storeCtx, dagRun, attemptKey, leaseMessage)
	o.deleteActiveRun(ctx, storeCtx, dagRun, attemptKey, activeRunMessage)
}

func (o *attemptOwnership) deleteLease(
	ctx context.Context,
	storeCtx context.Context,
	dagRun ir.DAGRunRef,
	attemptKey string,
	message string,
) {
	if o.leaseStore == nil || attemptKey == "" {
		return
	}
	if err := o.leaseStore.Delete(storeCtx, attemptKey); err != nil &&
		!errors.Is(err, dispatch.ErrDAGRunLeaseNotFound) {
		logger.Warn(ctx, message,
			tag.RunID(dagRun.ID),
			tag.Error(err),
		)
	}
}

func (o *attemptOwnership) deleteActiveRun(
	ctx context.Context,
	storeCtx context.Context,
	dagRun ir.DAGRunRef,
	attemptKey string,
	message string,
) {
	if o.activeRunStore == nil || attemptKey == "" {
		return
	}
	if err := o.activeRunStore.Delete(storeCtx, attemptKey); err != nil &&
		!errors.Is(err, dispatch.ErrActiveRunNotFound) {
		logger.Warn(ctx, message,
			tag.RunID(dagRun.ID),
			tag.AttemptKey(attemptKey),
			tag.Error(err),
		)
	}
}

func (o *attemptOwnership) indexedRunMatchesStatus(
	record dispatch.ActiveDistributedRun,
	runStatus *ir.DAGRunStatus,
) bool {
	if _, ok := remoteWorkerID(runStatus, record.WorkerID); !ok {
		return false
	}
	if runStatus.Status != ir.Running &&
		runStatus.Status != ir.NotStarted &&
		runStatus.Status != ir.Queued {
		return false
	}

	attemptKey := dispatch.AttemptKeyForStatus(runStatus, record.AttemptID)
	if attemptKey == "" || attemptKey != record.AttemptKey {
		return false
	}
	if record.AttemptID != "" {
		attemptID := runStatus.AttemptID
		if attemptID == "" {
			attemptID = record.AttemptID
		}
		if attemptID != record.AttemptID {
			return false
		}
	}
	return true
}

func isTerminalRunStatus(status ir.Status) bool {
	return status != ir.NotStarted && !status.IsActive()
}

func isCancellableTerminalRunStatus(status ir.Status) bool {
	return isTerminalRunStatus(status) && !status.IsSuccess()
}

func sameAttemptStatus(current, incoming *ir.DAGRunStatus) bool {
	if current == nil || incoming == nil {
		return false
	}
	if current.AttemptID == "" && current.AttemptKey == "" {
		return true
	}
	if current.AttemptID != "" && incoming.AttemptID != "" && current.AttemptID != incoming.AttemptID {
		return false
	}
	if current.AttemptKey != "" && incoming.AttemptKey != "" && current.AttemptKey != incoming.AttemptKey {
		return false
	}
	if current.AttemptID != "" && incoming.AttemptID != "" {
		return true
	}
	return current.AttemptKey != "" && current.AttemptKey == incoming.AttemptKey
}

func remoteWorkerID(status *ir.DAGRunStatus, fallbackWorkerID string) (string, bool) {
	if status == nil {
		return "", false
	}
	if dispatch.IsRemoteWorkerID(status.WorkerID) {
		return status.WorkerID, true
	}
	if status.WorkerID != "" {
		return "", false
	}
	if status.Status != ir.Queued && status.Status != ir.NotStarted {
		return "", false
	}
	if !dispatch.IsRemoteWorkerID(fallbackWorkerID) {
		return "", false
	}
	return fallbackWorkerID, true
}

func queueNameForStatus(status *ir.DAGRunStatus) string {
	if status == nil || status.ProcGroup == "" {
		if status == nil {
			return ""
		}
		return status.Name
	}
	return status.ProcGroup
}

func logRejectedRemoteStatusUpdate(
	ctx context.Context,
	workerID string,
	incoming *ir.DAGRunStatus,
	latest *ir.DAGRunStatus,
	reason string,
) {
	attrs := []slog.Attr{
		tag.WorkerID(workerID),
		slog.String("reason", reason),
	}
	if incoming != nil {
		attrs = append(attrs,
			tag.RunID(incoming.DAGRunID),
			tag.AttemptID(incoming.AttemptID),
			tag.AttemptKey(incoming.AttemptKey),
			slog.String("reported-status", incoming.Status.String()),
		)
	}
	if latest != nil {
		attrs = append(attrs,
			slog.String("latest-attempt-id", latest.AttemptID),
			slog.String("latest-attempt-key", latest.AttemptKey),
			slog.String("latest-status", latest.Status.String()),
		)
	}
	logger.Warn(ctx, "Rejected remote status update", attrs...)
}
