// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package doc

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/core/docs"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// defaultListOpts returns default ListDocsOptions for tests.
func defaultListOpts(page, perPage int) docs.ListDocsOptions {
	return docs.ListDocsOptions{Page: page, PerPage: perPage, Sort: docs.DocSortFieldType, Order: docs.DocSortOrderAsc}
}

// defaultFlatOpts returns default ListDocsOptions for flat listing.
func defaultFlatOpts(page, perPage int) docs.ListDocsOptions {
	return docs.ListDocsOptions{Page: page, PerPage: perPage, Sort: docs.DocSortFieldName, Order: docs.DocSortOrderAsc}
}

// setModTime sets the modification time on a file.
func setModTime(t *testing.T, path string, mt time.Time) {
	t.Helper()
	require.NoError(t, os.Chtimes(path, mt, mt))
}

// marshalDocFile produces doc file content with optional frontmatter (test helper).
func marshalDocFile(title, content string) []byte {
	var buf bytes.Buffer
	if title != "" {
		fm := docFrontmatter{Title: title}
		fmBytes, err := yaml.Marshal(fm)
		if err == nil {
			buf.WriteString("---\n")
			buf.Write(fmBytes)
			buf.WriteString("---\n")
		}
	}
	if content != "" {
		buf.WriteString(content)
		if !strings.HasSuffix(content, "\n") {
			buf.WriteString("\n")
		}
	}
	return buf.Bytes()
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	store, err := New(dir)
	require.NoError(t, err)
	return store
}

func TestCreate(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	err := store.Create(ctx, "test-doc", "# Hello\n\nWorld")
	require.NoError(t, err)

	doc, err := store.Get(ctx, "test-doc")
	require.NoError(t, err)
	assert.Equal(t, "test-doc", doc.ID)
	assert.Equal(t, "# Hello\n\nWorld", doc.Content)
}

func TestCreateDuplicate(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "test-doc", "content"))
	err := store.Create(ctx, "test-doc", "content2")
	assert.ErrorIs(t, err, docs.ErrDocAlreadyExists)
}

func TestCreateNested(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	err := store.Create(ctx, "docs/deploy/production", "# Deploy Prod")
	require.NoError(t, err)

	doc, err := store.Get(ctx, "docs/deploy/production")
	require.NoError(t, err)
	assert.Equal(t, "docs/deploy/production", doc.ID)
	assert.Equal(t, "# Deploy Prod", doc.Content)
}

func TestCreateRejectsFileDirectoryNameCollisions(t *testing.T) {
	t.Run("file blocks child document", func(t *testing.T) {
		store := newTestStore(t)
		ctx := context.Background()

		require.NoError(t, store.Create(ctx, "foo", "file content"))
		err := store.Create(ctx, "foo/child", "child content")
		assert.ErrorIs(t, err, docs.ErrDocPathConflict)
	})

	t.Run("directory blocks document", func(t *testing.T) {
		store := newTestStore(t)
		ctx := context.Background()

		require.NoError(t, store.Create(ctx, "foo/child", "child content"))
		err := store.Create(ctx, "foo", "file content")
		assert.ErrorIs(t, err, docs.ErrDocAlreadyExists)
	})
}

func TestCreateInvalidID(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	err := store.Create(ctx, "../escape", "content")
	assert.ErrorIs(t, err, docs.ErrInvalidDocID)

	err = store.Create(ctx, "", "content")
	assert.ErrorIs(t, err, docs.ErrInvalidDocID)
}

func TestGetNotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.Get(ctx, "nonexistent")
	assert.ErrorIs(t, err, docs.ErrDocNotFound)
}

func TestSymlinkDocFileIsIgnored(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "secret.md")
	require.NoError(t, os.WriteFile(outsidePath, []byte("needle outside\n"), 0600))

	linkPath := filepath.Join(store.baseDir, "linked.md")
	if err := os.Symlink(outsidePath, linkPath); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}

	_, err := store.Get(ctx, "linked")
	assert.ErrorIs(t, err, docs.ErrDocNotFound)

	list, err := store.ListFlat(ctx, defaultFlatOpts(1, 10))
	require.NoError(t, err)
	assert.Empty(t, list.Items)

	search, err := store.Search(ctx, "needle")
	require.NoError(t, err)
	assert.Empty(t, search)

	cursorSearch, err := store.SearchCursor(ctx, docs.SearchDocsOptions{
		Query: "needle",
		Limit: 10,
	})
	require.NoError(t, err)
	assert.Empty(t, cursorSearch.Items)
}

func TestCreateRejectsSymlinkedExistingAncestor(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	outsideDir := t.TempDir()

	linkPath := filepath.Join(store.baseDir, "linked")
	if err := os.Symlink(outsideDir, linkPath); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}

	err := store.Create(ctx, "linked/new/doc", "content")
	require.Error(t, err)
	_, statErr := os.Stat(filepath.Join(outsideDir, "new", "doc.md"))
	assert.True(t, os.IsNotExist(statErr))
}

func TestUpdate(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "test-doc", "original"))

	err := store.Update(ctx, "test-doc", "updated content")
	require.NoError(t, err)

	doc, err := store.Get(ctx, "test-doc")
	require.NoError(t, err)
	assert.Equal(t, "updated content", doc.Content)
}

func TestUpdateSucceedsWhenMetadataIsCorrupt(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "test-doc", "original"))
	require.NoError(t, os.WriteFile(store.metadataPath(), []byte("not json"), filePermissions))

	require.NoError(t, store.Update(ctx, "test-doc", "updated content"))
	doc, err := store.Get(ctx, "test-doc")
	require.NoError(t, err)
	assert.Equal(t, "updated content", doc.Content)

	result, err := store.ListFlat(ctx, defaultFlatOpts(1, 10))
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "test-doc", result.Items[0].ID)
}

func TestUpdateNotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	err := store.Update(ctx, "nonexistent", "content")
	assert.ErrorIs(t, err, docs.ErrDocNotFound)
}

func TestDelete(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "test-doc", "content"))

	err := store.Delete(ctx, "test-doc")
	require.NoError(t, err)

	_, err = store.Get(ctx, "test-doc")
	assert.ErrorIs(t, err, docs.ErrDocNotFound)
}

func TestDeleteCleansEmptyParents(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "a/b/c", "content"))

	err := store.Delete(ctx, "a/b/c")
	require.NoError(t, err)

	// Parent directories should be cleaned up.
	_, err = os.Stat(filepath.Join(store.baseDir, "a", "b"))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(store.baseDir, "a"))
	assert.True(t, os.IsNotExist(err))
}

func TestDeleteNotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	err := store.Delete(ctx, "nonexistent")
	assert.ErrorIs(t, err, docs.ErrDocNotFound)
}

func TestRename(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "old-name", "content"))

	err := store.Rename(ctx, "old-name", "new-name")
	require.NoError(t, err)

	_, err = store.Get(ctx, "old-name")
	assert.ErrorIs(t, err, docs.ErrDocNotFound)

	doc, err := store.Get(ctx, "new-name")
	require.NoError(t, err)
	assert.Equal(t, "content", doc.Content)
}

func TestRenameToExisting(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "doc-a", "content a"))
	require.NoError(t, store.Create(ctx, "doc-b", "content b"))

	err := store.Rename(ctx, "doc-a", "doc-b")
	assert.ErrorIs(t, err, docs.ErrDocAlreadyExists)
}

func TestRenameNested(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "old/path", "content"))

	err := store.Rename(ctx, "old/path", "new/dir/path")
	require.NoError(t, err)

	doc, err := store.Get(ctx, "new/dir/path")
	require.NoError(t, err)
	assert.Equal(t, "content", doc.Content)

	// Old parent should be cleaned up.
	_, err = os.Stat(filepath.Join(store.baseDir, "old"))
	assert.True(t, os.IsNotExist(err))
}

func TestRenameRejectsAncestorDocumentCollision(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "destination", "content"))
	require.NoError(t, store.Create(ctx, "source", "source content"))

	err := store.Rename(ctx, "source", "destination/child")
	assert.ErrorIs(t, err, docs.ErrDocPathConflict)

	doc, getErr := store.Get(ctx, "source")
	require.NoError(t, getErr)
	assert.Equal(t, "source content", doc.Content)
}

func TestRenameRejectsDirectoryMoveIntoOwnSubtree(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "guides/intro/start", "start content"))
	require.NoError(t, store.Create(ctx, "guides/reference", "reference content"))

	err := store.Rename(ctx, "guides", "guides/intro/guides")
	assert.ErrorIs(t, err, docs.ErrDocPathConflict)

	start, getErr := store.Get(ctx, "guides/intro/start")
	require.NoError(t, getErr)
	assert.Equal(t, "start content", start.Content)
	reference, getErr := store.Get(ctx, "guides/reference")
	require.NoError(t, getErr)
	assert.Equal(t, "reference content", reference.Content)
}

func TestListFlat(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "b-doc", "b"))
	require.NoError(t, store.Create(ctx, "a-doc", "a"))
	require.NoError(t, store.Create(ctx, "sub/c-doc", "c"))

	result, err := store.ListFlat(ctx, defaultFlatOpts(1, 50))
	require.NoError(t, err)
	assert.Equal(t, 3, result.TotalCount)

	// Should be sorted alphabetically.
	assert.Equal(t, "a-doc", result.Items[0].ID)
	assert.Equal(t, "b-doc", result.Items[1].ID)
	assert.Equal(t, "sub/c-doc", result.Items[2].ID)
}

func TestListFlatWithPathPrefix(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "ops/doc", "ops"))
	require.NoError(t, store.Create(ctx, "prod/doc", "prod"))

	opts := defaultFlatOpts(1, 50)
	opts.PathPrefix = "ops"
	result, err := store.ListFlat(ctx, opts)
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "doc", result.Items[0].ID)
}

func TestPathPrefixRespectsExcludedRoots(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "ops/doc", "needle"))
	opts := docs.ListDocsOptions{
		Page:             1,
		PerPage:          50,
		PathPrefix:       "ops",
		ExcludePathRoots: []string{"ops"},
	}

	flat, err := store.ListFlat(ctx, opts)
	require.NoError(t, err)
	assert.Empty(t, flat.Items)

	tree, err := store.List(ctx, opts)
	require.NoError(t, err)
	assert.Empty(t, tree.Items)

	search, err := store.SearchCursor(ctx, docs.SearchDocsOptions{
		Query:            "needle",
		Limit:            10,
		MatchLimit:       1,
		PathPrefix:       "ops",
		ExcludePathRoots: []string{"ops"},
	})
	require.NoError(t, err)
	assert.Empty(t, search.Items)
}

func TestListFlatExcludePathRootsBeforePagination(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "aaa/hidden", "hidden"))
	require.NoError(t, store.Create(ctx, "bbb", "visible"))

	opts := defaultFlatOpts(1, 1)
	opts.ExcludePathRoots = []string{"aaa"}
	result, err := store.ListFlat(ctx, opts)
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "bbb", result.Items[0].ID)
	assert.Equal(t, 1, result.TotalCount)
	assert.Equal(t, 1, result.TotalPages)
}

func TestListTree(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "readme", "top level"))
	require.NoError(t, store.Create(ctx, "guides/deploy", "deploy guide"))
	require.NoError(t, store.Create(ctx, "guides/debug", "debug guide"))

	result, err := store.List(ctx, defaultListOpts(1, 50))
	require.NoError(t, err)
	assert.Equal(t, 2, result.TotalCount) // 1 dir + 1 file at top level

	// Directories sort before files.
	assert.Equal(t, "directory", result.Items[0].Type)
	assert.Equal(t, "guides", result.Items[0].Name)
	assert.Equal(t, 2, len(result.Items[0].Children))
	assert.Equal(t, "file", result.Items[1].Type)
	assert.Equal(t, "readme.md", result.Items[1].Name)
}

func TestListRefreshesCachedIndexFromDirectoryFingerprints(t *testing.T) {
	store := newTestStore(t)
	store.indexCheckInterval = 0
	ctx := context.Background()

	result, err := store.ListFlat(ctx, defaultFlatOpts(1, 50))
	require.NoError(t, err)
	require.Empty(t, result.Items)
	require.True(t, store.indexBuilt)

	require.NoError(t, os.WriteFile(filepath.Join(store.baseDir, "external.md"), []byte("---\ntitle: External\n---\nbody"), 0600))

	result, err = store.ListFlat(ctx, defaultFlatOpts(1, 50))
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "external", result.Items[0].ID)
	assert.Equal(t, "External", result.Items[0].Title)
}

func TestListRefreshesCachedIndexFromFileFingerprints(t *testing.T) {
	store := newTestStore(t)
	store.indexCheckInterval = 0
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "fingerprint", "---\ntitle: Old\n---\nbody"))
	result, err := store.ListFlat(ctx, defaultFlatOpts(1, 50))
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "Old", result.Items[0].Title)

	require.NoError(t, os.WriteFile(filepath.Join(store.baseDir, "fingerprint.md"), []byte("---\ntitle: New\n---\nbody changed"), 0600))

	result, err = store.ListFlat(ctx, defaultFlatOpts(1, 50))
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "New", result.Items[0].Title)
}

func TestListDoesNotRefreshCachedIndexBeforeCheckInterval(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	result, err := store.ListFlat(ctx, defaultFlatOpts(1, 50))
	require.NoError(t, err)
	require.Empty(t, result.Items)

	require.NoError(t, os.WriteFile(filepath.Join(store.baseDir, "external.md"), []byte("body"), 0600))

	result, err = store.ListFlat(ctx, defaultFlatOpts(1, 50))
	require.NoError(t, err)
	require.Empty(t, result.Items)

	store.mu.Lock()
	store.indexCheckedAt = time.Now().Add(-store.indexCheckInterval - time.Second)
	store.mu.Unlock()

	result, err = store.ListFlat(ctx, defaultFlatOpts(1, 50))
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "external", result.Items[0].ID)
}

func TestCachedIndexTracksStoreMutations(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.List(ctx, defaultListOpts(1, 50))
	require.NoError(t, err)
	require.True(t, store.indexBuilt)

	require.NoError(t, store.Create(ctx, "docs/deploy", "---\ntitle: Deploy\n---\nbody"))
	result, err := store.List(ctx, defaultListOpts(1, 50))
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "docs", result.Items[0].ID)
	require.Len(t, result.Items[0].Children, 1)
	assert.Equal(t, "Deploy", result.Items[0].Children[0].Title)

	require.NoError(t, store.Update(ctx, "docs/deploy", "---\ntitle: Deploy Updated\n---\nbody"))
	flat, err := store.ListFlat(ctx, defaultFlatOpts(1, 50))
	require.NoError(t, err)
	require.Len(t, flat.Items, 1)
	assert.Equal(t, "Deploy Updated", flat.Items[0].Title)

	require.NoError(t, store.Rename(ctx, "docs", "ops"))
	result, err = store.List(ctx, defaultListOpts(1, 50))
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "ops", result.Items[0].ID)

	require.NoError(t, store.Delete(ctx, "ops/deploy"))
	result, err = store.List(ctx, defaultListOpts(1, 50))
	require.NoError(t, err)
	assert.Empty(t, result.Items)
}

func TestRenameDirectoryRebuildsCachedIndexAfterCallerCancel(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "docs/deploy", "---\ntitle: Deploy\n---\nbody"))
	flat, err := store.ListFlat(ctx, defaultFlatOpts(1, 50))
	require.NoError(t, err)
	require.Len(t, flat.Items, 1)
	assert.Equal(t, "docs/deploy", flat.Items[0].ID)

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	require.NoError(t, store.Rename(canceledCtx, "docs", "ops"))

	flat, err = store.ListFlat(ctx, defaultFlatOpts(1, 50))
	require.NoError(t, err)
	require.Len(t, flat.Items, 1)
	assert.Equal(t, "ops/deploy", flat.Items[0].ID)
}

func TestListTreeExcludePathRootsBeforePagination(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "aaa", "hidden"))
	require.NoError(t, store.Create(ctx, "bbb", "visible"))

	opts := defaultListOpts(1, 1)
	opts.ExcludePathRoots = []string{"aaa"}
	result, err := store.List(ctx, opts)
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "bbb", result.Items[0].ID)
	assert.Equal(t, 1, result.TotalCount)
	assert.Equal(t, 1, result.TotalPages)
}

func TestSearch(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "doc1", "hello world"))
	require.NoError(t, store.Create(ctx, "doc2", "goodbye world"))
	require.NoError(t, store.Create(ctx, "doc3", "nothing here"))

	results, err := store.Search(ctx, "world")
	require.NoError(t, err)
	assert.Len(t, results, 2)
}

func TestSearchRanking(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// zz-body sorts last by ID but has the most matching lines.
	require.NoError(t, store.Create(ctx, "zz-body", "etl\netl\netl\netl"))
	// title-hit outranks any body-only match count.
	require.NoError(t, store.Create(ctx, "a-title", "---\ntitle: ETL Runbook\n---\nunrelated body etl"))
	require.NoError(t, store.Create(ctx, "m-single", "one etl mention"))

	results, err := store.Search(ctx, "etl")
	require.NoError(t, err)
	require.Len(t, results, 3)
	assert.Equal(t, "a-title", results[0].ID)
	assert.Equal(t, "zz-body", results[1].ID)
	assert.Equal(t, "m-single", results[2].ID)
	assert.Positive(t, results[0].MatchCount)
}

func TestSearchRankingTiebreakByID(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "b", "needle"))
	require.NoError(t, store.Create(ctx, "a", "needle"))

	results, err := store.Search(ctx, "needle")
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, "a", results[0].ID)
	assert.Equal(t, "b", results[1].ID)
}

func TestSearchNoResults(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "doc1", "hello"))

	results, err := store.Search(ctx, "nonexistent")
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestFrontmatter(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Write a file with frontmatter directly.
	filePath := filepath.Join(store.baseDir, "with-title.md")
	data := marshalDocFile("My Custom Title", "# Content here")
	require.NoError(t, os.WriteFile(filePath, data, 0600))

	doc, err := store.Get(ctx, "with-title")
	require.NoError(t, err)
	assert.Equal(t, "My Custom Title", doc.Title)
	// Content now includes the full file with frontmatter.
	assert.Equal(t, "---\ntitle: My Custom Title\n---\n# Content here", doc.Content)
}

func TestTitleFromID(t *testing.T) {
	assert.Equal(t, "deploy-guide", titleFromID("docs/deploy-guide"))
	assert.Equal(t, "simple", titleFromID("simple"))
}

func TestValidateDocID(t *testing.T) {
	// Valid IDs.
	assert.NoError(t, docs.ValidateDocID("simple"))
	assert.NoError(t, docs.ValidateDocID("with-hyphen"))
	assert.NoError(t, docs.ValidateDocID("with_underscore"))
	assert.NoError(t, docs.ValidateDocID("_leading-underscore"))
	assert.NoError(t, docs.ValidateDocID("with space"))
	assert.NoError(t, docs.ValidateDocID("with.dot"))
	assert.NoError(t, docs.ValidateDocID("MixedCase"))
	assert.NoError(t, docs.ValidateDocID("nested/path/doc"))
	assert.NoError(t, docs.ValidateDocID("nested/_partial"))
	assert.NoError(t, docs.ValidateDocID("123"))

	// Invalid IDs.
	assert.ErrorIs(t, docs.ValidateDocID(""), docs.ErrInvalidDocID)
	assert.ErrorIs(t, docs.ValidateDocID("../escape"), docs.ErrInvalidDocID)
	assert.ErrorIs(t, docs.ValidateDocID("/leading-slash"), docs.ErrInvalidDocID)
	assert.ErrorIs(t, docs.ValidateDocID("trailing/"), docs.ErrInvalidDocID)
	assert.ErrorIs(t, docs.ValidateDocID("trailing-space "), docs.ErrInvalidDocID)
	assert.ErrorIs(t, docs.ValidateDocID("nested/trailing-dot."), docs.ErrInvalidDocID)
}

func TestParseDocFileNoFrontmatter(t *testing.T) {
	doc, err := parseDocFile([]byte("# Just markdown\n\nNo frontmatter here"), "test")
	require.NoError(t, err)
	assert.Equal(t, "test", doc.Title) // Title derived from ID.
	assert.Equal(t, "# Just markdown\n\nNo frontmatter here", doc.Content)
}

func TestParseDocFileWithFrontmatter(t *testing.T) {
	input := "---\ntitle: My Doc\ndescription: Helps the agent choose this doc.\n---\n# Content"
	doc, err := parseDocFile([]byte(input), "test")
	require.NoError(t, err)
	assert.Equal(t, "My Doc", doc.Title)
	assert.Equal(t, "Helps the agent choose this doc.", doc.Description)
	// Content now includes the full file with frontmatter.
	assert.Equal(t, "---\ntitle: My Doc\ndescription: Helps the agent choose this doc.\n---\n# Content", doc.Content)
}

func TestListFlatIncludesDescriptionFromFrontmatter(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	content := "---\ntitle: Restart API\ndescription: Restart the API service and verify health.\n---\n# Restart API"
	require.NoError(t, store.Create(ctx, "docs/restart-api", content))

	result, err := store.ListFlat(ctx, defaultFlatOpts(1, 50))
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "Restart API", result.Items[0].Title)
	assert.Equal(t, "Restart the API service and verify health.", result.Items[0].Description)
}

func TestParseDocFileTags(t *testing.T) {
	tests := []struct {
		name  string
		input string
		tags  []string
	}{
		{
			name:  "list form",
			input: "---\ntitle: T\ntags:\n  - ops\n  - runbook\n---\nbody",
			tags:  []string{"ops", "runbook"},
		},
		{
			name:  "comma-separated scalar",
			input: "---\ntitle: T\ntags: ops, runbook\n---\nbody",
			tags:  []string{"ops", "runbook"},
		},
		{
			name:  "case-insensitive dedupe keeps first casing",
			input: "---\ntags: [Ops, ops, runbook]\n---\nbody",
			tags:  []string{"Ops", "runbook"},
		},
		{
			name:  "blank entries dropped",
			input: "---\ntags: ['', '  ', ops]\n---\nbody",
			tags:  []string{"ops"},
		},
		{
			name:  "absent",
			input: "---\ntitle: T\n---\nbody",
			tags:  nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := parseDocFile([]byte(tc.input), "test")
			require.NoError(t, err)
			assert.Equal(t, tc.tags, doc.Tags)
		})
	}
}

func TestParseDocFileMalformedTagsKeepTitle(t *testing.T) {
	// A tags value of the wrong shape must not invalidate the other
	// frontmatter fields.
	input := "---\ntitle: Kept\ntags:\n  nested: map\n---\nbody"
	doc, err := parseDocFile([]byte(input), "test")
	require.NoError(t, err)
	assert.Equal(t, "Kept", doc.Title)
	assert.Nil(t, doc.Tags)
}

func TestListFlatFilterByTags(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "a", "---\ntags: [ops, runbook]\n---\nbody"))
	require.NoError(t, store.Create(ctx, "b", "---\ntags: [ops]\n---\nbody"))
	require.NoError(t, store.Create(ctx, "c", "no tags"))

	opts := defaultFlatOpts(1, 50)
	opts.Tags = []string{"OPS"}
	result, err := store.ListFlat(ctx, opts)
	require.NoError(t, err)
	require.Len(t, result.Items, 2)
	assert.Equal(t, []string{"ops", "runbook"}, result.Items[0].Tags)

	// AND semantics: both tags must be present.
	opts.Tags = []string{"ops", "runbook"}
	result, err = store.ListFlat(ctx, opts)
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "a", result.Items[0].ID)
}

func TestListTreeIncludesTags(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "guides/tagged", "---\ntags: [ops]\n---\nbody"))

	result, err := store.List(ctx, defaultListOpts(1, 50))
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.Len(t, result.Items[0].Children, 1)
	assert.Equal(t, []string{"ops"}, result.Items[0].Children[0].Tags)
}

func TestSearchCursorFilterByTags(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "a", "---\ntags: [ops]\n---\nneedle"))
	require.NoError(t, store.Create(ctx, "b", "needle"))

	result, err := store.SearchCursor(ctx, docs.SearchDocsOptions{Query: "needle", Limit: 10, Tags: []string{"ops"}})
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "a", result.Items[0].ID)
	assert.Equal(t, []string{"ops"}, result.Items[0].Tags)

	// A cursor issued for one tag filter must be rejected under another.
	require.NoError(t, store.Create(ctx, "c", "---\ntags: [ops]\n---\nneedle"))
	first, err := store.SearchCursor(ctx, docs.SearchDocsOptions{Query: "needle", Limit: 1, Tags: []string{"ops"}})
	require.NoError(t, err)
	require.True(t, first.HasMore)
	_, err = store.SearchCursor(ctx, docs.SearchDocsOptions{Query: "needle", Limit: 1, Cursor: first.NextCursor})
	assert.ErrorIs(t, err, pagination.ErrInvalidCursor)
}

func TestIndexRefreshesTagsAfterUpdate(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "doc", "---\ntags: [old]\n---\nbody"))
	require.NoError(t, store.Update(ctx, "doc", "---\ntags: [new]\n---\nbody"))

	result, err := store.ListFlat(ctx, defaultFlatOpts(1, 50))
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Equal(t, []string{"new"}, result.Items[0].Tags)
}

func TestListFlatSkipsNonConformingFiles(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "good-doc", "content"))

	// Write a hidden file directly — it should be skipped.
	require.NoError(t, os.WriteFile(filepath.Join(store.baseDir, ".hidden.md"), []byte("hidden"), 0600))

	result, err := store.ListFlat(ctx, defaultFlatOpts(1, 50))
	require.NoError(t, err)
	assert.Equal(t, 1, result.TotalCount)
	assert.Equal(t, "good-doc", result.Items[0].ID)
}

func TestBuildTreeSkipsNonConformingFiles(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "good-doc", "content"))

	// Write a non-conforming file directly.
	require.NoError(t, os.WriteFile(filepath.Join(store.baseDir, ".hidden.md"), []byte("hidden"), 0600))

	result, err := store.List(ctx, defaultListOpts(1, 50))
	require.NoError(t, err)
	assert.Equal(t, 1, result.TotalCount)
	assert.Equal(t, "good-doc", result.Items[0].ID)
}

func TestListTreeIncludesLeadingUnderscoreDocs(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, os.MkdirAll(filepath.Join(store.baseDir, "guides"), 0750))
	require.NoError(t, os.WriteFile(filepath.Join(store.baseDir, "_index.md"), []byte("top"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(store.baseDir, "guides", "_partial.md"), []byte("nested"), 0600))

	result, err := store.List(ctx, defaultListOpts(1, 50))
	require.NoError(t, err)
	require.Len(t, result.Items, 2)
	assert.Equal(t, "guides", result.Items[0].ID)
	require.Len(t, result.Items[0].Children, 1)
	assert.Equal(t, "guides/_partial", result.Items[0].Children[0].ID)
	assert.Equal(t, "_index", result.Items[1].ID)
}

func TestGetLeadingUnderscoreDoc(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, os.WriteFile(filepath.Join(store.baseDir, "_index.md"), []byte("content"), 0600))

	doc, err := store.Get(ctx, "_index")
	require.NoError(t, err)
	assert.Equal(t, "_index", doc.ID)
	assert.Equal(t, "content", doc.Content)
}

func TestSearchSkipsNonConformingFiles(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "good-doc", "findme"))

	// Write a non-conforming file that also contains the search term.
	require.NoError(t, os.WriteFile(filepath.Join(store.baseDir, ".hidden.md"), []byte("findme"), 0600))

	results, err := store.Search(ctx, "findme")
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "good-doc", results[0].ID)
}

func TestListFlatPagination(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "doc-a", "a"))
	require.NoError(t, store.Create(ctx, "doc-b", "b"))
	require.NoError(t, store.Create(ctx, "doc-c", "c"))

	// Page 1 of 2.
	result, err := store.ListFlat(ctx, defaultFlatOpts(1, 2))
	require.NoError(t, err)
	assert.Equal(t, 3, result.TotalCount)
	assert.Len(t, result.Items, 2)
	assert.Equal(t, "doc-a", result.Items[0].ID)
	assert.Equal(t, "doc-b", result.Items[1].ID)

	// Page 2 of 2.
	result, err = store.ListFlat(ctx, defaultFlatOpts(2, 2))
	require.NoError(t, err)
	assert.Equal(t, 3, result.TotalCount)
	assert.Len(t, result.Items, 1)
	assert.Equal(t, "doc-c", result.Items[0].ID)
}

func TestListFlatEmptyPage(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "doc-a", "a"))

	// Out-of-range page.
	result, err := store.ListFlat(ctx, defaultFlatOpts(10, 50))
	require.NoError(t, err)
	assert.Equal(t, 1, result.TotalCount)
	assert.Empty(t, result.Items)
}

func TestListTreePagination(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "doc-a", "a"))
	require.NoError(t, store.Create(ctx, "doc-b", "b"))
	require.NoError(t, store.Create(ctx, "doc-c", "c"))

	// Page 1 of 2 top-level nodes.
	result, err := store.List(ctx, defaultListOpts(1, 2))
	require.NoError(t, err)
	assert.Equal(t, 3, result.TotalCount)
	assert.Len(t, result.Items, 2)

	// Page 2.
	result, err = store.List(ctx, defaultListOpts(2, 2))
	require.NoError(t, err)
	assert.Equal(t, 3, result.TotalCount)
	assert.Len(t, result.Items, 1)
}

func TestDocFilePathTraversal(t *testing.T) {
	store := newTestStore(t)

	_, err := store.docFilePath("../../etc/passwd")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "path traversal")
}

func TestUpdateWithFrontmatter(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create with frontmatter.
	content := "---\ntitle: Original Title\n---\n# Body"
	require.NoError(t, store.Create(ctx, "fm-doc", content))

	doc, err := store.Get(ctx, "fm-doc")
	require.NoError(t, err)
	assert.Equal(t, "Original Title", doc.Title)
	assert.Equal(t, content, doc.Content)

	// Update with new frontmatter.
	updated := "---\ntitle: New Title\n---\n# Updated Body"
	require.NoError(t, store.Update(ctx, "fm-doc", updated))

	doc, err = store.Get(ctx, "fm-doc")
	require.NoError(t, err)
	assert.Equal(t, "New Title", doc.Title)
	assert.Equal(t, updated, doc.Content)
}

func TestSearchInvalidRegex(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "doc1", "hello"))

	results, err := store.Search(ctx, "[invalid")
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestSearchEmptyQuery(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "doc1", "hello"))

	results, err := store.Search(ctx, "")
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestBuildTreeExcludesNonMdFiles(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "real-doc", "content"))

	// Write non-.md files directly.
	require.NoError(t, os.WriteFile(filepath.Join(store.baseDir, "notes.txt"), []byte("text"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(store.baseDir, "config.yaml"), []byte("yaml: true"), 0600))

	result, err := store.List(ctx, defaultListOpts(1, 50))
	require.NoError(t, err)
	assert.Equal(t, 1, result.TotalCount)
	assert.Equal(t, "real-doc", result.Items[0].ID)
}

func TestCreateLargeID(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	id252 := strings.Repeat("a", 252)
	err := store.Create(ctx, id252, "content")
	require.NoError(t, err)

	err = store.Create(ctx, id252+"x", "content")
	assert.ErrorIs(t, err, docs.ErrInvalidDocID)
}

func TestValidateDocIDBoundary(t *testing.T) {
	// Exactly at the limit.
	assert.NoError(t, docs.ValidateDocID(strings.Repeat("a", 252)))

	// One over the limit.
	assert.ErrorIs(t, docs.ValidateDocID(strings.Repeat("a", 253)), docs.ErrInvalidDocID)
}

func TestValidateDocIDRejectsReservedDeviceNames(t *testing.T) {
	for _, id := range []string{"CON", "guides/prn", "aux.txt", "COM1", "lpt9.log"} {
		t.Run(id, func(t *testing.T) {
			assert.ErrorIs(t, docs.ValidateDocID(id), docs.ErrInvalidDocID)
		})
	}
	assert.NoError(t, docs.ValidateDocID("console"))
	assert.NoError(t, docs.ValidateDocID("com10"))
}

func TestTimestamps(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "ts-doc", "content"))

	doc, err := store.Get(ctx, "ts-doc")
	require.NoError(t, err)
	assert.NotEmpty(t, doc.CreatedAt)
	assert.NotEmpty(t, doc.UpdatedAt)
	createdAt := doc.CreatedAt

	require.NoError(t, store.Update(ctx, "ts-doc", "updated"))
	doc, err = store.Get(ctx, "ts-doc")
	require.NoError(t, err)
	assert.Equal(t, createdAt, doc.CreatedAt)

	require.NoError(t, store.Rename(ctx, "ts-doc", "renamed-ts-doc"))
	doc, err = store.Get(ctx, "renamed-ts-doc")
	require.NoError(t, err)
	assert.Equal(t, createdAt, doc.CreatedAt)
}

// ---------------------------------------------------------------------------
// Additional tests for increased coverage
// ---------------------------------------------------------------------------

func TestParseDocFileEOFFrontmatter(t *testing.T) {
	// Frontmatter terminated by \n--- at EOF (no trailing newline after closing ---).
	input := "---\ntitle: EOF\n---"
	doc, err := parseDocFile([]byte(input), "eof-doc")
	require.NoError(t, err)
	assert.Equal(t, "EOF", doc.Title)
	assert.Equal(t, "---\ntitle: EOF\n---", doc.Content)
}

func TestGetNotFoundNonexistent(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.Get(ctx, "nonexistent")
	assert.ErrorIs(t, err, docs.ErrDocNotFound)
}

func TestGetInvalidID(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	_, err := store.Get(ctx, "..invalid")
	assert.ErrorIs(t, err, docs.ErrInvalidDocID)
}

func TestCreateEmptyContent(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	err := store.Create(ctx, "empty", "")
	require.NoError(t, err)

	doc, err := store.Get(ctx, "empty")
	require.NoError(t, err)
	assert.Equal(t, "", doc.Content)
}

func TestUpdateInvalidID(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	err := store.Update(ctx, "..invalid", "x")
	assert.ErrorIs(t, err, docs.ErrInvalidDocID)
}

func TestDeleteInvalidID(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	err := store.Delete(ctx, "..invalid")
	assert.ErrorIs(t, err, docs.ErrInvalidDocID)
}

func TestDeleteCleansParents(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "sub/dir/doc", "content"))

	err := store.Delete(ctx, "sub/dir/doc")
	require.NoError(t, err)

	// Verify sub/dir and sub directories were cleaned up.
	_, err = os.Stat(filepath.Join(store.baseDir, "sub", "dir"))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(store.baseDir, "sub"))
	assert.True(t, os.IsNotExist(err))
}

func TestRenameSourceNotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	err := store.Rename(ctx, "nonexistent", "new")
	assert.ErrorIs(t, err, docs.ErrDocNotFound)
}

func TestRenameInvalidIDs(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Invalid source ID.
	err := store.Rename(ctx, "..invalid", "new-name")
	assert.ErrorIs(t, err, docs.ErrInvalidDocID)

	// Valid source but invalid target.
	require.NoError(t, store.Create(ctx, "valid-source", "content"))
	err = store.Rename(ctx, "valid-source", "..invalid")
	assert.ErrorIs(t, err, docs.ErrInvalidDocID)
}

func TestRenameCrossDirectory(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "old-dir/doc", "cross-dir content"))

	err := store.Rename(ctx, "old-dir/doc", "new-dir/doc")
	require.NoError(t, err)

	// New path has content.
	doc, err := store.Get(ctx, "new-dir/doc")
	require.NoError(t, err)
	assert.Equal(t, "cross-dir content", doc.Content)

	// Old path not found.
	_, err = store.Get(ctx, "old-dir/doc")
	assert.ErrorIs(t, err, docs.ErrDocNotFound)

	// Old directory cleaned up.
	_, err = os.Stat(filepath.Join(store.baseDir, "old-dir"))
	assert.True(t, os.IsNotExist(err))

	// New directory exists.
	_, err = os.Stat(filepath.Join(store.baseDir, "new-dir"))
	assert.False(t, os.IsNotExist(err))
}

func TestListEmpty(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	result, err := store.List(ctx, defaultListOpts(1, 10))
	require.NoError(t, err)
	assert.Equal(t, 0, result.TotalCount)
	assert.Empty(t, result.Items)
}

func TestListTreeEmptyPage(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "doc-a", "a"))

	// Out-of-range page for tree list triggers offset > total branch.
	result, err := store.List(ctx, defaultListOpts(10, 50))
	require.NoError(t, err)
	assert.Equal(t, 1, result.TotalCount)
	assert.Empty(t, result.Items)
}

func TestListFlatEmpty(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	result, err := store.ListFlat(ctx, defaultFlatOpts(1, 10))
	require.NoError(t, err)
	assert.Equal(t, 0, result.TotalCount)
	assert.Empty(t, result.Items)
}

func TestSearchIsCaseSensitive(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "doc1", "Hello World"))

	results, err := store.Search(ctx, "hello")
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestSearchWithFrontmatter(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	content := "---\ntitle: Searchable Doc\n---\n# Body with keyword"
	require.NoError(t, store.Create(ctx, "fm-search", content))

	results, err := store.Search(ctx, "keyword")
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "Searchable Doc", results[0].Title)
}

func TestCreateInvalidIDVariants(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Leading slash.
	err := store.Create(ctx, "/leading", "content")
	assert.ErrorIs(t, err, docs.ErrInvalidDocID)

	// Trailing slash.
	err = store.Create(ctx, "trailing/", "content")
	assert.ErrorIs(t, err, docs.ErrInvalidDocID)

	// Double dot segment.
	err = store.Create(ctx, "a/../b", "content")
	assert.ErrorIs(t, err, docs.ErrInvalidDocID)
}

func TestUpdateEmptyContent(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "to-empty", "original"))

	err := store.Update(ctx, "to-empty", "")
	require.NoError(t, err)

	doc, err := store.Get(ctx, "to-empty")
	require.NoError(t, err)
	assert.Equal(t, "", doc.Content)
}

func TestDeletePreservesOtherDocsInDir(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "shared/doc-a", "a"))
	require.NoError(t, store.Create(ctx, "shared/doc-b", "b"))

	err := store.Delete(ctx, "shared/doc-a")
	require.NoError(t, err)

	// The shared directory should still exist because doc-b is still there.
	_, err = os.Stat(filepath.Join(store.baseDir, "shared"))
	assert.False(t, os.IsNotExist(err))

	// doc-b should still be accessible.
	doc, err := store.Get(ctx, "shared/doc-b")
	require.NoError(t, err)
	assert.Equal(t, "b", doc.Content)
}

func TestBuildTreeNestedDirectories(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "a/b/c", "deep"))
	require.NoError(t, store.Create(ctx, "a/d", "sibling"))

	result, err := store.List(ctx, defaultListOpts(1, 50))
	require.NoError(t, err)
	// Top level should have directory "a".
	require.Equal(t, 1, result.TotalCount)
	assert.Equal(t, "directory", result.Items[0].Type)
	assert.Equal(t, "a", result.Items[0].Name)
	// "a" should have children: directory "b" and file "d.md".
	assert.Equal(t, 2, len(result.Items[0].Children))
}

func TestRenamePreservesContent(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	content := "---\ntitle: My Title\n---\n# Important content"
	require.NoError(t, store.Create(ctx, "original", content))

	err := store.Rename(ctx, "original", "renamed")
	require.NoError(t, err)

	doc, err := store.Get(ctx, "renamed")
	require.NoError(t, err)
	assert.Equal(t, "My Title", doc.Title)
	assert.Equal(t, content, doc.Content)
}

func TestParseDocFileCRLF(t *testing.T) {
	// Verify CRLF normalization.
	input := "---\r\ntitle: CRLF\r\n---\r\n# Content"
	doc, err := parseDocFile([]byte(input), "crlf-doc")
	require.NoError(t, err)
	assert.Equal(t, "CRLF", doc.Title)
	assert.Equal(t, "---\ntitle: CRLF\n---\n# Content", doc.Content)
}

func TestParseDocFileOnlyFrontmatter(t *testing.T) {
	// File with frontmatter but no body.
	input := "---\ntitle: No Body\n---\n"
	doc, err := parseDocFile([]byte(input), "no-body")
	require.NoError(t, err)
	assert.Equal(t, "No Body", doc.Title)
	assert.Equal(t, "---\ntitle: No Body\n---", doc.Content)
}

func TestParseDocFileInvalidFrontmatter(t *testing.T) {
	// Invalid YAML in frontmatter should not fail, just fall back to ID-based title.
	input := "---\n: invalid: yaml: :\n---\n# Body"
	doc, err := parseDocFile([]byte(input), "bad-fm")
	require.NoError(t, err)
	assert.Equal(t, "bad-fm", doc.Title)
	assert.Equal(t, "---\n: invalid: yaml: :\n---\n# Body", doc.Content)
}

func TestParseDocFileUnclosedFrontmatter(t *testing.T) {
	// Opening --- but no closing --- should treat entire content as body.
	input := "---\ntitle: Unclosed\nsome content here"
	doc, err := parseDocFile([]byte(input), "unclosed")
	require.NoError(t, err)
	// No closing delimiter, so title falls back to ID.
	assert.Equal(t, "unclosed", doc.Title)
	assert.Equal(t, "---\ntitle: Unclosed\nsome content here", doc.Content)
}

func TestGetReadPermissionError(t *testing.T) {
	testutil.SkipIfPOSIXPermissionErrorsUnsupported(t)

	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "perm-doc", "content"))

	// Make file unreadable.
	filePath := filepath.Join(store.baseDir, "perm-doc.md")
	require.NoError(t, os.Chmod(filePath, 0000))
	t.Cleanup(func() { _ = os.Chmod(filePath, 0600) })

	_, err := store.Get(ctx, "perm-doc")
	assert.Error(t, err)
	assert.NotErrorIs(t, err, docs.ErrDocNotFound)
}

func TestCreateMkdirPermissionError(t *testing.T) {
	testutil.SkipIfPOSIXPermissionErrorsUnsupported(t)

	store := newTestStore(t)
	ctx := context.Background()

	// Make base dir read-only so MkdirAll for nested path fails.
	require.NoError(t, os.Chmod(store.baseDir, 0500))
	t.Cleanup(func() { _ = os.Chmod(store.baseDir, 0750) })

	err := store.Create(ctx, "sub/doc", "content")
	assert.Error(t, err)
}

func TestDeleteRemovePermissionError(t *testing.T) {
	testutil.SkipIfPOSIXPermissionErrorsUnsupported(t)

	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "perm-del", "content"))

	// Make directory read-only so os.Remove of file fails.
	require.NoError(t, os.Chmod(store.baseDir, 0500))
	t.Cleanup(func() { _ = os.Chmod(store.baseDir, 0750) })

	err := store.Delete(ctx, "perm-del")
	assert.Error(t, err)
	assert.NotErrorIs(t, err, docs.ErrDocNotFound)
}

func TestUpdateWritePermissionError(t *testing.T) {
	testutil.SkipIfPOSIXPermissionErrorsUnsupported(t)

	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "perm-upd", "original"))

	// Make directory read-only so atomic write (create temp file) fails.
	require.NoError(t, os.Chmod(store.baseDir, 0500))
	t.Cleanup(func() { _ = os.Chmod(store.baseDir, 0750) })

	err := store.Update(ctx, "perm-upd", "updated")
	assert.Error(t, err)
}

func TestRenameMkdirPermissionError(t *testing.T) {
	testutil.SkipIfPOSIXPermissionErrorsUnsupported(t)

	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "src-doc", "content"))

	// Make base dir read-only so MkdirAll for target path fails.
	require.NoError(t, os.Chmod(store.baseDir, 0500))
	t.Cleanup(func() { _ = os.Chmod(store.baseDir, 0750) })

	err := store.Rename(ctx, "src-doc", "new-dir/target")
	assert.Error(t, err)
}

func TestNewReturnsBaseDirCreationError(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0600))

	store, err := New(filepath.Join(blocker, "impossible"))
	require.Error(t, err)
	assert.Nil(t, store)
}

func TestListFlatWithUnreadableFile(t *testing.T) {
	testutil.SkipIfPOSIXPermissionErrorsUnsupported(t)

	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "readable", "content"))
	require.NoError(t, store.Create(ctx, "unreadable", "secret"))

	// Make one file unreadable.
	filePath := filepath.Join(store.baseDir, "unreadable.md")
	require.NoError(t, os.Chmod(filePath, 0000))
	t.Cleanup(func() { _ = os.Chmod(filePath, 0600) })

	result, err := store.ListFlat(ctx, defaultFlatOpts(1, 50))
	require.NoError(t, err)
	// Only the readable file should be listed.
	assert.Equal(t, 1, result.TotalCount)
	assert.Equal(t, "readable", result.Items[0].ID)
}

func TestSearchWithUnreadableFile(t *testing.T) {
	testutil.SkipIfPOSIXPermissionErrorsUnsupported(t)

	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "findable", "searchterm"))
	require.NoError(t, store.Create(ctx, "hidden", "searchterm"))

	// Make one file unreadable.
	filePath := filepath.Join(store.baseDir, "hidden.md")
	require.NoError(t, os.Chmod(filePath, 0000))
	t.Cleanup(func() { _ = os.Chmod(filePath, 0600) })

	results, err := store.Search(ctx, "searchterm")
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "findable", results[0].ID)
}

func TestBuildTreeWithUnreadableFile(t *testing.T) {
	testutil.SkipIfPOSIXPermissionErrorsUnsupported(t)

	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "visible", "content"))
	require.NoError(t, store.Create(ctx, "noperm", "content"))

	// Make one file unreadable to trigger readErr in buildTree.
	filePath := filepath.Join(store.baseDir, "noperm.md")
	require.NoError(t, os.Chmod(filePath, 0000))
	t.Cleanup(func() { _ = os.Chmod(filePath, 0600) })

	result, err := store.List(ctx, defaultListOpts(1, 50))
	require.NoError(t, err)
	// Both files should still appear in tree (unreadable file uses ID-based title).
	assert.Equal(t, 2, result.TotalCount)
}

func TestCreateWritePermissionError(t *testing.T) {
	testutil.SkipIfPOSIXPermissionErrorsUnsupported(t)

	store := newTestStore(t)
	ctx := context.Background()

	// Make base dir read-only so atomic write fails.
	require.NoError(t, os.Chmod(store.baseDir, 0500))
	t.Cleanup(func() { _ = os.Chmod(store.baseDir, 0750) })

	err := store.Create(ctx, "fail-write", "content")
	assert.Error(t, err)
}

func TestListFlatEmptyStore(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	result, err := store.ListFlat(ctx, defaultFlatOpts(5, 10))
	require.NoError(t, err)
	assert.Equal(t, 0, result.TotalCount)
	assert.Empty(t, result.Items)
}

func TestListFlatMissingBaseDir(t *testing.T) {
	// When base dir doesn't exist, ListFlat returns empty results (WalkDir error is swallowed).
	store, err := New(filepath.Join(t.TempDir(), "nonexistent"))
	require.NoError(t, err)
	_ = os.RemoveAll(store.baseDir)
	ctx := context.Background()

	result, err := store.ListFlat(ctx, defaultFlatOpts(1, 10))
	require.NoError(t, err)
	assert.Equal(t, 0, result.TotalCount)
}

func TestListTreeMissingBaseDir(t *testing.T) {
	// When base dir doesn't exist, List returns empty results.
	store, err := New(filepath.Join(t.TempDir(), "nonexistent"))
	require.NoError(t, err)
	_ = os.RemoveAll(store.baseDir)
	ctx := context.Background()

	result, err := store.List(ctx, defaultListOpts(1, 10))
	require.NoError(t, err)
	assert.Equal(t, 0, result.TotalCount)
}

func TestSearchMissingBaseDir(t *testing.T) {
	// When base dir doesn't exist, Search returns empty results.
	store, err := New(filepath.Join(t.TempDir(), "nonexistent"))
	require.NoError(t, err)
	_ = os.RemoveAll(store.baseDir)
	ctx := context.Background()

	results, err := store.Search(ctx, "anything")
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestSearchCursorFailsWhenBaseDirIsNotDirectory(t *testing.T) {
	basePath := filepath.Join(t.TempDir(), "docs")
	store, err := New(basePath)
	require.NoError(t, err)
	require.NoError(t, os.Remove(basePath))
	require.NoError(t, os.WriteFile(basePath, []byte("x"), 0600))

	result, err := store.SearchCursor(context.Background(), docs.SearchDocsOptions{
		Query:      "needle",
		Limit:      1,
		MatchLimit: 1,
	})
	require.Nil(t, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a directory")
}

func TestSearchCursorUsesStableSortedIDsAcrossNestedDocs(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "a/child", "needle"))
	require.NoError(t, store.Create(ctx, "b", "needle"))

	firstPage, err := store.SearchCursor(ctx, docs.SearchDocsOptions{
		Query:      "needle",
		Limit:      1,
		MatchLimit: 1,
	})
	require.NoError(t, err)
	require.Len(t, firstPage.Items, 1)
	require.True(t, firstPage.HasMore)
	require.NotEmpty(t, firstPage.NextCursor)
	assert.Equal(t, "a/child", firstPage.Items[0].ID)

	secondPage, err := store.SearchCursor(ctx, docs.SearchDocsOptions{
		Query:      "needle",
		Limit:      1,
		MatchLimit: 1,
		Cursor:     firstPage.NextCursor,
	})
	require.NoError(t, err)
	require.Len(t, secondPage.Items, 1)
	assert.Equal(t, "b", secondPage.Items[0].ID)
	assert.False(t, secondPage.HasMore)
}

func TestSearchCursorWithPathPrefix(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "ops/a", "needle"))
	require.NoError(t, store.Create(ctx, "prod/a", "needle"))

	result, err := store.SearchCursor(ctx, docs.SearchDocsOptions{
		Query:      "needle",
		Limit:      10,
		MatchLimit: 1,
		PathPrefix: "ops",
	})
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Equal(t, "a", result.Items[0].ID)

	matches, err := store.SearchMatches(ctx, "a", docs.SearchDocMatchesOptions{
		Query:      "needle",
		Limit:      1,
		PathPrefix: "ops",
	})
	require.NoError(t, err)
	require.Len(t, matches.Items, 1)
}

func TestSearchCursorWithFilterPrefix(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "guides/a", "needle"))
	require.NoError(t, store.Create(ctx, "guides/b", "needle"))
	require.NoError(t, store.Create(ctx, "runbooks/c", "needle"))

	firstPage, err := store.SearchCursor(ctx, docs.SearchDocsOptions{
		Query:        "needle",
		Limit:        1,
		MatchLimit:   1,
		FilterPrefix: "guides",
	})
	require.NoError(t, err)
	require.Len(t, firstPage.Items, 1)
	assert.Equal(t, "guides/a", firstPage.Items[0].ID)
	assert.False(t, firstPage.Items[0].ModTime.IsZero())
	assert.True(t, firstPage.HasMore)
	require.NotEmpty(t, firstPage.NextCursor)

	secondPage, err := store.SearchCursor(ctx, docs.SearchDocsOptions{
		Query:        "needle",
		Limit:        1,
		MatchLimit:   1,
		FilterPrefix: "guides",
		Cursor:       firstPage.NextCursor,
	})
	require.NoError(t, err)
	require.Len(t, secondPage.Items, 1)
	assert.Equal(t, "guides/b", secondPage.Items[0].ID)
	assert.False(t, secondPage.HasMore)

	result, err := store.SearchCursor(ctx, docs.SearchDocsOptions{
		Query:        "needle",
		Limit:        1,
		MatchLimit:   1,
		FilterPrefix: "runbooks",
		Cursor:       firstPage.NextCursor,
	})
	require.Nil(t, result)
	assert.ErrorIs(t, err, pagination.ErrInvalidCursor)
}

func TestSearchMatchesRejectsInvalidDocID(t *testing.T) {
	store := newTestStore(t)

	result, err := store.SearchMatches(context.Background(), "../bad", docs.SearchDocMatchesOptions{
		Query: "needle",
		Limit: 1,
	})
	require.Nil(t, result)
	assert.ErrorIs(t, err, docs.ErrInvalidDocID)
}

func TestCleanEmptyParentsNonRemovable(t *testing.T) {
	testutil.SkipIfPOSIXPermissionErrorsUnsupported(t)

	store := newTestStore(t)
	ctx := context.Background()

	// Create a nested doc.
	require.NoError(t, store.Create(ctx, "level1/level2/doc", "content"))

	// Make level1 non-writable so level2 cannot be removed.
	level1Dir := filepath.Join(store.baseDir, "level1")
	require.NoError(t, os.Chmod(level1Dir, 0500))
	t.Cleanup(func() { _ = os.Chmod(level1Dir, 0750) })

	// Delete the doc -- os.Remove succeeds for the file (in level2),
	// but cleanEmptyParents cannot remove level2 (because level1 is read-only).
	// The delete should still succeed (cleanEmptyParents is best-effort).
	err := store.Delete(ctx, "level1/level2/doc")
	require.NoError(t, err)

	// level2 should still exist (cleanup failed).
	_, err = os.Stat(filepath.Join(store.baseDir, "level1", "level2"))
	assert.False(t, os.IsNotExist(err))
}

func TestRenameDirectory(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "folder/doc1", "content1"))
	require.NoError(t, store.Create(ctx, "folder/doc2", "content2"))

	err := store.Rename(ctx, "folder", "moved")
	require.NoError(t, err)

	doc1, err := store.Get(ctx, "moved/doc1")
	require.NoError(t, err)
	assert.Equal(t, "content1", doc1.Content)

	doc2, err := store.Get(ctx, "moved/doc2")
	require.NoError(t, err)
	assert.Equal(t, "content2", doc2.Content)

	_, err = store.Get(ctx, "folder/doc1")
	assert.ErrorIs(t, err, docs.ErrDocNotFound)

	_, err = os.Stat(filepath.Join(store.baseDir, "folder"))
	assert.True(t, os.IsNotExist(err))
}

func TestRenameDirectoryNested(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "a/b/doc1", "c1"))

	err := store.Rename(ctx, "a/b", "c/d")
	require.NoError(t, err)

	doc, err := store.Get(ctx, "c/d/doc1")
	require.NoError(t, err)
	assert.Equal(t, "c1", doc.Content)

	_, err = store.Get(ctx, "a/b/doc1")
	assert.ErrorIs(t, err, docs.ErrDocNotFound)

	// Empty parent "a" should be cleaned up.
	_, err = os.Stat(filepath.Join(store.baseDir, "a"))
	assert.True(t, os.IsNotExist(err))
}

func TestRenameDirectoryToExisting(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "folder1/doc", "c1"))
	require.NoError(t, store.Create(ctx, "folder2/doc", "c2"))

	err := store.Rename(ctx, "folder1", "folder2")
	assert.ErrorIs(t, err, docs.ErrDocAlreadyExists)

	// Original files untouched.
	doc, err := store.Get(ctx, "folder1/doc")
	require.NoError(t, err)
	assert.Equal(t, "c1", doc.Content)
}

func TestRenameDirectoryNotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	err := store.Rename(ctx, "nonexistent", "target")
	assert.ErrorIs(t, err, docs.ErrDocNotFound)
}

func TestRenameDirectoryTargetFileConflict(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "folder/doc", "content"))
	require.NoError(t, store.Create(ctx, "readme", "hi"))

	// Cannot rename dir "folder" to "readme" because readme.md exists.
	err := store.Rename(ctx, "folder", "readme")
	assert.ErrorIs(t, err, docs.ErrDocAlreadyExists)
}

func TestRenameDirectoryPreservesContent(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	content := string(marshalDocFile("My Title", "# Hello\nbody"))
	require.NoError(t, store.Create(ctx, "dir/doc", content))

	err := store.Rename(ctx, "dir", "newdir")
	require.NoError(t, err)

	doc, err := store.Get(ctx, "newdir/doc")
	require.NoError(t, err)
	assert.Equal(t, "My Title", doc.Title)
	assert.Equal(t, strings.TrimRight(content, "\n"), doc.Content)
}

func TestRenameDirectoryToRoot(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "parent/child/doc", "content"))

	err := store.Rename(ctx, "parent/child", "child")
	require.NoError(t, err)

	doc, err := store.Get(ctx, "child/doc")
	require.NoError(t, err)
	assert.Equal(t, "content", doc.Content)

	_, err = store.Get(ctx, "parent/child/doc")
	assert.ErrorIs(t, err, docs.ErrDocNotFound)

	// parent/ should be cleaned up.
	_, err = os.Stat(filepath.Join(store.baseDir, "parent"))
	assert.True(t, os.IsNotExist(err))
}

func TestRenameDirectoryRootToNested(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "flat/doc", "content"))

	err := store.Rename(ctx, "flat", "deep/nested/flat")
	require.NoError(t, err)

	doc, err := store.Get(ctx, "deep/nested/flat/doc")
	require.NoError(t, err)
	assert.Equal(t, "content", doc.Content)
}

func TestRenameDirectoryCleansEmptyParents(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "deep/nested/dir/doc", "content"))

	err := store.Rename(ctx, "deep/nested/dir", "moved")
	require.NoError(t, err)

	// deep/nested and deep should be cleaned up.
	_, err = os.Stat(filepath.Join(store.baseDir, "deep", "nested"))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(store.baseDir, "deep"))
	assert.True(t, os.IsNotExist(err))
}

func TestRenameDirectoryMkdirFails(t *testing.T) {
	testutil.SkipIfPOSIXPermissionErrorsUnsupported(t)

	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "src/doc", "content"))

	// Make base dir read-only so MkdirAll for target parent fails.
	require.NoError(t, os.Chmod(store.baseDir, 0500))
	t.Cleanup(func() { _ = os.Chmod(store.baseDir, 0750) })

	err := store.Rename(ctx, "src", "unwritable/target")
	assert.Error(t, err)
}

func TestRenameDirectoryOsRenameFails(t *testing.T) {
	testutil.SkipIfPOSIXPermissionErrorsUnsupported(t)

	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "srcdir/doc", "content"))

	// Make the source's parent directory (baseDir) read-only so os.Rename fails.
	require.NoError(t, os.Chmod(store.baseDir, 0500))
	t.Cleanup(func() { _ = os.Chmod(store.baseDir, 0750) })

	err := store.Rename(ctx, "srcdir", "destdir")
	assert.Error(t, err)
}

func TestRenameOsRenameError(t *testing.T) {
	testutil.SkipIfPOSIXPermissionErrorsUnsupported(t)

	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "rename-src", "content"))

	// Create target directory, then make it read-only so os.Rename into it fails.
	targetDir := filepath.Join(store.baseDir, "target-dir")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	require.NoError(t, os.Chmod(targetDir, 0500))
	t.Cleanup(func() { _ = os.Chmod(targetDir, 0750) })

	err := store.Rename(ctx, "rename-src", "target-dir/dest")
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// Directory Delete Tests
// ---------------------------------------------------------------------------

func TestDeleteDirectory(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "folder/doc1", "content1"))
	require.NoError(t, store.Create(ctx, "folder/doc2", "content2"))

	err := store.Delete(ctx, "folder")
	require.NoError(t, err)

	// All children should be gone.
	_, err = store.Get(ctx, "folder/doc1")
	assert.ErrorIs(t, err, docs.ErrDocNotFound)
	_, err = store.Get(ctx, "folder/doc2")
	assert.ErrorIs(t, err, docs.ErrDocNotFound)
	// Directory itself should be gone.
	_, err = os.Stat(filepath.Join(store.baseDir, "folder"))
	assert.True(t, os.IsNotExist(err))
}

func TestDeleteDirectoryEmpty(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create then delete doc to leave an empty dir.
	require.NoError(t, store.Create(ctx, "emptydir/doc", "content"))
	require.NoError(t, os.Remove(filepath.Join(store.baseDir, "emptydir", "doc.md")))

	err := store.Delete(ctx, "emptydir")
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(store.baseDir, "emptydir"))
	assert.True(t, os.IsNotExist(err))
}

func TestDeleteDirectoryNested(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "a/b/c/doc", "content"))

	err := store.Delete(ctx, "a/b/c")
	require.NoError(t, err)

	// c and its parents should be cleaned.
	_, err = os.Stat(filepath.Join(store.baseDir, "a", "b", "c"))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(store.baseDir, "a", "b"))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(store.baseDir, "a"))
	assert.True(t, os.IsNotExist(err))
}

func TestMutationRejectsAmbiguousFileAndDirectoryID(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, os.MkdirAll(filepath.Join(store.baseDir, "foo"), docDirPermissions))
	require.NoError(t, os.WriteFile(filepath.Join(store.baseDir, "foo.md"), []byte("file content\n"), filePermissions))
	require.NoError(t, os.WriteFile(filepath.Join(store.baseDir, "foo", "child.md"), []byte("child content\n"), filePermissions))

	assert.ErrorIs(t, store.Delete(ctx, "foo"), docs.ErrDocPathConflict)
	assert.ErrorIs(t, store.Rename(ctx, "foo", "renamed"), docs.ErrDocPathConflict)
	deleted, failed, err := store.DeleteBatch(ctx, []string{"foo"})
	require.NoError(t, err)
	assert.Empty(t, deleted)
	require.Len(t, failed, 1)
	assert.Equal(t, "foo", failed[0].ID)
	assert.Equal(t, docs.ErrDocPathConflict.Error(), failed[0].Error)

	doc, err := store.Get(ctx, "foo")
	require.NoError(t, err)
	assert.Equal(t, "file content", doc.Content)
	doc, err = store.Get(ctx, "foo/child")
	require.NoError(t, err)
	assert.Equal(t, "child content", doc.Content)
}

func TestDeleteDirectoryNotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	err := store.Delete(ctx, "nonexistent-dir")
	assert.ErrorIs(t, err, docs.ErrDocNotFound)
}

// ---------------------------------------------------------------------------
// Batch Delete Tests
// ---------------------------------------------------------------------------

func TestDeleteBatch(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "a", "content-a"))
	require.NoError(t, store.Create(ctx, "b", "content-b"))
	require.NoError(t, store.Create(ctx, "c", "content-c"))

	deleted, failed, err := store.DeleteBatch(ctx, []string{"a", "b", "c"})
	require.NoError(t, err)
	assert.Len(t, deleted, 3)
	assert.Empty(t, failed)

	// All should be gone.
	for _, id := range []string{"a", "b", "c"} {
		_, err := store.Get(ctx, id)
		assert.ErrorIs(t, err, docs.ErrDocNotFound)
	}
}

func TestDeleteBatchMixed(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "file1", "content1"))
	require.NoError(t, store.Create(ctx, "file2", "content2"))
	require.NoError(t, store.Create(ctx, "dir/child1", "child1"))
	require.NoError(t, store.Create(ctx, "dir/child2", "child2"))

	deleted, failed, err := store.DeleteBatch(ctx, []string{"file1", "file2", "dir"})
	require.NoError(t, err)
	assert.Len(t, deleted, 3)
	assert.Empty(t, failed)

	_, err = store.Get(ctx, "dir/child1")
	assert.ErrorIs(t, err, docs.ErrDocNotFound)
}

func TestDeleteBatchPartialFailure(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "valid1", "content1"))
	require.NoError(t, store.Create(ctx, "valid2", "content2"))

	deleted, failed, err := store.DeleteBatch(ctx, []string{"valid1", "..invalid", "valid2"})
	require.NoError(t, err)
	assert.Len(t, deleted, 2)
	assert.Len(t, failed, 1)
	assert.Equal(t, "..invalid", failed[0].ID)
}

func TestDeleteBatchDeduplication(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "dir/doc1", "content1"))
	require.NoError(t, store.Create(ctx, "dir/doc2", "content2"))

	// Delete dir (parent) and dir/doc1 (child) — child is subsumed.
	deleted, failed, err := store.DeleteBatch(ctx, []string{"dir", "dir/doc1"})
	require.NoError(t, err)
	assert.Len(t, deleted, 2)
	assert.Empty(t, failed)

	// Both should be gone.
	_, err = store.Get(ctx, "dir/doc1")
	assert.ErrorIs(t, err, docs.ErrDocNotFound)
}

func TestDeleteBatchAllNotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	deleted, failed, err := store.DeleteBatch(ctx, []string{"ghost1", "ghost2"})
	require.NoError(t, err)
	assert.Len(t, deleted, 2) // not-found = success (idempotency)
	assert.Empty(t, failed)
}

func TestDeleteBatchEmpty(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	deleted, failed, err := store.DeleteBatch(ctx, []string{})
	require.NoError(t, err)
	assert.Empty(t, deleted)
	assert.Empty(t, failed)
}

func TestDeleteBatchInvalidIDs(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	deleted, failed, err := store.DeleteBatch(ctx, []string{"..bad1", "../bad2"})
	require.NoError(t, err)
	assert.Empty(t, deleted)
	assert.Len(t, failed, 2)
}

func TestDeleteBatchPathTraversal(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "legit", "content"))

	deleted, failed, err := store.DeleteBatch(ctx, []string{"legit", "../escape"})
	require.NoError(t, err)
	assert.Len(t, deleted, 1)
	assert.Equal(t, "legit", deleted[0])
	assert.Len(t, failed, 1)
	assert.Equal(t, "../escape", failed[0].ID)
}

func TestDeleteBatchDirectoryWithFiles(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for i := range 5 {
		require.NoError(t, store.Create(ctx, "bigdir/doc"+strings.Repeat("x", i+1), "content"))
	}

	deleted, failed, err := store.DeleteBatch(ctx, []string{"bigdir"})
	require.NoError(t, err)
	assert.Len(t, deleted, 1)
	assert.Empty(t, failed)

	// All children should be gone.
	for i := range 5 {
		_, err := store.Get(ctx, "bigdir/doc"+strings.Repeat("x", i+1))
		assert.ErrorIs(t, err, docs.ErrDocNotFound)
	}
	// Parent dir should be cleaned.
	_, err = os.Stat(filepath.Join(store.baseDir, "bigdir"))
	assert.True(t, os.IsNotExist(err))
}

func TestDeleteBatchCleansEmptyParents(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "deep/nested/doc", "content"))

	deleted, failed, err := store.DeleteBatch(ctx, []string{"deep/nested/doc"})
	require.NoError(t, err)
	assert.Len(t, deleted, 1)
	assert.Empty(t, failed)

	// All parent dirs should be cleaned.
	_, err = os.Stat(filepath.Join(store.baseDir, "deep", "nested"))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(store.baseDir, "deep"))
	assert.True(t, os.IsNotExist(err))
}
