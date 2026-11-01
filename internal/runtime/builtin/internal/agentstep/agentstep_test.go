// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agentstep_test

import (
	"errors"
	"testing"

	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStructuredAnswer(t *testing.T) {
	t.Parallel()

	answer, err := agentstep.StructuredAnswer(&llmpkg.ChatResponse{ToolCalls: []llmpkg.ToolCall{{
		Function: llmpkg.ToolCallFunction{Name: agentstep.RespondToolName, Arguments: `{"ok":1}`},
	}}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":1}`, string(answer))

	answer, err = agentstep.StructuredAnswer(&llmpkg.ChatResponse{Content: "```json\n{\"ok\":true}\n```"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":true}`, string(answer))

	_, err = agentstep.StructuredAnswer(&llmpkg.ChatResponse{Content: "I cannot help"})
	assert.Error(t, err)
}

// A blank answer is replaced, since providers reject an empty assistant
// message.
func TestRejectedAnswer(t *testing.T) {
	t.Parallel()

	respond := func(arguments string) *llmpkg.ChatResponse {
		return &llmpkg.ChatResponse{ToolCalls: []llmpkg.ToolCall{{
			Function: llmpkg.ToolCallFunction{Name: agentstep.RespondToolName, Arguments: arguments},
		}}}
	}
	assert.Equal(t, `{"ok":`, agentstep.RejectedAnswer(respond(`{"ok":`)))
	assert.Equal(t, "(no answer)", agentstep.RejectedAnswer(respond("")))
	assert.Equal(t, "(no answer)", agentstep.RejectedAnswer(respond("  \n")))
	assert.Equal(t, "plain text", agentstep.RejectedAnswer(&llmpkg.ChatResponse{Content: "plain text"}))
	assert.Equal(t, "(no answer)", agentstep.RejectedAnswer(&llmpkg.ChatResponse{Content: " "}))
	assert.Equal(t, "(no answer)", agentstep.RejectedAnswer(&llmpkg.ChatResponse{}))

	// Text beside a blank respond call is what the model actually said.
	blankCall := respond("")
	blankCall.Content = "I could not find the total."
	assert.Equal(t, "I could not find the total.", agentstep.RejectedAnswer(blankCall))
}

// The correction goes on the request's own copy of the messages, so a
// slice shared with the next model's request is never written.
func TestAppendCorrection(t *testing.T) {
	t.Parallel()

	shared := make([]llmpkg.Message, 2, 4)
	req := &llmpkg.ChatRequest{Messages: shared}
	agentstep.AppendCorrection(req, `{"total":`, errors.New("bad answer"))

	require.Len(t, req.Messages, 4)
	assert.Equal(t, llmpkg.Message{Role: llmpkg.RoleAssistant, Content: `{"total":`}, req.Messages[2])
	assert.Equal(t, llmpkg.Message{Role: llmpkg.RoleUser, Content: agentstep.Correction(errors.New("bad answer"))}, req.Messages[3])
	assert.Empty(t, shared[:4][2].Role, "the shared slice is not written")
}

func TestToolParametersStripsAnnotations(t *testing.T) {
	t.Parallel()

	schema := map[string]any{"$schema": "x", "$id": "y", "type": "object"}
	assert.Equal(t, map[string]any{"type": "object"}, agentstep.ToolParameters(schema))
	assert.Contains(t, schema, "$schema", "the caller's schema is left as it was")
}

func TestVariables(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"user", "pass_1"}, agentstep.VariableReferences("Log in as %user% with %pass_1% and 100%"))
	assert.Equal(t, "alice / %unknown%", agentstep.SubstituteVariables("%user% / %unknown%", map[string]string{"user": "alice"}))
}

func TestValidateDuration(t *testing.T) {
	t.Parallel()

	require.NoError(t, agentstep.ValidateDuration("timeout", "30s"))
	require.NoError(t, agentstep.ValidateDuration("timeout", ""))
	require.NoError(t, agentstep.ValidateDuration("timeout", "${TIMEOUT}"), "references resolve at run time")
	require.ErrorContains(t, agentstep.ValidateDuration("timeout", "-1s"), `timeout "-1s" must be a positive duration`)
	require.Error(t, agentstep.ValidateDuration("timeout", "soon"))
}
