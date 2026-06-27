// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/service/healthcheck"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/require"
)

func TestWorkerHealthServerStartsAndStops(t *testing.T) {
	t.Parallel()

	w := NewWorker("test-worker", 1, newMockRemoteCoordinatorClient(), nil, &config.Config{})
	w.SetHandler(noopHealthTaskHandler{})
	w.healthServer = healthcheck.NewServerWithAddr("worker", "127.0.0.1:0")

	ctx := context.Background()
	errCh := make(chan error, 1)
	go func() {
		errCh <- w.Start(ctx)
	}()

	baseURL := requireWorkerHealthServerURL(t, w.healthServer)
	requireHealthyWorkerHealth(t, baseURL)

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, w.Stop(stopCtx))

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop in time")
	}

	requireWorkerHealthStopped(t, baseURL)
}

func TestWorkerHealthServerStaysHealthyDuringHeartbeatFailures(t *testing.T) {
	t.Parallel()

	var heartbeatCalls atomic.Int32
	client := newMockRemoteCoordinatorClient()
	client.HeartbeatFunc = func(context.Context, *coordinatorv1.HeartbeatRequest) (*coordinatorv1.HeartbeatResponse, error) {
		heartbeatCalls.Add(1)
		return nil, errors.New("heartbeat failed")
	}

	w := NewWorker("test-worker", 1, client, nil, &config.Config{})
	w.SetHandler(noopHealthTaskHandler{})
	w.healthServer = healthcheck.NewServerWithAddr("worker", "127.0.0.1:0")

	ctx := context.Background()
	errCh := make(chan error, 1)
	go func() {
		errCh <- w.Start(ctx)
	}()

	baseURL := requireWorkerHealthServerURL(t, w.healthServer)
	requireHealthyWorkerHealth(t, baseURL)

	require.Eventually(t, func() bool {
		return heartbeatCalls.Load() >= 2
	}, 5*time.Second, 10*time.Millisecond, "expected at least 2 heartbeat failures")
	requireHealthyWorkerHealth(t, baseURL)

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, w.Stop(stopCtx))

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop in time")
	}
}

type noopHealthTaskHandler struct{}

func (noopHealthTaskHandler) Handle(context.Context, *coordinatorv1.Task) error {
	return nil
}

func requireWorkerHealthServerURL(t *testing.T, hs *healthcheck.Server) string {
	t.Helper()

	var url string
	require.Eventually(t, func() bool {
		url = hs.URL()
		return url != ""
	}, 5*time.Second, 10*time.Millisecond, "health server did not bind an address")
	return url
}

func requireHealthyWorkerHealth(t *testing.T, baseURL string) {
	t.Helper()

	client := &http.Client{Timeout: time.Second}

	require.Eventually(t, func() bool {
		resp, err := client.Get(baseURL + "/health")
		if err != nil {
			return false
		}
		defer func() {
			_ = resp.Body.Close()
		}()
		if resp.StatusCode != http.StatusOK {
			return false
		}

		var healthResp healthcheck.Response
		if err := json.NewDecoder(resp.Body).Decode(&healthResp); err != nil {
			return false
		}

		return healthResp.Status == "healthy"
	}, 5*time.Second, 10*time.Millisecond, "worker health endpoint did not become healthy")
}

func requireWorkerHealthStopped(t *testing.T, baseURL string) {
	t.Helper()

	client := &http.Client{Timeout: 200 * time.Millisecond}

	require.Eventually(t, func() bool {
		resp, err := client.Get(baseURL + "/health")
		if err == nil {
			_ = resp.Body.Close()
			return false
		}
		return true
	}, 5*time.Second, 10*time.Millisecond, "worker health endpoint still responded after stop")
}
