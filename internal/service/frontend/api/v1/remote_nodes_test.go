// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/remotenode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func guardedRemote(t *testing.T, wantToken string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if wantToken != "" && r.Header.Get("Authorization") != "Bearer "+wantToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	return srv
}

func TestTestNodeConnection(t *testing.T) {
	t.Parallel()

	t.Run("ValidTokenSucceeds", func(t *testing.T) {
		t.Parallel()

		srv := guardedRemote(t, "good-token")
		result := testNodeConnection(context.Background(), &remotenode.RemoteNode{
			APIBaseURL: srv.URL + "/api/v1",
			AuthType:   remotenode.AuthTypeToken,
			AuthToken:  "good-token",
		})

		assert.True(t, result.Success)
		require.NotNil(t, result.Message)
		assert.Contains(t, *result.Message, "credentials accepted")
	})

	// The regression: probing /health passed a node whose every real request 401s.
	t.Run("RejectedTokenFails", func(t *testing.T) {
		t.Parallel()

		srv := guardedRemote(t, "good-token")
		result := testNodeConnection(context.Background(), &remotenode.RemoteNode{
			APIBaseURL: srv.URL + "/api/v1",
			AuthType:   remotenode.AuthTypeToken,
			AuthToken:  "wrong-token",
		})

		assert.False(t, result.Success)
		require.NotNil(t, result.Error)
		assert.Contains(t, *result.Error, "rejected the credentials")
		assert.Contains(t, *result.Error, "401")
	})

	t.Run("MissingCredentialsFail", func(t *testing.T) {
		t.Parallel()

		srv := guardedRemote(t, "good-token")
		result := testNodeConnection(context.Background(), &remotenode.RemoteNode{
			APIBaseURL: srv.URL + "/api/v1",
			AuthType:   remotenode.AuthTypeNone,
		})

		assert.False(t, result.Success)
		require.NotNil(t, result.Error)
		assert.Contains(t, *result.Error, "rejected the credentials")
	})

	t.Run("UnguardedRemoteReportsCredentialsUnverified", func(t *testing.T) {
		t.Parallel()

		srv := guardedRemote(t, "")
		result := testNodeConnection(context.Background(), &remotenode.RemoteNode{
			APIBaseURL: srv.URL + "/api/v1",
			AuthType:   remotenode.AuthTypeToken,
			AuthToken:  "never-checked",
		})

		assert.True(t, result.Success)
		require.NotNil(t, result.Message)
		assert.Contains(t, *result.Message, "does not require authentication")
	})

	t.Run("UnreachableRemoteFails", func(t *testing.T) {
		t.Parallel()

		srv := guardedRemote(t, "good-token")
		url := srv.URL
		srv.Close()

		result := testNodeConnection(context.Background(), &remotenode.RemoteNode{
			APIBaseURL: url + "/api/v1",
			AuthType:   remotenode.AuthTypeToken,
			AuthToken:  "good-token",
		})

		assert.False(t, result.Success)
		require.NotNil(t, result.Error)
		assert.Contains(t, *result.Error, "Connection failed")
	})
}
