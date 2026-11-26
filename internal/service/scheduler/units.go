// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/project"
)

// One scheduler process schedules every project. The scheduler holds what
// exists once per process, and schedules the default project itself; each
// other project gets a unit, a scheduler built from that project's stores:
//
//	Scheduler     HA lock, health server, service registration, signals,
//	 |            pause flag, restart backoff, cron loop, wake hold
//	 +- default project's entry reader, planner, queues, scanners, monitors
//	 +- projectUnits    follows the registry and the DAG directories
//	     +- unit: weather   (the same, for one project)
//	     +- unit: sim
//
// A unit runs only while the scheduler holds the lock, and ticks when the
// scheduler ticks.

// missingCheckInterval is how often the DAG directories of the projects
// are checked for a project that went missing or came back.
const missingCheckInterval = time.Minute

// scope is what a scheduler schedules: one project's DAGs, where its runs
// write, and the seed of its slots' run IDs.
type scope struct {
	// project names the project, or is "" for the default project, which
	// child processes address unless told otherwise.
	project                string
	dagsDir                string
	logDir                 string
	artifactDir            string
	baseConfig             string
	workspaceBaseConfigDir string
	slotSeed               string
}

// configScope is the scope of the default project, on the configured paths.
func configScope(cfg *config.Config) scope {
	logDir, artifactDir := runDirs(cfg)
	return scope{
		dagsDir:                cfg.Paths.DAGsDir,
		logDir:                 logDir,
		artifactDir:            artifactDir,
		baseConfig:             cfg.Paths.BaseConfig,
		workspaceBaseConfigDir: cfg.WorkspaceBaseConfigDir(),
		slotSeed:               cfg.Paths.DataDir,
	}
}

// projectScope is the scope of project p, whose stores are given.
func projectScope(p project.Project, stores *persisfile.ProjectStores) scope {
	baseConfig, workspaceDir := stores.BaseConfig()
	return scope{
		project:                p.Name,
		dagsDir:                stores.Paths.DAGsDir,
		logDir:                 stores.Paths.LogDir,
		artifactDir:            stores.Paths.ArtifactDir,
		baseConfig:             baseConfig,
		workspaceBaseConfigDir: workspaceDir,
		slotSeed:               p.SlotSeed,
	}
}

// newUnit builds the unit of project p from deps. The unit leaves the
// lock, health server, and service registration to s, and keeps s's clock
// and OpenCode host.
func (s *Scheduler) newUnit(cfg *config.Config, p project.Project, stores *persisfile.ProjectStores, deps Dependencies) (*Scheduler, error) {
	unit, err := newWithScope(cfg, deps, projectScope(p, stores))
	if err != nil {
		return nil, err
	}
	unit.disableHealthServer = true
	unit.serviceRegistry = nil
	unit.SetClock(s.clock)
	unit.dagExecutor.openCodeHost = s.dagExecutor.openCodeHost
	return unit, nil
}

// startAsUnit migrates the suspension state of the unit's project and
// starts the unit. The unit's loops run on wg until ctx ends or the unit
// stops.
func (s *Scheduler) startAsUnit(ctx context.Context, wg *sync.WaitGroup) error {
	var state startupState
	err := s.dagRepository.MigrateSuspensionState(ctx)
	if err == nil {
		err = s.startUnit(ctx, wg, &state)
	}
	if err != nil {
		s.cleanupFailedStartup(state)
	}
	return err
}

// projectUnits runs a unit for each registered project other than the
// default one, while the project is not marked deleting and its DAG
// directory exists. It opens no store: the cache does.
type projectUnits struct {
	stores *persisfile.ProjectStoreCache
	open   func(context.Context, project.Project, *persisfile.ProjectStores) (*Scheduler, error)
	// recheck is how often the DAG directories are checked.
	recheck time.Duration

	// changed wakes run when the registry changed.
	changed chan struct{}
	// quit ends run; done closes when run has stopped every unit.
	quit     chan struct{}
	done     chan struct{}
	stopOnce sync.Once

	mu sync.Mutex
	// projects is the registry's content as last heard.
	projects []project.Project
	// started reports that run started, so stop waits for it.
	started bool

	// tickMu keeps a unit from stopping during a tick of it, and guards
	// running.
	tickMu  sync.Mutex
	running map[string]*unit
	// missing holds the projects whose DAG directory was last found gone,
	// so each change is logged once. Only run touches it.
	missing map[string]bool
}

// unit is a started unit.
type unit struct {
	scheduler *Scheduler
	stores    *persisfile.ProjectStores
	ctx       context.Context
	cancel    context.CancelFunc
	loops     sync.WaitGroup
}

func newProjectUnits(
	stores *persisfile.ProjectStoreCache,
	open func(context.Context, project.Project, *persisfile.ProjectStores) (*Scheduler, error),
) *projectUnits {
	u := &projectUnits{
		stores:  stores,
		open:    open,
		recheck: missingCheckInterval,
		changed: make(chan struct{}, 1),
		quit:    make(chan struct{}),
		done:    make(chan struct{}),
		running: make(map[string]*unit),
		missing: make(map[string]bool),
	}
	stores.OnChange(u.heard)
	return u
}

// heard records the registry's content and wakes run.
func (u *projectUnits) heard(projects []project.Project) {
	u.mu.Lock()
	u.projects = projects
	u.mu.Unlock()
	select {
	case u.changed <- struct{}{}:
	default:
	}
}

// start runs the units on wg until ctx ends or stop is called. It does
// nothing on a nil u.
func (u *projectUnits) start(ctx context.Context, wg *sync.WaitGroup) {
	if u == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	select {
	case <-u.quit:
		return
	default:
	}
	u.started = true
	wg.Go(func() { u.run(ctx) })
}

// stop stops every unit and waits until they have stopped. It does nothing
// on a nil u.
func (u *projectUnits) stop() {
	if u == nil {
		return
	}
	u.mu.Lock()
	u.stopOnce.Do(func() { close(u.quit) })
	started := u.started
	u.mu.Unlock()
	if started {
		<-u.done
	}
}

// each calls fn with each running unit and the context it runs in. It does
// nothing on a nil u.
func (u *projectUnits) each(fn func(context.Context, *Scheduler)) {
	if u == nil {
		return
	}
	u.tickMu.Lock()
	defer u.tickMu.Unlock()
	for _, r := range u.running {
		fn(r.ctx, r.scheduler)
	}
}

func (u *projectUnits) run(ctx context.Context) {
	defer close(u.done)
	defer u.stopAll()
	ticker := time.NewTicker(u.recheck)
	defer ticker.Stop()
	for {
		u.follow(ctx)
		select {
		case <-ctx.Done():
			return
		case <-u.quit:
			return
		case <-u.changed:
		case <-ticker.C:
		}
	}
}

// follow starts and stops units to match the registry, the cache, and the
// DAG directories.
func (u *projectUnits) follow(ctx context.Context) {
	u.mu.Lock()
	projects := u.projects
	u.mu.Unlock()

	want := make(map[string]project.Project)
	stores := make(map[string]*persisfile.ProjectStores)
	for _, p := range projects {
		if p.Deleting || strings.EqualFold(p.Name, project.DefaultName) {
			continue
		}
		opened, ok := u.stores.Get(p.ID)
		if !ok || u.isMissing(ctx, p, opened) {
			continue
		}
		want[p.ID], stores[p.ID] = p, opened
	}

	for id, r := range u.snapshot() {
		if _, ok := want[id]; !ok || stores[id] != r.stores {
			u.stopUnit(id)
		}
	}
	for id, p := range want {
		if u.isRunning(id) || u.stopping() {
			continue
		}
		u.startUnit(ctx, p, stores[id])
	}
}

// isMissing reports whether the DAG directory of p is gone, and logs when
// that changes.
func (u *projectUnits) isMissing(ctx context.Context, p project.Project, stores *persisfile.ProjectStores) bool {
	_, err := os.Stat(stores.Paths.DAGsDir)
	missing := errors.Is(err, fs.ErrNotExist)
	switch was := u.missing[p.ID]; {
	case missing && !was:
		u.missing[p.ID] = true
		logger.Warn(ctx, "Not scheduling a project whose DAG directory is missing",
			slog.String("project", p.Name), tag.Dir(stores.Paths.DAGsDir))
	case !missing && was:
		delete(u.missing, p.ID)
		logger.Info(ctx, "Scheduling a project whose DAG directory is back",
			slog.String("project", p.Name), tag.Dir(stores.Paths.DAGsDir))
	}
	return missing
}

func (u *projectUnits) startUnit(ctx context.Context, p project.Project, stores *persisfile.ProjectStores) {
	ctx = logger.WithValues(ctx, slog.String("project", p.Name))
	scheduler, err := u.open(ctx, p, stores)
	if err != nil {
		logger.Error(ctx, "Failed to build the scheduler unit of a project", tag.Error(err))
		return
	}
	// The unit's runs record their events in the unit's project.
	if source, ok := eventstore.SourceFromContext(ctx); ok {
		if scheduler.events == nil {
			logger.Error(ctx, "Failed to start the scheduler unit of a project: its event store is unavailable")
			scheduler.closeDAGExecutor(ctx)
			return
		}
		ctx = eventstore.WithContext(ctx, scheduler.events, source)
	}
	r := &unit{scheduler: scheduler, stores: stores}
	r.ctx, r.cancel = context.WithCancel(ctx)
	if err := scheduler.startAsUnit(r.ctx, &r.loops); err != nil {
		r.cancel()
		logger.Error(ctx, "Failed to start the scheduler unit of a project", tag.Error(err))
		return
	}
	u.tickMu.Lock()
	u.running[p.ID] = r
	u.tickMu.Unlock()
	logger.Info(ctx, "Started the scheduler unit of a project", tag.Dir(stores.Paths.DAGsDir))
}

func (u *projectUnits) stopUnit(id string) {
	u.tickMu.Lock()
	r := u.running[id]
	delete(u.running, id)
	u.tickMu.Unlock()
	if r == nil {
		return
	}
	// The unit flushes its state as it stops, so stopping must not be cut
	// short by the context that ended it.
	stopCtx := context.WithoutCancel(r.ctx)
	r.scheduler.Stop(stopCtx)
	r.cancel()
	r.loops.Wait()
	logger.Info(stopCtx, "Stopped the scheduler unit of a project")
}

func (u *projectUnits) stopAll() {
	for id := range u.snapshot() {
		u.stopUnit(id)
	}
}

func (u *projectUnits) snapshot() map[string]*unit {
	u.tickMu.Lock()
	defer u.tickMu.Unlock()
	running := make(map[string]*unit, len(u.running))
	for id, r := range u.running {
		running[id] = r
	}
	return running
}

func (u *projectUnits) isRunning(id string) bool {
	u.tickMu.Lock()
	defer u.tickMu.Unlock()
	_, ok := u.running[id]
	return ok
}

func (u *projectUnits) stopping() bool {
	select {
	case <-u.quit:
		return true
	default:
		return false
	}
}
