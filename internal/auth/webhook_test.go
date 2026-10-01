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

func TestNewWebhook(t *testing.T) {
	t.Parallel()

	t.Run("Valid", func(t *testing.T) {
		t.Parallel()
		wh, err := NewWebhook("dag.yaml", "hash", "dagu_wh_", "creator")
		require.NoError(t, err)
		assert.NotEmpty(t, wh.ID)
		assert.Equal(t, "dag.yaml", wh.DAGName)
		assert.Equal(t, "hash", wh.TokenHash)
		assert.Equal(t, "dagu_wh_", wh.TokenPrefix)
		assert.Equal(t, "creator", wh.CreatedBy)
		assert.True(t, wh.Enabled)
		assert.Nil(t, wh.LastUsedAt)
		assert.False(t, wh.CreatedAt.IsZero())
		assert.Equal(t, wh.CreatedAt, wh.UpdatedAt)
	})

	t.Run("EmptyDAGName", func(t *testing.T) {
		t.Parallel()
		_, err := NewWebhook("", "hash", "", "")
		assert.ErrorIs(t, err, ErrInvalidWebhookDAGName)
	})

	t.Run("EmptyTokenHash", func(t *testing.T) {
		t.Parallel()
		_, err := NewWebhook("dag.yaml", "", "", "")
		assert.ErrorIs(t, err, ErrInvalidWebhookTokenHash)
	})
}

func TestWebhook_StorageRoundtrip(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	lastUsed := now.Add(-time.Hour)

	original := &Webhook{
		ID:          "id",
		DAGName:     "dag.yaml",
		TokenHash:   "hash",
		TokenPrefix: "dagu_wh_",
		Enabled:     false,
		CreatedAt:   now,
		UpdatedAt:   now,
		CreatedBy:   "creator",
		LastUsedAt:  &lastUsed,
	}

	storage := original.ToStorage()
	recovered := storage.ToWebhook()

	assert.Equal(t, original.ID, recovered.ID)
	assert.Equal(t, original.DAGName, recovered.DAGName)
	assert.Equal(t, original.TokenHash, recovered.TokenHash)
	assert.Equal(t, original.TokenPrefix, recovered.TokenPrefix)
	assert.Equal(t, original.Enabled, recovered.Enabled)
	assert.Equal(t, original.CreatedBy, recovered.CreatedBy)
	assert.Equal(t, *original.LastUsedAt, *recovered.LastUsedAt)
}

func TestWebhook_StorageRoundtripProfileTokens(t *testing.T) {
	t.Parallel()
	token := NewWebhookProfileToken("customer-a", "customer-a", "token-hash", "dagu_wh_abcd", "admin")
	wh := &Webhook{ID: "id", DAGName: "dag", TokenHash: "hash", ProfileTokens: []WebhookProfileToken{token}}

	data, err := json.Marshal(wh.ToStorage())
	require.NoError(t, err)
	assert.Contains(t, string(data), "token-hash")

	var stored WebhookForStorage
	require.NoError(t, json.Unmarshal(data, &stored))
	assert.Equal(t, []WebhookProfileToken{token}, stored.ToWebhook().ProfileTokens)
}

func TestWebhook_StorageRoundtripNilLastUsed(t *testing.T) {
	t.Parallel()
	wh := &Webhook{ID: "id", DAGName: "dag", TokenHash: "hash"}

	storage := wh.ToStorage()
	assert.Nil(t, storage.LastUsedAt)

	recovered := storage.ToWebhook()
	assert.Nil(t, recovered.LastUsedAt)
}

func TestWebhook_JSONExcludesTokenHash(t *testing.T) {
	t.Parallel()
	wh := &Webhook{
		ID:        "id",
		DAGName:   "dag.yaml",
		TokenHash: "secret-hash",
		Enabled:   true,
	}

	data, err := json.Marshal(wh)
	require.NoError(t, err)

	// TokenHash should NOT appear in JSON (json:"-" tag)
	assert.NotContains(t, string(data), "secret-hash")
	assert.NotContains(t, string(data), "tokenHash")
}

func TestWebhook_JSONExcludesProfileTokenHash(t *testing.T) {
	t.Parallel()
	wh := &Webhook{
		ID:            "id",
		DAGName:       "dag.yaml",
		TokenHash:     "hash",
		ProfileTokens: []WebhookProfileToken{NewWebhookProfileToken("a", "a", "secret-hash", "dagu_wh_abcd", "admin")},
	}

	data, err := json.Marshal(wh)
	require.NoError(t, err)
	assert.Contains(t, string(data), "dagu_wh_abcd")
	assert.NotContains(t, string(data), "secret-hash")
}

func TestWebhookForStorage_JSONIncludesTokenHash(t *testing.T) {
	t.Parallel()
	storage := &WebhookForStorage{
		ID:        "id",
		DAGName:   "dag.yaml",
		TokenHash: "secret-hash",
	}

	data, err := json.Marshal(storage)
	require.NoError(t, err)

	// TokenHash SHOULD appear in storage JSON
	assert.Contains(t, string(data), "secret-hash")
	assert.Contains(t, string(data), "tokenHash")
}
