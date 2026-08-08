// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent_test

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
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
	status := &dagrun.DAGRunStatus{
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

func TestRetryNodesRejectMissingRestoredSourceStep(t *testing.T) {
	t.Parallel()

	status := &dagrun.DAGRunStatus{
		Nodes: []*ir.Node{
			{Step: ir.Step{Name: "missing"}, Status: ir.NodeFailed},
		},
	}

	_, err := agent.RetryNodesForTest(&ir.DAG{}, status)
	require.ErrorIs(t, err, runtime.ErrMissingNode)
}
