// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/runtime/agent"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
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

func (s staticNodes) Nodes(context.Context) ([]inventory.ResolvedNode, error) {
	return s, nil
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

	// The aggregate is the stdout of the last step, the one with nodes.
	var aggregate nodeAggregate
	if data, err := os.ReadFile(status.Nodes[len(status.Nodes)-1].Stdout); err == nil && len(data) > 0 {
		require.NoError(t, json.Unmarshal(data, &aggregate))
	}
	return &status, aggregate
}

// childRecorder is a sub-workflow runner that records each child run it is
// asked to start, and starts none.
type childRecorder struct {
	mu      sync.Mutex
	started []string
}

func (r *childRecorder) ShouldRun(context.Context, executor.SubWorkflowRequest) bool { return true }

func (r *childRecorder) Run(_ context.Context, req executor.SubWorkflowRequest) (*ir.RunStatus, error) {
	return nil, r.record(req.RunID)
}

func (r *childRecorder) Retry(_ context.Context, req executor.SubWorkflowRetryRequest) (*ir.RunStatus, error) {
	return nil, r.record(req.RunID)
}

func (*childRecorder) Cancel(context.Context, executor.SubWorkflowCancelRequest) error { return nil }

func (r *childRecorder) record(runID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = append(r.started, runID)
	return errors.New("child runs are not started in this test")
}

func (r *childRecorder) runs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.started)
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

			children := &childRecorder{}
			th := test.Setup(t)
			dag := th.DAG(t, tc.yaml)
			dagAgent := dag.Agent(test.WithAgentOptions(agent.Options{
				NodeSelector: nodes,
				SubWorkflowRunnerFactory: func(context.Context) (executor.SubWorkflowRunner, error) {
					return children, nil
				},
			}))
			dagAgent.RunCheckErr(t, tc.wantErr)
			require.Equal(t, ir.NodeFailed, dagAgent.Status(th.Context).Nodes[0].Status)
			require.Empty(t, children.runs(), "a child run was started")
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

// sshNode starts an SSH server that accepts a new key, and returns a node
// that reaches it with that key, from a file, and its host key pinned.
func sshNode(t *testing.T, name string, labels ...string) (inventory.Node, *harness.SSHServer) {
	t.Helper()

	private, public := harness.NewSSHKey(t)
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	require.NoError(t, os.WriteFile(keyPath, private, 0o600))
	server := harness.StartSSHServer(t, harness.SSHServerOptions{User: "tester", AuthorizedKeys: []ssh.PublicKey{public}})
	port, err := strconv.Atoi(server.Port)
	require.NoError(t, err)
	return inventory.Node{
		Name: name,
		Host: "127.0.0.1",
		Connection: inventory.Connection{
			Port:    port,
			User:    "tester",
			Key:     keyPath,
			HostKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(server.HostKey))),
		},
		Labels: append([]string{"web"}, labels...),
	}, server
}

// resolved applies the inventory rules to nodes, as the store does.
func resolved(t *testing.T, nodes ...inventory.Node) staticNodes {
	t.Helper()

	out, err := inventory.Resolve([]inventory.Inventory{{ID: "fleet", Name: "fleet", Nodes: nodes}})
	require.NoError(t, err)
	return out
}

// A command step runs as the only step of a child bound to each node, so the
// command reaches each node over SSH, and its retry policy applies to each
// node, not to the step.
func TestNodeSteps_CommandStepRunsOnEachNode(t *testing.T) {
	t.Parallel()

	web01, server01 := sshNode(t, "web01")
	web02, server02 := sshNode(t, "web02")
	status, aggregate := runNodeStep(t, resolved(t, web01, web02), `
steps:
  - id: clean
    run: exit 1
    retry_policy: {limit: 2, interval_sec: 0}
    nodes: {labels: web, keep_going: true}
`, "failed")

	require.Equal(t, ir.Failed, status.Status)
	require.Zero(t, status.Nodes[0].RetryCount, "the retry policy applies to each node, not the step")
	require.Equal(t, map[string]string{"web01": "failed", "web02": "failed"}, nodeStatuses(aggregate))
	require.Len(t, server01.Commands(), 3, "web01 runs the command and both retries")
	require.Len(t, server02.Commands(), 3, "web02 runs the command and both retries")
}

// A command step's child reads what the step would read without nodes: the
// outputs of earlier steps, as variables and as step references, beside the
// node's fields.
func TestNodeSteps_CommandStepReadsEarlierOutputs(t *testing.T) {
	t.Parallel()

	web01, _ := sshNode(t, "web01")
	dir := filepath.ToSlash(t.TempDir())
	runNodeStep(t, resolved(t, web01), `
steps:
  - id: build
    run: echo 1.2.3
    output: VERSION
  - id: deploy
    depends: [build]
    run: echo "$VERSION ${env.VERSION} ${build.exit_code} ${node.name}" > `+dir+`/deployed
    nodes: web
`, "")

	data, err := os.ReadFile(filepath.Join(dir, "deployed"))
	require.NoError(t, err)
	require.Equal(t, "1.2.3 1.2.3 0 web01\n", string(data))
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
	web01, server := sshNode(t, "web01")
	_, err = inventories.Create(th.Context, inventory.Inventory{Name: "fleet", Nodes: []inventory.Node{web01}})
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
	th.RunCommand(t, cmd.Start(), test.CmdTest{
		Args: []string{"start", "--run-id", runID, "--params", "version=2.1", dagFile},
	})
	require.Len(t, server.Commands(), 1)

	status, _ := readAttemptStatusAndOutputs(t, th, "clean-web", runID)
	require.Len(t, status.Nodes[0].SubRuns, 1)
	child, _ := readSubAttemptStatusAndOutputs(t, th, ir.NewDAGRunRef("clean-web", runID), status.Nodes[0].SubRuns[0].DAGRunID)
	require.Equal(t, "web01", child.BoundNode)
	require.Equal(t, []string{"version=2.1"}, child.ParamsList)
	require.Equal(t, ir.Succeeded, child.Status)
}
