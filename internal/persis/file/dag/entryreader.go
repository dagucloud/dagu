// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/filenotify"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/persis"

	"github.com/fsnotify/fsnotify"
)

// entryReaderImpl manages DAGs on local filesystem.
type entryReaderImpl struct {
	baseConfigPath         string
	workspaceBaseConfigDir string
	calendarsDir           string
	baseConfigState        string
	baseWatchPaths         []baseWatchPath
	targetDir              string
	registry               map[string]*ir.DAG
	loads                  map[string]dagFileLoad
	watchedDirs            map[string]struct{}
	lock                   sync.Mutex
	dagRepository          *persis.DAGRepository
	dagSource              *dagFileSource
	watcher                filenotify.FileWatcher
	recursive              bool
	quit                   chan struct{}
	closeOnce              sync.Once
	events                 chan persis.DAGChangeEvent
	newWatcher             func() filenotify.FileWatcher
	resyncInterval         time.Duration
	symlinkPollInterval    time.Duration
	symlinkTargets         map[string]symlinkTarget
	marker                 *ChangeMarker
	markerInterval         time.Duration
	markerRefreshGap       time.Duration
	markerSeen             markerVersions
	reportedIssues         map[string]struct{}
}

// EntryReaderOption customizes a filesystem DAG entry reader.
type EntryReaderOption func(*entryReaderImpl)

// withResyncInterval sets how often the reader re-reads the whole DAG
// directory regardless of watcher events or the change marker. It is the safety
// net for changes that do not go through the Dagu store, such as DAG files
// edited directly or pulled by git sync. A non-positive value disables it.
func withResyncInterval(interval time.Duration) EntryReaderOption {
	return func(er *entryReaderImpl) {
		er.resyncInterval = interval
	}
}

// withSymlinkPollInterval sets how often the reader checks what its symlinked
// DAG files resolve to. A non-positive value disables the check.
func withSymlinkPollInterval(interval time.Duration) EntryReaderOption {
	return func(er *entryReaderImpl) {
		er.symlinkPollInterval = interval
	}
}

// WatchCalendars makes the reader load DAGs again when a calendar file in
// dir changes, so schedules follow the calendars they name.
func WatchCalendars(dir string) EntryReaderOption {
	return func(er *entryReaderImpl) {
		er.calendarsDir = dir
	}
}

// WithChangeMarker makes the reader refresh the registry when the change
// marker of another host changes, checked every interval. Such a refresh
// starts no sooner than 15 seconds after the previous refresh ends, so a burst
// of remote edits costs a few full re-reads rather than one per check.
// path is this host's marker, as returned by ChangeMarkerPath; the reader
// ignores it because its file watches already report this host's changes. An
// empty path disables the marker and a non-positive interval selects the
// default.
func WithChangeMarker(path string, interval time.Duration) EntryReaderOption {
	return func(er *entryReaderImpl) {
		er.marker = nil
		if path == "" {
			return
		}
		er.marker = NewChangeMarker(path)
		if interval <= 0 {
			interval = defaultChangeMarkerInterval
		}
		er.markerInterval = interval
	}
}

// markerSignalsChange polls the change markers and reports whether the registry
// should be refreshed, which is when another host's marker is new or changed.
func (er *entryReaderImpl) markerSignalsChange(ctx context.Context) bool {
	next, changed := er.marker.poll(ctx, er.markerSeen)
	er.markerSeen = next
	return changed
}

// withMarkerRefreshGap sets the minimum time between refreshes triggered by
// change markers.
func withMarkerRefreshGap(gap time.Duration) EntryReaderOption {
	return func(er *entryReaderImpl) {
		er.markerRefreshGap = gap
	}
}

// withWatcherFactory replaces the file watcher implementation.
func withWatcherFactory(factory func() filenotify.FileWatcher) EntryReaderOption {
	return func(er *entryReaderImpl) {
		er.newWatcher = factory
	}
}

const (
	// defaultChangeMarkerInterval is how often the change marker is checked.
	defaultChangeMarkerInterval = 5 * time.Second
	// defaultMarkerRefreshGap is the minimum time from the end of a refresh to
	// a refresh triggered by a change marker. Each refresh re-reads every DAG,
	// and the shared index is rebuilt after every store write, so sustained
	// remote edits would otherwise keep a large registry refreshing.
	defaultMarkerRefreshGap = 15 * time.Second
	// defaultResyncInterval is the full re-read period. The change marker covers
	// edits made through Dagu on another host; this covers edits made by other
	// tools (git, kubectl cp, editors), which native file watches also miss on a
	// network file system.
	defaultResyncInterval = 5 * time.Minute
	// defaultSymlinkPollInterval is how often symlinked DAG files are checked
	// for a new target. Directory watches miss such changes: the target can be
	// outside the watched directories, and kqueue reports nothing when a link
	// is re-pointed under the same name.
	defaultSymlinkPollInterval = 5 * time.Second
	// minStampAge is how long before its load a file must have been last
	// modified for a full refresh to trust an unchanged stamp. File systems
	// record modification times in ticks of up to 2s, so a write in the same
	// tick as the load can leave the stamp unchanged.
	minStampAge = 5 * time.Second
)

type dagFileStamp struct {
	size    int64
	modTime int64
}

// symlinkTarget is the file a symlinked DAG file resolved to when observed.
// The zero value records a link that did not resolve.
type symlinkTarget struct {
	path  string
	stamp dagFileStamp
}

// dagFileLoad records what a registry entry was loaded from, so a full refresh
// can keep the entry while none of it has changed.
type dagFileLoad struct {
	stamp dagFileStamp
	// statAt is when stamp was taken, before the load, in Unix nanoseconds.
	statAt int64
	// baseState describes the base configs the entry was loaded with.
	baseState string
}

type registryState struct {
	dags   map[string]*ir.DAG
	loads  map[string]dagFileLoad
	issues []string
}

// NewFileEntryReader creates a filesystem DAG entry reader.
func NewFileEntryReader(dir string, dagRepository *persis.DAGRepository, recursive bool, baseConfigPath, workspaceBaseConfigDir string, opts ...EntryReaderOption) *entryReaderImpl {
	er := &entryReaderImpl{
		targetDir:              dir,
		baseConfigPath:         baseConfigPath,
		workspaceBaseConfigDir: workspaceBaseConfigDir,
		registry:               make(map[string]*ir.DAG),
		loads:                  make(map[string]dagFileLoad),
		watchedDirs:            make(map[string]struct{}),
		dagRepository:          dagRepository,
		dagSource:              newDAGFileSource(dir, dagRepository),
		recursive:              recursive,
		quit:                   make(chan struct{}),
		events:                 make(chan persis.DAGChangeEvent, 64),
		newWatcher:             func() filenotify.FileWatcher { return filenotify.New(time.Minute) },
		resyncInterval:         defaultResyncInterval,
		symlinkPollInterval:    defaultSymlinkPollInterval,
		markerRefreshGap:       defaultMarkerRefreshGap,
	}
	for _, opt := range opts {
		opt(er)
	}
	return er
}

// Init loads the initial DAG registry and starts watching the target directory.
func (er *entryReaderImpl) Init(ctx context.Context) error {
	for _, path := range []*string{&er.targetDir, &er.baseConfigPath, &er.workspaceBaseConfigDir, &er.calendarsDir} {
		if *path != "" {
			absolute, err := filepath.Abs(*path)
			if err != nil {
				return err
			}
			*path = absolute
		}
	}
	er.baseConfigState = er.describeInputState()
	// Read the marker before loading so a change made while loading is seen by
	// the next poll.
	er.markerSeen, _ = er.marker.poll(ctx, nil)
	if er.recursive {
		return er.initRecursive(ctx)
	}

	er.lock.Lock()
	defer er.lock.Unlock()
	er.watcher = er.newWatcher()
	if err := er.addInitialWatches([]string{er.targetDir}); err != nil {
		_ = er.watcher.Close()
		return err
	}
	er.symlinkTargets = er.observeSymlinkTargets()
	if err := er.initialize(ctx); err != nil {
		_ = er.watcher.Close()
		return fmt.Errorf("failed to initialize DAGs: %w", err)
	}
	return nil
}

// Start forwards watcher events into registry updates until the reader stops.
func (er *entryReaderImpl) Start(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error(ctx, "Entry reader watcher panicked", tag.Error(fmt.Errorf("%v", r)))
		}
	}()
	er.watch(ctx)
}

const registryRefreshDelay = 75 * time.Millisecond

func (er *entryReaderImpl) watch(ctx context.Context) {
	var refreshTimer *time.Timer
	var refresh <-chan time.Time
	scheduleRefresh := func() {
		if refreshTimer == nil {
			refreshTimer = time.NewTimer(registryRefreshDelay)
			refresh = refreshTimer.C
			return
		}
		if !refreshTimer.Stop() {
			select {
			case <-refreshTimer.C:
			default:
			}
		}
		refreshTimer.Reset(registryRefreshDelay)
		refresh = refreshTimer.C
	}
	defer func() {
		if refreshTimer != nil {
			refreshTimer.Stop()
		}
	}()

	// Watches only see writes made on this host; on a network file system the
	// rest is picked up by re-reading the directory on a timer.
	var resync <-chan time.Time
	if er.resyncInterval > 0 {
		resyncTicker := time.NewTicker(er.resyncInterval)
		defer resyncTicker.Stop()
		resync = resyncTicker.C
	}

	var symlinkPoll <-chan time.Time
	if er.symlinkPollInterval > 0 {
		symlinkTicker := time.NewTicker(er.symlinkPollInterval)
		defer symlinkTicker.Stop()
		symlinkPoll = symlinkTicker.C
	}

	var markerPoll <-chan time.Time
	if er.marker != nil && er.markerInterval > 0 {
		markerTicker := time.NewTicker(er.markerInterval)
		defer markerTicker.Stop()
		markerPoll = markerTicker.C
	}
	// A marker change that arrives within markerRefreshGap of the end of the
	// previous refresh is deferred, not dropped. The gap runs from the end so
	// that a slow re-read of a large registry cannot use it up.
	var lastRefreshEnd time.Time
	var deferredMarkerRefresh <-chan time.Time
	refreshAfterGap := func() {
		if wait := er.markerRefreshGap - time.Since(lastRefreshEnd); wait > 0 {
			deferredMarkerRefresh = time.After(wait)
			return
		}
		scheduleRefresh()
	}

	for {
		select {
		case <-er.quit:
			return
		case <-ctx.Done():
			return
		case <-resync:
			scheduleRefresh()
		case <-symlinkPoll:
			// A pending refresh observes the links itself, and rescheduling it
			// would postpone it.
			if refresh == nil && !maps.Equal(er.observeSymlinkTargets(), er.symlinkTargets) {
				scheduleRefresh()
			}
		case <-markerPoll:
			if !er.markerSignalsChange(ctx) || deferredMarkerRefresh != nil {
				continue
			}
			refreshAfterGap()
		case <-deferredMarkerRefresh:
			// A refresh may have ended since the timer was set.
			deferredMarkerRefresh = nil
			refreshAfterGap()
		case event, ok := <-er.watcher.Events():
			if !ok {
				return
			}
			if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
				// Native watches disappear with their directory, even if its name is reused.
				for dir := range er.watchedDirs {
					if pathWithin(dir, event.Name) {
						_ = er.watcher.Remove(dir)
						delete(er.watchedDirs, dir)
					}
				}
			}
			// A symlink target or its link directory can change without an event
			// named after the configured base file.
			baseState := er.describeInputState()
			if baseState != er.baseConfigState {
				er.baseConfigState = baseState
				scheduleRefresh()
				continue
			}
			switch {
			case er.isBaseConfigEvent(event):
				scheduleRefresh()
			case !pathWithin(event.Name, er.targetDir), pathWithin(event.Name, er.workspaceBaseConfigDir):
				continue
			case er.recursive:
				if needsRecursiveRefresh(event) {
					scheduleRefresh()
				}
			case fileutil.IsYAMLFile(event.Name):
				er.handleFSEvent(ctx, event)
			}
		case <-refresh:
			refresh = nil
			if err := er.refreshRegistry(ctx); err != nil {
				logger.Error(ctx, "Failed to refresh DAG registry", tag.Error(err))
			}
			lastRefreshEnd = time.Now()
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

// handleFSEvent processes a filesystem event and emits a persis.DAGChangeEvent.
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
			er.applyDAGFileSnapshot(ctx, fileName, snapshot)
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

	er.applyDAGFileSnapshot(ctx, fileName, snapshot)
	logger.Info(ctx, "DAG added/updated", tag.Name(fileName))
}

// applyDAGFileSnapshot stores a loaded DAG and emits the matching add/update events.
func (er *entryReaderImpl) applyDAGFileSnapshot(ctx context.Context, fileName string, snapshot dagFileSnapshot) {
	dag := snapshot.dag
	// Determine add vs update by checking registry before updating
	er.lock.Lock()
	oldDAG, existed := er.registry[fileName]
	var oldDAGName string
	if existed && oldDAG.Name != dag.Name {
		oldDAGName = oldDAG.Name
	}
	er.registry[fileName] = dag
	if er.loads == nil {
		er.loads = make(map[string]dagFileLoad)
	}
	er.loads[fileName] = er.snapshotLoad(snapshot)
	er.lock.Unlock()

	// If the DAG name changed, emit delete for the old name first
	if oldDAGName != "" {
		er.sendEvent(ctx, persis.DAGChangeEvent{
			Type: persis.DAGChangeDeleted,
			DAGEntry: persis.DAGEntry{
				DefinitionID: definitionIDForFile(fileName),
				DAG:          oldDAG,
			},
		})
	}

	changeType := persis.DAGChangeAdded
	if existed && oldDAGName == "" {
		changeType = persis.DAGChangeUpdated
	}
	er.sendEvent(ctx, persis.DAGChangeEvent{
		Type: changeType,
		DAGEntry: persis.DAGEntry{
			DefinitionID: definitionIDForFile(fileName),
			DAG:          dag,
		},
	})
}

// snapshotLoad records how a watcher snapshot was loaded. The watch loop keeps
// baseConfigState current before it handles a DAG file event.
func (er *entryReaderImpl) snapshotLoad(snapshot dagFileSnapshot) dagFileLoad {
	return dagFileLoad{stamp: snapshot.stamp, statAt: snapshot.statAt, baseState: er.baseConfigState}
}

// removeDAGFile drops a confirmed-absent DAG file from the registry.
func (er *entryReaderImpl) removeDAGFile(ctx context.Context, fileName string) {
	// Capture DAG name from registry before deleting
	er.lock.Lock()
	dag, existed := er.registry[fileName]
	delete(er.registry, fileName)
	delete(er.loads, fileName)
	er.lock.Unlock()

	if existed && dag != nil {
		er.sendEvent(ctx, persis.DAGChangeEvent{
			Type: persis.DAGChangeDeleted,
			DAGEntry: persis.DAGEntry{
				DefinitionID: definitionIDForFile(fileName),
				DAG:          dag,
			},
		})
	}
	logger.Info(ctx, "DAG removed", tag.Name(fileName))
}

// sendEvent sends a persis.DAGChangeEvent on the channel.
// Returns immediately if the entry reader is shutting down or the context is cancelled.
func (er *entryReaderImpl) sendEvent(ctx context.Context, event persis.DAGChangeEvent) {
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

// Entries returns the currently loaded DAG metadata.
func (er *entryReaderImpl) Entries() []persis.DAGEntry {
	er.lock.Lock()
	defer er.lock.Unlock()

	entries := make([]persis.DAGEntry, 0, len(er.registry))
	for fileName, dag := range er.registry {
		entries = append(entries, persis.DAGEntry{DefinitionID: definitionIDForFile(fileName), DAG: dag})
	}
	return entries
}

func (er *entryReaderImpl) Events() <-chan persis.DAGChangeEvent {
	return er.events
}

func definitionIDForFile(fileName string) string {
	base := filepath.Base(filepath.FromSlash(fileName))
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func (er *entryReaderImpl) initRecursive(ctx context.Context) error {
	er.watcher = er.newWatcher()

	scannedAt := time.Now()
	scan, err := Discover(er.targetDir, DiscoveryOptions{Recursive: true})
	if err != nil {
		_ = er.watcher.Close()
		return fmt.Errorf("failed to initialize recursive DAGs: %w", err)
	}
	if err := er.addInitialWatches(scan.Dirs); err != nil {
		_ = er.watcher.Close()
		return err
	}
	er.symlinkTargets = er.observeSymlinkTargets()

	state, err := er.loadRegistry(ctx, scan, scannedAt)
	if err != nil {
		_ = er.watcher.Close()
		return fmt.Errorf("failed to initialize recursive DAGs: %w", err)
	}
	er.reportIssues(ctx, state.issues)

	er.lock.Lock()
	er.registry = state.dags
	er.loads = state.loads
	er.lock.Unlock()
	return nil
}

func (er *entryReaderImpl) refreshRegistry(ctx context.Context) error {
	scannedAt := time.Now()
	// Observed before the scan, so a link changed during the refresh no longer
	// matches, and kept only once the refresh succeeds, so a failed one is
	// retried by the next symlink poll.
	symlinkTargets := er.observeSymlinkTargets()
	scan, err := Discover(er.targetDir, DiscoveryOptions{Recursive: er.recursive})
	if err != nil {
		return err
	}
	er.syncWatches(ctx, scan.Dirs)

	state, err := er.loadRegistry(ctx, scan, scannedAt)
	if err != nil {
		return err
	}
	er.reportIssues(ctx, state.issues)

	events := er.replaceRegistry(state)
	er.symlinkTargets = symlinkTargets
	for i, event := range events {
		// The events of one refresh form one change.
		event.More = i < len(events)-1
		er.sendEvent(ctx, event)
	}
	return nil
}

// reportIssues logs the DAGs a full refresh excluded. An issue is logged as an
// error when it first appears and at debug level while it persists, so the
// periodic refresh does not repeat the same errors.
func (er *entryReaderImpl) reportIssues(ctx context.Context, issues []string) {
	reported := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		reported[issue] = struct{}{}
		if _, seen := er.reportedIssues[issue]; seen {
			logger.Debug(ctx, "DAG excluded from scheduler", tag.Error(errors.New(issue)))
			continue
		}
		logger.Error(ctx, "DAG excluded from scheduler", tag.Error(errors.New(issue)))
	}
	er.reportedIssues = reported
}

func (er *entryReaderImpl) syncWatches(ctx context.Context, dirs []string) {
	dirs, err := er.withBaseWatchDirs(dirs)
	if err != nil {
		logger.Error(ctx, "Failed to discover base config watches", tag.Error(err))
		return
	}
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

// loadRegistry loads every listed DAG, keeping registry entries that loading
// again would not change. scan is the target directory as seen at scannedAt,
// before the listing.
func (er *entryReaderImpl) loadRegistry(ctx context.Context, scan DiscoveryResult, scannedAt time.Time) (registryState, error) {
	// Described before loading, so a base config change during the load makes
	// the next refresh load again.
	baseState := er.describeInputState()
	scanned := make(map[string]DiscoveredFile, len(scan.Files))
	for _, file := range scan.Files {
		scanned[file.RelPath] = file
	}

	paginator := pagination.NewPaginator(1, math.MaxInt)
	result, issues, err := er.dagRepository.List(ctx, persis.DAGListOptions{Paginator: &paginator})
	if err != nil {
		return registryState{}, err
	}

	dags := make(map[string]*ir.DAG, len(result.Items))
	loads := make(map[string]dagFileLoad, len(result.Items))
	for _, listedDAG := range result.Items {
		if er.isBaseConfigEvent(fsnotify.Event{Name: listedDAG.Location}) {
			continue
		}
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
		// The stamp is always taken before loading: a write that lands in
		// between must still look like a change to the next refresh.
		load := dagFileLoad{baseState: baseState}
		if file, ok := scanned[key]; ok {
			load.stamp = dagFileStamp{size: file.Size, modTime: file.ModTime}
			load.statAt = scannedAt.UnixNano()
			if dag, prev, ok := er.reusableDAG(key, file.ResolvedPath, load); ok {
				dags[key] = dag
				loads[key] = prev
				continue
			}
		} else {
			// Missing from the scan, such as a file created after it or a
			// symlink the scan skips.
			load.statAt = time.Now().UnixNano()
			info, err := os.Stat(listedDAG.Location)
			if err != nil {
				issues = append(issues, fmt.Sprintf("reading %s failed: %s", key, err))
				continue
			}
			load.stamp = dagFileStamp{size: info.Size(), modTime: info.ModTime().UnixNano()}
		}
		dag, err := er.dagRepository.GetMetadata(ctx, locator)
		if err != nil {
			issues = append(issues, fmt.Sprintf("reading %s failed: %s", key, err))
			continue
		}

		dags[key] = dag
		loads[key] = load
	}
	sort.Strings(issues)
	return registryState{
		dags:   dags,
		loads:  loads,
		issues: issues,
	}, nil
}

// describeInputState describes the base configs and calendar files the
// loaded DAGs depend on.
func (er *entryReaderImpl) describeInputState() string {
	return describeDefinitionInputState(er.baseConfigPath, er.workspaceBaseConfigDir, er.calendarsDir)
}

// scheduleCalendarDigests identifies the calendars bound to a DAG's start
// schedules, so a calendar edit counts as a change to the DAGs using it.
func scheduleCalendarDigests(dag *ir.DAG) []string {
	var digests []string
	for _, sched := range dag.Schedule {
		if cal, ok := sched.BoundCalendar(); ok {
			digests = append(digests, cal.Digest)
		}
	}
	return digests
}

// reusableDAG returns the registry entry for key, and how it was loaded, when
// loading the file again would give the same DAG: the file still has the
// resolved target, stamp and base configs of the earlier load, and had not
// been modified for minStampAge before that load.
func (er *entryReaderImpl) reusableDAG(key, resolvedPath string, current dagFileLoad) (*ir.DAG, dagFileLoad, bool) {
	er.lock.Lock()
	defer er.lock.Unlock()

	dag, ok := er.registry[key]
	prev := er.loads[key]
	if !ok || dag == nil || dag.SourceFile != resolvedPath ||
		prev.stamp != current.stamp || prev.baseState != current.baseState ||
		prev.stamp.modTime > prev.statAt-int64(minStampAge) {
		return nil, dagFileLoad{}, false
	}
	return dag, prev, true
}

func (er *entryReaderImpl) replaceRegistry(state registryState) []persis.DAGChangeEvent {
	er.lock.Lock()
	defer er.lock.Unlock()

	oldKeys := sortedRegistryKeys(er.registry)
	newKeys := sortedRegistryKeys(state.dags)
	events := make([]persis.DAGChangeEvent, 0)
	for _, key := range oldKeys {
		if _, exists := state.dags[key]; exists {
			continue
		}
		if oldDAG := er.registry[key]; oldDAG != nil {
			events = append(events, persis.DAGChangeEvent{
				Type: persis.DAGChangeDeleted,
				DAGEntry: persis.DAGEntry{
					DefinitionID: definitionIDForFile(key),
					DAG:          oldDAG,
				},
			})
		}
	}
	for _, key := range newKeys {
		dag := state.dags[key]
		oldDAG, existed := er.registry[key]
		if !existed {
			events = append(events, persis.DAGChangeEvent{
				Type: persis.DAGChangeAdded,
				DAGEntry: persis.DAGEntry{
					DefinitionID: definitionIDForFile(key),
					DAG:          dag,
				},
			})
			continue
		}
		if oldDAG.Name != dag.Name {
			events = append(events,
				persis.DAGChangeEvent{Type: persis.DAGChangeDeleted, DAGEntry: persis.DAGEntry{DefinitionID: definitionIDForFile(key), DAG: oldDAG}},
				persis.DAGChangeEvent{Type: persis.DAGChangeAdded, DAGEntry: persis.DAGEntry{DefinitionID: definitionIDForFile(key), DAG: dag}},
			)
			continue
		}
		// A reload can change the DAG without changing the stamp, such as a
		// symlink retargeted to a file of the same size and time.
		if er.loads[key].stamp != state.loads[key].stamp || oldDAG.SourceFile != dag.SourceFile ||
			!bytes.Equal(oldDAG.YamlData, dag.YamlData) || !bytes.Equal(oldDAG.BaseConfigData, dag.BaseConfigData) ||
			!slices.Equal(scheduleCalendarDigests(oldDAG), scheduleCalendarDigests(dag)) {
			events = append(events, persis.DAGChangeEvent{
				Type: persis.DAGChangeUpdated,
				DAGEntry: persis.DAGEntry{
					DefinitionID: definitionIDForFile(key),
					DAG:          dag,
				},
			})
		}
	}

	er.registry = state.dags
	er.loads = state.loads
	return events
}

// observeSymlinkTargets returns, by link path, what each symlinked DAG file in
// the watched DAG directories resolves to.
func (er *entryReaderImpl) observeSymlinkTargets() map[string]symlinkTarget {
	targets := make(map[string]symlinkTarget)
	for dir := range er.watchedDirs {
		if !pathWithin(dir, er.targetDir) {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink == 0 || !fileutil.IsYAMLFile(entry.Name()) {
				continue
			}
			link := filepath.Join(dir, entry.Name())
			var target symlinkTarget
			if resolved, err := filepath.EvalSymlinks(link); err == nil {
				if info, err := os.Stat(resolved); err == nil {
					target = symlinkTarget{
						path:  resolved,
						stamp: dagFileStamp{size: info.Size(), modTime: info.ModTime().UnixNano()},
					}
				}
			}
			targets[link] = target
		}
	}
	return targets
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
		if fileutil.IsYAMLFile(fi.Name()) && !er.isBaseConfigEvent(fsnotify.Event{Name: filepath.Join(er.targetDir, fi.Name())}) {
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
			er.loads[fi.Name()] = er.snapshotLoad(snapshot)
			dags = append(dags, fi.Name())
		}
	}

	logger.Debug(ctx, "DAGs loaded", slog.String("dags", strings.Join(dags, ",")))
	return nil
}
