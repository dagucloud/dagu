// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
)

// ErrNoCoordinators is the error of a worker without worker.coordinators.
var ErrNoCoordinators = errors.New("worker.coordinators is required")

// NewCoordinatorClient creates a client using the worker's configured
// coordinators. localRegistry is the service registry in the worker's data
// directory, which tells whether those coordinators serve built-in TLS.
func NewCoordinatorClient(ctx context.Context, cfg *config.Config, localRegistry serviceregistry.ServiceRegistry) (coordinator.Client, error) {
	if len(cfg.Worker.Coordinators) == 0 {
		return nil, ErrNoCoordinators
	}
	clientConfig, err := CoordinatorClientConfig(cfg, localRegistry)
	if err != nil {
		return nil, err
	}
	registry, err := CoordinatorRegistry(cfg)
	if err != nil {
		return nil, err
	}
	logger.Info(ctx, "Using static coordinator discovery",
		slog.Any("coordinators", cfg.Worker.Coordinators))
	return coordinator.New(registry, clientConfig), nil
}

// CoordinatorClientConfig returns the configuration the worker's
// coordinator client dials with. localRegistry is the service registry in
// the worker's data directory.
func CoordinatorClientConfig(cfg *config.Config, localRegistry serviceregistry.ServiceRegistry) (*coordinator.Config, error) {
	clientConfig := coordinator.ConfigFromPeer(cfg.Core.Peer)
	clientConfig.WorkspaceBundleDir = workspacebundle.StoreDir(cfg.Paths.DataDir)
	clientConfig.LocalRegistry = localRegistry
	if err := clientConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid coordinator client configuration: %w", err)
	}
	return clientConfig, nil
}

// CoordinatorRegistry returns the coordinators the worker calls. It fails
// with ErrNoCoordinators without worker.coordinators.
func CoordinatorRegistry(cfg *config.Config) (*coordinator.StaticRegistry, error) {
	if len(cfg.Worker.Coordinators) == 0 {
		return nil, ErrNoCoordinators
	}
	registry, err := coordinator.NewStaticRegistry(cfg.Worker.Coordinators)
	if err != nil {
		return nil, fmt.Errorf("failed to create static registry: %w", err)
	}
	return registry, nil
}
