// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/dagdiscovery"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler/filenotify"

	"github.com/fsnotify/fsnotify"
)

// EntryReader is responsible for managing DAG definitions and watching for changes.
type EntryReader interface {
	// Init initializes the DAG registry by loading all DAGs from the target directory.
	// This must be called before Start.
	Init(ctx context.Context) error
	// Start starts watching the DAG directory for changes.
	// This method blocks until Stop is called or context is canceled.
	Start(ctx context.Context)
	// Stop stops watching the DAG directory.
	Stop()
	// DAGs returns a snapshot of all currently loaded DAG definitions.
	DAGs() []*ir.DAG
	// DAGStore returns the backing store used for loading DAG details and suspension state.
	DAGStore() exec.DAGStore
}

var _ EntryReader = (*entryReaderImpl)(nil)

type dagFileStamp struct {
	size    int64
	modTime int64
}

type registryState struct {
	dags   map[string]*ir.DAG
	stamps map[string]dagFileStamp
	issues []string
}

// entryReaderImpl manages DAGs on local filesystem.
type entryReaderImpl struct {
	targetDir   string
	registry    map[string]*ir.DAG
	stamps      map[string]dagFileStamp
	watchedDirs map[string]struct{}
	lock        sync.Mutex
	dagStore    exec.DAGStore
	dagSource   *dagFileSource
	watcher     filenotify.FileWatcher
	recursive   bool
	quit        chan struct{}
	closeOnce   sync.Once
	events      chan DAGChangeEvent
}

// NewEntryReader creates a new DAG manager with the given configuration.
func NewEntryReader(dir string, dagCli exec.DAGStore, recursive bool) EntryReader {
	return &entryReaderImpl{
		targetDir:   dir,
		registry:    make(map[string]*ir.DAG),
		stamps:      make(map[string]dagFileStamp),
		watchedDirs: make(map[string]struct{}),
		dagStore:    dagCli,
		dagSource:   newDAGFileSource(dir),
		recursive:   recursive,
		quit:        make(chan struct{}),
	}
}

// setEvents wires the event channel used to notify the TickPlanner of DAG
// changes. Must be called before Start().
func (er *entryReaderImpl) setEvents(ch chan DAGChangeEvent) {
	er.events = ch
}

// Init loads the initial DAG registry and starts watching the target directory.
func (er *entryReaderImpl) Init(ctx context.Context) error {
	if er.recursive {
		return er.initRecursive(ctx)
	}

	er.lock.Lock()
	defer er.lock.Unlock()

	if err := er.initialize(ctx); err != nil {
		logger.Error(ctx, "Failed to initialize DAG registry", tag.Error(err))
		return fmt.Errorf("failed to initialize DAGs: %w", err)
	}

	// Create and configure the file watcher
	er.watcher = filenotify.New(time.Minute)
	if err := er.watcher.Add(er.targetDir); err != nil {
		_ = er.watcher.Close()
		return fmt.Errorf("failed to watch DAG directory %s: %w", er.targetDir, err)
	}

	return nil
}

// Start forwards watcher events into registry updates until the reader stops.
func (er *entryReaderImpl) Start(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error(ctx, "Entry reader watcher panicked", tag.Error(panicToError(r)))
		}
	}()
	if er.recursive {
		er.startRecursive(ctx)
		return
	}

	for {
		select {
		case <-er.quit:
			return

		case <-ctx.Done():
			return

		case event, ok := <-er.watcher.Events():
			if !ok {
				return
			}

			if !fileutil.IsYAMLFile(event.Name) {
				continue
			}

			er.handleFSEvent(ctx, event)

		case err, ok := <-er.watcher.Errors():
			if !ok {
				return
			}
			logger.Error(ctx, "Watcher error", tag.Error(err))
		}
	}
}

const recursiveRefreshDelay = 75 * time.Millisecond

func (er *entryReaderImpl) startRecursive(ctx context.Context) {
	var refreshTimer *time.Timer
	var refresh <-chan time.Time
	scheduleRefresh := func() {
		if refreshTimer == nil {
			refreshTimer = time.NewTimer(recursiveRefreshDelay)
			refresh = refreshTimer.C
			return
		}
		if !refreshTimer.Stop() {
			select {
			case <-refreshTimer.C:
			default:
			}
		}
		refreshTimer.Reset(recursiveRefreshDelay)
		refresh = refreshTimer.C
	}
	defer func() {
		if refreshTimer != nil {
			refreshTimer.Stop()
		}
	}()

	for {
		select {
		case <-er.quit:
			return
		case <-ctx.Done():
			return
		case event, ok := <-er.watcher.Events():
			if !ok {
				return
			}
			if needsRecursiveRefresh(event) {
				scheduleRefresh()
			}
		case <-refresh:
			refresh = nil
			if err := er.refreshRecursive(ctx); err != nil {
				logger.Error(ctx, "Failed to refresh recursive DAG registry", tag.Error(err))
			}
		case err, ok := <-er.watcher.Errors():
			if !ok {
				return
			}
			logger.Error(ctx, "Watcher error", tag.Error(err))
		}
	}
}

func needsRecursiveRefresh(event fsnotify.Event) bool {
	if fileutil.IsYAMLFile(event.Name) {
		return true
	}
	return event.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0
}

// handleFSEvent processes a filesystem event and emits a DAGChangeEvent.
func (er *entryReaderImpl) handleFSEvent(ctx context.Context, event fsnotify.Event) {
	fileName := filepath.Base(event.Name)

	if event.Op&(fsnotify.Create|fsnotify.Write) != 0 {
		er.reloadDAGFile(ctx, fileName, event.Name)
		return
	}

	if event.Op&(fsnotify.Rename|fsnotify.Remove) != 0 {
		snapshot, err := er.dagSource.snapshot(ctx, fileName)
		if err != nil {
			logger.Error(ctx, "DAG load failed",
				tag.Error(err),
				tag.File(event.Name))
			return
		}
		if snapshot.exists {
			er.applyDAGFileSnapshot(ctx, fileName, snapshot.dag)
			logger.Info(ctx, "DAG added/updated", tag.Name(fileName))
			return
		}

		er.removeDAGFile(ctx, fileName)
	}
}

// reloadDAGFile reloads a create/write event when the file still snapshots as present.
func (er *entryReaderImpl) reloadDAGFile(ctx context.Context, fileName, eventName string) {
	snapshot, err := er.dagSource.snapshot(ctx, fileName)
	if err != nil {
		logger.Error(ctx, "DAG load failed",
			tag.Error(err),
			tag.File(eventName))
		return
	}
	if !snapshot.exists {
		return
	}

	er.applyDAGFileSnapshot(ctx, fileName, snapshot.dag)
	logger.Info(ctx, "DAG added/updated", tag.Name(fileName))
}

// applyDAGFileSnapshot stores a loaded DAG and emits the matching add/update events.
func (er *entryReaderImpl) applyDAGFileSnapshot(ctx context.Context, fileName string, dag *ir.DAG) {
	// Determine add vs update by checking registry before updating
	er.lock.Lock()
	oldDAG, existed := er.registry[fileName]
	var oldDAGName string
	if existed && oldDAG.Name != dag.Name {
		oldDAGName = oldDAG.Name
	}
	er.registry[fileName] = dag
	er.lock.Unlock()

	// If the DAG name changed, emit delete for the old name first
	if oldDAGName != "" {
		er.sendEvent(ctx, DAGChangeEvent{
			Type:    DAGChangeDeleted,
			DAGName: oldDAGName,
		})
	}

	changeType := DAGChangeAdded
	if existed && oldDAGName == "" {
		changeType = DAGChangeUpdated
	}
	er.sendEvent(ctx, DAGChangeEvent{
		Type:    changeType,
		DAG:     dag,
		DAGName: dag.Name,
	})
}

// removeDAGFile drops a confirmed-absent DAG file from the registry.
func (er *entryReaderImpl) removeDAGFile(ctx context.Context, fileName string) {
	// Capture DAG name from registry before deleting
	er.lock.Lock()
	dag, existed := er.registry[fileName]
	delete(er.registry, fileName)
	er.lock.Unlock()

	if existed && dag != nil {
		er.sendEvent(ctx, DAGChangeEvent{
			Type:    DAGChangeDeleted,
			DAGName: dag.Name,
		})
	}
	logger.Info(ctx, "DAG removed", tag.Name(fileName))
}

// sendEvent sends a DAGChangeEvent on the channel.
// Returns immediately if the entry reader is shutting down or the context is cancelled.
func (er *entryReaderImpl) sendEvent(ctx context.Context, event DAGChangeEvent) {
	if er.events == nil {
		return
	}
	select {
	case er.events <- event:
	case <-er.quit:
	case <-ctx.Done():
	}
}

// Stop closes the watcher and prevents future event sends.
func (er *entryReaderImpl) Stop() {
	er.lock.Lock()
	defer er.lock.Unlock()

	er.closeOnce.Do(func() {
		close(er.quit)
		if er.watcher != nil {
			_ = er.watcher.Close()
		}
	})
}

// DAGs returns the currently loaded DAG metadata.
func (er *entryReaderImpl) DAGs() []*ir.DAG {
	er.lock.Lock()
	defer er.lock.Unlock()

	dags := make([]*ir.DAG, 0, len(er.registry))
	for _, dag := range er.registry {
		dags = append(dags, dag)
	}
	return dags
}

// DAGStore returns the backing DAG store for full DAG details.
func (er *entryReaderImpl) DAGStore() exec.DAGStore {
	return er.dagStore
}

func (er *entryReaderImpl) initRecursive(ctx context.Context) error {
	er.watcher = filenotify.New(time.Minute)

	scan, err := dagdiscovery.Scan(er.targetDir, true)
	if err != nil {
		_ = er.watcher.Close()
		return fmt.Errorf("failed to initialize recursive DAGs: %w", err)
	}
	for _, dir := range scan.Dirs {
		if err := er.watcher.Add(dir); err != nil {
			_ = er.watcher.Close()
			return fmt.Errorf(
				"failed to initialize recursive DAGs: failed to watch DAG directory %s: %w",
				dir,
				err,
			)
		}
		er.watchedDirs[dir] = struct{}{}
	}

	state, err := er.loadRegistry(ctx)
	if err != nil {
		_ = er.watcher.Close()
		return fmt.Errorf("failed to initialize recursive DAGs: %w", err)
	}
	for _, issue := range state.issues {
		logger.Error(ctx, "DAG excluded from scheduler", tag.Error(errors.New(issue)))
	}

	er.lock.Lock()
	er.registry = state.dags
	er.stamps = state.stamps
	er.lock.Unlock()
	return nil
}

func (er *entryReaderImpl) refreshRecursive(ctx context.Context) error {
	scan, err := dagdiscovery.Scan(er.targetDir, true)
	if err != nil {
		return err
	}
	er.syncWatches(ctx, scan.Dirs)

	state, err := er.loadRegistry(ctx)
	if err != nil {
		return err
	}
	for _, issue := range state.issues {
		logger.Error(ctx, "DAG excluded from scheduler", tag.Error(errors.New(issue)))
	}

	events := er.replaceRegistry(state)
	for _, event := range events {
		er.sendEvent(ctx, event)
	}
	return nil
}

func (er *entryReaderImpl) syncWatches(ctx context.Context, dirs []string) {
	next := make(map[string]struct{}, len(dirs))
	for _, dir := range dirs {
		next[dir] = struct{}{}
		if _, exists := er.watchedDirs[dir]; exists {
			continue
		}
		if err := er.watcher.Add(dir); err != nil {
			logger.Error(ctx, "Failed to watch DAG directory", tag.Dir(dir), tag.Error(err))
			continue
		}
		er.watchedDirs[dir] = struct{}{}
	}

	for dir := range er.watchedDirs {
		if _, exists := next[dir]; exists {
			continue
		}
		_ = er.watcher.Remove(dir)
		delete(er.watchedDirs, dir)
	}
}

func (er *entryReaderImpl) loadRegistry(ctx context.Context) (registryState, error) {
	paginator := pagination.NewPaginator(1, math.MaxInt)
	result, issues, err := er.dagStore.List(ctx, exec.ListDAGsOptions{Paginator: &paginator})
	if err != nil {
		return registryState{}, err
	}

	dags := make(map[string]*ir.DAG, len(result.Items))
	stamps := make(map[string]dagFileStamp, len(result.Items))
	for _, listedDAG := range result.Items {
		if len(listedDAG.BuildErrors) > 0 {
			issues = append(issues,
				fmt.Sprintf("reading %s failed: %s", listedDAG.FileName(), errors.Join(listedDAG.BuildErrors...)))
			continue
		}

		relPath, err := filepath.Rel(er.targetDir, listedDAG.Location)
		if err != nil || relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
			issues = append(issues,
				fmt.Sprintf("DAG path is outside the discovery directory: %s", listedDAG.Location))
			continue
		}
		key := filepath.ToSlash(relPath)
		locator := key
		if !strings.Contains(locator, "/") {
			locator = "./" + locator
		}
		dag, err := er.dagStore.GetMetadata(ctx, locator)
		if err != nil {
			issues = append(issues, fmt.Sprintf("reading %s failed: %s", key, err))
			continue
		}
		info, err := os.Stat(dag.Location)
		if err != nil {
			issues = append(issues, fmt.Sprintf("reading %s failed: %s", key, err))
			continue
		}

		dags[key] = dag
		stamps[key] = dagFileStamp{size: info.Size(), modTime: info.ModTime().UnixNano()}
	}
	sort.Strings(issues)
	return registryState{
		dags:   dags,
		stamps: stamps,
		issues: issues,
	}, nil
}

func (er *entryReaderImpl) replaceRegistry(state registryState) []DAGChangeEvent {
	er.lock.Lock()
	defer er.lock.Unlock()

	oldKeys := sortedRegistryKeys(er.registry)
	newKeys := sortedRegistryKeys(state.dags)
	events := make([]DAGChangeEvent, 0)
	for _, key := range oldKeys {
		if _, exists := state.dags[key]; exists {
			continue
		}
		if oldDAG := er.registry[key]; oldDAG != nil {
			events = append(events, DAGChangeEvent{
				Type:    DAGChangeDeleted,
				DAGName: oldDAG.Name,
			})
		}
	}
	for _, key := range newKeys {
		dag := state.dags[key]
		oldDAG, existed := er.registry[key]
		if !existed {
			events = append(events, DAGChangeEvent{
				Type:    DAGChangeAdded,
				DAG:     dag,
				DAGName: dag.Name,
			})
			continue
		}
		if oldDAG.Name != dag.Name {
			events = append(events,
				DAGChangeEvent{Type: DAGChangeDeleted, DAGName: oldDAG.Name},
				DAGChangeEvent{Type: DAGChangeAdded, DAG: dag, DAGName: dag.Name},
			)
			continue
		}
		if er.stamps[key] != state.stamps[key] {
			events = append(events, DAGChangeEvent{
				Type:    DAGChangeUpdated,
				DAG:     dag,
				DAGName: dag.Name,
			})
		}
	}

	er.registry = state.dags
	er.stamps = state.stamps
	return events
}

func sortedRegistryKeys(registry map[string]*ir.DAG) []string {
	keys := make([]string, 0, len(registry))
	for key := range registry {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// initialize loads existing YAML files through the same stable snapshot path as watcher events.
func (er *entryReaderImpl) initialize(ctx context.Context) error {
	// Note: This method expects the caller to already hold er.lock
	logger.Info(ctx, "Loading DAGs", tag.Dir(er.targetDir))
	fis, err := os.ReadDir(er.targetDir)
	if err != nil {
		logger.Error(ctx, "Failed to read DAG directory",
			tag.Dir(er.targetDir),
			tag.Error(err),
		)
		return err
	}

	var dags []string
	for _, fi := range fis {
		if fileutil.IsYAMLFile(fi.Name()) {
			snapshot, err := er.dagSource.snapshot(ctx, fi.Name())
			if err != nil {
				logger.Error(ctx, "DAG load failed",
					tag.Error(err),
					tag.Name(fi.Name()))
				continue
			}
			if !snapshot.exists {
				continue
			}
			er.registry[fi.Name()] = snapshot.dag
			dags = append(dags, fi.Name())
		}
	}

	logger.Debug(ctx, "DAGs loaded", slog.String("dags", strings.Join(dags, ",")))
	return nil
}
