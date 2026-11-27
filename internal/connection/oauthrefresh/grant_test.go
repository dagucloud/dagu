// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package oauthrefresh

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// tokenEndpoint records refresh requests and answers them from respond.
type tokenEndpoint struct {
	mu       sync.Mutex
	requests []url.Values
	respond  func(form url.Values) (int, map[string]any)
}

func (e *tokenEndpoint) start(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		e.mu.Lock()
		e.requests = append(e.requests, r.PostForm)
		e.mu.Unlock()
		status, body := e.respond(r.PostForm)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestGrantRefresh(t *testing.T) {
	t.Parallel()

	endpoint := &tokenEndpoint{respond: func(form url.Values) (int, map[string]any) {
		return http.StatusOK, map[string]any{
			"access_token":  "access-for-" + form.Get("refresh_token"),
			"refresh_token": "rotated",
			"token_type":    "Bearer",
			"expires_in":    3600,
		}
	}}
	grant := Grant{
		TokenURL:     endpoint.start(t),
		ClientID:     "client",
		RefreshToken: "configured",
		Scopes:       []string{"https://outlook.office.com/.default", "offline_access"},
	}

	token, err := grant.Refresh(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, "access-for-configured", token.AccessToken)
	assert.WithinDuration(t, time.Now().Add(time.Hour), token.Expiry, time.Minute)

	// The next refresh uses the rotated token the provider returned.
	token, err = grant.Refresh(context.Background(), &oauth2.Token{RefreshToken: token.RefreshToken})
	require.NoError(t, err)
	assert.Equal(t, "access-for-rotated", token.AccessToken)

	require.Len(t, endpoint.requests, 2)
	first := endpoint.requests[0]
	assert.Equal(t, "refresh_token", first.Get("grant_type"))
	assert.Equal(t, "client", first.Get("client_id"))
	assert.Equal(t, "https://outlook.office.com/.default offline_access", first.Get("scope"))
	assert.False(t, first.Has("client_secret"), "a public client sends no secret")
}

func TestGrantRefreshInvalidGrant(t *testing.T) {
	t.Parallel()

	endpoint := &tokenEndpoint{respond: func(url.Values) (int, map[string]any) {
		return http.StatusBadRequest, map[string]any{
			"error":             "invalid_grant",
			"error_description": "AADSTS70000: The refresh token has expired.\r\nTrace ID: abc",
		}
	}}
	grant := Grant{TokenURL: endpoint.start(t), ClientID: "client", ClientSecret: "secret", RefreshToken: "revoked"}

	_, err := grant.Refresh(context.Background(), nil)
	var tokenErr *TokenError
	require.ErrorAs(t, err, &tokenErr)
	assert.Equal(t, "invalid_grant", tokenErr.Code)
	assert.Equal(t, "AADSTS70000: The refresh token has expired.", tokenErr.Description)
	assert.NotContains(t, err.Error(), "revoked", "errors never echo the refresh token")

	require.Len(t, endpoint.requests, 1)
	assert.Equal(t, "secret", endpoint.requests[0].Get("client_secret"))
	assert.False(t, endpoint.requests[0].Has("scope"))
}
