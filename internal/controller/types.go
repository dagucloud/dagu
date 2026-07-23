// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package controller implements Controller definitions, lifecycle state, routing, and reconciliation.
package controller

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

const (
	DefinitionType           = "controller"
	DefinitionVersion        = 1
	RuntimeVersion           = 1
	DefaultStateName         = "default"
	DefaultMaxTurns          = 100
	MaxRuntimeBytes          = 1 << 20
	RouterInstructionPattern = "${{.RouterInstruction}}"
)

// Definition is the persisted Controller YAML model.
type Definition struct {
	Type        string                    `json:"type" yaml:"type"`
	Version     int                       `json:"version" yaml:"version"`
	ID          string                    `json:"id" yaml:"id"`
	Name        string                    `json:"name" yaml:"name"`
	Description string                    `json:"description" yaml:"description"`
	MaxTurns    int                       `json:"maxTurns,omitempty" yaml:"maxTurns,omitempty"`
	Labels      []string                  `json:"labels,omitempty" yaml:"labels,omitempty"`
	LLM         ControllerRouterLLMConfig `json:"llm" yaml:"llm"`
	DAGs        []string                  `json:"dags" yaml:"dags"`
	States      map[string]State          `json:"states" yaml:"states"`
}

// EffectiveMaxTurns returns the configured limit or the contract default.
func (d Definition) EffectiveMaxTurns() int {
	if d.MaxTurns == 0 {
		return DefaultMaxTurns
	}
	return d.MaxTurns
}

// Workspace returns the effective workspace. An empty value is the default workspace.
func (d Definition) Workspace() string {
	workspaceName, _ := exec.WorkspaceNameFromLabels(core.NewLabels(d.Labels))
	return workspaceName
}

// ControllerRouterLLMConfig is the strict Router-specific LLM profile.
type ControllerRouterLLMConfig struct {
	Provider string  `json:"provider" yaml:"provider"`
	Model    string  `json:"model" yaml:"model"`
	System   *string `json:"system,omitempty" yaml:"system,omitempty"`
}

// EffectiveSystem returns the configured template or the built-in placeholder-only default.
func (c ControllerRouterLLMConfig) EffectiveSystem() string {
	if c.System == nil {
		return RouterInstructionPattern
	}
	return *c.System
}

// State is one node in a Controller definition graph.
type State struct {
	Description string       `json:"description" yaml:"description"`
	DAGs        []string     `json:"dags,omitempty" yaml:"dags,omitempty"`
	Transitions []Transition `json:"transitions,omitempty" yaml:"transitions,omitempty"`
	Terminal    string       `json:"terminal,omitempty" yaml:"terminal,omitempty"`
}

// Transition describes an allowed state edge and its natural-language condition.
type Transition struct {
	To   string `json:"to" yaml:"to"`
	When string `json:"when" yaml:"when"`
}

// Runtime is the single current-or-last execution snapshot for a Controller.
type Runtime struct {
	RuntimeVersion  int               `json:"runtimeVersion"`
	ID              string            `json:"id"`
	Workspace       string            `json:"workspace"`
	Status          core.Status       `json:"status"`
	CurrentState    string            `json:"currentState"`
	TurnCount       int               `json:"turnCount"`
	WaitingQuestion *string           `json:"waitingQuestion"`
	ActiveDAGRun    *ActiveDAGRun     `json:"activeDAGRun"`
	DAGRunRefs      []DAGRunRef       `json:"dagRunRefs"`
	Context         []exec.LLMMessage `json:"context"`
	LastError       *string           `json:"lastError"`
	StartedAt       time.Time         `json:"startedAt"`
	UpdatedAt       time.Time         `json:"updatedAt"`
	FinishedAt      *time.Time        `json:"finishedAt"`
}

// ActiveDAGRun contains the durable request needed to enqueue or recover one child run.
// Params may contain secrets and must not be exposed through public API projections.
type ActiveDAGRun struct {
	ToolCallID string          `json:"toolCallId"`
	DAG        string          `json:"dag"`
	Params     json.RawMessage `json:"params"`
	DAGRunID   string          `json:"dagRunId"`
}

// DAGRunRef links the current runtime snapshot to an existing DAG run.
type DAGRunRef struct {
	State    string `json:"state"`
	DAG      string `json:"dag"`
	DAGRunID string `json:"dagRunId"`
}

// RuntimeView is the API-safe runtime projection. Executable child parameters are omitted.
type RuntimeView struct {
	Workspace       string            `json:"workspace"`
	Status          core.Status       `json:"status"`
	CurrentState    string            `json:"currentState"`
	TurnCount       int               `json:"turnCount"`
	WaitingQuestion *string           `json:"waitingQuestion"`
	ActiveDAGRun    *DAGRunRef        `json:"activeDAGRun"`
	DAGRunRefs      []DAGRunRef       `json:"dagRunRefs"`
	Context         []exec.LLMMessage `json:"context"`
	LastError       *string           `json:"lastError"`
	StartedAt       time.Time         `json:"startedAt"`
	UpdatedAt       time.Time         `json:"updatedAt"`
	FinishedAt      *time.Time        `json:"finishedAt"`
}

// Public returns a detached API-safe runtime projection.
func (r Runtime) Public() RuntimeView {
	view := RuntimeView{
		Workspace:       r.Workspace,
		Status:          r.Status,
		CurrentState:    r.CurrentState,
		TurnCount:       r.TurnCount,
		WaitingQuestion: cloneStringPointer(r.WaitingQuestion),
		DAGRunRefs:      append([]DAGRunRef(nil), r.DAGRunRefs...),
		Context:         publicContextMessages(r.Context),
		LastError:       cloneStringPointer(r.LastError),
		StartedAt:       r.StartedAt,
		UpdatedAt:       r.UpdatedAt,
		FinishedAt:      cloneTimePointer(r.FinishedAt),
	}
	if r.ActiveDAGRun != nil {
		view.ActiveDAGRun = &DAGRunRef{
			State:    r.CurrentState,
			DAG:      r.ActiveDAGRun.DAG,
			DAGRunID: r.ActiveDAGRun.DAGRunID,
		}
	}
	return view
}

// Summary is the compact list representation for one Controller.
type Summary struct {
	ID                string      `json:"id"`
	Name              string      `json:"name"`
	Description       string      `json:"description"`
	Workspace         string      `json:"workspace"`
	Status            core.Status `json:"status"`
	CurrentState      string      `json:"currentState,omitempty"`
	TurnCount         int         `json:"turnCount"`
	MaxTurns          int         `json:"maxTurns"`
	WaitingQuestion   *string     `json:"waitingQuestion"`
	ActiveDAGRun      *DAGRunRef  `json:"activeDAGRun"`
	LatestDAGRun      *DAGRunRef  `json:"latestDAGRun"`
	LastError         *string     `json:"lastError"`
	FinishedAt        *time.Time  `json:"finishedAt"`
	ResourceUpdatedAt time.Time   `json:"resourceUpdatedAt"`
}

// DefinitionWarning describes a non-blocking issue in a valid Controller definition.
type DefinitionWarning struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Detail contains the persisted definition and an API-safe runtime snapshot.
type Detail struct {
	RawYAML           string              `json:"spec"`
	Definition        Definition          `json:"definition"`
	Runtime           *RuntimeView        `json:"runtime"`
	Warnings          []DefinitionWarning `json:"warnings"`
	ResourceUpdatedAt time.Time           `json:"resourceUpdatedAt"`
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func publicContextMessages(messages []exec.LLMMessage) []exec.LLMMessage {
	result := make([]exec.LLMMessage, len(messages))
	for index, message := range messages {
		result[index] = message
		if message.Metadata != nil {
			metadata := *message.Metadata
			result[index].Metadata = &metadata
		}
		if len(message.ToolCalls) == 0 {
			continue
		}
		result[index].ToolCalls = append([]exec.ToolCall(nil), message.ToolCalls...)
		for callIndex := range result[index].ToolCalls {
			call := &result[index].ToolCalls[callIndex]
			if call.Function.Name == routeToolName {
				call.Function.Arguments = redactRouteParams(call.Function.Arguments)
			}
		}
	}
	return result
}

func redactRouteParams(arguments string) string {
	var values map[string]json.RawMessage
	if err := json.Unmarshal([]byte(arguments), &values); err != nil {
		return `{}`
	}
	redacted := false
	for key := range values {
		if strings.EqualFold(key, "params") {
			delete(values, key)
			redacted = true
		}
	}
	if !redacted {
		return arguments
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return `{}`
	}
	return string(encoded)
}
