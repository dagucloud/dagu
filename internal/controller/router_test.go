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
		RouterProviderFactoryFunc(func(ControllerRouterLLMConfig) (llm.Provider, error) { return provider, nil }),
		RoutingDAGResolverFunc(func(_ context.Context, fileName string) (RoutingDAG, error) {
			return RoutingDAG{
				FileName:    fileName,
				Description: "Classifies an alert.",
				ParamDefs: []core.ParamDef{
					{Name: "a", Type: core.ParamDefTypeString, Required: true, Description: "Alert value"},
					{Name: "z", Type: core.ParamDefTypeInteger},
				},
			}, nil
		}),
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
		RouterProviderFactoryFunc(func(ControllerRouterLLMConfig) (llm.Provider, error) { return provider, nil }),
		RoutingDAGResolverFunc(func(_ context.Context, fileName string) (RoutingDAG, error) {
			return RoutingDAG{
				FileName:  fileName,
				ParamDefs: []core.ParamDef{{Name: "alert", Type: core.ParamDefTypeString, Required: true}},
			}, nil
		}),
	)

	_, err := router.Decide(context.Background(), routerTestDefinition(), routerTestRuntime())
	assert.ErrorIs(t, err, ErrRouterDecision)
	assert.Equal(t, 1, provider.calls)
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
			router := NewRouter(RouterProviderFactoryFunc(func(ControllerRouterLLMConfig) (llm.Provider, error) {
				return provider, nil
			}), nil)
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
	router := NewRouter(RouterProviderFactoryFunc(func(ControllerRouterLLMConfig) (llm.Provider, error) {
		return provider, nil
	}), nil)
	router.SetTimeout(time.Millisecond)

	_, err := router.Decide(context.Background(), routerTestDefinition(), routerTestRuntime())
	assert.ErrorIs(t, err, ErrRouterCall)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
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
