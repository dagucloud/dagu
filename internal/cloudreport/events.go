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
	"time"
	"unicode/utf8"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

const (
	// eventCheckInterval is how often the event store is checked for events
	// that call for an early report.
	eventCheckInterval = 5 * time.Second
	maxReportEvents    = 500
	// maxReportEventBytes bounds the encoded events of one report, leaving
	// room for the rest of it under the 1 MiB that Dagu Console accepts.
	maxReportEventBytes = 512 << 10
	maxFailedSteps      = 50
	// maxStepNameBytes is the longest failed step name Dagu Console keeps.
	maxStepNameBytes = 512
	// maxNameBytes and maxIDBytes are the longest DAG name and run or event
	// ID Dagu Console keeps; it leaves out an event with a longer one. With
	// them, an event encodes to at most about 28 KiB, well inside a batch.
	maxNameBytes = 512
	maxIDBytes   = 256
	// caughtUpSaveInterval is how often an idle reporter records that it has
	// seen every event, so that a restart does not take idleness for a gap.
	caughtUpSaveInterval = time.Hour
	eventStateVersion    = 1
)

const (
	warnRead            = "Failed to read DAG-run events; reports to Dagu Console carry health only"
	warnSave            = "Failed to save which DAG-run events Dagu Console has acknowledged"
	warnStateUnreadable = "Unreadable Dagu Console report state; reporting DAG-run events from the newest one"
	warnLease           = "Failed to take the lease to report DAG-run events to Dagu Console"
	warnLeaseLost       = "Lost the lease to report DAG-run events to Dagu Console"
)

// Events configures the DAG-run events that reports carry.
type Events struct {
	// Reader is the event store. A nil Reader leaves events out of reports.
	Reader eventstore.DAGRunReader
	// RetentionDays is how many days the event store keeps events; 0 keeps
	// them forever.
	RetentionDays int
	// State persists which events Dagu Console has acknowledged.
	State StateStore
	// Lease lets one process at a time report the events of a shared data
	// directory.
	Lease Lease
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
	// terminal events end a DAG-run attempt and name its failed steps.
	terminal bool
	// urgent events are reported early.
	urgent bool
}

// reportedEventTypes lists the event types that reports carry.
var reportedEventTypes = map[eventstore.EventType]eventTraits{
	eventstore.TypeDAGRunQueued:             {},
	eventstore.TypeDAGRunRunning:            {},
	eventstore.TypeDAGRunWaiting:            {urgent: true},
	eventstore.TypeDAGRunSucceeded:          {terminal: true},
	eventstore.TypeDAGRunPartiallySucceeded: {terminal: true},
	eventstore.TypeDAGRunFailed:             {terminal: true, urgent: true},
	eventstore.TypeDAGRunAborted:            {terminal: true, urgent: true},
	eventstore.TypeDAGRunRejected:           {terminal: true, urgent: true},
}

// reportEvent is a DAG-run status change in a report.
type reportEvent struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	DAGName     string   `json:"dag_name"`
	DAGRunID    string   `json:"dag_run_id"`
	AttemptID   string   `json:"attempt_id"`
	Status      string   `json:"status"`
	OccurredAt  string   `json:"occurred_at"`
	QueuedAt    string   `json:"queued_at,omitempty"`
	StartedAt   string   `json:"started_at,omitempty"`
	FinishedAt  string   `json:"finished_at,omitempty"`
	FailedSteps []string `json:"failed_steps,omitempty"`
}

// reportGap tells Dagu Console that events after Since were lost.
type reportGap struct {
	Since string `json:"since"`
}

// newReportEvent describes event for Dagu Console. It reports false for an
// event that reports leave out: an unreported type, a sub-DAG run, an event
// without a valid status snapshot, or one with a name or ID longer than Dagu
// Console keeps.
func newReportEvent(event *eventstore.Event) (reportEvent, bool) {
	traits, ok := reportedEventTypes[event.Type]
	if !ok {
		return reportEvent{}, false
	}
	snapshot, err := eventstore.DAGRunSnapshotFromEvent(event)
	if err != nil {
		return reportEvent{}, false
	}
	status := snapshot.DAGRunStatus()
	if !status.Parent.Zero() {
		return reportEvent{}, false
	}
	if len(status.Name) > maxNameBytes || len(event.ID) > maxIDBytes ||
		len(status.DAGRunID) > maxIDBytes || len(status.AttemptID) > maxIDBytes {
		return reportEvent{}, false
	}

	reported := reportEvent{
		ID:         event.ID,
		Type:       string(event.Type),
		DAGName:    status.Name,
		DAGRunID:   status.DAGRunID,
		AttemptID:  status.AttemptID,
		Status:     event.Status,
		OccurredAt: formatTime(event.OccurredAt),
		QueuedAt:   snapshotTime(status.QueuedAt),
		StartedAt:  snapshotTime(status.StartedAt),
		FinishedAt: snapshotTime(status.FinishedAt),
	}
	if traits.terminal {
		reported.FailedSteps = failedSteps(status)
	}
	return reported, true
}

// failedSteps names the failed steps of status, handlers included.
func failedSteps(status *ir.DAGRunStatus) []string {
	var names []string
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
}

// eventBatch is what the next report carries from the event store.
type eventBatch struct {
	events []reportEvent
	// next is the position after events, and token identifies it.
	next  position
	token string
	// capped reports that more events wait after the batch.
	capped bool
	// size is the encoded size of events.
	size   int
	readAt time.Time
	// lastEventAt is when the latest event in the batch occurred.
	lastEventAt time.Time
	// urgent names the events in the batch that call for an early report.
	urgent []string
}

// fits reports whether an event encoded in size bytes can join the batch.
func (b *eventBatch) fits(size int) bool {
	return len(b.events) < maxReportEvents && b.size+size <= maxReportEventBytes
}

func (b *eventBatch) add(event *eventstore.Event, reported reportEvent, size int) {
	b.events = append(b.events, reported)
	b.size += size
	if reportedEventTypes[event.Type].urgent {
		b.urgent = append(b.urgent, event.ID)
	}
	if event.OccurredAt.After(b.lastEventAt) {
		b.lastEventAt = event.OccurredAt
	}
}

func (b *eventBatch) ids() []string {
	ids := make([]string, 0, len(b.events))
	for _, event := range b.events {
		ids = append(ids, event.ID)
	}
	return ids
}

// eventFeed reads the events that reports carry and tracks which of them
// Dagu Console has acknowledged. The report loop owns it.
type eventFeed struct {
	reader        eventstore.DAGRunReader
	retentionDays int
	store         StateStore
	lease         Lease
	now           func() time.Time

	held bool
	// loaded reports that state was read or written under the lease.
	loaded bool
	state  eventState
	batch  *eventBatch
	// notified holds urgent events that accepted reports carried without
	// moving the position, so that they call for no more early reports.
	notified map[string]struct{}
	standby  bool
	warning  string
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
		lease:         events.Lease,
		now:           time.Now,
	}
}

// poll prepares the events of the next report of serverID. It reads the event
// store only when read is set, and reports whether the events include an
// urgent one that no accepted report has carried.
func (f *eventFeed) poll(ctx context.Context, serverID string, read bool) bool {
	f.batch = nil
	if !f.hold(ctx) || !f.load(ctx, serverID) || !read {
		return false
	}
	return f.read(ctx)
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
// when there is no state, and after events were lost.
func (f *eventFeed) load(ctx context.Context, serverID string) bool {
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

// restart saves next at the newest event in the store.
func (f *eventFeed) restart(ctx context.Context, next eventState) bool {
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
	readAt := f.now()

	acked := make(map[string]struct{}, len(f.state.Acked))
	for _, id := range f.state.Acked {
		acked[id] = struct{}{}
	}
	batch := &eventBatch{events: []reportEvent{}, next: position{Cursor: cursor}, readAt: readAt}
	unacked := false
	for _, event := range events {
		if _, ok := acked[event.ID]; ok {
			continue
		}
		unacked = true
		reported, ok := newReportEvent(event)
		if !ok {
			continue
		}
		size := encodedSize(reported)
		if !batch.fits(size) {
			batch.capped = true
			break
		}
		batch.add(event, reported, size)
	}

	if !unacked {
		f.advance(ctx, cursor, readAt)
		return false
	}
	if batch.capped {
		batch.next = position{Cursor: f.state.Cursor, Acked: slices.Concat(f.state.Acked, batch.ids())}
	}
	batch.token = batch.next.token()
	f.batch = batch
	return slices.ContainsFunc(batch.urgent, func(id string) bool {
		_, ok := f.notified[id]
		return !ok
	})
}

// encodedSize is how many bytes reported adds to a report's events.
func encodedSize(reported reportEvent) int {
	data, _ := json.Marshal(reported)
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

// fill adds the prepared events, and any gap, to req.
func (f *eventFeed) fill(req *reportRequest) {
	if f == nil || !f.loaded {
		return
	}
	if f.batch != nil {
		req.Events = f.batch.events
		req.Cursor = f.batch.token
	}
	if !f.state.GapSince.IsZero() {
		req.Gap = &reportGap{Since: formatTime(f.state.GapSince)}
	}
}

// acknowledge applies Dagu Console's acceptance of a report that fill
// prepared. The position moves only when ack names the position the report
// carried. It reports whether more events wait to be reported.
func (f *eventFeed) acknowledge(ctx context.Context, ack string) bool {
	if f == nil || !f.loaded {
		return false
	}
	batch := f.batch
	f.batch = nil
	moved := batch != nil && ack == batch.token
	if batch != nil && !moved {
		if f.notified == nil {
			f.notified = make(map[string]struct{})
		}
		for _, id := range batch.urgent {
			f.notified[id] = struct{}{}
		}
	}
	if !moved && f.state.GapSince.IsZero() {
		return false
	}

	next := f.state
	next.GapSince = time.Time{}
	if moved {
		next.position = batch.next
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
	f.held, f.loaded = false, false
	f.state, f.batch, f.notified = eventState{}, nil, nil
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
