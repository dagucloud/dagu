// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aiendpoint_test

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
)

// The prefix is the smaller of 6 characters and 10 percent of the key length,
// rounded down. A key of fewer than 10 characters has no prefix.
func TestKeyDisplayPrefixLength(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		length int
		prefix int
	}{
		{1, 0}, {9, 0}, {10, 1}, {19, 1}, {20, 2}, {35, 3}, {59, 5}, {60, 6}, {61, 6}, {200, 6},
	} {
		key := strings.Repeat("k", tc.length)
		prefix, _ := aiendpoint.KeyDisplay(key)
		assert.Len(t, prefix, tc.prefix, "key of %d characters", tc.length)
	}

	prefix, _ := aiendpoint.KeyDisplay("sk-ant-api03-" + strings.Repeat("x", 80))
	assert.Equal(t, "sk-ant", prefix)
}

func TestKeyDisplayFingerprint(t *testing.T) {
	t.Parallel()

	key := "sk-ant-api03-secret-value"
	sum := sha256.Sum256([]byte(key))
	_, fingerprint := aiendpoint.KeyDisplay(key)
	assert.Equal(t, hex.EncodeToString(sum[:])[:12], fingerprint)
	assert.Len(t, fingerprint, 12)

	_, other := aiendpoint.KeyDisplay(key + "x")
	assert.NotEqual(t, fingerprint, other)
}

func TestValidateKey(t *testing.T) {
	t.Parallel()

	require.NoError(t, aiendpoint.ValidateKey("sk-123"))
	require.NoError(t, aiendpoint.ValidateKey(strings.Repeat("k", aiendpoint.MaxKeyBytes)))
	for name, key := range map[string]string{
		"empty":           "",
		"too long":        strings.Repeat("k", aiendpoint.MaxKeyBytes+1),
		"newline":         "sk-1\n23",
		"tab":             "sk-1\t23",
		"nul":             "sk-1\x0023",
		"invalid utf-8":   "sk-\xff\xfe",
		"delete":          "sk-\x7f",
		"c1 control rune": "sk-\u0085",
	} {
		err := aiendpoint.ValidateKey(key)
		require.ErrorIs(t, err, aiendpoint.ErrInvalid, name)
		if key != "" {
			assert.NotContains(t, err.Error(), key, name)
		}
	}
}

func TestValidateName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"cle1", "a", "my-endpoint-2", strings.Repeat("a", 64)} {
		require.NoError(t, aiendpoint.ValidateName(name), name)
	}
	for _, name := range []string{"", "1abc", "Abc", "a_b", "-a", "a b", "a/b", strings.Repeat("a", 65)} {
		require.ErrorIs(t, aiendpoint.ValidateName(name), aiendpoint.ErrInvalid, name)
	}
}

func TestParseScope(t *testing.T) {
	t.Parallel()

	scope, err := aiendpoint.ParseScope("global")
	require.NoError(t, err)
	assert.True(t, scope.IsGlobal())
	assert.Equal(t, "global", scope.String())
	assert.Equal(t, ".global", scope.SecretWorkspace())

	scope, err = aiendpoint.ParseScope("workspace:team_a")
	require.NoError(t, err)
	assert.Equal(t, "team_a", scope.Workspace)
	assert.Equal(t, "workspace:team_a", scope.String())
	assert.Equal(t, "team_a", scope.SecretWorkspace())

	for _, raw := range []string{"", "workspace:", "workspace:all", "workspace:a/b", "team", "Global", "workspace:default"} {
		_, err := aiendpoint.ParseScope(raw)
		require.ErrorIs(t, err, aiendpoint.ErrInvalid, raw)
	}
}

func TestLookupPreset(t *testing.T) {
	t.Parallel()

	row, ok := aiendpoint.LookupPreset("deepseek", aiendpoint.ProtocolAnthropic)
	require.True(t, ok)
	assert.Equal(t, "https://api.deepseek.com/anthropic", row.BaseURL)
	assert.Equal(t, "anthropic", row.ChatType)

	_, ok = aiendpoint.LookupPreset("anthropic", aiendpoint.ProtocolOpenAI)
	assert.False(t, ok, "the preset anthropic has one row")
	_, ok = aiendpoint.LookupPreset("xai", aiendpoint.ProtocolAnthropic)
	assert.False(t, ok, "the table has no anthropic row for xai")
	_, ok = aiendpoint.LookupPreset("nope", aiendpoint.ProtocolOpenAI)
	assert.False(t, ok)

	custom, ok := aiendpoint.LookupPreset(aiendpoint.PresetCustom, aiendpoint.ProtocolOpenAI)
	require.True(t, ok)
	assert.Empty(t, custom.BaseURL)

	for _, row := range aiendpoint.Presets() {
		assert.NotEmpty(t, row.ChatType, row.Name)
		assert.Contains(t, []aiendpoint.Auth{aiendpoint.AuthXAPIKey, aiendpoint.AuthBearer}, row.Auth, row.Name)
		if row.BaseURL != "" {
			assert.True(t, strings.HasPrefix(row.BaseURL, "https://") || (row.Local && strings.HasPrefix(row.BaseURL, "http://")), row.Name)
		}
	}
}
