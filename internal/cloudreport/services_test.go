// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/samples"
)

// fakeServices is a server's service DAGs with their run histories.
type fakeServices struct {
	dags []ServiceDAG
	dirs map[string]string
}

func (s *fakeServices) List(context.Context) ([]ServiceDAG, error) { return s.dags, nil }

func (s *fakeServices) Names(context.Context) ([]string, error) {
	var names []string
	for _, d := range s.dags {
		names = append(names, d.Name)
	}
	return names, nil
}

func (s *fakeServices) HistoryDir(name string) (string, bool) {
	dir, ok := s.dirs[name]
	return dir, ok
}

// serviceHistory is the run history of the loop service DAG.
type serviceHistory struct {
	t   *testing.T
	dir string
}

func newServiceHistory(t *testing.T) *serviceHistory {
	return &serviceHistory{t: t, dir: t.TempDir()}
}

// record records runs of the history, each started at its time, failed when
// fail says so.
func (h *serviceHistory) record(at time.Time, runs int, fail func(i int) bool) {
	h.t.Helper()
	clock := at
	rec, err := samples.OpenRecorder(context.Background(), h.dir, samples.Options{Now: func() time.Time { return clock }, RunHistory: true})
	require.NoError(h.t, err)
	for i := range runs {
		clock = at.Add(time.Duration(i) * 10 * time.Second)
		s := samples.Sample{Time: clock, Duration: 2 * time.Second, RunID: fmt.Sprintf("run-%d", i)}
		if fail(i) {
			s.ExitCode, s.Message = 1, "boom"
		}
		require.NoError(h.t, rec.Record(context.Background(), s))
	}
	require.NoError(h.t, rec.Close(context.Background()))
}

func always(bool) func(int) bool { return func(int) bool { return true } }
func never(int) bool             { return false }

// loopServices is the loop service DAG, failing, with its history in dir.
func loopServices(dir string) *fakeServices {
	latest := run("loop", "run-9", ir.Failed)
	latest.Error = "boom"
	return &fakeServices{
		dags: []ServiceDAG{{Name: "loop", RestartPolicy: "always", RestartWait: time.Second, State: "failing", Latest: latest}},
		dirs: map[string]string{"loop": dir},
	}
}

// servicesReport is the part of a report about service DAGs.
type servicesReport struct {
	ServiceDAGs *[]serviceDAGEntry `json:"service_dags"`
	Total       *int               `json:"service_dags_total"`
}

func decodeServices(t *testing.T, report receivedReport) servicesReport {
	t.Helper()
	var got servicesReport
	require.NoError(t, json.Unmarshal([]byte(report.body), &got))
	return got
}

// startServiceReporter starts a reporter of the events in dir at level that
// lists services.
func startServiceReporter(t *testing.T, console *fakeConsole, dir *eventDir, services ServiceDAGs, level config.ReportLevel,
	start time.Time,
) (*Reporter, *fakeClock) {
	t.Helper()
	events := dir.events()
	events.Services = services
	r := newReporter(console.credentials, nil, fixedLevel(level))
	r.events = newEventFeed(events)
	clock := startReporterAt(t.Context(), t, r, noJitter, start)
	clock.wait()
	return r, clock
}

// emitService stores the event of a status change of a service DAG's run.
func (d *eventDir) emitService(eventType eventstore.EventType, status *ir.DAGRunStatus) {
	d.t.Helper()
	d.put(eventstore.NewDAGRunEvent(eventstore.Source{Service: eventstore.SourceServiceScheduler}, eventType, status,
		map[string]any{eventstore.ServiceDAGDataKey: true}))
}

// A service DAG's runs never reach a report, finished or in progress, and
// make no report early; another DAG's runs still do.
func TestReportsCarryNoServiceRuns(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	console := newFakeConsole(t)
	_, clock := startServiceReporter(t, console, dir, &fakeServices{}, config.ReportRuns, testClockStart)
	dir.emitService(eventstore.TypeDAGRunRunning, run("loop", "run-1", ir.Running))
	dir.emitService(eventstore.TypeDAGRunFailed, run("loop", "run-1", ir.Failed))
	dir.emit(eventstore.TypeDAGRunSucceeded, etlRun(ir.Succeeded))

	report, passed := clock.nextReport(console)

	assert.Equal(t, maxStartDelay/2, passed, "a service run's failure is not urgent")
	assert.Equal(t, []string{"run-1"}, decodeFinished(t, report).ids(t))
	assert.Equal(t, "etl", decodeReport(t, report).Finished[0].Workflow)
	assert.Empty(t, decodeActive(t, report).ids(t))
}

// A report at runs lists each service DAG with its state, its open outage,
// its events, and its hours; their error text goes only at errors, and a
// report at health lists none.
func TestReportListsServiceDAGs(t *testing.T) {
	t.Parallel()

	history := newServiceHistory(t)
	history.record(testClockStart.Add(-20*time.Minute), 3, always(true))
	report := func(t *testing.T, level config.ReportLevel) servicesReport {
		t.Helper()
		dir := newEventDir(t)
		console := newFakeConsole(t)
		_, clock := startServiceReporter(t, console, dir, loopServices(history.dir), level, testClockStart)
		return decodeServices(t, nextReport(t, clock, console))
	}

	got := report(t, config.ReportRuns)
	require.NotNil(t, got.ServiceDAGs)
	require.Len(t, *got.ServiceDAGs, 1)
	assert.Equal(t, 1, *got.Total)
	loop := (*got.ServiceDAGs)[0]
	assert.Equal(t, "failing", loop.State)
	assert.Equal(t, &serviceOutage{Open: true, FailedCount: 3, Since: "2026-10-10T09:10:00Z", LastFailureAt: "2026-10-10T09:10:20Z"},
		loop.Outage)
	require.Len(t, loop.Events, 1)
	assert.Equal(t, serviceEvent{Type: "failed", At: "2026-10-10T09:10:00Z", DAGRunID: "run-0", FailedCount: 1}, loop.Events[0])
	require.NotEmpty(t, loop.Hours)
	last := loop.Hours[len(loop.Hours)-1]
	assert.Equal(t, "2026-10-10T09:00:00Z", last.Start)
	assert.Equal(t, uint64(3), last.Count)
	assert.Equal(t, int64(6000), last.SumMs)
	assert.Empty(t, loop.LatestRun.Error, "no error text below errors")

	errors := report(t, config.ReportErrors)
	assert.Equal(t, "boom", (*errors.ServiceDAGs)[0].LatestRun.Error)
	assert.Equal(t, "boom", (*errors.ServiceDAGs)[0].Events[0].Message)

	assert.Nil(t, report(t, config.ReportHealth).ServiceDAGs)
}

// A new outage of a service DAG whose run just ended sends a report early,
// once per serviceUrgentEvery; a service that keeps failing in the same
// outage, or recovers, waits for the next report.
func TestAServiceOutageReportsEarly(t *testing.T) {
	t.Parallel()

	history := newServiceHistory(t)
	history.record(testClockStart.Add(-20*time.Minute), 3, never)
	dir := newEventDir(t)
	console := newFakeConsole(t)
	_, clock := startServiceReporter(t, console, dir, loopServices(history.dir), config.ReportRuns, testClockStart)
	nextReport(t, clock, console)

	history.record(clock.now().Add(-time.Second), 1, always(true))
	dir.emitService(eventstore.TypeDAGRunFailed, run("loop", "run-10", ir.Failed))
	report, passed := clock.nextReport(console)
	assert.Equal(t, eventCheckInterval, passed)
	events := (*decodeServices(t, report).ServiceDAGs)[0].Events
	require.Len(t, events, 1)
	assert.Equal(t, "failed", events[0].Type)

	history.record(clock.now().Add(-time.Second), 1, always(true))
	dir.emitService(eventstore.TypeDAGRunFailed, run("loop", "run-11", ir.Failed))
	_, passed = clock.nextReport(console)
	assert.Greater(t, passed, eventCheckInterval, "the same outage goes at the normal pace")

	history.record(clock.now().Add(-time.Second), 1, never)
	dir.emitService(eventstore.TypeDAGRunSucceeded, run("loop", "run-12", ir.Succeeded))
	_, passed = clock.nextReport(console)
	assert.Greater(t, passed, eventCheckInterval, "a recovery goes at the normal pace")
	history.record(clock.now().Add(-time.Second), 1, always(true))
	dir.emitService(eventstore.TypeDAGRunFailed, run("loop", "run-13", ir.Failed))
	_, passed = clock.nextReport(console)
	assert.Greater(t, passed, eventCheckInterval, "a new outage within ten minutes of the last early report")
}

// What an accepted report sent of a service DAG holds after the server
// restarts: the next report sends no event again, and its hours start at
// the hour of the last one.
func TestServiceMarksSurviveARestart(t *testing.T) {
	t.Parallel()

	history := newServiceHistory(t)
	history.record(testClockStart.Add(-20*time.Minute), 3, always(true))
	dir := newEventDir(t)
	console := newFakeConsole(t)
	services := loopServices(history.dir)
	reporter, clock := startServiceReporter(t, console, dir, services, config.ReportRuns, testClockStart)
	first := (*decodeServices(t, nextReport(t, clock, console)).ServiceDAGs)[0]
	require.Len(t, first.Events, 1)
	require.NotEmpty(t, first.Hours)
	reporter.Stop()

	_, clock = startServiceReporter(t, console, dir, services, config.ReportRuns, clock.now())
	again := (*decodeServices(t, nextReport(t, clock, console)).ServiceDAGs)[0]
	assert.Empty(t, again.Events)
	require.Len(t, again.Hours, 1)
	assert.Equal(t, "2026-10-10T09:00:00Z", again.Hours[0].Start)
}

// Service DAGs past the size of a report lose their oldest hours first, then
// their oldest events, then the last DAGs.
func TestFitServiceDAGsDropsTheOldestHoursFirst(t *testing.T) {
	t.Parallel()

	hours := func(n int, start time.Time) []serviceHour {
		out := make([]serviceHour, n)
		for i := range out {
			out[i] = serviceHour{Start: formatTime(start.Add(time.Duration(i) * time.Hour)), Count: 1}
		}
		return out
	}
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var entries []serviceDAGEntry
	for i := range 200 {
		entries = append(entries, serviceDAGEntry{Workflow: fmt.Sprintf("dag-%03d", i), State: "running",
			Hours: hours(24, start.Add(time.Duration(i)*time.Minute)),
			Events: []serviceEvent{{Type: "failed", At: formatTime(start)}}})
	}

	fitted := fitServiceDAGs(entries)

	data, err := json.Marshal(fitted)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(data), maxServiceDAGBytes)
	assert.Len(t, fitted, 200, "hours go before any DAG")
	assert.Len(t, fitted[199].Events, 1, "hours go before events")
	assert.Less(t, len(fitted[0].Hours), len(fitted[199].Hours), "the oldest hours go first")
}
