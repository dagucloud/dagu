// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"net/http"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	fileaudit "github.com/dagucloud/dagu/v2/internal/persis/file/audit"
	filemonitor "github.com/dagucloud/dagu/v2/internal/persis/file/monitor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeCommands appends n terminal command entries.
func writeCommands(t *testing.T, d *auditDir, n int) {
	t.Helper()
	for range n {
		d.write(t, testClockStart, audit.NewEntry(audit.CategoryTerminal, "command", "u1", "ops"))
	}
}

// sentIDs returns the distinct entry IDs the requests carried, and the gaps.
func sentIDs(requests []auditReceived) (map[string]bool, []*auditGap) {
	ids := map[string]bool{}
	var gaps []*auditGap
	for _, request := range requests {
		for _, id := range request.ids() {
			ids[id] = true
		}
		if request.body.Gap != nil {
			gaps = append(gaps, request.body.Gap)
		}
	}
	return ids, gaps
}

// drainFor drains s, sending batches for send and ending by end from now, and
// returns when the drain must end.
func drainFor(s *AuditShipper, send, end time.Duration) time.Time {
	now := time.Now()
	s.Drain(now.Add(send), now.Add(end))
	return now.Add(end)
}

// A stopping server with a backlog and a reachable Console sends all of it
// before it exits (RFC 021 Verification 9).
func TestAuditDrainSendsTheBacklog(t *testing.T) {
	d := newAuditDir(t)
	console := newAuditFakeConsole(t)
	s, _ := startSendingShipper(t, d, console)
	writeCommands(t, d, 8000)

	drainFor(s, 22*time.Second, 25*time.Second)

	ids, gaps := sentIDs(console.requests())
	assert.Len(t, ids, 8000)
	assert.Empty(t, gaps)
}

// Draining is the last chance to send, so the backoff after a failure gives
// way to it.
func TestAuditDrainRetriesAfterAFailure(t *testing.T) {
	d := newAuditDir(t)
	console := newAuditFakeConsole(t)
	failed := false
	console.answer = func(auditReceived) (int, string) {
		if !failed {
			failed = true
			return http.StatusInternalServerError, ""
		}
		return http.StatusOK, ""
	}
	writeCommands(t, d, 1)
	s, _ := startSendingShipper(t, d, console)
	require.Len(t, console.requests(), 1)

	drainFor(s, 22*time.Second, 25*time.Second)

	ids, _ := sentIDs(console.requests())
	assert.Len(t, ids, 1)
}

// A hold Console asks for that ends before the send time is waited out, and
// the entries go.
func TestAuditDrainWaitsOutAShortHold(t *testing.T) {
	d := newAuditDir(t)
	console := newAuditFakeConsole(t)
	held := false
	console.answer = func(auditReceived) (int, string) {
		if !held {
			held = true
			return http.StatusTooManyRequests, "1"
		}
		return http.StatusOK, ""
	}
	writeCommands(t, d, 3)
	s, _ := startSendingShipper(t, d, console)
	require.Len(t, console.requests(), 1)

	drainFor(s, 22*time.Second, 25*time.Second)

	ids, gaps := sentIDs(console.requests())
	assert.Len(t, ids, 3)
	assert.Empty(t, gaps)
}

// With Console unreachable, a stopping server still ends its drain in time
// (RFC 021 Verification 11).
func TestAuditDrainEndsInTime(t *testing.T) {
	d := newAuditDir(t)
	console := newAuditFakeConsole(t)
	s, _ := startSendingShipper(t, d, console)
	writeCommands(t, d, 1)
	console.unreachable.Store(true)

	end := drainFor(s, time.Second, 4*time.Second)

	assert.WithinDuration(t, end, time.Now(), time.Second)
}

// A batch already in flight when the drain starts ends with the drain's send
// time, not the request timeout, so the drain still ends in time.
func TestAuditDrainEndsABatchInFlight(t *testing.T) {
	d := newAuditDir(t)
	writeCommands(t, d, 1)
	console := newAuditFakeConsole(t)
	console.unreachable.Store(true)
	s := newAuditShipper(fileaudit.NewFeed(d.dir), d.state, filemonitor.NewLease(d.stateFile, nil),
		config.AuditAll, newAuditSender(noSecrets{}).send)
	s.credentials, s.licensed = console.credentials, func() bool { return true }
	clock := &fakeClock{t: t, waits: make(chan time.Duration, 16), fire: make(chan time.Time), at: testClockStart}
	s.after, s.now = clock.after, clock.now
	s.start(t.Context())
	t.Cleanup(s.Stop)
	require.Eventually(t, func() bool { return console.hanging.Load() > 0 }, testTimeout, 10*time.Millisecond)

	end := drainFor(s, time.Second, 4*time.Second)

	assert.WithinDuration(t, end, time.Now(), time.Second)
}

// What a stopping server could not send goes as one stopped gap, which is not
// saved: the entries stay on disk and go after the next start.
func TestAuditDrainReportsWhatIsLeft(t *testing.T) {
	d := newAuditDir(t)
	console := newAuditFakeConsole(t)
	s, _ := startSendingShipper(t, d, console)
	first := d.write(t, testClockStart.Add(time.Second), audit.NewEntry(audit.CategoryTerminal, "command", "u1", "ops"))
	writeCommands(t, d, 1)

	// No time is left to send.
	drainFor(s, 0, 3*time.Second)

	ids, gaps := sentIDs(console.requests())
	assert.Empty(t, ids)
	require.Len(t, gaps, 1)
	assert.Equal(t, auditGapStopped, gaps[0].Reason)
	assert.Equal(t, first.Timestamp, gaps[0].From)

	startSendingShipper(t, d, console)
	ids, _ = sentIDs(console.requests())
	assert.Len(t, ids, 2)
}

// A stopping server over Console's quota keeps its entries, sends no gap
// against Console's wish, and the entries go after the next start (RFC 021
// Verification 10).
func TestAuditDrainKeepsEntriesOverQuota(t *testing.T) {
	d := newAuditDir(t)
	console := newAuditFakeConsole(t)
	overQuota := true
	console.answer = func(auditReceived) (int, string) {
		if overQuota {
			return http.StatusTooManyRequests, "3600"
		}
		return http.StatusOK, ""
	}
	writeCommands(t, d, 3)
	s, _ := startSendingShipper(t, d, console)
	require.Len(t, console.requests(), 1)

	started := time.Now()
	drainFor(s, 22*time.Second, 25*time.Second)

	assert.Less(t, time.Since(started), time.Second)
	assert.Empty(t, console.requests())
	overQuota = false
	startSendingShipper(t, d, console)
	ids, _ := sentIDs(console.requests())
	assert.Len(t, ids, 3)
}
