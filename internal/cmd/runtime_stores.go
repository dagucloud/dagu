// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"github.com/dagucloud/dagu/v2/internal/build"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/agent"
	"github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/secret/providers"
)

type executionStores struct {
	SecretStore  secret.Store
	ProfileStore profile.Store
	// SecretResolver and ProfileResolver resolve a run's secrets and
	// profiles in the command's project alone while projects are on. Both
	// are nil while projects are off, and a run then adds the layers of the
	// workspace its DAG's label names.
	SecretResolver       providers.ReferenceResolver
	ProfileResolver      profile.RuntimeResolver
	Connections          agent.ConnectionChecker
	MaterializationStore build.MaterializationStore
	Inventory            inventory.Store
	// Nodes finds the nodes of steps with nodes: the inventory nodes and
	// the worker nodes. It is nil without inventories.
	Nodes    runtime.NodeSelector
	HostKeys inventory.HostKeyStore
}

// runtimeStores creates the runtime store bundle for this command context.
func (c *Context) runtimeStores() executionStores {
	inventories := c.runtimeInventoryStore()
	stores := executionStores{
		SecretStore:          c.settings.SecretStore(c),
		ProfileStore:         c.settings.ProfileStore(c),
		MaterializationStore: c.projectStores.MaterializationStore,
		Inventory:            inventories,
		HostKeys:             c.runtimeHostKeyStore(),
	}
	if inventories != nil {
		stores.Nodes = inventory.NewProjectNodes(inventories, c.runtimeWorkerNodeStore())
	}
	if c.serverStores != nil {
		stores.Connections = &storeConnectionChecker{open: c.serverStores.ConnectionStore}
	}
	if c.Config.ProjectsEnabled {
		if stores.SecretStore != nil {
			stores.SecretResolver = secret.NewProjectReferenceResolver(stores.SecretStore)
		}
		if stores.ProfileStore != nil {
			stores.ProfileResolver = profile.NewProjectResolver(stores.ProfileStore, stores.SecretStore)
		}
	}
	return stores
}

// subDAGSecretResolver returns the resolver a sub-DAG run uses for its
// secrets, or nil when the run builds its own.
func (s executionStores) subDAGSecretResolver() func(*ir.DAG) providers.ReferenceResolver {
	if s.SecretResolver == nil {
		return nil
	}
	return func(*ir.DAG) providers.ReferenceResolver { return s.SecretResolver }
}

// subDAGProfileResolver returns the resolver a sub-DAG run uses for its
// profiles, or nil when the run builds its own.
func (s executionStores) subDAGProfileResolver() func(*ir.DAG) profile.RuntimeResolver {
	if s.ProfileResolver == nil {
		return nil
	}
	return func(*ir.DAG) profile.RuntimeResolver { return s.ProfileResolver }
}

// inventoryStore opens the node inventories of this installation.
func (c *Context) inventoryStore() (inventory.Store, error) {
	return c.settings.InventoryStore()
}

// runtimeInventoryStore returns the node inventories for runs, or nil when
// the backend cannot hold them.
func (c *Context) runtimeInventoryStore() inventory.Store {
	inventories, err := c.inventoryStore()
	if err != nil {
		logger.Warn(c, "Node inventory is unavailable", tag.Error(err))
		return nil
	}
	return inventories
}

// serviceInventoryStore returns the node inventories for a long-running
// service, which keeps them in memory, or nil when the backend cannot hold
// them.
func (c *Context) serviceInventoryStore() inventory.Store {
	s, err := c.settings.CachedInventoryStore()
	if err != nil {
		logger.Warn(c, "Node inventory is unavailable", tag.Error(err))
		return nil
	}
	return s
}

// hostKeyStore opens the host keys trusted for the nodes of this
// installation.
func (c *Context) hostKeyStore() (*store.HostKeyStore, error) {
	return c.settings.HostKeyStore()
}

// workerNodeStore opens the worker nodes of this installation.
func (c *Context) workerNodeStore() (*store.WorkerNodeStore, error) {
	return c.serverStores.WorkerNodeStore()
}

// runtimeWorkerNodeStore returns the worker nodes for runs, or nil when the
// backend cannot hold them. A run that cannot go to workers, as one of a
// project other than the default one or of a library DAG cannot, has no
// worker nodes, so its steps never match a node they cannot run on.
func (c *Context) runtimeWorkerNodeStore() inventory.WorkerNodeStore {
	if c.checkProjectDispatch() != nil {
		return nil
	}
	workers, err := c.workerNodeStore()
	if err != nil {
		logger.Warn(c, "Worker nodes are unavailable", tag.Error(err))
		return nil
	}
	return workers
}

// runtimeHostKeyStore returns the host keys trusted for nodes, or nil when
// the backend cannot hold them.
func (c *Context) runtimeHostKeyStore() inventory.HostKeyStore {
	keys, err := c.hostKeyStore()
	if err != nil {
		logger.Warn(c, "Trusted host keys are unavailable", tag.Error(err))
		return nil
	}
	return keys
}
