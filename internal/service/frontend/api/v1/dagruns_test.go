// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/filedagrun"
	"github.com/dagucloud/dagu/internal/runtime/transform"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dagRunEventuallyTimeout(base time.Duration) time.Duration {
	if runtime.GOOS == "windows" {
		return base * 24
	}
	return base
}

func dagRunSyncTimeoutSeconds() int {
	if runtime.GOOS == "windows" {
		return 120
	}
	return 30
}

func holdUntilFileExistsCommand(path string) string {
	return test.ForOS(
		fmt.Sprintf("while [ ! -f %s ]; do\n  sleep 0.05\ndone", test.PosixQuote(path)),
		fmt.Sprintf("while (-not (Test-Path %s)) {\n  Start-Sleep -Milliseconds 50\n}", test.PowerShellQuote(path)),
	)
}

func newHoldFile(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "release")
	t.Cleanup(func() {
		_ = os.WriteFile(path, []byte("release"), 0o600)
	})
	return path
}

func releaseHoldFile(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte("release"), 0o600))
}

func waitForDAGRunStatus(
	t *testing.T,
	server test.Server,
	dagName string,
	dagRunID string,
	timeout time.Duration,
	predicate func(*exec.DAGRunStatus) bool,
) *exec.DAGRunStatus {
	t.Helper()

	dag := &core.DAG{Name: dagName}
	var status *exec.DAGRunStatus
	require.Eventually(t, func() bool {
		current, err := server.DAGRunMgr.GetCurrentStatus(server.Context, dag, dagRunID)
		if err != nil || current == nil {
			return false
		}
		status = current
		return predicate(current)
	}, dagRunEventuallyTimeout(timeout), 200*time.Millisecond)

	return status
}

func waitForStoredDAGRunStatus(
	t *testing.T,
	server test.Server,
	dagName string,
	dagRunID string,
	timeout time.Duration,
	predicate func(*exec.DAGRunStatus) bool,
) *exec.DAGRunStatus {
	t.Helper()

	ref := exec.NewDAGRunRef(dagName, dagRunID)
	// Read persisted status through a fresh store without the long-lived server cache.
	// API tests intentionally verify out-of-band status writes (approve/reject/reschedule),
	// so cached reads can hide valid cross-process updates on Windows.
	store := filedagrun.New(
		server.Config.Paths.DAGRunsDir,
		filedagrun.WithLatestStatusToday(server.Config.Server.LatestStatusToday),
		filedagrun.WithLocation(server.Config.Core.Location),
	)
	var status *exec.DAGRunStatus
	require.Eventually(t, func() bool {
		attempt, err := store.FindAttempt(server.Context, ref)
		if err != nil {
			return false
		}
		current, err := attempt.ReadStatus(server.Context)
		if err != nil || current == nil {
			return false
		}
		status = current
		return predicate(current)
	}, dagRunEventuallyTimeout(timeout), 200*time.Millisecond)

	return status
}

func hasNodeWithStatus(status *exec.DAGRunStatus, stepName string, nodeStatus core.NodeStatus) bool {
	if status == nil {
		return false
	}

	for _, node := range status.Nodes {
		if node.Step.Name == stepName && node.Status == nodeStatus {
			return true
		}
	}

	return false
}

func postJSONWithConservativeTransport(t *testing.T, server test.Server, path string, body any) (int, []byte) {
	t.Helper()

	payload, err := json.Marshal(body)
	require.NoError(t, err, "failed to marshal request body")

	transport := &http.Transport{
		DisableCompression: true,
		DisableKeepAlives:  true,
	}
	t.Cleanup(transport.CloseIdleConnections)

	client := &http.Client{Transport: transport}
	req, err := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf("http://%s:%d%s", server.Config.Server.Host, server.Config.Server.Port, path),
		bytes.NewReader(payload),
	)
	require.NoError(t, err, "failed to build POST request")

	req.Close = true
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Connection", "close")
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	require.NoError(t, err, "failed to make POST request")
	defer func() {
		_ = resp.Body.Close()
	}()

	responseBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "failed to read response body")

	return resp.StatusCode, responseBody
}

func syncSuccessDagSpec() string {
	if runtime.GOOS == "windows" {
		return `steps:
  - name: echo-step
    run: "echo hello sync"
    with:
      shell: cmd`
	}

	return `steps:
  - name: echo-step
    run: "echo hello sync"`
}

func TestGetDAGRunSpec(t *testing.T) {
	server := test.SetupServer(t)

	dagSpec := `steps:
  - name: main
    run: "echo spec_test"`

	// Create a new DAG
	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: "spec_test_dag",
		Spec: &dagSpec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	// Start a DAG run
	startResp := server.Client().Post("/api/v1/dags/spec_test_dag/start", api.ExecuteDAGJSONRequestBody{}).
		ExpectStatus(http.StatusOK).Send(t)

	var startBody api.ExecuteDAG200JSONResponse
	startResp.Unmarshal(t, &startBody)
	require.NotEmpty(t, startBody.DagRunId)

	waitForDAGRunStatus(t, server, "spec_test_dag", startBody.DagRunId, 10*time.Second, func(status *exec.DAGRunStatus) bool {
		return status.Status == core.Succeeded
	})

	// Fetch the DAG spec for the DAG run
	specResp := server.Client().Get(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/spec", "spec_test_dag", startBody.DagRunId),
	).ExpectStatus(http.StatusOK).Send(t)

	var specBody api.GetDAGRunSpec200JSONResponse
	specResp.Unmarshal(t, &specBody)
	require.NotEmpty(t, specBody.Spec)
	require.Contains(t, specBody.Spec, "echo spec_test")

	// Test 404 for non-existent DAG
	_ = server.Client().Get(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/spec", "non_existent_dag", startBody.DagRunId),
	).ExpectStatus(http.StatusNotFound).Send(t)
}

func TestGetDAGRunSpecInline(t *testing.T) {
	server := test.SetupServer(t)

	inlineSpec := `steps:
  - name: inline_step
    run: "echo inline_dag_test"`

	name := "inline_spec_dag"

	// Execute an inline DAG run
	execResp := server.Client().Post("/api/v1/dag-runs", api.ExecuteDAGRunFromSpecJSONRequestBody{
		Spec: inlineSpec,
		Name: &name,
	}).ExpectStatus(http.StatusOK).Send(t)

	var execBody api.ExecuteDAGRunFromSpec200JSONResponse
	execResp.Unmarshal(t, &execBody)
	require.NotEmpty(t, execBody.DagRunId)

	specBody := requireDAGRunSpec(t, server, name, execBody.DagRunId)
	require.Contains(t, specBody.Spec, "echo inline_dag_test")
}

func TestGetDAGRunSpecInlineStartWithLabelsDoesNotPatchSpec(t *testing.T) {
	server := test.SetupServer(t)

	inlineSpec := `steps:
  - name: inline_step
    run: "echo inline_start_labels"`
	name := "inline_spec_start_labels"
	labels := []string{"env=prod", "team=backend"}

	execResp := server.Client().Post("/api/v1/dag-runs", api.ExecuteDAGRunFromSpecJSONRequestBody{
		Spec:   inlineSpec,
		Name:   &name,
		Labels: &labels,
	}).ExpectStatus(http.StatusOK).Send(t)

	var execBody api.ExecuteDAGRunFromSpec200JSONResponse
	execResp.Unmarshal(t, &execBody)
	require.NotEmpty(t, execBody.DagRunId)

	details := requireDAGRunDetails(t, server, name, execBody.DagRunId)
	require.NotNil(t, details.DagRunDetails.Labels)
	assert.ElementsMatch(t, labels, *details.DagRunDetails.Labels)

	specBody := requireDAGRunSpec(t, server, name, execBody.DagRunId)
	require.Contains(t, specBody.Spec, "echo inline_start_labels")
	require.NotContains(t, specBody.Spec, "labels:")
	require.NotContains(t, specBody.Spec, "tags:")
	require.NotContains(t, specBody.Spec, "env=prod")
	require.NotContains(t, specBody.Spec, "team=backend")
}

func requireDAGRunSpec(t *testing.T, server test.Server, dagName, dagRunID string) api.GetDAGRunSpec200JSONResponse {
	t.Helper()

	var specBody api.GetDAGRunSpec200JSONResponse
	require.Eventually(t, func() bool {
		specResp := server.Client().Get(
			fmt.Sprintf("/api/v1/dag-runs/%s/%s/spec", dagName, dagRunID),
		).Send(t)
		if specResp.Response.StatusCode() != http.StatusOK {
			return false
		}

		specResp.Unmarshal(t, &specBody)
		return specBody.Spec != ""
	}, dagRunEventuallyTimeout(10*time.Second), 200*time.Millisecond)

	return specBody
}

func requireDAGRunDetails(t *testing.T, server test.Server, dagName, dagRunID string) api.GetDAGRunDetails200JSONResponse {
	t.Helper()

	var details api.GetDAGRunDetails200JSONResponse
	require.Eventually(t, func() bool {
		resp := server.Client().Get(
			fmt.Sprintf("/api/v1/dag-runs/%s/%s", dagName, dagRunID),
		).Send(t)
		if resp.Response.StatusCode() != http.StatusOK {
			return false
		}

		resp.Unmarshal(t, &details)
		return details.DagRunDetails.DagRunId == dagRunID
	}, dagRunEventuallyTimeout(10*time.Second), 200*time.Millisecond)

	return details
}

func TestGetDAGRunSpecInlineEnqueueWithLabelsPatchesSpec(t *testing.T) {
	server := test.SetupServer(t)

	inlineSpec := `steps:
  - name: inline_step
    run: "echo inline_enqueue_labels"`
	name := "inline_enqueue_labels"
	labels := []string{"env=prod", "team=backend"}

	enqResp := server.Client().Post("/api/v1/dag-runs/enqueue", api.EnqueueDAGRunFromSpecJSONRequestBody{
		Spec:   inlineSpec,
		Name:   &name,
		Labels: &labels,
	}).ExpectStatus(http.StatusOK).Send(t)

	var enqBody api.EnqueueDAGRunFromSpec200JSONResponse
	enqResp.Unmarshal(t, &enqBody)
	require.NotEmpty(t, enqBody.DagRunId)

	require.Eventually(t, func() bool {
		statusResp := server.Client().
			Get(fmt.Sprintf("/api/v1/dag-runs/%s/%s", name, enqBody.DagRunId)).
			Send(t)
		if statusResp.Response.StatusCode() != http.StatusOK {
			return false
		}

		var dagRunStatus api.GetDAGRunDetails200JSONResponse
		statusResp.Unmarshal(t, &dagRunStatus)
		status := dagRunStatus.DagRunDetails.Status
		return status == api.Status(core.Queued) ||
			status == api.Status(core.Running) ||
			status == api.Status(core.Succeeded)
	}, dagRunEventuallyTimeout(10*time.Second), 200*time.Millisecond)
	details := requireDAGRunDetails(t, server, name, enqBody.DagRunId)
	require.NotNil(t, details.DagRunDetails.Labels)
	assert.ElementsMatch(t, labels, *details.DagRunDetails.Labels)

	specResp := server.Client().Get(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/spec", name, enqBody.DagRunId),
	).ExpectStatus(http.StatusOK).Send(t)

	var specBody api.GetDAGRunSpec200JSONResponse
	specResp.Unmarshal(t, &specBody)
	require.Contains(t, specBody.Spec, "echo inline_enqueue_labels")
	require.Contains(t, specBody.Spec, "labels:")
	require.NotContains(t, specBody.Spec, "tags:")
	require.Contains(t, specBody.Spec, "env=prod")
	require.Contains(t, specBody.Spec, "team=backend")
}

func TestGetDAGRunSpecFileEnqueueWithLabelsDoesNotPatchSpec(t *testing.T) {
	server := test.SetupServer(t)

	dagSpec := `steps:
  - name: main
    run: "echo file_enqueue_labels"`
	dagName := "file_enqueue_labels"
	labels := []string{"env=staging", "priority=low"}

	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: dagName,
		Spec: &dagSpec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	enqResp := server.Client().Post(
		fmt.Sprintf("/api/v1/dags/%s/enqueue", dagName),
		api.EnqueueDAGDAGRunJSONRequestBody{Labels: &labels},
	).ExpectStatus(http.StatusOK).Send(t)

	var enqBody api.EnqueueDAGDAGRun200JSONResponse
	enqResp.Unmarshal(t, &enqBody)
	require.NotEmpty(t, enqBody.DagRunId)

	require.Eventually(t, func() bool {
		statusResp := server.Client().
			Get(fmt.Sprintf("/api/v1/dag-runs/%s/%s", dagName, enqBody.DagRunId)).
			Send(t)
		if statusResp.Response.StatusCode() != http.StatusOK {
			return false
		}

		var dagRunStatus api.GetDAGRunDetails200JSONResponse
		statusResp.Unmarshal(t, &dagRunStatus)
		status := dagRunStatus.DagRunDetails.Status
		return status == api.Status(core.Queued) ||
			status == api.Status(core.Running) ||
			status == api.Status(core.Succeeded)
	}, dagRunEventuallyTimeout(10*time.Second), 200*time.Millisecond)
	details := requireDAGRunDetails(t, server, dagName, enqBody.DagRunId)
	require.NotNil(t, details.DagRunDetails.Labels)
	assert.ElementsMatch(t, labels, *details.DagRunDetails.Labels)

	specResp := server.Client().Get(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/spec", dagName, enqBody.DagRunId),
	).ExpectStatus(http.StatusOK).Send(t)

	var specBody api.GetDAGRunSpec200JSONResponse
	specResp.Unmarshal(t, &specBody)
	require.Contains(t, specBody.Spec, "echo file_enqueue_labels")
	require.NotContains(t, specBody.Spec, "labels:")
	require.NotContains(t, specBody.Spec, "tags:")
	require.NotContains(t, specBody.Spec, "env=staging")
	require.NotContains(t, specBody.Spec, "priority=low")
}

func TestGetSubDAGRunSpec(t *testing.T) {
	server := test.SetupServer(t)
	childCommand := test.Output("subdag-spec")

	// Create a parent DAG with an inline sub-DAG definition
	dagSpec := fmt.Sprintf(`steps:
  - name: call_child
    action: dag.run
    with:
      dag: child_dag
      params: "MSG=hello"

---

name: child_dag
params:
  - MSG
steps:
  - name: echo_message
    run: %q`, childCommand)

	// Create the parent DAG
	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: "parent_dag_for_subdag_spec",
		Spec: &dagSpec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	// Start the parent DAG
	startResp := server.Client().Post("/api/v1/dags/parent_dag_for_subdag_spec/start", api.ExecuteDAGJSONRequestBody{}).
		ExpectStatus(http.StatusOK).Send(t)

	var startBody api.ExecuteDAG200JSONResponse
	startResp.Unmarshal(t, &startBody)
	require.NotEmpty(t, startBody.DagRunId)

	status := waitForDAGRunStatus(t, server, "parent_dag_for_subdag_spec", startBody.DagRunId, 30*time.Second,
		func(status *exec.DAGRunStatus) bool {
			return status.Status == core.Succeeded &&
				len(status.Nodes) == 1 &&
				len(status.Nodes[0].SubRuns) == 1
		},
	)
	require.Len(t, status.Nodes, 1, "Expected 1 node (the call_child step)")

	callNode := status.Nodes[0]
	require.Equal(t, "call_child", callNode.Step.Name)
	subDAGRunID := callNode.SubRuns[0].DAGRunID

	// Test 1: Fetch the sub-DAG spec successfully
	subSpecResp := server.Client().Get(
		fmt.Sprintf("/api/v1/dag-runs/parent_dag_for_subdag_spec/%s/sub-dag-runs/%s/spec",
			startBody.DagRunId, subDAGRunID),
	).ExpectStatus(http.StatusOK).Send(t)

	var subSpecBody api.GetSubDAGRunSpec200JSONResponse
	subSpecResp.Unmarshal(t, &subSpecBody)
	require.NotEmpty(t, subSpecBody.Spec, "Sub-DAG spec should not be empty")
	require.Contains(t, subSpecBody.Spec, "child_dag", "Spec should contain child_dag name")
	require.Contains(t, subSpecBody.Spec, "echo_message", "Spec should contain echo_message step")
	require.Contains(t, subSpecBody.Spec, "subdag-spec", "Spec should contain the command")

	// Test 2: 404 for non-existent sub-DAG run ID
	_ = server.Client().Get(
		fmt.Sprintf("/api/v1/dag-runs/parent_dag_for_subdag_spec/%s/sub-dag-runs/%s/spec",
			startBody.DagRunId, "non_existent_sub_dag_id"),
	).ExpectStatus(http.StatusNotFound).Send(t)

	// Test 3: 404 for non-existent parent DAG
	_ = server.Client().Get(
		fmt.Sprintf("/api/v1/dag-runs/non_existent_dag/%s/sub-dag-runs/%s/spec",
			startBody.DagRunId, subDAGRunID),
	).ExpectStatus(http.StatusNotFound).Send(t)

	// Test 4: 404 for non-existent parent DAG run ID
	_ = server.Client().Get(
		fmt.Sprintf("/api/v1/dag-runs/parent_dag_for_subdag_spec/%s/sub-dag-runs/%s/spec",
			"non_existent_run_id", subDAGRunID),
	).ExpectStatus(http.StatusNotFound).Send(t)
}

func TestApproveDAGRunStep(t *testing.T) {
	server := test.SetupServer(t)

	dagSpec := fmt.Sprintf(`type: graph
steps:
  - name: wait-step
    run: %q
    approval:
      prompt: "Please approve"
  - name: after-wait
    depends: [wait-step]
    run: "echo approved"`, "exit 0")

	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: "approval_test_dag",
		Spec: &dagSpec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	// Start the DAG
	startResp := server.Client().Post("/api/v1/dags/approval_test_dag/start", api.ExecuteDAGJSONRequestBody{}).
		ExpectStatus(http.StatusOK).Send(t)

	var startBody api.ExecuteDAG200JSONResponse
	startResp.Unmarshal(t, &startBody)
	require.NotEmpty(t, startBody.DagRunId)

	// Wait for DAG to enter Wait status
	waitForStoredDAGRunStatus(t, server, "approval_test_dag", startBody.DagRunId, 10*time.Second, func(status *exec.DAGRunStatus) bool {
		return status.Status == core.Waiting && hasNodeWithStatus(status, "wait-step", core.NodeWaiting)
	})

	// Approve the wait step
	approveResp := server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/approval_test_dag/%s/steps/wait-step/approve", startBody.DagRunId),
		api.ApproveStepRequest{},
	).ExpectStatus(http.StatusOK).Send(t)

	var approveBody api.ApproveDAGRunStep200JSONResponse
	approveResp.Unmarshal(t, &approveBody)
	require.Equal(t, startBody.DagRunId, approveBody.DagRunId)
	require.Equal(t, "wait-step", approveBody.StepName)
	require.True(t, approveBody.Resumed)

	// Wait for DAG to complete
	waitForStoredDAGRunStatus(t, server, "approval_test_dag", startBody.DagRunId, 10*time.Second, func(status *exec.DAGRunStatus) bool {
		return status.Status == core.Succeeded
	})
}

func TestApproveDAGRunStepWithInputs(t *testing.T) {
	server := test.SetupServer(t)

	dagSpec := fmt.Sprintf(`type: graph
steps:
  - name: wait-step
    run: %q
    approval:
      prompt: "Please provide reason"
      input:
        - reason
        - approver
      required:
        - reason
  - name: after-wait
    depends: [wait-step]
    run: %q`, "exit 0", test.EnvOutput("reason", "approver"))

	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: "approval_inputs_dag",
		Spec: &dagSpec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	// Start the DAG
	startResp := server.Client().Post("/api/v1/dags/approval_inputs_dag/start", api.ExecuteDAGJSONRequestBody{}).
		ExpectStatus(http.StatusOK).Send(t)

	var startBody api.ExecuteDAG200JSONResponse
	startResp.Unmarshal(t, &startBody)
	require.NotEmpty(t, startBody.DagRunId)

	// Wait for DAG to enter Wait status
	waitForStoredDAGRunStatus(t, server, "approval_inputs_dag", startBody.DagRunId, 10*time.Second, func(status *exec.DAGRunStatus) bool {
		return status.Status == core.Waiting && hasNodeWithStatus(status, "wait-step", core.NodeWaiting)
	})

	// Approve with inputs
	inputs := map[string]string{
		"reason":   "testing",
		"approver": "test-user",
	}
	approveResp := server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/approval_inputs_dag/%s/steps/wait-step/approve", startBody.DagRunId),
		api.ApproveStepRequest{Inputs: &inputs},
	).ExpectStatus(http.StatusOK).Send(t)

	var approveBody api.ApproveDAGRunStep200JSONResponse
	approveResp.Unmarshal(t, &approveBody)
	require.True(t, approveBody.Resumed)

	// Wait for DAG to complete
	status := waitForStoredDAGRunStatus(t, server, "approval_inputs_dag", startBody.DagRunId, 10*time.Second, func(status *exec.DAGRunStatus) bool {
		return status.Status == core.Succeeded
	})
	require.Len(t, status.Nodes, 2)

	var waitNode, afterWaitNode *exec.Node
	for _, node := range status.Nodes {
		switch node.Step.Name {
		case "wait-step":
			waitNode = node
		case "after-wait":
			afterWaitNode = node
		}
	}
	require.NotNil(t, waitNode)
	require.NotNil(t, afterWaitNode)
	require.Equal(t, inputs, waitNode.ApprovalInputs)

	stdout, err := os.ReadFile(afterWaitNode.Stdout)
	require.NoError(t, err)
	require.Equal(t, "testing|test-user", strings.TrimSpace(string(stdout)))
}

func TestApproveDAGRunStepMissingRequired(t *testing.T) {
	server := test.SetupServer(t)

	dag := &core.DAG{
		Name: "approval_required_dag",
		Type: core.TypeGraph,
		Steps: []core.Step{
			{
				Name: "wait-step",
				Approval: &core.ApprovalConfig{
					Prompt:   "Please provide reason",
					Input:    []string{"reason"},
					Required: []string{"reason"},
				},
			},
			{
				Name:    "after-wait",
				Depends: []string{"wait-step"},
			},
		},
	}
	dagRunID := "approval-required-run"
	seedLatestDAGRunStatus(
		t,
		server,
		dag,
		dagRunID,
		core.Waiting,
		seedDAGRunStatusOptions{
			nodeStatuses: map[string]core.NodeStatus{
				"wait-step": core.NodeWaiting,
			},
		},
	)

	// Try to approve without required input - should fail
	_ = server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/approval_required_dag/%s/steps/wait-step/approve", dagRunID),
		api.ApproveStepRequest{},
	).ExpectStatus(http.StatusBadRequest).Send(t)
}

func TestApproveDAGRunStepNotWaiting(t *testing.T) {
	server := test.SetupServer(t)

	dagSpec := `steps:
  - name: main
    run: "echo done"`

	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: "no_wait_dag",
		Spec: &dagSpec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	startResp := server.Client().Post("/api/v1/dags/no_wait_dag/start", api.ExecuteDAGJSONRequestBody{}).
		ExpectStatus(http.StatusOK).Send(t)

	var startBody api.ExecuteDAG200JSONResponse
	startResp.Unmarshal(t, &startBody)

	waitForDAGRunStatus(t, server, "no_wait_dag", startBody.DagRunId, 10*time.Second, func(status *exec.DAGRunStatus) bool {
		return status.Status == core.Succeeded
	})

	resp := server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/no_wait_dag/%s/steps/main/approve", startBody.DagRunId),
		api.ApproveStepRequest{},
	).Send(t)
	require.Contains(t, []int{http.StatusBadRequest, http.StatusNotFound}, resp.Response.StatusCode())
}

func TestRejectDAGRunStep(t *testing.T) {
	server := test.SetupServer(t)

	dag := server.DAG(t, `name: rejection_test_dag
type: graph
steps:
  - name: wait-step
    run: "exit 0"
    approval:
      prompt: "Please approve"
  - name: after-wait
    depends: [wait-step]
    run: "echo should not run"`)
	ref := seedLatestDAGRunStatus(t, server, dag.DAG, "reject-waiting-run", core.Waiting, seedDAGRunStatusOptions{
		nodeStatuses: map[string]core.NodeStatus{
			"wait-step": core.NodeWaiting,
		},
	})

	// Reject the wait step
	reason := "test rejection reason"
	rejectResp := server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/steps/wait-step/reject", ref.Name, ref.ID),
		api.RejectStepRequest{Reason: &reason},
	).ExpectStatus(http.StatusOK).Send(t)

	var rejectBody api.RejectDAGRunStep200JSONResponse
	rejectResp.Unmarshal(t, &rejectBody)
	require.Equal(t, api.DAGRunId(ref.ID), rejectBody.DagRunId)
	require.Equal(t, "wait-step", rejectBody.StepName)

	// Verify DAG status is Rejected
	status := waitForStoredDAGRunStatus(t, server, ref.Name, ref.ID, 2*time.Second, func(status *exec.DAGRunStatus) bool {
		return status.Status == core.Rejected
	})
	require.True(t, hasNodeWithStatus(status, "wait-step", core.NodeRejected))
	require.Equal(t, reason, status.Nodes[0].RejectionReason)
}

func TestRejectDAGRunStepNotWaiting(t *testing.T) {
	server := test.SetupServer(t)

	dagSpec := `steps:
  - name: main
    run: "echo done"`

	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: "reject_not_waiting_dag",
		Spec: &dagSpec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	startResp := server.Client().Post("/api/v1/dags/reject_not_waiting_dag/start", api.ExecuteDAGJSONRequestBody{}).
		ExpectStatus(http.StatusOK).Send(t)

	var startBody api.ExecuteDAG200JSONResponse
	startResp.Unmarshal(t, &startBody)

	waitForDAGRunStatus(t, server, "reject_not_waiting_dag", startBody.DagRunId, 10*time.Second, func(status *exec.DAGRunStatus) bool {
		return status.Status == core.Succeeded
	})

	resp := server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/reject_not_waiting_dag/%s/steps/main/reject", startBody.DagRunId),
		api.RejectStepRequest{},
	).Send(t)
	require.Contains(t, []int{http.StatusBadRequest, http.StatusNotFound}, resp.Response.StatusCode())
}

func TestRescheduleDAGRun(t *testing.T) {
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Queues.Enabled = true
		cfg.Queues.Config = []config.QueueConfig{
			{Name: "reschedule_dag", MaxActiveRuns: 1},
		}
	}))

	dagSpec := `steps:
  - name: main
    run: "echo reschedule"`

	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: "reschedule_dag",
		Spec: &dagSpec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	startResp := server.Client().Post("/api/v1/dags/reschedule_dag/start", api.ExecuteDAGJSONRequestBody{}).
		ExpectStatus(http.StatusOK).Send(t)

	var startBody api.ExecuteDAG200JSONResponse
	startResp.Unmarshal(t, &startBody)
	require.NotEmpty(t, startBody.DagRunId)

	waitForDAGRunStatus(t, server, "reschedule_dag", startBody.DagRunId, 10*time.Second, func(status *exec.DAGRunStatus) bool {
		return status.Status == core.Succeeded
	})

	rescheduleResp := server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/reschedule", "reschedule_dag", startBody.DagRunId),
		api.RescheduleDAGRunJSONRequestBody{},
	).ExpectStatus(http.StatusOK).Send(t)

	var rescheduleBody api.RescheduleDAGRun200JSONResponse
	rescheduleResp.Unmarshal(t, &rescheduleBody)
	require.NotEmpty(t, rescheduleBody.DagRunId)
	require.True(t, rescheduleBody.Queued)

	test.ProcessQueuedInlineRun(t, server, "reschedule_dag")

	waitForDAGRunStatus(t, server, "reschedule_dag", rescheduleBody.DagRunId, 10*time.Second, func(status *exec.DAGRunStatus) bool {
		return status.Status == core.Succeeded
	})
}

func TestRescheduleDAGRunResolvesLatest(t *testing.T) {
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Queues.Enabled = true
		cfg.Queues.Config = []config.QueueConfig{
			{Name: "reschedule_latest_dag", MaxActiveRuns: 1},
		}
	}))

	dagSpec := `steps:
  - name: main
    run: "echo reschedule latest"`

	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: "reschedule_latest_dag",
		Spec: &dagSpec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	startResp := server.Client().Post("/api/v1/dags/reschedule_latest_dag/start", api.ExecuteDAGJSONRequestBody{}).
		ExpectStatus(http.StatusOK).Send(t)

	var startBody api.ExecuteDAG200JSONResponse
	startResp.Unmarshal(t, &startBody)
	require.NotEmpty(t, startBody.DagRunId)

	require.Eventually(t, func() bool {
		url := fmt.Sprintf("/api/v1/dags/reschedule_latest_dag/dag-runs/%s", startBody.DagRunId)
		statusResp := server.Client().Get(url).Send(t)
		if statusResp.Response.StatusCode() != http.StatusOK {
			return false
		}

		var dagRunStatus api.GetDAGDAGRunDetails200JSONResponse
		statusResp.Unmarshal(t, &dagRunStatus)
		return dagRunStatus.DagRun.Status == api.Status(core.Succeeded)
	}, dagRunEventuallyTimeout(10*time.Second), 200*time.Millisecond)

	rescheduleResp := server.Client().Post(
		"/api/v1/dag-runs/reschedule_latest_dag/latest/reschedule",
		api.RescheduleDAGRunJSONRequestBody{},
	).ExpectStatus(http.StatusOK).Send(t)

	var rescheduleBody api.RescheduleDAGRun200JSONResponse
	rescheduleResp.Unmarshal(t, &rescheduleBody)
	require.NotEmpty(t, rescheduleBody.DagRunId)
	require.True(t, rescheduleBody.Queued)

	test.ProcessQueuedInlineRun(t, server, "reschedule_latest_dag")

	require.Eventually(t, func() bool {
		url := fmt.Sprintf("/api/v1/dags/reschedule_latest_dag/dag-runs/%s", rescheduleBody.DagRunId)
		statusResp := server.Client().Get(url).Send(t)
		if statusResp.Response.StatusCode() != http.StatusOK {
			return false
		}

		var dagRunStatus api.GetDAGDAGRunDetails200JSONResponse
		statusResp.Unmarshal(t, &dagRunStatus)
		return dagRunStatus.DagRun.Status == api.Status(core.Succeeded)
	}, dagRunEventuallyTimeout(10*time.Second), 200*time.Millisecond)
}

func TestRescheduleDAGRunFromInlineStartUsesPersistedSnapshot(t *testing.T) {
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Queues.Enabled = true
		cfg.Queues.Config = []config.QueueConfig{
			{Name: "inline_reschedule_start", MaxActiveRuns: 1},
		}
	}))

	runID, dagLocation := test.CreateInlineDAGRunForReschedule(t, server, "inline_reschedule_start", false)
	require.NoFileExists(t, dagLocation)
	assertRescheduleSpecSourceFlag(t, server, "inline_reschedule_start", runID, false)

	rescheduledRunID := rescheduleInlineDAGRun(t, server, "inline_reschedule_start", runID)
	test.ProcessQueuedInlineRun(t, server, "inline_reschedule_start")
	test.AssertInlineRescheduledRunParams(t, server, "inline_reschedule_start", rescheduledRunID)
}

func TestRescheduleDAGRunFromInlineEnqueueUsesPersistedSnapshot(t *testing.T) {
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Queues.Enabled = true
		cfg.Queues.Config = []config.QueueConfig{
			{Name: "inline_reschedule_enqueue", MaxActiveRuns: 1},
		}
	}))

	runID, dagLocation := test.CreateInlineDAGRunForReschedule(t, server, "inline_reschedule_enqueue", true)
	require.NoFileExists(t, dagLocation)
	assertRescheduleSpecSourceFlag(t, server, "inline_reschedule_enqueue", runID, false)

	rescheduledRunID := rescheduleInlineDAGRun(t, server, "inline_reschedule_enqueue", runID)
	test.ProcessQueuedInlineRun(t, server, "inline_reschedule_enqueue")
	test.AssertInlineRescheduledRunParams(t, server, "inline_reschedule_enqueue", rescheduledRunID)
}

func TestRescheduleDAGRunUsesPersistedBaseConfigSnapshot(t *testing.T) {
	dagName := "reschedule_base_snapshot"
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Queues.Enabled = true
		cfg.Queues.Config = []config.QueueConfig{
			{Name: dagName, MaxActiveRuns: 1},
		}
	}))

	require.NoError(t, os.WriteFile(server.Config.Paths.BaseConfig, []byte(`
env:
  BASE_FROM_SNAPSHOT: old
`), 0600))

	dagSpec := `steps:
  - name: main
    run: echo "$BASE_FROM_SNAPSHOT"`

	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: dagName,
		Spec: &dagSpec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	startResp := server.Client().Post(
		fmt.Sprintf("/api/v1/dags/%s/start", dagName),
		api.ExecuteDAGJSONRequestBody{},
	).ExpectStatus(http.StatusOK).Send(t)

	var startBody api.ExecuteDAG200JSONResponse
	startResp.Unmarshal(t, &startBody)
	require.NotEmpty(t, startBody.DagRunId)

	waitForDAGRunStatus(t, server, dagName, startBody.DagRunId, 10*time.Second, func(status *exec.DAGRunStatus) bool {
		return status.Status == core.Succeeded
	})

	_, originalDAG := test.WaitForAttemptSnapshotWithDAG(t, server, dagName, startBody.DagRunId)
	require.Contains(t, string(originalDAG.BaseConfigData), "BASE_FROM_SNAPSHOT: old")

	require.NoError(t, os.WriteFile(server.Config.Paths.BaseConfig, []byte(`
env:
  BASE_FROM_SNAPSHOT: new
`), 0600))

	rescheduleResp := server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/reschedule", dagName, startBody.DagRunId),
		api.RescheduleDAGRunJSONRequestBody{},
	).ExpectStatus(http.StatusOK).Send(t)

	var rescheduleBody api.RescheduleDAGRun200JSONResponse
	rescheduleResp.Unmarshal(t, &rescheduleBody)
	require.NotEmpty(t, rescheduleBody.DagRunId)
	require.True(t, rescheduleBody.Queued)

	_, rescheduledDAG := test.WaitForAttemptSnapshotWithDAG(t, server, dagName, rescheduleBody.DagRunId)
	assert.Contains(t, string(rescheduledDAG.BaseConfigData), "BASE_FROM_SNAPSHOT: old")
	assert.NotContains(t, string(rescheduledDAG.BaseConfigData), "BASE_FROM_SNAPSHOT: new")
}

func TestRescheduleDAGRunCanUseCurrentDAGFile(t *testing.T) {
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Queues.Enabled = true
		cfg.Queues.Config = []config.QueueConfig{
			{Name: "reschedule_use_current_file", MaxActiveRuns: 1},
		}
	}))

	dagName := "reschedule_use_current_file"
	initialSpec := `queue: reschedule_use_current_file
steps:
  - name: main
    run: echo stored snapshot`

	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: dagName,
		Spec: &initialSpec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	startResp := server.Client().Post(
		fmt.Sprintf("/api/v1/dags/%s/start", dagName),
		api.ExecuteDAGJSONRequestBody{},
	).ExpectStatus(http.StatusOK).Send(t)

	var startBody api.ExecuteDAG200JSONResponse
	startResp.Unmarshal(t, &startBody)
	require.NotEmpty(t, startBody.DagRunId)

	require.Eventually(t, func() bool {
		url := fmt.Sprintf("/api/v1/dags/%s/dag-runs/%s", dagName, startBody.DagRunId)
		statusResp := server.Client().Get(url).Send(t)
		if statusResp.Response.StatusCode() != http.StatusOK {
			return false
		}

		var dagRunStatus api.GetDAGDAGRunDetails200JSONResponse
		statusResp.Unmarshal(t, &dagRunStatus)
		return dagRunStatus.DagRun.Status == api.Status(core.Succeeded)
	}, dagRunEventuallyTimeout(10*time.Second), 200*time.Millisecond)

	currentSpec := `queue: reschedule_use_current_file
steps:
  - name: main
    run: echo current file`
	dagPath := filepath.Join(server.Config.Paths.DAGsDir, dagName+".yaml")
	assertRescheduleSpecSourceFlag(t, server, dagName, startBody.DagRunId, true)
	originalAttempt, originalDAG := test.WaitForAttemptSnapshotWithDAG(t, server, dagName, startBody.DagRunId)
	require.NotNil(t, originalAttempt)
	require.Equal(t, dagPath, originalDAG.SourceFile)
	require.NoError(t, os.WriteFile(dagPath, []byte(currentSpec), 0o600))
	useCurrentDagFile := true

	resp := server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/reschedule", dagName, startBody.DagRunId),
		api.RescheduleDAGRunJSONRequestBody{UseCurrentDagFile: &useCurrentDagFile},
	).ExpectStatus(http.StatusOK).Send(t)

	var body api.RescheduleDAGRun200JSONResponse
	resp.Unmarshal(t, &body)
	require.NotEmpty(t, body.DagRunId)
	require.True(t, body.Queued)

	test.ProcessQueuedInlineRun(t, server, dagName)

	_, dag := test.WaitForAttemptSnapshotWithDAG(t, server, dagName, body.DagRunId)
	require.Contains(t, string(dag.YamlData), "echo current file")
	require.Equal(t, dagPath, dag.SourceFile)

	waitForStoredDAGRunStatus(t, server, dagName, body.DagRunId, 10*time.Second, func(status *exec.DAGRunStatus) bool {
		return status.Status == core.Succeeded
	})
}

func TestRescheduleDAGRunRequiresQueuesEnabled(t *testing.T) {
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Queues.Enabled = false
		cfg.Queues.Config = nil
	}))

	dagSpec := `steps:
  - name: main
    run: "echo reschedule disabled"`

	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: "reschedule_requires_queue_dag",
		Spec: &dagSpec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	startResp := server.Client().Post(
		"/api/v1/dags/reschedule_requires_queue_dag/start",
		api.ExecuteDAGJSONRequestBody{},
	).ExpectStatus(http.StatusOK).Send(t)

	var startBody api.ExecuteDAG200JSONResponse
	startResp.Unmarshal(t, &startBody)
	require.NotEmpty(t, startBody.DagRunId)

	require.Eventually(t, func() bool {
		url := fmt.Sprintf("/api/v1/dags/reschedule_requires_queue_dag/dag-runs/%s", startBody.DagRunId)
		statusResp := server.Client().Get(url).Send(t)
		if statusResp.Response.StatusCode() != http.StatusOK {
			return false
		}

		var dagRunStatus api.GetDAGDAGRunDetails200JSONResponse
		statusResp.Unmarshal(t, &dagRunStatus)
		return dagRunStatus.DagRun.Status == api.Status(core.Succeeded)
	}, dagRunEventuallyTimeout(10*time.Second), 200*time.Millisecond)

	server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/reschedule", "reschedule_requires_queue_dag", startBody.DagRunId),
		api.RescheduleDAGRunJSONRequestBody{},
	).ExpectStatus(http.StatusBadRequest).Send(t)
}

func TestRetryDAGRunQueuesRetryForQueuedDAGs(t *testing.T) {
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Queues.Enabled = true
		cfg.Queues.Config = []config.QueueConfig{
			{Name: "single-retry-queue", MaxActiveRuns: 1},
		}
	}))

	dag := server.DAG(t, `
name: single_retry_queue_dag
queue: single-retry-queue
steps:
  - name: main
    run: echo queued retry
`)

	seedLatestDAGRunStatus(t, server, dag.DAG, "queued-run", core.Failed, seedDAGRunStatusOptions{
		errorText: "queued run failed",
	})

	server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/retry", dag.Name, "queued-run"),
		api.RetryDAGRunJSONRequestBody{DagRunId: "queued-run"},
	).ExpectStatus(http.StatusOK).Send(t)

	attempt, err := server.DAGRunStore.FindAttempt(server.Context, exec.NewDAGRunRef(dag.Name, "queued-run"))
	require.NoError(t, err)

	status, err := attempt.ReadStatus(server.Context)
	require.NoError(t, err)
	require.Equal(t, core.Queued, status.Status)
	require.Equal(t, core.TriggerTypeRetry, status.TriggerType)
}

func TestGetSubDAGRunsIncludesTopLevelDagEnqueueRun(t *testing.T) {
	server := test.SetupServer(t)

	parent := server.DAG(t, `
name: dag_enqueue_parent
steps:
  - name: enqueue-child
    run: echo queued
`)
	child := server.DAG(t, `
name: dag_enqueue_child
steps:
  - name: child
    run: echo child
`)

	parentRunID := "parent-run"
	childRunID := "child-run"
	seedLatestDAGRunStatus(t, server, parent.DAG, parentRunID, core.Succeeded, seedDAGRunStatusOptions{
		subRuns: map[string][]exec.SubDAGRun{
			"enqueue-child": {{
				DAGRunID: childRunID,
				DAGName:  child.Name,
				Params:   "TARGET=async",
			}},
		},
	})
	seedLatestDAGRunStatus(t, server, child.DAG, childRunID, core.Queued, seedDAGRunStatusOptions{})

	resp := server.Client().Get(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/sub-dag-runs", parent.Name, parentRunID),
	).ExpectStatus(http.StatusOK).Send(t)

	var body api.GetSubDAGRuns200JSONResponse
	resp.Unmarshal(t, &body)
	require.Len(t, body.SubRuns, 1)
	require.Equal(t, childRunID, body.SubRuns[0].DagRunId)
	require.Equal(t, api.Status(core.Queued), body.SubRuns[0].Status)
	require.NotNil(t, body.SubRuns[0].DagName)
	require.Equal(t, child.Name, *body.SubRuns[0].DagName)
	require.NotNil(t, body.SubRuns[0].Params)
	require.Equal(t, "TARGET=async", *body.SubRuns[0].Params)
}

func TestRetryDAGRunStartsLocalRetrySubprocess(t *testing.T) {
	server := test.SetupServer(t)

	retryCommand := `
if [ -f "$DAG_RUN_LOG_FILE.marker" ]; then
  echo local retry
else
  touch "$DAG_RUN_LOG_FILE.marker"
  exit 1
fi
`
	if runtime.GOOS == "windows" {
		retryCommand = `
if (Test-Path "$env:DAG_RUN_LOG_FILE.marker") {
  Write-Output "local retry"
} else {
  New-Item -ItemType File -Path "$env:DAG_RUN_LOG_FILE.marker" -Force | Out-Null
  exit 1
}
`
	}

	dagSpec := fmt.Sprintf(`
steps:
  - name: main
    run: |
%s
`, indentCommandBlock(retryCommand, 6))

	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: "single_retry_local_dag",
		Spec: &dagSpec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	startResp := server.Client().Post(
		"/api/v1/dags/single_retry_local_dag/start",
		api.ExecuteDAGJSONRequestBody{},
	).ExpectStatus(http.StatusOK).Send(t)

	var startBody api.ExecuteDAG200JSONResponse
	startResp.Unmarshal(t, &startBody)
	require.NotEmpty(t, startBody.DagRunId)

	require.Eventually(t, func() bool {
		resp := server.Client().Get(
			fmt.Sprintf("/api/v1/dag-runs/%s/%s", "single_retry_local_dag", startBody.DagRunId),
		).Send(t)
		if resp.Response.StatusCode() != http.StatusOK {
			return false
		}

		var details api.GetDAGRunDetails200JSONResponse
		resp.Unmarshal(t, &details)
		return details.DagRunDetails.Status == api.Status(core.Failed)
	}, dagRunEventuallyTimeout(15*time.Second), 200*time.Millisecond)

	server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/retry", "single_retry_local_dag", "latest"),
		api.RetryDAGRunJSONRequestBody{},
	).ExpectStatus(http.StatusOK).Send(t)

	require.Eventually(t, func() bool {
		resp := server.Client().Get(
			fmt.Sprintf("/api/v1/dag-runs/%s/%s", "single_retry_local_dag", "latest"),
		).Send(t)
		if resp.Response.StatusCode() != http.StatusOK {
			return false
		}

		var details api.GetDAGRunDetails200JSONResponse
		resp.Unmarshal(t, &details)
		return details.DagRunDetails.Status == api.Status(core.Succeeded)
	}, dagRunEventuallyTimeout(15*time.Second), 200*time.Millisecond)
}

func TestTerminateDAGRunCancelsFailedAutoRetryPendingRun(t *testing.T) {
	server := test.SetupServer(t)

	dag := server.DAG(t, `
name: cancel_failed_retry_dag
retry_policy:
  limit: 3
  interval_sec: 60
steps:
  - name: main
    run: "echo fail"
`)

	ref := seedLatestDAGRunStatus(
		t,
		server,
		dag.DAG,
		"cancel-failed-run",
		core.Failed,
		seedDAGRunStatusOptions{
			autoRetryCount: 1,
			errorText:      "step failed",
		},
	)

	_ = server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/stop", dag.Name, ref.ID),
		nil,
	).ExpectStatus(http.StatusOK).Send(t)

	persisted := test.ReadRunStatus(server.Context, t, server.DAGRunStore, ref)
	require.Equal(t, core.Aborted, persisted.Status)
	require.Equal(t, 1, persisted.AutoRetryCount)
	require.Equal(t, 3, persisted.AutoRetryLimit)
	require.Equal(t, "step failed", persisted.Error)
	require.Len(t, persisted.Nodes, 1)
	require.Equal(t, core.NodeFailed, persisted.Nodes[0].Status)

	resp := server.Client().Get(fmt.Sprintf("/api/v1/dag-runs/%s/%s", dag.Name, ref.ID)).
		ExpectStatus(http.StatusOK).
		Send(t)

	var body api.GetDAGRunDetails200JSONResponse
	resp.Unmarshal(t, &body)
	require.Equal(t, api.Status(core.Aborted), body.DagRunDetails.Status)
}

func TestTerminateDAGRunRejectsFailedRunWithoutPendingAutoRetry(t *testing.T) {
	server := test.SetupServer(t)

	dag := server.DAG(t, `
name: cancel_failed_retry_exhausted_dag
retry_policy:
  limit: 3
  interval_sec: 60
steps:
  - name: main
    run: "echo fail"
`)

	ref := seedLatestDAGRunStatus(
		t,
		server,
		dag.DAG,
		"cancel-failed-exhausted-run",
		core.Failed,
		seedDAGRunStatusOptions{
			autoRetryCount: 3,
			errorText:      "still failed",
		},
	)

	resp := server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/stop", dag.Name, ref.ID),
		nil,
	).ExpectStatus(http.StatusBadRequest).Send(t)

	var errBody api.Error
	resp.Unmarshal(t, &errBody)
	require.Equal(t, api.ErrorCodeBadRequest, errBody.Code)
	require.Contains(t, errBody.Message, "not pending auto-retry")

	persisted := test.ReadRunStatus(server.Context, t, server.DAGRunStore, ref)
	require.Equal(t, core.Failed, persisted.Status)
	require.Equal(t, 3, persisted.AutoRetryCount)
}

func TestExecuteDAGSync(t *testing.T) {
	server := test.SetupServer(t)

	dagSpec := syncSuccessDagSpec()

	// Create a new DAG
	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: "sync_test_dag",
		Spec: &dagSpec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	// Execute synchronously with timeout
	timeout := dagRunSyncTimeoutSeconds()
	statusCode, responseBody := postJSONWithConservativeTransport(t, server, "/api/v1/dags/sync_test_dag/start-sync", api.ExecuteDAGSyncJSONRequestBody{
		Timeout: timeout,
	})
	require.Equal(t, http.StatusOK, statusCode, "unexpected status code")

	var syncBody api.ExecuteDAGSync200JSONResponse
	require.NoError(t, json.Unmarshal(responseBody, &syncBody))

	// Verify the response contains full DAGRunDetails
	require.NotEmpty(t, syncBody.DagRun.DagRunId)
	require.Equal(t, "sync_test_dag", syncBody.DagRun.Name)
	require.Equal(t, api.Status(core.Succeeded), syncBody.DagRun.Status)
	require.Equal(t, api.StatusLabel("succeeded"), syncBody.DagRun.StatusLabel)
	require.NotNil(t, syncBody.DagRun.Nodes)
	require.Len(t, syncBody.DagRun.Nodes, 1)
	require.Equal(t, "echo-step", syncBody.DagRun.Nodes[0].Step.Name)
}

func TestExecuteDAGSyncTimeout(t *testing.T) {
	server := test.SetupServer(t)
	releaseFile := filepath.Join(t.TempDir(), "sync-timeout.release")
	t.Cleanup(func() {
		_ = os.WriteFile(releaseFile, []byte("ok"), 0600)
	})

	// Create a DAG with a step that takes longer than the timeout
	dagSpec := fmt.Sprintf(`steps:
  - name: slow-step
    run: |
%s`, indentCommandBlock(holdUntilFileExistsCommand(releaseFile), 6))

	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: "sync_timeout_dag",
		Spec: &dagSpec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	// Execute synchronously with a very short timeout (1 second)
	timeout := 1
	syncResp := server.Client().Post("/api/v1/dags/sync_timeout_dag/start-sync", api.ExecuteDAGSyncJSONRequestBody{
		Timeout: timeout,
	}).ExpectStatus(http.StatusRequestTimeout).Send(t)

	var errBody api.TimeoutError
	syncResp.Unmarshal(t, &errBody)
	require.Equal(t, api.ErrorCodeTimeout, errBody.Code)
	require.Contains(t, errBody.Message, "timeout")
	require.Contains(t, errBody.Message, "DAG run continues in background")
	require.NotEmpty(t, errBody.DagRunId, "408 response should include dagRunId for tracking")

	require.NoError(t, os.WriteFile(releaseFile, []byte("ok"), 0600))
	waitForDAGRunStatus(t, server, "sync_timeout_dag", errBody.DagRunId, 15*time.Second, func(status *exec.DAGRunStatus) bool {
		return status.Status == core.Succeeded
	})
}

func TestExecuteDAGSyncWithWaitingStatus(t *testing.T) {
	server := test.SetupServer(t)

	// Create a DAG with approval step that will wait for approval
	dagSpec := fmt.Sprintf(`steps:
  - name: wait-step
    run: %q
    approval:
      prompt: "Approve this"`, "exit 0")

	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: "sync_waiting_dag",
		Spec: &dagSpec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	// Execute synchronously - should return when DAG reaches waiting status
	timeout := 30
	syncResp := server.Client().Post("/api/v1/dags/sync_waiting_dag/start-sync", api.ExecuteDAGSyncJSONRequestBody{
		Timeout: timeout,
	}).ExpectStatus(http.StatusOK).Send(t)

	var syncBody api.ExecuteDAGSync200JSONResponse
	syncResp.Unmarshal(t, &syncBody)

	// Should return with waiting status (not timeout)
	require.NotEmpty(t, syncBody.DagRun.DagRunId)
	require.Equal(t, api.Status(core.Waiting), syncBody.DagRun.Status)
	require.Equal(t, api.StatusLabel("waiting"), syncBody.DagRun.StatusLabel)
}

type seedDAGRunStatusOptions struct {
	autoRetryCount int
	errorText      string
	parentRef      exec.DAGRunRef
	paramsList     []string
	nodeStatuses   map[string]core.NodeStatus
	subRuns        map[string][]exec.SubDAGRun
}

func seedLatestDAGRunStatus(
	t *testing.T,
	server test.Server,
	dag *core.DAG,
	dagRunID string,
	status core.Status,
	opts seedDAGRunStatusOptions,
) exec.DAGRunRef {
	t.Helper()

	attempt, err := server.DAGRunStore.CreateAttempt(
		server.Context,
		dag,
		time.Now().Add(-2*time.Minute),
		dagRunID,
		exec.NewDAGRunAttemptOptions{},
	)
	require.NoError(t, err)

	ref := exec.NewDAGRunRef(dag.Name, dagRunID)
	statusOptions := []transform.StatusOption{
		transform.WithAttemptID(attempt.ID()),
		transform.WithHierarchyRefs(ref, opts.parentRef),
		transform.WithAutoRetryCount(opts.autoRetryCount),
		transform.WithError(opts.errorText),
	}
	if !status.IsActive() && status != core.NotStarted {
		statusOptions = append(statusOptions, transform.WithFinishedAt(time.Now().Add(-time.Minute)))
	}

	dagRunStatus := transform.NewStatusBuilder(dag).Create(
		dagRunID,
		status,
		0,
		time.Now().Add(-2*time.Minute),
		statusOptions...,
	)
	if len(opts.paramsList) > 0 {
		dagRunStatus.ParamsList = append([]string(nil), opts.paramsList...)
	}
	if len(dagRunStatus.Nodes) > 0 && status == core.Failed {
		dagRunStatus.Nodes[0].Status = core.NodeFailed
		dagRunStatus.Nodes[0].FinishedAt = exec.FormatTime(time.Now().Add(-time.Minute))
		dagRunStatus.Nodes[0].Error = opts.errorText
	}
	for stepName, nodeStatus := range opts.nodeStatuses {
		found := false
		for _, node := range dagRunStatus.Nodes {
			if node.Step.Name != stepName {
				continue
			}
			node.Status = nodeStatus
			found = true
			break
		}
		require.Truef(t, found, "seeded DAG-run status step %q not found", stepName)
	}
	for stepName, subRuns := range opts.subRuns {
		found := false
		for _, node := range dagRunStatus.Nodes {
			if node.Step.Name != stepName {
				continue
			}
			node.SubRuns = append([]exec.SubDAGRun(nil), subRuns...)
			found = true
			break
		}
		require.Truef(t, found, "seeded DAG-run status step %q not found", stepName)
	}

	require.NoError(t, attempt.Open(server.Context))
	require.NoError(t, attempt.Write(server.Context, dagRunStatus))
	require.NoError(t, attempt.Close(server.Context))

	return ref
}

func TestUpdateDAGRunStepStatusRecomputesAggregateStatus(t *testing.T) {
	server := test.SetupServer(t)

	dag := &core.DAG{
		Name: "manual_step_status_aggregate",
		Steps: []core.Step{
			{Name: "step1"},
			{Name: "step2"},
		},
	}
	const dagRunID = "manual-step-status-run"
	seedLatestDAGRunStatus(
		t,
		server,
		dag,
		dagRunID,
		core.Succeeded,
		seedDAGRunStatusOptions{
			nodeStatuses: map[string]core.NodeStatus{
				"step1": core.NodeSucceeded,
				"step2": core.NodeSucceeded,
			},
		},
	)

	server.Client().Patch(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/steps/%s/status", dag.Name, dagRunID, "step1"),
		api.UpdateDAGRunStepStatusJSONRequestBody{Status: api.NodeStatusFailed},
	).ExpectStatus(http.StatusOK).Send(t)

	status := waitForStoredDAGRunStatus(
		t,
		server,
		dag.Name,
		dagRunID,
		5*time.Second,
		func(status *exec.DAGRunStatus) bool {
			return status.Status == core.Failed &&
				hasNodeWithStatus(status, "step1", core.NodeFailed)
		},
	)
	require.Equal(t, core.Failed, status.Status)
}

func TestDeleteDAGRun(t *testing.T) {
	server := test.SetupServer(t)
	dag := &core.DAG{Name: "delete_run_dag"}
	ref := seedLatestDAGRunStatus(
		t,
		server,
		dag,
		"delete-run-1",
		core.Succeeded,
		seedDAGRunStatusOptions{},
	)

	server.Client().Delete(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s", ref.Name, ref.ID),
	).ExpectStatus(http.StatusNoContent).Send(t)

	_, err := server.DAGRunStore.FindAttempt(server.Context, ref)
	require.ErrorIs(t, err, exec.ErrDAGRunIDNotFound)

	server.Client().Get(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s", ref.Name, ref.ID),
	).ExpectStatus(http.StatusNotFound).Send(t)
}

func TestDeleteDAGRunRejectsLatestAlias(t *testing.T) {
	server := test.SetupServer(t)

	resp := server.Client().Delete(
		"/api/v1/dag-runs/delete_run_dag/latest",
	).ExpectStatus(http.StatusBadRequest).Send(t)

	var body api.Error
	resp.Unmarshal(t, &body)
	require.Equal(t, api.ErrorCodeBadRequest, body.Code)
	require.Contains(t, body.Message, "latest cannot be used")
}

func TestDeleteDAGRunRejectsActiveRun(t *testing.T) {
	server := test.SetupServer(t)
	dag := &core.DAG{Name: "delete_active_run_dag"}
	ref := seedLatestDAGRunStatus(
		t,
		server,
		dag,
		"active-run-1",
		core.Running,
		seedDAGRunStatusOptions{},
	)

	resp := server.Client().Delete(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s", ref.Name, ref.ID),
	).ExpectStatus(http.StatusBadRequest).Send(t)

	var body api.Error
	resp.Unmarshal(t, &body)
	require.Equal(t, api.ErrorCodeBadRequest, body.Code)
	require.Contains(t, body.Message, "stop or dequeue it before deleting")

	_, err := server.DAGRunStore.FindAttempt(server.Context, ref)
	require.NoError(t, err)
}

func indentCommandBlock(command string, spaces int) string {
	trimmed := strings.Trim(command, "\n")
	if trimmed == "" {
		return ""
	}

	prefix := strings.Repeat(" ", spaces)
	lines := strings.Split(trimmed, "\n")
	return prefix + strings.Join(lines, "\n"+prefix)
}

func rescheduleInlineDAGRun(t *testing.T, server test.Server, dagName, dagRunID string) string {
	t.Helper()

	resp := server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/reschedule", dagName, dagRunID),
		api.RescheduleDAGRunJSONRequestBody{},
	).ExpectStatus(http.StatusOK).Send(t)

	var body api.RescheduleDAGRun200JSONResponse
	resp.Unmarshal(t, &body)
	require.NotEmpty(t, body.DagRunId)
	require.True(t, body.Queued)
	return body.DagRunId
}

func assertRescheduleSpecSourceFlag(t *testing.T, server test.Server, dagName, dagRunID string, want bool) {
	t.Helper()

	resp := server.Client().Get(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s", dagName, dagRunID),
	).ExpectStatus(http.StatusOK).Send(t)

	var body api.GetDAGRunDetails200JSONResponse
	resp.Unmarshal(t, &body)
	got := body.DagRunDetails.SpecFromFile != nil && *body.DagRunDetails.SpecFromFile
	require.Equal(t, want, got)
}

func TestExecuteDAGSyncSingleton(t *testing.T) {
	server := test.SetupServer(t)
	releaseFile := filepath.Join(t.TempDir(), "sync-singleton.release")
	t.Cleanup(func() {
		_ = os.WriteFile(releaseFile, []byte("ok"), 0600)
	})

	// Create a DAG with a slow step
	dagSpec := fmt.Sprintf(`steps:
  - name: slow-step
    run: |
%s`, indentCommandBlock(holdUntilFileExistsCommand(releaseFile), 6))

	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: "sync_singleton_dag",
		Spec: &dagSpec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	// Start the DAG asynchronously first
	startResp := server.Client().Post("/api/v1/dags/sync_singleton_dag/start", api.ExecuteDAGJSONRequestBody{}).
		ExpectStatus(http.StatusOK).Send(t)

	var startBody api.ExecuteDAG200JSONResponse
	startResp.Unmarshal(t, &startBody)
	require.NotEmpty(t, startBody.DagRunId)

	// Try to start another sync execution with singleton mode - should fail with 409
	singleton := true
	timeout := 30
	_ = server.Client().Post("/api/v1/dags/sync_singleton_dag/start-sync", api.ExecuteDAGSyncJSONRequestBody{
		Timeout:   timeout,
		Singleton: &singleton,
	}).ExpectStatus(http.StatusConflict).Send(t)

	require.NoError(t, os.WriteFile(releaseFile, []byte("ok"), 0600))
	waitForDAGRunStatus(t, server, "sync_singleton_dag", startBody.DagRunId, 15*time.Second, func(status *exec.DAGRunStatus) bool {
		return status.Status == core.Succeeded
	})
}

func TestListDAGRunsFilterByLabels(t *testing.T) {
	server := test.SetupServer(t)

	// Create DAGs with different labels
	dagSpecProd := `labels:
  - prod
  - critical
steps:
  - name: main
    run: "echo prod-critical"`

	dagSpecDev := `labels:
  - dev
  - critical
steps:
  - name: main
    run: "echo dev-critical"`

	dagSpecTest := `labels:
  - test
steps:
  - name: main
    run: "echo test-only"`

	// Create the DAGs
	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: "label_filter_prod",
		Spec: &dagSpecProd,
	}).ExpectStatus(http.StatusCreated).Send(t)

	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: "label_filter_dev",
		Spec: &dagSpecDev,
	}).ExpectStatus(http.StatusCreated).Send(t)

	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: "label_filter_test",
		Spec: &dagSpecTest,
	}).ExpectStatus(http.StatusCreated).Send(t)

	// Start DAG runs for each
	var prodRunId, devRunId, testRunId string

	startResp := server.Client().Post("/api/v1/dags/label_filter_prod/start", api.ExecuteDAGJSONRequestBody{}).
		ExpectStatus(http.StatusOK).Send(t)
	var startBody api.ExecuteDAG200JSONResponse
	startResp.Unmarshal(t, &startBody)
	prodRunId = startBody.DagRunId

	startResp = server.Client().Post("/api/v1/dags/label_filter_dev/start", api.ExecuteDAGJSONRequestBody{}).
		ExpectStatus(http.StatusOK).Send(t)
	startResp.Unmarshal(t, &startBody)
	devRunId = startBody.DagRunId

	startResp = server.Client().Post("/api/v1/dags/label_filter_test/start", api.ExecuteDAGJSONRequestBody{}).
		ExpectStatus(http.StatusOK).Send(t)
	startResp.Unmarshal(t, &startBody)
	testRunId = startBody.DagRunId

	// Wait for all runs to complete
	for _, pair := range []struct {
		name  string
		runId string
	}{
		{"label_filter_prod", prodRunId},
		{"label_filter_dev", devRunId},
		{"label_filter_test", testRunId},
	} {
		require.Eventually(t, func() bool {
			url := fmt.Sprintf("/api/v1/dags/%s/dag-runs/%s", pair.name, pair.runId)
			statusResp := server.Client().Get(url).Send(t)
			if statusResp.Response.StatusCode() != http.StatusOK {
				return false
			}
			var dagRunStatus api.GetDAGDAGRunDetails200JSONResponse
			statusResp.Unmarshal(t, &dagRunStatus)
			return dagRunStatus.DagRun.Status == api.Status(core.Succeeded)
		}, dagRunEventuallyTimeout(10*time.Second), 200*time.Millisecond)
	}

	fetchNamesByLabels := func(labels string) map[string]bool {
		listResp := server.Client().Get("/api/v1/dag-runs?labels=" + labels).
			ExpectStatus(http.StatusOK).Send(t)
		var listBody api.ListDAGRuns200JSONResponse
		listResp.Unmarshal(t, &listBody)

		names := make(map[string]bool, len(listBody.DagRuns))
		for _, run := range listBody.DagRuns {
			names[run.Name] = true
		}
		return names
	}

	requireFilterEventually := func(labels string, wantPresent, wantAbsent []string) {
		require.Eventually(t, func() bool {
			names := fetchNamesByLabels(labels)
			for _, name := range wantPresent {
				if !names[name] {
					return false
				}
			}
			for _, name := range wantAbsent {
				if names[name] {
					return false
				}
			}
			return true
		}, dagRunEventuallyTimeout(5*time.Second), 200*time.Millisecond)
	}

	requireFilterEventually("critical", []string{"label_filter_prod", "label_filter_dev"}, []string{"label_filter_test"})
	requireFilterEventually("prod,critical", []string{"label_filter_prod"}, []string{"label_filter_dev", "label_filter_test"})
	requireFilterEventually("nonexistent", nil, []string{"label_filter_prod", "label_filter_dev", "label_filter_test"})
	requireFilterEventually("test", []string{"label_filter_test"}, []string{"label_filter_prod", "label_filter_dev"})
	requireFilterEventually("CRITICAL", []string{"label_filter_prod", "label_filter_dev"}, []string{"label_filter_test"})
}

func TestListDAGRunsFilterByPartialName(t *testing.T) {
	server := test.SetupServer(t)

	spec := `steps:
  - name: main
    run: "echo search"`

	for idx, dagName := range []string{
		"test-params-flag",
		"other-dag",
		"alpha-test-case",
	} {
		dag := server.DAG(t, fmt.Sprintf("name: %s\n%s", dagName, spec))
		seedLatestDAGRunStatus(
			t,
			server,
			dag.DAG,
			fmt.Sprintf("search-run-%d", idx),
			core.Succeeded,
			seedDAGRunStatusOptions{},
		)
	}

	resp := server.Client().Get("/api/v1/dag-runs?name=test").
		ExpectStatus(http.StatusOK).Send(t)

	var body api.ListDAGRuns200JSONResponse
	resp.Unmarshal(t, &body)

	names := make(map[string]bool)
	for _, run := range body.DagRuns {
		names[run.Name] = true
	}

	require.True(t, names["test-params-flag"])
	require.True(t, names["alpha-test-case"])
	require.False(t, names["other-dag"])
}

func TestListDAGRunsByNameRemainsExact(t *testing.T) {
	server := test.SetupServer(t)

	spec := `steps:
  - name: main
    run: "echo search"`

	for idx, dagName := range []string{
		"test-params-flag",
		"alpha-test-case",
	} {
		dag := server.DAG(t, fmt.Sprintf("name: %s\n%s", dagName, spec))
		seedLatestDAGRunStatus(
			t,
			server,
			dag.DAG,
			fmt.Sprintf("exact-run-%d", idx),
			core.Succeeded,
			seedDAGRunStatusOptions{},
		)
	}

	resp := server.Client().Get("/api/v1/dag-runs/test-params-flag").
		ExpectStatus(http.StatusOK).Send(t)

	var body api.ListDAGRunsByName200JSONResponse
	resp.Unmarshal(t, &body)

	require.NotEmpty(t, body.DagRuns)
	for _, run := range body.DagRuns {
		require.Equal(t, "test-params-flag", run.Name)
	}
}
