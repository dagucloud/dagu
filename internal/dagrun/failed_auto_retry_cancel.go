// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"errors"

	"github.com/dagucloud/dagu/v2/internal/ir"
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
	CurrentStatus *ir.DAGRunStatus
}

func (e *FailedAutoRetryCancelStateChangedError) Error() string {
	return ErrFailedAutoRetryCancelStateChanged.Error()
}

func (e *FailedAutoRetryCancelStateChangedError) Unwrap() error {
	return ErrFailedAutoRetryCancelStateChanged
}

// FailedAutoRetryCancelEligibilityOf classifies whether the provided status can
// be canceled while it is failed and still waiting for a DAG-level auto-retry.
func FailedAutoRetryCancelEligibilityOf(status *ir.DAGRunStatus) FailedAutoRetryCancelEligibility {
	switch {
	case status == nil:
		return FailedAutoRetryCancelMissingStatus
	case status.Status != ir.Failed:
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
func CanCancelFailedAutoRetryPendingRun(status *ir.DAGRunStatus) bool {
	return FailedAutoRetryCancelEligibilityOf(status) == FailedAutoRetryCancelEligible
}
