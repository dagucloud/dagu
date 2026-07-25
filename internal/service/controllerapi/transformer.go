// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package controllerapi converts Controller domain views to the public API contract.
package controllerapi

import (
	"time"

	api "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/controller"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

// Summary converts a compact Controller domain view.
func Summary(item controller.Summary) api.ControllerSummary {
	result := api.ControllerSummary{
		Id:                item.ID,
		Name:              item.Name,
		Workspace:         item.Workspace,
		Status:            api.Status(item.Status),
		StatusLabel:       api.StatusLabel(item.Status.String()),
		CurrentState:      item.CurrentState,
		TurnCount:         item.TurnCount,
		MaxTurns:          item.MaxTurns,
		WaitingQuestion:   cloneString(item.WaitingQuestion),
		ActiveDAGRun:      activeDAGRun(item.ActiveDAGRun),
		LatestDAGRun:      listDAGRun(item.LatestDAGRun),
		LastError:         cloneString(item.LastError),
		FinishedAt:        cloneTime(item.FinishedAt),
		ResourceUpdatedAt: item.ResourceUpdatedAt,
	}
	if item.Description != "" {
		result.Description = new(item.Description)
	}
	return result
}

func listDAGRun(run *controller.DAGRunRef) *api.ControllerListDAGRun {
	if run == nil {
		return nil
	}
	return &api.ControllerListDAGRun{
		State:    run.State,
		Dag:      run.DAG,
		DagRunId: run.DAGRunID,
	}
}

// Detail converts a Controller definition and its current-or-last runtime view.
func Detail(item controller.Detail) api.ControllerDetail {
	return api.ControllerDetail{
		Id:                item.Definition.ID,
		Definition:        definition(item.Definition),
		Runtime:           runtime(item.Runtime),
		DagRuns:           []api.DAGRunSummary{},
		Spec:              item.RawYAML,
		Warnings:          warnings(item.Warnings),
		ResourceUpdatedAt: item.ResourceUpdatedAt,
	}
}

func warnings(values []controller.DefinitionWarning) []api.ControllerWarning {
	result := make([]api.ControllerWarning, 0, len(values))
	for _, value := range values {
		result = append(result, api.ControllerWarning{
			Code:    value.Code,
			Path:    value.Path,
			Message: value.Message,
		})
	}
	return result
}

func definition(value controller.Definition) api.ControllerDefinition {
	states := make(map[string]api.ControllerState, len(value.States))
	for name, state := range value.States {
		transitions := make([]api.ControllerTransition, 0, len(state.Transitions))
		for _, transition := range state.Transitions {
			transitions = append(transitions, api.ControllerTransition{
				To:   transition.To,
				When: transition.When,
			})
		}
		converted := api.ControllerState{
			Description: state.Description,
			Dags:        nonNilStrings(state.DAGs),
			Transitions: transitions,
		}
		if state.Terminal != "" {
			terminal := api.ControllerStateTerminal(state.Terminal)
			converted.Terminal = &terminal
		}
		states[name] = converted
	}
	return api.ControllerDefinition{
		Type:        api.ControllerDefinitionType(value.Type),
		Version:     api.ControllerDefinitionVersion(value.Version),
		Id:          value.ID,
		Name:        value.Name,
		Description: value.Description,
		MaxTurns:    value.EffectiveMaxTurns(),
		Labels:      nonNilStrings(value.Labels),
		Llm: api.ControllerRouterLLMConfig{
			Provider: value.LLM.Provider,
			Model:    value.LLM.Model,
			System:   cloneString(value.LLM.System),
		},
		Dags:   nonNilStrings(value.DAGs),
		States: states,
	}
}

func runtime(value *controller.RuntimeView) api.ControllerRuntime {
	if value == nil {
		return api.ControllerRuntime{
			Status:       api.Status(core.NotStarted),
			StatusLabel:  api.StatusLabel(core.NotStarted.String()),
			CurrentState: "",
			TurnCount:    0,
			DagRunRefs:   []api.ControllerDAGRunRef{},
			Context:      []api.ControllerContextMessage{},
		}
	}
	result := api.ControllerRuntime{
		Status:          api.Status(value.Status),
		StatusLabel:     api.StatusLabel(value.Status.String()),
		CurrentState:    value.CurrentState,
		TurnCount:       value.TurnCount,
		WaitingQuestion: cloneString(value.WaitingQuestion),
		ActiveDAGRun:    activeDAGRun(value.ActiveDAGRun),
		DagRunRefs:      dagRunRefs(value.DAGRunRefs),
		Context:         contextMessages(value.Context),
		LastError:       cloneString(value.LastError),
		FinishedAt:      cloneTime(value.FinishedAt),
	}
	if !value.StartedAt.IsZero() {
		result.StartedAt = new(value.StartedAt)
	}
	if !value.UpdatedAt.IsZero() {
		result.UpdatedAt = new(value.UpdatedAt)
	}
	return result
}

func activeDAGRun(run *controller.DAGRunRef) *api.ControllerDAGRunRef {
	if run == nil {
		return nil
	}
	return &api.ControllerDAGRunRef{
		State:    run.State,
		Dag:      run.DAG,
		DagRunId: run.DAGRunID,
	}
}

func dagRunRefs(refs []controller.DAGRunRef) []api.ControllerDAGRunRef {
	result := make([]api.ControllerDAGRunRef, 0, len(refs))
	for _, ref := range refs {
		result = append(result, api.ControllerDAGRunRef{
			State:    ref.State,
			Dag:      ref.DAG,
			DagRunId: ref.DAGRunID,
		})
	}
	return result
}

func contextMessages(messages []exec.LLMMessage) []api.ControllerContextMessage {
	result := make([]api.ControllerContextMessage, 0, len(messages))
	for _, message := range messages {
		converted := api.ControllerContextMessage{
			Role: api.ControllerContextMessageRole(message.Role),
		}
		if message.Content != "" {
			converted.Content = new(message.Content)
		}
		if message.ToolCallID != "" {
			converted.ToolCallId = new(message.ToolCallID)
		}
		if len(message.ToolCalls) > 0 {
			calls := make([]api.ControllerToolCall, 0, len(message.ToolCalls))
			for _, call := range message.ToolCalls {
				calls = append(calls, api.ControllerToolCall{
					Id:   call.ID,
					Type: api.ControllerToolCallType(call.Type),
					Function: api.ControllerToolFunction{
						Name:      call.Function.Name,
						Arguments: call.Function.Arguments,
					},
				})
			}
			converted.ToolCalls = &calls
		}
		converted.Metadata = messageMetadata(message.Metadata)
		result = append(result, converted)
	}
	return result
}

func messageMetadata(metadata *exec.LLMMessageMetadata) *api.ChatMessageMetadata {
	if metadata == nil {
		return nil
	}
	result := &api.ChatMessageMetadata{}
	if metadata.Provider != "" {
		result.Provider = new(metadata.Provider)
	}
	if metadata.Model != "" {
		result.Model = new(metadata.Model)
	}
	if metadata.PromptTokens != 0 {
		result.PromptTokens = new(metadata.PromptTokens)
	}
	if metadata.CompletionTokens != 0 {
		result.CompletionTokens = new(metadata.CompletionTokens)
	}
	if metadata.TotalTokens != 0 {
		result.TotalTokens = new(metadata.TotalTokens)
	}
	return result
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return append([]string(nil), values...)
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	return new(*value)
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	return new(*value)
}
