// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
)

// A DAG with watch: starts a run for each file that arrives in a folder.
// The folder is read every watch.interval. A file counts as arrived once
// its size and modification time are the same on two readings in a row,
// so a file still being copied is left alone. Each arrival starts one run,
// with the file's path as WATCH_FILE and its name as WATCH_NAME, and is
// remembered by its size and time, so the file runs again only when it is
// replaced. The files a folder holds when it is first watched are
// remembered without running: a watch takes what arrives, not an archive.
// A file that arrives while the scheduler is off is new when it returns.

// watchTick is how often the watcher looks for folders due to be read.
const watchTick = time.Second

// WatchEnqueueFunc starts a run for a file that arrived, with params as
// WatchRuntimeParams gives them.
type WatchEnqueueFunc func(ctx context.Context, entry DAGEntry, runID, params string) error

// WatchRuntimeParams are the params a file's run receives.
func WatchRuntimeParams(path string) string {
	return fmt.Sprintf("%s=%s %s=%s", ir.WatchFileParam, strconv.Quote(path), ir.WatchNameParam, strconv.Quote(filepath.Base(path)))
}

// WatchRunID names the run of one version of a file, the same every time
// it is derived: when the ledger that remembers the file is lost, as it is
// when the process stops between queuing the run and saving the ledger,
// the file is queued again under the same ID and the queue, which already
// holds it, leaves it alone.
func WatchRunID(dagName, path string, mark schedulerstate.FileMark) string {
	sum := sha256.Sum256([]byte(dagName + "\x00" + path + "\x00" + strconv.FormatInt(mark.Size, 10) + "\x00" + strconv.FormatInt(mark.ModTime.UnixNano(), 10)))
	return "file-" + hex.EncodeToString(sum[:16])
}

type folderWatcherConfig struct {
	Entries     func() []DAGEntry
	IsSuspended IsSuspendedFunc
	Enqueue     WatchEnqueueFunc
	// Store keeps the ledgers across restarts; nil keeps them in memory.
	Store schedulerstate.WatchStore
	Clock Clock
}

// folderWatcher reads the folders DAGs watch and starts their runs.
type folderWatcher struct {
	cfg folderWatcherConfig

	mu      sync.Mutex
	ledgers map[string]schedulerstate.WatchLedger
	// pending are the files seen once, by DAG, waiting to hold still.
	pending map[string]map[string]schedulerstate.FileMark
	// readAt is when each DAG's folder was last read.
	readAt map[string]time.Time
	// trouble is the last error reading each DAG's folder, logged once.
	trouble map[string]string
}

func newFolderWatcher(cfg folderWatcherConfig) *folderWatcher {
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	return &folderWatcher{
		cfg:     cfg,
		ledgers: map[string]schedulerstate.WatchLedger{},
		pending: map[string]map[string]schedulerstate.FileMark{},
		readAt:  map[string]time.Time{},
		trouble: map[string]string{},
	}
}

// Start reads the watched folders until ctx ends.
func (w *folderWatcher) Start(ctx context.Context) {
	if w.cfg.Store != nil {
		ledgers, err := w.cfg.Store.LoadWatch(ctx)
		if err != nil {
			logger.Error(ctx, "Failed to load watched folders; watching them as new", tag.Error(err))
		} else {
			w.ledgers = ledgers
		}
	}
	ticker := time.NewTicker(watchTick)
	defer ticker.Stop()
	for {
		w.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// tick reads every watched folder that is due.
func (w *folderWatcher) tick(ctx context.Context) {
	now := w.cfg.Clock()
	changed := false
	for _, entry := range w.cfg.Entries() {
		if entry.DAG == nil || entry.DAG.Watch == nil {
			continue
		}
		if last, ok := w.readAt[entry.DAG.Name]; ok && now.Sub(last) < entry.DAG.Watch.Interval {
			continue
		}
		w.readAt[entry.DAG.Name] = now
		if w.read(ctx, entry) {
			changed = true
		}
	}
	// A DAG the entries leave out keeps its record: it may be back after an
	// edit that did not load, and a file that arrived meanwhile must still
	// run then. A record of a DAG that is gone for good costs a few bytes.
	if changed && w.cfg.Store != nil {
		if err := w.cfg.Store.SaveWatch(ctx, w.ledgers); err != nil {
			logger.Error(ctx, "Failed to save watched folders", tag.Error(err))
		}
	}
}

// read reads one DAG's folder and starts the runs of the files that
// arrived. It reports whether the ledger changed.
func (w *folderWatcher) read(ctx context.Context, entry DAGEntry) bool {
	dag := entry.DAG
	cfg := dag.Watch
	dir, err := watchDir(cfg.Path)
	if err == nil {
		var found map[string]schedulerstate.FileMark
		found, err = readFolder(dir, cfg.Pattern)
		if err == nil {
			delete(w.trouble, dag.Name)
			return w.settle(ctx, entry, dir, found)
		}
	}
	// The folder may be a share that is off for a while; say so once.
	if w.trouble[dag.Name] != err.Error() {
		w.trouble[dag.Name] = err.Error()
		logger.Error(ctx, "Cannot read the watched folder", tag.DAG(dag.Name), slog.String("path", cfg.Path), tag.Error(err))
	}
	return false
}

// settle compares what the folder holds with what the ledger remembers,
// starts a run for each file that has held still since the last reading,
// and reports whether the ledger changed.
func (w *folderWatcher) settle(ctx context.Context, entry DAGEntry, dir string, found map[string]schedulerstate.FileMark) bool {
	dag := entry.DAG
	w.mu.Lock()
	defer w.mu.Unlock()
	ledger, known := w.ledgers[dag.Name]
	if !known || ledger.Path != dir || ledger.Pattern != dag.Watch.Pattern {
		w.ledgers[dag.Name] = schedulerstate.WatchLedger{Path: dir, Pattern: dag.Watch.Pattern, Files: maps.Clone(found)}
		delete(w.pending, dag.Name)
		logger.Info(ctx, "Watching a folder for new files",
			tag.DAG(dag.Name), slog.String("path", dir), slog.String("pattern", dag.Watch.Pattern),
			slog.Int("filesAlreadyThere", len(found)))
		return true
	}
	pending := w.pending[dag.Name]
	if pending == nil {
		pending = map[string]schedulerstate.FileMark{}
		w.pending[dag.Name] = pending
	}
	changed := false
	if ledger.Files == nil {
		ledger.Files = map[string]schedulerstate.FileMark{}
	}
	for name, mark := range found {
		if ledger.Files[name].Same(mark) {
			continue
		}
		if !pending[name].Same(mark) {
			pending[name] = mark
			continue
		}
		if w.start(ctx, entry, filepath.Join(dir, name), mark) {
			ledger.Files[name] = mark
			delete(pending, name)
			changed = true
		}
	}
	for name := range ledger.Files {
		if _, present := found[name]; !present {
			delete(ledger.Files, name)
			changed = true
		}
	}
	for name := range pending {
		if _, present := found[name]; !present {
			delete(pending, name)
		}
	}
	w.ledgers[dag.Name] = ledger
	return changed
}

// start starts the run for a file that arrived, unless the DAG is
// suspended, and reports whether the file is now taken care of. A file
// whose run could not start, or that arrived while the DAG was suspended,
// is tried again at the next reading.
func (w *folderWatcher) start(ctx context.Context, entry DAGEntry, path string, mark schedulerstate.FileMark) bool {
	dag := entry.DAG
	suspended, err := isSuspendedDAG(ctx, w.cfg.IsSuspended, nil, dag, entry.DefinitionID)
	if err != nil {
		logger.Error(ctx, "Failed to check DAG suspension; leaving the file for the next reading", tag.DAG(dag.Name), tag.Error(err))
		return false
	}
	if suspended {
		return false
	}
	runID := WatchRunID(dag.Name, path, mark)
	if err := w.cfg.Enqueue(ctx, entry, runID, WatchRuntimeParams(path)); err != nil {
		logger.Error(ctx, "Failed to start the run for a file", tag.DAG(dag.Name), slog.String("file", path), tag.Error(err))
		return false
	}
	logger.Info(ctx, "A file arrived; its run is queued", tag.DAG(dag.Name), tag.RunID(runID), slog.String("file", path))
	return true
}

// watchDir makes the watched folder absolute: a leading ~ is the home
// directory.
func watchDir(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[1:])
	}
	return filepath.Abs(path)
}

// inProgressExtensions are the extensions of files still being written.
var inProgressExtensions = map[string]bool{".tmp": true, ".part": true, ".partial": true, ".crdownload": true, ".download": true}

// readFolder returns the files in a folder, by name, that a watch takes:
// regular files whose names match the pattern, leaving out hidden files,
// Office lock files, and files still being written.
func readFolder(dir, pattern string) (map[string]schedulerstate.FileMark, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	found := map[string]schedulerstate.FileMark{}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "~$") || inProgressExtensions[strings.ToLower(filepath.Ext(name))] {
			continue
		}
		if pattern != "" {
			if matched, err := filepath.Match(pattern, name); err != nil || !matched {
				continue
			}
		}
		info, err := entry.Info()
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		found[name] = schedulerstate.FileMark{Size: info.Size(), ModTime: info.ModTime()}
	}
	return found, nil
}
