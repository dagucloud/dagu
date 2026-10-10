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
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/llm"
	fileeventstore "github.com/dagucloud/dagu/v2/internal/persis/file/eventstore"
	filemonitor "github.com/dagucloud/dagu/v2/internal/persis/file/monitor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

// testRecordedAt is when the first event of a test is recorded.
var testRecordedAt = time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)

// Reports carry the status changes of root DAG runs, without updates, the
// runs of sub-DAGs, errors, or logs.
func TestReportEvents(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	console := newFakeConsole(t)
	clock := startEventReporter(t, console, dir, testClockStart)

	queuedID := dir.emit(eventstore.TypeDAGRunQueued, etlRun(ir.Queued))
	running := etlRun(ir.Running)
	running.QueuedAt = "not a time"
	running.Nodes = []*ir.Node{{Step: ir.Step{Name: "extract"}, Status: ir.NodeFailed}}
	runningID := dir.emit(eventstore.TypeDAGRunRunning, running)
	dir.emit(eventstore.TypeDAGRunUpdated, etlRun(ir.Running))
	failed := etlRun(ir.Failed)
	failed.Error = "load failed"
	failed.Log = "/var/log/dagu/etl.log"
	failed.Nodes = []*ir.Node{
		{Step: ir.Step{Name: "extract"}, Status: ir.NodeSucceeded},
		{Step: ir.Step{Name: "load"}, Status: ir.NodeFailed, Error: "db.internal: connection refused"},
		{Step: ir.Step{Name: "notify"}, Status: ir.NodeFailed},
	}
	failed.OnFailure = &ir.Node{Step: ir.Step{Name: "on_failure"}, Status: ir.NodeFailed}
	failedID := dir.emit(eventstore.TypeDAGRunFailed, failed)
	child := etlRun(ir.Failed)
	child.Name, child.DAGRunID, child.AttemptID = "load", "child-1", "child-attempt-1"
	child.Root, child.Parent = ir.NewDAGRunRef("etl", "run-1"), ir.NewDAGRunRef("etl", "run-1")
	dir.emit(eventstore.TypeDAGRunFailed, child)
	dir.put(&eventstore.Event{
		ID: "dag_without_snapshot", SchemaVersion: eventstore.SchemaVersion, OccurredAt: testRecordedAt,
		Kind: eventstore.KindDAGRun, Type: eventstore.TypeDAGRunFailed, SourceService: eventstore.SourceServiceScheduler,
		DAGName: "etl", DAGRunID: "run-2", AttemptID: "attempt-2", Status: "failed",
	})
	dir.put(eventstore.NewLLMUsageEvent(eventstore.Source{Service: eventstore.SourceServiceServer},
		"session-1", "user-1", "model-1", "message-1", testRecordedAt, &llm.Usage{TotalTokens: 10}, nil))

	report, _ := clock.nextReport(console)
	events := decodeEvents(t, report)
	assert.JSONEq(t, fmt.Sprintf(`[
		{
			"id": %q, "type": "dag.run.queued", "dag_name": "etl", "dag_run_id": "run-1",
			"attempt_id": "attempt-1", "status": "queued", "occurred_at": "2026-10-10T08:59:00Z",
			"queued_at": "2026-10-10T08:59:00Z"
		},
		{
			"id": %q, "type": "dag.run.running", "dag_name": "etl", "dag_run_id": "run-1",
			"attempt_id": "attempt-1", "status": "running", "occurred_at": "2026-10-10T09:00:00Z",
			"started_at": "2026-10-10T09:00:00Z"
		},
		{
			"id": %q, "type": "dag.run.failed", "dag_name": "etl", "dag_run_id": "run-1",
			"attempt_id": "attempt-1", "status": "failed", "occurred_at": "2026-10-10T09:05:00Z",
			"queued_at": "2026-10-10T08:59:00Z", "started_at": "2026-10-10T09:00:00Z",
			"finished_at": "2026-10-10T09:05:00Z", "failed_steps": ["load", "notify", "on_failure"]
		}
	]`, queuedID, runningID, failedID), string(events.Events))
	assert.NotEmpty(t, events.Cursor)
	assert.LessOrEqual(t, len(events.Cursor), 4096)
	assert.Nil(t, events.Gap)

	// The acknowledged cursor is past the events that reports leave out.
	assert.Equal(t, eventReport{}, decodeEvents(t, nextReport(t, clock, console)))
}

// A read that finds only events that reports leave out still moves the
// cursor, so the console acknowledges a report with no events.
func TestReportEventsOnlySkipped(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	console := newFakeConsole(t)
	clock := startEventReporter(t, console, dir, testClockStart)

	dir.emit(eventstore.TypeDAGRunUpdated, etlRun(ir.Running))

	events := decodeEvents(t, nextReport(t, clock, console))
	assert.JSONEq(t, `[]`, string(events.Events))
	assert.NotEmpty(t, events.Cursor)

	assert.Equal(t, eventReport{}, decodeEvents(t, nextReport(t, clock, console)))
}

func TestReportEventsNoHistory(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	dir.emit(eventstore.TypeDAGRunSucceeded, etlRun(ir.Succeeded))
	console := newFakeConsole(t)
	clock := startEventReporter(t, console, dir, testClockStart)

	newRun := etlRun(ir.Queued)
	newRun.DAGRunID = "run-2"
	newID := dir.emit(eventstore.TypeDAGRunQueued, newRun)

	assert.Equal(t, []string{newID}, decodeEvents(t, nextReport(t, clock, console)).ids(t))
}

func TestReportEventsAck(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	console := newFakeConsole(t, consoleResponse{status: http.StatusOK, body: `{"ack": "another cursor"}`})
	clock := startEventReporter(t, console, dir, testClockStart)
	id := dir.emit(eventstore.TypeDAGRunSucceeded, etlRun(ir.Succeeded))

	first := decodeEvents(t, nextReport(t, clock, console))
	second := decodeEvents(t, nextReport(t, clock, console))
	third := decodeEvents(t, nextReport(t, clock, console))

	assert.Equal(t, []string{id}, first.ids(t))
	assert.Equal(t, first, second, "an unacknowledged report is sent again")
	assert.Equal(t, eventReport{}, third, "an acknowledged report is not sent again")
}

// A reporter resumes from the cursor that the console last acknowledged for
// its server.
func TestReportEventsResume(t *testing.T) {
	t.Parallel()

	unavailable := consoleResponse{status: http.StatusServiceUnavailable}
	tests := []struct {
		name      string
		responses []consoleResponse
		serverID  string
		want      func(oldID, newID string) []string
	}{
		{
			name:     "acknowledged",
			serverID: "srv-1",
			want:     func(_, newID string) []string { return []string{newID} },
		},
		{
			name:      "unacknowledged",
			responses: []consoleResponse{unavailable},
			serverID:  "srv-1",
			want:      func(oldID, newID string) []string { return []string{oldID, newID} },
		},
		{
			name:      "another server",
			responses: []consoleResponse{unavailable},
			serverID:  "srv-2",
			want:      func(string, string) []string { return nil },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := newEventDir(t)
			first := newFakeConsole(t, tc.responses...)
			reporter, clock := newEventReporter(t, first.credentials, dir, testClockStart)
			oldID := dir.emit(eventstore.TypeDAGRunSucceeded, etlRun(ir.Succeeded))
			nextReport(t, clock, first)
			reporter.Stop()

			newRun := etlRun(ir.Queued)
			newRun.DAGRunID = "run-2"
			newID := dir.emit(eventstore.TypeDAGRunQueued, newRun)
			second := newFakeConsole(t)
			credentials := func() (license.CloudCredentials, bool) {
				creds, _ := second.credentials()
				creds.ServerID = tc.serverID
				return creds, true
			}
			_, clock = newEventReporter(t, credentials, dir, testClockStart)

			assert.Equal(t, tc.want(oldID, newID), decodeEvents(t, nextReport(t, clock, second)).ids(t))
		})
	}
}

// Events that the event store removed while the reporter was away are
// reported as a gap that starts at the last acknowledged event.
func TestReportEventsGap(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	console := newFakeConsole(t)
	reporter, clock := newEventReporter(t, console.credentials, dir, testClockStart)
	dir.emit(eventstore.TypeDAGRunSucceeded, etlRun(ir.Succeeded))
	nextReport(t, clock, console)
	reporter.Stop()

	lost := etlRun(ir.Failed)
	lost.DAGRunID = "run-2"
	dir.emit(eventstore.TypeDAGRunFailed, lost)
	_, clock = newEventReporter(t, console.credentials, dir, testClockStart.Add(72*time.Hour))

	gap := decodeEvents(t, nextReport(t, clock, console))
	newRun := etlRun(ir.Queued)
	newRun.DAGRunID = "run-3"
	newID := dir.emit(eventstore.TypeDAGRunQueued, newRun)
	after := decodeEvents(t, nextReport(t, clock, console))

	assert.Equal(t, &reportGap{Since: "2026-10-10T09:05:00Z"}, gap.Gap)
	assert.Nil(t, gap.ids(t))
	assert.NotEmpty(t, gap.Cursor, "a gap carries a cursor to acknowledge")
	assert.Equal(t, []string{newID}, after.ids(t))
	assert.Nil(t, after.Gap, "a gap is reported once")
}

// A gap is reported again until Dagu Console acknowledges the report that
// carried it.
func TestReportEventsGapUntilAcknowledged(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	first := newFakeConsole(t)
	reporter, clock := newEventReporter(t, first.credentials, dir, testClockStart)
	dir.emit(eventstore.TypeDAGRunSucceeded, etlRun(ir.Succeeded))
	nextReport(t, clock, first)
	reporter.Stop()

	console := newFakeConsole(t, consoleResponse{status: http.StatusOK, body: `{}`})
	_, clock = newEventReporter(t, console.credentials, dir, testClockStart.Add(72*time.Hour))

	unacknowledged := decodeEvents(t, nextReport(t, clock, console))
	again := decodeEvents(t, nextReport(t, clock, console))
	after := decodeEvents(t, nextReport(t, clock, console))

	require.NotNil(t, unacknowledged.Gap)
	assert.Equal(t, unacknowledged.Gap, again.Gap, "an answer without the cursor confirms nothing")
	assert.Nil(t, after.Gap)
}

// An idle reporter is not mistaken for one that missed events.
func TestReportEventsIdleIsNoGap(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	console := newFakeConsole(t)
	reporter, clock := newEventReporter(t, console.credentials, dir, testClockStart)
	for range 3 {
		clock.shift(23 * time.Hour)
		assert.Nil(t, decodeEvents(t, nextReport(t, clock, console)).Gap)
	}
	reporter.Stop()

	_, clock = newEventReporter(t, console.credentials, dir, clock.now())
	assert.Equal(t, eventReport{}, decodeEvents(t, nextReport(t, clock, console)))
}

func TestReportEventsEarly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		eventType eventstore.EventType
		status    ir.Status
		want      time.Duration
	}{
		{eventType: eventstore.TypeDAGRunFailed, status: ir.Failed, want: 5 * time.Second},
		{eventType: eventstore.TypeDAGRunAborted, status: ir.Aborted, want: 5 * time.Second},
		{eventType: eventstore.TypeDAGRunRejected, status: ir.Rejected, want: 5 * time.Second},
		{eventType: eventstore.TypeDAGRunWaiting, status: ir.Waiting, want: 5 * time.Second},
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
			id := dir.emit(tc.eventType, etlRun(tc.status))

			report, passed := clock.nextReport(console)

			assert.Equal(t, tc.want, passed)
			assert.Equal(t, []string{id}, decodeEvents(t, report).ids(t))
		})
	}
}

// Early reports neither repeat for an event the console accepted without
// acknowledging, nor cut short a wait after a failed report.
func TestReportEventsEarlyHoldsBack(t *testing.T) {
	t.Parallel()

	t.Run("unacknowledged", func(t *testing.T) {
		t.Parallel()

		dir := newEventDir(t)
		console := newFakeConsole(t, consoleResponse{status: http.StatusOK, body: `{}`})
		clock := startEventReporter(t, console, dir, testClockStart)
		id := dir.emit(eventstore.TypeDAGRunFailed, etlRun(ir.Failed))

		first, firstPassed := clock.nextReport(console)
		second, secondPassed := clock.nextReport(console)

		assert.Equal(t, 5*time.Second, firstPassed)
		assert.Equal(t, time.Minute, secondPassed)
		assert.Equal(t, []string{id}, decodeEvents(t, first).ids(t))
		assert.Equal(t, decodeEvents(t, first), decodeEvents(t, second))
	})

	t.Run("console unavailable", func(t *testing.T) {
		t.Parallel()

		dir := newEventDir(t)
		console := newFakeConsole(t, consoleResponse{status: http.StatusServiceUnavailable})
		clock := startEventReporter(t, console, dir, testClockStart)
		nextReport(t, clock, console)
		id := dir.emit(eventstore.TypeDAGRunFailed, etlRun(ir.Failed))

		report, passed := clock.nextReport(console)

		assert.Equal(t, time.Minute, passed)
		assert.Equal(t, []string{id}, decodeEvents(t, report).ids(t))
	})
}

// A backlog larger than one report drains in reports 5 seconds apart, oldest
// first.
func TestReportEventsBacklog(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	console := newFakeConsole(t)
	clock := startEventReporter(t, console, dir, testClockStart)
	ids := dir.emitQueued(2*maxReportEvents + 1)
	dir.collect()

	var batches [][]string
	var waits []time.Duration
	for range 4 {
		report, passed := clock.nextReport(console)
		batches = append(batches, decodeEvents(t, report).ids(t))
		waits = append(waits, passed)
	}

	assert.Equal(t, [][]string{ids[:maxReportEvents], ids[maxReportEvents : 2*maxReportEvents], ids[2*maxReportEvents:], nil}, batches)
	assert.Equal(t, []time.Duration{maxStartDelay / 2, 5 * time.Second, 5 * time.Second, time.Minute}, waits)
}

// Failed runs with many long step names fill a report by size before count,
// and every report stays well under what Dagu Console accepts.
func TestReportEventsSizeCap(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	console := newFakeConsole(t)
	clock := startEventReporter(t, console, dir, testClockStart)
	longName := strings.Repeat("あ", 300) // 900 bytes
	var ids []string
	for i := range 45 {
		run := etlRun(ir.Failed)
		run.DAGRunID = fmt.Sprintf("run-%d", i)
		for j := range maxFailedSteps {
			run.Nodes = append(run.Nodes, &ir.Node{
				Step:   ir.Step{Name: fmt.Sprintf("%02d-%s", j, longName)},
				Status: ir.NodeFailed,
			})
		}
		ids = append(ids, dir.emit(eventstore.TypeDAGRunFailed, run))
	}
	dir.collect()

	var delivered []string
	for len(delivered) < len(ids) {
		report, _ := clock.nextReport(console)
		require.Less(t, len(report.body), 1<<20, "a report fits what Dagu Console accepts")
		events := decodeEvents(t, report)
		require.LessOrEqual(t, len(events.Events), maxReportEventBytes)
		var batch []reportEvent
		require.NoError(t, json.Unmarshal(events.Events, &batch))
		require.NotEmpty(t, batch)
		require.Less(t, len(batch), len(ids), "a batch of large events is capped by size")
		for _, event := range batch {
			require.Len(t, event.FailedSteps, maxFailedSteps)
			for _, name := range event.FailedSteps {
				require.LessOrEqual(t, len(name), maxStepNameBytes)
				require.True(t, utf8.ValidString(name), "a step name is cut between characters")
			}
			delivered = append(delivered, event.ID)
		}
	}
	assert.ElementsMatch(t, ids, delivered, "each event is reported once")
}

// An event with a name or ID longer than Dagu Console keeps is left out like
// other unreported events, so it cannot hold the events after it back.
func TestReportEventsOversizedIdentifiers(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	console := newFakeConsole(t)
	clock := startEventReporter(t, console, dir, testClockStart)
	longRunID := etlRun(ir.Failed)
	longRunID.DAGRunID = strings.Repeat("r", maxIDBytes+1)
	longName := etlRun(ir.Failed)
	longName.DAGRunID, longName.Name = "run-2", strings.Repeat("n", maxNameBytes+1)
	dir.emit(eventstore.TypeDAGRunFailed, longRunID)
	dir.emit(eventstore.TypeDAGRunFailed, longName)
	kept := etlRun(ir.Failed)
	kept.DAGRunID = "run-3"
	keptID := dir.emit(eventstore.TypeDAGRunFailed, kept)
	dir.collect()

	first := decodeEvents(t, nextReport(t, clock, console))
	assert.Equal(t, []string{keptID}, first.ids(t))
	assert.NotEmpty(t, first.Cursor)
	assert.Nil(t, decodeEvents(t, nextReport(t, clock, console)).ids(t), "the position moved past all three")
}

// Of the processes that share a data directory, the one holding the lease
// reports events; the others report health until it lets go.
func TestReportEventsLease(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
	holderConsole := newFakeConsole(t)
	holder, holderClock := newEventReporter(t, holderConsole.credentials, dir, testClockStart)
	standbyConsole := newFakeConsole(t)
	_, standbyClock := newEventReporter(t, standbyConsole.credentials, dir, testClockStart)

	failedID := dir.emit(eventstore.TypeDAGRunFailed, etlRun(ir.Failed))
	held, heldPassed := holderClock.nextReport(holderConsole)
	standby, standbyPassed := standbyClock.nextReport(standbyConsole)
	assert.Equal(t, 5*time.Second, heldPassed)
	assert.Equal(t, []string{failedID}, decodeEvents(t, held).ids(t))
	assert.Equal(t, maxStartDelay/2, standbyPassed)
	assert.Equal(t, eventReport{}, decodeEvents(t, standby))

	holder.Stop()
	standbyClock.elapse()
	standbyClock.wait()
	retry := etlRun(ir.Failed)
	retry.AttemptID = "attempt-2"
	retryID := dir.emit(eventstore.TypeDAGRunFailed, retry)

	takeover, passed := standbyClock.nextReport(standbyConsole)
	assert.Equal(t, 5*time.Second, passed)
	assert.Equal(t, []string{retryID}, decodeEvents(t, takeover).ids(t))
}

// An event store that cannot be read leaves events out of reports, and the
// failure is logged once.
func TestReportEventsUnreadable(t *testing.T) {
	t.Parallel()

	dir := newEventDir(t)
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
		assert.Equal(t, eventReport{}, decodeEvents(t, nextReport(t, clock, console)))
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
		assert.Equal(t, eventReport{}, decodeEvents(t, nextReport(t, clock, console)))
	}
	assert.Equal(t, 1, strings.Count(logs.String(), "the event store is disabled"))
}

// eventDir is a data directory shared by reporters: an event store and the
// reporters' state.
type eventDir struct {
	t         *testing.T
	store     *fileeventstore.Store
	storeDir  string
	stateFile string
	recorded  int
}

func newEventDir(t *testing.T) *eventDir {
	t.Helper()
	dir := t.TempDir()
	storeDir := filepath.Join(dir, "events")
	store, err := fileeventstore.New(storeDir)
	require.NoError(t, err)
	return &eventDir{t: t, store: store, storeDir: storeDir, stateFile: filepath.Join(dir, "cloud", "report-state.json")}
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
		Lease:         filemonitor.NewLease(d.stateFile, nil),
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

// emitQueued stores the queued events of n runs and returns their IDs in the
// order they are recorded. The writes run concurrently because each one is
// synced to disk.
func (d *eventDir) emitQueued(n int) []string {
	d.t.Helper()
	ids := make([]string, 0, n)
	var writes errgroup.Group
	writes.SetLimit(16)
	for i := range n {
		run := etlRun(ir.Queued)
		run.DAGRunID = fmt.Sprintf("run-%d", i)
		event := eventstore.NewDAGRunEvent(eventstore.Source{Service: eventstore.SourceServiceScheduler}, eventstore.TypeDAGRunQueued, run, nil)
		d.stamp(event)
		ids = append(ids, event.ID)
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

// etlRun is a root run of the etl DAG that was queued at 08:59, started at
// 09:00, and, in a final status, finished at 09:05.
func etlRun(status ir.Status) *ir.DAGRunStatus {
	run := &ir.DAGRunStatus{
		Name:      "etl",
		DAGRunID:  "run-1",
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
	r := newReporter(credentials, nil, fixedLevel(config.ReportRuns))
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

// eventReport is the part of a report about events.
type eventReport struct {
	Events json.RawMessage `json:"events"`
	Cursor string          `json:"cursor"`
	Gap    *reportGap      `json:"gap"`
}

func decodeEvents(t *testing.T, report receivedReport) eventReport {
	t.Helper()
	var events eventReport
	require.NoError(t, json.Unmarshal([]byte(report.body), &events))
	return events
}

// ids returns the IDs of the events in r, or nil when r has none.
func (r eventReport) ids(t *testing.T) []string {
	t.Helper()
	if r.Events == nil {
		return nil
	}
	var events []reportEvent
	require.NoError(t, json.Unmarshal(r.Events, &events))
	var ids []string
	for _, event := range events {
		ids = append(ids, event.ID)
	}
	return ids
}

type failingReader struct{}

func (failingReader) DAGRunHeadCursor(context.Context) (eventstore.DAGRunCursor, error) {
	return eventstore.DAGRunCursor{}, errors.New("event store unavailable")
}

func (failingReader) ReadDAGRunEvents(context.Context, eventstore.DAGRunCursor) ([]*eventstore.Event, eventstore.DAGRunCursor, error) {
	return nil, eventstore.DAGRunCursor{}, errors.New("event store unavailable")
}
