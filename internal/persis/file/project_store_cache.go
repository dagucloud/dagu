// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
)

// ProjectStoreCache holds the stores of every registered project, keyed by
// project ID, and follows the registry as it changes:
//
//	registry gains P            -> open P's stores
//	registry drops P, or marks  -> release P's stores, tell subscribers,
//	  it deleting                  then record every project marked
//	                               deleting as released
//
// Stores that fail to open are opened again after a wait that grows from
// openRetryMin to openRetryMax, until they open.
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
	// released is the last set of IDs recorded, and unrecorded reports that
	// a later record failed. changeMu guards them.
	released   []string
	unrecorded bool
	// onChange is called after each change the cache follows.
	onChange []func([]project.Project)

	// changeMu makes the follow of a change and the retry of failed opens
	// one at a time, and guards projects and failed: the registry's content
	// last followed, and whether a store of it failed to open.
	changeMu sync.Mutex
	projects []project.Project
	failed   bool
	// retry wakes the loop that opens failed stores again, which first
	// waits retryMin.
	retry    chan struct{}
	retryMin time.Duration
}

// openRetryMin and openRetryMax bound the wait before stores that failed
// to open are opened again.
const (
	openRetryMin = 5 * time.Second
	openRetryMax = 5 * time.Minute
)

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
		retry:    make(chan struct{}, 1),
		retryMin: openRetryMin,
	}
}

// Run registers the default project when the registry lacks it, then keeps
// the cache in step with the registry until ctx ends.
func (c *ProjectStoreCache) Run(ctx context.Context) {
	if _, err := c.registry.EnsureDefault(ctx); err != nil {
		logger.Warn(ctx, "Failed to register the default project", tag.Error(err))
	}
	var wg sync.WaitGroup
	wg.Go(func() { c.retryOpens(ctx) })
	c.registry.Watch(ctx, func(projects []project.Project) {
		c.changeMu.Lock()
		defer c.changeMu.Unlock()
		c.projects = projects
		c.change(ctx)
	})
	wg.Wait()
}

// change follows the registry's content last heard, tells subscribers, and
// then records the projects marked deleting as released. The caller holds
// changeMu.
func (c *ProjectStoreCache) change(ctx context.Context) {
	var deleting []string
	deleting, c.failed = c.follow(ctx, c.projects)
	for _, fn := range c.onChange {
		fn(c.projects)
	}
	c.recordReleased(ctx, deleting)
	if c.failed {
		select {
		case c.retry <- struct{}{}:
		default:
		}
	}
}

// retryOpens opens again the stores that failed to open, after a growing
// wait, until ctx ends.
func (c *ProjectStoreCache) retryOpens(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.retry:
		}
		wait := c.retryMin
		for c.retryFailed(ctx, wait) {
			wait = min(2*wait, openRetryMax)
		}
	}
}

// retryFailed waits, then follows the registry's content again if a store
// of it has not opened. It reports whether one still has not.
func (c *ProjectStoreCache) retryFailed(ctx context.Context, wait time.Duration) bool {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
	}
	c.changeMu.Lock()
	defer c.changeMu.Unlock()
	if c.failed {
		c.change(ctx)
	}
	return c.failed
}

// OnChange calls fn with every registered project each time the cache has
// followed a change of the registry, or opened stores that had failed to
// open, once Get serves the stores of the projects it opened. Calls are
// made one at a time. The cache records a project marked deleting as
// released only after fn returns, so fn returns once it has stopped using
// the stores of the projects the registry drops or marks deleting.
// OnChange must be called before Run.
func (c *ProjectStoreCache) OnChange(fn func([]project.Project)) {
	c.onChange = append(c.onChange, fn)
}

// Default returns the default project's stores.
func (c *ProjectStoreCache) Default() *ProjectStores {
	return c.defaults
}

// Get returns the stores of the project with the given ID. It reports false
// while the cache holds none: the registry lacks the project or marks it
// deleting, or its stores failed to open and are to be opened again.
func (c *ProjectStoreCache) Get(id string) (*ProjectStores, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	stores, ok := c.stores[id]
	return stores, ok
}

// follow opens and releases stores to match projects, the registry's
// content. It returns the projects marked deleting, and whether stores
// failed to open.
func (c *ProjectStoreCache) follow(ctx context.Context, projects []project.Project) (deleting []string, failed bool) {
	live := make(map[string]bool, len(projects))
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
				failed = true
				continue
			}
		}
		c.mu.Lock()
		c.stores[p.ID] = stores
		c.mu.Unlock()
	}
	return deleting, failed
}

// recordReleased records ids once the cache holds none of their stores,
// unless they are the IDs recorded last. After a failed record, the next
// change records its IDs whatever they are.
func (c *ProjectStoreCache) recordReleased(ctx context.Context, ids []string) {
	slices.Sort(ids)
	if !c.unrecorded && slices.Equal(ids, c.released) {
		return
	}
	if err := c.releases.SetReleasedProjects(ctx, ids); err != nil {
		c.unrecorded = true
		logger.Warn(ctx, "Failed to record released projects", tag.Error(err))
		return
	}
	c.released, c.unrecorded = ids, false
}
