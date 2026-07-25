// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package controller_test

import (
	"encoding/json"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime/controller"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testDAG() *core.DAG {
	return &core.DAG{
		Type: core.TypeController,
		Tasks: []core.ControllerTask{
			{Name: "first", Description: "one"},
			{Name: "second", Description: "two"},
		},
	}
}

func TestState_CompletionDrivesTermination(t *testing.T) {
	t.Parallel()

	state := controller.NewState(testDAG())
	require.False(t, state.AllDone())
	assert.Equal(t, []string{"first", "second"}, state.OpenTaskNames())

	require.NoError(t, state.CompleteTask("first", "done"))
	assert.Equal(t, []string{"second"}, state.OpenTaskNames())

	require.NoError(t, state.CompleteTask("second", "done"))
	assert.True(t, state.AllDone())
}

func TestState_RejectsUnknownAndRepeatedCompletion(t *testing.T) {
	t.Parallel()

	state := controller.NewState(testDAG())

	err := state.CompleteTask("nope", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown task "nope"`)

	require.NoError(t, state.CompleteTask("first", "done"))
	err = state.CompleteTask("first", "again")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already complete")
}

func TestLoadState_PreservesProgressAcrossAttempts(t *testing.T) {
	t.Parallel()

	state := controller.NewState(testDAG())
	require.NoError(t, state.CompleteTask("first", "because"))
	state.RecordStepRun("alpha")
	state.Turns = 4

	raw, err := state.Marshal()
	require.NoError(t, err)

	messages := []exec.LLMMessage{{Role: exec.RoleAssistant, Content: "hello"}}
	restored, err := controller.LoadState(raw, messages, testDAG())
	require.NoError(t, err)

	assert.True(t, restored.Tasks[0].Done)
	assert.Equal(t, "because", restored.Tasks[0].Reason)
	assert.False(t, restored.Tasks[1].Done)
	assert.Equal(t, 4, restored.Turns)
	assert.Equal(t, 1, restored.StepRunCount("alpha"))
	assert.Equal(t, messages, restored.Messages())
}

func TestLoadState_ReconcilesAnEditedTaskList(t *testing.T) {
	t.Parallel()

	state := controller.NewState(testDAG())
	require.NoError(t, state.CompleteTask("first", "because"))
	raw, err := state.Marshal()
	require.NoError(t, err)

	edited := &core.DAG{
		Type: core.TypeController,
		Tasks: []core.ControllerTask{
			{Name: "first", Description: "one"},
			{Name: "third", Description: "new goal"},
		},
	}

	restored, err := controller.LoadState(raw, nil, edited)
	require.NoError(t, err)

	// Progress on a surviving task is kept; a removed task does not linger and a
	// newly declared one starts open.
	require.Len(t, restored.Tasks, 2)
	assert.True(t, restored.Tasks[0].Done)
	assert.Equal(t, "third", restored.Tasks[1].Name)
	assert.False(t, restored.Tasks[1].Done)
}

func TestTasksFromState_ToleratesUnusableState(t *testing.T) {
	t.Parallel()

	assert.Nil(t, controller.TasksFromState(nil))
	assert.Nil(t, controller.TasksFromState(json.RawMessage("not json")))
}

func TestNewCatalog(t *testing.T) {
	t.Parallel()

	dag := testDAG()
	dag.LocalDAGs = map[string]*core.DAG{
		"child": {
			Name:        "child",
			Description: "the child workflow",
			ParamDefs:   []core.ParamDef{{Name: "target", Type: core.ParamDefTypeString, Required: true}},
		},
	}
	dag.Steps = []core.Step{
		{Name: "run child", SubDAG: &core.SubDAG{Name: "child"}},
		{Name: "review", HumanTask: &core.HumanTaskConfig{Prompt: "ok?"}},
		{Name: "run child"}, // same identifier, so the tool name must be disambiguated
		core.NewControllerStep(dag),
	}

	catalog, err := controller.NewCatalog(t.Context(), dag)
	require.NoError(t, err)

	names := catalog.ToolNames()
	assert.Equal(t, []string{"run_child", "review", "run_child_2", controller.CompleteTaskTool}, names)

	// The controller step is not one of the actions the model may pick.
	_, ok := catalog.StepFor(core.ControllerStepName)
	assert.False(t, ok)

	step, ok := catalog.StepFor("run_child_2")
	require.True(t, ok)
	assert.Equal(t, "run child", step)

	tools := catalog.Tools()
	require.Len(t, tools, 4)
	assert.Equal(t, "the child workflow", tools[0].Function.Description)
	assert.Equal(t, []string{"target"}, tools[0].Function.Parameters["required"])
	assert.Contains(t, tools[1].Function.Description, "ok?")
}

func TestParamString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     map[string]any
		expected string
	}{
		{name: "Empty", args: nil, expected: ""},
		{
			name:     "SortedForStableChildRunIDs",
			args:     map[string]any{"zeta": "z", "alpha": "a"},
			expected: "alpha=a zeta=z",
		},
		{
			name:     "WholeNumbersLoseTheirFraction",
			args:     map[string]any{"count": float64(3)},
			expected: "count=3",
		},
		{
			name:     "ValuesWithSpacesAreQuoted",
			args:     map[string]any{"msg": "hello world"},
			expected: `msg="hello world"`,
		},
		{
			name:     "StructuredValuesBecomeJSON",
			args:     map[string]any{"items": []any{"a", "b"}},
			expected: `items=["a","b"]`,
		},
		{
			name:     "Booleans",
			args:     map[string]any{"force": true},
			expected: "force=true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, controller.ParamString(tt.args))
		})
	}
}
