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

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/service/scheduler/filenotify"
	"github.com/fsnotify/fsnotify"
)

const (
	defaultAppStreamBufferSize = 32
	appStreamDebounceInterval  = 200 * time.Millisecond
)

type AppEventType string

const (
	AppEventTypeReset      AppEventType = "reset"
	AppEventTypeDAGChanged AppEventType = "dag.changed"
	AppEventTypeQueue      AppEventType = "queue.changed"
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
	watcher    filenotify.FileWatcher
	onEvent    func(root, relPath string, op fsnotify.Op)
	onReset    func(reason string)
	done       chan struct{}
	stopOnce   sync.Once
	wg         sync.WaitGroup
}

type appWatcher interface {
	Start(context.Context) error
	Stop()
}

type watchScope int

const (
	watchScopeRootOnly watchScope = iota
	watchScopeOneLevel
)

func newDirectoryWatcher(root string, createRoot bool, onEvent func(root, relPath string, op fsnotify.Op), onReset func(reason string)) *directoryWatcher {
	return newWatcher(root, createRoot, watchScopeRootOnly, onEvent, onReset)
}

func newOneLevelDirectoryWatcher(root string, createRoot bool, onEvent func(root, relPath string, op fsnotify.Op), onReset func(reason string)) *directoryWatcher {
	return newWatcher(root, createRoot, watchScopeOneLevel, onEvent, onReset)
}

func newWatcher(root string, createRoot bool, scope watchScope, onEvent func(root, relPath string, op fsnotify.Op), onReset func(reason string)) *directoryWatcher {
	return &directoryWatcher{
		root:       root,
		createRoot: createRoot,
		scope:      scope,
		onEvent:    onEvent,
		onReset:    onReset,
		done:       make(chan struct{}),
	}
}

func (w *directoryWatcher) Start(ctx context.Context) error {
	ready, err := prepareWatchRoot(w.root, w.createRoot)
	if err != nil || !ready {
		return err
	}

	w.watcher = filenotify.New(time.Second)
	if err := w.addWatch(w.root); err != nil {
		return err
	}

	if w.scope == watchScopeOneLevel {
		paths, err := oneLevelWatchPaths(w.root)
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

	if event.Op&fsnotify.Create != 0 && w.scope == watchScopeOneLevel {
		if err := w.addCreatedChildDir(event.Name); err != nil {
			w.onReset(fmt.Sprintf("failed to register watcher for %s: %v", event.Name, err))
		}
	}

	relPath, err := filepath.Rel(w.root, event.Name)
	if err != nil || relPath == "." {
		return
	}
	w.onEvent(w.root, filepath.ToSlash(relPath), event.Op)
}

func (w *directoryWatcher) addCreatedChildDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	parent := filepath.Clean(filepath.Dir(path))
	if parent != filepath.Clean(w.root) {
		return nil
	}
	return w.watcher.Add(path)
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
	service.watchers = append(service.watchers,
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
