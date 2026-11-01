// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/persis"
	fileeventstore "github.com/dagucloud/dagu/v2/internal/persis/file/eventstore"
	filemonitor "github.com/dagucloud/dagu/v2/internal/persis/file/monitor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

// testRecordedAt is when the first event of a test is recorded.
var testRecordedAt = time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)

// Reports carry the top-level runs that finished and the runs in progress,
// without updates, the runs of sub-DAGs, errors, or logs.
func TestReportRuns(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	console := newFakeConsole(t)
	clock := startEventReporter(t, console, dir, testClockStart)

	queued := etlRun(ir.Queued)
	queued.TriggerType = ir.TriggerTypeWebhook
	dir.emit(eventstore.TypeDAGRunQueued, queued)
	running := etlRun(ir.Running)
	running.TriggerType = ir.TriggerTypeWebhook
	running.QueuedAt = "not a time"
	running.Nodes = []*ir.Node{{Step: ir.Step{Name: "extract"}, Status: ir.NodeFailed}}
	dir.emit(eventstore.TypeDAGRunRunning, running)
	dir.emit(eventstore.TypeDAGRunUpdated, etlRun(ir.Running))
	failed := etlRun(ir.Failed)
	failed.TriggerType = ir.TriggerTypeWebhook
	failed.Error = "load failed"
	failed.Log = "/var/log/dagu/etl.log"
	failed.Params = "token=secret"
	failed.Nodes = []*ir.Node{
		{Step: ir.Step{Name: "extract"}, Status: ir.NodeSucceeded},
		{Step: ir.Step{Name: "load"}, Status: ir.NodeFailed, Error: "db.internal: connection refused"},
		{Step: ir.Step{Name: "notify"}, Status: ir.NodeFailed},
	}
	failed.OnFailure = &ir.Node{Step: ir.Step{Name: "on_failure"}, Status: ir.NodeFailed}
	dir.emit(eventstore.TypeDAGRunFailed, failed)
	child := etlRun(ir.Running)
	child.Name, child.DAGRunID, child.AttemptID = "load", "child-1", "child-attempt-1"
	child.Root, child.Parent = ir.NewDAGRunRef("etl", "run-1"), ir.NewDAGRunRef("etl", "run-1")
	dir.emit(eventstore.TypeDAGRunRunning, child)
	child.Status, child.FinishedAt = ir.Failed, "2026-10-10T09:04:00Z"
	dir.emit(eventstore.TypeDAGRunFailed, child)
	dir.put(&eventstore.Event{
		ID: "dag_without_snapshot", SchemaVersion: eventstore.SchemaVersion, OccurredAt: testRecordedAt,
		Kind: eventstore.KindDAGRun, Type: eventstore.TypeDAGRunFailed, SourceService: eventstore.SourceServiceScheduler,
		DAGName: "etl", DAGRunID: "run-2", AttemptID: "attempt-2", Status: "failed",
	})
	dir.put(eventstore.NewLLMUsageEvent(eventstore.Source{Service: eventstore.SourceServiceServer},
		"session-1", "user-1", "model-1", "message-1", testRecordedAt, &llm.Usage{TotalTokens: 10}, nil))
	dir.emit(eventstore.TypeDAGRunQueued, &ir.DAGRunStatus{
		Name: "etl", DAGRunID: "run-2", AttemptID: "attempt-2", Status: ir.Queued, QueuedAt: "2026-10-10T09:08:00Z",
	})
	dir.emit(eventstore.TypeDAGRunRunning, &ir.DAGRunStatus{
		Name: "report", DAGRunID: "run-3", AttemptID: "attempt-3", Status: ir.Running,
		TriggerType: ir.TriggerTypeScheduler, StartedAt: "2026-10-10T09:10:00Z",
	})

	report, _ := clock.nextReport(console)
	finished := decodeFinished(t, report)
	assert.JSONEq(t, `[
		{
			"workflow": "etl", "dag_run_id": "run-1", "attempt_id": "attempt-1", "status": "failed",
			"trigger_type": "webhook", "queued_at": "2026-10-10T08:59:00Z", "started_at": "2026-10-10T09:00:00Z",
			"finished_at": "2026-10-10T09:05:00Z", "failed_steps": ["load", "notify", "on_failure"]
		}
	]`, string(finished.Finished))
	assert.NotEmpty(t, finished.Cursor)
	assert.LessOrEqual(t, len(finished.Cursor), 4096)
	assert.Nil(t, finished.Gap)
	wantActive := `{
		"active": [
			{
				"workflow": "report", "dag_run_id": "run-3", "attempt_id": "attempt-3", "status": "running",
				"trigger_type": "scheduler", "queued_at": null, "started_at": "2026-10-10T09:10:00Z"
			},
			{
				"workflow": "etl", "dag_run_id": "run-2", "attempt_id": "attempt-2", "status": "queued",
				"trigger_type": "", "queued_at": "2026-10-10T09:08:00Z", "started_at": null
			}
		],
		"active_total": 2
	}`
	assert.JSONEq(t, wantActive, decodeActive(t, report).json(t))

	// The acknowledged cursor is past the events that reports leave out, and
	// every report lists the runs in progress.
	next := nextReport(t, clock, console)
	assert.Equal(t, finishedReport{}, decodeFinished(t, next))
	assert.JSONEq(t, wantActive, decodeActive(t, next).json(t))
}

// At the errors level, a failed run also carries why it failed: the errors
// of its failed steps without terminal escapes, each cut to its end, where
// the error says what went wrong; or the run's own error when no step says.
// A run that partly succeeded carries none.
func TestReportRunsWithErrors(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	console := newFakeConsole(t)
	_, clock := newEventReporterAt(t, console.credentials, dir, testClockStart, config.ReportErrors)

	failed := etlRun(ir.Failed)
	failed.Error = "the run failed"
	failed.Nodes = []*ir.Node{
		{Step: ir.Step{Name: "load"}, Status: ir.NodeFailed, Error: "exit status 1\nrecent stderr (tail):\n\x1b[31mconnection refused\x1b[0m"},
		{Step: ir.Step{Name: "copy"}, Status: ir.NodeFailed, Error: strings.Repeat("é", 2000) + "\nError: disk full"},
		{Step: ir.Step{Name: "notify"}, Status: ir.NodeFailed},
	}
	dir.emit(eventstore.TypeDAGRunFailed, failed)
	timedOut := etlRun(ir.Failed)
	timedOut.DAGRunID, timedOut.AttemptID, timedOut.Error = "run-2", "attempt-2", "the run timed out"
	dir.emit(eventstore.TypeDAGRunFailed, timedOut)
	partly := etlRun(ir.PartiallySucceeded)
	partly.DAGRunID, partly.AttemptID = "run-3", "attempt-3"
	partly.Nodes = []*ir.Node{{Step: ir.Step{Name: "optional"}, Status: ir.NodeFailed, Error: "skipped data"}}
	dir.emit(eventstore.TypeDAGRunPartiallySucceeded, partly)

	report, _ := clock.nextReport(console)
	var runs []finishedRun
	require.NoError(t, json.Unmarshal(decodeFinished(t, report).Finished, &runs))
	require.Len(t, runs, 3)
	assert.Empty(t, runs[2].Errors, "a run that partly succeeded did not fail")
	require.Len(t, runs[0].Errors, 2, "a step without an error says nothing, and the run's own error waits")
	assert.Equal(t, stepError{Step: "load", Message: "exit status 1\nrecent stderr (tail):\nconnection refused"}, runs[0].Errors[0])
	assert.Equal(t, "copy", runs[0].Errors[1].Step)
	assert.LessOrEqual(t, len(runs[0].Errors[1].Message), maxErrorBytes)
	assert.True(t, strings.HasPrefix(runs[0].Errors[1].Message, "…é"), "a message is cut between characters")
	assert.True(t, strings.HasSuffix(runs[0].Errors[1].Message, "Error: disk full"))
	assert.Equal(t, []stepError{{Message: "the run timed out"}}, runs[1].Errors)
}

// A read that finds only events that reports leave out still moves the
// cursor, so the console acknowledges a report with no finished runs.
func TestReportRunsOnlySkipped(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	console := newFakeConsole(t)
	clock := startEventReporter(t, console, dir, testClockStart)

	dir.emit(eventstore.TypeDAGRunUpdated, etlRun(ir.Running))

	finished := decodeFinished(t, nextReport(t, clock, console))
	assert.JSONEq(t, `[]`, string(finished.Finished))
	assert.NotEmpty(t, finished.Cursor)

	assert.Equal(t, finishedReport{}, decodeFinished(t, nextReport(t, clock, console)))
}

// A reporter without saved state reports the runs that finish from then on,
// and lists the runs in progress from the run history.
func TestReportRunsNoHistory(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	dir.emit(eventstore.TypeDAGRunSucceeded, etlRun(ir.Succeeded))
	dir.emit(eventstore.TypeDAGRunQueued, run("etl", "run-2", ir.Queued))
	dir.statuses.set(run("etl", "run-2", ir.Running), run("etl", "run-9", ir.Succeeded))
	console := newFakeConsole(t)
	clock := startEventReporter(t, console, dir, testClockStart)

	dir.emit(eventstore.TypeDAGRunSucceeded, run("etl", "run-3", ir.Succeeded))
	report := nextReport(t, clock, console)

	assert.Equal(t, []string{"run-3"}, decodeFinished(t, report).ids(t))
	assert.Equal(t, []string{"run-2"}, decodeActive(t, report).ids(t))
}

func TestReportRunsAck(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	console := newFakeConsole(t, consoleResponse{status: http.StatusOK, body: `{"ack": "another cursor"}`})
	clock := startEventReporter(t, console, dir, testClockStart)
	dir.emit(eventstore.TypeDAGRunSucceeded, etlRun(ir.Succeeded))

	first := decodeFinished(t, nextReport(t, clock, console))
	second := decodeFinished(t, nextReport(t, clock, console))
	third := decodeFinished(t, nextReport(t, clock, console))

	assert.Equal(t, []string{"run-1"}, first.ids(t))
	assert.Equal(t, first, second, "an unacknowledged report is sent again")
	assert.Equal(t, finishedReport{}, third, "an acknowledged report is not sent again")
}

// A reporter resumes from the cursor and the runs in progress that the
// console last acknowledged for its server. For another server, it starts
// over and lists the runs in progress from the run history.
func TestReportRunsResume(t *testing.T) {
	t.Parallel()

	unavailable := consoleResponse{status: http.StatusServiceUnavailable}
	tests := []struct {
		name         string
		responses    []consoleResponse
		serverID     string
		wantFinished []string
		wantActive   []string
	}{
		{
			name:         "acknowledged",
			serverID:     "srv-1",
			wantFinished: []string{"run-2"},
			wantActive:   []string{"run-3", "run-4"},
		},
		{
			name:         "unacknowledged",
			responses:    []consoleResponse{unavailable},
			serverID:     "srv-1",
			wantFinished: []string{"run-1", "run-2"},
			wantActive:   []string{"run-3", "run-4"},
		},
		{
			name:       "another server",
			responses:  []consoleResponse{unavailable},
			serverID:   "srv-2",
			wantActive: []string{"run-8"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := newEventDir(t)
			first := newFakeConsole(t, tc.responses...)
			reporter, clock := newEventReporter(t, first.credentials, dir, testClockStart)
			dir.emit(eventstore.TypeDAGRunSucceeded, etlRun(ir.Succeeded))
			dir.emit(eventstore.TypeDAGRunRunning, run("etl", "run-3", ir.Running))
			nextReport(t, clock, first)
			reporter.Stop()

			dir.emit(eventstore.TypeDAGRunSucceeded, run("etl", "run-2", ir.Succeeded))
			dir.emit(eventstore.TypeDAGRunQueued, run("etl", "run-4", ir.Queued))
			dir.statuses.set(run("etl", "run-8", ir.Running))
			second := newFakeConsole(t)
			credentials := func() (license.CloudCredentials, bool) {
				creds, _ := second.credentials()
				creds.ServerID = tc.serverID
				return creds, true
			}
			_, clock = newEventReporter(t, credentials, dir, testClockStart)
			report := nextReport(t, clock, second)

			assert.Equal(t, tc.wantFinished, decodeFinished(t, report).ids(t))
			assert.Equal(t, tc.wantActive, decodeActive(t, report).ids(t))
		})
	}
}

// Events that the event store removed while the reporter was away are
// reported as a gap that starts at the last acknowledged event, and the runs
// in progress are listed anew from the run history.
func TestReportRunsGap(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	console := newFakeConsole(t)
	reporter, clock := newEventReporter(t, console.credentials, dir, testClockStart)
	dir.emit(eventstore.TypeDAGRunSucceeded, etlRun(ir.Succeeded))
	dir.emit(eventstore.TypeDAGRunRunning, run("etl", "run-5", ir.Running))
	nextReport(t, clock, console)
	reporter.Stop()

	dir.emit(eventstore.TypeDAGRunFailed, run("etl", "run-2", ir.Failed))
	dir.statuses.set(run("etl", "run-6", ir.Running))
	_, clock = newEventReporter(t, console.credentials, dir, testClockStart.Add(72*time.Hour))

	gap := nextReport(t, clock, console)
	dir.emit(eventstore.TypeDAGRunSucceeded, run("etl", "run-3", ir.Succeeded))
	after := nextReport(t, clock, console)

	assert.Equal(t, &reportGap{Since: "2026-10-10T09:05:00Z"}, decodeFinished(t, gap).Gap)
	assert.Nil(t, decodeFinished(t, gap).ids(t))
	assert.NotEmpty(t, decodeFinished(t, gap).Cursor, "a gap carries a cursor to acknowledge")
	assert.Equal(t, []string{"run-6"}, decodeActive(t, gap).ids(t))
	assert.Equal(t, []string{"run-3"}, decodeFinished(t, after).ids(t))
	assert.Nil(t, decodeFinished(t, after).Gap, "a gap is reported once")
}

// A gap is reported again until Dagu Console acknowledges the report that
// carried it.
func TestReportRunsGapUntilAcknowledged(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	first := newFakeConsole(t)
	reporter, clock := newEventReporter(t, first.credentials, dir, testClockStart)
	dir.emit(eventstore.TypeDAGRunSucceeded, etlRun(ir.Succeeded))
	nextReport(t, clock, first)
	reporter.Stop()

	console := newFakeConsole(t, consoleResponse{status: http.StatusOK, body: `{}`})
	_, clock = newEventReporter(t, console.credentials, dir, testClockStart.Add(72*time.Hour))

	unacknowledged := decodeFinished(t, nextReport(t, clock, console))
	again := decodeFinished(t, nextReport(t, clock, console))
	after := decodeFinished(t, nextReport(t, clock, console))

	require.NotNil(t, unacknowledged.Gap)
	assert.Equal(t, unacknowledged.Gap, again.Gap, "an answer without the cursor confirms nothing")
	assert.Nil(t, after.Gap)
}

// An idle reporter is not mistaken for one that missed events.
func TestReportRunsIdleIsNoGap(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	console := newFakeConsole(t)
	reporter, clock := newEventReporter(t, console.credentials, dir, testClockStart)
	for range 3 {
		clock.shift(23 * time.Hour)
		assert.Nil(t, decodeFinished(t, nextReport(t, clock, console)).Gap)
	}
	reporter.Stop()

	_, clock = newEventReporter(t, console.credentials, dir, clock.now())
	assert.Equal(t, finishedReport{}, decodeFinished(t, nextReport(t, clock, console)))
}

// Before the first accepted report, only a run that failed, was aborted, or
// was rejected is reported early.
func TestReportRunsEarly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		eventType eventstore.EventType
		status    ir.Status
		want      time.Duration
	}{
		{eventType: eventstore.TypeDAGRunFailed, status: ir.Failed, want: 5 * time.Second},
		{eventType: eventstore.TypeDAGRunAborted, status: ir.Aborted, want: 5 * time.Second},
		{eventType: eventstore.TypeDAGRunRejected, status: ir.Rejected, want: 5 * time.Second},
		{eventType: eventstore.TypeDAGRunWaiting, status: ir.Waiting, want: maxStartDelay / 2},
		{eventType: eventstore.TypeDAGRunQueued, status: ir.Queued, want: maxStartDelay / 2},
		{eventType: eventstore.TypeDAGRunRunning, status: ir.Running, want: maxStartDelay / 2},
		{eventType: eventstore.TypeDAGRunSucceeded, status: ir.Succeeded, want: maxStartDelay / 2},
		{eventType: eventstore.TypeDAGRunPartiallySucceeded, status: ir.PartiallySucceeded, want: maxStartDelay / 2},
	}
	for _, tc := range tests {
		t.Run(string(tc.eventType), func(t *testing.T) {
			t.Parallel()

			dir := newEventDir(t)
			console := newFakeConsole(t)
			clock := startEventReporter(t, console, dir, testClockStart)
			dir.emit(tc.eventType, etlRun(tc.status))

			report, passed := clock.nextReport(console)

			assert.Equal(t, tc.want, passed)
			if tc.status.IsActive() {
				assert.Equal(t, []string{"run-1"}, decodeActive(t, report).ids(t))
			} else {
				assert.Equal(t, []string{"run-1"}, decodeFinished(t, report).ids(t))
			}
		})
	}
}

// Once a report was accepted, a change to the runs in progress is reported
// early; a run that finishes without having been in progress is not.
func TestReportActiveEarly(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	console := newFakeConsole(t)
	clock := startEventReporter(t, console, dir, testClockStart)
	nextReport(t, clock, console)

	dir.emit(eventstore.TypeDAGRunQueued, etlRun(ir.Queued))
	queued, queuedPassed := clock.nextReport(console)
	dir.emit(eventstore.TypeDAGRunRunning, etlRun(ir.Running))
	_, runningPassed := clock.nextReport(console)
	dir.emit(eventstore.TypeDAGRunSucceeded, run("etl", "run-2", ir.Succeeded))
	_, unrelatedPassed := clock.nextReport(console)
	dir.emit(eventstore.TypeDAGRunSucceeded, etlRun(ir.Succeeded))
	succeeded, succeededPassed := clock.nextReport(console)

	assert.Equal(t, eventCheckInterval, queuedPassed)
	assert.Equal(t, []string{"run-1"}, decodeActive(t, queued).ids(t))
	assert.Equal(t, eventCheckInterval, runningPassed)
	assert.Equal(t, defaultInterval, unrelatedPassed)
	assert.Equal(t, eventCheckInterval, succeededPassed)
	assert.Equal(t, []string{}, decodeActive(t, succeeded).ids(t))
	assert.Equal(t, []string{"run-1"}, decodeFinished(t, succeeded).ids(t))
}

// Early reports neither repeat for news the console accepted without
// acknowledging, nor cut short a wait after a failed report.
func TestReportRunsEarlyHoldsBack(t *testing.T) {
	t.Parallel()

	t.Run("unacknowledged failure", func(t *testing.T) {
		t.Parallel()

		dir := newEventDir(t)
		console := newFakeConsole(t, consoleResponse{status: http.StatusOK, body: `{}`})
		clock := startEventReporter(t, console, dir, testClockStart)
		dir.emit(eventstore.TypeDAGRunFailed, etlRun(ir.Failed))

		first, firstPassed := clock.nextReport(console)
		second, secondPassed := clock.nextReport(console)

		assert.Equal(t, 5*time.Second, firstPassed)
		assert.Equal(t, time.Minute, secondPassed)
		assert.Equal(t, []string{"run-1"}, decodeFinished(t, first).ids(t))
		assert.Equal(t, decodeFinished(t, first), decodeFinished(t, second))
	})

	t.Run("unacknowledged change in progress", func(t *testing.T) {
		t.Parallel()

		dir := newEventDir(t)
		console := newFakeConsole(t, consoleResponse{status: http.StatusOK, body: `{}`}, consoleResponse{status: http.StatusOK, body: `{}`})
		clock := startEventReporter(t, console, dir, testClockStart)
		nextReport(t, clock, console)
		dir.emit(eventstore.TypeDAGRunQueued, etlRun(ir.Queued))

		first, firstPassed := clock.nextReport(console)
		second, secondPassed := clock.nextReport(console)

		assert.Equal(t, 5*time.Second, firstPassed)
		assert.Equal(t, time.Minute, secondPassed)
		assert.Equal(t, []string{"run-1"}, decodeActive(t, first).ids(t))
		assert.Equal(t, decodeActive(t, first), decodeActive(t, second))
	})

	t.Run("console unavailable", func(t *testing.T) {
		t.Parallel()

		dir := newEventDir(t)
		console := newFakeConsole(t, consoleResponse{status: http.StatusServiceUnavailable})
		clock := startEventReporter(t, console, dir, testClockStart)
		nextReport(t, clock, console)
		dir.emit(eventstore.TypeDAGRunFailed, etlRun(ir.Failed))

		report, passed := clock.nextReport(console)

		assert.Equal(t, time.Minute, passed)
		assert.Equal(t, []string{"run-1"}, decodeFinished(t, report).ids(t))
	})
}

// A backlog larger than one report drains in reports 5 seconds apart, oldest
// first.
func TestReportRunsBacklog(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	console := newFakeConsole(t)
	clock := startEventReporter(t, console, dir, testClockStart)
	ids := dir.emitSucceeded(2*maxFinishedRuns + 1)
	dir.collect()

	var batches [][]string
	var waits []time.Duration
	for range 4 {
		report, passed := clock.nextReport(console)
		batches = append(batches, decodeFinished(t, report).ids(t))
		waits = append(waits, passed)
	}

	assert.Equal(t, [][]string{ids[:maxFinishedRuns], ids[maxFinishedRuns : 2*maxFinishedRuns], ids[2*maxFinishedRuns:], nil}, batches)
	assert.Equal(t, []time.Duration{maxStartDelay / 2, 5 * time.Second, 5 * time.Second, time.Minute}, waits)
}

// The runs in progress of a backlog follow its reports: each lists what the
// runs it carries leave in progress, and none applies an event again.
func TestReportRunsBacklogInProgress(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	console := newFakeConsole(t)
	clock := startEventReporter(t, console, dir, testClockStart)
	dir.emit(eventstore.TypeDAGRunRunning, run("etl", "short", ir.Running))
	dir.emit(eventstore.TypeDAGRunSucceeded, run("etl", "short", ir.Succeeded))
	dir.emit(eventstore.TypeDAGRunRunning, run("etl", "long", ir.Running))
	ids := dir.emitSucceeded(maxFinishedRuns)
	dir.emit(eventstore.TypeDAGRunSucceeded, run("etl", "long", ir.Succeeded))
	dir.collect()

	first := nextReport(t, clock, console)
	second := nextReport(t, clock, console)

	assert.Equal(t, append([]string{"short"}, ids[:maxFinishedRuns-1]...), decodeFinished(t, first).ids(t))
	assert.Equal(t, []string{"long"}, decodeActive(t, first).ids(t))
	assert.Equal(t, []string{ids[maxFinishedRuns-1], "long"}, decodeFinished(t, second).ids(t))
	assert.Equal(t, []string{}, decodeActive(t, second).ids(t))
}

// Failed runs with many long step names fill a report by size before count,
// and every report stays well under what Dagu Console accepts.
func TestReportRunsSizeCap(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	console := newFakeConsole(t)
	// The errors level carries the most: each run's errors too.
	_, clock := newEventReporterAt(t, console.credentials, dir, testClockStart, config.ReportErrors)
	longName := strings.Repeat("あ", 300) // 900 bytes
	longError := strings.Repeat("\u0001", 3000)
	var ids []string
	for i := range 45 {
		failed := run("etl", fmt.Sprintf("run-%d", i), ir.Failed)
		for j := range maxFailedSteps {
			node := &ir.Node{Step: ir.Step{Name: fmt.Sprintf("%02d-%s", j, longName)}, Status: ir.NodeFailed}
			// One error more than a run carries, so the cap is reached.
			if j <= maxRunErrors {
				node.Error = longError
			}
			failed.Nodes = append(failed.Nodes, node)
		}
		dir.emit(eventstore.TypeDAGRunFailed, failed)
		ids = append(ids, failed.DAGRunID)
	}
	dir.collect()

	var delivered []string
	for len(delivered) < len(ids) {
		report, _ := clock.nextReport(console)
		require.Less(t, len(report.body), 1<<20, "a report fits what Dagu Console accepts")
		finished := decodeFinished(t, report)
		require.LessOrEqual(t, len(finished.Finished), maxFinishedBytes)
		var batch []finishedRun
		require.NoError(t, json.Unmarshal(finished.Finished, &batch))
		require.NotEmpty(t, batch)
		require.Less(t, len(batch), len(ids), "a batch of large runs is capped by size")
		for _, run := range batch {
			require.Len(t, run.FailedSteps, maxFailedSteps)
			require.Len(t, run.Errors, maxRunErrors)
			for _, name := range run.FailedSteps {
				require.LessOrEqual(t, len(name), maxStepNameBytes)
				require.True(t, utf8.ValidString(name), "a step name is cut between characters")
			}
			delivered = append(delivered, run.DAGRunID)
		}
	}
	assert.ElementsMatch(t, ids, delivered, "each run is reported once")
}

// A run with a name or ID longer than Dagu Console keeps is left out like
// other unreported events, so it cannot hold the runs after it back.
func TestReportRunsOversizedIdentifiers(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	console := newFakeConsole(t)
	clock := startEventReporter(t, console, dir, testClockStart)
	dir.emit(eventstore.TypeDAGRunFailed, run("etl", strings.Repeat("r", maxIDBytes+1), ir.Failed))
	dir.emit(eventstore.TypeDAGRunFailed, run(strings.Repeat("n", maxNameBytes+1), "run-2", ir.Failed))
	dir.emit(eventstore.TypeDAGRunRunning, run("etl", strings.Repeat("r", maxIDBytes+1), ir.Running))
	dir.emit(eventstore.TypeDAGRunFailed, run("etl", "run-3", ir.Failed))
	dir.collect()

	first := nextReport(t, clock, console)
	assert.Equal(t, []string{"run-3"}, decodeFinished(t, first).ids(t))
	assert.Equal(t, []string{}, decodeActive(t, first).ids(t))
	assert.NotEmpty(t, decodeFinished(t, first).Cursor)
	assert.Nil(t, decodeFinished(t, nextReport(t, clock, console)).ids(t), "the position moved past all of them")
}

// Of the processes that share a data directory, the one holding the lease
// reports runs; the others report health until it lets go.
func TestReportRunsLease(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	holderConsole := newFakeConsole(t)
	holder, holderClock := newEventReporter(t, holderConsole.credentials, dir, testClockStart)
	standbyConsole := newFakeConsole(t)
	_, standbyClock := newEventReporter(t, standbyConsole.credentials, dir, testClockStart)

	dir.emit(eventstore.TypeDAGRunFailed, etlRun(ir.Failed))
	held, heldPassed := holderClock.nextReport(holderConsole)
	standby, standbyPassed := standbyClock.nextReport(standbyConsole)
	assert.Equal(t, 5*time.Second, heldPassed)
	assert.Equal(t, []string{"run-1"}, decodeFinished(t, held).ids(t))
	assert.Equal(t, maxStartDelay/2, standbyPassed)
	assert.Equal(t, finishedReport{}, decodeFinished(t, standby))
	assert.Nil(t, decodeActive(t, standby).ids(t))

	holder.Stop()
	standbyClock.elapse()
	standbyClock.wait()
	retry := etlRun(ir.Failed)
	retry.AttemptID = "attempt-2"
	dir.emit(eventstore.TypeDAGRunFailed, retry)

	takeover, passed := standbyClock.nextReport(standbyConsole)
	assert.Equal(t, 5*time.Second, passed)
	assert.Equal(t, []string{"run-1"}, decodeFinished(t, takeover).ids(t))
}

// An event store whose events cannot be read leaves runs out of reports, even
// the runs in progress known from before, and the failure is logged once.
func TestReportRunsUnreadable(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	dir.statuses.set(etlRun(ir.Running))
	console := newFakeConsole(t)
	var logs bytes.Buffer
	ctx := logger.WithLogger(t.Context(), logger.NewLogger(
		logger.WithFormat("text"),
		logger.WithWriter(&logs),
		logger.WithQuiet(),
	))
	events := dir.events()
	events.Reader = failingReader{}
	r := newReporter(console.credentials, nil, fixedLevel(config.ReportRuns))
	r.events = newEventFeed(events)
	clock := startReporterAt(ctx, t, r, noJitter, testClockStart)
	clock.wait()

	for range 3 {
		report := nextReport(t, clock, console)
		assert.Equal(t, finishedReport{}, decodeFinished(t, report))
		assert.Nil(t, decodeActive(t, report).ids(t))
	}
	assert.Equal(t, 1, strings.Count(logs.String(), warnRead))
}

func TestReportWithoutEvents(t *testing.T) {
	t.Parallel()

	console := newFakeConsole(t)
	var logs bytes.Buffer
	ctx := logger.WithLogger(t.Context(), logger.NewLogger(
		logger.WithFormat("text"),
		logger.WithWriter(&logs),
		logger.WithQuiet(),
	))
	clock := startReporter(ctx, t, newReporter(console.credentials, nil, fixedLevel(config.ReportRuns)), noJitter)
	clock.wait()

	for range 3 {
		report := nextReport(t, clock, console)
		assert.Equal(t, finishedReport{}, decodeFinished(t, report))
		assert.Nil(t, decodeActive(t, report).ids(t))
	}
	assert.Equal(t, 1, strings.Count(logs.String(), "the event store is disabled"))
}

// A run is listed in progress from the event that queues it until the event
// that ends its attempt. A later attempt replaces an earlier one, and the end
// of the earlier one leaves the later one listed.
func TestReportActiveFollowsEvents(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	console := newFakeConsole(t)
	clock := startEventReporter(t, console, dir, testClockStart)
	statuses := func() []string {
		t.Helper()
		var names []string
		for _, run := range decodeActive(t, nextReport(t, clock, console)).runs(t) {
			names = append(names, run.AttemptID+" "+run.Status)
		}
		return names
	}

	dir.emit(eventstore.TypeDAGRunQueued, etlRun(ir.Queued))
	assert.Equal(t, []string{"attempt-1 queued"}, statuses())
	dir.emit(eventstore.TypeDAGRunRunning, etlRun(ir.Running))
	assert.Equal(t, []string{"attempt-1 running"}, statuses())
	dir.emit(eventstore.TypeDAGRunWaiting, etlRun(ir.Waiting))
	assert.Equal(t, []string{"attempt-1 waiting"}, statuses())
	dir.emit(eventstore.TypeDAGRunFailed, etlRun(ir.Failed))
	// An empty list is sent as an empty list, not left out, so Dagu Console
	// clears the runs it showed instead of keeping them as unknown.
	ended := decodeActive(t, nextReport(t, clock, console))
	assert.JSONEq(t, `[]`, string(ended.Active))
	require.NotNil(t, ended.Total)
	assert.Zero(t, *ended.Total)

	retry := etlRun(ir.Running)
	retry.AttemptID = "attempt-2"
	dir.emit(eventstore.TypeDAGRunRunning, retry)
	dir.emit(eventstore.TypeDAGRunAborted, etlRun(ir.Aborted))
	assert.Equal(t, []string{"attempt-2 running"}, statuses())
}

// Reports list at most maxActiveRuns runs in progress, those that started or
// were queued most recently, and count them all.
func TestReportActiveCap(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	var listed []*ir.DAGRunStatus
	for i := range maxActiveRuns + 50 {
		status := run("etl", fmt.Sprintf("run-%03d", i), ir.Running)
		status.StartedAt = testRecordedAt.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)
		listed = append(listed, status)
	}
	dir.statuses.set(listed...)
	console := newFakeConsole(t)
	clock := startEventReporter(t, console, dir, testClockStart)

	active := decodeActive(t, nextReport(t, clock, console))

	ids := active.ids(t)
	require.Len(t, ids, maxActiveRuns)
	assert.Equal(t, "run-249", ids[0])
	assert.Equal(t, "run-050", ids[maxActiveRuns-1])
	require.NotNil(t, active.Total)
	assert.Equal(t, maxActiveRuns+50, *active.Total)
}

// The run history lists the runs in progress in the background: reports go
// out without them meanwhile, and the runs that end in the meantime are not
// listed once it is done.
func TestReportActiveRebuildInBackground(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	dir.statuses.set(run("etl", "run-1", ir.Running), run("etl", "run-2", ir.Queued), subRun())
	release := dir.statuses.hold()
	console := newFakeConsole(t)
	r := newReporter(console.credentials, nil, fixedLevel(config.ReportRuns))
	r.events = newEventFeed(dir.events())
	rebuilt := make(chan struct{}, 1)
	clock := startReporterSpawning(t.Context(), t, r, noJitter, testClockStart, func(fn func()) {
		go func() {
			fn()
			rebuilt <- struct{}{}
		}()
	})
	clock.wait()

	dir.emit(eventstore.TypeDAGRunSucceeded, run("etl", "run-1", ir.Succeeded))
	during := nextReport(t, clock, console)
	close(release)
	select {
	case <-rebuilt:
	case <-time.After(testTimeout):
		t.Fatal("the run history was not listed")
	}
	after := nextReport(t, clock, console)

	assert.Equal(t, []string{"run-1"}, decodeFinished(t, during).ids(t))
	assert.Equal(t, inProgress{}, decodeActive(t, during), "runs in progress are left out until they are known")
	assert.Equal(t, []string{"run-2"}, decodeActive(t, after).ids(t))
}

// A run history that cannot be read leaves the runs in progress out of
// reports, logs the failure once, and is read again later.
func TestReportActiveRebuildRetries(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	dir.statuses.set(run("etl", "run-1", ir.Running))
	dir.statuses.fail(2)
	console := newFakeConsole(t)
	var logs bytes.Buffer
	ctx := logger.WithLogger(t.Context(), logger.NewLogger(
		logger.WithFormat("text"),
		logger.WithWriter(&logs),
		logger.WithQuiet(),
	))
	r := newReporter(console.credentials, nil, fixedLevel(config.ReportRuns))
	r.events = newEventFeed(dir.events())
	clock := startReporterAt(ctx, t, r, noJitter, testClockStart)
	clock.wait()

	var reports []inProgress
	for range 3 {
		reports = append(reports, decodeActive(t, nextReport(t, clock, console)))
	}

	assert.Equal(t, inProgress{}, reports[0])
	assert.Equal(t, inProgress{}, reports[1])
	assert.Equal(t, []string{"run-1"}, reports[2].ids(t))
	assert.Equal(t, 1, strings.Count(logs.String(), warnActive))
}

// Dagu Console can pause storing finished runs. Reports carry them anyway,
// and monitoring shows until when, as the latest accepted report was told.
func TestReportRunsPaused(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	console := newFakeConsole(t, consoleResponse{status: http.StatusOK, body: `{"runs_paused_until": "2026-10-11T00:00:00Z"}`})
	m := fixedLevel(config.ReportRuns)
	r := newReporter(console.credentials, nil, m)
	r.events = newEventFeed(dir.events())
	clock := startReporterAt(t.Context(), t, r, noJitter, testClockStart)
	clock.wait()
	nextReport(t, clock, console)

	status, err := m.Status(t.Context())
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC), status.RunsPausedUntil.UTC())

	dir.emit(eventstore.TypeDAGRunSucceeded, etlRun(ir.Succeeded))
	assert.Equal(t, []string{"run-1"}, decodeFinished(t, nextReport(t, clock, console)).ids(t))
	status, err = m.Status(t.Context())
	require.NoError(t, err)
	assert.True(t, status.RunsPausedUntil.IsZero(), "a response without a pause ends it")
}

// eventDir is a data directory shared by reporters: an event store, the run
// history, and the reporters' state.
type eventDir struct {
	t         *testing.T
	store     *fileeventstore.Store
	storeDir  string
	stateFile string
	statuses  *fakeStatuses
	recorded  int
}

func newEventDir(t *testing.T) *eventDir {
	t.Helper()
	dir := t.TempDir()
	storeDir := filepath.Join(dir, "events")
	store, err := fileeventstore.New(storeDir)
	require.NoError(t, err)
	return &eventDir{
		t:         t,
		store:     store,
		storeDir:  storeDir,
		stateFile: filepath.Join(dir, "cloud", "report-state.json"),
		statuses:  &fakeStatuses{},
	}
}

// collect moves the stored events from the inbox to the event log, as the
// event collector does.
func (d *eventDir) collect() {
	d.t.Helper()
	collector, err := fileeventstore.NewCollector(d.storeDir, 0, fileeventstore.WithBatchSize(d.recorded))
	require.NoError(d.t, err)
	require.NoError(d.t, collector.DrainOnce(context.Background()))
}

func (d *eventDir) events() Events {
	return Events{
		Reader:        d.store,
		RetentionDays: 1,
		State:         filemonitor.NewStateStore(d.stateFile),
		Stale:         filemonitor.NewStateStore(d.stateFile + ".stale"),
		Lease:         filemonitor.NewLease(d.stateFile, nil),
		Statuses:      d.statuses,
	}
}

// emit stores the event of a status change.
func (d *eventDir) emit(eventType eventstore.EventType, status *ir.DAGRunStatus) string {
	d.t.Helper()
	return d.put(eventstore.NewDAGRunEvent(eventstore.Source{Service: eventstore.SourceServiceScheduler}, eventType, status, nil))
}

// put stores event as recorded after the events put before it.
func (d *eventDir) put(event *eventstore.Event) string {
	d.t.Helper()
	d.stamp(event)
	require.NoError(d.t, d.store.Emit(context.Background(), event))
	return event.ID
}

// emitSucceeded stores the events of n runs that succeeded and returns their
// run IDs in the order they are recorded. The writes run concurrently because
// each one is synced to disk.
func (d *eventDir) emitSucceeded(n int) []string {
	d.t.Helper()
	ids := make([]string, 0, n)
	var writes errgroup.Group
	writes.SetLimit(16)
	for i := range n {
		status := run("etl", fmt.Sprintf("run-%d", i), ir.Succeeded)
		event := eventstore.NewDAGRunEvent(eventstore.Source{Service: eventstore.SourceServiceScheduler}, eventstore.TypeDAGRunSucceeded, status, nil)
		d.stamp(event)
		ids = append(ids, status.DAGRunID)
		writes.Go(func() error { return d.store.Emit(context.Background(), event) })
	}
	require.NoError(d.t, writes.Wait())
	return ids
}

// stamp records event after the events stamped before it.
func (d *eventDir) stamp(event *eventstore.Event) {
	d.recorded++
	event.RecordedAt = testRecordedAt.Add(time.Duration(d.recorded) * time.Millisecond)
}

// etlRun is attempt-1 of run-1 of the etl DAG; see run.
func etlRun(status ir.Status) *ir.DAGRunStatus {
	return run("etl", "run-1", status)
}

// run is attempt-1 of a top-level run of name that was queued at 08:59,
// started at 09:00 unless it is queued, and, in a final status, finished at
// 09:05.
func run(name, dagRunID string, status ir.Status) *ir.DAGRunStatus {
	run := &ir.DAGRunStatus{
		Name:      name,
		DAGRunID:  dagRunID,
		AttemptID: "attempt-1",
		Status:    status,
		QueuedAt:  "2026-10-10T08:59:00Z",
	}
	if status != ir.Queued {
		run.StartedAt = "2026-10-10T09:00:00Z"
	}
	if !status.IsActive() {
		run.FinishedAt = "2026-10-10T09:05:00Z"
	}
	return run
}

// subRun is a sub-DAG run of run-1 of the etl DAG, in progress.
func subRun() *ir.DAGRunStatus {
	child := run("load", "child-1", ir.Running)
	child.Root, child.Parent = ir.NewDAGRunRef("etl", "run-1"), ir.NewDAGRunRef("etl", "run-1")
	return child
}

// startEventReporter starts a reporter of the events in dir, and returns once
// it has found where to report from.
func startEventReporter(t *testing.T, console *fakeConsole, dir *eventDir, start time.Time) *fakeClock {
	t.Helper()
	_, clock := newEventReporter(t, console.credentials, dir, start)
	return clock
}

func newEventReporter(
	t *testing.T,
	credentials func() (license.CloudCredentials, bool),
	dir *eventDir,
	start time.Time,
) (*Reporter, *fakeClock) {
	t.Helper()
	return newEventReporterAt(t, credentials, dir, start, config.ReportRuns)
}

// newEventReporterAt is newEventReporter reporting at level.
func newEventReporterAt(
	t *testing.T,
	credentials func() (license.CloudCredentials, bool),
	dir *eventDir,
	start time.Time,
	level config.ReportLevel,
) (*Reporter, *fakeClock) {
	t.Helper()
	r := newReporter(credentials, nil, fixedLevel(level))
	r.events = newEventFeed(dir.events())
	clock := startReporterAt(t.Context(), t, r, noJitter, start)
	clock.wait()
	return r, clock
}

// nextReport ends waits until console receives a report, and returns the
// report and the time that passed. It expects the reporter to be waiting.
func (c *fakeClock) nextReport(console *fakeConsole) (receivedReport, time.Duration) {
	c.t.Helper()
	start := c.now()
	for range 100 {
		c.elapse()
		// The reporter waits again only after the console has answered.
		c.wait()
		select {
		case report := <-console.reports:
			return report, c.now().Sub(start)
		default:
		}
	}
	c.t.Fatal("console received no report")
	return receivedReport{}, 0
}

// shift moves the clock without ending the reporter's wait.
func (c *fakeClock) shift(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

func nextReport(t *testing.T, clock *fakeClock, console *fakeConsole) receivedReport {
	t.Helper()
	report, _ := clock.nextReport(console)
	return report
}

// finishedReport is the part of a report about finished runs.
type finishedReport struct {
	Finished json.RawMessage `json:"finished"`
	Cursor   string          `json:"cursor"`
	Gap      *reportGap      `json:"gap"`
}

func decodeFinished(t *testing.T, report receivedReport) finishedReport {
	t.Helper()
	var finished finishedReport
	require.NoError(t, json.Unmarshal([]byte(report.body), &finished))
	return finished
}

// ids returns the run IDs of the finished runs in r, or nil when it has none.
func (r finishedReport) ids(t *testing.T) []string {
	t.Helper()
	if r.Finished == nil {
		return nil
	}
	var runs []finishedRun
	require.NoError(t, json.Unmarshal(r.Finished, &runs))
	var ids []string
	for _, run := range runs {
		ids = append(ids, run.DAGRunID)
	}
	return ids
}

// inProgress is the part of a report about runs in progress.
type inProgress struct {
	Active json.RawMessage `json:"active"`
	Total  *int            `json:"active_total"`
}

func decodeActive(t *testing.T, report receivedReport) inProgress {
	t.Helper()
	var active inProgress
	require.NoError(t, json.Unmarshal([]byte(report.body), &active))
	return active
}

// runs returns the runs in progress listed in p.
func (p inProgress) runs(t *testing.T) []activeRun {
	t.Helper()
	if p.Active == nil {
		return nil
	}
	var runs []activeRun
	require.NoError(t, json.Unmarshal(p.Active, &runs))
	return runs
}

// ids returns the run IDs of the runs in progress in p, or nil when p leaves
// them out.
func (p inProgress) ids(t *testing.T) []string {
	t.Helper()
	if p.Active == nil {
		return nil
	}
	ids := []string{}
	for _, run := range p.runs(t) {
		ids = append(ids, run.DAGRunID)
	}
	return ids
}

// json returns the runs in progress and their count as one JSON object.
func (p inProgress) json(t *testing.T) string {
	t.Helper()
	data, err := json.Marshal(p)
	require.NoError(t, err)
	return string(data)
}

// failingReader finds the newest event but cannot read any.
type failingReader struct{}

func (failingReader) DAGRunHeadCursor(context.Context) (eventstore.DAGRunCursor, error) {
	return eventstore.DAGRunCursor{}, nil
}

func (failingReader) ReadDAGRunEvents(context.Context, eventstore.DAGRunCursor) ([]*eventstore.Event, eventstore.DAGRunCursor, error) {
	return nil, eventstore.DAGRunCursor{}, errors.New("event store unavailable")
}

// fakeStatuses is a run history that lists the runs it holds whose status is
// asked for.
type fakeStatuses struct {
	mu       sync.Mutex
	statuses []*ir.DAGRunStatus
	// failures is how many listings fail before they succeed again.
	failures int
	// release holds listings until it is closed.
	release chan struct{}
}

func (s *fakeStatuses) set(statuses ...*ir.DAGRunStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statuses = statuses
}

func (s *fakeStatuses) fail(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = n
}

// hold makes listings wait until the returned channel is closed.
func (s *fakeStatuses) hold() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.release = make(chan struct{})
	return s.release
}

func (s *fakeStatuses) ListStatuses(ctx context.Context, options persis.DAGRunListOptions) ([]*ir.DAGRunStatus, error) {
	s.mu.Lock()
	release := s.release
	s.mu.Unlock()
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failures > 0 {
		s.failures--
		return nil, errors.New("run history unavailable")
	}
	var listed []*ir.DAGRunStatus
	for _, status := range s.statuses {
		if slices.Contains(options.Statuses, status.Status) {
			listed = append(listed, status)
		}
	}
	return listed, nil
}
