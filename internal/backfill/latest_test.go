// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill_test

import (
	"testing"

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
