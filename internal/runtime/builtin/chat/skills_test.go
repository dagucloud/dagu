// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chat

import (
	"bytes"
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agenttools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func skillEntry(name, body string) ir.SkillEntry {
	return ir.SkillEntry{Name: name, Description: "About " + name, Body: body}
}

// The tool loop sends the skill sections on each request, returns the body
// of a loaded skill, and keeps the sections out of the saved session.
func TestExecutor_LoadSkillRoundTrip(t *testing.T) {
	t.Parallel()

	var requests []*llmpkg.ChatRequest
	provider := &mockProvider{
		chatFunc: func(_ context.Context, req *llmpkg.ChatRequest) (*llmpkg.ChatResponse, error) {
			requests = append(requests, req)
			if len(requests) == 1 {
				return &llmpkg.ChatResponse{ToolCalls: []llmpkg.ToolCall{{
					ID:       "call-1",
					Type:     "function",
					Function: llmpkg.ToolCallFunction{Name: "load_skill", Arguments: `{"name":"refund"}`},
				}}}, nil
			}
			return &llmpkg.ChatResponse{Content: "done"}, nil
		},
	}
	cfg := &ir.LLMConfig{
		Provider: "local", Model: "m",
		Skills: []ir.SkillEntry{skillEntry("refund", "Refund in 30 days.")},
	}
	ctx := chatRuntimeContext(t, nil)
	registry, err := agenttools.BuildToolRegistry(ctx, cfg)
	require.NoError(t, err)
	var stdout bytes.Buffer
	executor := &Executor{stdout: &stdout, step: ir.Step{LLM: cfg}, toolRegistry: registry}

	err = executor.runWithToolsForModel(ctx, provider,
		[]ir.LLMMessage{{Role: ir.LLMRoleSystem, Content: "You help."}, {Role: ir.LLMRoleUser, Content: "hello"}}, cfg)
	require.NoError(t, err)

	require.Len(t, requests, 2)
	for _, req := range requests {
		assert.Contains(t, req.Messages[0].Content, "- refund: About refund")
		require.Len(t, req.Tools, 1)
		assert.Equal(t, "load_skill", req.Tools[0].Function.Name)
	}
	last := requests[1].Messages[len(requests[1].Messages)-1]
	assert.Equal(t, llmpkg.RoleTool, last.Role)
	assert.Equal(t, "<skill_content name=\"refund\">\nRefund in 30 days.\n</skill_content>", last.Content)

	for _, msg := range executor.GetMessages() {
		assert.NotContains(t, msg.Content, "## Skills")
	}
	assert.Equal(t, "skills", executor.GetToolDefinitions()[0].Source)
	assert.Equal(t, "refund", executor.GetSkillDefinitions()[0].Name)
}
