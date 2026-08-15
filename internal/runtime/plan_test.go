// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
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
func makeNode(name string, status ir.NodeStatus, depends ...string) *runtime.Node {
	return runtime.NodeWithData(runtime.NodeData{
		Step:  ir.Step{Name: name, Depends: depends},
		State: runtime.NodeState{Status: status},
	})
}

func TestPlan_Cyclic(t *testing.T) {
	step1 := ir.Step{Name: "1", Depends: []string{"2"}}
	step2 := ir.Step{Name: "2", Depends: []string{"1"}}
	_, err := runtime.NewPlan(step1, step2)
	require.Error(t, err)
	require.ErrorIs(t, err, runtime.ErrCyclicPlan)
}

func TestPlan_NodeByName(t *testing.T) {
	steps := []ir.Step{{Name: "a"}, {Name: "b", Depends: []string{"a"}}}
	p, err := runtime.NewPlan(steps...)
	require.NoError(t, err)
	require.NotNil(t, p.GetNodeByName("a"))
	require.NotNil(t, p.GetNodeByName("b"))
	require.Nil(t, p.GetNodeByName("c"))
}

func TestPlan_ExplicitDependencyRemainsExplicit(t *testing.T) {
	t.Parallel()

	plan, err := runtime.NewPlan(
		ir.Step{Name: "producer"},
		ir.Step{Name: "consumer", Depends: []string{"producer"}},
	)
	require.NoError(t, err)
	require.NoError(t, plan.AddInferredDependency("producer", "consumer"))

	producer := plan.GetNodeByName("producer")
	consumer := plan.GetNodeByName("consumer")
	require.False(t, plan.IsInferredDependency(producer.ID(), consumer.ID()))
}

func TestPlan_DependencyStructures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		steps             []ir.Step
		wantTotalDeps     int
		wantOutgoingCount int // nodes with dependents
		wantIncomingCount int // nodes with dependencies
	}{
		{
			name: "basic",
			steps: []ir.Step{
				{Name: "step1", Commands: []ir.CommandEntry{{Command: "echo", Args: []string{"1"}}}},
				{Name: "step2", Commands: []ir.CommandEntry{{Command: "echo", Args: []string{"2"}}}, Depends: []string{"step1"}},
				{Name: "step3", Commands: []ir.CommandEntry{{Command: "echo", Args: []string{"3"}}}, Depends: []string{"step2", "step1"}},
			},
			wantTotalDeps:     3, // 1->2,1->3,2->3
			wantOutgoingCount: 2, // step1 (has 2,3), step2 (has 3)
			wantIncomingCount: 2, // step2 (has 1), step3 (has 1,2)
		},
		{
			name: "single chain",
			steps: []ir.Step{
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
			steps: []ir.Step{
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
	dag := &ir.DAG{Steps: []ir.Step{
		{Name: "1"}, {Name: "2", Depends: []string{"1"}}, {Name: "3", Depends: []string{"2"}},
		{Name: "4"}, {Name: "5", Depends: []string{"4"}}, {Name: "6", Depends: []string{"5"}}, {Name: "7", Depends: []string{"6"}},
		{Name: "8"},
	}}
	nodes := []*runtime.Node{
		makeNode("1", ir.NodeSucceeded),
		makeNode("2", ir.NodeFailed, "1"),
		makeNode("3", ir.NodeAborted, "2"),
		makeNode("4", ir.NodeSkipped),
		makeNode("5", ir.NodeFailed, "4"),
		makeNode("6", ir.NodeSucceeded, "5"),
		makeNode("7", ir.NodeSkipped, "6"),
		makeNode("8", ir.NodeSkipped),
	}
	p, err := runtime.CreateRetryPlan(ctx, dag, nodes...)
	require.NoError(t, err)
	require.NotNil(t, p)
	// expectations based on upstream failures and aborted states triggering retry propagation
	require.Equal(t, ir.NodeSucceeded, nodes[0].State().Status)
	require.Equal(t, ir.NodeNotStarted, nodes[1].State().Status)
	require.Equal(t, ir.NodeNotStarted, nodes[2].State().Status)
	require.Equal(t, ir.NodeSkipped, nodes[3].State().Status)
	require.Equal(t, ir.NodeNotStarted, nodes[4].State().Status)
	require.Equal(t, ir.NodeNotStarted, nodes[5].State().Status)
	require.Equal(t, ir.NodeNotStarted, nodes[6].State().Status)
	require.Equal(t, ir.NodeSkipped, nodes[7].State().Status)
}

func TestRetryPlanWithRejectedNode(t *testing.T) {
	ctx := context.Background()
	dag := &ir.DAG{Steps: []ir.Step{
		{Name: "1"}, {Name: "2", Depends: []string{"1"}}, {Name: "3", Depends: []string{"2"}},
	}}

	// Create rejected node with metadata
	rejectedNode := runtime.NodeWithData(runtime.NodeData{
		Step: ir.Step{Name: "2", Depends: []string{"1"}},
		State: runtime.NodeState{
			Status:          ir.NodeRejected,
			RejectedAt:      "2024-01-15T10:00:00Z",
			RejectedBy:      "test-user",
			RejectedByID:    "user-1",
			RejectionReason: "test reason",
		},
	})

	nodes := []*runtime.Node{
		makeNode("1", ir.NodeSucceeded),
		rejectedNode,
		makeNode("3", ir.NodeAborted, "2"),
	}
	p, err := runtime.CreateRetryPlan(ctx, dag, nodes...)
	require.NoError(t, err)
	require.NotNil(t, p)

	// Rejected node should be cleared and retried
	require.Equal(t, ir.NodeSucceeded, nodes[0].State().Status)
	require.Equal(t, ir.NodeNotStarted, nodes[1].State().Status)
	require.Equal(t, ir.NodeNotStarted, nodes[2].State().Status)

	// Rejection metadata should be cleared
	require.Empty(t, rejectedNode.State().RejectedAt)
	require.Empty(t, rejectedNode.State().RejectedBy)
	require.Empty(t, rejectedNode.State().RejectedByID)
	require.Empty(t, rejectedNode.State().RejectionReason)
}

func TestRetryPlan_RebindsStepsFromRestoredDAG(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dag := &ir.DAG{Steps: []ir.Step{
		{Name: "approve"},
		{
			Name:    "container-step",
			Depends: []string{"approve"},
			Env:     []string{"STEP_ENV=from-step"},
			Container: &ir.Container{
				Image: "alpine:3",
				Env:   []string{"CONTAINER_ENV=from-container"},
			},
		},
	}}
	nodes := []*runtime.Node{
		makeNode("approve", ir.NodeSucceeded),
		runtime.NodeWithData(runtime.NodeData{
			Step: ir.Step{
				Name:    "container-step",
				Depends: []string{"approve"},
				Container: &ir.Container{
					Image: "alpine:3",
				},
			},
			State: runtime.NodeState{Status: ir.NodeNotStarted},
		}),
	}

	p, err := runtime.CreateRetryPlan(ctx, dag, nodes...)
	require.NoError(t, err)
	require.NotNil(t, p)

	rebound := nodes[1].Step()
	require.Equal(t, ir.NodeNotStarted, nodes[1].State().Status)
	require.Equal(t, []string{"STEP_ENV=from-step"}, rebound.Env)
	require.NotNil(t, rebound.Container)
	require.Equal(t, []string{"CONTAINER_ENV=from-container"}, rebound.Container.Env)
}

func TestStepRetryPlan_RebindsStepsFromRestoredDAG(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{Steps: []ir.Step{
		{
			Name: "container-step",
			Env:  []string{"STEP_ENV=from-step"},
			Container: &ir.Container{
				Image: "alpine:3",
				Env:   []string{"CONTAINER_ENV=from-container"},
			},
		},
	}}
	nodes := []*runtime.Node{
		runtime.NodeWithData(runtime.NodeData{
			Step: ir.Step{
				Name: "container-step",
				Container: &ir.Container{
					Image: "alpine:3",
				},
			},
			State: runtime.NodeState{Status: ir.NodeSucceeded},
		}),
	}

	p, err := runtime.CreateStepRetryPlan(dag, nodes, "container-step")
	require.NoError(t, err)
	require.NotNil(t, p)

	rebound := nodes[0].Step()
	require.Equal(t, ir.NodeNotStarted, nodes[0].State().Status)
	require.Equal(t, []string{"STEP_ENV=from-step"}, rebound.Env)
	require.NotNil(t, rebound.Container)
	require.Equal(t, []string{"CONTAINER_ENV=from-container"}, rebound.Container.Env)
}

func TestRetryPlan_ReplacesPersistedRuntimeMutatedStepSnapshot(t *testing.T) {
	t.Parallel()

	sourceStep := ir.Step{
		Name:   "target",
		Dir:    "${STEP_DIR}",
		Script: "echo source",
		Commands: []ir.CommandEntry{
			{Command: "echo", Args: []string{"source"}, CmdWithArgs: "echo source"},
		},
		Stdout: "/source/stdout",
		Stderr: "/source/stderr",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "command",
			Config: map[string]any{
				"shell": "bash",
			},
		},
		RetryPolicy: ir.RetryPolicy{
			LimitStr:       "${RETRY_LIMIT}",
			IntervalSecStr: "${RETRY_INTERVAL}",
		},
		RepeatPolicy: ir.RepeatPolicy{
			RepeatMode:  ir.RepeatModeUntil,
			LimitStr:    "${REPEAT_LIMIT}",
			IntervalStr: "${REPEAT_INTERVAL}",
		},
	}
	dag := &ir.DAG{
		Steps: []ir.Step{sourceStep},
	}
	node := runtime.NodeWithData(runtime.NodeData{
		Step: ir.Step{
			Name:   "target",
			Dir:    "/stale/effective/work/dir",
			Script: "echo evaluated",
			Commands: []ir.CommandEntry{
				{Command: "echo", Args: []string{"evaluated"}, CmdWithArgs: "echo evaluated"},
			},
			Stdout: "/stale/stdout",
			Stderr: "/stale/stderr",
			ExecutorConfig: ir.ExecutorConfig{
				Type: "command",
				Config: map[string]any{
					"shell": "sh",
				},
			},
			RetryPolicy: ir.RetryPolicy{
				Limit:    3,
				Interval: time.Second,
			},
			RepeatPolicy: ir.RepeatPolicy{
				RepeatMode: ir.RepeatModeWhile,
				Limit:      4,
				Interval:   time.Second,
			},
		},
		State: runtime.NodeState{
			Status:     ir.NodeFailed,
			WorkingDir: "/stale/effective/work/dir",
		},
	})

	plan, err := runtime.CreateRetryPlan(context.Background(), dag, node)
	require.NoError(t, err)

	target := plan.GetNodeByName("target")
	require.NotNil(t, target)
	require.Equal(t, sourceStep, target.Step())
	require.Equal(t, ir.NodeNotStarted, target.State().Status)
	require.Empty(t, target.State().WorkingDir)
}

func TestStepRetryPlan(t *testing.T) {
	dag := &ir.DAG{Steps: []ir.Step{
		{Name: "1"}, {Name: "2", Depends: []string{"1"}}, {Name: "3", Depends: []string{"2"}},
		{Name: "4"}, {Name: "5", Depends: []string{"4"}}, {Name: "6", Depends: []string{"5"}}, {Name: "7", Depends: []string{"6"}},
	}}
	baseNodes := []*runtime.Node{
		makeNode("1", ir.NodeSucceeded),
		makeNode("2", ir.NodeFailed, "1"),
		makeNode("3", ir.NodeAborted, "2"),
		makeNode("4", ir.NodeSkipped),
		makeNode("5", ir.NodeFailed, "4"),
		makeNode("6", ir.NodeSucceeded, "5"),
		makeNode("7", ir.NodeSkipped, "6"),
	}
	tests := []struct {
		name       string
		step       string
		wantStatus map[string]ir.NodeStatus
	}{
		{
			name: "retry failed step",
			step: "2",
			wantStatus: map[string]ir.NodeStatus{
				"1": ir.NodeSucceeded,
				"2": ir.NodeNotStarted,
				"3": ir.NodeAborted,
				"4": ir.NodeSkipped,
				"5": ir.NodeFailed,
				"6": ir.NodeSucceeded,
				"7": ir.NodeSkipped,
			},
		},
		{
			name: "retry succeeded first",
			step: "1",
			wantStatus: map[string]ir.NodeStatus{
				"1": ir.NodeNotStarted,
				"2": ir.NodeFailed,
				"3": ir.NodeAborted,
				"4": ir.NodeSkipped,
				"5": ir.NodeFailed,
				"6": ir.NodeSucceeded,
				"7": ir.NodeSkipped,
			},
		},
		{
			name: "retry succeeded middle",
			step: "6",
			wantStatus: map[string]ir.NodeStatus{
				"1": ir.NodeSucceeded,
				"2": ir.NodeFailed,
				"3": ir.NodeAborted,
				"4": ir.NodeSkipped,
				"5": ir.NodeFailed,
				"6": ir.NodeNotStarted,
				"7": ir.NodeSkipped,
			},
		},
		{
			name: "retry succeeded last",
			step: "7",
			wantStatus: map[string]ir.NodeStatus{
				"1": ir.NodeSucceeded,
				"2": ir.NodeFailed,
				"3": ir.NodeAborted,
				"4": ir.NodeSkipped,
				"5": ir.NodeFailed,
				"6": ir.NodeSucceeded,
				"7": ir.NodeNotStarted,
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
	dag := &ir.DAG{Steps: []ir.Step{
		{Name: "retrying-step", RetryPolicy: ir.RetryPolicy{Limit: 1}},
	}}
	nodes := []*runtime.Node{
		runtime.NodeWithData(runtime.NodeData{
			Step: dag.Steps[0],
			State: runtime.NodeState{
				Status:     ir.NodeRetrying,
				RetryCount: 1,
			},
		}),
	}

	p, err := runtime.CreateStepRetryPlan(dag, nodes, "retrying-step")
	require.NoError(t, err)
	require.NotNil(t, p)
	require.Equal(t, ir.NodeNotStarted, nodes[0].State().Status)
	require.Equal(t, 1, nodes[0].State().RetryCount)
}

func TestStepRetryPlanContinuesManagedAgentSession(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{Steps: []ir.Step{{Name: "implement"}}}
	node := runtime.NodeWithData(runtime.NodeData{
		Step: dag.Steps[0],
		State: runtime.NodeState{
			Status: ir.NodeFailed,
			AgentSession: &ir.AgentSession{
				Provider: "opencode", SessionID: "session-1", SessionOwned: true,
				OwnerWorkerID: "worker-a", State: ir.AgentSessionFailed, PromptSent: true,
			},
		},
	})

	_, err := runtime.CreateStepRetryPlan(dag, []*runtime.Node{node}, "implement")
	require.NoError(t, err)

	session := node.GetAgentSession()
	require.NotNil(t, session)
	require.Equal(t, "session-1", session.SessionID)
	require.Equal(t, "worker-a", session.OwnerWorkerID)
	require.Equal(t, ir.AgentSessionStarting, session.State)
	require.True(t, session.PromptSent)
}

func TestPlan_Timing(t *testing.T) {
	steps := []ir.Step{{Name: "a"}}
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
				makeNode("a", ir.NodeSucceeded),
				makeNode("b", ir.NodeSucceeded, "a"),
			},
		},
		{
			name: "one running",
			nodes: []*runtime.Node{
				makeNode("a", ir.NodeSucceeded),
				makeNode("b", ir.NodeRunning, "a"),
			},
			wantHasRunning: true,
		},
		{
			name: "one waiting with blocked dependents",
			nodes: []*runtime.Node{
				makeNode("a", ir.NodeWaiting),
				makeNode("b", ir.NodeNotStarted, "a"),
			},
			wantHasWaiting:    true,
			wantHasNotStarted: true,
		},
		{
			name: "one rejected",
			nodes: []*runtime.Node{
				makeNode("a", ir.NodeRejected),
				makeNode("b", ir.NodeNotStarted, "a"),
			},
			wantHasRejected:   true,
			wantHasNotStarted: true,
		},
		{
			name: "rejected and waiting together",
			nodes: []*runtime.Node{
				makeNode("a", ir.NodeRejected),
				makeNode("b", ir.NodeWaiting),
			},
			wantHasRejected: true,
			wantHasWaiting:  true,
		},
		{
			name: "mix of all states",
			nodes: []*runtime.Node{
				makeNode("a", ir.NodeRunning),
				makeNode("b", ir.NodeWaiting),
				makeNode("c", ir.NodeNotStarted, "b"),
				makeNode("d", ir.NodeSucceeded),
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
				makeNode("a", ir.NodeSucceeded),
			},
			wantNames: nil,
		},
		{
			name: "one waiting",
			nodes: []*runtime.Node{
				makeNode("wait-step", ir.NodeWaiting),
			},
			wantNames: []string{"wait-step"},
		},
		{
			name: "multiple waiting",
			nodes: []*runtime.Node{
				makeNode("wait-1", ir.NodeWaiting),
				makeNode("wait-2", ir.NodeWaiting),
				makeNode("not-waiting", ir.NodeSucceeded),
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

func TestCreateRetryPlan_PreservesExplicitStepWorkingDirOnly(t *testing.T) {
	t.Parallel()

	const explicitStepWorkDir = "explicit-step-work-dir"

	tests := []struct {
		name         string
		dagStepDir   string
		stateWorkDir string
		wantStepDir  string
		wantEnvDir   string
	}{
		{
			name:         "implicit step working dir ignores persisted effective work dir",
			stateWorkDir: "/var/lib/dagu/data/dag-runs/example/dag-runs/2026/06/04/dag-run_20260604_134911Z_run/work",
			wantStepDir:  "",
			wantEnvDir:   "fresh-run-work-dir",
		},
		{
			name:         "configured step working dir keeps restored source dir",
			dagStepDir:   explicitStepWorkDir,
			stateWorkDir: "/var/lib/dagu/data/dag-runs/example/work",
			wantStepDir:  explicitStepWorkDir,
			wantEnvDir:   explicitStepWorkDir,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dagStepDir := tt.dagStepDir
			wantStepDir := tt.wantStepDir
			wantEnvDir := tt.wantEnvDir
			if dagStepDir == explicitStepWorkDir {
				dagStepDir = t.TempDir()
				wantStepDir = dagStepDir
				wantEnvDir = dagStepDir
			}

			dag := &ir.DAG{
				Name: "retry-work-dir",
				Steps: []ir.Step{
					{Name: "target", Dir: dagStepDir},
				},
			}
			node := runtime.NodeWithData(runtime.NodeData{
				Step: ir.Step{Name: "target"},
				State: runtime.NodeState{
					Status:     ir.NodeFailed,
					WorkingDir: tt.stateWorkDir,
				},
			})
			freshRunWorkDir := t.TempDir()
			ctx := runtime.NewContext(
				context.Background(),
				dag,
				"retry-run",
				"dag.log",
				runtime.WithWorkDir(freshRunWorkDir),
			)

			plan, err := runtime.CreateRetryPlan(ctx, dag, node)
			require.NoError(t, err)

			target := plan.GetNodeByName("target")
			require.NotNil(t, target)
			require.Equal(t, wantStepDir, target.Step().Dir)

			if wantEnvDir == "fresh-run-work-dir" {
				wantEnvDir = freshRunWorkDir
			}
			env := runtime.NewPlanEnv(ctx, target.Step(), plan)
			require.Equal(t, wantEnvDir, env.WorkingDir)
		})
	}
}

func TestCreateRetryPlan_ExplicitVariableStepDirUsesRestoredSourceDefinition(t *testing.T) {
	t.Parallel()

	restoredStepWorkDir := t.TempDir()
	dag := &ir.DAG{
		Name: "retry-variable-work-dir",
		Env: []string{
			"STEP_WORK_DIR=" + restoredStepWorkDir,
		},
		Steps: []ir.Step{
			{Name: "target", Dir: "${STEP_WORK_DIR}"},
		},
	}
	node := runtime.NodeWithData(runtime.NodeData{
		Step: ir.Step{Name: "target", Dir: restoredStepWorkDir},
		State: runtime.NodeState{
			Status:     ir.NodeFailed,
			WorkingDir: "/stale/effective/work/dir",
		},
	})
	ctx := runtime.NewContext(context.Background(), dag, "retry-run", "dag.log")

	plan, err := runtime.CreateRetryPlan(ctx, dag, node)
	require.NoError(t, err)

	target := plan.GetNodeByName("target")
	require.NotNil(t, target)
	require.Equal(t, "${STEP_WORK_DIR}", target.Step().Dir)

	env := runtime.NewPlanEnv(ctx, target.Step(), plan)
	require.Equal(t, restoredStepWorkDir, env.WorkingDir)
}

func TestCreateRetryPlan_CommandStepUsesNewRunWorkDirAfterRetry(t *testing.T) {
	t.Parallel()

	staleRunWorkDir := "/var/lib/dagu/data/dag-runs/abc_build-c3c5/dag-runs/2026/06/04/dag-run_20260604_134911Z_019e92e5-2799-762b-9e13-6bb7eac6e62f/work"
	freshRunWorkDir := t.TempDir()
	dag := &ir.DAG{
		Name: "command-retry-work-dir",
		Steps: []ir.Step{
			{
				Name:   "build",
				Script: "echo ok",
			},
		},
	}
	node := runtime.NodeWithData(runtime.NodeData{
		Step: ir.Step{
			Name:   "build",
			Script: "echo ok",
		},
		State: runtime.NodeState{
			Status:     ir.NodeFailed,
			WorkingDir: staleRunWorkDir,
		},
	})
	ctx := runtime.NewContext(
		context.Background(),
		dag,
		"retry-run",
		"dag.log",
		runtime.WithWorkDir(freshRunWorkDir),
	)

	plan, err := runtime.CreateRetryPlan(ctx, dag, node)
	require.NoError(t, err)

	target := plan.GetNodeByName("build")
	require.NotNil(t, target)
	require.Empty(t, target.Step().Dir)

	env := runtime.NewPlanEnv(ctx, target.Step(), plan)
	require.Equal(t, freshRunWorkDir, env.WorkingDir)
	require.NotEqual(t, staleRunWorkDir, env.WorkingDir)
}
