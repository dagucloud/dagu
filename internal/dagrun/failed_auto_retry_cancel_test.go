// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
)

func TestFailedAutoRetryCancelEligibilityOf(t *testing.T) {
	t.Parallel()

	base := &ir.DAGRunStatus{
		Name:           "retry-dag",
		DAGRunID:       "run-1",
		AttemptID:      "attempt-1",
		Status:         ir.Failed,
		AutoRetryCount: 1,
		AutoRetryLimit: 3,
	}

	t.Run("Eligible", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, FailedAutoRetryCancelEligible, FailedAutoRetryCancelEligibilityOf(base))
		assert.True(t, CanCancelFailedAutoRetryPendingRun(base))
	})

	t.Run("MissingStatus", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, FailedAutoRetryCancelMissingStatus, FailedAutoRetryCancelEligibilityOf(nil))
		assert.False(t, CanCancelFailedAutoRetryPendingRun(nil))
	})

	t.Run("NotRoot", func(t *testing.T) {
		t.Parallel()
		status := *base
		status.Parent = ir.NewDAGRunRef("retry-dag", "parent-run")
		assert.Equal(t, FailedAutoRetryCancelNotRoot, FailedAutoRetryCancelEligibilityOf(&status))
	})

	t.Run("NotPendingAutoRetry", func(t *testing.T) {
		t.Parallel()
		status := *base
		status.AutoRetryCount = status.AutoRetryLimit
		assert.Equal(t, FailedAutoRetryCancelNotPending, FailedAutoRetryCancelEligibilityOf(&status))
		assert.False(t, CanCancelFailedAutoRetryPendingRun(&status))
	})

	t.Run("NotPendingNoRetryConfigured", func(t *testing.T) {
		t.Parallel()
		status := *base
		status.AutoRetryLimit = 0
		status.AutoRetryCount = 0
		assert.Equal(t, FailedAutoRetryCancelNotPending, FailedAutoRetryCancelEligibilityOf(&status))
		assert.False(t, CanCancelFailedAutoRetryPendingRun(&status))
	})

	t.Run("NotFailed", func(t *testing.T) {
		t.Parallel()
		status := *base
		status.Status = ir.Succeeded
		assert.Equal(t, FailedAutoRetryCancelNotPending, FailedAutoRetryCancelEligibilityOf(&status))
	})
}
