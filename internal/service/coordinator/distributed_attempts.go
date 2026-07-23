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
	Owner          exec.CoordinatorEndpoint
	LeaseStore     exec.DAGRunLeaseStore
	ActiveRunStore exec.ActiveDistributedRunStore
	Now            func() time.Time
}

type attemptOwnership struct {
	owner          exec.CoordinatorEndpoint
	leaseStore     exec.DAGRunLeaseStore
	activeRunStore exec.ActiveDistributedRunStore
	now            func() time.Time
}

func newAttemptOwnership(cfg attemptOwnershipConfig) *attemptOwnership {
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &attemptOwnership{
		owner:          cfg.Owner,
		leaseStore:     cfg.LeaseStore,
		activeRunStore: cfg.ActiveRunStore,
		now:            now,
	}
}

func (h *Handler) attemptOwnership() *attemptOwnership {
	return newAttemptOwnership(attemptOwnershipConfig{
		Owner:          h.owner,
		LeaseStore:     h.dagRunLeaseStore,
		ActiveRunStore: h.activeDistributedRunStore,
	})
}

func (o *attemptOwnership) statusDecision(
	ctx context.Context,
	latest *exec.DAGRunStatus,
	incoming *exec.DAGRunStatus,
	opts statusDecisionOptions,
) (bool, string, error) {
	if latest == nil || incoming == nil {
		return false, remoteAttemptRejectedLeaseInactive, nil
	}
	if !sameAttemptStatus(latest, incoming) {
		return false, remoteAttemptRejectedSuperseded, nil
	}
	claimKey := opts.ClaimKey
	if claimKey == "" {
		claimKey = latest.EffectiveClaimKey()
	}
	if !isTerminalRunStatus(latest.Status) {
		leaseRequired := incoming.Status == core.Waiting ||
			latest.Status == core.Queued ||
			latest.Status == core.Waiting
		matches, err := o.leaseMatchesReporter(ctx, claimKey, incoming.WorkerID, opts.WorkerID, leaseRequired)
		if err != nil {
			return false, "", err
		}
		if !matches {
			return false, remoteAttemptRejectedLeaseInactive, nil
		}
		return true, "", nil
	}
	if incoming.Status.IsActive() || incoming.Status == core.NotStarted {
		return false, remoteAttemptRejectedLeaseInactive, nil
	}
	if latest.Status == incoming.Status {
		return true, "", nil
	}
	if opts.CancellationRequested && latest.Status == core.Failed && incoming.Status == core.Aborted {
		return true, "", nil
	}
	return false, remoteAttemptRejectedTerminal, nil
}

type statusDecisionOptions struct {
	CancellationRequested bool
	ClaimKey              string
	WorkerID              string
}

func (o *attemptOwnership) leaseMatchesReporter(
	ctx context.Context,
	claimKey string,
	statusWorkerID string,
	reportingWorkerID string,
	required bool,
) (bool, error) {
	if statusWorkerID != "" && reportingWorkerID != "" && statusWorkerID != reportingWorkerID {
		return false, nil
	}
	if o.leaseStore == nil {
		return true, nil
	}
	if claimKey == "" {
		return !required, nil
	}
	workerID := statusWorkerID
	if workerID == "" {
		workerID = reportingWorkerID
	}

	lease, err := o.leaseStore.Get(ctx, claimKey)
	if err == nil {
		return lease.MatchesClaim(claimKey, workerID), nil
	}
	if errors.Is(err, exec.ErrDAGRunLeaseNotFound) {
		return !required, nil
	}
	return false, err
}

func (o *attemptOwnership) refreshWaitingLease(
	ctx context.Context,
	status *exec.DAGRunStatus,
	reportingWorkerID string,
) (bool, error) {
	if status == nil || status.Status != core.Waiting || o.leaseStore == nil {
		return true, nil
	}
	claimKey := status.EffectiveClaimKey()
	if claimKey == "" {
		claimKey = exec.AttemptKeyForStatus(status, status.AttemptID)
	}
	matches, err := o.leaseMatchesReporter(
		ctx,
		claimKey,
		status.WorkerID,
		reportingWorkerID,
		true,
	)
	if err != nil || !matches {
		return matches, err
	}
	if err := o.leaseStore.Touch(ctx, claimKey, o.now()); err != nil {
		if errors.Is(err, exec.ErrDAGRunLeaseNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (o *attemptOwnership) syncFromStatus(
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
		attemptKey := exec.AttemptKeyForStatus(status, fallbackAttemptID)
		if attemptKey == "" {
			return
		}
		o.deleteTracking(
			ctx,
			context.WithoutCancel(ctx),
			status.DAGRun(),
			attemptKey,
			"Failed to delete distributed run lease",
			"Failed to delete active distributed run",
		)
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
	if !o.deleteActiveRun(ctx, storeCtx, dagRun, attemptKey, activeRunMessage) {
		return
	}
	// Lease removal publishes final attempt release after auxiliary tracking settles.
	o.deleteLease(ctx, storeCtx, dagRun, attemptKey, leaseMessage)
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
) bool {
	if o.activeRunStore == nil || attemptKey == "" {
		return true
	}
	if err := o.activeRunStore.Delete(storeCtx, attemptKey); err != nil {
		if errors.Is(err, exec.ErrActiveRunNotFound) {
			return true
		}
		logger.Warn(ctx, message,
			tag.RunID(dagRun.ID),
			tag.AttemptKey(attemptKey),
			tag.Error(err),
		)
		return false
	}
	return true
}

func (o *attemptOwnership) indexedRunMatchesStatus(
	record exec.ActiveDistributedRun,
	runStatus *exec.DAGRunStatus,
) bool {
	if _, ok := remoteWorkerID(runStatus, record.WorkerID); !ok {
		return false
	}
	if runStatus.Status != core.Running &&
		runStatus.Status != core.NotStarted &&
		runStatus.Status != core.Queued &&
		runStatus.Status != core.Waiting {
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

func remoteWorkerID(status *exec.DAGRunStatus, fallbackWorkerID string) (string, bool) {
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
