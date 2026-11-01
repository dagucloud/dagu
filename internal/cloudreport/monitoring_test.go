// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/license"
	filemonitor "github.com/dagucloud/dagu/v2/internal/persis/file/monitor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMonitoringChoice(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cloud", "report-settings.json")
	m := NewMonitoring("", filemonitor.NewStateStore(path))
	status, err := m.Status(t.Context())
	require.NoError(t, err)
	assert.Equal(t, Status{Level: config.ReportOff}, status)

	require.NoError(t, m.DismissNotice(t.Context()))
	require.NoError(t, m.SetLevel(t.Context(), config.ReportRuns))

	// A restarted server keeps the choice.
	status, err = NewMonitoring("", filemonitor.NewStateStore(path)).Status(t.Context())
	require.NoError(t, err)
	assert.Equal(t, Status{Level: config.ReportRuns, Chosen: true, NoticeDismissed: true, ErrorsOffered: true}, status)
}

// A level chosen before servers could send error messages was chosen without
// them, so they are offered until the notice is dismissed.
func TestMonitoringOffersErrorsToAnEarlierChoice(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "report-settings.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"level":"runs"}`), 0o600))
	m := NewMonitoring("", filemonitor.NewStateStore(path))
	status, err := m.Status(t.Context())
	require.NoError(t, err)
	assert.False(t, status.ErrorsOffered)

	require.NoError(t, m.DismissErrorsNotice(t.Context()))
	status, err = m.Status(t.Context())
	require.NoError(t, err)
	assert.Equal(t, Status{Level: config.ReportRuns, Chosen: true, ErrorsOffered: true}, status)
}

// A level set in configuration is fixed; the stored choice is never read, so
// the store here is nil.
func TestMonitoringConfigured(t *testing.T) {
	t.Parallel()

	m := NewMonitoring(config.ReportHealth, nil)

	require.ErrorIs(t, m.SetLevel(t.Context(), config.ReportRuns), ErrLevelConfigured)
	require.NoError(t, m.DismissNotice(t.Context()))
	require.NoError(t, m.DismissErrorsNotice(t.Context()))
	status, err := m.Status(t.Context())
	require.NoError(t, err)
	assert.Equal(t, Status{Level: config.ReportHealth, Configured: true}, status)
}

// A choice is announced before the next one is saved, so a late
// announcement cannot undo a later choice, such as an off cancelling a
// report that a later runs allowed.
func TestMonitoringAnnouncesInSaveOrder(t *testing.T) {
	t.Parallel()

	m := chosenLevel(t)
	announcing := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	var announced []config.ReportLevel
	m.watch(func(level config.ReportLevel) {
		if level == config.ReportOff {
			close(announcing)
			<-release
		}
		mu.Lock()
		defer mu.Unlock()
		announced = append(announced, level)
	})

	first := make(chan error, 1)
	go func() { first <- m.SetLevel(t.Context(), config.ReportOff) }()
	<-announcing
	second := make(chan error, 1)
	go func() { second <- m.SetLevel(t.Context(), config.ReportRuns) }()
	select {
	case err := <-second:
		t.Fatalf("runs was chosen while off was still being announced: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-first)
	require.NoError(t, <-second)

	assert.Equal(t, []config.ReportLevel{config.ReportOff, config.ReportRuns}, announced)
	status, err := m.Status(t.Context())
	require.NoError(t, err)
	assert.Equal(t, config.ReportRuns, status.Level)
}

func TestMonitoringUnknownLevel(t *testing.T) {
	t.Parallel()

	m := chosenLevel(t)

	require.ErrorIs(t, m.SetLevel(t.Context(), "everything"), ErrUnknownLevel)
	status, err := m.Status(t.Context())
	require.NoError(t, err)
	assert.False(t, status.Chosen)
}

// The last report is shown as sent, except for the heartbeat secret.
func TestLastReportRedacted(t *testing.T) {
	t.Parallel()

	console := newFakeConsole(t)
	m := fixedLevel(config.ReportHealth)
	clock := startReporter(t.Context(), t, newReporter(console.credentials, nil, m), noJitter)
	status, err := m.Status(t.Context())
	require.NoError(t, err)
	assert.True(t, status.LastReportAt.IsZero())
	assert.Nil(t, status.LastReport)

	clock.wait()
	clock.next()
	sent := console.next(t)

	status, err = m.Status(t.Context())
	require.NoError(t, err)
	require.Contains(t, sent.body, `"heartbeat_secret":"hb-secret"`)
	assert.Equal(t,
		strings.Replace(sent.body, `"heartbeat_secret":"hb-secret"`, `"heartbeat_secret":"[redacted]"`, 1),
		string(status.LastReport))
	assert.False(t, status.LastReportAt.IsZero())
}

// A report that Dagu Console does not accept is not shown as the last one.
func TestLastReportOnlyWhenAccepted(t *testing.T) {
	t.Parallel()

	console := newFakeConsole(t, consoleResponse{status: http.StatusInternalServerError})
	m := fixedLevel(config.ReportHealth)
	clock := startReporter(t.Context(), t, newReporter(console.credentials, nil, m), noJitter)
	clock.wait()

	clock.next()
	console.next(t)
	status, err := m.Status(t.Context())
	require.NoError(t, err)
	assert.Nil(t, status.LastReport)

	clock.next()
	accepted := console.next(t)
	status, err = m.Status(t.Context())
	require.NoError(t, err)
	assert.Equal(t,
		strings.Replace(accepted.body, `"heartbeat_secret":"hb-secret"`, `"heartbeat_secret":"[redacted]"`, 1),
		string(status.LastReport))
}

// The reporter follows the level an administrator chooses. Off sends
// nothing. Health sends no runs and leaves the events lease to others. Runs
// sends the runs in progress and those that finish once it is chosen, never
// those that finished before, even after a period below runs. Off again
// stops at once, and sends only one report saying so.
func TestReportFollowsLevel(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	console := newFakeConsole(t)
	m := chosenLevel(t)
	r := newReporter(console.credentials, nil, m)
	r.events = newEventFeed(dir.events())
	clock := startReporterAt(t.Context(), t, r, noJitter, testClockStart)

	// Off: the event store is not checked, and the due report is not sent.
	assert.Equal(t, maxStartDelay/2, clock.wait())
	dir.emit(eventstore.TypeDAGRunFailed, etlRun(ir.Failed))
	assert.Equal(t, defaultInterval, clock.next())
	assertNoReport(t, console)
	assertLeaseFree(t, dir)

	// Turning reporting on ends the wait, so the health report goes out at
	// once and the next one is an interval later.
	require.NoError(t, m.SetLevel(t.Context(), config.ReportHealth))
	assert.Equal(t, defaultInterval, clock.wait())
	health := console.next(t)
	assert.Equal(t, config.Version, decodeReport(t, health).Health.Version)
	assertNoRuns(t, health)
	assertLeaseFree(t, dir)
	dir.emit(eventstore.TypeDAGRunRunning, run("etl", "run-2", ir.Running))
	dir.statuses.set(run("etl", "run-2", ir.Running))

	require.NoError(t, m.SetLevel(t.Context(), config.ReportRuns))
	chosen := nextReport(t, clock, console)
	assert.Equal(t, finishedReport{}, decodeFinished(t, chosen), "runs that finished before runs was chosen are left out")
	assert.Equal(t, []string{"run-2"}, decodeActive(t, chosen).ids(t))
	failed := etlRun(ir.Failed)
	failed.AttemptID = "attempt-2"
	dir.emit(eventstore.TypeDAGRunFailed, failed)
	runs, passed := clock.nextReport(console)
	assert.Equal(t, eventCheckInterval, passed)
	assert.Equal(t, []string{"run-1"}, decodeFinished(t, runs).ids(t))
	assertLeaseHeld(t, dir)

	require.NoError(t, m.SetLevel(t.Context(), config.ReportHealth))
	skipped := etlRun(ir.Failed)
	skipped.AttemptID = "attempt-3"
	dir.emit(eventstore.TypeDAGRunFailed, skipped)
	assertNoRuns(t, nextReport(t, clock, console))
	assertLeaseFree(t, dir)

	require.NoError(t, m.SetLevel(t.Context(), config.ReportRuns))
	assert.Equal(t, finishedReport{}, decodeFinished(t, nextReport(t, clock, console)),
		"runs that finished at health are left out")

	require.NoError(t, m.SetLevel(t.Context(), config.ReportOff))
	dir.emit(eventstore.TypeDAGRunAborted, etlRun(ir.Aborted))
	assert.JSONEq(t, offReport, nextReport(t, clock, console).body)
	for range 3 {
		clock.next()
	}
	assertNoReport(t, console)
	assertLeaseFree(t, dir)
}

// A reporter that starts below runs, fixed by configuration or chosen before,
// leaves out the runs that finished below runs once runs is chosen again: in
// the same process, or at a restart at runs while the event store still holds
// them. No gap is reported for them either, even after the event store has
// removed them.
func TestReportStartBelowRuns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// configured fixes the level below runs, and runs is chosen again by
		// restarting at runs; otherwise chosen is the level, and runs is
		// chosen in the same process.
		configured config.ReportLevel
		chosen     config.ReportLevel
		// later is how long after the first start the restart at runs is.
		later time.Duration
	}{
		{name: "configured health", configured: config.ReportHealth},
		{name: "configured off", configured: config.ReportOff},
		{name: "configured health, events removed", configured: config.ReportHealth, later: 72 * time.Hour},
		{name: "chosen health", chosen: config.ReportHealth},
		{name: "chosen off", chosen: config.ReportOff},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := newEventDir(t)
			console := newFakeConsole(t)
			first, clock := newEventReporter(t, console.credentials, dir, testClockStart)
			dir.emit(eventstore.TypeDAGRunSucceeded, etlRun(ir.Succeeded))
			assert.Equal(t, []string{"run-1"}, decodeFinished(t, nextReport(t, clock, console)).ids(t))
			first.Stop()

			m := NewMonitoring(tc.configured, newSettingsStore(t))
			if tc.chosen != "" {
				require.NoError(t, m.SetLevel(t.Context(), tc.chosen))
			}
			r := newReporter(console.credentials, nil, m)
			r.events = newEventFeed(dir.events())
			clock = startReporterAt(t.Context(), t, r, noJitter, testClockStart)
			if tc.configured == config.ReportOff {
				assertStops(t, r)
			} else {
				clock.wait()
			}
			dir.emit(eventstore.TypeDAGRunFailed, run("etl", "run-2", ir.Failed))

			if tc.configured != "" {
				r.Stop()
				_, clock = newEventReporter(t, console.credentials, dir, testClockStart.Add(tc.later))
			} else {
				require.NoError(t, m.SetLevel(t.Context(), config.ReportRuns))
			}
			report := nextReport(t, clock, console)
			assert.Equal(t, config.ReportRuns, decodeReport(t, report).Level)
			assert.Nil(t, decodeFinished(t, report).ids(t))
			assert.Nil(t, decodeFinished(t, report).Gap)
		})
	}
}

// A reporter below runs while another process holds the events lease, as a
// process that stopped at runs does for a while, leaves the saved position to
// the next process that takes the lease, which discards it: at a restart at
// runs, or in the same process once runs is chosen.
func TestReportBelowRunsWithoutLease(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// configured fixes the level below runs, and runs is chosen again by
		// restarting at runs; otherwise runs is chosen in the same process.
		configured config.ReportLevel
	}{
		{name: "restart at runs", configured: config.ReportOff},
		{name: "runs chosen"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := newEventDir(t)
			console := newFakeConsole(t)
			first, _ := newEventReporter(t, console.credentials, dir, testClockStart)
			first.Stop()
			other := filemonitor.NewLease(dir.stateFile, nil)
			require.NoError(t, other.TryLock())

			m := NewMonitoring(tc.configured, newSettingsStore(t))
			r := newReporter(console.credentials, nil, m)
			r.events = newEventFeed(dir.events())
			clock := startReporterAt(t.Context(), t, r, noJitter, testClockStart)
			if tc.configured != "" {
				assertStops(t, r)
			} else {
				clock.wait()
			}
			dir.emit(eventstore.TypeDAGRunFailed, etlRun(ir.Failed))
			require.NoError(t, other.Unlock())

			if tc.configured != "" {
				_, clock = newEventReporter(t, console.credentials, dir, testClockStart)
			} else {
				require.NoError(t, m.SetLevel(t.Context(), config.ReportRuns))
			}
			assert.Nil(t, decodeFinished(t, nextReport(t, clock, console)).ids(t))
		})
	}
}

// Off tells Dagu Console once that the server no longer reports. The report
// is retried while Dagu Console is unavailable, but not once it answers, and
// nothing follows it until a level that reports is chosen again.
func TestReportOff(t *testing.T) {
	t.Parallel()

	unavailable := consoleResponse{status: http.StatusServiceUnavailable}
	tests := []struct {
		name      string
		responses []consoleResponse
		// waits are those after each report saying that reporting stopped.
		waits []time.Duration
	}{
		{
			name:      "retried until accepted",
			responses: []consoleResponse{unavailable, unavailable},
			waits:     []time.Duration{time.Minute, 2 * time.Minute, time.Minute},
		},
		{
			name:      "rejected",
			responses: []consoleResponse{{status: http.StatusBadRequest, body: `{"message":"invalid report"}`}},
			waits:     []time.Duration{time.Minute},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			accepted := consoleResponse{status: http.StatusOK, body: `{}`}
			console := newFakeConsole(t, append([]consoleResponse{accepted}, tc.responses...)...)
			m := chosenLevel(t)
			require.NoError(t, m.SetLevel(t.Context(), config.ReportHealth))
			clock := startReporter(t.Context(), t, newReporter(console.credentials, nil, m), noJitter)
			clock.wait()
			clock.next()
			assert.Equal(t, config.ReportHealth, decodeReport(t, console.next(t)).Level)

			require.NoError(t, m.SetLevel(t.Context(), config.ReportOff))
			var waits []time.Duration
			for range tc.waits {
				waits = append(waits, clock.next())
				assert.JSONEq(t, offReport, console.next(t).body)
			}
			assert.Equal(t, tc.waits, waits)
			for range 3 {
				clock.next()
			}
			assertNoReport(t, console)

			require.NoError(t, m.SetLevel(t.Context(), config.ReportHealth))
			clock.next()
			resumed := decodeReport(t, console.next(t))
			assert.Equal(t, config.ReportHealth, resumed.Level)
			assert.Equal(t, config.Version, resumed.Health.Version)
		})
	}
}

// A server that starts at off, chosen or fixed by configuration, tells Dagu
// Console once that it no longer reports when its last report was at a level
// that reports: it stopped before telling, or its configuration changed while
// it was down. A reporter whose configuration fixes off then stops.
func TestReportOffAfterRestart(t *testing.T) {
	t.Parallel()

	t.Run("configured", func(t *testing.T) {
		t.Parallel()

		console := newFakeConsole(t)
		store := reportedHealth(t, console)
		r := newReporter(console.credentials, nil, NewMonitoring(config.ReportOff, store))
		clock := startReporter(t.Context(), t, r, noJitter)
		assert.Equal(t, maxStartDelay/2, clock.wait())
		clock.elapse()
		assert.JSONEq(t, offReport, console.next(t).body)
		assertStops(t, r)

		// Started again, it has nothing left to send.
		r = newReporter(console.credentials, nil, NewMonitoring(config.ReportOff, store))
		startReporter(t.Context(), t, r, noJitter)
		assertStops(t, r)
		assertNoReport(t, console)
	})

	t.Run("chosen", func(t *testing.T) {
		t.Parallel()

		console := newFakeConsole(t)
		m := NewMonitoring("", reportedHealth(t, console))
		require.NoError(t, m.SetLevel(t.Context(), config.ReportOff))
		clock := startReporter(t.Context(), t, newReporter(console.credentials, nil, m), noJitter)
		clock.wait()
		clock.next()
		assert.JSONEq(t, offReport, console.next(t).body)
		for range 3 {
			clock.next()
		}
		assertNoReport(t, console)
	})

	t.Run("never reported", func(t *testing.T) {
		t.Parallel()

		console := newFakeConsole(t)
		r := newReporter(console.credentials, nil, NewMonitoring(config.ReportOff, newSettingsStore(t)))
		startReporter(t.Context(), t, r, noJitter)
		assertStops(t, r)
		assertNoReport(t, console)
	})
}

// Choosing off stops a report in flight, and the report is not retried or
// counted as a failure. The next report says that reporting stopped.
func TestReportOffStopsReportInFlight(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	inFlight := make(chan struct{})
	cancelled := make(chan struct{})
	later := make(chan string, 2)
	console := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The server notices a cancelled request only after reading its body.
		body, _ := io.ReadAll(r.Body)
		switch requests.Add(1) {
		case 1:
			_, _ = io.WriteString(w, `{"next_report_seconds": 120}`)
		case 2:
			close(inFlight)
			<-r.Context().Done()
			close(cancelled)
		default:
			later <- string(body)
		}
	}))
	t.Cleanup(console.Close)
	credentials := func() (license.CloudCredentials, bool) {
		return license.CloudCredentials{CloudURL: console.URL, ServerID: "srv-1"}, true
	}
	var logs bytes.Buffer
	ctx := logger.WithLogger(t.Context(), logger.NewLogger(
		logger.WithFormat("text"),
		logger.WithWriter(&logs),
		logger.WithQuiet(),
	))
	m := chosenLevel(t)
	require.NoError(t, m.SetLevel(ctx, config.ReportHealth))
	clock := startReporter(ctx, t, newReporter(credentials, nil, m), noJitter)
	clock.wait()
	require.Equal(t, 2*defaultInterval, clock.next())

	clock.elapse()
	<-inFlight
	require.NoError(t, m.SetLevel(ctx, config.ReportOff))

	// A failed report would back off for initialBackoff instead.
	assert.Equal(t, 2*defaultInterval, clock.wait())
	select {
	case <-cancelled:
	case <-time.After(testTimeout):
		t.Fatal("the report in flight was not cancelled")
	}
	clock.next()
	select {
	case body := <-later:
		assert.JSONEq(t, `{"protocol":2,"license_id":"","server_id":"srv-1","heartbeat_secret":"","level":"off"}`, body)
	case <-time.After(testTimeout):
		t.Fatal("the console was not told that reporting stopped")
	}
	clock.next()
	assert.Equal(t, int32(3), requests.Load())
	assert.NotContains(t, logs.String(), "Failed to report")
}

// A level that cannot be read is taken as off, but not as a choice of off,
// so Dagu Console is not told that reporting stopped.
func TestReportUnreadableLevel(t *testing.T) {
	t.Parallel()

	console := newFakeConsole(t)
	path := filepath.Join(t.TempDir(), "report-settings.json")
	var logs bytes.Buffer
	ctx := logger.WithLogger(t.Context(), logger.NewLogger(
		logger.WithFormat("text"),
		logger.WithWriter(&logs),
		logger.WithQuiet(),
	))
	m := NewMonitoring("", filemonitor.NewStateStore(path))
	require.NoError(t, m.SetLevel(ctx, config.ReportHealth))
	clock := startReporter(ctx, t, newReporter(console.credentials, nil, m), noJitter)
	clock.wait()
	clock.next()
	console.next(t)

	require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))
	for range 3 {
		clock.next()
	}
	assertNoReport(t, console)
	assert.Equal(t, 1, strings.Count(logs.String(), "Failed to read what to report to Dagu Console"))
}

// offReport is the report that tells the fake console that the server no
// longer reports.
const offReport = `{"protocol":2,"license_id":"lic-1","server_id":"srv-1","heartbeat_secret":"hb-secret","level":"off"}`

// chosenLevel is monitoring whose level an administrator chooses, starting
// with none.
func chosenLevel(t *testing.T) *Monitoring {
	t.Helper()
	return NewMonitoring("", newSettingsStore(t))
}

// newSettingsStore returns an empty store for report settings.
func newSettingsStore(t *testing.T) StateStore {
	t.Helper()
	return filemonitor.NewStateStore(filepath.Join(t.TempDir(), "report-settings.json"))
}

// reportedHealth returns a store in which a reporter at health, fixed by
// configuration, recorded a report it sent to console and then stopped.
func reportedHealth(t *testing.T, console *fakeConsole) StateStore {
	t.Helper()
	store := newSettingsStore(t)
	r := newReporter(console.credentials, nil, NewMonitoring(config.ReportHealth, store))
	clock := startReporter(t.Context(), t, r, noJitter)
	clock.wait()
	clock.next()
	console.next(t)
	r.Stop()
	return store
}

// assertStops checks that r stops by itself.
func assertStops(t *testing.T, r *Reporter) {
	t.Helper()
	select {
	case <-r.done:
	case <-time.After(testTimeout):
		t.Fatal("the reporter kept running")
	}
}

// assertNoReport checks that console has received no report since the last
// one the test took. The reporter sends only while it is not waiting, so
// this holds once it waits again.
func assertNoReport(t *testing.T, console *fakeConsole) {
	t.Helper()
	select {
	case report := <-console.reports:
		t.Fatalf("console received a report: %s", report.body)
	default:
	}
}

// assertNoRuns checks that report says nothing about runs.
func assertNoRuns(t *testing.T, report receivedReport) {
	t.Helper()
	assert.Equal(t, finishedReport{}, decodeFinished(t, report))
	assert.Equal(t, inProgress{}, decodeActive(t, report))
}

func assertLeaseFree(t *testing.T, dir *eventDir) {
	t.Helper()
	lease := filemonitor.NewLease(dir.stateFile, nil)
	require.NoError(t, lease.TryLock(), "the reporter holds the events lease")
	require.NoError(t, lease.Unlock())
}

func assertLeaseHeld(t *testing.T, dir *eventDir) {
	t.Helper()
	require.ErrorIs(t, filemonitor.NewLease(dir.stateFile, nil).TryLock(), dirlock.ErrLockConflict)
}
