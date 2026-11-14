// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The names an xlsx.extract step publishes are known from its schema; they
// are unknown only while the schema cannot be read.
func TestXlsxExtractOutputsDynamicOnlyWhenUnreadable(t *testing.T) {
	t.Parallel()

	fixed := []string{"cells", "sheet", "source", "warnings"}
	names := func(contract capturedOutputContract) []string {
		out := make([]string, 0, len(contract.declarations))
		for _, d := range contract.declarations {
			out = append(out, d.Name)
		}
		return out
	}

	literal := xlsxExtractOutputs(map[string]any{"type": "object", "properties": map[string]any{"total": map[string]any{"type": "number"}}})
	assert.False(t, literal.dynamic)
	assert.ElementsMatch(t, append([]string{"total"}, fixed...), names(literal))

	noProperties := xlsxExtractOutputs(map[string]any{"type": "object"})
	assert.False(t, noProperties.dynamic, "a schema without properties publishes exactly the fixed outputs")
	assert.ElementsMatch(t, fixed, names(noProperties))

	reference := xlsxExtractOutputs("${params.SCHEMA}")
	assert.True(t, reference.dynamic, "a schema resolved at run time names outputs inspection cannot list")
	assert.ElementsMatch(t, fixed, names(reference))

	badProperties := xlsxExtractOutputs(map[string]any{"type": "object", "properties": []any{"total"}})
	assert.True(t, badProperties.dynamic, "properties that are not a map cannot be read")
}
