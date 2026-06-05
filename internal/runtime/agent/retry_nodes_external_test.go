// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent_test

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	agent "github.com/dagucloud/dagu/internal/runtime/agent"
	"github.com/stretchr/testify/require"
)

func TestRetryNodesUseRestoredDAGStepDefinition(t *testing.T) {
	t.Parallel()

	sourceStep := core.Step{
		Name:   "target",
		Dir:    "${STEP_DIR}",
		Script: "echo source",
		Commands: []core.CommandEntry{
			{Command: "echo", Args: []string{"source"}, CmdWithArgs: "echo source"},
		},
		Stdout: "/source/stdout",
		Stderr: "/source/stderr",
		RetryPolicy: core.RetryPolicy{
			LimitStr:       "${RETRY_LIMIT}",
			IntervalSecStr: "${RETRY_INTERVAL}",
		},
		RepeatPolicy: core.RepeatPolicy{
			RepeatMode:  core.RepeatModeUntil,
			LimitStr:    "${REPEAT_LIMIT}",
			IntervalStr: "${REPEAT_INTERVAL}",
		},
	}
	status := &exec.DAGRunStatus{
		Nodes: []*exec.Node{
			{
				Step: core.Step{
					Name:   "target",
					Dir:    "/stale/effective/work/dir",
					Script: "echo stale",
					Commands: []core.CommandEntry{
						{Command: "echo", Args: []string{"stale"}, CmdWithArgs: "echo stale"},
					},
					Stdout: "/stale/stdout",
					Stderr: "/stale/stderr",
					RetryPolicy: core.RetryPolicy{
						Limit:    3,
						Interval: time.Second,
					},
					RepeatPolicy: core.RepeatPolicy{
						RepeatMode: core.RepeatModeWhile,
						Limit:      4,
						Interval:   time.Second,
					},
				},
				Status:     core.NodeFailed,
				Stdout:     "/persisted/stdout",
				Stderr:     "/persisted/stderr",
				WorkingDir: "/persisted/work/dir",
				RetryCount: 2,
			},
		},
	}

	nodes, err := agent.RetryNodesForTest(&core.DAG{Steps: []core.Step{sourceStep}}, status)
	require.NoError(t, err)
	require.Len(t, nodes, 1)

	require.Equal(t, sourceStep, nodes[0].Step())
	state := nodes[0].State()
	require.Equal(t, core.NodeFailed, state.Status)
	require.Equal(t, "/persisted/stdout", state.Stdout)
	require.Equal(t, "/persisted/stderr", state.Stderr)
	require.Equal(t, "/persisted/work/dir", state.WorkingDir)
	require.Equal(t, 2, state.RetryCount)
}

func TestRetryNodesRejectMissingRestoredSourceStep(t *testing.T) {
	t.Parallel()

	status := &exec.DAGRunStatus{
		Nodes: []*exec.Node{
			{Step: core.Step{Name: "missing"}, Status: core.NodeFailed},
		},
	}

	_, err := agent.RetryNodesForTest(&core.DAG{}, status)
	require.ErrorIs(t, err, runtime.ErrMissingNode)
}
