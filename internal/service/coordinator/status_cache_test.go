// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"errors"
	"testing"
	"time"

	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// The file store's cache hands every reader the same status object. A
// coordinator write must change only its own copy: otherwise other readers
// see a status that is not stored, and keep seeing it when the write fails.
func TestHandlerWriteLeavesCachedStatusAlone(t *testing.T) {
	t.Parallel()

	errWrite := errors.New("disk full")

	t.Run("HeldDispatch", func(t *testing.T) {
		t.Parallel()

		h := NewHandler(HandlerConfig{})
		attempt := &mockAttempt{
			status:      &ir.DAGRunStatus{Name: "routed", DAGRunID: "run-held", Status: ir.NotStarted},
			sharedReads: true,
			writeError:  errWrite,
		}

		err := h.markPreparedAttemptHeld(t.Context(), cpuTask("run-held"), &preparedDispatchAttempt{attempt: attempt}, errNoAvailableWorkers)
		require.ErrorIs(t, err, errWrite)

		stored, err := attempt.ReadStatus(t.Context())
		require.NoError(t, err)
		assert.Equal(t, ir.NotStarted, stored.Status)
		assert.Empty(t, stored.Conditions)
	})

	t.Run("LeaseRefresh", func(t *testing.T) {
		t.Parallel()

		store := newMockDAGRunStore()
		h := NewHandler(HandlerConfig{DAGRunRepository: store.repository, StaleLeaseThreshold: 10 * time.Second})
		lease := time.Now().Add(-time.Minute).UnixMilli()
		attempt := store.addAttempt(ir.NewDAGRunRef("dag", "run-lease"), &ir.DAGRunStatus{
			Name:       "dag",
			DAGRunID:   "run-lease",
			AttemptKey: "attempt-key",
			Status:     ir.Running,
			WorkerID:   "worker-1",
			LeaseAt:    lease,
		})
		attempt.sharedReads = true
		attempt.writeError = errWrite

		h.refreshLeaseForRunningTask(t.Context(), "worker-1", &coordinatorv1.RunningTask{
			DagRunId:   "run-lease",
			DagName:    "dag",
			AttemptKey: "attempt-key",
		}, time.Now())

		stored, err := attempt.ReadStatus(t.Context())
		require.NoError(t, err)
		assert.Equal(t, lease, stored.LeaseAt)
	})

	t.Run("NotStartedCancellation", func(t *testing.T) {
		t.Parallel()

		attempt := &mockAttempt{
			status:      &ir.DAGRunStatus{Name: "dag", DAGRunID: "run-cancel", Status: ir.Queued},
			sharedReads: true,
			writeError:  errWrite,
		}

		err := finalizeNotStartedCancellation(t.Context(), attempt)
		require.ErrorIs(t, err, errWrite)

		stored, err := attempt.ReadStatus(t.Context())
		require.NoError(t, err)
		assert.Equal(t, ir.Queued, stored.Status)
		assert.Empty(t, stored.Error)
	})
}
