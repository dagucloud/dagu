// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package eventstore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/service/eventstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreReadNotificationEventsTracksCommittedOffsetsAndInbox(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	store, err := New(baseDir)
	require.NoError(t, err)

	oldEvent := testEvent("evt-old", time.Date(2026, 3, 29, 9, 0, 0, 0, time.UTC))
	oldInboxEvent := testEvent("evt-old-inbox", time.Date(2026, 3, 29, 9, 30, 0, 0, time.UTC))
	require.NoError(t, store.Emit(context.Background(), oldInboxEvent))
	writeCommittedEvents(t, store.baseDir, oldEvent.OccurredAt, [][]byte{mustMarshalEvent(t, oldEvent)})

	cursor, err := store.NotificationHeadCursor(context.Background())
	require.NoError(t, err)

	committedEvent := testEvent("evt-committed", time.Date(2026, 3, 29, 9, 45, 0, 0, time.UTC))
	committedEvent.RecordedAt = time.Date(2026, 3, 29, 10, 0, 0, 0, time.UTC)
	appendCommittedEvents(t, store.baseDir, committedEvent.OccurredAt, [][]byte{mustMarshalEvent(t, committedEvent)})

	duplicateEvent := testEvent("evt-duplicate", time.Date(2026, 3, 29, 10, 5, 0, 0, time.UTC))
	duplicateEvent.RecordedAt = time.Date(2026, 3, 29, 10, 5, 0, 0, time.UTC)
	appendCommittedEvents(t, store.baseDir, duplicateEvent.OccurredAt, [][]byte{mustMarshalEvent(t, duplicateEvent)})
	require.NoError(t, store.Emit(context.Background(), duplicateEvent))

	inboxEvent := testEvent("evt-inbox", time.Date(2026, 3, 29, 10, 10, 0, 0, time.UTC))
	inboxEvent.RecordedAt = time.Date(2026, 3, 29, 10, 10, 0, 0, time.UTC)
	require.NoError(t, store.Emit(context.Background(), inboxEvent))

	events, nextCursor, err := store.ReadNotificationEvents(context.Background(), cursor)
	require.NoError(t, err)
	require.Len(t, events, 3)
	assert.Equal(t, []string{"evt-committed", "evt-duplicate", "evt-inbox"}, notificationEventIDs(events))
	assert.True(t, nextCursor.LastInboxFile > cursor.LastInboxFile)
	assert.GreaterOrEqual(t, len(nextCursor.CommittedOffsets), len(cursor.CommittedOffsets))
}

func TestStoreReadNotificationEventsQuarantinesMalformedInboxFiles(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	store, err := New(baseDir)
	require.NoError(t, err)

	cursor, err := store.NotificationHeadCursor(context.Background())
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(store.inboxDir, "00000000000000000001-bad.json"), []byte("{"), filePermissions))

	goodEvent := testEvent("evt-good", time.Date(2026, 3, 29, 10, 15, 0, 0, time.UTC))
	require.NoError(t, store.Emit(context.Background(), goodEvent))

	events, nextCursor, err := store.ReadNotificationEvents(context.Background(), cursor)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "evt-good", events[0].ID)
	assert.NotEmpty(t, nextCursor.LastInboxFile)
	_, statErr := os.Stat(filepath.Join(store.inboxDir, "00000000000000000001-bad.json"))
	assert.ErrorIs(t, statErr, os.ErrNotExist)
	_, statErr = os.Stat(filepath.Join(store.quarantineDir, "00000000000000000001-bad.json"))
	require.NoError(t, statErr)

	events, nextCursor, err = store.ReadNotificationEvents(context.Background(), nextCursor)
	require.NoError(t, err)
	assert.Empty(t, events)
	assert.NotEmpty(t, nextCursor.LastInboxFile)
}

func notificationEventIDs(events []*eventstore.Event) []string {
	ids := make([]string, 0, len(events))
	for _, event := range events {
		if event == nil {
			continue
		}
		ids = append(ids, event.ID)
	}
	return ids
}

func appendCommittedEvents(t *testing.T, baseDir string, slot time.Time, lines [][]byte) {
	t.Helper()

	path := filepath.Join(baseDir, logPrefix+slot.UTC().Format(hourFormat)+logSuffix)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, filePermissions) //nolint:gosec // test path
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	content := make([]string, 0, len(lines))
	for _, line := range lines {
		content = append(content, string(line))
	}
	_, err = f.WriteString(strings.Join(content, "\n") + "\n")
	require.NoError(t, err)
}
