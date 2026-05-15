// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package secret

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReferenceResolver_ResolvesDaguManagedValue(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newMemoryStoreForTest()
	now := time.Date(2026, 5, 14, 1, 2, 3, 0, time.UTC)
	sec, err := New(CreateInput{
		Workspace:    "payments",
		Ref:          "prod/db-password",
		ProviderType: ProviderDaguManaged,
		CreatedBy:    "alice",
	}, now)
	require.NoError(t, err)
	require.NoError(t, store.Create(ctx, sec, &WriteValueInput{
		Value:     "managed-secret",
		CreatedBy: "alice",
		CreatedAt: now,
	}))

	resolver := NewReferenceResolver(store, "payments")
	value, err := resolver.ResolveReference(ctx, core.SecretRef{
		Name: "DB_PASSWORD",
		Ref:  "prod/db-password",
	})
	require.NoError(t, err)
	assert.Equal(t, "managed-secret", value)
}

func TestReferenceResolver_DoesNotResolveAcrossWorkspaces(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newMemoryStoreForTest()
	now := time.Date(2026, 5, 14, 1, 2, 3, 0, time.UTC)
	sec, err := New(CreateInput{
		Workspace:    "ops",
		Ref:          "prod/db-password",
		ProviderType: ProviderDaguManaged,
		CreatedBy:    "alice",
	}, now)
	require.NoError(t, err)
	require.NoError(t, store.Create(ctx, sec, &WriteValueInput{
		Value:     "managed-secret",
		CreatedBy: "alice",
		CreatedAt: now,
	}))

	resolver := NewReferenceResolver(store, "payments")
	_, err = resolver.ResolveReference(ctx, core.SecretRef{
		Name: "DB_PASSWORD",
		Ref:  "prod/db-password",
	})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestReferenceResolver_FailsClosedForDisabledSecret(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newMemoryStoreForTest()
	now := time.Date(2026, 5, 14, 1, 2, 3, 0, time.UTC)
	sec, err := New(CreateInput{
		Workspace:    "payments",
		Ref:          "prod/db-password",
		ProviderType: ProviderDaguManaged,
		CreatedBy:    "alice",
	}, now)
	require.NoError(t, err)
	sec.Status = StatusDisabled
	require.NoError(t, store.Create(ctx, sec, &WriteValueInput{
		Value:     "managed-secret",
		CreatedBy: "alice",
		CreatedAt: now,
	}))

	resolver := NewReferenceResolver(store, "payments")
	_, err = resolver.ResolveReference(ctx, core.SecretRef{
		Name: "DB_PASSWORD",
		Ref:  "prod/db-password",
	})
	require.ErrorIs(t, err, ErrDisabled)
}
