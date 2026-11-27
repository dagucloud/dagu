// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/v2/api/v1"
	authmodel "github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/dagucloud/dagu/v2/internal/project"
	apiv1 "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/v2/internal/wiki"
)

// projectLifecycle creates and deletes the projects of a registry on the
// server's stores.
type projectLifecycle struct {
	cfg      *config.Config
	server   *persisfile.ServerStores
	registry project.Registry
	delete   persisfile.DeleteProjectOptions
}

var _ apiv1.ProjectLifecycle = (*projectLifecycle)(nil)

// NewProjectLifecycle returns what creates and deletes the projects of the
// registry in paths.data_dir, for a process that serves no API. It reads
// the default wiki only when its directory exists.
func NewProjectLifecycle(cfg *config.Config, server *persisfile.ServerStores) (apiv1.ProjectLifecycle, error) {
	registry := projectregistry.New(cfg.Paths.DataDir)
	var wikis *projectWikis
	if exists, err := dirExists(cfg.Paths.WikiDir); err != nil {
		return nil, err
	} else if exists {
		shared, err := newWikiStore(cfg)
		if err != nil {
			return nil, err
		}
		wikis = newProjectWikis(cfg, registry, shared)
	}
	return newProjectLifecycle(cfg, server, registry, wikis, nil, nil), nil
}

// newProjectLifecycle returns the lifecycle of registry's projects. wikis
// may be nil when there is no default wiki, and users and keys nil to open
// the server's.
func newProjectLifecycle(
	cfg *config.Config,
	server *persisfile.ServerStores,
	registry project.Registry,
	wikis *projectWikis,
	users authmodel.UserStore,
	keys authmodel.APIKeyStore,
) *projectLifecycle {
	l := &projectLifecycle{
		cfg:      cfg,
		server:   server,
		registry: registry,
		delete:   persisfile.DeleteProjectOptions{Users: users, APIKeys: keys},
	}
	if wikis != nil {
		l.delete.Wiki = wikis
	}
	return l
}

func (l *projectLifecycle) Create(ctx context.Context, in project.CreateInput) (project.Project, error) {
	return persisfile.CreateProject(ctx, l.cfg, l.registry, in)
}

func (l *projectLifecycle) SetDisplayName(ctx context.Context, id, displayName string) (project.Project, error) {
	return l.registry.SetDisplayName(ctx, id, displayName)
}

func (l *projectLifecycle) Delete(ctx context.Context, name string) (project.Project, error) {
	return persisfile.DeleteProject(ctx, l.cfg, l.server, l.registry, name, l.delete)
}

func (l *projectLifecycle) State(ctx context.Context, p project.Project) (api.ProjectState, error) {
	state, err := l.server.ProjectState(ctx, p)
	return api.ProjectState(state), err
}

func (l *projectLifecycle) Unattached(ctx context.Context) ([]api.UnattachedStateDir, error) {
	dirs, err := l.server.UnattachedStateDirs(ctx, l.registry)
	if err != nil {
		return nil, err
	}
	out := make([]api.UnattachedStateDir, 0, len(dirs))
	for _, dir := range dirs {
		out = append(out, api.UnattachedStateDir{Path: dir.Path, Name: dir.Name})
	}
	return out, nil
}

var _ persisfile.ProjectWiki = (*projectWikis)(nil)

// InPlacePages reports whether <wiki>/X/, the folder that p, a migrated
// project, reads in place, holds pages. The default wiki shows them once
// p is no longer registered.
func (w *projectWikis) InPlacePages(ctx context.Context, p project.Project) (bool, error) {
	if _, err := w.migratedRoots(ctx); err != nil {
		return false, err
	}
	view := &wikiView{store: w.shared, root: w.inPlaceRoot(p.MigratedFrom)}
	pages, err := view.ListFlat(ctx, wiki.ListPagesOptions{Page: 1, PerPage: 1})
	if err != nil {
		return false, fmt.Errorf("list the wiki pages in %s: %w", p.MigratedFrom, err)
	}
	return len(pages.Items) > 0, nil
}

// SettleHistory does nothing yet: the wiki store cannot move a migrated
// project's page history out of the default wiki's stores, so the history
// of p's in-place pages stays there.
func (w *projectWikis) SettleHistory(context.Context, project.Project) error {
	return nil
}
