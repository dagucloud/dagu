// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill_test

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A DAG shows its open backfill, then the backfill that ended failed until
// the slot it failed succeeds.
func TestLatest(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	latest := func() string {
		t.Helper()
		report, err := backfill.Latest(f.ctx, f.store, f.dispatcher.ReadRun, f.repository, f.dag, f.sched)
		require.NoError(t, err)
		if report == nil {
			return ""
		}
		return report.Record.ID + " " + string(report.Record.Status)
	}
	assert.Empty(t, latest(), "no backfill")

	record := f.submit(backfill.Options{})
	assert.Equal(t, record.ID+" ", latest(), "the open backfill")

	f.tick()
	f.finish(f.queued[0], ir.Failed)
	f.tick()
	assert.Equal(t, record.ID+" failed", latest(), "its failed slot has no successful run")

	require.NoError(t, f.write("fixed", at(1, 2), ir.Succeeded, ir.TriggerTypeManual, ""))
	assert.Empty(t, latest(), "the failed slot succeeded since")
}

// A newer failed backfill whose slots succeeded since leaves an older one
// that still has a failed slot as the DAG's latest.
func TestLatestLooksPastAResolvedFailure(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	older := f.submit(backfill.Options{})
	f.tick()
	f.finish(f.queued[0], ir.Failed)
	f.tick()
	require.Equal(t, backfill.StatusFailed, f.record(older.ID).Status)

	options := backfill.Options{Slots: []time.Time{at(2, 2)}, Rerun: backfill.RerunNone, MaxConcurrency: 1, OnFailure: backfill.OnFailureStop}
	result, err := f.service.Submit(f.ctx, backfill.SubmitRequest{
		Plan:    backfill.PlanRequest{DAG: f.dag, Schedule: f.sched, Slots: options.Slots, Rerun: backfill.RerunNone},
		Options: options,
	})
	require.NoError(t, err)
	newer := result.Record
	f.tick()
	f.finish(f.queued[1], ir.Failed)
	f.tick()
	require.Equal(t, backfill.StatusFailed, f.record(newer.ID).Status)
	require.NoError(t, f.write("fixed", at(2, 2), ir.Succeeded, ir.TriggerTypeManual, ""))

	report, err := backfill.Latest(f.ctx, f.store, f.dispatcher.ReadRun, f.repository, f.dag, f.sched)
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.Equal(t, older.ID, report.Record.ID)
}
