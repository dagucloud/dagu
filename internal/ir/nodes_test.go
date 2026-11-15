// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/require"
)

// The child of a command step with nodes runs that step alone, bound to a
// node. What the parent step applies stays with the parent.
func TestNodeStepChild(t *testing.T) {
	t.Parallel()

	clean := ir.Step{
		Name:          "clean",
		Commands:      []ir.CommandEntry{{CmdWithArgs: "rm -rf /tmp/app-cache"}},
		Nodes:         &ir.NodeTargets{Selector: "web", MaxConcurrent: 1},
		Depends:       []string{"build"},
		Preconditions: []*ir.Condition{{Condition: "true"}},
		ContinueOn:    ir.ContinueOn{Failure: true},
		Output:        "RESULT",
		RetryPolicy:   ir.RetryPolicy{Limit: 2},
	}
	parent := &ir.DAG{
		Name:           "deploy",
		Env:            []string{"APP=web"},
		Steps:          []ir.Step{{Name: "build"}, clean},
		HandlerOn:      ir.HandlerOn{Failure: &ir.Step{Name: "notify"}},
		SSH:            &ir.SSHConfig{Host: "bastion"},
		WorkerSelector: map[string]string{"gpu": "true"},
		Schedule:       []ir.Schedule{{Expression: "0 * * * *"}},
	}

	child, err := parent.NodeStepChild("clean")
	require.NoError(t, err)
	require.True(t, child.NodeRequired)
	require.Equal(t, "deploy", child.Name)
	require.Equal(t, []string{"APP=web"}, child.Env)
	require.Len(t, child.Steps, 1)

	step := child.Steps[0]
	require.Equal(t, clean.Commands, step.Commands)
	require.Equal(t, clean.RetryPolicy, step.RetryPolicy)
	require.Nil(t, step.Nodes)
	require.Empty(t, step.Depends)
	require.Empty(t, step.Preconditions)
	require.Zero(t, step.ContinueOn)
	require.Empty(t, step.Output)

	require.Nil(t, child.HandlerOn.Failure)
	require.Nil(t, child.SSH)
	require.Nil(t, child.WorkerSelector)
	require.Empty(t, child.Schedule)
	require.Len(t, parent.Steps, 2, "the parent keeps its steps")

	_, err = parent.NodeStepChild("build")
	require.ErrorContains(t, err, `step "build" is not a command step with nodes`)
	_, err = parent.NodeStepChild("missing")
	require.ErrorContains(t, err, `step "missing" not found`)
}
