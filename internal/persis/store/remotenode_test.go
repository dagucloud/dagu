// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	"github.com/dagucloud/dagu/internal/remotenode"
)

func newTestEncryptor(t *testing.T) *crypto.Encryptor {
	t.Helper()
	encryptor, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)
	return encryptor
}

func newMemoryRemoteNodeStore(t *testing.T) *store.RemoteNodeStore {
	t.Helper()
	s, err := store.NewRemoteNodeStore(testutil.NewMemoryBackend().Collection("nodes"), newTestEncryptor(t))
	require.NoError(t, err)
	return s
}

func newFileRemoteNodeStore(t *testing.T, dir string) *store.RemoteNodeStore {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o750))
	s, err := store.NewRemoteNodeStore(file.NewCollection(dir, file.WithIndentedJSON()), newTestEncryptor(t))
	require.NoError(t, err)
	return s
}

func sampleRemoteNode(id, name string) *remotenode.RemoteNode {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	return &remotenode.RemoteNode{
		ID:                id,
		Name:              name,
		Description:       "test node",
		APIBaseURL:        "https://example.com",
		AuthType:          remotenode.AuthTypeBasic,
		BasicAuthUsername: "user",
		BasicAuthPassword: "secret-pwd",
		AuthToken:         "secret-token",
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func TestRemoteNodeStore_CreateAndGetByID(t *testing.T) {
	t.Parallel()
	s := newMemoryRemoteNodeStore(t)
	ctx := context.Background()
	node := sampleRemoteNode("n-1", "alpha")
	require.NoError(t, s.Create(ctx, node))

	got, err := s.GetByID(ctx, "n-1")
	require.NoError(t, err)
	assert.Equal(t, "alpha", got.Name)
	assert.Equal(t, "secret-pwd", got.BasicAuthPassword)
	assert.Equal(t, "secret-token", got.AuthToken)
}

func TestRemoteNodeStore_GetByName(t *testing.T) {
	t.Parallel()
	s := newMemoryRemoteNodeStore(t)
	ctx := context.Background()
	require.NoError(t, s.Create(ctx, sampleRemoteNode("n-1", "alpha")))

	got, err := s.GetByName(ctx, "alpha")
	require.NoError(t, err)
	assert.Equal(t, "n-1", got.ID)
}

func TestRemoteNodeStore_DuplicateNameRejected(t *testing.T) {
	t.Parallel()
	s := newMemoryRemoteNodeStore(t)
	ctx := context.Background()
	require.NoError(t, s.Create(ctx, sampleRemoteNode("n-1", "alpha")))
	err := s.Create(ctx, sampleRemoteNode("n-2", "alpha"))
	assert.ErrorIs(t, err, remotenode.ErrRemoteNodeAlreadyExists)
}

func TestRemoteNodeStore_UpdateRenames(t *testing.T) {
	t.Parallel()
	s := newMemoryRemoteNodeStore(t)
	ctx := context.Background()
	require.NoError(t, s.Create(ctx, sampleRemoteNode("n-1", "alpha")))

	updated := sampleRemoteNode("n-1", "beta")
	require.NoError(t, s.Update(ctx, updated))

	_, err := s.GetByName(ctx, "alpha")
	assert.ErrorIs(t, err, remotenode.ErrRemoteNodeNotFound)
	got, err := s.GetByName(ctx, "beta")
	require.NoError(t, err)
	assert.Equal(t, "n-1", got.ID)
}

func TestRemoteNodeStore_Delete(t *testing.T) {
	t.Parallel()
	s := newMemoryRemoteNodeStore(t)
	ctx := context.Background()
	require.NoError(t, s.Create(ctx, sampleRemoteNode("n-1", "alpha")))
	require.NoError(t, s.Delete(ctx, "n-1"))
	_, err := s.GetByID(ctx, "n-1")
	assert.ErrorIs(t, err, remotenode.ErrRemoteNodeNotFound)
}

func TestRemoteNodeStore_File_CredentialsEncryptedOnDisk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := newFileRemoteNodeStore(t, dir)
	node := sampleRemoteNode("n-1", "alpha")
	require.NoError(t, s.Create(context.Background(), node))

	raw, err := os.ReadFile(filepath.Join(dir, "n-1.json"))
	require.NoError(t, err)
	content := string(raw)
	assert.NotContains(t, content, "secret-pwd")
	assert.NotContains(t, content, "secret-token")
}
