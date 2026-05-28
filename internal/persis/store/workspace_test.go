// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	"github.com/dagucloud/dagu/internal/workspace"
)

func newMemoryWorkspaceStore(t *testing.T) *store.WorkspaceStore {
	t.Helper()
	col := testutil.NewMemoryBackend().Collection("workspaces")
	s, err := store.NewWorkspaceStore(col)
	require.NoError(t, err)
	return s
}

func newFileWorkspaceStore(t *testing.T, dir string) *store.WorkspaceStore {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o750))
	s, err := store.NewWorkspaceStore(file.NewCollection(dir, file.WithIndentedJSON()))
	require.NoError(t, err)
	return s
}

func sampleWorkspace(id, name string) *workspace.Workspace {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	return &workspace.Workspace{
		ID:          id,
		Name:        name,
		Description: "test workspace",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func TestWorkspaceStore_CreateAndGetByID(t *testing.T) {
	t.Parallel()
	s := newMemoryWorkspaceStore(t)
	ctx := context.Background()

	ws := sampleWorkspace("ws-1", "alpha")
	require.NoError(t, s.Create(ctx, ws))

	got, err := s.GetByID(ctx, "ws-1")
	require.NoError(t, err)
	assert.Equal(t, "ws-1", got.ID)
	assert.Equal(t, "alpha", got.Name)
}

func TestWorkspaceStore_GetByName(t *testing.T) {
	t.Parallel()
	s := newMemoryWorkspaceStore(t)
	ctx := context.Background()
	require.NoError(t, s.Create(ctx, sampleWorkspace("ws-1", "alpha")))

	got, err := s.GetByName(ctx, "alpha")
	require.NoError(t, err)
	assert.Equal(t, "ws-1", got.ID)
}

func TestWorkspaceStore_GetByID_NotFound(t *testing.T) {
	t.Parallel()
	s := newMemoryWorkspaceStore(t)
	_, err := s.GetByID(context.Background(), "missing")
	assert.ErrorIs(t, err, workspace.ErrWorkspaceNotFound)
}

func TestWorkspaceStore_Create_DuplicateNameRejected(t *testing.T) {
	t.Parallel()
	s := newMemoryWorkspaceStore(t)
	ctx := context.Background()
	require.NoError(t, s.Create(ctx, sampleWorkspace("ws-1", "alpha")))
	err := s.Create(ctx, sampleWorkspace("ws-2", "alpha"))
	assert.ErrorIs(t, err, workspace.ErrWorkspaceAlreadyExists)
}

func TestWorkspaceStore_UpdateRenamesIndex(t *testing.T) {
	t.Parallel()
	s := newMemoryWorkspaceStore(t)
	ctx := context.Background()
	require.NoError(t, s.Create(ctx, sampleWorkspace("ws-1", "alpha")))

	updated := sampleWorkspace("ws-1", "beta")
	updated.UpdatedAt = time.Now().UTC()
	require.NoError(t, s.Update(ctx, updated))

	_, err := s.GetByName(ctx, "alpha")
	assert.ErrorIs(t, err, workspace.ErrWorkspaceNotFound)

	got, err := s.GetByName(ctx, "beta")
	require.NoError(t, err)
	assert.Equal(t, "ws-1", got.ID)
}

func TestWorkspaceStore_Delete(t *testing.T) {
	t.Parallel()
	s := newMemoryWorkspaceStore(t)
	ctx := context.Background()
	require.NoError(t, s.Create(ctx, sampleWorkspace("ws-1", "alpha")))
	require.NoError(t, s.Delete(ctx, "ws-1"))

	_, err := s.GetByID(ctx, "ws-1")
	assert.ErrorIs(t, err, workspace.ErrWorkspaceNotFound)
	_, err = s.GetByName(ctx, "alpha")
	assert.ErrorIs(t, err, workspace.ErrWorkspaceNotFound)
}

func TestWorkspaceStore_RebuildsIndexOnReopen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s1 := newFileWorkspaceStore(t, dir)
	ctx := context.Background()
	require.NoError(t, s1.Create(ctx, sampleWorkspace("ws-1", "alpha")))

	s2 := newFileWorkspaceStore(t, dir)
	got, err := s2.GetByName(ctx, "alpha")
	require.NoError(t, err)
	assert.Equal(t, "ws-1", got.ID)
}

// On-disk bytes equal json.MarshalIndent(WorkspaceForStorage, "", "  ").
func TestWorkspaceStore_File_OnDiskFormatMatchesReleasedJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := newFileWorkspaceStore(t, dir)
	ws := sampleWorkspace("ws-1", "alpha")
	require.NoError(t, s.Create(context.Background(), ws))

	got, err := os.ReadFile(filepath.Join(dir, "ws-1.json"))
	require.NoError(t, err)

	expected, err := json.MarshalIndent(ws.ToStorage(), "", "  ")
	require.NoError(t, err)
	assert.True(t, bytes.Equal(got, expected),
		"on-disk bytes must equal json.MarshalIndent output")
}
