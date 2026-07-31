// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/dagdiscovery"
	"github.com/dagucloud/dagu/internal/service/scheduler/filenotify"

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
	DAGs() []*core.DAG
	// DAGStore returns the backing store used for loading DAG details and suspension state.
	DAGStore() exec.DAGStore
}

var _ EntryReader = (*entryReaderImpl)(nil)

// EntryReaderOption configures DAG registry discovery.
type EntryReaderOption func(*entryReaderImpl)

// WithRecursiveDiscovery controls whether the scheduler discovers nested DAG files.
func WithRecursiveDiscovery(recursive bool) EntryReaderOption {
	return func(er *entryReaderImpl) {
		er.recursiveDiscovery = recursive
	}
}

type dagFileStamp struct {
	size    int64
	modTime int64
}

type recursiveDAGSnapshot struct {
	registry        map[string]*core.DAG
	stamps          map[string]dagFileStamp
	discoveryErrors []string
}

// entryReaderImpl manages DAGs on local filesystem.
type entryReaderImpl struct {
	targetDir          string
	registry           map[string]*core.DAG
	stamps             map[string]dagFileStamp
	watchedDirs        map[string]struct{}
	lock               sync.Mutex
	dagStore           exec.DAGStore
	dagSource          *dagFileSource
	watcher            filenotify.FileWatcher
	recursiveDiscovery bool
	quit               chan struct{}
	closeOnce          sync.Once
	events             chan DAGChangeEvent
}

// NewEntryReader creates a new DAG manager with the given configuration.
func NewEntryReader(dir string, dagCli exec.DAGStore, opts ...EntryReaderOption) EntryReader {
	er := &entryReaderImpl{
		targetDir:   dir,
		registry:    make(map[string]*core.DAG),
		stamps:      make(map[string]dagFileStamp),
		watchedDirs: make(map[string]struct{}),
		dagStore:    dagCli,
		dagSource:   newDAGFileSource(dir),
		quit:        make(chan struct{}),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(er)
		}
	}
	return er
}

// setEvents wires the event channel used to notify the TickPlanner of DAG
// changes. Must be called before Start().
func (er *entryReaderImpl) setEvents(ch chan DAGChangeEvent) {
	er.events = ch
}

// Init loads the initial DAG registry and starts watching the target directory.
func (er *entryReaderImpl) Init(ctx context.Context) error {
	if er.recursiveDiscovery {
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
	if er.recursiveDiscovery {
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
			if recursiveEventMayChangeCatalog(event) {
				scheduleRefresh()
			}
		case <-refresh:
			refresh = nil
			if err := er.refreshRecursive(ctx, true, false); err != nil {
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

func recursiveEventMayChangeCatalog(event fsnotify.Event) bool {
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
func (er *entryReaderImpl) applyDAGFileSnapshot(ctx context.Context, fileName string, dag *core.DAG) {
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
func (er *entryReaderImpl) DAGs() []*core.DAG {
	er.lock.Lock()
	defer er.lock.Unlock()

	dags := make([]*core.DAG, 0, len(er.registry))
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
	if err := er.refreshRecursive(ctx, false, true); err != nil {
		_ = er.watcher.Close()
		return fmt.Errorf("failed to initialize recursive DAGs: %w", err)
	}
	return nil
}

func (er *entryReaderImpl) refreshRecursive(ctx context.Context, emitEvents, strictWatches bool) error {
	scan, err := dagdiscovery.Scan(er.targetDir, true)
	if err != nil {
		return err
	}
	if err := er.syncRecursiveWatches(ctx, scan.Directories, strictWatches); err != nil {
		return err
	}

	snapshot, err := er.recursiveRegistrySnapshot(ctx)
	if err != nil {
		return err
	}
	for _, discoveryErr := range snapshot.discoveryErrors {
		logger.Error(ctx, "DAG excluded from scheduler", tag.Error(errors.New(discoveryErr)))
	}

	events := er.replaceRecursiveRegistry(snapshot.registry, snapshot.stamps, emitEvents)
	for _, event := range events {
		er.sendEvent(ctx, event)
	}
	return nil
}

func (er *entryReaderImpl) syncRecursiveWatches(ctx context.Context, directories []string, strict bool) error {
	next := make(map[string]struct{}, len(directories))
	for _, dir := range directories {
		next[dir] = struct{}{}
		if _, exists := er.watchedDirs[dir]; exists {
			continue
		}
		if err := er.watcher.Add(dir); err != nil {
			if strict {
				return fmt.Errorf("failed to watch DAG directory %s: %w", dir, err)
			}
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
	return nil
}

func (er *entryReaderImpl) recursiveRegistrySnapshot(
	ctx context.Context,
) (recursiveDAGSnapshot, error) {
	paginator := exec.NewPaginator(1, int(^uint(0)>>1))
	result, discoveryErrors, err := er.dagStore.List(ctx, exec.ListDAGsOptions{Paginator: &paginator})
	if err != nil {
		return recursiveDAGSnapshot{}, err
	}

	registry := make(map[string]*core.DAG, len(result.Items))
	stamps := make(map[string]dagFileStamp, len(result.Items))
	for _, listedDAG := range result.Items {
		if len(listedDAG.BuildErrors) > 0 {
			discoveryErrors = append(discoveryErrors,
				fmt.Sprintf("reading %s failed: %s", listedDAG.FileName(), errors.Join(listedDAG.BuildErrors...)))
			continue
		}

		relativePath, err := filepath.Rel(er.targetDir, listedDAG.Location)
		if err != nil || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
			discoveryErrors = append(discoveryErrors,
				fmt.Sprintf("DAG path is outside the discovery directory: %s", listedDAG.Location))
			continue
		}
		locator := filepath.ToSlash(relativePath)
		if !strings.Contains(locator, "/") {
			locator = "./" + locator
		}
		dag, err := er.dagStore.GetMetadata(ctx, locator)
		if err != nil {
			discoveryErrors = append(discoveryErrors,
				fmt.Sprintf("reading %s failed: %s", filepath.ToSlash(relativePath), err))
			continue
		}
		info, err := os.Stat(dag.Location)
		if err != nil {
			discoveryErrors = append(discoveryErrors,
				fmt.Sprintf("reading %s failed: %s", filepath.ToSlash(relativePath), err))
			continue
		}

		key := filepath.ToSlash(relativePath)
		registry[key] = dag
		stamps[key] = dagFileStamp{size: info.Size(), modTime: info.ModTime().UnixNano()}
	}
	sort.Strings(discoveryErrors)
	return recursiveDAGSnapshot{
		registry:        registry,
		stamps:          stamps,
		discoveryErrors: discoveryErrors,
	}, nil
}

func (er *entryReaderImpl) replaceRecursiveRegistry(
	registry map[string]*core.DAG,
	stamps map[string]dagFileStamp,
	emitEvents bool,
) []DAGChangeEvent {
	er.lock.Lock()
	defer er.lock.Unlock()

	if !emitEvents {
		er.registry = registry
		er.stamps = stamps
		return nil
	}

	oldKeys := sortedDAGRegistryKeys(er.registry)
	newKeys := sortedDAGRegistryKeys(registry)
	events := make([]DAGChangeEvent, 0)
	for _, key := range oldKeys {
		if _, exists := registry[key]; exists {
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
		dag := registry[key]
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
		if er.stamps[key] != stamps[key] {
			events = append(events, DAGChangeEvent{
				Type:    DAGChangeUpdated,
				DAG:     dag,
				DAGName: dag.Name,
			})
		}
	}

	er.registry = registry
	er.stamps = stamps
	return events
}

func sortedDAGRegistryKeys(registry map[string]*core.DAG) []string {
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
