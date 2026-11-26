// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	apiv1 "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
)

// webhookProjects lets webhook requests reach the registered projects other
// than the default one. It keeps a project's webhooks in memory while the
// cache holds the project's stores.
type webhookProjects struct {
	cfg      *config.Config
	server   *persisfile.ServerStores
	registry project.Registry
	cache    *persisfile.ProjectStoreCache

	mu sync.Mutex
	// open holds each project's target by project ID, with the stores it
	// was built from.
	open map[string]webhookProject
}

type webhookProject struct {
	stores *persisfile.ProjectStores
	target *apiv1.WebhookProject
}

// NewWebhookProjects returns the projects that webhook requests reach: those
// in registry whose stores cache holds.
func NewWebhookProjects(
	cfg *config.Config,
	server *persisfile.ServerStores,
	registry project.Registry,
	cache *persisfile.ProjectStoreCache,
) apiv1.WebhookProjects {
	return &webhookProjects{
		cfg:      cfg,
		server:   server,
		registry: registry,
		cache:    cache,
		open:     make(map[string]webhookProject),
	}
}

func (w *webhookProjects) Get(ctx context.Context, name string) (*apiv1.WebhookProject, error) {
	p, err := w.registry.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	return w.target(ctx, p)
}

// Migrated finds a migrated project whose webhook for the DAG predates the
// project's registration, so migration brought it from v2 with the DAG, and
// whose DAG of that name still exists as its own. A project whose webhooks
// cannot be read fails the lookup only when it has a DAG of that name, since
// the default project's DAG might then not be the one the URL reaches.
func (w *webhookProjects) Migrated(ctx context.Context, dagName string) (*apiv1.WebhookProject, error) {
	projects, err := w.registry.List(ctx)
	if err != nil {
		return nil, err
	}
	w.forgetUnlisted(projects)
	for _, p := range projects {
		if p.MigratedFrom == "" || p.Deleting || strings.EqualFold(p.Name, project.DefaultName) {
			continue
		}
		target, err := w.target(ctx, p)
		if errors.Is(err, project.ErrNotFound) {
			continue
		}
		var webhook *auth.Webhook
		if err == nil {
			webhook, err = target.Webhooks.GetByDAGName(ctx, dagName)
		}
		if errors.Is(err, auth.ErrWebhookNotFound) {
			continue
		}
		if err != nil {
			if err := w.unreadable(ctx, p, dagName, err); err != nil {
				return nil, err
			}
			continue
		}
		if !webhook.CreatedAt.Before(p.CreatedAt) {
			continue
		}
		own, err := w.ownDAG(ctx, target.DAGRepository, p.Name, dagName)
		if err != nil {
			return nil, err
		}
		if own {
			return target, nil
		}
	}
	return nil, nil
}

// unreadable returns err, the failure to read p's webhooks, when p has a
// DAG called dagName, and logs it otherwise.
func (w *webhookProjects) unreadable(ctx context.Context, p project.Project, dagName string, err error) error {
	stores, ok := w.cache.Get(p.ID)
	if !ok {
		return nil
	}
	own, ownErr := w.ownDAG(ctx, stores.DAGRepository, p.Name, dagName)
	if own || ownErr != nil {
		return errors.Join(err, ownErr)
	}
	logger.Warn(ctx, "Failed to read a migrated project's webhooks", tag.Name(p.Name), tag.Error(err))
	return nil
}

func (w *webhookProjects) Library(ctx context.Context, path string) (bool, error) {
	owner, err := w.server.DAGOwners().Of(ctx, path)
	return owner.Library, err
}

// ownDAG reports whether the project called projectName, whose DAGs dags
// holds, has a DAG called dagName of its own, not one found in the library.
func (w *webhookProjects) ownDAG(ctx context.Context, dags *persis.DAGRepository, projectName, dagName string) (bool, error) {
	dag, err := dags.GetMetadata(ctx, dagName)
	if errors.Is(err, persis.ErrDAGNotFound) || errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	owner, err := w.server.DAGOwners().OfIn(ctx, projectName, dag.Location)
	return owner.Is(projectName), err
}

// target returns the webhook target of p, built once for each opening of
// p's stores.
func (w *webhookProjects) target(ctx context.Context, p project.Project) (*apiv1.WebhookProject, error) {
	stores, ok := w.cache.Get(p.ID)
	w.mu.Lock()
	defer w.mu.Unlock()
	if !ok {
		delete(w.open, p.ID)
		return nil, fmt.Errorf("project %s is not open: %w", p.Name, project.ErrNotFound)
	}
	if open, ok := w.open[p.ID]; ok && open.stores == stores {
		return open.target, nil
	}

	enc, encErr := w.server.Encryptor()
	if encErr != nil {
		logger.Warn(ctx, "Failed to prepare encryption for a project's webhooks", tag.Name(p.Name), tag.Error(encErr))
	}
	webhooks, err := stores.WebhookStore(enc)
	if err != nil {
		return nil, fmt.Errorf("open the webhooks of project %s: %w", p.Name, err)
	}
	settings, err := stores.DAGSettingsStore()
	if err != nil {
		return nil, fmt.Errorf("open the DAG settings of project %s: %w", p.Name, err)
	}
	baseConfig, _ := stores.BaseConfig()
	target := &apiv1.WebhookProject{
		Name:             p.Name,
		Webhooks:         webhooks,
		DAGRepository:    stores.DAGRepository,
		DAGRunRepository: stores.DAGRunRepository,
		DAGRunManager: runtime.NewManager(stores.DAGRunRepository, stores.ProcRepository, w.cfg,
			runtime.WithManagerProject(p.Name)),
		QueueStore:       stores.QueueStore,
		ProfileStore:     stores.ProfileStore(ctx),
		DAGSettingsStore: settings,
		LogDir:           stores.Paths.LogDir,
		ArtifactDir:      stores.Paths.ArtifactDir,
		BaseConfig:       baseConfig,
	}
	// Without the encryptor, the next request opens the webhooks again, so
	// they become readable once the key is.
	if encErr == nil {
		w.open[p.ID] = webhookProject{stores: stores, target: target}
	}
	return target, nil
}

// forgetUnlisted drops the targets of projects the registry no longer
// lists.
func (w *webhookProjects) forgetUnlisted(projects []project.Project) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for id := range w.open {
		if !slices.ContainsFunc(projects, func(p project.Project) bool { return p.ID == id }) {
			delete(w.open, id)
		}
	}
}
