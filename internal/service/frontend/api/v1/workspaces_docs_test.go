// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"testing"

	apigen "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/core/docs"
	workspacepkg "github.com/dagucloud/dagu/v2/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateWorkspaceMovesDocuments(t *testing.T) {
	docStore := &mockDocStore{docs: map[string]*docs.Doc{
		"ops/runbook": {ID: "ops/runbook", Title: "runbook", Content: "content"},
	}}
	workspaceStore := &mockWorkspaceStore{workspaces: []*workspacepkg.Workspace{
		{ID: "workspace-id", Name: "ops"},
	}}
	setup := newDocTestSetupWithStore(t, docStore, workspaceStore)
	newName := apigen.WorkspaceName("platform")

	response, err := setup.api.UpdateWorkspace(adminCtx(), apigen.UpdateWorkspaceRequestObject{
		WorkspaceId: "workspace-id",
		Body:        &apigen.UpdateWorkspaceRequest{Name: &newName},
	})
	require.NoError(t, err)
	assert.IsType(t, apigen.UpdateWorkspace200JSONResponse{}, response)
	assert.NotContains(t, docStore.docs, "ops/runbook")
	assert.Equal(t, "content", docStore.docs["platform/runbook"].Content)
	require.Len(t, workspaceStore.workspaces, 1)
	assert.Equal(t, "platform", workspaceStore.workspaces[0].Name)
}

func TestUpdateWorkspaceRestoresDocumentsWhenUpdateFails(t *testing.T) {
	docStore := &mockDocStore{docs: map[string]*docs.Doc{
		"ops/runbook": {ID: "ops/runbook", Title: "runbook", Content: "content"},
	}}
	workspaceStore := &mockWorkspaceStore{
		workspaces: []*workspacepkg.Workspace{{ID: "workspace-id", Name: "ops"}},
		updateErr:  errForced,
	}
	setup := newDocTestSetupWithStore(t, docStore, workspaceStore)
	newName := apigen.WorkspaceName("platform")

	_, err := setup.api.UpdateWorkspace(adminCtx(), apigen.UpdateWorkspaceRequestObject{
		WorkspaceId: "workspace-id",
		Body:        &apigen.UpdateWorkspaceRequest{Name: &newName},
	})
	require.ErrorIs(t, err, errForced)
	assert.Equal(t, "content", docStore.docs["ops/runbook"].Content)
	assert.NotContains(t, docStore.docs, "platform/runbook")
}

func TestUpdateWorkspaceRejectsOccupiedDocumentPath(t *testing.T) {
	docStore := &mockDocStore{docs: map[string]*docs.Doc{
		"ops/runbook":     {ID: "ops/runbook", Title: "runbook", Content: "source content"},
		"platform/readme": {ID: "platform/readme", Title: "readme", Content: "target content"},
	}}
	workspaceStore := &mockWorkspaceStore{workspaces: []*workspacepkg.Workspace{
		{ID: "workspace-id", Name: "ops"},
	}}
	setup := newDocTestSetupWithStore(t, docStore, workspaceStore)
	newName := apigen.WorkspaceName("platform")

	response, err := setup.api.UpdateWorkspace(adminCtx(), apigen.UpdateWorkspaceRequestObject{
		WorkspaceId: "workspace-id",
		Body:        &apigen.UpdateWorkspaceRequest{Name: &newName},
	})
	require.NoError(t, err)
	assert.IsType(t, apigen.UpdateWorkspace409JSONResponse{}, response)
	assert.Equal(t, "source content", docStore.docs["ops/runbook"].Content)
	assert.Equal(t, "target content", docStore.docs["platform/readme"].Content)
	require.Len(t, workspaceStore.workspaces, 1)
	assert.Equal(t, "ops", workspaceStore.workspaces[0].Name)
}

func TestDeleteWorkspaceRequiresDocumentsToBeRemoved(t *testing.T) {
	docStore := &mockDocStore{docs: map[string]*docs.Doc{
		"ops/runbook": {ID: "ops/runbook", Title: "runbook", Content: "content"},
	}}
	workspaceStore := &mockWorkspaceStore{workspaces: []*workspacepkg.Workspace{
		{ID: "workspace-id", Name: "ops"},
	}}
	setup := newDocTestSetupWithStore(t, docStore, workspaceStore)

	response, err := setup.api.DeleteWorkspace(adminCtx(), apigen.DeleteWorkspaceRequestObject{
		WorkspaceId: "workspace-id",
	})
	require.NoError(t, err)
	assert.IsType(t, apigen.DeleteWorkspace409JSONResponse{}, response)
	assert.Contains(t, docStore.docs, "ops/runbook")
	require.Len(t, workspaceStore.workspaces, 1)
}

func TestCreateWorkspaceRejectsExistingDocumentPath(t *testing.T) {
	docStore := &mockDocStore{docs: map[string]*docs.Doc{
		"ops/runbook": {ID: "ops/runbook", Title: "runbook", Content: "content"},
	}}
	workspaceStore := &mockWorkspaceStore{}
	setup := newDocTestSetupWithStore(t, docStore, workspaceStore)

	response, err := setup.api.CreateWorkspace(adminCtx(), apigen.CreateWorkspaceRequestObject{
		Body: &apigen.CreateWorkspaceRequest{Name: "ops"},
	})
	require.NoError(t, err)
	assert.IsType(t, apigen.CreateWorkspace409JSONResponse{}, response)
	assert.Empty(t, workspaceStore.workspaces)
}
