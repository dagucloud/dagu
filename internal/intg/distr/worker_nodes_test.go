// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package distr_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/noderuns"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persiststore "github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/runtime/agent"
	"github.com/dagucloud/dagu/v2/internal/test"
)

// workerNodes is a project of fixed worker nodes, matched as the store
// matches.
type workerNodes []inventory.ResolvedNode

func (n workerNodes) Select(_ context.Context, selector string) ([]inventory.ResolvedNode, error) {
	filters, err := inventory.ParseSelector(selector)
	if err != nil {
		return nil, err
	}
	return inventory.Select(n, filters), nil
}

func (n workerNodes) Named(_ context.Context, names []string) ([]inventory.ResolvedNode, error) {
	return inventory.Named(n, names), nil
}

// A step with nodes on the Dagu host that matches a worker node whose worker
// is offline fails that node at once, although the coordinator holds other
// dispatches for an hour until a worker registers (RFC 013 §Matching).
func TestWorkerNodes_OfflineWorkerFailsAtOnce(t *testing.T) {
	t.Parallel()

	f := newTestFixture(t, `
steps:
  - id: rollout
    run: echo ${node.name}
    nodes: site=tokyo
`, withWorkerWaitTimeout(time.Hour))
	nodes := workerNodes{{Name: "gone-gw", Kind: inventory.NodeKindWorker, Host: "gw.tokyo", Labels: ir.NewLabels([]string{"site=tokyo"})}}

	started := time.Now()
	dagAgent := f.dagWrapper.Agent(test.WithAgentOptions(agent.Options{NodeSelector: nodes}))
	dagAgent.RunError(t)
	require.Less(t, time.Since(started), distrTestTimeout(30*time.Second), "the node does not wait for its worker")

	status := dagAgent.Status(f.coord.Context)
	require.Len(t, status.Nodes, 1)
	rollout := status.Nodes[0]
	require.Equal(t, ir.NodeFailed, rollout.Status)
	require.Contains(t, rollout.Error, `worker node "gone-gw" is offline`)

	records, err := noderuns.Read(noderuns.Dir(rollout.Stdout, "rollout"))
	require.NoError(t, err)
	require.Len(t, records.Runs, 1)
	require.Equal(t, "gone-gw", records.Runs[0].Node)
	require.Equal(t, ir.NodeRunFailed, records.Runs[0].Status)
}

// startWorkerNodes starts a worker for each ID, with labels, and waits until
// the coordinator records each as a worker node. It returns the worker
// nodes of the installation.
func startWorkerNodes(t *testing.T, f *testFixture, labels map[string]string, ids ...string) inventory.WorkerNodeStore {
	t.Helper()
	workers, err := persiststore.NewWorkerNodeStore(f.coord.Backend.Collection(persis.CollectionWorkerNodes), f.coord.WorkerHeartbeatStore)
	require.NoError(t, err)
	for _, id := range ids {
		f.workers = append(f.workers, f.setupWorker(id, labels, ""))
	}
	f.h.Wait.EventuallyEveryWithin("the coordinator records the worker nodes", distrTestTimeout(30*time.Second), 100*time.Millisecond, func() bool {
		for _, id := range ids {
			if _, err := workers.Get(f.coord.Context, id); err != nil {
				return false
			}
		}
		return true
	})
	return workers
}

// nodeStepResults reads the aggregate a step with nodes wrote to stdout.
func nodeStepResults(t *testing.T, stdout string) []struct {
	Node     string            `json:"node"`
	DAGRunID string            `json:"dagRunId"`
	Status   string            `json:"status"`
	Outputs  map[string]string `json:"outputs"`
} {
	t.Helper()
	data, err := fileutil.ReadFile(stdout)
	require.NoError(t, err)
	var aggregate struct {
		Results []struct {
			Node     string            `json:"node"`
			DAGRunID string            `json:"dagRunId"`
			Status   string            `json:"status"`
			Outputs  map[string]string `json:"outputs"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal(data, &aggregate))
	return aggregate.Results
}

// childStatus returns the status of a child run of root.
func childStatus(t *testing.T, f *testFixture, root ir.DAGRunRef, runID string) *ir.DAGRunStatus {
	t.Helper()
	attempt, err := f.coord.DAGRunRepository.FindSubAttempt(f.coord.Context, root, runID)
	require.NoError(t, err)
	status, err := attempt.ReadStatus(f.coord.Context)
	require.NoError(t, err)
	return status
}

// A step with nodes on the Dagu host that matches worker nodes dispatches one
// child to each worker by its ID, and each child reads its node (RFC 013
// §Bound runs).
func TestWorkerNodes_HostStepRunsOnEachWorker(t *testing.T) {
	t.Parallel()

	f := newTestFixture(t, `
steps:
  - id: deploy
    action: dag.run
    with:
      dag: deploy-one
    nodes:
      labels: role=gw
      max_concurrent: 2
---
name: deploy-one
node: required
steps:
  - id: where
    run: echo "${node.name} ${node.host}"
    output: WHERE
`, withWorkerCount(0))
	workers := startWorkerNodes(t, f, map[string]string{"role": "gw"}, "gw-1", "gw-2")
	inventories, err := persiststore.NewInventoryStore(f.coord.Backend.Collection(persis.CollectionInventories),
		f.coord.Backend.Lock(persis.CollectionInventories), nil)
	require.NoError(t, err)

	dagAgent := f.dagWrapper.Agent(test.WithAgentOptions(agent.Options{NodeSelector: inventory.NewProjectNodes(inventories, workers)}))
	dagAgent.RunSuccess(t)

	status := dagAgent.Status(f.coord.Context)
	root := ir.DAGRunRef{Name: status.Name, ID: status.DAGRunID}
	results := nodeStepResults(t, status.Nodes[0].Stdout)
	require.Len(t, results, 2)
	for i, id := range []string{"gw-1", "gw-2"} {
		node, err := workers.Get(f.coord.Context, id)
		require.NoError(t, err)
		require.Equal(t, id, results[i].Node)
		require.Equal(t, "succeeded", results[i].Status)
		require.Equal(t, fmt.Sprintf("%s %s", id, node.Host), results[i].Outputs["WHERE"])
		require.Equal(t, id, childStatus(t, f, root, results[i].DAGRunID).WorkerID, "the child runs on its worker")
	}
}

// A command step with nodes in a run on a worker runs once on each of an SSH
// node and a worker node, and lists them in name order (RFC 013 acceptance
// item 2). The worker node's child runs that step alone, on that worker. The
// shell decides how echo splits its arguments, so the test reads how often
// each node was reached, how each child run ended, and that the worker
// child's log names its node.
func TestWorkerNodes_CommandStepOnSSHAndWorkerNodes(t *testing.T) {
	t.Parallel()

	f := newTestFixture(t, `
worker_selector:
  test: "true"
steps:
  - id: check
    run: echo checked ${node.name}
    nodes: role=web
  - id: sibling
    run: echo sibling
`, withLogPersistence())
	startWorkerNodes(t, f, map[string]string{"role": "web"}, "gw-1")
	servers, _ := startFleet(t, f, fleetNode{name: "web01", label: "role=web"})

	runID, err := f.dispatchDirect()
	require.NoError(t, err)
	status := f.waitForStatus(ir.Succeeded, executionStatusTimeout())
	f.assertWorkerID(status, "worker-1")

	require.Len(t, servers["web01"].Commands(), 1, "web01 runs the step once")

	// The step's aggregate stays on the worker; its child runs are listed in
	// node name order.
	children := status.Nodes[0].SubRuns
	require.Len(t, children, 2)
	require.Equal(t, "gw-1", children[0].Node)
	require.Equal(t, "web01", children[1].Node)
	root := ir.DAGRunRef{Name: status.Name, ID: runID}
	onWorker := childStatus(t, f, root, children[0].DAGRunID)
	require.Equal(t, "gw-1", onWorker.WorkerID)
	require.Equal(t, "gw-1", onWorker.BoundNode)
	require.Len(t, onWorker.Nodes, 1, "the child runs the step alone")
	require.Equal(t, "check", onWorker.Nodes[0].Step.Name)
	require.Equal(t, ir.NodeSucceeded, onWorker.Nodes[0].Status)
	require.Contains(t, getLogContent(t, onWorker.Nodes[0].Stdout), "gw-1", "${node.name} resolves on the worker")
	require.Equal(t, "sibling", status.Nodes[1].Step.Name)
	require.Equal(t, ir.NodeSucceeded, status.Nodes[1].Status)
	require.Equal(t, "worker-1", childStatus(t, f, root, children[1].DAGRunID).WorkerID, "an SSH node's child runs where its parent runs")
}
