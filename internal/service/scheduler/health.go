// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import "github.com/dagucloud/dagu/v2/internal/service/healthcheck"

// HealthServer represents the scheduler health check server.
type HealthServer = healthcheck.Server

// HealthResponse represents the scheduler health check response.
type HealthResponse = healthcheck.Response

// NewHealthServer creates a new scheduler health check server.
func NewHealthServer(port int) *HealthServer {
	return healthcheck.NewServer("scheduler", port)
}

func newHealthServerWithAddr(addr string) *HealthServer {
	return healthcheck.NewServerWithAddr("scheduler", addr)
}
