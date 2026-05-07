// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/stretchr/testify/require"
)

// helper to count total dependencies in a plan
func totalDeps(p *runtime.Plan) int {
	c := 0
	for _, node := range p.Nodes() {
		c += len(p.Dependents(node.ID()))
	}
	return c
}

// helper to quickly make a Node
func makeNode(name string, status core.NodeStatus, depends ...string) *runtime.Node {
	return runtime.NodeWithData(runtime.NodeData{
		Step:  core.Step{Name: name, Depends: depends},
		State: runtime.NodeState{Status: status},
	})
}

func TestPlan_Cyclic(t *testing.T) {
	step1 := core.Step{Name: "1", Depends: []string{"2"}}
	step2 := core.Step{Name: "2", Depends: []string{"1"}}
	_, err := runtime.NewPlan(step1, step2)
	require.Error(t, err)
	require.ErrorIs(t, err, runtime.ErrCyclicPlan)
}

func TestPlan_NodeByName(t *testing.T) {
	steps := []core.Step{{Name: "a"}, {Name: "b", Depends: []string{"a"}}}
	p, err := runtime.NewPlan(steps...)
	require.NoError(t, err)
	require.NotNil(t, p.GetNodeByName("a"))
	require.NotNil(t, p.GetNodeByName("b"))
	require.Nil(t, p.GetNodeByName("c"))
}

func TestPlan_DependencyStructures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		steps             []core.Step
		wantTotalDeps     int
		wantOutgoingCount int // nodes with dependents
		wantIncomingCount int // nodes with dependencies
	}{
		{
			name: "basic",
			steps: []core.Step{
				{Name: "step1", Commands: []core.CommandEntry{{Command: "echo", Args: []string{"1"}}}},
				{Name: "step2", Commands: []core.CommandEntry{{Command: "echo", Args: []string{"2"}}}, Depends: []string{"step1"}},
				{Name: "step3", Commands: []core.CommandEntry{{Command: "echo", Args: []string{"3"}}}, Depends: []string{"step2", "step1"}},
			},
			wantTotalDeps:     3, // 1->2,1->3,2->3
			wantOutgoingCount: 2, // step1 (has 2,3), step2 (has 3)
			wantIncomingCount: 2, // step2 (has 1), step3 (has 1,2)
		},
		{
			name: "single chain",
			steps: []core.Step{
				{Name: "download"},
				{Name: "process", Depends: []string{"download"}},
				{Name: "cleanup", Depends: []string{"process"}},
			},
			wantTotalDeps:     2,
			wantOutgoingCount: 2,
			wantIncomingCount: 2,
		},
		{
			name: "fan in/out",
			steps: []core.Step{
				{Name: "download"},
				{Name: "extract"},
				{Name: "process", Depends: []string{"download", "extract"}},
				{Name: "cleanup", Depends: []string{"process"}},
			},
			wantTotalDeps:     3, // dl->process, extract->process, process->cleanup
			wantOutgoingCount: 3, // download, extract, process
			wantIncomingCount: 2, // process, cleanup
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := runtime.NewPlan(tt.steps...)
			require.NoError(t, err)
			require.Equal(t, tt.wantTotalDeps, totalDeps(p))

			outgoing := 0
			incoming := 0
			for _, n := range p.Nodes() {
				if len(p.Dependents(n.ID())) > 0 {
					outgoing++
				}
				if len(p.Dependencies(n.ID())) > 0 {
					incoming++
				}
			}

			require.Equal(t, tt.wantOutgoingCount, outgoing)
			require.Equal(t, tt.wantIncomingCount, incoming)
		})
	}
}

func TestRetryPlan(t *testing.T) {
	ctx := context.Background()
	dag := &core.DAG{Steps: []core.Step{
		{Name: "1"}, {Name: "2", Depends: []string{"1"}}, {Name: "3", Depends: []string{"2"}},
		{Name: "4"}, {Name: "5", Depends: []string{"4"}}, {Name: "6", Depends: []string{"5"}}, {Name: "7", Depends: []string{"6"}},
		{Name: "8"},
	}}
	nodes := []*runtime.Node{
		makeNode("1", core.NodeSucceeded),
		makeNode("2", core.NodeFailed, "1"),
		makeNode("3", core.NodeAborted, "2"),
		makeNode("4", core.NodeSkipped),
		makeNode("5", core.NodeFailed, "4"),
		makeNode("6", core.NodeSucceeded, "5"),
		makeNode("7", core.NodeSkipped, "6"),
		makeNode("8", core.NodeSkipped),
	}
	p, err := runtime.CreateRetryPlan(ctx, dag, nodes...)
	require.NoError(t, err)
	require.NotNil(t, p)
	// expectations based on upstream failures and aborted states triggering retry propagation
	require.Equal(t, core.NodeSucceeded, nodes[0].State().Status)
	require.Equal(t, core.NodeNotStarted, nodes[1].State().Status)
	require.Equal(t, core.NodeNotStarted, nodes[2].State().Status)
	require.Equal(t, core.NodeSkipped, nodes[3].State().Status)
	require.Equal(t, core.NodeNotStarted, nodes[4].State().Status)
	require.Equal(t, core.NodeNotStarted, nodes[5].State().Status)
	require.Equal(t, core.NodeNotStarted, nodes[6].State().Status)
	require.Equal(t, core.NodeSkipped, nodes[7].State().Status)
}

func TestRetryPlanWithRejectedNode(t *testing.T) {
	ctx := context.Background()
	dag := &core.DAG{Steps: []core.Step{
		{Name: "1"}, {Name: "2", Depends: []string{"1"}}, {Name: "3", Depends: []string{"2"}},
	}}

	// Create rejected node with metadata
	rejectedNode := runtime.NodeWithData(runtime.NodeData{
		Step: core.Step{Name: "2", Depends: []string{"1"}},
		State: runtime.NodeState{
			Status:          core.NodeRejected,
			RejectedAt:      "2024-01-15T10:00:00Z",
			RejectedBy:      "test-user",
			RejectionReason: "test reason",
		},
	})

	nodes := []*runtime.Node{
		makeNode("1", core.NodeSucceeded),
		rejectedNode,
		makeNode("3", core.NodeAborted, "2"),
	}
	p, err := runtime.CreateRetryPlan(ctx, dag, nodes...)
	require.NoError(t, err)
	require.NotNil(t, p)

	// Rejected node should be cleared and retried
	require.Equal(t, core.NodeSucceeded, nodes[0].State().Status)
	require.Equal(t, core.NodeNotStarted, nodes[1].State().Status)
	require.Equal(t, core.NodeNotStarted, nodes[2].State().Status)

	// Rejection metadata should be cleared
	require.Empty(t, rejectedNode.State().RejectedAt)
	require.Empty(t, rejectedNode.State().RejectedBy)
	require.Empty(t, rejectedNode.State().RejectionReason)
}

func TestRetryPlan_RebindsStepsFromRestoredDAG(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dag := &core.DAG{Steps: []core.Step{
		{Name: "approve"},
		{
			Name:    "container-step",
			Depends: []string{"approve"},
			Env:     []string{"STEP_ENV=from-step"},
			Container: &core.Container{
				Image: "alpine:3",
				Env:   []string{"CONTAINER_ENV=from-container"},
			},
		},
	}}
	nodes := []*runtime.Node{
		makeNode("approve", core.NodeSucceeded),
		runtime.NodeWithData(runtime.NodeData{
			Step: core.Step{
				Name:    "container-step",
				Depends: []string{"approve"},
				Container: &core.Container{
					Image: "alpine:3",
				},
			},
			State: runtime.NodeState{Status: core.NodeNotStarted},
		}),
	}

	p, err := runtime.CreateRetryPlan(ctx, dag, nodes...)
	require.NoError(t, err)
	require.NotNil(t, p)

	rebound := nodes[1].Step()
	require.Equal(t, core.NodeNotStarted, nodes[1].State().Status)
	require.Equal(t, []string{"STEP_ENV=from-step"}, rebound.Env)
	require.NotNil(t, rebound.Container)
	require.Equal(t, []string{"CONTAINER_ENV=from-container"}, rebound.Container.Env)
}

func TestStepRetryPlan_RebindsStepsFromRestoredDAG(t *testing.T) {
	t.Parallel()

	dag := &core.DAG{Steps: []core.Step{
		{
			Name: "container-step",
			Env:  []string{"STEP_ENV=from-step"},
			Container: &core.Container{
				Image: "alpine:3",
				Env:   []string{"CONTAINER_ENV=from-container"},
			},
		},
	}}
	nodes := []*runtime.Node{
		runtime.NodeWithData(runtime.NodeData{
			Step: core.Step{
				Name: "container-step",
				Container: &core.Container{
					Image: "alpine:3",
				},
			},
			State: runtime.NodeState{Status: core.NodeSucceeded},
		}),
	}

	p, err := runtime.CreateStepRetryPlan(dag, nodes, "container-step")
	require.NoError(t, err)
	require.NotNil(t, p)

	rebound := nodes[0].Step()
	require.Equal(t, core.NodeNotStarted, nodes[0].State().Status)
	require.Equal(t, []string{"STEP_ENV=from-step"}, rebound.Env)
	require.NotNil(t, rebound.Container)
	require.Equal(t, []string{"CONTAINER_ENV=from-container"}, rebound.Container.Env)
}

func TestStepRetryPlan(t *testing.T) {
	dag := &core.DAG{Steps: []core.Step{
		{Name: "1"}, {Name: "2", Depends: []string{"1"}}, {Name: "3", Depends: []string{"2"}},
		{Name: "4"}, {Name: "5", Depends: []string{"4"}}, {Name: "6", Depends: []string{"5"}}, {Name: "7", Depends: []string{"6"}},
	}}
	baseNodes := []*runtime.Node{
		makeNode("1", core.NodeSucceeded),
		makeNode("2", core.NodeFailed, "1"),
		makeNode("3", core.NodeAborted, "2"),
		makeNode("4", core.NodeSkipped),
		makeNode("5", core.NodeFailed, "4"),
		makeNode("6", core.NodeSucceeded, "5"),
		makeNode("7", core.NodeSkipped, "6"),
	}
	tests := []struct {
		name       string
		step       string
		wantStatus map[string]core.NodeStatus
	}{
		{
			name: "retry failed step",
			step: "2",
			wantStatus: map[string]core.NodeStatus{
				"1": core.NodeSucceeded,
				"2": core.NodeNotStarted,
				"3": core.NodeAborted,
				"4": core.NodeSkipped,
				"5": core.NodeFailed,
				"6": core.NodeSucceeded,
				"7": core.NodeSkipped,
			},
		},
		{
			name: "retry succeeded first",
			step: "1",
			wantStatus: map[string]core.NodeStatus{
				"1": core.NodeNotStarted,
				"2": core.NodeFailed,
				"3": core.NodeAborted,
				"4": core.NodeSkipped,
				"5": core.NodeFailed,
				"6": core.NodeSucceeded,
				"7": core.NodeSkipped,
			},
		},
		{
			name: "retry succeeded middle",
			step: "6",
			wantStatus: map[string]core.NodeStatus{
				"1": core.NodeSucceeded,
				"2": core.NodeFailed,
				"3": core.NodeAborted,
				"4": core.NodeSkipped,
				"5": core.NodeFailed,
				"6": core.NodeNotStarted,
				"7": core.NodeSkipped,
			},
		},
		{
			name: "retry succeeded last",
			step: "7",
			wantStatus: map[string]core.NodeStatus{
				"1": core.NodeSucceeded,
				"2": core.NodeFailed,
				"3": core.NodeAborted,
				"4": core.NodeSkipped,
				"5": core.NodeFailed,
				"6": core.NodeSucceeded,
				"7": core.NodeNotStarted,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// deep copy nodes (statuses) for isolation
			nodes := make([]*runtime.Node, 0, len(baseNodes))
			for _, n := range baseNodes {
				nodes = append(nodes, makeNode(n.Name(), n.State().Status, n.Step().Depends...))
			}
			p, err := runtime.CreateStepRetryPlan(dag, nodes, tt.step)
			require.NoError(t, err)
			require.NotNil(t, p)
			for _, n := range nodes {
				require.Equal(t, tt.wantStatus[n.Name()], n.State().Status, "status mismatch for %s", n.Name())
			}
		})
	}
}

func TestStepRetryPlan_PreservesRetryCountForRetryingStep(t *testing.T) {
	dag := &core.DAG{Steps: []core.Step{
		{Name: "retrying-step", RetryPolicy: core.RetryPolicy{Limit: 1}},
	}}
	nodes := []*runtime.Node{
		runtime.NodeWithData(runtime.NodeData{
			Step: dag.Steps[0],
			State: runtime.NodeState{
				Status:     core.NodeRetrying,
				RetryCount: 1,
			},
		}),
	}

	p, err := runtime.CreateStepRetryPlan(dag, nodes, "retrying-step")
	require.NoError(t, err)
	require.NotNil(t, p)
	require.Equal(t, core.NodeNotStarted, nodes[0].State().Status)
	require.Equal(t, 1, nodes[0].State().RetryCount)
}

func TestPlan_Timing(t *testing.T) {
	steps := []core.Step{{Name: "a"}}
	p, err := runtime.NewPlan(steps...)
	require.NoError(t, err)
	require.True(t, p.IsStarted())
	require.False(t, p.IsFinished())
	require.True(t, p.Duration() >= 0)
	p.Finish()
	require.True(t, p.IsFinished())
	finish := p.FinishAt()
	require.WithinDuration(t, time.Now(), finish, time.Second)
}

func TestPlan_NodeStates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		nodes             []*runtime.Node
		wantHasRunning    bool
		wantHasWaiting    bool
		wantHasNotStarted bool
		wantHasRejected   bool
	}{
		{
			name: "all succeeded",
			nodes: []*runtime.Node{
				makeNode("a", core.NodeSucceeded),
				makeNode("b", core.NodeSucceeded, "a"),
			},
		},
		{
			name: "one running",
			nodes: []*runtime.Node{
				makeNode("a", core.NodeSucceeded),
				makeNode("b", core.NodeRunning, "a"),
			},
			wantHasRunning: true,
		},
		{
			name: "one waiting with blocked dependents",
			nodes: []*runtime.Node{
				makeNode("a", core.NodeWaiting),
				makeNode("b", core.NodeNotStarted, "a"),
			},
			wantHasWaiting:    true,
			wantHasNotStarted: true,
		},
		{
			name: "one rejected",
			nodes: []*runtime.Node{
				makeNode("a", core.NodeRejected),
				makeNode("b", core.NodeNotStarted, "a"),
			},
			wantHasRejected:   true,
			wantHasNotStarted: true,
		},
		{
			name: "rejected and waiting together",
			nodes: []*runtime.Node{
				makeNode("a", core.NodeRejected),
				makeNode("b", core.NodeWaiting),
			},
			wantHasRejected: true,
			wantHasWaiting:  true,
		},
		{
			name: "mix of all states",
			nodes: []*runtime.Node{
				makeNode("a", core.NodeRunning),
				makeNode("b", core.NodeWaiting),
				makeNode("c", core.NodeNotStarted, "b"),
				makeNode("d", core.NodeSucceeded),
			},
			wantHasRunning:    true,
			wantHasWaiting:    true,
			wantHasNotStarted: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, err := runtime.NewPlanFromNodes(tt.nodes...)
			require.NoError(t, err)

			states := p.NodeStates()
			require.Equal(t, tt.wantHasRunning, states.HasRunning, "hasRunning")
			require.Equal(t, tt.wantHasWaiting, states.HasWaiting, "hasWaiting")
			require.Equal(t, tt.wantHasNotStarted, states.HasNotStarted, "hasNotStarted")
			require.Equal(t, tt.wantHasRejected, states.HasRejected, "hasRejected")
		})
	}
}

func TestPlan_WaitingStepNames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		nodes     []*runtime.Node
		wantNames []string
	}{
		{
			name: "no waiting",
			nodes: []*runtime.Node{
				makeNode("a", core.NodeSucceeded),
			},
			wantNames: nil,
		},
		{
			name: "one waiting",
			nodes: []*runtime.Node{
				makeNode("wait-step", core.NodeWaiting),
			},
			wantNames: []string{"wait-step"},
		},
		{
			name: "multiple waiting",
			nodes: []*runtime.Node{
				makeNode("wait-1", core.NodeWaiting),
				makeNode("wait-2", core.NodeWaiting),
				makeNode("not-waiting", core.NodeSucceeded),
			},
			wantNames: []string{"wait-1", "wait-2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, err := runtime.NewPlanFromNodes(tt.nodes...)
			require.NoError(t, err)

			names := p.WaitingStepNames()
			require.Equal(t, tt.wantNames, names)
		})
	}
}

func TestCreateRetryPlan_PrefersPersistedNodeWorkingDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		nodeDir string
	}{
		{name: "restored dag step dir changed"},
		{name: "persisted node step dir changed", nodeDir: "/stale-node-work"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dag := &core.DAG{
				Name: "retry-work-dir",
				Steps: []core.Step{
					{Name: "target", Dir: "/changed-work"},
				},
			}
			node := runtime.NodeWithData(runtime.NodeData{
				Step: core.Step{Name: "target", Dir: tt.nodeDir},
				State: runtime.NodeState{
					Status:     core.NodeFailed,
					WorkingDir: "/original-work",
				},
			})

			plan, err := runtime.CreateRetryPlan(context.Background(), dag, node)
			require.NoError(t, err)

			target := plan.GetNodeByName("target")
			require.NotNil(t, target)
			require.Equal(t, "/original-work", target.Step().Dir)
		})
	}
}
