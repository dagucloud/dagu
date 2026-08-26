// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package eventstore

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/stretchr/testify/require"
)

const benchmarkSeenIDCount = 100_000

var benchmarkSeenIDs any

func BenchmarkSeenIDsRetention(b *testing.B) {
	for range b.N {
		benchmarkSeenIDs = nil
		runtime.GC()

		var before runtime.MemStats
		runtime.ReadMemStats(&before)

		seen := newSeenSet()
		for i := range benchmarkSeenIDCount {
			seen.add(fmt.Sprintf("dag_%064x", i))
		}

		runtime.GC()
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		runtime.KeepAlive(seen)

		bytesPerID := float64(after.HeapAlloc-before.HeapAlloc) / benchmarkSeenIDCount
		b.ReportMetric(bytesPerID, "live-B/id")
		benchmarkSeenIDs = seen
	}
}

func TestSeenSetPreservesIDs(t *testing.T) {
	t.Parallel()

	digest := strings.Repeat("a", 64)
	ids := []string{
		"dag_" + digest,
		"dag_update_" + digest,
		"llm_" + digest,
		"dag_" + strings.ToUpper(digest),
		"dag_short",
		"evt-legacy",
	}

	seen := newSeenSet()
	for _, id := range ids {
		require.False(t, seen.has(id), id)
		seen.add(id)
		require.True(t, seen.has(id), id)
	}

	for _, id := range ids {
		require.True(t, seen.has(id), id)
	}
}

func TestCollectorDrainOnceAppendsByHourAndDeduplicatesAcrossRestart(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	store, err := New(baseDir)
	require.NoError(t, err)

	dayOne := time.Date(2026, 3, 28, 23, 0, 0, 0, time.UTC)
	dayTwo := time.Date(2026, 3, 29, 1, 0, 0, 0, time.UTC)
	eventOne := testEvent("dag_"+strings.Repeat("a", 64), dayOne)
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

	require.NoError(t, store.Emit(context.Background(), testEvent(eventOne.ID, dayOne)))
	require.NoError(t, store.Emit(context.Background(), testEvent(eventTwo.ID, dayTwo)))
	require.NoError(t, restarted.DrainOnce(context.Background()))

	assertInboxCount(t, store.inboxDir, 0)
	assertLogLineCount(t, filepath.Join(baseDir, "_2026032823.jsonl"), 1)
	assertLogLineCount(t, filepath.Join(baseDir, "_2026032901.jsonl"), 1)
}

func TestCollectorDrainOncePreservesInboxAfterMalformedCommittedEvent(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	event := testEvent("evt-after-malformed", time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC))
	validJSON := string(mustMarshalEvent(t, event))
	malformedJSON := strings.Replace(
		validJSON,
		`"schema_version":`+strconv.Itoa(event.SchemaVersion),
		`"schema_version":"bad"`,
		1,
	)
	require.NotEqual(t, validJSON, malformedJSON)
	writeCommittedEvents(t, baseDir, event.OccurredAt, [][]byte{[]byte(malformedJSON)})

	store, err := New(baseDir)
	require.NoError(t, err)
	require.NoError(t, store.Emit(context.Background(), event))

	collector, err := NewCollector(baseDir, 10)
	require.NoError(t, err)
	require.NoError(t, collector.loadSeenIDs())
	require.False(t, collector.seenIDs.has(event.ID))

	require.NoError(t, collector.DrainOnce(context.Background()))
	assertInboxCount(t, store.inboxDir, 0)
	assertLogLineCount(t, filepath.Join(baseDir, "_2026032912.jsonl"), 2)
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
	hasExpired := collector.seenIDs.has(expiredEvent.ID)
	hasRecent := collector.seenIDs.has(recentEvent.ID)
	require.True(t, hasExpired)
	require.True(t, hasRecent)

	collector.cleanupExpired()

	hasExpired = collector.seenIDs.has(expiredEvent.ID)
	hasRecent = collector.seenIDs.has(recentEvent.ID)
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
	require.True(t, collector.seenIDs.has(event.ID))
}

func TestCollectorSeenIDAllocs(t *testing.T) {
	const (
		largeFieldCount   = 512
		maxAllocationRate = 2
	)

	newCollector := func(data map[string]any) *Collector {
		baseDir := t.TempDir()
		event := testEvent("evt-allocs", time.Date(2026, 3, 29, 22, 0, 0, 0, time.UTC))
		event.Data = data
		writeCommittedEvents(t, baseDir, event.OccurredAt, [][]byte{mustMarshalEvent(t, event)})

		collector, err := NewCollector(baseDir, 10)
		require.NoError(t, err)
		return collector
	}

	small := newCollector(map[string]any{"0": 0})
	largeData := make(map[string]any, largeFieldCount)
	for i := range largeFieldCount {
		largeData[strconv.Itoa(i)] = i
	}
	large := newCollector(largeData)

	var smallErr error
	smallAllocs := testing.AllocsPerRun(5, func() {
		smallErr = small.loadSeenIDs()
	})
	require.NoError(t, smallErr)

	var largeErr error
	largeAllocs := testing.AllocsPerRun(5, func() {
		largeErr = large.loadSeenIDs()
	})
	require.NoError(t, largeErr)
	require.LessOrEqual(t, largeAllocs, smallAllocs*maxAllocationRate)
}

func TestCollectorPendingEventAllocs(t *testing.T) {
	const (
		largeFieldCount   = 512
		maxAllocationRate = 2
	)

	newPendingEvent := func(data map[string]any) (*Collector, string) {
		collector, err := NewCollector(t.TempDir(), 10)
		require.NoError(t, err)

		event := testEvent("evt-allocs", time.Date(2026, 3, 29, 22, 0, 0, 0, time.UTC))
		event.Data = data
		path := filepath.Join(collector.store.inboxDir, "pending.json")
		require.NoError(t, os.WriteFile(path, mustMarshalEvent(t, event), filePermissions))

		return collector, path
	}

	small, smallPath := newPendingEvent(map[string]any{"0": 0})
	largeData := make(map[string]any, largeFieldCount)
	for i := range largeFieldCount {
		largeData[strconv.Itoa(i)] = i
	}
	large, largePath := newPendingEvent(largeData)

	var smallErr error
	smallAllocs := testing.AllocsPerRun(5, func() {
		_, smallErr = small.readPendingEvent(smallPath)
	})
	require.NoError(t, smallErr)

	var largeErr error
	largeAllocs := testing.AllocsPerRun(5, func() {
		_, largeErr = large.readPendingEvent(largePath)
	})
	require.NoError(t, largeErr)
	require.LessOrEqual(t, largeAllocs, smallAllocs*maxAllocationRate)
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
