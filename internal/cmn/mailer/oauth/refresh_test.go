// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/mailer/oauthconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRefreshTokenFuncRejectsSMTPOnlyProvider(t *testing.T) {
	t.Parallel()

	_, err := NewRefreshTokenFunc("ops@example.com", &oauthconfig.Config{
		Provider: oauthconfig.ProviderMicrosoft, TenantID: "tenant", ClientID: "client", ClientSecret: "secret",
	})
	require.EqualError(t, err, "oauth.provider must be google_refresh or microsoft_refresh")
}

// The posted form carries the configured client and refresh token.
func TestRefreshTokenGrant(t *testing.T) {
	t.Parallel()

	forms := make(chan url.Values, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		forms <- r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access","expires_in":3600}`))
	}))
	t.Cleanup(server.Close)

	cfg := oauthconfig.Config{ClientID: "client", ClientSecret: "secret", RefreshToken: "configured"}
	token, err := refreshTokenGrant(cfg, server.URL, microsoftMailScopes(cfg))(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, "access", token.AccessToken)

	form := <-forms
	assert.Equal(t, "refresh_token", form.Get("grant_type"))
	assert.Equal(t, "client", form.Get("client_id"))
	assert.Equal(t, "secret", form.Get("client_secret"))
	assert.Equal(t, "configured", form.Get("refresh_token"))
	assert.Equal(t, "https://outlook.office.com/.default offline_access", form.Get("scope"))
}

func TestMicrosoftMailScopes(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"https://outlook.office.com/.default", "offline_access"},
		microsoftMailScopes(oauthconfig.Config{}))
	assert.Equal(t,
		[]string{"https://outlook.office.com/IMAP.AccessAsUser.All", "https://outlook.office.com/SMTP.Send", "offline_access"},
		microsoftMailScopes(oauthconfig.Config{Scopes: []string{
			"https://outlook.office.com/IMAP.AccessAsUser.All", "https://outlook.office.com/SMTP.Send",
		}}))
}
