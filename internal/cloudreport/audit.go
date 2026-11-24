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
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/license"
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
	// auditGapStopped is the reason of the gap for entries a stopping server
	// could not send.
	auditGapStopped = "stopped"
)

const (
	warnAuditRead      = "Failed to read the audit entries to send to Dagu Console"
	warnAuditSend      = "Failed to send audit entries to Dagu Console"
	warnAuditSave      = "Failed to save which audit entries Dagu Console has stored"
	warnAuditLoad      = "Failed to read the audit shipping state; trying again"
	warnAuditState     = "Unreadable audit shipping state; sending the audit entries on disk from the oldest"
	warnAuditLease     = "Failed to take the lease to send audit entries to Dagu Console"
	warnAuditLeaseLost = "Lost the lease to send audit entries to Dagu Console"
	warnAuditHeld      = "Holding audit entries for Dagu Console"
	warnAuditStopped   = "Failed to tell Dagu Console which audit entries this stopping server did not send"
)

// auditGapNamespace scopes the IDs derived for gaps.
var auditGapNamespace = uuid.MustParse("6f1d3c2e-8a4b-4c5d-9e7f-0a1b2c3d4e5f")

// auditReadTools are the MCP tools that only read, as their ReadOnlyHint
// annotation says.
var auditReadTools = []string{"dagu_read"}

// AuditFeed reads the entries an audit store holds after a position.
type AuditFeed interface {
	Read(ctx context.Context, q audit.FeedQuery) (audit.FeedChunk, error)
	// Unsent returns how many stored bytes lie past position.
	Unsent(ctx context.Context, position []byte) (int64, error)
}

// AuditOptions configures sending a server's audit entries to Dagu Console.
type AuditOptions struct {
	// Level is which entries go.
	Level config.AuditLevel
	Feed  AuditFeed
	// State keeps the position Console has stored up to, and Lease lets one
	// process per installation send.
	State StateStore
	Lease Lease
	// Credentials returns the license's credentials while the server is
	// connected to Dagu Console.
	Credentials func() (license.CloudCredentials, bool)
	// Licensed reports whether the license includes audit logs.
	Licensed func() bool
	// Secrets gives the values entries are masked with again before they
	// are sent.
	Secrets audit.SecretSource
}

// StartAudit sends the audit entries the feed holds to Dagu Console while the
// server is connected with a license that includes audit logs, until Drain or
// Stop; ctx only carries values.
func StartAudit(ctx context.Context, opts AuditOptions) *AuditShipper {
	s := newAuditShipper(opts.Feed, opts.State, opts.Lease, opts.Level, newAuditSender(opts.Secrets).send)
	s.credentials, s.licensed = opts.Credentials, opts.Licensed
	s.start(ctx)
	return s
}

// AuditShipper sends the audit entries a store holds, from the position its
// state saves, in batches. The process holding the lease sends; others stand
// by.
type AuditShipper struct {
	feed        AuditFeed
	state       StateStore
	lease       Lease
	level       config.AuditLevel
	credentials func() (license.CloudCredentials, bool)
	licensed    func() bool
	// send delivers a batch and returns nil once Console has stored it, or
	// an *auditRefusal when Console holds sending.
	send  func(context.Context, license.CloudCredentials, auditBatch) error
	after func(time.Duration) <-chan time.Time
	now   func() time.Time

	// base carries ctx's values without its cancellation; the loop ends only
	// on cancel, which Stop calls, or after a drain.
	base    context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	drainAt chan auditDrain

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
	// heldUntil holds sending until then, as Console asked or after a
	// failure; refusedBy holds it while the license is the one Console
	// refused.
	heldUntil time.Time
	refusedBy *auditCredentials
	backoff   time.Duration
	// consoleHeld reports that Console asked for heldUntil, rather than a
	// backoff after a failure.
	consoleHeld bool
	// waiting reports that the server lacks the connection or license to
	// send, as last logged.
	waiting bool
}

// auditCredentials identifies the license and server Console refused.
type auditCredentials struct {
	licenseID, serverID, secret string
}

func credentialsOf(creds license.CloudCredentials) auditCredentials {
	return auditCredentials{licenseID: creds.LicenseID, serverID: creds.ServerID, secret: creds.HeartbeatSecret}
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

// auditDrain bounds a drain: batches go until sendUntil, and a stopped gap
// for what is left until endBy.
type auditDrain struct {
	sendUntil, endBy time.Time
}

// auditOutcome is how a pass ended.
type auditOutcome int

const (
	// auditBlocked: nothing could go: not connected, refused, held, failed,
	// standing by, or out of time.
	auditBlocked auditOutcome = iota
	// auditSent: batches went, and entries may have come since.
	auditSent
	// auditIdle: everything on disk is stored.
	auditIdle
)

// auditPass is a batch and the state to save once Console stores it.
type auditPass struct {
	batch auditBatch
	next  auditState
	// more reports that another batch goes at once: this one is full, or a
	// gap is left for another request.
	more bool
}

func newAuditShipper(
	feed AuditFeed,
	state StateStore,
	lease Lease,
	level config.AuditLevel,
	send func(context.Context, license.CloudCredentials, auditBatch) error,
) *AuditShipper {
	return &AuditShipper{
		feed:  feed,
		state: state,
		lease: lease,
		level: level,
		send:  send,
		after: time.After,
		now:   time.Now,
	}
}

func (s *AuditShipper) start(ctx context.Context) {
	s.base = context.WithoutCancel(ctx)
	var loop context.Context
	loop, s.cancel = context.WithCancel(s.base)
	s.done, s.drainAt = make(chan struct{}), make(chan auditDrain, 1)
	go s.run(loop)
}

// Stop ends shipping at once, and releases the lease.
func (s *AuditShipper) Stop() {
	s.cancel()
	<-s.done
}

// Drain sends what is left before the process stops: batches until
// sendUntil, then a stopped gap for what still is, by endBy. The entries stay
// on disk for the next start. It returns once done, and is called at most
// once, instead of Stop.
func (s *AuditShipper) Drain(sendUntil, endBy time.Time) {
	s.drainAt <- auditDrain{sendUntil: sendUntil, endBy: endBy}
	// A batch in flight when the drain is asked for ends with its send time.
	deadline := time.AfterFunc(time.Until(sendUntil), s.cancel)
	<-s.done
	deadline.Stop()
	s.cancel()
}

func (s *AuditShipper) run(ctx context.Context) {
	defer close(s.done)
	defer s.release()
	for {
		s.ship(ctx)
		select {
		case <-ctx.Done():
			// The drain's deadline also cancels ctx; the drain goes first.
			select {
			case d := <-s.drainAt:
				s.drain(d)
			default:
			}
			return
		case d := <-s.drainAt:
			s.drain(d)
			return
		case <-s.after(auditCheckInterval):
		}
	}
}

// drain sends batches back to back until everything is stored or nothing can
// go, starting with a check of every file, and then reports what is left as
// a stopped gap. A hold Console asked for is waited out when it ends in time.
func (s *AuditShipper) drain(d auditDrain) {
	logger.Info(s.base, "Sending the audit entries left to Dagu Console before stopping")
	sendCtx, cancel := context.WithDeadline(s.base, d.sendUntil)
	defer cancel()
	s.scannedAt = time.Time{}
	if !s.consoleHeld {
		// Draining is the last chance; a backoff after a failure gives way.
		s.heldUntil = time.Time{}
	}
	outcome := auditBlocked
	for sendCtx.Err() == nil {
		outcome = s.ship(sendCtx)
		if outcome == auditSent || outcome == auditBlocked && s.waitHold(sendCtx) {
			continue
		}
		break
	}
	if outcome == auditIdle {
		return
	}
	gapCtx, cancelGap := context.WithDeadline(s.base, d.endBy)
	defer cancelGap()
	s.reportStopped(gapCtx)
}

// waitHold waits out the hold Console asked for, and reports whether it did:
// only one that ends before ctx does.
func (s *AuditShipper) waitHold(ctx context.Context) bool {
	if !s.consoleHeld || s.refusedBy != nil {
		return false
	}
	wait := s.heldUntil.Sub(s.now())
	if deadline, ok := ctx.Deadline(); wait <= 0 || ok && time.Now().Add(wait).After(deadline) {
		return false
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		s.heldUntil = time.Time{}
		return true
	}
}

// reportStopped tells Console, best effort, that entries from the first one
// not stored until now may not arrive. It is not saved: if the disk survives,
// the entries go after the next start. It sends nothing while another process
// holds the lease, or while Console refuses this server or asked it to wait.
func (s *AuditShipper) reportStopped(ctx context.Context) {
	creds, ok := s.connected(ctx)
	if !ok || !s.held || s.consoleHeld && s.now().Before(s.heldUntil) {
		return
	}
	from, ok := s.firstUnsent(ctx)
	if !ok {
		return
	}
	gap := newAuditGap(auditGapStopped, from, s.now())
	if err := s.send(ctx, creds, auditBatch{Gap: &gap}); err != nil {
		s.warn(ctx, warnAuditStopped, err)
	}
}

// firstUnsent returns when the first entry the level sends and Console has
// not stored was written.
func (s *AuditShipper) firstUnsent(ctx context.Context) (time.Time, bool) {
	if s.pending != nil && len(s.pending.batch.Entries) > 0 {
		return s.pending.batch.Entries[0].Timestamp, true
	}
	if !s.loaded {
		return time.Time{}, false
	}
	return s.firstAfter(ctx, s.saved.Position)
}

// Backlog reports what waits to be sent, from the position saved last, so
// that a process standing by reports it too. It reads only file sizes while
// nothing waits.
func (s *AuditShipper) Backlog(ctx context.Context) (unsentBytes int64, oldestUnsent time.Time, ok bool) {
	data, found, err := s.state.Load(ctx)
	if err != nil {
		return 0, time.Time{}, false
	}
	var state auditState
	if found && (json.Unmarshal(data, &state) != nil || state.Version != auditStateVersion) {
		return 0, time.Time{}, false
	}
	unsent, err := s.feed.Unsent(ctx, state.Position)
	if err != nil {
		return 0, time.Time{}, false
	}
	if unsent > 0 {
		oldestUnsent, _ = s.firstAfter(ctx, state.Position)
	}
	return unsent, oldestUnsent, true
}

// firstAfter returns when the first entry the level sends after position was
// written, reading past the entries it skips.
func (s *AuditShipper) firstAfter(ctx context.Context, position []byte) (time.Time, bool) {
	for ctx.Err() == nil {
		chunk, err := s.feed.Read(ctx, audit.FeedQuery{Position: position, Now: s.now(), Full: true, Keep: s.sends, Limit: 1})
		if err != nil {
			return time.Time{}, false
		}
		if len(chunk.Entries) > 0 {
			return chunk.Entries[0].Timestamp, true
		}
		if !chunk.More {
			break
		}
		position = chunk.Position
	}
	return time.Time{}, false
}

// ship sends batches while a full one, or a gap left over, calls for another,
// and returns how it ended. Every audit file is checked on the first pass and
// once a minute after it; today's file on the others. A check of every file
// stays due until all it found is stored. A batch Console did not store goes
// again first, as built, so a failing Console costs no reads.
func (s *AuditShipper) ship(ctx context.Context) auditOutcome {
	creds, ok := s.connected(ctx)
	if !ok {
		// No file I/O, and the lease goes to a process that can send.
		s.release()
		return auditBlocked
	}
	if !s.hold(ctx) || s.now().Before(s.heldUntil) || !s.load(ctx) {
		return auditBlocked
	}
	startedAt := s.now()
	full := s.scannedAt.IsZero() || startedAt.Sub(s.scannedAt) >= auditScanInterval
	outcome := auditSent
	for first := true; ; first = false {
		// Each batch goes only while the server may still send, with the
		// credentials it has then.
		if !first {
			if creds, ok = s.connected(ctx); !ok {
				s.release()
				return auditBlocked
			}
		}
		pass, resend := s.pending, s.pending != nil
		if !resend {
			built, err := s.build(ctx, full)
			if ctx.Err() != nil {
				// Stopping: what was read but not sent is read again later.
				return auditBlocked
			}
			if err != nil {
				s.warn(ctx, warnAuditRead, err)
				return auditBlocked
			}
			s.recovered(warnAuditRead)
			pass = &built
		}
		if len(pass.batch.Entries) == 0 && pass.batch.Gap == nil {
			// Entries the level skips still move the position.
			if !bytes.Equal(pass.next.Position, s.saved.Position) && !s.save(ctx, pass.next) {
				return auditBlocked
			}
			outcome = auditIdle
			break
		}
		if err := s.send(ctx, creds, pass.batch); err != nil {
			s.pending = pass
			s.refused(ctx, creds, err)
			return auditBlocked
		}
		s.pending, s.backoff, s.consoleHeld = nil, 0, false
		s.recovered(warnAuditSend, warnAuditHeld)
		if !s.save(ctx, pass.next) {
			return auditBlocked
		}
		// After a batch sent again, this pass still reads what came since.
		if !pass.more && !resend {
			break
		}
	}
	if full {
		s.scannedAt = startedAt
	}
	return outcome
}

// connected returns the credentials to send with while the server is
// connected to Dagu Console with a license that includes audit logs, and
// Console has not refused that license.
func (s *AuditShipper) connected(ctx context.Context) (license.CloudCredentials, bool) {
	creds, ok := s.credentials()
	ok = ok && s.licensed()
	if ok && s.refusedBy != nil {
		if *s.refusedBy == credentialsOf(creds) {
			return creds, false
		}
		s.refusedBy = nil
		s.recovered(warnAuditHeld)
	}
	if ok == s.waiting {
		s.waiting = !ok
		if !ok {
			logger.Info(ctx, "Audit entries go to Dagu Console once this server connects with a license that includes audit logs")
		}
	}
	return creds, ok
}

// refused holds sending after a failed batch: as long as Console asked, until
// the license changes, or, after any other failure, for a backoff that grows
// from a minute to five.
func (s *AuditShipper) refused(ctx context.Context, creds license.CloudCredentials, err error) {
	var refusal *auditRefusal
	switch {
	case errors.As(err, &refusal) && refusal.until:
		by := credentialsOf(creds)
		s.refusedBy = &by
		s.warn(ctx, warnAuditHeld, err)
	case errors.As(err, &refusal):
		s.heldUntil, s.consoleHeld = s.now().Add(refusal.wait), true
		s.warn(ctx, warnAuditHeld, err)
	default:
		s.backoff = min(max(2*s.backoff, initialBackoff), maxBackoff)
		s.heldUntil, s.consoleHeld = s.now().Add(s.backoff), false
		s.warn(ctx, warnAuditSend, err)
	}
}

// build reads the next batch after the saved position.
func (s *AuditShipper) build(ctx context.Context, full bool) (auditPass, error) {
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
func (s *AuditShipper) sends(entry *audit.Entry) bool {
	if !entry.Masked {
		return false
	}
	if entry.Category == audit.CategoryMCP && (entry.Result == "received" || entry.Result == "started") {
		return false
	}
	return s.level == config.AuditAll || !isSuccessfulRead(entry)
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
func (s *AuditShipper) hold(ctx context.Context) bool {
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
func (s *AuditShipper) load(ctx context.Context) bool {
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
func (s *AuditShipper) save(ctx context.Context, state auditState) bool {
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
func (s *AuditShipper) release() {
	if s.held {
		_ = s.lease.Unlock()
	}
	s.drop()
}

func (s *AuditShipper) drop() {
	s.held, s.loaded = false, false
	s.saved, s.pending, s.scannedAt = auditState{}, nil, time.Time{}
}

// warn logs a failure unless it repeats the previous one.
func (s *AuditShipper) warn(ctx context.Context, msg string, err error) {
	if s.warning == msg {
		return
	}
	s.warning = msg
	logger.Warn(ctx, msg, tag.Error(err))
}

// recovered lets the failures in msgs be logged again.
func (s *AuditShipper) recovered(msgs ...string) {
	if slices.Contains(msgs, s.warning) {
		s.warning = ""
	}
}
