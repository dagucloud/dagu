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

// newCoordinatorClient creates a client that dials the worker's configured
// coordinators with clientConfig.
func newCoordinatorClient(ctx context.Context, cfg *config.Config, clientConfig *coordinator.Config) (coordinator.Client, error) {
	registry, err := CoordinatorRegistry(cfg)
	if err != nil {
		return nil, err
	}
	logger.Info(ctx, "Using static coordinator discovery",
		slog.Any("coordinators", cfg.Worker.Coordinators))
	return coordinator.New(registry, clientConfig), nil
}

// CoordinatorClientConfig returns the configuration the worker's
// coordinator client dials with at start, for dagu worker --check: with the
// worker certificate it keeps, also one a coordinator refused as revoked,
// or else with its worker.invite. It takes no lock and never enrolls.
// localRegistry is the service registry in the worker's data directory.
func CoordinatorClientConfig(cfg *config.Config, localRegistry serviceregistry.ServiceRegistry) (*coordinator.Config, error) {
	clientConfig, err := withStartCredential(cfg, clientConfigOf(cfg, localRegistry))
	if err != nil {
		return nil, err
	}
	if err := clientConfig.Validate(); err != nil {
		return nil, fmt.Errorf("invalid coordinator client configuration: %w", err)
	}
	return clientConfig, nil
}

// withStartCredential returns clientConfig with the invite or the kept
// worker certificate that the worker starts with, by Spec 108 §Worker Start.
func withStartCredential(cfg *config.Config, clientConfig *coordinator.Config) (*coordinator.Config, error) {
	var invite *coordinator.Invite
	switch {
	case cfg.Worker.Invite != "":
		parsed, ok := coordinator.ParseInvite(cfg.Worker.Invite)
		if !ok {
			return nil, errInviteRefused
		}
		invite = &parsed
		clientConfig = clientConfig.WithInvite(parsed)
	case !usesKeptCertificate(cfg.Core.Peer):
		return clientConfig, nil
	}
	cred, err := newCredentialStore(cfg.Paths.DataDir).load()
	if err != nil {
		return nil, errUnreadable(cfg.Paths.DataDir, err)
	}
	if keptUseOf(cred, invite) != keptUnused {
		clientConfig = clientConfig.WithWorkerCertificate(cred.cert, cred.ca)
	}
	return clientConfig, nil
}

// clientConfigOf returns the worker's client configuration before
// validation.
func clientConfigOf(cfg *config.Config, localRegistry serviceregistry.ServiceRegistry) *coordinator.Config {
	clientConfig := coordinator.ConfigFromPeer(cfg.Core.Peer)
	clientConfig.WorkspaceBundleDir = workspacebundle.StoreDir(cfg.Paths.DataDir)
	clientConfig.LocalRegistry = localRegistry
	return clientConfig
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
