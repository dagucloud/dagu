// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package transform_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/collections"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/transform"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNodeFieldsRoundTrip(t *testing.T) {
	outputVars := &collections.SyncMap{}
	outputVars.Store("KEY", "KEY=value")
	statusDetails := []ir.NodeStatusDetail{{Label: "customer-a", Status: ir.NodeFailed}}

	original := &ir.Node{
		Step: ir.Step{
			Name:      "test-step",
			HumanTask: &ir.HumanTaskConfig{Prompt: "Review production deployment"},
		},
		Status:                 ir.NodeSucceeded,
		Stdout:                 "/tmp/stdout.log",
		Stderr:                 "/tmp/stderr.log",
		WorkingDir:             "/tmp/original-work",
		StartedAt:              "2024-01-15T10:00:00Z",
		FinishedAt:             "2024-01-15T10:05:00Z",
		RetriedAt:              "2024-01-15T10:01:00Z",
		RetryCount:             2,
		DoneCount:              3,
		Repeated:               true,
		Error:                  "test error",
		StatusDetails:          statusDetails,
		SubRuns:                []ir.SubDAGRun{{DAGRunID: "sub-1", Params: "p1"}},
		SubRunsRepeated:        []ir.SubDAGRun{{DAGRunID: "sub-2", Params: "p2"}},
		OutputVariables:        outputVars,
		HumanTaskInput:         json.RawMessage(`{"window":"2026-07-20T12:00:00Z"}`),
		HumanTaskCompletedBy:   "operator",
		HumanTaskCompletedByID: "user-1",
		ApprovalInputs:         map[string]string{"input1": "value1"},
		ApprovedAt:             "2024-01-15T10:02:00Z",
		ApprovedBy:             "admin",
		ApprovedByID:           "user-2",
		RejectedAt:             "2024-01-15T10:03:00Z",
		RejectedBy:             "reviewer",
		RejectedByID:           "user-3",
		RejectionReason:        "test rejection reason",
	}

	// Round-trip: execution.Node -> runtime.Node -> execution.Node
	runtimeNode := transform.ToNode(original)
	state := runtimeNode.State()
	require.Len(t, state.StatusDetails, 1)
	assert.Equal(t, "customer-a", state.StatusDetails[0].Label)
	assert.Equal(t, ir.NodeFailed, state.StatusDetails[0].Status)

	dag := &ir.DAG{Name: "test", Steps: []ir.Step{original.Step}}
	status := dagrun.NewStatusBuilder(dag).Create("run-1", ir.Succeeded, 0, time.Now(),
		transform.WithNodes([]runtime.NodeData{{Step: original.Step, State: state}}))

	result := status.Nodes[0]

	// OutputVariables is a pointer, compare separately
	assert.Equal(t, original.OutputVariables, result.OutputVariables)

	// Compare rest of the struct
	original.OutputVariables = nil
	result.OutputVariables = nil
	assert.Equal(t, original, result)
}

func TestNodeChatMessagesRoundTrip(t *testing.T) {
	original := &ir.Node{
		Step:   ir.Step{Name: "chat-step"},
		Status: ir.NodeSucceeded,
		ChatMessages: []ir.LLMMessage{
			{Role: ir.LLMRoleSystem, Content: "You are helpful."},
			{Role: ir.LLMRoleUser, Content: "Hello!"},
			{Role: ir.LLMRoleAssistant, Content: "Hi there!", Metadata: &ir.LLMMessageMetadata{
				Provider:         "openai",
				Model:            "gpt-4",
				PromptTokens:     5,
				CompletionTokens: 3,
				TotalTokens:      8,
			}},
		},
	}

	// Round-trip: execution.Node -> runtime.Node -> execution.Node
	runtimeNode := transform.ToNode(original)
	state := runtimeNode.State()

	// Verify ChatMessages are preserved in runtime.NodeState
	require.Len(t, state.ChatMessages, 3)
	assert.Equal(t, ir.LLMRoleSystem, state.ChatMessages[0].Role)
	assert.Equal(t, "You are helpful.", state.ChatMessages[0].Content)
	assert.Nil(t, state.ChatMessages[0].Metadata)

	assert.Equal(t, ir.LLMRoleUser, state.ChatMessages[1].Role)
	assert.Equal(t, "Hello!", state.ChatMessages[1].Content)

	assert.Equal(t, ir.LLMRoleAssistant, state.ChatMessages[2].Role)
	assert.Equal(t, "Hi there!", state.ChatMessages[2].Content)
	assert.NotNil(t, state.ChatMessages[2].Metadata)
	assert.Equal(t, "openai", state.ChatMessages[2].Metadata.Provider)
	assert.Equal(t, "gpt-4", state.ChatMessages[2].Metadata.Model)
	assert.Equal(t, 5, state.ChatMessages[2].Metadata.PromptTokens)
	assert.Equal(t, 3, state.ChatMessages[2].Metadata.CompletionTokens)
	assert.Equal(t, 8, state.ChatMessages[2].Metadata.TotalTokens)

	// Verify round-trip through status builder
	dag := &ir.DAG{Name: "test", Steps: []ir.Step{original.Step}}
	status := dagrun.NewStatusBuilder(dag).Create("run-1", ir.Succeeded, 0, time.Now(),
		transform.WithNodes([]runtime.NodeData{{Step: original.Step, State: state}}))

	result := status.Nodes[0]

	// Verify ChatMessages are preserved in ir.Node
	require.Len(t, result.ChatMessages, 3)
	assert.Equal(t, original.ChatMessages[0].Role, result.ChatMessages[0].Role)
	assert.Equal(t, original.ChatMessages[0].Content, result.ChatMessages[0].Content)
	assert.Equal(t, original.ChatMessages[1].Role, result.ChatMessages[1].Role)
	assert.Equal(t, original.ChatMessages[1].Content, result.ChatMessages[1].Content)
	assert.Equal(t, original.ChatMessages[2].Role, result.ChatMessages[2].Role)
	assert.Equal(t, original.ChatMessages[2].Content, result.ChatMessages[2].Content)
	assert.NotNil(t, result.ChatMessages[2].Metadata)
	assert.Equal(t, original.ChatMessages[2].Metadata.Provider, result.ChatMessages[2].Metadata.Provider)
	assert.Equal(t, original.ChatMessages[2].Metadata.Model, result.ChatMessages[2].Metadata.Model)
	assert.Equal(t, original.ChatMessages[2].Metadata.TotalTokens, result.ChatMessages[2].Metadata.TotalTokens)
}

func TestNodeEmptyChatMessages(t *testing.T) {
	// Test that nodes without ChatMessages work correctly
	original := &ir.Node{
		Step:   ir.Step{Name: "no-chat-step"},
		Status: ir.NodeSucceeded,
		// No ChatMessages
	}

	runtimeNode := transform.ToNode(original)
	state := runtimeNode.State()

	// Verify nil ChatMessages remain nil
	assert.Nil(t, state.ChatMessages)

	dag := &ir.DAG{Name: "test", Steps: []ir.Step{original.Step}}
	status := dagrun.NewStatusBuilder(dag).Create("run-1", ir.Succeeded, 0, time.Now(),
		transform.WithNodes([]runtime.NodeData{{Step: original.Step, State: state}}))

	result := status.Nodes[0]
	assert.Nil(t, result.ChatMessages)
}
