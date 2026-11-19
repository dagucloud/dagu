// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openObject is an object schema without additionalProperties: false.
func openObject() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"b": map[string]any{"type": "string"}}}
}

// closedWith returns a closed top-level schema with one property a.
func closedWith(a map[string]any) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{"a": a},
	}
}

func TestAgentOutputSchemaClosed(t *testing.T) {
	t.Parallel()

	require.NoError(t, validateAgentOutputSchema(closedWith(map[string]any{
		"type":  "array",
		"items": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"b": map[string]any{}}},
	})))

	for name, tc := range map[string]struct {
		schema map[string]any
		want   string
	}{
		"not an object":  {map[string]any{"type": "string"}, "type: object"},
		"no property":    {map[string]any{"type": "object", "additionalProperties": false}, "at least one property"},
		"open top level": {map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{}}}, "output_schema is an object schema"},
		"tool_calls property": {map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"tool_calls": map[string]any{"type": "array"}},
		}, "tool_calls"},
		"required not listed": {map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"a": map[string]any{}}, "required": []any{"b"},
		}, `required names "b"`},
		"items":                 {closedWith(map[string]any{"items": openObject()}), "output_schema.properties.a.items"},
		"prefixItems":           {closedWith(map[string]any{"prefixItems": []any{openObject()}}), "output_schema.properties.a.prefixItems[0]"},
		"unevaluatedItems":      {closedWith(map[string]any{"unevaluatedItems": openObject()}), "output_schema.properties.a.unevaluatedItems"},
		"dependentSchemas":      {closedWith(map[string]any{"dependentSchemas": map[string]any{"x": openObject()}}), "output_schema.properties.a.dependentSchemas.x"},
		"unevaluatedProperties": {closedWith(map[string]any{"unevaluatedProperties": openObject()}), "output_schema.properties.a.unevaluatedProperties"},
		"anyOf":                 {closedWith(map[string]any{"anyOf": []any{openObject()}}), "output_schema.properties.a.anyOf[0]"},
		"$defs":                 {closedWith(map[string]any{"$defs": map[string]any{"x": openObject()}}), "output_schema.properties.a.$defs.x"},
	} {
		err := validateAgentOutputSchema(tc.schema)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), tc.want, name)
	}
}

func TestAgentOutputSchemaSizeLimit(t *testing.T) {
	t.Parallel()

	big := closedWith(map[string]any{"type": "string", "description": strings.Repeat("x", maxAgentSchemaBytes)})
	err := validateAgentOutputSchema(big)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "above the limit of 100000 bytes")

	below := closedWith(map[string]any{"type": "string", "description": strings.Repeat("x", 90000)})
	require.NoError(t, validateAgentOutputSchema(below))
}
