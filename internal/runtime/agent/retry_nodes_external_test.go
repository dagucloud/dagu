// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent_test

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	agent "github.com/dagucloud/dagu/v2/internal/runtime/agent"
	"github.com/stretchr/testify/require"
)

func TestRetryNodesUseRestoredDAGStepDefinition(t *testing.T) {
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
	status := &ir.DAGRunStatus{
		Nodes: []*ir.Node{
			{
				Step: ir.Step{
					Name:   "target",
					Dir:    "/stale/effective/work/dir",
					Script: "echo stale",
					Commands: []ir.CommandEntry{
						{Command: "echo", Args: []string{"stale"}, CmdWithArgs: "echo stale"},
					},
					Stdout: "/stale/stdout",
					Stderr: "/stale/stderr",
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
				Status:     ir.NodeFailed,
				Stdout:     "/persisted/stdout",
				Stderr:     "/persisted/stderr",
				WorkingDir: "/persisted/work/dir",
				RetryCount: 2,
			},
		},
	}

	nodes, err := agent.RetryNodesForTest(&ir.DAG{Steps: []ir.Step{sourceStep}}, status)
	require.NoError(t, err)
	require.Len(t, nodes, 1)

	require.Equal(t, sourceStep, nodes[0].Step())
	state := nodes[0].State()
	require.Equal(t, ir.NodeFailed, state.Status)
	require.Equal(t, "/persisted/stdout", state.Stdout)
	require.Equal(t, "/persisted/stderr", state.Stderr)
	require.Equal(t, "/persisted/work/dir", state.WorkingDir)
	require.Equal(t, 2, state.RetryCount)
}

func TestRetryNodesCarryPersistedHumanTask(t *testing.T) {
	t.Parallel()

	sourceStep := ir.Step{
		Name:      "review",
		HumanTask: &ir.HumanTaskConfig{Prompt: "Review ${params.target}"},
	}
	resolved := &ir.HumanTaskConfig{Prompt: "Review production"}
	status := &ir.DAGRunStatus{
		Nodes: []*ir.Node{
			{
				Step:   ir.Step{Name: "review", HumanTask: resolved},
				Status: ir.NodeWaiting,
			},
		},
	}

	nodes, err := agent.RetryNodesForTest(&ir.DAG{Steps: []ir.Step{sourceStep}}, status)
	require.NoError(t, err)
	require.Len(t, nodes, 1)

	require.Equal(t, resolved, nodes[0].Step().HumanTask)
	require.Equal(t, ir.NodeWaiting, nodes[0].State().Status)
}

func TestRetryNodesRejectMissingRestoredSourceStep(t *testing.T) {
	t.Parallel()

	status := &ir.DAGRunStatus{
		Nodes: []*ir.Node{
			{Step: ir.Step{Name: "missing"}, Status: ir.NodeFailed},
		},
	}

	_, err := agent.RetryNodesForTest(&ir.DAG{}, status)
	require.ErrorIs(t, err, runtime.ErrMissingNode)
}

// The retry plan, not retryNodes, decides which human tasks keep the carried
// snapshot. Push-back resets a task to not started without clearing its step.
func TestRetryPlanReopensResetHumanTaskFromTemplate(t *testing.T) {
	t.Parallel()

	sourceStep := ir.Step{
		Name:      "review",
		HumanTask: &ir.HumanTaskConfig{Prompt: "Review ${params.target}"},
	}
	status := &ir.DAGRunStatus{
		Nodes: []*ir.Node{
			{
				Step:   ir.Step{Name: "review", HumanTask: &ir.HumanTaskConfig{Prompt: "Review production"}},
				Status: ir.NodeNotStarted,
			},
		},
	}
	dag := &ir.DAG{Steps: []ir.Step{sourceStep}}

	nodes, err := agent.RetryNodesForTest(dag, status)
	require.NoError(t, err)
	_, err = runtime.CreateRetryPlan(context.Background(), dag, nodes...)
	require.NoError(t, err)

	require.Equal(t, sourceStep.HumanTask, nodes[0].Step().HumanTask)
}
