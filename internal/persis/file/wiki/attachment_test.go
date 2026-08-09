// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package wiki

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wikimodel "github.com/dagucloud/dagu/v2/internal/wiki"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func attachmentDiskPath(store *Store, pageID, name string) string {
	return filepath.Join(store.baseDir, wikiPageAttachmentsDirName, filepath.FromSlash(pageID), name)
}

func readAttachment(t *testing.T, store *Store, id, name string) string {
	t.Helper()
	reader, _, err := store.OpenAttachment(context.Background(), id, name)
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	return string(data)
}

func TestAttachmentRoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "page", "body"))

	attachment, err := store.PutAttachment(ctx, "page", "logo.png", strings.NewReader("png-bytes"))
	require.NoError(t, err)
	assert.Equal(t, "logo.png", attachment.Name)
	assert.Equal(t, int64(len("png-bytes")), attachment.Size)

	assert.Equal(t, "png-bytes", readAttachment(t, store, "page", "logo.png"))

	// The blob lives at the deterministic location git sync mirrors.
	data, err := os.ReadFile(attachmentDiskPath(store, "page", "logo.png"))
	require.NoError(t, err)
	assert.Equal(t, "png-bytes", string(data))
}

func TestAttachmentReplaceSameName(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "page", "body"))
	_, err := store.PutAttachment(ctx, "page", "logo.png", strings.NewReader("v1"))
	require.NoError(t, err)
	_, err = store.PutAttachment(ctx, "page", "logo.png", strings.NewReader("v2"))
	require.NoError(t, err)

	assert.Equal(t, "v2", readAttachment(t, store, "page", "logo.png"))
}

func TestAttachmentRequiresWikiPage(t *testing.T) {
	store := newTestStore(t)

	_, err := store.PutAttachment(context.Background(), "missing", "logo.png", strings.NewReader("x"))
	assert.ErrorIs(t, err, wikimodel.ErrPageNotFound)
}

func TestAttachmentNameValidation(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	require.NoError(t, store.Create(ctx, "page", "body"))

	invalid := []string{
		"", "a/b", "../escape", ".hidden", "trailing.", "con",
		// Reserved extensions: attachments must never look like pages or DAGs.
		"note.md", "note.MD", "flow.yaml", "flow.yml",
	}
	for _, name := range invalid {
		_, err := store.PutAttachment(ctx, "page", name, strings.NewReader("x"))
		assert.ErrorIs(t, err, wikimodel.ErrInvalidAttachmentName, "name %q", name)
	}
}

func TestAttachmentInvisibleToPageIndex(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "page", "needle"))
	_, err := store.PutAttachment(ctx, "page", "logo.png", strings.NewReader("x"))
	require.NoError(t, err)

	flat, err := store.ListFlat(ctx, defaultFlatOpts(1, 50))
	require.NoError(t, err)
	require.Len(t, flat.Items, 1)
	assert.Equal(t, "page", flat.Items[0].ID)

	tree, err := store.List(ctx, defaultListOpts(1, 50))
	require.NoError(t, err)
	require.Len(t, tree.Items, 1)
	assert.Equal(t, "page", tree.Items[0].ID)
}

func TestAttachmentRenameCarries(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "old", "body"))
	_, err := store.PutAttachment(ctx, "old", "logo.png", strings.NewReader("x"))
	require.NoError(t, err)
	require.NoError(t, store.Rename(ctx, "old", "sub/new"))

	assert.Equal(t, "x", readAttachment(t, store, "sub/new", "logo.png"))
	_, _, err = store.OpenAttachment(ctx, "old", "logo.png")
	assert.ErrorIs(t, err, wikimodel.ErrPageAttachmentNotFound)
}

func TestAttachmentDirectoryRenameCarries(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "guides/deploy", "body"))
	_, err := store.PutAttachment(ctx, "guides/deploy", "logo.png", strings.NewReader("x"))
	require.NoError(t, err)
	require.NoError(t, store.Rename(ctx, "guides", "renamed"))

	assert.Equal(t, "x", readAttachment(t, store, "renamed/deploy", "logo.png"))
}

func TestAttachmentDirectoryRenameMergesNonEmptyStaleTarget(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Stale target holding a NON-EMPTY per-page subdirectory at the rename
	// destination: the merge must descend rather than fail.
	stale := attachmentDiskPath(store, "new/page", "stale.png")
	require.NoError(t, os.MkdirAll(filepath.Dir(stale), 0750))
	require.NoError(t, os.WriteFile(stale, []byte("stale"), 0600))

	require.NoError(t, store.Create(ctx, "old/page", "body"))
	_, err := store.PutAttachment(ctx, "old/page", "fresh.png", strings.NewReader("fresh"))
	require.NoError(t, err)
	require.NoError(t, store.Rename(ctx, "old", "new"))

	assert.Equal(t, "fresh", readAttachment(t, store, "new/page", "fresh.png"))
	assert.Equal(t, "stale", readAttachment(t, store, "new/page", "stale.png"))
	_, statErr := os.Lstat(filepath.Join(store.baseDir, wikiPageAttachmentsDirName, "old"))
	assert.True(t, os.IsNotExist(statErr))
}

func TestAttachmentRenameMergesStaleTarget(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Stale leftover directory at the rename target.
	stalePath := attachmentDiskPath(store, "new", "stale.png")
	require.NoError(t, os.MkdirAll(filepath.Dir(stalePath), 0750))
	require.NoError(t, os.WriteFile(stalePath, []byte("stale"), 0600))

	require.NoError(t, store.Create(ctx, "old", "body"))
	_, err := store.PutAttachment(ctx, "old", "logo.png", strings.NewReader("fresh"))
	require.NoError(t, err)
	require.NoError(t, store.Rename(ctx, "old", "new"))

	assert.Equal(t, "fresh", readAttachment(t, store, "new", "logo.png"))
	assert.Equal(t, "stale", readAttachment(t, store, "new", "stale.png"))
}

func TestAttachmentDeletePurges(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "page", "body"))
	_, err := store.PutAttachment(ctx, "page", "logo.png", strings.NewReader("x"))
	require.NoError(t, err)
	require.NoError(t, store.Delete(ctx, "page"))

	_, _, err = store.OpenAttachment(ctx, "page", "logo.png")
	assert.ErrorIs(t, err, wikimodel.ErrPageAttachmentNotFound)
	_, statErr := os.Lstat(attachmentDiskPath(store, "page", "logo.png"))
	assert.True(t, os.IsNotExist(statErr))
}

func TestAttachmentDirectoryDeletePurges(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "guides/deploy", "body"))
	_, err := store.PutAttachment(ctx, "guides/deploy", "logo.png", strings.NewReader("x"))
	require.NoError(t, err)
	require.NoError(t, store.Delete(ctx, "guides"))

	_, _, err = store.OpenAttachment(ctx, "guides/deploy", "logo.png")
	assert.ErrorIs(t, err, wikimodel.ErrPageAttachmentNotFound)
}
