// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chat

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// classifySchema is an output_schema a model can answer through respond.
func classifySchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"category": map[string]any{"type": "string", "enum": []any{"refund", "complaint", "question"}},
			"amount":   map[string]any{"type": "number"},
		},
		"required": []any{"category"},
	}
}

func TestValidateStep(t *testing.T) {
	t.Parallel()

	withSchema := func(mutate func(map[string]any)) map[string]any {
		schema := classifySchema()
		mutate(schema)
		return schema
	}
	tests := []struct {
		name    string
		schema  map[string]any
		llm     *ir.LLMConfig
		wantErr string
	}{
		{name: "NoSchema", llm: &ir.LLMConfig{Tools: []string{"respond"}}},
		{name: "Valid", schema: classifySchema(), llm: &ir.LLMConfig{Tools: []string{"lookup"}}},
		{
			name:    "MissingType",
			schema:  withSchema(func(s map[string]any) { delete(s, "type") }),
			wantErr: "type: object",
		},
		{
			name:    "ArrayType",
			schema:  withSchema(func(s map[string]any) { s["type"] = "array" }),
			wantErr: "type: object",
		},
		{
			name:    "NoProperties",
			schema:  map[string]any{"type": "object"},
			wantErr: "at least one property",
		},
		{
			name:    "RequiredNotListed",
			schema:  withSchema(func(s map[string]any) { s["required"] = []any{"category", "currency"} }),
			wantErr: "currency",
		},
		{
			name:    "WebSearch",
			schema:  classifySchema(),
			llm:     &ir.LLMConfig{WebSearch: &ir.WebSearchConfig{Enabled: true}},
			wantErr: "web search",
		},
		{
			name:    "RespondTool",
			schema:  classifySchema(),
			llm:     &ir.LLMConfig{Tools: []string{"lookup", "respond"}},
			wantErr: "reserved",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateStep(ir.Step{OutputSchema: tt.schema, LLM: tt.llm})
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// Handler steps are not validated at load time, so the executor applies the
// same checks.
func TestNewChatExecutorValidatesOutputSchema(t *testing.T) {
	t.Parallel()

	_, err := newChatExecutor(chatRuntimeContext(t, nil), ir.Step{
		LLM:          &ir.LLMConfig{Provider: "openai", Model: "gpt-4o"},
		OutputSchema: map[string]any{"type": "object"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one property")
}

// A tool DAG whose name is respond is found only once the DAG loads.
func TestNewChatExecutorRejectsRespondToolDAG(t *testing.T) {
	t.Parallel()

	ctx := chatRuntimeContext(t, nil)
	rCtx := runtime.GetDAGContext(ctx)
	rCtx.DAG.LocalDAGs = map[string]*ir.DAG{"answer-tool": {Name: "respond"}}

	step := ir.Step{
		LLM: &ir.LLMConfig{Provider: "openai", Model: "gpt-4o", Tools: []string{"answer-tool"}},
	}
	_, err := newChatExecutor(ctx, step)
	require.NoError(t, err, "the name is free without output_schema")

	step.OutputSchema = classifySchema()
	_, err = newChatExecutor(ctx, step)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reserved")
}
