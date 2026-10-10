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
	assert.Equal(t, Status{Level: config.ReportRuns, Chosen: true, NoticeDismissed: true}, status)
}

// A level set in configuration is fixed; the stored choice is never read, so
// the store here is nil.
func TestMonitoringConfigured(t *testing.T) {
	t.Parallel()

	m := NewMonitoring(config.ReportHealth, nil)

	require.ErrorIs(t, m.SetLevel(t.Context(), config.ReportRuns), ErrLevelConfigured)
	require.NoError(t, m.DismissNotice(t.Context()))
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
// nothing. Health sends no events and leaves the events lease to others.
// Runs sends the events that occur once it is chosen, never those from
// before, even after a period below runs. Off again stops at once.
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

	require.NoError(t, m.SetLevel(t.Context(), config.ReportHealth))
	health, passed := clock.nextReport(console)
	assert.Equal(t, defaultInterval, passed)
	assert.Equal(t, config.Version, decodeReport(t, health).Health.Version)
	assert.Equal(t, eventReport{}, decodeEvents(t, health))
	assertLeaseFree(t, dir)

	require.NoError(t, m.SetLevel(t.Context(), config.ReportRuns))
	assert.Equal(t, eventReport{}, decodeEvents(t, nextReport(t, clock, console)),
		"events from before runs was chosen are left out")
	failed := etlRun(ir.Failed)
	failed.AttemptID = "attempt-2"
	failedID := dir.emit(eventstore.TypeDAGRunFailed, failed)
	runs, passed := clock.nextReport(console)
	assert.Equal(t, eventCheckInterval, passed)
	assert.Equal(t, []string{failedID}, decodeEvents(t, runs).ids(t))
	assertLeaseHeld(t, dir)

	require.NoError(t, m.SetLevel(t.Context(), config.ReportHealth))
	skipped := etlRun(ir.Failed)
	skipped.AttemptID = "attempt-3"
	dir.emit(eventstore.TypeDAGRunFailed, skipped)
	assert.Equal(t, eventReport{}, decodeEvents(t, nextReport(t, clock, console)))
	assertLeaseFree(t, dir)

	require.NoError(t, m.SetLevel(t.Context(), config.ReportRuns))
	assert.Equal(t, eventReport{}, decodeEvents(t, nextReport(t, clock, console)),
		"events that occurred at health are left out")

	require.NoError(t, m.SetLevel(t.Context(), config.ReportOff))
	dir.emit(eventstore.TypeDAGRunAborted, etlRun(ir.Aborted))
	for range 3 {
		clock.next()
	}
	assertNoReport(t, console)
	assertLeaseFree(t, dir)
}

// Choosing off stops a report in flight, and the report is not retried or
// counted as a failure.
func TestReportOffStopsReportInFlight(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	inFlight := make(chan struct{})
	cancelled := make(chan struct{})
	console := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The server notices a cancelled request only after reading its body.
		_, _ = io.Copy(io.Discard, r.Body)
		if requests.Add(1) == 1 {
			_, _ = io.WriteString(w, `{"next_report_seconds": 120}`)
			return
		}
		close(inFlight)
		<-r.Context().Done()
		close(cancelled)
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
	clock.next()
	assert.Equal(t, int32(2), requests.Load())
	assert.NotContains(t, logs.String(), "Failed to report")
}

// A level that cannot be read is taken as off.
func TestReportUnreadableLevel(t *testing.T) {
	t.Parallel()

	console := newFakeConsole(t)
	path := filepath.Join(t.TempDir(), "report-settings.json")
	require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))
	var logs bytes.Buffer
	ctx := logger.WithLogger(t.Context(), logger.NewLogger(
		logger.WithFormat("text"),
		logger.WithWriter(&logs),
		logger.WithQuiet(),
	))
	m := NewMonitoring("", filemonitor.NewStateStore(path))
	clock := startReporter(ctx, t, newReporter(console.credentials, nil, m), noJitter)
	clock.wait()

	for range 3 {
		clock.next()
	}
	assertNoReport(t, console)
	assert.Equal(t, 1, strings.Count(logs.String(), "Failed to read what to report to Dagu Console"))
}

// chosenLevel is monitoring whose level an administrator chooses, starting
// with none.
func chosenLevel(t *testing.T) *Monitoring {
	t.Helper()
	return NewMonitoring("", filemonitor.NewStateStore(filepath.Join(t.TempDir(), "report-settings.json")))
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
