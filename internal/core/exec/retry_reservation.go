// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
)

const retryReservationConditionType = "RetryReserved"

// RetryReservation records the source state held while a direct retry starts.
type RetryReservation struct {
	DAGRun             DAGRunRef
	AttemptID          string
	OriginalStatus     core.Status
	OriginalQueuedAt   string
	OriginalConditions []DAGRunCondition
	OriginalTrigger    core.TriggerType
}

// ReserveRetry atomically reserves the latest terminal attempt for a direct retry.
func ReserveRetry(ctx context.Context, store DAGRunStore, status *DAGRunStatus) (*DAGRunStatus, *RetryReservation, error) {
	if store == nil {
		return nil, nil, errors.New("reserve retry: DAG-run store is not configured")
	}
	if status == nil {
		return nil, nil, errors.New("reserve retry: DAG-run status is nil")
	}
	if status.Status.IsActive() {
		return status, nil, fmt.Errorf("%w: %s", ErrDAGRunActive, status.DAGRun())
	}

	reservation := &RetryReservation{
		DAGRun:             status.DAGRun(),
		AttemptID:          status.AttemptID,
		OriginalStatus:     status.Status,
		OriginalQueuedAt:   status.QueuedAt,
		OriginalConditions: append([]DAGRunCondition(nil), status.Conditions...),
		OriginalTrigger:    status.TriggerType,
	}
	updated, swapped, err := store.CompareAndSwapLatestAttemptStatus(
		ctx,
		reservation.DAGRun,
		reservation.AttemptID,
		reservation.OriginalStatus,
		func(latest *DAGRunStatus) error {
			now := time.Now()
			latest.Status = core.Queued
			latest.QueuedAt = stringutil.FormatTime(now)
			latest.Conditions = []DAGRunCondition{NewDAGRunCondition(
				retryReservationConditionType,
				"True",
				"DirectRetry",
				"A direct retry has reserved this DAG run",
				now,
			)}
			latest.TriggerType = core.TriggerTypeRetry
			return nil
		},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("reserve retry: %w", err)
	}
	if !swapped {
		if updated != nil && updated.Status.IsActive() {
			return updated, nil, fmt.Errorf("%w: %s", ErrDAGRunActive, reservation.DAGRun)
		}
		return updated, nil, ErrRetryStaleLatest
	}
	return updated, reservation, nil
}

// IsRetryReserved reports whether a queued run is reserved by a direct retry request.
func IsRetryReserved(status *DAGRunStatus) bool {
	if status == nil || status.Status != core.Queued {
		return false
	}
	for _, condition := range status.Conditions {
		if condition.Type == retryReservationConditionType && condition.Status == "True" {
			return true
		}
	}
	return false
}

// RollbackRetryReservation restores a reservation that did not start execution.
func RollbackRetryReservation(ctx context.Context, store DAGRunStore, reservation *RetryReservation) error {
	if store == nil || reservation == nil {
		return nil
	}
	_, swapped, err := store.CompareAndSwapLatestAttemptStatus(
		ctx,
		reservation.DAGRun,
		reservation.AttemptID,
		core.Queued,
		func(latest *DAGRunStatus) error {
			latest.Status = reservation.OriginalStatus
			latest.QueuedAt = reservation.OriginalQueuedAt
			latest.Conditions = append([]DAGRunCondition(nil), reservation.OriginalConditions...)
			latest.TriggerType = reservation.OriginalTrigger
			return nil
		},
	)
	if err != nil {
		return err
	}
	if !swapped {
		return ErrRetryStaleLatest
	}
	return nil
}

// FailRetryReservation marks an internally claimed reservation that could not start.
func FailRetryReservation(ctx context.Context, store DAGRunStore, dagRun DAGRunRef, attemptID string, cause error) error {
	if store == nil || dagRun.Zero() || attemptID == "" {
		return nil
	}
	_, swapped, err := store.CompareAndSwapLatestAttemptStatus(
		ctx,
		dagRun,
		attemptID,
		core.Queued,
		func(latest *DAGRunStatus) error {
			latest.Status = core.Failed
			latest.QueuedAt = ""
			latest.FinishedAt = stringutil.FormatTime(time.Now())
			if cause != nil {
				latest.Error = cause.Error()
			}
			return nil
		},
	)
	if err != nil {
		return err
	}
	if !swapped {
		return ErrRetryStaleLatest
	}
	return nil
}
