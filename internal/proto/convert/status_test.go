// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package convert

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/collections"
	"github.com/dagucloud/dagu/v2/internal/ir"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDAGRunStatusToProto(t *testing.T) {
	t.Run("nil status", func(t *testing.T) {
		result, err := DAGRunStatusToProto(nil)
		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("basic status", func(t *testing.T) {
		status := &ir.DAGRunStatus{
			Name:     "test-dag",
			DAGRunID: "run-123",
			Status:   ir.Running,
		}

		result, err := DAGRunStatusToProto(status)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.NotEmpty(t, result.JsonData)
	})
}

func TestProtoToDAGRunStatus(t *testing.T) {
	t.Run("nil proto", func(t *testing.T) {
		result, err := ProtoToDAGRunStatus(nil)
		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("empty json_data", func(t *testing.T) {
		proto := &coordinatorv1.DAGRunStatusProto{JsonData: ""}
		result, err := ProtoToDAGRunStatus(proto)
		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("invalid json_data", func(t *testing.T) {
		proto := &coordinatorv1.DAGRunStatusProto{JsonData: "not valid json"}
		result, err := ProtoToDAGRunStatus(proto)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "failed to unmarshal")
	})
}

func TestRoundTrip(t *testing.T) {
	t.Run("full status roundtrip", func(t *testing.T) {
		outputVars := &collections.SyncMap{}
		outputVars.Store("key1", "value1")
		outputVars.Store("key2", "value2")
		outputsValue := `{"messageId":"msg-123","accepted":true}`

		original := &ir.DAGRunStatus{
			Name:       "test-dag",
			DAGRunID:   "run-123",
			AttemptID:  "attempt-1",
			Status:     ir.Running,
			WorkerID:   "worker-1",
			PID:        12345,
			Root:       ir.DAGRunRef{Name: "root-dag", ID: "root-run"},
			Parent:     ir.DAGRunRef{Name: "parent-dag", ID: "parent-run"},
			CreatedAt:  1234567890,
			QueuedAt:   "2024-01-01T00:00:00Z",
			StartedAt:  "2024-01-01T00:01:00Z",
			FinishedAt: "2024-01-01T00:02:00Z",
			Log:        "/path/to/log",
			Error:      "test error",
			Params:     "key=value",
			ParamsList: []string{"key=value"},
			Nodes: []*ir.Node{
				{
					Step: ir.Step{
						Name:           "step-1",
						Description:    "first step",
						ExecutorConfig: ir.ExecutorConfig{Type: "shell"},
					},
					Status:          ir.NodeSucceeded,
					Stdout:          "/path/stdout.log",
					Stderr:          "/path/stderr.log",
					StartedAt:       "2024-01-01T00:00:00Z",
					FinishedAt:      "2024-01-01T00:01:00Z",
					Error:           "step error",
					RetryCount:      2,
					DoneCount:       3,
					RetriedAt:       "2024-01-01T00:00:30Z",
					OutputVariables: outputVars,
					OutputsValue:    &outputsValue,
					SubRuns: []ir.SubDAGRun{
						{DAGRunID: "sub-run-1", Params: "p1=v1"},
						{DAGRunID: "sub-run-2", Params: "p2=v2"},
					},
				},
			},
			OnInit:    &ir.Node{Step: ir.Step{Name: "on-init"}, Status: ir.NodeSucceeded},
			OnExit:    &ir.Node{Step: ir.Step{Name: "on-exit"}, Status: ir.NodeSucceeded},
			OnSuccess: &ir.Node{Step: ir.Step{Name: "on-success"}, Status: ir.NodeSucceeded},
			OnFailure: &ir.Node{Step: ir.Step{Name: "on-failure"}, Status: ir.NodeNotStarted},
			OnAbort:   &ir.Node{Step: ir.Step{Name: "onAbort"}, Status: ir.NodeNotStarted},
			OnWait:    &ir.Node{Step: ir.Step{Name: "on-wait"}, Status: ir.NodeNotStarted},
		}

		// Convert to proto and back
		proto, err := DAGRunStatusToProto(original)
		require.NoError(t, err)
		result, err := ProtoToDAGRunStatus(proto)
		require.NoError(t, err)

		// Verify all fields are preserved
		require.NotNil(t, result)
		assert.Equal(t, original.Name, result.Name)
		assert.Equal(t, original.DAGRunID, result.DAGRunID)
		assert.Equal(t, original.AttemptID, result.AttemptID)
		assert.Equal(t, original.Status, result.Status)
		assert.Equal(t, original.WorkerID, result.WorkerID)
		assert.Equal(t, original.PID, result.PID)
		assert.Equal(t, original.Root.Name, result.Root.Name)
		assert.Equal(t, original.Root.ID, result.Root.ID)
		assert.Equal(t, original.Parent.Name, result.Parent.Name)
		assert.Equal(t, original.Parent.ID, result.Parent.ID)
		assert.Equal(t, original.CreatedAt, result.CreatedAt)
		assert.Equal(t, original.QueuedAt, result.QueuedAt)
		assert.Equal(t, original.StartedAt, result.StartedAt)
		assert.Equal(t, original.FinishedAt, result.FinishedAt)
		assert.Equal(t, original.Log, result.Log)
		assert.Equal(t, original.Error, result.Error)
		assert.Equal(t, original.Params, result.Params)
		assert.Equal(t, original.ParamsList, result.ParamsList)

		// Verify nodes
		require.Len(t, result.Nodes, 1)
		node := result.Nodes[0]
		assert.Equal(t, "step-1", node.Step.Name)
		assert.Equal(t, "first step", node.Step.Description)
		assert.Equal(t, "shell", node.Step.ExecutorConfig.Type)
		assert.Equal(t, ir.NodeSucceeded, node.Status)
		assert.Equal(t, "/path/stdout.log", node.Stdout)
		assert.Equal(t, "/path/stderr.log", node.Stderr)
		assert.Equal(t, 2, node.RetryCount)
		assert.Equal(t, 3, node.DoneCount)
		require.NotNil(t, node.OutputsValue)
		assert.JSONEq(t, outputsValue, *node.OutputsValue)
		require.Len(t, node.SubRuns, 2)
		assert.Equal(t, "sub-run-1", node.SubRuns[0].DAGRunID)
		assert.Equal(t, "sub-run-2", node.SubRuns[1].DAGRunID)

		// Verify handler nodes
		require.NotNil(t, result.OnInit)
		assert.Equal(t, "on-init", result.OnInit.Step.Name)
		require.NotNil(t, result.OnExit)
		assert.Equal(t, "on-exit", result.OnExit.Step.Name)
		require.NotNil(t, result.OnSuccess)
		assert.Equal(t, "on-success", result.OnSuccess.Step.Name)
		require.NotNil(t, result.OnFailure)
		assert.Equal(t, "on-failure", result.OnFailure.Step.Name)
		require.NotNil(t, result.OnAbort)
		assert.Equal(t, "onAbort", result.OnAbort.Step.Name)
		require.NotNil(t, result.OnWait)
		assert.Equal(t, "on-wait", result.OnWait.Step.Name)
	})

	t.Run("roundtrip with ChatMessages", func(t *testing.T) {
		original := &ir.DAGRunStatus{
			Name:     "chat-dag",
			DAGRunID: "chat-run-123",
			Status:   ir.Succeeded,
			Nodes: []*ir.Node{
				{
					Step:   ir.Step{Name: "chat-step"},
					Status: ir.NodeSucceeded,
					ChatMessages: []ir.LLMMessage{
						{Role: ir.LLMRoleSystem, Content: "You are a helpful assistant."},
						{Role: ir.LLMRoleUser, Content: "Hello!"},
						{Role: ir.LLMRoleAssistant, Content: "Hi there! How can I help?", Metadata: &ir.LLMMessageMetadata{
							Provider:         "openai",
							Model:            "gpt-4",
							PromptTokens:     10,
							CompletionTokens: 8,
							TotalTokens:      18,
						}},
					},
				},
				{
					Step:   ir.Step{Name: "no-messages-step"},
					Status: ir.NodeSucceeded,
					// No ChatMessages - tests omitempty behavior
				},
			},
		}

		// Convert to proto and back
		proto, err := DAGRunStatusToProto(original)
		require.NoError(t, err)
		result, err := ProtoToDAGRunStatus(proto)
		require.NoError(t, err)

		// Verify ChatMessages are preserved
		require.NotNil(t, result)
		require.Len(t, result.Nodes, 2)

		// First node with messages
		chatNode := result.Nodes[0]
		require.Len(t, chatNode.ChatMessages, 3)
		assert.Equal(t, ir.LLMRoleSystem, chatNode.ChatMessages[0].Role)
		assert.Equal(t, "You are a helpful assistant.", chatNode.ChatMessages[0].Content)
		assert.Nil(t, chatNode.ChatMessages[0].Metadata)

		assert.Equal(t, ir.LLMRoleUser, chatNode.ChatMessages[1].Role)
		assert.Equal(t, "Hello!", chatNode.ChatMessages[1].Content)

		assert.Equal(t, ir.LLMRoleAssistant, chatNode.ChatMessages[2].Role)
		assert.Equal(t, "Hi there! How can I help?", chatNode.ChatMessages[2].Content)
		require.NotNil(t, chatNode.ChatMessages[2].Metadata)
		assert.Equal(t, "openai", chatNode.ChatMessages[2].Metadata.Provider)
		assert.Equal(t, "gpt-4", chatNode.ChatMessages[2].Metadata.Model)
		assert.Equal(t, 10, chatNode.ChatMessages[2].Metadata.PromptTokens)
		assert.Equal(t, 8, chatNode.ChatMessages[2].Metadata.CompletionTokens)
		assert.Equal(t, 18, chatNode.ChatMessages[2].Metadata.TotalTokens)

		// Second node without messages
		noMsgNode := result.Nodes[1]
		assert.Nil(t, noMsgNode.ChatMessages)
	})
}
