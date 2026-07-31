// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/google/jsonschema-go/jsonschema"
)

type inlineJSONSchemaClassification struct {
	valid               bool
	propertiesPresent   bool
	malformedProperties any
}

// isInlineJSONSchema returns true when params is a top-level JSON Schema object
// using the canonical object form. This keeps legacy params maps compatible.
func isInlineJSONSchema(input any) bool {
	return classifyInlineJSONSchema(input).valid
}

func classifyInlineJSONSchema(input any) inlineJSONSchemaClassification {
	m, ok := input.(map[string]any)
	if !ok {
		return inlineJSONSchemaClassification{}
	}
	// External schema format takes precedence.
	if _, ok := extractParamsSchemaDeclaration(input); ok {
		return inlineJSONSchemaClassification{}
	}
	typeName, ok := m["type"].(string)
	if !ok || strings.TrimSpace(typeName) != "object" {
		return inlineJSONSchemaClassification{}
	}
	props, ok := m["properties"]
	if !ok {
		return inlineJSONSchemaClassification{}
	}

	if _, ok := props.(map[string]any); ok {
		return inlineJSONSchemaClassification{
			valid:             true,
			propertiesPresent: true,
		}
	}

	return inlineJSONSchemaClassification{
		propertiesPresent:   true,
		malformedProperties: props,
	}
}

func malformedInlineJSONSchemaShapeError(input any) error {
	classification := classifyInlineJSONSchema(input)
	if !classification.propertiesPresent || classification.valid {
		return nil
	}

	return core.NewValidationError(
		"params",
		classification.malformedProperties,
		fmt.Errorf("inline JSON Schema properties must be an object keyed by parameter name"),
	)
}

func buildInlineSchemaParamPlan(input any, skipValidation bool) (*dagParamPlan, error) {
	root, resolved, err := parseInlineSchema(input, !skipValidation)
	if err != nil {
		return nil, err
	}

	schemaOrder := topLevelSchemaOrder(root)
	schemaProperties := map[string]*jsonschema.Schema{}
	if root != nil {
		maps.Copy(schemaProperties, root.Properties)
	}

	typedDefaults := explicitInlineSchemaDefaults(root)
	if !skipValidation {
		typedDefaults, err = validateSchemaMap(typedDefaults, resolved, true)
		if err != nil {
			return nil, err
		}
	}

	plan := &dagParamPlan{
		kind:             dagParamKindInlineSchema,
		schema:           resolved,
		schemaOrder:      schemaOrder,
		schemaProperties: schemaProperties,
		schemaClosed:     schemaDisallowsAdditionalProperties(root),
		entries:          entriesFromTypedMap(typedDefaults, schemaOrder),
	}

	if paramDefs, ok := deriveExternalSchemaParamDefs(root, typedDefaults); ok {
		plan.paramDefs = paramDefs
	}

	return plan, nil
}

func parseInlineSchema(input any, validate bool) (*jsonschema.Schema, *jsonschema.Resolved, error) {
	m, ok := input.(map[string]any)
	if !ok {
		return nil, nil, core.NewValidationError("params", input, fmt.Errorf("%w: expected an object for inline JSON Schema", ErrInvalidParamValue))
	}

	stripped := make(map[string]any, len(m))
	maps.Copy(stripped, m)
	delete(stripped, "readOnly")
	delete(stripped, "readonly")

	raw, err := json.Marshal(stripped)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal inline param schema: %w", err)
	}

	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, nil, fmt.Errorf("failed to parse inline param schema: %w", err)
	}
	if !validate {
		return &schema, nil, nil
	}

	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resolve inline param schema: %w", err)
	}
	return resolved.Schema(), resolved, nil
}

func explicitInlineSchemaDefaults(root *jsonschema.Schema) map[string]any {
	if root == nil || len(root.Properties) == 0 {
		return map[string]any{}
	}

	defaults := make(map[string]any, len(root.Properties))
	for name, property := range root.Properties {
		if property == nil || len(property.Default) == 0 {
			continue
		}
		if _, ok := schemaScalarType(property); !ok {
			continue
		}
		var value any
		if err := json.Unmarshal(property.Default, &value); err != nil {
			continue
		}
		defaults[name] = value
	}
	return defaults
}

func resolveInlineSchemaEntries(plan *dagParamPlan, rawParams string, paramsList []string) ([]dagParamEntry, error) {
	if plan.schema != nil {
		return resolveExternalSchemaEntries(plan, rawParams, paramsList)
	}
	return resolveInlineSchemaEntriesNoValidation(plan, rawParams, paramsList)
}

func resolveInlineSchemaEntriesNoValidation(plan *dagParamPlan, rawParams string, paramsList []string) ([]dagParamEntry, error) {
	overridePairs, err := parseOverridePairs(rawParams, paramsList)
	if err != nil {
		return nil, err
	}
	overridePairs, internalPairs := splitInternalRuntimeOverridePairs(overridePairs, declaredRuntimeParamNamesForPlan(plan))

	values := make(map[string]any, len(plan.entries)+len(overridePairs))
	for _, entry := range plan.entries {
		if !entry.HasValue || entry.Name == "" {
			continue
		}
		values[entry.Name] = entry.Value
	}

	for _, pair := range overridePairs {
		if pair.Name == "" {
			return nil, fmt.Errorf("positional parameters are not supported for schema-backed params")
		}
		if plan.schemaClosed {
			if _, ok := plan.schemaProperties[pair.Name]; !ok {
				accepted := slices.Clone(plan.schemaOrder)
				sortStrings(accepted)
				return nil, fmt.Errorf(
					"unknown parameter(s): %s; accepted parameters are: %s",
					quotedNames([]string{pair.Name}),
					strings.Join(accepted, ", "),
				)
			}
		}
		values[pair.Name] = pair.Value
	}

	return appendInternalRuntimeEntries(entriesFromTypedMap(values, plan.schemaOrder), internalPairs), nil
}

func schemaDisallowsAdditionalProperties(root *jsonschema.Schema) bool {
	if root == nil || root.AdditionalProperties == nil {
		return false
	}
	ap := root.AdditionalProperties
	return ap.Not != nil && isEmptySchema(ap.Not)
}

func isEmptySchema(s *jsonschema.Schema) bool {
	return s != nil && reflect.ValueOf(*s).IsZero()
}

func sortStrings(values []string) {
	if len(values) < 2 {
		return
	}
	slices.Sort(values)
}
