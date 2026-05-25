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

	"github.com/dagucloud/dagu/internal/core"
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
		registry:  make(map[string]*core.DAG),
		dagSource: newDAGFileSource(dir),
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
	er.registry["update-test.yaml"] = &core.DAG{Name: "update-test"}

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
	er.registry["remove-test.yaml"] = &core.DAG{Name: "remove-test"}

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

	er.registry["replace-test.yaml"] = &core.DAG{Name: "replace-test"}
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
	er.registry["rename-test.yaml"] = &core.DAG{Name: "old-name"}

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
