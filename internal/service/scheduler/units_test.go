// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	filedag "github.com/dagucloud/dagu/v2/internal/persis/file/dag"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

// The scheduler runs a unit for each registered project other than the
// default one, from the project's own stores, without a restart: while the
// project is not marked deleting and its DAG directory exists. A tick of
// the scheduler is a tick of each unit.
func TestProjectUnitsFollowRegistry(t *testing.T) {
	t.Parallel()
	f := newUnitsFixture(t)
	weather, sim := f.create(t, "weather"), f.create(t, "sim")
	require.NoError(t, os.Remove(filepath.Join(f.cfg.Paths.ProjectsDir, sim.Name)))
	f.writeDAG(t, weather, "forecast")

	f.registry.show(f.defaults, weather, sim)
	f.requireRunning(t, weather.ID)
	assert.False(t, f.units.isRunning(sim.ID), "a project whose DAG directory is missing")

	tick := time.Now().Truncate(time.Minute)
	planner, _ := newTestTickPlanner(&mockStateStore{state: newMockState(tick)})
	process := &Scheduler{planner: planner, clock: time.Now, units: f.units}
	process.runTickSafely(context.Background(), tick)
	select {
	case run := <-f.dispatched:
		assert.Equal(t, "weather/forecast", run)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the unit of weather did not dispatch its scheduled DAG")
	}

	f.writeDAG(t, sim, "auction")
	f.requireRunning(t, sim.ID)

	deleting := weather
	deleting.Deleting = true
	f.registry.show(f.defaults, deleting, sim)
	require.Eventually(t, func() bool { return !f.units.isRunning(weather.ID) },
		5*time.Second, 10*time.Millisecond, "a project marked deleting")

	require.NoError(t, os.RemoveAll(filepath.Join(f.cfg.Paths.ProjectsDir, sim.Name)))
	require.Eventually(t, func() bool { return !f.units.isRunning(sim.ID) },
		5*time.Second, 10*time.Millisecond, "a project whose DAG directory is gone")
}

// unitsFixture runs the units of an install whose registry a test changes.
// Each unit reports a dispatched run as "<project>/<DAG>".
type unitsFixture struct {
	cfg        *config.Config
	defaults   project.Project
	registry   *unitsRegistry
	units      *projectUnits
	dispatched chan string
}

func newUnitsFixture(t *testing.T) *unitsFixture {
	t.Helper()
	root := t.TempDir()
	data := filepath.Join(root, "data")
	cfg := &config.Config{ProjectsEnabled: true}
	cfg.Core.SkipExamples = true
	cfg.Core.Location = time.UTC
	cfg.Paths = config.PathsConfig{
		DAGsDir:            filepath.Join(root, "dags"),
		ProjectsDir:        filepath.Join(root, "projects"),
		DataDir:            data,
		LogDir:             filepath.Join(root, "logs"),
		ArtifactDir:        filepath.Join(data, "artifacts"),
		ServiceRegistryDir: filepath.Join(data, "service-registry"),
	}
	server := persisfile.NewServerStores(cfg)
	defaults, err := projectregistry.New(data).EnsureDefault(context.Background())
	require.NoError(t, err)
	f := &unitsFixture{
		cfg: cfg, defaults: defaults,
		registry: &unitsRegistry{defaults: defaults, contents: make(chan []project.Project)}, dispatched: make(chan string, 10),
	}
	cache := persisfile.NewProjectStoreCache(f.registry, nil,
		func(ctx context.Context, p project.Project) (*persisfile.ProjectStores, error) {
			return persisfile.NewProjectStores(ctx, cfg, server, p, persisfile.ProjectStoreOptions{})
		}, noReleases{})
	f.units = newProjectUnits(cache, f.openUnit)
	f.units.recheck = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { cache.Run(ctx) })
	f.units.start(ctx, &wg)
	t.Cleanup(func() {
		f.units.stop()
		cancel()
		wg.Wait()
	})
	return f
}

func (f *unitsFixture) openUnit(_ context.Context, p project.Project, stores *persisfile.ProjectStores) (*Scheduler, error) {
	baseConfig, workspaceDir := stores.BaseConfig()
	unit, err := newWithScope(f.cfg, Dependencies{
		EntryReader: filedag.NewFileEntryReader(stores.Paths.DAGsDir, stores.DAGRepository,
			false, baseConfig, workspaceDir),
		DAGRunManager:       runtime.NewManager(stores.DAGRunRepository, stores.ProcRepository, f.cfg),
		DAGRepository:       stores.DAGRepository,
		DAGRunRepository:    stores.DAGRunRepository,
		QueueStore:          stores.QueueStore,
		ProcRepository:      stores.ProcRepository,
		SchedulerStateStore: stores.SchedulerStateStore,
	}, projectScope(p, stores))
	if err != nil {
		return nil, err
	}
	unit.SetDispatchFunc(func(_ context.Context, entry DAGEntry, _ string, _ ir.TriggerType, _ time.Time, _ string) error {
		f.dispatched <- p.Name + "/" + entry.DAG.Name
		return nil
	})
	return unit, nil
}

// create creates a project, its DAG directory included.
func (f *unitsFixture) create(t *testing.T, name string) project.Project {
	t.Helper()
	p, err := persisfile.CreateProject(context.Background(), f.cfg, projectregistry.New(f.cfg.Paths.DataDir),
		project.CreateInput{Name: name})
	require.NoError(t, err)
	return p
}

// writeDAG writes a DAG that runs every minute into the DAG directory of p.
func (f *unitsFixture) writeDAG(t *testing.T, p project.Project, name string) {
	t.Helper()
	dir := filepath.Join(f.cfg.Paths.ProjectsDir, p.Name)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".yaml"),
		[]byte("schedule: \"* * * * *\"\nsteps:\n  - run: echo ok\n"), 0o600))
}

func (f *unitsFixture) requireRunning(t *testing.T, id string) {
	t.Helper()
	require.Eventually(t, func() bool { return f.units.isRunning(id) },
		5*time.Second, 10*time.Millisecond, "the unit of %s did not start", id)
}

// unitsRegistry hands the store cache each registry content a test shows.
type unitsRegistry struct {
	project.Registry
	defaults project.Project
	contents chan []project.Project
}

func (r *unitsRegistry) show(projects ...project.Project) {
	r.contents <- projects
}

func (r *unitsRegistry) EnsureDefault(context.Context) (project.Project, error) {
	return r.defaults, nil
}

func (r *unitsRegistry) Watch(ctx context.Context, onChange func([]project.Project)) {
	for {
		select {
		case <-ctx.Done():
			return
		case projects := <-r.contents:
			onChange(projects)
		}
	}
}

type noReleases struct{}

func (noReleases) SetReleasedProjects(context.Context, []string) error { return nil }
