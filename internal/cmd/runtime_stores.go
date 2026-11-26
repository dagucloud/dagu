// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/build"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/secret"
)

type executionStores struct {
	SecretStore          secret.Store
	ProfileStore         profile.Store
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
		stores.Nodes = projectNodes{inventories: inventories, workers: c.runtimeWorkerNodeStore()}
	}
	return stores
}

// projectNodes reads the nodes a run on the Dagu host targets: the nodes of
// the inventories and the worker nodes, which every project shares.
type projectNodes struct {
	inventories inventory.Store
	// workers is nil when the worker nodes cannot be read.
	workers inventory.WorkerNodeStore
}

// Nodes returns every node, in name order.
func (n projectNodes) Nodes(ctx context.Context) ([]inventory.ResolvedNode, error) {
	nodes, err := n.inventories.Nodes(ctx)
	if err != nil || n.workers == nil {
		return nodes, err
	}
	workers, err := n.workers.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read the worker nodes: %w", err)
	}
	return inventory.WithWorkers(nodes, workers), nil
}

// Select returns the nodes a selector matches, in name order.
func (n projectNodes) Select(ctx context.Context, selector string) ([]inventory.ResolvedNode, error) {
	filters, err := inventory.ParseSelector(selector)
	if err != nil {
		return nil, err
	}
	nodes, err := n.Nodes(ctx)
	if err != nil {
		return nil, err
	}
	return inventory.Select(nodes, filters), nil
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
	hostKeys, err := c.hostKeyStore()
	if err == nil {
		var s *store.InventoryStore
		s, err = store.NewCachedInventoryStore(
			c.backend.Collection(persis.CollectionInventories),
			c.backend.Lock(persis.CollectionInventories),
			hostKeys,
		)
		if err == nil {
			return s
		}
	}
	logger.Warn(c, "Node inventory is unavailable", tag.Error(err))
	return nil
}

// hostKeyStore opens the host keys trusted for the nodes of this
// installation.
func (c *Context) hostKeyStore() (*store.HostKeyStore, error) {
	return c.settings.HostKeyStore()
}

// workerNodeStore opens the worker nodes of this installation.
func (c *Context) workerNodeStore() (*store.WorkerNodeStore, error) {
	return store.NewWorkerNodeStore(c.backend.Collection(persis.CollectionWorkerNodes), c.Persistence.WorkerHeartbeatStore)
}

// runtimeWorkerNodeStore returns the worker nodes for runs, or nil when the
// backend cannot hold them.
func (c *Context) runtimeWorkerNodeStore() inventory.WorkerNodeStore {
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
