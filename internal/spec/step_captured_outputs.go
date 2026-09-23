// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import "github.com/dagucloud/dagu/v2/internal/ir"

// deriveCapturedStepOutputs records the names a step publishes outside
// DAGU_OUTPUT_FILE so strict step-output references can be validated against
// them. Declarations authored in YAML take precedence, and a step whose
// published names are only known at run time contributes none.
//
// It is safe to call more than once: previously derived names are recomputed.
func deriveCapturedStepOutputs(result *ir.Step) {
	if result == nil {
		return
	}

	authored := make([]ir.StepOutputDeclaration, 0, len(result.Outputs))
	taken := make(map[string]struct{}, len(result.Outputs))
	for _, output := range result.Outputs {
		if output.Source == ir.StepDeclaredOutputSourceCapture {
			continue
		}
		authored = append(authored, output)
		taken[output.Name] = struct{}{}
	}

	for _, derived := range capturedOutputs(result).declarations {
		if _, exists := taken[derived.Name]; exists {
			continue
		}
		taken[derived.Name] = struct{}{}
		authored = append(authored, derived)
	}

	if len(authored) == 0 {
		result.Outputs = nil
		return
	}
	result.Outputs = authored
}

// capturedOutputContract describes the names a step publishes outside
// DAGU_OUTPUT_FILE.
type capturedOutputContract struct {
	declarations []ir.StepOutputDeclaration
	// dynamic reports that the step publishes names inspection cannot list, so
	// a reference to one of them cannot be checked before the step runs.
	dynamic bool
}

// capturedOutputs describes the names published by the mechanism that wins at
// run time, mirroring the precedence in Node.captureOutput.
func capturedOutputs(step *ir.Step) capturedOutputContract {
	switch {
	case step.HasStdoutOutputs():
		names, ok := stdoutOutputNames(step.StdoutOutputs)
		if !ok {
			return capturedOutputContract{dynamic: true}
		}
		return capturedOutputContract{declarations: capturedNames(names)}
	case step.HasStructuredOutput():
		return capturedOutputContract{declarations: capturedNames(sortedKeys(step.StructuredOutput))}
	case step.HasOutputSchema():
		properties, ok := step.OutputSchema["properties"].(map[string]any)
		if !ok {
			return capturedOutputContract{dynamic: true}
		}
		// An open schema validates names it never lists, and a run publishes
		// whatever it accepted, so the listed names are only a lower bound.
		return capturedOutputContract{
			declarations: outputSchemaDeclarations(properties),
			dynamic:      !schemaForbidsExtraProperties(step.OutputSchema),
		}
	case isOutputsWriteStep(step):
		values, ok := step.ExecutorConfig.Config["values"].(map[string]any)
		if !ok {
			return capturedOutputContract{dynamic: true}
		}
		return capturedOutputContract{declarations: capturedNames(sortedKeys(values))}
	case step.ExecutorConfig.Type == ir.ExecutorTypeBrowser:
		return browserExtractOutputs(step.ExecutorConfig.Config["do"])
	}
	return capturedOutputContract{}
}

// browserExtractOutputs lists the fields a browser step extracts. Each
// extract operation publishes the top-level properties its schema lists.
func browserExtractOutputs(operations any) capturedOutputContract {
	items, ok := operations.([]any)
	if !ok {
		return capturedOutputContract{}
	}
	var contract capturedOutputContract
	for _, item := range items {
		operation, _ := item.(map[string]any)
		extract, ok := operation["extract"].(map[string]any)
		if !ok {
			continue
		}
		schema, _ := extract["schema"].(map[string]any)
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			contract.dynamic = true
			continue
		}
		contract.declarations = append(contract.declarations, outputSchemaDeclarations(properties)...)
	}
	return contract
}

// stdoutOutputNames returns the field names a stdout outputs config publishes.
// It reports false when the config decodes stdout into an object whose keys
// only a run reveals.
func stdoutOutputNames(cfg *ir.StepOutputsConfig) ([]string, bool) {
	switch {
	case len(cfg.Fields) > 0:
		return sortedKeys(cfg.Fields), true
	case cfg.Field != "":
		return []string{cfg.Field}, true
	default:
		return nil, false
	}
}

func isOutputsWriteStep(step *ir.Step) bool {
	return step.ExecutorConfig.Type == ir.ExecutorTypeOutputs
}

// outputSchemaDeclarations converts the top-level property names of an output
// schema into declarations.
func outputSchemaDeclarations(properties map[string]any) []ir.StepOutputDeclaration {
	declarations := make([]ir.StepOutputDeclaration, 0, len(properties))
	for _, name := range sortedKeys(properties) {
		if !declaredOutputNamePattern.MatchString(name) {
			continue
		}
		declarations = append(declarations, ir.StepOutputDeclaration{
			Name:   name,
			Type:   schemaOutputType(properties[name]),
			Source: ir.StepDeclaredOutputSourceCapture,
		})
	}
	return declarations
}

// schemaForbidsExtraProperties reports whether a schema accepts only the names
// it lists. A pattern-matched property is unlisted, so it opens the schema the
// same way an additional property does.
func schemaForbidsExtraProperties(schema map[string]any) bool {
	if _, ok := schema["patternProperties"]; ok {
		return false
	}
	additional, ok := schema["additionalProperties"].(bool)
	return ok && !additional
}

// schemaOutputType maps a schema property to a declared output type. Only a
// string property carries a string value; every other shape is JSON text.
func schemaOutputType(property any) string {
	object, ok := property.(map[string]any)
	if !ok {
		return ir.StepDeclaredOutputTypeJSON
	}
	if object["type"] == "string" {
		return ir.StepDeclaredOutputTypeString
	}
	return ir.StepDeclaredOutputTypeJSON
}

func capturedNames(names []string) []ir.StepOutputDeclaration {
	declarations := make([]ir.StepOutputDeclaration, 0, len(names))
	for _, name := range names {
		if !declaredOutputNamePattern.MatchString(name) {
			continue
		}
		declarations = append(declarations, ir.StepOutputDeclaration{
			Name:   name,
			Source: ir.StepDeclaredOutputSourceCapture,
		})
	}
	return declarations
}
