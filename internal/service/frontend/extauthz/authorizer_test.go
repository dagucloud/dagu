// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package extauthz

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/stretchr/testify/require"
)

func TestNewReadsFiles(t *testing.T) {
	t.Parallel()
	keys := newTestKeys(t)
	pdp, srv := newFakePDP(t, decisionKey("alice", "events.read", ServerResourceID))

	cfg := config.AuthExternal{
		Enabled: true,
		Token: config.ExternalIdentityToken{
			Header:         "x-identity-token",
			Issuer:         "https://gateway.example.org",
			Audience:       "dagu",
			MaxAge:         time.Hour,
			PublicKeysFile: writeFile(t, pemPublicKey(t, keys.edPub)),
		},
		AuthZEN: config.ExternalAuthZEN{
			BaseURL:   srv.URL,
			Timeout:   time.Second,
			TokenFile: writeFile(t, []byte("pdp-token\n")),
		},
	}
	authz, err := New(cfg)
	require.NoError(t, err)

	claims := validClaims()
	claims["iat"] = time.Now().Add(-time.Second).Unix()
	claims["exp"] = time.Now().Add(time.Minute).Unix()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Identity-Token", signEd(t, keys.edPriv, claims, ""))

	subject, err := authz.Authenticate(req)
	require.NoError(t, err)
	require.Equal(t, alice, subject)
	require.NoError(t, authz.Check(context.Background(), subject, ActionEventsRead, ServerResource()))
	pdp.mu.Lock()
	require.Equal(t, []string{"Bearer pdp-token"}, pdp.auth)
	pdp.mu.Unlock()

	t.Run("EmptyTokenFile", func(t *testing.T) {
		t.Parallel()
		bad := cfg
		bad.AuthZEN.TokenFile = writeFile(t, []byte("\n"))
		_, err := New(bad)
		require.ErrorContains(t, err, "auth.external.authzen.token_file")
	})
	t.Run("MissingTokenFile", func(t *testing.T) {
		t.Parallel()
		bad := cfg
		bad.AuthZEN.TokenFile = "/nonexistent/pdp-token"
		_, err := New(bad)
		require.ErrorContains(t, err, "auth.external.authzen.token_file")
	})
	t.Run("MissingKeysFile", func(t *testing.T) {
		t.Parallel()
		bad := cfg
		bad.Token.PublicKeysFile = "/nonexistent/keys.pem"
		_, err := New(bad)
		require.ErrorContains(t, err, "auth.external.token.public_keys_file")
	})
}

func newTestAuthorizer(t *testing.T, keys testKeys, allowed ...string) (*Authorizer, *fakePDP) {
	t.Helper()
	pdp, srv := newFakePDP(t, allowed...)
	return NewWith("X-Identity-Token", newTestVerifier(t, keys), NewPDPClient(PDPConfig{BaseURL: srv.URL, Timeout: time.Second})), pdp
}

func TestAuthenticateHeader(t *testing.T) {
	t.Parallel()
	keys := newTestKeys(t)
	authz, _ := newTestAuthorizer(t, keys)
	token := signEd(t, keys.edPriv, validClaims(), "")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	_, err := authz.Authenticate(req)
	require.ErrorIs(t, err, ErrMissingToken)

	req.Header.Set("X-Identity-Token", token)
	subject, err := authz.Authenticate(req)
	require.NoError(t, err)
	require.Equal(t, "alice", subject.ID)

	req.Header.Add("X-Identity-Token", token)
	_, err = authz.Authenticate(req)
	require.Error(t, err, "a repeated header is rejected")
}

func TestCheckDecisions(t *testing.T) {
	t.Parallel()
	keys := newTestKeys(t)
	authz, pdp := newTestAuthorizer(t, keys, decisionKey("alice", "runs.read", "a"))
	ctx := context.Background()

	require.NoError(t, authz.Check(ctx, alice, ActionRunsRead, WorkflowResource("a", "")))
	require.ErrorIs(t, authz.Check(ctx, alice, ActionRunsRead, WorkflowResource("b", "")), ErrDenied)

	pdp.set("", http.StatusServiceUnavailable, 0)
	err := authz.Check(ctx, alice, ActionRunsRead, WorkflowResource("a", ""))
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrDenied))
}

func TestMiddleware(t *testing.T) {
	t.Parallel()
	keys := newTestKeys(t)
	authz, pdp := newTestAuthorizer(t, keys, decisionKey("alice", "events.read", ServerResourceID))
	var seen Subject
	handler := authz.Middleware(ActionEventsRead)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = SubjectFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	serve := func(claims map[string]any) int {
		req := httptest.NewRequest(http.MethodGet, "/events/stream", nil)
		if claims != nil {
			req.Header.Set("X-Identity-Token", signEd(t, keys.edPriv, claims, ""))
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	require.Equal(t, http.StatusUnauthorized, serve(nil))
	require.Equal(t, 0, pdp.callCount(), "the PDP is not asked without a valid token")

	require.Equal(t, http.StatusNoContent, serve(validClaims()))
	require.Equal(t, alice, seen)

	bob := validClaims()
	bob["sub"] = "bob"
	require.Equal(t, http.StatusForbidden, serve(bob))
}
