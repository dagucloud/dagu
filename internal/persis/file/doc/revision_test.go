// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package doc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/docs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStoreWithRevisions(t *testing.T) *Store {
	t.Helper()
	store, err := New(t.TempDir(), WithDataDir(t.TempDir()))
	require.NoError(t, err)
	return store
}

func revisionBlobCount(t *testing.T, store *Store) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(store.dataDir, docRevisionsDirName))
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	return len(entries)
}

func TestRevisionSnapshotOnUpdate(t *testing.T) {
	store := newTestStoreWithRevisions(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "doc", "v1"))
	require.NoError(t, store.Update(ctx, "doc", "v2"))
	require.NoError(t, store.Update(ctx, "doc", "v3"))

	revisions, err := store.ListRevisions(ctx, "doc")
	require.NoError(t, err)
	require.Len(t, revisions, 2)
	assert.Empty(t, revisions[0].Content)

	// Newest first: the latest snapshot holds v2, the older one v1.
	newest, err := store.GetRevision(ctx, "doc", revisions[0].Rev)
	require.NoError(t, err)
	assert.Equal(t, "v2", newest.Content)
	oldest, err := store.GetRevision(ctx, "doc", revisions[1].Rev)
	require.NoError(t, err)
	assert.Equal(t, "v1", oldest.Content)
}

func TestRevisionSnapshotsEmptyPriorContent(t *testing.T) {
	store := newTestStoreWithRevisions(t)
	ctx := context.Background()

	// The first save of a document created empty must still enter history.
	require.NoError(t, store.Create(ctx, "doc", ""))
	require.NoError(t, store.Update(ctx, "doc", "v1"))

	revisions, err := store.ListRevisions(ctx, "doc")
	require.NoError(t, err)
	require.Len(t, revisions, 1)

	revision, err := store.GetRevision(ctx, "doc", revisions[0].Rev)
	require.NoError(t, err)
	assert.Empty(t, revision.Content)
}

func TestRevisionSkipsUnchangedContent(t *testing.T) {
	store := newTestStoreWithRevisions(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "doc", "same"))
	require.NoError(t, store.Update(ctx, "doc", "same"))

	revisions, err := store.ListRevisions(ctx, "doc")
	require.NoError(t, err)
	assert.Empty(t, revisions)
}

func TestRevisionLimitPrunesBlobs(t *testing.T) {
	store := newTestStoreWithRevisions(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "doc", "v0"))
	for i := range docRevisionLimit + 5 {
		require.NoError(t, store.Update(ctx, "doc", "v"+string(rune('a'+i))))
	}

	revisions, err := store.ListRevisions(ctx, "doc")
	require.NoError(t, err)
	assert.Len(t, revisions, docRevisionLimit)
	assert.Equal(t, docRevisionLimit, revisionBlobCount(t, store))
}

func TestRevisionRenameCarriesHistory(t *testing.T) {
	store := newTestStoreWithRevisions(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "old", "v1"))
	require.NoError(t, store.Update(ctx, "old", "v2"))
	require.NoError(t, store.Rename(ctx, "old", "sub/new"))

	revisions, err := store.ListRevisions(ctx, "sub/new")
	require.NoError(t, err)
	require.Len(t, revisions, 1)

	old, err := store.ListRevisions(ctx, "old")
	require.NoError(t, err)
	assert.Empty(t, old)
}

func TestRevisionDirectoryRenameCarriesHistory(t *testing.T) {
	store := newTestStoreWithRevisions(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "dir/doc", "v1"))
	require.NoError(t, store.Update(ctx, "dir/doc", "v2"))
	require.NoError(t, store.Rename(ctx, "dir", "renamed"))

	revisions, err := store.ListRevisions(ctx, "renamed/doc")
	require.NoError(t, err)
	assert.Len(t, revisions, 1)
}

func TestRevisionDeletePurgesBlobs(t *testing.T) {
	store := newTestStoreWithRevisions(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "doc", "v1"))
	require.NoError(t, store.Update(ctx, "doc", "v2"))
	require.Equal(t, 1, revisionBlobCount(t, store))

	require.NoError(t, store.Delete(ctx, "doc"))

	revisions, err := store.ListRevisions(ctx, "doc")
	require.NoError(t, err)
	assert.Empty(t, revisions)
	assert.Equal(t, 0, revisionBlobCount(t, store))
}

func TestRevisionsDisabledWithoutDataDir(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "doc", "v1"))
	require.NoError(t, store.Update(ctx, "doc", "v2"))

	revisions, err := store.ListRevisions(ctx, "doc")
	require.NoError(t, err)
	assert.Empty(t, revisions)

	_, err = store.GetRevision(ctx, "doc", "any")
	assert.ErrorIs(t, err, docs.ErrDocRevisionNotFound)
}

func TestGetRevisionRejectsUnsafeName(t *testing.T) {
	store := newTestStoreWithRevisions(t)

	_, err := store.GetRevision(context.Background(), "doc", "../escape")
	assert.ErrorIs(t, err, docs.ErrDocRevisionNotFound)
}
