// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package controllerapi

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/controller"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetailMapsControllerRuntimeAndContext(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	waitingQuestion := "Which region?"
	lastError := "router_unavailable"
	detail := &controller.Detail{
		RawYAML: "type: controller\n",
		Definition: controller.Definition{
			Type:        controller.DefinitionType,
			Version:     controller.DefinitionVersion,
			ID:          "ctrl_aaaaaaaaaaaaaaaa",
			Name:        "Incident flow",
			Description: "Route incidents.",
			LLM:         controller.ControllerRouterLLMConfig{Provider: "openai", Model: "gpt-4o"},
			DAGs:        []string{},
			States: map[string]controller.State{
				"done": {Terminal: "succeeded"},
			},
		},
		Runtime: &controller.RuntimeView{
			Status:          core.Waiting,
			CurrentState:    "needs_input",
			TurnCount:       2,
			WaitingQuestion: &waitingQuestion,
			ActiveDAGRun: &controller.DAGRunRef{
				State: "needs_input", DAG: "collect", DAGRunID: "run-1",
			},
			DAGRunRefs: []controller.DAGRunRef{{State: "default", DAG: "classify", DAGRunID: "run-0"}},
			Context: []exec.LLMMessage{{
				Role:    exec.RoleAssistant,
				Content: "routing",
				Metadata: &exec.LLMMessageMetadata{
					Provider: "openai", Model: "gpt-4o", TotalTokens: 12,
				},
			}},
			LastError:  &lastError,
			StartedAt:  now,
			UpdatedAt:  now,
			FinishedAt: &now,
		},
		Warnings: []controller.DefinitionWarning{{
			Code:    "unreachable_state",
			Path:    "states.orphaned",
			Message: "State is unreachable from default",
		}},
		ResourceUpdatedAt: now,
	}

	result := Detail(*detail)

	assert.Equal(t, controller.DefaultMaxTurns, result.Definition.MaxTurns)
	require.NotNil(t, result.Definition.States["done"].Terminal)
	assert.Equal(t, "succeeded", string(*result.Definition.States["done"].Terminal))
	require.NotNil(t, result.Runtime.ActiveDAGRun)
	assert.Equal(t, "needs_input", result.Runtime.ActiveDAGRun.State)
	require.Len(t, result.Runtime.DagRunRefs, 1)
	require.Len(t, result.Runtime.Context, 1)
	require.NotNil(t, result.Runtime.Context[0].Metadata)
	require.NotNil(t, result.Runtime.Context[0].Metadata.TotalTokens)
	assert.Equal(t, 12, *result.Runtime.Context[0].Metadata.TotalTokens)
	require.NotNil(t, result.Runtime.LastError)
	assert.Equal(t, lastError, *result.Runtime.LastError)
	require.NotNil(t, result.Runtime.FinishedAt)
	assert.Equal(t, now, *result.Runtime.FinishedAt)
	require.Len(t, result.Warnings, 1)
	assert.Equal(t, "unreachable_state", result.Warnings[0].Code)
	assert.Equal(t, "states.orphaned", result.Warnings[0].Path)
	assert.Equal(t, "State is unreachable from default", result.Warnings[0].Message)
}
