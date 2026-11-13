// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// OnRetry learns of each retry that makes an ended run active again: when
// the retry queues the run, and on the first write of a retry attempt. A
// new run and a change between active statuses are no retry.
func TestRepositoryReportsRetries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var retried []ir.Status
	repository := testutil.NewFileDAGRunRepository(filepath.Join(t.TempDir(), "dag-runs"), persis.DAGRunRepositoryOptions{
		OnRetry: func(_ context.Context, status *ir.DAGRunStatus) {
			retried = append(retried, status.Status)
		},
	})
	dag := &ir.DAG{Name: "etl"}
	ref := ir.NewDAGRunRef(dag.Name, "run-1")
	write := func(options persis.DAGRunCreateAttemptOptions, statuses ...ir.Status) string {
		t.Helper()
		attempt, err := repository.CreateAttempt(ctx, dag, time.Now(), ref.ID, options)
		require.NoError(t, err)
		require.NoError(t, attempt.Open(ctx))
		for _, status := range statuses {
			require.NoError(t, attempt.Write(ctx, ir.DAGRunStatus{Name: ref.Name, DAGRunID: ref.ID, AttemptID: attempt.ID(), Status: status}))
		}
		require.NoError(t, attempt.Close(ctx))
		return attempt.ID()
	}
	swap := func(attemptID string, from, to ir.Status) {
		t.Helper()
		_, swapped, err := repository.CompareAndSwapLatestAttemptStatus(ctx, ref, attemptID, from,
			func(status *ir.DAGRunStatus) error {
				status.Status = to
				return nil
			}, persis.DAGRunCompareAndSwapOptions{})
		require.NoError(t, err)
		require.True(t, swapped)
	}

	attemptID := write(persis.DAGRunCreateAttemptOptions{}, ir.Queued, ir.Running, ir.Failed)
	assert.Empty(t, retried, "a new run")

	swap(attemptID, ir.Failed, ir.Queued)
	swap(attemptID, ir.Queued, ir.Running)
	assert.Equal(t, []ir.Status{ir.Queued}, retried, "a retry queued the run")

	write(persis.DAGRunCreateAttemptOptions{Retry: true}, ir.Running, ir.Succeeded)
	assert.Equal(t, []ir.Status{ir.Queued, ir.Running}, retried, "a retry attempt started")
}
