// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package oauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/dagucloud/dagu/v2/internal/cmn/mailer/oauthconfig"
)

const driveScope = "https://www.googleapis.com/auth/drive.file"

func TestSourceInvalidate(t *testing.T) {
	t.Parallel()

	var fetches atomic.Int32
	fetch := func(context.Context) (*oauth2.Token, error) {
		n := fetches.Add(1)
		return &oauth2.Token{AccessToken: fmt.Sprintf("token-%d", n), Expiry: time.Now().Add(time.Hour)}, nil
	}
	source := NewFetchSource(t.Name(), fetch)
	shared := NewFetchSource(t.Name(), fetch)

	first, err := source.Token(t.Context())
	require.NoError(t, err)
	same, err := shared.Token(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "token-1", same.AccessToken, "sources with one key share the cached token")

	source.Invalidate(first)
	second, err := shared.Token(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "token-2", second.AccessToken)

	// A token that was already replaced does not drop its replacement.
	source.Invalidate(first)
	again, err := source.Token(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "token-2", again.AccessToken)
	assert.Equal(t, int32(2), fetches.Load())
}

func TestAPIRefreshTokenGrants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		cfg       oauthconfig.Config
		wantScope string
	}{
		{
			name:      "GoogleNarrowsScopes",
			cfg:       oauthconfig.Config{Provider: oauthconfig.ProviderGoogleRefresh, ClientID: "id", ClientSecret: "secret", RefreshToken: "refresh", Scopes: []string{driveScope}},
			wantScope: driveScope,
		},
		{
			name: "GoogleKeepsGrantedScopes",
			cfg:  oauthconfig.Config{Provider: oauthconfig.ProviderGoogleRefresh, ClientID: "id", ClientSecret: "secret", RefreshToken: "refresh"},
		},
		{
			name:      "MicrosoftAddsOfflineAccess",
			cfg:       oauthconfig.Config{Provider: oauthconfig.ProviderMicrosoftRefresh, ClientID: "id", RefreshToken: "refresh", Scopes: []string{"Files.Read"}},
			wantScope: "Files.Read offline_access",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			endpoint := &tokenEndpoint{respond: func(url.Values) (int, map[string]any) {
				return http.StatusOK, map[string]any{"access_token": "access", "expires_in": 3600}
			}}
			tokenURL := endpoint.start(t)
			refresh, err := apiRefresh(tt.cfg, tokenURL, tokenURL)
			require.NoError(t, err)

			token, err := refresh(t.Context(), nil)
			require.NoError(t, err)
			assert.Equal(t, "access", token.AccessToken)
			require.Len(t, endpoint.requests, 1)
			assert.Equal(t, "refresh_token", endpoint.requests[0].Get("grant_type"))
			assert.Equal(t, tt.wantScope, endpoint.requests[0].Get("scope"))
		})
	}
}

// A service account signs a JWT for the configured scopes and acts as
// itself, not on behalf of a user.
func TestAPIRefreshServiceAccount(t *testing.T) {
	t.Parallel()

	endpoint := &tokenEndpoint{respond: func(url.Values) (int, map[string]any) {
		return http.StatusOK, map[string]any{"access_token": "sa-access", "token_type": "Bearer", "expires_in": 3600}
	}}
	tokenURL := endpoint.start(t)
	cfg := oauthconfig.Config{
		Provider:           oauthconfig.ProviderGoogleServiceAccount,
		ServiceAccountJSON: serviceAccountKey(t),
		Scopes:             []string{driveScope},
	}
	refresh, err := apiRefresh(cfg, tokenURL, "")
	require.NoError(t, err)

	token, err := refresh(t.Context(), nil)
	require.NoError(t, err)
	assert.Equal(t, "sa-access", token.AccessToken)

	require.Len(t, endpoint.requests, 1)
	assert.Equal(t, "urn:ietf:params:oauth:grant-type:jwt-bearer", endpoint.requests[0].Get("grant_type"))
	parts := strings.Split(endpoint.requests[0].Get("assertion"), ".")
	require.Len(t, parts, 3)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var claims map[string]any
	require.NoError(t, json.Unmarshal(payload, &claims))
	assert.Equal(t, driveScope, claims["scope"])
	assert.Equal(t, "robot@project.iam.gserviceaccount.com", claims["iss"])
	assert.NotContains(t, claims, "sub")
}

func serviceAccountKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	data, err := json.Marshal(map[string]string{
		"type":         "service_account",
		"client_email": "robot@project.iam.gserviceaccount.com",
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
	})
	require.NoError(t, err)
	return string(data)
}
