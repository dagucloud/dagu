// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	store, err := New(t.TempDir())
	require.NoError(t, err)
	return store, context.Background()
}

func createTestSession(id, userID string) *agent.Session {
	now := time.Now()
	return &agent.Session{
		ID:        id,
		UserID:    userID,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func createTestMessage(id, content string, seqID int64, msgType agent.MessageType) *agent.Message {
	return &agent.Message{
		ID:         id,
		Content:    content,
		SequenceID: seqID,
		Type:       msgType,
		CreatedAt:  time.Now(),
	}
}

func TestNew(t *testing.T) {
	tests := []struct {
		name        string
		baseDir     string
		wantErr     bool
		errContains string
	}{
		{
			name:        "empty baseDir returns error",
			baseDir:     "",
			wantErr:     true,
			errContains: "cannot be empty",
		},
		{
			name:    "valid baseDir creates store",
			baseDir: t.TempDir(),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := New(tt.baseDir)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				assert.Nil(t, store)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, store)
			}
		})
	}
}

func TestNew_DirectoryCreationFailure(t *testing.T) {
	// Create a file where directory should be created
	tmpDir := t.TempDir()
	blockingFile := filepath.Join(tmpDir, "blocked")
	err := os.WriteFile(blockingFile, []byte("block"), 0600)
	require.NoError(t, err)

	// Try to create store with path that can't be a directory
	store, err := New(filepath.Join(blockingFile, "subdir"))
	require.Error(t, err)
	assert.Nil(t, store)
	assert.Contains(t, err.Error(), "failed to create directory")
}

func TestNew_WithExistingSessions(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Create first store and add data
	store1, err := New(tmpDir)
	require.NoError(t, err)

	sess := createTestSession("sess1", "user1")
	require.NoError(t, store1.CreateSession(ctx, sess))

	// Create second store - should rebuild index
	store2, err := New(tmpDir)
	require.NoError(t, err)

	// Verify data is accessible
	retrieved, err := store2.GetSession(ctx, "sess1")
	require.NoError(t, err)
	assert.Equal(t, "sess1", retrieved.ID)
}

func TestValidateSession(t *testing.T) {
	tests := []struct {
		name          string
		sess          *agent.Session
		requireUserID bool
		wantErr       error
	}{
		{
			name:          "nil session",
			sess:          nil,
			requireUserID: false,
			wantErr:       nil, // returns generic error, not sentinel
		},
		{
			name:          "empty ID",
			sess:          &agent.Session{},
			requireUserID: false,
			wantErr:       agent.ErrInvalidSessionID,
		},
		{
			name:          "empty userID when required",
			sess:          &agent.Session{ID: "123"},
			requireUserID: true,
			wantErr:       agent.ErrInvalidUserID,
		},
		{
			name:          "valid without userID check",
			sess:          &agent.Session{ID: "123"},
			requireUserID: false,
			wantErr:       nil,
		},
		{
			name:          "valid with userID",
			sess:          &agent.Session{ID: "123", UserID: "user1"},
			requireUserID: true,
			wantErr:       nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSession(tt.sess, tt.requireUserID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else if tt.sess == nil {
				assert.Error(t, err) // nil sess returns error but not a sentinel
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestStore_CreateSession(t *testing.T) {
	tests := []struct {
		name    string
		sess    *agent.Session
		wantErr error
	}{
		{
			name:    "valid session",
			sess:    createTestSession("sess1", "user1"),
			wantErr: nil,
		},
		{
			name:    "empty ID",
			sess:    &agent.Session{UserID: "user1"},
			wantErr: agent.ErrInvalidSessionID,
		},
		{
			name:    "empty userID",
			sess:    &agent.Session{ID: "sess1"},
			wantErr: agent.ErrInvalidUserID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, ctx := setupTestStore(t)
			err := store.CreateSession(ctx, tt.sess)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestStore_CreateSession_Duplicate(t *testing.T) {
	store, ctx := setupTestStore(t)

	sess := createTestSession("sess1", "user1")
	require.NoError(t, store.CreateSession(ctx, sess))

	// Try to create duplicate
	err := store.CreateSession(ctx, sess)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestStore_GetSession(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*Store, context.Context)
		id      string
		wantErr error
	}{
		{
			name: "exists",
			setup: func(s *Store, ctx context.Context) {
				_ = s.CreateSession(ctx, createTestSession("sess1", "user1"))
			},
			id:      "sess1",
			wantErr: nil,
		},
		{
			name:    "not found",
			setup:   nil,
			id:      "nonexistent",
			wantErr: agent.ErrSessionNotFound,
		},
		{
			name:    "empty ID",
			setup:   nil,
			id:      "",
			wantErr: agent.ErrInvalidSessionID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, ctx := setupTestStore(t)
			if tt.setup != nil {
				tt.setup(store, ctx)
			}

			sess, err := store.GetSession(ctx, tt.id)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, sess)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, sess)
				assert.Equal(t, tt.id, sess.ID)
			}
		})
	}
}

func TestStore_ListSessions(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*Store, context.Context)
		userID    string
		wantCount int
		wantErr   error
	}{
		{
			name:      "empty userID",
			setup:     nil,
			userID:    "",
			wantCount: 0,
			wantErr:   agent.ErrInvalidUserID,
		},
		{
			name:      "no sessions",
			setup:     nil,
			userID:    "user1",
			wantCount: 0,
			wantErr:   nil,
		},
		{
			name: "multiple sessions",
			setup: func(s *Store, ctx context.Context) {
				_ = s.CreateSession(ctx, createTestSession("sess1", "user1"))
				_ = s.CreateSession(ctx, createTestSession("sess2", "user1"))
			},
			userID:    "user1",
			wantCount: 2,
			wantErr:   nil,
		},
		{
			name: "user isolation",
			setup: func(s *Store, ctx context.Context) {
				_ = s.CreateSession(ctx, createTestSession("sess1", "user1"))
				_ = s.CreateSession(ctx, createTestSession("sess2", "user2"))
			},
			userID:    "user1",
			wantCount: 1,
			wantErr:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, ctx := setupTestStore(t)
			if tt.setup != nil {
				tt.setup(store, ctx)
			}

			sessions, err := store.ListSessions(ctx, tt.userID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
				assert.Len(t, sessions, tt.wantCount)
			}
		})
	}
}

func TestStore_UpdateSession(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*Store, context.Context) *agent.Session
		update  func(*agent.Session)
		wantErr error
	}{
		{
			name: "update title",
			setup: func(s *Store, ctx context.Context) *agent.Session {
				sess := createTestSession("sess1", "user1")
				_ = s.CreateSession(ctx, sess)
				return sess
			},
			update: func(sess *agent.Session) {
				sess.Title = "Updated Title"
				sess.UpdatedAt = time.Now()
			},
			wantErr: nil,
		},
		{
			name:  "not found",
			setup: nil,
			update: func(sess *agent.Session) {
				sess.ID = "nonexistent"
			},
			wantErr: agent.ErrSessionNotFound,
		},
		{
			name:  "empty ID",
			setup: nil,
			update: func(sess *agent.Session) {
				sess.ID = ""
			},
			wantErr: agent.ErrInvalidSessionID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, ctx := setupTestStore(t)

			var sess *agent.Session
			if tt.setup != nil {
				sess = tt.setup(store, ctx)
			} else {
				sess = &agent.Session{}
			}

			tt.update(sess)
			err := store.UpdateSession(ctx, sess)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)

				// Verify update persisted
				retrieved, err := store.GetSession(ctx, sess.ID)
				require.NoError(t, err)
				assert.Equal(t, sess.Title, retrieved.Title)
			}
		})
	}
}

func TestStore_DeleteSession(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*Store, context.Context)
		id      string
		wantErr error
	}{
		{
			name: "exists",
			setup: func(s *Store, ctx context.Context) {
				_ = s.CreateSession(ctx, createTestSession("sess1", "user1"))
			},
			id:      "sess1",
			wantErr: nil,
		},
		{
			name:    "not found",
			setup:   nil,
			id:      "nonexistent",
			wantErr: agent.ErrSessionNotFound,
		},
		{
			name:    "empty ID",
			setup:   nil,
			id:      "",
			wantErr: agent.ErrInvalidSessionID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, ctx := setupTestStore(t)
			if tt.setup != nil {
				tt.setup(store, ctx)
			}

			err := store.DeleteSession(ctx, tt.id)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)

				// Verify deletion
				_, err := store.GetSession(ctx, tt.id)
				assert.ErrorIs(t, err, agent.ErrSessionNotFound)
			}
		})
	}
}

func TestStore_AddMessage(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*Store, context.Context)
		sessionID string
		msg       *agent.Message
		wantErr   bool
		errIs     error
	}{
		{
			name: "valid message",
			setup: func(s *Store, ctx context.Context) {
				_ = s.CreateSession(ctx, createTestSession("sess1", "user1"))
			},
			sessionID: "sess1",
			msg:       createTestMessage("msg1", "Hello", 1, agent.MessageTypeUser),
			wantErr:   false,
		},
		{
			name:      "empty sessionID",
			setup:     nil,
			sessionID: "",
			msg:       createTestMessage("msg1", "Hello", 1, agent.MessageTypeUser),
			wantErr:   true,
			errIs:     agent.ErrInvalidSessionID,
		},
		{
			name:      "session not found",
			setup:     nil,
			sessionID: "nonexistent",
			msg:       createTestMessage("msg1", "Hello", 1, agent.MessageTypeUser),
			wantErr:   true,
			errIs:     agent.ErrSessionNotFound,
		},
		{
			name: "nil message",
			setup: func(s *Store, ctx context.Context) {
				_ = s.CreateSession(ctx, createTestSession("sess1", "user1"))
			},
			sessionID: "sess1",
			msg:       nil,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, ctx := setupTestStore(t)
			if tt.setup != nil {
				tt.setup(store, ctx)
			}

			err := store.AddMessage(ctx, tt.sessionID, tt.msg)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errIs != nil {
					assert.ErrorIs(t, err, tt.errIs)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestStore_GetMessages(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*Store, context.Context)
		sessionID string
		wantCount int
		wantErr   error
	}{
		{
			name: "with messages",
			setup: func(s *Store, ctx context.Context) {
				_ = s.CreateSession(ctx, createTestSession("sess1", "user1"))
				_ = s.AddMessage(ctx, "sess1", createTestMessage("msg1", "Hello", 1, agent.MessageTypeUser))
				_ = s.AddMessage(ctx, "sess1", createTestMessage("msg2", "Hi", 2, agent.MessageTypeAssistant))
			},
			sessionID: "sess1",
			wantCount: 2,
			wantErr:   nil,
		},
		{
			name: "no messages",
			setup: func(s *Store, ctx context.Context) {
				_ = s.CreateSession(ctx, createTestSession("sess1", "user1"))
			},
			sessionID: "sess1",
			wantCount: 0,
			wantErr:   nil,
		},
		{
			name:      "empty sessionID",
			setup:     nil,
			sessionID: "",
			wantCount: 0,
			wantErr:   agent.ErrInvalidSessionID,
		},
		{
			name:      "not found",
			setup:     nil,
			sessionID: "nonexistent",
			wantCount: 0,
			wantErr:   agent.ErrSessionNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, ctx := setupTestStore(t)
			if tt.setup != nil {
				tt.setup(store, ctx)
			}

			msgs, err := store.GetMessages(ctx, tt.sessionID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
				assert.Len(t, msgs, tt.wantCount)
			}
		})
	}
}

func TestStore_GetLatestSequenceID(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*Store, context.Context)
		sessionID string
		want      int64
		wantErr   error
	}{
		{
			name: "with messages",
			setup: func(s *Store, ctx context.Context) {
				_ = s.CreateSession(ctx, createTestSession("sess1", "user1"))
				_ = s.AddMessage(ctx, "sess1", createTestMessage("msg1", "Hello", 1, agent.MessageTypeUser))
				_ = s.AddMessage(ctx, "sess1", createTestMessage("msg2", "Hi", 5, agent.MessageTypeAssistant))
				_ = s.AddMessage(ctx, "sess1", createTestMessage("msg3", "Ok", 3, agent.MessageTypeUser))
			},
			sessionID: "sess1",
			want:      5,
			wantErr:   nil,
		},
		{
			name: "no messages returns 0",
			setup: func(s *Store, ctx context.Context) {
				_ = s.CreateSession(ctx, createTestSession("sess1", "user1"))
			},
			sessionID: "sess1",
			want:      0,
			wantErr:   nil,
		},
		{
			name:      "empty sessionID",
			setup:     nil,
			sessionID: "",
			want:      0,
			wantErr:   agent.ErrInvalidSessionID,
		},
		{
			name:      "not found",
			setup:     nil,
			sessionID: "nonexistent",
			want:      0,
			wantErr:   agent.ErrSessionNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, ctx := setupTestStore(t)
			if tt.setup != nil {
				tt.setup(store, ctx)
			}

			seqID, err := store.GetLatestSequenceID(ctx, tt.sessionID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, seqID)
			}
		})
	}
}

func TestTruncateTitle(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "short string",
			input: "Hello",
			want:  "Hello",
		},
		{
			name:  "exactly 50 chars",
			input: strings.Repeat("a", 50),
			want:  strings.Repeat("a", 50),
		},
		{
			name:  "51 chars gets truncated",
			input: strings.Repeat("a", 51),
			want:  strings.Repeat("a", 47) + "...",
		},
		{
			name:  "100 chars",
			input: strings.Repeat("b", 100),
			want:  strings.Repeat("b", 47) + "...",
		},
		{
			name:  "unicode characters",
			input: strings.Repeat("日", 51),
			want:  strings.Repeat("日", 47) + "...",
		},
		{
			name:  "emoji",
			input: strings.Repeat("😀", 51),
			want:  strings.Repeat("😀", 47) + "...",
		},
		{
			name:  "mixed content exceeds limit",
			input: "Hello " + strings.Repeat("世界", 30),
			want:  "", // computed dynamically below
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateTitle(tt.input)

			if tt.name == "mixed content exceeds limit" {
				// For mixed content, verify length constraint and ellipsis
				runes := []rune(got)
				assert.LessOrEqual(t, len(runes), maxTitleLength)
				assert.True(t, strings.HasSuffix(got, "..."))
			} else {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestRemoveFromSlice(t *testing.T) {
	tests := []struct {
		name   string
		slice  []string
		target string
		want   []string
	}{
		{
			name:   "empty slice",
			slice:  []string{},
			target: "a",
			want:   []string{},
		},
		{
			name:   "not found",
			slice:  []string{"a", "b", "c"},
			target: "d",
			want:   []string{"a", "b", "c"},
		},
		{
			name:   "remove first",
			slice:  []string{"a", "b", "c"},
			target: "a",
			want:   []string{"b", "c"},
		},
		{
			name:   "remove middle",
			slice:  []string{"a", "b", "c"},
			target: "b",
			want:   []string{"a", "c"},
		},
		{
			name:   "remove last",
			slice:  []string{"a", "b", "c"},
			target: "c",
			want:   []string{"a", "b"},
		},
		{
			name:   "single element found",
			slice:  []string{"a"},
			target: "a",
			want:   []string{},
		},
		{
			name:   "single element not found",
			slice:  []string{"a"},
			target: "b",
			want:   []string{"a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := removeFromSlice(tt.slice, tt.target)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSessionForStorage_SetTitleFromMessage(t *testing.T) {
	tests := []struct {
		name          string
		initialTitle  string
		msg           *agent.Message
		expectedTitle string
	}{
		{
			name:          "sets title from first user message",
			initialTitle:  "",
			msg:           createTestMessage("msg1", "Hello world", 1, agent.MessageTypeUser),
			expectedTitle: "Hello world",
		},
		{
			name:          "does not override existing title",
			initialTitle:  "Existing Title",
			msg:           createTestMessage("msg1", "New content", 1, agent.MessageTypeUser),
			expectedTitle: "Existing Title",
		},
		{
			name:          "ignores assistant messages",
			initialTitle:  "",
			msg:           createTestMessage("msg1", "Assistant response", 1, agent.MessageTypeAssistant),
			expectedTitle: "",
		},
		{
			name:          "ignores empty content",
			initialTitle:  "",
			msg:           createTestMessage("msg1", "", 1, agent.MessageTypeUser),
			expectedTitle: "",
		},
		{
			name:          "truncates long title",
			initialTitle:  "",
			msg:           createTestMessage("msg1", strings.Repeat("x", 100), 1, agent.MessageTypeUser),
			expectedTitle: strings.Repeat("x", 47) + "...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess := &SessionForStorage{
				ID:     "test",
				Title:  tt.initialTitle,
				UserID: "user1",
			}

			sess.setTitleFromMessage(tt.msg)

			assert.Equal(t, tt.expectedTitle, sess.Title)
		})
	}
}

func TestStore_RemoveSessionFromUserIndex(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(*Store, context.Context)
		id     string
		userID string
		verify func(*testing.T, *Store)
	}{
		{
			name: "remove with known userID",
			setup: func(s *Store, ctx context.Context) {
				_ = s.CreateSession(ctx, createTestSession("sess1", "user1"))
			},
			id:     "sess1",
			userID: "user1",
			verify: func(t *testing.T, s *Store) {
				s.mu.RLock()
				defer s.mu.RUnlock()
				assert.NotContains(t, s.byUser["user1"], "sess1")
			},
		},
		{
			name: "remove with unknown userID searches all users",
			setup: func(s *Store, ctx context.Context) {
				_ = s.CreateSession(ctx, createTestSession("sess1", "user1"))
			},
			id:     "sess1",
			userID: "", // unknown
			verify: func(t *testing.T, s *Store) {
				s.mu.RLock()
				defer s.mu.RUnlock()
				assert.NotContains(t, s.byUser["user1"], "sess1")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, ctx := setupTestStore(t)
			if tt.setup != nil {
				tt.setup(store, ctx)
			}

			store.mu.Lock()
			store.removeSessionFromUserIndex(tt.id, tt.userID)
			store.mu.Unlock()

			tt.verify(t, store)
		})
	}
}

func TestStore_SortUserSessions(t *testing.T) {
	store, ctx := setupTestStore(t)

	now := time.Now()
	sessions := []*agent.Session{
		{ID: "sess1", UserID: "user1", CreatedAt: now, UpdatedAt: now},
		{ID: "sess2", UserID: "user1", CreatedAt: now, UpdatedAt: now.Add(time.Hour)},
		{ID: "sess3", UserID: "user1", CreatedAt: now, UpdatedAt: now.Add(-time.Hour)},
	}

	for _, sess := range sessions {
		require.NoError(t, store.CreateSession(ctx, sess))
	}

	// Force re-sort
	store.mu.Lock()
	store.sortUserSessions("user1")
	store.mu.Unlock()

	// Verify order (most recent first)
	store.mu.RLock()
	ids := store.byUser["user1"]
	store.mu.RUnlock()

	assert.Equal(t, "sess2", ids[0]) // newest
	assert.Equal(t, "sess1", ids[1]) // middle
	assert.Equal(t, "sess3", ids[2]) // oldest
}

func TestStore_LoadSessionFromFile_Errors(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(string) string // returns file path
		wantErr     bool
		errContains string
	}{
		{
			name: "valid JSON",
			setup: func(dir string) string {
				path := filepath.Join(dir, "valid.json")
				sess := &SessionForStorage{ID: "test", UserID: "user1"}
				data, _ := json.Marshal(sess)
				_ = os.WriteFile(path, data, 0600)
				return path
			},
			wantErr: false,
		},
		{
			name: "invalid JSON",
			setup: func(dir string) string {
				path := filepath.Join(dir, "invalid.json")
				_ = os.WriteFile(path, []byte("{invalid}"), 0600)
				return path
			},
			wantErr:     true,
			errContains: "failed to parse",
		},
		{
			name: "file not found",
			setup: func(dir string) string {
				return filepath.Join(dir, "nonexistent.json")
			},
			wantErr:     true,
			errContains: "failed to read",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			store, _ := New(tmpDir)
			filePath := tt.setup(tmpDir)

			sess, err := store.loadSessionFromFile(filePath)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				assert.Nil(t, sess)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, sess)
			}
		})
	}
}

func TestStore_WriteSessionToFile_Errors(t *testing.T) {
	t.Run("write to read-only directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		store, err := New(tmpDir)
		require.NoError(t, err)

		readOnlyDir := filepath.Join(tmpDir, "readonly")
		testutil.BlockPathWithFile(t, readOnlyDir)

		sess := &SessionForStorage{ID: "test", UserID: "user1"}
		filePath := filepath.Join(readOnlyDir, "test.json")

		err = store.writeSessionToFile(filePath, sess)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create temp file")
	})

	t.Run("rename blocked by existing directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		store, err := New(tmpDir)
		require.NoError(t, err)

		// Create a directory with the target name
		targetPath := filepath.Join(tmpDir, "blocked.json")
		require.NoError(t, os.MkdirAll(targetPath, 0750))

		sess := &SessionForStorage{ID: "test", UserID: "user1"}

		err = store.writeSessionToFile(targetPath, sess)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to rename")
	})
}

func TestStore_IndexUserDirectory_Errors(t *testing.T) {
	t.Run("invalid JSON files are skipped", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create user dir with invalid file
		userDir := filepath.Join(tmpDir, "user1")
		require.NoError(t, os.MkdirAll(userDir, 0750))
		invalidFile := filepath.Join(userDir, "invalid.json")
		require.NoError(t, os.WriteFile(invalidFile, []byte("{bad}"), 0600))

		// Create valid file
		validSess := &SessionForStorage{ID: "valid", UserID: "user1"}
		validData, _ := json.Marshal(validSess)
		validFile := filepath.Join(userDir, "valid.json")
		require.NoError(t, os.WriteFile(validFile, validData, 0600))

		store, err := New(tmpDir)
		require.NoError(t, err)

		// Should have only indexed the valid file
		store.mu.RLock()
		_, exists := store.byID["valid"]
		store.mu.RUnlock()
		assert.True(t, exists)
	})

	t.Run("non-json files are skipped", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create user dir with non-json file
		userDir := filepath.Join(tmpDir, "user1")
		require.NoError(t, os.MkdirAll(userDir, 0750))
		require.NoError(t, os.WriteFile(filepath.Join(userDir, "readme.txt"), []byte("text"), 0600))

		store, err := New(tmpDir)
		require.NoError(t, err)

		store.mu.RLock()
		count := len(store.byID)
		store.mu.RUnlock()
		assert.Equal(t, 0, count)
	})

	t.Run("subdirectories in user dir are skipped", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create user dir with subdirectory
		userDir := filepath.Join(tmpDir, "user1")
		require.NoError(t, os.MkdirAll(filepath.Join(userDir, "subdir"), 0750))

		store, err := New(tmpDir)
		require.NoError(t, err)

		store.mu.RLock()
		count := len(store.byID)
		store.mu.RUnlock()
		assert.Equal(t, 0, count)
	})
}

func TestSessionForStorage_ToSession(t *testing.T) {
	now := time.Now()
	stored := &SessionForStorage{
		ID:        "test-id",
		UserID:    "user-id",
		DAGName:   "my-dag",
		Title:     "Test Title",
		CreatedAt: now,
		UpdatedAt: now,
		Messages:  []agent.Message{{ID: "msg1"}},
	}

	sess := stored.ToSession()

	assert.Equal(t, stored.ID, sess.ID)
	assert.Equal(t, stored.UserID, sess.UserID)
	assert.Equal(t, stored.DAGName, sess.DAGName)
	assert.Equal(t, stored.Title, sess.Title)
	assert.Equal(t, stored.CreatedAt, sess.CreatedAt)
	assert.Equal(t, stored.UpdatedAt, sess.UpdatedAt)
}

func TestFromSession(t *testing.T) {
	now := time.Now()
	sess := &agent.Session{
		ID:        "test-id",
		UserID:    "user-id",
		DAGName:   "my-dag",
		Title:     "Test Title",
		CreatedAt: now,
		UpdatedAt: now,
	}
	msgs := []agent.Message{{ID: "msg1"}, {ID: "msg2"}}

	stored := FromSession(sess, msgs)

	assert.Equal(t, sess.ID, stored.ID)
	assert.Equal(t, sess.UserID, stored.UserID)
	assert.Equal(t, sess.DAGName, stored.DAGName)
	assert.Equal(t, sess.Title, stored.Title)
	assert.Equal(t, sess.CreatedAt, stored.CreatedAt)
	assert.Equal(t, sess.UpdatedAt, stored.UpdatedAt)
	assert.Len(t, stored.Messages, 2)
}

func TestStore_RebuildIndex(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	// Create store and add data
	store1, err := New(tmpDir)
	require.NoError(t, err)

	sess := createTestSession("sess1", "user1")
	require.NoError(t, store1.CreateSession(ctx, sess))

	msg := createTestMessage("msg1", "Hello", 1, agent.MessageTypeUser)
	require.NoError(t, store1.AddMessage(ctx, sess.ID, msg))

	// Simulate restart
	store2, err := New(tmpDir)
	require.NoError(t, err)

	// Verify data persisted
	retrieved, err := store2.GetSession(ctx, "sess1")
	require.NoError(t, err)
	assert.Equal(t, "sess1", retrieved.ID)
	assert.Equal(t, "Hello", retrieved.Title) // auto-generated from message

	msgs, err := store2.GetMessages(ctx, "sess1")
	require.NoError(t, err)
	assert.Len(t, msgs, 1)

	sessions, err := store2.ListSessions(ctx, "user1")
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
}

func TestStore_LoadSessionByID_FileDeleted(t *testing.T) {
	store, ctx := setupTestStore(t)

	// Create session
	sess := createTestSession("sess1", "user1")
	require.NoError(t, store.CreateSession(ctx, sess))

	// Delete the file directly (simulating external deletion)
	store.mu.RLock()
	filePath := store.byID["sess1"]
	store.mu.RUnlock()
	require.NoError(t, os.Remove(filePath))

	// Try to load - should return ErrSessionNotFound
	_, err := store.GetSession(ctx, "sess1")
	assert.ErrorIs(t, err, agent.ErrSessionNotFound)
}

func TestStore_ListSessions_SkipsDeletedFiles(t *testing.T) {
	store, ctx := setupTestStore(t)

	// Create two sessions
	require.NoError(t, store.CreateSession(ctx, createTestSession("sess1", "user1")))
	require.NoError(t, store.CreateSession(ctx, createTestSession("sess2", "user1")))

	// Delete one file externally
	store.mu.RLock()
	filePath := store.byID["sess1"]
	store.mu.RUnlock()
	require.NoError(t, os.Remove(filePath))

	// List should skip the deleted session
	sessions, err := store.ListSessions(ctx, "user1")
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
	assert.Equal(t, "sess2", sessions[0].ID)
}

func TestStore_CreateSession_UserDirCreationError(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := New(tmpDir)
	require.NoError(t, err)

	// Create a file where user directory should be
	userPath := filepath.Join(tmpDir, "user1")
	require.NoError(t, os.WriteFile(userPath, []byte("block"), 0600))

	sess := createTestSession("sess1", "user1")
	err = store.CreateSession(context.Background(), sess)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create user directory")
}

func TestStore_DeleteSession_FileAlreadyDeleted(t *testing.T) {
	store, ctx := setupTestStore(t)

	// Create session
	sess := createTestSession("sess1", "user1")
	require.NoError(t, store.CreateSession(ctx, sess))

	// Delete file externally first
	store.mu.RLock()
	filePath := store.byID["sess1"]
	store.mu.RUnlock()
	require.NoError(t, os.Remove(filePath))

	// DeleteSession should still work (clean up index)
	err := store.DeleteSession(ctx, "sess1")
	require.NoError(t, err)

	// Verify removed from index
	_, err = store.GetSession(ctx, "sess1")
	assert.ErrorIs(t, err, agent.ErrSessionNotFound)
}

func TestStore_RebuildIndex_NonDirectorySkipped(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file in base dir (should be skipped, not a user directory)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "readme.txt"), []byte("text"), 0600))

	store, err := New(tmpDir)
	require.NoError(t, err)

	store.mu.RLock()
	count := len(store.byID)
	store.mu.RUnlock()
	assert.Equal(t, 0, count)
}

func TestStore_Options(t *testing.T) {
	optionCalled := false
	testOption := func(_ *Store) {
		optionCalled = true
	}

	_, err := New(t.TempDir(), testOption)
	require.NoError(t, err)
	assert.True(t, optionCalled)
}

func TestStore_CorruptedFileErrors(t *testing.T) {
	tests := []struct {
		name      string
		operation func(*Store, context.Context, *agent.Session) error
	}{
		{
			name: "UpdateSession",
			operation: func(s *Store, ctx context.Context, sess *agent.Session) error {
				sess.Title = "New Title"
				return s.UpdateSession(ctx, sess)
			},
		},
		{
			name: "AddMessage",
			operation: func(s *Store, ctx context.Context, _ *agent.Session) error {
				msg := createTestMessage("msg1", "Hello", 1, agent.MessageTypeUser)
				return s.AddMessage(ctx, "sess1", msg)
			},
		},
		{
			name: "GetMessages",
			operation: func(s *Store, ctx context.Context, _ *agent.Session) error {
				_, err := s.GetMessages(ctx, "sess1")
				return err
			},
		},
		{
			name: "DeleteSession",
			operation: func(s *Store, ctx context.Context, _ *agent.Session) error {
				return s.DeleteSession(ctx, "sess1")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, ctx := setupTestStore(t)

			sess := createTestSession("sess1", "user1")
			require.NoError(t, store.CreateSession(ctx, sess))

			// Corrupt the file
			store.mu.RLock()
			filePath := store.byID["sess1"]
			store.mu.RUnlock()
			require.NoError(t, os.WriteFile(filePath, []byte("{invalid}"), 0600))

			err := tt.operation(store, ctx, sess)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "failed to load")
		})
	}
}

func TestValidateMessageInput(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		msg       *agent.Message
		wantErr   bool
		errIs     error
	}{
		{
			name:      "valid",
			sessionID: "sess1",
			msg:       createTestMessage("msg1", "Hello", 1, agent.MessageTypeUser),
			wantErr:   false,
		},
		{
			name:      "empty session ID",
			sessionID: "",
			msg:       createTestMessage("msg1", "Hello", 1, agent.MessageTypeUser),
			wantErr:   true,
			errIs:     agent.ErrInvalidSessionID,
		},
		{
			name:      "nil message",
			sessionID: "sess1",
			msg:       nil,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMessageInput(tt.sessionID, tt.msg)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errIs != nil {
					assert.ErrorIs(t, err, tt.errIs)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestStore_SubSession_RoundTrip(t *testing.T) {
	store, ctx := setupTestStore(t)

	now := time.Now()
	sess := &agent.Session{
		ID:              "sub-1",
		UserID:          "user1",
		ParentSessionID: "parent-1",
		DelegateTask:    "analyze logs",
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	require.NoError(t, store.CreateSession(ctx, sess))

	retrieved, err := store.GetSession(ctx, "sub-1")
	require.NoError(t, err)
	assert.Equal(t, "parent-1", retrieved.ParentSessionID)
	assert.Equal(t, "analyze logs", retrieved.DelegateTask)
	assert.Equal(t, "user1", retrieved.UserID)
}

func TestFromSession_WithDelegateFields(t *testing.T) {
	now := time.Now()
	sess := &agent.Session{
		ID:              "sub-1",
		UserID:          "user1",
		ParentSessionID: "parent-1",
		DelegateTask:    "run tests",
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	stored := FromSession(sess, nil)

	assert.Equal(t, "parent-1", stored.ParentSessionID)
	assert.Equal(t, "run tests", stored.DelegateTask)
}

func TestSessionForStorage_ToSession_WithDelegateFields(t *testing.T) {
	now := time.Now()
	stored := &SessionForStorage{
		ID:              "sub-1",
		UserID:          "user1",
		ParentSessionID: "parent-1",
		DelegateTask:    "deploy",
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	sess := stored.ToSession()

	assert.Equal(t, "parent-1", sess.ParentSessionID)
	assert.Equal(t, "deploy", sess.DelegateTask)
}

func TestStore_SubSession_FilteringPattern(t *testing.T) {
	store, ctx := setupTestStore(t)

	now := time.Now()

	// Create parent session
	parent := &agent.Session{
		ID: "parent-1", UserID: "user1",
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, store.CreateSession(ctx, parent))

	// Create sub-session
	sub := &agent.Session{
		ID: "sub-1", UserID: "user1",
		ParentSessionID: "parent-1",
		DelegateTask:    "sub-task",
		CreatedAt:       now, UpdatedAt: now.Add(time.Second),
	}
	require.NoError(t, store.CreateSession(ctx, sub))

	// ListSessions returns both
	all, err := store.ListSessions(ctx, "user1")
	require.NoError(t, err)
	assert.Len(t, all, 2)

	// Filter like appendPersistedSessions does
	var topLevel []*agent.Session
	for _, s := range all {
		if s.ParentSessionID == "" {
			topLevel = append(topLevel, s)
		}
	}
	assert.Len(t, topLevel, 1)
	assert.Equal(t, "parent-1", topLevel[0].ID)
}

func TestStore_RebuildIndex_PreservesSubSessionFields(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	store1, err := New(tmpDir)
	require.NoError(t, err)

	now := time.Now()
	sess := &agent.Session{
		ID: "sub-1", UserID: "user1",
		ParentSessionID: "parent-1",
		DelegateTask:    "rebuild test",
		CreatedAt:       now, UpdatedAt: now,
	}
	require.NoError(t, store1.CreateSession(ctx, sess))

	// Simulate restart
	store2, err := New(tmpDir)
	require.NoError(t, err)

	retrieved, err := store2.GetSession(ctx, "sub-1")
	require.NoError(t, err)
	assert.Equal(t, "parent-1", retrieved.ParentSessionID)
	assert.Equal(t, "rebuild test", retrieved.DelegateTask)
}

func TestStore_SubSession_MessagesIsolated(t *testing.T) {
	store, ctx := setupTestStore(t)

	now := time.Now()
	parent := &agent.Session{
		ID: "parent-1", UserID: "user1",
		CreatedAt: now, UpdatedAt: now,
	}
	sub := &agent.Session{
		ID: "sub-1", UserID: "user1",
		ParentSessionID: "parent-1",
		CreatedAt:       now, UpdatedAt: now,
	}
	require.NoError(t, store.CreateSession(ctx, parent))
	require.NoError(t, store.CreateSession(ctx, sub))

	// Add messages to each
	require.NoError(t, store.AddMessage(ctx, "parent-1",
		createTestMessage("pmsg1", "parent message", 1, agent.MessageTypeUser)))
	require.NoError(t, store.AddMessage(ctx, "sub-1",
		createTestMessage("smsg1", "sub message", 1, agent.MessageTypeUser)))

	parentMsgs, err := store.GetMessages(ctx, "parent-1")
	require.NoError(t, err)
	assert.Len(t, parentMsgs, 1)
	assert.Equal(t, "parent message", parentMsgs[0].Content)

	subMsgs, err := store.GetMessages(ctx, "sub-1")
	require.NoError(t, err)
	assert.Len(t, subMsgs, 1)
	assert.Equal(t, "sub message", subMsgs[0].Content)
}

func setupTestStoreWithMaxPerUser(t *testing.T, maxPerUser int) (*Store, context.Context) {
	t.Helper()
	store, err := New(t.TempDir(), WithMaxPerUser(maxPerUser))
	require.NoError(t, err)
	return store, context.Background()
}

func createTestSessionWithTime(id, userID string, updatedAt time.Time) *agent.Session {
	return &agent.Session{
		ID:        id,
		UserID:    userID,
		CreatedAt: updatedAt,
		UpdatedAt: updatedAt,
	}
}

func TestCreateSession_EnforcesMaxPerUser(t *testing.T) {
	store, ctx := setupTestStoreWithMaxPerUser(t, 3)

	base := time.Now()
	// Create 5 sessions with increasing timestamps so ordering is deterministic.
	for i := range 5 {
		sess := createTestSessionWithTime(
			fmt.Sprintf("sess-%d", i),
			"user1",
			base.Add(time.Duration(i)*time.Second),
		)
		require.NoError(t, store.CreateSession(ctx, sess))
	}

	// Newest 3 should survive, oldest 2 should be deleted.
	for i := range 5 {
		_, err := store.GetSession(ctx, fmt.Sprintf("sess-%d", i))
		if i < 2 {
			assert.ErrorIs(t, err, agent.ErrSessionNotFound, "session sess-%d should have been deleted", i)
		} else {
			assert.NoError(t, err, "session sess-%d should still exist", i)
		}
	}
}

func TestCreateSession_MaxPerUser_SubSessionsNotCounted(t *testing.T) {
	store, ctx := setupTestStoreWithMaxPerUser(t, 3)

	base := time.Now()
	// Create 3 top-level sessions.
	for i := range 3 {
		sess := createTestSessionWithTime(
			fmt.Sprintf("parent-%d", i),
			"user1",
			base.Add(time.Duration(i)*time.Second),
		)
		require.NoError(t, store.CreateSession(ctx, sess))
	}

	// Create 5 sub-sessions under parent-2 — these should NOT count toward limit.
	for i := range 5 {
		sub := createTestSessionWithTime(
			fmt.Sprintf("sub-%d", i),
			"user1",
			base.Add(time.Duration(10+i)*time.Second),
		)
		sub.ParentSessionID = "parent-2"
		require.NoError(t, store.CreateSession(ctx, sub))
	}

	// All 3 parents should still exist (sub-sessions don't count).
	for i := range 3 {
		_, err := store.GetSession(ctx, fmt.Sprintf("parent-%d", i))
		assert.NoError(t, err, "parent-%d should still exist", i)
	}
}

func TestCreateSession_MaxPerUser_DeletesSubSessionsWithParent(t *testing.T) {
	store, ctx := setupTestStoreWithMaxPerUser(t, 2)

	base := time.Now()
	// Create parent-0 (oldest).
	sess0 := createTestSessionWithTime("parent-0", "user1", base)
	require.NoError(t, store.CreateSession(ctx, sess0))

	// Create 4 sub-sessions under parent-0 (3+ to catch range-mutation bugs).
	for i := range 4 {
		sub := createTestSessionWithTime(
			fmt.Sprintf("sub-%d", i),
			"user1",
			base.Add(time.Duration(1+i)*time.Second),
		)
		sub.ParentSessionID = "parent-0"
		require.NoError(t, store.CreateSession(ctx, sub))
	}

	// Create parent-1 and parent-2, pushing parent-0 beyond the limit of 2.
	sess1 := createTestSessionWithTime("parent-1", "user1", base.Add(10*time.Second))
	require.NoError(t, store.CreateSession(ctx, sess1))

	sess2 := createTestSessionWithTime("parent-2", "user1", base.Add(20*time.Second))
	require.NoError(t, store.CreateSession(ctx, sess2))

	// parent-0 should be deleted along with ALL its sub-sessions.
	_, err := store.GetSession(ctx, "parent-0")
	assert.ErrorIs(t, err, agent.ErrSessionNotFound)
	for i := range 4 {
		_, err = store.GetSession(ctx, fmt.Sprintf("sub-%d", i))
		assert.ErrorIs(t, err, agent.ErrSessionNotFound, "sub-%d should have been deleted", i)
	}

	// parent-1 and parent-2 should survive.
	_, err = store.GetSession(ctx, "parent-1")
	assert.NoError(t, err)
	_, err = store.GetSession(ctx, "parent-2")
	assert.NoError(t, err)
}

func TestCreateSession_MaxPerUser_ZeroMeansUnlimited(t *testing.T) {
	store, ctx := setupTestStoreWithMaxPerUser(t, 0)

	base := time.Now()
	for i := range 200 {
		sess := createTestSessionWithTime(
			fmt.Sprintf("sess-%d", i),
			"user1",
			base.Add(time.Duration(i)*time.Millisecond),
		)
		require.NoError(t, store.CreateSession(ctx, sess))
	}

	// All 200 should exist.
	sessions, err := store.ListSessions(ctx, "user1")
	require.NoError(t, err)
	assert.Len(t, sessions, 200)
}

func TestCreateSession_MaxPerUser_MultipleUsers(t *testing.T) {
	store, ctx := setupTestStoreWithMaxPerUser(t, 2)

	base := time.Now()
	// Create 4 sessions for user1.
	for i := range 4 {
		sess := createTestSessionWithTime(
			fmt.Sprintf("u1-sess-%d", i),
			"user1",
			base.Add(time.Duration(i)*time.Second),
		)
		require.NoError(t, store.CreateSession(ctx, sess))
	}

	// Create 4 sessions for user2.
	for i := range 4 {
		sess := createTestSessionWithTime(
			fmt.Sprintf("u2-sess-%d", i),
			"user2",
			base.Add(time.Duration(i)*time.Second),
		)
		require.NoError(t, store.CreateSession(ctx, sess))
	}

	// Each user should have exactly 2 sessions (the newest).
	sessions1, err := store.ListSessions(ctx, "user1")
	require.NoError(t, err)
	assert.Len(t, sessions1, 2)
	assert.Equal(t, "u1-sess-3", sessions1[0].ID)
	assert.Equal(t, "u1-sess-2", sessions1[1].ID)

	sessions2, err := store.ListSessions(ctx, "user2")
	require.NoError(t, err)
	assert.Len(t, sessions2, 2)
	assert.Equal(t, "u2-sess-3", sessions2[0].ID)
	assert.Equal(t, "u2-sess-2", sessions2[1].ID)
}
