// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workspacebundle

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreRejectsEmptyDir(t *testing.T) {
	t.Parallel()

	store := NewStore(" \t ", DefaultLimits())

	err := store.Put(context.Background(), Descriptor{}, nil)
	assert.ErrorContains(t, err, "workspace bundle store is not configured")
}

func TestPackDirectoryToFileCreatesVerifiedArchive(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "input.txt"), []byte("input"), 0o644))
	stagingDir := filepath.Join(t.TempDir(), "staging")

	desc, archivePath, err := PackDirectoryToFile(root, stagingDir, PackOptions{
		DAGPath:  "dag.yaml",
		DAGData:  []byte("steps: []\n"),
		Includes: []string{"input.txt"},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.Remove(archivePath)) })
	assert.Equal(t, stagingDir, filepath.Dir(archivePath))

	archive, err := os.ReadFile(archivePath)
	require.NoError(t, err)
	assert.Equal(t, int64(len(archive)), desc.Size)
	require.NoError(t, Verify(archive, desc.Digest))
}

func TestPackDirectoryToFileRemovesPartialArchive(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "input.txt"), bytes.Repeat([]byte("input"), 128), 0o644))
	stagingDir := filepath.Join(t.TempDir(), "staging")

	_, _, err := PackDirectoryToFile(root, stagingDir, PackOptions{
		DAGPath:  "dag.yaml",
		DAGData:  []byte("steps: []\n"),
		Includes: []string{"input.txt"},
		Limits:   Limits{MaxCompressedSize: 1},
	})
	require.ErrorContains(t, err, "compressed size limit")
	entries, readErr := os.ReadDir(stagingDir)
	require.NoError(t, readErr)
	assert.Empty(t, entries)
}

func TestStorePutReaderAndOpen(t *testing.T) {
	t.Parallel()

	data := bytes.Repeat([]byte("bundle"), 1024)
	desc := Descriptor{Digest: Digest(data), Size: int64(len(data))}
	store := NewStore(t.TempDir(), DefaultLimits())

	require.NoError(t, store.PutReader(context.Background(), desc, bytes.NewReader(data)))
	file, size, err := store.Open(context.Background(), desc.Digest)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, file.Close()) })
	actual, err := io.ReadAll(file)
	require.NoError(t, err)
	assert.Equal(t, int64(len(data)), size)
	assert.Equal(t, data, actual)
}

func TestStorePutReplacesCorruptBundle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	data := []byte("valid bundle")
	desc := Descriptor{Digest: Digest(data), Size: int64(len(data))}
	store := NewStore(t.TempDir(), DefaultLimits())
	require.NoError(t, store.Put(ctx, desc, data))
	bundlePath, err := store.path(desc.Digest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(bundlePath, []byte("bad"), 0o600))

	require.NoError(t, store.Put(ctx, desc, data))
	actual, err := store.Get(ctx, desc.Digest)
	require.NoError(t, err)
	assert.Equal(t, data, actual)
}

func TestStoreCleanupRemovesExpiredBundle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Now().UTC()
	store := NewStore(t.TempDir(), DefaultLimits())
	oldData := []byte("old bundle")
	oldDesc := Descriptor{Digest: Digest(oldData), Size: int64(len(oldData))}
	freshData := []byte("fresh bundle")
	freshDesc := Descriptor{Digest: Digest(freshData), Size: int64(len(freshData))}
	require.NoError(t, store.Put(ctx, oldDesc, oldData))
	require.NoError(t, store.Put(ctx, freshDesc, freshData))
	oldPath, err := store.path(oldDesc.Digest)
	require.NoError(t, err)
	require.NoError(t, os.Chtimes(oldPath, now.Add(-2*time.Hour), now.Add(-2*time.Hour)))

	removed, err := store.Cleanup(ctx, now.Add(-time.Hour), nil)
	require.NoError(t, err)
	assert.Equal(t, 1, removed)
	assert.False(t, store.Has(oldDesc.Digest))
	assert.True(t, store.Has(freshDesc.Digest))
}

func TestStoreCleanupPreservesProtectedBundle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Now().UTC()
	store := NewStore(t.TempDir(), DefaultLimits())
	data := []byte("protected bundle")
	desc := Descriptor{Digest: Digest(data), Size: int64(len(data))}
	require.NoError(t, store.Put(ctx, desc, data))
	bundlePath, err := store.path(desc.Digest)
	require.NoError(t, err)
	require.NoError(t, os.Chtimes(bundlePath, now.Add(-2*time.Hour), now.Add(-2*time.Hour)))

	removed, err := store.Cleanup(ctx, now.Add(-time.Hour), map[string]struct{}{desc.Digest: {}})
	require.NoError(t, err)
	assert.Zero(t, removed)
	assert.True(t, store.Has(desc.Digest))
}

func TestStoreAccessRefreshesExpiration(t *testing.T) {
	t.Parallel()

	tests := map[string]func(context.Context, *Store, Descriptor, []byte) error{
		"touch": func(ctx context.Context, store *Store, desc Descriptor, _ []byte) error {
			exists, err := store.Touch(ctx, desc.Digest)
			if !exists && err == nil {
				return os.ErrNotExist
			}
			return err
		},
		"open": func(ctx context.Context, store *Store, desc Descriptor, _ []byte) error {
			file, _, err := store.Open(ctx, desc.Digest)
			if err != nil {
				return err
			}
			return file.Close()
		},
		"duplicate put": func(ctx context.Context, store *Store, desc Descriptor, data []byte) error {
			return store.Put(ctx, desc, data)
		},
	}
	for name, access := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			now := time.Now().UTC()
			store := NewStore(t.TempDir(), DefaultLimits())
			data := []byte("cached bundle")
			desc := Descriptor{Digest: Digest(data), Size: int64(len(data))}
			require.NoError(t, store.Put(ctx, desc, data))
			bundlePath, err := store.path(desc.Digest)
			require.NoError(t, err)
			require.NoError(t, os.Chtimes(bundlePath, now.Add(-2*time.Hour), now.Add(-2*time.Hour)))

			require.NoError(t, access(ctx, store, desc, data))
			removed, err := store.Cleanup(ctx, now.Add(-time.Hour), nil)
			require.NoError(t, err)
			assert.Zero(t, removed)
			assert.True(t, store.Has(desc.Digest))
		})
	}
}

func TestStoreCorruptAccessDoesNotRefreshExpiration(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*testing.T, context.Context, *Store, Descriptor){
		"touch": func(t *testing.T, ctx context.Context, store *Store, desc Descriptor) {
			t.Helper()

			exists, err := store.Touch(ctx, desc.Digest)
			require.NoError(t, err)
			assert.False(t, exists)
		},
		"open": func(t *testing.T, ctx context.Context, store *Store, desc Descriptor) {
			t.Helper()

			_, _, err := store.Open(ctx, desc.Digest)
			require.ErrorContains(t, err, "digest mismatch")
		},
	}
	for name, access := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			now := time.Now().UTC()
			store := NewStore(t.TempDir(), DefaultLimits())
			data := []byte("valid bundle")
			desc := Descriptor{Digest: Digest(data), Size: int64(len(data))}
			require.NoError(t, store.Put(ctx, desc, data))
			bundlePath, err := store.path(desc.Digest)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(bundlePath, []byte("corrupt"), 0o600))
			require.NoError(t, os.Chtimes(bundlePath, now.Add(-2*time.Hour), now.Add(-2*time.Hour)))

			access(t, ctx, store, desc)
			removed, err := store.Cleanup(ctx, now.Add(-time.Hour), nil)
			require.NoError(t, err)
			assert.Equal(t, 1, removed)
			assert.False(t, store.Has(desc.Digest))
		})
	}
}

func TestStoreTouchReturnsStorageError(t *testing.T) {
	t.Parallel()

	store := NewStore(t.TempDir(), DefaultLimits())
	digest := Digest([]byte("bundle"))
	bundlePath, err := store.path(digest)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(bundlePath, 0o700))

	exists, err := store.Touch(context.Background(), digest)
	assert.False(t, exists)
	require.Error(t, err)
}

func TestStoreCleanupIgnoresForeignPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Now().UTC()
	dir := t.TempDir()
	store := NewStore(dir, DefaultLimits())
	data := []byte("fresh bundle")
	desc := Descriptor{Digest: Digest(data), Size: int64(len(data))}
	require.NoError(t, store.Put(ctx, desc, data))

	foreignDir := filepath.Join(dir, "foreign")
	require.NoError(t, os.MkdirAll(foreignDir, 0o750))
	foreignPath := filepath.Join(foreignDir, desc.Digest+archiveExt)
	require.NoError(t, os.WriteFile(foreignPath, data, 0o600))
	require.NoError(t, os.Chtimes(foreignPath, now.Add(-2*time.Hour), now.Add(-2*time.Hour)))

	removed, err := store.Cleanup(ctx, now.Add(-time.Hour), nil)
	require.NoError(t, err)
	assert.Zero(t, removed)
	assert.True(t, store.Has(desc.Digest))
	assert.FileExists(t, foreignPath)
}

func TestStoreCleanupContinuesAfterEntryError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission behavior is Unix-specific")
	}

	ctx := context.Background()
	now := time.Now().UTC()
	dir := t.TempDir()
	blockedDir := filepath.Join(dir, "!blocked")
	require.NoError(t, os.Mkdir(blockedDir, 0o700))
	require.NoError(t, os.Chmod(blockedDir, 0))
	t.Cleanup(func() { require.NoError(t, os.Chmod(blockedDir, 0o700)) })
	if _, err := os.ReadDir(blockedDir); err == nil {
		t.Skip("filesystem permissions do not block traversal")
	}

	store := NewStore(dir, DefaultLimits())
	data := []byte("expired bundle")
	desc := Descriptor{Digest: Digest(data), Size: int64(len(data))}
	require.NoError(t, store.Put(ctx, desc, data))
	bundlePath, err := store.path(desc.Digest)
	require.NoError(t, err)
	require.NoError(t, os.Chtimes(bundlePath, now.Add(-2*time.Hour), now.Add(-2*time.Hour)))

	removed, err := store.Cleanup(ctx, now.Add(-time.Hour), nil)
	require.Error(t, err)
	assert.Equal(t, 1, removed)
	assert.False(t, store.Has(desc.Digest))
}

func TestStoreCleanupAndTouchAreConsistent(t *testing.T) {
	t.Parallel()

	for range 10 {
		ctx := context.Background()
		now := time.Now().UTC()
		dir := t.TempDir()
		first := NewStore(dir, DefaultLimits())
		second := NewStore(dir, DefaultLimits())
		data := []byte("shared bundle")
		desc := Descriptor{Digest: Digest(data), Size: int64(len(data))}
		require.NoError(t, first.Put(ctx, desc, data))
		bundlePath, err := first.path(desc.Digest)
		require.NoError(t, err)
		require.NoError(t, os.Chtimes(bundlePath, now.Add(-2*time.Hour), now.Add(-2*time.Hour)))

		start := make(chan struct{})
		cleanupDone := make(chan error, 1)
		touchDone := make(chan struct {
			exists bool
			err    error
		}, 1)
		go func() {
			<-start
			_, err := first.Cleanup(ctx, now.Add(-time.Hour), nil)
			cleanupDone <- err
		}()
		go func() {
			<-start
			exists, err := second.Touch(ctx, desc.Digest)
			touchDone <- struct {
				exists bool
				err    error
			}{exists: exists, err: err}
		}()
		close(start)

		require.NoError(t, <-cleanupDone)
		touched := <-touchDone
		require.NoError(t, touched.err)
		if touched.exists {
			assert.True(t, first.Has(desc.Digest))
		}
	}
}

func TestStoreRenewsLockDuringLongOperation(t *testing.T) {
	lock := &heartbeatLock{heartbeat: make(chan struct{}, 1)}
	store := NewStore(t.TempDir(), DefaultLimits())
	store.lock = lock
	store.lockHeartbeatInterval = time.Millisecond

	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- store.withLock(t.Context(), func(context.Context) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered

	select {
	case <-lock.heartbeat:
	case <-time.After(time.Second):
		close(release)
		require.NoError(t, <-done)
		t.Fatal("workspace bundle store lock was not renewed")
	}
	close(release)
	require.NoError(t, <-done)
	assert.Equal(t, int32(1), lock.unlocks.Load())
}

func TestStoreLockWaitHonorsContext(t *testing.T) {
	store := NewStore(t.TempDir(), DefaultLimits())
	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- store.withLock(t.Context(), func(context.Context) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- store.withLock(ctx, func(context.Context) error {
			return nil
		})
	}()

	select {
	case err := <-secondDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(100 * time.Millisecond):
		close(release)
		require.NoError(t, <-firstDone)
		require.ErrorIs(t, <-secondDone, context.Canceled)
		t.Fatal("local workspace bundle lock ignored cancellation")
	}

	close(release)
	require.NoError(t, <-firstDone)
}

func TestStoreStopsOperationAfterHeartbeatFailure(t *testing.T) {
	lock := &heartbeatLock{
		heartbeat:    make(chan struct{}, 1),
		heartbeatErr: errors.New("lock ownership lost"),
	}
	store := NewStore(t.TempDir(), DefaultLimits())
	store.lock = lock
	store.lockHeartbeatInterval = time.Millisecond

	err := store.withLock(t.Context(), func(ctx context.Context) error {
		<-lock.heartbeat
		<-ctx.Done()
		return context.Cause(ctx)
	})

	require.ErrorContains(t, err, "lock ownership lost")
	assert.Equal(t, int32(1), lock.unlocks.Load())
}

func TestStoreHeartbeatPreventsLockSteal(t *testing.T) {
	dir := t.TempDir()
	first := NewStore(dir, DefaultLimits())
	second := NewStore(dir, DefaultLimits())
	lockOptions := &dirlock.LockOptions{StaleThreshold: time.Minute}
	firstLock := &gatedHeartbeatLock{
		DirLock: dirlock.New(dir, lockOptions),
		started: make(chan struct{}),
		resume:  make(chan struct{}),
		done:    make(chan error),
	}
	first.lock = firstLock
	second.lock = dirlock.New(dir, lockOptions)
	first.lockHeartbeatInterval = time.Millisecond

	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- first.withLock(t.Context(), func(context.Context) error {
			close(firstEntered)
			<-releaseFirst
			return nil
		})
	}()
	<-firstEntered

	select {
	case <-firstLock.started:
	case <-time.After(time.Second):
		close(releaseFirst)
		require.NoError(t, <-firstDone)
		t.Fatal("workspace bundle store heartbeat did not start")
	}

	// Force the lease stale while its heartbeat is paused.
	lockPath := filepath.Join(dir, dirlock.LockDirectoryName)
	staleTime := time.Now().Add(-2 * lockOptions.StaleThreshold)
	require.NoError(t, os.Chtimes(lockPath, staleTime, staleTime))
	close(firstLock.resume)
	require.NoError(t, <-firstLock.done)
	require.ErrorIs(t, second.lock.TryLock(), dirlock.ErrLockConflict)

	close(releaseFirst)
	require.NoError(t, <-firstDone)
	require.NoError(t, second.lock.TryLock())
	require.NoError(t, second.lock.Unlock())
}

type gatedHeartbeatLock struct {
	dirlock.DirLock
	started chan struct{}
	resume  chan struct{}
	done    chan error
	once    sync.Once
}

func (l *gatedHeartbeatLock) Heartbeat(ctx context.Context) error {
	first := false
	l.once.Do(func() {
		first = true
		close(l.started)
	})
	if !first {
		return l.DirLock.Heartbeat(ctx)
	}

	<-l.resume
	err := l.DirLock.Heartbeat(ctx)
	l.done <- err
	return err
}

type heartbeatLock struct {
	heartbeat    chan struct{}
	heartbeatErr error
	locked       atomic.Bool
	unlocks      atomic.Int32
	mu           sync.Mutex
}

var _ dirlock.DirLock = (*heartbeatLock)(nil)

func (l *heartbeatLock) TryLock() error {
	if !l.locked.CompareAndSwap(false, true) {
		return dirlock.ErrLockConflict
	}
	return nil
}

func (l *heartbeatLock) Lock(context.Context) error {
	return l.TryLock()
}

func (l *heartbeatLock) Unlock() error {
	l.locked.Store(false)
	l.unlocks.Add(1)
	return nil
}

func (l *heartbeatLock) IsLocked() bool {
	return l.locked.Load()
}

func (l *heartbeatLock) IsHeldByMe() bool {
	return l.locked.Load()
}

func (l *heartbeatLock) Info() (*dirlock.LockInfo, error) {
	return nil, nil
}

func (l *heartbeatLock) Heartbeat(context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	select {
	case l.heartbeat <- struct{}{}:
	default:
	}
	return l.heartbeatErr
}

func TestPackDirectorySelectsDependenciesAndInjectsDAGSnapshot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "scripts", "nested"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "config"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "empty"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "scripts", "backup.sh"), []byte("backup"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "scripts", "nested", "cleanup.sh"), []byte("cleanup"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "config", "app.yaml"), []byte("enabled: true"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ignored.txt"), []byte("ignored"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dag.yaml"), []byte("stale"), 0o644))

	dagData := []byte("steps:\n  - run: echo bundled\n")
	desc, data, err := PackDirectory(root, PackOptions{
		DAGPath:  "dag.yaml",
		DAGData:  dagData,
		Includes: []string{"scripts/**", "config", "empty"},
	})
	require.NoError(t, err)
	require.Equal(t, Digest(data), desc.Digest)

	dest := filepath.Join(t.TempDir(), "workspace")
	require.NoError(t, Extract(data, dest, *desc, DefaultLimits()))

	assert.FileExists(t, filepath.Join(dest, "scripts", "backup.sh"))
	assert.FileExists(t, filepath.Join(dest, "scripts", "nested", "cleanup.sh"))
	assert.FileExists(t, filepath.Join(dest, "config", "app.yaml"))
	assert.DirExists(t, filepath.Join(dest, "empty"))
	assert.NoFileExists(t, filepath.Join(dest, "ignored.txt"))
	actualDAG, err := os.ReadFile(filepath.Join(dest, "dag.yaml"))
	require.NoError(t, err)
	assert.Equal(t, dagData, actualDAG)
}

func TestPackDirectorySelectedFilesIncludeDAGFromDisk(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "dag.yaml"), []byte("steps: []\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dependency.txt"), []byte("dependency"), 0o644))

	desc, data, err := PackDirectory(root, PackOptions{
		DAGPath:  "dag.yaml",
		Includes: []string{"dependency.txt"},
	})
	require.NoError(t, err)

	dest := filepath.Join(t.TempDir(), "workspace")
	require.NoError(t, Extract(data, dest, *desc, DefaultLimits()))

	actualDAG, err := os.ReadFile(filepath.Join(dest, "dag.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "steps: []\n", string(actualDAG))
}

func TestPackDirectoryRejectsInvalidDependencies(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".git", "config"), []byte("git"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "target.txt"), []byte("target"), 0o644))
	require.NoError(t, os.Symlink("target.txt", filepath.Join(root, "link.txt")))

	tests := []struct {
		name    string
		include string
		wantErr string
	}{
		{name: "Empty", include: " ", wantErr: "path is required"},
		{name: "Absolute", include: filepath.Join(root, "target.txt"), wantErr: "path must be relative"},
		{name: "Escape", include: "../target.txt", wantErr: "path escapes workspace bundle"},
		{name: "InvalidGlob", include: "[", wantErr: "invalid workspace include pattern"},
		{name: "NoMatch", include: "missing/**", wantErr: "matched no files"},
		{name: "Git", include: ".git/config", wantErr: "does not support .git path"},
		{name: "Symlink", include: "link.txt", wantErr: "does not support symlink"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := PackDirectory(root, PackOptions{
				DAGPath:  "dag.yaml",
				DAGData:  []byte("steps: []\n"),
				Includes: []string{tt.include},
			})
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestPackDirectorySelectedBundleIsDeterministic(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.txt"), []byte("b"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644))
	opts := PackOptions{
		DAGPath:  "dag.yaml",
		DAGData:  []byte("steps: []\n"),
		Includes: []string{"*.txt", "a.txt"},
	}

	first, firstData, err := PackDirectory(root, opts)
	require.NoError(t, err)
	second, secondData, err := PackDirectory(root, opts)
	require.NoError(t, err)
	assert.Equal(t, first.Digest, second.Digest)
	assert.Equal(t, firstData, secondData)
}

func TestPackDirectoryStopsSelectedTraversalAtFileLimit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	dependencies := filepath.Join(root, "dependencies")
	require.NoError(t, os.Mkdir(dependencies, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dependencies, "a.txt"), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dependencies, "b.txt"), []byte("b"), 0o644))
	require.NoError(t, os.Symlink("a.txt", filepath.Join(dependencies, "z-link")))

	_, _, err := PackDirectory(root, PackOptions{
		DAGPath:  "dag.yaml",
		DAGData:  []byte("steps: []\n"),
		Includes: []string{"dependencies"},
		Limits:   Limits{MaxFiles: 2},
	})
	require.ErrorContains(t, err, "workspace bundle exceeds file count limit 2")
}
