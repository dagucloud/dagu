// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/test"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/require"
)

func TestMetrics_PublicMode(t *testing.T) {
	t.Parallel()
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Server.Auth.Mode = config.AuthModeBasic
		cfg.Server.Auth.Basic.Username = "admin"
		cfg.Server.Auth.Basic.Password = "secret"
		cfg.Server.Metrics = config.MetricsAccessPublic
	}))

	resp := server.Client().Get("/api/v1/metrics").ExpectStatus(http.StatusOK).Send(t)
	require.Contains(t, resp.Response.Header().Get("Content-Type"), "text/plain")
	require.NotEmpty(t, resp.Body)

	server.Client().Get("/api/v1/dag-runs").ExpectStatus(http.StatusUnauthorized).Send(t)
}

func TestMetrics_PrivateMode_Unauthorized(t *testing.T) {
	t.Parallel()
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Server.Auth.Mode = config.AuthModeBasic
		cfg.Server.Auth.Basic.Username = "admin"
		cfg.Server.Auth.Basic.Password = "secret"
		cfg.Server.Metrics = config.MetricsAccessPrivate
	}))

	server.Client().Get("/api/v1/metrics").ExpectStatus(http.StatusUnauthorized).Send(t)
}

func TestMetrics_PrivateMode_WithBasicAuth(t *testing.T) {
	t.Parallel()
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Server.Auth.Mode = config.AuthModeBasic
		cfg.Server.Auth.Basic.Username = "admin"
		cfg.Server.Auth.Basic.Password = "secret"
		cfg.Server.Metrics = config.MetricsAccessPrivate
	}))

	resp := server.Client().Get("/api/v1/metrics").
		WithBasicAuth("admin", "secret").
		ExpectStatus(http.StatusOK).Send(t)
	require.Contains(t, resp.Response.Header().Get("Content-Type"), "text/plain")
	require.NotEmpty(t, resp.Body)
}

func TestMetrics_ExportsWorkerMetrics(t *testing.T) {
	t.Parallel()
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Server.Metrics = config.MetricsAccessPublic
	}))
	require.NoError(t, server.WorkerHeartbeatStore.Upsert(context.Background(), exec.WorkerHeartbeatRecord{
		WorkerID: "worker-a",
		Labels: map[string]string{
			"pool":   "gpu",
			"region": "ap-northeast-1",
		},
		Stats: &coordinatorv1.WorkerStats{
			TotalPollers: 4,
			BusyPollers:  2,
			RunningTasks: []*coordinatorv1.RunningTask{
				{DagRunId: "run-1", DagName: "dag-1", StartedAt: time.Now().Add(-time.Minute).Unix()},
			},
		},
		LastHeartbeatAt: time.Now().UnixMilli(),
	}))

	resp := server.Client().Get("/api/v1/metrics").ExpectStatus(http.StatusOK).Send(t)
	require.Contains(t, resp.Body, "dagu_workers_registered")
	require.Contains(t, resp.Body, "dagu_worker_info")
	require.Contains(t, resp.Body, "dagu_worker_running_tasks")
	require.Contains(t, resp.Body, `worker_id="worker-a"`)
	require.Contains(t, resp.Body, `label_name="pool"`)
	require.Contains(t, resp.Body, `label_value="gpu"`)
	require.Contains(t, resp.Body, `label_name="region"`)
	require.Contains(t, resp.Body, `label_value="ap-northeast-1"`)
}

func TestMetrics_DefaultsToPrivate(t *testing.T) {
	t.Parallel()
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Server.Auth.Mode = config.AuthModeBasic
		cfg.Server.Auth.Basic.Username = "admin"
		cfg.Server.Auth.Basic.Password = "secret"
	}))

	server.Client().Get("/api/v1/metrics").ExpectStatus(http.StatusUnauthorized).Send(t)
}
