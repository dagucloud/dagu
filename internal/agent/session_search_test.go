// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sessionSearchInput(t *testing.T, query string, limit int) json.RawMessage {
	t.Helper()
	input := map[string]any{"query": query}
	if limit > 0 {
		input["limit"] = limit
	}
	b, err := json.Marshal(input)
	require.NoError(t, err)
	return b
}

func seedSearchSession(t *testing.T, store *mockSessionStore, sess *Session, messages ...Message) {
	t.Helper()
	require.NoError(t, store.CreateSession(context.Background(), sess))
	for i := range messages {
		require.NoError(t, store.AddMessage(context.Background(), sess.ID, &messages[i]))
	}
}

func TestSessionSearchTool_Run(t *testing.T) {
	t.Parallel()

	t.Run("searches persisted sessions for current user only", func(t *testing.T) {
		t.Parallel()
		store := newMockSessionStore()
		now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
		seedSearchSession(t, store, &Session{
			ID:        "past-session",
			UserID:    "admin",
			Title:     "Import cleanup",
			DAGName:   "daily-import",
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-time.Hour),
		}, Message{
			Type:       MessageTypeUser,
			SequenceID: 1,
			Content:    "Can you debug the import failure?",
			CreatedAt:  now.Add(-2 * time.Hour),
		}, Message{
			Type:       MessageTypeAssistant,
			SequenceID: 2,
			Content:    "The fix was to write reports under DAG_RUN_ARTIFACTS_DIR.",
			CreatedAt:  now.Add(-time.Hour),
		})
		seedSearchSession(t, store, &Session{
			ID:        "current-session",
			UserID:    "admin",
			CreatedAt: now,
			UpdatedAt: now,
		}, Message{
			Type:       MessageTypeUser,
			SequenceID: 1,
			Content:    "DAG_RUN_ARTIFACTS_DIR in the current turn should not be returned.",
			CreatedAt:  now,
		})
		seedSearchSession(t, store, &Session{
			ID:        "other-user-session",
			UserID:    "other-user",
			CreatedAt: now,
			UpdatedAt: now,
		}, Message{
			Type:       MessageTypeUser,
			SequenceID: 1,
			Content:    "Other user also mentioned DAG_RUN_ARTIFACTS_DIR.",
			CreatedAt:  now,
		})

		result := NewSessionSearchTool().Run(ToolContext{
			Context:      context.Background(),
			SessionID:    "current-session",
			User:         UserIdentity{UserID: "admin"},
			SessionStore: store,
		}, sessionSearchInput(t, "dag_run_artifacts_dir", 5))

		assert.False(t, result.IsError)
		assert.Contains(t, result.Content, "past-session")
		assert.Contains(t, result.Content, "daily-import")
		assert.Contains(t, result.Content, "DAG_RUN_ARTIFACTS_DIR")
		assert.NotContains(t, result.Content, "current-session")
		assert.NotContains(t, result.Content, "other-user-session")
		assert.NotContains(t, result.Content, "Other user")
	})

	t.Run("requires query", func(t *testing.T) {
		t.Parallel()

		result := NewSessionSearchTool().Run(ToolContext{
			Context:      context.Background(),
			User:         UserIdentity{UserID: "admin"},
			SessionStore: newMockSessionStore(),
		}, sessionSearchInput(t, "   ", 0))

		assert.True(t, result.IsError)
		assert.Contains(t, result.Content, "Query is required")
	})

	t.Run("requires session store", func(t *testing.T) {
		t.Parallel()

		result := NewSessionSearchTool().Run(ToolContext{
			Context: context.Background(),
			User:    UserIdentity{UserID: "admin"},
		}, sessionSearchInput(t, "anything", 0))

		assert.True(t, result.IsError)
		assert.Contains(t, result.Content, "Session store is not available")
	})

	t.Run("returns empty result message", func(t *testing.T) {
		t.Parallel()
		store := newMockSessionStore()
		now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
		seedSearchSession(t, store, &Session{
			ID:        "past-session",
			UserID:    "admin",
			Title:     "Unrelated task",
			CreatedAt: now.Add(-time.Hour),
			UpdatedAt: now.Add(-time.Hour),
		}, Message{
			Type:       MessageTypeUser,
			SequenceID: 1,
			Content:    "This transcript does not contain the target phrase.",
			CreatedAt:  now.Add(-time.Hour),
		})

		result := NewSessionSearchTool().Run(ToolContext{
			Context:      context.Background(),
			User:         UserIdentity{UserID: "admin"},
			SessionStore: store,
		}, sessionSearchInput(t, "missing needle", 0))

		assert.False(t, result.IsError)
		assert.Contains(t, result.Content, `No matching past sessions found for "missing needle".`)
	})

	t.Run("returns metadata-only matches", func(t *testing.T) {
		t.Parallel()
		store := newMockSessionStore()
		now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
		seedSearchSession(t, store, &Session{
			ID:        "metadata-session",
			UserID:    "admin",
			Title:     "Release notes",
			DAGName:   "publish-docs",
			CreatedAt: now.Add(-2 * time.Hour),
			UpdatedAt: now.Add(-time.Hour),
		}, Message{
			Type:       MessageTypeAssistant,
			SequenceID: 1,
			Content:    "This transcript should not be shown because only metadata matched.",
			CreatedAt:  now.Add(-time.Hour),
		})

		result := NewSessionSearchTool().Run(ToolContext{
			Context:      context.Background(),
			User:         UserIdentity{UserID: "admin"},
			SessionStore: store,
		}, sessionSearchInput(t, "release", 5))

		assert.False(t, result.IsError)
		assert.Contains(t, result.Content, "metadata-session")
		assert.Contains(t, result.Content, "Release notes")
		assert.Contains(t, result.Content, "matched session metadata")
		assert.NotContains(t, result.Content, "This transcript should not be shown")
	})
}

func TestSessionSearchLimitHelpers(t *testing.T) {
	t.Parallel()

	assert.Equal(t, defaultSessionSearchLimit, normalizeSessionSearchLimit(0))
	assert.Equal(t, defaultSessionSearchLimit, normalizeSessionSearchLimit(-1))
	assert.Equal(t, 3, normalizeSessionSearchLimit(3))
	assert.Equal(t, maxSessionSearchLimit, normalizeSessionSearchLimit(maxSessionSearchLimit+1))
}

func TestPrepareSessionSearchCandidates(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	total := maxSessionSearchLimit*sessionSearchScanMultiplier + 5
	sessions := make([]*Session, 0, total)
	for i := range total {
		sessions = append(sessions, &Session{
			ID:        fmt.Sprintf("session-%03d", i),
			UserID:    "admin",
			UpdatedAt: now.Add(time.Duration(i) * time.Minute),
		})
	}

	candidates := prepareSessionSearchCandidates(sessions, maxSessionSearchLimit)

	assert.Len(t, candidates, maxSessionSearchLimit*sessionSearchScanMultiplier)
	assert.Equal(t, "session-404", candidates[0].ID)
	assert.Equal(t, "session-005", candidates[len(candidates)-1].ID)
	assert.Equal(t, "session-000", sessions[0].ID)
}
