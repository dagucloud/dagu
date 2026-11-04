// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

// A checkpoint resume must preserve a partial ancestor and completed manual
// gates through the real API and a subprocess built from the current source.
func TestChildResumePartial(t *testing.T) {
	server := test.SetupServer(t)
	markerDir := t.TempDir()
	allowBPath := filepath.Join(markerDir, "allow-b.txt")
	foreachOutputPath := filepath.Join(markerDir, "foreach-${foreach.item}.out")
	appendMarker := func(path, value string) string {
		return test.ForOS(
			fmt.Sprintf("printf '%%s\\n' %s >> %s", test.PosixQuote(value), test.PosixQuote(path)),
			fmt.Sprintf("[System.IO.File]::AppendAllText(%s, \"%s`n\")", test.PowerShellQuote(path), value),
		)
	}
	bodyCommand := test.ForOS(
		fmt.Sprintf("printf 'run\\n' >> %s\nif [ \"${foreach.item}\" = \"b\" ] && [ ! -f %s ]; then\n  exit 1\nfi", test.PosixQuote(foreachOutputPath), test.PosixQuote(allowBPath)),
		fmt.Sprintf("[System.IO.File]::AppendAllText(%s, \"run`n\")\nif (%s -eq 'b' -and -not (Test-Path %s)) {\n  exit 1\n}", test.PowerShellQuote(foreachOutputPath), test.PowerShellQuote("${foreach.item}"), test.PowerShellQuote(allowBPath)),
	)

	dagName := "child_resume_partial"
	dagSpec := fmt.Sprintf(`type: graph
steps:
  - name: call-child
    action: dag.run
    with:
      dag: child_resume_partial_child
  - name: parent-long
    run: "exit 0"
    approval:
      prompt: "Approve parent"

---
name: child_resume_partial_child
type: graph
steps:
  - name: each
    foreach:
      items: ["a", "b"]
      key: ${foreach.item}
      steps:
        - name: body
          run: |
%s
  - name: gate
    run: |
%s
    depends: [each]
    approval:
      prompt: "Approve gate"
  - name: other
    run: |
%s
    approval:
      prompt: "Approve other"
  - name: after
    depends: [gate]
    run: |
%s
`, indentCommandBlock(bodyCommand, 12), indentCommandBlock(appendMarker(filepath.Join(markerDir, "gate.out"), "gate"), 6), indentCommandBlock(appendMarker(filepath.Join(markerDir, "other.out"), "other"), 6), indentCommandBlock(appendMarker(filepath.Join(markerDir, "after.out"), "after"), 6))

	server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: dagName,
		Spec: &dagSpec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	startResp := server.Client().Post("/api/v1/dags/"+dagName+"/start", api.ExecuteDAGJSONRequestBody{}).
		ExpectStatus(http.StatusOK).Send(t)
	var startBody api.ExecuteDAG200JSONResponse
	startResp.Unmarshal(t, &startBody)

	var subDAGRunID string
	waitForStoredDAGRunStatus(t, server, dagName, startBody.DagRunId, 10*time.Second, func(status *ir.DAGRunStatus) bool {
		callChild := findNodeByName(status, "call-child")
		if callChild == nil || len(callChild.SubRuns) != 1 {
			return false
		}
		subDAGRunID = callChild.SubRuns[0].DAGRunID
		return status.Status == ir.Waiting && callChild.Status == ir.NodeWaiting &&
			hasNodeWithStatus(status, "parent-long", ir.NodeWaiting)
	})
	rootRef := ir.NewDAGRunRef(dagName, startBody.DagRunId)
	waitForStoredSubDAGRunStatus(t, server, rootRef, subDAGRunID, 10*time.Second, func(status *ir.DAGRunStatus) bool {
		return status.Status == ir.Waiting &&
			hasNodeWithStatus(status, "each", ir.NodePartiallySucceeded) &&
			hasNodeWithStatus(status, "gate", ir.NodeWaiting) &&
			hasNodeWithStatus(status, "other", ir.NodeWaiting)
	})

	approve := func(stepName string) api.ApproveSubDAGRunStep200JSONResponse {
		resp := server.Client().Post(
			fmt.Sprintf("/api/v1/dag-runs/%s/%s/sub-dag-runs/%s/steps/%s/approve", dagName, startBody.DagRunId, subDAGRunID, stepName),
			api.ApproveStepRequest{},
		).ExpectStatus(http.StatusOK).Send(t)
		var body api.ApproveSubDAGRunStep200JSONResponse
		resp.Unmarshal(t, &body)
		return body
	}

	require.False(t, approve("gate").Resumed)
	waitForStoredSubDAGRunStatus(t, server, rootRef, subDAGRunID, 10*time.Second, func(status *ir.DAGRunStatus) bool {
		return status.Status == ir.Waiting && hasNodeWithStatus(status, "each", ir.NodePartiallySucceeded) &&
			hasNodeWithStatus(status, "gate", ir.NodeSucceeded) &&
			hasNodeWithStatus(status, "other", ir.NodeWaiting) &&
			hasNodeWithStatus(status, "after", ir.NodeNotStarted)
	})
	require.Equal(t, "run\n", readChildResumeMarker(t, filepath.Join(markerDir, "foreach-a.out")))
	require.Equal(t, "run\n", readChildResumeMarker(t, filepath.Join(markerDir, "foreach-b.out")))
	require.Equal(t, "gate\n", readChildResumeMarker(t, filepath.Join(markerDir, "gate.out")))

	require.True(t, approve("other").Resumed)
	var lastChild *ir.DAGRunStatus
	defer func() {
		if !t.Failed() || lastChild == nil {
			return
		}
		t.Logf("last child status=%s resume=%t", lastChild.Status, lastChild.Resume)
		for _, node := range lastChild.Nodes {
			t.Logf("last child node %s=%s", node.Step.Name, node.Status)
		}
		for _, item := range []string{"a", "b"} {
			data, err := os.ReadFile(filepath.Join(markerDir, "foreach-"+item+".out"))
			t.Logf("foreach %s side effects=%q error=%v", item, data, err)
		}
	}()
	terminal := waitForStoredSubDAGRunStatus(t, server, rootRef, subDAGRunID, 10*time.Second, func(status *ir.DAGRunStatus) bool {
		lastChild = status
		return status.Status == ir.PartiallySucceeded
	})
	require.Equal(t, ir.NodeSucceeded, requireNodeByName(t, terminal, "after").Status)
	require.Equal(t, "run\n", readChildResumeMarker(t, filepath.Join(markerDir, "foreach-a.out")))
	require.Equal(t, "run\n", readChildResumeMarker(t, filepath.Join(markerDir, "foreach-b.out")))
	require.Equal(t, "gate\n", readChildResumeMarker(t, filepath.Join(markerDir, "gate.out")))
	require.Equal(t, "other\n", readChildResumeMarker(t, filepath.Join(markerDir, "other.out")))
	require.Equal(t, "after\n", readChildResumeMarker(t, filepath.Join(markerDir, "after.out")))

	// The root keeps waiting on its own gate, while its call step takes the
	// finished child's result.
	waitForStoredDAGRunStatus(t, server, dagName, startBody.DagRunId, 20*time.Second, func(status *ir.DAGRunStatus) bool {
		return status.Status == ir.Waiting &&
			hasNodeWithStatus(status, "call-child", ir.NodePartiallySucceeded) &&
			hasNodeWithStatus(status, "parent-long", ir.NodeWaiting)
	})
}

// Approving the last gate of a sub-run must also finish the run that called
// it, including the steps after the call step.
func TestChildApprovalResumesRoot(t *testing.T) {
	server := test.SetupServer(t)
	root := startChildResumeDAG(t, server, "child_approval_root", `type: graph
steps:
  - name: call-child
    action: dag.run
    with:
      dag: child_approval_root_child
  - name: after-call
    depends: [call-child]
    run: "exit 0"

---
name: child_approval_root_child
type: graph
steps:
  - name: gate
    run: "exit 0"
    approval:
      prompt: "Approve gate"
  - name: after-gate
    depends: [gate]
    run: "exit 0"
`)
	childID := waitForRootCallWaiting(t, server, root, "call-child")
	waitForSubRunGateWaiting(t, server, root, childID, "gate")

	require.True(t, approveSubRunStep(t, server, root, childID, "gate").Resumed)

	child := waitForStoredSubDAGRunStatus(t, server, root, childID, 20*time.Second, func(status *ir.DAGRunStatus) bool {
		return !status.Status.IsActive()
	})
	require.Equal(t, ir.Succeeded, child.Status)
	require.Equal(t, ir.NodeSucceeded, requireNodeByName(t, child, "after-gate").Status)
	rootStatus := waitForStoredDAGRunStatus(t, server, root.Name, root.ID, 20*time.Second, func(status *ir.DAGRunStatus) bool {
		return !status.Status.IsActive()
	})
	require.Equal(t, ir.Succeeded, rootStatus.Status)
	require.Equal(t, ir.NodeSucceeded, requireNodeByName(t, rootStatus, "after-call").Status)
}

// A gate two calls deep resumes every waiting ancestor once it is approved.
func TestNestedApprovalResumesAncestors(t *testing.T) {
	server := test.SetupServer(t)
	root := startChildResumeDAG(t, server, "nested_approval_root", nestedApprovalSpec("nested_approval"))
	midID := waitForRootCallWaiting(t, server, root, "call-report")
	leafID := waitForSubRunCallWaiting(t, server, root, midID, "call-notify")
	waitForSubRunGateWaiting(t, server, root, leafID, "send")

	require.True(t, approveSubRunStep(t, server, root, leafID, "send").Resumed)

	leaf := waitForStoredSubDAGRunStatus(t, server, root, leafID, 20*time.Second, func(status *ir.DAGRunStatus) bool {
		return !status.Status.IsActive()
	})
	require.Equal(t, ir.Succeeded, leaf.Status)
	mid := waitForStoredSubDAGRunStatus(t, server, root, midID, 20*time.Second, func(status *ir.DAGRunStatus) bool {
		return !status.Status.IsActive()
	})
	require.Equal(t, ir.Succeeded, mid.Status)
	require.Equal(t, ir.NodeSucceeded, requireNodeByName(t, mid, "after-notify").Status)
	rootStatus := waitForStoredDAGRunStatus(t, server, root.Name, root.ID, 20*time.Second, func(status *ir.DAGRunStatus) bool {
		return !status.Status.IsActive()
	})
	require.Equal(t, ir.Succeeded, rootStatus.Status)
	require.Equal(t, ir.NodeSucceeded, requireNodeByName(t, rootStatus, "after-report").Status)
}

// Rejecting a gate two calls deep ends every waiting ancestor instead of
// leaving it waiting on a sub-run that will not continue.
func TestNestedRejectionFailsAncestors(t *testing.T) {
	server := test.SetupServer(t)
	root := startChildResumeDAG(t, server, "nested_rejection_root", nestedApprovalSpec("nested_rejection"))
	midID := waitForRootCallWaiting(t, server, root, "call-report")
	leafID := waitForSubRunCallWaiting(t, server, root, midID, "call-notify")
	waitForSubRunGateWaiting(t, server, root, leafID, "send")

	server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/sub-dag-runs/%s/steps/send/reject", root.Name, root.ID, leafID),
		api.RejectStepRequest{},
	).ExpectStatus(http.StatusOK).Send(t)

	leaf := waitForStoredSubDAGRunStatus(t, server, root, leafID, 20*time.Second, func(status *ir.DAGRunStatus) bool {
		return !status.Status.IsActive()
	})
	require.Equal(t, ir.Rejected, leaf.Status)
	mid := waitForStoredSubDAGRunStatus(t, server, root, midID, 20*time.Second, func(status *ir.DAGRunStatus) bool {
		return !status.Status.IsActive()
	})
	require.Equal(t, ir.Failed, mid.Status)
	require.Equal(t, ir.NodeFailed, requireNodeByName(t, mid, "call-notify").Status)
	rootStatus := waitForStoredDAGRunStatus(t, server, root.Name, root.ID, 20*time.Second, func(status *ir.DAGRunStatus) bool {
		return !status.Status.IsActive()
	})
	require.Equal(t, ir.Failed, rootStatus.Status)
	require.Equal(t, ir.NodeFailed, requireNodeByName(t, rootStatus, "call-report").Status)
	require.NotEqual(t, ir.NodeSucceeded, requireNodeByName(t, rootStatus, "after-report").Status)
}

// A parallel call step keeps waiting while any of its sub-runs waits, and
// finishes once the last one is approved.
func TestParallelChildApprovalWaitsForSiblings(t *testing.T) {
	server := test.SetupServer(t)
	root := startChildResumeDAG(t, server, "parallel_approval_root", `type: graph
steps:
  - name: fan-out
    action: dag.run
    with:
      dag: parallel_approval_child
    parallel:
      items: [one, two]
  - name: after-fan-out
    depends: [fan-out]
    run: "exit 0"

---
name: parallel_approval_child
type: graph
steps:
  - name: gate
    run: "exit 0"
    approval:
      prompt: "Approve gate"
`)
	var childIDs []string
	waitForStoredDAGRunStatus(t, server, root.Name, root.ID, 20*time.Second, func(status *ir.DAGRunStatus) bool {
		node := findNodeByName(status, "fan-out")
		if status.Status != ir.Waiting || node == nil || node.Status != ir.NodeWaiting || len(node.SubRuns) != 2 {
			return false
		}
		childIDs = []string{node.SubRuns[0].DAGRunID, node.SubRuns[1].DAGRunID}
		return true
	})
	for _, childID := range childIDs {
		waitForSubRunGateWaiting(t, server, root, childID, "gate")
	}

	require.True(t, approveSubRunStep(t, server, root, childIDs[0], "gate").Resumed)
	waitForStoredSubDAGRunStatus(t, server, root, childIDs[0], 20*time.Second, func(status *ir.DAGRunStatus) bool {
		return status.Status == ir.Succeeded
	})
	waitForStoredDAGRunStatus(t, server, root.Name, root.ID, 20*time.Second, func(status *ir.DAGRunStatus) bool {
		return status.Status == ir.Waiting && hasNodeWithStatus(status, "fan-out", ir.NodeWaiting) &&
			hasNodeWithStatus(status, "after-fan-out", ir.NodeNotStarted)
	})
	waitForSubRunGateWaiting(t, server, root, childIDs[1], "gate")

	require.True(t, approveSubRunStep(t, server, root, childIDs[1], "gate").Resumed)
	rootStatus := waitForStoredDAGRunStatus(t, server, root.Name, root.ID, 20*time.Second, func(status *ir.DAGRunStatus) bool {
		return !status.Status.IsActive()
	})
	require.Equal(t, ir.Succeeded, rootStatus.Status)
	require.Equal(t, ir.NodeSucceeded, requireNodeByName(t, rootStatus, "after-fan-out").Status)
}

// nestedApprovalSpec returns a root that calls a report run, which calls a
// notify run whose send step waits for approval. Each level has a step after
// its call step.
func nestedApprovalSpec(prefix string) string {
	return fmt.Sprintf(`type: graph
steps:
  - name: call-report
    action: dag.run
    with:
      dag: %[1]s_report
  - name: after-report
    depends: [call-report]
    run: "exit 0"

---
name: %[1]s_report
type: graph
steps:
  - name: call-notify
    action: dag.run
    with:
      dag: %[1]s_notify
  - name: after-notify
    depends: [call-notify]
    run: "exit 0"

---
name: %[1]s_notify
type: graph
steps:
  - name: send
    run: "exit 0"
    approval:
      prompt: "Approve send"
  - name: after-send
    depends: [send]
    run: "exit 0"
`, prefix)
}

func startChildResumeDAG(t *testing.T, server test.Server, dagName, dagSpec string) ir.DAGRunRef {
	t.Helper()
	server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: dagName,
		Spec: &dagSpec,
	}).ExpectStatus(http.StatusCreated).Send(t)
	resp := server.Client().Post("/api/v1/dags/"+dagName+"/start", api.ExecuteDAGJSONRequestBody{}).
		ExpectStatus(http.StatusOK).Send(t)
	var body api.ExecuteDAG200JSONResponse
	resp.Unmarshal(t, &body)
	require.NotEmpty(t, body.DagRunId)
	return ir.NewDAGRunRef(dagName, body.DagRunId)
}

// waitForRootCallWaiting waits until the root waits on stepName and returns
// the sub-run that step called.
func waitForRootCallWaiting(t *testing.T, server test.Server, root ir.DAGRunRef, stepName string) string {
	t.Helper()
	var subRunID string
	waitForStoredDAGRunStatus(t, server, root.Name, root.ID, 20*time.Second, func(status *ir.DAGRunStatus) bool {
		subRunID = waitingCallSubRun(status, stepName)
		return subRunID != ""
	})
	return subRunID
}

// waitForSubRunCallWaiting waits until a sub-run waits on stepName and
// returns the sub-run that step called.
func waitForSubRunCallWaiting(t *testing.T, server test.Server, root ir.DAGRunRef, parentID, stepName string) string {
	t.Helper()
	var subRunID string
	waitForStoredSubDAGRunStatus(t, server, root, parentID, 20*time.Second, func(status *ir.DAGRunStatus) bool {
		subRunID = waitingCallSubRun(status, stepName)
		return subRunID != ""
	})
	return subRunID
}

func waitingCallSubRun(status *ir.DAGRunStatus, stepName string) string {
	node := findNodeByName(status, stepName)
	if status.Status != ir.Waiting || node == nil || node.Status != ir.NodeWaiting || len(node.SubRuns) != 1 {
		return ""
	}
	return node.SubRuns[0].DAGRunID
}

func waitForSubRunGateWaiting(t *testing.T, server test.Server, root ir.DAGRunRef, subRunID, stepName string) {
	t.Helper()
	waitForStoredSubDAGRunStatus(t, server, root, subRunID, 20*time.Second, func(status *ir.DAGRunStatus) bool {
		return status.Status == ir.Waiting && hasNodeWithStatus(status, stepName, ir.NodeWaiting)
	})
}

func approveSubRunStep(t *testing.T, server test.Server, root ir.DAGRunRef, subRunID, stepName string) api.ApproveSubDAGRunStep200JSONResponse {
	t.Helper()
	resp := server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/sub-dag-runs/%s/steps/%s/approve", root.Name, root.ID, subRunID, stepName),
		api.ApproveStepRequest{},
	).ExpectStatus(http.StatusOK).Send(t)
	var body api.ApproveSubDAGRunStep200JSONResponse
	resp.Unmarshal(t, &body)
	return body
}

func readChildResumeMarker(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	return strings.ReplaceAll(string(contents), "\r\n", "\n")
}
