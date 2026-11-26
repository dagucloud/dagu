// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	fileeventstore "github.com/dagucloud/dagu/v2/internal/persis/file/eventstore"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	apiv1 "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
)

// projectAPIs keeps an API for each project other than the default one,
// built on the stores that the cache holds for the project. An API lives as
// long as those stores: once the cache releases them, the next lookup of
// another project drops it.
type projectAPIs struct {
	ctx    context.Context
	stores *persisfile.ProjectStoreCache
	// root is the default project's API.
	root  func() *apiv1.API
	build func(project.Project, *persisfile.ProjectStores) (*apiv1.API, error)

	mu   sync.Mutex
	apis map[string]projectAPI
}

type projectAPI struct {
	stores *persisfile.ProjectStores
	api    *apiv1.API
}

// get returns the API of p, and false while the cache holds no stores of
// p.
func (p *projectAPIs) get(proj project.Project) (*apiv1.API, bool) {
	if strings.EqualFold(proj.Name, project.DefaultName) {
		return p.root(), true
	}
	stores, ok := p.stores.Get(proj.ID)
	if !ok {
		return nil, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if kept, ok := p.apis[proj.ID]; ok && kept.stores == stores {
		return kept.api, true
	}
	for id := range p.apis {
		if _, open := p.stores.Get(id); !open {
			delete(p.apis, id)
		}
	}
	api, err := p.build(proj, stores)
	if err != nil {
		logger.Warn(p.ctx, "Failed to open the API of a project", tag.Name(proj.Name), tag.Error(err))
		return nil, false
	}
	p.apis[proj.ID] = projectAPI{stores: stores, api: api}
	return api, true
}

// projectConfig returns cfg with the project's DAG directory, base config,
// and run, log, and artifact directories in place of the default
// project's, since handlers read these paths beside the stores.
func projectConfig(cfg *config.Config, stores *persisfile.ProjectStores) *config.Config {
	projectCfg := *cfg
	paths := &projectCfg.Paths
	paths.DAGsDir = stores.Paths.DAGsDir
	paths.BaseConfig, _ = stores.BaseConfig()
	paths.DAGRunsDir = stores.Paths.DAGRunsDir
	paths.DAGRunWorkDir = stores.Paths.DAGRunWorkDir
	paths.LogDir = stores.Paths.LogDir
	paths.ArtifactDir = stores.Paths.ArtifactDir
	return &projectCfg
}

// projectAPIOptions returns the options of a project's API: base, the
// options of the default project's API, with the project's own stores in
// place of the default project's.
func projectAPIOptions(cfg *config.Config, stores *persisfile.ProjectStores, base []apiv1.APIOption) ([]apiv1.APIOption, error) {
	opts := append(slices.Clone(base),
		apiv1.WithArtifactRepository(stores.ArtifactRepository),
		apiv1.WithSchedulerStateStore(stores.SchedulerStateStore),
		apiv1.WithBackfillStore(stores.BackfillStore),
		apiv1.WithPeriodStore(stores.PeriodStore),
	)
	inventories, err := stores.CachedInventoryStore()
	if err != nil {
		return nil, fmt.Errorf("open the inventory store: %w", err)
	}
	opts = append(opts, apiv1.WithInventoryStore(inventories))
	if cfg.EventStore.Enabled {
		events, err := fileeventstore.New(stores.Paths.EventStoreDir)
		if err != nil {
			return nil, fmt.Errorf("open the event store: %w", err)
		}
		opts = append(opts, apiv1.WithEventService(eventstore.New(events)))
	}
	return opts, nil
}

// projectRunManager returns the manager of the runs of p.
func projectRunManager(cfg *config.Config, p project.Project, stores *persisfile.ProjectStores) runtime.Manager {
	return runtime.NewManager(stores.DAGRunRepository, stores.ProcRepository, cfg, runtime.WithManagerProject(p.Name))
}
