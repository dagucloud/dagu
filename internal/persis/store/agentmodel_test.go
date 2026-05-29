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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
)

func newMemoryAgentModelStore(t *testing.T) *store.AgentModelStore {
	t.Helper()
	col := testutil.NewMemoryBackend().Collection("models")
	s, err := store.NewAgentModelStore(col)
	require.NoError(t, err)
	return s
}

func newFileAgentModelStore(t *testing.T, dir string) *store.AgentModelStore {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o750))
	s, err := store.NewAgentModelStore(file.NewCollection(dir, file.WithIndentedJSON()))
	require.NoError(t, err)
	return s
}

func sampleModel(id, name string) *agent.ModelConfig {
	return &agent.ModelConfig{ID: id, Name: name, Provider: "anthropic", Model: "claude-opus-4-8"}
}

func TestAgentModelStore_CreateAndGetByID(t *testing.T) {
	t.Parallel()
	s := newMemoryAgentModelStore(t)
	ctx := context.Background()
	require.NoError(t, s.Create(ctx, sampleModel("m-1", "alpha")))
	got, err := s.GetByID(ctx, "m-1")
	require.NoError(t, err)
	assert.Equal(t, "alpha", got.Name)
}

func TestAgentModelStore_Create_DuplicateNameRejected(t *testing.T) {
	t.Parallel()
	s := newMemoryAgentModelStore(t)
	ctx := context.Background()
	require.NoError(t, s.Create(ctx, sampleModel("m-1", "alpha")))
	err := s.Create(ctx, sampleModel("m-2", "alpha"))
	assert.ErrorIs(t, err, agent.ErrModelNameAlreadyExists)
}

func TestAgentModelStore_Create_DuplicateIDRejected(t *testing.T) {
	t.Parallel()
	s := newMemoryAgentModelStore(t)
	ctx := context.Background()
	require.NoError(t, s.Create(ctx, sampleModel("m-1", "alpha")))
	err := s.Create(ctx, sampleModel("m-1", "beta"))
	assert.ErrorIs(t, err, agent.ErrModelAlreadyExists)
}

func TestAgentModelStore_GetByID_NotFound(t *testing.T) {
	t.Parallel()
	s := newMemoryAgentModelStore(t)
	_, err := s.GetByID(context.Background(), "missing")
	assert.ErrorIs(t, err, agent.ErrModelNotFound)
}

func TestAgentModelStore_List_SortedByName(t *testing.T) {
	t.Parallel()
	s := newMemoryAgentModelStore(t)
	ctx := context.Background()
	require.NoError(t, s.Create(ctx, sampleModel("m-1", "zeta")))
	require.NoError(t, s.Create(ctx, sampleModel("m-2", "alpha")))

	models, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, models, 2)
	assert.Equal(t, "alpha", models[0].Name)
	assert.Equal(t, "zeta", models[1].Name)
}

func TestAgentModelStore_UpdateRenamesIndex(t *testing.T) {
	t.Parallel()
	s := newMemoryAgentModelStore(t)
	ctx := context.Background()
	require.NoError(t, s.Create(ctx, sampleModel("m-1", "alpha")))

	updated := sampleModel("m-1", "beta")
	require.NoError(t, s.Update(ctx, updated))

	got, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "beta", got[0].Name)

	// byName must have released "alpha" and claimed "beta": creating "alpha" succeeds, "beta" rejects.
	require.NoError(t, s.Create(ctx, sampleModel("m-2", "alpha")))
	err = s.Create(ctx, sampleModel("m-3", "beta"))
	assert.ErrorIs(t, err, agent.ErrModelNameAlreadyExists)
}

func TestAgentModelStore_Delete(t *testing.T) {
	t.Parallel()
	s := newMemoryAgentModelStore(t)
	ctx := context.Background()
	require.NoError(t, s.Create(ctx, sampleModel("m-1", "alpha")))
	require.NoError(t, s.Delete(ctx, "m-1"))

	_, err := s.GetByID(ctx, "m-1")
	assert.ErrorIs(t, err, agent.ErrModelNotFound)
}

func TestAgentModelStore_RebuildsIndexOnReopen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s1 := newFileAgentModelStore(t, dir)
	ctx := context.Background()
	require.NoError(t, s1.Create(ctx, sampleModel("m-1", "alpha")))

	s2 := newFileAgentModelStore(t, dir)
	got, err := s2.List(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "m-1", got[0].ID)

	// byName must have been rebuilt: creating a new model with the same name rejects.
	err = s2.Create(ctx, sampleModel("m-2", "alpha"))
	assert.ErrorIs(t, err, agent.ErrModelNameAlreadyExists)
}

// On-disk bytes equal json.MarshalIndent(model, "", "  ") at {dir}/{id}.json.
func TestAgentModelStore_File_OnDiskFormatMatchesReleasedJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := newFileAgentModelStore(t, dir)
	m := sampleModel("m-1", "alpha")
	require.NoError(t, s.Create(context.Background(), m))

	got, err := os.ReadFile(filepath.Join(dir, "m-1.json"))
	require.NoError(t, err)
	expected, err := json.MarshalIndent(m, "", "  ")
	require.NoError(t, err)
	assert.True(t, bytes.Equal(got, expected),
		"on-disk bytes must equal json.MarshalIndent output")
}
