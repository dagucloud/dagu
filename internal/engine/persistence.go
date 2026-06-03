// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/agentsnapshot"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/dagstate"
	"github.com/dagucloud/dagu/internal/runtime/runstate"
)

// PersistenceFactory wires backend-specific stores after configuration is loaded.
type PersistenceFactory func(context.Context, *config.Config) (Persistence, error)

// Persistence contains the storage dependencies required by Engine.
type Persistence struct {
	DAGStore             exec.DAGStore
	DAGRunStore          exec.DAGRunStore
	RunStateStore        runstate.Store
	ProcStore            exec.ProcStore
	StateStore           dagstate.Store
	ServiceRegistry      exec.ServiceRegistry
	DAGStoreFactory      DAGStoreFactory
	AgentStoresFactory   AgentStoresFactory
	SnapshotStoreFactory agentsnapshot.StoreFactory
}

// DAGStoreFactoryOptions configures a backend-specific DAG definition store.
type DAGStoreFactoryOptions struct {
	SearchPaths []string
}

// DAGStoreFactory creates DAG stores needed by execution-scoped loaders.
type DAGStoreFactory func(context.Context, *config.Config, DAGStoreFactoryOptions) (exec.DAGStore, error)

// AgentStoresFactory creates runtime agent stores for local execution.
type AgentStoresFactory func(context.Context, *config.Config) AgentStores

// AgentStores contains the stores and resolvers used by runtime agent flows.
type AgentStores = agent.RuntimeStores

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
	if opts.DAGRunStore != nil {
		p.DAGRunStore = opts.DAGRunStore
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
	if override.DAGStore != nil {
		base.DAGStore = override.DAGStore
	}
	if override.DAGRunStore != nil {
		base.DAGRunStore = override.DAGRunStore
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
	if override.DAGStoreFactory != nil {
		base.DAGStoreFactory = override.DAGStoreFactory
	}
	if override.AgentStoresFactory != nil {
		base.AgentStoresFactory = override.AgentStoresFactory
	}
	if override.SnapshotStoreFactory != nil {
		base.SnapshotStoreFactory = override.SnapshotStoreFactory
	}
	return base
}

func validatePersistence(ctx context.Context, p Persistence) error {
	var errs []error
	if p.DAGStore == nil {
		errs = append(errs, errors.New("DAG store is not configured"))
	}
	if p.DAGRunStore == nil && p.RunStateStore == nil {
		errs = append(errs, errors.New("DAG-run store or run-state store is not configured"))
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
	if p.DAGStoreFactory == nil {
		errs = append(errs, errors.New("DAG store factory is not configured"))
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
