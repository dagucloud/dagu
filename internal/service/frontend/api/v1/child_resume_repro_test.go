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
}

func readChildResumeMarker(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	return strings.ReplaceAll(string(contents), "\r\n", "\n")
}
