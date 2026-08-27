// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package eventstore

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	coreeventstore "github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/stretchr/testify/require"
)

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

	collector, err := NewCollector(baseDir, 0)
	require.NoError(t, err)
	require.NoError(t, collector.DrainOnce(context.Background()))

	assertInboxCount(t, store.inboxDir, 0)
	assertLogLineCount(t, filepath.Join(baseDir, "_2026032823.jsonl"), 1)
	assertLogLineCount(t, filepath.Join(baseDir, "_2026032901.jsonl"), 1)

	restarted, err := NewCollector(baseDir, 0)
	require.NoError(t, err)

	require.NoError(t, store.Emit(context.Background(), testEvent(eventOne.ID, dayOne.Add(time.Hour))))
	require.NoError(t, store.Emit(context.Background(), testEvent(eventTwo.ID, dayTwo)))
	require.NoError(t, restarted.DrainOnce(context.Background()))

	assertInboxCount(t, store.inboxDir, 0)
	assertLogLineCount(t, filepath.Join(baseDir, "_2026032823.jsonl"), 1)
	assertFileExists(t, filepath.Join(baseDir, "_2026032900.jsonl"), false)
	assertLogLineCount(t, filepath.Join(baseDir, "_2026032901.jsonl"), 1)
	result, err := store.Query(context.Background(), coreeventstore.QueryFilter{})
	require.NoError(t, err)
	require.Len(t, result.Entries, 2)
}

func TestCollectorDrainOnceDeduplicatesAcrossHours(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	store, err := New(baseDir)
	require.NoError(t, err)
	collector, err := NewCollector(baseDir, 0)
	require.NoError(t, err)

	firstHour := time.Date(2026, 3, 29, 10, 59, 0, 0, time.UTC)
	event := testEvent("evt-cross-hour", firstHour)
	require.NoError(t, store.Emit(context.Background(), event))
	require.NoError(t, collector.DrainOnce(context.Background()))

	require.NoError(t, store.Emit(context.Background(), testEvent(event.ID, firstHour.Add(time.Hour))))
	require.NoError(t, collector.DrainOnce(context.Background()))

	result, err := store.Query(context.Background(), coreeventstore.QueryFilter{})
	require.NoError(t, err)
	require.Len(t, result.Entries, 1)
	assertFileExists(t, filepath.Join(baseDir, "_2026032911.jsonl"), false)
}

func TestCollectorDrainOnceVerifiesFilterMatches(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	hour := time.Date(2026, 3, 29, 10, 0, 0, 0, time.UTC)
	committed := testEvent("evt-committed", hour)
	writeCommittedEvents(t, baseDir, hour, [][]byte{mustMarshalEvent(t, committed)})

	store, err := New(baseDir)
	require.NoError(t, err)
	collector, err := NewCollector(baseDir, 0, WithDedupeCacheBytes(1))
	require.NoError(t, err)
	require.NoError(t, collector.ensureCommittedIDs())

	var unique *coreeventstore.Event
	for i := range 1000 {
		candidate := testEvent("evt-unique-"+strconv.Itoa(i), hour.Add(time.Hour))
		if collector.committedIDs.mayContain(candidate.ID) {
			unique = candidate
			break
		}
	}
	require.NotNil(t, unique)

	require.NoError(t, store.Emit(context.Background(), unique))
	require.NoError(t, collector.DrainOnce(context.Background()))

	result, err := store.Query(context.Background(), coreeventstore.QueryFilter{})
	require.NoError(t, err)
	require.Len(t, result.Entries, 2)
}

func TestEventIDFilterUsesBudget(t *testing.T) {
	t.Parallel()

	const budget = 128
	collector, err := NewCollector(t.TempDir(), 0, WithDedupeCacheBytes(budget))
	require.NoError(t, err)
	require.NoError(t, collector.ensureCommittedIDs())
	collector.committedIDs.add("evt-filtered")

	require.Len(t, collector.committedIDs.bits, budget)
	require.True(t, collector.committedIDs.mayContain("evt-filtered"))
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

func TestCollectorDrainOnceReadsLargeCommittedEventLine(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	store, err := New(baseDir)
	require.NoError(t, err)

	collector, err := NewCollector(baseDir, 10)
	require.NoError(t, err)

	event := testEvent("evt-large", time.Date(2026, 3, 29, 22, 0, 0, 0, time.UTC))
	event.Data = map[string]any{
		"payload": strings.Repeat("x", 128*1024),
	}
	writeCommittedEvents(t, baseDir, event.OccurredAt, [][]byte{mustMarshalEvent(t, event)})
	require.NoError(t, store.Emit(context.Background(), event))

	require.NoError(t, collector.DrainOnce(context.Background()))
	assertInboxCount(t, store.inboxDir, 0)
	assertLogLineCount(t, filepath.Join(baseDir, "_2026032922.jsonl"), 1)
}

func TestCollectorCommittedIDAllocs(t *testing.T) {
	const (
		baselineFieldCount = 64
		largeFieldCount    = 512
		maxAllocationRate  = 2
	)

	newCollector := func(data map[string]any) (*Collector, string, map[string]struct{}) {
		baseDir := t.TempDir()
		event := testEvent("evt-allocs", time.Date(2026, 3, 29, 22, 0, 0, 0, time.UTC))
		event.Data = data
		writeCommittedEvents(t, baseDir, event.OccurredAt, [][]byte{mustMarshalEvent(t, event)})

		collector, err := NewCollector(baseDir, 10)
		require.NoError(t, err)
		path := filepath.Join(baseDir, "_2026032922.jsonl")
		return collector, path, map[string]struct{}{event.ID: {}}
	}

	small, smallPath, smallIDs := newCollector(testEventData(baselineFieldCount))
	large, largePath, largeIDs := newCollector(testEventData(largeFieldCount))

	var smallErr error
	smallAllocs := testing.AllocsPerRun(5, func() {
		_, smallErr = small.findCommittedIDs(smallPath, smallIDs)
	})
	require.NoError(t, smallErr)

	var largeErr error
	largeAllocs := testing.AllocsPerRun(5, func() {
		_, largeErr = large.findCommittedIDs(largePath, largeIDs)
	})
	require.NoError(t, largeErr)
	require.LessOrEqual(t, largeAllocs, smallAllocs*maxAllocationRate)
}

func TestCollectorPendingEventAllocs(t *testing.T) {
	const (
		baselineFieldCount = 64
		largeFieldCount    = 512
		maxAllocationRate  = 2
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

	small, smallPath := newPendingEvent(testEventData(baselineFieldCount))
	large, largePath := newPendingEvent(testEventData(largeFieldCount))

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

func testEventData(fieldCount int) map[string]any {
	data := make(map[string]any, fieldCount)
	for i := range fieldCount {
		data[strconv.Itoa(i)] = i
	}
	return data
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
