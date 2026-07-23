// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type routerTestProvider struct {
	request  *llm.ChatRequest
	response *llm.ChatResponse
	err      error
	calls    int
}

func (p *routerTestProvider) Chat(_ context.Context, request *llm.ChatRequest) (*llm.ChatResponse, error) {
	p.calls++
	p.request = request
	return p.response, p.err
}

func (*routerTestProvider) ChatStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamEvent, error) {
	return nil, errors.New("unexpected streaming call")
}

func (*routerTestProvider) Name() string { return "test" }

func TestRouterDecideBuildsContractAndValidatesRun(t *testing.T) {
	t.Parallel()

	provider := &routerTestProvider{response: routeResponse(`{"action":"run","next_state":"default","reason":"The alert is ready for classification.","dag":"classify","params":{"z":1,"a":"value"}}`)}
	definition := routerTestDefinition()
	customSystem := RouterInstructionPattern + "\n\nPrefer evidence."
	definition.LLM.System = &customSystem
	runtime := routerTestRuntime()
	router := NewRouter(
		func(ControllerRouterLLMConfig) (llm.Provider, error) { return provider, nil },
		func(_ context.Context, fileName string) (DAGMetadata, error) {
			return DAGMetadata{
				FileName:    fileName,
				Name:        fileName,
				Description: "Classifies an alert.",
				ParamDefs: []core.ParamDef{
					{Name: "a", Type: core.ParamDefTypeString, Required: true, Description: "Alert value"},
					{Name: "z", Type: core.ParamDefTypeInteger},
				},
			}, nil
		},
	)

	decision, err := router.Decide(context.Background(), definition, runtime)
	require.NoError(t, err)
	require.Equal(t, 1, provider.calls)
	require.NotNil(t, provider.request)
	assert.Equal(t, "model", provider.request.Model)
	assert.Equal(t, "required", provider.request.ToolChoice)
	require.Len(t, provider.request.Tools, 1)
	assert.Equal(t, routeToolName, provider.request.Tools[0].Function.Name)
	require.Len(t, provider.request.Messages, 4)
	assert.Equal(t, RouterInstructionV1+"\n\nPrefer evidence.", provider.request.Messages[0].Content)
	assertEnvelopeKind(t, provider.request.Messages[1].Content, "routing_control")
	var control struct {
		Payload struct {
			Status string `json:"status"`
		} `json:"payload"`
	}
	require.NoError(t, json.Unmarshal([]byte(provider.request.Messages[1].Content), &control))
	assert.Equal(t, "running", control.Payload.Status)
	assertEnvelopeKind(t, provider.request.Messages[2].Content, "workflow_metadata")
	assertEnvelopeKind(t, provider.request.Messages[3].Content, "user_directives")

	assert.Equal(t, "run", decision.Action)
	assert.Equal(t, "default", decision.NextState)
	assert.Equal(t, "classify", decision.DAG)
	assert.JSONEq(t, `{"a":"value","z":1}`, string(decision.Params))
	assert.Equal(t, "call-1", decision.ToolCallID)
	require.Len(t, decision.Assistant.ToolCalls, 1)
	assert.Equal(t, "openai", decision.Assistant.Metadata.Provider)
}

func TestRouterRejectsRunParamsOutsideCurrentDAGSchema(t *testing.T) {
	t.Parallel()

	provider := &routerTestProvider{response: routeResponse(`{"action":"run","next_state":"default","reason":"The alert is ready.","dag":"classify","params":{"unknown":"value"}}`)}
	router := NewRouter(
		func(ControllerRouterLLMConfig) (llm.Provider, error) { return provider, nil },
		func(_ context.Context, fileName string) (DAGMetadata, error) {
			return DAGMetadata{
				FileName:  fileName,
				Name:      fileName,
				ParamDefs: []core.ParamDef{{Name: "alert", Type: core.ParamDefTypeString, Required: true}},
			}, nil
		},
	)

	_, err := router.Decide(context.Background(), routerTestDefinition(), routerTestRuntime())
	assert.ErrorIs(t, err, ErrRouterDecision)
	assert.Equal(t, 1, provider.calls)
}

func TestRouterRejectsUnknownParamsFromRenderableDAGSchema(t *testing.T) {
	t.Parallel()

	provider := &routerTestProvider{response: routeResponse(`{"action":"run","next_state":"default","reason":"The alert is ready.","dag":"classify","params":{"alert":"A-123","unknown":"value"}}`)}
	router := NewRouter(
		func(ControllerRouterLLMConfig) (llm.Provider, error) { return provider, nil },
		func(_ context.Context, fileName string) (DAGMetadata, error) {
			return DAGMetadata{
				FileName:    fileName,
				Name:        fileName,
				ParamSchema: json.RawMessage(`{"type":"object","properties":{"alert":{"type":"string"}},"required":["alert"]}`),
			}, nil
		},
	)

	_, err := router.Decide(context.Background(), routerTestDefinition(), routerTestRuntime())

	assert.ErrorIs(t, err, ErrRouterDecision)
	assert.Equal(t, 1, provider.calls)
}

func TestParameterSchemaEnforcesObjectRoot(t *testing.T) {
	t.Parallel()

	schema, ok := parameterSchema(DAGMetadata{
		ParamSchema: json.RawMessage(`{"type":"string","properties":{"alert":{"type":"string"}}}`),
	}).(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "object", schema["type"])
	assert.Equal(t, false, schema["additionalProperties"])
}

func TestRouterPersistsCanonicalToolArguments(t *testing.T) {
	t.Parallel()

	provider := &routerTestProvider{response: routeResponse(`{"Action":"run","Next_State":"default","Reason":"The alert is ready.","DAG":"classify","Params":{"alert":"A-123"}}`)}
	router := NewRouter(
		func(ControllerRouterLLMConfig) (llm.Provider, error) { return provider, nil },
		func(_ context.Context, fileName string) (DAGMetadata, error) {
			return DAGMetadata{
				FileName:  fileName,
				Name:      fileName,
				ParamDefs: []core.ParamDef{{Name: "alert", Type: core.ParamDefTypeString, Required: true}},
			}, nil
		},
	)

	decision, err := router.Decide(context.Background(), routerTestDefinition(), routerTestRuntime())
	require.NoError(t, err)
	require.Len(t, decision.Assistant.ToolCalls, 1)
	assert.JSONEq(t,
		`{"action":"run","next_state":"default","reason":"The alert is ready.","dag":"classify","params":{"alert":"A-123"}}`,
		decision.Assistant.ToolCalls[0].Function.Arguments,
	)
}

func TestRouterMaterializesDefaultsWithoutRoundingNumbers(t *testing.T) {
	t.Parallel()

	provider := &routerTestProvider{response: routeResponse(`{"action":"run","next_state":"default","reason":"The alert is ready.","dag":"classify","params":{}}`)}
	router := NewRouter(
		func(ControllerRouterLLMConfig) (llm.Provider, error) { return provider, nil },
		func(_ context.Context, fileName string) (DAGMetadata, error) {
			return DAGMetadata{
				FileName: fileName,
				Name:     fileName,
				ParamDefs: []core.ParamDef{
					{Name: "sequence", Type: core.ParamDefTypeInteger, Default: int64(9007199254740993)},
					{Name: "region", Type: core.ParamDefTypeString, Default: "us-east"},
				},
			}, nil
		},
	)

	decision, err := router.Decide(context.Background(), routerTestDefinition(), routerTestRuntime())
	require.NoError(t, err)
	assert.Equal(t, `{"region":"us-east","sequence":9007199254740993}`, string(decision.Params))
	require.Len(t, decision.Assistant.ToolCalls, 1)
	arguments, err := decodeRouteArguments(decision.Assistant.ToolCalls[0].Function.Arguments)
	require.NoError(t, err)
	assert.Equal(t, string(decision.Params), string(arguments.Params))
}

func TestRouterNormalizesWhitespaceOnlyAssistantContent(t *testing.T) {
	t.Parallel()

	response := routeResponse(`{"action":"wait","next_state":"default","reason":"Input is missing.","question":"Which region?"}`)
	response.Content = " \n\t"
	provider := &routerTestProvider{response: response}
	router := NewRouter(func(ControllerRouterLLMConfig) (llm.Provider, error) {
		return provider, nil
	}, routerTestDAGResolver)

	decision, err := router.Decide(context.Background(), routerTestDefinition(), routerTestRuntime())

	require.NoError(t, err)
	assert.Empty(t, decision.Assistant.Content)
}

func TestRouterRejectsUntrustedResponseShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response *llm.ChatResponse
	}{
		{name: "prose", response: &llm.ChatResponse{Content: "I will run it", ToolCalls: routeResponse(`{"action":"wait","next_state":"default","reason":"Input is missing.","question":"Which region?"}`).ToolCalls}},
		{name: "no tool", response: &llm.ChatResponse{}},
		{name: "multiple tools", response: &llm.ChatResponse{ToolCalls: append(routeResponse(`{"action":"wait","next_state":"default","reason":"Input is missing.","question":"Which region?"}`).ToolCalls, routeResponse(`{"action":"wait","next_state":"default","reason":"Input is missing.","question":"Which region?"}`).ToolCalls...)}},
		{name: "unknown field", response: routeResponse(`{"action":"wait","next_state":"default","reason":"Input is missing.","question":"Which region?","status":"running"}`)},
		{name: "illegal state", response: routeResponse(`{"action":"wait","next_state":"missing","reason":"Input is missing.","question":"Which region?"}`)},
		{name: "run without params", response: routeResponse(`{"action":"run","next_state":"default","reason":"Evidence exists.","dag":"classify"}`)},
		{name: "complete nonterminal", response: routeResponse(`{"action":"complete","next_state":"default","reason":"Work is complete."}`)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			provider := &routerTestProvider{response: test.response}
			router := NewRouter(func(ControllerRouterLLMConfig) (llm.Provider, error) {
				return provider, nil
			}, routerTestDAGResolver)
			_, err := router.Decide(context.Background(), routerTestDefinition(), routerTestRuntime())
			assert.ErrorIs(t, err, ErrRouterDecision)
			assert.Equal(t, 1, provider.calls)
		})
	}
}

func TestRoutingOutcomeMessage(t *testing.T) {
	t.Parallel()

	waitMessage, err := RoutingOutcomeMessage(RouteDecision{Action: "wait", NextState: "default", ToolCallID: "wait-1"}, "")
	require.NoError(t, err)
	assert.Equal(t, exec.RoleTool, waitMessage.Role)
	assert.Equal(t, "wait-1", waitMessage.ToolCallID)
	assert.JSONEq(t, `{"kind":"routing_outcome","trust":"dagu_generated","source":"controller_runner","payload":{"action":"wait","outcome":"waiting_for_prompt","state":"default"}}`, waitMessage.Content)

	completeMessage, err := RoutingOutcomeMessage(RouteDecision{Action: "complete", NextState: "done", ToolCallID: "done-1"}, "succeeded")
	require.NoError(t, err)
	assert.JSONEq(t, `{"kind":"routing_outcome","trust":"dagu_generated","source":"controller_runner","payload":{"action":"complete","outcome":"completed","state":"done","status":"succeeded"}}`, completeMessage.Content)
}

func TestRouterHonorsPerTurnDeadline(t *testing.T) {
	t.Parallel()

	provider := &deadlineProvider{}
	router := NewRouter(func(ControllerRouterLLMConfig) (llm.Provider, error) {
		return provider, nil
	}, routerTestDAGResolver)
	router.timeout = time.Millisecond

	_, err := router.Decide(context.Background(), routerTestDefinition(), routerTestRuntime())
	assert.ErrorIs(t, err, ErrRouterCall)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestRouterRejectsNilProvider(t *testing.T) {
	t.Parallel()

	router := NewRouter(func(ControllerRouterLLMConfig) (llm.Provider, error) {
		return nil, nil
	}, routerTestDAGResolver)

	_, err := router.Decide(context.Background(), routerTestDefinition(), routerTestRuntime())

	assert.ErrorIs(t, err, ErrRouterCall)
}

func TestRouterDeadlineIncludesDAGResolution(t *testing.T) {
	t.Parallel()

	provider := &routerTestProvider{response: routeResponse(`{"action":"wait","next_state":"default","reason":"Input is missing.","question":"Which region?"}`)}
	router := NewRouter(func(ControllerRouterLLMConfig) (llm.Provider, error) {
		return provider, nil
	}, func(ctx context.Context, _ string) (DAGMetadata, error) {
		<-ctx.Done()
		return DAGMetadata{}, assert.AnError
	})
	router.timeout = time.Millisecond

	_, err := router.Decide(context.Background(), routerTestDefinition(), routerTestRuntime())

	assert.ErrorIs(t, err, ErrRouterCall)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Zero(t, provider.calls)
}

func TestRouterRejectsSuccessfulResponseAfterDeadline(t *testing.T) {
	t.Parallel()

	router := NewRouter(func(ControllerRouterLLMConfig) (llm.Provider, error) {
		return &lateSuccessProvider{}, nil
	}, routerTestDAGResolver)
	router.timeout = time.Millisecond

	_, err := router.Decide(context.Background(), routerTestDefinition(), routerTestRuntime())

	assert.ErrorIs(t, err, ErrRouterCall)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestRouterRejectsDAGMetadataDriftBeforeProviderCall(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		metadata DAGMetadata
	}{
		{name: "declared name", metadata: DAGMetadata{FileName: "classify", Name: "other"}},
		{name: "workspace", metadata: DAGMetadata{FileName: "classify", Name: "classify", Workspace: "security"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			provider := &routerTestProvider{response: routeResponse(`{"action":"wait","next_state":"default","reason":"Input is missing.","question":"Which region?"}`)}
			router := NewRouter(func(ControllerRouterLLMConfig) (llm.Provider, error) {
				return provider, nil
			}, func(context.Context, string) (DAGMetadata, error) {
				return test.metadata, nil
			})

			_, err := router.Decide(context.Background(), routerTestDefinition(), routerTestRuntime())

			assert.ErrorIs(t, err, ErrRouterCall)
			assert.Zero(t, provider.calls)
		})
	}
}

func TestRouterValidateCurrentParamsRejectsDAGMetadataDrift(t *testing.T) {
	t.Parallel()

	definition := routerTestDefinition()
	router := NewRouter(nil, func(context.Context, string) (DAGMetadata, error) {
		return DAGMetadata{FileName: "classify", Name: "classify", Workspace: "security"}, nil
	})

	_, err := router.ValidateCurrentParams(context.Background(), definition, "classify", json.RawMessage(`{}`))

	assert.ErrorIs(t, err, ErrRouterDecision)
	assert.Contains(t, err.Error(), "different workspace")
}

type deadlineProvider struct{}

func (*deadlineProvider) Chat(ctx context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (*deadlineProvider) ChatStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamEvent, error) {
	return nil, errors.New("unexpected streaming call")
}

func (*deadlineProvider) Name() string { return "deadline" }

type lateSuccessProvider struct{}

func (*lateSuccessProvider) Chat(ctx context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	<-ctx.Done()
	return routeResponse(`{"action":"wait","next_state":"default","reason":"Input is missing.","question":"Which region?"}`), nil
}

func (*lateSuccessProvider) ChatStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamEvent, error) {
	return nil, errors.New("unexpected streaming call")
}

func (*lateSuccessProvider) Name() string { return "late-success" }

func TestRouterFailsClosedWithoutDAGResolver(t *testing.T) {
	t.Parallel()

	provider := &routerTestProvider{response: routeResponse(`{"action":"wait","next_state":"default","reason":"Input is missing.","question":"Which region?"}`)}
	router := NewRouter(func(ControllerRouterLLMConfig) (llm.Provider, error) {
		return provider, nil
	}, nil)

	_, err := router.Decide(context.Background(), routerTestDefinition(), routerTestRuntime())

	assert.ErrorIs(t, err, ErrRouterCall)
	assert.Contains(t, err.Error(), "DAG metadata resolver is not configured")
	assert.Zero(t, provider.calls)
}

func routerTestDAGResolver(_ context.Context, fileName string) (DAGMetadata, error) {
	return DAGMetadata{FileName: fileName, Name: fileName}, nil
}

func routeResponse(arguments string) *llm.ChatResponse {
	return &llm.ChatResponse{
		ToolCalls: []llm.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: llm.ToolCallFunction{
				Name:      routeToolName,
				Arguments: arguments,
			},
		}},
		Usage: llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}
}

func routerTestDefinition() Definition {
	return Definition{
		Type: DefinitionType, Version: DefinitionVersion, ID: "ctrl_abcdefghijklmnop",
		Name: "Alert controller", Description: "Routes an alert.", MaxTurns: 10,
		LLM:  ControllerRouterLLMConfig{Provider: "openai", Model: "model"},
		DAGs: []string{"classify"},
		States: map[string]State{
			"default": {Description: "Classify the alert.", DAGs: []string{"classify"}, Transitions: []Transition{{To: "done", When: "Classification is complete."}}},
			"done":    {Description: "The work is complete.", Terminal: "succeeded"},
		},
	}
}

func routerTestRuntime() Runtime {
	return Runtime{
		RuntimeVersion: RuntimeVersion,
		ID:             "ctrl_abcdefghijklmnop",
		Status:         core.Running,
		CurrentState:   "default",
		TurnCount:      1,
		Context:        []exec.LLMMessage{{Role: exec.RoleUser, Content: "Investigate alert A-123."}},
	}
}

func assertEnvelopeKind(t *testing.T, content, expected string) {
	t.Helper()
	var envelope struct {
		Kind string `json:"kind"`
	}
	require.NoError(t, json.Unmarshal([]byte(content), &envelope))
	assert.Equal(t, expected, envelope.Kind)
}
