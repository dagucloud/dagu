// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
)

func externalAuthzServerConfig(keysFile, pdpURL string) *config.Config {
	return &config.Config{
		DefaultExecMode: config.ExecutionModeLocal,
		Server: config.Server{
			APIBasePath: "/api/v1",
			Auth: config.Auth{
				Mode: config.AuthModeNone,
				External: config.AuthExternal{
					Enabled: true,
					Token: config.ExternalIdentityToken{
						Header: "X-Identity-Token", Issuer: "gw", Audience: "dagu",
						MaxAge: time.Minute, PublicKeysFile: keysFile,
					},
					AuthZEN: config.ExternalAuthZEN{BaseURL: pdpURL, Timeout: time.Second},
				},
			},
			AccessLog: config.AccessLogNone,
			Terminal:  config.TerminalConfig{MaxSessions: 5},
			SSE: config.SSEConfig{
				MaxTopicsPerConnection: 20,
				MaxClients:             100,
				HeartbeatInterval:      time.Hour,
			},
		},
		UI:       config.UI{MaxDashboardPageLimit: 100},
		Webhooks: config.WebhooksConfig{MaxPayloadSize: config.DefaultWebhookMaxPayloadSize},
	}
}

func TestServerExternalAuthzGatesSurfaces(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKIXPublicKey(pub)
	require.NoError(t, err)
	keysFile := filepath.Join(t.TempDir(), "keys.pem")
	require.NoError(t, os.WriteFile(keysFile, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600))

	allowed := map[string]bool{"alice settings.read server": true}
	pdp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Subject  struct{ ID string }
			Action   struct{ Name string }
			Resource struct{ Type string }
		}
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		key := strings.Join([]string{req.Subject.ID, req.Action.Name, req.Resource.Type}, " ")
		_ = json.NewEncoder(w).Encode(map[string]bool{"decision": allowed[key]})
	}))
	t.Cleanup(pdp.Close)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv, err := NewServer(ServerConfig{Context: ctx, Config: externalAuthzServerConfig(keysFile, pdp.URL)}, WithListener(listener))
	require.NoError(t, err)
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.Serve(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-serveDone:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Error("server did not shut down")
		}
	})

	client := &http.Client{
		Transport: &http.Transport{DisableKeepAlives: true},
		Timeout:   5 * time.Second,
	}
	baseURL := "http://" + listener.Addr().String()
	require.Eventually(t, func() bool {
		resp, err := client.Get(baseURL + "/api/v1/health")
		if err != nil {
			return false
		}
		return resp.Body.Close() == nil
	}, 10*time.Second, 20*time.Millisecond, "server never started serving")

	token := func(sub string) string {
		now := time.Now()
		signed, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
			"iss": "gw", "aud": "dagu", "sub": sub,
			"iat": now.Add(-time.Second).Unix(), "exp": now.Add(time.Minute).Unix(),
		}).SignedString(priv)
		require.NoError(t, err)
		return signed
	}
	status := func(method, path, tok string) int {
		req, err := http.NewRequestWithContext(ctx, method, baseURL+path, nil)
		require.NoError(t, err)
		if tok != "" {
			req.Header.Set("X-Identity-Token", tok)
		}
		resp, err := client.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode
	}

	assert.Equal(t, http.StatusOK, status(http.MethodGet, "/api/v1/health", ""))
	assert.Equal(t, http.StatusUnauthorized, status(http.MethodGet, "/api/v1/dags", ""))
	assert.Equal(t, http.StatusUnauthorized, status(http.MethodGet, "/api/v1/dags", token("alice")+"x"))
	assert.Equal(t, http.StatusForbidden, status(http.MethodGet, "/api/v1/dags", token("alice")))
	assert.Equal(t, http.StatusOK, status(http.MethodGet, "/api/v1/openapi.json", token("alice")))
	assert.Equal(t, http.StatusForbidden, status(http.MethodGet, "/api/v1/openapi.json", token("bob")))
	assert.Equal(t, http.StatusUnauthorized, status(http.MethodGet, "/api/v1/events/stream", ""))
	assert.Equal(t, http.StatusForbidden, status(http.MethodGet, "/api/v1/events/stream", token("alice")))
	assert.Equal(t, http.StatusUnauthorized, status(http.MethodPost, "/mcp", ""))
	assert.Equal(t, http.StatusForbidden, status(http.MethodPost, "/mcp", token("alice")))
}

func TestServerExternalAuthzFailsAtStart(t *testing.T) {
	cfg := externalAuthzServerConfig(filepath.Join(t.TempDir(), "missing.pem"), "http://127.0.0.1:1")
	_, err := NewServer(ServerConfig{Context: context.Background(), Config: cfg})
	require.ErrorContains(t, err, "auth.external.token.public_keys_file")
}
