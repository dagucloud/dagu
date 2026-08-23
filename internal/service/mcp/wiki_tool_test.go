// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	filewiki "github.com/dagucloud/dagu/v2/internal/persis/file/wiki"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	frontendapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/v2/internal/wiki"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func TestWikiPageResourceSupportsCanonicalAndLegacyURIs(t *testing.T) {
	ctx := context.Background()
	api, store := newWikiMCPTestAPI(t)
	require.NoError(t, store.Create(ctx, "operations/guides/deploy", "# Deploy\n\nneedle"))
	session := connectTestClient(t, ctx, NewServer(api))

	templates, err := session.ListResourceTemplates(ctx, nil)
	require.NoError(t, err)
	templateURIs := resourceTemplateURIs(templates.ResourceTemplates)
	require.Contains(t, templateURIs, "dagu://wiki/{workspace}/{path}")

	const uri = "dagu://wiki/operations/guides%2Fdeploy"
	input, readErr := parseReadResourceURI(uri)
	require.Nil(t, readErr)
	require.Equal(t, readInput{
		Target:    readTargetWikiPage,
		Workspace: "operations",
		Path:      "guides/deploy",
		URI:       uri,
	}, input)

	resource, err := session.ReadResource(ctx, &mcpsdk.ReadResourceParams{URI: uri})
	require.NoError(t, err)
	require.Len(t, resource.Contents, 1)
	require.Equal(t, resourceMIMEText, resource.Contents[0].MIMEType)
	require.Equal(t, "# Deploy\n\nneedle", resource.Contents[0].Text)

	// Legacy docs URIs stay readable through the read tool's URI mode and
	// resolve to canonical wiki output.
	legacy := callTool(t, ctx, session, toolRead, readInput{URI: "dagu://docs/operations/guides%2Fdeploy"})
	require.False(t, legacy.IsError)
	legacyOutput := structuredMap(t, legacy)
	require.Equal(t, readTargetWikiPage, legacyOutput["target"])
	require.Equal(t, uri, legacyOutput["uri"])
	legacyData, ok := legacyOutput["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "# Deploy\n\nneedle", legacyData["content"])
}

func TestReadToolListsReadsAndSearchesWikiPages(t *testing.T) {
	ctx := context.Background()
	api, store := newWikiMCPTestAPI(t)
	require.NoError(t, store.Create(ctx, "guides/debug", "# Debug\n\nneedle"))
	require.NoError(t, store.Create(ctx, "guides/deploy", "# Deploy\n\nneedle"))
	require.NoError(t, store.Create(ctx, "runbooks/restart", "# Restart\n\nneedle"))
	session := connectTestClient(t, ctx, NewServer(api))

	list := callTool(t, ctx, session, toolRead, readInput{
		Target:    readTargetWiki,
		Workspace: defaultWikiWorkspace,
		Query:     "flat=true&perPage=100",
		Prefix:    "guides",
	})
	require.False(t, list.IsError)
	require.Contains(t, structuredJSON(t, list), "dagu://wiki/default/guides%2Fdeploy")
	require.NotContains(t, structuredJSON(t, list), "runbooks")

	read := callTool(t, ctx, session, toolRead, readInput{
		Target:    readTargetWikiPage,
		Workspace: defaultWikiWorkspace,
		Path:      "guides/deploy",
	})
	require.False(t, read.IsError)
	require.Contains(t, structuredJSON(t, read), "# Deploy")

	search := callTool(t, ctx, session, toolRead, readInput{
		Target:    readTargetWikiSearch,
		Workspace: defaultWikiWorkspace,
		Search:    "needle",
		Prefix:    "guides",
		Limit:     1,
	})
	require.False(t, search.IsError)
	var firstPage struct {
		Data struct {
			Results []struct {
				ID         string `json:"id"`
				ModifiedAt any    `json:"modifiedAt"`
			} `json:"results"`
			HasMore    bool   `json:"hasMore"`
			NextCursor string `json:"nextCursor"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(structuredJSON(t, search)), &firstPage))
	require.Len(t, firstPage.Data.Results, 1)
	require.Equal(t, "guides/debug", firstPage.Data.Results[0].ID)
	require.NotNil(t, firstPage.Data.Results[0].ModifiedAt)
	require.True(t, firstPage.Data.HasMore)
	require.NotEmpty(t, firstPage.Data.NextCursor)

	next := callTool(t, ctx, session, toolRead, readInput{
		Target:    readTargetWikiSearch,
		Workspace: defaultWikiWorkspace,
		Search:    "needle",
		Prefix:    "guides",
		Cursor:    firstPage.Data.NextCursor,
		Limit:     1,
	})
	require.False(t, next.IsError)
	require.Contains(t, structuredJSON(t, next), "dagu://wiki/default/guides%2Fdeploy")
	require.NotContains(t, structuredJSON(t, next), "runbooks")
}

func TestReadToolRejectsInvalidWikiDiscoveryInput(t *testing.T) {
	for _, target := range []string{readTargetWikiSearch, legacyReadTargetDocSearch} {
		t.Run(target+" limit type", func(t *testing.T) {
			_, readErr := parseReadToolInput(json.RawMessage(`{
				"target":"` + target + `",
				"search":"needle",
				"limit":"1"
			}`))
			require.NotNil(t, readErr)
			require.Equal(t, readFieldLimit, readErr.Field)
		})

		t.Run(target+" limit range", func(t *testing.T) {
			_, readErr := parseReadToolInput(json.RawMessage(`{
				"target":"` + target + `",
				"search":"needle",
				"limit":51
			}`))
			require.NotNil(t, readErr)
			require.Equal(t, readFieldLimit, readErr.Field)
		})
	}

	for _, target := range []string{readTargetWiki, legacyReadTargetDocs} {
		t.Run(target+" query", func(t *testing.T) {
			_, readErr := parseReadToolInput(json.RawMessage(`{
				"target":"` + target + `",
				"prefix":"guides",
				"query":"page=%zz"
			}`))
			require.NotNil(t, readErr)
			require.Equal(t, readErrorInvalidToolInput, readErr.Code)
		})
	}
}

func TestChangeToolPreviewsAndAppliesWikiPageUpsert(t *testing.T) {
	ctx := context.Background()
	api, store := newWikiMCPTestAPI(t)
	session := connectTestClient(t, ctx, NewServer(api))
	input := changeInput{
		Type:      changeTypeUpsertWikiPage,
		Workspace: "operations",
		Path:      "runbooks/restart",
		Content:   "# Restart",
	}
	storedPath := "operations/" + input.Path

	preview := callTool(t, ctx, session, toolChange, input)
	require.False(t, preview.IsError)
	require.Contains(t, structuredJSON(t, preview), `"applied":false`)
	_, err := store.Get(ctx, storedPath)
	require.ErrorIs(t, err, wiki.ErrPageNotFound)

	input.Mode = changeModeApply
	apply := callTool(t, ctx, session, toolChange, input)
	require.False(t, apply.IsError)
	page, err := store.Get(ctx, storedPath)
	require.NoError(t, err)
	require.Equal(t, input.Content, page.Content)

	input.Content = "# Restart safely"
	update := callTool(t, ctx, session, toolChange, input)
	require.False(t, update.IsError)
	page, err = store.Get(ctx, storedPath)
	require.NoError(t, err)
	require.Equal(t, input.Content, page.Content)
}

func TestChangeToolRenamesAndDeletesWikiPageDirectories(t *testing.T) {
	ctx := context.Background()
	api, store := newWikiMCPTestAPI(t)
	require.NoError(t, store.Create(ctx, "guides/deploy", "# Deploy"))
	session := connectTestClient(t, ctx, NewServer(api))

	rename := changeInput{
		Type:      changeTypeRenameWikiPage,
		Workspace: defaultWikiWorkspace,
		Path:      "guides",
		NewPath:   "handbook",
	}
	preview := callTool(t, ctx, session, toolChange, rename)
	require.False(t, preview.IsError)
	require.Equal(t, "directory", structuredMap(t, preview)["nodeType"])
	_, err := store.Get(ctx, "guides/deploy")
	require.NoError(t, err)

	rename.Mode = changeModeApply
	apply := callTool(t, ctx, session, toolChange, rename)
	require.False(t, apply.IsError)
	_, err = store.Get(ctx, "handbook/deploy")
	require.NoError(t, err)
	_, err = store.Get(ctx, "guides/deploy")
	require.ErrorIs(t, err, wiki.ErrPageNotFound)

	remove := changeInput{
		Mode:      changeModeApply,
		Type:      changeTypeDeleteWikiPage,
		Workspace: defaultWikiWorkspace,
		Path:      "handbook",
	}
	deleted := callTool(t, ctx, session, toolChange, remove)
	require.False(t, deleted.IsError)
	_, err = store.Get(ctx, "handbook/deploy")
	require.ErrorIs(t, err, wiki.ErrPageNotFound)
}

func TestWikiPageUpsertPreviewReportsFileAncestorConflict(t *testing.T) {
	ctx := context.Background()
	api, store := newWikiMCPTestAPI(t)
	require.NoError(t, store.Create(ctx, "guides", "# Guides"))
	session := connectTestClient(t, ctx, NewServer(api))

	result := callTool(t, ctx, session, toolChange, changeInput{
		Type:      changeTypeUpsertWikiPage,
		Workspace: defaultWikiWorkspace,
		Path:      "guides/deploy",
		Content:   "# Deploy",
	})
	require.True(t, result.IsError)
	require.Equal(t, changeErrorConflict, structuredMap(t, result)["code"])
	_, err := store.Get(ctx, "guides")
	require.NoError(t, err)
}

func TestLegacyWikiPageChangeAliasRejectsAllWorkspace(t *testing.T) {
	_, changeErr := parseChangeToolInput(json.RawMessage(`{
		"type":"delete_doc",
		"workspace":"all",
		"path":"guides/deploy"
	}`))
	require.NotNil(t, changeErr)
	require.Equal(t, changeErrorInvalidToolInput, changeErr.Code)
	require.Equal(t, changeFieldWorkspace, changeErr.Field)
}

func TestWikiPageChangeApplyUsesWikiAPIPermissions(t *testing.T) {
	ctx := context.Background()
	api, store := newWikiMCPTestAPIWithWritePermission(t, false)
	session := connectTestClient(t, ctx, NewServer(api))

	result := callTool(t, ctx, session, toolChange, changeInput{
		Mode:      changeModeApply,
		Type:      changeTypeUpsertWikiPage,
		Workspace: defaultWikiWorkspace,
		Path:      "runbooks/restart",
		Content:   "# Restart",
	})
	require.True(t, result.IsError)
	require.Equal(t, changeErrorUnauthorized, structuredMap(t, result)["code"])
	_, err := store.Get(ctx, "runbooks/restart")
	require.ErrorIs(t, err, wiki.ErrPageNotFound)
}

func newWikiMCPTestAPI(t *testing.T) (*frontendapi.API, *filewiki.Store) {
	return newWikiMCPTestAPIWithWritePermission(t, true)
}

func newWikiMCPTestAPIWithWritePermission(t *testing.T, writeWiki bool) (*frontendapi.API, *filewiki.Store) {
	t.Helper()
	store, err := filewiki.New(t.TempDir())
	require.NoError(t, err)
	cfg := &config.Config{}
	cfg.Server.Permissions = map[config.Permission]bool{
		config.PermissionWriteDAGs: writeWiki,
	}
	api := frontendapi.New(
		nil,
		nil,
		nil,
		nil,
		runtime.Manager{},
		cfg,
		nil,
		nil,
		prometheus.NewRegistry(),
		nil,
		frontendapi.WithWikiStore(store),
	)
	return api, store
}

func callTool(
	t *testing.T,
	ctx context.Context,
	session *mcpsdk.ClientSession,
	name string,
	arguments any,
) *mcpsdk.CallToolResult {
	t.Helper()
	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: arguments})
	require.NoError(t, err)
	return result
}

func structuredJSON(t *testing.T, result *mcpsdk.CallToolResult) string {
	t.Helper()
	data, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	return string(data)
}

func structuredMap(t *testing.T, result *mcpsdk.CallToolResult) map[string]any {
	t.Helper()
	output, ok := result.StructuredContent.(map[string]any)
	require.True(t, ok)
	return output
}

func resourceTemplateURIs(templates []*mcpsdk.ResourceTemplate) []string {
	result := make([]string, 0, len(templates))
	for _, template := range templates {
		result = append(result, template.URITemplate)
	}
	return result
}
