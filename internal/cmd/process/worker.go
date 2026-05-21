// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package process

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/coordinator"
)

// BuildWorkerCoordinatorClientConfig creates coordinator client config from application config.
// It returns nil config when no static coordinators are configured, so the worker can use service discovery.
func BuildWorkerCoordinatorClientConfig(cfg *config.Config) (*coordinator.Config, bool, error) {
	if len(cfg.Worker.Coordinators) == 0 {
		return nil, false, nil
	}

	coordCliCfg := coordinator.DefaultConfig()
	coordCliCfg.CAFile = cfg.Core.Peer.ClientCaFile
	coordCliCfg.CertFile = cfg.Core.Peer.CertFile
	coordCliCfg.KeyFile = cfg.Core.Peer.KeyFile
	coordCliCfg.SkipTLSVerify = cfg.Core.Peer.SkipTLSVerify
	coordCliCfg.Insecure = cfg.Core.Peer.Insecure
	if cfg.Core.Peer.MaxRetries > 0 {
		coordCliCfg.MaxRetries = cfg.Core.Peer.MaxRetries
	}
	if cfg.Core.Peer.RetryInterval > 0 {
		coordCliCfg.RetryInterval = cfg.Core.Peer.RetryInterval
	}

	if err := coordCliCfg.Validate(); err != nil {
		return nil, false, fmt.Errorf("invalid coordinator client configuration: %w", err)
	}

	return coordCliCfg, true, nil
}

// NewWorkerCoordinatorClient creates the worker coordinator client and reports
// whether the worker should use the shared-nothing remote task handler.
func NewWorkerCoordinatorClient(
	ctx context.Context,
	cfg *config.Config,
	registry exec.ServiceRegistry,
) (coordinator.Client, bool, error) {
	coordCliCfg, useRemoteHandler, err := BuildWorkerCoordinatorClientConfig(cfg)
	if err != nil {
		return nil, false, err
	}

	if coordCliCfg == nil {
		return NewCoordinatorClient(ctx, cfg, registry), false, nil
	}

	staticRegistry, err := coordinator.NewStaticRegistry(cfg.Worker.Coordinators)
	if err != nil {
		return nil, false, fmt.Errorf("failed to create static registry: %w", err)
	}
	logger.Info(ctx, "Using static coordinator discovery",
		slog.Any("coordinators", cfg.Worker.Coordinators))

	return coordinator.New(staticRegistry, coordCliCfg), useRemoteHandler, nil
}
