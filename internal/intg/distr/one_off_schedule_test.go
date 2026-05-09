// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package distr_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/scheduler"
	"github.com/stretchr/testify/require"
)

func TestOneOffScheduleRunsDistributed(t *testing.T) {
	scheduledAt := time.Date(2026, 3, 29, 2, 10, 0, 0, time.UTC)
	armedAt := scheduledAt.Add(-time.Minute)

	f := newTestFixture(t, `
name: distributed-one-off-test
schedule:
  start:
    - at: "`+scheduledAt.Format(time.RFC3339)+`"
worker_selector:
  test: "true"
steps:
  - name: echo-step
    run: echo "distributed-one-off"
`)
	defer f.cleanup()

	f.coord.Config.Scheduler.RetryFailureWindow = 0

	var callCount atomic.Int32
	f.startSchedulerWithClock(30*time.Second, func() time.Time {
		if callCount.Add(1) <= 2 {
			return armedAt
		}
		return scheduledAt
	})

	status := f.waitForStatus(core.Succeeded, 20*time.Second)

	oneOffSchedule, err := core.NewOneOffSchedule(scheduledAt.Format(time.RFC3339))
	require.NoError(t, err)

	require.Equal(
		t,
		scheduler.GenerateOneOffRunID(f.dagWrapper.Name, oneOffSchedule.Fingerprint(), scheduledAt),
		status.DAGRunID,
	)
	require.Equal(t, stringutil.FormatTime(scheduledAt), status.ScheduleTime)
	f.assertWorkerID(status, "worker-1")
	f.assertAllNodesSucceeded(status)

	f.stopScheduler()
	f.startSchedulerWithClock(30*time.Second, func() time.Time {
		return scheduledAt.Add(time.Minute)
	})

	require.Never(t, func() bool {
		statuses, err := f.coord.DAGRunStore.ListStatuses(
			f.coord.Context,
			exec.WithExactName(f.dagWrapper.Name),
			exec.WithAllHistory(),
		)
		require.NoError(t, err)
		return len(statuses) != 1
	}, 3*time.Second, 100*time.Millisecond)
}
