// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
)

const (
	outputReferenceUnknown outputReferenceValidationStatus = iota
	outputReferenceValid
	outputReferenceInvalid
)

type outputReferenceValidationStatus int

type outputReferenceLocation struct {
	StepName string
	Field    string
}

func (l outputReferenceLocation) displayField() string {
	if l.StepName == "" {
		return l.Field
	}
	return fmt.Sprintf("step %q %s", l.StepName, l.Field)
}

type publishedOutputContract struct {
	StepName string
	Source   string
	Schema   map[string]any
	Keys     map[string]StepOutputEntry
}

// validateOutputReferences conservatively checks step output references.
func (d *DAG) validateOutputReferences() []string {
	if d == nil || len(d.Steps) == 0 {
		return nil
	}

	stepByName := make(map[string]Step, len(d.Steps))
	contracts := make(map[string]publishedOutputContract, len(d.Steps))
	for _, step := range d.Steps {
		stepByName[step.Name] = step
		contract, ok := buildPublishedOutputContract(step)
		if ok {
			contracts[step.Name] = contract
		}
	}

	var warnings []string
	seen := make(map[string]struct{})
	for _, field := range ReferenceFields(d) {
		refs := outputReferences(field.Value)
		if len(refs) == 0 {
			continue
		}
		location := outputReferenceLocation{StepName: field.OwnerStepName, Field: field.Path}
		for _, ref := range refs {
			if warning := outputReferenceGraphWarning(location, stepByName, ref); warning != "" {
				if appendOutputReferenceWarning(&warnings, seen, location.StepName, field.Path, ref.Expression, warning) {
					continue
				}
			}
			contract, ok := contracts[ref.StepName]
			if !ok {
				continue
			}
			result := contract.validatePath(ref.Path)
			if result == outputReferenceInvalid {
				appendOutputReferenceWarning(&warnings, seen, location.StepName, field.Path, ref.Expression, outputReferenceError(location, contract, ref).Error())
			}
		}
	}
	return warnings
}

func outputReferenceGraphWarning(location outputReferenceLocation, stepByName map[string]Step, ref cmnvalue.StepOutputReference) string {
	if _, ok := stepByName[ref.StepName]; !ok {
		return fmt.Sprintf(`%s references %s, but step %q does not exist`, location.displayField(), ref.Expression, ref.StepName)
	}
	if location.StepName == "" {
		return fmt.Sprintf(`%s references %s, but step outputs are only available to step-owned fields`, location.displayField(), ref.Expression)
	}
	if location.StepName == ref.StepName {
		return fmt.Sprintf(`step %q %s references its own output %s`, location.StepName, location.Field, ref.Expression)
	}
	if _, ok := stepByName[location.StepName]; ok && !isUpstreamDependency(stepByName, location.StepName, ref.StepName) {
		return fmt.Sprintf(`step %q %s references %s, but step %q is not an upstream dependency`, location.StepName, location.Field, ref.Expression, ref.StepName)
	}
	return ""
}

func appendOutputReferenceWarning(warnings *[]string, seen map[string]struct{}, stepName, field, expression, warning string) bool {
	key := stepName + "\x00" + field + "\x00" + expression
	if _, exists := seen[key]; exists {
		return false
	}
	seen[key] = struct{}{}
	*warnings = append(*warnings, warning)
	return true
}

func outputReferences(raw string) []cmnvalue.StepOutputReference {
	return cmnvalue.StepOutputReferences(raw)
}

func buildPublishedOutputContract(step Step) (publishedOutputContract, bool) {
	if len(step.StructuredOutput) > 0 {
		return publishedOutputContract{
			StepName: step.Name,
			Source:   "output",
			Keys:     maps.Clone(step.StructuredOutput),
		}, true
	}
	if step.HasOutputSchema() {
		return publishedOutputContract{
			StepName: step.Name,
			Source:   "output_schema",
			Schema:   maps.Clone(step.OutputSchema),
		}, true
	}
	return publishedOutputContract{}, false
}

func (c publishedOutputContract) validatePath(path []string) outputReferenceValidationStatus {
	if len(path) == 0 {
		return outputReferenceUnknown
	}
	if c.Keys != nil {
		entry, ok := c.Keys[path[0]]
		if !ok {
			return outputReferenceInvalid
		}
		if len(path) == 1 {
			return outputReferenceValid
		}
		if entry.HasValue {
			return validateLiteralOutputPath(entry.Value, path[1:])
		}
		return outputReferenceUnknown
	}
	if c.Schema != nil {
		return validateSchemaOutputPath(c.Schema, path)
	}
	return outputReferenceUnknown
}

func validateLiteralOutputPath(value any, path []string) outputReferenceValidationStatus {
	if len(path) == 0 {
		return outputReferenceValid
	}
	m, ok := schemaMap(value)
	if !ok {
		return outputReferenceInvalid
	}
	next, ok := m[path[0]]
	if !ok {
		return outputReferenceInvalid
	}
	return validateLiteralOutputPath(next, path[1:])
}

func validateSchemaOutputPath(schema map[string]any, path []string) outputReferenceValidationStatus {
	if len(path) == 0 {
		return outputReferenceValid
	}
	if _, hasRef := schema["$ref"]; hasRef {
		return outputReferenceUnknown
	}
	if hasSchemaComposition(schema) {
		return validateComposedSchemaOutputPath(schema, path)
	}
	if typ, ok := schema["type"].(string); ok && typ != "object" {
		return outputReferenceInvalid
	}
	if !schemaLooksObject(schema) {
		return outputReferenceUnknown
	}
	properties, _ := schemaMap(schema["properties"])
	propertySchema, exists := properties[path[0]]
	if !exists {
		if schemaPatternPropertiesMayAllow(schema, path[0]) {
			return outputReferenceUnknown
		}
		if schemaAdditionalPropertiesFalse(schema) {
			return outputReferenceInvalid
		}
		return outputReferenceUnknown
	}
	if len(path) == 1 {
		return outputReferenceValid
	}
	nested, ok := schemaMap(propertySchema)
	if !ok {
		return outputReferenceUnknown
	}
	return validateSchemaOutputPath(nested, path[1:])
}

func hasSchemaComposition(schema map[string]any) bool {
	_, hasAnyOf := schema["anyOf"]
	_, hasOneOf := schema["oneOf"]
	_, hasAllOf := schema["allOf"]
	return hasAnyOf || hasOneOf || hasAllOf
}

func validateComposedSchemaOutputPath(schema map[string]any, path []string) outputReferenceValidationStatus {
	for _, key := range []string{"anyOf", "oneOf"} {
		branches, ok := schemaArray(schema[key])
		if !ok || len(branches) == 0 {
			continue
		}
		for _, branch := range branches {
			branchSchema, ok := schemaMap(branch)
			if !ok {
				return outputReferenceUnknown
			}
			branchStatus := validateSchemaOutputPath(branchSchema, path)
			if branchStatus == outputReferenceValid || branchStatus == outputReferenceUnknown {
				return outputReferenceUnknown
			}
		}
		return outputReferenceInvalid
	}

	branches, ok := schemaArray(schema["allOf"])
	if !ok || len(branches) == 0 {
		return outputReferenceUnknown
	}
	status := outputReferenceValid
	for _, branch := range branches {
		branchSchema, ok := schemaMap(branch)
		if !ok {
			return outputReferenceUnknown
		}
		branchStatus := validateSchemaOutputPath(branchSchema, path)
		if branchStatus == outputReferenceInvalid {
			return outputReferenceInvalid
		}
		if branchStatus == outputReferenceUnknown {
			status = outputReferenceUnknown
		}
	}
	return status
}

func schemaLooksObject(schema map[string]any) bool {
	if typ, ok := schema["type"].(string); ok {
		return typ == "object"
	}
	_, hasProperties := schema["properties"]
	return hasProperties
}

func schemaAdditionalPropertiesFalse(schema map[string]any) bool {
	value, ok := schema["additionalProperties"]
	if !ok {
		return false
	}
	boolValue, ok := value.(bool)
	return ok && !boolValue
}

func schemaPatternPropertiesMayAllow(schema map[string]any, property string) bool {
	patternProperties, ok := schemaMap(schema["patternProperties"])
	if !ok {
		return false
	}
	for pattern := range patternProperties {
		matched, err := regexp.MatchString(pattern, property)
		if err != nil || matched {
			return true
		}
	}
	return false
}

func schemaMap(value any) (map[string]any, bool) {
	if value == nil {
		return nil, false
	}
	if m, ok := value.(map[string]any); ok {
		return m, true
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() != reflect.Map || rv.Type().Key().Kind() != reflect.String {
		return nil, false
	}
	out := make(map[string]any, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		out[iter.Key().String()] = iter.Value().Interface()
	}
	return out, true
}

func schemaArray(value any) ([]any, bool) {
	switch v := value.(type) {
	case []any:
		return v, true
	case []map[string]any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = item
		}
		return out, true
	default:
		return nil, false
	}
}

func outputReferenceError(location outputReferenceLocation, contract publishedOutputContract, ref cmnvalue.StepOutputReference) error {
	known := contract.knownFields()
	if known != "" {
		return fmt.Errorf(
			`step %q %s references %s, but step %q publishes no output field %q from %s; known fields: %s`,
			location.StepName,
			location.Field,
			ref.Expression,
			ref.StepName,
			strings.Join(ref.Path, "."),
			contract.Source,
			known,
		)
	}
	return fmt.Errorf(
		`step %q %s references %s, but step %q publishes no output field %q from %s`,
		location.StepName,
		location.Field,
		ref.Expression,
		ref.StepName,
		strings.Join(ref.Path, "."),
		contract.Source,
	)
}

func (c publishedOutputContract) knownFields() string {
	var keys []string
	if c.Keys != nil {
		keys = slices.Collect(maps.Keys(c.Keys))
	} else if props, ok := schemaMap(c.Schema["properties"]); ok {
		keys = slices.Collect(maps.Keys(props))
	}
	if len(keys) == 0 {
		return ""
	}
	slices.Sort(keys)
	return strings.Join(keys, ", ")
}
