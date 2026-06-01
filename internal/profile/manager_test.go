// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package profile_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/cmn/crypto"
	persiststore "github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	"github.com/dagucloud/dagu/internal/profile"
	secretpkg "github.com/dagucloud/dagu/internal/secret"
)

func TestManagerSetSecretCreatesManagedSecretAndProfileEntry(t *testing.T) {
	ctx := context.Background()
	profileStore, secretStore := newProfileManagerStores(t)
	manager := profile.NewManager(profileStore, secretStore)

	prof, err := profile.New(profile.CreateInput{Name: "local", CreatedBy: "alice"}, managerTestNow())
	require.NoError(t, err)
	require.NoError(t, profileStore.Create(ctx, prof))

	updated, err := manager.SetSecret(ctx, prof, "API_TOKEN", "super-secret", "alice")
	require.NoError(t, err)

	require.Len(t, updated.Entries, 1)
	entry := updated.Entries[0]
	assert.Equal(t, "API_TOKEN", entry.Key)
	assert.Equal(t, profile.EntryKindSecret, entry.Kind)
	assert.NotEmpty(t, entry.SecretID)
	assert.Empty(t, entry.Value)

	sec, err := secretStore.GetByRef(ctx, "", profile.SecretRef("local", "API_TOKEN"))
	require.NoError(t, err)
	assert.Equal(t, entry.SecretID, sec.ID)

	value, version, err := secretStore.ResolveValue(ctx, entry.SecretID)
	require.NoError(t, err)
	assert.Equal(t, "super-secret", value)
	assert.Equal(t, 1, version.Version)
}

func TestManagerSetSecretDoesNotRotateWhenProfileUpdateFails(t *testing.T) {
	ctx := context.Background()
	profileStore, secretStore := newProfileManagerStores(t)

	prof, err := profile.New(profile.CreateInput{Name: "local", CreatedBy: "alice"}, managerTestNow())
	require.NoError(t, err)
	require.NoError(t, profileStore.Create(ctx, prof))

	sec, err := secretpkg.New(secretpkg.CreateInput{
		Workspace:    "",
		Ref:          profile.SecretRef("local", "API_TOKEN"),
		ProviderType: secretpkg.ProviderDaguManaged,
		CreatedBy:    "alice",
	}, managerTestNow())
	require.NoError(t, err)
	require.NoError(t, secretStore.Create(ctx, sec, &secretpkg.WriteValueInput{
		Value:     "old-value",
		CreatedBy: "alice",
	}))

	manager := profile.NewManager(failingProfileUpdateStore{
		Store: profileStore,
		err:   fmt.Errorf("forced profile update failure"),
	}, secretStore)

	_, err = manager.SetSecret(ctx, prof, "API_TOKEN", "new-value", "alice")
	require.Error(t, err)

	value, _, err := secretStore.ResolveValue(ctx, sec.ID)
	require.NoError(t, err)
	assert.Equal(t, "old-value", value)
}

func TestManagerEnsureRunnableRejectsDisabledProfiles(t *testing.T) {
	ctx := context.Background()
	profileStore, secretStore := newProfileManagerStores(t)
	manager := profile.NewManager(profileStore, secretStore)

	prof, err := profile.New(profile.CreateInput{Name: "local", CreatedBy: "alice"}, managerTestNow())
	require.NoError(t, err)
	require.NoError(t, prof.SetStatus(profile.StatusDisabled, "alice", managerTestNow()))
	require.NoError(t, profileStore.Create(ctx, prof))

	_, err = manager.EnsureRunnable(ctx, "local")
	assert.ErrorIs(t, err, profile.ErrDisabled)
}

func newProfileManagerStores(t *testing.T) (profile.Store, secretpkg.Store) {
	t.Helper()

	backend := testutil.NewMemoryBackend()
	profileStore, err := persiststore.NewProfileStore(backend.Collection("profiles"))
	require.NoError(t, err)

	enc, err := crypto.NewEncryptor("test-key-for-profile-manager")
	require.NoError(t, err)
	secretStore, err := persiststore.NewSecretStore(backend.Collection("secrets"), enc)
	require.NoError(t, err)

	return profileStore, secretStore
}

func managerTestNow() time.Time {
	return time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
}

type failingProfileUpdateStore struct {
	profile.Store
	err error
}

func (s failingProfileUpdateStore) Update(context.Context, *profile.Profile) error {
	return s.err
}
