// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agentloop_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/runtime/agentloop"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	// Registers the executors the child DAG in the round-trip test needs.
	_ "github.com/dagucloud/dagu/v2/internal/runtime/builtin"
)

func testDAG() *ir.DAG {
	return &ir.DAG{
		Type: ir.TypeAgent,
		Tasks: []ir.AgentTask{
			{Name: "first", Description: "one"},
			{Name: "second", Description: "two"},
		},
	}
}

func TestState_SettlingDrivesTermination(t *testing.T) {
	t.Parallel()

	state := agentloop.NewState(testDAG())
	require.False(t, state.Settled())
	assert.Equal(t, []string{"first", "second"}, state.OpenTaskNames())

	require.NoError(t, state.SetTaskStatus("first", agentloop.TaskCompleted, "done"))
	assert.Equal(t, []string{"second"}, state.OpenTaskNames())

	// A skipped task settles the goal without claiming it was achieved, and
	// leaves the run succeeding.
	require.NoError(t, state.SetTaskStatus("second", agentloop.TaskSkipped, "not needed"))
	assert.True(t, state.Settled())
	assert.Empty(t, state.FailedTasks())
}

func TestState_FailedTaskIsSettledButReported(t *testing.T) {
	t.Parallel()

	state := agentloop.NewState(testDAG())
	require.NoError(t, state.SetTaskStatus("first", agentloop.TaskCompleted, "done"))
	require.NoError(t, state.SetTaskStatus("second", agentloop.TaskFailed, "impossible"))

	assert.True(t, state.Settled(), "a failed task no longer needs attention")
	failed := state.FailedTasks()
	require.Len(t, failed, 1)
	assert.Equal(t, "second", failed[0].Name)
	assert.Equal(t, "impossible", failed[0].Reason)
}

func TestState_RejectsUnknownTaskAndRestatedStatus(t *testing.T) {
	t.Parallel()

	state := agentloop.NewState(testDAG())

	err := state.SetTaskStatus("nope", agentloop.TaskCompleted, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown task "nope"`)

	require.NoError(t, state.SetTaskStatus("first", agentloop.TaskCompleted, "done"))
	err = state.SetTaskStatus("first", agentloop.TaskCompleted, "again")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already completed")

	// Reopening is a change of status, so it is allowed.
	require.NoError(t, state.SetTaskStatus("first", agentloop.TaskOpen, "review rejected it"))
}

func TestLoadState_PreservesProgressAcrossAttempts(t *testing.T) {
	t.Parallel()

	state := agentloop.NewState(testDAG())
	require.NoError(t, state.SetTaskStatus("first", agentloop.TaskCompleted, "because"))
	state.RecordStepRun("alpha")
	state.Turns = 4

	raw, err := state.Marshal()
	require.NoError(t, err)

	messages := []ir.LLMMessage{{Role: ir.LLMRoleAssistant, Content: "hello"}}
	restored, err := agentloop.LoadState(raw, messages, testDAG())
	require.NoError(t, err)

	assert.Equal(t, agentloop.TaskCompleted, restored.Tasks[0].Status)
	assert.Equal(t, "because", restored.Tasks[0].Reason)
	assert.Equal(t, agentloop.TaskOpen, restored.Tasks[1].Status)
	assert.Equal(t, 4, restored.Turns)
	assert.Equal(t, 1, restored.StepRunCount("alpha"))
	assert.Equal(t, messages, restored.Messages())
}

func TestState_PendingBatchRoundTripsAndReadsLegacyState(t *testing.T) {
	t.Parallel()

	dag := testDAG()
	legacy, err := agentloop.LoadState(json.RawMessage(`{
  "tasks": [],
  "pending": {
    "toolCallId": "legacy_call",
    "toolName": "alpha",
    "step": "alpha"
  }
}`), nil, dag)
	require.NoError(t, err)
	assert.Equal(t, []agentloop.PendingAction{{
		ToolCallID: "legacy_call",
		ToolName:   "alpha",
		Step:       "alpha",
	}}, legacy.PendingBatch())

	want := []agentloop.PendingAction{
		{ToolCallID: "call_alpha", ToolName: "alpha", Step: "alpha"},
		{ToolCallID: "call_beta", ToolName: "beta", Step: "beta"},
	}
	legacy.SetPendingBatch(want)
	raw, err := legacy.Marshal()
	require.NoError(t, err)

	restored, err := agentloop.LoadState(raw, nil, dag)
	require.NoError(t, err)
	assert.Equal(t, want, restored.PendingBatch())
	restored.ClearPendingBatch()
	assert.Empty(t, restored.PendingBatch())
}

func TestState_FinalizeEventFallsBackToLegacyStep(t *testing.T) {
	t.Parallel()

	state := agentloop.State{Events: []agentloop.Event{
		{Name: "alpha", Status: ir.NodeWaiting.String()},
		{Name: "alpha", ToolCallID: "new_call", Status: ir.NodeWaiting.String()},
	}}

	state.FinalizeEvent("legacy_call", "alpha", ir.NodeSucceeded.String(), "finished", "")

	assert.Equal(t, ir.NodeSucceeded.String(), state.Events[0].Status)
	assert.Equal(t, "finished", state.Events[0].FinishedAt)
	assert.Equal(t, ir.NodeWaiting.String(), state.Events[1].Status)
}

func TestLoadState_ReconcilesAnEditedTaskList(t *testing.T) {
	t.Parallel()

	state := agentloop.NewState(testDAG())
	require.NoError(t, state.SetTaskStatus("first", agentloop.TaskCompleted, "because"))
	raw, err := state.Marshal()
	require.NoError(t, err)

	edited := &ir.DAG{
		Type: ir.TypeAgent,
		Tasks: []ir.AgentTask{
			{Name: "first", Description: "one"},
			{Name: "third", Description: "new goal"},
		},
	}

	restored, err := agentloop.LoadState(raw, nil, edited)
	require.NoError(t, err)

	// Progress on a surviving task is kept; a removed task does not linger and a
	// newly declared one starts open.
	require.Len(t, restored.Tasks, 2)
	assert.Equal(t, agentloop.TaskCompleted, restored.Tasks[0].Status)
	assert.Equal(t, "third", restored.Tasks[1].Name)
	assert.Equal(t, agentloop.TaskOpen, restored.Tasks[1].Status)
}

func TestTasksFromState_ToleratesUnusableState(t *testing.T) {
	t.Parallel()

	assert.Nil(t, agentloop.TasksFromState(nil))
	assert.Nil(t, agentloop.TasksFromState(json.RawMessage("not json")))
}

func TestNewCatalog(t *testing.T) {
	t.Parallel()

	dag := testDAG()
	dag.LocalDAGs = map[string]*ir.DAG{
		"child": {
			Name:        "child",
			Description: "the child workflow",
			ParamDefs:   []ir.ParamDef{{Name: "target", Type: ir.ParamDefTypeString, Required: true}},
		},
	}
	dag.Steps = []ir.Step{
		{Name: "run child", SubDAG: &ir.SubDAG{Name: "child"}},
		{Name: "review", HumanTask: &ir.HumanTaskConfig{Prompt: "ok?"}},
		{Name: "run child"}, // same identifier, so the tool name must be disambiguated
		ir.NewAgentStep(dag),
	}

	catalog, err := agentloop.NewCatalog(t.Context(), dag)
	require.NoError(t, err)

	names := catalog.ToolNames()
	assert.Equal(t, []string{
		"run_child", "review", "run_child_2",
		agentloop.AskUserTool, agentloop.SetTaskStatusTool,
	}, names)

	// The agent step is not one of the actions the model may pick.
	_, ok := catalog.StepFor(ir.AgentStepName)
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
	dag.LocalDAGs = map[string]*ir.DAG{
		"check": {
			Name: "check",
			ParamDefs: []ir.ParamDef{
				{Name: "aspect", Type: ir.ParamDefTypeString, Required: true},
				{Name: "strict", Type: ir.ParamDefTypeString},
			},
		},
	}
	dag.Steps = []ir.Step{
		{
			Name:   "check vocabulary",
			SubDAG: &ir.SubDAG{Name: "check", Params: `aspect="vocabulary"`},
			Params: ir.NewSimpleParams(map[string]string{"aspect": "vocabulary"}),
		},
		ir.NewAgentStep(dag),
	}

	catalog, err := agentloop.NewCatalog(t.Context(), dag)
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
type: agent
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

	_, err = agentloop.NewCatalog(t.Context(), dag)
	require.Error(t, err)
	assert.Contains(t, err.Error(),
		`step "check vocabulary": agent child DAG parameters must be named`)
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
			assert.Equal(t, tt.want, agentloop.MergeParams(tt.stepParams, tt.args, pinned))
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
			assert.Equal(t, tt.expected, agentloop.ParamString(tt.args))
		})
	}
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
				spec.WithParams(agentloop.ParamString(tt.args)))
			require.NoError(t, err)
			assert.Equal(t, tt.want, dag.Params)
		})
	}
}

// stubProvider records the request it was handed and returns a scripted response.
type stubProvider struct {
	got      *llm.ChatRequest
	response *llm.ChatResponse
}

func (p *stubProvider) Name() string { return "stub" }
func (p *stubProvider) Chat(_ context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	p.got = req
	if p.response != nil {
		return p.response, nil
	}
	return &llm.ChatResponse{Content: "done", FinishReason: "stop"}, nil
}
func (p *stubProvider) ChatStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamEvent, error) {
	return nil, nil
}

func TestPlanner_PreservesEveryActionCallInOneTurn(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{response: &llm.ChatResponse{
		FinishReason: "tool_calls",
		ToolCalls: []llm.ToolCall{
			{
				ID:   "call_alpha",
				Type: "function",
				Function: llm.ToolCallFunction{
					Name:      "alpha",
					Arguments: `{}`,
				},
			},
			{
				ID:   "call_beta",
				Type: "function",
				Function: llm.ToolCallFunction{
					Name:      "beta",
					Arguments: `{"target":"second"}`,
				},
			},
		},
	}}
	dag := &ir.DAG{
		Type: ir.TypeAgent,
		Steps: []ir.Step{
			{Name: "alpha"},
			{Name: "beta"},
		},
		Tasks: []ir.AgentTask{{Name: "done", Description: "Both actions ran."}},
	}
	catalog, err := agentloop.NewCatalog(t.Context(), dag)
	require.NoError(t, err)
	planner := agentloop.NewPlanner(
		provider, &ir.LLMConfig{Model: "test"}, catalog, "", nil)
	state := agentloop.NewState(dag)

	decisions, err := planner.Next(t.Context(), state)
	require.NoError(t, err)
	require.Len(t, decisions, 2)
	assert.Equal(t, "alpha", decisions[0].Step)
	assert.Equal(t, "beta", decisions[1].Step)
	assert.Equal(t, map[string]any{"target": "second"}, decisions[1].Args)

	messages := state.Messages()
	require.Len(t, messages, 1)
	require.Len(t, messages[0].ToolCalls, 2)
	assert.Equal(t, "call_alpha", messages[0].ToolCalls[0].ID)
	assert.Equal(t, "call_beta", messages[0].ToolCalls[1].ID)
	assert.Equal(t, 1, state.Turns)
}

// TestPlanner_MasksTheOutboundCopy covers an outbound leak. The system prompt and
// task descriptions are resolved against the run scope, so a reference to a
// secret becomes the secret itself. Only the copy sent to the model is masked;
// the run keeps its transcript readable.
func TestPlanner_MasksTheOutboundCopy(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{}
	catalog, err := agentloop.NewCatalog(t.Context(), &ir.DAG{
		Type:  ir.TypeAgent,
		Steps: []ir.Step{{Name: "alpha"}},
	})
	require.NoError(t, err)

	mask := func(msgs []ir.LLMMessage) []ir.LLMMessage {
		out := make([]ir.LLMMessage, len(msgs))
		for i, m := range msgs {
			out[i] = m
			out[i].Content = strings.ReplaceAll(m.Content, "super-secret", "***")
		}
		return out
	}

	planner := agentloop.NewPlanner(provider, &ir.LLMConfig{Model: "m"}, catalog,
		"Authenticate with super-secret.", mask)

	state := agentloop.NewState(&ir.DAG{Tasks: []ir.AgentTask{{Name: "t", Description: "d"}}})
	state.Append(ir.LLMMessage{Role: ir.LLMRoleUser, Content: "token is super-secret"})

	_, err = planner.Next(t.Context(), state)
	require.NoError(t, err)
	require.NotNil(t, provider.got)

	for _, m := range provider.got.Messages {
		assert.NotContains(t, m.Content, "super-secret", "the model must not receive the raw value")
	}
	assert.Contains(t, transcriptOf(state), "super-secret",
		"the run's own transcript keeps the resolved text")
}

func transcriptOf(s *agentloop.State) string {
	var out strings.Builder
	for _, m := range s.Messages() {
		out.WriteString(m.Content + "\n")
	}
	return out.String()
}
