// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec

import (
	"context"
	"errors"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failedAutoRetryCancelStoreStub struct {
	compareAndSwap func(
		ctx context.Context,
		dagRun DAGRunRef,
		expectedAttemptID string,
		expectedStatus core.Status,
		mutate func(*DAGRunStatus) error,
	) (*DAGRunStatus, bool, error)
}

func (s *failedAutoRetryCancelStoreStub) CompareAndSwapLatestAttemptStatus(
	ctx context.Context,
	dagRun DAGRunRef,
	expectedAttemptID string,
	expectedStatus core.Status,
	mutate func(*DAGRunStatus) error,
	_ ...CompareAndSwapStatusOption,
) (*DAGRunStatus, bool, error) {
	return s.compareAndSwap(ctx, dagRun, expectedAttemptID, expectedStatus, mutate)
}

func TestFailedAutoRetryCancelEligibilityOf(t *testing.T) {
	t.Parallel()

	base := &DAGRunStatus{
		Name:           "retry-dag",
		DAGRunID:       "run-1",
		AttemptID:      "attempt-1",
		Status:         core.Failed,
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
		status.Parent = NewDAGRunRef("retry-dag", "parent-run")
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
		status.Status = core.Succeeded
		assert.Equal(t, FailedAutoRetryCancelNotPending, FailedAutoRetryCancelEligibilityOf(&status))
	})
}

func TestCancelFailedAutoRetryPendingRun(t *testing.T) {
	t.Parallel()

	status := &DAGRunStatus{
		Name:           "retry-dag",
		DAGRunID:       "run-1",
		AttemptID:      "attempt-1",
		Status:         core.Failed,
		AutoRetryCount: 1,
		AutoRetryLimit: 3,
	}

	t.Run("MutatesToAborted", func(t *testing.T) {
		t.Parallel()

		err := CancelFailedAutoRetryPendingRun(
			context.Background(),
			&failedAutoRetryCancelStoreStub{
				compareAndSwap: func(
					_ context.Context,
					dagRun DAGRunRef,
					expectedAttemptID string,
					expectedStatus core.Status,
					mutate func(*DAGRunStatus) error,
				) (*DAGRunStatus, bool, error) {
					assert.Equal(t, status.DAGRun(), dagRun)
					assert.Equal(t, status.AttemptID, expectedAttemptID)
					assert.Equal(t, core.Failed, expectedStatus)

					latest := &DAGRunStatus{Status: core.Failed}
					require.NoError(t, mutate(latest))
					assert.Equal(t, core.Aborted, latest.Status)
					return latest, true, nil
				},
			},
			status,
		)
		require.NoError(t, err)
	})

	t.Run("ReturnsStateChangedError", func(t *testing.T) {
		t.Parallel()

		err := CancelFailedAutoRetryPendingRun(
			context.Background(),
			&failedAutoRetryCancelStoreStub{
				compareAndSwap: func(
					_ context.Context,
					_ DAGRunRef,
					_ string,
					_ core.Status,
					_ func(*DAGRunStatus) error,
				) (*DAGRunStatus, bool, error) {
					return &DAGRunStatus{Status: core.Queued}, false, nil
				},
			},
			status,
		)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrFailedAutoRetryCancelStateChanged)

		var stateChangedErr *FailedAutoRetryCancelStateChangedError
		require.True(t, errors.As(err, &stateChangedErr))
		require.NotNil(t, stateChangedErr.CurrentStatus)
		assert.Equal(t, core.Queued, stateChangedErr.CurrentStatus.Status)
	})

	t.Run("ReturnsErrorForIneligibleStatus", func(t *testing.T) {
		t.Parallel()

		compareAndSwapCalled := false
		ineligible := &DAGRunStatus{
			Name:           "retry-dag",
			DAGRunID:       "run-1",
			AttemptID:      "attempt-1",
			Status:         core.Succeeded,
			AutoRetryCount: 1,
			AutoRetryLimit: 3,
		}

		err := CancelFailedAutoRetryPendingRun(
			context.Background(),
			&failedAutoRetryCancelStoreStub{
				compareAndSwap: func(
					_ context.Context,
					_ DAGRunRef,
					_ string,
					_ core.Status,
					_ func(*DAGRunStatus) error,
				) (*DAGRunStatus, bool, error) {
					compareAndSwapCalled = true
					return nil, false, nil
				},
			},
			ineligible,
		)
		require.Error(t, err)
		assert.ErrorContains(t, err, "not eligible")
		assert.False(t, compareAndSwapCalled)
	})

	t.Run("WrapsStoreError", func(t *testing.T) {
		t.Parallel()

		storeErr := errors.New("store failure")
		err := CancelFailedAutoRetryPendingRun(
			context.Background(),
			&failedAutoRetryCancelStoreStub{
				compareAndSwap: func(
					_ context.Context,
					_ DAGRunRef,
					_ string,
					_ core.Status,
					_ func(*DAGRunStatus) error,
				) (*DAGRunStatus, bool, error) {
					return nil, false, storeErr
				},
			},
			status,
		)
		require.Error(t, err)
		require.ErrorIs(t, err, storeErr)
		assert.ErrorContains(t, err, "cancel failed auto-retry pending DAG-run")
	})
}
