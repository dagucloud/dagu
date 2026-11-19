// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aiendpoint_test

import (
	"context"
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

// An error about a URL does not repeat its query, its fragment or its
// password, where a pasted URL can hold a key, also when the URL does not
// parse.
func TestURLErrorsHideQueryAndFragment(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	stores := newSharedStores(t)
	endpoints, err := aiendpoint.NewService(stores.endpoints, stores.secrets)
	require.NoError(t, err)
	servers, err := aiendpoint.NewMCPService(stores.servers, stores.secrets)
	require.NoError(t, err)

	const leak = "sk-leak-0123456789"
	withPassword := []string{
		"https://user:" + leak + "@api.example.com/%ZZ",
		"https://user:" + leak + "@api example.com/v1",
		"user:" + leak + "@api.example.com/v1",
		// The ? or # of the password comes before the @.
		"https://user:" + leak + "?x@api.example.com/%ZZ",
		"https://user:" + leak + "#x@api.example.com/%ZZ",
	}
	for _, raw := range append([]string{
		"https://api.example.com/v1?key=" + leak,
		"https://api.example.com/v1#" + leak,
		"ftp://api.example.com/v1?key=" + leak,
		"http://api.example.com/v1?key=" + leak,
		"api.example.com/v1?key=" + leak,
	}, withPassword...) {
		in := raceEndpointInput(raceKeyWinner)
		in.BaseURL = raw
		_, err := endpoints.Create(ctx, in)
		require.ErrorIs(t, err, aiendpoint.ErrInvalid, raw)
		assert.NotContains(t, err.Error(), leak, raw)
	}

	for _, raw := range append([]string{"ftp://docs.example.com/mcp?token=" + leak}, withPassword...) {
		in := raceMCPInput(raceKeyWinner)
		in.URL = raw
		_, err = servers.Create(ctx, in)
		require.ErrorIs(t, err, aiendpoint.ErrInvalid, raw)
		assert.NotContains(t, err.Error(), leak, raw)
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
