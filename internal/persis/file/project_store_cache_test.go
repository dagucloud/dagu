// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
)

var (
	defaultProject = project.Project{ID: "id-default", Name: project.DefaultName}
	weatherProject = project.Project{ID: "id-weather", Name: "weather"}
)

// The cache opens a project's stores when the registry gains it, and
// releases them when the registry marks it deleting or drops it. Each time
// the projects marked deleting change, it records them after releasing
// their stores.
func TestProjectStoreCacheFollowsRegistry(t *testing.T) {
	defaults := &persisfile.ProjectStores{}
	var opened []string
	// recordsAtOpen counts the records made before each open.
	var recordsAtOpen []int
	registry, releases := newFakeRegistry(), &releaseLog{}
	cache := persisfile.NewProjectStoreCache(registry, defaults,
		func(_ context.Context, p project.Project) (*persisfile.ProjectStores, error) {
			opened = append(opened, p.Name)
			recordsAtOpen = append(recordsAtOpen, len(releases.recorded()))
			return &persisfile.ProjectStores{}, nil
		}, releases)
	runCache(t, cache)

	registry.show(defaultProject, weatherProject)
	assert.True(t, registry.ensured.Load())
	assert.Same(t, defaults, cache.Default())
	got, ok := cache.Get(defaultProject.ID)
	require.True(t, ok)
	assert.Same(t, defaults, got)
	first, ok := cache.Get(weatherProject.ID)
	require.True(t, ok)
	assert.NotSame(t, defaults, first)
	assert.Empty(t, releases.recorded())

	deleting := weatherProject
	deleting.Deleting = true
	registry.show(defaultProject, deleting)
	_, ok = cache.Get(weatherProject.ID)
	assert.False(t, ok)
	assert.Equal(t, [][]string{{weatherProject.ID}}, releases.recorded())

	// A refused delete removes the mark, so the stores open again and the
	// release is withdrawn.
	registry.show(defaultProject, weatherProject)
	again, ok := cache.Get(weatherProject.ID)
	require.True(t, ok)
	assert.NotSame(t, first, again)
	assert.Equal(t, [][]string{{weatherProject.ID}, nil}, releases.recorded())
	assert.Equal(t, []int{0, 2}, recordsAtOpen,
		"the release is withdrawn before the stores open again, so a later delete waits")

	registry.show(defaultProject)
	_, ok = cache.Get(weatherProject.ID)
	assert.False(t, ok)
	assert.Len(t, releases.recorded(), 2, "nothing new to record")
	assert.Equal(t, []string{"weather", "weather"}, opened)
}

// Stores that fail to open stay closed until the registry changes again.
func TestProjectStoreCacheRetriesFailedOpen(t *testing.T) {
	fail := true
	registry := newFakeRegistry()
	cache := persisfile.NewProjectStoreCache(registry, &persisfile.ProjectStores{},
		func(context.Context, project.Project) (*persisfile.ProjectStores, error) {
			if fail {
				return nil, errors.New("disk full")
			}
			return &persisfile.ProjectStores{}, nil
		}, &releaseLog{})
	runCache(t, cache)

	registry.show(defaultProject, weatherProject)
	_, ok := cache.Get(weatherProject.ID)
	assert.False(t, ok)

	fail = false
	registry.show(defaultProject, weatherProject, project.Project{ID: "id-billing", Name: "billing"})
	_, ok = cache.Get(weatherProject.ID)
	assert.True(t, ok)
}

// After a failed record, the cache records again even when the projects
// marked deleting are back to the set it recorded last, so the entry does
// not keep a release the process took back.
func TestProjectStoreCacheRetriesFailedRecord(t *testing.T) {
	registry, releases := newFakeRegistry(), &releaseLog{}
	cache := persisfile.NewProjectStoreCache(registry, &persisfile.ProjectStores{},
		func(context.Context, project.Project) (*persisfile.ProjectStores, error) {
			return &persisfile.ProjectStores{}, nil
		}, releases)
	runCache(t, cache)

	deleting := weatherProject
	deleting.Deleting = true
	releases.setFailing(true)
	registry.show(defaultProject, deleting)
	releases.setFailing(false)
	registry.show(defaultProject, weatherProject)
	assert.Equal(t, [][]string{{weatherProject.ID}, nil}, releases.recorded())
}

// A process that starts while a project is marked deleting registers the
// default project and records the release in its service registry entry,
// where another process reads it.
func TestProjectStoreCacheRecordsReleaseInServiceRegistry(t *testing.T) {
	ctx := context.Background()
	cfg := projectStoresConfig(t, true)
	server := persisfile.NewServerStores(cfg)
	registry := projectregistry.New(cfg.Paths.DataDir)
	weather, err := registry.Create(ctx, project.CreateInput{Name: "weather"})
	require.NoError(t, err)
	_, err = registry.SetDeleting(ctx, weather.ID, true)
	require.NoError(t, err)
	require.NoError(t, server.ServiceRegistry.Register(ctx, serviceregistry.ServiceNameScheduler,
		serviceregistry.HostInfo{ID: "scheduler-1", Host: "localhost"}))
	t.Cleanup(func() { server.ServiceRegistry.Unregister(context.Background()) })

	open := func(ctx context.Context, p project.Project) (*persisfile.ProjectStores, error) {
		return persisfile.NewProjectStores(ctx, cfg, server, p, persisfile.ProjectStoreOptions{})
	}
	defaults, err := open(ctx, project.Project{Name: project.DefaultName})
	require.NoError(t, err)
	cache := persisfile.NewProjectStoreCache(registry, defaults, open, server.ProjectReleases)
	runCache(t, cache)

	require.Eventually(t, func() bool {
		members, err := persisfile.NewServiceRegistry(cfg).GetServiceMembers(ctx, serviceregistry.ServiceNameScheduler)
		return err == nil && len(members) == 1 && slices.Equal(members[0].ReleasedProjects, []string{weather.ID})
	}, 5*time.Second, 20*time.Millisecond)
	registered, err := registry.Get(ctx, project.DefaultName)
	require.NoError(t, err)
	// The release is recorded before the default project's stores open.
	var got *persisfile.ProjectStores
	require.Eventually(t, func() bool {
		var ok bool
		got, ok = cache.Get(registered.ID)
		return ok
	}, 5*time.Second, 20*time.Millisecond)
	assert.Same(t, defaults, got)
	_, ok := cache.Get(weather.ID)
	assert.False(t, ok)
}

// runCache runs cache until the test ends.
func runCache(t *testing.T, cache *persisfile.ProjectStoreCache) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		cache.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

// fakeRegistry hands the cache each registry content a test shows, and
// waits until the cache has followed it.
type fakeRegistry struct {
	project.Registry
	contents chan []project.Project
	followed chan struct{}
	ensured  atomic.Bool
}

func newFakeRegistry() *fakeRegistry {
	return &fakeRegistry{contents: make(chan []project.Project), followed: make(chan struct{})}
}

func (r *fakeRegistry) show(projects ...project.Project) {
	r.contents <- projects
	<-r.followed
}

func (r *fakeRegistry) EnsureDefault(context.Context) (project.Project, error) {
	r.ensured.Store(true)
	return defaultProject, nil
}

func (r *fakeRegistry) Watch(ctx context.Context, onChange func([]project.Project)) {
	for {
		select {
		case <-ctx.Done():
			return
		case projects := <-r.contents:
			onChange(projects)
			r.followed <- struct{}{}
		}
	}
}

// releaseLog keeps each set of released projects the cache tries to record.
type releaseLog struct {
	mu      sync.Mutex
	sets    [][]string
	failing bool
}

func (l *releaseLog) SetReleasedProjects(_ context.Context, ids []string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sets = append(l.sets, slices.Clone(ids))
	if l.failing {
		return errors.New("disk full")
	}
	return nil
}

func (l *releaseLog) setFailing(failing bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failing = failing
}

func (l *releaseLog) recorded() [][]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.sets)
}
