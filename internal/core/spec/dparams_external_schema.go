// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"encoding/json"
	"fmt"
	"maps"
	"sort"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/google/jsonschema-go/jsonschema"
)

func deriveExternalSchemaParamDefs(root *jsonschema.Schema, defaults map[string]any) ([]ir.ParamDef, bool) {
	if root == nil {
		return nil, false
	}
	if root.Type != "" && root.Type != "object" {
		return nil, false
	}
	if len(root.Properties) == 0 {
		return nil, false
	}
	if len(root.PatternProperties) > 0 || len(root.AllOf) > 0 || len(root.AnyOf) > 0 || len(root.OneOf) > 0 || root.Not != nil || root.If != nil || root.Then != nil || root.Else != nil {
		return nil, false
	}

	required := map[string]bool{}
	for _, name := range root.Required {
		required[name] = true
	}

	order := topLevelSchemaOrder(root)
	paramDefs := make([]ir.ParamDef, 0, len(order))
	for _, name := range order {
		property := root.Properties[name]
		if property == nil {
			continue
		}

		paramType, ok := schemaScalarType(property)
		if !ok {
			return nil, false
		}

		def := ir.ParamDef{
			Name:        name,
			Type:        paramType,
			Description: property.Description,
			Required:    required[name],
		}
		if value, ok := defaults[name]; ok {
			def.Default = value
		}
		if len(property.Enum) > 0 {
			def.Enum = append([]any(nil), property.Enum...)
		}
		if property.Minimum != nil {
			minimum := *property.Minimum
			def.Minimum = &minimum
		}
		if property.Maximum != nil {
			maximum := *property.Maximum
			def.Maximum = &maximum
		}
		if property.MinLength != nil {
			minLength := *property.MinLength
			def.MinLength = &minLength
		}
		if property.MaxLength != nil {
			maxLength := *property.MaxLength
			def.MaxLength = &maxLength
		}
		if property.Pattern != "" {
			pattern := property.Pattern
			def.Pattern = &pattern
		}

		paramDefs = append(paramDefs, def)
	}

	extraNames := make([]string, 0)
	for name := range defaults {
		if _, ok := root.Properties[name]; ok {
			continue
		}
		extraNames = append(extraNames, name)
	}
	sort.Strings(extraNames)
	for _, name := range extraNames {
		paramDefs = append(paramDefs, ir.ParamDef{
			Name:    name,
			Type:    ir.ParamDefTypeString,
			Default: stringifyUntypedValue(defaults[name]),
		})
	}

	return paramDefs, true
}

func resolveExternalSchemaEntries(plan *dagParamPlan, rawParams string, paramsList []string) ([]dagParamEntry, error) {
	overridePairs, err := parseOverridePairs(rawParams, paramsList)
	if err != nil {
		return nil, err
	}
	overridePairs, internalPairs := splitInternalRuntimeOverridePairs(overridePairs, declaredRuntimeParamNames(plan.entries))

	basePairs := make([]paramPair, 0, len(plan.entries))
	for _, entry := range plan.entries {
		if !entry.HasValue {
			continue
		}
		basePairs = append(basePairs, paramPair{Name: entry.Name, Value: entry.Value})
	}

	typedMap, err := schemaPairsToMap(basePairs, plan.schemaProperties, true)
	if err != nil {
		return nil, err
	}
	for _, pair := range overridePairs {
		if pair.Name == "" {
			return nil, fmt.Errorf("positional parameters are not supported for schema-backed params")
		}
		value, err := coerceSchemaPairValue(pair.Name, pair.Value, plan.schemaProperties[pair.Name], true)
		if err != nil {
			return nil, err
		}
		typedMap[pair.Name] = value
	}

	typedMap, err = validateSchemaMap(typedMap, plan.schema, false)
	if err != nil {
		return nil, err
	}

	return appendInternalRuntimeEntries(entriesFromTypedMap(typedMap, plan.schemaOrder), internalPairs), nil
}

func validateSchemaMap(values map[string]any, schema *jsonschema.Resolved, metadataMode bool) (map[string]any, error) {
	working := maps.Clone(values)
	if err := schema.ApplyDefaults(&working); err != nil {
		return nil, fmt.Errorf("failed to apply schema defaults: %w", err)
	}

	validateSchema := schema
	if metadataMode && len(schema.Schema().Required) > 0 {
		if !hasAllRequiredKeys(working, schema.Schema().Required) {
			clone := schema.Schema().CloneSchemas()
			clone.Required = nil
			resolved, err := clone.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
			if err != nil {
				return nil, fmt.Errorf("failed to prepare parameter schema: %w", err)
			}
			validateSchema = resolved
		}
	}

	if err := validateSchema.Validate(working); err != nil {
		return nil, fmt.Errorf("parameter validation failed: %w", err)
	}

	return working, nil
}

func schemaPairsToMap(pairs []paramPair, properties map[string]*jsonschema.Schema, allowSchemaFallbackJSON bool) (map[string]any, error) {
	result := make(map[string]any, len(pairs))
	for _, pair := range pairs {
		if pair.Name == "" {
			continue
		}
		value, err := coerceSchemaPairValue(pair.Name, pair.Value, properties[pair.Name], allowSchemaFallbackJSON)
		if err != nil {
			return nil, err
		}
		result[pair.Name] = value
	}
	return result, nil
}

func coerceSchemaPairValue(name, raw string, schema *jsonschema.Schema, allowSchemaFallbackJSON bool) (any, error) {
	paramType, ok := schemaScalarType(schema)
	if ok {
		value, err := coerceStringToType(raw, paramType)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return value, nil
	}
	if allowSchemaFallbackJSON {
		return parseJSONLikeValue(raw), nil
	}
	return raw, nil
}

func schemaScalarType(schema *jsonschema.Schema) (string, bool) {
	if schema == nil {
		return "", false
	}
	switch schema.Type {
	case ir.ParamDefTypeString, ir.ParamDefTypeInteger, ir.ParamDefTypeNumber, ir.ParamDefTypeBoolean:
		return schema.Type, true
	}
	if len(schema.Types) == 1 {
		switch schema.Types[0] {
		case ir.ParamDefTypeString, ir.ParamDefTypeInteger, ir.ParamDefTypeNumber, ir.ParamDefTypeBoolean:
			return schema.Types[0], true
		}
	}
	if len(schema.Enum) == 0 {
		return "", false
	}

	firstType, ok := inferScalarType(schema.Enum[0])
	if !ok {
		return "", false
	}
	for _, item := range schema.Enum[1:] {
		itemType, ok := inferScalarType(item)
		if !ok || itemType != firstType {
			return "", false
		}
	}
	return firstType, true
}

func inferScalarType(value any) (string, bool) {
	switch value.(type) {
	case string:
		return ir.ParamDefTypeString, true
	case bool:
		return ir.ParamDefTypeBoolean, true
	case float32, float64:
		return ir.ParamDefTypeNumber, true
	case int, int8, int16, int32, int64:
		return ir.ParamDefTypeInteger, true
	case uint, uint8, uint16, uint32, uint64:
		return ir.ParamDefTypeInteger, true
	default:
		return "", false
	}
}

func mergeTypedMapIntoEntries(entries []dagParamEntry, typedValues map[string]any, schemaOrder []string) []dagParamEntry {
	result := cloneParamEntries(entries)
	remaining := maps.Clone(typedValues)
	seen := map[string]struct{}{}

	for i := range result {
		if result[i].Name == "" {
			continue
		}
		value, ok := remaining[result[i].Name]
		if !ok {
			result[i].HasValue = false
			result[i].Value = ""
			continue
		}
		result[i].Value = stringifyTypedValue(value)
		result[i].HasValue = true
		delete(remaining, result[i].Name)
		seen[result[i].Name] = struct{}{}
	}

	for _, name := range schemaOrder {
		if _, ok := seen[name]; ok {
			continue
		}
		value, ok := remaining[name]
		if !ok {
			continue
		}
		result = append(result, dagParamEntry{
			Name:     name,
			Value:    stringifyTypedValue(value),
			HasValue: true,
		})
		delete(remaining, name)
	}

	if len(remaining) == 0 {
		return result
	}

	extraNames := make([]string, 0, len(remaining))
	for name := range remaining {
		extraNames = append(extraNames, name)
	}
	sort.Strings(extraNames)
	for _, name := range extraNames {
		result = append(result, dagParamEntry{
			Name:     name,
			Value:    stringifyTypedValue(remaining[name]),
			HasValue: true,
		})
	}

	return result
}

func entriesFromTypedMap(values map[string]any, schemaOrder []string) []dagParamEntry {
	entries := make([]dagParamEntry, 0, len(values))
	seen := map[string]struct{}{}

	for _, name := range schemaOrder {
		value, ok := values[name]
		if !ok {
			continue
		}
		entries = append(entries, dagParamEntry{
			Name:     name,
			Value:    stringifyTypedValue(value),
			HasValue: true,
		})
		seen[name] = struct{}{}
	}

	extraNames := make([]string, 0)
	for name := range values {
		if _, ok := seen[name]; ok {
			continue
		}
		extraNames = append(extraNames, name)
	}
	sort.Strings(extraNames)
	for _, name := range extraNames {
		entries = append(entries, dagParamEntry{
			Name:     name,
			Value:    stringifyTypedValue(values[name]),
			HasValue: true,
		})
	}

	return entries
}

func topLevelSchemaOrder(root *jsonschema.Schema) []string {
	if root == nil || len(root.Properties) == 0 {
		return nil
	}

	if len(root.PropertyOrder) > 0 {
		order := make([]string, 0, len(root.Properties))
		seen := map[string]struct{}{}
		for _, name := range root.PropertyOrder {
			if _, ok := root.Properties[name]; !ok {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			order = append(order, name)
			seen[name] = struct{}{}
		}
		if len(order) == len(root.Properties) {
			return order
		}
		extraNames := make([]string, 0)
		for name := range root.Properties {
			if _, ok := seen[name]; ok {
				continue
			}
			extraNames = append(extraNames, name)
		}
		sort.Strings(extraNames)
		return append(order, extraNames...)
	}

	order := make([]string, 0, len(root.Properties))
	for name := range root.Properties {
		order = append(order, name)
	}
	sort.Strings(order)
	return order
}

func extractSchemaValues(input any) any {
	paramsMap, ok := input.(map[string]any)
	if !ok {
		return nil
	}
	return paramsMap["values"]
}

func hasAllRequiredKeys(values map[string]any, required []string) bool {
	for _, name := range required {
		if _, ok := values[name]; !ok {
			return false
		}
	}
	return true
}

func stringifyTypedValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(data)
	}
}

func stringifyUntypedValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}

func parseJSONLikeValue(value string) any {
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err == nil {
		return decoded
	}
	return value
}
