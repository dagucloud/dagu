// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"testing"

	apigen "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/core/docs"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	apiv1 "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchDocs(t *testing.T) {
	t.Parallel()

	t.Run("returns results", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)
		setup.store.docs["doc1"] = &docs.Doc{ID: "doc1", Title: "doc1", Description: "World doc", Content: "hello world"}
		setup.store.docs["doc2"] = &docs.Doc{ID: "doc2", Title: "doc2", Content: "goodbye world"}
		setup.store.docs["doc3"] = &docs.Doc{ID: "doc3", Title: "doc3", Content: "nothing here"}

		resp, err := setup.api.SearchDocs(adminCtx(), apigen.SearchDocsRequestObject{
			Params: apigen.SearchDocsParams{Q: "world"},
		})
		require.NoError(t, err)

		searchResp, ok := resp.(apigen.SearchDocs200JSONResponse)
		require.True(t, ok)
		assert.Len(t, searchResp.Results, 2)
		assert.Equal(t, "World doc", searchResp.Results[0].Description)
	})

	t.Run("empty query", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)

		_, err := setup.api.SearchDocs(adminCtx(), apigen.SearchDocsRequestObject{
			Params: apigen.SearchDocsParams{Q: ""},
		})
		require.Error(t, err)

		_, err = setup.api.SearchDocs(adminCtx(), apigen.SearchDocsRequestObject{
			Params: apigen.SearchDocsParams{Q: "   "},
		})
		require.Error(t, err)
	})

	t.Run("no results", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)
		setup.store.docs["doc1"] = &docs.Doc{ID: "doc1", Title: "doc1", Content: "hello"}

		resp, err := setup.api.SearchDocs(adminCtx(), apigen.SearchDocsRequestObject{
			Params: apigen.SearchDocsParams{Q: "nonexistent-term"},
		})
		require.NoError(t, err)

		searchResp, ok := resp.(apigen.SearchDocs200JSONResponse)
		require.True(t, ok)
		assert.Empty(t, searchResp.Results)
	})

	t.Run("no doc store", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{}
		a := apiv1.New(nil, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil)

		_, err := a.SearchDocs(adminCtx(), apigen.SearchDocsRequestObject{
			Params: apigen.SearchDocsParams{Q: "hello"},
		})
		require.Error(t, err)
	})
}

func TestSearchDocsWithMatches(t *testing.T) {
	t.Parallel()

	t.Run("returns results with match details", func(t *testing.T) {
		t.Parallel()

		setup := newDocTestSetup(t)
		setup.store.docs["doc1"] = &docs.Doc{
			ID: "doc1", Title: "doc1",
			Content: "line one\nhello world\nline three",
		}

		resp, err := setup.api.SearchDocs(adminCtx(), apigen.SearchDocsRequestObject{
			Params: apigen.SearchDocsParams{Q: "hello"},
		})
		require.NoError(t, err)

		searchResp, ok := resp.(apigen.SearchDocs200JSONResponse)
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

func TestSearchDocMatchesAcceptsAggregateCursorForWorkspaceResult(t *testing.T) {
	t.Parallel()

	setup := newDocTestSetupWithWorkspaces(t, "ops")
	setup.store.docs["ops/deploy"] = &docs.Doc{
		ID:      "ops/deploy",
		Title:   "deploy",
		Content: "needle one\nneedle two\nneedle three\n",
	}

	feedResp, err := setup.api.SearchDocFeed(adminCtx(), apigen.SearchDocFeedRequestObject{
		Params: apigen.SearchDocFeedParams{
			Q: "needle",
		},
	})
	require.NoError(t, err)

	feedPage := feedResp.(apigen.SearchDocFeed200JSONResponse)
	require.Len(t, feedPage.Results, 1)
	assert.Equal(t, "ops/deploy", feedPage.Results[0].Id)
	require.NotNil(t, feedPage.Results[0].NextMatchesCursor)

	workspace := apigen.Workspace("ops")
	limit := apigen.SearchMatchLimit(2)
	matchesResp, err := setup.api.SearchDocMatches(adminCtx(), apigen.SearchDocMatchesRequestObject{
		Params: apigen.SearchDocMatchesParams{
			Path:      "deploy",
			Q:         "needle",
			Limit:     &limit,
			Cursor:    feedPage.Results[0].NextMatchesCursor,
			Workspace: &workspace,
		},
	})
	require.NoError(t, err)

	matchesPage := matchesResp.(apigen.SearchDocMatches200JSONResponse)
	assert.Len(t, matchesPage.Matches, 2)
	assert.False(t, matchesPage.HasMore)
	assert.Equal(t, 2, matchesPage.Matches[0].LineNumber)
	assert.Equal(t, 3, matchesPage.Matches[1].LineNumber)
}
