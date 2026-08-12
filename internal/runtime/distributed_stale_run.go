// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

const defaultStaleWorkerHeartbeatThreshold = 30 * time.Second

// StaleRunRepairConfig provides the stores, thresholds, and clock used to
// confirm and repair stale remote runs.
type StaleRunRepairConfig struct {
	DAGRunRepository              *dagrun.Repository
	DAGRunLeaseStore              dispatch.DAGRunLeaseStore
	WorkerHeartbeatStore          dispatch.WorkerHeartbeatStore
	StaleLeaseThreshold           time.Duration
	StaleWorkerHeartbeatThreshold time.Duration
	Now                           func() time.Time
}

// RepairStaleRemoteRun marks an active remote run failed only when both the
// claim lease and worker evidence confirm that its execution claim is gone.
func RepairStaleRemoteRun(
	ctx context.Context,
	cfg StaleRunRepairConfig,
	status *ir.DAGRunStatus,
	fallbackAttemptID string,
	fallbackWorkerID string,
) (*ir.DAGRunStatus, bool, error) {
	if status == nil || cfg.DAGRunRepository == nil || cfg.DAGRunLeaseStore == nil || cfg.WorkerHeartbeatStore == nil {
		return status, false, nil
	}

	workerID, ok := remoteWorkerIDForStatus(status, fallbackWorkerID)
	if !ok {
		return status, false, nil
	}
	if !statusRepairable(status.Status) {
		return status, false, nil
	}

	attemptID := status.AttemptID
	if attemptID == "" {
		attemptID = fallbackAttemptID
	}
	if attemptID == "" {
		return status, false, nil
	}

	attemptKey := dispatch.AttemptKeyForStatus(status, attemptID)
	if attemptKey == "" {
		return status, false, nil
	}

	now := time.Now().UTC()
	if cfg.Now != nil {
		now = cfg.Now().UTC()
	}

	claimKey := status.EffectiveClaimKey()
	if claimKey == "" {
		claimKey = attemptKey
	}
	lease, err := cfg.DAGRunLeaseStore.Get(ctx, claimKey)
	switch {
	case err == nil:
		if lease != nil && lease.AttemptKey != claimKey {
			return status, false, nil
		}
	case errors.Is(err, dispatch.ErrDAGRunLeaseNotFound):
	default:
		return status, false, err
	}
	if lease.MatchesClaim(claimKey, workerID) && lease.IsFresh(now, staleLeaseThresholdOrDefault(cfg.StaleLeaseThreshold)) {
		return status, false, nil
	}

	record, err := cfg.WorkerHeartbeatStore.Get(ctx, workerID)
	switch {
	case err == nil:
		if workerHeartbeatFresh(record, now, staleWorkerHeartbeatThresholdOrDefault(cfg.StaleWorkerHeartbeatThreshold)) {
			if record.Stats == nil {
				return status, false, nil
			}
			if workerReportsClaim(record, status, attemptKey, claimKey) {
				return status, false, nil
			}
		}
	case errors.Is(err, dispatch.ErrWorkerHeartbeatNotFound):
	default:
		return status, false, err
	}

	reason := dispatch.DistributedLeaseExpiredReason(workerID)
	currentStatus, swapped, err := cfg.DAGRunRepository.CompareAndSwapLatestAttemptStatus(
		ctx,
		status.DAGRun(),
		attemptID,
		status.Status,
		func(current *ir.DAGRunStatus) error {
			markActiveStatusFailed(current, reason, now)
			return nil
		},
		dagrun.WithCompareAndSwapRootDAGRun(status.Root),
		dagrun.WithCompareAndSwapExpectedAttemptKey(attemptKey),
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

func remoteWorkerIDForStatus(status *ir.DAGRunStatus, fallbackWorkerID string) (string, bool) {
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

func statusRepairable(status ir.Status) bool {
	return status == ir.Running || status == ir.Queued || status == ir.NotStarted
}

func workerHeartbeatFresh(record *dispatch.WorkerHeartbeatRecord, now time.Time, threshold time.Duration) bool {
	if record == nil || record.LastHeartbeatAt == 0 || threshold <= 0 {
		return false
	}
	return now.Sub(record.LastHeartbeatTime()) < threshold
}

func workerReportsClaim(record *dispatch.WorkerHeartbeatRecord, status *ir.DAGRunStatus, attemptKey, claimKey string) bool {
	if record == nil || record.Stats == nil {
		return false
	}

	for _, task := range record.Stats.RunningTasks {
		if task == nil {
			continue
		}
		if task.AttemptKey != "" && task.AttemptKey == claimKey {
			return true
		}
		if task.AttemptKey == "" && claimKey == attemptKey && task.DAGRunID == status.DAGRunID && task.DAGName == status.Name {
			return true
		}
	}

	return false
}

func staleLeaseThresholdOrDefault(threshold time.Duration) time.Duration {
	if threshold <= 0 {
		return dagrun.DefaultStaleLeaseThreshold
	}
	return threshold
}

func staleWorkerHeartbeatThresholdOrDefault(threshold time.Duration) time.Duration {
	if threshold <= 0 {
		return defaultStaleWorkerHeartbeatThreshold
	}
	return threshold
}
