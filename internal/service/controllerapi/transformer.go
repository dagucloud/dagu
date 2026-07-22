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
		StatusLabel:       api.StatusLabel(item.StatusLabel),
		CurrentState:      item.CurrentState,
		TurnCount:         item.TurnCount,
		MaxTurns:          item.MaxTurns,
		WaitingQuestion:   cloneString(item.WaitingQuestion),
		ActiveDAGRun:      activeDAGRun(item.ActiveDAGRun),
		LatestDAGRun:      listDAGRun(item.LatestDAGRun),
		LastError:         lastError(item.LastError),
		FinishedAt:        cloneTime(item.FinishedAt),
		ResourceUpdatedAt: time.Time{},
	}
	if item.Description != "" {
		result.Description = stringPointer(item.Description)
	}
	if item.ResourceUpdatedAt != nil {
		result.ResourceUpdatedAt = *item.ResourceUpdatedAt
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
func Detail(item *controller.Detail) api.ControllerDetail {
	if item == nil {
		return api.ControllerDetail{
			DagRuns:  []api.DAGRunSummary{},
			Errors:   []api.ControllerValidationIssue{},
			Warnings: []api.ControllerValidationIssue{},
		}
	}
	runtime := Runtime(item.Runtime)
	return api.ControllerDetail{
		Id:                item.Definition.ID,
		Definition:        Definition(item.Definition),
		Runtime:           runtime,
		DagRuns:           []api.DAGRunSummary{},
		Spec:              item.RawYAML,
		Errors:            []api.ControllerValidationIssue{},
		Warnings:          []api.ControllerValidationIssue{},
		ResourceUpdatedAt: item.ResourceUpdatedAt,
	}
}

// Definition converts the parsed Controller YAML model.
func Definition(definition controller.Definition) api.ControllerDefinition {
	states := make(map[string]api.ControllerState, len(definition.States))
	for name, state := range definition.States {
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
	result := api.ControllerDefinition{
		Type:        api.ControllerDefinitionType(definition.Type),
		Version:     api.ControllerDefinitionVersion(definition.Version),
		Id:          definition.ID,
		Name:        definition.Name,
		Description: definition.Description,
		MaxTurns:    definition.EffectiveMaxTurns(),
		Labels:      nonNilStrings(definition.Labels),
		Llm: api.ControllerRouterLLMConfig{
			Provider: api.ControllerRouterLLMConfigProvider(definition.LLM.Provider),
			Model:    definition.LLM.Model,
			System:   cloneString(definition.LLM.System),
		},
		Dags:   nonNilStrings(definition.DAGs),
		States: states,
	}
	return result
}

// Runtime converts an API-safe Controller runtime projection.
func Runtime(runtime *controller.RuntimeView) api.ControllerRuntime {
	if runtime == nil {
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
		Status:          api.Status(runtime.Status),
		StatusLabel:     api.StatusLabel(runtime.StatusLabel),
		CurrentState:    runtime.CurrentState,
		TurnCount:       runtime.TurnCount,
		WaitingQuestion: cloneString(runtime.WaitingQuestion),
		ActiveDAGRun:    activeDAGRun(runtime.ActiveDAGRun),
		DagRunRefs:      dagRunRefs(runtime.DAGRunRefs),
		Context:         contextMessages(runtime.Context),
		LastError:       lastError(runtime.LastError),
		FinishedAt:      cloneTime(runtime.FinishedAt),
	}
	if !runtime.StartedAt.IsZero() {
		result.StartedAt = timePointer(runtime.StartedAt)
	}
	if !runtime.UpdatedAt.IsZero() {
		result.UpdatedAt = timePointer(runtime.UpdatedAt)
	}
	return result
}

// ValidationIssues converts structured definition errors.
func ValidationIssues(issues []controller.ValidationIssue) []api.ControllerValidationIssue {
	result := make([]api.ControllerValidationIssue, 0, len(issues))
	for _, issue := range issues {
		converted := api.ControllerValidationIssue{
			Code:    issue.Code,
			Path:    issue.Path,
			Message: issue.Message,
		}
		if issue.Line > 0 {
			converted.Line = intPointer(issue.Line)
		}
		if issue.Column > 0 {
			converted.Column = intPointer(issue.Column)
		}
		result = append(result, converted)
	}
	return result
}

func activeDAGRun(run *controller.PublicActiveDAGRun) *api.ControllerDAGRunRef {
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
			converted.Content = stringPointer(message.Content)
		}
		if message.ToolCallID != "" {
			converted.ToolCallId = stringPointer(message.ToolCallID)
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
		result.Provider = stringPointer(metadata.Provider)
	}
	if metadata.Model != "" {
		result.Model = stringPointer(metadata.Model)
	}
	if metadata.PromptTokens != 0 {
		result.PromptTokens = intPointer(metadata.PromptTokens)
	}
	if metadata.CompletionTokens != 0 {
		result.CompletionTokens = intPointer(metadata.CompletionTokens)
	}
	if metadata.TotalTokens != 0 {
		result.TotalTokens = intPointer(metadata.TotalTokens)
	}
	return result
}

func lastError(code *string) *api.ControllerLastError {
	if code == nil {
		return nil
	}
	return &api.ControllerLastError{
		Code: *code,
	}
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
	return stringPointer(*value)
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	return timePointer(*value)
}

func stringPointer(value string) *string     { return &value }
func timePointer(value time.Time) *time.Time { return &value }
func intPointer(value int) *int              { return &value }
