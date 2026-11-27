// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package wiki

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	wikimodel "github.com/dagucloud/dagu/v2/internal/wiki"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// historyFixture is a shared store whose directory Ops holds the page
// runbook, with a revision and an attachment, and a store of its own for
// those pages.
type historyFixture struct {
	shared *Store
	own    *Store
}

func newHistoryFixture(t *testing.T) *historyFixture {
	t.Helper()
	ctx := context.Background()
	shared := newTestStoreWithRevisions(t)
	require.NoError(t, shared.Create(ctx, "Ops/runbook", "v1"))
	require.NoError(t, shared.Update(ctx, "Ops/runbook", "v2"))
	_, err := shared.PutAttachment(ctx, "Ops/runbook", "diagram.png", strings.NewReader("png"))
	require.NoError(t, err)
	require.NoError(t, shared.Create(ctx, "home", "x"))

	stateDir := t.TempDir()
	own, err := New(t.TempDir(), WithDataDir(stateDir), WithMetadataDir(stateDir))
	require.NoError(t, err)
	return &historyFixture{shared: shared, own: own}
}

// movePage moves the runbook page file to the store of its own, as an admin
// moving the directory would.
func (f *historyFixture) movePage(t *testing.T) {
	t.Helper()
	require.NoError(t, os.Rename(
		filepath.Join(f.shared.baseDir, "Ops", "runbook.md"),
		filepath.Join(f.own.baseDir, "runbook.md")))
}

func TestMoveHistory(t *testing.T) {
	f := newHistoryFixture(t)
	ctx := context.Background()
	// A creation time the moved file's own times cannot give.
	const createdAt = "2020-01-02T03:04:05Z"
	require.NoError(t, f.shared.setPageCreatedAt("Ops/runbook", createdAt))
	f.movePage(t)

	// The root matches the directory without regard to case.
	require.NoError(t, f.shared.MoveHistory(ctx, "ops", f.own))

	revisions, err := f.own.ListRevisions(ctx, "runbook")
	require.NoError(t, err)
	require.Len(t, revisions, 1)
	revision, err := f.own.GetRevision(ctx, "runbook", revisions[0].Rev)
	require.NoError(t, err)
	assert.Equal(t, "v1", revision.Content)
	assert.Equal(t, "png", readAttachment(t, f.own, "runbook", "diagram.png"))
	page, err := f.own.Get(ctx, "runbook")
	require.NoError(t, err)
	assert.Equal(t, createdAt, page.CreatedAt)

	// Nothing of the moved pages stays in the shared store.
	revisions, err = f.shared.ListRevisions(ctx, "Ops/runbook")
	require.NoError(t, err)
	assert.Empty(t, revisions)
	_, _, err = f.shared.OpenAttachment(ctx, "Ops/runbook", "diagram.png")
	require.ErrorIs(t, err, wikimodel.ErrPageAttachmentNotFound)
	assert.NoDirExists(t, filepath.Join(f.shared.baseDir, wikiPageAttachmentsDirName))
	assert.Equal(t, 1, revisionBlobCount(t, f.own))
	assert.Equal(t, 0, revisionBlobCount(t, f.shared))
	metadata, err := f.shared.loadMetadata()
	require.NoError(t, err)
	assert.Equal(t, []string{"home"}, keys(metadata.CreatedAt))
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestMoveHistoryKeepsDifferentAttachment gives the store of its own an
// attachment of the same name with other content. That one stays, and the
// shared one stays in place with a warning, so neither is lost.
func TestMoveHistoryKeepsDifferentAttachment(t *testing.T) {
	f := newHistoryFixture(t)
	f.movePage(t)
	_, err := f.own.PutAttachment(context.Background(), "runbook", "diagram.png", strings.NewReader("new"))
	require.NoError(t, err)

	var logs bytes.Buffer
	ctx := logger.WithLogger(context.Background(), logger.NewLogger(logger.WithWriter(&logs), logger.WithQuiet()))
	require.NoError(t, f.shared.MoveHistory(ctx, "Ops", f.own))

	assert.Equal(t, "new", readAttachment(t, f.own, "runbook", "diagram.png"))
	assert.Equal(t, "png", readAttachment(t, f.shared, "Ops/runbook", "diagram.png"))
	assert.Contains(t, logs.String(), "diagram.png")
}

// TestMoveHistoryFinishesInterruptedMove replays a move that stopped after
// the attachment and the revisions reached the store of its own.
func TestMoveHistoryFinishesInterruptedMove(t *testing.T) {
	f := newHistoryFixture(t)
	ctx := context.Background()
	f.movePage(t)
	src := attachmentDiskPath(f.shared, "Ops/runbook", "diagram.png")
	dst := attachmentDiskPath(f.own, "runbook", "diagram.png")
	require.NoError(t, os.MkdirAll(filepath.Dir(dst), wikiDirPermissions))
	require.NoError(t, copyFileAtomic(src, dst))
	manifest, err := f.shared.loadRevisionsManifest()
	require.NoError(t, err)
	for _, entry := range manifest["Ops/runbook"] {
		require.NoError(t, copyRevisionBlob(f.shared.revisionBlobPath(entry.File), f.own.revisionBlobPath(entry.File)))
	}
	require.NoError(t, f.own.saveRevisionsManifest(pageRevisionsManifest{"runbook": manifest["Ops/runbook"]}))

	require.NoError(t, f.shared.MoveHistory(ctx, "Ops", f.own))

	revisions, err := f.own.ListRevisions(ctx, "runbook")
	require.NoError(t, err)
	assert.Len(t, revisions, 1)
	assert.Equal(t, "png", readAttachment(t, f.own, "runbook", "diagram.png"))
	assert.NoFileExists(t, src)
	assert.Equal(t, 0, revisionBlobCount(t, f.shared))
}

// TestMoveHistoryCapsRevisions merges revisions the store of its own made
// after the switch with the moved ones, keeping the newest up to the limit.
func TestMoveHistoryCapsRevisions(t *testing.T) {
	f := newHistoryFixture(t)
	ctx := context.Background()
	f.movePage(t)
	for i := range pageRevisionLimit {
		require.NoError(t, f.own.Update(ctx, "runbook", strings.Repeat("x", i+1)))
	}

	require.NoError(t, f.shared.MoveHistory(ctx, "Ops", f.own))

	revisions, err := f.own.ListRevisions(ctx, "runbook")
	require.NoError(t, err)
	assert.Len(t, revisions, pageRevisionLimit)
	for _, revision := range revisions {
		got, err := f.own.GetRevision(ctx, "runbook", revision.Rev)
		require.NoError(t, err)
		assert.NotEqual(t, "v1", got.Content)
	}
	assert.Equal(t, pageRevisionLimit, revisionBlobCount(t, f.own))
}
