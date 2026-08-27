// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workspacebundle

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

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

func TestStoreReferenceLifecycle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()
	store := NewStore(dir, DefaultLimits())
	first := []byte("first bundle")
	firstDesc := Descriptor{Digest: Digest(first), Size: int64(len(first))}
	second := []byte("second bundle")
	secondDesc := Descriptor{Digest: Digest(second), Size: int64(len(second))}
	require.NoError(t, store.Put(ctx, firstDesc, first))
	require.NoError(t, store.Put(ctx, secondDesc, second))

	retained, err := store.Retain(ctx, "attempt-a", firstDesc.Digest)
	require.NoError(t, err)
	assert.True(t, retained)
	retained, err = store.Retain(ctx, "attempt-a", firstDesc.Digest)
	require.NoError(t, err)
	assert.False(t, retained)
	_, err = store.Retain(ctx, "attempt-b", firstDesc.Digest)
	require.NoError(t, err)
	_, err = store.Retain(ctx, "attempt-a", secondDesc.Digest)
	require.ErrorContains(t, err, "already references")

	references, err := store.ListReferences(ctx)
	require.NoError(t, err)
	require.Len(t, references, 2)

	require.NoError(t, store.Release(ctx, "attempt-a"))
	assert.True(t, store.Has(firstDesc.Digest))

	restarted := NewStore(dir, DefaultLimits())
	require.NoError(t, restarted.Release(ctx, "attempt-b"))
	assert.False(t, restarted.Has(firstDesc.Digest))
	require.NoError(t, restarted.Release(ctx, "attempt-b"))
}

func TestStoreReleaseDoesNotLeaveDanglingReference(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()
	first := NewStore(dir, DefaultLimits())
	second := NewStore(dir, DefaultLimits())
	data := []byte("shared bundle")
	desc := Descriptor{Digest: Digest(data), Size: int64(len(data))}
	require.NoError(t, first.Put(ctx, desc, data))
	_, err := first.Retain(ctx, "attempt-a", desc.Digest)
	require.NoError(t, err)

	// Release pauses after WalkDir snapshots the original reference directory.
	releaseCtx := newBlockingErrContext(ctx, 2)
	t.Cleanup(releaseCtx.unblock)
	releaseDone := make(chan error, 1)
	go func() {
		releaseDone <- first.Release(releaseCtx, "attempt-a")
	}()
	waitForSignal(t, releaseCtx.blocked, "release did not reach the reference snapshot")

	retainCtx := newObservedDoneContext(ctx)
	retainDone := make(chan retainResult, 1)
	go func() {
		retained, err := second.Retain(retainCtx, "attempt-b", desc.Digest)
		retainDone <- retainResult{retained: retained, err: err}
	}()
	result, completed := waitForRetainOrLock(t, retainDone, retainCtx.waiting)

	releaseCtx.unblock()
	require.NoError(t, waitForResult(t, releaseDone, "release did not finish"))
	if !completed {
		result = waitForResult(t, retainDone, "retain did not finish")
	}

	assertRetainConsistency(t, second, desc.Digest, result)
}

func TestStoreCleanupDoesNotLeaveDanglingReference(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()
	first := NewStore(dir, DefaultLimits())
	second := NewStore(dir, DefaultLimits())
	data := []byte("shared bundle")
	desc := Descriptor{Digest: Digest(data), Size: int64(len(data))}
	require.NoError(t, first.Put(ctx, desc, data))

	// Cleanup pauses after snapshotting references and managed bundle entries.
	cleanupCtx := newBlockingErrContext(ctx, 1)
	t.Cleanup(cleanupCtx.unblock)
	cleanupDone := make(chan error, 1)
	go func() {
		_, err := first.CleanupUnreferenced(cleanupCtx, time.Now().Add(time.Hour))
		cleanupDone <- err
	}()
	waitForSignal(t, cleanupCtx.blocked, "cleanup did not reach the managed bundle snapshot")

	retainCtx := newObservedDoneContext(ctx)
	retainDone := make(chan retainResult, 1)
	go func() {
		retained, err := second.Retain(retainCtx, "attempt-b", desc.Digest)
		retainDone <- retainResult{retained: retained, err: err}
	}()
	result, completed := waitForRetainOrLock(t, retainDone, retainCtx.waiting)

	cleanupCtx.unblock()
	require.NoError(t, waitForResult(t, cleanupDone, "cleanup did not finish"))
	if !completed {
		result = waitForResult(t, retainDone, "retain did not finish")
	}

	assertRetainConsistency(t, second, desc.Digest, result)
}

func TestStoreCleanupUnreferencedPreservesLegacyBundle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()
	store := NewStore(dir, DefaultLimits())
	managed := []byte("managed bundle")
	managedDesc := Descriptor{Digest: Digest(managed), Size: int64(len(managed))}
	require.NoError(t, store.Put(ctx, managedDesc, managed))

	legacy := []byte("legacy bundle")
	legacyDigest := Digest(legacy)
	legacyPath, err := store.path(legacyDigest)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(legacyPath), 0o750))
	require.NoError(t, os.WriteFile(legacyPath, legacy, 0o600))

	removed, err := store.CleanupUnreferenced(ctx, time.Now().Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 1, removed)
	assert.False(t, store.Has(managedDesc.Digest))
	assert.True(t, store.Has(legacyDigest))
}

type retainResult struct {
	retained bool
	err      error
}

type blockingErrContext struct {
	context.Context
	blocked    chan struct{}
	resume     chan struct{}
	resumeOnce sync.Once
	blockAt    int
	calls      int
}

func newBlockingErrContext(ctx context.Context, blockAt int) *blockingErrContext {
	return &blockingErrContext{
		Context: ctx,
		blocked: make(chan struct{}),
		resume:  make(chan struct{}),
		blockAt: blockAt,
	}
}

func (c *blockingErrContext) Err() error {
	c.calls++
	if c.calls == c.blockAt {
		close(c.blocked)
		<-c.resume
	}
	return c.Context.Err()
}

func (c *blockingErrContext) unblock() {
	c.resumeOnce.Do(func() {
		close(c.resume)
	})
}

type observedDoneContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func newObservedDoneContext(ctx context.Context) *observedDoneContext {
	return &observedDoneContext{
		Context: ctx,
		waiting: make(chan struct{}),
	}
}

func (c *observedDoneContext) Done() <-chan struct{} {
	c.once.Do(func() {
		close(c.waiting)
	})
	return c.Context.Done()
}

func waitForSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal(message)
	}
}

func waitForRetainOrLock(t *testing.T, result <-chan retainResult, waiting <-chan struct{}) (retainResult, bool) {
	t.Helper()
	select {
	case value := <-result:
		return value, true
	case <-waiting:
		return retainResult{}, false
	case <-time.After(5 * time.Second):
		t.Fatal("retain neither completed nor waited for the store lock")
		return retainResult{}, false
	}
}

func waitForResult[T any](t *testing.T, result <-chan T, message string) T {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal(message)
		var zero T
		return zero
	}
}

func assertRetainConsistency(t *testing.T, store *Store, digest string, result retainResult) {
	t.Helper()
	if result.err == nil {
		assert.True(t, result.retained)
		assert.True(t, store.Has(digest), "successful retain must preserve its bundle")
		return
	}
	require.ErrorIs(t, result.err, os.ErrNotExist)
	references, err := store.ListReferences(context.Background())
	require.NoError(t, err)
	assert.Empty(t, references)
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
