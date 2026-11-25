// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package stepoauth_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/stepoauth"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/stepoauth/stepoauthtest"
)

const calendarScope = "https://www.googleapis.com/auth/calendar.events"

func TestValidate(t *testing.T) {
	t.Parallel()

	cloud := func() stepoauth.Config {
		return stepoauth.Config{Provider: "dagu-cloud", Account: "me@example.com", Scopes: []string{calendarScope}}
	}
	googleRefresh := func() stepoauth.Config {
		return stepoauth.Config{Provider: "google", ClientID: "id", ClientSecret: "secret", RefreshToken: "refresh"}
	}
	microsoft := func() stepoauth.Config {
		return stepoauth.Config{Provider: "microsoft", ClientID: "id", RefreshToken: "refresh", Scopes: []string{"Files.Read"}}
	}
	with := func(cfg stepoauth.Config, change func(*stepoauth.Config)) stepoauth.Config {
		change(&cfg)
		return cfg
	}

	tests := []struct {
		name    string
		cfg     stepoauth.Config
		headers map[string]string
		wantErr string
	}{
		{name: "DaguCloud", cfg: cloud()},
		{name: "GoogleRefreshToken", cfg: googleRefresh()},
		{name: "GoogleRefreshTokenWithScopes", cfg: with(googleRefresh(), func(c *stepoauth.Config) { c.Scopes = []string{calendarScope} })},
		{name: "GoogleServiceAccount", cfg: stepoauth.Config{Provider: "google", ServiceAccountJSON: "{}", Scopes: []string{calendarScope}}},
		{name: "Microsoft", cfg: microsoft()},
		{name: "OtherHeaders", cfg: cloud(), headers: map[string]string{"X-Trace": "1"}},
		{
			name:    "AuthorizationHeader",
			cfg:     cloud(),
			headers: map[string]string{"authorization": "Bearer static"},
			wantErr: "oauth cannot be combined with an Authorization header",
		},
		{name: "UnknownProvider", cfg: stepoauth.Config{Provider: "github"}, wantErr: "oauth.provider must be google, microsoft, or dagu-cloud"},
		{
			name:    "DaguCloudWithoutAccount",
			cfg:     with(cloud(), func(c *stepoauth.Config) { c.Account = "" }),
			wantErr: `oauth.account is required for provider "dagu-cloud"`,
		},
		{
			name:    "DaguCloudWithoutScopes",
			cfg:     with(cloud(), func(c *stepoauth.Config) { c.Scopes = nil }),
			wantErr: `oauth.scopes is required for provider "dagu-cloud"`,
		},
		{
			name:    "DaguCloudWithClientID",
			cfg:     with(cloud(), func(c *stepoauth.Config) { c.ClientID = "id" }),
			wantErr: `oauth.client_id is not valid for provider "dagu-cloud"`,
		},
		{
			name:    "GoogleWithoutClientSecret",
			cfg:     with(googleRefresh(), func(c *stepoauth.Config) { c.ClientSecret = "" }),
			wantErr: `oauth.client_secret is required for provider "google"`,
		},
		{
			name:    "GoogleWithoutCredentials",
			cfg:     stepoauth.Config{Provider: "google", ClientID: "id"},
			wantErr: `oauth.refresh_token or oauth.service_account_json is required for provider "google"`,
		},
		{
			name:    "GoogleWithBothCredentials",
			cfg:     with(googleRefresh(), func(c *stepoauth.Config) { c.ServiceAccountJSON = "{}" }),
			wantErr: "oauth.refresh_token and oauth.service_account_json cannot both be set",
		},
		{
			name:    "GoogleServiceAccountWithoutScopes",
			cfg:     stepoauth.Config{Provider: "google", ServiceAccountJSON: "{}"},
			wantErr: "oauth.scopes is required with oauth.service_account_json",
		},
		{
			name:    "GoogleWithTenant",
			cfg:     with(googleRefresh(), func(c *stepoauth.Config) { c.TenantID = "contoso" }),
			wantErr: `oauth.tenant_id is not valid for provider "google"`,
		},
		{
			name:    "MicrosoftWithoutScopes",
			cfg:     with(microsoft(), func(c *stepoauth.Config) { c.Scopes = nil }),
			wantErr: `oauth.scopes is required for provider "microsoft"`,
		},
		{
			name:    "EmptyScope",
			cfg:     with(cloud(), func(c *stepoauth.Config) { c.Scopes = []string{" "} }),
			wantErr: "oauth.scopes must not contain an empty scope",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.cfg.Validate(tt.headers)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tt.wantErr)
		})
	}
}

// target is a resource server that accepts only the bearer token it is given
// and records what each request carried.
type target struct {
	server *httptest.Server
	accept string

	mu     sync.Mutex
	tokens []string
	bodies []string
}

func newTarget(t *testing.T, accept string) *target {
	t.Helper()
	tg := &target{accept: accept}
	tg.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		tg.mu.Lock()
		tg.tokens = append(tg.tokens, token)
		tg.bodies = append(tg.bodies, string(body))
		tg.mu.Unlock()
		if token != tg.accept {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(tg.server.Close)
	return tg
}

func (tg *target) seen() ([]string, []string) {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	return append([]string(nil), tg.tokens...), append([]string(nil), tg.bodies...)
}

// cloudClient returns an HTTP client that authorizes requests with tokens
// from console for me@example.com.
func cloudClient(t *testing.T, console *stepoauthtest.Console) *http.Client {
	t.Helper()
	cfg := &stepoauth.Config{Provider: "dagu-cloud", Account: "me@example.com", Scopes: []string{calendarScope}}
	transport, err := stepoauth.NewTransport(console.Context(t.Context()), cfg, http.DefaultTransport)
	require.NoError(t, err)
	return &http.Client{Transport: transport}
}

func send(t *testing.T, client *http.Client, method, url, body string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
	require.NoError(t, err)
	resp, err := client.Do(req)
	if err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	return resp, err
}

func TestDaguCloudTokenIsCached(t *testing.T) {
	t.Parallel()
	console := stepoauthtest.NewConsole(t, stepoauthtest.Token("cloud-token", time.Hour))
	tg := newTarget(t, "cloud-token")
	client := cloudClient(t, console)

	for range 2 {
		resp, err := send(t, client, http.MethodGet, tg.server.URL, "")
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	}

	requests := console.Requests()
	require.Len(t, requests, 1, "the token is reused until it nears expiry")
	assert.Equal(t, stepoauthtest.TokenRequest{
		Path:            "/api/v1/servers/google/token",
		LicenseID:       stepoauthtest.LicenseID,
		ServerID:        stepoauthtest.ServerID,
		HeartbeatSecret: stepoauthtest.HeartbeatSecret,
		Account:         "me@example.com",
		Scopes:          []string{calendarScope},
	}, requests[0])
	tokens, _ := tg.seen()
	assert.Equal(t, []string{"cloud-token", "cloud-token"}, tokens)
}

// A token that expires within five minutes is replaced before the next
// request.
func TestDaguCloudTokenReplacedBeforeExpiry(t *testing.T) {
	t.Parallel()
	console := stepoauthtest.NewConsole(t,
		stepoauthtest.Token("short-lived", 4*time.Minute),
		stepoauthtest.Token("fresh", time.Hour),
	)
	tg := newTarget(t, "fresh")
	client := cloudClient(t, console)

	_, err := send(t, client, http.MethodGet, tg.server.URL, "")
	require.NoError(t, err)
	resp, err := send(t, client, http.MethodGet, tg.server.URL, "")
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Len(t, console.Requests(), 2)
}

func TestUnauthorizedRetriesOnceWithNewToken(t *testing.T) {
	t.Parallel()
	console := stepoauthtest.NewConsole(t,
		stepoauthtest.Token("revoked", time.Hour),
		stepoauthtest.Token("fresh", time.Hour),
	)
	tg := newTarget(t, "fresh")

	resp, err := send(t, cloudClient(t, console), http.MethodPost, tg.server.URL, `{"summary":"standup"}`)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	tokens, bodies := tg.seen()
	assert.Equal(t, []string{"revoked", "fresh"}, tokens)
	assert.Equal(t, []string{`{"summary":"standup"}`, `{"summary":"standup"}`}, bodies, "the retry resends the body")
}

func TestUnauthorizedRetryIsNotRepeated(t *testing.T) {
	t.Parallel()
	console := stepoauthtest.NewConsole(t, stepoauthtest.Token("rejected", time.Hour))
	tg := newTarget(t, "never")

	resp, err := send(t, cloudClient(t, console), http.MethodGet, tg.server.URL, "")
	require.NoError(t, err)

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	tokens, _ := tg.seen()
	assert.Len(t, tokens, 2)
}

func TestDaguCloudRefusalNamesConnectURL(t *testing.T) {
	t.Parallel()
	console := stepoauthtest.NewConsole(t, stepoauthtest.Refusal(http.StatusNotFound,
		"not_connected", "me@example.com is not connected.", "https://console.example/connect/google"))
	tg := newTarget(t, "unused")

	_, err := send(t, cloudClient(t, console), http.MethodGet, tg.server.URL, "")

	var tokenErr *stepoauth.TokenError
	require.ErrorAs(t, err, &tokenErr)
	assert.Equal(t,
		"oauth: Dagu Cloud cannot issue a token for me@example.com (not_connected): me@example.com is not connected; "+
			"connect the account in Dagu Console: https://console.example/connect/google",
		tokenErr.Error())
	tokens, _ := tg.seen()
	assert.Empty(t, tokens, "nothing is sent without a token")
}

func TestDaguCloudNeedsConnectedServer(t *testing.T) {
	t.Parallel()
	cfg := &stepoauth.Config{Provider: "dagu-cloud", Account: "me@example.com", Scopes: []string{calendarScope}}

	_, err := stepoauth.NewTransport(t.Context(), cfg, http.DefaultTransport)

	require.EqualError(t, err, "oauth: provider dagu-cloud needs this server connected to Dagu Cloud; connect it on the License page")
}
