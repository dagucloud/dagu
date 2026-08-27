// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workspacebundle

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
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
