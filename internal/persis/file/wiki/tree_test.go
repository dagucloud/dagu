// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package wiki

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	wikimodel "github.com/dagucloud/dagu/v2/internal/wiki"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListTreeSortNameAsc(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "c-page", "c"))
	require.NoError(t, store.Create(ctx, "a-page", "a"))
	require.NoError(t, store.Create(ctx, "b-page", "b"))

	result, err := store.List(ctx, wikimodel.ListPagesOptions{Page: 1, PerPage: 50, Sort: wikimodel.PageSortFieldName, Order: wikimodel.PageSortOrderAsc})
	require.NoError(t, err)
	require.Len(t, result.Items, 3)
	assert.Equal(t, "a-page", result.Items[0].ID)
	assert.Equal(t, "b-page", result.Items[1].ID)
	assert.Equal(t, "c-page", result.Items[2].ID)
}

func TestListTreeSortNameDesc(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "c-page", "c"))
	require.NoError(t, store.Create(ctx, "a-page", "a"))
	require.NoError(t, store.Create(ctx, "b-page", "b"))

	result, err := store.List(ctx, wikimodel.ListPagesOptions{Page: 1, PerPage: 50, Sort: wikimodel.PageSortFieldName, Order: wikimodel.PageSortOrderDesc})
	require.NoError(t, err)
	require.Len(t, result.Items, 3)
	assert.Equal(t, "c-page", result.Items[0].ID)
	assert.Equal(t, "b-page", result.Items[1].ID)
	assert.Equal(t, "a-page", result.Items[2].ID)
}

func TestListTreeSortTypeAsc(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "file-b", "b"))
	require.NoError(t, store.Create(ctx, "dir-a/child", "c"))
	require.NoError(t, store.Create(ctx, "file-a", "a"))

	result, err := store.List(ctx, wikimodel.ListPagesOptions{Page: 1, PerPage: 50, Sort: wikimodel.PageSortFieldType, Order: wikimodel.PageSortOrderAsc})
	require.NoError(t, err)
	require.Len(t, result.Items, 3)
	assert.Equal(t, "directory", result.Items[0].Type)
	assert.Equal(t, "dir-a", result.Items[0].Name)
	assert.Equal(t, "file", result.Items[1].Type)
	assert.Equal(t, "file-a.md", result.Items[1].Name)
	assert.Equal(t, "file", result.Items[2].Type)
	assert.Equal(t, "file-b.md", result.Items[2].Name)
}

func TestListTreeSortTypeDesc(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "file-b", "b"))
	require.NoError(t, store.Create(ctx, "dir-a/child", "c"))
	require.NoError(t, store.Create(ctx, "file-a", "a"))

	result, err := store.List(ctx, wikimodel.ListPagesOptions{Page: 1, PerPage: 50, Sort: wikimodel.PageSortFieldType, Order: wikimodel.PageSortOrderDesc})
	require.NoError(t, err)
	require.Len(t, result.Items, 3)
	assert.Equal(t, "file", result.Items[0].Type)
	assert.Equal(t, "file", result.Items[1].Type)
	assert.Equal(t, "directory", result.Items[2].Type)
}

func TestListTreeSortMtimeDesc(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "old", "old"))
	require.NoError(t, store.Create(ctx, "mid", "mid"))
	require.NoError(t, store.Create(ctx, "new", "new"))

	now := time.Now()
	setModTime(t, filepath.Join(store.baseDir, "old.md"), now.Add(-2*time.Hour))
	setModTime(t, filepath.Join(store.baseDir, "mid.md"), now.Add(-1*time.Hour))
	setModTime(t, filepath.Join(store.baseDir, "new.md"), now)

	result, err := store.List(ctx, wikimodel.ListPagesOptions{Page: 1, PerPage: 50, Sort: wikimodel.PageSortFieldMTime, Order: wikimodel.PageSortOrderDesc})
	require.NoError(t, err)
	require.Len(t, result.Items, 3)
	assert.Equal(t, "new", result.Items[0].ID)
	assert.Equal(t, "mid", result.Items[1].ID)
	assert.Equal(t, "old", result.Items[2].ID)
}

func TestListTreeSortMtimeAsc(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "old", "old"))
	require.NoError(t, store.Create(ctx, "mid", "mid"))
	require.NoError(t, store.Create(ctx, "new", "new"))

	now := time.Now()
	setModTime(t, filepath.Join(store.baseDir, "old.md"), now.Add(-2*time.Hour))
	setModTime(t, filepath.Join(store.baseDir, "mid.md"), now.Add(-1*time.Hour))
	setModTime(t, filepath.Join(store.baseDir, "new.md"), now)

	result, err := store.List(ctx, wikimodel.ListPagesOptions{Page: 1, PerPage: 50, Sort: wikimodel.PageSortFieldMTime, Order: wikimodel.PageSortOrderAsc})
	require.NoError(t, err)
	require.Len(t, result.Items, 3)
	assert.Equal(t, "old", result.Items[0].ID)
	assert.Equal(t, "mid", result.Items[1].ID)
	assert.Equal(t, "new", result.Items[2].ID)
}

func TestListTreePropagatesDirectoryMtime(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "a-dir/child", "old child"))
	require.NoError(t, store.Create(ctx, "b-dir/child", "new child"))

	now := time.Now()
	oldTime := now.Add(-2 * time.Hour)
	setModTime(t, filepath.Join(store.baseDir, "a-dir", "child.md"), oldTime)
	setModTime(t, filepath.Join(store.baseDir, "b-dir", "child.md"), now)
	setModTime(t, filepath.Join(store.baseDir, "a-dir"), now.Add(-4*time.Hour))
	setModTime(t, filepath.Join(store.baseDir, "b-dir"), now.Add(-4*time.Hour))

	result, err := store.List(ctx, wikimodel.ListPagesOptions{Page: 1, PerPage: 50, Sort: wikimodel.PageSortFieldMTime, Order: wikimodel.PageSortOrderDesc})
	require.NoError(t, err)
	require.Len(t, result.Items, 2)

	nodes := make(map[string]*wikimodel.PageTreeNode, len(result.Items))
	for _, node := range result.Items {
		nodes[node.ID] = node
	}
	require.Contains(t, nodes, "a-dir")
	require.Contains(t, nodes, "b-dir")
	assert.Equal(t, oldTime.Unix(), nodes["a-dir"].ModTime.Unix())
	assert.Equal(t, now.Unix(), nodes["b-dir"].ModTime.Unix())
}

func TestListTreeSortMtimeStable(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "b-page", "b"))
	require.NoError(t, store.Create(ctx, "a-page", "a"))

	sameTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	setModTime(t, filepath.Join(store.baseDir, "a-page.md"), sameTime)
	setModTime(t, filepath.Join(store.baseDir, "b-page.md"), sameTime)

	result, err := store.List(ctx, wikimodel.ListPagesOptions{Page: 1, PerPage: 50, Sort: wikimodel.PageSortFieldMTime, Order: wikimodel.PageSortOrderAsc})
	require.NoError(t, err)
	require.Len(t, result.Items, 2)
	assert.Equal(t, "a-page", result.Items[0].ID)
	assert.Equal(t, "b-page", result.Items[1].ID)
}

func TestListTreeSortDefaultsToTypeAsc(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "file-a", "a"))
	require.NoError(t, store.Create(ctx, "dir-a/child", "c"))

	result, err := store.List(ctx, wikimodel.ListPagesOptions{Page: 1, PerPage: 50})
	require.NoError(t, err)
	require.Len(t, result.Items, 2)
	assert.Equal(t, "directory", result.Items[0].Type)
	assert.Equal(t, "file", result.Items[1].Type)
}

func TestListFlatSortNameDesc(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "a-page", "a"))
	require.NoError(t, store.Create(ctx, "c-page", "c"))
	require.NoError(t, store.Create(ctx, "b-page", "b"))

	result, err := store.ListFlat(ctx, wikimodel.ListPagesOptions{Page: 1, PerPage: 50, Sort: wikimodel.PageSortFieldName, Order: wikimodel.PageSortOrderDesc})
	require.NoError(t, err)
	require.Len(t, result.Items, 3)
	assert.Equal(t, "c-page", result.Items[0].ID)
	assert.Equal(t, "b-page", result.Items[1].ID)
	assert.Equal(t, "a-page", result.Items[2].ID)
}

func TestListFlatSortMtime(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "old-page", "old"))
	require.NoError(t, store.Create(ctx, "new-page", "new"))

	now := time.Now()
	setModTime(t, filepath.Join(store.baseDir, "old-page.md"), now.Add(-1*time.Hour))
	setModTime(t, filepath.Join(store.baseDir, "new-page.md"), now)

	result, err := store.ListFlat(ctx, wikimodel.ListPagesOptions{Page: 1, PerPage: 50, Sort: wikimodel.PageSortFieldMTime, Order: wikimodel.PageSortOrderDesc})
	require.NoError(t, err)
	require.Len(t, result.Items, 2)
	assert.Equal(t, "new-page", result.Items[0].ID)
	assert.Equal(t, "old-page", result.Items[1].ID)
}

func TestListTreeSortNestedChildren(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "dir/c-child", "c"))
	require.NoError(t, store.Create(ctx, "dir/a-child", "a"))
	require.NoError(t, store.Create(ctx, "dir/b-child", "b"))

	result, err := store.List(ctx, wikimodel.ListPagesOptions{Page: 1, PerPage: 50, Sort: wikimodel.PageSortFieldName, Order: wikimodel.PageSortOrderAsc})
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.Len(t, result.Items[0].Children, 3)
	assert.Equal(t, "dir/a-child", result.Items[0].Children[0].ID)
	assert.Equal(t, "dir/b-child", result.Items[0].Children[1].ID)
	assert.Equal(t, "dir/c-child", result.Items[0].Children[2].ID)
}

func TestListTreeSortMtimeKeepsDirectoriesAlphabetical(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "alpha/child", "alpha"))
	require.NoError(t, store.Create(ctx, "beta/child", "beta"))
	require.NoError(t, store.Create(ctx, "old-file", "old"))
	require.NoError(t, store.Create(ctx, "new-file", "new"))

	now := time.Now()
	setModTime(t, filepath.Join(store.baseDir, "alpha", "child.md"), now.Add(-4*time.Hour))
	setModTime(t, filepath.Join(store.baseDir, "beta", "child.md"), now.Add(-1*time.Hour))
	setModTime(t, filepath.Join(store.baseDir, "old-file.md"), now.Add(-3*time.Hour))
	setModTime(t, filepath.Join(store.baseDir, "new-file.md"), now)

	result, err := store.List(ctx, wikimodel.ListPagesOptions{
		Page:    1,
		PerPage: 50,
		Sort:    wikimodel.PageSortFieldMTime,
		Order:   wikimodel.PageSortOrderDesc,
	})
	require.NoError(t, err)
	require.Len(t, result.Items, 4)

	assert.Equal(t, "alpha", result.Items[0].ID)
	assert.Equal(t, "beta", result.Items[1].ID)
	assert.Equal(t, "new-file", result.Items[2].ID)
	assert.Equal(t, "old-file", result.Items[3].ID)
}

func TestListTreeSortPaginationConsistency(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.Create(ctx, "c-page", "c"))
	require.NoError(t, store.Create(ctx, "a-page", "a"))
	require.NoError(t, store.Create(ctx, "b-page", "b"))

	opts := wikimodel.ListPagesOptions{Page: 1, PerPage: 2, Sort: wikimodel.PageSortFieldName, Order: wikimodel.PageSortOrderDesc}
	page1, err := store.List(ctx, opts)
	require.NoError(t, err)
	require.Len(t, page1.Items, 2)

	opts.Page = 2
	page2, err := store.List(ctx, opts)
	require.NoError(t, err)
	require.Len(t, page2.Items, 1)

	assert.Equal(t, "c-page", page1.Items[0].ID)
	assert.Equal(t, "b-page", page1.Items[1].ID)
	assert.Equal(t, "a-page", page2.Items[0].ID)
}

func TestPropagateModTime(t *testing.T) {
	now := time.Now()
	nodes := []*wikimodel.PageTreeNode{
		{
			ID: "dir", Name: "dir", Type: "directory",
			ModTime: now.Add(-10 * time.Hour),
			Children: []*wikimodel.PageTreeNode{
				{ID: "dir/old", Name: "old", Type: "file", ModTime: now.Add(-5 * time.Hour)},
				{
					ID: "dir/sub", Name: "sub", Type: "directory",
					ModTime: now.Add(-8 * time.Hour),
					Children: []*wikimodel.PageTreeNode{
						{ID: "dir/sub/newest", Name: "newest", Type: "file", ModTime: now},
					},
				},
			},
		},
	}

	maxTime := propagateModTime(nodes)
	assert.Equal(t, now.Unix(), maxTime.Unix())
	assert.Equal(t, now.Unix(), nodes[0].ModTime.Unix())
	assert.Equal(t, now.Unix(), nodes[0].Children[1].ModTime.Unix())
}

func TestPropagateModTimeEmptyDir(t *testing.T) {
	dirTime := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	nodes := []*wikimodel.PageTreeNode{
		{
			ID: "empty", Name: "empty", Type: "directory",
			ModTime:  dirTime,
			Children: []*wikimodel.PageTreeNode{},
		},
	}

	propagateModTime(nodes)
	assert.Equal(t, dirTime, nodes[0].ModTime)
}
