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

// An open backfill estimates the time its slots left take from the pace of
// the runs it has finished since it was last planned.
func TestDescribeEstimatesTimeLeft(t *testing.T) {
	t.Parallel()
	f := newDispatchFixture(t)
	record := f.submit(backfill.Options{})
	estimate := func() time.Duration {
		t.Helper()
		report, err := backfill.Describe(f.ctx, f.store, f.dispatcher.ReadRun, f.record(record.ID))
		require.NoError(t, err)
		return report.Estimate
	}

	f.tick()
	require.Len(t, f.queued, 1)
	assert.Zero(t, estimate(), "no run has finished yet")

	f.clock = f.clock.Add(10 * time.Minute)
	f.finish(f.queued[0], ir.Succeeded)
	f.tick()
	// One run in 10 minutes, and two slots left.
	assert.Equal(t, 20*time.Minute, estimate())
}
