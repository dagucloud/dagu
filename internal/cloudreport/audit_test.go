// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/license"
	fileaudit "github.com/dagucloud/dagu/v2/internal/persis/file/audit"
	filemonitor "github.com/dagucloud/dagu/v2/internal/persis/file/monitor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noSecrets is a secret source without values, so the store marks every
// entry masked.
type noSecrets struct{}

func (noSecrets) CurrentValues(context.Context) ([]string, error) { return nil, nil }
func (noSecrets) Revision() uint64                                { return 0 }

// auditDir is a server's audit files and the shipper's state file.
type auditDir struct {
	dir       string
	stateFile string
	store     *fileaudit.Store
	state     StateStore
}

func newAuditDir(t *testing.T) *auditDir {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "audit")
	store, err := fileaudit.New(dir, 0, fileaudit.WithMasker(audit.NewMasker(noSecrets{})))
	require.NoError(t, err)
	stateFile := filepath.Join(root, "cloud", "audit-state.json")
	return &auditDir{dir: dir, stateFile: stateFile, store: store, state: filemonitor.NewStateStore(stateFile)}
}

// write appends an entry stamped at.
func (d *auditDir) write(t *testing.T, at time.Time, entry *audit.Entry) *audit.Entry {
	t.Helper()
	entry.Timestamp = at
	require.NoError(t, d.store.Append(context.Background(), entry))
	return entry
}

// writeCall appends the entries of one MCP tool call that succeeded.
func (d *auditDir) writeCall(t *testing.T, at time.Time, tool string) {
	t.Helper()
	for _, result := range []string{"received", "started", "succeeded"} {
		entry := audit.NewEntry(audit.CategoryMCP, "mcp.tool_call."+result, "u1", "ops")
		entry.Result, entry.MCPTool = result, tool
		d.write(t, at, entry)
	}
}

// errConsoleBusy is Console asking to send again on the next pass.
var errConsoleBusy = &auditRefusal{msg: "Dagu Console is busy", wait: auditCheckInterval}

// auditConsole records the batches a shipper sends, and fails a send while
// fail holds errors.
type auditConsole struct {
	batches chan auditBatch
	fail    chan error
}

func newAuditConsole() *auditConsole {
	return &auditConsole{batches: make(chan auditBatch, 16), fail: make(chan error, 4)}
}

func (c *auditConsole) send(_ context.Context, _ license.CloudCredentials, batch auditBatch) error {
	c.batches <- batch
	select {
	case err := <-c.fail:
		return err
	default:
		return nil
	}
}

// sent returns the batches sent so far.
func (c *auditConsole) sent() []auditBatch {
	var batches []auditBatch
	for {
		select {
		case batch := <-c.batches:
			batches = append(batches, batch)
		default:
			return batches
		}
	}
}

// startShipper runs a shipper over d at level with a clock the test controls,
// and returns the clock after the first pass.
func startShipper(t *testing.T, d *auditDir, level config.AuditLevel, console *auditConsole) *fakeClock {
	t.Helper()
	_, clock := runShipper(t, d, level, console)
	return clock
}

// testCredentials are a license connected to Dagu Console that includes
// audit logs.
func testCredentials() (license.CloudCredentials, bool) {
	return license.CloudCredentials{CloudURL: "http://console.invalid", LicenseID: "lic-1", ServerID: "srv-1", HeartbeatSecret: "hb-secret"}, true
}

// newTestShipper is a shipper connected with testCredentials.
func newTestShipper(
	feed AuditFeed,
	state StateStore,
	lease Lease,
	level config.AuditLevel,
	send func(context.Context, license.CloudCredentials, auditBatch) error,
) *auditShipper {
	s := newAuditShipper(feed, state, lease, level, send)
	s.credentials, s.licensed = testCredentials, func() bool { return true }
	return s
}

// runShipper is startShipper that also returns the shipper.
func runShipper(t *testing.T, d *auditDir, level config.AuditLevel, console *auditConsole) (*auditShipper, *fakeClock) {
	t.Helper()
	s := newTestShipper(fileaudit.NewFeed(d.dir), d.state, filemonitor.NewLease(d.stateFile, nil), level, console.send)
	clock := &fakeClock{t: t, waits: make(chan time.Duration, 16), fire: make(chan time.Time), at: testClockStart}
	s.after, s.now = clock.after, clock.now
	s.start(t.Context())
	t.Cleanup(s.stop)
	require.Equal(t, auditCheckInterval, clock.wait())
	return s, clock
}

// countingStore counts the saves of a StateStore.
type countingStore struct {
	StateStore
	saves atomic.Int32
}

func (s *countingStore) Save(ctx context.Context, data []byte) error {
	s.saves.Add(1)
	return s.StateStore.Save(ctx, data)
}

func sentEntries(batches []auditBatch) []*audit.Entry {
	var entries []*audit.Entry
	for _, batch := range batches {
		entries = append(entries, batch.Entries...)
	}
	return entries
}

// 100 MCP calls, 80 reads and 20 changes, write 300 local entries. Of each
// call only the result goes, and at changes no successful read.
func TestAuditShipperLevels(t *testing.T) {
	for level, want := range map[config.AuditLevel]int{config.AuditChanges: 20, config.AuditAll: 100} {
		t.Run(string(level), func(t *testing.T) {
			d := newAuditDir(t)
			for i := range 100 {
				tool := "dagu_read"
				if i%5 == 0 {
					tool = "dagu_change"
				}
				d.writeCall(t, testClockStart, tool)
			}
			console := newAuditConsole()

			startShipper(t, d, level, console)

			entries := sentEntries(console.sent())
			require.Len(t, entries, want)
			for _, entry := range entries {
				assert.Equal(t, "mcp.tool_call.succeeded", entry.Action)
			}
		})
	}
}

func TestAuditShipperSendsWhatTheLevelSends(t *testing.T) {
	entry := func(category audit.Category, action, result, tool string) *audit.Entry {
		return &audit.Entry{Category: category, Action: action, Result: result, MCPTool: tool, Masked: true}
	}
	for _, tc := range []struct {
		name    string
		entry   *audit.Entry
		changes bool
		all     bool
	}{
		{"read tool succeeded", entry(audit.CategoryMCP, "mcp.tool_call.succeeded", "succeeded", "dagu_read"), false, true},
		{"read tool failed", entry(audit.CategoryMCP, "mcp.tool_call.failed", "failed", "dagu_read"), true, true},
		{"read tool denied", entry(audit.CategoryMCP, "mcp.tool_call.denied", "denied", "dagu_read"), true, true},
		{"resource read succeeded", entry(audit.CategoryMCP, "mcp.resource.read.succeeded", "succeeded", ""), false, true},
		{"resource read received", entry(audit.CategoryMCP, "mcp.resource.read.received", "received", ""), false, false},
		{"change tool started", entry(audit.CategoryMCP, "mcp.tool_call.started", "started", "dagu_change"), false, false},
		{"change tool succeeded", entry(audit.CategoryMCP, "mcp.tool_call.succeeded", "succeeded", "dagu_change"), true, true},
		// A secret whose value is "read" masks the tool's name on disk.
		{"read tool with a masked name", entry(audit.CategoryMCP, "mcp.tool_call.succeeded", "succeeded", "dagu_*******"), true, true},
		{"terminal command", entry(audit.CategoryTerminal, "command", "", ""), true, true},
		{"cleanup", entry(audit.CategorySystem, "audit_cleanup", "", ""), true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.changes, (&auditShipper{level: config.AuditChanges}).sends(tc.entry), "changes")
			assert.Equal(t, tc.all, (&auditShipper{level: config.AuditAll}).sends(tc.entry), "all")
		})
	}
}

// Entries written before masking at write, or while the secret values failed
// to load, lack the marker and are never sent.
func TestAuditShipperSendsOnlyMaskedEntries(t *testing.T) {
	d := newAuditDir(t)
	unmarked, err := fileaudit.New(d.dir, 0)
	require.NoError(t, err)
	old := audit.NewEntry(audit.CategoryTerminal, "command", "u1", "ops")
	old.Timestamp = testClockStart
	require.NoError(t, unmarked.Append(context.Background(), old))
	masked := d.write(t, testClockStart, audit.NewEntry(audit.CategoryTerminal, "command", "u1", "ops"))
	console := newAuditConsole()

	startShipper(t, d, config.AuditAll, console)

	entries := sentEntries(console.sent())
	require.Len(t, entries, 1)
	assert.Equal(t, masked.ID, entries[0].ID)
}

// Entries the level skips move the saved position without a send, and a
// server without new entries writes nothing.
func TestAuditShipperIdleWritesNothing(t *testing.T) {
	d := newAuditDir(t)
	d.writeCall(t, testClockStart, "dagu_read")
	state := &countingStore{StateStore: d.state}
	d.state = state
	console := newAuditConsole()

	clock := startShipper(t, d, config.AuditChanges, console)
	assert.Empty(t, console.sent())
	saves := state.saves.Load()
	require.Positive(t, saves)

	clock.next()
	clock.shift(auditScanInterval)
	clock.next()
	assert.Empty(t, console.sent())
	assert.Equal(t, saves, state.saves.Load())
}

// After a full batch, the next one goes at once.
func TestAuditShipperSendsFullBatchesAtOnce(t *testing.T) {
	d := newAuditDir(t)
	for range 1500 {
		d.write(t, testClockStart, audit.NewEntry(audit.CategoryTerminal, "command", "u1", "ops"))
	}
	console := newAuditConsole()

	startShipper(t, d, config.AuditAll, console)

	batches := console.sent()
	require.Len(t, batches, 2)
	assert.Len(t, batches[0].Entries, auditBatchEntries)
	assert.Len(t, batches[1].Entries, 500)
}

// countingFeed counts the reads of a feed.
type countingFeed struct {
	AuditFeed
	reads atomic.Int32
}

func (f *countingFeed) Read(ctx context.Context, q audit.FeedQuery) (audit.FeedChunk, error) {
	f.reads.Add(1)
	return f.AuditFeed.Read(ctx, q)
}

// A batch Console did not store goes again as built: a line past the read
// budget behind it is not read again while Console fails.
func TestAuditShipperResendsWithoutReadingAgain(t *testing.T) {
	d := newAuditDir(t)
	entry := d.write(t, testClockStart, audit.NewEntry(audit.CategoryTerminal, "command", "u1", "ops"))
	appendRaw(t, d, strings.Repeat("x", 10<<20))
	console := newAuditConsole()
	console.fail <- errConsoleBusy
	console.fail <- errConsoleBusy
	feed := &countingFeed{AuditFeed: fileaudit.NewFeed(d.dir)}
	s := newTestShipper(feed, d.state, filemonitor.NewLease(d.stateFile, nil), config.AuditAll, console.send)
	clock := &fakeClock{t: t, waits: make(chan time.Duration, 16), fire: make(chan time.Time), at: testClockStart}
	s.after, s.now = clock.after, clock.now
	s.start(t.Context())
	t.Cleanup(s.stop)
	clock.wait()
	require.Len(t, sentEntries(console.sent()), 1)
	reads := feed.reads.Load()

	clock.next()
	resent := sentEntries(console.sent())
	require.Len(t, resent, 1)
	assert.Equal(t, entry.ID, resent[0].ID)
	assert.Equal(t, reads, feed.reads.Load(), "a failed batch goes again without a read")

	clock.next()
	require.Len(t, sentEntries(console.sent()), 1)
	clock.next()
	assert.Empty(t, console.sent())
}

// A batch Console did not store moves nothing, and goes again on the next
// pass.
func TestAuditShipperResendsWhatConsoleDidNotStore(t *testing.T) {
	d := newAuditDir(t)
	entry := d.write(t, testClockStart, audit.NewEntry(audit.CategoryTerminal, "command", "u1", "ops"))
	console := newAuditConsole()
	console.fail <- errConsoleBusy

	clock := startShipper(t, d, config.AuditAll, console)
	require.Len(t, sentEntries(console.sent()), 1)

	clock.next()
	resent := sentEntries(console.sent())
	require.Len(t, resent, 1)
	assert.Equal(t, entry.ID, resent[0].ID)

	clock.next()
	assert.Empty(t, console.sent())
}

// The start is saved before the first batch, so a file removed before that
// batch is stored counts as lost, even after a restart.
func TestAuditShipperKeepsItsStartWhenTheFirstSendFails(t *testing.T) {
	d := newAuditDir(t)
	yesterday := testClockStart.AddDate(0, 0, -1)
	d.write(t, yesterday, audit.NewEntry(audit.CategoryTerminal, "command", "u1", "ops"))
	console := newAuditConsole()
	console.fail <- errConsoleBusy
	first, _ := runShipper(t, d, config.AuditAll, console)
	require.Len(t, sentEntries(console.sent()), 1)
	first.stop()

	require.NoError(t, os.Remove(filepath.Join(d.dir, yesterday.Format("2006-01-02")+".jsonl")))
	startShipper(t, d, config.AuditAll, console)

	batches := console.sent()
	require.Len(t, batches, 1)
	assert.Empty(t, batches[0].Entries)
	require.NotNil(t, batches[0].Gap)
	assert.Equal(t, audit.LossDeleted, batches[0].Gap.Reason)
}

// A check of every file whose batch Console did not store runs again on the
// next pass, not a minute later.
func TestAuditShipperRepeatsAFailedFullCheck(t *testing.T) {
	d := newAuditDir(t)
	console := newAuditConsole()
	clock := startShipper(t, d, config.AuditAll, console)

	late := d.write(t, testClockStart.AddDate(0, 0, -1), audit.NewEntry(audit.CategoryTerminal, "command", "u1", "ops"))
	console.fail <- errConsoleBusy
	clock.shift(auditScanInterval)
	clock.next()
	require.Len(t, sentEntries(console.sent()), 1)

	clock.next()
	entries := sentEntries(console.sent())
	require.Len(t, entries, 1)
	assert.Equal(t, late.ID, entries[0].ID)
}

// Large entries end a batch before 1,000, and the rest go in the next one at
// once.
func TestAuditShipperBoundsBatchBytes(t *testing.T) {
	d := newAuditDir(t)
	for range 10 {
		entry := audit.NewEntry(audit.CategoryTerminal, "command", "u1", "ops")
		entry.Details = `{"command":"` + strings.Repeat("x", 600<<10) + `"}`
		d.write(t, testClockStart, entry)
	}
	console := newAuditConsole()

	startShipper(t, d, config.AuditAll, console)

	batches := console.sent()
	require.Greater(t, len(batches), 1)
	assert.Len(t, sentEntries(batches), 10)
}

// endlessFeed always has more to read and never an entry to send, as a long
// backlog the level skips. reading closes once the shipper reads on from a
// read that returned More.
type endlessFeed struct {
	reads   atomic.Int32
	once    sync.Once
	reading chan struct{}
}

func (f *endlessFeed) Read(context.Context, audit.FeedQuery) (audit.FeedChunk, error) {
	if f.reads.Add(1) > 2 {
		f.once.Do(func() { close(f.reading) })
	}
	return audit.FeedChunk{Position: []byte(`{}`), More: true}, nil
}

// Stopping does not wait for the shipper to read through a backlog.
func TestAuditShipperStopsDuringALongRead(t *testing.T) {
	d := newAuditDir(t)
	feed := &endlessFeed{reading: make(chan struct{})}
	s := newTestShipper(feed, d.state, filemonitor.NewLease(d.stateFile, nil), config.AuditChanges, newAuditConsole().send)
	s.start(t.Context())
	select {
	case <-feed.reading:
	case <-time.After(testTimeout):
		t.Fatal("the shipper did not read")
	}

	stopped := make(chan struct{})
	go func() {
		s.stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(testTimeout):
		t.Fatal("the shipper did not stop")
	}
}

// An entry stamped just before midnight lands in yesterday's file after the
// day changed; the check of every file, once a minute, finds it.
func TestAuditShipperFindsLateEntriesWithinAMinute(t *testing.T) {
	d := newAuditDir(t)
	console := newAuditConsole()
	clock := startShipper(t, d, config.AuditAll, console)

	late := d.write(t, testClockStart.AddDate(0, 0, -1), audit.NewEntry(audit.CategoryTerminal, "command", "u1", "ops"))
	clock.shift(auditScanInterval - 2*auditCheckInterval)
	clock.next()
	assert.Empty(t, console.sent(), "today's check leaves older files alone")

	clock.next()
	entries := sentEntries(console.sent())
	require.Len(t, entries, 1)
	assert.Equal(t, late.ID, entries[0].ID)
}

// A request carries one gap: losses of two reasons go in two batches, and
// losses of one reason merge into one range.
func TestAuditShipperSendsOneGapPerBatch(t *testing.T) {
	d := newAuditDir(t)
	yesterday := testClockStart.AddDate(0, 0, -1)
	d.write(t, yesterday, audit.NewEntry(audit.CategoryTerminal, "command", "u1", "ops"))
	console := newAuditConsole()
	clock := startShipper(t, d, config.AuditAll, console)
	require.Len(t, console.sent(), 1)

	require.NoError(t, os.Remove(filepath.Join(d.dir, yesterday.Format("2006-01-02")+".jsonl")))
	before := d.write(t, testClockStart, audit.NewEntry(audit.CategoryTerminal, "command", "u1", "ops"))
	appendRaw(t, d, "garbled\n")
	appendRaw(t, d, "garbled\n")
	after := d.write(t, testClockStart.Add(time.Second), audit.NewEntry(audit.CategoryTerminal, "command", "u1", "ops"))
	clock.shift(auditScanInterval)
	clock.next()

	batches := console.sent()
	require.Len(t, batches, 2)
	assert.Len(t, batches[0].Entries, 2)
	var reasons []string
	for _, batch := range batches {
		require.NotNil(t, batch.Gap)
		reasons = append(reasons, batch.Gap.Reason)
		if batch.Gap.Reason == audit.LossRejected {
			assert.Equal(t, before.Timestamp, batch.Gap.From)
			assert.Equal(t, after.Timestamp, batch.Gap.To)
		}
	}
	assert.ElementsMatch(t, []string{audit.LossDeleted, audit.LossRejected}, reasons)
}

func appendRaw(t *testing.T, d *auditDir, text string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(d.dir, testClockStart.Format("2006-01-02")+".jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString(text)
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

// A gap resent after an answer was lost keeps its ID, so Console stores it
// once; widening its range gives it a new one.
func TestAuditGapIDDerivesFromReasonAndRange(t *testing.T) {
	from, to := testClockStart, testClockStart.Add(time.Hour)
	gap := mergeLosses(nil, []audit.FeedLoss{{Reason: audit.LossRejected, From: from, To: to}})
	again := mergeLosses(nil, []audit.FeedLoss{{Reason: audit.LossRejected, From: from, To: to}})
	require.Len(t, gap, 1)
	assert.Equal(t, gap[0].ID, again[0].ID)

	wider := mergeLosses(gap, []audit.FeedLoss{{Reason: audit.LossRejected, From: to, To: to.Add(time.Hour)}})
	require.Len(t, wider, 1)
	assert.Equal(t, from, wider[0].From)
	assert.Equal(t, to.Add(time.Hour), wider[0].To)
	assert.NotEqual(t, gap[0].ID, wider[0].ID)
}

// One process per installation sends; another takes over when it stops, and
// goes on from what Console stored.
func TestAuditShipperLease(t *testing.T) {
	d := newAuditDir(t)
	first := d.write(t, testClockStart, audit.NewEntry(audit.CategoryTerminal, "command", "u1", "ops"))
	holderConsole := newAuditConsole()
	holder, _ := runShipper(t, d, config.AuditAll, holderConsole)
	sent := sentEntries(holderConsole.sent())
	require.Len(t, sent, 1)
	assert.Equal(t, first.ID, sent[0].ID)

	standbyConsole := newAuditConsole()
	standby := startShipper(t, d, config.AuditAll, standbyConsole)
	assert.Empty(t, standbyConsole.sent())

	holder.stop()
	second := d.write(t, testClockStart, audit.NewEntry(audit.CategoryTerminal, "command", "u1", "ops"))
	standby.next()
	sent = sentEntries(standbyConsole.sent())
	require.Len(t, sent, 1)
	assert.Equal(t, second.ID, sent[0].ID)
}
