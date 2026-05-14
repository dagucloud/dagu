// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	"github.com/dagucloud/dagu/internal/service/scheduler"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getJSONWhenAvailable(t *testing.T, server test.Server, url string, out any) bool {
	t.Helper()

	resp := server.Client().Get(url).Send(t)
	if resp.Response.StatusCode() == http.StatusNotFound {
		return false
	}

	require.Equal(t, http.StatusOK, resp.Response.StatusCode(), "unexpected status code")
	resp.Unmarshal(t, out)
	return true
}

func sendRawRequestStatus(
	t *testing.T,
	server test.Server,
	method string,
	requestPath string,
	body []byte,
) int {
	t.Helper()

	baseURL := fmt.Sprintf(
		"http://%s:%d",
		server.Config.Server.Host,
		server.Config.Server.Port,
	)
	var bodyReader *bytes.Reader
	if body == nil {
		bodyReader = bytes.NewReader(nil)
	} else {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, baseURL+requestPath, bodyReader)
	require.NoError(t, err)
	req.URL.RawPath = requestPath
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp.StatusCode
}

func apiStatusOutputValue(t *testing.T, status *exec.DAGRunStatus, key string) string {
	t.Helper()

	require.NotNil(t, status)
	for _, node := range status.Nodes {
		if node.OutputVariables == nil {
			continue
		}
		value, ok := node.OutputVariables.Load(key)
		if ok {
			result, ok := value.(string)
			require.True(t, ok, "output %q has unexpected type %T", key, value)
			result = strings.TrimPrefix(result, key+"=")
			return result
		}
	}

	t.Fatalf("output %q not found in DAG-run status", key)
	return ""
}

func TestDAGRunHistoryReturnsNotFoundForMissingDAG(t *testing.T) {
	server := test.SetupServer(t)

	server.Client().Get("/api/v1/dags/missing-dag/dag-runs").
		ExpectStatus(http.StatusNotFound).Send(t)
}

func TestDAGWritesDisabledInReadOnlyMode(t *testing.T) {
	// Setup server with gitSync.enabled=true, pushEnabled=false (read-only mode)
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.GitSync.Enabled = true
		cfg.GitSync.PushEnabled = false
	}))

	t.Run("CreateNewDAG", func(t *testing.T) {
		spec := "steps:\n  - command: echo test"
		server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
			Name: "test_dag",
			Spec: &spec,
		}).ExpectStatus(http.StatusForbidden).Send(t)
	})

	t.Run("DeleteDAG", func(t *testing.T) {
		server.Client().Delete("/api/v1/dags/any_dag").
			ExpectStatus(http.StatusForbidden).Send(t)
	})

	t.Run("UpdateDAGSpec", func(t *testing.T) {
		server.Client().Put("/api/v1/dags/any_dag/spec", api.UpdateDAGSpecJSONRequestBody{
			Spec: "steps:\n  - command: echo updated",
		}).ExpectStatus(http.StatusForbidden).Send(t)
	})

	t.Run("RenameDAG", func(t *testing.T) {
		server.Client().Post("/api/v1/dags/any_dag/rename", api.RenameDAGJSONRequestBody{
			NewFileName: "new_name",
		}).ExpectStatus(http.StatusForbidden).Send(t)
	})
}

func TestDAGWritesAllowedWhenPushEnabled(t *testing.T) {
	// Setup server with gitSync.enabled=true, pushEnabled=true
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.GitSync.Enabled = true
		cfg.GitSync.PushEnabled = true
	}))

	// Test CreateNewDAG is allowed
	spec := "steps:\n  - command: echo test"
	server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: "test_dag_push_enabled",
		Spec: &spec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	// Cleanup
	server.Client().Delete("/api/v1/dags/test_dag_push_enabled").ExpectStatus(http.StatusNoContent).Send(t)
}

func TestDAGWritesAllowedWhenGitSyncDisabled(t *testing.T) {
	// Setup server with gitSync.enabled=false (default)
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.GitSync.Enabled = false
	}))

	// Test CreateNewDAG is allowed
	spec := "steps:\n  - command: echo test"
	server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: "test_dag_gitsync_disabled",
		Spec: &spec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	// Cleanup
	server.Client().Delete("/api/v1/dags/test_dag_gitsync_disabled").ExpectStatus(http.StatusNoContent).Send(t)
}

func TestDAGSpecInheritsBaseGraphType(t *testing.T) {
	server := test.SetupServer(t)

	require.NoError(t, os.WriteFile(server.Config.Paths.BaseConfig, []byte("type: graph\n"), 0600))

	spec := `
steps:
  - name: build
    run: echo build
  - name: test
    run: echo test
    depends: [build]
`
	dagName := "inherits_base_graph_type"

	server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: dagName,
		Spec: &spec,
	}).ExpectStatus(http.StatusCreated).Send(t)
	t.Cleanup(func() {
		server.Client().Delete("/api/v1/dags/" + dagName).Send(t)
	})

	t.Run("ValidateDAGSpec", func(t *testing.T) {
		resp := server.Client().Post("/api/v1/dags/validate", api.ValidateDAGSpecJSONRequestBody{
			Name: &dagName,
			Spec: spec,
		}).ExpectStatus(http.StatusOK).Send(t)

		var body api.ValidateDAGSpec200JSONResponse
		resp.Unmarshal(t, &body)
		require.True(t, body.Valid)
		require.Empty(t, body.Errors)
	})

	t.Run("GetDAGSpec", func(t *testing.T) {
		resp := server.Client().Get("/api/v1/dags/" + dagName + "/spec").
			ExpectStatus(http.StatusOK).
			Send(t)

		var body api.GetDAGSpec200JSONResponse
		resp.Unmarshal(t, &body)
		require.Empty(t, body.Errors)
		require.NotNil(t, body.Dag)
		require.NotNil(t, body.Dag.Steps)
		require.Len(t, *body.Dag.Steps, 2)
		require.NotNil(t, (*body.Dag.Steps)[1].Depends)
		require.Equal(t, []string{"build"}, *(*body.Dag.Steps)[1].Depends)
	})

	t.Run("UpdateDAGSpec", func(t *testing.T) {
		resp := server.Client().Put("/api/v1/dags/"+dagName+"/spec", api.UpdateDAGSpecJSONRequestBody{
			Spec: spec,
		}).ExpectStatus(http.StatusOK).Send(t)

		var body api.UpdateDAGSpec200JSONResponse
		resp.Unmarshal(t, &body)
		require.Empty(t, body.Errors)
	})
}

func TestCreateNewDAGPathTraversal(t *testing.T) {
	server := test.SetupServer(t)

	traversalNames := []string{
		"../../tmp/traversal",
		"../escape",
		"foo/bar",
		"../../../etc/malicious",
	}

	for _, name := range traversalNames {
		t.Run("with_spec/"+name, func(t *testing.T) {
			spec := "steps:\n  - command: echo test"
			server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
				Name: name,
				Spec: &spec,
			}).ExpectStatus(http.StatusBadRequest).Send(t)
		})

		t.Run("without_spec/"+name, func(t *testing.T) {
			server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
				Name: name,
			}).ExpectStatus(http.StatusBadRequest).Send(t)
		})
	}

	t.Run("empty_name", func(t *testing.T) {
		server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
			Name: "",
		}).ExpectStatus(http.StatusBadRequest).Send(t)
	})

	t.Run("dot_dot_name", func(t *testing.T) {
		server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
			Name: "..",
		}).ExpectStatus(http.StatusBadRequest).Send(t)
	})
}

func TestDAGFileNameRejectsEncodedTraversal(t *testing.T) {
	server := test.SetupServer(t)

	tests := []struct {
		name       string
		method     string
		path       string
		body       []byte
		wantStatus int
	}{
		{
			name:       "get spec",
			method:     http.MethodGet,
			path:       "/api/v1/dags/..%2F..%2Ftmp%2Fsecret/spec",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "delete dag",
			method:     http.MethodDelete,
			path:       "/api/v1/dags/..%2F..%2Ftmp%2Fsecret",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "start dag",
			method:     http.MethodPost,
			path:       "/api/v1/dags/..%2F..%2Ftmp%2Fsecret/start",
			body:       []byte(`{}`),
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status := sendRawRequestStatus(t, server, tc.method, tc.path, tc.body)
			require.Equal(t, tc.wantStatus, status)
		})
	}
}

func TestDAG(t *testing.T) {
	server := test.SetupServer(t)

	t.Run("CreateExecuteDelete", func(t *testing.T) {
		spec := fmt.Sprintf(`
steps:
  - %s
`, test.ShellQuote("exit 0"))
		// Create a new DAG
		_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
			Name: "test_dag",
			Spec: &spec,
		}).ExpectStatus(http.StatusCreated).Send(t)

		// Fetch the created DAG with the list endpoint
		resp := server.Client().Get("/api/v1/dags?name=test_dag").ExpectStatus(http.StatusOK).Send(t)
		var apiResp api.ListDAGs200JSONResponse
		resp.Unmarshal(t, &apiResp)

		require.Len(t, apiResp.Dags, 1, "expected one DAG")

		// Execute the created DAG
		resp = server.Client().Post("/api/v1/dags/test_dag/start", api.ExecuteDAGJSONRequestBody{}).
			ExpectStatus(http.StatusOK).Send(t)

		var execResp api.ExecuteDAG200JSONResponse
		resp.Unmarshal(t, &execResp)

		require.NotEmpty(t, execResp.DagRunId, "expected a non-empty dag-run ID")

		// Check the status of the dag-run
		require.Eventually(t, func() bool {
			url := fmt.Sprintf("/api/v1/dags/test_dag/dag-runs/%s", execResp.DagRunId)
			var dagRunStatus api.GetDAGDAGRunDetails200JSONResponse
			if !getJSONWhenAvailable(t, server, url, &dagRunStatus) {
				return false
			}

			return dagRunStatus.DagRun.Status == api.Status(core.Succeeded)
		}, dagRunEventuallyTimeout(5*time.Second), time.Second, "expected DAG to complete")

		// Delete the created DAG
		_ = server.Client().Delete("/api/v1/dags/test_dag").ExpectStatus(http.StatusNoContent).Send(t)
	})

	t.Run("ListDAGsSorting", func(t *testing.T) {
		// Test that ListDAGs respects sort parameters
		resp := server.Client().Get("/api/v1/dags?sort=name&order=asc").ExpectStatus(http.StatusOK).Send(t)
		var apiResp api.ListDAGs200JSONResponse
		resp.Unmarshal(t, &apiResp)

		// The test should pass regardless of the sort result
		// since we're only testing that the endpoint accepts the parameters
		require.NotNil(t, apiResp.Dags)
		require.NotNil(t, apiResp.Pagination)
	})

	t.Run("ExecuteDAGWithSingleton", func(t *testing.T) {
		// Create a new DAG
		_ = server.Client().Post("/api/v1/dags", api.CreateNewDAG201JSONResponse{
			Name: "test_singleton_dag",
		}).ExpectStatus(http.StatusCreated).Send(t)

		// Execute the DAG with singleton flag
		singleton := true
		resp := server.Client().Post("/api/v1/dags/test_singleton_dag/start", api.ExecuteDAGJSONRequestBody{
			Singleton: &singleton,
		}).ExpectStatus(http.StatusOK).Send(t)

		var execResp api.ExecuteDAG200JSONResponse
		resp.Unmarshal(t, &execResp)
		require.NotEmpty(t, execResp.DagRunId, "expected a non-empty dag-run ID")

		// Clean up
		_ = server.Client().Delete("/api/v1/dags/test_singleton_dag").ExpectStatus(http.StatusNoContent).Send(t)
	})

	t.Run("ExecuteDAGWithJSONParams", func(t *testing.T) {
		// Case 1: DAG with named params defined - JSON keys map to those params.
		// Verifies that JSON parameters are parsed as named key-value pairs,
		// not tokenized by whitespace (regression test for JSON params bug).
		spec := `
params:
  - key1: default1
  - key2: default2
steps:
  - name: echo_params
    run: echo "key1=${key1} key2=${key2}"
`
		dagName := "test_json_params"

		_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
			Name: dagName,
			Spec: &spec,
		}).ExpectStatus(http.StatusCreated).Send(t)

		jsonParams := `{"key1": "test1", "key2": "test2"}`
		resp := server.Client().Post("/api/v1/dags/"+dagName+"/start", api.ExecuteDAGJSONRequestBody{
			Params: &jsonParams,
		}).ExpectStatus(http.StatusOK).Send(t)

		var execResp api.ExecuteDAG200JSONResponse
		resp.Unmarshal(t, &execResp)
		require.NotEmpty(t, execResp.DagRunId)

		var dagRunDetails api.GetDAGDAGRunDetails200JSONResponse
		require.Eventually(t, func() bool {
			url := fmt.Sprintf("/api/v1/dags/%s/dag-runs/%s", dagName, execResp.DagRunId)
			if !getJSONWhenAvailable(t, server, url, &dagRunDetails) {
				return false
			}
			return dagRunDetails.DagRun.Status == api.Status(core.Succeeded)
		}, dagRunEventuallyTimeout(10*time.Second), 500*time.Millisecond, "DAG should complete")

		require.NotNil(t, dagRunDetails.DagRun.Params)
		params := *dagRunDetails.DagRun.Params
		require.Contains(t, params, "key1=test1")
		require.Contains(t, params, "key2=test2")
		require.NotContains(t, params, "1={", "JSON should not be tokenized")

		_ = server.Client().Delete("/api/v1/dags/" + dagName).ExpectStatus(http.StatusNoContent).Send(t)
	})

	t.Run("ExecuteDAGWithJSONPositionalArg", func(t *testing.T) {
		// Case 2: No named params defined - JSON passed as positional arg.
		// The entire JSON is stored as $1 and accessible via JSON path syntax ${1.key}.
		spec := `
steps:
  - name: show_json
    run: echo "key1=${1.key1} key2=${1.key2}"
`
		dagName := "test_json_positional"

		_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
			Name: dagName,
			Spec: &spec,
		}).ExpectStatus(http.StatusCreated).Send(t)

		// Pass JSON as a single positional argument using array syntax
		jsonParams := `["{\"key1\": \"val1\", \"key2\": \"val2\"}"]`
		resp := server.Client().Post("/api/v1/dags/"+dagName+"/start", api.ExecuteDAGJSONRequestBody{
			Params: &jsonParams,
		}).ExpectStatus(http.StatusOK).Send(t)

		var execResp api.ExecuteDAG200JSONResponse
		resp.Unmarshal(t, &execResp)
		require.NotEmpty(t, execResp.DagRunId)

		var dagRunDetails api.GetDAGDAGRunDetails200JSONResponse
		require.Eventually(t, func() bool {
			url := fmt.Sprintf("/api/v1/dags/%s/dag-runs/%s", dagName, execResp.DagRunId)
			if !getJSONWhenAvailable(t, server, url, &dagRunDetails) {
				return false
			}
			return dagRunDetails.DagRun.Status == api.Status(core.Succeeded)
		}, dagRunEventuallyTimeout(10*time.Second), 500*time.Millisecond, "DAG should complete")

		require.NotNil(t, dagRunDetails.DagRun.Params)
		params := *dagRunDetails.DagRun.Params
		// Positional arg $1 should contain the full JSON string
		require.Contains(t, params, `1={"key1": "val1", "key2": "val2"}`)

		_ = server.Client().Delete("/api/v1/dags/" + dagName).ExpectStatus(http.StatusNoContent).Send(t)
	})

	t.Run("ExecuteDAGAllowsFewerPositionalParamsThanDeclared", func(t *testing.T) {
		spec := `
params: "p1 p2"
steps:
  - name: echo
    run: echo "${1} ${2}"
`
		dagName := "test_positional_fewer_allowed"

		_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
			Name: dagName,
			Spec: &spec,
		}).ExpectStatus(http.StatusCreated).Send(t)

		params := "one"
		resp := server.Client().Post("/api/v1/dags/"+dagName+"/start", api.ExecuteDAGJSONRequestBody{
			Params: &params,
		}).ExpectStatus(http.StatusOK).Send(t)

		var execResp api.ExecuteDAG200JSONResponse
		resp.Unmarshal(t, &execResp)
		require.NotEmpty(t, execResp.DagRunId)
		require.Eventually(t, func() bool {
			url := fmt.Sprintf("/api/v1/dags/%s/dag-runs/%s", dagName, execResp.DagRunId)
			var dagRunStatus api.GetDAGDAGRunDetails200JSONResponse
			if !getJSONWhenAvailable(t, server, url, &dagRunStatus) {
				return false
			}
			return dagRunStatus.DagRun.Status == api.Status(core.Succeeded)
		}, dagRunEventuallyTimeout(5*time.Second), 500*time.Millisecond)

		_ = server.Client().Delete("/api/v1/dags/" + dagName).ExpectStatus(http.StatusNoContent).Send(t)
	})

	t.Run("ExecuteDAGRejectsTooManyPositionalParams", func(t *testing.T) {
		spec := `
params: "p1 p2"
steps:
  - name: echo
    run: echo "${1} ${2}"
`
		dagName := "test_positional_too_many_rejected"

		_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
			Name: dagName,
			Spec: &spec,
		}).ExpectStatus(http.StatusCreated).Send(t)

		params := "one two three"
		resp := server.Client().Post("/api/v1/dags/"+dagName+"/start", api.ExecuteDAGJSONRequestBody{
			Params: &params,
		}).ExpectStatus(http.StatusBadRequest).Send(t)

		var errResp api.Error
		resp.Unmarshal(t, &errResp)
		require.Equal(t, api.ErrorCodeBadRequest, errResp.Code)
		require.Contains(t, errResp.Message, "too many positional params: expected at most 2, got 3")

		_ = server.Client().Delete("/api/v1/dags/" + dagName).ExpectStatus(http.StatusNoContent).Send(t)
	})

	t.Run("ExecuteDAGWithLabels", func(t *testing.T) {
		spec := `
steps:
  - name: echo_labels
    run: echo "labeled"
`
		dagName := "test_labels_dag"

		_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
			Name: dagName,
			Spec: &spec,
		}).ExpectStatus(http.StatusCreated).Send(t)

		labels := []string{"env=prod", "team=backend"}
		resp := server.Client().Post("/api/v1/dags/"+dagName+"/start", api.ExecuteDAGJSONRequestBody{
			Labels: &labels,
		}).ExpectStatus(http.StatusOK).Send(t)

		var execResp api.ExecuteDAG200JSONResponse
		resp.Unmarshal(t, &execResp)
		require.NotEmpty(t, execResp.DagRunId)

		var details api.GetDAGRunDetails200JSONResponse
		require.Eventually(t, func() bool {
			if !getJSONWhenAvailable(t, server, fmt.Sprintf("/api/v1/dag-runs/%s/%s", dagName, execResp.DagRunId), &details) {
				return false
			}
			return details.DagRunDetails.Labels != nil
		}, 5*time.Second, 250*time.Millisecond)
		assert.ElementsMatch(t, labels, *details.DagRunDetails.Labels)

		_ = server.Client().Delete("/api/v1/dags/" + dagName).ExpectStatus(http.StatusNoContent).Send(t)
	})

	t.Run("ExecuteDAGWithInvalidLabels", func(t *testing.T) {
		spec := `
steps:
  - name: echo
    run: echo "test"
`
		dagName := "test_invalid_labels_dag"

		_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
			Name: dagName,
			Spec: &spec,
		}).ExpectStatus(http.StatusCreated).Send(t)

		labels := []string{"!!!invalid"}
		resp := server.Client().Post("/api/v1/dags/"+dagName+"/start", api.ExecuteDAGJSONRequestBody{
			Labels: &labels,
		}).ExpectStatus(http.StatusBadRequest).Send(t)

		var errResp api.Error
		resp.Unmarshal(t, &errResp)
		require.Equal(t, api.ErrorCodeBadRequest, errResp.Code)

		_ = server.Client().Delete("/api/v1/dags/" + dagName).ExpectStatus(http.StatusNoContent).Send(t)
	})

	t.Run("EnqueueDAGWithLabels", func(t *testing.T) {
		spec := `
steps:
  - name: echo_labels
    run: echo "enqueued"
`
		dagName := "test_enqueue_labels_dag"

		_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
			Name: dagName,
			Spec: &spec,
		}).ExpectStatus(http.StatusCreated).Send(t)

		labels := []string{"env=staging", "priority=low"}
		resp := server.Client().Post("/api/v1/dags/"+dagName+"/enqueue", api.EnqueueDAGDAGRunJSONRequestBody{
			Labels: &labels,
		}).ExpectStatus(http.StatusOK).Send(t)

		var enqResp api.EnqueueDAGDAGRun200JSONResponse
		resp.Unmarshal(t, &enqResp)
		require.NotEmpty(t, enqResp.DagRunId)

		var details api.GetDAGRunDetails200JSONResponse
		require.Eventually(t, func() bool {
			if !getJSONWhenAvailable(t, server, fmt.Sprintf("/api/v1/dag-runs/%s/%s", dagName, enqResp.DagRunId), &details) {
				return false
			}
			return details.DagRunDetails.Labels != nil
		}, 5*time.Second, 250*time.Millisecond)
		assert.ElementsMatch(t, labels, *details.DagRunDetails.Labels)

		_ = server.Client().Delete("/api/v1/dags/" + dagName).ExpectStatus(http.StatusNoContent).Send(t)
	})

	t.Run("EnqueueDAGWithInvalidLabels", func(t *testing.T) {
		spec := `
steps:
  - name: echo
    run: echo "test"
`
		dagName := "test_enqueue_invalid_labels_dag"

		_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
			Name: dagName,
			Spec: &spec,
		}).ExpectStatus(http.StatusCreated).Send(t)

		labels := []string{"@@@bad-label"}
		resp := server.Client().Post("/api/v1/dags/"+dagName+"/enqueue", api.EnqueueDAGDAGRunJSONRequestBody{
			Labels: &labels,
		}).ExpectStatus(http.StatusBadRequest).Send(t)

		var errResp api.Error
		resp.Unmarshal(t, &errResp)
		require.Equal(t, api.ErrorCodeBadRequest, errResp.Code)

		_ = server.Client().Delete("/api/v1/dags/" + dagName).ExpectStatus(http.StatusNoContent).Send(t)
	})

	t.Run("EnqueueDAGRunFromSpec", func(t *testing.T) {
		spec := fmt.Sprintf(`
steps:
  - %s
`, test.ShellQuote("exit 0"))
		name := "inline_enqueue_spec"

		resp := server.Client().Post("/api/v1/dag-runs/enqueue", api.EnqueueDAGRunFromSpecJSONRequestBody{
			Spec: spec,
			Name: &name,
		}).
			ExpectStatus(http.StatusOK).
			Send(t)

		var body api.EnqueueDAGRunFromSpec200JSONResponse
		resp.Unmarshal(t, &body)
		require.NotEmpty(t, body.DagRunId, "expected a non-empty dag-run ID")

		require.Eventually(t, func() bool {
			var dagRun api.GetDAGRunDetails200JSONResponse
			if !getJSONWhenAvailable(t, server, fmt.Sprintf("/api/v1/dag-runs/%s/%s", name, body.DagRunId), &dagRun) {
				return false
			}

			s := dagRun.DagRunDetails.Status
			return s == api.Status(core.Queued) || s == api.Status(core.Running) || s == api.Status(core.Succeeded)
		}, 5*time.Second, 250*time.Millisecond, "expected DAG-run to reach queued state")
	})

	t.Run("HistoryGridDataUsesExecutionOrder", func(t *testing.T) {
		spec := `
type: graph
steps:
  - name: c_leaf
    run: echo c
  - name: a_root
    run: echo a
  - name: b_mid
    run: echo b
    depends: [a_root]
`
		dagName := "test_history_execution_order"

		_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
			Name: dagName,
			Spec: &spec,
		}).ExpectStatus(http.StatusCreated).Send(t)
		t.Cleanup(func() {
			_ = server.Client().Delete("/api/v1/dags/" + dagName).Send(t)
		})

		resp := server.Client().Post("/api/v1/dags/"+dagName+"/start", api.ExecuteDAGJSONRequestBody{}).
			ExpectStatus(http.StatusOK).Send(t)
		var execResp api.ExecuteDAG200JSONResponse
		resp.Unmarshal(t, &execResp)
		require.NotEmpty(t, execResp.DagRunId)

		require.Eventually(t, func() bool {
			var dagRunStatus api.GetDAGDAGRunDetails200JSONResponse
			if !getJSONWhenAvailable(t, server, fmt.Sprintf("/api/v1/dags/%s/dag-runs/%s", dagName, execResp.DagRunId), &dagRunStatus) {
				return false
			}
			return dagRunStatus.DagRun.Status == api.Status(core.Succeeded)
		}, dagRunEventuallyTimeout(10*time.Second), 500*time.Millisecond, "expected DAG to complete")

		resp = server.Client().Get("/api/v1/dags/" + dagName + "/dag-runs").
			ExpectStatus(http.StatusOK).Send(t)
		var history api.GetDAGDAGRunHistory200JSONResponse
		resp.Unmarshal(t, &history)

		pos := make(map[string]int, len(history.GridData))
		for i, item := range history.GridData {
			pos[item.Name] = i
		}

		require.Contains(t, pos, "c_leaf")
		require.Contains(t, pos, "a_root")
		require.Contains(t, pos, "b_mid")
		require.Less(t, pos["c_leaf"], pos["a_root"])
		require.Less(t, pos["a_root"], pos["b_mid"])
	})

	t.Run("StartPreservesExplicitEnvFromFilteredChild", func(t *testing.T) {
		t.Setenv("API_START_EXPLICIT_ENV", "from-host")

		spec := fmt.Sprintf(`
env:
  - EXPORTED_SECRET: ${API_START_EXPLICIT_ENV}
steps:
  - name: capture
    run: %q
    output: RESULT
`, test.EnvOutput("EXPORTED_SECRET", "API_START_EXPLICIT_ENV"))
		dagName := "api_start_explicit_env"

		_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
			Name: dagName,
			Spec: &spec,
		}).ExpectStatus(http.StatusCreated).Send(t)
		t.Cleanup(func() {
			_ = server.Client().Delete("/api/v1/dags/" + dagName).Send(t)
		})

		resp := server.Client().Post("/api/v1/dags/"+dagName+"/start", api.ExecuteDAGJSONRequestBody{}).
			ExpectStatus(http.StatusOK).Send(t)

		var body api.ExecuteDAG200JSONResponse
		resp.Unmarshal(t, &body)
		require.NotEmpty(t, body.DagRunId)

		ref := exec.NewDAGRunRef(dagName, body.DagRunId)
		require.Eventually(t, func() bool {
			attempt, err := server.DAGRunStore.FindAttempt(server.Context, ref)
			if err != nil {
				return false
			}
			status, err := attempt.ReadStatus(server.Context)
			if err != nil {
				return false
			}
			return status.Status == core.Succeeded
		}, dagRunEventuallyTimeout(10*time.Second), 200*time.Millisecond)

		attempt, err := server.DAGRunStore.FindAttempt(server.Context, ref)
		require.NoError(t, err)
		status, err := attempt.ReadStatus(server.Context)
		require.NoError(t, err)
		require.Equal(t, "from-host|", apiStatusOutputValue(t, status, "RESULT"))
	})

	t.Run("EnqueuePersistsExplicitEnvForFilteredChild", func(t *testing.T) {
		t.Setenv("API_ENQUEUE_EXPLICIT_ENV", "from-host")

		spec := fmt.Sprintf(`
queue: api_enqueue_explicit_env
env:
  - EXPORTED_SECRET: ${API_ENQUEUE_EXPLICIT_ENV}
steps:
  - name: capture
    run: %q
    output: RESULT
`, test.EnvOutput("EXPORTED_SECRET", "API_ENQUEUE_EXPLICIT_ENV"))
		dagName := "api_enqueue_explicit_env"

		_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
			Name: dagName,
			Spec: &spec,
		}).ExpectStatus(http.StatusCreated).Send(t)
		t.Cleanup(func() {
			_ = server.Client().Delete("/api/v1/dags/" + dagName).Send(t)
		})

		resp := server.Client().Post("/api/v1/dags/"+dagName+"/enqueue", api.EnqueueDAGDAGRunJSONRequestBody{}).
			ExpectStatus(http.StatusOK).Send(t)

		var body api.EnqueueDAGDAGRun200JSONResponse
		resp.Unmarshal(t, &body)
		require.NotEmpty(t, body.DagRunId)

		attempt, err := server.DAGRunStore.FindAttempt(server.Context, exec.NewDAGRunRef(dagName, body.DagRunId))
		require.NoError(t, err)

		status, err := attempt.ReadStatus(server.Context)
		require.NoError(t, err)
		require.Equal(t, core.Queued, status.Status)

		queueProcessor := scheduler.NewQueueProcessor(
			server.QueueStore,
			server.DAGRunStore,
			server.ProcStore,
			scheduler.NewDAGExecutor(
				coordinator.New(server.ServiceRegistry, coordinator.DefaultConfig()),
				server.SubCmdBuilder,
				server.Config.DefaultExecMode,
				server.Config.Paths.BaseConfig,
				nil,
			),
			config.Queues{
				Enabled: true,
				Config: []config.QueueConfig{
					{Name: dagName, MaxActiveRuns: 1},
				},
			},
		)
		queueProcessor.ProcessQueueItems(server.Context, dagName)

		require.Eventually(t, func() bool {
			latestAttempt, err := server.DAGRunStore.FindAttempt(server.Context, exec.NewDAGRunRef(dagName, body.DagRunId))
			if err != nil {
				return false
			}
			latestStatus, err := latestAttempt.ReadStatus(server.Context)
			if err != nil {
				return false
			}
			return latestStatus.Status == core.Succeeded
		}, dagRunEventuallyTimeout(10*time.Second), 200*time.Millisecond)

		latestAttempt, err := server.DAGRunStore.FindAttempt(server.Context, exec.NewDAGRunRef(dagName, body.DagRunId))
		require.NoError(t, err)
		latestStatus, err := latestAttempt.ReadStatus(server.Context)
		require.NoError(t, err)
		require.Equal(t, "from-host|", apiStatusOutputValue(t, latestStatus, "RESULT"))
	})
}

func TestListDAGsMatchesFileNameWhenDagNameDiffers(t *testing.T) {
	server := test.SetupServer(t)

	spec := `
name: test_name
steps:
  - run: echo test
`
	server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: "approvaltest",
		Spec: &spec,
	}).ExpectStatus(http.StatusCreated).Send(t)
	t.Cleanup(func() {
		server.Client().Delete("/api/v1/dags/approvaltest").Send(t)
	})

	resp := server.Client().
		Get("/api/v1/dags?name=approvaltest").
		ExpectStatus(http.StatusOK).
		Send(t)

	var body api.ListDAGs200JSONResponse
	resp.Unmarshal(t, &body)

	require.Len(t, body.Dags, 1)
	require.Equal(t, "approvaltest", body.Dags[0].FileName)
	require.Equal(t, "test_name", body.Dags[0].Dag.Name)
}
