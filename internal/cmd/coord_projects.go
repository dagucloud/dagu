// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
)

// coordinatorProjects serves a coordinator the stores of the registered
// projects that a cache holds. It opens a project's settings stores once per
// set of stores the cache holds for it.
type coordinatorProjects struct {
	cache *file.ProjectStoreCache

	mu     sync.Mutex
	opened map[dispatch.ProjectScope]openedProjectStores
}

// openedProjectStores are the coordinator stores built from source.
type openedProjectStores struct {
	source *file.ProjectStores
	stores *coordinator.ProjectStores
}

// coordinatorProjectSource returns the coordinator's source of the stores
// that cache holds, or nil while projects are off and there is no cache.
func coordinatorProjectSource(cache *file.ProjectStoreCache) coordinator.ProjectSource {
	if cache == nil {
		return nil
	}
	return &coordinatorProjects{cache: cache, opened: make(map[dispatch.ProjectScope]openedProjectStores)}
}

func (p *coordinatorProjects) Stores(ctx context.Context, scope dispatch.ProjectScope) (*coordinator.ProjectStores, error) {
	project := p.cache.Default()
	if scope.ID != "" {
		var ok bool
		if project, ok = p.cache.Get(scope.ID); !ok {
			return nil, fmt.Errorf("project %s is not registered, or its stores are not open", scope.ID)
		}
	}
	runs := project
	if scope.Library {
		if runs = project.Library(); runs == nil {
			return nil, fmt.Errorf("project %s has no library stores", scope.ID)
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if opened, ok := p.opened[scope]; ok && opened.source == runs {
		return opened.stores, nil
	}
	stores := openCoordinatorStores(ctx, project, runs)
	p.opened[scope] = openedProjectStores{source: runs, stores: stores}
	return stores, nil
}

func (p *coordinatorProjects) Projects() []dispatch.ProjectScope {
	defaults := p.cache.Default()
	var scopes []dispatch.ProjectScope
	held := make(map[string]bool)
	for _, id := range p.cache.IDs() {
		held[id] = true
		if stores, ok := p.cache.Get(id); ok && stores != defaults {
			scopes = append(scopes, dispatch.ProjectScope{ID: id})
		}
	}
	// Forget the stores of the projects the cache released.
	p.mu.Lock()
	for scope := range p.opened {
		if scope.ID != "" && !held[scope.ID] {
			delete(p.opened, scope)
		}
	}
	p.mu.Unlock()
	return scopes
}

// openCoordinatorStores returns the coordinator stores of the runs that
// runs records, with the settings of project, whose runs they are.
func openCoordinatorStores(ctx context.Context, project, runs *file.ProjectStores) *coordinator.ProjectStores {
	stores := &coordinator.ProjectStores{
		DAGRunRepository:         runs.DAGRunRepository,
		LogDir:                   runs.Paths.LogDir,
		ArtifactDir:              runs.Paths.ArtifactDir,
		StateStore:               runs.StateStore,
		DAGRepository:            project.DAGRepository,
		SecretStore:              project.SecretStore(ctx),
		ProfileStore:             project.ProfileStore(ctx),
		AgentSessionCleanupQueue: runs.AgentSessionCleanupQueue,
	}
	if inventories, err := project.CachedInventoryStore(); err != nil {
		logger.Warn(ctx, "Node inventory of a project is unavailable", tag.Dir(project.Paths.StateDir), tag.Error(err))
	} else {
		stores.NodeInventory = inventories
	}
	if hostKeys, err := project.HostKeyStore(); err != nil {
		logger.Warn(ctx, "Trusted host keys of a project are unavailable", tag.Dir(project.Paths.StateDir), tag.Error(err))
	} else {
		stores.TrustedHostKeys = hostKeys
	}
	return stores
}
