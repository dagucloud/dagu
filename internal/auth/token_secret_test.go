// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTokenSecret(t *testing.T) {
	t.Run("valid key", func(t *testing.T) {
		ts, err := auth.NewTokenSecret([]byte("my-secret-key"))
		require.NoError(t, err)
		assert.True(t, ts.IsValid())
		assert.Equal(t, []byte("my-secret-key"), ts.SigningKey())
	})

	t.Run("nil key", func(t *testing.T) {
		_, err := auth.NewTokenSecret(nil)
		assert.ErrorIs(t, err, auth.ErrInvalidTokenSecret)
	})

	t.Run("empty key", func(t *testing.T) {
		_, err := auth.NewTokenSecret([]byte{})
		assert.ErrorIs(t, err, auth.ErrInvalidTokenSecret)
	})

	t.Run("defensive copy on construction", func(t *testing.T) {
		original := []byte("secret")
		ts, err := auth.NewTokenSecret(original)
		require.NoError(t, err)

		// Mutate original — TokenSecret must not be affected.
		original[0] = 'X'
		assert.Equal(t, []byte("secret"), ts.SigningKey())
	})

	t.Run("defensive copy on SigningKey output", func(t *testing.T) {
		ts, err := auth.NewTokenSecret([]byte("secret"))
		require.NoError(t, err)

		// Mutate returned key — TokenSecret must not be affected.
		key := ts.SigningKey()
		key[0] = 'X'
		assert.Equal(t, []byte("secret"), ts.SigningKey())
	})
}

func TestNewTokenSecretFromString(t *testing.T) {
	t.Run("valid string", func(t *testing.T) {
		ts, err := auth.NewTokenSecretFromString("my-secret")
		require.NoError(t, err)
		assert.True(t, ts.IsValid())
		assert.Equal(t, []byte("my-secret"), ts.SigningKey())
	})

	t.Run("empty string", func(t *testing.T) {
		_, err := auth.NewTokenSecretFromString("")
		assert.ErrorIs(t, err, auth.ErrInvalidTokenSecret)
	})
}

func TestTokenSecret_ZeroValue(t *testing.T) {
	var ts auth.TokenSecret
	assert.False(t, ts.IsValid())
	assert.Nil(t, ts.SigningKey())
}

func TestTokenSecret_Redaction(t *testing.T) {
	ts, err := auth.NewTokenSecretFromString("super-secret-key")
	require.NoError(t, err)

	t.Run("String", func(t *testing.T) {
		assert.Equal(t, "[REDACTED]", ts.String())
	})

	t.Run("GoString", func(t *testing.T) {
		assert.Equal(t, "auth.TokenSecret{[REDACTED]}", ts.GoString())
	})

	t.Run("fmt Sprintf", func(t *testing.T) {
		assert.Equal(t, "[REDACTED]", ts.String())
		assert.Equal(t, "auth.TokenSecret{[REDACTED]}", fmt.Sprintf("%#v", ts))
	})

	t.Run("MarshalJSON", func(t *testing.T) {
		data, err := json.Marshal(ts)
		require.NoError(t, err)
		assert.Equal(t, `"[REDACTED]"`, string(data))
	})

	t.Run("MarshalText", func(t *testing.T) {
		data, err := ts.MarshalText()
		require.NoError(t, err)
		assert.Equal(t, "[REDACTED]", string(data))
	})
}
