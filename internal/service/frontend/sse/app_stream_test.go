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

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
)

func TestLegacySuspendFlagEvent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	legacy := filepath.Join(root, "legacy")
	require.NoError(t, os.Mkdir(legacy, 0o750))
	flag := filepath.Join(legacy, "alpha.suspend")
	require.NoError(t, os.WriteFile(flag, nil, 0o600))
	service, err := NewAppStreamService(AppStreamConfig{
		Paths: config.PathsConfig{
			SuspendFlagsDir:       filepath.Join(root, "suspend"),
			SuspendFlagsDirLegacy: legacy,
		},
	})
	require.NoError(t, err)
	t.Cleanup(service.Shutdown)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, unsubscribe := service.Subscribe(ctx)
	defer unsubscribe()
	require.NoError(t, os.Remove(flag))
	select {
	case event := <-events:
		assert.Equal(t, AppEventTypeDAGChanged, event.Type)
		assert.Equal(t, "suspend_flag_removed", event.Reason)
	case <-ctx.Done():
		t.Fatal("legacy flag removal did not trigger a DAG refresh")
	}
}

type recordingFileWatcher struct {
	added []string
}

func (*recordingFileWatcher) Events() <-chan fsnotify.Event { return nil }
func (*recordingFileWatcher) Errors() <-chan error          { return nil }
func (w *recordingFileWatcher) Add(name string) error {
	w.added = append(w.added, name)
	return nil
}
func (*recordingFileWatcher) Remove(string) error { return nil }
func (*recordingFileWatcher) Close() error        { return nil }

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

	paths, err := recursiveWatchPaths(root, "")
	require.NoError(t, err)

	assert.Contains(t, paths, root)
	assert.Contains(t, paths, filepath.Join(root, "a"))
	assert.Contains(t, paths, filepath.Join(root, "a", "b"))
}

func TestRecursiveWatchPathsSkipsAttachmentSubtree(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "guides"), 0750))
	require.NoError(t, os.MkdirAll(filepath.Join(root, wikiPageAttachmentsDirName, "guides", "doc"), 0750))

	paths, err := recursiveWatchPaths(root, wikiPageAttachmentsDirName)
	require.NoError(t, err)

	assert.Contains(t, paths, filepath.Join(root, "guides"))
	assert.NotContains(t, paths, filepath.Join(root, wikiPageAttachmentsDirName))
	assert.NotContains(t, paths, filepath.Join(root, wikiPageAttachmentsDirName, "guides"))
}

func TestRecursiveWatcherSkipsCreatedAttachmentDirectory(t *testing.T) {
	root := t.TempDir()
	attachmentDir := filepath.Join(root, wikiPageAttachmentsDirName)
	wikiDir := filepath.Join(root, "guides")
	require.NoError(t, os.MkdirAll(attachmentDir, 0750))
	require.NoError(t, os.MkdirAll(wikiDir, 0750))

	recorder := &recordingFileWatcher{}
	watcher := newRecursiveDirectoryWatcher(root, false, func(string, string, fsnotify.Op) {}, func(string) {})
	watcher.skipDirName = wikiPageAttachmentsDirName
	watcher.watcher = recorder

	require.NoError(t, watcher.addCreatedDirWatches(attachmentDir))
	assert.Empty(t, recorder.added)
	require.NoError(t, watcher.addCreatedDirWatches(wikiDir))
	assert.Equal(t, []string{wikiDir}, recorder.added)
}

func TestSnapshotMarkdownFilesIncludesOnlyMarkdown(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "nested"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "top.md"), []byte("# top\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "notes.txt"), []byte("ignore\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "upper.MD"), []byte("ignore\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "nested", "page.md"), []byte("# page\n"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, wikiPageAttachmentsDirName, "nested"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(root, wikiPageAttachmentsDirName, "nested", "hidden.md"), []byte("attachment"), 0600))

	files, err := snapshotMarkdownFiles(root)
	require.NoError(t, err)

	assert.Contains(t, files, "top.md")
	assert.Contains(t, files, "nested/page.md")
	assert.NotContains(t, files, "notes.txt")
	assert.NotContains(t, files, "upper.MD")
	assert.NotContains(t, files, filepath.ToSlash(filepath.Join(wikiPageAttachmentsDirName, "nested", "hidden.md")))
}

func TestHandleWikiPageEventSkipsAttachmentPaths(t *testing.T) {
	coalescer := newAppEventCoalescer(time.Hour, func(AppEvent) {})
	t.Cleanup(func() {
		coalescer.mu.Lock()
		defer coalescer.mu.Unlock()
		if coalescer.timer != nil {
			coalescer.timer.Stop()
		}
	})
	service := &AppStreamService{coalescer: coalescer}

	service.handleWikiPageEvent("", ".attachments/guide/page.md", fsnotify.Write)
	assert.Empty(t, coalescer.pending)

	service.handleWikiPageEvent("", "guide/page.md", fsnotify.Write)
	require.Len(t, coalescer.pending, 1)
	for _, event := range coalescer.pending {
		assert.Equal(t, AppEventTypeWiki, event.Type)
		assert.Equal(t, "guide/page", event.Path)
	}
}

// The pause flag and the watermark share a directory, and both have to invalidate
// DAG listings so next-run columns stay truthful.
func TestHandleSchedulerStateEventCoversWatermarkAndPause(t *testing.T) {
	newService := func(t *testing.T) *AppStreamService {
		t.Helper()
		coalescer := newAppEventCoalescer(time.Hour, func(AppEvent) {})
		t.Cleanup(func() {
			coalescer.mu.Lock()
			defer coalescer.mu.Unlock()
			if coalescer.timer != nil {
				coalescer.timer.Stop()
			}
		})
		return &AppStreamService{coalescer: coalescer}
	}

	for _, name := range []string{schedulerStateFileName, schedulerPauseFileName} {
		service := newService(t)
		service.handleSchedulerStateEvent("", name, fsnotify.Write)
		require.Len(t, service.coalescer.pending, 1, "expected %s to raise an event", name)
		for _, event := range service.coalescer.pending {
			assert.Equal(t, AppEventTypeScheduler, event.Type)
		}
	}

	service := newService(t)
	service.handleSchedulerStateEvent("", "locks/some.lock", fsnotify.Write)
	assert.Empty(t, service.coalescer.pending)
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
	snapshot, err := snapshotMarkdownFiles(root)
	require.NoError(t, err)
	watcher.snapshot = snapshot

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
