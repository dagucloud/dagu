// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/runtime/agent"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// staticNodes is an inventory of fixed nodes, matched as the store matches.
type staticNodes []inventory.ResolvedNode

func (s staticNodes) Select(_ context.Context, selector string) ([]inventory.ResolvedNode, error) {
	filters, err := inventory.ParseSelector(selector)
	if err != nil {
		return nil, err
	}
	return inventory.Select(s, filters), nil
}

func webNode(name string, labels ...string) inventory.ResolvedNode {
	return inventory.ResolvedNode{Name: name, Host: name + ".internal", Labels: ir.NewLabels(append([]string{"web"}, labels...))}
}

// nodeAggregate is the part of a node step's aggregate the tests read.
type nodeAggregate struct {
	Summary struct {
		Total     int `json:"total"`
		Succeeded int `json:"succeeded"`
		Failed    int `json:"failed"`
	} `json:"summary"`
	Results []struct {
		Node   string `json:"node"`
		Status string `json:"status"`
	} `json:"results"`
}

// runNodeStep runs a DAG whose first step targets nodes, and returns the
// run's status and that step's aggregate.
func runNodeStep(t *testing.T, nodes staticNodes, yaml string, wantErr string) (*ir.DAGRunStatus, nodeAggregate) {
	t.Helper()

	th := test.Setup(t)
	dag := th.DAG(t, yaml)
	dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{NodeSelector: nodes}))
	if wantErr == "" {
		dagAgent.RunSuccess(t)
	} else {
		dagAgent.RunCheckErr(t, wantErr)
	}
	status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)

	var aggregate nodeAggregate
	if data, err := os.ReadFile(status.Nodes[0].Stdout); err == nil && len(data) > 0 {
		require.NoError(t, json.Unmarshal(data, &aggregate))
	}
	return &status, aggregate
}

func nodeStatuses(aggregate nodeAggregate) map[string]string {
	statuses := make(map[string]string, len(aggregate.Results))
	for _, result := range aggregate.Results {
		statuses[result.Node] = result.Status
	}
	return statuses
}

const boundChildSucceeds = `
---
name: deploy-one
node: required
steps:
  - id: noop
    action: noop
`

const boundChildFails = `
---
name: deploy-one
node: required
steps:
  - id: fail
    action: js.run
    with:
      script: throw new Error("boom")
`

func TestNodeSteps_OneBoundChildPerNode(t *testing.T) {
	t.Parallel()

	nodes := staticNodes{webNode("web01"), webNode("web02"), webNode("web03")}
	nodes[1].Disabled = true
	status, aggregate := runNodeStep(t, nodes, `
steps:
  - id: deploy
    action: dag.run
    with:
      dag: deploy-one
    nodes: web
`+boundChildSucceeds, "")

	require.Equal(t, ir.Succeeded, status.Status)
	var bound []string
	for _, run := range status.Nodes[0].SubRuns {
		bound = append(bound, run.Node)
	}
	require.Equal(t, []string{"web01", "web03"}, bound)
	require.Equal(t, []string{"web01", "web02", "web03"}, []string{
		aggregate.Results[0].Node, aggregate.Results[1].Node, aggregate.Results[2].Node,
	})
	require.Equal(t, map[string]string{"web01": "succeeded", "web02": "disabled", "web03": "succeeded"}, nodeStatuses(aggregate))
	require.Equal(t, 3, aggregate.Summary.Total)
	require.Equal(t, 2, aggregate.Summary.Succeeded)
}

// With keep_going false and one node at a time, the first failure leaves
// every later node unstarted. keep_going true runs them all.
func TestNodeSteps_FailurePolicy(t *testing.T) {
	t.Parallel()

	nodes := staticNodes{webNode("web01"), webNode("web02"), webNode("web03")}
	for _, tc := range []struct {
		name      string
		nodes     string
		want      map[string]string
		wantTotal int
	}{
		{
			name:  "StopAtFirstFailure",
			nodes: "web",
			want:  map[string]string{"web01": "failed", "web02": "not_started", "web03": "not_started"},
		},
		{
			name:  "TwoAtOnce",
			nodes: "{labels: web, max_concurrent: 2}",
			want:  map[string]string{"web01": "failed", "web02": "failed", "web03": "not_started"},
		},
		{
			name:  "KeepGoing",
			nodes: "{labels: web, keep_going: true}",
			want:  map[string]string{"web01": "failed", "web02": "failed", "web03": "failed"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, aggregate := runNodeStep(t, nodes, `
steps:
  - id: deploy
    action: dag.run
    with:
      dag: deploy-one
    nodes: `+tc.nodes+`
`+boundChildFails, "failed")

			require.Equal(t, ir.Failed, status.Status)
			require.Equal(t, tc.want, nodeStatuses(aggregate))
		})
	}
}

func TestNodeSteps_FailBeforeAnyChild(t *testing.T) {
	t.Parallel()

	nodes := staticNodes{webNode("web01", "env=prod"), webNode("web02", "env=dev")}
	for _, tc := range []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "NoMatch",
			yaml:    "steps:\n  - id: deploy\n    action: dag.run\n    with:\n      dag: deploy-one\n    nodes: db\n" + boundChildSucceeds,
			wantErr: "selector matched no nodes: db",
		},
		{
			name:    "ChildNotNodeRequired",
			yaml:    "steps:\n  - id: deploy\n    action: dag.run\n    with:\n      dag: deploy-one\n    nodes: web\n---\nname: deploy-one\nsteps:\n  - action: noop\n",
			wantErr: `DAG "deploy-one" does not declare node: required`,
		},
		{
			name:    "ReferenceAddsTerm",
			yaml:    "params:\n  - env: \"prod,!canary\"\nsteps:\n  - id: deploy\n    action: dag.run\n    with:\n      dag: deploy-one\n    nodes: web,env=${params.env}\n" + boundChildSucceeds,
			wantErr: `nodes selector "${params.env}" resolved to "prod,!canary"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, _ := runNodeStep(t, nodes, tc.yaml, tc.wantErr)
			require.Equal(t, ir.NodeFailed, status.Nodes[0].Status)
		})
	}
}

// A reference that resolves to one label value narrows the match.
func TestNodeSteps_ReferenceInSelector(t *testing.T) {
	t.Parallel()

	nodes := staticNodes{webNode("web01", "env=prod"), webNode("web02", "env=dev")}
	_, aggregate := runNodeStep(t, nodes, `
params:
  - env: prod
steps:
  - id: deploy
    action: dag.run
    with:
      dag: deploy-one
    nodes: web,env=${params.env}
`+boundChildSucceeds, "")

	require.Equal(t, map[string]string{"web01": "succeeded"}, nodeStatuses(aggregate))
}

// A command step runs as the only step of a child bound to each node. Until
// the node is bound over SSH, that child fails before its command runs, so
// the command never runs on the Dagu host.
func TestNodeSteps_CommandStepNeverRunsLocally(t *testing.T) {
	t.Parallel()

	marker := t.TempDir() + "/ran"
	status, aggregate := runNodeStep(t, staticNodes{webNode("web01"), webNode("web02")}, `
steps:
  - id: clean
    run: touch `+marker+`
    retry_policy: {limit: 2, interval_sec: 0}
    nodes: {labels: web, keep_going: true}
`, "failed")

	require.Equal(t, ir.Failed, status.Status)
	require.Zero(t, status.Nodes[0].RetryCount, "the retry policy applies to each node, not the step")
	require.Equal(t, map[string]string{"web01": "failed", "web02": "failed"}, nodeStatuses(aggregate))
	require.NoFileExists(t, marker)
}

// dagu start reads the inventory from its data directory, and a command
// step's child, the parent's definition narrowed to the step, takes the parent
// run's params, so it loads even when a param has no default.
func TestNodeSteps_StartReadsInventory(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	backend := file.NewBackend(th.Config.Paths)
	inventories, err := store.NewInventoryStore(
		backend.Collection(persis.CollectionInventories),
		backend.Lock(persis.CollectionInventories),
	)
	require.NoError(t, err)
	_, err = inventories.Create(th.Context, inventory.Inventory{
		Name:  "fleet",
		Nodes: []inventory.Node{{Name: "web01", Host: "10.0.1.11", Labels: []string{"web"}}},
	})
	require.NoError(t, err)

	dagFile := th.CreateDAGFile(t, "clean-web.yaml", `
name: clean-web
params:
  - name: version
    required: true
steps:
  - id: clean
    run: echo ${params.version}
    nodes: web
`)
	runID := uuid.Must(uuid.NewV7()).String()
	err = th.RunCommandWithError(t, cmd.Start(), test.CmdTest{
		Args: []string{"start", "--run-id", runID, "--params", "version=2.1", dagFile},
	})
	require.ErrorContains(t, err, `node web01 failed: step "clean" runs commands, and running commands on inventory node "web01" is not available yet`)

	status, _ := readAttemptStatusAndOutputs(t, th, "clean-web", runID)
	require.Len(t, status.Nodes[0].SubRuns, 1)
	child, _ := readSubAttemptStatusAndOutputs(t, th, ir.NewDAGRunRef("clean-web", runID), status.Nodes[0].SubRuns[0].DAGRunID)
	require.Equal(t, "web01", child.BoundNode)
	require.Equal(t, []string{"version=2.1"}, child.ParamsList)
	require.Equal(t, ir.Failed, child.Status)
	require.Equal(t, ir.NodeNotStarted, child.Nodes[0].Status)
}
