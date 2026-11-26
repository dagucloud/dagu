// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/build"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	filematerialization "github.com/dagucloud/dagu/v2/internal/persis/file/materialization"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/secret"
)

type executionStores struct {
	SecretStore          secret.Store
	ProfileStore         profile.Store
	MaterializationStore build.MaterializationStore
	Inventory            inventory.Store
	HostKeys             inventory.HostKeyStore
}

// runtimeStores creates the runtime store bundle for this command context.
func (c *Context) runtimeStores() executionStores {
	return executionStores{
		SecretStore:          file.NewSecretStore(c.Context, c.Config, c.backend.Collection(persis.CollectionSecrets)),
		ProfileStore:         file.NewProfileStore(c.Context, c.Config, c.backend.Collection(persis.CollectionProfiles)),
		MaterializationStore: filematerialization.New(filepath.Join(c.Config.Paths.DataDir, "materializations")),
		Inventory:            c.runtimeInventoryStore(),
		HostKeys:             c.runtimeHostKeyStore(),
	}
}

// inventoryStore opens the node inventories of this installation.
func (c *Context) inventoryStore() (inventory.Store, error) {
	hostKeys, err := c.hostKeyStore()
	if err != nil {
		return nil, err
	}
	s, err := store.NewInventoryStore(
		c.backend.Collection(persis.CollectionInventories),
		c.backend.Lock(persis.CollectionInventories),
		hostKeys,
	)
	if err != nil {
		return nil, err
	}
	return s, nil
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

// hostKeyStore opens the host keys trusted for the nodes of this
// installation.
func (c *Context) hostKeyStore() (*store.HostKeyStore, error) {
	return store.NewHostKeyStore(c.backend.Collection(persis.CollectionHostKeys))
}

// workerNodeStore opens the worker nodes of this installation.
func (c *Context) workerNodeStore() (*store.WorkerNodeStore, error) {
	return store.NewWorkerNodeStore(c.backend.Collection(persis.CollectionWorkerNodes), c.Persistence.WorkerHeartbeatStore)
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
