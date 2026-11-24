// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec089_node_targeting_test

import (
	"path/filepath"
	"testing"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

// nodeRunRows returns each row's node and the status of its runs, by step.
func nodeRunRows(list api.NodeRunList) []string {
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

// The node runs of a run are read by node, failed first, through a server
// that reads the run dagu start made (Spec 089 §Viewing node runs).
func TestNodeRunsView(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	dataDir := nodesDataDir(t)
	dagu.RunWithEnv([]string{"DAGU_DATA_DIR=" + dataDir}, "start", "--run-id=view-1", "node_view.yaml").ExpectNonZeroExitCode()

	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Paths.DataDir = dataDir
		cfg.Paths.DAGRunsDir = filepath.Join(dataDir, "dag-runs")
	}))
	get := func(t *testing.T, path string, v any) {
		t.Helper()
		server.Client().Get("/api/v1/dag-runs/node_view/view-1"+path).ExpectStatus(200).Send(t).Unmarshal(t, v)
	}

	var list api.NodeRunList
	get(t, "/node-runs", &list)
	require.Equal(t, []string{
		"web03 deploy=succeeded check=failed",
		"web01 deploy=succeeded check=succeeded",
		"web02 deploy=disabled check=disabled",
	}, nodeRunRows(list))
	require.Equal(t, []api.NodeRunStep{
		{Name: "deploy", Counts: api.NodeRunCounts{Total: 3, Succeeded: 2, Disabled: 1}},
		{Name: "check", Counts: api.NodeRunCounts{Total: 3, Succeeded: 1, Failed: 1, Disabled: 1}},
	}, list.Steps)

	failed := list.Items[0].Runs[1]
	require.Equal(t, "check-one", *failed.DagName)
	require.Equal(t, api.StepProgress{Done: 1, Total: 2}, *failed.Steps)
	require.Equal(t, "probe", *failed.FailedStep)
	require.NotNil(t, failed.StartedAt)
	require.NotNil(t, failed.FinishedAt)

	var failedOnly api.NodeRunList
	get(t, "/node-runs?failedOnly=true", &failedOnly)
	require.Equal(t, []string{"web03 deploy=succeeded check=failed"}, nodeRunRows(failedOnly))

	var second api.NodeRunList
	get(t, "/node-runs?perPage=1&page=2", &second)
	require.Equal(t, []string{"web01 deploy=succeeded check=succeeded"}, nodeRunRows(second))
	require.Equal(t, 3, second.Pagination.TotalPages)

	var details api.GetDAGRunDetails200JSONResponse
	get(t, "", &details)
	require.Equal(t, list.Steps[0].Counts, *details.DagRunDetails.Nodes[0].NodeRuns)
	require.Equal(t, list.Steps[1].Counts, *details.DagRunDetails.Nodes[1].NodeRuns)

	var child api.GetSubDAGRunDetails200JSONResponse
	get(t, "/sub-dag-runs/"+*failed.DagRunId, &child)
	require.Equal(t, "web03", *child.DagRunDetails.BoundNode)
}
