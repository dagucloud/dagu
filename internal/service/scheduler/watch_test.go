// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// watchFixture drives a folder watcher by hand: each read is one call of
// tick with the clock past the interval.
type watchFixture struct {
	t         *testing.T
	dir       string
	watcher   *folderWatcher
	entry     DAGEntry
	now       time.Time
	suspended bool

	mu      sync.Mutex
	started []string
	runIDs  []string
	fail    bool
}

func newWatchFixture(t *testing.T, pattern string, watchStore schedulerstate.WatchStore) *watchFixture {
	t.Helper()
	f := &watchFixture{t: t, dir: t.TempDir(), now: time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)}
	f.entry = DAGEntry{DefinitionID: "inbox.yaml", DAG: &ir.DAG{Name: "inbox", Watch: &ir.WatchConfig{Path: f.dir, Pattern: pattern, Interval: 10 * time.Second}}}
	f.watcher = newFolderWatcher(folderWatcherConfig{
		Entries:     func() []DAGEntry { return []DAGEntry{f.entry} },
		IsSuspended: func(context.Context, string) (bool, error) { return f.suspended, nil },
		Enqueue: func(_ context.Context, entry DAGEntry, runID string, params string) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.fail {
				return assert.AnError
			}
			require.Equal(t, "inbox", entry.DAG.Name)
			f.started = append(f.started, params)
			f.runIDs = append(f.runIDs, runID)
			return nil
		},
		Store: watchStore,
		Clock: func() time.Time { return f.now },
	})
	return f
}

// read reads the folder once, as the next due tick does.
func (f *watchFixture) read() {
	f.t.Helper()
	f.now = f.now.Add(10 * time.Second)
	f.watcher.tick(context.Background())
}

func (f *watchFixture) write(name, content string, modTime time.Time) string {
	f.t.Helper()
	path := filepath.Join(f.dir, name)
	require.NoError(f.t, os.WriteFile(path, []byte(content), 0o600))
	require.NoError(f.t, os.Chtimes(path, modTime, modTime))
	return path
}

func (f *watchFixture) runs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.started...)
}

func TestWatchRunsAFileOnceItHoldsStill(t *testing.T) {
	t.Parallel()

	f := newWatchFixture(t, "*.pdf", nil)
	old := f.write("old.pdf", "already there", f.now.Add(-time.Hour))
	f.read()
	assert.Empty(t, f.runs(), "files already in the folder are left alone")

	path := f.write("invoice.pdf", "new", f.now)
	f.read()
	assert.Empty(t, f.runs(), "a file seen once may still be being written")
	f.read()
	assert.Equal(t, []string{WatchRuntimeParams(path)}, f.runs(), "a file that held still starts one run")
	f.read()
	f.read()
	assert.Len(t, f.runs(), 1, "a file runs once")

	f.write("invoice.pdf", "new content", f.now.Add(time.Minute))
	f.read()
	f.read()
	assert.Len(t, f.runs(), 2, "a replaced file runs again")

	require.NoError(t, os.Remove(old))
	f.read()
	f.write("old.pdf", "back", f.now)
	f.read()
	f.read()
	assert.Len(t, f.runs(), 3, "a file that went away and came back is new")
}

func TestWatchWaitsForAFileBeingWritten(t *testing.T) {
	t.Parallel()

	f := newWatchFixture(t, "", nil)
	f.read()
	f.write("export.csv", "part", f.now)
	f.read()
	f.write("export.csv", "part of the file", f.now.Add(5*time.Second))
	f.read()
	assert.Empty(t, f.runs(), "a file that grew is not done yet")
	f.read()
	assert.Len(t, f.runs(), 1)
}

func TestWatchLeavesOutFilesNotMeantForIt(t *testing.T) {
	t.Parallel()

	f := newWatchFixture(t, "*.pdf", nil)
	f.read()
	f.write("notes.txt", "x", f.now)
	f.write(".hidden.pdf", "x", f.now)
	f.write("~$lock.pdf", "x", f.now)
	f.write("invoice.pdf.part", "x", f.now)
	f.write("download.crdownload", "x", f.now)
	require.NoError(t, os.Mkdir(filepath.Join(f.dir, "folder.pdf"), 0o700))
	f.read()
	f.read()
	assert.Empty(t, f.runs())
}

func TestWatchHoldsFilesWhileSuspended(t *testing.T) {
	t.Parallel()

	f := newWatchFixture(t, "", nil)
	f.read()
	f.suspended = true
	path := f.write("a.txt", "x", f.now)
	f.read()
	f.read()
	f.read()
	assert.Empty(t, f.runs(), "nothing starts while the DAG is suspended")
	f.suspended = false
	f.read()
	assert.Equal(t, []string{WatchRuntimeParams(path)}, f.runs(), "the file runs once the DAG resumes")
}

func TestWatchRetriesAFileWhoseRunDidNotStart(t *testing.T) {
	t.Parallel()

	f := newWatchFixture(t, "", nil)
	f.read()
	f.fail = true
	f.write("a.txt", "x", f.now)
	f.read()
	f.read()
	assert.Empty(t, f.runs())
	f.fail = false
	f.read()
	assert.Len(t, f.runs(), 1)
}

func TestWatchRemembersFilesAcrossRestarts(t *testing.T) {
	t.Parallel()

	watchStore := store.NewWatchStateStore(testutil.NewMemoryBackend().Collection("watch"))
	f := newWatchFixture(t, "", watchStore)
	f.watcher.Start(contextDone())
	f.read()
	f.write("a.txt", "x", f.now)
	f.read()
	f.read()
	require.Len(t, f.runs(), 1)

	// A new watcher, as after a restart, loads the ledger: a.txt is known,
	// and the file that arrived meanwhile is new.
	arrived := f.write("b.txt", "x", f.now)
	again := newWatchFixture(t, "", watchStore)
	again.dir, again.entry.DAG.Watch.Path = f.dir, f.dir
	again.now = f.now
	again.watcher.Start(contextDone())
	again.read()
	again.read()
	assert.Equal(t, []string{WatchRuntimeParams(arrived)}, again.runs())
}

// A run is named after the file's version, so a file queued again after the
// ledger was lost, as when the process stopped before saving it, is the same
// run to the queue.
func TestWatchNamesARunAfterTheFileVersion(t *testing.T) {
	t.Parallel()

	f := newWatchFixture(t, "", nil)
	f.read()
	path := f.write("a.txt", "x", f.now)
	f.read()
	f.read()
	require.Len(t, f.runIDs, 1)
	assert.Regexp(t, `^file-[0-9a-f]{32}$`, f.runIDs[0])
	assert.NoError(t, ir.ValidateDAGRunID(f.runIDs[0]))

	// The ledger is lost; the same file version is queued under the same ID.
	f.watcher.ledgers = map[string]schedulerstate.WatchLedger{}
	f.read()
	f.read()
	f.read()
	require.Len(t, f.runIDs, 2)
	assert.Equal(t, f.runIDs[0], f.runIDs[1])

	f.write("a.txt", "changed", f.now.Add(time.Minute))
	f.read()
	f.read()
	require.Len(t, f.runIDs, 3)
	assert.NotEqual(t, f.runIDs[0], f.runIDs[2], "another version of the file is another run")
	assert.Equal(t, WatchRunID("inbox", path, schedulerstate.FileMark{Size: 7, ModTime: f.now.Add(40 * time.Second)}), f.runIDs[2])
}

// A DAG the entries leave out for a while, as one being edited is, keeps
// its record, so a file that arrives meanwhile runs once the DAG is back.
func TestWatchKeepsTheRecordWhileADAGIsAway(t *testing.T) {
	t.Parallel()

	f := newWatchFixture(t, "", nil)
	f.read()
	entry := f.entry
	f.entry = DAGEntry{}
	path := f.write("a.txt", "x", f.now)
	f.read()
	f.read()
	f.entry = entry
	f.read()
	f.read()
	assert.Equal(t, []string{WatchRuntimeParams(path)}, f.runs())
}

func TestWatchStartsOverWhenTheFolderChanges(t *testing.T) {
	t.Parallel()

	f := newWatchFixture(t, "", nil)
	f.read()
	other := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(other, "there.txt"), []byte("x"), 0o600))
	f.entry.DAG.Watch.Path = other
	f.read()
	f.read()
	f.read()
	assert.Empty(t, f.runs(), "the files a newly named folder holds are left alone")
}

func TestWatchRuntimeParamsQuoteThePath(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "請求書 2026.pdf")
	params := WatchRuntimeParams(path)
	assert.Equal(t, "WATCH_FILE="+strconv.Quote(path)+" WATCH_NAME="+strconv.Quote("請求書 2026.pdf"), params)
}

func TestReadFolderKeepsTheSizeAndTime(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	at := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	path := filepath.Join(dir, "a.txt")
	require.NoError(t, os.WriteFile(path, []byte("hello"), 0o600))
	require.NoError(t, os.Chtimes(path, at, at))

	found, err := readFolder(dir, "")
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.True(t, found["a.txt"].Same(schedulerstate.FileMark{Size: 5, ModTime: at}), "%+v", found["a.txt"])

	_, err = readFolder(filepath.Join(dir, "missing"), "")
	require.Error(t, err)
}

// contextDone is a context that has already ended, so Start loads the
// ledger and returns without waiting on its ticker.
func contextDone() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
