// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Recursive definitions and a definition reached through two union branches
// end the walk and the expansion instead of looping, and a definition is
// expanded only where it is first reached.
func TestSchemaReferenceCycles(t *testing.T) {
	t.Parallel()

	ref := func(name string) map[string]any {
		return map[string]any{"$ref": "#/definitions/" + name}
	}
	root := map[string]any{
		"definitions": map[string]any{
			"self":   map[string]any{"oneOf": []any{ref("self")}},
			"a":      map[string]any{"oneOf": []any{ref("b")}},
			"b":      map[string]any{"oneOf": []any{ref("a")}},
			"shared": map[string]any{"properties": map[string]any{"x": map[string]any{"type": "string"}}},
			"union":  map[string]any{"oneOf": []any{ref("shared"), ref("shared")}},
		},
	}

	for _, name := range []string{"self", "a"} {
		_, ok := schemaChild(root, ref(name), "missing", map[string]bool{})
		assert.False(t, ok, name)
		assert.NotNil(t, expandSchemaShape(root, ref(name), map[string]bool{}), name)
	}

	child, ok := schemaChild(root, ref("union"), "x", map[string]bool{})
	require.True(t, ok)
	assert.Equal(t, map[string]any{"type": "string"}, child)

	expanded, ok := expandSchemaShape(root, ref("union"), map[string]bool{}).(map[string]any)
	require.True(t, ok)
	branches, ok := expanded["oneOf"].([]any)
	require.True(t, ok)
	require.Len(t, branches, 2)
	assert.Contains(t, branches[0], "properties")
	assert.Equal(t, ref("shared"), branches[1])
}
