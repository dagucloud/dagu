// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package process

import (
	"context"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/license"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/proc"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/service/frontend"
	apiv1 "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/v2/internal/service/resource"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/dagucloud/dagu/v2/internal/telemetry"
)

// ServerConfig contains the wiring needed to construct the frontend process role.
type ServerConfig struct {
	Context              context.Context
	Config               *config.Config
	DAGRunStore          dagrun.DAGRunStore
	QueueStore           queue.QueueStore
	ProcStore            proc.ProcStore
	DAGRunManager        runtime.Manager
	ServiceRegistry      serviceregistry.ServiceRegistry
	DAGRunLeaseStore     dispatch.DAGRunLeaseStore
	WorkerHeartbeatStore dispatch.WorkerHeartbeatStore
	LicenseManager       *license.Manager
	ResourceService      *resource.Service
}

// NewServer creates the frontend server and process-local telemetry wiring.
func NewServer(cfg ServerConfig, opts ...frontend.ServerOption) (*frontend.Server, error) {
	ctx := cfg.Context
	if ctx == nil {
		ctx = context.Background()
	}

	limits := cfg.Config.Cache.Limits()
	dagCache := fileutil.NewCache[*ir.DAG]("dag_definition", limits.DAG.Limit, limits.DAG.TTL)
	dagCache.StartEviction(ctx)

	dagRepository, err := NewDAGRepository(cfg.Config, DAGRepositoryConfig{Cache: dagCache})
	if err != nil {
		return nil, err
	}

	coordinatorClient := NewCoordinatorClient(ctx, cfg.Config, cfg.ServiceRegistry)
	collector := telemetry.NewCollector(
		config.Version,
		dagRepository,
		cfg.DAGRunStore,
		cfg.QueueStore,
		cfg.ServiceRegistry,
	)
	collector.SetWorkerHeartbeatStore(cfg.WorkerHeartbeatStore)
	collector.RegisterCache(dagCache)

	metricsRegistry := telemetry.NewRegistry(collector)

	if cfg.LicenseManager != nil {
		opts = append(opts, frontend.WithLicenseManager(cfg.LicenseManager))
	}
	if cfg.DAGRunLeaseStore != nil {
		opts = append(opts, frontend.WithAPIOption(apiv1.WithDAGRunLeaseStore(cfg.DAGRunLeaseStore)))
	}
	if cfg.WorkerHeartbeatStore != nil {
		opts = append(opts, frontend.WithAPIOption(apiv1.WithWorkerHeartbeatStore(cfg.WorkerHeartbeatStore)))
	}
	opts = append(opts, frontend.WithAPIOption(apiv1.WithSchedulerStateStore(
		scheduler.NewWatermarkStore(
			file.NewCollection(filepath.Join(cfg.Config.Paths.DataDir, "scheduler"), file.WithIndentedJSON()),
		),
	)))

	return frontend.NewServer(
		ctx,
		cfg.Config,
		dagRepository,
		cfg.DAGRunStore,
		cfg.QueueStore,
		cfg.ProcStore,
		cfg.DAGRunManager,
		coordinatorClient,
		cfg.ServiceRegistry,
		metricsRegistry,
		cfg.ResourceService,
		NewFrontendStoreFactories(),
		opts...,
	)
}
