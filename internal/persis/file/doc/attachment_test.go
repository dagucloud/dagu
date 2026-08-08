// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package doc

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core/docs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func attachmentBlobCount(t *testing.T, store *Store) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(store.dataDir, docAttachmentsDirName))
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	return len(entries)
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
	store := newTestStoreWithRevisions(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "doc", "body"))

	attachment, err := store.PutAttachment(ctx, "doc", "logo.png", strings.NewReader("png-bytes"))
	require.NoError(t, err)
	assert.Equal(t, "logo.png", attachment.Name)
	assert.Equal(t, int64(len("png-bytes")), attachment.Size)

	assert.Equal(t, "png-bytes", readAttachment(t, store, "doc", "logo.png"))
}

func TestAttachmentReplaceSameName(t *testing.T) {
	store := newTestStoreWithRevisions(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "doc", "body"))
	_, err := store.PutAttachment(ctx, "doc", "logo.png", strings.NewReader("v1"))
	require.NoError(t, err)
	_, err = store.PutAttachment(ctx, "doc", "logo.png", strings.NewReader("v2"))
	require.NoError(t, err)

	assert.Equal(t, "v2", readAttachment(t, store, "doc", "logo.png"))
	assert.Equal(t, 1, attachmentBlobCount(t, store))
}

func TestAttachmentRequiresDoc(t *testing.T) {
	store := newTestStoreWithRevisions(t)

	_, err := store.PutAttachment(context.Background(), "missing", "logo.png", strings.NewReader("x"))
	assert.ErrorIs(t, err, docs.ErrDocNotFound)
}

func TestAttachmentNameValidation(t *testing.T) {
	store := newTestStoreWithRevisions(t)
	ctx := context.Background()
	require.NoError(t, store.Create(ctx, "doc", "body"))

	for _, name := range []string{"", "a/b", "../escape", ".hidden", "trailing.", "con"} {
		_, err := store.PutAttachment(ctx, "doc", name, strings.NewReader("x"))
		assert.ErrorIs(t, err, docs.ErrInvalidAttachmentName, "name %q", name)
	}
}

func TestAttachmentRenameCarries(t *testing.T) {
	store := newTestStoreWithRevisions(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "old", "body"))
	_, err := store.PutAttachment(ctx, "old", "logo.png", strings.NewReader("x"))
	require.NoError(t, err)
	require.NoError(t, store.Rename(ctx, "old", "sub/new"))

	assert.Equal(t, "x", readAttachment(t, store, "sub/new", "logo.png"))
	_, _, err = store.OpenAttachment(ctx, "old", "logo.png")
	assert.ErrorIs(t, err, docs.ErrDocAttachmentNotFound)
}

func TestAttachmentDeletePurges(t *testing.T) {
	store := newTestStoreWithRevisions(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "doc", "body"))
	_, err := store.PutAttachment(ctx, "doc", "logo.png", strings.NewReader("x"))
	require.NoError(t, err)
	require.NoError(t, store.Delete(ctx, "doc"))

	_, _, err = store.OpenAttachment(ctx, "doc", "logo.png")
	assert.ErrorIs(t, err, docs.ErrDocAttachmentNotFound)
	assert.Equal(t, 0, attachmentBlobCount(t, store))
}

func TestAttachmentDisabledWithoutDataDir(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	require.NoError(t, store.Create(ctx, "doc", "body"))

	_, err := store.PutAttachment(ctx, "doc", "logo.png", strings.NewReader("x"))
	assert.ErrorIs(t, err, docs.ErrDocAttachmentNotFound)
}
