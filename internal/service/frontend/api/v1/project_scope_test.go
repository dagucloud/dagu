// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const projectTestDAG = "steps:\n  - id: hello\n    run: echo hello\n"

// setupProjectDAGsServer starts a server with projects on and the projects
// payments and infra. The default project has cleanup, payments has etl,
// and infra has etl and report.
func setupProjectDAGsServer(t *testing.T) (test.Server, string) {
	t.Helper()
	server := setupProjectsAuthServer(t, "payments", "infra")
	writeProjectDAG(t, server, "default", "cleanup")
	writeProjectDAG(t, server, "payments", "etl")
	writeProjectDAG(t, server, "infra", "etl")
	writeProjectDAG(t, server, "infra", "report")
	token := getAdminToken(t, server)
	// The server opens the stores of a project created after it started
	// when its registry watch next looks.
	require.Eventually(t, func() bool {
		return projectGet(t, server, token, "/api/v1/dags?project=infra").Response.StatusCode() == http.StatusOK
	}, 15*time.Second, 100*time.Millisecond)
	return server, token
}

func writeProjectDAG(t *testing.T, server test.Server, projectName, dagName string) {
	t.Helper()
	paths, err := server.Config.ProjectPaths(projectName)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(paths.DAGsDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(paths.DAGsDir, dagName+".yaml"), []byte(projectTestDAG), 0o600))
}

func projectGet(t *testing.T, server test.Server, token, path string) *test.Response {
	t.Helper()
	return server.Client().Get(path).WithBearerToken(token).Send(t)
}

func listedDAGNames(t *testing.T, server test.Server, token, path string) []string {
	t.Helper()
	resp := server.Client().Get(path).WithBearerToken(token).ExpectStatus(http.StatusOK).Send(t)
	var list api.ListDAGs200JSONResponse
	resp.Unmarshal(t, &list)
	names := make([]string, 0, len(list.Dags))
	for _, dag := range list.Dags {
		names = append(names, dag.FileName)
	}
	return names
}

func errorMessage(t *testing.T, resp *test.Response) string {
	t.Helper()
	var body api.Error
	resp.Unmarshal(t, &body)
	return body.Message
}

// A DAG path serves the DAGs of the project that project names, matched
// without regard to case, and of the default project without it (Spec 102
// "Choosing a Project", API).
func TestProjectParam_SelectsProjectDAGs(t *testing.T) {
	t.Parallel()
	server, token := setupProjectDAGsServer(t)

	assert.ElementsMatch(t, []string{"cleanup"}, listedDAGNames(t, server, token, "/api/v1/dags"))
	assert.ElementsMatch(t, []string{"etl", "report"}, listedDAGNames(t, server, token, "/api/v1/dags?project=infra"))
	assert.ElementsMatch(t, []string{"etl"}, listedDAGNames(t, server, token, "/api/v1/dags?project=Payments"))
	server.Client().Get("/api/v1/dags/report?project=INFRA").WithBearerToken(token).
		ExpectStatus(http.StatusOK).Send(t)

	resp := projectGet(t, server, token, "/api/v1/dags?project=missing")
	require.Equal(t, http.StatusNotFound, resp.Response.StatusCode())
	assert.Contains(t, errorMessage(t, resp), `project "missing" not found`)
}

// A request for a DAG the project lacks gets 404, and the error names the
// projects the caller can see that have a DAG of that name.
func TestProjectParam_MissingDAGNamesProjects(t *testing.T) {
	t.Parallel()
	server, adminToken := setupProjectDAGsServer(t)

	resp := projectGet(t, server, adminToken, "/api/v1/dags/etl")
	require.Equal(t, http.StatusNotFound, resp.Response.StatusCode())
	assert.Equal(t, "DAG etl not found in project default; projects with a DAG of that name: infra, payments",
		errorMessage(t, resp))

	// A project the caller cannot see is not named.
	paymentsViewer := createAPIKey(t, server, adminToken, newProjectAPIKeyRequest("payments-viewer",
		api.ProjectGrant{Project: "payments", Role: api.UserRoleViewer}))
	resp = projectGet(t, server, paymentsViewer.Key, "/api/v1/dags/report?project=payments")
	require.Equal(t, http.StatusNotFound, resp.Response.StatusCode())
	assert.Equal(t, "DAG report not found in project payments", errorMessage(t, resp))
}

// A caller with no role in a project gets 404 for its DAGs, the default
// project's included, as for a missing project, and needs a role that may
// run DAGs to start one.
func TestProjectParam_HidesProjectsWithoutRole(t *testing.T) {
	t.Parallel()
	server, adminToken := setupProjectDAGsServer(t)
	viewer := createAPIKey(t, server, adminToken, newProjectAPIKeyRequest("payments-viewer",
		api.ProjectGrant{Project: "payments", Role: api.UserRoleViewer}))

	assert.ElementsMatch(t, []string{"etl"}, listedDAGNames(t, server, viewer.Key, "/api/v1/dags?project=payments"))
	for _, path := range []string{
		"/api/v1/dags?project=infra",
		"/api/v1/dags/etl?project=infra",
		"/api/v1/dags",
		"/api/v1/dag-runs?project=infra",
	} {
		resp := projectGet(t, server, viewer.Key, path)
		assert.Equal(t, http.StatusNotFound, resp.Response.StatusCode(), path)
	}
	server.Client().Post("/api/v1/dags/etl/start?project=payments", api.ExecuteDAGJSONRequestBody{}).
		WithBearerToken(viewer.Key).ExpectStatus(http.StatusForbidden).Send(t)
}

// A run started in a project runs there and is read only through that
// project. Its project's queues are not read, so it cannot be queued.
func TestProjectParam_RunsInProject(t *testing.T) {
	t.Parallel()
	server, token := setupProjectDAGsServer(t)

	resp := server.Client().Post("/api/v1/dags/etl/start?project=payments", api.ExecuteDAGJSONRequestBody{}).
		WithBearerToken(token).ExpectStatus(http.StatusOK).Send(t)
	var started api.ExecuteDAG200JSONResponse
	resp.Unmarshal(t, &started)
	runPath := "/api/v1/dag-runs/etl/" + started.DagRunId

	require.Eventually(t, func() bool {
		resp := projectGet(t, server, token, runPath+"?project=payments")
		if resp.Response.StatusCode() != http.StatusOK {
			return false
		}
		var details api.GetDAGRunDetails200JSONResponse
		resp.Unmarshal(t, &details)
		return details.DagRunDetails.Status == api.Status(ir.Succeeded)
	}, dagRunEventuallyTimeout(15*time.Second), 200*time.Millisecond)
	for _, other := range []string{runPath, runPath + "?project=infra"} {
		assert.Equal(t, http.StatusNotFound, projectGet(t, server, token, other).Response.StatusCode(), other)
	}

	resp = server.Client().Post("/api/v1/dags/etl/enqueue?project=payments", api.EnqueueDAGDAGRunJSONRequestBody{}).
		WithBearerToken(token).ExpectStatus(http.StatusBadRequest).Send(t)
	assert.Contains(t, errorMessage(t, resp), "cannot queue runs outside the default project")
}

// A path that takes no project refuses one, rather than answer for
// another project.
func TestProjectParam_RefusedWhereNotTaken(t *testing.T) {
	t.Parallel()
	server, token := setupProjectDAGsServer(t)

	resp := projectGet(t, server, token, "/api/v1/dags/etl/settings?project=payments")
	require.Equal(t, http.StatusBadRequest, resp.Response.StatusCode())
	assert.Contains(t, errorMessage(t, resp), "does not take the project parameter")
}

// While projects are off, project selects nothing, as on v2.
func TestProjectParam_IgnoredWhileProjectsOff(t *testing.T) {
	t.Parallel()
	server := setupBuiltinAuthServer(t, func(cfg *config.Config) {
		require.NoError(t, os.MkdirAll(cfg.Paths.DAGsDir, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(cfg.Paths.DAGsDir, "cleanup.yaml"), []byte(projectTestDAG), 0o600))
	})
	token := getAdminToken(t, server)

	assert.ElementsMatch(t, []string{"cleanup"}, listedDAGNames(t, server, token, "/api/v1/dags?project=missing"))
}
