// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/agentsession"
	"github.com/dagucloud/dagu/v2/internal/backfill"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
)

// Persistence contains the persistence dependencies shared by commands and services.
type Persistence struct {
	DAGRepository             *persis.DAGRepository
	DAGRunRepository          *persis.DAGRunRepository
	ArtifactRepository        *persis.ArtifactRepository
	ProcRepository            *persis.ProcRepository
	QueueStore                queue.QueueStore
	StateStore                dagrun.StateStore
	SchedulerStateStore       schedulerstate.Store
	SchedulerPauseStore       schedulerstate.PauseStore
	RestartBackoffStore       schedulerstate.RestartBackoffStore
	PeriodStore               schedulerstate.PeriodStore
	WatchStateStore           schedulerstate.WatchStore
	ServiceRegistry           serviceregistry.ServiceRegistry
	DispatchTaskStore         dispatch.DispatchTaskStore
	WorkerHeartbeatStore      dispatch.WorkerHeartbeatStore
	DAGRunLeaseStore          dispatch.DAGRunLeaseStore
	ActiveDistributedRunStore dispatch.ActiveDistributedRunStore
	AgentSessionCleanupQueue  *agentsession.CleanupQueue
	BackfillStore             backfill.Store
}

// openProject opens the stores of the project p.
func openProject(
	ctx context.Context,
	cfg *config.Config,
	server *file.ServerStores,
	p project.Project,
	opts file.ProjectStoreOptions,
) (*file.ProjectStores, error) {
	stores, err := file.NewProjectStores(ctx, cfg, server, p, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to open the stores of the %s project: %w", p.Name, err)
	}
	if err := stores.ProcRepository.Validate(ctx); err != nil {
		return nil, fmt.Errorf("failed to validate proc directory %s: %w", stores.Paths.ProcDir, err)
	}
	return stores, nil
}

// followProjects keeps the stores of every registered project open, and
// records the projects it releases, until ctx ends, and returns the cache
// that holds them. It does nothing and returns nil while projects are off,
// since the default project is then the only one.
func (c *Context) followProjects(ctx context.Context) *file.ProjectStoreCache {
	cache := c.newProjectStoreCache()
	runProjectStores(ctx, cache)
	return cache
}

// newProjectStoreCache returns the cache that followProjects runs, or nil
// while projects are off.
func (c *Context) newProjectStoreCache() *file.ProjectStoreCache {
	if !c.Config.ProjectsEnabled {
		return nil
	}
	return file.NewProjectStoreCache(
		projectregistry.New(c.Config.Paths.DataDir),
		c.projectStores,
		func(ctx context.Context, p project.Project) (*file.ProjectStores, error) {
			return file.NewProjectStores(ctx, c.Config, c.serverStores, p, c.storeOptions)
		},
		c.serverStores.ProjectReleases,
	)
}

// runProjectStores runs cache until ctx ends. It does nothing on a nil
// cache.
func runProjectStores(ctx context.Context, cache *file.ProjectStoreCache) {
	if cache != nil {
		go cache.Run(ctx)
	}
}

// newPersistence gathers the stores that commands and services use from
// the server-wide stores and one project's stores.
func newPersistence(server *file.ServerStores, stores *file.ProjectStores) Persistence {
	return Persistence{
		DAGRepository:             stores.DAGRepository,
		DAGRunRepository:          stores.DAGRunRepository,
		ArtifactRepository:        stores.ArtifactRepository,
		ProcRepository:            stores.ProcRepository,
		QueueStore:                stores.QueueStore,
		StateStore:                stores.StateStore,
		SchedulerStateStore:       stores.SchedulerStateStore,
		SchedulerPauseStore:       server.SchedulerPauseStore,
		RestartBackoffStore:       server.RestartBackoffStore,
		PeriodStore:               stores.PeriodStore,
		WatchStateStore:           stores.WatchStateStore,
		ServiceRegistry:           server.ServiceRegistry,
		DispatchTaskStore:         server.DispatchTaskStore,
		WorkerHeartbeatStore:      server.WorkerHeartbeatStore,
		DAGRunLeaseStore:          server.DAGRunLeaseStore,
		ActiveDistributedRunStore: server.ActiveDistributedRunStore,
		AgentSessionCleanupQueue:  stores.AgentSessionCleanupQueue,
		BackfillStore:             stores.BackfillStore,
	}
}
