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

// StartClient is how the worker's coordinator client starts, for dagu
// worker --check.
type StartClient struct {
	// Config is the configuration the client dials with: with the worker
	// certificate the worker keeps, also one a coordinator refused as
	// revoked, or else with its worker.invite.
	Config *coordinator.Config
	// Removed is the failure the worker exits with at start because the
	// certificate it keeps records a refusal as revoked, and nil otherwise.
	Removed error
	// Refused is the failure the start ends with when a coordinator refuses
	// that certificate as revoked, and nil when the worker then enrolls
	// again with worker.invite or presents no certificate.
	Refused error
}

// CoordinatorClientConfig returns how the worker's coordinator client
// starts. It refuses the settings that the start refuses, takes no lock,
// and never enrolls. localRegistry is the service registry in the worker's
// data directory.
func CoordinatorClientConfig(cfg *config.Config, localRegistry serviceregistry.ServiceRegistry) (StartClient, error) {
	if _, err := workerLabels(cfg.Worker.Labels); err != nil {
		return StartClient{}, err
	}
	invite, cred, err := startCredential(cfg)
	if err != nil {
		return StartClient{}, err
	}
	var removed, refused error
	clientConfig := clientConfigOf(cfg, localRegistry)
	if invite != nil {
		clientConfig = clientConfig.WithInvite(*invite)
	}
	kubernetes := coordinator.ProcessEnvironment() == coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_KUBERNETES
	writable := func() bool { return newCredentialStore(cfg.Paths.DataDir).writable() }
	switch keptUseOf(cred, invite) {
	case keptUnused:
		// The start enrolls in place of the kept certificate only when it
		// can keep the new one.
		if cred != nil && !writable() {
			return StartClient{}, errCannotReplace(cfg.Paths.DataDir)
		}
	case keptRemoved:
		// The check still dials with it, to show what coordinators answer.
		removed = errRemoved(kubernetes)
		refused = removed
		clientConfig = clientConfig.WithWorkerCertificate(cred.cert, cred.ca)
	case keptConnect:
		// After a refusal, the start decides as for a recorded one.
		marked := *cred
		marked.removed = true
		switch {
		case keptUseOf(&marked, invite) != keptUnused:
			refused = errRemoved(kubernetes)
		case !writable():
			refused = errCannotReplace(cfg.Paths.DataDir)
		}
		clientConfig = clientConfig.WithWorkerCertificate(cred.cert, cred.ca)
	}
	if err := clientConfig.Validate(); err != nil {
		return StartClient{}, fmt.Errorf("invalid coordinator client configuration: %w", err)
	}
	return StartClient{Config: clientConfig, Removed: removed, Refused: refused}, nil
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
