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

type publishedOutputContract struct {
	StepName string
	Source   string
	Schema   map[string]any
	Keys     map[string]StepOutputEntry
}

// validateOutputReferences conservatively checks ${step.output.field} references.
// It only reports errors when a referenced field is definitely absent from a
// closed published-output contract. Unknown or open contracts are ignored to
// avoid false positives.
func (d *DAG) validateOutputReferences() []error {
	if d == nil || len(d.Steps) == 0 {
		return nil
	}

	contracts := make(map[string]publishedOutputContract, len(d.Steps))
	for _, step := range d.Steps {
		contract, ok := buildPublishedOutputContract(step)
		if ok {
			contracts[step.Name] = contract
		}
	}
	if len(contracts) == 0 {
		return nil
	}

	var errs []error
	seen := make(map[string]struct{})
	for _, field := range ResolvableFields(d) {
		if !strings.Contains(field.Value, ".output.") {
			continue
		}
		location := outputReferenceLocation{StepName: field.OwnerStepName, Field: field.Path}
		for _, ref := range outputReferences(field.Value) {
			contract, ok := contracts[ref.StepName]
			if !ok {
				continue
			}
			result := contract.validatePath(ref.Path)
			if result == outputReferenceInvalid {
				key := field.OwnerStepName + "\x00" + ref.Expression
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				errs = append(errs, outputReferenceError(location, contract, ref))
			}
		}
	}
	return errs
}

func outputReferences(raw string) []cmnvalue.StepOutputReference {
	refs := cmnvalue.ScanReferences(raw)
	out := make([]cmnvalue.StepOutputReference, 0)
	for _, ref := range refs {
		if ref.StepOutput == nil {
			continue
		}
		out = append(out, *ref.StepOutput)
	}
	return out
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
