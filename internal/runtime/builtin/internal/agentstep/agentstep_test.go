// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agentstep_test

import (
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
