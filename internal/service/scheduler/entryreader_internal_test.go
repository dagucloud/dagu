// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	filedag "github.com/dagucloud/dagu/v2/internal/persis/file/dag"
	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSendEvent_UnblocksOnQuit verifies shutdown unblocks a pending event send.
func TestSendEvent_UnblocksOnQuit(t *testing.T) {
	t.Parallel()

	er := &entryReaderImpl{
		events: make(chan DAGChangeEvent), // unbuffered
		quit:   make(chan struct{}),
	}

	done := make(chan struct{})
	go func() {
		er.sendEvent(context.Background(), DAGChangeEvent{
			Type:    DAGChangeAdded,
			DAGName: "test",
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
		events: make(chan DAGChangeEvent), // unbuffered
		quit:   make(chan struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		er.sendEvent(ctx, DAGChangeEvent{
			Type:    DAGChangeAdded,
			DAGName: "test",
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
		er.sendEvent(context.Background(), DAGChangeEvent{
			Type:    DAGChangeAdded,
			DAGName: "test",
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
func newTestEntryReader(dir string, events chan DAGChangeEvent) *entryReaderImpl {
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
	events := make(chan DAGChangeEvent, 10)
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
		assert.Equal(t, DAGChangeAdded, event.Type)
		assert.Equal(t, "create-test", event.DAGName)
		assert.NotNil(t, event.DAG)
	case <-time.After(time.Second):
		t.Fatal("expected DAGChangeAdded event")
	}
}

// TestHandleFSEvent_WriteUpdatesDAG verifies write events update existing registry entries.
func TestHandleFSEvent_WriteUpdatesDAG(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	events := make(chan DAGChangeEvent, 10)
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
		assert.Equal(t, DAGChangeUpdated, event.Type)
		assert.Equal(t, "update-test", event.DAGName)
	case <-time.After(time.Second):
		t.Fatal("expected DAGChangeUpdated event")
	}
}

// TestHandleFSEvent_RemoveDeletesDAG verifies remove events delete confirmed-absent DAG files.
func TestHandleFSEvent_RemoveDeletesDAG(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	events := make(chan DAGChangeEvent, 10)
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
		assert.Equal(t, DAGChangeDeleted, event.Type)
		assert.Equal(t, "remove-test", event.DAGName)
	case <-time.After(time.Second):
		t.Fatal("expected DAGChangeDeleted event")
	}
}

// TestHandleFSEvent_RemoveReloadsDAGWhenFileStillExists verifies remove events reload files that still exist after replacement.
func TestHandleFSEvent_RemoveReloadsDAGWhenFileStillExists(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	events := make(chan DAGChangeEvent, 10)
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
		assert.Equal(t, DAGChangeUpdated, event.Type)
		assert.Equal(t, "replace-test", event.DAGName)
		assert.NotNil(t, event.DAG)
	case <-time.After(time.Second):
		t.Fatal("expected DAGChangeUpdated event")
	}
}

// TestHandleFSEvent_NameChangeEmitsDeleteThenAdd verifies renamed DAG metadata emits delete before add.
func TestHandleFSEvent_NameChangeEmitsDeleteThenAdd(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	events := make(chan DAGChangeEvent, 10)
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
	var receivedEvents []DAGChangeEvent
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
	assert.Equal(t, DAGChangeDeleted, receivedEvents[0].Type)
	assert.Equal(t, "old-name", receivedEvents[0].DAGName)
	assert.Equal(t, DAGChangeAdded, receivedEvents[1].Type)
	assert.Equal(t, "new-name", receivedEvents[1].DAGName)
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

	store := filedag.New(
		tmpDir,
		filedag.WithSkipExamples(true),
		filedag.WithRecursiveDiscovery(true),
	)
	events := make(chan DAGChangeEvent, 10)
	er := NewEntryReader(tmpDir, store, true).(*entryReaderImpl)
	er.events = events
	require.NoError(t, er.Init(context.Background()))
	t.Cleanup(er.Stop)

	require.Len(t, er.DAGs(), 1)
	assert.Equal(t, ir.OverlapPolicyLatest, er.DAGs()[0].OverlapPolicy)
	assert.Contains(t, er.watchedDirs, filepath.Join(tmpDir, "team"))

	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "other"), 0750))
	writeDAGFile(t, filepath.Join(tmpDir, "other"), "second.yaml", "shared-name")
	require.NoError(t, er.refreshRecursive(context.Background()))
	require.Empty(t, er.DAGs())

	select {
	case event := <-events:
		assert.Equal(t, DAGChangeDeleted, event.Type)
		assert.Equal(t, "shared-name", event.DAGName)
	case <-time.After(time.Second):
		t.Fatal("expected conflict to remove the scheduled DAG")
	}

	require.NoError(t, os.Remove(filepath.Join(tmpDir, "other", "second.yaml")))
	require.NoError(t, er.refreshRecursive(context.Background()))
	require.Len(t, er.DAGs(), 1)

	select {
	case event := <-events:
		assert.Equal(t, DAGChangeAdded, event.Type)
		assert.Equal(t, "shared-name", event.DAGName)
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
			targetPath := writeDAGFile(t, targetDir, "source.yaml", "external")
			if err := os.Symlink(targetPath, filepath.Join(linkDir, "external.yaml")); err != nil {
				t.Skipf("symlink creation is unavailable: %v", err)
			}

			store := filedag.New(
				root,
				filedag.WithSkipExamples(true),
				filedag.WithRecursiveDiscovery(tc.recursive),
				filedag.WithSymlinks(tc.symlinks),
			)
			reader := NewEntryReader(root, store, tc.recursive)
			require.NoError(t, reader.Init(context.Background()))
			t.Cleanup(reader.Stop)

			dags := reader.DAGs()
			require.Len(t, dags, tc.expected)
			if tc.expected == 1 {
				assert.Equal(t, "external", dags[0].Name)
			}
		})
	}
}

func TestRecursiveEntryReaderWatchesNewDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	store := filedag.New(
		tmpDir,
		filedag.WithSkipExamples(true),
		filedag.WithRecursiveDiscovery(true),
	)
	events := make(chan DAGChangeEvent, 10)
	er := NewEntryReader(tmpDir, store, true).(*entryReaderImpl)
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
		assert.Equal(t, DAGChangeAdded, event.Type)
		assert.Equal(t, "watched", event.DAGName)
	case <-time.After(5 * time.Second):
		t.Fatal("expected a nested DAG add event")
	}
}
