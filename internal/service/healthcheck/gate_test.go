// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package healthcheck

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrationGate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	gate, err := StartMigrationGate(ctx,
		[]GateEndpoint{{Addr: "127.0.0.1:0", HealthPath: "/api/v1/health"}},
		func() any { return map[string]any{"phase": "moving", "moved": 3} })
	require.NoError(t, err)
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			_ = gate.Stop(ctx)
		}
	})
	base := "http://" + gate.listeners[0].Addr().String()

	t.Run("HealthReportsProgress", func(t *testing.T) {
		resp, err := http.Get(base + "/api/v1/health")
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		var body struct {
			Status    string         `json:"status"`
			Migration map[string]any `json:"migration"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		assert.Equal(t, "migrating", body.Status)
		assert.Equal(t, map[string]any{"phase": "moving", "moved": float64(3)}, body.Migration)
	})

	t.Run("OtherRequestsUnavailable", func(t *testing.T) {
		for _, req := range []struct{ method, path string }{
			{http.MethodGet, "/api/v1/dags"},
			{http.MethodGet, "/"},
			{http.MethodPost, "/api/v1/health"},
		} {
			request, err := http.NewRequestWithContext(ctx, req.method, base+req.path, nil)
			require.NoError(t, err)
			resp, err := http.DefaultClient.Do(request)
			require.NoError(t, err)
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode, "%s %s", req.method, req.path)
			assert.Equal(t, gateRetryAfter, resp.Header.Get("Retry-After"))
		}
	})

	t.Run("StopFreesAddress", func(t *testing.T) {
		addr := gate.listeners[0].Addr().String()
		require.NoError(t, gate.Stop(ctx))
		stopped = true
		listener, err := net.Listen("tcp", addr)
		require.NoError(t, err)
		require.NoError(t, listener.Close())
	})
}
