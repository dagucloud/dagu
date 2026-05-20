// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

type attemptOwnershipConfig struct {
	Owner               exec.CoordinatorEndpoint
	LeaseStore          exec.DAGRunLeaseStore
	ActiveRunStore      exec.ActiveDistributedRunStore
	StaleLeaseThreshold time.Duration
	Now                 func() time.Time
}

type attemptOwnership struct {
	owner               exec.CoordinatorEndpoint
	leaseStore          exec.DAGRunLeaseStore
	activeRunStore      exec.ActiveDistributedRunStore
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

func (h *Handler) distributedAttempts() *attemptOwnership {
	return newAttemptOwnership(attemptOwnershipConfig{
		Owner:               h.owner,
		LeaseStore:          h.dagRunLeaseStore,
		ActiveRunStore:      h.activeDistributedRunStore,
		StaleLeaseThreshold: h.staleLeaseThreshold,
	})
}

func (o *attemptOwnership) statusDecision(
	ctx context.Context,
	latest *exec.DAGRunStatus,
	incoming *exec.DAGRunStatus,
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
	if o.leaseInactive(ctx, latest.AttemptKey) && (incoming.Status.IsActive() || incoming.Status == core.NotStarted) {
		return false, remoteAttemptRejectedLeaseInactive
	}
	if latest.Status == incoming.Status {
		return true, ""
	}
	return false, remoteAttemptRejectedTerminal
}

func (o *attemptOwnership) leaseInactive(ctx context.Context, attemptKey string) bool {
	if o.leaseStore == nil || attemptKey == "" {
		return false
	}
	lease, err := o.leaseStore.Get(ctx, attemptKey)
	switch {
	case err == nil:
		return !lease.IsFresh(o.now(), o.staleLeaseThreshold)
	case errors.Is(err, exec.ErrDAGRunLeaseNotFound):
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
	status *exec.DAGRunStatus,
	fallbackAttemptID string,
) {
	o.syncLeaseFromStatus(ctx, workerID, status, fallbackAttemptID)
	o.syncActiveRunFromStatus(ctx, workerID, status, fallbackAttemptID)
}

func (o *attemptOwnership) syncLeaseFromStatus(
	ctx context.Context,
	workerID string,
	status *exec.DAGRunStatus,
	fallbackAttemptID string,
) {
	if o.leaseStore == nil || status == nil {
		return
	}

	switch status.Status {
	case core.Running, core.NotStarted, core.Queued:
		o.upsertLeaseFromStatus(ctx, workerID, status, fallbackAttemptID)
	case core.Failed, core.Aborted, core.Succeeded,
		core.PartiallySucceeded, core.Waiting, core.Rejected:
		attemptKey := exec.AttemptKeyForStatus(status, fallbackAttemptID)
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
	status *exec.DAGRunStatus,
	fallbackAttemptID string,
) {
	if o.leaseStore == nil || status == nil {
		return
	}

	attemptKey := exec.AttemptKeyForStatus(status, fallbackAttemptID)
	if attemptKey == "" {
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
	if !exec.IsRemoteWorkerID(workerID) {
		return
	}

	queueName := queueNameForStatus(status)
	now := o.now()
	lease := exec.DAGRunLease{
		AttemptKey: attemptKey,
		DAGRun: exec.DAGRunRef{
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
	status *exec.DAGRunStatus,
	fallbackAttemptID string,
) {
	if status == nil {
		return
	}

	switch status.Status {
	case core.Running, core.NotStarted, core.Queued:
		o.upsertLeaseFromStatus(ctx, workerID, status, fallbackAttemptID)
		o.upsertActiveFromStatus(ctx, status, workerID, fallbackAttemptID)
	case core.Failed, core.Aborted, core.Succeeded,
		core.PartiallySucceeded, core.Waiting, core.Rejected:
	}
}

func (o *attemptOwnership) syncActiveRunFromStatus(
	ctx context.Context,
	workerID string,
	status *exec.DAGRunStatus,
	fallbackAttemptID string,
) {
	if o.activeRunStore == nil || status == nil {
		return
	}

	attemptKey := exec.AttemptKeyForStatus(status, fallbackAttemptID)
	if attemptKey == "" {
		return
	}

	switch status.Status {
	case core.Running, core.NotStarted, core.Queued:
		o.upsertActiveFromStatus(ctx, status, workerID, fallbackAttemptID)
	case core.Failed, core.Aborted, core.Succeeded,
		core.PartiallySucceeded, core.Waiting, core.Rejected:
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
	runStatus *exec.DAGRunStatus,
	workerID string,
	fallbackAttemptID string,
) {
	if o.activeRunStore == nil || runStatus == nil {
		return
	}

	attemptKey := exec.AttemptKeyForStatus(runStatus, fallbackAttemptID)
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
	if !exec.IsRemoteWorkerID(workerID) {
		return
	}

	record := exec.ActiveDistributedRun{
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
	if !exec.IsRemoteWorkerID(workerID) {
		return
	}

	root := exec.DAGRunRef{Name: task.RootDagRunName, ID: task.RootDagRunId}
	if root.Zero() {
		root = exec.DAGRunRef{Name: task.Target, ID: task.DagRunId}
	}

	record := exec.ActiveDistributedRun{
		AttemptKey: task.AttemptKey,
		DAGRun: exec.DAGRunRef{
			Name: task.Target,
			ID:   task.DagRunId,
		},
		Root:      root,
		AttemptID: task.AttemptId,
		WorkerID:  workerID,
		Status:    core.Queued,
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
) exec.DAGRunLease {
	root := exec.DAGRunRef{Name: task.RootDagRunName, ID: task.RootDagRunId}
	if root.Zero() {
		root = exec.DAGRunRef{Name: task.Target, ID: task.DagRunId}
	}
	queueName := task.QueueName
	if queueName == "" {
		queueName = task.Target
	}
	return exec.DAGRunLease{
		AttemptKey: task.AttemptKey,
		DAGRun: exec.DAGRunRef{
			Name: task.Target,
			ID:   task.DagRunId,
		},
		Root:            root,
		AttemptID:       task.AttemptId,
		QueueName:       queueName,
		WorkerID:        workerID,
		Owner:           o.owner,
		ClaimedAt:       now.UnixMilli(),
		LastHeartbeatAt: now.UnixMilli(),
	}
}

func (o *attemptOwnership) deleteTracking(
	ctx context.Context,
	storeCtx context.Context,
	dagRun exec.DAGRunRef,
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
	dagRun exec.DAGRunRef,
	attemptKey string,
	message string,
) {
	if o.leaseStore == nil || attemptKey == "" {
		return
	}
	if err := o.leaseStore.Delete(storeCtx, attemptKey); err != nil &&
		!errors.Is(err, exec.ErrDAGRunLeaseNotFound) {
		logger.Warn(ctx, message,
			tag.RunID(dagRun.ID),
			tag.Error(err),
		)
	}
}

func (o *attemptOwnership) deleteActiveRun(
	ctx context.Context,
	storeCtx context.Context,
	dagRun exec.DAGRunRef,
	attemptKey string,
	message string,
) {
	if o.activeRunStore == nil || attemptKey == "" {
		return
	}
	if err := o.activeRunStore.Delete(storeCtx, attemptKey); err != nil &&
		!errors.Is(err, exec.ErrActiveRunNotFound) {
		logger.Warn(ctx, message,
			tag.RunID(dagRun.ID),
			tag.AttemptKey(attemptKey),
			tag.Error(err),
		)
	}
}

func (o *attemptOwnership) indexedRunMatchesStatus(
	record exec.ActiveDistributedRun,
	runStatus *exec.DAGRunStatus,
) bool {
	if _, ok := distributedWorkerIDForStatus(runStatus, record.WorkerID); !ok {
		return false
	}
	if runStatus.Status != core.Running &&
		runStatus.Status != core.NotStarted &&
		runStatus.Status != core.Queued {
		return false
	}

	attemptKey := exec.AttemptKeyForStatus(runStatus, record.AttemptID)
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

func isTerminalRunStatus(status core.Status) bool {
	return status != core.NotStarted && !status.IsActive()
}

func isCancellableTerminalRunStatus(status core.Status) bool {
	return isTerminalRunStatus(status) && !status.IsSuccess()
}

func sameAttemptStatus(current, incoming *exec.DAGRunStatus) bool {
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

func distributedWorkerIDForStatus(status *exec.DAGRunStatus, fallbackWorkerID string) (string, bool) {
	if status == nil {
		return "", false
	}
	if exec.IsRemoteWorkerID(status.WorkerID) {
		return status.WorkerID, true
	}
	if status.WorkerID != "" {
		return "", false
	}
	if status.Status != core.Queued && status.Status != core.NotStarted {
		return "", false
	}
	if !exec.IsRemoteWorkerID(fallbackWorkerID) {
		return "", false
	}
	return fallbackWorkerID, true
}

func queueNameForStatus(status *exec.DAGRunStatus) string {
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
	incoming *exec.DAGRunStatus,
	latest *exec.DAGRunStatus,
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
