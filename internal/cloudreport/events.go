// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

const (
	// eventCheckInterval is how often the event store is checked for events
	// that call for an early report.
	eventCheckInterval = 5 * time.Second
	maxFinishedRuns    = 500
	// maxFinishedBytes bounds the encoded finished runs of one report. With
	// at most maxActiveRuns runs in progress, about 250 KiB, the report
	// stays under the 1 MiB that Dagu Console accepts.
	maxFinishedBytes = 512 << 10
	maxFailedSteps   = 50
	// maxStepNameBytes is the longest failed step name Dagu Console keeps.
	maxStepNameBytes = 512
	// maxRunErrors and maxErrorBytes bound the errors a finished run carries
	// at the errors level, as Dagu Console keeps them.
	maxRunErrors  = 5
	maxErrorBytes = 2048
	// maxNameBytes and maxIDBytes are the longest DAG name and run or
	// attempt ID Dagu Console keeps; reports leave out a run with a longer
	// one. With them, a finished run encodes to about 41 KiB at most, and
	// about 245 KiB even if JSON escapes every byte: always inside a batch.
	maxNameBytes = 512
	maxIDBytes   = 256
	// caughtUpSaveInterval is how often an idle reporter records that it has
	// seen every event, so that a restart does not take idleness for a gap.
	caughtUpSaveInterval = time.Hour
	eventStateVersion    = 2
)

const (
	warnRead            = "Failed to read DAG-run events; reports to Dagu Console carry health only"
	warnSave            = "Failed to save which DAG-run events Dagu Console has acknowledged"
	warnStateUnreadable = "Unreadable Dagu Console report state; reporting DAG-run events from the newest one"
	warnLease           = "Failed to take the lease to report DAG-run events to Dagu Console"
	warnLeaseLost       = "Lost the lease to report DAG-run events to Dagu Console"
	warnActive          = "Failed to list the DAG runs in progress; reports to Dagu Console leave them out until listing succeeds"
	warnMarkStale       = "Failed to record that DAG-run events stopped being reported to Dagu Console"
	warnClearStale      = "Failed to clear the record that DAG-run events stopped being reported to Dagu Console"
)

// Events configures the DAG runs that reports carry: the runs that finish,
// read from the event store, and the runs in progress.
type Events struct {
	// Reader is the event store. A nil Reader leaves runs out of reports.
	Reader eventstore.DAGRunReader
	// RetentionDays is how many days the event store keeps events; 0 keeps
	// them forever.
	RetentionDays int
	// State persists which events Dagu Console has acknowledged.
	State StateStore
	// Stale persists, without the lease, that the position in State predates
	// a period below runs, so that the next process to take the lease
	// discards it. It must be set when Reader is.
	Stale StateStore
	// Lease lets one process at a time report the events of a shared data
	// directory.
	Lease Lease
	// Statuses lists the runs in progress when they are not known from the
	// events: when the reporter starts without a saved list of them, and
	// after events were lost. It must be set when Reader is.
	Statuses RunStatuses
}

// StateStore persists encoded reporter state.
type StateStore interface {
	Load(ctx context.Context) (data []byte, found bool, err error)
	Save(ctx context.Context, data []byte) error
}

// Lease is a lock shared by processes. TryLock fails with
// dirlock.ErrLockConflict while another process holds it, and Heartbeat fails
// once the lease is lost.
type Lease interface {
	TryLock() error
	Heartbeat(ctx context.Context) error
	Unlock() error
}

type eventTraits struct {
	// finished events end a DAG-run attempt.
	finished bool
	// urgent events are reported early.
	urgent bool
}

// reportedEventTypes lists the event types that change what reports carry.
var reportedEventTypes = map[eventstore.EventType]eventTraits{
	eventstore.TypeDAGRunQueued:             {},
	eventstore.TypeDAGRunRunning:            {},
	eventstore.TypeDAGRunWaiting:            {},
	eventstore.TypeDAGRunSucceeded:          {finished: true},
	eventstore.TypeDAGRunPartiallySucceeded: {finished: true},
	eventstore.TypeDAGRunFailed:             {finished: true, urgent: true},
	eventstore.TypeDAGRunAborted:            {finished: true, urgent: true},
	eventstore.TypeDAGRunRejected:           {finished: true, urgent: true},
}

// finishedRun is a top-level DAG-run attempt that ended, as a report carries
// it.
type finishedRun struct {
	Workflow    string     `json:"workflow"`
	DAGRunID    string     `json:"dag_run_id"`
	AttemptID   string     `json:"attempt_id"`
	Status      string     `json:"status"`
	TriggerType string     `json:"trigger_type"`
	QueuedAt    reportTime `json:"queued_at"`
	StartedAt   reportTime `json:"started_at"`
	FinishedAt  string     `json:"finished_at"`
	FailedSteps []string   `json:"failed_steps"`
	// Errors say why the run failed; reports carry them only at the errors
	// level.
	Errors []stepError `json:"errors,omitempty"`
}

// stepError is why a run failed: a failed step's error, or the run's own
// when Step is empty.
type stepError struct {
	Step    string `json:"step"`
	Message string `json:"message"`
}

// reportGap tells Dagu Console that events after Since were lost.
type reportGap struct {
	Since string `json:"since"`
}

// reportedEvent is what an event changes in reports.
type reportedEvent struct {
	change runChange
	// finished is set for an event that ends an attempt.
	finished *finishedRun
	urgent   bool
}

// newReportedEvent describes event for Dagu Console. It reports false for an
// event that reports leave out: an unreported type, a sub-DAG run, an event
// without a valid status snapshot, or one with a name or ID longer than Dagu
// Console keeps.
func newReportedEvent(event *eventstore.Event) (reportedEvent, bool) {
	traits, ok := reportedEventTypes[event.Type]
	if !ok {
		return reportedEvent{}, false
	}
	snapshot, err := eventstore.DAGRunSnapshotFromEvent(event)
	if err != nil {
		return reportedEvent{}, false
	}
	status := snapshot.DAGRunStatus()
	if !reportable(status) {
		return reportedEvent{}, false
	}

	run := newActiveRun(status)
	reported := reportedEvent{change: runChange{run: run, ended: traits.finished}, urgent: traits.urgent}
	if traits.finished {
		finishedAt := snapshotTime(status.FinishedAt)
		if finishedAt == "" {
			finishedAt = formatTime(event.OccurredAt)
		}
		reported.finished = &finishedRun{
			Workflow:    run.Workflow,
			DAGRunID:    run.DAGRunID,
			AttemptID:   run.AttemptID,
			Status:      run.Status,
			TriggerType: run.TriggerType,
			QueuedAt:    run.QueuedAt,
			StartedAt:   run.StartedAt,
			FinishedAt:  finishedAt,
			FailedSteps: failedSteps(status),
		}
		if runFailed(status.Status) {
			reported.finished.Errors = runErrors(status)
		}
	}
	return reported, true
}

// failedSteps names the failed steps of status, handlers included.
func failedSteps(status *ir.DAGRunStatus) []string {
	names := []string{}
	for _, node := range status.NodesInRunOrder() {
		if node.Status != ir.NodeFailed || node.Step.Name == "" {
			continue
		}
		names = append(names, truncateBytes(node.Step.Name, maxStepNameBytes))
		if len(names) == maxFailedSteps {
			break
		}
	}
	return names
}

// runFailed reports whether a run ended without succeeding: failed,
// aborted, or rejected. A run that partly succeeded did not fail.
func runFailed(status ir.Status) bool {
	return status == ir.Failed || status == ir.Aborted || status == ir.Rejected
}

// runErrors returns why status failed: the errors of its failed steps,
// handlers included, in run order, else the run's own, at most maxRunErrors
// of them. Each is without terminal escapes and cut to its last
// maxErrorBytes, where an error says what went wrong. A step's error is
// masked as the run's status is, and ends with the tail of its stderr.
func runErrors(status *ir.DAGRunStatus) []stepError {
	var errs []stepError
	for _, node := range status.NodesInRunOrder() {
		if node.Status != ir.NodeFailed {
			continue
		}
		if message := errorMessage(node.Error); message != "" {
			errs = append(errs, stepError{Step: truncateBytes(node.Step.Name, maxStepNameBytes), Message: message})
			if len(errs) == maxRunErrors {
				return errs
			}
		}
	}
	if message := errorMessage(status.Error); len(errs) == 0 && message != "" {
		errs = append(errs, stepError{Message: message})
	}
	return errs
}

// errorMessage is an error as reports carry it: trimmed, without terminal
// escapes, and cut to its last maxErrorBytes after an ellipsis.
func errorMessage(text string) string {
	text = strings.TrimSpace(stringutil.StripANSI(text))
	if len(text) <= maxErrorBytes {
		return text
	}
	const ellipsis = "…"
	i := len(text) - (maxErrorBytes - len(ellipsis))
	for i < len(text) && !utf8.RuneStart(text[i]) {
		i++
	}
	return ellipsis + text[i:]
}

// withoutErrors returns runs as a level that sends no errors carries them.
func withoutErrors(runs []finishedRun) []finishedRun {
	out := make([]finishedRun, len(runs))
	for i, run := range runs {
		run.Errors = nil
		out[i] = run
	}
	return out
}

// truncateBytes shortens s to at most n bytes without splitting a character.
func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// snapshotTime formats a time from a status snapshot, or returns "" when the
// value is empty or unparseable.
func snapshotTime(value string) string {
	t, err := stringutil.ParseTime(value)
	if err != nil || t.IsZero() {
		return ""
	}
	return formatTime(t)
}

// position is a point in the event store. Dagu Console has acknowledged the
// events before Cursor and the events named in Acked.
type position struct {
	Cursor eventstore.DAGRunCursor `json:"cursor"`
	// Acked names acknowledged events after Cursor. It is set while a backlog
	// is reported in several batches, because a cursor cannot point between
	// the events of one read.
	Acked []string `json:"acked,omitempty"`
}

// token identifies the position to Dagu Console. It is a digest because a
// cursor can outgrow what the console keeps.
func (p position) token() string {
	data, _ := json.Marshal(p)
	sum := sha256.Sum256(data)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// eventState is the persisted part of eventFeed.
type eventState struct {
	Version int `json:"version"`
	// ServerID is the server that the position is reported for.
	ServerID string `json:"server_id"`
	position
	// CaughtUpAt is when the position last covered every event in the store.
	CaughtUpAt time.Time `json:"caught_up_at"`
	// LastEventAt is when the latest acknowledged event occurred.
	LastEventAt time.Time `json:"last_event_at,omitzero"`
	// GapSince marks lost events that no accepted report has mentioned yet.
	GapSince time.Time `json:"gap_since,omitzero"`
	// Active holds the runs in progress as of the position. It is nil while
	// they are unknown.
	Active activeSet `json:"active,omitzero"`
}

// eventBatch is what the next report carries from the event store.
type eventBatch struct {
	finished []finishedRun
	// changes are how the events of the batch change the runs in progress,
	// in order.
	changes []runChange
	// ids names the events of the batch.
	ids []string
	// next is the position after the batch, and token identifies it.
	next  position
	token string
	// capped reports that more events wait after the batch.
	capped bool
	// size is the encoded size of finished.
	size   int
	readAt time.Time
	// lastEventAt is when the latest event in the batch occurred.
	lastEventAt time.Time
	// urgent names the events in the batch that call for an early report.
	urgent []string
}

// fits reports whether a finished run encoded in size bytes can join the
// batch.
func (b *eventBatch) fits(size int) bool {
	return len(b.finished) < maxFinishedRuns && b.size+size <= maxFinishedBytes
}

func (b *eventBatch) add(event *eventstore.Event, reported reportedEvent, size int) {
	if reported.finished != nil {
		b.finished = append(b.finished, *reported.finished)
		b.size += size
	}
	if reported.urgent {
		b.urgent = append(b.urgent, event.ID)
	}
	b.changes = append(b.changes, reported.change)
	b.ids = append(b.ids, event.ID)
	if event.OccurredAt.After(b.lastEventAt) {
		b.lastEventAt = event.OccurredAt
	}
}

// eventFeed reads the events that reports carry, tracks which of them Dagu
// Console has acknowledged, and follows the runs in progress. The report loop
// owns it.
type eventFeed struct {
	reader        eventstore.DAGRunReader
	retentionDays int
	store         StateStore
	stale         StateStore
	lease         Lease
	statuses      RunStatuses
	now           func() time.Time
	// spawn runs a rebuild in the background.
	spawn func(func())

	held bool
	// loaded reports that state was read or written under the lease.
	loaded bool
	state  eventState
	batch  *eventBatch
	// fresh reports that the event store was read for the next report.
	fresh bool
	// sent is the cursor the last prepared report carried, which its
	// acknowledgement must name, and sentActive is what it said about the
	// runs in progress.
	sent       string
	sentActive activeReport
	// accepted is what the last accepted report said about the runs in
	// progress, or nil before the first.
	accepted *activeReport
	// notified holds urgent events that accepted reports carried without
	// moving the position, so that they call for no more early reports.
	notified map[string]struct{}
	// rebuild lists the runs in progress while they are unknown, and
	// rebuildAfter delays the next attempt after one failed.
	rebuild      *rebuild
	rebuildAfter time.Time
	standby      bool
	warning      string
}

// newEventFeed returns nil when events has no Reader.
func newEventFeed(events Events) *eventFeed {
	if events.Reader == nil {
		return nil
	}
	return &eventFeed{
		reader:        events.Reader,
		retentionDays: events.RetentionDays,
		store:         events.State,
		stale:         events.Stale,
		lease:         events.Lease,
		statuses:      events.Statuses,
		now:           time.Now,
		spawn:         func(fn func()) { go fn() },
	}
}

// poll prepares the next report of serverID. It reads the event store only
// when read is set, and reports whether the report calls for sending early:
// it carries an urgent event that no accepted report has carried, or the
// runs in progress differ from what the last accepted report said.
func (f *eventFeed) poll(ctx context.Context, serverID string, read bool) bool {
	f.batch, f.fresh = nil, false
	if !f.hold(ctx) || !f.load(ctx, serverID) {
		return false
	}
	f.rebuildActive(ctx)
	if !read {
		return false
	}
	urgent := f.read(ctx)
	return urgent || f.activeChanged()
}

// hold takes or keeps the lease.
func (f *eventFeed) hold(ctx context.Context) bool {
	if f.held {
		err := f.lease.Heartbeat(ctx)
		if err == nil {
			return true
		}
		f.warn(ctx, warnLeaseLost, err)
		// Another process owns the lease now; Unlock releases only what this
		// process still holds.
		_ = f.lease.Unlock()
		f.drop()
		return false
	}

	err := f.lease.TryLock()
	switch {
	case err == nil:
		f.held, f.standby = true, false
		f.recovered(warnLease, warnLeaseLost)
		logger.Info(ctx, "This process reports DAG-run events to Dagu Console")
		return true
	case errors.Is(err, dirlock.ErrLockConflict):
		if !f.standby {
			f.standby = true
			logger.Info(ctx, "Another process reports DAG-run events to Dagu Console")
		}
	default:
		f.warn(ctx, warnLease, err)
	}
	return false
}

// load reads the persisted state of serverID. It moves to the newest event
// when there is no state, when the state is marked stale, and after events
// were lost.
func (f *eventFeed) load(ctx context.Context, serverID string) bool {
	if !f.loaded && f.isStale(ctx) {
		if !f.restart(ctx, eventState{ServerID: serverID}) {
			return false
		}
		// Cleared only after the restart, so that a failure in between
		// leaves out more events rather than reporting stale ones.
		if err := f.saveStale(ctx, false); err != nil {
			f.warn(ctx, warnClearStale, err)
		}
		return true
	}
	if !f.loaded {
		state, err := f.readState(ctx)
		if err != nil {
			f.warn(ctx, warnStateUnreadable, err)
		}
		if state != nil {
			f.state, f.loaded = *state, true
		}
	}

	switch {
	case !f.loaded || f.state.ServerID != serverID:
		return f.restart(ctx, eventState{ServerID: serverID})
	case f.expired():
		return f.restart(ctx, eventState{
			ServerID:    serverID,
			LastEventAt: f.state.LastEventAt,
			GapSince:    f.gapSince(),
		})
	default:
		return true
	}
}

func (f *eventFeed) readState(ctx context.Context) (*eventState, error) {
	data, found, err := f.store.Load(ctx)
	if err != nil || !found {
		return nil, err
	}
	var state eventState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	if state.Version != eventStateVersion {
		return nil, fmt.Errorf("unsupported version %d", state.Version)
	}
	return &state, nil
}

// expired reports whether the event store may have removed events after the
// position. The store removes the events that occurred before the UTC day
// that began RetentionDays days ago.
func (f *eventFeed) expired() bool {
	if f.retentionDays <= 0 {
		return false
	}
	now := f.now().UTC()
	cutoff := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -f.retentionDays)
	return f.state.CaughtUpAt.Before(cutoff)
}

// gapSince returns when the lost events may start: after the latest
// acknowledged event, or after the position last caught up when no event was
// acknowledged. A gap that is not reported yet keeps its start.
func (f *eventFeed) gapSince() time.Time {
	switch {
	case !f.state.GapSince.IsZero():
		return f.state.GapSince
	case !f.state.LastEventAt.IsZero():
		return f.state.LastEventAt
	default:
		return f.state.CaughtUpAt
	}
}

// restart saves next at the newest event in the store, with the runs in
// progress unknown.
func (f *eventFeed) restart(ctx context.Context, next eventState) bool {
	f.stopRebuild()
	cursor, err := f.reader.DAGRunHeadCursor(ctx)
	if err != nil {
		f.warn(ctx, warnRead, err)
		return false
	}
	next.Version = eventStateVersion
	next.position = position{Cursor: cursor}
	next.CaughtUpAt = f.now()
	return f.save(ctx, next)
}

// read prepares a batch of the events after the position, and reports
// whether it includes an urgent event that no accepted report has carried.
func (f *eventFeed) read(ctx context.Context) bool {
	events, cursor, err := f.reader.ReadDAGRunEvents(ctx, f.state.Cursor)
	if err != nil {
		f.warn(ctx, warnRead, err)
		return false
	}
	f.recovered(warnRead)
	f.fresh = true
	readAt := f.now()

	acked := make(map[string]struct{}, len(f.state.Acked))
	for _, id := range f.state.Acked {
		acked[id] = struct{}{}
	}
	batch := &eventBatch{finished: []finishedRun{}, next: position{Cursor: cursor}, readAt: readAt}
	unacked := false
	for _, event := range events {
		if _, ok := acked[event.ID]; ok {
			continue
		}
		unacked = true
		reported, ok := newReportedEvent(event)
		if !ok {
			continue
		}
		size := 0
		if reported.finished != nil {
			size = encodedSize(*reported.finished)
			if !batch.fits(size) {
				batch.capped = true
				break
			}
		}
		batch.add(event, reported, size)
	}

	if !unacked {
		f.advance(ctx, cursor, readAt)
		return false
	}
	if batch.capped {
		// The events after the cap are read again, so the position names
		// every event the batch applies, not only those it reports.
		batch.next = position{Cursor: f.state.Cursor, Acked: slices.Concat(f.state.Acked, batch.ids)}
	}
	batch.token = batch.next.token()
	f.batch = batch
	return slices.ContainsFunc(batch.urgent, func(id string) bool {
		_, ok := f.notified[id]
		return !ok
	})
}

// encodedSize is how many bytes run adds to a report's finished runs.
func encodedSize(run finishedRun) int {
	data, _ := json.Marshal(run)
	return len(data) + len(",")
}

// advance moves the position past changes in the store that hold no event to
// report. An unchanged position is saved only now and then, to record that
// nothing was missed.
func (f *eventFeed) advance(ctx context.Context, cursor eventstore.DAGRunCursor, readAt time.Time) {
	if len(f.state.Acked) == 0 && f.state.Cursor.Equal(cursor) &&
		readAt.Sub(f.state.CaughtUpAt) < caughtUpSaveInterval {
		return
	}
	next := f.state
	next.position = position{Cursor: cursor}
	next.CaughtUpAt = readAt
	f.save(ctx, next)
}

// activeReport returns what the next report says about the runs in
// progress: nothing unless the event store was read for it.
func (f *eventFeed) activeReport() activeReport {
	if !f.fresh {
		return activeReport{}
	}
	set := f.state.Active
	if f.batch != nil {
		set = set.with(f.batch.changes)
	}
	return newActiveReport(set)
}

// activeChanged reports whether the runs in progress differ from what the
// last accepted report said about them.
func (f *eventFeed) activeChanged() bool {
	current := f.activeReport()
	return current.known && f.accepted != nil && !current.equal(*f.accepted)
}

// rebuildActive starts listing the runs in progress while they are unknown,
// and makes them known once the listing is done.
func (f *eventFeed) rebuildActive(ctx context.Context) {
	if f.state.Active != nil {
		return
	}
	if f.rebuild == nil {
		if f.now().Before(f.rebuildAfter) {
			return
		}
		f.startRebuild(ctx)
	}
	select {
	case <-f.rebuild.done:
	default:
		return
	}
	done := f.rebuild
	f.rebuild = nil
	if done.err != nil {
		f.warn(ctx, warnActive, done.err)
		f.rebuildAfter = f.now().Add(rebuildRetryInterval)
		return
	}
	f.recovered(warnActive)
	next := f.state
	next.Active = done.runs.with(done.pending)
	f.save(ctx, next)
}

// startRebuild lists the runs in progress in the background. The changes
// that reports apply meanwhile are kept to apply on top of what it finds.
func (f *eventFeed) startRebuild(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	r := &rebuild{cancel: cancel, done: make(chan struct{})}
	since := f.now().Add(-activeLookback)
	f.rebuild = r
	f.spawn(func() {
		defer close(r.done)
		statuses, err := f.statuses.ListStatuses(ctx, persis.DAGRunListOptions{
			Statuses:  activeStatuses,
			From:      persis.NewUTC(since),
			Unbounded: true,
		})
		r.runs, r.err = activeSetOf(statuses), err
	})
}

// stopRebuild cancels the rebuild in progress and waits for it to end.
func (f *eventFeed) stopRebuild() {
	if f.rebuild == nil {
		return
	}
	f.rebuild.cancel()
	<-f.rebuild.done
	f.rebuild = nil
}

// reports reports whether this process reports the events, holding their
// lease.
func (f *eventFeed) reports() bool {
	return f != nil && f.loaded
}

// fill adds the prepared finished runs, the runs in progress, and any gap to
// req.
func (f *eventFeed) fill(req *reportRequest) {
	if f == nil || !f.loaded {
		return
	}
	if f.batch != nil {
		req.Finished = f.batch.finished
		if req.Level != config.ReportErrors {
			req.Finished = withoutErrors(req.Finished)
		}
		req.Cursor = f.batch.token
	}
	if !f.state.GapSince.IsZero() {
		req.Gap = &reportGap{Since: formatTime(f.state.GapSince)}
		if req.Cursor == "" {
			// A gap carries the position it was found at, so that an
			// acknowledgement can confirm Dagu Console recorded it.
			req.Cursor = f.state.token()
		}
	}
	f.sent = req.Cursor
	f.sentActive = f.activeReport()
	if f.sentActive.known {
		total := f.sentActive.total
		req.Active, req.ActiveTotal = f.sentActive.runs, &total
	}
}

// acknowledge applies Dagu Console's acceptance of a report that fill
// prepared. The position moves only when ack names the position the report
// carried. It reports whether more events wait to be reported.
func (f *eventFeed) acknowledge(ctx context.Context, ack string) bool {
	if f == nil || !f.loaded {
		return false
	}
	batch, sent := f.batch, f.sent
	f.batch, f.sent = nil, ""
	accepted := f.sentActive
	f.accepted = &accepted
	// Only an acknowledgement of the cursor the report carried confirms its
	// events and gap.
	confirmed := sent != "" && ack == sent
	moved := batch != nil && confirmed
	if batch != nil && !moved {
		if f.notified == nil {
			f.notified = make(map[string]struct{})
		}
		for _, id := range batch.urgent {
			f.notified[id] = struct{}{}
		}
	}
	if !confirmed {
		return false
	}

	next := f.state
	next.GapSince = time.Time{}
	if moved {
		next.position = batch.next
		next.Active = f.state.Active.with(batch.changes)
		if !batch.capped {
			next.CaughtUpAt = batch.readAt
		}
		if batch.lastEventAt.After(next.LastEventAt) {
			next.LastEventAt = batch.lastEventAt
		}
	}
	if !f.save(ctx, next) {
		return false
	}
	if moved {
		f.notified = nil
		if next.Active == nil && f.rebuild != nil {
			f.rebuild.pending = append(f.rebuild.pending, batch.changes...)
		}
	}
	return moved && batch.capped
}

// save makes next the state if it persists while the lease is held.
func (f *eventFeed) save(ctx context.Context, next eventState) bool {
	if !f.hold(ctx) {
		return false
	}
	data, err := json.Marshal(next)
	if err == nil {
		err = f.store.Save(ctx, data)
	}
	if err != nil {
		f.warn(ctx, warnSave, err)
		return false
	}
	f.recovered(warnSave)
	f.state, f.loaded = next, true
	return true
}

// forget discards the saved position and gives up the lease, so that
// reporting events again later, in this process or another, leaves out the
// events in between. Without the lease, it marks a saved position stale for
// the next process that takes the lease to discard.
func (f *eventFeed) forget(ctx context.Context) {
	if f == nil {
		return
	}
	if !f.held || !f.save(ctx, eventState{Version: eventStateVersion}) {
		f.markStale(ctx)
	}
	f.close(ctx)
}

// staleMark is the persisted record that the saved position predates a
// period below runs.
type staleMark struct {
	Stale bool `json:"stale"`
}

// markStale marks the saved position stale. It writes nothing while no
// position is saved, so that a server that never reported runs creates no
// files, or while the position is already marked.
func (f *eventFeed) markStale(ctx context.Context) {
	state, err := f.readState(ctx)
	if err == nil && (state == nil || state.ServerID == "") {
		return
	}
	if f.isStale(ctx) {
		return
	}
	if err := f.saveStale(ctx, true); err != nil {
		f.warn(ctx, warnMarkStale, err)
		return
	}
	f.recovered(warnMarkStale)
}

// isStale reports whether the saved position is marked stale. A mark that
// cannot be read counts as one, so that stale events are never reported.
func (f *eventFeed) isStale(ctx context.Context) bool {
	data, found, err := f.stale.Load(ctx)
	if err != nil {
		return true
	}
	if !found {
		return false
	}
	var mark staleMark
	return json.Unmarshal(data, &mark) != nil || mark.Stale
}

func (f *eventFeed) saveStale(ctx context.Context, stale bool) error {
	data, err := json.Marshal(staleMark{Stale: stale})
	if err != nil {
		return err
	}
	return f.stale.Save(ctx, data)
}

// close gives up the lease.
func (f *eventFeed) close(ctx context.Context) {
	if f == nil || !f.held {
		return
	}
	if err := f.lease.Unlock(); err != nil {
		logger.Warn(ctx, "Failed to release the lease to report DAG-run events to Dagu Console", tag.Error(err))
	}
	f.drop()
}

// drop forgets the lease and everything read under it.
func (f *eventFeed) drop() {
	f.stopRebuild()
	f.held, f.loaded, f.fresh = false, false, false
	f.state, f.batch, f.notified, f.accepted = eventState{}, nil, nil, nil
	f.rebuildAfter = time.Time{}
}

// warn logs a failure unless it repeats the previous one.
func (f *eventFeed) warn(ctx context.Context, msg string, err error) {
	if f.warning == msg {
		return
	}
	f.warning = msg
	logger.Warn(ctx, msg, tag.Error(err))
}

// recovered lets the failures in msgs be logged again.
func (f *eventFeed) recovered(msgs ...string) {
	if slices.Contains(msgs, f.warning) {
		f.warning = ""
	}
}
