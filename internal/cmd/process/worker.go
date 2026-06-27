// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package process

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/service/coordinator"
)

// BuildWorkerCoordinatorClientConfig creates coordinator client config from application config.
func BuildWorkerCoordinatorClientConfig(cfg *config.Config) (*coordinator.Config, error) {
	if len(cfg.Worker.Coordinators) == 0 {
		return nil, fmt.Errorf("worker.coordinators is required")
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
		return nil, fmt.Errorf("invalid coordinator client configuration: %w", err)
	}

	return coordCliCfg, nil
}

// NewWorkerCoordinatorClient creates the worker coordinator client.
func NewWorkerCoordinatorClient(
	ctx context.Context,
	cfg *config.Config,
) (coordinator.Client, error) {
	coordCliCfg, err := BuildWorkerCoordinatorClientConfig(cfg)
	if err != nil {
		return nil, err
	}

	staticRegistry, err := coordinator.NewStaticRegistry(cfg.Worker.Coordinators)
	if err != nil {
		return nil, fmt.Errorf("failed to create static registry: %w", err)
	}
	logger.Info(ctx, "Using static coordinator discovery",
		slog.Any("coordinators", cfg.Worker.Coordinators))

	return coordinator.New(staticRegistry, coordCliCfg), nil
}
