// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagucloud/dagu/internal/core"
)

// FailedAutoRetryCancelEligibility describes whether a failed DAG-run can be
// canceled before the scheduler issues the next DAG-level auto-retry.
type FailedAutoRetryCancelEligibility int

const (
	FailedAutoRetryCancelEligible FailedAutoRetryCancelEligibility = iota
	FailedAutoRetryCancelMissingStatus
	FailedAutoRetryCancelNotRoot
	FailedAutoRetryCancelNotPending
)

var ErrFailedAutoRetryCancelStateChanged = errors.New(
	"dag-run state changed before failed auto-retry cancel could be applied",
)

// FailedAutoRetryCancelStateChangedError reports the latest observed status when
// another actor changed the latest attempt before the cancel CAS completed.
type FailedAutoRetryCancelStateChangedError struct {
	CurrentStatus *DAGRunStatus
}

func (e *FailedAutoRetryCancelStateChangedError) Error() string {
	return ErrFailedAutoRetryCancelStateChanged.Error()
}

func (e *FailedAutoRetryCancelStateChangedError) Unwrap() error {
	return ErrFailedAutoRetryCancelStateChanged
}

// FailedAutoRetryCancelEligibilityOf classifies whether the provided status can
// be canceled while it is failed and still waiting for a DAG-level auto-retry.
func FailedAutoRetryCancelEligibilityOf(status *DAGRunStatus) FailedAutoRetryCancelEligibility {
	switch {
	case status == nil:
		return FailedAutoRetryCancelMissingStatus
	case status.Status != core.Failed:
		return FailedAutoRetryCancelNotPending
	case !status.Parent.Zero():
		return FailedAutoRetryCancelNotRoot
	case status.AutoRetryLimit <= 0 || status.AutoRetryCount >= status.AutoRetryLimit:
		return FailedAutoRetryCancelNotPending
	default:
		return FailedAutoRetryCancelEligible
	}
}

// CanCancelFailedAutoRetryPendingRun returns true when a failed DAG-run is a
// root run and still has remaining DAG-level auto-retry budget.
func CanCancelFailedAutoRetryPendingRun(status *DAGRunStatus) bool {
	return FailedAutoRetryCancelEligibilityOf(status) == FailedAutoRetryCancelEligible
}

type latestAttemptStatusSwapper interface {
	CompareAndSwapLatestAttemptStatus(
		ctx context.Context,
		dagRun DAGRunRef,
		expectedAttemptID string,
		expectedStatus core.Status,
		mutate func(*DAGRunStatus) error,
		opts ...CompareAndSwapStatusOption,
	) (*DAGRunStatus, bool, error)
}

// CancelFailedAutoRetryPendingRun atomically marks the latest failed attempt as
// aborted so the retry scanner stops treating it as pending auto-retry.
func CancelFailedAutoRetryPendingRun(
	ctx context.Context,
	dagRunStore latestAttemptStatusSwapper,
	status *DAGRunStatus,
) error {
	if !CanCancelFailedAutoRetryPendingRun(status) {
		return fmt.Errorf("dag-run is not eligible for failed auto-retry cancel")
	}

	updatedStatus, swapped, err := dagRunStore.CompareAndSwapLatestAttemptStatus(
		ctx,
		status.DAGRun(),
		status.AttemptID,
		core.Failed,
		func(latest *DAGRunStatus) error {
			latest.Status = core.Aborted
			return nil
		},
	)
	if err != nil {
		return fmt.Errorf("cancel failed auto-retry pending DAG-run: %w", err)
	}
	if swapped {
		return nil
	}

	return &FailedAutoRetryCancelStateChangedError{CurrentStatus: updatedStatus}
}
