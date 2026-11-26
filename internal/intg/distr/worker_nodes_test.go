// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package distr_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/noderuns"
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

func (n workerNodes) Nodes(context.Context) ([]inventory.ResolvedNode, error) {
	return n, nil
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
