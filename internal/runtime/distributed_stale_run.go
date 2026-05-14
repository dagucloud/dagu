// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

const defaultStaleWorkerHeartbeatThreshold = 30 * time.Second

// DistributedRunRepairConfig configures conservative stale distributed-run repair.
type DistributedRunRepairConfig struct {
	DAGRunStore                   exec.DAGRunStore
	DAGRunLeaseStore              exec.DAGRunLeaseStore
	WorkerHeartbeatStore          exec.WorkerHeartbeatStore
	StaleLeaseThreshold           time.Duration
	StaleWorkerHeartbeatThreshold time.Duration
	Now                           func() time.Time
}

// ConfirmAndRepairStaleDistributedRun marks a remote active attempt failed only
// when both the run lease and worker evidence confirm that the exact attempt is gone.
func ConfirmAndRepairStaleDistributedRun(
	ctx context.Context,
	cfg DistributedRunRepairConfig,
	status *exec.DAGRunStatus,
	fallbackAttemptID string,
	fallbackWorkerID string,
) (*exec.DAGRunStatus, bool, error) {
	if status == nil || cfg.DAGRunStore == nil || cfg.DAGRunLeaseStore == nil || cfg.WorkerHeartbeatStore == nil {
		return status, false, nil
	}

	workerID, ok := remoteWorkerIDForStatus(status, fallbackWorkerID)
	if !ok {
		return status, false, nil
	}
	if !statusEligibleForDistributedRepair(status.Status) {
		return status, false, nil
	}

	attemptID := status.AttemptID
	if attemptID == "" {
		attemptID = fallbackAttemptID
	}
	if attemptID == "" {
		return status, false, nil
	}

	attemptKey := exec.AttemptKeyForStatus(status, attemptID)
	if attemptKey == "" {
		return status, false, nil
	}

	now := time.Now().UTC()
	if cfg.Now != nil {
		now = cfg.Now().UTC()
	}

	lease, err := cfg.DAGRunLeaseStore.Get(ctx, attemptKey)
	switch {
	case err == nil:
		if exec.LeaseMatchesStatus(lease, status, attemptID, now, staleLeaseThresholdOrDefault(cfg.StaleLeaseThreshold)) {
			return status, false, nil
		}
		if lease != nil && !exec.LeaseIdentityMatchesStatus(lease, status, attemptID) {
			return status, false, nil
		}
	case errors.Is(err, exec.ErrDAGRunLeaseNotFound):
	default:
		return status, false, err
	}

	record, err := cfg.WorkerHeartbeatStore.Get(ctx, workerID)
	switch {
	case err == nil:
		if workerHeartbeatFresh(record, now, staleWorkerHeartbeatThresholdOrDefault(cfg.StaleWorkerHeartbeatThreshold)) {
			if record.Stats == nil {
				return status, false, nil
			}
			if workerHeartbeatReportsAttempt(record, status, attemptKey) {
				return status, false, nil
			}
		}
	case errors.Is(err, exec.ErrWorkerHeartbeatNotFound):
	default:
		return status, false, err
	}

	reason := exec.DistributedLeaseExpiredReason(workerID)
	currentStatus, swapped, err := cfg.DAGRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		status.DAGRun(),
		attemptID,
		status.Status,
		func(current *exec.DAGRunStatus) error {
			markActiveStatusFailed(current, reason, now)
			return nil
		},
		exec.WithCompareAndSwapRootDAGRun(status.Root),
		exec.WithCompareAndSwapExpectedAttemptKey(attemptKey),
	)
	if err != nil {
		return nil, false, err
	}
	if !swapped {
		if currentStatus != nil {
			return currentStatus, false, nil
		}
		return status, false, nil
	}
	return currentStatus, true, nil
}

func remoteWorkerIDForStatus(status *exec.DAGRunStatus, fallbackWorkerID string) (string, bool) {
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

func statusEligibleForDistributedRepair(status core.Status) bool {
	return status == core.Running || status == core.Queued || status == core.NotStarted
}

func workerHeartbeatFresh(record *exec.WorkerHeartbeatRecord, now time.Time, threshold time.Duration) bool {
	if record == nil || record.LastHeartbeatAt == 0 || threshold <= 0 {
		return false
	}
	return now.Sub(record.LastHeartbeatTime()) < threshold
}

func workerHeartbeatReportsAttempt(record *exec.WorkerHeartbeatRecord, status *exec.DAGRunStatus, attemptKey string) bool {
	if record == nil || record.Stats == nil {
		return false
	}

	for _, task := range record.Stats.RunningTasks {
		if task == nil {
			continue
		}
		if task.AttemptKey != "" && task.AttemptKey == attemptKey {
			return true
		}
		if task.AttemptKey == "" && task.DagRunId == status.DAGRunID && task.DagName == status.Name {
			return true
		}
	}

	return false
}

func staleLeaseThresholdOrDefault(threshold time.Duration) time.Duration {
	if threshold <= 0 {
		return exec.DefaultStaleLeaseThreshold
	}
	return threshold
}

func staleWorkerHeartbeatThresholdOrDefault(threshold time.Duration) time.Duration {
	if threshold <= 0 {
		return defaultStaleWorkerHeartbeatThreshold
	}
	return threshold
}
