// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/stepoauth/stepoauthtest"
)

// oauthCfg calls toolName with a dagu-cloud oauth block instead of headers.
func (f *fixture) oauthCfg(toolName string) map[string]any {
	return map[string]any{
		"url":  f.srv.URL,
		"tool": toolName,
		"oauth": map[string]any{
			"provider": "dagu-cloud",
			"account":  "me@example.com",
			"scopes":   []any{"https://www.googleapis.com/auth/calendar.events"},
		},
	}
}

// The server rejects the first token, so the call succeeds only by retrying
// with a new one; every later request of the session reuses it.
func TestOAuthCallRetriesWithNewToken(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	console := stepoauthtest.NewConsole(t,
		stepoauthtest.Token("revoked", time.Hour),
		stepoauthtest.Token(token, time.Hour),
	)

	out, err := run(console.Context(t.Context()), t, f.oauthCfg("structured"))
	require.NoError(t, err)

	assert.Equal(t, float64(7), decode(t, out)["structured_content"].(map[string]any)["n"])
	assert.Len(t, console.Requests(), 2)
	f.mu.Lock()
	defer f.mu.Unlock()
	assert.Equal(t, "Bearer revoked", f.headers[0].Get("Authorization"))
	for _, h := range f.headers[1:] {
		assert.Equal(t, "Bearer "+token, h.Get("Authorization"))
	}
}

func TestOAuthRefusalIsAuth(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	console := stepoauthtest.NewConsole(t, stepoauthtest.Refusal(http.StatusConflict,
		"reconnect", "Google revoked access", "https://console.example/connect/google"))

	out, err := run(console.Context(t.Context()), t, f.oauthCfg("echo"))
	require.Error(t, err)

	assert.Equal(t, kindAuth, errKind(t, err))
	assert.Contains(t, err.Error(), "(reconnect): Google revoked access; connect the account in Dagu Console: https://console.example/connect/google")
	assert.Contains(t, out, `"kind":"auth"`)
	f.mu.Lock()
	defer f.mu.Unlock()
	assert.Empty(t, f.headers, "nothing is sent without a token")
}

func TestOAuthWithoutCloudConnectionIsAuth(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	_, err := run(t.Context(), t, f.oauthCfg("echo"))
	require.Error(t, err)

	assert.Equal(t, kindAuth, errKind(t, err))
	assert.Contains(t, err.Error(), "needs this server connected to Dagu Cloud")
}

func TestOAuthRejectsAuthorizationHeader(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	cfg := f.oauthCfg("echo")
	cfg["headers"] = map[string]any{"Authorization": "Bearer " + token}

	err := validateStep(mcpStep(cfg))

	require.EqualError(t, err, "mcp.call: with.oauth cannot be combined with an Authorization header")
}
