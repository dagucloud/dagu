// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec097_service_run_history_test

import (
	"os"
	"slices"
	"testing"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A run that fails gives a failed sample and a failed event with its run ID.
// The next run that does not fail gives a recovered event.
func TestFailedAndRecoveredEvents(t *testing.T) {
	t.Parallel()
	p := newProject(t, "flaky")
	flag := p.dagu.ProjectPath("flaky/fail.flag")
	p.dagu.WriteFile("flaky/fail.flag", "")
	p.startServer()
	p.startScheduler()

	var failed api.RunHistoryEvent
	require.Eventually(t, func() bool {
		found := eventsOfKind(p.events("service_flaky", nil).Events, api.RunHistoryEventKindFailed)
		if len(found) == 0 {
			return false
		}
		failed = found[0]
		return true
	}, wait, poll, "no failed event")

	require.NotEmpty(t, failed.RunId)
	retained := p.ended("service_flaky")
	idx := slices.IndexFunc(retained, func(r api.DAGRunSummary) bool { return r.DagRunId == failed.RunId })
	require.GreaterOrEqual(t, idx, 0, "the failed event names a run that the server keeps")
	assert.Equal(t, api.StatusFailed, retained[idx].Status)

	var windows api.SampleAvailability
	require.Eventually(t, func() bool {
		windows = p.availability("service_flaky")
		return windows.Windows[0].Failures >= 1
	}, wait, poll, "no failure in the availability")
	assert.NotNil(t, windows.LastFailureAt)
	require.NotNil(t, windows.Windows[0].Availability)
	assert.Less(t, *windows.Windows[0].Availability, 1.0)

	// Failed runs follow one another: one outage, one failed event.
	require.NoError(t, os.Remove(flag))
	var recovered api.RunHistoryEvent
	require.Eventually(t, func() bool {
		found := eventsOfKind(p.events("service_flaky", nil).Events, api.RunHistoryEventKindRecovered)
		if len(found) == 0 {
			return false
		}
		recovered = found[0]
		return true
	}, wait, poll, "no recovered event")

	require.NotEmpty(t, recovered.RunId)
	retained = p.ended("service_flaky")
	idx = slices.IndexFunc(retained, func(r api.DAGRunSummary) bool { return r.DagRunId == recovered.RunId })
	require.GreaterOrEqual(t, idx, 0, "the recovered event names a run that the server keeps")
	assert.Equal(t, api.StatusSuccess, retained[idx].Status)
	require.NotNil(t, recovered.FailedCount)
	assert.GreaterOrEqual(t, *recovered.FailedCount, int64(1))
	require.NotNil(t, recovered.OutageSeconds)
	assert.GreaterOrEqual(t, *recovered.OutageSeconds, int64(0))

	all := p.events("service_flaky", nil).Events
	assert.Len(t, eventsOfKind(all, api.RunHistoryEventKindFailed), 1, "one outage gives one failed event")
	assert.Empty(t, eventsOfKind(all, api.RunHistoryEventKindGap))
	assert.False(t, all[0].Time.Before(all[len(all)-1].Time), "the newest event is first")
}

// A scheduler that stops for more than restart_wait_sec plus 60 seconds
// leaves a gap: the first run after the restart records a gap event that holds
// the length of the gap and the ID of this run.
//
// This is the slow test of the suite: it waits 65 seconds with no scheduler.
func TestGapAfterStoppedScheduler(t *testing.T) {
	if testing.Short() {
		t.Skip("waits 65 seconds with no scheduler")
	}
	t.Parallel()
	p := newProject(t, "ok")
	p.startServer()
	scheduler := p.startScheduler()

	require.Eventually(t, func() bool {
		return p.availability("service_ok").TotalCount >= 2
	}, wait, poll, "fewer than 2 samples")
	require.Empty(t, eventsOfKind(p.events("service_ok", nil).Events, api.RunHistoryEventKindGap), "no gap while the scheduler runs")

	// A gap follows a run that succeeded. Stop between two runs, so that the
	// stop does not abort a run.
	require.Eventually(t, func() bool {
		runs := p.runs("service_ok")
		return len(runs) > 0 && len(p.ended("service_ok")) == len(runs) && runs[0].Status == api.StatusSuccess
	}, wait, 100*time.Millisecond, "no moment between two runs")
	scheduler.Stop()
	known := runIDs(p.runs("service_ok"))
	// restart_wait_sec is 1: a gap is more than 61 seconds.
	time.Sleep(65 * time.Second)
	restarted := p.startScheduler()

	var gap api.RunHistoryEvent
	found := assert.Eventually(t, func() bool {
		found := eventsOfKind(p.events("service_ok", nil).Events, api.RunHistoryEventKindGap)
		if len(found) == 0 {
			return false
		}
		gap = found[0]
		return true
	}, wait, poll)
	if !found {
		for _, r := range p.runs("service_ok") {
			t.Logf("run %s status %v start %s end %s known %t", r.DagRunId, r.Status, r.StartedAt, r.FinishedAt, slices.Contains(known, r.DagRunId))
		}
		t.Logf("events: %+v", p.events("service_ok", nil).Events)
		t.Logf("restarted scheduler output:\n%s", restarted.Output())
		t.FailNow()
	}

	require.NotNil(t, gap.GapSeconds)
	assert.GreaterOrEqual(t, *gap.GapSeconds, int64(61))
	assert.Less(t, *gap.GapSeconds, int64(300))

	// The gap event names the first run that started after the restart.
	var fresh []api.DAGRunSummary
	for _, r := range p.runs("service_ok") {
		if !slices.Contains(known, r.DagRunId) {
			fresh = append(fresh, r)
		}
	}
	require.NotEmpty(t, fresh)
	first := slices.MinFunc(fresh, func(a, b api.DAGRunSummary) int {
		return time.Time.Compare(parseTime(t, a.StartedAt), parseTime(t, b.StartedAt))
	})
	assert.Equal(t, first.DagRunId, gap.RunId)

	assert.Len(t, eventsOfKind(p.events("service_ok", nil).Events, api.RunHistoryEventKindGap), 1, "one stop gives one gap event")
}

func parseTime(t *testing.T, s string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return at
}
