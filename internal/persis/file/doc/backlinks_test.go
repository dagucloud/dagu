// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package doc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBacklinks(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "target", "the linked doc"))
	require.NoError(t, store.Create(ctx, "linker-a", "see [[target]]"))
	require.NoError(t, store.Create(ctx, "linker-b", "see [[target#section|label]]"))
	require.NoError(t, store.Create(ctx, "unrelated", "see [[other]]"))
	require.NoError(t, store.Create(ctx, "code-only", "```\n[[target]]\n```"))

	results, err := store.Backlinks(ctx, "target", "")
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, "linker-a", results[0].ID)
	assert.Equal(t, "linker-b", results[1].ID)
}

func TestBacklinksWorkspaceRelative(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "ws/guides/target", "the linked doc"))
	// Relative link written from inside the workspace.
	require.NoError(t, store.Create(ctx, "ws/runbooks/linker", "see [[guides/target]]"))
	// Absolute stored-ID link written from outside the workspace.
	require.NoError(t, store.Create(ctx, "outside", "see [[ws/guides/target]]"))
	// Same relative link outside the workspace must not match.
	require.NoError(t, store.Create(ctx, "other/linker", "see [[guides/target]]"))

	results, err := store.Backlinks(ctx, "ws/guides/target", "ws")
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, "outside", results[0].ID)
	assert.Equal(t, "ws/runbooks/linker", results[1].ID)
}

func TestBacklinksSchemeTarget(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "runbook", "status [[dag:daily-etl|ETL]]"))
	require.NoError(t, store.Create(ctx, "other", "no dag links"))

	results, err := store.Backlinks(ctx, "dag:daily-etl", "")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "runbook", results[0].ID)
}

func TestBacklinksRefreshAfterUpdate(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "target", "doc"))
	require.NoError(t, store.Create(ctx, "linker", "no link yet"))

	results, err := store.Backlinks(ctx, "target", "")
	require.NoError(t, err)
	assert.Empty(t, results)

	require.NoError(t, store.Update(ctx, "linker", "now [[target]]"))
	results, err = store.Backlinks(ctx, "target", "")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "linker", results[0].ID)
}

func TestBacklinksEmptyTarget(t *testing.T) {
	store := newTestStore(t)
	results, err := store.Backlinks(context.Background(), "", "")
	require.NoError(t, err)
	assert.Nil(t, results)
}
