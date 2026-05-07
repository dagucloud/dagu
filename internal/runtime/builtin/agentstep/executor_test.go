// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agentstep

import (
	"context"
	"io"
	"testing"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutor_SetContextAndGetMessages(t *testing.T) {
	t.Parallel()

	e := &Executor{}

	// Initially empty.
	assert.Empty(t, e.GetMessages())

	// SetContext stores messages.
	msgs := []exec.LLMMessage{
		{Role: exec.RoleSystem, Content: "be helpful"},
		{Role: exec.RoleUser, Content: "hello"},
	}
	e.SetContext(msgs)
	assert.Equal(t, msgs, e.contextMessages)

	// GetMessages returns savedMessages, not contextMessages.
	assert.Empty(t, e.GetMessages())

	// Simulate saved messages after execution.
	e.savedMessages = []exec.LLMMessage{
		{Role: exec.RoleUser, Content: "test"},
		{Role: exec.RoleAssistant, Content: "response"},
	}
	assert.Len(t, e.GetMessages(), 2)
	assert.Equal(t, "response", e.GetMessages()[1].Content)
}

func TestContextToLLMHistory_NilInput(t *testing.T) {
	t.Parallel()
	assert.Nil(t, contextToLLMHistory(nil))
}

func TestContextToLLMHistory_EmptyInput(t *testing.T) {
	t.Parallel()
	assert.Nil(t, contextToLLMHistory([]exec.LLMMessage{}))
}

func TestContextToLLMHistory_FiltersSystemMessages(t *testing.T) {
	t.Parallel()
	msgs := []exec.LLMMessage{
		{Role: exec.RoleSystem, Content: "system prompt"},
		{Role: exec.RoleUser, Content: "hello"},
		{Role: exec.RoleSystem, Content: "another system"},
		{Role: exec.RoleAssistant, Content: "hi"},
	}
	result := contextToLLMHistory(msgs)
	require.Len(t, result, 2)
	assert.Equal(t, llm.RoleUser, result[0].Role)
	assert.Equal(t, "hello", result[0].Content)
	assert.Equal(t, llm.RoleAssistant, result[1].Role)
	assert.Equal(t, "hi", result[1].Content)
}

func TestContextToLLMHistory_ConvertsAllRoles(t *testing.T) {
	t.Parallel()
	msgs := []exec.LLMMessage{
		{Role: exec.RoleUser, Content: "question"},
		{Role: exec.RoleAssistant, Content: "answer"},
		{Role: exec.RoleTool, Content: "result", ToolCallID: "tc-1"},
	}
	result := contextToLLMHistory(msgs)
	require.Len(t, result, 3)
	assert.Equal(t, llm.RoleUser, result[0].Role)
	assert.Equal(t, llm.RoleAssistant, result[1].Role)
	assert.Equal(t, llm.RoleTool, result[2].Role)
	assert.Equal(t, "tc-1", result[2].ToolCallID)
}

func TestContextToLLMHistory_ConvertsToolCalls(t *testing.T) {
	t.Parallel()
	msgs := []exec.LLMMessage{
		{
			Role:    exec.RoleAssistant,
			Content: "let me check",
			ToolCalls: []exec.ToolCall{
				{
					ID:   "call-1",
					Type: "function",
					Function: exec.ToolCallFunction{
						Name:      "read",
						Arguments: `{"path":"/tmp/f"}`,
					},
				},
			},
		},
	}
	result := contextToLLMHistory(msgs)
	require.Len(t, result, 1)
	require.Len(t, result[0].ToolCalls, 1)
	tc := result[0].ToolCalls[0]
	assert.Equal(t, "call-1", tc.ID)
	assert.Equal(t, "function", tc.Type)
	assert.Equal(t, "read", tc.Function.Name)
	assert.Equal(t, `{"path":"/tmp/f"}`, tc.Function.Arguments)
}

func TestBuildToolsIncludesConfiguredWebTools(t *testing.T) {
	t.Parallel()

	cfg := &agent.Config{
		WebTools: &agent.WebToolsConfig{
			Enabled: true,
			Backend: agent.WebToolsBackendTavily,
			Tavily: agent.TavilyWebToolsConfig{
				APIKey: "tvly-test",
			},
		},
	}
	tools := buildTools(
		context.Background(),
		exec.Context{},
		nil,
		cfg,
		agent.ResolveToolPolicy(agent.DefaultToolPolicy()),
		io.Discard,
	)

	assert.NotNil(t, agent.GetToolByName(tools, "web_search"))
	assert.NotNil(t, agent.GetToolByName(tools, "web_extract"))
}
