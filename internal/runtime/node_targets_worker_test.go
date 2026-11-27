// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
)

// mixedNodes is a project whose every selector matches an SSH node and a
// worker node.
type mixedNodes struct{}

func (mixedNodes) Select(context.Context, string) ([]inventory.ResolvedNode, error) {
	return []inventory.ResolvedNode{
		{Name: "web-gw", Kind: inventory.NodeKindWorker},
		{Name: "web01"},
	}, nil
}

func (n mixedNodes) Named(ctx context.Context, names []string) ([]inventory.ResolvedNode, error) {
	nodes, err := n.Select(ctx, "")
	return inventory.Named(nodes, names), err
}

// boundChildren records the node and worker of each child run it starts.
type boundChildren struct {
	childRuns
	mu      sync.Mutex
	workers map[string]string
}

func (c *boundChildren) Run(ctx context.Context, req executor.SubWorkflowRequest) (*ir.RunStatus, error) {
	c.mu.Lock()
	c.workers[req.Node] = req.TargetWorkerID
	c.mu.Unlock()
	return c.childRuns.Run(ctx, req)
}

// A child bound to a worker node runs on that worker; one bound to an SSH
// node runs where its parent runs.
func TestNodeChildTargetsWorker(t *testing.T) {
	t.Parallel()

	for _, step := range []ir.Step{
		newStep("rollout", withExecutorType(ir.ExecutorTypeDAG), withSubDAG("deploy-one"), withNodes("web")),
		newStep("rollout", withCommand("./deploy.sh"), withNodes("web")),
	} {
		children := &boundChildren{workers: make(map[string]string)}
		r := setupRunner(t)
		r.Context = executor.WithSubWorkflowRunner(r.Context, children)
		ph := r.newPlan(t, step)
		ph.steps = []ir.Step{step}
		ph.localDAGs = map[string]*ir.DAG{"deploy-one": deployOneDAG()}
		ph.contextOpts = []runtime.ContextOption{
			runtime.WithRootDAGRun(runtime.NewDAGRunRef("test_dag", r.cfg.DAGRunID)),
			runtime.WithNodeSelector(mixedNodes{}),
		}
		ph.assertRun(t, ir.Succeeded)

		assert.Equal(t, map[string]string{"web-gw": "web-gw", "web01": ""}, children.workers)
	}
}
