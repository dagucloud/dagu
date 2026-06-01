// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package profile_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	"github.com/dagucloud/dagu/internal/profile"
	"github.com/dagucloud/dagu/internal/secret"
)

func TestResolverResolvesVariablesAndSecrets(t *testing.T) {
	ctx := context.Background()

	profileStore, err := store.NewProfileStore(testutil.NewMemoryBackend().Collection("profiles"))
	require.NoError(t, err)
	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)
	secretStore, err := store.NewSecretStore(testutil.NewMemoryBackend().Collection("secrets"), enc)
	require.NoError(t, err)

	sec, err := secret.New(secret.CreateInput{
		Workspace:    secret.GlobalWorkspace,
		Ref:          "profile/local/clickhouse-dsn-py",
		ProviderType: secret.ProviderDaguManaged,
		CreatedBy:    "alice",
	}, time.Now())
	require.NoError(t, err)
	require.NoError(t, secretStore.Create(ctx, sec, &secret.WriteValueInput{
		Value:     "clickhouse://local",
		CreatedBy: "alice",
	}))

	p, err := profile.New(profile.CreateInput{Name: "local", CreatedBy: "alice"}, time.Now())
	require.NoError(t, err)
	require.NoError(t, p.SetVariable("LOG_LEVEL", "debug", "alice", time.Now()))
	require.NoError(t, p.SetSecret("CLICKHOUSE_DSN_PY", sec.ID, "alice", time.Now()))
	require.NoError(t, profileStore.Create(ctx, p))

	resolved, err := profile.NewResolver(profileStore, secretStore).Resolve(ctx, "local")
	require.NoError(t, err)

	assert.Equal(t, "local", resolved.Name)
	assert.Equal(t, map[string]string{"LOG_LEVEL": "debug"}, resolved.Variables)
	assert.Equal(t, map[string]string{"CLICKHOUSE_DSN_PY": "clickhouse://local"}, resolved.Secrets)
	assert.Equal(t, []profile.ResolvedEntry{
		{Key: "LOG_LEVEL", Kind: profile.EntryKindVariable},
		{Key: "CLICKHOUSE_DSN_PY", Kind: profile.EntryKindSecret},
	}, resolved.Entries)
	assert.Equal(t, []string{"LOG_LEVEL=debug"}, resolved.EnvVars(profile.EntryKindVariable))
	assert.Equal(t, []string{"CLICKHOUSE_DSN_PY=clickhouse://local"}, resolved.EnvVars(profile.EntryKindSecret))
}

func TestResolverFailsForDisabledProfile(t *testing.T) {
	ctx := context.Background()
	profileStore, err := store.NewProfileStore(testutil.NewMemoryBackend().Collection("profiles"))
	require.NoError(t, err)

	p, err := profile.New(profile.CreateInput{Name: "local"}, time.Now())
	require.NoError(t, err)
	require.NoError(t, p.SetStatus(profile.StatusDisabled, "alice", time.Now()))
	require.NoError(t, profileStore.Create(ctx, p))

	_, err = profile.NewResolver(profileStore, nil).Resolve(ctx, "local")
	assert.ErrorIs(t, err, profile.ErrDisabled)
}
