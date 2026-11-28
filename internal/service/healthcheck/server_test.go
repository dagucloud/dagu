// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package healthcheck

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServer(t *testing.T) {
	t.Run("StartStop", func(t *testing.T) {
		hs := NewServerWithAddr("test-service", "127.0.0.1:0")
		ctx := context.Background()

		err := hs.Start(ctx)
		require.NoError(t, err)

		resp, err := http.Get(hs.URL() + "/health")
		require.NoError(t, err)
		defer func() {
			err := resp.Body.Close()
			assert.NoError(t, err)
		}()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

		var healthResp Response
		err = json.NewDecoder(resp.Body).Decode(&healthResp)
		require.NoError(t, err)
		assert.Equal(t, "healthy", healthResp.Status)

		err = hs.Stop(ctx)
		require.NoError(t, err)
	})

	t.Run("DisabledWhenPortIsZero", func(t *testing.T) {
		hs := NewServer("test-service", 0)
		ctx := context.Background()

		err := hs.Start(ctx)
		require.NoError(t, err)
		assert.Empty(t, hs.URL())

		err = hs.Stop(ctx)
		require.NoError(t, err)
	})
}

// A server started again while it runs keeps serving on its port, as a
// worker's health check does once the worker that started it early starts.
func TestServerStartWhileRunning(t *testing.T) {
	hs := NewServerWithAddr("test-service", "127.0.0.1:0")
	require.NoError(t, hs.Start(t.Context()))
	url := hs.URL() + "/health"
	require.NoError(t, hs.Start(t.Context()))
	assert.Equal(t, url, hs.URL()+"/health")

	resp, err := http.Get(url)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, hs.Stop(t.Context()))
}

// The readiness endpoint answers 503 while the ready function reports
// false, and 200 otherwise, while the health endpoint answers 200
// throughout.
func TestServerReady(t *testing.T) {
	var ready atomic.Bool
	hs := NewServerWithAddr("test-service", "127.0.0.1:0")
	hs.SetReadyFunc(ready.Load)
	require.NoError(t, hs.Start(t.Context()))
	t.Cleanup(func() { _ = hs.Stop(context.Background()) })

	get := func(path string) int {
		resp, err := http.Get(hs.URL() + path)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode
	}
	assert.Equal(t, http.StatusOK, get(Path))
	assert.Equal(t, http.StatusServiceUnavailable, get(ReadyPath))
	ready.Store(true)
	assert.Equal(t, http.StatusOK, get(Path))
	assert.Equal(t, http.StatusOK, get(ReadyPath))

	plain := NewServerWithAddr("test-service", "127.0.0.1:0")
	require.NoError(t, plain.Start(t.Context()))
	t.Cleanup(func() { _ = plain.Stop(context.Background()) })
	resp, err := http.Get(plain.URL() + ReadyPath)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusOK, resp.StatusCode, "a server without a ready function is ready")
}
