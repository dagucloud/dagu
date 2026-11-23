// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
)

const (
	// auditCheckInterval is how often today's audit file is checked for new
	// entries.
	auditCheckInterval = 5 * time.Second
	// auditScanInterval is how often every audit file is checked, for
	// entries that land in an older day's file and for files removed.
	auditScanInterval = time.Minute
	// auditHeartbeatInterval is how often the lease is renewed, well within
	// the minute after which another process may take it over.
	auditHeartbeatInterval = 20 * time.Second
	// auditBatchEntries is the most entries one request to Console carries.
	auditBatchEntries = 1000
	// auditBatchBytes is the stored size past which a batch takes no more
	// reads, so a backlog of large entries is sent in parts.
	auditBatchBytes   = 4 << 20
	auditStateVersion = 1
)

const (
	warnAuditRead      = "Failed to read the audit entries to send to Dagu Console"
	warnAuditSend      = "Failed to send audit entries to Dagu Console"
	warnAuditSave      = "Failed to save which audit entries Dagu Console has stored"
	warnAuditLoad      = "Failed to read the audit shipping state; trying again"
	warnAuditState     = "Unreadable audit shipping state; sending the audit entries on disk from the oldest"
	warnAuditLease     = "Failed to take the lease to send audit entries to Dagu Console"
	warnAuditLeaseLost = "Lost the lease to send audit entries to Dagu Console"
)

// auditGapNamespace scopes the IDs derived for gaps.
var auditGapNamespace = uuid.MustParse("6f1d3c2e-8a4b-4c5d-9e7f-0a1b2c3d4e5f")

// auditLevel is which audit entries the shipper sends.
type auditLevel string

const (
	// auditChanges sends every entry except successful reads.
	auditChanges auditLevel = "changes"
	// auditAll sends every entry.
	auditAll auditLevel = "all"
)

// auditReadTools are the MCP tools that only read, as their ReadOnlyHint
// annotation says.
var auditReadTools = []string{"dagu_read"}

// auditFeed reads the entries an audit store holds after a position.
type auditFeed interface {
	Read(ctx context.Context, q audit.FeedQuery) (audit.FeedChunk, error)
}

// auditShipper sends the audit entries a store holds, from the position its
// state saves, in batches. The process holding the lease sends; others stand
// by.
type auditShipper struct {
	feed  auditFeed
	state StateStore
	lease Lease
	level auditLevel
	// send delivers a batch and returns nil once Console has stored it.
	send  func(context.Context, auditBatch) error
	after func(time.Duration) <-chan time.Time
	now   func() time.Time

	cancel context.CancelFunc
	done   chan struct{}

	// The fields below are owned by the loop.
	held        bool
	standby     bool
	heartbeatAt time.Time
	loaded      bool
	saved       auditState
	// pending is a batch Console did not store, sent again before anything
	// is read.
	pending   *auditPass
	scannedAt time.Time
	warning   string
}

// auditState is what the shipper saves once Console stores a batch.
type auditState struct {
	Version int `json:"version"`
	// Position is the feed's position after the entries Console stored.
	Position json.RawMessage `json:"position,omitempty"`
	// Gaps are the losses not sent yet, one per reason.
	Gaps []auditGap `json:"gaps,omitempty"`
}

// auditGap records entries that did not reach Console.
type auditGap struct {
	// ID derives from the reason and the range, so a resent gap is stored
	// once.
	ID     string    `json:"id"`
	Reason string    `json:"reason"`
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
}

// auditBatch is what one request to Console carries.
type auditBatch struct {
	Entries []*audit.Entry
	Gap     *auditGap
}

// auditPass is a batch and the state to save once Console stores it.
type auditPass struct {
	batch auditBatch
	next  auditState
	// more reports that another batch goes at once: this one is full, or a
	// gap is left for another request.
	more bool
}

func newAuditShipper(
	feed auditFeed,
	state StateStore,
	lease Lease,
	level auditLevel,
	send func(context.Context, auditBatch) error,
) *auditShipper {
	return &auditShipper{
		feed:  feed,
		state: state,
		lease: lease,
		level: level,
		send:  send,
		after: time.After,
		now:   time.Now,
	}
}

func (s *auditShipper) start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	s.done = make(chan struct{})
	go s.run(ctx)
}

// stop ends shipping, waits for a batch in flight, and releases the lease.
func (s *auditShipper) stop() {
	s.cancel()
	<-s.done
}

func (s *auditShipper) run(ctx context.Context) {
	defer close(s.done)
	defer s.release()
	for {
		s.ship(ctx)
		select {
		case <-ctx.Done():
			return
		case <-s.after(auditCheckInterval):
		}
	}
}

// ship sends batches while a full one, or a gap left over, calls for another.
// Every audit file is checked on the first pass and once a minute after it;
// today's file on the others. A check of every file stays due until all it
// found is stored. A batch Console did not store goes again first, as built,
// so a failing Console costs no reads.
func (s *auditShipper) ship(ctx context.Context) {
	if !s.hold(ctx) || !s.load(ctx) {
		return
	}
	startedAt := s.now()
	full := s.scannedAt.IsZero() || startedAt.Sub(s.scannedAt) >= auditScanInterval
	for {
		pass, resend := s.pending, s.pending != nil
		if !resend {
			built, err := s.build(ctx, full)
			if ctx.Err() != nil {
				// Stopping: what was read but not sent is read again later.
				return
			}
			if err != nil {
				s.warn(ctx, warnAuditRead, err)
				return
			}
			s.recovered(warnAuditRead)
			pass = &built
		}
		if len(pass.batch.Entries) == 0 && pass.batch.Gap == nil {
			// Entries the level skips still move the position.
			if !bytes.Equal(pass.next.Position, s.saved.Position) && !s.save(ctx, pass.next) {
				return
			}
			break
		}
		if err := s.send(ctx, pass.batch); err != nil {
			s.pending = pass
			s.warn(ctx, warnAuditSend, err)
			return
		}
		s.pending = nil
		s.recovered(warnAuditSend)
		if !s.save(ctx, pass.next) {
			return
		}
		// After a batch sent again, this pass still reads what came since.
		if !pass.more && !resend {
			break
		}
	}
	if full {
		s.scannedAt = startedAt
	}
}

// build reads the next batch after the saved position.
func (s *auditShipper) build(ctx context.Context, full bool) (auditPass, error) {
	next := auditState{Version: auditStateVersion, Position: s.saved.Position, Gaps: s.saved.Gaps}
	var entries []*audit.Entry
	var size int64
	filled := false
	for {
		if err := ctx.Err(); err != nil {
			return auditPass{}, err
		}
		chunk, err := s.feed.Read(ctx, audit.FeedQuery{
			Position: next.Position,
			Now:      s.now(),
			Full:     full,
			Keep:     s.sends,
			Limit:    auditBatchEntries - len(entries),
		})
		if err != nil {
			return auditPass{}, err
		}
		entries = append(entries, chunk.Entries...)
		size += chunk.Bytes
		next.Gaps = mergeLosses(next.Gaps, chunk.Losses)
		next.Position = chunk.Position
		if !chunk.More {
			break
		}
		if len(entries) >= auditBatchEntries || size >= auditBatchBytes {
			filled = true
			break
		}
	}

	batch := auditBatch{Entries: entries}
	// A request carries one gap; the others wait in the state.
	if len(next.Gaps) > 0 {
		gap := next.Gaps[0]
		batch.Gap = &gap
		next.Gaps = next.Gaps[1:]
	}
	return auditPass{batch: batch, next: next, more: filled || len(next.Gaps) > 0}, nil
}

// sends reports whether the level sends entry. Only masked entries go. Of an
// MCP call only its result goes, and at changes a successful read does not.
func (s *auditShipper) sends(entry *audit.Entry) bool {
	if !entry.Masked {
		return false
	}
	if entry.Category == audit.CategoryMCP && (entry.Result == "received" || entry.Result == "started") {
		return false
	}
	return s.level == auditAll || !isSuccessfulRead(entry)
}

// isSuccessfulRead reports an MCP resource read or read-only tool call that
// succeeded. A tool name masked on disk does not match, so its entry is sent.
func isSuccessfulRead(entry *audit.Entry) bool {
	switch entry.Action {
	case "mcp.resource.read.succeeded":
		return true
	case "mcp.tool_call.succeeded":
		return slices.Contains(auditReadTools, entry.MCPTool)
	}
	return false
}

// mergeLosses returns gaps with each loss added to the gap of its reason,
// whose range widens to cover it.
func mergeLosses(gaps []auditGap, losses []audit.FeedLoss) []auditGap {
	gaps = slices.Clone(gaps)
	for _, loss := range losses {
		i := slices.IndexFunc(gaps, func(gap auditGap) bool { return gap.Reason == loss.Reason })
		if i < 0 {
			gaps = append(gaps, newAuditGap(loss.Reason, loss.From, loss.To))
			continue
		}
		from, to := gaps[i].From, gaps[i].To
		if loss.From.Before(from) {
			from = loss.From
		}
		if loss.To.After(to) {
			to = loss.To
		}
		gaps[i] = newAuditGap(loss.Reason, from, to)
	}
	return gaps
}

func newAuditGap(reason string, from, to time.Time) auditGap {
	from, to = from.UTC(), to.UTC()
	name := reason + "\x00" + from.Format(time.RFC3339Nano) + "\x00" + to.Format(time.RFC3339Nano)
	return auditGap{
		ID:     uuid.NewSHA1(auditGapNamespace, []byte(name)).String(),
		Reason: reason,
		From:   from,
		To:     to,
	}
}

// hold takes or keeps the lease, and renews it every auditHeartbeatInterval.
func (s *auditShipper) hold(ctx context.Context) bool {
	if s.held {
		if s.now().Sub(s.heartbeatAt) < auditHeartbeatInterval {
			return true
		}
		if err := s.lease.Heartbeat(ctx); err != nil {
			s.warn(ctx, warnAuditLeaseLost, err)
			// Another process holds the lease now; Unlock releases only what
			// this process still holds.
			_ = s.lease.Unlock()
			s.drop()
			return false
		}
		s.heartbeatAt = s.now()
		return true
	}

	err := s.lease.TryLock()
	switch {
	case err == nil:
		s.held, s.standby, s.heartbeatAt = true, false, s.now()
		s.recovered(warnAuditLease, warnAuditLeaseLost)
		logger.Info(ctx, "This process sends audit entries to Dagu Console")
		return true
	case errors.Is(err, dirlock.ErrLockConflict):
		if !s.standby {
			s.standby = true
			logger.Info(ctx, "Another process sends audit entries to Dagu Console")
		}
	default:
		s.warn(ctx, warnAuditLease, err)
	}
	return false
}

// load reads the saved state once per hold of the lease. Without one, or
// with one it cannot read, the shipper starts at the oldest entry on disk,
// and saves that start before it reads, so files removed after it count as
// lost even while the first batch is not stored.
func (s *auditShipper) load(ctx context.Context) bool {
	if s.loaded {
		return true
	}
	data, found, err := s.state.Load(ctx)
	if err != nil {
		s.warn(ctx, warnAuditLoad, err)
		return false
	}
	s.recovered(warnAuditLoad)
	state := auditState{Version: auditStateVersion}
	if found {
		if err := json.Unmarshal(data, &state); err != nil || state.Version != auditStateVersion {
			if err == nil {
				err = fmt.Errorf("unknown version %d", state.Version)
			}
			logger.Warn(ctx, warnAuditState, tag.Error(err))
			state = auditState{Version: auditStateVersion}
		}
	}
	if len(state.Position) == 0 {
		chunk, err := s.feed.Read(ctx, audit.FeedQuery{Now: s.now(), Full: true})
		if err != nil {
			s.warn(ctx, warnAuditRead, err)
			return false
		}
		state.Position = chunk.Position
		if !s.save(ctx, state) {
			return false
		}
	}
	s.saved = state
	s.loaded = true
	return true
}

// save stores state as the shipper's position, while it holds the lease.
func (s *auditShipper) save(ctx context.Context, state auditState) bool {
	s.heartbeatAt = time.Time{}
	if !s.hold(ctx) {
		return false
	}
	data, err := json.Marshal(state)
	if err == nil {
		err = s.state.Save(ctx, data)
	}
	if err != nil {
		s.warn(ctx, warnAuditSave, err)
		return false
	}
	s.recovered(warnAuditSave)
	s.saved = state
	return true
}

// release gives up the lease when the shipper stops.
func (s *auditShipper) release() {
	if s.held {
		_ = s.lease.Unlock()
	}
	s.drop()
}

func (s *auditShipper) drop() {
	s.held, s.loaded = false, false
	s.saved, s.pending, s.scannedAt = auditState{}, nil, time.Time{}
}

// warn logs a failure unless it repeats the previous one.
func (s *auditShipper) warn(ctx context.Context, msg string, err error) {
	if s.warning == msg {
		return
	}
	s.warning = msg
	logger.Warn(ctx, msg, tag.Error(err))
}

// recovered lets the failures in msgs be logged again.
func (s *auditShipper) recovered(msgs ...string) {
	if slices.Contains(msgs, s.warning) {
		s.warning = ""
	}
}
