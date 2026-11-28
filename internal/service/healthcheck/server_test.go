// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package healthcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
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

// Listen holds the port from before Start until Stop, so no other process
// can take it in between, and Start serves on it.
func TestServerListen(t *testing.T) {
	for _, start := range []bool{true, false} {
		probe, err := net.Listen("tcp", ":0")
		require.NoError(t, err)
		port := probe.Addr().(*net.TCPAddr).Port
		require.NoError(t, probe.Close())

		hs := NewServer("test-service", port)
		require.NoError(t, hs.Listen())
		_, err = net.Listen("tcp", fmt.Sprintf(":%d", port))
		require.Error(t, err, "the port is held")

		if start {
			require.NoError(t, hs.Start(t.Context()))
			resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/health", port))
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		}
		require.NoError(t, hs.Stop(t.Context()))
		free, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		require.NoError(t, err, "Stop releases the port")
		require.NoError(t, free.Close())
	}
}
