// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/proc"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/runtime/runstate"
	"github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
)

// PersistenceFactory wires backend-specific stores after configuration is loaded.
type PersistenceFactory func(context.Context, *config.Config) (Persistence, error)

// Persistence contains the storage dependencies required by Engine.
type Persistence struct {
	DAGRepository        *persis.DAGRepository
	DAGRunRepository     *persis.DAGRunRepository
	RunStateStore        runstate.Store
	ProcStore            proc.ProcStore
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
	SecretStore  secret.Store
	ProfileStore profile.Store
}

type validatingProcStore interface {
	Validate(context.Context) error
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
	if override.ProcStore != nil {
		base.ProcStore = override.ProcStore
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
	if p.ProcStore == nil {
		errs = append(errs, errors.New("proc store is not configured"))
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
	if validator, ok := p.ProcStore.(validatingProcStore); ok {
		if err := validator.Validate(ctx); err != nil {
			return err
		}
	}
	return nil
}
