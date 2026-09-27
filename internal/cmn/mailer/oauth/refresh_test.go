// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/mailer/oauthconfig"
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

func TestRefreshTokenGrant(t *testing.T) {
	t.Parallel()

	endpoint := &tokenEndpoint{respond: func(form url.Values) (int, map[string]any) {
		return http.StatusOK, map[string]any{
			"access_token":  "access-for-" + form.Get("refresh_token"),
			"refresh_token": "rotated",
			"token_type":    "Bearer",
			"expires_in":    3600,
		}
	}}
	cfg := oauthconfig.Config{Provider: oauthconfig.ProviderMicrosoftRefresh, ClientID: "client", RefreshToken: "configured"}
	refresh := refreshTokenGrant(cfg, endpoint.start(t), []string{microsoftMailScope, "offline_access"})

	token, err := refresh(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, "access-for-configured", token.AccessToken)
	assert.WithinDuration(t, time.Now().Add(time.Hour), token.Expiry, time.Minute)

	// The next refresh uses the rotated token the provider returned.
	token, err = refresh(context.Background(), &oauth2.Token{RefreshToken: token.RefreshToken})
	require.NoError(t, err)
	assert.Equal(t, "access-for-rotated", token.AccessToken)

	require.Len(t, endpoint.requests, 2)
	first := endpoint.requests[0]
	assert.Equal(t, "refresh_token", first.Get("grant_type"))
	assert.Equal(t, "client", first.Get("client_id"))
	assert.Equal(t, "https://outlook.office.com/.default offline_access", first.Get("scope"))
	assert.False(t, first.Has("client_secret"), "a public client sends no secret")
}

func TestRefreshTokenGrantInvalidGrant(t *testing.T) {
	t.Parallel()

	endpoint := &tokenEndpoint{respond: func(url.Values) (int, map[string]any) {
		return http.StatusBadRequest, map[string]any{
			"error":             "invalid_grant",
			"error_description": "AADSTS70000: The refresh token has expired.\r\nTrace ID: abc",
		}
	}}
	cfg := oauthconfig.Config{Provider: oauthconfig.ProviderGoogleRefresh, ClientID: "client", ClientSecret: "secret", RefreshToken: "revoked"}

	_, err := refreshTokenGrant(cfg, endpoint.start(t), nil)(context.Background(), nil)
	var tokenErr *TokenError
	require.ErrorAs(t, err, &tokenErr)
	assert.Equal(t, "invalid_grant", tokenErr.Code)
	assert.Equal(t, "AADSTS70000: The refresh token has expired.", tokenErr.Description)
	assert.NotContains(t, err.Error(), "revoked", "errors never echo the refresh token")

	require.Len(t, endpoint.requests, 1)
	assert.Equal(t, "secret", endpoint.requests[0].Get("client_secret"))
	assert.False(t, endpoint.requests[0].Has("scope"))
}

func TestMicrosoftTokenURL(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "https://login.microsoftonline.com/common/oauth2/v2.0/token", microsoftTokenURL(""))
	assert.Equal(t, "https://login.microsoftonline.com/contoso/oauth2/v2.0/token", microsoftTokenURL("contoso"))
}

func TestNewRefreshTokenFuncRejectsSMTPOnlyProvider(t *testing.T) {
	t.Parallel()

	_, err := NewRefreshTokenFunc("ops@example.com", &oauthconfig.Config{
		Provider: oauthconfig.ProviderMicrosoft, TenantID: "tenant", ClientID: "client", ClientSecret: "secret",
	})
	require.EqualError(t, err, "oauth.provider must be google_refresh or microsoft_refresh")
}

func TestMicrosoftScopes(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"https://outlook.office.com/.default", "offline_access"},
		microsoftScopes(oauthconfig.Config{}))
	assert.Equal(t,
		[]string{"https://outlook.office.com/IMAP.AccessAsUser.All", "https://outlook.office.com/SMTP.Send", "offline_access"},
		microsoftScopes(oauthconfig.Config{Scopes: []string{
			"https://outlook.office.com/IMAP.AccessAsUser.All", "https://outlook.office.com/SMTP.Send",
		}}))
	assert.Equal(t, []string{"offline_access", "https://outlook.office.com/IMAP.AccessAsUser.All"},
		microsoftScopes(oauthconfig.Config{Scopes: []string{"offline_access", "https://outlook.office.com/IMAP.AccessAsUser.All"}}),
		"offline_access is not added twice")
}
