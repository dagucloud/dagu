// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

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
			Title:       property.Title,
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
	overridePairs, passThroughPairs := splitPassThroughOverridePairs(overridePairs, declaredRuntimeParamNames(plan.entries), plan.inheritedNames)

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

	entries, err := keepUntypedValueText(entriesFromTypedMap(typedMap, plan.schemaOrder), typedMap, slices.Concat(basePairs, overridePairs), plan.schema, plan.schemaProperties)
	if err != nil {
		return nil, err
	}
	return appendPassThroughEntries(entries, passThroughPairs), nil
}

func validateSchemaMap(values map[string]any, schema *jsonschema.Resolved, metadataMode bool) (map[string]any, error) {
	working := maps.Clone(values)
	if err := fillDefaults(working, schema.Schema(), false); err != nil {
		return nil, fmt.Errorf("failed to apply schema defaults: %w", err)
	}
	if err := schema.ApplyDefaults(&working); err != nil {
		return nil, fmt.Errorf("failed to apply schema defaults: %w", err)
	}

	validateSchema := schema
	if metadataMode && len(schema.Schema().Required) > 0 {
		if !hasAllRequiredKeys(working, schema.Schema().Required) {
			clone := schema.Schema().CloneSchemas()
			clone.Required = nil
			// The defaults were checked when schema resolved. Checking them
			// again would compare float64 defaults with exact enum members.
			resolved, err := clone.Resolve(nil)
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

// keepUntypedValueText gives each value the schema does not type the text the
// caller supplied. Such values are decoded only for validation, and encoding
// the decoded form again would change them, such as 1.10 becoming 1.1. A value
// that schema defaults filled is encoded again with its numbers as written.
func keepUntypedValueText(entries []dagParamEntry, validated map[string]any, pairs []paramPair, schema *jsonschema.Resolved, properties map[string]*jsonschema.Schema) ([]dagParamEntry, error) {
	supplied := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		if pair.Name == "" {
			continue
		}
		if _, typed := schemaScalarType(properties[pair.Name]); !typed {
			supplied[pair.Name] = pair.Value
		}
	}
	for i := range entries {
		name := entries[i].Name
		text, ok := supplied[name]
		if !ok {
			continue
		}
		if reflect.DeepEqual(parseJSONLikeValue(text), validated[name]) {
			entries[i].Value = text
			continue
		}
		filled := map[string]any{name: parseJSONNumberValue(text)}
		if err := schema.ApplyDefaults(&filled); err != nil {
			return nil, fmt.Errorf("failed to apply schema defaults: %w", err)
		}
		entries[i].Value = stringifyTypedValue(filled[name])
	}
	return entries, nil
}

func coerceSchemaPairValue(name, raw string, schema *jsonschema.Schema, allowSchemaFallbackJSON bool) (any, error) {
	paramType, ok := schemaScalarType(schema)
	if ok {
		value, err := coerceStringToType(raw, paramType)
		if err != nil && paramType == ir.ParamDefTypeInteger && schema.Type == "" && len(schema.Types) == 0 {
			// An untyped param takes its type from its enum, whose exact
			// members above the int64 range are uint64.
			if number, parseErr := strconv.ParseUint(strings.TrimSpace(raw), 10, 64); parseErr == nil {
				return number, nil
			}
		}
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

// parseJSONLikeValue decodes value as JSON for schema validation, or returns it
// as is when it is not JSON. Integers that fit in 64 bits keep their exact
// value, so bounds are checked against the number the run receives.
func parseJSONLikeValue(value string) any {
	if decoded, ok := exactJSONValue(value); ok {
		return decoded
	}
	return value
}

// exactJSONValue decodes text as one JSON value, keeping each integer that
// fits in 64 bits exact.
func exactJSONValue(text string) (any, bool) {
	decoded, ok := decodeJSONNumbers(text)
	if !ok {
		return nil, false
	}
	return validationNumbers(decoded)
}

// maxExactFloatInteger bounds the integers float64 holds exactly: every
// integer of smaller magnitude has an exact float64.
const maxExactFloatInteger = 1 << 53

// largeInteger reports whether value is an int64 or uint64 that float64 would
// round.
func largeInteger(value any) bool {
	switch v := value.(type) {
	case int64:
		return v > maxExactFloatInteger || v < -maxExactFloatInteger
	case uint64:
		return v > maxExactFloatInteger
	default:
		return false
	}
}

// numberTyped reports whether schema declares type number. Its values are
// coerced to float64, so its enum members and default stay float64 too.
func numberTyped(schema *jsonschema.Schema) bool {
	return schema.Type == "number" || slices.Contains(schema.Types, "number")
}

// decodeDefault decodes the default of property as the schema library does,
// with float64 numbers, except that an integer float64 would round stays exact
// unless property is number typed.
func decodeDefault(property *jsonschema.Schema) (any, bool) {
	if !numberTyped(property) {
		if exact, ok := exactJSONValue(string(property.Default)); ok && largeInteger(exact) {
			return exact, true
		}
	}
	var value any
	if err := json.Unmarshal(property.Default, &value); err != nil {
		return nil, false
	}
	return value, true
}

// fillDefaults sets each top-level property that values omits to its default,
// decoded by decodeDefault, since the schema library's own filling would round
// a large integer. Like the library, it skips required properties unless
// withRequired is set.
func fillDefaults(values map[string]any, schema *jsonschema.Schema, withRequired bool) error {
	for name, property := range schema.Properties {
		if _, supplied := values[name]; supplied || property == nil || len(property.Default) == 0 {
			continue
		}
		if !withRequired && slices.Contains(schema.Required, name) {
			continue
		}
		value, ok := decodeDefault(property)
		if !ok {
			return fmt.Errorf("%s: invalid default %s", name, property.Default)
		}
		values[name] = value
	}
	return nil
}

// schemaNumbers holds the members of a schema that the schema library decodes
// as float64.
type schemaNumbers struct {
	Enum  []any           `json:"enum"`
	Const any             `json:"const"`
	OneOf []schemaNumbers `json:"oneOf"`
	AnyOf []schemaNumbers `json:"anyOf"`
}

// keepExactEnums replaces each enum and const member that float64 rounds, in
// each top-level property of schema that is not number typed and in its oneOf
// and anyOf members, with the exact integer in data, the JSON that schema was
// decoded from. The property's type decides for its members, as it decides
// how its values and default decode. Call it after Resolve, which checks
// defaults as float64 against those members.
func keepExactEnums(schema *jsonschema.Schema, data []byte) {
	var root struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(data, &root) != nil {
		return
	}
	for name, raw := range root.Properties {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		property := schema.Properties[name]
		var numbers schemaNumbers
		// A boolean property schema has no members to keep.
		if property != nil && !numberTyped(property) && decoder.Decode(&numbers) == nil {
			numbers.apply(property)
		}
	}
}

func (n schemaNumbers) apply(schema *jsonschema.Schema) {
	if schema == nil {
		return
	}
	if len(n.Enum) == len(schema.Enum) {
		for i, member := range n.Enum {
			if exact, ok := exactLargeInteger(member); ok {
				schema.Enum[i] = exact
			}
		}
	}
	if exact, ok := exactLargeInteger(n.Const); ok && schema.Const != nil {
		schema.Const = &exact
	}
	for i, member := range n.OneOf {
		if i < len(schema.OneOf) {
			member.apply(schema.OneOf[i])
		}
	}
	for i, member := range n.AnyOf {
		if i < len(schema.AnyOf) {
			member.apply(schema.AnyOf[i])
		}
	}
}

// exactLargeInteger returns member as an exact integer when it is an integer
// literal that float64 would round.
func exactLargeInteger(member any) (any, bool) {
	number, ok := member.(json.Number)
	if !ok {
		return nil, false
	}
	exact, ok := validationNumbers(number)
	return exact, ok && largeInteger(exact)
}

// parseJSONNumberValue decodes value like parseJSONLikeValue but keeps each
// number as written.
func parseJSONNumberValue(value string) any {
	decoded, ok := decodeJSONNumbers(value)
	if !ok {
		return value
	}
	return decoded
}

func decodeJSONNumbers(value string) (any, bool) {
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, false
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, false
	}
	return decoded, true
}

// validationNumbers replaces each json.Number in value with a Go number, which
// the schema validator requires. It reports false for a number out of float64
// range, as json.Unmarshal does.
func validationNumbers(value any) (any, bool) {
	switch v := value.(type) {
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return n, true
		}
		if n, err := strconv.ParseUint(v.String(), 10, 64); err == nil {
			return n, true
		}
		f, err := v.Float64()
		return f, err == nil
	case map[string]any:
		for key, item := range v {
			converted, ok := validationNumbers(item)
			if !ok {
				return nil, false
			}
			v[key] = converted
		}
		return v, true
	case []any:
		for i, item := range v {
			converted, ok := validationNumbers(item)
			if !ok {
				return nil, false
			}
			v[i] = converted
		}
		return v, true
	default:
		return v, true
	}
}
