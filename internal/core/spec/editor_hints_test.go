// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEditorHintForLegacyDefinition_UsesEmptySchemaWhenInputSchemaMissing(t *testing.T) {
	t.Parallel()

	hint, ok, err := editorHintForLegacyDefinition(&customStepType{
		Name:        "greet",
		Type:        "command",
		Description: "Send a greeting",
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "greet", hint.Name)
	require.Equal(t, "command", hint.TargetType)
	require.Equal(t, "Send a greeting", hint.Description)
	require.Equal(t, map[string]any{}, hint.InputSchema)
}

func TestEditorHintForLegacyDefinition_ResolvesSchemaObject(t *testing.T) {
	t.Parallel()

	inputSchema, err := resolveCustomStepTypeInputSchema("greet", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"message": map[string]any{
				"type": "string",
			},
		},
	})
	require.NoError(t, err)

	hint, ok, err := editorHintForLegacyDefinition(&customStepType{
		Name:        "greet",
		Type:        "command",
		InputSchema: inputSchema,
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "object", hint.InputSchema["type"])

	properties, ok := hint.InputSchema["properties"].(map[string]any)
	require.True(t, ok)
	message, ok := properties["message"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "string", message["type"])
}
