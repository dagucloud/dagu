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
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
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
// or else with its worker.invite. It refuses the settings that the start
// refuses. removed is the failure the worker exits with at start because the
// certificate it keeps records a refusal as revoked, and nil otherwise. It
// takes no lock and never enrolls. localRegistry is the service registry in
// the worker's data directory.
func CoordinatorClientConfig(cfg *config.Config, localRegistry serviceregistry.ServiceRegistry) (clientConfig *coordinator.Config, removed error, err error) {
	if _, err := workerLabels(cfg.Worker.Labels); err != nil {
		return nil, nil, err
	}
	invite, cred, err := startCredential(cfg)
	if err != nil {
		return nil, nil, err
	}
	clientConfig = clientConfigOf(cfg, localRegistry)
	if invite != nil {
		clientConfig = clientConfig.WithInvite(*invite)
	}
	switch keptUseOf(cred, invite) {
	case keptRemoved:
		// The check still dials with it, to show what coordinators answer.
		removed = errRemoved(coordinator.ProcessEnvironment() == coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_KUBERNETES)
		clientConfig = clientConfig.WithWorkerCertificate(cred.cert, cred.ca)
	case keptConnect:
		clientConfig = clientConfig.WithWorkerCertificate(cred.cert, cred.ca)
	case keptUnused:
	}
	if err := clientConfig.Validate(); err != nil {
		return nil, nil, fmt.Errorf("invalid coordinator client configuration: %w", err)
	}
	return clientConfig, removed, nil
}

// startCredential returns worker.invite, nil without one, and the credential
// the worker keeps, nil without one or when its settings leave it unused. It
// fails as the worker's start does on an invite it cannot use or a kept
// credential it cannot read.
func startCredential(cfg *config.Config) (*coordinator.Invite, *credential, error) {
	var invite *coordinator.Invite
	switch {
	case cfg.Worker.Invite != "":
		parsed, ok := coordinator.ParseInvite(cfg.Worker.Invite)
		if !ok {
			return nil, nil, errInviteRefused
		}
		if err := checkCACertFile(cfg.Core.Peer.CACertFile, parsed); err != nil {
			return nil, nil, err
		}
		invite = &parsed
	case !usesKeptCertificate(cfg.Core.Peer):
		return nil, nil, nil
	}
	cred, err := newCredentialStore(cfg.Paths.DataDir).load()
	if err != nil {
		return nil, nil, errUnreadable(err)
	}
	return invite, cred, nil
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
