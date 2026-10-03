// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package chat

import (
	"errors"
	"fmt"
	"slices"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
)

const (
	fieldOutputSchema = "output_schema"
	fieldWebSearch    = "llm.web_search"
	fieldTools        = "llm.tools"
)

// validateStep checks the chat settings that the generic step checks leave
// out.
func validateStep(step ir.Step) error {
	return validateOutputSchema(step)
}

// validateOutputSchema checks that the model can answer a step's
// output_schema through the respond tool, whose parameters are that schema.
func validateOutputSchema(step ir.Step) error {
	if !step.HasOutputSchema() {
		return nil
	}
	schema := step.OutputSchema
	if schema["type"] != "object" {
		return ir.NewValidationError(fieldOutputSchema, nil,
			errors.New("a chat step's output_schema must declare type: object"))
	}
	properties, _ := schema["properties"].(map[string]any)
	if len(properties) == 0 {
		return ir.NewValidationError(fieldOutputSchema, nil,
			errors.New("a chat step's output_schema must list at least one property"))
	}
	for _, name := range requiredNames(schema["required"]) {
		if _, ok := properties[name]; !ok {
			// Unlisted names are dropped from the answer, so such a
			// schema could never be satisfied.
			return ir.NewValidationError(fieldOutputSchema, name,
				errors.New("a required property must be listed in properties"))
		}
	}
	if cfg := step.LLM; cfg != nil {
		if cfg.WebSearch != nil && cfg.WebSearch.Enabled {
			return ir.NewValidationError(fieldWebSearch, nil,
				errors.New("web search cannot be combined with output_schema"))
		}
		if slices.Contains(cfg.Tools, agentstep.RespondToolName) {
			return ir.NewValidationError(fieldTools, agentstep.RespondToolName, reservedToolError())
		}
	}
	return nil
}

// reservedToolError reports a tool that takes the name of the respond tool.
func reservedToolError() error {
	return fmt.Errorf("the tool name %q is reserved for the output_schema answer", agentstep.RespondToolName)
}

// requiredNames returns the property names a schema's required keyword lists.
func requiredNames(value any) []string {
	switch names := value.(type) {
	case []string:
		return names
	case []any:
		result := make([]string, 0, len(names))
		for _, name := range names {
			if s, ok := name.(string); ok {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}
