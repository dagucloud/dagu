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

func TestState_SettlingDrivesTermination(t *testing.T) {
	t.Parallel()

	state := controller.NewState(testDAG())
	require.False(t, state.Settled())
	assert.Equal(t, []string{"first", "second"}, state.OpenTaskNames())

	require.NoError(t, state.SetTaskStatus("first", controller.TaskCompleted, "done"))
	assert.Equal(t, []string{"second"}, state.OpenTaskNames())

	// A skipped task settles the goal without claiming it was achieved, and
	// leaves the run succeeding.
	require.NoError(t, state.SetTaskStatus("second", controller.TaskSkipped, "not needed"))
	assert.True(t, state.Settled())
	assert.Empty(t, state.FailedTasks())
}

func TestState_FailedTaskIsSettledButReported(t *testing.T) {
	t.Parallel()

	state := controller.NewState(testDAG())
	require.NoError(t, state.SetTaskStatus("first", controller.TaskCompleted, "done"))
	require.NoError(t, state.SetTaskStatus("second", controller.TaskFailed, "impossible"))

	assert.True(t, state.Settled(), "a failed task no longer needs attention")
	failed := state.FailedTasks()
	require.Len(t, failed, 1)
	assert.Equal(t, "second", failed[0].Name)
	assert.Equal(t, "impossible", failed[0].Reason)
}

func TestState_RejectsUnknownTaskAndRestatedStatus(t *testing.T) {
	t.Parallel()

	state := controller.NewState(testDAG())

	err := state.SetTaskStatus("nope", controller.TaskCompleted, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown task "nope"`)

	require.NoError(t, state.SetTaskStatus("first", controller.TaskCompleted, "done"))
	err = state.SetTaskStatus("first", controller.TaskCompleted, "again")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already completed")

	// Reopening is a change of status, so it is allowed.
	require.NoError(t, state.SetTaskStatus("first", controller.TaskOpen, "review rejected it"))
}

func TestLoadState_PreservesProgressAcrossAttempts(t *testing.T) {
	t.Parallel()

	state := controller.NewState(testDAG())
	require.NoError(t, state.SetTaskStatus("first", controller.TaskCompleted, "because"))
	state.RecordStepRun("alpha")
	state.Turns = 4

	raw, err := state.Marshal()
	require.NoError(t, err)

	messages := []exec.LLMMessage{{Role: exec.RoleAssistant, Content: "hello"}}
	restored, err := controller.LoadState(raw, messages, testDAG())
	require.NoError(t, err)

	assert.Equal(t, controller.TaskCompleted, restored.Tasks[0].Status)
	assert.Equal(t, "because", restored.Tasks[0].Reason)
	assert.Equal(t, controller.TaskOpen, restored.Tasks[1].Status)
	assert.Equal(t, 4, restored.Turns)
	assert.Equal(t, 1, restored.StepRunCount("alpha"))
	assert.Equal(t, messages, restored.Messages())
}

func TestLoadState_ReconcilesAnEditedTaskList(t *testing.T) {
	t.Parallel()

	state := controller.NewState(testDAG())
	require.NoError(t, state.SetTaskStatus("first", controller.TaskCompleted, "because"))
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
	assert.Equal(t, controller.TaskCompleted, restored.Tasks[0].Status)
	assert.Equal(t, "third", restored.Tasks[1].Name)
	assert.Equal(t, controller.TaskOpen, restored.Tasks[1].Status)
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
	assert.Equal(t, []string{
		"run_child", "review", "run_child_2",
		controller.AskUserTool, controller.SetTaskStatusTool,
	}, names)

	// The controller step is not one of the actions the model may pick.
	_, ok := catalog.StepFor(core.ControllerStepName)
	assert.False(t, ok)

	step, ok := catalog.StepFor("run_child_2")
	require.True(t, ok)
	assert.Equal(t, "run child", step)

	tools := catalog.Tools()
	require.Len(t, tools, 5)
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
			// The model writes these values, so anything the child DAG's param
			// splitter would re-interpret has to survive the trip.
			name:     "ValuesContainingQuotesAreQuoted",
			args:     map[string]any{"msg": `say"hi`},
			expected: `msg="say\"hi"`,
		},
		{
			name:     "ValuesContainingApostrophesAreQuoted",
			args:     map[string]any{"msg": "it's"},
			expected: `msg="it's"`,
		},
		{
			name:     "EmptyValuesAreQuoted",
			args:     map[string]any{"msg": ""},
			expected: `msg=""`,
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

// TestLoadState_RestoresARunSuspendedBeforeStatuses covers a run that was
// waiting on a person when the task model still carried a boolean.
func TestLoadState_RestoresARunSuspendedBeforeStatuses(t *testing.T) {
	t.Parallel()

	legacy := json.RawMessage(
		`{"tasks":[{"name":"first","done":true},{"name":"second"}],"turns":3}`)

	restored, err := controller.LoadState(legacy, nil, testDAG())
	require.NoError(t, err)

	assert.Equal(t, controller.TaskCompleted, restored.Tasks[0].Status)
	assert.Equal(t, controller.TaskOpen, restored.Tasks[1].Status)
	assert.False(t, restored.Settled())
}
