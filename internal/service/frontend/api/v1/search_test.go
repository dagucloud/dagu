// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	apigen "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/filedag"
	"github.com/dagucloud/dagu/internal/persis/filedoc"
	"github.com/dagucloud/dagu/internal/runtime"
	apiv1 "github.com/dagucloud/dagu/internal/service/frontend/api/v1"
	workspacepkg "github.com/dagucloud/dagu/internal/workspace"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type searchTestSetup struct {
	api      *apiv1.API
	dagStore exec.DAGStore
	docStore agent.DocStore
}

func newSearchAPI(dagStore exec.DAGStore, docStore agent.DocStore, extraOptions ...apiv1.APIOption) *apiv1.API {
	cfg := &config.Config{}

	options := []apiv1.APIOption{}
	if docStore != nil {
		options = append(options, apiv1.WithDocStore(docStore))
	}
	options = append(options, extraOptions...)

	return apiv1.New(
		dagStore,
		nil,
		nil,
		nil,
		runtime.Manager{},
		cfg,
		nil,
		nil,
		prometheus.NewRegistry(),
		nil,
		options...,
	)
}

func newSearchTestSetup(t *testing.T, withDocs bool) *searchTestSetup {
	t.Helper()

	dagStore := filedag.New(t.TempDir(), filedag.WithSkipExamples(true))
	var docStore agent.DocStore
	if withDocs {
		docStore = filedoc.New(t.TempDir())
	}

	return &searchTestSetup{
		api:      newSearchAPI(dagStore, docStore),
		dagStore: dagStore,
		docStore: docStore,
	}
}

func mustCreateDAG(t *testing.T, setup *searchTestSetup, name, spec string) {
	t.Helper()
	err := setup.dagStore.Create(context.Background(), name, []byte(spec))
	require.NoError(t, err)
}

func mustCreateDoc(t *testing.T, setup *searchTestSetup, id, content string) {
	t.Helper()
	require.NotNil(t, setup.docStore)
	err := setup.docStore.Create(context.Background(), id, content)
	require.NoError(t, err)
}

func TestSearchDAGFeed(t *testing.T) {
	t.Parallel()

	setup := newSearchTestSetup(t, true)

	mustCreateDAG(t, setup, "a-match", `name: a-match
steps:
  - run: echo "Needle."
  - run: echo "needle."
  - run: echo "needle."`)
	mustCreateDAG(t, setup, "b-match", `name: b-match
steps:
  - run: echo "needle."`)
	mustCreateDAG(t, setup, "c-skip", `name: c-skip
steps:
  - run: echo "needleX"`)

	limit := apigen.SearchLimit(1)
	resp, err := setup.api.SearchDAGFeed(adminCtx(), apigen.SearchDAGFeedRequestObject{
		Params: apigen.SearchDAGFeedParams{
			Q:     " needle. ",
			Limit: &limit,
		},
	})
	require.NoError(t, err)

	searchResp := resp.(apigen.SearchDAGFeed200JSONResponse)
	require.Len(t, searchResp.Results, 1)
	assert.Equal(t, "a-match", searchResp.Results[0].FileName)
	assert.True(t, searchResp.Results[0].HasMoreMatches)
	assert.NotNil(t, searchResp.Results[0].NextMatchesCursor)
	assert.Len(t, searchResp.Results[0].Matches, 1)
	assert.True(t, searchResp.HasMore)
	require.NotNil(t, searchResp.NextCursor)

	secondResp, err := setup.api.SearchDAGFeed(adminCtx(), apigen.SearchDAGFeedRequestObject{
		Params: apigen.SearchDAGFeedParams{
			Q:      "needle.",
			Limit:  &limit,
			Cursor: searchResp.NextCursor,
		},
	})
	require.NoError(t, err)

	secondPage := secondResp.(apigen.SearchDAGFeed200JSONResponse)
	require.Len(t, secondPage.Results, 1)
	assert.Equal(t, "b-match", secondPage.Results[0].FileName)
	assert.False(t, secondPage.HasMore)
	assert.Nil(t, secondPage.NextCursor)
}

func TestSearchDocFeed(t *testing.T) {
	t.Parallel()

	t.Run("returns cursor-based document results", func(t *testing.T) {
		t.Parallel()

		setup := newSearchTestSetup(t, true)
		mustCreateDoc(t, setup, "alpha", "Needle.\nneedle.\nneedle.")
		mustCreateDoc(t, setup, "beta", "needle.")
		mustCreateDoc(t, setup, "gamma", "needleX")

		limit := apigen.SearchLimit(1)
		resp, err := setup.api.SearchDocFeed(adminCtx(), apigen.SearchDocFeedRequestObject{
			Params: apigen.SearchDocFeedParams{
				Q:     "needle.",
				Limit: &limit,
			},
		})
		require.NoError(t, err)

		searchResp := resp.(apigen.SearchDocFeed200JSONResponse)
		require.Len(t, searchResp.Results, 1)
		assert.Equal(t, "alpha", searchResp.Results[0].Id)
		assert.True(t, searchResp.Results[0].HasMoreMatches)
		assert.NotNil(t, searchResp.Results[0].NextMatchesCursor)
		assert.Len(t, searchResp.Results[0].Matches, 1)
		assert.True(t, searchResp.HasMore)
		require.NotNil(t, searchResp.NextCursor)

		secondResp, err := setup.api.SearchDocFeed(adminCtx(), apigen.SearchDocFeedRequestObject{
			Params: apigen.SearchDocFeedParams{
				Q:      "needle.",
				Limit:  &limit,
				Cursor: searchResp.NextCursor,
			},
		})
		require.NoError(t, err)

		secondPage := secondResp.(apigen.SearchDocFeed200JSONResponse)
		require.Len(t, secondPage.Results, 1)
		assert.Equal(t, "beta", secondPage.Results[0].Id)
		assert.False(t, secondPage.HasMore)
	})

	t.Run("returns forbidden when doc search is unavailable", func(t *testing.T) {
		t.Parallel()

		setup := newSearchTestSetup(t, false)
		resp, err := setup.api.SearchDocFeed(adminCtx(), apigen.SearchDocFeedRequestObject{
			Params: apigen.SearchDocFeedParams{Q: "needle."},
		})
		require.Nil(t, resp)
		require.Error(t, err)

		apiErr, ok := err.(*apiv1.Error)
		require.True(t, ok)
		assert.Equal(t, 403, apiErr.HTTPStatus)
	})

	t.Run("filters hidden workspace docs before cursor pagination", func(t *testing.T) {
		t.Parallel()

		dagStore := filedag.New(t.TempDir(), filedag.WithSkipExamples(true))
		docStore := filedoc.New(t.TempDir())
		api := newSearchAPI(
			dagStore,
			docStore,
			apiv1.WithWorkspaceStore(&mockWorkspaceStore{
				workspaces: []*workspacepkg.Workspace{{ID: "workspace-1", Name: "ops"}},
			}),
		)
		setup := &searchTestSetup{api: api, dagStore: dagStore, docStore: docStore}
		mustCreateDoc(t, setup, "ops/hidden", "needle.")
		mustCreateDoc(t, setup, "public", "needle.")

		limit := apigen.SearchLimit(1)
		workspace := apigen.Workspace("default")
		resp, err := api.SearchDocFeed(adminCtx(), apigen.SearchDocFeedRequestObject{
			Params: apigen.SearchDocFeedParams{
				Q:         "needle.",
				Limit:     &limit,
				Workspace: &workspace,
			},
		})
		require.NoError(t, err)

		searchResp := resp.(apigen.SearchDocFeed200JSONResponse)
		require.Len(t, searchResp.Results, 1)
		assert.Equal(t, "public", searchResp.Results[0].Id)
		assert.False(t, searchResp.HasMore)
		assert.Nil(t, searchResp.NextCursor)
	})
}

func TestSearchDagMatches(t *testing.T) {
	t.Parallel()

	setup := newSearchTestSetup(t, false)
	mustCreateDAG(t, setup, "match-heavy", `name: match-heavy
steps:
  - run: echo "needle."
  - run: echo "needle."
  - run: echo "needle."
  - run: echo "needle."`)

	limit := apigen.SearchMatchLimit(3)
	resp, err := setup.api.SearchDagMatches(adminCtx(), apigen.SearchDagMatchesRequestObject{
		FileName: "match-heavy",
		Params: apigen.SearchDagMatchesParams{
			Q:     "needle.",
			Limit: &limit,
		},
	})
	require.NoError(t, err)

	matchResp := resp.(apigen.SearchDagMatches200JSONResponse)
	assert.Len(t, matchResp.Matches, 3)
	assert.True(t, matchResp.HasMore)
	require.NotNil(t, matchResp.NextCursor)

	secondResp, err := setup.api.SearchDagMatches(adminCtx(), apigen.SearchDagMatchesRequestObject{
		FileName: "match-heavy",
		Params: apigen.SearchDagMatchesParams{
			Q:      "needle.",
			Limit:  &limit,
			Cursor: matchResp.NextCursor,
		},
	})
	require.NoError(t, err)

	secondPage := secondResp.(apigen.SearchDagMatches200JSONResponse)
	assert.Len(t, secondPage.Matches, 1)
	assert.False(t, secondPage.HasMore)
}

func TestSearchDagMatchesUsesWorkspaceFromFeedCursor(t *testing.T) {
	t.Parallel()

	setup := newSearchTestSetup(t, false)
	mustCreateDAG(t, setup, "ops-heavy", `name: ops-heavy
labels:
  - workspace=ops
steps:
  - run: echo "needle."
  - run: echo "needle."
  - run: echo "needle."`)

	workspace := apigen.Workspace("ops")
	feedResp, err := setup.api.SearchDAGFeed(adminCtx(), apigen.SearchDAGFeedRequestObject{
		Params: apigen.SearchDAGFeedParams{
			Q:         "needle.",
			Workspace: &workspace,
		},
	})
	require.NoError(t, err)

	feedPage := feedResp.(apigen.SearchDAGFeed200JSONResponse)
	require.Len(t, feedPage.Results, 1)
	require.NotNil(t, feedPage.Results[0].NextMatchesCursor)

	limit := apigen.SearchMatchLimit(2)
	matchesResp, err := setup.api.SearchDagMatches(adminCtx(), apigen.SearchDagMatchesRequestObject{
		FileName: "ops-heavy",
		Params: apigen.SearchDagMatchesParams{
			Q:         "needle.",
			Limit:     &limit,
			Cursor:    feedPage.Results[0].NextMatchesCursor,
			Workspace: &workspace,
		},
	})
	require.NoError(t, err)

	matchesPage := matchesResp.(apigen.SearchDagMatches200JSONResponse)
	assert.Len(t, matchesPage.Matches, 2)
	assert.False(t, matchesPage.HasMore)
}

func TestSearchDocMatches(t *testing.T) {
	t.Parallel()

	setup := newSearchTestSetup(t, true)
	mustCreateDoc(t, setup, "guides/runbook", "needle.\nneedle.\nneedle.\nneedle.")

	limit := apigen.SearchMatchLimit(3)
	resp, err := setup.api.SearchDocMatches(adminCtx(), apigen.SearchDocMatchesRequestObject{
		Params: apigen.SearchDocMatchesParams{
			Path:  "guides/runbook",
			Q:     "needle.",
			Limit: &limit,
		},
	})
	require.NoError(t, err)

	matchResp := resp.(apigen.SearchDocMatches200JSONResponse)
	assert.Len(t, matchResp.Matches, 3)
	assert.True(t, matchResp.HasMore)
	require.NotNil(t, matchResp.NextCursor)

	secondResp, err := setup.api.SearchDocMatches(adminCtx(), apigen.SearchDocMatchesRequestObject{
		Params: apigen.SearchDocMatchesParams{
			Path:   "guides/runbook",
			Q:      "needle.",
			Limit:  &limit,
			Cursor: matchResp.NextCursor,
		},
	})
	require.NoError(t, err)

	secondPage := secondResp.(apigen.SearchDocMatches200JSONResponse)
	assert.Len(t, secondPage.Matches, 1)
	assert.False(t, secondPage.HasMore)
}

func TestSearchInvalidCursor(t *testing.T) {
	t.Parallel()

	setup := newSearchTestSetup(t, true)
	mustCreateDAG(t, setup, "match-heavy", `name: match-heavy
steps:
  - run: echo "needle."`)

	cursor := apigen.SearchCursor("bad-cursor")
	resp, err := setup.api.SearchDAGFeed(adminCtx(), apigen.SearchDAGFeedRequestObject{
		Params: apigen.SearchDAGFeedParams{
			Q:      "needle.",
			Cursor: &cursor,
		},
	})
	require.Nil(t, resp)
	require.Error(t, err)

	apiErr, ok := err.(*apiv1.Error)
	require.True(t, ok)
	assert.Equal(t, 400, apiErr.HTTPStatus)
}

func TestSearchDAGFeedReturnsErrorWhenSearchRootIsBroken(t *testing.T) {
	t.Parallel()

	basePath := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(basePath, []byte("x"), 0600))

	api := newSearchAPI(filedag.New(basePath, filedag.WithSkipExamples(true)), nil)
	resp, err := api.SearchDAGFeed(adminCtx(), apigen.SearchDAGFeedRequestObject{
		Params: apigen.SearchDAGFeedParams{Q: "needle"},
	})
	require.Nil(t, resp)
	require.Error(t, err)

	apiErr, ok := err.(*apiv1.Error)
	require.True(t, ok)
	assert.Equal(t, 500, apiErr.HTTPStatus)
}

func TestSearchDocFeedReturnsErrorWhenSearchRootIsBroken(t *testing.T) {
	t.Parallel()

	docBasePath := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(docBasePath, []byte("x"), 0600))

	api := newSearchAPI(
		filedag.New(t.TempDir(), filedag.WithSkipExamples(true)),
		filedoc.New(docBasePath),
	)
	resp, err := api.SearchDocFeed(adminCtx(), apigen.SearchDocFeedRequestObject{
		Params: apigen.SearchDocFeedParams{Q: "needle"},
	})
	require.Nil(t, resp)
	require.Error(t, err)

	apiErr, ok := err.(*apiv1.Error)
	require.True(t, ok)
	assert.Equal(t, 500, apiErr.HTTPStatus)
}
