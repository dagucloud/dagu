// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intake_test

import (
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/intake"
	"github.com/dagucloud/dagu/v2/internal/ir"
	_ "github.com/dagucloud/dagu/v2/internal/runtime/builtin"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/require"
)

// reuseSpec reads a page, keeps the rows a filter passes, and reports them;
// the filter reads the page's output by reference, so it depends on it
// without depends.
const reuseSpec = `name: listings
params:
  - MAX: 7000
env:
  - SITE: https://homes.example.com
steps:
  - id: page
    run: echo rows
    output: ROWS
  - id: filter
    run: echo "${steps.page.outputs.ROWS} under 7000"
    output: KEPT
  - id: report
    run: echo report
    depends: [filter]
  - id: notes
    run: echo notes
`

func loadReuseDAG(t *testing.T, yaml string) *ir.DAG {
	t.Helper()
	dag, err := spec.LoadYAML(t.Context(), []byte(yaml))
	require.NoError(t, err)
	return dag
}

func succeededRun(dag *ir.DAG) *ir.DAGRunStatus {
	status := &ir.DAGRunStatus{DAGRunID: "earlier", Status: ir.Succeeded, Params: strings.Join(dag.Params, " ")}
	for _, step := range dag.Steps {
		status.Nodes = append(status.Nodes, &ir.Node{Step: step, Status: ir.NodeSucceeded})
	}
	return status
}

func TestStepsToRun(t *testing.T) {
	t.Parallel()

	earlier := loadReuseDAG(t, reuseSpec)
	for _, tc := range []struct {
		name string
		yaml string
		// params replaces the parameters the run resolved, when set.
		params []string
		// failed is a step that failed in the earlier run.
		failed string
		want   []string
	}{
		{name: "Unchanged", yaml: reuseSpec, want: nil},
		{name: "ChangedStepAndTheStepsAfterIt", yaml: strings.Replace(reuseSpec, "under 7000", "under 5000", 1), want: []string{"filter", "report"}},
		{name: "ChangedFirstStepReachesAStepThatOnlyReadsIt", yaml: strings.Replace(reuseSpec, "echo rows", "echo all rows", 1), want: []string{"page", "filter", "report"}},
		{name: "ChangedEnvironment", yaml: strings.Replace(reuseSpec, "homes.example.com", "homes.example.org", 1), want: []string{"page", "filter", "report", "notes"}},
		{name: "ChangedParameters", yaml: reuseSpec, params: []string{"MAX=5000"}, want: []string{"page", "filter", "report", "notes"}},
		{name: "FailedStepAndTheStepsAfterIt", yaml: reuseSpec, failed: "filter", want: []string{"filter", "report"}},
		{name: "AddedStep", yaml: reuseSpec + "  - id: archive\n    run: echo archive\n", want: []string{"archive"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := succeededRun(earlier)
			for _, node := range source.Nodes {
				if node.Step.Name == tc.failed {
					node.Status = ir.NodeFailed
				}
			}
			dag := loadReuseDAG(t, tc.yaml)
			if tc.params != nil {
				dag.Params = tc.params
			}
			got, err := intake.StepsToRun(dag, earlier, source)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
