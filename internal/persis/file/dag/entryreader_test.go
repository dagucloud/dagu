// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/dagucloud/dagu/v2/internal/cmn/filenotify"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSendEvent_UnblocksOnQuit verifies shutdown unblocks a pending event send.
func TestSendEvent_UnblocksOnQuit(t *testing.T) {
	t.Parallel()

	er := &entryReaderImpl{
		events: make(chan persis.DAGChangeEvent), // unbuffered
		quit:   make(chan struct{}),
	}

	done := make(chan struct{})
	go func() {
		er.sendEvent(context.Background(), persis.DAGChangeEvent{
			Type:     persis.DAGChangeAdded,
			DAGEntry: persis.DAGEntry{DAG: &ir.DAG{Name: "test"}},
		})
		close(done)
	}()

	// Yield to let sendEvent goroutine enter the blocking select
	runtime.Gosched()

	// Close quit — this should unblock sendEvent
	close(er.quit)

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("sendEvent did not unblock after quit was closed")
	}
}

// TestSendEvent_UnblocksOnContextCancel verifies context cancellation unblocks a pending event send.
func TestSendEvent_UnblocksOnContextCancel(t *testing.T) {
	t.Parallel()

	er := &entryReaderImpl{
		events: make(chan persis.DAGChangeEvent), // unbuffered
		quit:   make(chan struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		er.sendEvent(ctx, persis.DAGChangeEvent{
			Type:     persis.DAGChangeAdded,
			DAGEntry: persis.DAGEntry{DAG: &ir.DAG{Name: "test"}},
		})
		close(done)
	}()

	// Yield to let sendEvent goroutine enter the blocking select
	runtime.Gosched()

	// Cancel context — this should unblock sendEvent
	cancel()

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("sendEvent did not unblock after context cancel")
	}
}

// TestSendEvent_NilChannelReturnsImmediately verifies missing event wiring cannot block shutdown.
func TestSendEvent_NilChannelReturnsImmediately(t *testing.T) {
	t.Parallel()

	er := &entryReaderImpl{
		events: nil,
		quit:   make(chan struct{}),
	}

	done := make(chan struct{})
	go func() {
		er.sendEvent(context.Background(), persis.DAGChangeEvent{
			Type:     persis.DAGChangeAdded,
			DAGEntry: persis.DAGEntry{DAG: &ir.DAG{Name: "test"}},
		})
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("sendEvent blocked on nil channel")
	}
}

// writeDAGFile writes a minimal DAG fixture and returns its path.
func writeDAGFile(t *testing.T, dir, fileName, dagName string) string {
	t.Helper()
	content := "name: " + dagName + "\nsteps:\n  - name: step1\n    command: echo hello\n"
	path := filepath.Join(dir, fileName)
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	return path
}

// newTestEntryReader creates an entry reader wired like the production constructor.
func newTestEntryReader(dir string, events chan persis.DAGChangeEvent) *entryReaderImpl {
	return &entryReaderImpl{
		targetDir: dir,
		registry:  make(map[string]*ir.DAG),
		dagSource: newDAGFileSource(dir, nil),
		quit:      make(chan struct{}),
		events:    events,
	}
}

// TestHandleFSEvent_CreateAddsDAG verifies create events load DAG metadata and emit an add event.
func TestHandleFSEvent_CreateAddsDAG(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	events := make(chan persis.DAGChangeEvent, 10)
	er := newTestEntryReader(tmpDir, events)

	writeDAGFile(t, tmpDir, "create-test.yaml", "create-test")

	er.handleFSEvent(context.Background(), fsnotify.Event{
		Name: filepath.Join(tmpDir, "create-test.yaml"),
		Op:   fsnotify.Create,
	})

	// Verify registry was updated
	er.lock.Lock()
	dag, ok := er.registry["create-test.yaml"]
	er.lock.Unlock()
	require.True(t, ok, "DAG should be in registry")
	assert.Equal(t, "create-test", dag.Name)

	// Verify Added event was sent
	select {
	case event := <-events:
		assert.Equal(t, persis.DAGChangeAdded, event.Type)
		assert.Equal(t, "create-test", event.DAG.Name)
		assert.NotNil(t, event.DAG)
	case <-time.After(time.Second):
		t.Fatal("expected persis.DAGChangeAdded event")
	}
}

// TestHandleFSEvent_WriteUpdatesDAG verifies write events update existing registry entries.
func TestHandleFSEvent_WriteUpdatesDAG(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	events := make(chan persis.DAGChangeEvent, 10)
	er := newTestEntryReader(tmpDir, events)

	// Pre-populate registry with existing DAG
	er.registry["update-test.yaml"] = &ir.DAG{Name: "update-test"}

	// Write updated file
	writeDAGFile(t, tmpDir, "update-test.yaml", "update-test")

	er.handleFSEvent(context.Background(), fsnotify.Event{
		Name: filepath.Join(tmpDir, "update-test.yaml"),
		Op:   fsnotify.Write,
	})

	// Verify Updated event was sent (not Added, since it existed)
	select {
	case event := <-events:
		assert.Equal(t, persis.DAGChangeUpdated, event.Type)
		assert.Equal(t, "update-test", event.DAG.Name)
	case <-time.After(time.Second):
		t.Fatal("expected persis.DAGChangeUpdated event")
	}
}

// TestHandleFSEvent_RemoveDeletesDAG verifies remove events delete confirmed-absent DAG files.
func TestHandleFSEvent_RemoveDeletesDAG(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	events := make(chan persis.DAGChangeEvent, 10)
	er := newTestEntryReader(tmpDir, events)

	// Pre-populate registry
	er.registry["remove-test.yaml"] = &ir.DAG{Name: "remove-test"}

	er.handleFSEvent(context.Background(), fsnotify.Event{
		Name: filepath.Join(tmpDir, "remove-test.yaml"),
		Op:   fsnotify.Remove,
	})

	// Verify registry entry was deleted
	er.lock.Lock()
	_, ok := er.registry["remove-test.yaml"]
	er.lock.Unlock()
	assert.False(t, ok, "DAG should be removed from registry")

	// Verify Deleted event was sent
	select {
	case event := <-events:
		assert.Equal(t, persis.DAGChangeDeleted, event.Type)
		assert.Equal(t, "remove-test", event.DAG.Name)
	case <-time.After(time.Second):
		t.Fatal("expected persis.DAGChangeDeleted event")
	}
}

// TestHandleFSEvent_RemoveReloadsDAGWhenFileStillExists verifies remove events reload files that still exist after replacement.
func TestHandleFSEvent_RemoveReloadsDAGWhenFileStillExists(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	events := make(chan persis.DAGChangeEvent, 10)
	er := newTestEntryReader(tmpDir, events)

	er.registry["replace-test.yaml"] = &ir.DAG{Name: "replace-test"}
	writeDAGFile(t, tmpDir, "replace-test.yaml", "replace-test")

	er.handleFSEvent(context.Background(), fsnotify.Event{
		Name: filepath.Join(tmpDir, "replace-test.yaml"),
		Op:   fsnotify.Remove,
	})

	er.lock.Lock()
	dag, ok := er.registry["replace-test.yaml"]
	er.lock.Unlock()
	require.True(t, ok, "DAG should stay in registry when the source file still exists")
	assert.Equal(t, "replace-test", dag.Name)

	select {
	case event := <-events:
		assert.Equal(t, persis.DAGChangeUpdated, event.Type)
		assert.Equal(t, "replace-test", event.DAG.Name)
		assert.NotNil(t, event.DAG)
	case <-time.After(time.Second):
		t.Fatal("expected persis.DAGChangeUpdated event")
	}
}

// TestHandleFSEvent_NameChangeEmitsDeleteThenAdd verifies renamed DAG metadata emits delete before add.
func TestHandleFSEvent_NameChangeEmitsDeleteThenAdd(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	events := make(chan persis.DAGChangeEvent, 10)
	er := newTestEntryReader(tmpDir, events)

	// Pre-populate registry with old name
	er.registry["rename-test.yaml"] = &ir.DAG{Name: "old-name"}

	// Write file with new name
	writeDAGFile(t, tmpDir, "rename-test.yaml", "new-name")

	er.handleFSEvent(context.Background(), fsnotify.Event{
		Name: filepath.Join(tmpDir, "rename-test.yaml"),
		Op:   fsnotify.Write,
	})

	// Should get Delete for old name, then Added for new name
	var receivedEvents []persis.DAGChangeEvent
	timeout := time.After(time.Second)
	for len(receivedEvents) < 2 {
		select {
		case event := <-events:
			receivedEvents = append(receivedEvents, event)
		case <-timeout:
			t.Fatalf("expected 2 events, got %d", len(receivedEvents))
		}
	}

	require.Len(t, receivedEvents, 2)
	assert.Equal(t, persis.DAGChangeDeleted, receivedEvents[0].Type)
	assert.Equal(t, "old-name", receivedEvents[0].DAG.Name)
	assert.Equal(t, persis.DAGChangeAdded, receivedEvents[1].Type)
	assert.Equal(t, "new-name", receivedEvents[1].DAG.Name)
}

func TestRecursiveEntryReaderRecoversFromNameConflict(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "team"), 0750))
	firstPath := writeDAGFile(t, filepath.Join(tmpDir, "team"), "first.yaml", "shared-name")
	require.NoError(t, os.WriteFile(firstPath, []byte(`
name: shared-name
overlap_policy: latest
steps:
  - name: step1
    command: echo hello
`), 0600))

	store := newRepository(
		tmpDir,
		WithSkipExamples(true),
		WithRecursiveDiscovery(true),
	)
	events := make(chan persis.DAGChangeEvent, 10)
	er := NewFileEntryReader(tmpDir, store, true, "", "")
	er.events = events
	require.NoError(t, er.Init(context.Background()))
	t.Cleanup(er.Stop)

	require.Len(t, er.Entries(), 1)
	assert.Equal(t, ir.OverlapPolicyLatest, er.Entries()[0].DAG.OverlapPolicy)
	assert.Contains(t, er.watchedDirs, filepath.Join(tmpDir, "team"))

	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "other"), 0750))
	writeDAGFile(t, filepath.Join(tmpDir, "other"), "second.yaml", "shared-name")
	require.NoError(t, er.refreshRegistry(context.Background()))
	require.Empty(t, er.Entries())

	select {
	case event := <-events:
		assert.Equal(t, persis.DAGChangeDeleted, event.Type)
		assert.Equal(t, "shared-name", event.DAG.Name)
	case <-time.After(time.Second):
		t.Fatal("expected conflict to remove the scheduled DAG")
	}

	require.NoError(t, os.Remove(filepath.Join(tmpDir, "other", "second.yaml")))
	require.NoError(t, er.refreshRegistry(context.Background()))
	require.Len(t, er.Entries(), 1)

	select {
	case event := <-events:
		assert.Equal(t, persis.DAGChangeAdded, event.Type)
		assert.Equal(t, "shared-name", event.DAG.Name)
	case <-time.After(time.Second):
		t.Fatal("expected the resolved DAG conflict to recover")
	}
}

func TestEntryReaderExternalDAGFileSymlink(t *testing.T) {
	tests := []struct {
		name      string
		recursive bool
		symlinks  bool
		expected  int
	}{
		{name: "NonRecursiveDisabled"},
		{name: "NonRecursiveEnabled", symlinks: true, expected: 1},
		{name: "RecursiveDisabled", recursive: true},
		{name: "RecursiveEnabled", recursive: true, symlinks: true, expected: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			linkDir := root
			if tc.recursive {
				linkDir = filepath.Join(root, "nested")
				require.NoError(t, os.MkdirAll(linkDir, 0750))
			}
			targetDir := t.TempDir()
			targetPath := filepath.Join(targetDir, "resolved-target-name-that-is-not-the-entry.yaml")
			require.NoError(t, os.WriteFile(targetPath, []byte("steps:\n  - run: echo external\n"), 0644))
			if err := os.Symlink(targetPath, filepath.Join(linkDir, "external.yaml")); err != nil {
				t.Skipf("symlink creation is unavailable: %v", err)
			}

			store := newRepository(
				root,
				WithSkipExamples(true),
				WithRecursiveDiscovery(tc.recursive),
				WithSymlinks(tc.symlinks),
			)
			reader := NewFileEntryReader(root, store, tc.recursive, "", "")
			require.NoError(t, reader.Init(context.Background()))
			t.Cleanup(reader.Stop)

			entries := reader.Entries()
			require.Len(t, entries, tc.expected)
			if tc.expected == 1 {
				assert.Equal(t, "external", entries[0].DAG.Name)
			}
		})
	}
}

func TestRecursiveEntryReaderWatchesNewDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	store := newRepository(
		tmpDir,
		WithSkipExamples(true),
		WithRecursiveDiscovery(true),
	)
	events := make(chan persis.DAGChangeEvent, 10)
	er := NewFileEntryReader(tmpDir, store, true, "", "")
	er.events = events

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, er.Init(ctx))
	go er.Start(ctx)
	t.Cleanup(func() {
		cancel()
		er.Stop()
	})

	nestedDir := filepath.Join(tmpDir, "new", "nested")
	require.NoError(t, os.MkdirAll(nestedDir, 0750))
	writeDAGFile(t, nestedDir, "watched.yaml", "watched")

	select {
	case event := <-events:
		assert.Equal(t, persis.DAGChangeAdded, event.Type)
		assert.Equal(t, "watched", event.DAG.Name)
	case <-time.After(5 * time.Second):
		t.Fatal("expected a nested DAG add event")
	}
}

// TestEntryReaderWatchesMixedCaseYAML covers a DAG file with an upper-case
// .YAML extension end to end: it is discovered through the watcher's
// extension filter and reloaded after a write event.
func TestEntryReaderWatchesMixedCaseYAML(t *testing.T) {
	tmpDir := t.TempDir()
	store := newRepository(
		tmpDir,
		WithSkipExamples(true),
	)
	events := make(chan persis.DAGChangeEvent, 10)
	er := NewFileEntryReader(tmpDir, store, false, "", "")
	er.events = events

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, er.Init(ctx))
	go er.Start(ctx)
	t.Cleanup(func() {
		cancel()
		er.Stop()
	})

	expect := func(kind persis.DAGChangeType, name string) {
		t.Helper()
		timeout := time.NewTimer(5 * time.Second)
		defer timeout.Stop()
		for {
			select {
			case event := <-events:
				if event.Type == kind && event.DAG != nil && event.DAG.Name == name {
					return
				}
			case <-timeout.C:
				t.Fatalf("missing change %v for %q", kind, name)
			}
		}
	}

	writeDAGFile(t, tmpDir, "mixed.YAML", "mixed")
	expect(persis.DAGChangeAdded, "mixed")

	writeDAGFile(t, tmpDir, "mixed.YAML", "mixed")
	expect(persis.DAGChangeUpdated, "mixed")
}

func TestBaseWatchLifecycle(t *testing.T) {
	for _, scope := range []string{"global", "workspace"} {
		t.Run(scope, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := filepath.Join(root, "dags")
			require.NoError(t, os.MkdirAll(dir, 0750))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "scheduled.yaml"), []byte("labels: [workspace=ops]\nsteps:\n  - run: echo tick\n"), 0600))
			base := filepath.Join(root, "config", "base.yaml")
			global, workspaces := base, ""
			recursive := scope == "workspace"
			if recursive {
				global, workspaces = "", filepath.Join(root, "workspaces")
				base = filepath.Join(workspaces, "ops", "base.yaml")
			}
			repo := newRepository(dir, WithSkipExamples(true), WithBaseConfig(global), WithWorkspaceBaseConfigDir(workspaces), WithRecursiveDiscovery(recursive), WithFileCache(fileutil.NewCache[*ir.DAG]("watch", 2, 0)))
			reader := NewFileEntryReader(dir, repo, recursive, global, workspaces)
			require.NoError(t, reader.Init(t.Context()))
			require.Len(t, reader.Entries(), 1)
			done := make(chan struct{})
			go func() { defer close(done); reader.Start(t.Context()) }()
			t.Cleanup(func() { reader.Stop(); <-done })
			expect := func(kind persis.DAGChangeType, queue string) {
				t.Helper()
				timeout := time.NewTimer(3 * time.Second)
				defer timeout.Stop()
				for {
					select {
					case event := <-reader.Events():
						if event.Type == kind && (kind == persis.DAGChangeDeleted || event.DAG.ProcGroup() == queue) {
							return
						}
					case <-timeout.C:
						t.Fatalf("missing change %v with queue %q", kind, queue)
					}
				}
			}
			// On Windows a base file op fails while the reader still holds it
			// open (Go file handles do not share delete access), and a
			// just-deleted watched directory stays delete-pending until its
			// watch handle closes; retry until both settle.
			retryFileOp := func(op func() error) {
				t.Helper()
				require.EventuallyWithT(t, func(c *assert.CollectT) {
					assert.NoError(c, op())
				}, 5*time.Second, 10*time.Millisecond)
			}
			write := func(body string) {
				t.Helper()
				retryFileOp(func() error { return os.MkdirAll(filepath.Dir(base), 0750) })
				retryFileOp(func() error { return os.WriteFile(base, []byte(body), 0600) })
			}
			write("queue: pool\n")
			expect(persis.DAGChangeUpdated, "pool")
			write("queue: [")
			expect(persis.DAGChangeDeleted, "")
			write("queue: recovered\n")
			expect(persis.DAGChangeAdded, "recovered")
			replacement := filepath.Join(filepath.Dir(base), "replacement.tmp")
			require.NoError(t, os.WriteFile(replacement, []byte("queue: atomic\n"), 0600))
			retryFileOp(func() error { return os.Rename(replacement, base) })
			expect(persis.DAGChangeUpdated, "atomic")
			// Recreating a directory before the debounce expires must restore its watch.
			retryFileOp(func() error { return os.RemoveAll(filepath.Dir(base)) })
			write("queue: restored\n")
			expect(persis.DAGChangeUpdated, "restored")
			write("queue: final\n")
			expect(persis.DAGChangeUpdated, "final")
			retryFileOp(func() error { return os.Remove(base) })
			expect(persis.DAGChangeUpdated, "scheduled")
		})
	}
}

// calendarWatchFrom is a business day of calendar jp until a test makes it
// a holiday.
var calendarWatchFrom = time.Date(2027, 1, 11, 0, 0, 0, 0, time.UTC)

// startCalendarWatch starts a reader on a DAG that runs at 18:00 on the
// business days of calendar jp, read from calendars.
func startCalendarWatch(t *testing.T, calendars string, opts ...EntryReaderOption) *entryReaderImpl {
	t.Helper()
	dir := t.TempDir()
	dagFile := filepath.Join(dir, "payroll.yaml")
	require.NoError(t, os.WriteFile(dagFile,
		[]byte("calendar: jp\nschedule: \"0 18 * * *\"\nsteps:\n  - run: echo pay\n"), 0600))
	// An old file, as most DAGs are, so a refresh may reuse the loaded DAG.
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(dagFile, old, old))

	repo := newRepository(dir, WithSkipExamples(true), WithCalendars(spec.NewCalendarSource(calendars)),
		WithFileCache(fileutil.NewCache[*ir.DAG]("calendar-watch", 2, 0)))
	reader := NewFileEntryReader(dir, repo, false, "", "", append([]EntryReaderOption{WatchCalendars(calendars)}, opts...)...)
	require.NoError(t, reader.Init(t.Context()))
	entries := reader.Entries()
	require.Len(t, entries, 1)
	require.Equal(t, time.Date(2027, 1, 11, 18, 0, 0, 0, time.UTC), entries[0].DAG.NextRun(calendarWatchFrom))

	done := make(chan struct{})
	go func() { defer close(done); reader.Start(t.Context()) }()
	t.Cleanup(func() { reader.Stop(); <-done })
	return reader
}

// requireReplannedPastHoliday waits for the DAG update that follows
// calendarWatchFrom becoming a holiday.
func requireReplannedPastHoliday(t *testing.T, reader *entryReaderImpl) {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case event := <-reader.Events():
			if event.Type != persis.DAGChangeUpdated {
				continue
			}
			assert.Equal(t, time.Date(2027, 1, 12, 18, 0, 0, 0, time.UTC), event.DAG.NextRun(calendarWatchFrom))
			return
		case <-timeout.C:
			t.Fatal("missing update after the calendar changed")
		}
	}
}

// A holiday added to a calendar file must re-plan the DAGs that use it,
// although their own files and base configs are unchanged.
func TestCalendarWatch(t *testing.T) {
	t.Parallel()

	calendars := t.TempDir()
	calendarFile := filepath.Join(calendars, "jp.yaml")
	require.NoError(t, os.WriteFile(calendarFile, []byte("holidays: [2027-01-01]\n"), 0600))
	reader := startCalendarWatch(t, calendars)

	require.NoError(t, os.WriteFile(calendarFile, []byte("holidays: [2027-01-01, 2027-01-11]\n"), 0600))
	requireReplannedPastHoliday(t, reader)
}

// A calendar replaced with one of the same size and modification time must
// still re-plan the DAGs that use it. kqueue may report no event for a
// rename onto an existing name, so the periodic re-read triggers the
// refresh here.
func TestCalendarRefreshSameStampSwap(t *testing.T) {
	t.Parallel()

	for _, tc := range sameStampSwaps {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calendars, swap := tc.prepare(t, "holidays: [2027-01-01]\n", "holidays: [2027-01-11]\n")
			reader := startCalendarWatch(t, calendars, withResyncInterval(250*time.Millisecond))
			swap()
			requireReplannedPastHoliday(t, reader)
		})
	}
}

type observedWatcher struct {
	filenotify.FileWatcher
	added func(string)
}

func TestDAGWatchSymlinkRoot(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "physical")
	require.NoError(t, os.MkdirAll(target, 0750))
	dir := filepath.Join(root, "dags")
	if err := os.Symlink(target, dir); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlinks unavailable: %v", err)
		}
		require.NoError(t, err)
	}
	workspaces := workspace.BaseConfigDir(dir)
	require.NoError(t, os.MkdirAll(filepath.Join(workspaces, "ops"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(workspaces, "ops", "base.yaml"), []byte("queue: inherited\n"), 0600))
	path := writeDAGFile(t, dir, "scheduled.yaml", "scheduled")
	repo := newRepository(dir, WithSkipExamples(true), WithWorkspaceBaseConfigDir(workspaces))
	reader := NewFileEntryReader(dir, repo, false, "", workspaces)
	require.NoError(t, reader.Init(t.Context()))
	require.Len(t, reader.Entries(), 1)
	done := make(chan struct{})
	go func() { defer close(done); reader.Start(t.Context()) }()
	t.Cleanup(func() { reader.Stop(); <-done })
	require.NoError(t, os.WriteFile(path, []byte("queue: updated\nsteps:\n  - run: echo tick\n"), 0600))
	select {
	case event := <-reader.Events():
		require.Equal(t, persis.DAGChangeUpdated, event.Type)
		require.Equal(t, "updated", event.DAG.ProcGroup())
	case <-time.After(time.Second):
		t.Fatal("ordinary DAG update through symlink root was not observed")
	}
}

func (w observedWatcher) Add(path string) error {
	if err := w.FileWatcher.Add(path); err != nil {
		return err
	}
	w.added(path)
	return nil
}

func TestBaseWatchSymlink(t *testing.T) {
	for _, kind := range []string{"existing", "dangling", "parent"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := filepath.Join(root, "dags")
			require.NoError(t, os.MkdirAll(dir, 0750))
			writeDAGFile(t, dir, "scheduled.yaml", "scheduled")
			target := filepath.Join(root, "shared", "base.yaml")
			require.NoError(t, os.MkdirAll(filepath.Dir(target), 0750))
			if kind == "dangling" {
				target = filepath.Join(root, "shared", "missing", "base.yaml")
			} else {
				require.NoError(t, os.WriteFile(target, []byte("queue: old\n"), 0600))
			}
			base := filepath.Join(root, "config", "base.yaml")
			require.NoError(t, os.MkdirAll(filepath.Dir(base), 0750))
			link, destination := base, target
			if kind == "parent" {
				link, destination = filepath.Join(root, "config", "current"), filepath.Dir(target)
				base = filepath.Join(link, "base.yaml")
			}
			symlink := func(target, link string) {
				t.Helper()
				if err := os.Symlink(target, link); err != nil {
					if runtime.GOOS == "windows" {
						t.Skipf("symlinks unavailable: %v", err)
					}
					require.NoError(t, err)
				}
			}
			symlink(destination, link)
			repo := newRepository(dir, WithBaseConfig(base), WithSkipExamples(true))
			reader := NewFileEntryReader(dir, repo, false, base, "")
			require.NoError(t, reader.Init(t.Context()))
			watchAdded := make(chan struct{})
			targetParent := filepath.Dir(target)
			reader.watcher = observedWatcher{FileWatcher: reader.watcher, added: func(path string) {
				if path == targetParent {
					select {
					case <-watchAdded:
					default:
						close(watchAdded)
					}
				}
			}}
			done := make(chan struct{})
			go func() { defer close(done); reader.Start(t.Context()) }()
			t.Cleanup(func() { reader.Stop(); <-done })
			// A watch event on a loaded runner arrives late rather than never,
			// so the waits are bounded well above the second it usually takes.
			expect := func(queue string) {
				t.Helper()
				timer := time.NewTimer(10 * time.Second)
				defer timer.Stop()
				for {
					select {
					case event := <-reader.Events():
						if event.Type == persis.DAGChangeUpdated && event.DAG.ProcGroup() == queue {
							return
						}
					case <-timer.C:
						t.Fatalf("base symlink change to queue %q was not observed", queue)
					}
				}
			}
			if kind == "dangling" {
				require.NoError(t, os.MkdirAll(filepath.Dir(target), 0750))
				// Wait for the directory event to be processed before creating its file.
				select {
				case <-watchAdded:
				case <-time.After(10 * time.Second):
					t.Fatal("new symlink target directory was not observed")
				}
			}
			require.NoError(t, os.WriteFile(target, []byte("queue: updated\n"), 0600))
			expect("updated")
			require.NoError(t, os.Remove(target))
			expect("scheduled")
			require.NoError(t, os.WriteFile(target, []byte("queue: recovered\n"), 0600))
			expect("recovered")
			// kqueue can omit same-name symlink replacement events even with a parent watch.
			if kind == "parent" && (runtime.GOOS == "linux" || runtime.GOOS == "windows") {
				target = filepath.Join(root, "replacement", "base.yaml")
				require.NoError(t, os.MkdirAll(filepath.Dir(target), 0750))
				require.NoError(t, os.WriteFile(target, []byte("queue: replacement\n"), 0600))
				require.NoError(t, os.Remove(link))
				symlink(filepath.Dir(target), link)
				expect("replacement")
				require.NoError(t, os.WriteFile(target, []byte("queue: latest\n"), 0600))
				expect("latest")
			}
		})
	}
}

// expectEvent waits for an event of the given type for the named DAG.
func expectEvent(t *testing.T, events <-chan persis.DAGChangeEvent, kind persis.DAGChangeType, name string) {
	t.Helper()

	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case event := <-events:
			if event.Type == kind && event.DAG != nil && event.DAG.Name == name {
				return
			}
		case <-timeout.C:
			t.Fatalf("missing change %v for %q", kind, name)
		}
	}
}

// nextEvent returns the next event, failing when none arrives in time.
func nextEvent(t *testing.T, events <-chan persis.DAGChangeEvent) persis.DAGChangeEvent {
	t.Helper()

	select {
	case event := <-events:
		return event
	case <-time.After(5 * time.Second):
		t.Fatal("missing change event")
		return persis.DAGChangeEvent{}
	}
}

// expectNoEvent fails when any event arrives within the window.
func expectNoEvent(t *testing.T, events <-chan persis.DAGChangeEvent, window time.Duration) {
	t.Helper()

	select {
	case event := <-events:
		t.Fatalf("unexpected change %v for %q", event.Type, event.DAG.Name)
	case <-time.After(window):
	}
}

// countingStore records which DAGs had their metadata loaded.
type countingStore struct {
	*Store
	mu    sync.Mutex
	loads []string
}

func (s *countingStore) GetMetadata(ctx context.Context, id string) (*ir.DAG, error) {
	s.mu.Lock()
	s.loads = append(s.loads, definitionIDForFile(id))
	s.mu.Unlock()
	return s.Store.GetMetadata(ctx, id)
}

// takeLoads returns the DAGs loaded since the previous call, sorted.
func (s *countingStore) takeLoads() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	loads := s.loads
	s.loads = nil
	sort.Strings(loads)
	return loads
}

// newRefreshTestReader returns an initialized reader whose watcher never
// fires, so the test drives every refresh, and the store it loads through.
func newRefreshTestReader(t *testing.T, dir string, recursive bool, storeOpts ...Option) (*entryReaderImpl, chan persis.DAGChangeEvent, *countingStore) {
	t.Helper()

	opts := append([]Option{WithSkipExamples(true), WithRecursiveDiscovery(recursive)}, storeOpts...)
	store := &countingStore{Store: NewStore(dir, opts...)}
	repo := persis.NewDAGRepository(store, persis.DAGRepositoryOptions{
		BaseConfigPath:         store.baseConfigPath,
		WorkspaceBaseConfigDir: store.workspaceBaseConfigDir,
	})
	er := NewFileEntryReader(dir, repo, recursive, store.baseConfigPath, store.workspaceBaseConfigDir,
		withWatcherFactory(newSilentWatcher))
	events := make(chan persis.DAGChangeEvent, 64)
	er.events = events
	require.NoError(t, er.Init(context.Background()))
	t.Cleanup(er.Stop)
	return er, events, store
}

// ageFile moves a file's modification time an hour back. A refresh reloads a
// file modified just before its last load, since a write within the same
// timestamp tick would leave its stamp unchanged.
func ageFile(t *testing.T, path string) {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(path, old, old))
}

// TestEntryReaderRefreshEmitsNothingWhenUnchanged keeps a full refresh from
// reporting or reloading DAGs that did not change, whether they were loaded at
// start-up or applied from a watcher event.
func TestEntryReaderRefreshEmitsNothingWhenUnchanged(t *testing.T) {
	for _, recursive := range []bool{false, true} {
		name := "NonRecursive"
		if recursive {
			name = "Recursive"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			ageFile(t, writeDAGFile(t, dir, "existing.yaml", "existing"))
			er, events, store := newRefreshTestReader(t, dir, recursive)
			ctx := context.Background()

			path := writeDAGFile(t, dir, "applied.yaml", "applied")
			ageFile(t, path)
			if recursive {
				// Recursive readers apply every change through a full refresh.
				require.NoError(t, er.refreshRegistry(ctx))
			} else {
				er.handleFSEvent(ctx, fsnotify.Event{Name: path, Op: fsnotify.Create})
			}
			expectEvent(t, events, persis.DAGChangeAdded, "applied")
			store.takeLoads()

			require.NoError(t, er.refreshRegistry(ctx))
			expectNoEvent(t, events, 100*time.Millisecond)
			assert.Empty(t, store.takeLoads())
		})
	}
}

// TestEntryReaderRefreshLoadsOnlyChangedDAGs verifies a full refresh loads
// only the DAG files that changed and drops deleted ones without loading.
func TestEntryReaderRefreshLoadsOnlyChangedDAGs(t *testing.T) {
	for _, recursive := range []bool{false, true} {
		name := "NonRecursive"
		if recursive {
			name = "Recursive"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for _, id := range []string{"a", "b", "c"} {
				ageFile(t, writeDAGFile(t, dir, id+".yaml", id))
			}
			er, events, store := newRefreshTestReader(t, dir, recursive)
			store.takeLoads()

			edited := filepath.Join(dir, "b.yaml")
			require.NoError(t, os.WriteFile(edited, []byte("name: b\ndescription: edited\nsteps:\n  - run: echo hello\n"), 0600))
			ageFile(t, edited)
			require.NoError(t, os.Remove(filepath.Join(dir, "c.yaml")))
			require.NoError(t, er.refreshRegistry(context.Background()))

			expectEvent(t, events, persis.DAGChangeDeleted, "c")
			expectEvent(t, events, persis.DAGChangeUpdated, "b")
			assert.Equal(t, []string{"b"}, store.takeLoads())
		})
	}
}

// TestEntryReaderRefreshLoadsAllOnBaseConfigChange verifies a base config
// change loads every DAG again, since each one merges it.
func TestEntryReaderRefreshLoadsAllOnBaseConfigChange(t *testing.T) {
	for _, scope := range []string{"global", "workspace"} {
		t.Run(scope, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "dags")
			require.NoError(t, os.MkdirAll(dir, 0750))
			for _, id := range []string{"a", "b"} {
				path := filepath.Join(dir, id+".yaml")
				require.NoError(t, os.WriteFile(path, []byte("labels: [workspace=ops]\nsteps:\n  - run: echo tick\n"), 0600))
				ageFile(t, path)
			}
			recursive := scope == "workspace"
			base := filepath.Join(root, "base.yaml")
			opts := []Option{WithBaseConfig(base)}
			if recursive {
				workspaces := filepath.Join(root, "workspaces")
				base = filepath.Join(workspaces, "ops", "base.yaml")
				opts = []Option{WithWorkspaceBaseConfigDir(workspaces)}
			}
			require.NoError(t, os.MkdirAll(filepath.Dir(base), 0750))
			require.NoError(t, os.WriteFile(base, []byte("queue: before\n"), 0600))
			er, events, store := newRefreshTestReader(t, dir, recursive, opts...)
			ctx := context.Background()
			store.takeLoads()

			require.NoError(t, os.WriteFile(base, []byte("queue: after\n"), 0600))
			require.NoError(t, er.refreshRegistry(ctx))
			for i := range 2 {
				event := nextEvent(t, events)
				assert.Equal(t, persis.DAGChangeUpdated, event.Type)
				assert.Equal(t, "after", event.DAG.ProcGroup())
				// The refresh is one change: only its last event ends it.
				assert.Equal(t, i == 0, event.More)
			}
			assert.Equal(t, []string{"a", "b"}, store.takeLoads())

			require.NoError(t, er.refreshRegistry(ctx))
			expectNoEvent(t, events, 100*time.Millisecond)
			assert.Empty(t, store.takeLoads())
		})
	}
}

// TestEntryReaderRefreshReloadsDAGLoadedWithRemovedBaseConfig covers a base
// config created and removed again before a refresh runs. A DAG applied in
// between merged it and must be loaded again, although the base config state
// is back to what the reader saw at start-up.
func TestEntryReaderRefreshReloadsDAGLoadedWithRemovedBaseConfig(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "dags")
	require.NoError(t, os.MkdirAll(dir, 0750))
	path := filepath.Join(dir, "a.yaml")
	require.NoError(t, os.WriteFile(path, []byte("steps:\n  - run: echo tick\n"), 0600))
	ageFile(t, path)
	base := filepath.Join(root, "base.yaml")
	er, events, _ := newRefreshTestReader(t, dir, false, WithBaseConfig(base))
	ctx := context.Background()

	require.NoError(t, os.WriteFile(base, []byte("queue: transient\n"), 0600))
	// The watch loop records the base config state before it applies a DAG event.
	er.baseConfigState = describeBaseConfigStateSet(er.baseConfigPath, er.workspaceBaseConfigDir)
	er.handleFSEvent(ctx, fsnotify.Event{Name: path, Op: fsnotify.Write})
	require.Equal(t, "transient", nextEvent(t, events).DAG.ProcGroup())

	require.NoError(t, os.Remove(base))
	require.NoError(t, er.refreshRegistry(ctx))
	event := nextEvent(t, events)
	assert.Equal(t, persis.DAGChangeUpdated, event.Type)
	assert.Equal(t, "a", event.DAG.ProcGroup())
}

// TestEntryReaderReportsEachIssueOnce keeps the periodic refresh from logging
// the same excluded DAG as an error on every pass, while an issue that clears
// and comes back is reported again.
func TestEntryReaderReportsEachIssueOnce(t *testing.T) {
	for _, recursive := range []bool{false, true} {
		name := "NonRecursive"
		if recursive {
			name = "Recursive"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			er, _, _ := newRefreshTestReader(t, dir, recursive)
			var logs bytes.Buffer
			ctx := logger.WithFixedLogger(context.Background(), logger.NewLogger(
				logger.WithFormat("text"), logger.WithWriter(&logs),
			))
			reported := func() int {
				t.Helper()
				require.NoError(t, er.refreshRegistry(ctx))
				count := 0
				for line := range strings.SplitSeq(logs.String(), "\n") {
					if strings.Contains(line, "level=ERROR") && strings.Contains(line, "DAG excluded from scheduler") {
						count++
					}
				}
				return count
			}
			path := filepath.Join(dir, "broken.yaml")
			write := func(body string) {
				t.Helper()
				require.NoError(t, os.WriteFile(path, []byte(body), 0600))
			}

			write("steps: [\n")
			assert.Equal(t, 1, reported())
			assert.Equal(t, 1, reported(), "a persisting issue is not logged again")
			write("steps:\n  - run: echo fixed\n")
			assert.Equal(t, 1, reported())
			write("steps: [\n")
			assert.Equal(t, 2, reported(), "an issue that comes back is logged again")
		})
	}
}

// entrySchedule returns the first schedule expression of the registry entry id.
func entrySchedule(t *testing.T, er *entryReaderImpl, id string) string {
	t.Helper()
	for _, entry := range er.Entries() {
		if entry.DefinitionID == id {
			require.NotEmpty(t, entry.DAG.Schedule)
			return entry.DAG.Schedule[0].Expression
		}
	}
	t.Fatalf("no registry entry %q", id)
	return ""
}

// expectScheduleUpdate verifies a refresh both holds and reports the new
// schedule of id. The planner only learns about it through the event.
func expectScheduleUpdate(t *testing.T, er *entryReaderImpl, events <-chan persis.DAGChangeEvent, id, schedule string) {
	t.Helper()
	assert.Equal(t, schedule, entrySchedule(t, er, id))
	event := nextEvent(t, events)
	assert.Equal(t, persis.DAGChangeUpdated, event.Type)
	require.NotEmpty(t, event.DAG.Schedule)
	assert.Equal(t, schedule, event.DAG.Schedule[0].Expression)
}

// TestEntryReaderRefreshReloadsRecentlyModifiedDAG covers a same-size write
// within the timestamp tick of the previous load: the stamp does not change,
// so only the file's age tells the refresh to load it again. The store has no
// metadata cache here, since the cache's own stamp check misses such a write.
func TestEntryReaderRefreshReloadsRecentlyModifiedDAG(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.yaml")
	require.NoError(t, os.WriteFile(path, []byte("schedule: \"1 * * * *\"\nsteps:\n  - run: echo tick\n"), 0600))
	er, events, _ := newRefreshTestReader(t, dir, false)
	ctx := context.Background()
	info, err := os.Stat(path)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(path, []byte("schedule: \"2 * * * *\"\nsteps:\n  - run: echo tick\n"), 0600))
	require.NoError(t, os.Chtimes(path, info.ModTime(), info.ModTime()))
	require.NoError(t, er.refreshRegistry(ctx))
	expectScheduleUpdate(t, er, events, "a", "2 * * * *")

	require.NoError(t, er.refreshRegistry(ctx))
	expectNoEvent(t, events, 100*time.Millisecond)
}

// TestEntryReaderRefreshFollowsSymlinkRetarget covers a DAG symlink pointed at
// another file with the same size and modification time: the stamp read
// through the link is unchanged, but the DAG now comes from the new target.
func TestEntryReaderRefreshFollowsSymlinkRetarget(t *testing.T) {
	dir := t.TempDir()
	targets := filepath.Join(dir, "targets")
	require.NoError(t, os.MkdirAll(targets, 0750))
	mtime := time.Now().Add(-time.Hour)
	for i, schedule := range []string{"1 * * * *", "2 * * * *"} {
		path := filepath.Join(targets, fmt.Sprintf("v%d.yaml", i+1))
		require.NoError(t, os.WriteFile(path, []byte("schedule: \""+schedule+"\"\nsteps:\n  - run: echo tick\n"), 0600))
		require.NoError(t, os.Chtimes(path, mtime, mtime))
	}
	link := filepath.Join(dir, "a.yaml")
	if err := os.Symlink(filepath.Join(targets, "v1.yaml"), link); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	er, events, _ := newRefreshTestReader(t, dir, false,
		WithFileCache(fileutil.NewCache[*ir.DAG]("dag_definition", 16, time.Hour)))
	ctx := context.Background()
	require.Equal(t, "1 * * * *", entrySchedule(t, er, "a"))

	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Symlink(filepath.Join(targets, "v2.yaml"), link))
	require.NoError(t, er.refreshRegistry(ctx))
	expectScheduleUpdate(t, er, events, "a", "2 * * * *")

	require.NoError(t, er.refreshRegistry(ctx))
	expectNoEvent(t, events, 100*time.Millisecond)
}

// silentWatcher models a watcher that never delivers events, like inotify on a
// network file system when another host writes the files.
type silentWatcher struct {
	events chan fsnotify.Event
	errors chan error
}

func newSilentWatcher() filenotify.FileWatcher {
	return &silentWatcher{events: make(chan fsnotify.Event), errors: make(chan error)}
}

func (w *silentWatcher) Events() <-chan fsnotify.Event { return w.events }
func (w *silentWatcher) Errors() <-chan error          { return w.errors }
func (w *silentWatcher) Add(string) error              { return nil }
func (w *silentWatcher) Remove(string) error           { return nil }
func (w *silentWatcher) Close() error                  { return nil }

// startSilentEntryReader starts a reader whose watcher never fires and returns
// its event channel. DAG file symlinks may point outside dir.
func startSilentEntryReader(t *testing.T, dir string, recursive bool, opts ...EntryReaderOption) (*entryReaderImpl, chan persis.DAGChangeEvent) {
	t.Helper()

	repo := newRepository(dir, WithSkipExamples(true), WithRecursiveDiscovery(recursive), WithSymlinks(true))
	return startSilentEntryReaderWith(t, dir, repo, recursive, opts...)
}

// startSilentEntryReaderWith is startSilentEntryReader loading through repo.
func startSilentEntryReaderWith(t *testing.T, dir string, repo *persis.DAGRepository, recursive bool, opts ...EntryReaderOption) (*entryReaderImpl, chan persis.DAGChangeEvent) {
	t.Helper()

	opts = append([]EntryReaderOption{withWatcherFactory(newSilentWatcher)}, opts...)
	er := NewFileEntryReader(dir, repo, recursive, "", "", opts...)
	events := make(chan persis.DAGChangeEvent, 64)
	er.events = events

	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, er.Init(ctx))
	go er.Start(ctx)
	t.Cleanup(func() {
		cancel()
		er.Stop()
	})
	return er, events
}

// TestEntryReaderResyncSeesChangesWithoutWatcherEvents covers DAG files that
// another host creates, edits, renames and deletes on a shared volume: the
// watcher stays silent, so only the periodic resync can report them.
func TestEntryReaderResyncSeesChangesWithoutWatcherEvents(t *testing.T) {
	for _, recursive := range []bool{false, true} {
		name := "NonRecursive"
		if recursive {
			name = "Recursive"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeDAGFile(t, dir, "existing.yaml", "existing")
			_, events := startSilentEntryReader(t, dir, recursive, withResyncInterval(100*time.Millisecond))

			writeDAGFile(t, dir, "remote.yaml", "remote")
			expectEvent(t, events, persis.DAGChangeAdded, "remote")

			writeDAGFile(t, dir, "remote.yaml", "remote-edited-by-another-host")
			expectEvent(t, events, persis.DAGChangeDeleted, "remote")
			expectEvent(t, events, persis.DAGChangeAdded, "remote-edited-by-another-host")

			require.NoError(t, os.Rename(filepath.Join(dir, "remote.yaml"), filepath.Join(dir, "moved.yaml")))
			expectEvent(t, events, persis.DAGChangeDeleted, "remote-edited-by-another-host")
			expectEvent(t, events, persis.DAGChangeAdded, "remote-edited-by-another-host")

			require.NoError(t, os.Remove(filepath.Join(dir, "existing.yaml")))
			expectEvent(t, events, persis.DAGChangeDeleted, "existing")
		})
	}
}

// TestEntryReaderResyncDisabled keeps the resync opt-out working.
func TestEntryReaderResyncDisabled(t *testing.T) {
	dir := t.TempDir()
	_, events := startSilentEntryReader(t, dir, false, withResyncInterval(0))

	writeDAGFile(t, dir, "remote.yaml", "remote")
	expectNoEvent(t, events, 300*time.Millisecond)
}

// TestEntryReaderPollsSymlinkTargets covers DAG symlinks managed outside the
// DAG directory, such as by nix home-manager. The watcher stays silent, as
// kqueue does when a link is re-pointed under the same name and every watcher
// does when a target outside the watched directories changes, and the resync
// is off, so only the symlink poll can report the changes.
func TestEntryReaderPollsSymlinkTargets(t *testing.T) {
	for _, recursive := range []bool{false, true} {
		name := "NonRecursive"
		if recursive {
			name = "Recursive"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			targets := t.TempDir()
			writeTarget := func(fileName, content string) string {
				t.Helper()
				path := filepath.Join(targets, fileName)
				require.NoError(t, os.WriteFile(path, []byte(content), 0600))
				return path
			}
			link := filepath.Join(dir, "a.yaml")
			repoint := func(target string) { repointSymlink(t, link, target) }
			if err := os.Symlink(writeTarget("v1.yaml", scheduledDAG("1 * * * *")), link); err != nil {
				t.Skipf("symlink creation is unavailable: %v", err)
			}
			er, events := startSilentEntryReader(t, dir, recursive,
				withResyncInterval(0), withSymlinkPollInterval(50*time.Millisecond))
			expectSchedule := func(kind persis.DAGChangeType, schedule string) {
				t.Helper()
				event := nextEvent(t, events)
				assert.Equal(t, kind, event.Type)
				require.NotEmpty(t, event.DAG.Schedule)
				assert.Equal(t, schedule, event.DAG.Schedule[0].Expression)
				assert.Equal(t, schedule, entrySchedule(t, er, "a"))
			}

			v2 := writeTarget("v2.yaml", scheduledDAG("2 * * * *"))
			repoint(v2)
			expectSchedule(persis.DAGChangeUpdated, "2 * * * *")

			writeTarget("v2.yaml", scheduledDAG("30 * * * *"))
			expectSchedule(persis.DAGChangeUpdated, "30 * * * *")

			// A target that fails to load must not stop the link being polled.
			repoint(writeTarget("broken.yaml", "steps: [\n"))
			expectEvent(t, events, persis.DAGChangeDeleted, "a")
			repoint(v2)
			expectSchedule(persis.DAGChangeAdded, "30 * * * *")

			require.NoError(t, os.Remove(v2))
			expectEvent(t, events, persis.DAGChangeDeleted, "a")
		})
	}
}

// TestEntryReaderRetriesFailedSymlinkRefresh keeps a re-pointed link pending
// while the refresh it starts fails, so a later poll still reloads the DAG.
func TestEntryReaderRetriesFailedSymlinkRefresh(t *testing.T) {
	dir, targets := t.TempDir(), t.TempDir()
	v1 := filepath.Join(targets, "v1.yaml")
	v2 := filepath.Join(targets, "v2.yaml")
	require.NoError(t, os.WriteFile(v1, []byte(scheduledDAG("1 * * * *")), 0600))
	require.NoError(t, os.WriteFile(v2, []byte(scheduledDAG("2 * * * *")), 0600))
	link := filepath.Join(dir, "a.yaml")
	if err := os.Symlink(v1, link); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	store := &failingCatalogStore{
		Store:  NewStore(dir, WithSkipExamples(true), WithSymlinks(true)),
		failed: make(chan struct{}, 1),
	}
	repo := persis.NewDAGRepository(store, persis.DAGRepositoryOptions{})
	er, events := startSilentEntryReaderWith(t, dir, repo, false,
		withResyncInterval(0), withSymlinkPollInterval(50*time.Millisecond))

	store.failing.Store(true)
	repointSymlink(t, link, v2)
	select {
	case <-store.failed:
	case <-time.After(5 * time.Second):
		t.Fatal("the re-pointed link did not start a refresh")
	}
	store.failing.Store(false)

	event := nextEvent(t, events)
	assert.Equal(t, persis.DAGChangeUpdated, event.Type)
	require.NotEmpty(t, event.DAG.Schedule)
	assert.Equal(t, "2 * * * *", event.DAG.Schedule[0].Expression)
	assert.Equal(t, "2 * * * *", entrySchedule(t, er, "a"))
}

// failingCatalogStore fails catalog reads while failing is set, as a store
// does on a transient I/O error, and signals failed on each such read.
type failingCatalogStore struct {
	*Store
	failing atomic.Bool
	failed  chan struct{}
}

func (s *failingCatalogStore) Catalog(ctx context.Context) (persis.DAGCatalog, error) {
	if s.failing.Load() {
		select {
		case s.failed <- struct{}{}:
		default:
		}
		return persis.DAGCatalog{}, errors.New("catalog unavailable")
	}
	return s.Store.Catalog(ctx)
}

// scheduledDAG returns a DAG definition that runs on schedule.
func scheduledDAG(schedule string) string {
	return "schedule: \"" + schedule + "\"\nsteps:\n  - run: echo tick\n"
}

// repointSymlink replaces link with a symlink to target in a single rename,
// as nix home-manager does on a switch.
func repointSymlink(t *testing.T, link, target string) {
	t.Helper()
	staged := link + ".staged"
	require.NoError(t, os.Symlink(target, staged))
	require.NoError(t, os.Rename(staged, link))
}
