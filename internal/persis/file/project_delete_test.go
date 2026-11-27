// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	fileserviceregistry "github.com/dagucloud/dagu/v2/internal/persis/file/serviceregistry"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
)

// deleteFixture is a data directory with projects on and the project
// weather created.
type deleteFixture struct {
	cfg      *config.Config
	server   *persisfile.ServerStores
	registry *projectregistry.Registry
	weather  project.Project
}

func newDeleteFixture(t *testing.T) *deleteFixture {
	t.Helper()
	cfg := projectStoresConfig(t, true)
	cfg.Paths.UsersDir = filepath.Join(cfg.Paths.DataDir, "users")
	cfg.Paths.APIKeysDir = filepath.Join(cfg.Paths.DataDir, "apikeys")
	registry := projectregistry.New(cfg.Paths.DataDir)
	weather, err := persisfile.CreateProject(context.Background(), cfg, registry, project.CreateInput{Name: "weather"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(cfg.Paths.ProjectsDir, "weather", "forecast.yaml"), []byte("steps:\n  - run: echo ok\n"), 0o600))
	return &deleteFixture{cfg: cfg, server: persisfile.NewServerStores(cfg), registry: registry, weather: weather}
}

func (f *deleteFixture) delete(name string, opts persisfile.DeleteProjectOptions) (project.Project, error) {
	return persisfile.DeleteProject(context.Background(), f.cfg, f.server, f.registry, name, opts)
}

func (f *deleteFixture) stores(t *testing.T) *persisfile.ProjectStores {
	t.Helper()
	stores, err := persisfile.NewProjectStores(context.Background(), f.cfg, f.server, f.weather, persisfile.ProjectStoreOptions{})
	require.NoError(t, err)
	return stores
}

// requireKept fails unless weather is registered, not marked deleting, and
// keeps its directories.
func (f *deleteFixture) requireKept(t *testing.T) {
	t.Helper()
	p, err := f.registry.Get(context.Background(), "weather")
	require.NoError(t, err)
	assert.False(t, p.Deleting, "a refused delete removes its mark")
	assert.FileExists(t, filepath.Join(f.cfg.Paths.ProjectsDir, "weather", "forecast.yaml"))
	assert.DirExists(t, filepath.Join(f.cfg.Paths.DataDir, "projects", "weather"))
}

// TestDeleteProject proves Spec 102 "Delete": a delete removes the
// project's DAG directory, state directories, grants, each API key left
// with no role, and its registry entry.
func TestDeleteProject(t *testing.T) {
	ctx := context.Background()
	f := newDeleteFixture(t)
	intraday, err := persisfile.CreateProject(ctx, f.cfg, f.registry, project.CreateInput{Name: "intraday"})
	require.NoError(t, err)
	onWeather := auth.ProjectGrant{ProjectID: f.weather.ID, Role: auth.RoleOperator}
	onIntraday := auth.ProjectGrant{ProjectID: intraday.ID, Role: auth.RoleViewer}

	users, err := f.server.UserStore()
	require.NoError(t, err)
	user := auth.NewUser("alice", "hash", auth.RoleViewer)
	user.ProjectGrants = []auth.ProjectGrant{onWeather, onIntraday}
	require.NoError(t, users.Create(ctx, user))

	keys, err := f.server.APIKeyStore()
	require.NoError(t, err)
	noServerRole := &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{}}
	newKey := func(name string, access *auth.WorkspaceAccess, grants ...auth.ProjectGrant) *auth.APIKey {
		key, err := auth.NewAPIKey(name, "", auth.RoleViewer, "hash-"+name, name, "admin")
		require.NoError(t, err)
		key.KeyDigest = "digest-" + name
		key.WorkspaceAccess, key.ProjectGrants = access, grants
		require.NoError(t, keys.Create(ctx, key))
		return key
	}
	onlyWeather := newKey("only-weather", noServerRole, onWeather)
	both := newKey("both", noServerRole, onWeather, onIntraday)
	serverWide := newKey("server-wide", auth.AllWorkspaceAccess(), onWeather)
	// A key limited to workspaces has no server-wide role, as the access
	// checker reads it.
	workspaceOnly := newKey("workspace-only",
		&auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{{Workspace: "ops", Role: auth.RoleViewer}}}, onWeather)

	deleted, err := f.delete("Weather", persisfile.DeleteProjectOptions{})
	require.NoError(t, err)
	assert.Equal(t, f.weather.ID, deleted.ID)

	_, err = f.registry.Get(ctx, "weather")
	require.ErrorIs(t, err, project.ErrNotFound)
	assert.NoDirExists(t, filepath.Join(f.cfg.Paths.ProjectsDir, "weather"))
	assert.NoDirExists(t, filepath.Join(f.cfg.Paths.DataDir, "projects", "weather"))
	assert.DirExists(t, filepath.Join(f.cfg.Paths.DataDir, "projects", "intraday"))

	stored, err := users.GetByID(ctx, user.ID)
	require.NoError(t, err)
	assert.Equal(t, []auth.ProjectGrant{onIntraday}, stored.ProjectGrants)
	for _, key := range []*auth.APIKey{onlyWeather, workspaceOnly} {
		_, err = keys.GetByID(ctx, key.ID)
		require.ErrorIs(t, err, auth.ErrAPIKeyNotFound, "%s: a key whose only role was on weather is removed", key.Name)
	}
	kept, err := keys.GetByID(ctx, both.ID)
	require.NoError(t, err)
	assert.Equal(t, []auth.ProjectGrant{onIntraday}, kept.ProjectGrants)
	kept, err = keys.GetByID(ctx, serverWide.ID)
	require.NoError(t, err)
	assert.Empty(t, kept.ProjectGrants)

	_, err = persisfile.CreateProject(ctx, f.cfg, f.registry, project.CreateInput{Name: "weather"})
	require.NoError(t, err, "nothing of the deleted project refuses a new one of its name")
}

// fakeWiki reports in-place wiki pages for every migrated project.
type fakeWiki struct{ settled []string }

func (w *fakeWiki) InPlacePages(context.Context, project.Project) (bool, error) { return true, nil }

func (w *fakeWiki) SettleHistory(_ context.Context, p project.Project) error {
	w.settled = append(w.settled, p.Name)
	return nil
}

// TestDeleteProjectRefusals proves Spec 102 "Errors" for delete: each
// refusal changes nothing.
func TestDeleteProjectRefusals(t *testing.T) {
	ctx := context.Background()

	t.Run("Default", func(t *testing.T) {
		f := newDeleteFixture(t)
		_, err := f.delete("DEFAULT", persisfile.DeleteProjectOptions{})
		require.ErrorIs(t, err, project.ErrDefaultProject)
	})
	t.Run("NotFound", func(t *testing.T) {
		f := newDeleteFixture(t)
		_, err := f.delete("nowhere", persisfile.DeleteProjectOptions{})
		require.ErrorIs(t, err, project.ErrNotFound)
	})

	// Each refusal names its reason.
	refusals := map[string]struct {
		setup  func(t *testing.T, f *deleteFixture)
		reason string
	}{
		"QueuedRun": {reason: "is queued", setup: func(t *testing.T, f *deleteFixture) {
			stores := f.stores(t)
			require.NoError(t, stores.QueueStore.Enqueue(ctx, "forecast", queue.QueuePriorityLow, ir.NewDAGRunRef("forecast", "run-1")))
		}},
		"WaitingRun": {reason: "is waiting", setup: func(t *testing.T, f *deleteFixture) {
			writeRunWithStatus(t, f.stores(t).DAGRunRepository, ir.Waiting)
		}},
		"LibraryRun": {reason: "is running", setup: func(t *testing.T, f *deleteFixture) {
			runs, _ := f.stores(t).LibraryRuns()
			writeRunWithStatus(t, runs, ir.Running)
		}},
		"RepositoryTracksDAG": {reason: "tracks files", setup: func(t *testing.T, f *deleteFixture) {
			gitTrack(t, f.cfg.Paths.ProjectsDir, "weather/forecast.yaml")
		}},
		"RepositoryInside": {reason: "vendor", setup: func(t *testing.T, f *deleteFixture) {
			writeFile(t, filepath.Join(f.cfg.Paths.ProjectsDir, "weather", "vendor", "lib.yaml"))
			gitTrack(t, filepath.Join(f.cfg.Paths.ProjectsDir, "weather", "vendor"), "lib.yaml")
		}},
		// A worktree's .git is a file, which names no index to read.
		"WorktreeAround": {reason: "tracks files", setup: func(t *testing.T, f *deleteFixture) {
			require.NoError(t, os.WriteFile(filepath.Join(f.cfg.Paths.ProjectsDir, ".git"), []byte("gitdir: elsewhere\n"), 0o600))
		}},
		// projects_dir links into a checkout that tracks the DAG.
		"RepositoryThroughLink": {reason: "tracks files", setup: func(t *testing.T, f *deleteFixture) {
			checkout := filepath.Join(t.TempDir(), "checkout")
			require.NoError(t, os.MkdirAll(checkout, 0o750))
			require.NoError(t, os.Rename(f.cfg.Paths.ProjectsDir, filepath.Join(checkout, "projects")))
			require.NoError(t, os.Symlink(filepath.Join(checkout, "projects"), f.cfg.Paths.ProjectsDir))
			gitTrack(t, checkout, "projects/weather/forecast.yaml")
		}},
		"CorruptIndex": {reason: "tracks files", setup: func(t *testing.T, f *deleteFixture) {
			gitTrack(t, f.cfg.Paths.ProjectsDir)
			require.NoError(t, os.WriteFile(filepath.Join(f.cfg.Paths.ProjectsDir, ".git", "index"), []byte("not an index"), 0o600))
		}},
		"ProtectedPath": {reason: "paths.calendars_dir", setup: func(_ *testing.T, f *deleteFixture) {
			f.cfg.Paths.CalendarsDir = filepath.Join(f.cfg.Paths.ProjectsDir, "weather", "calendars")
		}},
	}
	for name, refusal := range refusals {
		t.Run(name, func(t *testing.T) {
			f := newDeleteFixture(t)
			refusal.setup(t, f)
			_, err := f.delete("weather", persisfile.DeleteProjectOptions{})
			require.ErrorIs(t, err, project.ErrDeleteRefused)
			require.ErrorContains(t, err, refusal.reason)
			f.requireKept(t)
		})
	}

	// A refused delete of a missing project leaves it missing.
	t.Run("MissingProject", func(t *testing.T) {
		f := newDeleteFixture(t)
		writeRunWithStatus(t, f.stores(t).DAGRunRepository, ir.Waiting)
		dagsDir := filepath.Join(f.cfg.Paths.ProjectsDir, "weather")
		require.NoError(t, os.RemoveAll(dagsDir))
		_, err := f.delete("weather", persisfile.DeleteProjectOptions{})
		require.ErrorIs(t, err, project.ErrDeleteRefused)
		assert.NoDirExists(t, dagsDir)
		p, err := f.registry.Get(ctx, "weather")
		require.NoError(t, err)
		state, err := f.server.ProjectState(ctx, p)
		require.NoError(t, err)
		assert.Equal(t, persisfile.ProjectStateMissing, state)
	})

	t.Run("InPlaceWikiPages", func(t *testing.T) {
		f := newDeleteFixture(t)
		migrated, err := persisfile.CreateProject(ctx, f.cfg, f.registry, project.CreateInput{Name: "ops", MigratedFrom: "ops"})
		require.NoError(t, err)
		wiki := &fakeWiki{}
		_, err = f.delete("ops", persisfile.DeleteProjectOptions{Wiki: wiki})
		require.ErrorIs(t, err, project.ErrDeleteRefused)
		require.ErrorContains(t, err, "holds pages")
		kept, err := f.registry.Get(ctx, "ops")
		require.NoError(t, err)
		assert.Equal(t, migrated.ID, kept.ID)
		assert.Empty(t, wiki.settled)

		_, err = f.delete("weather", persisfile.DeleteProjectOptions{Wiki: wiki})
		require.NoError(t, err, "only a migrated project reads a wiki folder in place")
	})
}

// gitTrack makes dir a git checkout that tracks the files at paths,
// slash-separated and relative to dir.
func gitTrack(t *testing.T, dir string, paths ...string) {
	t.Helper()
	repo, err := git.PlainInit(dir, false)
	require.NoError(t, err)
	worktree, err := repo.Worktree()
	require.NoError(t, err)
	for _, path := range paths {
		_, err := worktree.Add(path)
		require.NoError(t, err)
	}
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte("steps:\n  - run: echo ok\n"), 0o600))
}

// TestDeleteProjectAllowsUntrackingRepository proves that a git checkout
// that tracks nothing in the project's DAG directory, such as a dotfiles
// repository in the home directory, does not refuse the delete.
func TestDeleteProjectAllowsUntrackingRepository(t *testing.T) {
	f := newDeleteFixture(t)
	home := filepath.Dir(f.cfg.Paths.ProjectsDir)
	writeFile(t, filepath.Join(home, ".bashrc"))
	gitTrack(t, home, ".bashrc")

	_, err := f.delete("weather", persisfile.DeleteProjectOptions{})
	require.NoError(t, err)
	assert.NoDirExists(t, filepath.Join(f.cfg.Paths.ProjectsDir, "weather"))
	assert.FileExists(t, filepath.Join(home, ".bashrc"))
}

func writeRunWithStatus(t *testing.T, runs *persis.DAGRunRepository, status ir.Status) {
	t.Helper()
	ctx := context.Background()
	dag := &ir.DAG{Name: "forecast"}
	attempt, err := runs.CreateAttempt(ctx, dag, time.Now(), "run-1", persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, ir.DAGRunStatus{Name: dag.Name, DAGRunID: "run-1", Status: status}))
	require.NoError(t, attempt.Close(ctx))
}

// TestDeleteProjectWaitsForRelease proves that a delete removes nothing
// until every live process has recorded the release of the project, and
// that one that is not released in time is refused.
func TestDeleteProjectWaitsForRelease(t *testing.T) {
	ctx := context.Background()
	persisfile.SetReleaseWait(t, 10*time.Second, 10*time.Millisecond)
	f := newDeleteFixture(t)
	scheduler := fileserviceregistry.New(f.cfg.Paths.ServiceRegistryDir)
	require.NoError(t, scheduler.Register(ctx, serviceregistry.ServiceNameScheduler, serviceregistry.HostInfo{ID: "scheduler-1"}))
	t.Cleanup(func() { scheduler.Unregister(ctx) })

	done := make(chan error, 1)
	go func() {
		_, err := f.delete("weather", persisfile.DeleteProjectOptions{})
		done <- err
	}()
	marked := waitMarked(t, f)
	select {
	case err := <-done:
		t.Fatalf("the delete ended before the scheduler released the project: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	assert.DirExists(t, filepath.Join(f.cfg.Paths.ProjectsDir, "weather"))

	require.NoError(t, scheduler.SetReleasedProjects(ctx, []string{marked.DeleteMark}))
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the delete did not end once the scheduler released the project")
	}
	assert.NoDirExists(t, filepath.Join(f.cfg.Paths.ProjectsDir, "weather"))

	t.Run("NotReleased", func(t *testing.T) {
		persisfile.SetReleaseWait(t, 100*time.Millisecond, 10*time.Millisecond)
		f := newDeleteFixture(t)
		holder := fileserviceregistry.New(f.cfg.Paths.ServiceRegistryDir)
		require.NoError(t, holder.Register(ctx, serviceregistry.ServiceNameServer, serviceregistry.HostInfo{ID: "server-1"}))
		t.Cleanup(func() { holder.Unregister(ctx) })

		_, err := f.delete("weather", persisfile.DeleteProjectOptions{})
		require.ErrorIs(t, err, project.ErrDeleteRefused)
		require.ErrorContains(t, err, "server server-1")
		f.requireKept(t)
	})

	// A release recorded for an earlier mark does not count for a new one.
	t.Run("ReleaseOfEarlierMark", func(t *testing.T) {
		persisfile.SetReleaseWait(t, 300*time.Millisecond, 10*time.Millisecond)
		f := newDeleteFixture(t)
		earlier, err := f.registry.SetDeleting(ctx, f.weather.ID, true)
		require.NoError(t, err)
		_, err = f.registry.SetDeleting(ctx, f.weather.ID, false)
		require.NoError(t, err)
		holder := fileserviceregistry.New(f.cfg.Paths.ServiceRegistryDir)
		require.NoError(t, holder.Register(ctx, serviceregistry.ServiceNameScheduler, serviceregistry.HostInfo{ID: "scheduler-1"}))
		t.Cleanup(func() { holder.Unregister(ctx) })
		require.NoError(t, holder.SetReleasedProjects(ctx, []string{earlier.DeleteMark}))

		_, err = f.delete("weather", persisfile.DeleteProjectOptions{})
		require.ErrorIs(t, err, project.ErrDeleteRefused)
		require.ErrorContains(t, err, "scheduler scheduler-1")
		f.requireKept(t)
	})

	// What changed on disk during the wait refuses the delete.
	t.Run("CheckedAgainAfterRelease", func(t *testing.T) {
		f := newDeleteFixture(t)
		holder := fileserviceregistry.New(f.cfg.Paths.ServiceRegistryDir)
		require.NoError(t, holder.Register(ctx, serviceregistry.ServiceNameScheduler, serviceregistry.HostInfo{ID: "scheduler-1"}))
		t.Cleanup(func() { holder.Unregister(ctx) })
		done := make(chan error, 1)
		go func() {
			_, err := f.delete("weather", persisfile.DeleteProjectOptions{})
			done <- err
		}()
		marked := waitMarked(t, f)
		gitTrack(t, f.cfg.Paths.ProjectsDir, "weather/forecast.yaml")
		require.NoError(t, holder.SetReleasedProjects(ctx, []string{marked.DeleteMark}))
		require.ErrorIs(t, <-done, project.ErrDeleteRefused)
		f.requireKept(t)
	})

	// One create or delete runs at a time across processes.
	t.Run("Busy", func(t *testing.T) {
		persisfile.SetLifecycleLockWait(t, 100*time.Millisecond)
		f := newDeleteFixture(t)
		holder := fileserviceregistry.New(f.cfg.Paths.ServiceRegistryDir)
		require.NoError(t, holder.Register(ctx, serviceregistry.ServiceNameScheduler, serviceregistry.HostInfo{ID: "scheduler-1"}))
		t.Cleanup(func() { holder.Unregister(ctx) })
		done := make(chan error, 1)
		go func() {
			_, err := f.delete("weather", persisfile.DeleteProjectOptions{})
			done <- err
		}()
		marked := waitMarked(t, f)
		_, err := persisfile.CreateProject(ctx, f.cfg, f.registry, project.CreateInput{Name: "intraday"})
		require.ErrorIs(t, err, project.ErrLifecycleBusy)
		_, err = f.delete("weather", persisfile.DeleteProjectOptions{})
		require.ErrorIs(t, err, project.ErrLifecycleBusy)
		require.NoError(t, holder.SetReleasedProjects(ctx, []string{marked.DeleteMark}))
		require.NoError(t, <-done)
	})
}

// waitMarked waits until weather is marked deleting and returns it.
func waitMarked(t *testing.T, f *deleteFixture) project.Project {
	t.Helper()
	var marked project.Project
	require.Eventually(t, func() bool {
		p, err := f.registry.Get(context.Background(), "weather")
		marked = p
		return err == nil && p.Deleting
	}, 5*time.Second, 10*time.Millisecond)
	return marked
}
