// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package eventstore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/service/eventstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreEmitWritesInboxFile(t *testing.T) {
	t.Parallel()

	store, err := New(t.TempDir())
	require.NoError(t, err)

	event := testEvent("evt-1", time.Date(2026, 3, 29, 10, 0, 0, 0, time.UTC))
	require.NoError(t, store.Emit(context.Background(), event))

	entries, err := os.ReadDir(store.inboxDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	data, err := os.ReadFile(filepath.Join(store.inboxDir, entries[0].Name()))
	require.NoError(t, err)

	var stored eventstore.Event
	require.NoError(t, json.Unmarshal(data, &stored))
	assert.Equal(t, event.ID, stored.ID)
	assert.Equal(t, event.Type, stored.Type)
	assert.Equal(t, event.OccurredAt, stored.OccurredAt)
}

func TestStoreEmitDefaultsRecordedAtInInboxPayload(t *testing.T) {
	t.Parallel()

	store, err := New(t.TempDir())
	require.NoError(t, err)

	event := testEvent("evt-zero-recorded", time.Date(2026, 3, 29, 10, 0, 0, 0, time.UTC))
	event.RecordedAt = time.Time{}
	require.NoError(t, store.Emit(context.Background(), event))

	entries, err := os.ReadDir(store.inboxDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	data, err := os.ReadFile(filepath.Join(store.inboxDir, entries[0].Name()))
	require.NoError(t, err)

	var stored eventstore.Event
	require.NoError(t, json.Unmarshal(data, &stored))
	assert.False(t, stored.RecordedAt.IsZero())
}

func TestStoreQuerySkipsMalformedAndPaginates(t *testing.T) {
	t.Parallel()

	store, err := New(t.TempDir())
	require.NoError(t, err)

	dayOne := time.Date(2026, 3, 28, 12, 0, 0, 0, time.UTC)
	dayTwo := time.Date(2026, 3, 29, 8, 0, 0, 0, time.UTC)
	dayTwoOther := time.Date(2026, 3, 29, 9, 0, 0, 0, time.UTC)

	eventOne := testEvent("evt-1", dayOne)
	eventOne.DAGName = "example"
	eventTwo := testEvent("evt-2", dayTwo)
	eventTwo.DAGName = "example"
	eventThree := testEvent("evt-3", dayTwoOther)
	eventThree.DAGName = "other"

	writeCommittedEvents(t, store.baseDir, dayOne, [][]byte{
		mustMarshalEvent(t, eventOne),
		[]byte("not-json"),
	})
	writeCommittedEvents(t, store.baseDir, dayTwo, [][]byte{
		mustMarshalEvent(t, eventTwo),
		mustMarshalEvent(t, eventThree),
	})

	firstPage, err := store.Query(context.Background(), eventstore.QueryFilter{
		DAGName:        "example",
		Limit:          1,
		PaginationMode: eventstore.QueryPaginationModeCursor,
	})
	require.NoError(t, err)
	require.Len(t, firstPage.Entries, 1)
	assert.Equal(t, "evt-2", firstPage.Entries[0].ID)
	require.NotEmpty(t, firstPage.NextCursor)

	secondPage, err := store.Query(context.Background(), eventstore.QueryFilter{
		DAGName:        "example",
		Limit:          1,
		Cursor:         firstPage.NextCursor,
		PaginationMode: eventstore.QueryPaginationModeCursor,
	})
	require.NoError(t, err)
	require.Len(t, secondPage.Entries, 1)
	assert.Equal(t, "evt-1", secondPage.Entries[0].ID)
	assert.True(t, secondPage.Entries[0].OccurredAt.Equal(dayOne))
	assert.Empty(t, secondPage.NextCursor)
}

func TestStoreQueryIncludesLegacyDailyFilesWithinHourlyRange(t *testing.T) {
	t.Parallel()

	store, err := New(t.TempDir())
	require.NoError(t, err)

	event := testEvent("evt-legacy", time.Date(2026, 3, 29, 18, 30, 0, 0, time.UTC))
	writeLegacyDailyEvents(t, store.baseDir, event.OccurredAt, [][]byte{
		mustMarshalEvent(t, event),
	})

	result, err := store.Query(context.Background(), eventstore.QueryFilter{
		StartTime: time.Date(2026, 3, 29, 18, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 3, 29, 19, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	require.Len(t, result.Entries, 1)
	assert.Equal(t, "evt-legacy", result.Entries[0].ID)
	assert.Empty(t, result.NextCursor)
}

func TestStoreQuerySupportsOffsetCompatibilityPagination(t *testing.T) {
	t.Parallel()

	store, err := New(t.TempDir())
	require.NoError(t, err)

	first := testEvent("evt-1", time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC))
	first.RecordedAt = first.OccurredAt.Add(time.Minute)
	second := testEvent("evt-2", time.Date(2026, 3, 29, 11, 0, 0, 0, time.UTC))
	second.RecordedAt = second.OccurredAt.Add(time.Minute)
	writeCommittedEvents(t, store.baseDir, first.OccurredAt, [][]byte{
		mustMarshalEvent(t, first),
	})
	writeCommittedEvents(t, store.baseDir, second.OccurredAt, [][]byte{
		mustMarshalEvent(t, second),
	})

	page, err := store.Query(context.Background(), eventstore.QueryFilter{
		DAGName: "example",
		Limit:   1,
		Offset:  1,
	})
	require.NoError(t, err)
	require.Len(t, page.Entries, 1)
	require.NotNil(t, page.Total)
	assert.Equal(t, 2, *page.Total)
	assert.Equal(t, "evt-2", page.Entries[0].ID)
	assert.Empty(t, page.NextCursor)
}

func TestStoreQueryReadsLargeCommittedEventLine(t *testing.T) {
	t.Parallel()

	store, err := New(t.TempDir())
	require.NoError(t, err)

	event := testEvent("evt-large", time.Date(2026, 3, 29, 20, 0, 0, 0, time.UTC))
	event.Data = map[string]any{
		"payload": strings.Repeat("x", 128*1024),
	}
	writeCommittedEvents(t, store.baseDir, event.OccurredAt, [][]byte{
		mustMarshalEvent(t, event),
	})

	result, err := store.Query(context.Background(), eventstore.QueryFilter{
		DAGName: event.DAGName,
	})
	require.NoError(t, err)
	require.Len(t, result.Entries, 1)
	assert.Equal(t, event.ID, result.Entries[0].ID)
	assert.Empty(t, result.NextCursor)
}

func TestStoreQueryRejectsInvalidCursor(t *testing.T) {
	t.Parallel()

	store, err := New(t.TempDir())
	require.NoError(t, err)

	result, err := store.Query(context.Background(), eventstore.QueryFilter{
		Cursor:         "not-a-valid-cursor",
		PaginationMode: eventstore.QueryPaginationModeCursor,
	})
	require.ErrorIs(t, err, eventstore.ErrInvalidQueryCursor)
	assert.Nil(t, result)
}

func TestStoreQueryReturnsEmptyWhenCursorFileWasRemoved(t *testing.T) {
	t.Parallel()

	store, err := New(t.TempDir())
	require.NoError(t, err)

	first := testEvent("evt-1", time.Date(2026, 3, 29, 10, 0, 0, 0, time.UTC))
	second := testEvent("evt-2", time.Date(2026, 3, 29, 9, 0, 0, 0, time.UTC))
	writeCommittedEvents(t, store.baseDir, first.OccurredAt, [][]byte{
		mustMarshalEvent(t, first),
	})
	writeCommittedEvents(t, store.baseDir, second.OccurredAt, [][]byte{
		mustMarshalEvent(t, second),
	})

	page, err := store.Query(context.Background(), eventstore.QueryFilter{
		Limit:          1,
		PaginationMode: eventstore.QueryPaginationModeCursor,
	})
	require.NoError(t, err)
	require.NotEmpty(t, page.NextCursor)

	cursor, err := decodeQueryCursor(page.NextCursor, eventstore.QueryFilter{})
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(store.baseDir, cursor.File)))

	resumed, err := store.Query(context.Background(), eventstore.QueryFilter{
		Limit:          1,
		Cursor:         page.NextCursor,
		PaginationMode: eventstore.QueryPaginationModeCursor,
	})
	require.NoError(t, err)
	assert.Empty(t, resumed.Entries)
	assert.Empty(t, resumed.NextCursor)
}

func mustMarshalEvent(t *testing.T, event *eventstore.Event) []byte {
	t.Helper()
	data, err := json.Marshal(event)
	require.NoError(t, err)
	return data
}

func writeCommittedEvents(t *testing.T, baseDir string, slot time.Time, lines [][]byte) {
	t.Helper()
	path := filepath.Join(baseDir, logPrefix+slot.UTC().Format(hourFormat)+logSuffix)
	content := make([]string, 0, len(lines))
	for _, line := range lines {
		content = append(content, string(line))
	}
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(content, "\n")+"\n"), filePermissions))
}

func writeLegacyDailyEvents(t *testing.T, baseDir string, day time.Time, lines [][]byte) {
	t.Helper()
	path := filepath.Join(baseDir, logPrefix+day.UTC().Format(dayFormat)+logSuffix)
	content := make([]string, 0, len(lines))
	for _, line := range lines {
		content = append(content, string(line))
	}
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(content, "\n")+"\n"), filePermissions))
}

func testEvent(id string, occurredAt time.Time) *eventstore.Event {
	return &eventstore.Event{
		ID:             id,
		SchemaVersion:  eventstore.SchemaVersion,
		OccurredAt:     occurredAt.UTC(),
		RecordedAt:     occurredAt.UTC().Add(10 * time.Millisecond),
		Kind:           eventstore.KindDAGRun,
		Type:           eventstore.TypeDAGRunFailed,
		SourceService:  eventstore.SourceServiceScheduler,
		SourceInstance: "test-instance",
		DAGName:        "example",
		DAGRunID:       "run-1",
		AttemptID:      "attempt-1",
		Status:         "failed",
	}
}
