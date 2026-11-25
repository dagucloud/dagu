// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package secret

import (
	"context"
	"testing"
	"time"

	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Secrets under the reserved prefix "ai/" belong to AI features. The DAG
// resolver must never return them, from the workspace scope or from the global
// fallback.
func TestReferenceResolver_RefusesReservedRefs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newMemoryStoreForTest()
	now := time.Date(2026, 5, 14, 1, 2, 3, 0, time.UTC)
	// The payments resolver finds ai/endpoint/local in its own scope and the
	// other refs through the global fallback.
	reserved := []struct{ workspace, ref string }{
		{GlobalWorkspace, "ai/endpoint/anthropic"},
		{GlobalWorkspace, "ai/mcp/search/authorization"},
		{GlobalWorkspace, "ai/other"},
		{"payments", "ai/endpoint/local"},
	}
	for _, stored := range reserved {
		sec, err := New(CreateInput{
			Workspace:    stored.workspace,
			Ref:          stored.ref,
			ProviderType: ProviderDaguManaged,
			CreatedBy:    "admin",
		}, now)
		require.NoError(t, err)
		require.NoError(t, store.Create(ctx, sec, &WriteValueInput{
			Value:     "provider-key",
			CreatedBy: "admin",
			CreatedAt: now,
		}))
	}

	for _, workspace := range []string{"", "payments"} {
		resolver := NewReferenceResolver(store, workspace)
		for _, stored := range reserved {
			ref := stored.ref
			r := secretref.Ref{Name: "K", Ref: ref}

			value, err := resolver.ResolveReference(ctx, r)
			require.ErrorIs(t, err, ErrReservedRef, "ResolveReference %q workspace %q", ref, workspace)
			assert.Empty(t, value)

			sec, value, err := resolver.ReadReference(ctx, r)
			require.ErrorIs(t, err, ErrReservedRef, "ReadReference %q workspace %q", ref, workspace)
			assert.Nil(t, sec)
			assert.Empty(t, value)

			require.ErrorIs(t, resolver.CheckReferenceAccessibility(ctx, r), ErrReservedRef,
				"CheckReferenceAccessibility %q workspace %q", ref, workspace)
		}
	}
}

// A ref outside the reserved prefix keeps resolving. A ref that only starts with the
// letters "ai" is not reserved.
func TestReferenceResolver_ResolvesRefsOutsideReservedPrefix(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newMemoryStoreForTest()
	now := time.Date(2026, 5, 14, 1, 2, 3, 0, time.UTC)
	for _, ref := range []string{"aim/key", "ai-tools/key"} {
		sec, err := New(CreateInput{
			Workspace:    GlobalWorkspace,
			Ref:          ref,
			ProviderType: ProviderDaguManaged,
			CreatedBy:    "alice",
		}, now)
		require.NoError(t, err)
		require.NoError(t, store.Create(ctx, sec, &WriteValueInput{Value: "v", CreatedBy: "alice", CreatedAt: now}))

		value, err := NewReferenceResolver(store, "").ResolveReference(ctx, secretref.Ref{Name: "K", Ref: ref})
		require.NoError(t, err, ref)
		assert.Equal(t, "v", value)
	}
}
