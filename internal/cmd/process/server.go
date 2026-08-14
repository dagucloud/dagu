// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package process

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/service/frontend"
	apiv1 "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/v2/internal/service/resource"
	"github.com/dagucloud/dagu/v2/internal/telemetry"
)

// ServerConfig contains the wiring needed to construct the frontend process role.
type ServerConfig struct {
	Context         context.Context
	Config          *config.Config
	Persistence     CorePersistence
	Caches          []fileutil.CacheMetrics
	DAGRunManager   runtime.Manager
	LicenseManager  *license.Manager
	ResourceService *resource.Service
}

// NewServer creates the frontend server and process-local telemetry wiring.
func NewServer(cfg ServerConfig, opts ...frontend.ServerOption) (*frontend.Server, error) {
	ctx := cfg.Context
	if ctx == nil {
		ctx = context.Background()
	}

	coordinatorClient := NewCoordinatorClient(ctx, cfg.Config, cfg.Persistence.ServiceRegistry)
	collector := telemetry.NewCollector(
		config.Version,
		cfg.Persistence.DAGRepository,
		cfg.Persistence.DAGRunRepository,
		cfg.Persistence.QueueStore,
		cfg.Persistence.ServiceRegistry,
	)
	collector.SetWorkerHeartbeatStore(cfg.Persistence.WorkerHeartbeatStore)
	for _, cache := range cfg.Caches {
		collector.RegisterCache(cache)
	}

	metricsRegistry := telemetry.NewRegistry(collector)

	if cfg.LicenseManager != nil {
		opts = append(opts, frontend.WithLicenseManager(cfg.LicenseManager))
	}
	if cfg.Persistence.DAGRunLeaseStore != nil {
		opts = append(opts, frontend.WithAPIOption(apiv1.WithDAGRunLeaseStore(cfg.Persistence.DAGRunLeaseStore)))
	}
	if cfg.Persistence.WorkerHeartbeatStore != nil {
		opts = append(opts, frontend.WithAPIOption(apiv1.WithWorkerHeartbeatStore(cfg.Persistence.WorkerHeartbeatStore)))
	}
	opts = append(opts, frontend.WithAPIOption(
		apiv1.WithSchedulerStateStore(cfg.Persistence.SchedulerStateStore),
	))

	return frontend.NewServer(
		ctx,
		cfg.Config,
		cfg.Persistence.DAGRepository,
		cfg.Persistence.DAGRunRepository,
		cfg.Persistence.QueueStore,
		cfg.Persistence.ProcRepository,
		cfg.DAGRunManager,
		coordinatorClient,
		cfg.Persistence.ServiceRegistry,
		metricsRegistry,
		cfg.ResourceService,
		NewFileFrontendStoreFactories(),
		opts...,
	)
}
