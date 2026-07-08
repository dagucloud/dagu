// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sse

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/cmn/config"
)

func TestDirectoryWatcherStopIsIdempotent(t *testing.T) {
	watcher := &directoryWatcher{
		done: make(chan struct{}),
	}

	require.NotPanics(t, func() {
		watcher.Stop()
		watcher.Stop()
	})
}

func TestAppStreamServiceShutdownIsIdempotent(t *testing.T) {
	service := &AppStreamService{
		cancel: func() {},
		watchers: []appWatcher{
			&directoryWatcher{done: make(chan struct{})},
		},
	}

	require.NotPanics(t, func() {
		service.Shutdown()
		service.Shutdown()
	})
}

func TestNewAppStreamServiceDoesNotCreateDAGRunsDir(t *testing.T) {
	root := t.TempDir()
	dagRunsDir := filepath.Join(root, "dag-runs")

	service, err := NewAppStreamService(AppStreamConfig{
		Paths: config.PathsConfig{
			SuspendFlagsDir: filepath.Join(root, "suspend"),
			DAGRunsDir:      dagRunsDir,
			QueueDir:        filepath.Join(root, "queue"),
		},
	})

	require.NoError(t, err)
	t.Cleanup(service.Shutdown)
	assert.NoDirExists(t, dagRunsDir)
}

func TestRecursiveWatchPathsIncludesNestedDirs(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "a", "b"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a", "b", "doc.md"), []byte("# doc\n"), 0600))

	paths, err := recursiveWatchPaths(root)
	require.NoError(t, err)

	assert.Contains(t, paths, root)
	assert.Contains(t, paths, filepath.Join(root, "a"))
	assert.Contains(t, paths, filepath.Join(root, "a", "b"))
}

func TestSnapshotMarkdownFilesIncludesOnlyMarkdown(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "nested"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "top.md"), []byte("# top\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "notes.txt"), []byte("ignore\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "upper.MD"), []byte("ignore\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "nested", "page.md"), []byte("# page\n"), 0600))

	files, err := snapshotMarkdownFiles(root)
	require.NoError(t, err)

	assert.Contains(t, files, "top.md")
	assert.Contains(t, files, "nested/page.md")
	assert.NotContains(t, files, "notes.txt")
	assert.NotContains(t, files, "upper.MD")
}

func TestMarkdownPollingWatcherEmitsOnlyMarkdownEvents(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "existing.md"), []byte("old\n"), 0600))

	var events []fsnotify.Event
	watcher := newMarkdownPollingWatcher(
		root,
		false,
		time.Hour,
		func(_ string, relPath string, op fsnotify.Op) {
			events = append(events, fsnotify.Event{Name: relPath, Op: op})
		},
		func(reason string) {
			t.Fatalf("unexpected reset: %s", reason)
		},
	)
	require.NoError(t, watcher.Start(context.Background()))
	t.Cleanup(watcher.Stop)

	require.NoError(t, os.WriteFile(filepath.Join(root, "ignore.txt"), []byte("ignore\n"), 0600))
	watcher.check()
	assert.Empty(t, events)

	require.NoError(t, os.WriteFile(filepath.Join(root, "created.md"), []byte("new\n"), 0600))
	watcher.check()
	require.Len(t, events, 1)
	assert.Equal(t, "created.md", events[0].Name)
	assert.True(t, events[0].Op&fsnotify.Create != 0)
	events = nil

	require.NoError(t, os.WriteFile(filepath.Join(root, "existing.md"), []byte("changed\n"), 0600))
	watcher.check()
	require.Len(t, events, 1)
	assert.Equal(t, "existing.md", events[0].Name)
	assert.True(t, events[0].Op&fsnotify.Write != 0)
	events = nil

	require.NoError(t, os.Remove(filepath.Join(root, "existing.md")))
	watcher.check()
	require.Len(t, events, 1)
	assert.Equal(t, "existing.md", events[0].Name)
	assert.True(t, events[0].Op&fsnotify.Remove != 0)
}
