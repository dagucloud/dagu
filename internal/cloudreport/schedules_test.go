// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	filemonitor "github.com/dagucloud/dagu/v2/internal/persis/file/monitor"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
)

type fakeSchedulerState struct {
	state *schedulerstate.State
	err   error
}

func (s fakeSchedulerState) Load(context.Context) (*schedulerstate.State, error) {
	return schedulerstate.Clone(s.state), s.err
}

func (fakeSchedulerState) Save(context.Context, *schedulerstate.State) error { return nil }

type fakePause struct{ paused bool }

func (p fakePause) IsPaused(context.Context) (bool, error) { return p.paused, nil }

func (p fakePause) Get(context.Context) (schedulerstate.Pause, error) {
	return schedulerstate.Pause{Paused: p.paused}, nil
}

func (fakePause) Set(context.Context, bool, string, string) error { return nil }

func (fakePause) SetHold(context.Context, bool, string) (bool, error) { return false, nil }

// scheduled is a DAG's watermark with its next run and, unless outcome is
// empty, its latest slot.
func scheduled(next time.Time, slot time.Time, outcome schedulerstate.SlotOutcome) schedulerstate.DAGWatermark {
	w := schedulerstate.DAGWatermark{NextRun: &next}
	if outcome != "" {
		w.LastSlot = &schedulerstate.ScheduleSlot{At: slot, Outcome: outcome}
	}
	return w
}

// schedulesReport is the part of a report about the scheduler.
type schedulesReport struct {
	SentAt string `json:"sent_at"`
	Health struct {
		Scheduler *schedulerHealth `json:"scheduler"`
	} `json:"health"`
	Schedules      *[]schedule `json:"schedules"`
	SchedulesTotal *int        `json:"schedules_total"`
}

func decodeSchedules(t *testing.T, report receivedReport) schedulesReport {
	t.Helper()
	var out schedulesReport
	require.NoError(t, json.Unmarshal([]byte(report.body), &out))
	return out
}

// Slots that did not settle come first, then the latest slots, then names;
// a DAG without a next run is not scheduled.
func TestSchedulesListUnsettledSlotsFirst(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	next := now.Add(time.Hour)
	at := func(h, m int) time.Time { return time.Date(2026, 10, 21, h, m, 0, 0, time.UTC) }
	state := &schedulerstate.State{LastTick: now, DAGs: map[string]schedulerstate.DAGWatermark{
		"done":      scheduled(next, at(11, 0), schedulerstate.SlotDispatched),
		"failed":    scheduled(next, at(10, 0), schedulerstate.SlotFailed),
		"planned":   scheduled(next, at(11, 0), schedulerstate.SlotPlanned),
		"new":       scheduled(next, at(11, 30), schedulerstate.SlotDispatched),
		"never":     scheduled(next, time.Time{}, ""),
		"suspended": {LastSlot: &schedulerstate.ScheduleSlot{At: at(9, 0), Outcome: schedulerstate.SlotMissed}},
	}}

	got, total := schedulesOf(state, now)

	var names []string
	for _, s := range got {
		names = append(names, s.Workflow)
	}
	assert.Equal(t, []string{"planned", "failed", "new", "done", "never"}, names)
	assert.Equal(t, 5, total)
	assert.Equal(t, schedule{Workflow: "failed", NextRun: "2026-10-21T13:00:00Z", LastSlot: "2026-10-21T10:00:00Z",
		LastOutcome: "failed"}, got[1])
	assert.Equal(t, schedule{Workflow: "never", NextRun: "2026-10-21T13:00:00Z"}, got[4])
}

// A next run that passed over a minute ago with no tick since is a slot no
// scheduler started.
func TestSchedulesTakeAnUntickedNextRunAsMissed(t *testing.T) {
	t.Parallel()

	due := time.Date(2026, 10, 21, 11, 0, 0, 0, time.UTC)
	of := func(lastTick, now time.Time, w schedulerstate.DAGWatermark) schedule {
		got, _ := schedulesOf(&schedulerstate.State{LastTick: lastTick, DAGs: map[string]schedulerstate.DAGWatermark{"etl": w}}, now)
		require.Len(t, got, 1)
		return got[0]
	}
	waiting := scheduled(due, due.Add(-time.Hour), schedulerstate.SlotDispatched)

	assert.Equal(t, "missed", of(due.Add(-time.Minute), due.Add(2*time.Minute), waiting).LastOutcome)
	assert.Equal(t, "2026-10-21T11:00:00Z", of(due.Add(-time.Minute), due.Add(2*time.Minute), waiting).LastSlot)
	assert.Equal(t, "dispatched", of(due.Add(-time.Minute), due.Add(30*time.Second), waiting).LastOutcome, "within a minute")
	assert.Equal(t, "dispatched", of(due, due.Add(2*time.Minute), waiting).LastOutcome, "a tick took it")
	assert.Equal(t, "running", of(due.Add(-time.Minute), due.Add(2*time.Minute),
		scheduled(due, due, schedulerstate.SlotRunning)).LastOutcome, "its slot is recorded")
}

// A report lists at most maxSchedules schedules within maxSchedulesBytes,
// and counts them all; even then the whole report stays under the size
// Dagu Console takes.
func TestSchedulesStayWithinTheirLimits(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	state := func(n int, name func(int) string) *schedulerstate.State {
		s := &schedulerstate.State{LastTick: now, DAGs: map[string]schedulerstate.DAGWatermark{}}
		for i := range n {
			s.DAGs[name(i)] = scheduled(now.Add(time.Hour), now, schedulerstate.SlotFailed)
		}
		return s
	}

	got, total := schedulesOf(state(maxSchedules+100, func(i int) string { return fmt.Sprintf("dag-%04d", i) }), now)
	assert.Len(t, got, maxSchedules)
	assert.Equal(t, maxSchedules+100, total)

	long := func(i int) string { return fmt.Sprintf("%04d", i) + strings.Repeat("x", 508) }
	got, total = schedulesOf(state(maxSchedules, long), now)
	encoded, err := json.Marshal(got)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(encoded), maxSchedulesBytes+1)
	assert.Equal(t, maxSchedules, total)
	body, err := json.Marshal(reportRequest{Schedules: got, SchedulesTotal: &total})
	require.NoError(t, err)
	assert.Less(t, len(body), 1<<20)
}

// Every report says when it was built and how the scheduler is. Only a
// report at runs, from the process that holds the event lease, lists the
// schedules; an unreadable state leaves both out.
func TestReportCarriesTheSchedules(t *testing.T) {
	t.Parallel()

	tick := testClockStart.Add(-time.Minute)
	state := &schedulerstate.State{LastTick: tick, DAGs: map[string]schedulerstate.DAGWatermark{
		"etl": scheduled(testClockStart.Add(time.Hour), tick, schedulerstate.SlotDispatched),
	}}
	report := func(t *testing.T, level config.ReportLevel, events bool, scheduler Scheduler) schedulesReport {
		t.Helper()
		dir := newEventDir(t)
		console := newFakeConsole(t)
		r := newReporter(console.credentials, nil, fixedLevel(level))
		if events {
			r.events = newEventFeed(dir.events())
		}
		r.scheduler = scheduler
		clock := startReporterAt(t.Context(), t, r, noJitter, testClockStart)
		clock.wait()
		return decodeSchedules(t, nextReport(t, clock, console))
	}
	both := Scheduler{State: fakeSchedulerState{state: state}, Pause: fakePause{paused: true}}

	t.Run("runs", func(t *testing.T) {
		t.Parallel()
		got := report(t, config.ReportRuns, true, both)
		assert.NotEmpty(t, got.SentAt)
		assert.Equal(t, &schedulerHealth{LastTick: "2026-10-10T09:29:00Z", Paused: true}, got.Health.Scheduler)
		require.NotNil(t, got.Schedules)
		assert.Equal(t, []schedule{{Workflow: "etl", NextRun: "2026-10-10T10:30:00Z", LastSlot: "2026-10-10T09:29:00Z",
			LastOutcome: "dispatched"}}, *got.Schedules)
		assert.Equal(t, 1, *got.SchedulesTotal)
	})
	t.Run("none scheduled", func(t *testing.T) {
		t.Parallel()
		got := report(t, config.ReportRuns, true, Scheduler{State: fakeSchedulerState{state: &schedulerstate.State{}}})
		require.NotNil(t, got.Schedules, "an empty list says none is scheduled")
		assert.Empty(t, *got.Schedules)
		assert.Equal(t, &schedulerHealth{}, got.Health.Scheduler)
	})
	t.Run("health", func(t *testing.T) {
		t.Parallel()
		got := report(t, config.ReportHealth, true, both)
		assert.NotNil(t, got.Health.Scheduler)
		assert.Nil(t, got.Schedules)
		assert.Nil(t, got.SchedulesTotal)
	})
	t.Run("without events", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, report(t, config.ReportRuns, false, both).Schedules)
	})
	t.Run("unreadable state", func(t *testing.T) {
		t.Parallel()
		got := report(t, config.ReportRuns, true, Scheduler{State: fakeSchedulerState{err: errors.New("unreadable")},
			Pause: fakePause{}})
		assert.Nil(t, got.Health.Scheduler)
		assert.Nil(t, got.Schedules)
	})
	t.Run("no scheduler", func(t *testing.T) {
		t.Parallel()
		got := report(t, config.ReportRuns, true, Scheduler{})
		assert.Nil(t, got.Health.Scheduler)
		assert.Nil(t, got.Schedules)
	})
}

// A process that does not hold the event lease lists no schedules, which
// the process that holds it lists.
func TestReportLeavesSchedulesToTheLeaseHolder(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	holder := filemonitor.NewLease(dir.stateFile, nil)
	require.NoError(t, holder.TryLock())
	t.Cleanup(func() { _ = holder.Unlock() })
	console := newFakeConsole(t)
	r := newReporter(console.credentials, nil, fixedLevel(config.ReportRuns))
	r.events = newEventFeed(dir.events())
	r.scheduler = Scheduler{State: fakeSchedulerState{state: &schedulerstate.State{}}}
	clock := startReporterAt(t.Context(), t, r, noJitter, testClockStart)
	clock.wait()

	assert.Nil(t, decodeSchedules(t, nextReport(t, clock, console)).Schedules)
}
