// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package transform_test

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/collections"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/transform"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNodeFieldsRoundTrip(t *testing.T) {
	outputVars := &collections.SyncMap{}
	outputVars.Store("KEY", "KEY=value")

	original := &exec.Node{
		Step:            core.Step{Name: "test-step"},
		Status:          core.NodeSucceeded,
		Stdout:          "/tmp/stdout.log",
		Stderr:          "/tmp/stderr.log",
		WorkingDir:      "/tmp/original-work",
		StartedAt:       "2024-01-15T10:00:00Z",
		FinishedAt:      "2024-01-15T10:05:00Z",
		RetriedAt:       "2024-01-15T10:01:00Z",
		RetryCount:      2,
		DoneCount:       3,
		Repeated:        true,
		Error:           "test error",
		SubRuns:         []exec.SubDAGRun{{DAGRunID: "sub-1", Params: "p1"}},
		SubRunsRepeated: []exec.SubDAGRun{{DAGRunID: "sub-2", Params: "p2"}},
		OutputVariables: outputVars,
		ApprovalInputs:  map[string]string{"input1": "value1"},
		ApprovedAt:      "2024-01-15T10:02:00Z",
		ApprovedBy:      "admin",
		RejectedAt:      "2024-01-15T10:03:00Z",
		RejectedBy:      "reviewer",
		RejectionReason: "test rejection reason",
	}

	// Round-trip: execution.Node -> runtime.Node -> execution.Node
	runtimeNode := transform.ToNode(original)
	state := runtimeNode.State()

	dag := &core.DAG{Name: "test", Steps: []core.Step{original.Step}}
	status := transform.NewStatusBuilder(dag).Create("run-1", core.Succeeded, 0, time.Now(),
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
	original := &exec.Node{
		Step:   core.Step{Name: "chat-step"},
		Status: core.NodeSucceeded,
		ChatMessages: []exec.LLMMessage{
			{Role: exec.RoleSystem, Content: "You are helpful."},
			{Role: exec.RoleUser, Content: "Hello!"},
			{Role: exec.RoleAssistant, Content: "Hi there!", Metadata: &exec.LLMMessageMetadata{
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
	assert.Equal(t, exec.RoleSystem, state.ChatMessages[0].Role)
	assert.Equal(t, "You are helpful.", state.ChatMessages[0].Content)
	assert.Nil(t, state.ChatMessages[0].Metadata)

	assert.Equal(t, exec.RoleUser, state.ChatMessages[1].Role)
	assert.Equal(t, "Hello!", state.ChatMessages[1].Content)

	assert.Equal(t, exec.RoleAssistant, state.ChatMessages[2].Role)
	assert.Equal(t, "Hi there!", state.ChatMessages[2].Content)
	assert.NotNil(t, state.ChatMessages[2].Metadata)
	assert.Equal(t, "openai", state.ChatMessages[2].Metadata.Provider)
	assert.Equal(t, "gpt-4", state.ChatMessages[2].Metadata.Model)
	assert.Equal(t, 5, state.ChatMessages[2].Metadata.PromptTokens)
	assert.Equal(t, 3, state.ChatMessages[2].Metadata.CompletionTokens)
	assert.Equal(t, 8, state.ChatMessages[2].Metadata.TotalTokens)

	// Verify round-trip through status builder
	dag := &core.DAG{Name: "test", Steps: []core.Step{original.Step}}
	status := transform.NewStatusBuilder(dag).Create("run-1", core.Succeeded, 0, time.Now(),
		transform.WithNodes([]runtime.NodeData{{Step: original.Step, State: state}}))

	result := status.Nodes[0]

	// Verify ChatMessages are preserved in exec.Node
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
	original := &exec.Node{
		Step:   core.Step{Name: "no-chat-step"},
		Status: core.NodeSucceeded,
		// No ChatMessages
	}

	runtimeNode := transform.ToNode(original)
	state := runtimeNode.State()

	// Verify nil ChatMessages remain nil
	assert.Nil(t, state.ChatMessages)

	dag := &core.DAG{Name: "test", Steps: []core.Step{original.Step}}
	status := transform.NewStatusBuilder(dag).Create("run-1", core.Succeeded, 0, time.Now(),
		transform.WithNodes([]runtime.NodeData{{Step: original.Step, State: state}}))

	result := status.Nodes[0]
	assert.Nil(t, result.ChatMessages)
}
