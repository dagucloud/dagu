// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"testing"

	apigen "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/wiki"
	workspacepkg "github.com/dagucloud/dagu/v2/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateWorkspaceMovesWikiPages(t *testing.T) {
	wikiStore := &mockWikiStore{pages: map[string]*wiki.Page{
		"ops/runbook": {ID: "ops/runbook", Title: "runbook", Content: "content"},
	}}
	workspaceStore := &mockWorkspaceStore{workspaces: []*workspacepkg.Workspace{
		{ID: "workspace-id", Name: "ops"},
	}}
	setup := newWikiPageTestSetupWithStore(t, wikiStore, workspaceStore)
	newName := apigen.WorkspaceName("platform")

	response, err := setup.api.UpdateWorkspace(adminCtx(), apigen.UpdateWorkspaceRequestObject{
		WorkspaceId: "workspace-id",
		Body:        &apigen.UpdateWorkspaceRequest{Name: &newName},
	})
	require.NoError(t, err)
	assert.IsType(t, apigen.UpdateWorkspace200JSONResponse{}, response)
	assert.NotContains(t, wikiStore.pages, "ops/runbook")
	assert.Equal(t, "content", wikiStore.pages["platform/runbook"].Content)
	require.Len(t, workspaceStore.workspaces, 1)
	assert.Equal(t, "platform", workspaceStore.workspaces[0].Name)
}

func TestUpdateWorkspaceRestoresWikiPagesWhenUpdateFails(t *testing.T) {
	wikiStore := &mockWikiStore{pages: map[string]*wiki.Page{
		"ops/runbook": {ID: "ops/runbook", Title: "runbook", Content: "content"},
	}}
	workspaceStore := &mockWorkspaceStore{
		workspaces: []*workspacepkg.Workspace{{ID: "workspace-id", Name: "ops"}},
		updateErr:  errForced,
	}
	setup := newWikiPageTestSetupWithStore(t, wikiStore, workspaceStore)
	newName := apigen.WorkspaceName("platform")

	_, err := setup.api.UpdateWorkspace(adminCtx(), apigen.UpdateWorkspaceRequestObject{
		WorkspaceId: "workspace-id",
		Body:        &apigen.UpdateWorkspaceRequest{Name: &newName},
	})
	require.ErrorIs(t, err, errForced)
	assert.Equal(t, "content", wikiStore.pages["ops/runbook"].Content)
	assert.NotContains(t, wikiStore.pages, "platform/runbook")
}

func TestUpdateWorkspaceRejectsOccupiedWikiPagePath(t *testing.T) {
	wikiStore := &mockWikiStore{pages: map[string]*wiki.Page{
		"ops/runbook":     {ID: "ops/runbook", Title: "runbook", Content: "source content"},
		"platform/readme": {ID: "platform/readme", Title: "readme", Content: "target content"},
	}}
	workspaceStore := &mockWorkspaceStore{workspaces: []*workspacepkg.Workspace{
		{ID: "workspace-id", Name: "ops"},
	}}
	setup := newWikiPageTestSetupWithStore(t, wikiStore, workspaceStore)
	newName := apigen.WorkspaceName("platform")

	response, err := setup.api.UpdateWorkspace(adminCtx(), apigen.UpdateWorkspaceRequestObject{
		WorkspaceId: "workspace-id",
		Body:        &apigen.UpdateWorkspaceRequest{Name: &newName},
	})
	require.NoError(t, err)
	assert.IsType(t, apigen.UpdateWorkspace409JSONResponse{}, response)
	assert.Equal(t, "source content", wikiStore.pages["ops/runbook"].Content)
	assert.Equal(t, "target content", wikiStore.pages["platform/readme"].Content)
	require.Len(t, workspaceStore.workspaces, 1)
	assert.Equal(t, "ops", workspaceStore.workspaces[0].Name)
}

func TestDeleteWorkspaceRequiresWikiPagesToBeRemoved(t *testing.T) {
	wikiStore := &mockWikiStore{pages: map[string]*wiki.Page{
		"ops/runbook": {ID: "ops/runbook", Title: "runbook", Content: "content"},
	}}
	workspaceStore := &mockWorkspaceStore{workspaces: []*workspacepkg.Workspace{
		{ID: "workspace-id", Name: "ops"},
	}}
	setup := newWikiPageTestSetupWithStore(t, wikiStore, workspaceStore)

	response, err := setup.api.DeleteWorkspace(adminCtx(), apigen.DeleteWorkspaceRequestObject{
		WorkspaceId: "workspace-id",
	})
	require.NoError(t, err)
	assert.IsType(t, apigen.DeleteWorkspace409JSONResponse{}, response)
	assert.Contains(t, wikiStore.pages, "ops/runbook")
	require.Len(t, workspaceStore.workspaces, 1)
}

func TestCreateWorkspaceRejectsExistingWikiPagePath(t *testing.T) {
	wikiStore := &mockWikiStore{pages: map[string]*wiki.Page{
		"ops/runbook": {ID: "ops/runbook", Title: "runbook", Content: "content"},
	}}
	workspaceStore := &mockWorkspaceStore{}
	setup := newWikiPageTestSetupWithStore(t, wikiStore, workspaceStore)

	response, err := setup.api.CreateWorkspace(adminCtx(), apigen.CreateWorkspaceRequestObject{
		Body: &apigen.CreateWorkspaceRequest{Name: "ops"},
	})
	require.NoError(t, err)
	assert.IsType(t, apigen.CreateWorkspace409JSONResponse{}, response)
	assert.Empty(t, workspaceStore.workspaces)
}
