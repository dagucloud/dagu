// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	filedag "github.com/dagucloud/dagu/v2/internal/persis/file/dag"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
)

// The scheduler runs a unit for each registered project other than the
// default one, from the project's own stores, without a restart: while the
// project is not marked deleting and its DAG directory exists. A unit runs
// the slots of the minute it starts in, as the scheduler's first tick runs
// the default project's, and each later tick of the scheduler ticks it.
func TestProjectUnitsFollowRegistry(t *testing.T) {
	t.Parallel()
	f := newUnitsFixture(t, 0)
	weather, sim := f.create(t, "weather"), f.create(t, "sim")
	require.NoError(t, os.Remove(filepath.Join(f.cfg.Paths.ProjectsDir, sim.Name)))
	f.writeDAG(t, weather, "forecast")

	f.registry.show(f.defaults, weather, sim)
	f.requireRunning(t, weather.ID)
	assert.False(t, f.units.isRunning(sim.ID), "a project whose DAG directory is missing")
	assert.Equal(t, "weather/forecast", f.nextRun(t), "the slot of the minute the unit starts in")

	f.process.runTickSafely(context.Background(), time.Now().Truncate(time.Minute).Add(time.Minute))
	assert.Equal(t, "weather/forecast", f.nextRun(t), "the slot of the scheduler's next tick")

	f.writeDAG(t, sim, "auction")
	f.requireRunning(t, sim.ID)

	deleting := weather
	deleting.Deleting = true
	f.registry.show(f.defaults, deleting, sim)
	assert.False(t, f.units.isRunning(weather.ID), "a project marked deleting stops before the change is followed")

	require.NoError(t, os.RemoveAll(filepath.Join(f.cfg.Paths.ProjectsDir, sim.Name)))
	require.Eventually(t, func() bool { return !f.units.isRunning(sim.ID) },
		5*time.Second, 10*time.Millisecond, "a project whose DAG directory is gone")
}

// After a takeover, a unit replays the slots its project missed within the
// scheduler's recovery window, as the default project does (Spec 085).
func TestProjectUnitsReplayAfterTakeover(t *testing.T) {
	t.Parallel()
	f := newUnitsFixture(t, 10*time.Minute)
	weather := f.create(t, "weather")
	f.writeDAG(t, weather, "forecast")
	f.saveLastTick(t, weather, "forecast", time.Now().Truncate(time.Minute).Add(-5*time.Minute))

	f.registry.show(f.defaults, weather)
	f.requireRunning(t, weather.ID)
	for {
		if run := f.nextRun(t); run == "weather/forecast catch-up" {
			return
		}
	}
}

// A unit that starts after the scheduler became active replays no slot,
// even after a takeover: only the units that start with the scheduler
// replay what the failover missed.
func TestProjectUnitsStartedLaterReplayNothing(t *testing.T) {
	t.Parallel()
	f := newUnitsFixture(t, 10*time.Minute)
	sim := f.create(t, "sim")
	f.registry.show(f.defaults, sim)
	f.requireRunning(t, sim.ID)

	weather := f.create(t, "weather")
	f.writeDAG(t, weather, "forecast")
	f.saveLastTick(t, weather, "forecast", time.Now().Truncate(time.Minute).Add(-5*time.Minute))
	f.registry.show(f.defaults, sim, weather)
	f.requireRunning(t, weather.ID)
	assert.Equal(t, "weather/forecast", f.nextRun(t), "the slot of the minute the unit starts in")
	f.process.runTickSafely(context.Background(), time.Now().Truncate(time.Minute).Add(time.Minute))
	assert.Equal(t, "weather/forecast", f.nextRun(t), "the slot of the scheduler's next tick")
	assert.Empty(t, f.queuedRuns(), "a catch-up run of a unit started after the takeover")
}

// A project the registry marks deleting is recorded as released only once
// its unit has stopped, also while the unit is still starting or already
// stopping when the change comes.
func TestProjectUnitsReleaseAfterUnitStops(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		// hold makes the unit of p hold in that step until released fires.
		hold func(f *unitsFixture, p project.Project, released <-chan struct{})
		// begin brings the unit of p into that step.
		begin func(t *testing.T, f *unitsFixture, p project.Project)
	}{
		{
			name: "starting",
			hold: func(f *unitsFixture, p project.Project, released <-chan struct{}) {
				f.onOpen = holdFor(p, released)
			},
			begin: func(_ *testing.T, f *unitsFixture, p project.Project) {
				f.registry.show(f.defaults, p)
			},
		},
		{
			name: "stopping",
			hold: func(f *unitsFixture, p project.Project, released <-chan struct{}) {
				f.onStop = holdFor(p, released)
			},
			begin: func(t *testing.T, f *unitsFixture, p project.Project) {
				f.registry.show(f.defaults, p)
				f.requireRunning(t, p.ID)
				require.NoError(t, os.RemoveAll(filepath.Join(f.cfg.Paths.ProjectsDir, p.Name)))
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newUnitsFixture(t, 0)
			weather := f.create(t, "weather")
			released := make(chan struct{})
			var once sync.Once
			var early atomic.Bool
			f.onRelease = func(ids []string) {
				if slices.Contains(ids, weather.ID) {
					early.Store(!f.units.idle(weather.ID))
					once.Do(func() { close(released) })
				}
			}
			tt.hold(f, weather, released)
			tt.begin(t, f, weather)
			require.Eventually(t, func() bool { return f.units.isBusy(weather.ID) },
				5*time.Second, 10*time.Millisecond)

			deleting := weather
			deleting.Deleting = true
			f.registry.show(f.defaults, deleting)
			select {
			case <-released:
			case <-time.After(5 * time.Second):
				require.FailNow(t, "the release was not recorded")
			}
			assert.False(t, early.Load(), "the release was recorded while the unit was %s", tt.name)
			assert.False(t, f.units.isRunning(weather.ID))
		})
	}
}

// holdFor returns a hook that holds a step of the unit of p until released
// fires, or a second passes: long enough for an early release to show.
func holdFor(p project.Project, released <-chan struct{}) func(project.Project) {
	return func(q project.Project) {
		if q.ID != p.ID {
			return
		}
		select {
		case <-released:
		case <-time.After(time.Second):
		}
	}
}

// idle reports whether no unit of the project is running, starting, or
// stopping.
func (u *projectUnits) idle(id string) bool {
	return !u.isBusy(id) && !u.isRunning(id)
}

func (u *projectUnits) isBusy(id string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	_, ok := u.busy[id]
	return ok
}

// unitsFixture runs the units of an install whose registry a test changes,
// in a scheduler process with the given recovery window. Each unit reports
// a run it starts as "<project>/<DAG>", and a run it queues to catch up as
// "<project>/<DAG> catch-up". A test sets the hooks before it shows the
// registry anything.
type unitsFixture struct {
	cfg      *config.Config
	server   *persisfile.ServerStores
	defaults project.Project
	registry *unitsRegistry
	process  *Scheduler
	units    *projectUnits
	runs     chan string

	// onOpen runs as a unit is built, onStop as it stops, and onRelease
	// as the cache records released projects.
	onOpen    func(project.Project)
	onStop    func(project.Project)
	onRelease func([]string)
}

func newUnitsFixture(t *testing.T, recovery time.Duration) *unitsFixture {
	t.Helper()
	root := t.TempDir()
	data := filepath.Join(root, "data")
	cfg := &config.Config{ProjectsEnabled: true}
	cfg.Core.SkipExamples = true
	cfg.Core.Location = time.UTC
	cfg.Queues.Enabled = true
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
	planner, _ := newTestTickPlanner(&mockStateStore{state: newMockState(time.Now().Truncate(time.Minute))})
	planner.cfg.RecoveryWindow = func() time.Duration { return recovery }
	f := &unitsFixture{
		cfg: cfg, server: server, defaults: defaults,
		registry: &unitsRegistry{defaults: defaults, contents: make(chan []project.Project), followed: make(chan struct{})},
		process:  &Scheduler{planner: planner, clock: time.Now, dagExecutor: &DAGExecutor{}},
		runs:     make(chan string, 100),
	}
	cache := persisfile.NewProjectStoreCache(f.registry, nil,
		func(ctx context.Context, p project.Project) (*persisfile.ProjectStores, error) {
			return persisfile.NewProjectStores(ctx, cfg, server, p, persisfile.ProjectStoreOptions{})
		}, fixtureReleases{f})
	f.units = newProjectUnits(f.process, cache, f.openUnit)
	f.units.recheck = 10 * time.Millisecond
	f.process.units = f.units

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
	if f.onOpen != nil {
		f.onOpen(p)
	}
	baseConfig, workspaceDir := stores.BaseConfig()
	unit, err := f.process.newUnit(f.cfg, p, stores, Dependencies{
		EntryReader: filedag.NewFileEntryReader(stores.Paths.DAGsDir, stores.DAGRepository,
			false, baseConfig, workspaceDir),
		DAGRunManager:       runtime.NewManager(stores.DAGRunRepository, stores.ProcRepository, f.cfg),
		DAGRepository:       stores.DAGRepository,
		DAGRunRepository:    stores.DAGRunRepository,
		QueueStore:          stores.QueueStore,
		ProcRepository:      stores.ProcRepository,
		SchedulerStateStore: stores.SchedulerStateStore,
		CoordinatorClient: stopHook{onStop: func() {
			if f.onStop != nil {
				f.onStop(p)
			}
		}},
	})
	if err != nil {
		return nil, err
	}
	unit.SetDispatchFunc(func(_ context.Context, entry DAGEntry, _ string, _ ir.TriggerType, _ time.Time, _ string) error {
		f.runs <- p.Name + "/" + entry.DAG.Name
		return nil
	})
	unit.planner.cfg.Enqueue = func(_ context.Context, entry DAGEntry, _ string, _ ir.TriggerType, _ time.Time, _ string) error {
		f.runs <- p.Name + "/" + entry.DAG.Name + " catch-up"
		return nil
	}
	return unit, nil
}

// nextRun returns the next run a unit started or queued.
func (f *unitsFixture) nextRun(t *testing.T) string {
	t.Helper()
	select {
	case run := <-f.runs:
		return run
	case <-time.After(5 * time.Second):
		require.FailNow(t, "no unit started or queued a run")
		return ""
	}
}

// queuedRuns returns the runs started or queued since the last read.
func (f *unitsFixture) queuedRuns() []string {
	var runs []string
	for {
		select {
		case run := <-f.runs:
			runs = append(runs, run)
		default:
			return runs
		}
	}
}

// saveLastTick saves the state of a scheduler of p that last ticked, and
// last scheduled the DAG name, at lastTick.
func (f *unitsFixture) saveLastTick(t *testing.T, p project.Project, name string, lastTick time.Time) {
	t.Helper()
	stores, err := persisfile.NewProjectStores(context.Background(), f.cfg, f.server, p, persisfile.ProjectStoreOptions{})
	require.NoError(t, err)
	require.NoError(t, stores.SchedulerStateStore.Save(context.Background(), &schedulerstate.State{
		LastTick: lastTick,
		DAGs: map[string]schedulerstate.DAGWatermark{
			name: {LastScheduledTime: lastTick, FirstSeen: lastTick.Add(-time.Hour)},
		},
	}))
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

// unitsRegistry hands the store cache each registry content a test shows,
// and waits until the cache has followed it.
type unitsRegistry struct {
	project.Registry
	defaults project.Project
	contents chan []project.Project
	followed chan struct{}
}

func (r *unitsRegistry) show(projects ...project.Project) {
	r.contents <- projects
	<-r.followed
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
			r.followed <- struct{}{}
		}
	}
}

// fixtureReleases hands each record of released projects to onRelease.
type fixtureReleases struct{ f *unitsFixture }

func (r fixtureReleases) SetReleasedProjects(_ context.Context, ids []string) error {
	if r.f.onRelease != nil {
		r.f.onRelease(ids)
	}
	return nil
}

// stopHook is the coordinator client of a unit, which the unit cleans up as
// it stops. It dispatches nothing.
type stopHook struct {
	dispatch.Dispatcher
	onStop func()
}

func (h stopHook) Cleanup(context.Context) error {
	h.onStop()
	return nil
}
