// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/logpath"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/noderuns"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

// seedNodeRuns seeds a root run and its sub-run, each with two steps with
// nodes and the records their executor writes:
//
//	node    deploy        clean
//	web01   succeeded     succeeded
//	web02   disabled      removed
//	web03   failed        not_started
//	web04   running       -
//
// The sub-run is bound to web01. Both runs give the deploy step deployStatus.
func seedNodeRuns(t *testing.T, deployStatus ir.NodeStatus) test.Server {
	t.Helper()
	server := test.SetupServer(t)
	logDir := t.TempDir()
	deployLog := filepath.Join(logDir, "deploy.out")
	cleanLog := filepath.Join(logDir, "clean.out")

	done := &ir.StepProgress{Done: 2, Total: 2}
	deploy := []ir.NodeRun{
		{Node: "web01", Status: ir.NodeRunSucceeded, DAGName: "deploy-one", DAGRunID: "d1", Steps: done},
		{Node: "web02", Status: ir.NodeRunDisabled},
		{Node: "web03", Status: ir.NodeRunFailed, DAGName: "deploy-one", DAGRunID: "d3", Steps: &ir.StepProgress{Done: 1, Total: 2}, FailedStep: "restart"},
		{Node: "web04", Status: ir.NodeRunRunning, DAGName: "deploy-one", DAGRunID: "d4"},
	}
	clean := []ir.NodeRun{
		{Node: "web01", Status: ir.NodeRunSucceeded, DAGName: "node-runs-api", DAGRunID: "c1", Steps: done},
		{Node: "web02", Status: ir.NodeRunRemoved, DAGName: "node-runs-api", DAGRunID: "c2"},
		{Node: "web03", Status: ir.NodeRunNotStarted, DAGName: "node-runs-api", DAGRunID: "c3"},
	}
	for step, runs := range map[string][]ir.NodeRun{"deploy": deploy, "clean": clean} {
		_, err := noderuns.Write(logpath.NodeRunsDir(logDir, step), runs)
		require.NoError(t, err)
	}

	nodes := &ir.NodeTargets{Selector: "web", MaxConcurrent: 1}
	dag := &ir.DAG{Name: "node-runs-api", Steps: []ir.Step{
		{Name: "setup"},
		{Name: "deploy", Nodes: nodes},
		{Name: "clean", Nodes: nodes},
	}}
	root := ir.NewDAGRunRef(dag.Name, "root")
	for _, runID := range []string{root.ID, "child"} {
		opts := persis.DAGRunCreateAttemptOptions{}
		if runID != root.ID {
			opts.RootDAGRun = root
		}
		attempt, err := server.DAGRunRepository.CreateAttempt(server.Context, dag, time.Now(), runID, opts)
		require.NoError(t, err)
		status := ir.InitialStatus(dag)
		status.DAGRunID = runID
		status.AttemptID = attempt.ID()
		status.Root = root
		status.Status = ir.Running
		status.Nodes[0].Stdout = filepath.Join(logDir, "setup.out")
		status.Nodes[1].Stdout = deployLog
		status.Nodes[1].Status = deployStatus
		status.Nodes[2].Stdout = cleanLog
		if runID != root.ID {
			status.BoundNode = "web01"
		}
		require.NoError(t, attempt.Open(server.Context))
		require.NoError(t, attempt.Write(server.Context, status))
		require.NoError(t, attempt.Close(server.Context))
	}
	return server
}

// rowsOf returns each row's node and the status of its runs, by step.
func rowsOf(list api.NodeRunList) []string {
	rows := make([]string, 0, len(list.Items))
	for _, row := range list.Items {
		cells := row.Node
		for _, run := range row.Runs {
			cells += " " + run.Step + "=" + string(run.Status)
		}
		rows = append(rows, cells)
	}
	return rows
}

func TestNodeRuns(t *testing.T) {
	server := seedNodeRuns(t, ir.NodeRunning)
	for _, base := range []string{"/api/v1/dag-runs/node-runs-api/root", "/api/v1/dag-runs/node-runs-api/root/sub-dag-runs/child"} {
		get := func(t *testing.T, query string) api.NodeRunList {
			t.Helper()
			var list api.NodeRunList
			server.Client().Get(base+"/node-runs"+query).ExpectStatus(200).Send(t).Unmarshal(t, &list)
			return list
		}

		t.Run(base+" failed rows lead, then running rows", func(t *testing.T) {
			list := get(t, "")
			require.Equal(t, []string{
				"web03 deploy=failed clean=not_started",
				"web04 deploy=running",
				"web01 deploy=succeeded clean=succeeded",
				"web02 deploy=disabled clean=removed",
			}, rowsOf(list))
			require.Equal(t, []api.NodeRunStep{
				{Name: "deploy", Counts: api.NodeRunCounts{Total: 4, Running: 1, Succeeded: 1, Failed: 1, Disabled: 1}},
				{Name: "clean", Counts: api.NodeRunCounts{Total: 3, NotStarted: 1, Succeeded: 1, Removed: 1}},
			}, list.Steps)
			require.Equal(t, 4, list.Pagination.TotalRecords)

			failed := list.Items[0].Runs[0]
			require.Equal(t, "d3", *failed.DagRunId)
			require.Equal(t, "deploy-one", *failed.DagName)
			require.Equal(t, api.StepProgress{Done: 1, Total: 2}, *failed.Steps)
			require.Equal(t, "restart", *failed.FailedStep)
		})

		t.Run(base+" failedOnly keeps the failed rows", func(t *testing.T) {
			list := get(t, "?failedOnly=true")
			require.Equal(t, []string{"web03 deploy=failed clean=not_started"}, rowsOf(list))
			require.Equal(t, 1, list.Pagination.TotalRecords)
		})

		t.Run(base+" pages", func(t *testing.T) {
			list := get(t, "?perPage=1&page=2")
			require.Equal(t, []string{"web04 deploy=running"}, rowsOf(list))
			require.Equal(t, 4, list.Pagination.TotalPages)
		})
	}

	t.Run("run details count the node runs of each step with nodes", func(t *testing.T) {
		var details api.GetDAGRunDetails200JSONResponse
		server.Client().Get("/api/v1/dag-runs/node-runs-api/root").ExpectStatus(200).Send(t).Unmarshal(t, &details)
		nodes := details.DagRunDetails.Nodes
		require.Nil(t, nodes[0].NodeRuns)
		require.Equal(t, &api.NodeRunCounts{Total: 4, Running: 1, Succeeded: 1, Failed: 1, Disabled: 1}, nodes[1].NodeRuns)
		require.Equal(t, &api.NodeRunCounts{Total: 3, NotStarted: 1, Succeeded: 1, Removed: 1}, nodes[2].NodeRuns)
		require.Nil(t, details.DagRunDetails.BoundNode)
	})

	t.Run("a bound run names its node", func(t *testing.T) {
		var details api.GetSubDAGRunDetails200JSONResponse
		server.Client().Get("/api/v1/dag-runs/node-runs-api/root/sub-dag-runs/child").ExpectStatus(200).Send(t).Unmarshal(t, &details)
		require.Equal(t, "web01", *details.DagRunDetails.BoundNode)
	})

	t.Run("an unknown run is not found", func(t *testing.T) {
		server.Client().Get("/api/v1/dag-runs/node-runs-api/nowhere/node-runs").ExpectStatus(404).Send(t)
	})
}

// A step that ends records its nodes once more. A node still recorded as
// running after the step ended ran when its run stopped without that write,
// as when its process died, so it ended as the step did.
func TestNodeRunsOfEndedStep(t *testing.T) {
	for stepStatus, want := range map[ir.NodeStatus]api.NodeRunCounts{
		ir.NodeFailed:  {Total: 4, Succeeded: 1, Failed: 2, Disabled: 1},
		ir.NodeAborted: {Total: 4, Succeeded: 1, Failed: 1, Aborted: 1, Disabled: 1},
	} {
		t.Run(stepStatus.String(), func(t *testing.T) {
			server := seedNodeRuns(t, stepStatus)

			var list api.NodeRunList
			server.Client().Get("/api/v1/dag-runs/node-runs-api/root/node-runs").ExpectStatus(200).Send(t).Unmarshal(t, &list)
			require.Equal(t, want, list.Steps[0].Counts)
			require.NotContains(t, rowsOf(list), "web04 deploy=running")

			var details api.GetDAGRunDetails200JSONResponse
			server.Client().Get("/api/v1/dag-runs/node-runs-api/root").ExpectStatus(200).Send(t).Unmarshal(t, &details)
			require.Equal(t, &want, details.DagRunDetails.Nodes[1].NodeRuns)
		})
	}
}
