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
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/project"
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
	return setupProjectDAGsServerWith(t, func(*config.Config) {})
}

// setupProjectDAGsServerWith is setupProjectDAGsServer with mutate also
// applied to the configuration.
func setupProjectDAGsServerWith(t *testing.T, mutate func(*config.Config)) (test.Server, string) {
	t.Helper()
	server := setupProjectsAuthServerWith(t, mutate, "payments", "infra")
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
	assert.Equal(t, queuedRunRefusal("payments"), errorMessage(t, resp))
}

// queuedRunRefusal is the error a request gets for queueing a run in the
// project called name.
func queuedRunRefusal(name string) string {
	return project.RefuseOtherProject(name, project.ErrQueuedRun).Error()
}

// writeProjectSpec writes a DAG file of the given spec to the project's DAG
// directory.
func writeProjectSpec(t *testing.T, server test.Server, projectName, dagName, spec string) {
	t.Helper()
	paths, err := server.Config.ProjectPaths(projectName)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(paths.DAGsDir, dagName+".yaml"), []byte(spec), 0o600))
}

// startProjectRun starts the DAG in the project and waits until the run
// satisfies done.
func startProjectRun(t *testing.T, server test.Server, token, projectName, dagName string, done func(api.DAGRunDetails) bool) api.DAGRunDetails {
	t.Helper()
	resp := server.Client().Post("/api/v1/dags/"+dagName+"/start?project="+projectName, api.ExecuteDAGJSONRequestBody{}).
		WithBearerToken(token).ExpectStatus(http.StatusOK).Send(t)
	var started api.ExecuteDAG200JSONResponse
	resp.Unmarshal(t, &started)
	return waitProjectRun(t, server, token, projectName, dagName, started.DagRunId, done)
}

func waitProjectRun(t *testing.T, server test.Server, token, projectName, dagName, runID string, done func(api.DAGRunDetails) bool) api.DAGRunDetails {
	t.Helper()
	var details api.GetDAGRunDetails200JSONResponse
	require.Eventually(t, func() bool {
		resp := projectGet(t, server, token, "/api/v1/dag-runs/"+dagName+"/"+runID+"?project="+projectName)
		if resp.Response.StatusCode() != http.StatusOK {
			return false
		}
		resp.Unmarshal(t, &details)
		return done(details.DagRunDetails)
	}, dagRunEventuallyTimeout(15*time.Second), 200*time.Millisecond)
	return details.DagRunDetails
}

func waitingRun(details api.DAGRunDetails) bool {
	return details.Status == api.Status(ir.Waiting)
}

// Deleting or renaming a project's DAG leaves alone the webhook and the pin
// of the default project's DAG of the same name, and does not tell whether
// the default project has a webhook under the new name.
func TestProjectParam_DAGChangesStayInProject(t *testing.T) {
	t.Parallel()
	server, token := setupProjectDAGsServer(t)
	writeProjectDAG(t, server, "default", "etl")
	for _, name := range []string{"etl", "cleanup"} {
		server.Client().Post("/api/v1/dags/"+name+"/webhook", nil).WithBearerToken(token).
			ExpectStatus(http.StatusCreated).Send(t)
	}
	server.Client().Put("/api/v1/dags/etl/pin", nil).WithBearerToken(token).
		ExpectStatus(http.StatusNoContent).Send(t)

	assert.Empty(t, pinnedDAGNames(t, server, token, "/api/v1/dags?project=payments"))

	server.Client().Post("/api/v1/dags/etl/rename?project=payments", api.RenameDAGJSONRequestBody{NewFileName: "cleanup"}).
		WithBearerToken(token).ExpectStatus(http.StatusOK).Send(t)
	server.Client().Delete("/api/v1/dags/cleanup?project=payments").WithBearerToken(token).
		ExpectStatus(http.StatusNoContent).Send(t)

	for _, name := range []string{"etl", "cleanup"} {
		server.Client().Get("/api/v1/dags/" + name + "/webhook").WithBearerToken(token).
			ExpectStatus(http.StatusOK).Send(t)
	}
	assert.Equal(t, []string{"etl"}, pinnedDAGNames(t, server, token, "/api/v1/dags"))
}

func pinnedDAGNames(t *testing.T, server test.Server, token, path string) []string {
	t.Helper()
	resp := server.Client().Get(path).WithBearerToken(token).ExpectStatus(http.StatusOK).Send(t)
	var list api.ListDAGs200JSONResponse
	resp.Unmarshal(t, &list)
	var pinned []string
	for _, dag := range list.Dags {
		if dag.Pinned {
			pinned = append(pinned, dag.FileName)
		}
	}
	return pinned
}

// A run in a project takes its runtime profile from the project: a profile
// of the default project alone is not found, and a protected profile of the
// project needs an admin although the default project has an unprotected
// profile of the same name.
func TestProjectParam_ProfilesFromProject(t *testing.T) {
	t.Parallel()
	server, adminToken := setupProjectDAGsServer(t)
	createProfile(t, server, "default", profile.CreateInput{Name: "only-in-default"})
	createProfile(t, server, "default", profile.CreateInput{Name: "prod"})
	createProfile(t, server, "payments", profile.CreateInput{Name: "prod", Protected: true})
	operator := createAPIKey(t, server, adminToken, newProjectAPIKeyRequest("payments-operator",
		api.ProjectGrant{Project: "payments", Role: api.UserRoleOperator}))

	start := func(token, profileName string) *test.Response {
		return server.Client().Post("/api/v1/dags/etl/start?project=payments", api.ExecuteDAGJSONRequestBody{
			Profile: &profileName,
		}).WithBearerToken(token).Send(t)
	}
	assert.Equal(t, http.StatusNotFound, start(adminToken, "only-in-default").Response.StatusCode())
	assert.Equal(t, http.StatusForbidden, start(operator.Key, "prod").Response.StatusCode())
}

func createProfile(t *testing.T, server test.Server, projectName string, in profile.CreateInput) {
	t.Helper()
	settings, err := file.NewProjectSettings(server.Config, file.NewServerStores(server.Config), projectName)
	require.NoError(t, err)
	created, err := profile.New(in, time.Now())
	require.NoError(t, err)
	require.NoError(t, settings.ProfileStore(server.Context).Create(server.Context, created))
}

// A human task or an approval in a project other than the default one
// would resume its run through a queue that no scheduler reads, so it is
// refused before anything is saved.
func TestProjectParam_RefusesQueuedResume(t *testing.T) {
	t.Parallel()
	server, token := setupProjectDAGsServerWith(t, func(cfg *config.Config) {
		cfg.Queues.Enabled = true
		cfg.Queues.Config = []config.QueueConfig{{Name: "gated", MaxActiveRuns: 1}}
	})
	writeProjectSpec(t, server, "payments", "review", `steps:
  - id: review
    action: human.task
    with:
      prompt: "Choose"
      form:
        type: object
        properties:
          count:
            type: integer
  - id: after
    depends: review
    run: echo done
`)
	writeProjectSpec(t, server, "payments", "gated", `steps:
  - name: gate
    run: echo gate
    approval:
      prompt: Approve
  - name: after
    depends: gate
    run: echo done
`)

	review := startProjectRun(t, server, token, "payments", "review", waitingRun)
	resp := server.Client().Post("/api/v1/dag-runs/review/"+review.DagRunId+"/human-tasks/review/complete?project=payments",
		map[string]any{"count": 3}).WithBearerToken(token).ExpectStatus(http.StatusBadRequest).Send(t)
	assert.Equal(t, queuedRunRefusal("payments"), errorMessage(t, resp))
	assert.True(t, waitingRun(waitProjectRun(t, server, token, "payments", "review", review.DagRunId, waitingRun)))

	gated := startProjectRun(t, server, token, "payments", "gated", waitingRun)
	resp = server.Client().Post("/api/v1/dag-runs/gated/"+gated.DagRunId+"/steps/gate/approve?project=payments",
		api.ApproveStepRequest{}).WithBearerToken(token).ExpectStatus(http.StatusBadRequest).Send(t)
	assert.Equal(t, queuedRunRefusal("payments"), errorMessage(t, resp))
	after := waitProjectRun(t, server, token, "payments", "gated", gated.DagRunId, waitingRun)
	assert.Nil(t, after.Nodes[0].ApprovedAt, "the approval is saved")
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
