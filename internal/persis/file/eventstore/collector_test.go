// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package eventstore

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/stretchr/testify/require"
)

func TestCollectorDrainOnceAppendsByHourAndDeduplicatesAcrossRestart(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	store, err := New(baseDir)
	require.NoError(t, err)

	dayOne := time.Date(2026, 3, 28, 23, 0, 0, 0, time.UTC)
	dayTwo := time.Date(2026, 3, 29, 1, 0, 0, 0, time.UTC)
	eventOne := testEvent("evt-1", dayOne)
	eventTwo := testEvent("evt-2", dayTwo)
	eventTwo.DAGRunID = "run-2"

	require.NoError(t, store.Emit(context.Background(), eventOne))
	require.NoError(t, store.Emit(context.Background(), eventTwo))

	collector, err := NewCollector(baseDir, 10)
	require.NoError(t, err)
	require.NoError(t, collector.DrainOnce(context.Background()))

	assertInboxCount(t, store.inboxDir, 0)
	assertLogLineCount(t, filepath.Join(baseDir, "_2026032823.jsonl"), 1)
	assertLogLineCount(t, filepath.Join(baseDir, "_2026032901.jsonl"), 1)

	restarted, err := NewCollector(baseDir, 10)
	require.NoError(t, err)
	require.NoError(t, restarted.loadSeenIDs())

	duplicate := testEvent("evt-1", dayOne)
	require.NoError(t, store.Emit(context.Background(), duplicate))
	require.NoError(t, restarted.DrainOnce(context.Background()))

	assertInboxCount(t, store.inboxDir, 0)
	assertLogLineCount(t, filepath.Join(baseDir, "_2026032823.jsonl"), 1)
}

func TestCollectorDrainOnceQuarantinesMalformedInbox(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	collector, err := NewCollector(baseDir, 10)
	require.NoError(t, err)

	badFile := filepath.Join(collector.store.inboxDir, "bad.json")
	require.NoError(t, os.WriteFile(badFile, []byte("{invalid"), filePermissions))

	require.NoError(t, collector.DrainOnce(context.Background()))

	assertInboxCount(t, collector.store.inboxDir, 0)
	entries, err := os.ReadDir(collector.store.quarantineDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestCollectorDrainOnceIgnoresAtomicWriteTempFiles(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	store, err := New(baseDir)
	require.NoError(t, err)

	event := testEvent("evt-final", time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC))
	require.NoError(t, store.Emit(context.Background(), event))

	tmpFile := filepath.Join(store.inboxDir, "pending.json.tmp.123")
	require.NoError(t, os.WriteFile(tmpFile, []byte("{partial"), filePermissions))

	collector, err := NewCollector(baseDir, 10)
	require.NoError(t, err)
	require.NoError(t, collector.DrainOnce(context.Background()))

	assertFileExists(t, tmpFile, true)
	assertInboxCount(t, store.inboxDir, 1)
	assertLogLineCount(t, filepath.Join(baseDir, "_2026032912.jsonl"), 1)

	entries, err := os.ReadDir(store.quarantineDir)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestCollectorDrainOnceDropsDuplicateInboxEventsWithinSinglePass(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	store, err := New(baseDir)
	require.NoError(t, err)

	event := testEvent("evt-dup", time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC))
	require.NoError(t, store.Emit(context.Background(), event))
	require.NoError(t, store.Emit(context.Background(), event))

	collector, err := NewCollector(baseDir, 10)
	require.NoError(t, err)
	require.NoError(t, collector.DrainOnce(context.Background()))

	assertInboxCount(t, store.inboxDir, 0)
	assertLogLineCount(t, filepath.Join(baseDir, "_2026032912.jsonl"), 1)
}

func TestCollectorCleanupExpiredPreservesInbox(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	now := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
	collector, err := NewCollector(baseDir, 10, WithNow(func() time.Time { return now }))
	require.NoError(t, err)

	expiredHour := now.AddDate(0, 0, -20)
	recentHour := now.Add(-time.Hour)

	expiredLog := filepath.Join(baseDir, "_"+expiredHour.UTC().Format(hourFormat)+".jsonl")
	recentLog := filepath.Join(baseDir, "_"+recentHour.UTC().Format(hourFormat)+".jsonl")
	expiredQuarantine := filepath.Join(collector.store.quarantineDir, "expired.json")
	inboxFile := filepath.Join(collector.store.inboxDir, "pending.json")

	require.NoError(t, os.WriteFile(expiredLog, []byte("{}\n"), filePermissions))
	require.NoError(t, os.WriteFile(recentLog, []byte("{}\n"), filePermissions))
	require.NoError(t, os.WriteFile(expiredQuarantine, []byte("{}"), filePermissions))
	require.NoError(t, os.WriteFile(inboxFile, []byte("{}"), filePermissions))
	require.NoError(t, os.Chtimes(expiredQuarantine, expiredHour, expiredHour))

	collector.cleanupExpired()

	assertFileExists(t, expiredLog, false)
	assertFileExists(t, recentLog, true)
	assertFileExists(t, expiredQuarantine, false)
	assertFileExists(t, inboxFile, true)
}

func TestCollectorCleanupExpiredRebuildsSeenIDs(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	now := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
	collector, err := NewCollector(baseDir, 10, WithNow(func() time.Time { return now }))
	require.NoError(t, err)

	expiredEvent := testEvent("evt-expired", now.AddDate(0, 0, -20))
	recentEvent := testEvent("evt-recent", now.Add(-time.Hour))
	writeCommittedEvents(t, baseDir, expiredEvent.OccurredAt, [][]byte{mustMarshalEvent(t, expiredEvent)})
	writeCommittedEvents(t, baseDir, recentEvent.OccurredAt, [][]byte{mustMarshalEvent(t, recentEvent)})

	require.NoError(t, collector.loadSeenIDs())
	_, hasExpired := collector.seenIDs[expiredEvent.ID]
	_, hasRecent := collector.seenIDs[recentEvent.ID]
	require.True(t, hasExpired)
	require.True(t, hasRecent)

	collector.cleanupExpired()

	_, hasExpired = collector.seenIDs[expiredEvent.ID]
	_, hasRecent = collector.seenIDs[recentEvent.ID]
	require.False(t, hasExpired)
	require.True(t, hasRecent)
}

func TestCollectorLoadSeenIDsReadsLargeCommittedEventLine(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	collector, err := NewCollector(baseDir, 10)
	require.NoError(t, err)

	event := testEvent("evt-large", time.Date(2026, 3, 29, 22, 0, 0, 0, time.UTC))
	event.Data = map[string]any{
		"payload": strings.Repeat("x", 128*1024),
	}
	writeCommittedEvents(t, baseDir, event.OccurredAt, [][]byte{mustMarshalEvent(t, event)})

	require.NoError(t, collector.loadSeenIDs())
	_, ok := collector.seenIDs[event.ID]
	require.True(t, ok)
}

func assertInboxCount(t *testing.T, dir string, count int) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, count)
}

func assertLogLineCount(t *testing.T, path string, expected int) {
	t.Helper()
	file, err := os.Open(path) //nolint:gosec // test file
	require.NoError(t, err)
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	fileutil.ConfigureScanner(scanner)
	count := 0
	for scanner.Scan() {
		count++
	}
	require.NoError(t, scanner.Err())
	require.Equal(t, expected, count)
}

func assertFileExists(t *testing.T, path string, exists bool) {
	t.Helper()
	_, err := os.Stat(path)
	if exists {
		require.NoError(t, err)
		return
	}
	require.ErrorIs(t, err, os.ErrNotExist)
}
