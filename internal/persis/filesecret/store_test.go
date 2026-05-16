// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package filesecret

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/secret"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_CreateManagedSecretEncryptsValue(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	now := time.Date(2026, 5, 14, 1, 2, 3, 0, time.UTC)

	sec, err := secret.New(secret.CreateInput{
		Workspace:    "payments",
		Ref:          "prod/db-password",
		Description:  "Primary database credential",
		ProviderType: secret.ProviderDaguManaged,
		CreatedBy:    "alice",
	}, now)
	require.NoError(t, err)

	err = store.Create(ctx, sec, &secret.WriteValueInput{
		Value:     "plain-secret-value",
		CreatedBy: "alice",
		CreatedAt: now,
	})
	require.NoError(t, err)

	got, err := store.GetByRef(ctx, "payments", "prod/db-password")
	require.NoError(t, err)
	assert.Equal(t, sec.ID, got.ID)
	assert.Equal(t, 1, got.CurrentVersion)
	assert.NotNil(t, got.LastRotatedAt)

	value, version, err := store.ResolveValue(ctx, got.ID)
	require.NoError(t, err)
	assert.Equal(t, "plain-secret-value", value)
	assert.Equal(t, 1, version.Version)

	files, err := os.ReadDir(store.baseDir)
	require.NoError(t, err)
	require.Len(t, files, 1)
	data, err := os.ReadFile(filepath.Join(store.baseDir, files[0].Name())) //nolint:gosec // test path
	require.NoError(t, err)
	assert.Contains(t, string(data), `"Ref": "prod/db-password"`)
	assert.NotContains(t, string(data), `"Path"`)
	assert.NotContains(t, string(data), `"DisplayName"`)
	assert.NotContains(t, string(data), `"DeploymentStage"`)
	assert.NotContains(t, string(data), `"Tags"`)
	assert.NotContains(t, string(data), "plain-secret-value")
}

func TestStore_EnforcesWorkspaceRefUniqueness(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	now := time.Date(2026, 5, 14, 1, 2, 3, 0, time.UTC)

	first, err := secret.New(secret.CreateInput{
		Workspace:    "payments",
		Ref:          "prod/db-password",
		ProviderType: secret.ProviderDaguManaged,
		CreatedBy:    "alice",
	}, now)
	require.NoError(t, err)
	require.NoError(t, store.Create(ctx, first, nil))

	duplicate, err := secret.New(secret.CreateInput{
		Workspace:    "payments",
		Ref:          "prod/db-password",
		ProviderType: secret.ProviderDaguManaged,
		CreatedBy:    "bob",
	}, now)
	require.NoError(t, err)

	err = store.Create(ctx, duplicate, nil)
	require.ErrorIs(t, err, secret.ErrAlreadyExists)

	otherWorkspace, err := secret.New(secret.CreateInput{
		Workspace:    "ops",
		Ref:          "prod/db-password",
		ProviderType: secret.ProviderDaguManaged,
		CreatedBy:    "bob",
	}, now)
	require.NoError(t, err)
	require.NoError(t, store.Create(ctx, otherWorkspace, nil))
}

func TestStore_WriteValueIncrementsVersion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	now := time.Date(2026, 5, 14, 1, 2, 3, 0, time.UTC)

	sec, err := secret.New(secret.CreateInput{
		Workspace:    "payments",
		Ref:          "prod/db-password",
		ProviderType: secret.ProviderDaguManaged,
		CreatedBy:    "alice",
	}, now)
	require.NoError(t, err)
	require.NoError(t, store.Create(ctx, sec, &secret.WriteValueInput{
		Value:     "first",
		CreatedBy: "alice",
		CreatedAt: now,
	}))

	updated, err := store.WriteValue(ctx, sec.ID, secret.WriteValueInput{
		Value:     "second",
		CreatedBy: "alice",
		CreatedAt: now.Add(time.Minute),
	})
	require.NoError(t, err)
	assert.Equal(t, 2, updated.CurrentVersion)

	value, version, err := store.ResolveValue(ctx, sec.ID)
	require.NoError(t, err)
	assert.Equal(t, "second", value)
	assert.Equal(t, 2, version.Version)
}

func newTestStore(t *testing.T) *Store {
	t.Helper()

	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)

	store, err := New(t.TempDir(), enc)
	require.NoError(t, err)
	return store
}
