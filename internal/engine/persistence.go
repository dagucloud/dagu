// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/build"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/profile"
	rtagent "github.com/dagucloud/dagu/v2/internal/runtime/agent"
	"github.com/dagucloud/dagu/v2/internal/runtime/runstate"
	"github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/secret/providers"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
)

// PersistenceFactory wires backend-specific stores after configuration is loaded.
type PersistenceFactory func(context.Context, *config.Config) (Persistence, error)

// Persistence contains the storage dependencies required by Engine.
type Persistence struct {
	DAGRepository        *persis.DAGRepository
	DAGRunRepository     *persis.DAGRunRepository
	RunStateStore        runstate.Store
	ProcRepository       *persis.ProcRepository
	StateStore           dagrun.StateStore
	ServiceRegistry      serviceregistry.ServiceRegistry
	DAGRepositoryFactory DAGRepositoryFactory
	RuntimeStoresFactory RuntimeStoresFactory
}

// DAGRepositoryFactoryOptions configures an execution-scoped DAG repository.
type DAGRepositoryFactoryOptions struct {
	SearchPaths []string
}

// DAGRepositoryFactory creates repositories needed by execution-scoped loaders.
type DAGRepositoryFactory func(context.Context, *config.Config, DAGRepositoryFactoryOptions) (*persis.DAGRepository, error)

// RuntimeStoresFactory creates stores for local workflow execution.
type RuntimeStoresFactory func(context.Context, *config.Config) RuntimeStores

// RuntimeStores contains the stores used by workflow execution.
type RuntimeStores struct {
	SecretStore          secret.Store
	ProfileStore         profile.Store
	MaterializationStore build.MaterializationStore
	// OpenConnectionStore opens the store that local runs check their
	// declared connections against. It is called once per run, on the
	// first check. When nil, runs do not check their connections.
	OpenConnectionStore func() (connection.Store, error)

	// connectionChecker checks a run's connections against the store that
	// OpenConnectionStore opens, or is nil without it.
	connectionChecker rtagent.ConnectionChecker
	// secretResolver and profileResolver resolve in the project alone while
	// projects are on, and are nil while they are off.
	secretResolver  providers.ReferenceResolver
	profileResolver profile.RuntimeResolver
}

// subDAGSecretResolver returns the resolver a sub-DAG run uses for its
// secrets, or nil when the run builds its own.
func (s RuntimeStores) subDAGSecretResolver() func(*ir.DAG) providers.ReferenceResolver {
	if s.secretResolver == nil {
		return nil
	}
	return func(*ir.DAG) providers.ReferenceResolver { return s.secretResolver }
}

// subDAGProfileResolver returns the resolver a sub-DAG run uses for its
// profiles, or nil when the run builds its own.
func (s RuntimeStores) subDAGProfileResolver() func(*ir.DAG) profile.RuntimeResolver {
	if s.profileResolver == nil {
		return nil
	}
	return func(*ir.DAG) profile.RuntimeResolver { return s.profileResolver }
}

func buildPersistence(ctx context.Context, cfg *config.Config, opts Options) (Persistence, error) {
	var p Persistence
	if opts.PersistenceFactory != nil {
		factoryPersistence, err := opts.PersistenceFactory(ctx, cfg)
		if err != nil {
			return Persistence{}, err
		}
		p = factoryPersistence
	}
	p = overridePersistence(p, opts.Persistence)
	if opts.DAGRunRepository != nil {
		p.DAGRunRepository = opts.DAGRunRepository
	}
	if opts.RunStateStore != nil {
		p.RunStateStore = opts.RunStateStore
	}
	if err := validatePersistence(ctx, p); err != nil {
		return Persistence{}, err
	}
	return p, nil
}

func overridePersistence(base, override Persistence) Persistence {
	if override.DAGRepository != nil {
		base.DAGRepository = override.DAGRepository
	}
	if override.DAGRunRepository != nil {
		base.DAGRunRepository = override.DAGRunRepository
	}
	if override.RunStateStore != nil {
		base.RunStateStore = override.RunStateStore
	}
	if override.ProcRepository != nil {
		base.ProcRepository = override.ProcRepository
	}
	if override.StateStore != nil {
		base.StateStore = override.StateStore
	}
	if override.ServiceRegistry != nil {
		base.ServiceRegistry = override.ServiceRegistry
	}
	if override.DAGRepositoryFactory != nil {
		base.DAGRepositoryFactory = override.DAGRepositoryFactory
	}
	if override.RuntimeStoresFactory != nil {
		base.RuntimeStoresFactory = override.RuntimeStoresFactory
	}
	return base
}

func validatePersistence(ctx context.Context, p Persistence) error {
	var errs []error
	if p.DAGRepository == nil {
		errs = append(errs, errors.New("DAG repository is not configured"))
	}
	if p.DAGRunRepository == nil && p.RunStateStore == nil {
		errs = append(errs, errors.New("DAG-run repository or run-state store is not configured"))
	}
	if p.ProcRepository == nil {
		errs = append(errs, errors.New("proc repository is not configured"))
	}
	if p.StateStore == nil {
		errs = append(errs, errors.New("state store is not configured"))
	}
	if p.ServiceRegistry == nil {
		errs = append(errs, errors.New("service registry is not configured"))
	}
	if p.DAGRepositoryFactory == nil {
		errs = append(errs, errors.New("DAG repository factory is not configured"))
	}
	if len(errs) > 0 {
		return fmt.Errorf("engine persistence: %w", errors.Join(errs...))
	}
	return p.ProcRepository.Validate(ctx)
}
