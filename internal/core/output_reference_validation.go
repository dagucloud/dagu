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
)

var outputReferencePattern = regexp.MustCompile(`\$\{([A-Za-z0-9_-]+)\.output\.([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)\}`)

type outputReferenceValidationStatus int

const (
	outputReferenceUnknown outputReferenceValidationStatus = iota
	outputReferenceValid
	outputReferenceInvalid
)

type outputReference struct {
	Expression string
	StepName   string
	Path       []string
}

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
	for _, step := range d.Steps {
		location := outputReferenceLocation{StepName: step.Name}
		for _, candidate := range collectStepOutputReferenceStrings(step) {
			location.Field = candidate.field
			for _, ref := range extractOutputReferences(candidate.value) {
				contract, ok := contracts[ref.StepName]
				if !ok {
					continue
				}
				result := contract.validatePath(ref.Path)
				if result == outputReferenceInvalid {
					key := step.Name + "\x00" + ref.Expression
					if _, exists := seen[key]; exists {
						continue
					}
					seen[key] = struct{}{}
					errs = append(errs, outputReferenceError(location, contract, ref))
				}
			}
		}
	}
	return errs
}

type outputReferenceString struct {
	field string
	value string
}

func collectStepOutputReferenceStrings(step Step) []outputReferenceString {
	var refs []outputReferenceString
	add := func(field, value string) {
		if strings.Contains(value, ".output.") {
			refs = append(refs, outputReferenceString{field: field, value: value})
		}
	}
	add("command", step.Command)
	add("cmdWithArgs", step.CmdWithArgs)
	add("cmdArgsSys", step.CmdArgsSys)
	add("shellCmdArgs", step.ShellCmdArgs)
	add("script", step.Script)
	add("stdout", step.Stdout)
	add("stderr", step.Stderr)
	add("dir", step.Dir)
	add("shell", step.Shell)
	for i, arg := range step.Args {
		add(fmt.Sprintf("args[%d]", i), arg)
	}
	for i, arg := range step.ShellArgs {
		add(fmt.Sprintf("shellArgs[%d]", i), arg)
	}
	for i, env := range step.Env {
		add(fmt.Sprintf("env[%d]", i), env)
	}
	for i, cmd := range step.Commands {
		add(fmt.Sprintf("commands[%d].command", i), cmd.Command)
		add(fmt.Sprintf("commands[%d].cmdWithArgs", i), cmd.CmdWithArgs)
		for j, arg := range cmd.Args {
			add(fmt.Sprintf("commands[%d].args[%d]", i, j), arg)
		}
	}
	for name, entry := range step.StructuredOutput {
		if entry.HasValue {
			collectOutputValueReferenceStrings(fmt.Sprintf("output.%s.value", name), entry.Value, add)
		}
		add(fmt.Sprintf("output.%s.path", name), entry.Path)
		add(fmt.Sprintf("output.%s.select", name), entry.Select)
	}
	collectOutputValueReferenceStrings("with", step.ExecutorConfig.Config, add)
	collectOutputValueReferenceStrings("params", step.Params, add)
	return refs
}

func collectOutputValueReferenceStrings(field string, value any, add func(string, string)) {
	switch v := value.(type) {
	case string:
		add(field, v)
	case []string:
		for i, item := range v {
			add(fmt.Sprintf("%s[%d]", field, i), item)
		}
	case []any:
		for i, item := range v {
			collectOutputValueReferenceStrings(fmt.Sprintf("%s[%d]", field, i), item, add)
		}
	case map[string]any:
		keys := slices.Collect(maps.Keys(v))
		slices.Sort(keys)
		for _, key := range keys {
			collectOutputValueReferenceStrings(field+"."+key, v[key], add)
		}
	case map[string]string:
		keys := slices.Collect(maps.Keys(v))
		slices.Sort(keys)
		for _, key := range keys {
			add(field+"."+key, v[key])
		}
	case Params:
		collectOutputValueReferenceStrings(field+".simple", v.Simple, add)
		collectOutputValueReferenceStrings(field+".rich", v.Rich, add)
		if len(v.Raw) > 0 {
			add(field+".raw", string(v.Raw))
		}
	default:
		collectReflectOutputValueReferenceStrings(field, value, add)
	}
}

func collectReflectOutputValueReferenceStrings(field string, value any, add func(string, string)) {
	if value == nil {
		return
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		for i := range rv.Len() {
			collectOutputValueReferenceStrings(fmt.Sprintf("%s[%d]", field, i), rv.Index(i).Interface(), add)
		}
		return
	}
	if rv.Kind() != reflect.Map || rv.Type().Key().Kind() != reflect.String {
		return
	}
	keys := make([]string, 0, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		keys = append(keys, iter.Key().String())
	}
	slices.Sort(keys)
	for _, key := range keys {
		collectOutputValueReferenceStrings(field+"."+key, rv.MapIndex(reflect.ValueOf(key)).Interface(), add)
	}
}

func extractOutputReferences(value string) []outputReference {
	matches := outputReferencePattern.FindAllStringSubmatch(value, -1)
	if len(matches) == 0 {
		return nil
	}
	refs := make([]outputReference, 0, len(matches))
	for _, match := range matches {
		if len(match) != 3 {
			continue
		}
		refs = append(refs, outputReference{
			Expression: match[0],
			StepName:   match[1],
			Path:       strings.Split(match[2], "."),
		})
	}
	return refs
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

func outputReferenceError(location outputReferenceLocation, contract publishedOutputContract, ref outputReference) error {
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
