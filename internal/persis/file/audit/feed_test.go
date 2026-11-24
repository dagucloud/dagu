// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// feedNow is the current time of the feed tests that need no cleaner.
var feedNow = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

// feedLine encodes an entry stamped at ts as one audit file line.
func feedLine(t *testing.T, id string, ts time.Time) string {
	t.Helper()
	data, err := json.Marshal(audit.Entry{ID: id, Timestamp: ts, Category: audit.CategoryUser, Action: "login"})
	require.NoError(t, err)
	return string(data) + "\n"
}

// appendText appends text to the day file of day.
func appendText(t *testing.T, dir string, day time.Time, text string) {
	t.Helper()
	path := filepath.Join(dir, day.Format(dateFormat)+auditFileExtension)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, auditFilePermissions)
	require.NoError(t, err)
	_, err = f.WriteString(text)
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

// readFeed reads the feed after pos and returns the chunk.
func readFeed(t *testing.T, dir string, pos []byte, now time.Time, full bool) audit.FeedChunk {
	t.Helper()
	chunk, err := NewFeed(dir).Read(context.Background(), audit.FeedQuery{Position: pos, Now: now, Full: full, Limit: 1000})
	require.NoError(t, err)
	return chunk
}

func entryIDs(chunk audit.FeedChunk) []string {
	ids := make([]string, 0, len(chunk.Entries))
	for _, entry := range chunk.Entries {
		ids = append(ids, entry.ID)
	}
	return ids
}

func TestFeed_ResumesAfterPosition(t *testing.T) {
	dir := t.TempDir()
	appendText(t, dir, feedNow, feedLine(t, "a", feedNow)+feedLine(t, "b", feedNow))

	first := readFeed(t, dir, nil, feedNow, true)
	assert.Equal(t, []string{"a", "b"}, entryIDs(first))
	assert.False(t, first.More)

	appendText(t, dir, feedNow, feedLine(t, "c", feedNow))
	second := readFeed(t, dir, first.Position, feedNow, false)
	assert.Equal(t, []string{"c"}, entryIDs(second))
	assert.Empty(t, readFeed(t, dir, second.Position, feedNow, false).Entries)
}

// A line still being written is left for a later read.
func TestFeed_WaitsForPartialLine(t *testing.T) {
	dir := t.TempDir()
	line := feedLine(t, "b", feedNow)
	appendText(t, dir, feedNow, feedLine(t, "a", feedNow)+line[:10])

	first := readFeed(t, dir, nil, feedNow, true)
	assert.Equal(t, []string{"a"}, entryIDs(first))
	assert.Empty(t, first.Losses)

	appendText(t, dir, feedNow, line[10:])
	assert.Equal(t, []string{"b"}, entryIDs(readFeed(t, dir, first.Position, feedNow, false)))
}

// A garbled line, or one over 1 MiB, is a rejected loss between the entries
// around it, which are both returned.
func TestFeed_RejectsBadLines(t *testing.T) {
	before, after := feedNow.Add(-time.Minute), feedNow
	huge, err := json.Marshal(audit.Entry{ID: "huge", Timestamp: feedNow, Username: strings.Repeat("x", feedMaxLineBytes)})
	require.NoError(t, err)

	for name, bad := range map[string]string{
		"garbled":  `{"id":"x","timestamp":` + "\n",
		"not JSON": "garbage\n",
		"too long": string(huge) + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			appendText(t, dir, feedNow, feedLine(t, "a", before)+bad+feedLine(t, "b", after))

			chunk := readFeed(t, dir, nil, feedNow, true)
			assert.Equal(t, []string{"a", "b"}, entryIDs(chunk))
			assert.Equal(t, []audit.FeedLoss{{Reason: audit.LossRejected, From: before, To: after}}, chunk.Losses)
		})
	}
}

// An oversized line still being written is passed over as it grows, not read
// again on every check, and reported once its newline arrives.
func TestFeed_PassesOverAGrowingOversizedLine(t *testing.T) {
	dir := t.TempDir()
	before, after := feedNow.Add(-time.Minute), feedNow
	appendText(t, dir, feedNow, feedLine(t, "a", before)+strings.Repeat("x", 2*feedMaxLineBytes))

	first := readFeed(t, dir, nil, feedNow, true)
	assert.Equal(t, []string{"a"}, entryIDs(first))
	assert.Empty(t, first.Losses)
	var pos feedPosition
	require.NoError(t, json.Unmarshal(first.Position, &pos))
	assert.Greater(t, pos.Files[feedNow.Format(dateFormat)+auditFileExtension].Offset, int64(2*feedMaxLineBytes),
		"the oversized part read is not read again")

	appendText(t, dir, feedNow, "\n"+feedLine(t, "b", after))
	second := readFeed(t, dir, first.Position, feedNow, false)
	assert.Equal(t, []string{"b"}, entryIDs(second))
	assert.Equal(t, []audit.FeedLoss{{Reason: audit.LossRejected, From: before, To: after}}, second.Losses)
}

// A run of bad lines that fills one read's byte budget stays open, and the
// next read closes it at the entry after it.
func TestFeed_KeepsARejectedRunOpenAcrossReads(t *testing.T) {
	dir := t.TempDir()
	before, after := feedNow.Add(-time.Minute), feedNow
	bad := strings.Repeat("x", feedMaxLineBytes+feedMaxLineBytes/2) + "\n"
	appendText(t, dir, feedNow, feedLine(t, "a", before)+bad+bad+bad+feedLine(t, "b", after))

	first := readFeed(t, dir, nil, feedNow, true)
	assert.Equal(t, []string{"a"}, entryIDs(first))
	assert.True(t, first.More)
	assert.Empty(t, first.Losses)

	second := readFeed(t, dir, first.Position, feedNow, true)
	assert.Equal(t, []string{"b"}, entryIDs(second))
	assert.Equal(t, []audit.FeedLoss{{Reason: audit.LossRejected, From: before, To: after}}, second.Losses)
}

// A run of bad lines at the end of today's file stays open, since the entry
// after it may still arrive. Once the day is over, a run no entry closed ends
// at midnight.
func TestFeed_KeepsTodaysRejectedRunOpen(t *testing.T) {
	dir := t.TempDir()
	before, after := feedNow.Add(-time.Minute), feedNow.Add(5*time.Second)
	appendText(t, dir, feedNow, feedLine(t, "a", before)+"garbage\n")

	first := readFeed(t, dir, nil, feedNow, true)
	assert.Equal(t, []string{"a"}, entryIDs(first))
	assert.Empty(t, first.Losses)

	appendText(t, dir, feedNow, feedLine(t, "b", after)+"garbage\n")
	second := readFeed(t, dir, first.Position, after, false)
	assert.Equal(t, []string{"b"}, entryIDs(second))
	assert.Equal(t, []audit.FeedLoss{{Reason: audit.LossRejected, From: before, To: after}}, second.Losses)

	midnight := time.Date(feedNow.Year(), feedNow.Month(), feedNow.Day()+1, 0, 0, 0, 0, time.UTC)
	third := readFeed(t, dir, second.Position, midnight.Add(time.Minute), true)
	assert.Equal(t, []audit.FeedLoss{{Reason: audit.LossRejected, From: after, To: midnight}}, third.Losses)
}

// Lines still being written count toward a read's byte budget, are read
// again only once their file grows, and do not keep a read from later files.
func TestFeed_ReadsPartialLinesOnlyOnceTheyGrow(t *testing.T) {
	dir := t.TempDir()
	var want []string
	var partial string
	for i := range 6 {
		day := feedNow.AddDate(0, 0, i-6)
		huge, err := json.Marshal(audit.Entry{ID: fmt.Sprintf("big%d", i), Timestamp: day, Username: strings.Repeat("x", 900<<10)})
		require.NoError(t, err)
		partial = string(huge)
		id := fmt.Sprintf("e%d", i)
		appendText(t, dir, day, feedLine(t, id, day)+partial)
		want = append(want, id)
	}
	appendText(t, dir, feedNow, feedLine(t, "today", feedNow))
	want = append(want, "today")

	first := readFeed(t, dir, nil, feedNow, true)
	assert.True(t, first.More, "partial lines count toward the byte budget")
	got := entryIDs(first)
	pos := first.Position
	for range 3 {
		chunk := readFeed(t, dir, pos, feedNow, false)
		got = append(got, entryIDs(chunk)...)
		pos = chunk.Position
	}
	assert.Equal(t, want, got)

	last := feedNow.AddDate(0, 0, -1)
	appendText(t, dir, last, "\n")
	assert.Equal(t, []string{"big5"}, entryIDs(readFeed(t, dir, pos, feedNow, true)))
}

// Entries in a file need not be in time order; a loss still runs forward.
func TestFeed_OrdersTheEndsOfARejectedRun(t *testing.T) {
	dir := t.TempDir()
	later, earlier := feedNow.Add(time.Minute), feedNow
	appendText(t, dir, feedNow, feedLine(t, "a", later)+"garbage\n"+feedLine(t, "b", earlier))

	chunk := readFeed(t, dir, nil, feedNow, true)

	assert.Equal(t, []audit.FeedLoss{{Reason: audit.LossRejected, From: earlier, To: later}}, chunk.Losses)
}

// The store picks a file by the entry's timestamp, so an entry stamped just
// before midnight can land in yesterday's file after it; a full read finds it.
func TestFeed_FullReadFindsLateEntriesInOlderFiles(t *testing.T) {
	dir := t.TempDir()
	yesterday := feedNow.AddDate(0, 0, -1)
	appendText(t, dir, yesterday, feedLine(t, "a", yesterday))
	first := readFeed(t, dir, nil, feedNow, true)
	require.Equal(t, []string{"a"}, entryIDs(first))

	late := time.Date(2026, 11, 19, 23, 59, 59, 900_000_000, time.UTC)
	appendText(t, dir, yesterday, feedLine(t, "late", late))

	assert.Empty(t, readFeed(t, dir, first.Position, feedNow, false).Entries)
	assert.Equal(t, []string{"late"}, entryIDs(readFeed(t, dir, first.Position, feedNow, true)))
}

// Entries Keep passes over do not count toward the limit, and the position
// moves past them.
func TestFeed_KeepSelectsEntries(t *testing.T) {
	dir := t.TempDir()
	appendText(t, dir, feedNow, feedLine(t, "a", feedNow)+feedLine(t, "skip", feedNow)+feedLine(t, "b", feedNow))
	keep := func(entry *audit.Entry) bool { return entry.ID != "skip" }

	chunk, err := NewFeed(dir).Read(context.Background(), audit.FeedQuery{Now: feedNow, Full: true, Keep: keep, Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, entryIDs(chunk))

	appendText(t, dir, feedNow, feedLine(t, "c", feedNow))
	assert.Equal(t, []string{"c"}, entryIDs(readFeed(t, dir, chunk.Position, feedNow, false)))
}

// A first read checks every file, and the read after it goes on with every
// file while the first stopped at its limit, whatever the caller asks.
func TestFeed_ContinuesAFullReadThatStopped(t *testing.T) {
	dir := t.TempDir()
	yesterday := feedNow.AddDate(0, 0, -1)
	appendText(t, dir, yesterday, feedLine(t, "a", yesterday)+feedLine(t, "b", yesterday))
	feed := NewFeed(dir)

	first, err := feed.Read(context.Background(), audit.FeedQuery{Now: feedNow, Limit: 1})
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, entryIDs(first))
	require.True(t, first.More)

	second, err := feed.Read(context.Background(), audit.FeedQuery{Position: first.Position, Now: feedNow, Limit: 1})
	require.NoError(t, err)
	assert.Equal(t, []string{"b"}, entryIDs(second))
}

// Unsent counts the bytes past the position: what is not read yet, and a
// file the position does not hold, whole.
func TestFeed_Unsent(t *testing.T) {
	dir := t.TempDir()
	yesterday := feedNow.AddDate(0, 0, -1)
	appendText(t, dir, yesterday, feedLine(t, "a", yesterday))
	first := readFeed(t, dir, nil, feedNow, true)
	feed := NewFeed(dir)

	caughtUp, err := feed.Unsent(context.Background(), first.Position)
	require.NoError(t, err)
	assert.Zero(t, caughtUp)

	unread := feedLine(t, "b", yesterday)
	appendText(t, dir, yesterday, unread)
	newFile := feedLine(t, "c", feedNow)
	appendText(t, dir, feedNow, newFile)
	unsent, err := feed.Unsent(context.Background(), first.Position)
	require.NoError(t, err)
	assert.Equal(t, int64(len(unread)+len(newFile)), unsent)

	all, err := feed.Unsent(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, unsent+int64(len(feedLine(t, "a", yesterday))), all)

	// A file replaced by a smaller one is read again from its start.
	replacement := feedLine(t, "r", yesterday)[:10] + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, yesterday.Format(dateFormat)+auditFileExtension), []byte(replacement), auditFilePermissions))
	replaced, err := feed.Unsent(context.Background(), first.Position)
	require.NoError(t, err)
	assert.Equal(t, int64(len(replacement)+len(newFile)), replaced)
}

func TestFeed_StopsAtLimit(t *testing.T) {
	dir := t.TempDir()
	appendText(t, dir, feedNow, feedLine(t, "a", feedNow)+feedLine(t, "b", feedNow)+feedLine(t, "c", feedNow))
	feed := NewFeed(dir)

	first, err := feed.Read(context.Background(), audit.FeedQuery{Now: feedNow, Full: true, Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, entryIDs(first))
	assert.True(t, first.More)

	second, err := feed.Read(context.Background(), audit.FeedQuery{Position: first.Position, Now: feedNow, Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, []string{"c"}, entryIDs(second))
	assert.False(t, second.More)
}

// retentionDir is an audit directory with a cleaner at 7-day retention, and
// the day 30 days ago, which it expires.
type retentionDir struct {
	dir     string
	expired time.Time
	cleaner *cleaner
}

func newRetentionDir(t *testing.T) retentionDir {
	t.Helper()
	dir := t.TempDir()
	store, err := New(dir, 0)
	require.NoError(t, err)
	expired := time.Now().UTC().AddDate(0, 0, -30).Truncate(24 * time.Hour)
	return retentionDir{
		dir:     dir,
		expired: expired,
		cleaner: &cleaner{baseDir: dir, retentionDays: 7, appendFn: store.appendDurable, stopCh: make(chan struct{})},
	}
}

// write appends an entry stamped hours into the expired day.
func (d retentionDir) write(t *testing.T, id string, hours int) {
	t.Helper()
	appendText(t, d.dir, d.expired, feedLine(t, id, d.expired.Add(time.Duration(hours)*time.Hour)))
}

// read runs a full read now, as the shipper would.
func (d retentionDir) read(t *testing.T, pos []byte) audit.FeedChunk {
	t.Helper()
	return readFeed(t, d.dir, pos, time.Now().UTC(), true)
}

func (d retentionDir) deletedLoss(from time.Time) []audit.FeedLoss {
	return []audit.FeedLoss{{Reason: audit.LossDeleted, From: from, To: d.expired.AddDate(0, 0, 1)}}
}

func TestFeed_Retention(t *testing.T) {
	t.Run("file read to its end", func(t *testing.T) {
		d := newRetentionDir(t)
		d.write(t, "old", 1)
		first := d.read(t, nil)
		require.Equal(t, []string{"old"}, entryIDs(first))

		d.cleaner.purgeExpiredFiles()
		chunk := d.read(t, first.Position)

		assert.Equal(t, []string{cleanupAction}, actions(chunk))
		assert.Empty(t, chunk.Losses)
	})

	t.Run("file with entries not read", func(t *testing.T) {
		d := newRetentionDir(t)
		d.write(t, "old", 1)
		first := d.read(t, nil)
		d.write(t, "unread", 2)

		d.cleaner.purgeExpiredFiles()

		assert.Equal(t, d.deletedLoss(d.expired.Add(time.Hour)), d.read(t, first.Position).Losses)
	})

	// dagu secret can write a file while the server is stopped, and retention
	// removes it on the next start before any read.
	t.Run("file never read", func(t *testing.T) {
		d := newRetentionDir(t)
		first := d.read(t, nil)
		d.write(t, "unread", 1)

		d.cleaner.purgeExpiredFiles()

		assert.Equal(t, d.deletedLoss(d.expired), d.read(t, first.Position).Losses)
	})

	t.Run("file removed before the first read", func(t *testing.T) {
		d := newRetentionDir(t)
		d.write(t, "old", 1)
		d.cleaner.purgeExpiredFiles()

		// The position starts after the cleanup. A coarse clock, as on
		// Windows, can stamp both with the same time, and a cleanup at the
		// start of a position counts as one the reader had to see.
		chunk := readFeed(t, d.dir, nil, time.Now().UTC().Add(time.Millisecond), true)

		assert.Equal(t, []string{cleanupAction}, actions(chunk))
		assert.Empty(t, chunk.Losses)
	})

	// A read can stop at its byte budget right after the file's last, bad
	// line; the run stays open, and is reported when the file goes.
	t.Run("file ending in bad lines", func(t *testing.T) {
		d := newRetentionDir(t)
		d.write(t, "old", 1)
		bad := strings.Repeat("x", 900<<10) + "\n"
		appendText(t, d.dir, d.expired, strings.Repeat(bad, 5))
		first := d.read(t, nil)
		require.True(t, first.More)
		require.Empty(t, first.Losses)

		d.cleaner.purgeExpiredFiles()
		chunk := d.read(t, first.Position)

		assert.Equal(t, []audit.FeedLoss{{
			Reason: audit.LossRejected, From: d.expired.Add(time.Hour), To: d.expired.AddDate(0, 0, 1),
		}}, chunk.Losses)
	})

	t.Run("file deleted by hand", func(t *testing.T) {
		d := newRetentionDir(t)
		d.write(t, "old", 1)
		first := d.read(t, nil)
		require.NoError(t, os.Remove(filepath.Join(d.dir, d.expired.Format(dateFormat)+auditFileExtension)))

		assert.Equal(t, d.deletedLoss(d.expired.Add(time.Hour)), d.read(t, first.Position).Losses)
	})
}

func actions(chunk audit.FeedChunk) []string {
	names := make([]string, 0, len(chunk.Entries))
	for _, entry := range chunk.Entries {
		names = append(names, entry.Action)
	}
	return names
}
