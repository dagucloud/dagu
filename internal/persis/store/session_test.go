// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
)

func newSessionStore(t *testing.T, opts ...store.SessionOption) *store.SessionStore {
	t.Helper()
	col := testutil.NewMemoryBackend().Collection("sessions")
	s, err := store.NewSessionStore(col, opts...)
	require.NoError(t, err)
	return s
}

func newSession(userID, id string) *agent.Session {
	now := time.Now().UTC()
	return &agent.Session{
		ID:        id,
		UserID:    userID,
		Model:     "gpt-4",
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestSessionCreateAndGet(t *testing.T) {
	ctx := context.Background()
	s := newSessionStore(t)
	sess := newSession("user-1", "sess-1")

	require.NoError(t, s.CreateSession(ctx, sess))

	got, err := s.GetSession(ctx, "sess-1")
	require.NoError(t, err)
	assert.Equal(t, "sess-1", got.ID)
	assert.Equal(t, "user-1", got.UserID)
}

func TestSessionGetSession_NotFound(t *testing.T) {
	ctx := context.Background()
	_, err := newSessionStore(t).GetSession(ctx, "missing")
	assert.ErrorIs(t, err, agent.ErrSessionNotFound)
}

func TestSessionListSessions(t *testing.T) {
	ctx := context.Background()
	s := newSessionStore(t)

	for _, id := range []string{"s1", "s2", "s3"} {
		require.NoError(t, s.CreateSession(ctx, newSession("alice", id)))
	}

	list, err := s.ListSessions(ctx, "alice")
	require.NoError(t, err)
	assert.Len(t, list, 3)

	list2, err := s.ListSessions(ctx, "bob")
	require.NoError(t, err)
	assert.Empty(t, list2)
}

func TestSessionUpdateSession(t *testing.T) {
	ctx := context.Background()
	s := newSessionStore(t)
	sess := newSession("u1", "s1")
	require.NoError(t, s.CreateSession(ctx, sess))

	sess.Title = "Updated Title"
	sess.UpdatedAt = time.Now().UTC()
	require.NoError(t, s.UpdateSession(ctx, sess))

	got, err := s.GetSession(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, "Updated Title", got.Title)
}

func TestSessionUpdateSession_NotFound(t *testing.T) {
	ctx := context.Background()
	assert.ErrorIs(t, newSessionStore(t).UpdateSession(ctx, newSession("u", "ghost")), agent.ErrSessionNotFound)
}

func TestSessionDeleteSession(t *testing.T) {
	ctx := context.Background()
	s := newSessionStore(t)
	sess := newSession("u1", "s1")
	require.NoError(t, s.CreateSession(ctx, sess))

	require.NoError(t, s.DeleteSession(ctx, "s1"))

	_, err := s.GetSession(ctx, "s1")
	assert.ErrorIs(t, err, agent.ErrSessionNotFound)

	list, err := s.ListSessions(ctx, "u1")
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestSessionDeleteSession_NotFound(t *testing.T) {
	ctx := context.Background()
	assert.ErrorIs(t, newSessionStore(t).DeleteSession(ctx, "nope"), agent.ErrSessionNotFound)
}

func TestSessionAddMessage(t *testing.T) {
	ctx := context.Background()
	s := newSessionStore(t)
	sess := newSession("u1", "s1")
	require.NoError(t, s.CreateSession(ctx, sess))

	msg := &agent.Message{
		SequenceID: 1,
		Type:       agent.MessageTypeUser,
		Content:    "hello",
	}
	require.NoError(t, s.AddMessage(ctx, "s1", msg))

	messages, err := s.GetMessages(ctx, "s1")
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.Equal(t, "hello", messages[0].Content)
}

func TestSessionAddMessage_SetsTitle(t *testing.T) {
	ctx := context.Background()
	s := newSessionStore(t)
	sess := newSession("u1", "s1")
	require.NoError(t, s.CreateSession(ctx, sess))

	require.NoError(t, s.AddMessage(ctx, "s1", &agent.Message{
		Type:    agent.MessageTypeUser,
		Content: "my question",
	}))

	got, err := s.GetSession(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, "my question", got.Title)
}

func TestSessionGetLatestSequenceID(t *testing.T) {
	ctx := context.Background()
	s := newSessionStore(t)
	sess := newSession("u1", "s1")
	require.NoError(t, s.CreateSession(ctx, sess))

	n, err := s.GetLatestSequenceID(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)

	for i := int64(1); i <= 3; i++ {
		require.NoError(t, s.AddMessage(ctx, "s1", &agent.Message{SequenceID: i, Type: agent.MessageTypeUser}))
	}

	n, err = s.GetLatestSequenceID(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, int64(3), n)
}

func TestSessionListSubSessions(t *testing.T) {
	ctx := context.Background()
	s := newSessionStore(t)
	parent := newSession("u1", "parent")
	require.NoError(t, s.CreateSession(ctx, parent))

	child1 := newSession("u1", "child-1")
	child1.ParentSessionID = "parent"
	child2 := newSession("u1", "child-2")
	child2.ParentSessionID = "parent"
	require.NoError(t, s.CreateSession(ctx, child1))
	require.NoError(t, s.CreateSession(ctx, child2))

	subs, err := s.ListSubSessions(ctx, "parent")
	require.NoError(t, err)
	assert.Len(t, subs, 2)
}

func TestSessionMaxPerUser(t *testing.T) {
	ctx := context.Background()
	s := newSessionStore(t, store.WithMaxPerUser(2))

	for _, id := range []string{"s1", "s2", "s3"} {
		require.NoError(t, s.CreateSession(ctx, newSession("alice", id)))
	}

	list, err := s.ListSessions(ctx, "alice")
	require.NoError(t, err)
	assert.Len(t, list, 2)
}

func TestAddMessage_Concurrent(t *testing.T) {
	ctx := context.Background()
	s := newSessionStore(t)
	sess := newSession("u1", "concurrent-sess")
	require.NoError(t, s.CreateSession(ctx, sess))

	const N = 20
	var wg sync.WaitGroup
	wg.Add(N)
	for i := range N {
		go func() {
			defer wg.Done()
			msg := &agent.Message{
				SequenceID: int64(i + 1),
				Type:       agent.MessageTypeUser,
				Content:    fmt.Sprintf("message-%d", i),
			}
			if err := s.AddMessage(ctx, "concurrent-sess", msg); err != nil {
				t.Errorf("AddMessage failed: %v", err)
			}
		}()
	}
	wg.Wait()

	messages, err := s.GetMessages(ctx, "concurrent-sess")
	require.NoError(t, err)
	assert.Len(t, messages, N)
}

func TestSessionIndexRebuiltOnStartup(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("sessions")

	s1, err := store.NewSessionStore(col)
	require.NoError(t, err)
	require.NoError(t, s1.CreateSession(ctx, newSession("alice", "s1")))
	require.NoError(t, s1.CreateSession(ctx, newSession("alice", "s2")))
	child := newSession("alice", "child-1")
	child.ParentSessionID = "s1"
	require.NoError(t, s1.CreateSession(ctx, child))

	s2, err := store.NewSessionStore(col)
	require.NoError(t, err)

	list, err := s2.ListSessions(ctx, "alice")
	require.NoError(t, err)
	assert.Len(t, list, 3)

	subs, err := s2.ListSubSessions(ctx, "s1")
	require.NoError(t, err)
	assert.Len(t, subs, 1)
}
