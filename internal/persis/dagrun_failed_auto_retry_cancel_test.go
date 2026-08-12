// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failedAutoRetryCancelStoreStub struct {
	testutil.DAGRunStoreStub
	compareAndSwap func(
		ctx context.Context,
		req persis.DAGRunCompareAndSwapStatusRequest,
	) (*ir.DAGRunStatus, bool, error)
}

func (s *failedAutoRetryCancelStoreStub) CompareAndSwapLatestAttemptStatus(
	ctx context.Context,
	req persis.DAGRunCompareAndSwapStatusRequest,
) (*ir.DAGRunStatus, bool, error) {
	return s.compareAndSwap(ctx, req)
}

func TestDAGRunRepositoryCancelFailedAutoRetryPendingRun(t *testing.T) {
	t.Parallel()

	status := &ir.DAGRunStatus{
		Name:           "retry-dag",
		DAGRunID:       "run-1",
		AttemptID:      "attempt-1",
		Status:         ir.Failed,
		AutoRetryCount: 1,
		AutoRetryLimit: 3,
	}

	t.Run("MutatesToAborted", func(t *testing.T) {
		t.Parallel()
		store := &failedAutoRetryCancelStoreStub{
			compareAndSwap: func(
				_ context.Context,
				req persis.DAGRunCompareAndSwapStatusRequest,
			) (*ir.DAGRunStatus, bool, error) {
				assert.Equal(t, status.DAGRun(), req.DAGRun)
				assert.Equal(t, status.AttemptID, req.ExpectedAttemptID)
				assert.Equal(t, ir.Failed, req.ExpectedStatus)

				latest := &ir.DAGRunStatus{Status: ir.Failed}
				require.NoError(t, req.Mutate(latest))
				assert.Equal(t, ir.Aborted, latest.Status)
				return latest, true, nil
			},
		}
		repository := persis.NewDAGRunRepository(store, nil, persis.DAGRunRepositoryOptions{})
		require.NoError(t, repository.CancelFailedAutoRetryPendingRun(context.Background(), status))
	})

	t.Run("ReturnsStateChangedError", func(t *testing.T) {
		t.Parallel()
		store := &failedAutoRetryCancelStoreStub{
			compareAndSwap: func(
				context.Context,
				persis.DAGRunCompareAndSwapStatusRequest,
			) (*ir.DAGRunStatus, bool, error) {
				return &ir.DAGRunStatus{Status: ir.Queued}, false, nil
			},
		}
		repository := persis.NewDAGRunRepository(store, nil, persis.DAGRunRepositoryOptions{})
		err := repository.CancelFailedAutoRetryPendingRun(context.Background(), status)
		require.ErrorIs(t, err, dagrun.ErrFailedAutoRetryCancelStateChanged)

		var stateChangedErr *dagrun.FailedAutoRetryCancelStateChangedError
		require.ErrorAs(t, err, &stateChangedErr)
		require.NotNil(t, stateChangedErr.CurrentStatus)
		assert.Equal(t, ir.Queued, stateChangedErr.CurrentStatus.Status)
	})

	t.Run("ReturnsErrorForIneligibleStatus", func(t *testing.T) {
		t.Parallel()
		compareAndSwapCalled := false
		store := &failedAutoRetryCancelStoreStub{
			compareAndSwap: func(
				context.Context,
				persis.DAGRunCompareAndSwapStatusRequest,
			) (*ir.DAGRunStatus, bool, error) {
				compareAndSwapCalled = true
				return nil, false, nil
			},
		}
		ineligible := *status
		ineligible.Status = ir.Succeeded
		repository := persis.NewDAGRunRepository(store, nil, persis.DAGRunRepositoryOptions{})
		err := repository.CancelFailedAutoRetryPendingRun(context.Background(), &ineligible)
		require.ErrorContains(t, err, "not eligible")
		assert.False(t, compareAndSwapCalled)
	})

	t.Run("WrapsStoreError", func(t *testing.T) {
		t.Parallel()
		storeErr := errors.New("store failure")
		store := &failedAutoRetryCancelStoreStub{
			compareAndSwap: func(
				context.Context,
				persis.DAGRunCompareAndSwapStatusRequest,
			) (*ir.DAGRunStatus, bool, error) {
				return nil, false, storeErr
			},
		}
		repository := persis.NewDAGRunRepository(store, nil, persis.DAGRunRepositoryOptions{})
		err := repository.CancelFailedAutoRetryPendingRun(context.Background(), status)
		require.ErrorIs(t, err, storeErr)
		assert.ErrorContains(t, err, "cancel failed auto-retry pending DAG-run")
	})
}
