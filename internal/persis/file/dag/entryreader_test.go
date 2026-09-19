// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"context"
	"github.com/dagucloud/dagu/v2/internal/cmn/filenotify"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	"os"
	"path/filepath"
	"runtime"
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
			write := func(body string) {
				t.Helper()
				require.NoError(t, os.MkdirAll(filepath.Dir(base), 0750))
				require.NoError(t, os.WriteFile(base, []byte(body), 0600))
			}
			write("queue: pool\n")
			expect(persis.DAGChangeUpdated, "pool")
			write("queue: [")
			expect(persis.DAGChangeDeleted, "")
			write("queue: recovered\n")
			expect(persis.DAGChangeAdded, "recovered")
			// The reader may still hold base open on Windows while it reacts to
			// the previous write; retry destructive ops until it releases it.
			retryFileOp := func(op func() error) {
				t.Helper()
				require.Eventually(t, func() bool { return op() == nil },
					3*time.Second, 10*time.Millisecond)
			}
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
			expect := func(queue string) {
				t.Helper()
				timer := time.NewTimer(time.Second)
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
				case <-time.After(time.Second):
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
