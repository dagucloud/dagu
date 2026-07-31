// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package controller_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/core/spec"
	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/runtime/controller"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	// Registers the executors the child DAG in the round-trip test needs.
	_ "github.com/dagucloud/dagu/v2/internal/runtime/builtin"
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

// TestNewCatalog_HidesParametersTheStepPins covers the case that let a run pass
// while doing nothing: a step fixed the aspect to grade, the model saw the
// parameter anyway, sent an empty string for it, and the check graded against no
// criteria and reported clean.
func TestNewCatalog_HidesParametersTheStepPins(t *testing.T) {
	t.Parallel()

	dag := testDAG()
	dag.LocalDAGs = map[string]*core.DAG{
		"check": {
			Name: "check",
			ParamDefs: []core.ParamDef{
				{Name: "aspect", Type: core.ParamDefTypeString, Required: true},
				{Name: "strict", Type: core.ParamDefTypeString},
			},
		},
	}
	dag.Steps = []core.Step{
		{
			Name:   "check vocabulary",
			SubDAG: &core.SubDAG{Name: "check", Params: `aspect="vocabulary"`},
			Params: core.NewSimpleParams(map[string]string{"aspect": "vocabulary"}),
		},
		core.NewControllerStep(dag),
	}

	catalog, err := controller.NewCatalog(t.Context(), dag)
	require.NoError(t, err)

	params := catalog.Tools()[0].Function.Parameters
	properties, _ := params["properties"].(map[string]any)
	assert.NotContains(t, properties, "aspect", "the step decided this one")
	assert.Contains(t, properties, "strict", "the model still chooses the rest")
	assert.Empty(t, params["required"], "a pinned parameter is not asked for")
}

func TestNewCatalog_RejectsPositionalPinnedParameters(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(t.Context(), []byte(`
type: controller
llm: {provider: anthropic, model: claude-opus-5}
steps:
  - name: check vocabulary
    action: dag.run
    with:
      dag: check
      params: vocabulary
tasks:
  - name: checked
    description: The check ran.
---
name: check
params:
  - name: aspect
    type: string
    required: true
steps:
  - run: echo ${params.aspect}
`))
	require.NoError(t, err)

	_, err = controller.NewCatalog(t.Context(), dag)
	require.Error(t, err)
	assert.Contains(t, err.Error(),
		`step "check vocabulary": controller child DAG parameters must be named`)
}

func TestMergeParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		stepParams string
		args       map[string]any
		pinned     []string
		want       string
	}{
		{
			name:       "an argument naming a pinned parameter is dropped",
			stepParams: `aspect="vocabulary"`,
			args:       map[string]any{"aspect": ""},
			pinned:     []string{"aspect"},
			want:       `aspect="vocabulary"`,
		},
		{
			name:       "parameters the step pinned survive alongside chosen ones",
			stepParams: `aspect="vocabulary" strict="high"`,
			args:       map[string]any{"depth": 2},
			pinned:     []string{"aspect", "strict"},
			want:       `aspect="vocabulary" strict="high" depth="2"`,
		},
		{
			name: "arguments alone render as before",
			args: map[string]any{"target": "prod"},
			want: `target="prod"`,
		},
		{
			name:       "a step that pins everything ignores the arguments",
			stepParams: `aspect="vocabulary"`,
			pinned:     []string{"aspect"},
			want:       `aspect="vocabulary"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pinned := make(map[string]struct{}, len(tt.pinned))
			for _, name := range tt.pinned {
				pinned[name] = struct{}{}
			}
			assert.Equal(t, tt.want, controller.MergeParams(tt.stepParams, tt.args, pinned))
		})
	}
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
			expected: `alpha="a" zeta="z"`,
		},
		{
			name:     "WholeNumbersLoseTheirFraction",
			args:     map[string]any{"count": float64(3)},
			expected: `count="3"`,
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
			expected: `items="[\"a\",\"b\"]"`,
		},
		{
			name:     "Booleans",
			args:     map[string]any{"force": true},
			expected: `force="true"`,
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

// TestParamString_SurvivesTheChildParser is the contract that matters: whatever
// ParamString renders has to come back out of the parser the child DAG actually
// uses. Reasoning about the wrong parser is how quoting was got wrong before.
func TestParamString_SurvivesTheChildParser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args map[string]any
		want []string
	}{
		{
			name: "StructuredValueArrivesWhole",
			args: map[string]any{"items": []any{"a", "b"}},
			want: []string{`items=["a","b"]`},
		},
		{
			name: "ObjectArrivesWhole",
			args: map[string]any{"obj": map[string]any{"k": "v"}},
			want: []string{`obj={"k":"v"}`},
		},
		{
			name: "SpacesStayInOneParameter",
			args: map[string]any{"msg": "hello world"},
			want: []string{"msg=hello world"},
		},
		{
			name: "NumbersAndBoolsAreUnchanged",
			args: map[string]any{"count": float64(3), "force": true},
			want: []string{"count=3", "force=true"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dag, err := spec.LoadYAML(t.Context(),
				[]byte("steps:\n  - name: a\n    run: echo hi\n"),
				spec.WithParams(controller.ParamString(tt.args)))
			require.NoError(t, err)
			assert.Equal(t, tt.want, dag.Params)
		})
	}
}

// stubProvider records the request it was handed and answers with no tool call.
type stubProvider struct{ got *llm.ChatRequest }

func (p *stubProvider) Name() string { return "stub" }
func (p *stubProvider) Chat(_ context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	p.got = req
	return &llm.ChatResponse{Content: "done", FinishReason: "stop"}, nil
}
func (p *stubProvider) ChatStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamEvent, error) {
	return nil, nil
}

// TestPlanner_MasksTheOutboundCopy covers an outbound leak. The system prompt and
// task descriptions are resolved against the run scope, so a reference to a
// secret becomes the secret itself. Only the copy sent to the model is masked;
// the run keeps its transcript readable.
func TestPlanner_MasksTheOutboundCopy(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{}
	catalog, err := controller.NewCatalog(t.Context(), &core.DAG{
		Type:  core.TypeController,
		Steps: []core.Step{{Name: "alpha"}},
	})
	require.NoError(t, err)

	mask := func(msgs []exec.LLMMessage) []exec.LLMMessage {
		out := make([]exec.LLMMessage, len(msgs))
		for i, m := range msgs {
			out[i] = m
			out[i].Content = strings.ReplaceAll(m.Content, "super-secret", "***")
		}
		return out
	}

	planner := controller.NewPlanner(provider, &core.LLMConfig{Model: "m"}, catalog,
		"Authenticate with super-secret.", mask)

	state := controller.NewState(&core.DAG{Tasks: []core.ControllerTask{{Name: "t", Description: "d"}}})
	state.Append(exec.LLMMessage{Role: exec.RoleUser, Content: "token is super-secret"})

	_, err = planner.Next(t.Context(), state)
	require.NoError(t, err)
	require.NotNil(t, provider.got)

	for _, m := range provider.got.Messages {
		assert.NotContains(t, m.Content, "super-secret", "the model must not receive the raw value")
	}
	assert.Contains(t, transcriptOf(state), "super-secret",
		"the run's own transcript keeps the resolved text")
}

func transcriptOf(s *controller.State) string {
	var out strings.Builder
	for _, m := range s.Messages() {
		out.WriteString(m.Content + "\n")
	}
	return out.String()
}
