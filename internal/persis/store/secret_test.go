// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	"github.com/dagucloud/dagu/internal/secret"
)

func newSecretStore(t *testing.T) *store.SecretStore {
	t.Helper()
	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)
	col := testutil.NewMemoryBackend().Collection("secrets")
	s, err := store.NewSecretStore(col, enc)
	require.NoError(t, err)
	return s
}

func newSecret(workspace, ref string) *secret.Secret {
	now := time.Now().UTC()
	sec, _ := secret.New(secret.CreateInput{
		Workspace:    workspace,
		Ref:          ref,
		ProviderType: secret.ProviderDaguManaged,
		CreatedBy:    "test",
	}, now)
	return sec
}

func TestSecretCreate(t *testing.T) {
	ctx := context.Background()
	s := newSecretStore(t)
	sec := newSecret("payments", "prod/db-pass")

	require.NoError(t, s.Create(ctx, sec, nil))

	got, err := s.GetByID(ctx, sec.ID)
	require.NoError(t, err)
	assert.Equal(t, sec.ID, got.ID)
	assert.Equal(t, "payments", got.Workspace)
	assert.Equal(t, "prod/db-pass", got.Ref)
}

func TestSecretCreate_WithInitialValue(t *testing.T) {
	ctx := context.Background()
	s := newSecretStore(t)
	sec := newSecret("payments", "prod/db-pass")

	require.NoError(t, s.Create(ctx, sec, &secret.WriteValueInput{
		Value:     "my-secret",
		CreatedBy: "alice",
	}))

	got, err := s.GetByID(ctx, sec.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, got.CurrentVersion)
	assert.NotNil(t, got.LastRotatedAt)
}

func TestSecretCreate_DuplicateRef(t *testing.T) {
	ctx := context.Background()
	s := newSecretStore(t)

	require.NoError(t, s.Create(ctx, newSecret("payments", "prod/db-pass"), nil))

	dup := newSecret("payments", "prod/db-pass")
	assert.ErrorIs(t, s.Create(ctx, dup, nil), secret.ErrAlreadyExists)
}

func TestSecretCreate_SameRefDifferentWorkspace(t *testing.T) {
	ctx := context.Background()
	s := newSecretStore(t)

	require.NoError(t, s.Create(ctx, newSecret("ws-a", "shared/key"), nil))
	require.NoError(t, s.Create(ctx, newSecret("ws-b", "shared/key"), nil))
}

func TestSecretCreate_EmptyWorkspaceNormalizesToGlobal(t *testing.T) {
	ctx := context.Background()
	s := newSecretStore(t)
	sec := newSecret("", "prod/key")

	require.NoError(t, s.Create(ctx, sec, nil))

	got, err := s.GetByRef(ctx, "", "prod/key")
	require.NoError(t, err)
	assert.Equal(t, secret.GlobalWorkspace, got.Workspace)

	dup := newSecret(secret.GlobalWorkspace, "prod/key")
	assert.ErrorIs(t, s.Create(ctx, dup, nil), secret.ErrAlreadyExists)
}

func TestSecretGetByID_NotFound(t *testing.T) {
	ctx := context.Background()
	_, err := newSecretStore(t).GetByID(ctx, "missing")
	assert.ErrorIs(t, err, secret.ErrNotFound)
}

func TestSecretGetByRef(t *testing.T) {
	ctx := context.Background()
	s := newSecretStore(t)
	sec := newSecret("ops", "infra/token")
	require.NoError(t, s.Create(ctx, sec, nil))

	got, err := s.GetByRef(ctx, "ops", "infra/token")
	require.NoError(t, err)
	assert.Equal(t, sec.ID, got.ID)
}

func TestSecretGetByRef_NotFound(t *testing.T) {
	ctx := context.Background()
	_, err := newSecretStore(t).GetByRef(ctx, "any", "no/such")
	assert.ErrorIs(t, err, secret.ErrNotFound)
}

func TestSecretList(t *testing.T) {
	ctx := context.Background()
	s := newSecretStore(t)
	require.NoError(t, s.Create(ctx, newSecret("ws-a", "key-1"), nil))
	require.NoError(t, s.Create(ctx, newSecret("ws-a", "key-2"), nil))
	require.NoError(t, s.Create(ctx, newSecret("ws-b", "key-1"), nil))

	all, err := s.List(ctx, secret.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, all, 3)

	wsA := "ws-a"
	filtered, err := s.List(ctx, secret.ListOptions{Workspace: &wsA})
	require.NoError(t, err)
	assert.Len(t, filtered, 2)
}

func TestSecretUpdate(t *testing.T) {
	ctx := context.Background()
	s := newSecretStore(t)
	sec := newSecret("ws-a", "key-1")
	require.NoError(t, s.Create(ctx, sec, nil))

	sec.Description = "updated"
	require.NoError(t, s.Update(ctx, sec))

	got, err := s.GetByID(ctx, sec.ID)
	require.NoError(t, err)
	assert.Equal(t, "updated", got.Description)
}

func TestSecretUpdate_NotFound(t *testing.T) {
	ctx := context.Background()
	assert.ErrorIs(t, newSecretStore(t).Update(ctx, newSecret("ws", "k")), secret.ErrNotFound)
}

func TestSecretUpdate_RefChange(t *testing.T) {
	ctx := context.Background()
	s := newSecretStore(t)
	sec := newSecret("ws", "old-ref")
	require.NoError(t, s.Create(ctx, sec, nil))

	sec.Ref = "new-ref"
	require.NoError(t, s.Update(ctx, sec))

	_, err := s.GetByRef(ctx, "ws", "old-ref")
	assert.ErrorIs(t, err, secret.ErrNotFound)

	got, err := s.GetByRef(ctx, "ws", "new-ref")
	require.NoError(t, err)
	assert.Equal(t, sec.ID, got.ID)
}

func TestSecretUpdate_RefConflict(t *testing.T) {
	ctx := context.Background()
	s := newSecretStore(t)
	require.NoError(t, s.Create(ctx, newSecret("ws", "ref-a"), nil))
	b := newSecret("ws", "ref-b")
	require.NoError(t, s.Create(ctx, b, nil))

	b.Ref = "ref-a"
	assert.ErrorIs(t, s.Update(ctx, b), secret.ErrAlreadyExists)
}

func TestSecretDelete(t *testing.T) {
	ctx := context.Background()
	s := newSecretStore(t)
	sec := newSecret("ws", "my-ref")
	require.NoError(t, s.Create(ctx, sec, nil))

	require.NoError(t, s.Delete(ctx, sec.ID))

	_, err := s.GetByID(ctx, sec.ID)
	assert.ErrorIs(t, err, secret.ErrNotFound)

	_, err = s.GetByRef(ctx, "ws", "my-ref")
	assert.ErrorIs(t, err, secret.ErrNotFound)
}

func TestSecretDelete_NotFound(t *testing.T) {
	ctx := context.Background()
	assert.ErrorIs(t, newSecretStore(t).Delete(ctx, "nope"), secret.ErrNotFound)
}

func TestSecretWriteValue(t *testing.T) {
	ctx := context.Background()
	s := newSecretStore(t)
	sec := newSecret("ws", "db-pass")
	require.NoError(t, s.Create(ctx, sec, nil))

	updated, err := s.WriteValue(ctx, sec.ID, secret.WriteValueInput{
		Value:     "v1",
		CreatedBy: "alice",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, updated.CurrentVersion)

	updated2, err := s.WriteValue(ctx, sec.ID, secret.WriteValueInput{
		Value:     "v2",
		CreatedBy: "alice",
	})
	require.NoError(t, err)
	assert.Equal(t, 2, updated2.CurrentVersion)
}

func TestSecretGetCurrentVersion(t *testing.T) {
	ctx := context.Background()
	s := newSecretStore(t)
	sec := newSecret("ws", "db-pass")
	require.NoError(t, s.Create(ctx, sec, &secret.WriteValueInput{Value: "val", CreatedBy: "alice"}))

	meta, err := s.GetCurrentVersion(ctx, sec.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, meta.Version)
	assert.Equal(t, sec.ID, meta.SecretID)
}

func TestSecretGetCurrentVersion_NoValue(t *testing.T) {
	ctx := context.Background()
	s := newSecretStore(t)
	sec := newSecret("ws", "empty")
	require.NoError(t, s.Create(ctx, sec, nil))

	_, err := s.GetCurrentVersion(ctx, sec.ID)
	assert.ErrorIs(t, err, secret.ErrNoValue)
}

func TestSecretResolveValue(t *testing.T) {
	ctx := context.Background()
	s := newSecretStore(t)
	sec := newSecret("ws", "db-pass")
	require.NoError(t, s.Create(ctx, sec, &secret.WriteValueInput{Value: "plaintext", CreatedBy: "alice"}))

	value, meta, err := s.ResolveValue(ctx, sec.ID)
	require.NoError(t, err)
	assert.Equal(t, "plaintext", value)
	assert.Equal(t, 1, meta.Version)
}

func TestSecretResolveValue_Disabled(t *testing.T) {
	ctx := context.Background()
	s := newSecretStore(t)
	sec := newSecret("ws", "db-pass")
	require.NoError(t, s.Create(ctx, sec, &secret.WriteValueInput{Value: "val", CreatedBy: "alice"}))

	sec.Status = secret.StatusDisabled
	require.NoError(t, s.Update(ctx, sec))

	_, _, err := s.ResolveValue(ctx, sec.ID)
	assert.ErrorIs(t, err, secret.ErrDisabled)
}

func TestSecretIndexRebuiltOnStartup(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("secrets")
	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)

	s1, err := store.NewSecretStore(col, enc)
	require.NoError(t, err)
	require.NoError(t, s1.Create(ctx, newSecret("ws", "ref-a"), nil))
	require.NoError(t, s1.Create(ctx, newSecret("ws", "ref-b"), nil))

	s2, err := store.NewSecretStore(col, enc)
	require.NoError(t, err)

	got, err := s2.GetByRef(ctx, "ws", "ref-a")
	require.NoError(t, err)
	assert.Equal(t, "ref-a", got.Ref)

	dup := newSecret("ws", "ref-a")
	assert.ErrorIs(t, s2.Create(ctx, dup, nil), secret.ErrAlreadyExists)
}
