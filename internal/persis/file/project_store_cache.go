// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"log/slog"
	"slices"
	"sync"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
)

// ProjectStoreCache holds the stores of every registered project, keyed by
// project ID, and follows the registry as it changes:
//
//	registry gains P            -> open P's stores
//	registry drops P, or marks  -> release P's stores, then record every
//	  it deleting                  project marked deleting as released
//
// File stores hold no file open between calls, so releasing a project's
// stores frees its directories once the calls in flight end.
type ProjectStoreCache struct {
	registry project.Registry
	defaults *ProjectStores
	open     ProjectStoreOpener
	releases serviceregistry.ProjectReleaseRecorder

	mu     sync.RWMutex
	stores map[string]*ProjectStores
	// released is the last set of IDs recorded. Only the goroutine that
	// runs Run touches it.
	released []string
}

// ProjectStoreOpener opens the stores of a project other than the default
// one.
type ProjectStoreOpener func(context.Context, project.Project) (*ProjectStores, error)

// NewProjectStoreCache returns a cache that serves defaults for the default
// project, which is never released, opens every other project with open,
// and records released projects with releases.
func NewProjectStoreCache(
	registry project.Registry,
	defaults *ProjectStores,
	open ProjectStoreOpener,
	releases serviceregistry.ProjectReleaseRecorder,
) *ProjectStoreCache {
	return &ProjectStoreCache{
		registry: registry,
		defaults: defaults,
		open:     open,
		releases: releases,
		stores:   make(map[string]*ProjectStores),
	}
}

// Run registers the default project when the registry lacks it, then keeps
// the cache in step with the registry until ctx ends.
func (c *ProjectStoreCache) Run(ctx context.Context) {
	if _, err := c.registry.EnsureDefault(ctx); err != nil {
		logger.Warn(ctx, "Failed to register the default project", tag.Error(err))
	}
	c.registry.Watch(ctx, func(projects []project.Project) {
		c.follow(ctx, projects)
	})
}

// Default returns the default project's stores.
func (c *ProjectStoreCache) Default() *ProjectStores {
	return c.defaults
}

// Get returns the stores of the project with the given ID. It reports false
// while the cache holds none: the registry lacks the project or marks it
// deleting, or its stores failed to open, which is tried again when the
// registry next changes.
func (c *ProjectStoreCache) Get(id string) (*ProjectStores, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	stores, ok := c.stores[id]
	return stores, ok
}

// follow opens and releases stores to match projects, the registry's
// content.
func (c *ProjectStoreCache) follow(ctx context.Context, projects []project.Project) {
	live := make(map[string]bool, len(projects))
	var deleting []string
	for _, p := range projects {
		if p.Deleting {
			deleting = append(deleting, p.ID)
		} else {
			live[p.ID] = true
		}
	}

	c.mu.Lock()
	for id := range c.stores {
		if !live[id] {
			delete(c.stores, id)
		}
	}
	c.mu.Unlock()

	for _, p := range projects {
		if !live[p.ID] {
			continue
		}
		if _, ok := c.Get(p.ID); ok {
			continue
		}
		stores := c.defaults
		if p.Name != project.DefaultName {
			var err error
			if stores, err = c.open(ctx, p); err != nil {
				logger.Warn(ctx, "Failed to open the stores of a project",
					slog.String("project", p.Name), tag.Error(err))
				continue
			}
		}
		c.mu.Lock()
		c.stores[p.ID] = stores
		c.mu.Unlock()
	}

	c.recordReleased(ctx, deleting)
}

// recordReleased records ids once the cache holds none of their stores, when
// they differ from the IDs recorded last. A failed record is tried again on
// the next change.
func (c *ProjectStoreCache) recordReleased(ctx context.Context, ids []string) {
	slices.Sort(ids)
	if slices.Equal(ids, c.released) {
		return
	}
	if err := c.releases.SetReleasedProjects(ctx, ids); err != nil {
		logger.Warn(ctx, "Failed to record released projects", tag.Error(err))
		return
	}
	c.released = ids
}
