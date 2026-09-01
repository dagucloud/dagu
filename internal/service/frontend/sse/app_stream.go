// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sse

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler/filenotify"
	"github.com/fsnotify/fsnotify"
)

const (
	defaultAppStreamBufferSize = 32
	appStreamDebounceInterval  = 200 * time.Millisecond
	wikiPollingInterval        = 30 * time.Second
	schedulerStateFileName     = "state.json"
)

type AppEventType string

const (
	AppEventTypeReset      AppEventType = "reset"
	AppEventTypeDAGChanged AppEventType = "dag.changed"
	AppEventTypeQueue      AppEventType = "queue.changed"
	AppEventTypeScheduler  AppEventType = "scheduler.state.changed"
	AppEventTypeWiki       AppEventType = "wiki.page.changed"
)

// AppEvent carries low-volume invalidations that tell the UI what to revalidate.
type AppEvent struct {
	Type       AppEventType `json:"type"`
	FileName   string       `json:"fileName,omitempty"`
	Path       string       `json:"path,omitempty"`
	QueueName  string       `json:"queueName,omitempty"`
	Reason     string       `json:"reason,omitempty"`
	Node       string       `json:"node,omitempty"`
	ServerTime string       `json:"serverTime,omitempty"`
	Version    int          `json:"version,omitempty"`
}

type appSubscriber struct {
	ch     chan AppEvent
	ctx    context.Context
	cancel context.CancelFunc
}

type AppHub struct {
	mu          sync.Mutex
	subscribers map[*appSubscriber]struct{}
}

func NewAppHub() *AppHub {
	return &AppHub{
		subscribers: make(map[*appSubscriber]struct{}),
	}
}

func (h *AppHub) Subscribe(ctx context.Context) (<-chan AppEvent, func()) {
	subCtx, cancel := context.WithCancel(ctx)
	sub := &appSubscriber{
		ch:     make(chan AppEvent, defaultAppStreamBufferSize),
		ctx:    subCtx,
		cancel: cancel,
	}

	h.mu.Lock()
	h.subscribers[sub] = struct{}{}
	h.mu.Unlock()

	return sub.ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.subscribers[sub]; !ok {
			return
		}
		delete(h.subscribers, sub)
		close(sub.ch)
		sub.cancel()
	}
}

func (h *AppHub) Publish(event AppEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for sub := range h.subscribers {
		select {
		case <-sub.ctx.Done():
			delete(h.subscribers, sub)
			close(sub.ch)
		case sub.ch <- event:
		default:
			// Slow clients are disconnected so one stuck browser tab cannot
			// back up the shared invalidation stream.
			delete(h.subscribers, sub)
			close(sub.ch)
			sub.cancel()
		}
	}
}

type appEventCoalescer struct {
	mu      sync.Mutex
	pending map[string]AppEvent
	timer   *time.Timer
	delay   time.Duration
	publish func(AppEvent)
}

func newAppEventCoalescer(delay time.Duration, publish func(AppEvent)) *appEventCoalescer {
	return &appEventCoalescer{
		pending: make(map[string]AppEvent),
		delay:   delay,
		publish: publish,
	}
}

func (c *appEventCoalescer) Enqueue(event AppEvent) {
	if event.Type == AppEventTypeReset {
		c.PublishReset(event.Reason)
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending[c.key(event)] = event
	if c.timer != nil {
		return
	}
	c.timer = time.AfterFunc(c.delay, c.flush)
}

func (c *appEventCoalescer) PublishReset(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	c.pending = make(map[string]AppEvent)
	c.publish(AppEvent{
		Type:   AppEventTypeReset,
		Reason: reason,
	})
}

func (c *appEventCoalescer) key(event AppEvent) string {
	return string(event.Type) + "|" + event.FileName + "|" + event.Path + "|" + event.QueueName
}

func (c *appEventCoalescer) flush() {
	c.mu.Lock()
	events := make([]AppEvent, 0, len(c.pending))
	for _, event := range c.pending {
		events = append(events, event)
	}
	c.pending = make(map[string]AppEvent)
	c.timer = nil
	c.mu.Unlock()

	for _, event := range events {
		c.publish(event)
	}
}

type directoryWatcher struct {
	root       string
	createRoot bool
	scope      watchScope
	// skipDirName excludes directories with this base name (and their
	// subtrees) from watch registration; their events are unwanted and the
	// per-directory watches would consume kernel resources.
	skipDirName    string
	newFileWatcher fileWatcherFactory
	watcher        filenotify.FileWatcher
	onEvent        func(root, relPath string, op fsnotify.Op)
	onReset        func(reason string)
	done           chan struct{}
	stopOnce       sync.Once
	wg             sync.WaitGroup
}

type appWatcher interface {
	Start(context.Context) error
	Stop()
}

type watchScope int

const (
	watchScopeRootOnly watchScope = iota
	watchScopeOneLevel
	watchScopeRecursive
)

type fileWatcherFactory func() (filenotify.FileWatcher, error)

func newDirectoryWatcher(root string, createRoot bool, onEvent func(root, relPath string, op fsnotify.Op), onReset func(reason string)) *directoryWatcher {
	return newWatcher(root, createRoot, watchScopeRootOnly, onEvent, onReset)
}

func newOneLevelDirectoryWatcher(root string, createRoot bool, onEvent func(root, relPath string, op fsnotify.Op), onReset func(reason string)) *directoryWatcher {
	return newWatcher(root, createRoot, watchScopeOneLevel, onEvent, onReset)
}

func newRecursiveDirectoryWatcher(root string, createRoot bool, onEvent func(root, relPath string, op fsnotify.Op), onReset func(reason string)) *directoryWatcher {
	return newWatcher(root, createRoot, watchScopeRecursive, onEvent, onReset)
}

func newWikiDirectoryWatcher(root string, createRoot bool, onEvent func(root, relPath string, op fsnotify.Op), onReset func(reason string)) appWatcher {
	return &wikiDirectoryWatcher{
		root:       root,
		createRoot: createRoot,
		onEvent:    onEvent,
		onReset:    onReset,
	}
}

func newWatcher(root string, createRoot bool, scope watchScope, onEvent func(root, relPath string, op fsnotify.Op), onReset func(reason string)) *directoryWatcher {
	return &directoryWatcher{
		root:           root,
		createRoot:     createRoot,
		scope:          scope,
		newFileWatcher: defaultFileWatcherFactory,
		onEvent:        onEvent,
		onReset:        onReset,
		done:           make(chan struct{}),
	}
}

func defaultFileWatcherFactory() (filenotify.FileWatcher, error) {
	return filenotify.New(time.Second), nil
}

func (w *directoryWatcher) Start(ctx context.Context) error {
	ready, err := prepareWatchRoot(w.root, w.createRoot)
	if err != nil || !ready {
		return err
	}

	w.watcher, err = w.newFileWatcher()
	if err != nil {
		return err
	}
	if err := w.addWatch(w.root); err != nil {
		return err
	}

	if w.scope == watchScopeOneLevel || w.scope == watchScopeRecursive {
		paths, err := watchPathsForScope(w.root, w.scope, w.skipDirName)
		if err != nil {
			_ = w.watcher.Close()
			return err
		}
		for _, path := range paths {
			if path == w.root {
				continue
			}
			if err := w.addWatch(path); err != nil {
				return err
			}
		}
	}

	w.wg.Go(func() {
		w.loop(ctx)
	})
	return nil
}

func (w *directoryWatcher) addWatch(path string) error {
	if err := w.watcher.Add(path); err != nil {
		_ = w.watcher.Close()
		return err
	}
	return nil
}

func (w *directoryWatcher) Stop() {
	w.stopOnce.Do(func() {
		close(w.done)
		if w.watcher != nil {
			_ = w.watcher.Close()
		}
	})
	w.wg.Wait()
}

func (w *directoryWatcher) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.done:
			return
		case err, ok := <-w.watcher.Errors():
			if !ok {
				return
			}
			w.onReset(fmt.Sprintf("watcher error for %s: %v", w.root, err))
		case event, ok := <-w.watcher.Events():
			if !ok {
				return
			}
			w.handleEvent(event)
		}
	}
}

func (w *directoryWatcher) handleEvent(event fsnotify.Event) {
	if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
		return
	}

	if event.Op&fsnotify.Create != 0 && (w.scope == watchScopeOneLevel || w.scope == watchScopeRecursive) {
		if err := w.addCreatedDirWatches(event.Name); err != nil {
			w.onReset(fmt.Sprintf("failed to register watcher for %s: %v", event.Name, err))
		}
	}

	relPath, err := filepath.Rel(w.root, event.Name)
	if err != nil || relPath == "." {
		return
	}
	w.onEvent(w.root, filepath.ToSlash(relPath), event.Op)
}

func (w *directoryWatcher) addCreatedDirWatches(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	if w.skipDirName != "" && filepath.Base(path) == w.skipDirName {
		return nil
	}
	parent := filepath.Clean(filepath.Dir(path))
	if w.scope == watchScopeOneLevel && parent != filepath.Clean(w.root) {
		return nil
	}
	if w.scope != watchScopeRecursive {
		return w.watcher.Add(path)
	}
	return filepath.WalkDir(path, func(childPath string, entry os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if w.skipDirName != "" && entry.Name() == w.skipDirName {
			return filepath.SkipDir
		}
		info, err := entry.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		return w.watcher.Add(childPath)
	})
}

type wikiDirectoryWatcher struct {
	root       string
	createRoot bool
	onEvent    func(root, relPath string, op fsnotify.Op)
	onReset    func(reason string)
	active     appWatcher
}

func (w *wikiDirectoryWatcher) Start(ctx context.Context) error {
	ready, err := prepareWatchRoot(w.root, w.createRoot)
	if err != nil || !ready {
		return err
	}

	eventWatcher := newRecursiveDirectoryWatcher(w.root, false, w.onEvent, w.onReset)
	eventWatcher.newFileWatcher = filenotify.NewEventWatcher
	eventWatcher.skipDirName = wikiPageAttachmentsDirName
	eventErr := eventWatcher.Start(ctx)
	if eventErr == nil {
		w.active = eventWatcher
		return nil
	}
	eventWatcher.Stop()

	pollingWatcher := newMarkdownPollingWatcher(w.root, false, wikiPollingInterval, w.onEvent, w.onReset)
	if pollingErr := pollingWatcher.Start(ctx); pollingErr != nil {
		return fmt.Errorf("start Wiki watcher: event watcher: %w; markdown poller: %v", eventErr, pollingErr)
	}
	w.active = pollingWatcher
	return nil
}

func (w *wikiDirectoryWatcher) Stop() {
	if w.active != nil {
		w.active.Stop()
	}
}

type markdownPollingWatcher struct {
	root       string
	createRoot bool
	interval   time.Duration
	onEvent    func(root, relPath string, op fsnotify.Op)
	onReset    func(reason string)
	snapshot   map[string]markdownFileState
	done       chan struct{}
	stopOnce   sync.Once
	wg         sync.WaitGroup
}

type markdownFileState struct {
	size    int64
	mode    os.FileMode
	modTime time.Time
}

func newMarkdownPollingWatcher(root string, createRoot bool, interval time.Duration, onEvent func(root, relPath string, op fsnotify.Op), onReset func(reason string)) *markdownPollingWatcher {
	return &markdownPollingWatcher{
		root:       root,
		createRoot: createRoot,
		interval:   interval,
		onEvent:    onEvent,
		onReset:    onReset,
		done:       make(chan struct{}),
	}
}

func (w *markdownPollingWatcher) Start(ctx context.Context) error {
	ready, err := prepareWatchRoot(w.root, w.createRoot)
	if err != nil || !ready {
		return err
	}
	snapshot, err := snapshotMarkdownFiles(w.root)
	if err != nil {
		return err
	}
	w.snapshot = snapshot
	w.wg.Go(func() {
		w.loop(ctx)
	})
	return nil
}

func (w *markdownPollingWatcher) Stop() {
	w.stopOnce.Do(func() {
		close(w.done)
	})
	w.wg.Wait()
}

func (w *markdownPollingWatcher) loop(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.done:
			return
		case <-ticker.C:
			w.check()
		}
	}
}

func (w *markdownPollingWatcher) check() {
	next, err := snapshotMarkdownFiles(w.root)
	if err != nil {
		w.onReset(fmt.Sprintf("Wiki polling scan failed for %s: %v", w.root, err))
		return
	}

	for relPath, state := range next {
		prev, ok := w.snapshot[relPath]
		if !ok {
			w.onEvent(w.root, relPath, fsnotify.Create)
			continue
		}
		if !prev.equal(state) {
			w.onEvent(w.root, relPath, fsnotify.Write)
		}
	}
	for relPath := range w.snapshot {
		if _, ok := next[relPath]; !ok {
			w.onEvent(w.root, relPath, fsnotify.Remove)
		}
	}
	w.snapshot = next
}

func (s markdownFileState) equal(other markdownFileState) bool {
	return s.size == other.size &&
		s.mode == other.mode &&
		s.modTime.Equal(other.modTime)
}

func snapshotMarkdownFiles(root string) (map[string]markdownFileState, error) {
	files := map[string]markdownFileState{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry == nil || path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == wikiPageAttachmentsDirName {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(entry.Name()) != ".md" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relPath)] = markdownFileState{
			size:    info.Size(),
			mode:    info.Mode(),
			modTime: info.ModTime(),
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return files, nil
	}
	return files, err
}

func watchPathsForScope(root string, scope watchScope, skipDirName string) ([]string, error) {
	if scope == watchScopeRecursive {
		return recursiveWatchPaths(root, skipDirName)
	}
	return oneLevelWatchPaths(root)
}

func oneLevelWatchPaths(root string) ([]string, error) {
	paths := []string{root}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			paths = append(paths, filepath.Join(root, entry.Name()))
		}
	}
	return paths, nil
}

func recursiveWatchPaths(root string, skipDirName string) ([]string, error) {
	paths := []string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if skipDirName != "" && entry.Name() == skipDirName && path != root {
			return filepath.SkipDir
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	return paths, err
}

func prepareWatchRoot(root string, createRoot bool) (bool, error) {
	if root == "" {
		return false, nil
	}
	if createRoot {
		if err := os.MkdirAll(root, 0750); err != nil {
			return false, err
		}
		return true, nil
	}
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return true, nil
}

type AppStreamService struct {
	hub       *AppHub
	coalescer *appEventCoalescer
	watchers  []appWatcher
	ctx       context.Context
	cancel    context.CancelFunc
	stopOnce  sync.Once
}

type AppStreamConfig struct {
	Paths config.PathsConfig
}

func NewAppStreamService(cfg AppStreamConfig) (*AppStreamService, error) {
	ctx, cancel := context.WithCancel(context.Background())
	hub := NewAppHub()
	service := &AppStreamService{
		hub:    hub,
		ctx:    ctx,
		cancel: cancel,
	}
	service.coalescer = newAppEventCoalescer(appStreamDebounceInterval, hub.Publish)

	primaryDAGRoot := ""
	if cfg.Paths.DAGsDir != "" {
		primaryDAGRoot = filepath.Clean(cfg.Paths.DAGsDir)
	}

	paths := uniqueNonEmptyPaths(
		cfg.Paths.DAGsDir,
		cfg.Paths.AltDAGsDir,
	)
	for _, dagRoot := range paths {
		service.watchers = append(service.watchers, newDirectoryWatcher(
			dagRoot,
			dagRoot == primaryDAGRoot,
			service.handleDAGFileEvent,
			service.publishReset,
		))
	}
	if cfg.Paths.DataDir != "" {
		service.watchers = append(service.watchers, newDirectoryWatcher(
			persisfile.SchedulerStateDir(cfg.Paths),
			true,
			service.handleSchedulerStateEvent,
			service.publishReset,
		))
	}
	service.watchers = append(service.watchers,
		newWikiDirectoryWatcher(cfg.Paths.WikiDir, true, service.handleWikiPageEvent, service.publishReset),
		newDirectoryWatcher(cfg.Paths.SuspendFlagsDir, true, service.handleSuspendFlagEvent, service.publishReset),
		newOneLevelDirectoryWatcher(cfg.Paths.QueueDir, true, service.handleQueueEvent, service.publishReset),
	)

	for _, watcher := range service.watchers {
		if watcher == nil {
			continue
		}
		if err := watcher.Start(ctx); err != nil {
			service.Shutdown()
			return nil, err
		}
	}

	return service, nil
}

func uniqueNonEmptyPaths(paths ...string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if path == "" {
			continue
		}
		cleaned := filepath.Clean(path)
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	return out
}

func (s *AppStreamService) Shutdown() {
	s.stopOnce.Do(func() {
		s.cancel()
		for _, watcher := range s.watchers {
			if watcher != nil {
				watcher.Stop()
			}
		}
	})
}

func (s *AppStreamService) Subscribe(ctx context.Context) (<-chan AppEvent, func()) {
	return s.hub.Subscribe(ctx)
}

func (s *AppStreamService) publishReset(reason string) {
	s.coalescer.PublishReset(reason)
}

func (s *AppStreamService) handleDAGFileEvent(_, relPath string, op fsnotify.Op) {
	ext := strings.ToLower(filepath.Ext(relPath))
	if ext != ".yaml" && ext != ".yml" {
		return
	}
	s.coalescer.Enqueue(AppEvent{
		Type:     AppEventTypeDAGChanged,
		FileName: filepath.ToSlash(relPath),
		Reason:   fileEventReason(op),
	})
}

func (s *AppStreamService) handleSuspendFlagEvent(_, relPath string, op fsnotify.Op) {
	if filepath.Ext(relPath) != ".suspend" {
		return
	}
	s.coalescer.Enqueue(AppEvent{
		Type:   AppEventTypeDAGChanged,
		Reason: "suspend_flag_" + fileEventReason(op),
	})
}

func (s *AppStreamService) handleSchedulerStateEvent(_, relPath string, op fsnotify.Op) {
	if filepath.ToSlash(relPath) != schedulerStateFileName {
		return
	}
	s.coalescer.Enqueue(AppEvent{
		Type:   AppEventTypeScheduler,
		Reason: fileEventReason(op),
	})
}

func (s *AppStreamService) handleQueueEvent(_, relPath string, op fsnotify.Op) {
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	if len(parts) == 0 {
		return
	}
	base := filepath.Base(relPath)
	if !strings.HasPrefix(base, "item_") || filepath.Ext(base) != ".json" {
		return
	}
	s.coalescer.Enqueue(AppEvent{
		Type:      AppEventTypeQueue,
		QueueName: parts[0],
		Reason:    fileEventReason(op),
	})
}

// wikiPageAttachmentsDirName is the reserved attachment subtree inside the Wiki
// directory. Files under it are attachments rather than Wiki pages and must
// not produce Wiki page invalidations.
const wikiPageAttachmentsDirName = ".attachments"

func isWikiPageAttachmentPath(relPath string) bool {
	for segment := range strings.SplitSeq(filepath.ToSlash(relPath), "/") {
		if segment == wikiPageAttachmentsDirName {
			return true
		}
	}
	return false
}

func (s *AppStreamService) handleWikiPageEvent(_, relPath string, op fsnotify.Op) {
	if filepath.Ext(relPath) != ".md" || isWikiPageAttachmentPath(relPath) {
		return
	}
	s.coalescer.Enqueue(AppEvent{
		Type:   AppEventTypeWiki,
		Path:   strings.TrimSuffix(filepath.ToSlash(relPath), ".md"),
		Reason: fileEventReason(op),
	})
}

func fileEventReason(op fsnotify.Op) string {
	switch {
	case op&fsnotify.Create != 0:
		return "created"
	case op&fsnotify.Remove != 0:
		return "removed"
	case op&fsnotify.Rename != 0:
		return "renamed"
	default:
		return "updated"
	}
}
