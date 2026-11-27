// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func listProjects(t *testing.T, server test.Server, token string) api.ProjectListResponse {
	t.Helper()
	resp := server.Client().Get("/api/v1/projects").
		WithBearerToken(token).
		ExpectStatus(http.StatusOK).
		Send(t)
	var result api.ProjectListResponse
	resp.Unmarshal(t, &result)
	return result
}

func projectNames(list api.ProjectListResponse) []string {
	names := make([]string, 0, len(list.Projects))
	for _, p := range list.Projects {
		names = append(names, p.Name)
	}
	return names
}

// TestProjects proves Spec 102 "Lifecycle" over the API: an admin creates
// a project, changes its display name, and deletes it, and every caller
// lists only the projects it has a role in.
func TestProjects(t *testing.T) {
	t.Parallel()
	server := setupProjectsAuthServer(t, "payments")
	_, err := projectregistry.New(server.Config.Paths.DataDir).EnsureDefault(context.Background())
	require.NoError(t, err)
	adminToken := getAdminToken(t, server)

	resp := server.Client().Post("/api/v1/projects", api.CreateProjectRequest{Name: "ops", DisplayName: new("Operations")}).
		WithBearerToken(adminToken).
		ExpectStatus(http.StatusCreated).
		Send(t)
	var created api.Project
	resp.Unmarshal(t, &created)
	assert.Equal(t, "ops", created.Name)
	assert.Equal(t, "Operations", created.DisplayName)
	assert.Equal(t, api.ProjectStateActive, created.State)
	assert.Equal(t, api.UserRoleAdmin, created.Role)
	assert.DirExists(t, filepath.Join(server.Config.Paths.ProjectsDir, "ops"))

	for _, tc := range []struct {
		name   string
		status int
	}{{"OPS", http.StatusConflict}, {"-ops", http.StatusBadRequest}, {"default", http.StatusBadRequest}} {
		server.Client().Post("/api/v1/projects", api.CreateProjectRequest{Name: tc.name}).
			WithBearerToken(adminToken).
			ExpectStatus(tc.status).
			Send(t)
	}

	server.Client().Patch("/api/v1/projects/ops", api.UpdateProjectRequest{}).
		WithBearerToken(adminToken).
		ExpectStatus(http.StatusBadRequest).
		Send(t)
	resp = server.Client().Patch("/api/v1/projects/OPS", api.UpdateProjectRequest{DisplayName: new("Ops team")}).
		WithBearerToken(adminToken).
		ExpectStatus(http.StatusOK).
		Send(t)
	var updated api.Project
	resp.Unmarshal(t, &updated)
	assert.Equal(t, "Ops team", updated.DisplayName)

	list := listProjects(t, server, adminToken)
	assert.Equal(t, []string{"default", "ops", "payments"}, projectNames(list))
	require.NotNil(t, list.UnattachedStateDirs)
	assert.Empty(t, *list.UnattachedStateDirs)

	viewer := createAPIKey(t, server, adminToken,
		newProjectAPIKeyRequest("payments-viewer", api.ProjectGrant{Project: "payments", Role: api.UserRoleViewer}))
	list = listProjects(t, server, viewer.Key)
	require.Len(t, list.Projects, 1)
	assert.Equal(t, "payments", list.Projects[0].Name)
	assert.Equal(t, api.UserRoleViewer, list.Projects[0].Role)
	assert.Nil(t, list.UnattachedStateDirs, "only admins see unattached state directories")
	server.Client().Post("/api/v1/projects", api.CreateProjectRequest{Name: "mine"}).
		WithBearerToken(viewer.Key).
		ExpectStatus(http.StatusForbidden).
		Send(t)
	server.Client().Delete("/api/v1/projects/payments").
		WithBearerToken(viewer.Key).
		ExpectStatus(http.StatusForbidden).
		Send(t)

	// A git checkout owns the files in it, so the delete is refused.
	gitDir := filepath.Join(server.Config.Paths.ProjectsDir, "ops", ".git")
	require.NoError(t, os.MkdirAll(gitDir, 0o750))
	server.Client().Delete("/api/v1/projects/ops").
		WithBearerToken(adminToken).
		ExpectStatus(http.StatusConflict).
		Send(t)
	require.NoError(t, os.RemoveAll(gitDir))

	server.Client().Delete("/api/v1/projects/ops").
		WithBearerToken(adminToken).
		ExpectStatus(http.StatusNoContent).
		Send(t)
	assert.Equal(t, []string{"default", "payments"}, projectNames(listProjects(t, server, adminToken)))
	assert.NoDirExists(t, filepath.Join(server.Config.Paths.ProjectsDir, "ops"))
	server.Client().Delete("/api/v1/projects/ops").
		WithBearerToken(adminToken).
		ExpectStatus(http.StatusNotFound).
		Send(t)
	server.Client().Delete("/api/v1/projects/default").
		WithBearerToken(adminToken).
		ExpectStatus(http.StatusConflict).
		Send(t)

	// The payments key had a role only through its grant.
	server.Client().Delete("/api/v1/projects/payments").
		WithBearerToken(adminToken).
		ExpectStatus(http.StatusNoContent).
		Send(t)
	assert.NotContains(t, listAPIKeyNames(t, server, adminToken), "payments-viewer")
}

// TestProjectsOff proves that /projects answers as a path that does not
// exist while projects are off, a body that does not decode included.
func TestProjectsOff(t *testing.T) {
	t.Parallel()
	server := setupBuiltinAuthServer(t)
	adminToken := getAdminToken(t, server)
	absent := server.Client().Get("/api/v1/no-such-path").WithBearerToken(adminToken).Send(t)
	require.Equal(t, http.StatusNotFound, absent.Response.StatusCode())

	for _, request := range []*test.Request{
		server.Client().Get("/api/v1/projects"),
		server.Client().Post("/api/v1/projects", api.CreateProjectRequest{Name: "ops"}),
		server.Client().Post("/api/v1/projects", "not an object"),
		server.Client().Patch("/api/v1/projects/ops", api.UpdateProjectRequest{DisplayName: new("Ops")}),
		server.Client().Delete("/api/v1/projects/ops"),
	} {
		resp := request.WithBearerToken(adminToken).Send(t)
		assert.Equal(t, absent.Response.StatusCode(), resp.Response.StatusCode())
		assert.Equal(t, absent.Body, resp.Body)
		assert.Equal(t, absent.Response.Header().Get("Content-Type"), resp.Response.Header().Get("Content-Type"))
	}
	assert.NoDirExists(t, filepath.Join(server.Config.Paths.ProjectsDir, "ops"))
}
