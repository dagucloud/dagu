// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	localapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/prometheus/client_golang/prometheus"
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

func apiStatusOutputValue(t *testing.T, status *ir.DAGRunStatus, key string) string {
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

func asGetDAGSpecResp(t *testing.T, respObj any) *api.GetDAGSpec200JSONResponse {
	t.Helper()

	resp, ok := respObj.(*api.GetDAGSpec200JSONResponse)
	if ok {
		return resp
	}
	valueResp, ok := respObj.(api.GetDAGSpec200JSONResponse)
	require.True(t, ok, "expected GetDAGSpec 200 response, got %T", respObj)
	return &valueResp
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

			return dagRunStatus.DagRun.Status == api.Status(ir.Succeeded)
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
			return dagRunDetails.DagRun.Status == api.Status(ir.Succeeded)
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
			return dagRunDetails.DagRun.Status == api.Status(ir.Succeeded)
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
			return dagRunStatus.DagRun.Status == api.Status(ir.Succeeded)
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
			return s == api.Status(ir.Queued) || s == api.Status(ir.Running) || s == api.Status(ir.Succeeded)
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
			return dagRunStatus.DagRun.Status == api.Status(ir.Succeeded)
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

		ref := ir.NewDAGRunRef(dagName, body.DagRunId)
		require.Eventually(t, func() bool {
			attempt, err := server.DAGRunRepository.FindAttempt(server.Context, ref)
			if err != nil {
				return false
			}
			status, err := attempt.ReadStatus(server.Context)
			if err != nil {
				return false
			}
			return status.Status == ir.Succeeded
		}, dagRunEventuallyTimeout(10*time.Second), 200*time.Millisecond)

		attempt, err := server.DAGRunRepository.FindAttempt(server.Context, ref)
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

		attempt, err := server.DAGRunRepository.FindAttempt(server.Context, ir.NewDAGRunRef(dagName, body.DagRunId))
		require.NoError(t, err)

		status, err := attempt.ReadStatus(server.Context)
		require.NoError(t, err)
		require.Equal(t, ir.Queued, status.Status)

		queueProcessor := scheduler.NewQueueProcessor(
			server.QueueStore,
			server.DAGRunRepository,
			server.ProcRepository,
			scheduler.NewDAGExecutor(
				coordinator.New(server.ServiceRegistry, test.CoordinatorClientConfig(server.Config.Paths.DataDir)),
				server.SubCmdBuilder,
				server.Config.DefaultExecMode,
				server.Config.Paths.BaseConfig,
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
			latestAttempt, err := server.DAGRunRepository.FindAttempt(server.Context, ir.NewDAGRunRef(dagName, body.DagRunId))
			if err != nil {
				return false
			}
			latestStatus, err := latestAttempt.ReadStatus(server.Context)
			if err != nil {
				return false
			}
			return latestStatus.Status == ir.Succeeded
		}, dagRunEventuallyTimeout(10*time.Second), 200*time.Millisecond)

		latestAttempt, err := server.DAGRunRepository.FindAttempt(server.Context, ir.NewDAGRunRef(dagName, body.DagRunId))
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

func TestRecursiveDAGDiscoveryUsesFileNameAPI(t *testing.T) {
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.DAGDiscovery.Recursive = true
	}))

	nestedDir := filepath.Join(server.Config.Paths.DAGsDir, "team", "services")
	require.NoError(t, os.MkdirAll(nestedDir, 0750))
	dagSpec := fmt.Sprintf(`
name: nested-effective-name
steps:
  - %s
`, test.ShellQuote("exit 0"))
	require.NoError(t, os.WriteFile(filepath.Join(nestedDir, "nested-file.yaml"), []byte(dagSpec), 0600))

	resp := server.Client().Get("/api/v1/dags?name=nested-file").ExpectStatus(http.StatusOK).Send(t)
	var listBody api.ListDAGs200JSONResponse
	resp.Unmarshal(t, &listBody)
	require.Len(t, listBody.Dags, 1)
	assert.Equal(t, "nested-file", listBody.Dags[0].FileName)
	assert.Equal(t, "nested-effective-name", listBody.Dags[0].Dag.Name)

	resp = server.Client().Get("/api/v1/dags/nested-file").ExpectStatus(http.StatusOK).Send(t)
	var details api.GetDAGDetails200JSONResponse
	resp.Unmarshal(t, &details)
	require.NotNil(t, details.Dag)
	assert.Equal(t, "nested-effective-name", details.Dag.Name)

	resp = server.Client().Post("/api/v1/dags/nested-file/start", api.ExecuteDAGJSONRequestBody{}).
		ExpectStatus(http.StatusOK).Send(t)
	var executeBody api.ExecuteDAG200JSONResponse
	resp.Unmarshal(t, &executeBody)
	require.NotEmpty(t, executeBody.DagRunId)

	require.Eventually(t, func() bool {
		url := fmt.Sprintf("/api/v1/dags/nested-file/dag-runs/%s", executeBody.DagRunId)
		var status api.GetDAGDAGRunDetails200JSONResponse
		if !getJSONWhenAvailable(t, server, url, &status) {
			return false
		}
		return status.DagRun.Status == api.Status(ir.Succeeded)
	}, dagRunEventuallyTimeout(5*time.Second), 200*time.Millisecond)
}

func TestExternalDAGFileSymlinkAPI(t *testing.T) {
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.DAGDiscovery.Symlinks = true
	}))

	targetDir := t.TempDir()
	targetPath := filepath.Join(targetDir, "resolved-target-name-that-is-not-the-entry.yaml")
	dagSpec := fmt.Sprintf(`
steps:
  - %s
`, test.ShellQuote("exit 0"))
	require.NoError(t, os.WriteFile(targetPath, []byte(dagSpec), 0600))
	if err := os.Symlink(targetPath, filepath.Join(server.Config.Paths.DAGsDir, "external-link.yaml")); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}

	resp := server.Client().Get("/api/v1/dags?name=external-link").
		ExpectStatus(http.StatusOK).Send(t)
	var listBody api.ListDAGs200JSONResponse
	resp.Unmarshal(t, &listBody)
	require.Len(t, listBody.Dags, 1)
	assert.Equal(t, "external-link", listBody.Dags[0].Dag.Name)

	resp = server.Client().Get("/api/v1/dags/external-link").
		ExpectStatus(http.StatusOK).Send(t)
	var details api.GetDAGDetails200JSONResponse
	resp.Unmarshal(t, &details)
	require.NotNil(t, details.Dag)
	assert.Equal(t, "external-link", details.Dag.Name)

	resp = server.Client().Post(
		"/api/v1/dags/external-link/enqueue",
		api.EnqueueDAGDAGRunJSONRequestBody{},
	).ExpectStatus(http.StatusOK).Send(t)
	var enqueueBody api.EnqueueDAGDAGRun200JSONResponse
	resp.Unmarshal(t, &enqueueBody)
	require.NotEmpty(t, enqueueBody.DagRunId)

	resp = server.Client().Put("/api/v1/dags/external-link/spec", api.UpdateDAGSpecJSONRequestBody{
		Spec: dagSpec,
	}).ExpectStatus(http.StatusForbidden).Send(t)
	var errorBody api.Error
	resp.Unmarshal(t, &errorBody)
	assert.Equal(t, api.ErrorCodeForbidden, errorBody.Code)
	assert.Contains(t, errorBody.Message, "external file symlink")

	server.Client().Delete("/api/v1/dags/external-link").
		ExpectStatus(http.StatusForbidden).Send(t)
}

type stubSchedulerStateStore struct {
	state *schedulerstate.State
}

func (s stubSchedulerStateStore) Load(context.Context) (*schedulerstate.State, error) {
	return schedulerstate.Clone(s.state), nil
}

func (stubSchedulerStateStore) Save(context.Context, *schedulerstate.State) error {
	return nil
}

func TestListDAGsDataPreservesNextRunAcrossSSEPath(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	scheduledAt := time.Now().UTC().Truncate(time.Minute).Add(-5 * time.Minute)
	dag := helper.DAG(t, fmt.Sprintf(`
name: sse-next-run-dag
schedule:
  - at: "%s"
steps:
  - run: echo hi
`, scheduledAt.Format(time.RFC3339)))

	state := &schedulerstate.State{
		DAGs: map[string]schedulerstate.DAGWatermark{
			dag.Name: {
				NextRun: &scheduledAt,
				OneOffs: map[string]schedulerstate.OneOffScheduleState{
					dag.Schedule[0].Fingerprint(): {
						ScheduledTime: scheduledAt,
						Status:        schedulerstate.OneOffStatusPending,
					},
				},
			},
		},
	}

	apiImpl := localapi.New(
		helper.DAGRepository,
		helper.DAGRunRepository,
		helper.QueueStore,
		helper.ProcRepository,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
		localapi.WithSchedulerStateStore(stubSchedulerStateStore{state: state}),
	)

	name := dag.Name
	listRespObj, err := apiImpl.ListDAGs(context.Background(), api.ListDAGsRequestObject{
		Params: api.ListDAGsParams{Name: &name},
	})
	require.NoError(t, err)

	listResp, ok := listRespObj.(*api.ListDAGs200JSONResponse)
	require.True(t, ok)
	require.Len(t, listResp.Dags, 1)
	require.NotNil(t, listResp.Dags[0].NextRun)
	require.True(t, scheduledAt.Equal(*listResp.Dags[0].NextRun))

	sseRespAny, err := apiImpl.GetDAGsListData(context.Background(), "name="+name)
	require.NoError(t, err)

	sseResp, ok := sseRespAny.(api.ListDAGs200JSONResponse)
	require.True(t, ok)
	require.Len(t, sseResp.Dags, 1)
	require.NotNil(t, sseResp.Dags[0].NextRun)
	require.True(t, listResp.Dags[0].NextRun.Equal(*sseResp.Dags[0].NextRun))
}

func TestListDAGsDataUsesStoredEmptyNextRunProjection(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	dag := helper.DAG(t, `
name: inactive-profile-next-run-dag
schedule:
  - expression: "* * * * *"
    profile: prod
steps:
  - run: echo hi
`)

	state := &schedulerstate.State{
		DAGs: map[string]schedulerstate.DAGWatermark{
			dag.Name: {},
		},
	}

	apiImpl := localapi.New(
		helper.DAGRepository,
		helper.DAGRunRepository,
		helper.QueueStore,
		helper.ProcRepository,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
		localapi.WithSchedulerStateStore(stubSchedulerStateStore{state: state}),
	)

	name := dag.Name
	listRespObj, err := apiImpl.ListDAGs(context.Background(), api.ListDAGsRequestObject{
		Params: api.ListDAGsParams{Name: &name},
	})
	require.NoError(t, err)

	listResp, ok := listRespObj.(*api.ListDAGs200JSONResponse)
	require.True(t, ok)
	require.Len(t, listResp.Dags, 1)
	require.Nil(t, listResp.Dags[0].NextRun)
}

func TestListDAGsDataFallsBackForMissingUnprofiledProjection(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	dag := helper.DAG(t, `
name: cold-unprofiled-next-run-dag
schedule:
  - expression: "* * * * *"
steps:
  - run: echo hi
`)

	state := &schedulerstate.State{
		DAGs: map[string]schedulerstate.DAGWatermark{},
	}

	apiImpl := localapi.New(
		helper.DAGRepository,
		helper.DAGRunRepository,
		helper.QueueStore,
		helper.ProcRepository,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
		localapi.WithSchedulerStateStore(stubSchedulerStateStore{state: state}),
	)

	name := dag.Name
	listRespObj, err := apiImpl.ListDAGs(context.Background(), api.ListDAGsRequestObject{
		Params: api.ListDAGsParams{Name: &name},
	})
	require.NoError(t, err)

	listResp, ok := listRespObj.(*api.ListDAGs200JSONResponse)
	require.True(t, ok)
	require.Len(t, listResp.Dags, 1)
	require.NotNil(t, listResp.Dags[0].NextRun)
}

func TestNextRunProjectionUsesConfiguredLocation(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	est := time.FixedZone("EST", -5*3600)
	helper.Config.Core.Location = est

	schedule, err := ir.NewCronSchedule("0 15 * * *")
	require.NoError(t, err)

	dag := &ir.DAG{
		Name:     "timezone-next-run-dag",
		Schedule: []ir.Schedule{schedule},
	}

	apiImpl := localapi.New(
		helper.DAGRepository,
		helper.DAGRunRepository,
		helper.QueueStore,
		helper.ProcRepository,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
	)

	now := time.Date(2026, 2, 7, 20, 30, 0, 0, time.UTC)
	next := localapi.NextRunProjectionForTest(context.Background(), apiImpl)(dag, now)

	require.Equal(t, time.Date(2026, 2, 8, 15, 0, 0, 0, est), next)
}

func TestGetDAGsListDataUsesConfiguredListDefaults(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	helper.Config.UI.DAGs.SortField = "name"
	helper.Config.UI.DAGs.SortOrder = "desc"
	helper.DAG(t, `
name: sse-sort-alpha
steps:
  - run: echo alpha
`)
	helper.DAG(t, `
name: sse-sort-zulu
steps:
  - run: echo zulu
`)

	apiImpl := localapi.New(
		helper.DAGRepository,
		helper.DAGRunRepository,
		helper.QueueStore,
		helper.ProcRepository,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
	)

	listRespObj, err := apiImpl.ListDAGs(context.Background(), api.ListDAGsRequestObject{
		Params: api.ListDAGsParams{},
	})
	require.NoError(t, err)

	listResp, ok := listRespObj.(*api.ListDAGs200JSONResponse)
	require.True(t, ok)
	require.Len(t, listResp.Dags, 2)
	require.Equal(t, "sse-sort-zulu", listResp.Dags[0].Dag.Name)

	sseRespAny, err := apiImpl.GetDAGsListData(context.Background(), "")
	require.NoError(t, err)

	sseResp, ok := sseRespAny.(api.ListDAGs200JSONResponse)
	require.True(t, ok)
	require.Len(t, sseResp.Dags, 2)
	require.Equal(t, listResp.Dags[0].Dag.Name, sseResp.Dags[0].Dag.Name)
	require.Equal(t, listResp.Dags[1].Dag.Name, sseResp.Dags[1].Dag.Name)
}

func TestListDAGsActiveFilterMatchesSSEPath(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	helper.DAG(t, `
name: active-filter-live
schedule: "0 * * * *"
steps:
  - run: echo live
`)
	suspended := helper.DAG(t, `
name: active-filter-suspended
schedule: "0 * * * *"
steps:
  - run: echo suspended
`)
	helper.DAG(t, `
name: active-filter-unscheduled
steps:
  - run: echo unscheduled
`)
	require.NoError(t, helper.DAGRepository.SetSuspended(context.Background(), suspended.FileName(), true))

	apiImpl := localapi.New(
		helper.DAGRepository,
		helper.DAGRunRepository,
		helper.QueueStore,
		helper.ProcRepository,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
	)

	active := true
	listRespObj, err := apiImpl.ListDAGs(context.Background(), api.ListDAGsRequestObject{
		Params: api.ListDAGsParams{Active: &active},
	})
	require.NoError(t, err)
	listResp, ok := listRespObj.(*api.ListDAGs200JSONResponse)
	require.True(t, ok)
	require.Len(t, listResp.Dags, 1)
	assert.Equal(t, "active-filter-live", listResp.Dags[0].Dag.Name)
	assert.Equal(t, 1, listResp.Pagination.TotalRecords)

	sseRespAny, err := apiImpl.GetDAGsListData(context.Background(), "active=true")
	require.NoError(t, err)
	sseResp, ok := sseRespAny.(api.ListDAGs200JSONResponse)
	require.True(t, ok)
	require.Len(t, sseResp.Dags, 1)
	assert.Equal(t, listResp.Dags[0].Dag.Name, sseResp.Dags[0].Dag.Name)
	assert.Equal(t, listResp.Pagination.TotalRecords, sseResp.Pagination.TotalRecords)
}

func TestGetDAGDetails_InvalidYAML_Returns200WithErrors(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())

	// Write an invalid YAML file directly to the DAGs directory
	invalidYAML := `this is not valid yaml: [unterminated`
	dagFile := helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "invalid-dag", []byte(invalidYAML))
	fileName := filepath.Base(dagFile)
	// Strip .yaml extension to match how the API resolves filenames
	fileName = fileName[:len(fileName)-len(".yaml")]

	apiImpl := localapi.New(
		helper.DAGRepository,
		helper.DAGRunRepository,
		helper.QueueStore,
		helper.ProcRepository,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
	)

	respObj, err := apiImpl.GetDAGDetails(context.Background(), api.GetDAGDetailsRequestObject{
		FileName: fileName,
	})
	// Should NOT return an error (which would become a 404/500)
	require.NoError(t, err)

	resp, ok := respObj.(api.GetDAGDetails200JSONResponse)
	require.True(t, ok, "expected 200 response, got %T", respObj)

	// Should contain build errors describing the YAML parse failure
	require.NotEmpty(t, resp.Errors, "expected build errors for invalid YAML")
}

func TestUpdateDAGSpec_AllowsLegacyDefinitionRuntimeVariableInput(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "legacy-definition-runtime-save", []byte(`
name: legacy-definition-runtime-save
steps:
  - run: echo original
`))

	apiImpl := localapi.New(
		helper.DAGRepository,
		helper.DAGRunRepository,
		helper.QueueStore,
		helper.ProcRepository,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
	)

	specText := `
name: legacy-definition-runtime-save
type: graph
step_types:
  repeat:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      required: [message, count]
      properties:
        message:
          type: string
        count:
          type: integer
    template:
      exec:
        command: /bin/echo
        args:
          - {$input: message}
          - {$input: count}
steps:
  - id: produce
    run: echo 3
    output: COUNT
  - id: consume
    depends: [produce]
    type: repeat
    with:
      message: runtime value
      count: ${COUNT}
`

	respObj, err := apiImpl.UpdateDAGSpec(context.Background(), api.UpdateDAGSpecRequestObject{
		FileName: "legacy-definition-runtime-save",
		Body: &api.UpdateDAGSpecJSONRequestBody{
			Spec: specText,
		},
	})
	require.NoError(t, err)

	resp, ok := respObj.(api.UpdateDAGSpec200JSONResponse)
	require.True(t, ok, "expected 200 response, got %T", respObj)
	require.Empty(t, resp.Errors)
}

func TestUpdateDAGSpec_StepConfigAliasCompatibility(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "step-config-alias-api", []byte(`
name: step-config-alias-api
steps:
  - run: echo original
`))

	apiImpl := localapi.New(
		helper.DAGRepository,
		helper.DAGRunRepository,
		helper.QueueStore,
		helper.ProcRepository,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
	)

	respObj, err := apiImpl.UpdateDAGSpec(context.Background(), api.UpdateDAGSpecRequestObject{
		FileName: "step-config-alias-api",
		Body: &api.UpdateDAGSpecJSONRequestBody{
			Spec: `
name: step-config-alias-api
steps:
  - name: request
    type: http
    command: GET https://example.com
    config:
      timeout: 30
`,
		},
	})
	require.NoError(t, err)

	resp, ok := respObj.(api.UpdateDAGSpec200JSONResponse)
	require.True(t, ok, "expected 200 response, got %T", respObj)
	require.Empty(t, resp.Errors)
}

func TestUpdateDAGSpec_RejectsStepWithAndLegacyConfigTogether(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "step-mixed-config-api", []byte(`
name: step-mixed-config-api
steps:
  - run: echo original
`))

	apiImpl := localapi.New(
		helper.DAGRepository,
		helper.DAGRunRepository,
		helper.QueueStore,
		helper.ProcRepository,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
	)

	respObj, err := apiImpl.UpdateDAGSpec(context.Background(), api.UpdateDAGSpecRequestObject{
		FileName: "step-mixed-config-api",
		Body: &api.UpdateDAGSpecJSONRequestBody{
			Spec: `
name: step-mixed-config-api
steps:
  - name: request
    type: http
    command: GET https://example.com
    with:
      timeout: 30
    config:
      timeout: 60
`,
		},
	})
	require.NoError(t, err)

	resp, ok := respObj.(api.UpdateDAGSpec200JSONResponse)
	require.True(t, ok, "expected 200 response, got %T", respObj)
	require.NotEmpty(t, resp.Errors)
	require.Contains(t, resp.Errors[0], `fields "with" and "config" cannot be used together`)
}

func TestUpdateDAGSpec_NotifiesDAGMutation(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "dag-update-notify", []byte(`
name: dag-update-notify
schedule: "34 * * * *"
steps:
  - run: echo original
`))

	var notified []string
	apiImpl := localapi.New(
		helper.DAGRepository,
		helper.DAGRunRepository,
		helper.QueueStore,
		helper.ProcRepository,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
		localapi.WithDAGMutationNotifier(func(fileName string) {
			notified = append(notified, fileName)
		}),
	)

	respObj, err := apiImpl.UpdateDAGSpec(context.Background(), api.UpdateDAGSpecRequestObject{
		FileName: "dag-update-notify",
		Body: &api.UpdateDAGSpecJSONRequestBody{
			Spec: `
name: dag-update-notify
schedule: "43 * * * *"
steps:
  - run: echo updated
`,
		},
	})
	require.NoError(t, err)

	resp, ok := respObj.(api.UpdateDAGSpec200JSONResponse)
	require.True(t, ok, "expected 200 response, got %T", respObj)
	require.Empty(t, resp.Errors)
	require.Equal(t, []string{"dag-update-notify"}, notified)
}

func TestUpdateDAGSuspensionState_NotifiesDAGMutation(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	dag := helper.DAG(t, `
name: dag-suspend-notify
schedule: "43 * * * *"
steps:
  - run: echo original
`)

	var notified []string
	apiImpl := localapi.New(
		helper.DAGRepository,
		helper.DAGRunRepository,
		helper.QueueStore,
		helper.ProcRepository,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
		localapi.WithDAGMutationNotifier(func(fileName string) {
			notified = append(notified, fileName)
		}),
	)

	respObj, err := apiImpl.UpdateDAGSuspensionState(context.Background(), api.UpdateDAGSuspensionStateRequestObject{
		FileName: dag.FileName(),
		Body: &api.UpdateDAGSuspensionStateJSONRequestBody{
			Suspend: true,
		},
	})
	require.NoError(t, err)

	_, ok := respObj.(api.UpdateDAGSuspensionState200Response)
	require.True(t, ok, "expected 200 response, got %T", respObj)
	require.Equal(t, []string{dag.FileName()}, notified)
}

func TestGetDAGDetails_EditorHintsIncludeInheritedLegacyDefinitions(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	require.NoError(t, os.WriteFile(helper.Config.Paths.BaseConfig, []byte(`
step_types:
  greet:
    type: command
    description: Send a greeting
    input_schema:
      type: object
      additionalProperties: false
      required: [message]
      properties:
        message:
          type: string
    template:
      exec:
        command: echo
        args:
          - {$input: message}
actions:
  slack.notify:
    description: Send Slack notification
    input_schema:
      type: object
      additionalProperties: false
      required: [text]
      properties:
        text:
          type: string
    template:
      action: http.request
      with:
        method: POST
        url: ${SLACK_WEBHOOK_URL}
        body: {$input: text}
`), 0o600))

	dag := helper.DAG(t, `
name: inherited-editor-hints
steps:
  - run: echo hi
`)

	apiImpl := localapi.New(
		helper.DAGRepository,
		helper.DAGRunRepository,
		helper.QueueStore,
		helper.ProcRepository,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
	)

	respObj, err := apiImpl.GetDAGDetails(context.Background(), api.GetDAGDetailsRequestObject{
		FileName: dag.FileName(),
	})
	require.NoError(t, err)

	resp, ok := respObj.(api.GetDAGDetails200JSONResponse)
	require.True(t, ok)
	require.NotNil(t, resp.EditorHints)
	require.Len(t, resp.EditorHints.InheritedLegacyDefinitions, 1)
	require.NotNil(t, resp.EditorHints.InheritedCustomActions)
	require.Len(t, *resp.EditorHints.InheritedCustomActions, 1)

	hint := resp.EditorHints.InheritedLegacyDefinitions[0]
	require.Equal(t, "greet", hint.Name)
	require.Equal(t, "command", hint.TargetType)
	require.NotNil(t, hint.Description)
	require.Equal(t, "Send a greeting", *hint.Description)

	properties, ok := hint.InputSchema["properties"].(map[string]any)
	require.True(t, ok)
	message, ok := properties["message"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "string", message["type"])

	actionHint := (*resp.EditorHints.InheritedCustomActions)[0]
	require.Equal(t, "slack.notify", actionHint.Name)
	require.NotNil(t, actionHint.Description)
	require.Equal(t, "Send Slack notification", *actionHint.Description)

	actionProperties, ok := actionHint.InputSchema["properties"].(map[string]any)
	require.True(t, ok)
	text, ok := actionProperties["text"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "string", text["type"])
}

func TestGetDAGDetails_EditorHintsKeepDistinctDescriptions(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	require.NoError(t, os.WriteFile(helper.Config.Paths.BaseConfig, []byte(`
step_types:
  greet:
    type: command
    description: First description
    input_schema:
      type: object
      properties: {}
    template:
      exec:
        command: echo
  wave:
    type: command
    description: Second description
    input_schema:
      type: object
      properties: {}
    template:
      exec:
        command: echo
`), 0o600))

	dag := helper.DAG(t, `
name: inherited-editor-hint-descriptions
steps:
  - run: echo hi
`)

	apiImpl := localapi.New(
		helper.DAGRepository,
		helper.DAGRunRepository,
		helper.QueueStore,
		helper.ProcRepository,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
	)

	respObj, err := apiImpl.GetDAGDetails(context.Background(), api.GetDAGDetailsRequestObject{
		FileName: dag.FileName(),
	})
	require.NoError(t, err)

	resp, ok := respObj.(api.GetDAGDetails200JSONResponse)
	require.True(t, ok)
	require.NotNil(t, resp.EditorHints)
	require.Len(t, resp.EditorHints.InheritedLegacyDefinitions, 2)

	require.NotNil(t, resp.EditorHints.InheritedLegacyDefinitions[0].Description)
	require.NotNil(t, resp.EditorHints.InheritedLegacyDefinitions[1].Description)
	require.Equal(t, "First description", *resp.EditorHints.InheritedLegacyDefinitions[0].Description)
	require.Equal(t, "Second description", *resp.EditorHints.InheritedLegacyDefinitions[1].Description)
}

func TestGetDAGDetails_InvalidYAMLStillReturnsEditorHints(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	require.NoError(t, os.WriteFile(helper.Config.Paths.BaseConfig, []byte(`
step_types:
  greet:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      required: [message]
      properties:
        message:
          type: string
    template:
      exec:
        command: echo
        args:
          - {$input: message}
`), 0o600))

	invalidYAML := `this is not valid yaml: [unterminated`
	dagFile := helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "invalid-hints-dag", []byte(invalidYAML))
	fileName := filepath.Base(dagFile)
	fileName = fileName[:len(fileName)-len(".yaml")]

	apiImpl := localapi.New(
		helper.DAGRepository,
		helper.DAGRunRepository,
		helper.QueueStore,
		helper.ProcRepository,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
	)

	respObj, err := apiImpl.GetDAGDetails(context.Background(), api.GetDAGDetailsRequestObject{
		FileName: fileName,
	})
	require.NoError(t, err)

	resp, ok := respObj.(api.GetDAGDetails200JSONResponse)
	require.True(t, ok)
	require.NotEmpty(t, resp.Errors)
	require.NotNil(t, resp.EditorHints)
	require.Len(t, resp.EditorHints.InheritedLegacyDefinitions, 1)
	require.Equal(t, "greet", resp.EditorHints.InheritedLegacyDefinitions[0].Name)
}

func TestGetDAGDetails_NonExistent_Returns404(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())

	apiImpl := localapi.New(
		helper.DAGRepository,
		helper.DAGRunRepository,
		helper.QueueStore,
		helper.ProcRepository,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
	)

	_, err := apiImpl.GetDAGDetails(context.Background(), api.GetDAGDetailsRequestObject{
		FileName: "does-not-exist",
	})
	var apiErr *localapi.Error
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, 404, apiErr.HTTPStatus)
	require.Equal(t, api.ErrorCodeNotFound, apiErr.Code)
}

func TestGetDAGsListDataIncludingAltDirs(t *testing.T) {
	t.Run("lists DAGs from both directories with query filters applied to the combined set", func(t *testing.T) {
		baseDir := t.TempDir()
		altDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(altDir, "alt-dag.yaml"), []byte("name: alt-dag\nsteps: []\n"), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(baseDir, "main-dag.yaml"), []byte("name: main-dag\nsteps: []\n"), 0600))

		cfg := &config.Config{}
		cfg.Paths.DAGsDir = baseDir
		cfg.Paths.AltDAGsDir = altDir
		cfg.Core.SkipExamples = true

		repo, err := persisfile.NewDAGRepository(cfg, persisfile.WithDAGSearchPaths([]string{altDir}))
		require.NoError(t, err)
		apiImpl := localapi.New(repo, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil)

		raw, err := apiImpl.GetDAGsListDataIncludingAltDirs(context.Background(), "")
		require.NoError(t, err)
		resp, ok := raw.(api.ListDAGs200JSONResponse)
		require.True(t, ok)
		var names []string
		for _, dag := range resp.Dags {
			names = append(names, dag.FileName)
		}
		require.ElementsMatch(t, []string{"alt-dag", "main-dag"}, names)

		rawFiltered, err := apiImpl.GetDAGsListDataIncludingAltDirs(context.Background(), "name=alt-dag")
		require.NoError(t, err)
		filtered, ok := rawFiltered.(api.ListDAGs200JSONResponse)
		require.True(t, ok)
		var filteredNames []string
		for _, dag := range filtered.Dags {
			filteredNames = append(filteredNames, dag.FileName)
		}
		require.Equal(t, []string{"alt-dag"}, filteredNames)
	})

	t.Run("matches regular listing when no alternate directory is configured", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Paths.DAGsDir = t.TempDir()
		cfg.Core.SkipExamples = true

		repo, err := persisfile.NewDAGRepository(cfg)
		require.NoError(t, err)
		apiImpl := localapi.New(repo, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil)

		raw, err := apiImpl.GetDAGsListDataIncludingAltDirs(context.Background(), "")
		require.NoError(t, err)
		resp, ok := raw.(api.ListDAGs200JSONResponse)
		require.True(t, ok)
		require.Empty(t, resp.Dags)
	})
}

func TestGetDAGDetailsAndSpecIncludeNextRun(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	scheduledAt := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	dag := helper.DAG(t, fmt.Sprintf(`
name: dag-details-next-run
schedule:
  - at: "%s"
steps:
  - run: echo hi
`, scheduledAt.Format(time.RFC3339)))

	state := &schedulerstate.State{
		DAGs: map[string]schedulerstate.DAGWatermark{
			dag.Name: {
				NextRun: &scheduledAt,
				OneOffs: map[string]schedulerstate.OneOffScheduleState{
					dag.Schedule[0].Fingerprint(): {
						ScheduledTime: scheduledAt,
						Status:        schedulerstate.OneOffStatusPending,
					},
				},
			},
		},
	}

	apiImpl := localapi.New(
		helper.DAGRepository,
		helper.DAGRunRepository,
		helper.QueueStore,
		helper.ProcRepository,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
		localapi.WithSchedulerStateStore(stubSchedulerStateStore{state: state}),
	)

	detailsRespObj, err := apiImpl.GetDAGDetails(context.Background(), api.GetDAGDetailsRequestObject{
		FileName: dag.FileName(),
	})
	require.NoError(t, err)

	detailsResp, ok := detailsRespObj.(api.GetDAGDetails200JSONResponse)
	require.True(t, ok)
	require.NotNil(t, detailsResp.Dag)
	require.NotNil(t, detailsResp.Dag.NextRun)
	require.True(t, scheduledAt.Equal(*detailsResp.Dag.NextRun))

	specRespObj, err := apiImpl.GetDAGSpec(context.Background(), api.GetDAGSpecRequestObject{
		FileName: dag.FileName(),
	})
	require.NoError(t, err)

	specResp := asGetDAGSpecResp(t, specRespObj)
	require.NotNil(t, specResp.Dag)
	require.NotNil(t, specResp.Dag.NextRun)
	require.True(t, scheduledAt.Equal(*specResp.Dag.NextRun))
}

func TestGetDAGSpecIncludesValueReferenceNotices(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	dag := helper.DAG(t, `
name: spec-value-resolution-notice
consts:
  - image: ${consts.missing}
steps:
  - run: echo ok
`)

	apiImpl := localapi.New(
		helper.DAGRepository,
		helper.DAGRunRepository,
		helper.QueueStore,
		helper.ProcRepository,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
	)

	specRespObj, err := apiImpl.GetDAGSpec(context.Background(), api.GetDAGSpecRequestObject{
		FileName: dag.FileName(),
	})
	require.NoError(t, err)

	specResp := asGetDAGSpecResp(t, specRespObj)
	require.Len(t, specResp.ValueReferenceNotices, 1)

	notice := specResp.ValueReferenceNotices[0]
	require.NotNil(t, notice.FieldPath)
	require.Equal(t, "consts.image", *notice.FieldPath)
	require.NotNil(t, notice.Token)
	require.Equal(t, "${consts.missing}", *notice.Token)
	require.NotEmpty(t, notice.Message)
}

func TestGetDAGSpecIncludesValueReferenceNoticeReason(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	dag := helper.DAG(t, `
name: spec-step-value-resolution-notice
steps:
  - id: build
    run: printf 'image=v1\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: image
  - id: deploy
    run: echo ${steps.build.outputs.image}
`)

	apiImpl := localapi.New(
		helper.DAGRepository,
		helper.DAGRunRepository,
		helper.QueueStore,
		helper.ProcRepository,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
	)

	specRespObj, err := apiImpl.GetDAGSpec(context.Background(), api.GetDAGSpecRequestObject{
		FileName: dag.FileName(),
	})
	require.NoError(t, err)

	specResp := asGetDAGSpecResp(t, specRespObj)
	require.Len(t, specResp.ValueReferenceNotices, 1)

	notice := specResp.ValueReferenceNotices[0]
	require.NotNil(t, notice.Reason)
	require.Equal(t, api.ValueReferenceNoticeReasonMissingDependency, *notice.Reason)
}

func TestGetDAGSpecIncludesValueReferenceNoticeClassWithoutReason(t *testing.T) {
	t.Parallel()

	helper := test.Setup(t, test.WithStatusPersistence())
	dag := helper.DAG(t, `
name: spec-runtime-value-resolution-notice
steps:
  - run: echo ${params.missing}
`)

	apiImpl := localapi.New(
		helper.DAGRepository,
		helper.DAGRunRepository,
		helper.QueueStore,
		helper.ProcRepository,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
	)

	specRespObj, err := apiImpl.GetDAGSpec(context.Background(), api.GetDAGSpecRequestObject{
		FileName: dag.FileName(),
	})
	require.NoError(t, err)

	specResp := asGetDAGSpecResp(t, specRespObj)
	require.Len(t, specResp.ValueReferenceNotices, 1)

	notice := specResp.ValueReferenceNotices[0]
	require.NotNil(t, notice.Class)
	require.Equal(t, api.ValueReferenceNoticeClassRuntimeOnly, *notice.Class)
}
