// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// retryFixture is a repository whose OnRetry records the status each retry
// is about to give a run, with the status the run still had then, and fails
// while fail is set.
type retryFixture struct {
	t          *testing.T
	ctx        context.Context
	repository *persis.DAGRunRepository
	dag        *ir.DAG
	ref        ir.DAGRunRef
	// retried holds "<status before> -> <status after>" per call.
	retried []string
	fail    bool
}

func newRetryFixture(t *testing.T) *retryFixture {
	t.Helper()
	f := &retryFixture{t: t, ctx: context.Background(), dag: &ir.DAG{Name: "etl"}, ref: ir.NewDAGRunRef("etl", "run-1")}
	f.repository = testutil.NewFileDAGRunRepository(filepath.Join(t.TempDir(), "dag-runs"), persis.DAGRunRepositoryOptions{
		OnRetry: func(_ context.Context, status *ir.DAGRunStatus) error {
			f.retried = append(f.retried, f.latest().String()+" -> "+status.Status.String())
			if f.fail {
				return errors.New("backfill store is down")
			}
			return nil
		},
	})
	return f
}

func (f *retryFixture) latest() ir.Status {
	f.t.Helper()
	attempt, err := f.repository.FindAttempt(f.ctx, f.ref)
	require.NoError(f.t, err)
	status, err := attempt.ReadStatusUncached(f.ctx)
	require.NoError(f.t, err)
	return status.Status
}

// write creates an attempt of the run and writes each status to it; it
// returns the attempt's ID and the error of the first write that failed.
func (f *retryFixture) write(options persis.DAGRunCreateAttemptOptions, statuses ...ir.Status) (string, error) {
	f.t.Helper()
	attempt, err := f.repository.CreateAttempt(f.ctx, f.dag, time.Now(), f.ref.ID, options)
	require.NoError(f.t, err)
	require.NoError(f.t, attempt.Open(f.ctx))
	defer func() { require.NoError(f.t, attempt.Close(f.ctx)) }()
	for _, status := range statuses {
		if err := attempt.Write(f.ctx, ir.DAGRunStatus{Name: f.ref.Name, DAGRunID: f.ref.ID, AttemptID: attempt.ID(), Status: status}); err != nil {
			return attempt.ID(), err
		}
	}
	return attempt.ID(), nil
}

func (f *retryFixture) swap(attemptID string, from, to ir.Status) error {
	f.t.Helper()
	_, _, err := f.repository.CompareAndSwapLatestAttemptStatus(f.ctx, f.ref, attemptID, from,
		func(status *ir.DAGRunStatus) error {
			status.Status = to
			return nil
		}, persis.DAGRunCompareAndSwapOptions{})
	return err
}

// OnRetry learns of each retry that makes an ended run active again, before
// the run becomes active: when the retry queues the run, and before the
// first write of a retry attempt. A new run and a change between active
// statuses are no retry.
func TestRepositoryReportsRetries(t *testing.T) {
	t.Parallel()
	f := newRetryFixture(t)

	attemptID, err := f.write(persis.DAGRunCreateAttemptOptions{}, ir.Queued, ir.Running, ir.Failed)
	require.NoError(t, err)
	assert.Empty(t, f.retried, "a new run")

	require.NoError(t, f.swap(attemptID, ir.Failed, ir.Queued))
	require.NoError(t, f.swap(attemptID, ir.Queued, ir.Running))
	require.NoError(t, f.swap(attemptID, ir.Running, ir.Failed))
	assert.Equal(t, []string{"failed -> queued"}, f.retried, "a retry queued the run")

	_, err = f.write(persis.DAGRunCreateAttemptOptions{Retry: true}, ir.Running, ir.Succeeded)
	require.NoError(t, err)
	assert.Equal(t, []string{"failed -> queued", "failed -> running"}, f.retried, "a retry attempt started")
}

// A retry fails when OnRetry fails, and the run stays ended.
func TestRepositoryFailsARetryOnRetryFails(t *testing.T) {
	t.Parallel()
	f := newRetryFixture(t)
	attemptID, err := f.write(persis.DAGRunCreateAttemptOptions{}, ir.Failed)
	require.NoError(t, err)
	f.fail = true

	require.Error(t, f.swap(attemptID, ir.Failed, ir.Queued))
	assert.Equal(t, ir.Failed, f.latest(), "a queued retry")

	_, err = f.write(persis.DAGRunCreateAttemptOptions{Retry: true}, ir.Running)
	require.Error(t, err)
	assert.Len(t, f.retried, 2)
}
