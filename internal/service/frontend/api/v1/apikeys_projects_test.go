// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupProjectsAuthServer starts a server with projects on and registers
// the named projects.
func setupProjectsAuthServer(t *testing.T, projects ...string) test.Server {
	t.Helper()
	server := setupBuiltinAuthServer(t, func(cfg *config.Config) {
		cfg.ProjectsEnabled = true
	})
	registry := projectregistry.New(server.Config.Paths.DataDir)
	for _, name := range projects {
		_, err := registry.Create(context.Background(), project.CreateInput{Name: name})
		require.NoError(t, err)
	}
	return server
}

// newProjectAPIKeyRequest returns a request for a key whose only roles are
// grants on projects: viewer limited to no workspace, plus the grants.
func newProjectAPIKeyRequest(name string, grants ...api.ProjectGrant) api.CreateAPIKeyRequest {
	request := newCreateAPIKeyRequest(name, api.UserRoleViewer)
	request.WorkspaceAccess = &api.WorkspaceAccess{All: false, Grants: []api.WorkspaceGrant{}}
	request.ProjectGrants = &grants
	return request
}

func createAPIKey(t *testing.T, server test.Server, token string, request api.CreateAPIKeyRequest) api.CreateAPIKeyResponse {
	t.Helper()
	resp := server.Client().Post("/api/v1/api-keys", request).
		WithBearerToken(token).
		ExpectStatus(http.StatusCreated).
		Send(t)
	var result api.CreateAPIKeyResponse
	resp.Unmarshal(t, &result)
	return result
}

func listAPIKeyNames(t *testing.T, server test.Server, token string) []string {
	t.Helper()
	resp := server.Client().Get("/api/v1/api-keys").
		WithBearerToken(token).
		ExpectStatus(http.StatusOK).
		Send(t)
	var result api.APIKeysListResponse
	resp.Unmarshal(t, &result)
	names := make([]string, 0, len(result.ApiKeys))
	for _, key := range result.ApiKeys {
		names = append(names, key.Name)
	}
	return names
}

// createProjectManagerKey creates a key that is manager of payments and
// viewer of infra, so it sees both projects but manages only payments.
func createProjectManagerKey(t *testing.T, server test.Server, adminToken string) api.CreateAPIKeyResponse {
	t.Helper()
	return createAPIKey(t, server, adminToken, newProjectAPIKeyRequest("payments-manager",
		api.ProjectGrant{Project: "payments", Role: api.UserRoleManager},
		api.ProjectGrant{Project: "infra", Role: api.UserRoleViewer}))
}

// A manager of project X creates an API key limited to X, and cannot create
// one with a server-wide role or a grant on a project where it is not
// manager (RFC 003 criterion 33). The manager here calls with an API key,
// whose grants reach the request's user.
func TestAPIKeys_ProjectManagerCreatesProjectKeys(t *testing.T) {
	t.Parallel()
	server := setupProjectsAuthServer(t, "payments", "infra", "hidden")
	adminToken := getAdminToken(t, server)

	manager := createProjectManagerKey(t, server, adminToken)
	require.NotNil(t, manager.ApiKey.ProjectGrants)
	assert.ElementsMatch(t, api.ProjectGrants{
		{Project: "payments", Role: api.UserRoleManager},
		{Project: "infra", Role: api.UserRoleViewer},
	}, *manager.ApiKey.ProjectGrants)

	created := createAPIKey(t, server, manager.Key,
		newProjectAPIKeyRequest("payments-runner", api.ProjectGrant{Project: "Payments", Role: api.UserRoleOperator}))
	require.NotNil(t, created.ApiKey.ProjectGrants)
	assert.Equal(t, api.ProjectGrants{{Project: "payments", Role: api.UserRoleOperator}}, *created.ApiKey.ProjectGrants)
	// Grants go up to the manager role, which the caller already holds there.
	createAPIKey(t, server, manager.Key,
		newProjectAPIKeyRequest("payments-deputy", api.ProjectGrant{Project: "payments", Role: api.UserRoleManager}))

	refused := []struct {
		name    string
		request api.CreateAPIKeyRequest
		status  int
	}{
		{"ServerWideRole", newCreateAPIKeyRequest("server-wide", api.UserRoleViewer), http.StatusForbidden},
		{"GrantOnOtherProject", newProjectAPIKeyRequest("infra-runner",
			api.ProjectGrant{Project: "infra", Role: api.UserRoleOperator}), http.StatusForbidden},
		{"GrantOnBothProjects", newProjectAPIKeyRequest("both",
			api.ProjectGrant{Project: "payments", Role: api.UserRoleOperator},
			api.ProjectGrant{Project: "infra", Role: api.UserRoleViewer}), http.StatusForbidden},
		{"MissingProject", newProjectAPIKeyRequest("missing",
			api.ProjectGrant{Project: "missing", Role: api.UserRoleViewer}), http.StatusBadRequest},
		// A project the caller cannot see answers as a missing one.
		{"HiddenProject", newProjectAPIKeyRequest("hidden",
			api.ProjectGrant{Project: "hidden", Role: api.UserRoleViewer}), http.StatusBadRequest},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			server.Client().Post("/api/v1/api-keys", tc.request).
				WithBearerToken(manager.Key).
				ExpectStatus(tc.status).
				Send(t)
		})
	}

	ownedByOther := newProjectAPIKeyRequest("owned", api.ProjectGrant{Project: "payments", Role: api.UserRoleViewer})
	ownedByOther.AttributionClass = api.CreateAPIKeyRequestAttributionClassUserOwned
	otherUser := "another-user"
	ownedByOther.OwnerUserId = &otherUser
	server.Client().Post("/api/v1/api-keys", ownedByOther).
		WithBearerToken(manager.Key).
		ExpectStatus(http.StatusForbidden).
		Send(t)
}

// A project manager sees, changes, and deletes only the keys it manages, and
// cannot change one into a key it does not manage.
func TestAPIKeys_ProjectManagerManagesOnlyItsKeys(t *testing.T) {
	t.Parallel()
	server := setupProjectsAuthServer(t, "payments", "infra")
	adminToken := getAdminToken(t, server)

	manager := createProjectManagerKey(t, server, adminToken)
	runner := createAPIKey(t, server, adminToken,
		newProjectAPIKeyRequest("payments-runner", api.ProjectGrant{Project: "payments", Role: api.UserRoleOperator}))
	serverWide := createAPIKey(t, server, adminToken, newCreateAPIKeyRequest("server-wide", api.UserRoleOperator))
	createAPIKey(t, server, adminToken,
		newProjectAPIKeyRequest("infra-runner", api.ProjectGrant{Project: "infra", Role: api.UserRoleOperator}))

	// The manager key itself has a grant on infra, which it does not manage.
	assert.Equal(t, []string{"payments-runner"}, listAPIKeyNames(t, server, manager.Key))
	assert.Len(t, listAPIKeyNames(t, server, adminToken), 4)

	runnerPath := "/api/v1/api-keys/" + runner.ApiKey.Id
	developer := api.ProjectGrants{{Project: "payments", Role: api.UserRoleDeveloper}}
	resp := server.Client().Patch(runnerPath, api.UpdateAPIKeyRequest{ProjectGrants: &developer}).
		WithBearerToken(manager.Key).
		ExpectStatus(http.StatusOK).
		Send(t)
	var updated api.APIKeyResponse
	resp.Unmarshal(t, &updated)
	require.NotNil(t, updated.ApiKey.ProjectGrants)
	assert.Equal(t, developer, *updated.ApiKey.ProjectGrants)

	withInfra := api.ProjectGrants{
		{Project: "payments", Role: api.UserRoleDeveloper},
		{Project: "infra", Role: api.UserRoleViewer},
	}
	server.Client().Patch(runnerPath, api.UpdateAPIKeyRequest{ProjectGrants: &withInfra}).
		WithBearerToken(manager.Key).
		ExpectStatus(http.StatusForbidden).
		Send(t)
	server.Client().Patch(runnerPath, api.UpdateAPIKeyRequest{
		WorkspaceAccess: &api.WorkspaceAccess{All: true, Grants: []api.WorkspaceGrant{}},
	}).WithBearerToken(manager.Key).ExpectStatus(http.StatusForbidden).Send(t)

	serverWidePath := "/api/v1/api-keys/" + serverWide.ApiKey.Id
	server.Client().Get(serverWidePath).WithBearerToken(manager.Key).ExpectStatus(http.StatusNotFound).Send(t)
	name := "renamed"
	server.Client().Patch(serverWidePath, api.UpdateAPIKeyRequest{Name: &name}).
		WithBearerToken(manager.Key).
		ExpectStatus(http.StatusNotFound).
		Send(t)
	server.Client().Delete(serverWidePath).WithBearerToken(manager.Key).ExpectStatus(http.StatusNotFound).Send(t)

	server.Client().Delete(runnerPath).WithBearerToken(manager.Key).ExpectStatus(http.StatusNoContent).Send(t)
	assert.Empty(t, listAPIKeyNames(t, server, manager.Key))
}

// A key owned by another user needs an admin, even when its grants name only
// projects the caller manages: a manager must not change what a key acting
// as that user may do.
func TestAPIKeys_ProjectManagerCannotManageAnotherUsersKey(t *testing.T) {
	t.Parallel()
	server := setupProjectsAuthServer(t, "payments", "infra")
	resp := server.Client().Post("/api/v1/auth/login", api.LoginRequest{
		Username: "admin",
		Password: "adminpass",
	}).ExpectStatus(http.StatusOK).Send(t)
	var login api.LoginResponse
	resp.Unmarshal(t, &login)

	manager := createProjectManagerKey(t, server, login.Token)
	owned := newProjectAPIKeyRequest("admin-owned", api.ProjectGrant{Project: "payments", Role: api.UserRoleViewer})
	owned.AttributionClass = api.CreateAPIKeyRequestAttributionClassUserOwned
	owned.OwnerUserId = &login.User.Id
	created := createAPIKey(t, server, login.Token, owned)
	path := "/api/v1/api-keys/" + created.ApiKey.Id

	assert.Empty(t, listAPIKeyNames(t, server, manager.Key))
	server.Client().Get(path).WithBearerToken(manager.Key).ExpectStatus(http.StatusNotFound).Send(t)
	raised := api.ProjectGrants{{Project: "payments", Role: api.UserRoleManager}}
	server.Client().Patch(path, api.UpdateAPIKeyRequest{ProjectGrants: &raised}).
		WithBearerToken(manager.Key).
		ExpectStatus(http.StatusNotFound).
		Send(t)
	server.Client().Delete(path).WithBearerToken(manager.Key).ExpectStatus(http.StatusNotFound).Send(t)
}

// A key without the manager role in any project still needs an admin.
func TestAPIKeys_ProjectRolesBelowManagerNeedAdmin(t *testing.T) {
	t.Parallel()
	server := setupProjectsAuthServer(t, "payments")
	adminToken := getAdminToken(t, server)

	developer := createAPIKey(t, server, adminToken,
		newProjectAPIKeyRequest("payments-developer", api.ProjectGrant{Project: "payments", Role: api.UserRoleDeveloper}))

	server.Client().Get("/api/v1/api-keys").WithBearerToken(developer.Key).ExpectStatus(http.StatusForbidden).Send(t)
	server.Client().Post("/api/v1/api-keys",
		newProjectAPIKeyRequest("viewer", api.ProjectGrant{Project: "payments", Role: api.UserRoleViewer})).
		WithBearerToken(developer.Key).
		ExpectStatus(http.StatusForbidden).
		Send(t)
}

// While projects are off, the API neither reads nor returns project grants.
func TestAPIKeys_ProjectGrantsIgnoredWhileProjectsOff(t *testing.T) {
	t.Parallel()
	server := setupBuiltinAuthServer(t)
	adminToken := getAdminToken(t, server)

	request := newCreateAPIKeyRequest("plain", api.UserRoleOperator)
	request.ProjectGrants = &api.ProjectGrants{{Project: "payments", Role: api.UserRoleManager}}
	created := createAPIKey(t, server, adminToken, request)
	assert.Nil(t, created.ApiKey.ProjectGrants)

	resp := server.Client().Get("/api/v1/api-keys/" + created.ApiKey.Id).
		WithBearerToken(adminToken).
		ExpectStatus(http.StatusOK).
		Send(t)
	var got api.APIKeyResponse
	resp.Unmarshal(t, &got)
	assert.Nil(t, got.ApiKey.ProjectGrants)
	assert.True(t, got.ApiKey.WorkspaceAccess.All)
}
