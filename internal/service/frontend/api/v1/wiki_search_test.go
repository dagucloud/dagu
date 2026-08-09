// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"testing"

	apigen "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	apiv1 "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/v2/internal/wiki"
	workspacepkg "github.com/dagucloud/dagu/v2/internal/workspace"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchWikiPages(t *testing.T) {
	t.Parallel()

	t.Run("returns results", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)
		setup.store.pages["doc1"] = &wiki.Page{ID: "doc1", Title: "doc1", Description: "World doc", Content: "hello world"}
		setup.store.pages["doc2"] = &wiki.Page{ID: "doc2", Title: "doc2", Content: "goodbye world"}
		setup.store.pages["doc3"] = &wiki.Page{ID: "doc3", Title: "doc3", Content: "nothing here"}

		resp, err := setup.api.SearchWikiPages(adminCtx(), apigen.SearchWikiPagesRequestObject{
			Params: apigen.SearchWikiPagesParams{Q: "world"},
		})
		require.NoError(t, err)

		searchResp, ok := resp.(apigen.SearchWikiPages200JSONResponse)
		require.True(t, ok)
		assert.Len(t, searchResp.Results, 2)
		assert.Equal(t, "World doc", searchResp.Results[0].Description)
		require.NotNil(t, searchResp.Results[0].ModifiedAt)
	})

	t.Run("empty query", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)

		_, err := setup.api.SearchWikiPages(adminCtx(), apigen.SearchWikiPagesRequestObject{
			Params: apigen.SearchWikiPagesParams{Q: ""},
		})
		require.Error(t, err)

		_, err = setup.api.SearchWikiPages(adminCtx(), apigen.SearchWikiPagesRequestObject{
			Params: apigen.SearchWikiPagesParams{Q: "   "},
		})
		require.Error(t, err)
	})

	t.Run("no results", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)
		setup.store.pages["doc1"] = &wiki.Page{ID: "doc1", Title: "doc1", Content: "hello"}

		resp, err := setup.api.SearchWikiPages(adminCtx(), apigen.SearchWikiPagesRequestObject{
			Params: apigen.SearchWikiPagesParams{Q: "nonexistent-term"},
		})
		require.NoError(t, err)

		searchResp, ok := resp.(apigen.SearchWikiPages200JSONResponse)
		require.True(t, ok)
		assert.Empty(t, searchResp.Results)
	})

	t.Run("respects workspace visibility", func(t *testing.T) {
		t.Parallel()

		store := &mockWikiStore{pages: map[string]*wiki.Page{
			"ops/deploy":    {ID: "ops/deploy", Title: "deploy", Content: "needle"},
			"secret/deploy": {ID: "secret/deploy", Title: "deploy", Content: "needle"},
		}}
		setup := newWikiPageTestSetupWithStoreOptions(
			t,
			store,
			&mockWorkspaceStore{workspaces: []*workspacepkg.Workspace{
				{ID: "ops", Name: "ops"},
				{ID: "secret", Name: "secret"},
			}},
			apiv1.WithAuthService(stubAuthService{}),
		)
		ctx := auth.WithUser(context.Background(), &auth.User{
			Username: "viewer",
			Role:     auth.RoleViewer,
			WorkspaceAccess: &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{
				{Workspace: "ops", Role: auth.RoleViewer},
			}},
		})

		resp, err := setup.api.SearchWikiPages(ctx, apigen.SearchWikiPagesRequestObject{
			Params: apigen.SearchWikiPagesParams{Q: "needle"},
		})
		require.NoError(t, err)
		searchResp, ok := resp.(apigen.SearchWikiPages200JSONResponse)
		require.True(t, ok)
		require.Len(t, searchResp.Results, 1)
		assert.Equal(t, "ops/deploy", searchResp.Results[0].Id)
	})

	t.Run("no Wiki store", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{}
		a := apiv1.New(nil, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil)

		_, err := a.SearchWikiPages(adminCtx(), apigen.SearchWikiPagesRequestObject{
			Params: apigen.SearchWikiPagesParams{Q: "hello"},
		})
		require.Error(t, err)
	})
}

func TestSearchWikiPagesWithMatches(t *testing.T) {
	t.Parallel()

	t.Run("returns results with match details", func(t *testing.T) {
		t.Parallel()

		setup := newWikiPageTestSetup(t)
		setup.store.pages["doc1"] = &wiki.Page{
			ID: "doc1", Title: "doc1",
			Content: "line one\nhello world\nline three",
		}

		resp, err := setup.api.SearchWikiPages(adminCtx(), apigen.SearchWikiPagesRequestObject{
			Params: apigen.SearchWikiPagesParams{Q: "hello"},
		})
		require.NoError(t, err)

		searchResp, ok := resp.(apigen.SearchWikiPages200JSONResponse)
		require.True(t, ok)
		require.Len(t, searchResp.Results, 1)
		item := searchResp.Results[0]
		assert.Equal(t, "doc1", item.Id)
		require.NotNil(t, item.Matches)
		assert.Len(t, *item.Matches, 1)
		assert.Equal(t, "hello world", (*item.Matches)[0].Line)
		assert.Equal(t, 2, (*item.Matches)[0].LineNumber)
	})
}

func TestSearchWikiPageMatchesAcceptsAggregateCursorForWorkspaceResult(t *testing.T) {
	t.Parallel()

	setup := newWikiPageTestSetupWithWorkspaces(t, "ops")
	setup.store.pages["ops/deploy"] = &wiki.Page{
		ID:      "ops/deploy",
		Title:   "deploy",
		Content: "needle one\nneedle two\nneedle three\n",
	}

	feedResp, err := setup.api.SearchWikiPageFeed(adminCtx(), apigen.SearchWikiPageFeedRequestObject{
		Params: apigen.SearchWikiPageFeedParams{
			Q: "needle",
		},
	})
	require.NoError(t, err)

	feedPage, ok := feedResp.(apigen.SearchWikiPageFeed200JSONResponse)
	require.True(t, ok)
	require.Len(t, feedPage.Results, 1)
	assert.Equal(t, "ops/deploy", feedPage.Results[0].Id)
	require.NotNil(t, feedPage.Results[0].NextMatchesCursor)

	workspace := apigen.Workspace("ops")
	limit := apigen.SearchMatchLimit(2)
	matchesResp, err := setup.api.SearchWikiPageMatches(adminCtx(), apigen.SearchWikiPageMatchesRequestObject{
		Params: apigen.SearchWikiPageMatchesParams{
			Path:      "deploy",
			Q:         "needle",
			Limit:     &limit,
			Cursor:    feedPage.Results[0].NextMatchesCursor,
			Workspace: &workspace,
		},
	})
	require.NoError(t, err)

	matchesPage, ok := matchesResp.(apigen.SearchWikiPageMatches200JSONResponse)
	require.True(t, ok)
	assert.Len(t, matchesPage.Matches, 2)
	assert.False(t, matchesPage.HasMore)
	assert.Equal(t, 2, matchesPage.Matches[0].LineNumber)
	assert.Equal(t, 3, matchesPage.Matches[1].LineNumber)
}

func TestSearchWikiPageFeedSupportsPrefixAndCursor(t *testing.T) {
	t.Parallel()

	setup := newWikiPageTestSetup(t)
	setup.store.pages["guides/a"] = &wiki.Page{ID: "guides/a", Title: "a", Content: "needle"}
	setup.store.pages["guides/b"] = &wiki.Page{ID: "guides/b", Title: "b", Content: "needle"}
	setup.store.pages["runbooks/c"] = &wiki.Page{ID: "runbooks/c", Title: "c", Content: "needle"}
	prefix := apigen.WikiPagePrefix("guides")
	limit := apigen.SearchLimit(1)

	firstResp, err := setup.api.SearchWikiPageFeed(adminCtx(), apigen.SearchWikiPageFeedRequestObject{
		Params: apigen.SearchWikiPageFeedParams{
			Q:      "needle",
			Prefix: &prefix,
			Limit:  &limit,
		},
	})
	require.NoError(t, err)
	firstPage, ok := firstResp.(apigen.SearchWikiPageFeed200JSONResponse)
	require.True(t, ok)
	require.Len(t, firstPage.Results, 1)
	assert.Equal(t, "guides/a", firstPage.Results[0].Id)
	require.NotNil(t, firstPage.Results[0].ModifiedAt)
	assert.True(t, firstPage.HasMore)
	require.NotNil(t, firstPage.NextCursor)

	secondResp, err := setup.api.SearchWikiPageFeed(adminCtx(), apigen.SearchWikiPageFeedRequestObject{
		Params: apigen.SearchWikiPageFeedParams{
			Q:      "needle",
			Prefix: &prefix,
			Limit:  &limit,
			Cursor: firstPage.NextCursor,
		},
	})
	require.NoError(t, err)
	secondPage, ok := secondResp.(apigen.SearchWikiPageFeed200JSONResponse)
	require.True(t, ok)
	require.Len(t, secondPage.Results, 1)
	assert.Equal(t, "guides/b", secondPage.Results[0].Id)
	assert.False(t, secondPage.HasMore)

	otherPrefix := apigen.WikiPagePrefix("runbooks")
	_, err = setup.api.SearchWikiPageFeed(adminCtx(), apigen.SearchWikiPageFeedRequestObject{
		Params: apigen.SearchWikiPageFeedParams{
			Q:      "needle",
			Prefix: &otherPrefix,
			Limit:  &limit,
			Cursor: firstPage.NextCursor,
		},
	})
	require.Error(t, err)
}
