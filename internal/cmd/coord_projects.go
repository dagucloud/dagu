// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"golang.org/x/sync/singleflight"
)

// projectStoreRetryDelay is how long a coordinator serves the stores of a
// project whose settings stores failed to open before it opens them again.
const projectStoreRetryDelay = 10 * time.Second

// coordinatorProjects serves a coordinator the stores of the registered
// projects that a cache holds. It opens a project's settings stores once per
// set of stores the cache holds for it.
type coordinatorProjects struct {
	cfg   *config.Config
	cache *file.ProjectStoreCache

	mu      sync.Mutex
	opened  map[dispatch.ProjectScope]openedProjectStores
	opening singleflight.Group
}

// openedProjectStores are the coordinator stores built from source. When
// some failed to open, retryAt is when they are opened again.
type openedProjectStores struct {
	source  *file.ProjectStores
	stores  *coordinator.ProjectStores
	retryAt time.Time
}

// coordinatorProjectSource returns the coordinator's source of the stores
// that cache holds, or nil while projects are off and there is no cache.
func coordinatorProjectSource(cfg *config.Config, cache *file.ProjectStoreCache) coordinator.ProjectSource {
	if cache == nil {
		return nil
	}
	return &coordinatorProjects{cfg: cfg, cache: cache, opened: make(map[dispatch.ProjectScope]openedProjectStores)}
}

func (p *coordinatorProjects) Stores(ctx context.Context, scope dispatch.ProjectScope) (*coordinator.ProjectStores, error) {
	project := p.cache.Default()
	if scope.ID != "" {
		var ok bool
		if project, ok = p.cache.Get(scope.ID); !ok {
			return nil, fmt.Errorf("project %s is not registered, or its stores are not open yet", scope.ID)
		}
		// The default project is addressed by the empty ID alone, so its
		// runs have one key for locks and leases.
		if project == p.cache.Default() {
			return nil, fmt.Errorf("project %s is the default project, which calls address without an ID", scope.ID)
		}
	}
	runs := project
	if scope.Library {
		if runs = project.Library(); runs == nil {
			return nil, fmt.Errorf("project %s has no library stores", scope.ID)
		}
	}

	p.mu.Lock()
	opened, ok := p.opened[scope]
	p.mu.Unlock()
	if ok && opened.source == runs && (opened.retryAt.IsZero() || time.Now().Before(opened.retryAt)) {
		return opened.stores, nil
	}
	// Stores open without the lock, so a slow open delays no other project,
	// and once per scope however many calls wait for them.
	stores, _, _ := p.opening.Do(fmt.Sprintf("%s\x00%t", scope.ID, scope.Library), func() (any, error) {
		stores, complete := openCoordinatorStores(ctx, p.cfg, project, runs)
		opened := openedProjectStores{source: runs, stores: stores}
		if !complete {
			opened.retryAt = time.Now().Add(projectStoreRetryDelay)
		}
		p.mu.Lock()
		p.opened[scope] = opened
		p.mu.Unlock()
		return stores, nil
	})
	return stores.(*coordinator.ProjectStores), nil
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
// runs records, with the settings of project, whose runs they are, and
// whether every store opened.
func openCoordinatorStores(ctx context.Context, cfg *config.Config, project, runs *file.ProjectStores) (*coordinator.ProjectStores, bool) {
	stores := &coordinator.ProjectStores{
		DAGRunRepository:         runs.DAGRunRepository,
		LogDir:                   runs.Paths.LogDir,
		ArtifactDir:              runs.Paths.ArtifactDir,
		StateStore:               runs.StateStore,
		DAGRepository:            project.DAGRepository,
		SecretStore:              project.SecretStore(ctx),
		ProfileStore:             project.ProfileStore(ctx),
		AgentSessionCleanupQueue: runs.AgentSessionCleanupQueue,
		EventService:             newEventService(ctx, cfg, project.Paths.EventStoreDir),
	}
	complete := stores.SecretStore != nil && stores.ProfileStore != nil &&
		(stores.EventService != nil || !cfg.EventStore.Enabled)
	if inventories, err := project.CachedInventoryStore(); err != nil {
		complete = false
		logger.Warn(ctx, "Node inventory of a project is unavailable", tag.Dir(project.Paths.StateDir), tag.Error(err))
	} else {
		stores.NodeInventory = inventories
	}
	if hostKeys, err := project.HostKeyStore(); err != nil {
		complete = false
		logger.Warn(ctx, "Trusted host keys of a project are unavailable", tag.Dir(project.Paths.StateDir), tag.Error(err))
	} else {
		stores.TrustedHostKeys = hostKeys
	}
	return stores, complete
}
