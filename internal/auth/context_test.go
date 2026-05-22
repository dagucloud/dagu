// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithUser(t *testing.T) {
	t.Run("stores user in context", func(t *testing.T) {
		user := NewUser("testuser", "hash", RoleAdmin)
		ctx := WithUser(context.Background(), user)

		retrieved, ok := UserFromContext(ctx)
		require.True(t, ok)
		assert.Equal(t, user.ID, retrieved.ID)
		assert.Equal(t, user.Username, retrieved.Username)
		assert.Equal(t, user.Role, retrieved.Role)
	})

	t.Run("stores nil user in context", func(t *testing.T) {
		ctx := WithUser(context.Background(), nil)

		retrieved, ok := UserFromContext(ctx)
		assert.True(t, ok)
		assert.Nil(t, retrieved)
	})
}

func TestUserFromContext(t *testing.T) {
	t.Run("returns user when present", func(t *testing.T) {
		user := NewUser("admin", "hash", RoleManager)
		ctx := WithUser(context.Background(), user)

		retrieved, ok := UserFromContext(ctx)
		require.True(t, ok)
		assert.Equal(t, user, retrieved)
	})

	t.Run("returns false when user not present", func(t *testing.T) {
		ctx := context.Background()

		retrieved, ok := UserFromContext(ctx)
		assert.False(t, ok)
		assert.Nil(t, retrieved)
	})

	t.Run("returns false when wrong type in context", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), userContextKey{}, "not a user")

		retrieved, ok := UserFromContext(ctx)
		assert.False(t, ok)
		assert.Nil(t, retrieved)
	})

	t.Run("preserves user through context chain", func(t *testing.T) {
		user := NewUser("chainuser", "hash", RoleViewer)
		ctx := WithUser(context.Background(), user)

		// Add more values to the context chain using a typed key
		type otherKey struct{}
		ctx = context.WithValue(ctx, otherKey{}, "other_value")

		retrieved, ok := UserFromContext(ctx)
		require.True(t, ok)
		assert.Equal(t, user.Username, retrieved.Username)
	})

	t.Run("latest user overwrites previous", func(t *testing.T) {
		user1 := NewUser("user1", "hash1", RoleAdmin)
		user2 := NewUser("user2", "hash2", RoleViewer)

		ctx := WithUser(context.Background(), user1)
		ctx = WithUser(ctx, user2)

		retrieved, ok := UserFromContext(ctx)
		require.True(t, ok)
		assert.Equal(t, user2.Username, retrieved.Username)
		assert.Equal(t, user2.Role, retrieved.Role)
	})
}
