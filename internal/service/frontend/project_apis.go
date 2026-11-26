// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"golang.org/x/sync/singleflight"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	fileeventstore "github.com/dagucloud/dagu/v2/internal/persis/file/eventstore"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	apiv1 "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
)

// projectAPIs keeps an API for each project other than the default one,
// built on the stores that the cache holds for the project. An API lives as
// long as those stores: a lookup drops the API of each project whose stores
// the cache has released.
type projectAPIs struct {
	ctx    context.Context
	stores *persisfile.ProjectStoreCache
	// root is the default project's API.
	root  func() *apiv1.API
	build func(project.Project, *persisfile.ProjectStores) (*apiv1.API, error)

	// builds lets one lookup build a project's API while the others of the
	// same project wait for it and those of other projects go on.
	builds singleflight.Group
	mu     sync.Mutex
	apis   map[string]projectAPI
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
	if api, ok := p.kept(proj.ID, stores); ok {
		return api, true
	}
	// The key names the stores too, so a lookup never joins a build on
	// stores that the cache has since replaced.
	key := fmt.Sprintf("%s\x00%p", proj.ID, stores)
	built, err, _ := p.builds.Do(key, func() (any, error) {
		if api, ok := p.kept(proj.ID, stores); ok {
			return api, nil
		}
		api, err := p.build(proj, stores)
		if err != nil {
			return nil, err
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		// The cache may have released the stores while the API was built.
		if current, ok := p.stores.Get(proj.ID); !ok || current != stores {
			return (*apiv1.API)(nil), nil
		}
		p.apis[proj.ID] = projectAPI{stores: stores, api: api}
		return api, nil
	})
	if err != nil {
		logger.Warn(p.ctx, "Failed to open the API of a project", tag.Name(proj.Name), tag.Error(err))
		return nil, false
	}
	api, _ := built.(*apiv1.API)
	return api, api != nil
}

// kept returns the API kept for the project with the given ID when it was
// built on stores. It first drops the APIs of the projects whose stores the
// cache has released.
func (p *projectAPIs) kept(id string, stores *persisfile.ProjectStores) (*apiv1.API, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for other, kept := range p.apis {
		if open, ok := p.stores.Get(other); !ok || open != kept.stores {
			delete(p.apis, other)
		}
	}
	kept, ok := p.apis[id]
	if !ok || kept.stores != stores {
		return nil, false
	}
	return kept.api, true
}

// dags returns the DAG store of p, and false while the cache holds no
// stores of p.
func (p *projectAPIs) dags(proj project.Project) (*persis.DAGRepository, bool) {
	if strings.EqualFold(proj.Name, project.DefaultName) {
		return p.stores.Default().DAGRepository, true
	}
	stores, ok := p.stores.Get(proj.ID)
	if !ok {
		return nil, false
	}
	return stores.DAGRepository, true
}

// projectSnapshot serves the registered projects from the list that the
// store cache last read, so a request reads no registry file. Until the
// cache has read the registry, it reads the registry itself.
type projectSnapshot struct {
	project.Registry
	stores *persisfile.ProjectStoreCache
}

// List returns every registered project, ordered by name.
func (s projectSnapshot) List(ctx context.Context) ([]project.Project, error) {
	projects, ok := s.stores.Projects()
	if !ok {
		return s.Registry.List(ctx)
	}
	return projects, nil
}

// Get returns the project whose name matches name without regard to case.
func (s projectSnapshot) Get(ctx context.Context, name string) (project.Project, error) {
	projects, ok := s.stores.Projects()
	if !ok {
		return s.Registry.Get(ctx, name)
	}
	for _, p := range projects {
		if strings.EqualFold(p.Name, name) {
			return p, nil
		}
	}
	return project.Project{}, fmt.Errorf("%w: %q", project.ErrNotFound, name)
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
func projectAPIOptions(
	ctx context.Context,
	cfg *config.Config,
	stores *persisfile.ProjectStores,
	base []apiv1.APIOption,
) ([]apiv1.APIOption, error) {
	opts := append(slices.Clone(base),
		apiv1.WithArtifactRepository(stores.ArtifactRepository),
		apiv1.WithSchedulerStateStore(stores.SchedulerStateStore),
		apiv1.WithBackfillStore(stores.BackfillStore),
		apiv1.WithPeriodStore(stores.PeriodStore),
		apiv1.WithProfileStore(stores.ProfileStore(ctx)),
		apiv1.WithSecretStore(stores.SecretStore(ctx)),
	)
	inventories, err := stores.CachedInventoryStore()
	if err != nil {
		return nil, fmt.Errorf("open the inventory store: %w", err)
	}
	settings, err := stores.DAGSettingsStore()
	if err != nil {
		return nil, fmt.Errorf("open the DAG settings store: %w", err)
	}
	pins, err := stores.DAGPinStore()
	if err != nil {
		return nil, fmt.Errorf("open the DAG pin store: %w", err)
	}
	opts = append(opts,
		apiv1.WithInventoryStore(inventories),
		apiv1.WithDAGSettingsStore(settings),
		apiv1.WithDAGPinStore(pins),
	)
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
