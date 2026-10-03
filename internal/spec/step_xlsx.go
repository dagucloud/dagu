// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"fmt"
	"slices"
	"sort"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// xlsxAction maps an xlsx.<operation> action to the xlsx executor. The
// reading operations publish fixed outputs, so output declarations on them
// are rejected.
func xlsxAction(operation string, fixedOutputs bool) actionNormalizer {
	return func(normalized map[string]any, with map[string]any) error {
		if fixedOutputs {
			if err := validateFixedOutputs(normalized, "xlsx"); err != nil {
				return err
			}
		}
		return normalizeOperationAction(normalized, "xlsx", with, operation)
	}
}

// xlsxExtractOperation is the xlsx operation that locates fields with a
// model and publishes the properties of with.schema as outputs.
const xlsxExtractOperation = "extract"

// xlsxExtractFixedOutputs are the outputs xlsx.extract publishes next to the
// schema's properties, so a property may not take one of these names.
var xlsxExtractFixedOutputs = []string{"cells", "sheet", "source", "warnings"}

// xlsxExtractAction maps xlsx.extract to the xlsx executor. with.llm moves to
// the step, where it replaces the DAG-level llm block as it does for browser
// steps; the schema's properties become the step's outputs, so authored
// outputs are rejected like those of every other xlsx action.
func xlsxExtractAction() actionNormalizer {
	return func(normalized map[string]any, with map[string]any) error {
		if err := validateFixedOutputs(normalized, "xlsx"); err != nil {
			return err
		}
		if err := validateXlsxExtractSchema(with); err != nil {
			return err
		}
		moveActionLLM(normalized, with)
		return normalizeOperationAction(normalized, "xlsx", with, xlsxExtractOperation)
	}
}

// validateXlsxExtractSchema rejects a schema property that would shadow one
// of the outputs xlsx.extract always publishes. The shape of the schema
// itself is checked by the executor's validation.
func validateXlsxExtractSchema(with map[string]any) error {
	schema, ok := with["schema"].(map[string]any)
	if !ok {
		return nil
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if slices.Contains(xlsxExtractFixedOutputs, name) {
			return ir.NewValidationError("with", with, fmt.Errorf("schema property %q collides with an output of xlsx.extract", name))
		}
	}
	return nil
}

// isXlsxExtractStep reports whether a step runs xlsx.extract.
func isXlsxExtractStep(step *ir.Step) bool {
	return step.ExecutorConfig.Type == ir.ExecutorTypeXlsx &&
		len(step.Commands) > 0 && step.Commands[0].Command == xlsxExtractOperation
}
