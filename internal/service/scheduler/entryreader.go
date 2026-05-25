// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
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

// entryReaderImpl manages DAGs on local filesystem.
type entryReaderImpl struct {
	targetDir string
	registry  map[string]*core.DAG
	lock      sync.Mutex
	dagStore  exec.DAGStore
	dagSource *dagFileSource
	watcher   filenotify.FileWatcher
	quit      chan struct{}
	closeOnce sync.Once
	events    chan DAGChangeEvent
}

// NewEntryReader creates a new DAG manager with the given configuration.
func NewEntryReader(dir string, dagCli exec.DAGStore) EntryReader {
	return &entryReaderImpl{
		targetDir: dir,
		registry:  make(map[string]*core.DAG),
		dagStore:  dagCli,
		dagSource: newDAGFileSource(dir),
		quit:      make(chan struct{}),
	}
}

// setEvents wires the event channel used to notify the TickPlanner of DAG
// changes. Must be called before Start().
func (er *entryReaderImpl) setEvents(ch chan DAGChangeEvent) {
	er.events = ch
}

// Init loads the initial DAG registry and starts watching the target directory.
func (er *entryReaderImpl) Init(ctx context.Context) error {
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
