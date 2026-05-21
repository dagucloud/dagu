// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package process

import (
	"context"
	"path/filepath"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/telemetry"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/license"
	"github.com/dagucloud/dagu/internal/persis/filewatermark"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/service/frontend"
	apiv1 "github.com/dagucloud/dagu/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/internal/service/resource"
)

// ServerConfig contains the wiring needed to construct the frontend process role.
type ServerConfig struct {
	Context              context.Context
	Config               *config.Config
	DAGRunStore          exec.DAGRunStore
	QueueStore           exec.QueueStore
	ProcStore            exec.ProcStore
	DAGRunManager        runtime.Manager
	ServiceRegistry      exec.ServiceRegistry
	DAGRunLeaseStore     exec.DAGRunLeaseStore
	WorkerHeartbeatStore exec.WorkerHeartbeatStore
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
	dagCache := fileutil.NewCache[*core.DAG]("dag_definition", limits.DAG.Limit, limits.DAG.TTL)
	dagCache.StartEviction(ctx)

	dagStore, err := NewDAGStore(cfg.Config, DAGStoreConfig{Cache: dagCache})
	if err != nil {
		return nil, err
	}

	coordinatorClient := NewCoordinatorClient(ctx, cfg.Config, cfg.ServiceRegistry)
	collector := telemetry.NewCollector(
		config.Version,
		dagStore,
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
		filewatermark.New(filepath.Join(cfg.Config.Paths.DataDir, "scheduler")),
	)))

	return frontend.NewServer(
		ctx,
		cfg.Config,
		dagStore,
		cfg.DAGRunStore,
		cfg.QueueStore,
		cfg.ProcStore,
		cfg.DAGRunManager,
		coordinatorClient,
		cfg.ServiceRegistry,
		metricsRegistry,
		collector,
		cfg.ResourceService,
		opts...,
	)
}
