// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"fmt"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
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

// Migrated returns nil: migration does not yet record which DAGs it
// placed in a project under their v2 webhook URLs, so every such URL
// reaches the default project.
func (w *webhookProjects) Migrated(context.Context, string) (*apiv1.WebhookProject, error) {
	return nil, nil
}

func (w *webhookProjects) Library(ctx context.Context, path string) (bool, error) {
	owner, err := w.server.DAGOwners().Of(ctx, path)
	return owner.Library, err
}

// target returns the webhook target of p, built once for each opening of
// p's stores.
func (w *webhookProjects) target(ctx context.Context, p project.Project) (*apiv1.WebhookProject, error) {
	stores, ok := w.cache.Get(p.ID)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.forgetClosed()
	if !ok {
		return nil, fmt.Errorf("the stores of project %s are not open", p.Name)
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

// forgetClosed drops the targets of projects whose stores the cache no
// longer holds. The caller holds mu.
func (w *webhookProjects) forgetClosed() {
	for id, open := range w.open {
		if stores, ok := w.cache.Get(id); !ok || stores != open.stores {
			delete(w.open, id)
		}
	}
}
