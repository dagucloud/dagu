// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/docs"
	filedoc "github.com/dagucloud/dagu/v2/internal/persis/file/doc"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	frontendapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

func TestDocumentResourceSupportsWorkspaceAndNestedPath(t *testing.T) {
	ctx := context.Background()
	api, store := newDocsMCPTestAPI(t)
	require.NoError(t, store.Create(ctx, "operations/guides/deploy", "# Deploy\n\nneedle"))
	session := connectTestClient(t, ctx, NewServer(api))

	templates, err := session.ListResourceTemplates(ctx, nil)
	require.NoError(t, err)
	require.Contains(t, resourceTemplateURIs(templates.ResourceTemplates), "dagu://docs/{workspace}/{path}")

	const uri = "dagu://docs/operations/guides%2Fdeploy"
	input, readErr := parseReadResourceURI(uri)
	require.Nil(t, readErr)
	require.Equal(t, readInput{
		Target:    readTargetDoc,
		Workspace: "operations",
		Path:      "guides/deploy",
		URI:       uri,
	}, input)

	resource, err := session.ReadResource(ctx, &mcpsdk.ReadResourceParams{URI: uri})
	require.NoError(t, err)
	require.Len(t, resource.Contents, 1)
	require.Equal(t, resourceMIMEText, resource.Contents[0].MIMEType)
	require.Equal(t, "# Deploy\n\nneedle", resource.Contents[0].Text)
}

func TestReadToolListsReadsAndSearchesDocuments(t *testing.T) {
	ctx := context.Background()
	api, store := newDocsMCPTestAPI(t)
	require.NoError(t, store.Create(ctx, "guides/debug", "# Debug\n\nneedle"))
	require.NoError(t, store.Create(ctx, "guides/deploy", "# Deploy\n\nneedle"))
	require.NoError(t, store.Create(ctx, "runbooks/restart", "# Restart\n\nneedle"))
	session := connectTestClient(t, ctx, NewServer(api))

	list := callTool(t, ctx, session, toolRead, readInput{
		Target:    readTargetDocs,
		Workspace: defaultDocWorkspace,
		Query:     "flat=true&perPage=100",
		Prefix:    "guides",
	})
	require.False(t, list.IsError)
	require.Contains(t, structuredJSON(t, list), "dagu://docs/default/guides%2Fdeploy")
	require.NotContains(t, structuredJSON(t, list), "runbooks")

	read := callTool(t, ctx, session, toolRead, readInput{
		Target:    readTargetDoc,
		Workspace: defaultDocWorkspace,
		Path:      "guides/deploy",
	})
	require.False(t, read.IsError)
	require.Contains(t, structuredJSON(t, read), "# Deploy")

	search := callTool(t, ctx, session, toolRead, readInput{
		Target:    readTargetDocSearch,
		Workspace: defaultDocWorkspace,
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
		Target:    readTargetDocSearch,
		Workspace: defaultDocWorkspace,
		Search:    "needle",
		Prefix:    "guides",
		Cursor:    firstPage.Data.NextCursor,
		Limit:     1,
	})
	require.False(t, next.IsError)
	require.Contains(t, structuredJSON(t, next), "dagu://docs/default/guides%2Fdeploy")
	require.NotContains(t, structuredJSON(t, next), "runbooks")
}

func TestReadToolRejectsInvalidDocumentDiscoveryInput(t *testing.T) {
	_, readErr := parseReadToolInput(json.RawMessage(`{
		"target":"doc_search",
		"search":"needle",
		"limit":"1"
	}`))
	require.NotNil(t, readErr)
	require.Equal(t, readFieldLimit, readErr.Field)

	_, readErr = parseReadToolInput(json.RawMessage(`{
		"target":"doc_search",
		"search":"needle",
		"limit":51
	}`))
	require.NotNil(t, readErr)
	require.Equal(t, readFieldLimit, readErr.Field)

	_, readErr = parseReadToolInput(json.RawMessage(`{
		"target":"docs",
		"prefix":"guides",
		"query":"page=%zz"
	}`))
	require.NotNil(t, readErr)
	require.Equal(t, readErrorInvalidToolInput, readErr.Code)
}

func TestChangeToolPreviewsAndAppliesDocumentUpsert(t *testing.T) {
	ctx := context.Background()
	api, store := newDocsMCPTestAPI(t)
	session := connectTestClient(t, ctx, NewServer(api))
	input := changeInput{
		Type:      changeTypeUpsertDoc,
		Workspace: "operations",
		Path:      "runbooks/restart",
		Content:   "# Restart",
	}
	storedPath := "operations/" + input.Path

	preview := callTool(t, ctx, session, toolChange, input)
	require.False(t, preview.IsError)
	require.Contains(t, structuredJSON(t, preview), `"applied":false`)
	_, err := store.Get(ctx, storedPath)
	require.ErrorIs(t, err, docs.ErrDocNotFound)

	input.Mode = changeModeApply
	apply := callTool(t, ctx, session, toolChange, input)
	require.False(t, apply.IsError)
	doc, err := store.Get(ctx, storedPath)
	require.NoError(t, err)
	require.Equal(t, input.Content, doc.Content)

	input.Content = "# Restart safely"
	update := callTool(t, ctx, session, toolChange, input)
	require.False(t, update.IsError)
	doc, err = store.Get(ctx, storedPath)
	require.NoError(t, err)
	require.Equal(t, input.Content, doc.Content)
}

func TestChangeToolRenamesAndDeletesDocumentDirectories(t *testing.T) {
	ctx := context.Background()
	api, store := newDocsMCPTestAPI(t)
	require.NoError(t, store.Create(ctx, "guides/deploy", "# Deploy"))
	session := connectTestClient(t, ctx, NewServer(api))

	rename := changeInput{
		Type:      changeTypeRenameDoc,
		Workspace: defaultDocWorkspace,
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
	require.ErrorIs(t, err, docs.ErrDocNotFound)

	remove := changeInput{
		Mode:      changeModeApply,
		Type:      changeTypeDeleteDoc,
		Workspace: defaultDocWorkspace,
		Path:      "handbook",
	}
	deleted := callTool(t, ctx, session, toolChange, remove)
	require.False(t, deleted.IsError)
	_, err = store.Get(ctx, "handbook/deploy")
	require.ErrorIs(t, err, docs.ErrDocNotFound)
}

func TestDocumentUpsertPreviewReportsFileAncestorConflict(t *testing.T) {
	ctx := context.Background()
	api, store := newDocsMCPTestAPI(t)
	require.NoError(t, store.Create(ctx, "guides", "# Guides"))
	session := connectTestClient(t, ctx, NewServer(api))

	result := callTool(t, ctx, session, toolChange, changeInput{
		Type:      changeTypeUpsertDoc,
		Workspace: defaultDocWorkspace,
		Path:      "guides/deploy",
		Content:   "# Deploy",
	})
	require.True(t, result.IsError)
	require.Equal(t, changeErrorConflict, structuredMap(t, result)["code"])
	_, err := store.Get(ctx, "guides")
	require.NoError(t, err)
}

func TestDocumentChangeRejectsAllWorkspace(t *testing.T) {
	_, changeErr := parseChangeToolInput(json.RawMessage(`{
		"type":"delete_doc",
		"workspace":"all",
		"path":"guides/deploy"
	}`))
	require.NotNil(t, changeErr)
	require.Equal(t, changeErrorInvalidToolInput, changeErr.Code)
	require.Equal(t, changeFieldWorkspace, changeErr.Field)
}

func TestDocumentChangeApplyUsesDocumentsAPIPermissions(t *testing.T) {
	ctx := context.Background()
	api, store := newDocsMCPTestAPIWithWritePermission(t, false)
	session := connectTestClient(t, ctx, NewServer(api))

	result := callTool(t, ctx, session, toolChange, changeInput{
		Mode:      changeModeApply,
		Type:      changeTypeUpsertDoc,
		Workspace: defaultDocWorkspace,
		Path:      "runbooks/restart",
		Content:   "# Restart",
	})
	require.True(t, result.IsError)
	require.Equal(t, changeErrorUnauthorized, structuredMap(t, result)["code"])
	_, err := store.Get(ctx, "runbooks/restart")
	require.ErrorIs(t, err, docs.ErrDocNotFound)
}

func newDocsMCPTestAPI(t *testing.T) (*frontendapi.API, *filedoc.Store) {
	return newDocsMCPTestAPIWithWritePermission(t, true)
}

func newDocsMCPTestAPIWithWritePermission(t *testing.T, writeDocs bool) (*frontendapi.API, *filedoc.Store) {
	t.Helper()
	store, err := filedoc.New(t.TempDir())
	require.NoError(t, err)
	cfg := &config.Config{}
	cfg.Server.Permissions = map[config.Permission]bool{
		config.PermissionWriteDAGs: writeDocs,
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
		frontendapi.WithDocStore(store),
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
