// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package healthcheck

import (
	"context"
	"encoding/json"
	"net/http"
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
