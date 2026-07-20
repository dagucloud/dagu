// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewUser(t *testing.T) {
	t.Run("creates user with all fields", func(t *testing.T) {
		before := time.Now().UTC()
		user := NewUser("testuser", "hashedpassword", RoleManager)
		after := time.Now().UTC()

		assert.NotEmpty(t, user.ID)
		assert.Equal(t, "testuser", user.Username)
		assert.Equal(t, "hashedpassword", user.PasswordHash)
		assert.Equal(t, RoleManager, user.Role)

		assert.True(t, !user.CreatedAt.Before(before))
		assert.True(t, !user.CreatedAt.After(after))
		assert.Equal(t, user.CreatedAt, user.UpdatedAt)
	})

	t.Run("generates unique IDs", func(t *testing.T) {
		ids := make(map[string]bool)
		for range 100 {
			user := NewUser("user", "hash", RoleViewer)
			assert.False(t, ids[user.ID], "ID should be unique")
			ids[user.ID] = true
		}
	})

	t.Run("supports all roles", func(t *testing.T) {
		roles := []Role{RoleAdmin, RoleManager, RoleDeveloper, RoleOperator, RoleViewer}
		for _, role := range roles {
			user := NewUser("user", "hash", role)
			assert.Equal(t, role, user.Role)
		}
	})

	t.Run("allows empty username", func(t *testing.T) {
		user := NewUser("", "hash", RoleViewer)
		assert.Empty(t, user.Username)
		assert.NotEmpty(t, user.ID)
	})

	t.Run("allows empty password hash", func(t *testing.T) {
		user := NewUser("user", "", RoleViewer)
		assert.Empty(t, user.PasswordHash)
	})
}

func TestUser_ToStorage(t *testing.T) {
	t.Run("converts all fields", func(t *testing.T) {
		now := time.Now().UTC()
		user := &User{
			ID:           "user-123",
			Username:     "admin",
			PasswordHash: "secret-hash",
			Role:         RoleAdmin,
			CreatedAt:    now,
			UpdatedAt:    now.Add(time.Hour),
		}

		storage := user.ToStorage()

		assert.Equal(t, user.ID, storage.ID)
		assert.Equal(t, user.Username, storage.Username)
		assert.Equal(t, user.PasswordHash, storage.PasswordHash)
		assert.Equal(t, user.Role, storage.Role)
		assert.Equal(t, user.CreatedAt, storage.CreatedAt)
		assert.Equal(t, user.UpdatedAt, storage.UpdatedAt)
	})

	t.Run("handles empty fields", func(t *testing.T) {
		user := &User{}
		storage := user.ToStorage()

		assert.Empty(t, storage.ID)
		assert.Empty(t, storage.Username)
		assert.Empty(t, storage.PasswordHash)
	})
}

func TestUserForStorage_ToUser(t *testing.T) {
	t.Run("converts all fields", func(t *testing.T) {
		now := time.Now().UTC()
		storage := &UserForStorage{
			ID:           "storage-456",
			Username:     "operator",
			PasswordHash: "stored-hash",
			Role:         RoleOperator,
			CreatedAt:    now,
			UpdatedAt:    now.Add(2 * time.Hour),
		}

		user := storage.ToUser()

		assert.Equal(t, storage.ID, user.ID)
		assert.Equal(t, storage.Username, user.Username)
		assert.Equal(t, storage.PasswordHash, user.PasswordHash)
		assert.Equal(t, storage.Role, user.Role)
		assert.Equal(t, storage.CreatedAt, user.CreatedAt)
		assert.Equal(t, storage.UpdatedAt, user.UpdatedAt)
	})

	t.Run("handles empty fields", func(t *testing.T) {
		storage := &UserForStorage{}
		user := storage.ToUser()

		assert.Empty(t, user.ID)
		assert.Empty(t, user.Username)
		assert.Empty(t, user.PasswordHash)
	})
}

func TestUser_StorageRoundtrip(t *testing.T) {
	t.Run("preserves all fields through roundtrip", func(t *testing.T) {
		now := time.Now().UTC()
		original := &User{
			ID:                 "roundtrip-id",
			Username:           "roundtrip-user",
			PasswordHash:       "roundtrip-hash",
			Role:               RoleManager,
			CreatedAt:          now,
			UpdatedAt:          now.Add(time.Minute),
			AuthProvider:       AuthProviderProxy,
			TrustedProxySource: "edge-cluster",
			TrustedProxyUser:   "opaque-id",
		}

		storage := original.ToStorage()
		recovered := storage.ToUser()

		assert.Equal(t, original.ID, recovered.ID)
		assert.Equal(t, original.Username, recovered.Username)
		assert.Equal(t, original.PasswordHash, recovered.PasswordHash)
		assert.Equal(t, original.Role, recovered.Role)
		assert.Equal(t, original.CreatedAt, recovered.CreatedAt)
		assert.Equal(t, original.UpdatedAt, recovered.UpdatedAt)
		assert.Equal(t, original.AuthProvider, recovered.AuthProvider)
		assert.Equal(t, original.TrustedProxySource, recovered.TrustedProxySource)
		assert.Equal(t, original.TrustedProxyUser, recovered.TrustedProxyUser)
	})
}

func TestUserCanUsePassword(t *testing.T) {
	tests := []struct {
		provider string
		expected bool
	}{
		{provider: "", expected: true},
		{provider: AuthProviderBuiltin, expected: true},
		{provider: AuthProviderOIDC, expected: false},
		{provider: AuthProviderProxy, expected: false},
	}
	for _, tc := range tests {
		t.Run(tc.provider, func(t *testing.T) {
			assert.Equal(t, tc.expected, (&User{AuthProvider: tc.provider}).CanUsePassword())
		})
	}
}

func TestUser_JSONSerialization(t *testing.T) {
	t.Run("excludes password hash", func(t *testing.T) {
		user := &User{
			ID:           "json-id",
			Username:     "jsonuser",
			PasswordHash: "should-be-excluded",
			Role:         RoleViewer,
		}

		data, err := json.Marshal(user)
		require.NoError(t, err)

		jsonStr := string(data)
		assert.NotContains(t, jsonStr, "should-be-excluded")
		assert.NotContains(t, jsonStr, "password_hash")
		assert.NotContains(t, jsonStr, "PasswordHash")
	})

	t.Run("includes other fields", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Second)
		user := &User{
			ID:        "json-id",
			Username:  "jsonuser",
			Role:      RoleAdmin,
			CreatedAt: now,
			UpdatedAt: now,
		}

		data, err := json.Marshal(user)
		require.NoError(t, err)

		var recovered User
		err = json.Unmarshal(data, &recovered)
		require.NoError(t, err)

		assert.Equal(t, user.ID, recovered.ID)
		assert.Equal(t, user.Username, recovered.Username)
		assert.Equal(t, user.Role, recovered.Role)
		assert.Empty(t, recovered.PasswordHash)
	})
}

func TestUserForStorage_JSONSerialization(t *testing.T) {
	t.Run("includes password hash", func(t *testing.T) {
		storage := &UserForStorage{
			ID:           "storage-id",
			Username:     "storageuser",
			PasswordHash: "included-hash",
			Role:         RoleManager,
		}

		data, err := json.Marshal(storage)
		require.NoError(t, err)

		jsonStr := string(data)
		assert.Contains(t, jsonStr, "included-hash")
		assert.Contains(t, jsonStr, "password_hash")
	})

	t.Run("roundtrip preserves all fields", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Second)
		original := &UserForStorage{
			ID:           "storage-rt-id",
			Username:     "storage-rt-user",
			PasswordHash: "storage-rt-hash",
			Role:         RoleOperator,
			CreatedAt:    now,
			UpdatedAt:    now,
		}

		data, err := json.Marshal(original)
		require.NoError(t, err)

		var recovered UserForStorage
		err = json.Unmarshal(data, &recovered)
		require.NoError(t, err)

		assert.Equal(t, original.ID, recovered.ID)
		assert.Equal(t, original.Username, recovered.Username)
		assert.Equal(t, original.PasswordHash, recovered.PasswordHash)
		assert.Equal(t, original.Role, recovered.Role)
	})
}
