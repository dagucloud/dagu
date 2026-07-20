// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/google/jsonschema-go/jsonschema"
)

var humanTaskFormRootFields = map[string]struct{}{
	"type":                 {},
	"title":                {},
	"description":          {},
	"properties":           {},
	"required":             {},
	"additionalProperties": {},
}

var humanTaskFormPropertyFields = map[string]struct{}{
	"type":        {},
	"title":       {},
	"description": {},
	"default":     {},
	"enum":        {},
	"oneOf":       {},
	"minimum":     {},
	"maximum":     {},
	"minLength":   {},
	"maxLength":   {},
	"pattern":     {},
}

var humanTaskFormOneOfFields = map[string]struct{}{
	"type":        {},
	"title":       {},
	"description": {},
	"const":       {},
}

// HumanTaskInputResult contains validated form values and their step outputs.
type HumanTaskInputResult struct {
	Values    map[string]any
	Canonical json.RawMessage
	Outputs   map[string]string
}

func buildHumanTaskForm(raw any) (json.RawMessage, []core.StepOutputDeclaration, error) {
	if raw == nil {
		return nil, nil, nil
	}

	form, ok := raw.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("form must be an object schema")
	}
	form = cloneMap(form)
	if err := validateHumanTaskFormShape(form); err != nil {
		return nil, nil, err
	}
	if _, ok := form["additionalProperties"]; !ok {
		form["additionalProperties"] = false
	}

	data, err := json.Marshal(form)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal form schema: %w", err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, nil, fmt.Errorf("parse form schema: %w", err)
	}
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		return nil, nil, fmt.Errorf("resolve form schema: %w", err)
	}
	root := resolved.Schema()

	var sanitized *jsonschema.Schema
	if len(root.Properties) == 0 {
		sanitized = &jsonschema.Schema{
			Type:        "object",
			Title:       root.Title,
			Description: root.Description,
			Properties:  map[string]*jsonschema.Schema{},
		}
	} else {
		var renderable bool
		sanitized, renderable = sanitizeRenderableParamSchema(root)
		if !renderable {
			return nil, nil, fmt.Errorf("form must use flat scalar fields supported by Dagu forms")
		}
		if err := preserveHumanTaskOneOfConstraints(root, sanitized); err != nil {
			return nil, nil, err
		}
	}

	normalizedData, err := json.Marshal(sanitized)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal normalized form schema: %w", err)
	}
	var normalized map[string]any
	if err := json.Unmarshal(normalizedData, &normalized); err != nil {
		return nil, nil, fmt.Errorf("decode normalized form schema: %w", err)
	}
	normalized["additionalProperties"] = form["additionalProperties"]
	normalizedData, err = json.Marshal(normalized)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal normalized form schema: %w", err)
	}

	outputs, err := deriveHumanTaskOutputs(sanitized)
	if err != nil {
		return nil, nil, err
	}
	return json.RawMessage(normalizedData), outputs, nil
}

func preserveHumanTaskOneOfConstraints(source, target *jsonschema.Schema) error {
	for name, sourceProperty := range source.Properties {
		if sourceProperty == nil || len(sourceProperty.OneOf) == 0 {
			continue
		}
		targetProperty := target.Properties[name]
		if targetProperty == nil || len(targetProperty.OneOf) != len(sourceProperty.OneOf) {
			return fmt.Errorf("form property %q has an invalid oneOf schema", name)
		}

		if propertyType, authored, valid := humanTaskAuthoredScalarType(sourceProperty); authored {
			if !valid {
				return fmt.Errorf("form property %q oneOf type must be scalar", name)
			}
			for idx, option := range sourceProperty.OneOf {
				if option == nil || option.Const == nil || !humanTaskScalarTypeAccepts(propertyType, *option.Const) {
					return fmt.Errorf("form property %q oneOf[%d].const does not match type %s", name, idx, propertyType)
				}
			}
			targetProperty.Type = propertyType
		}

		for idx, sourceOption := range sourceProperty.OneOf {
			optionType, authored, valid := humanTaskAuthoredScalarType(sourceOption)
			if !authored {
				continue
			}
			if !valid || sourceOption.Const == nil || !humanTaskScalarTypeAccepts(optionType, *sourceOption.Const) {
				return fmt.Errorf("form property %q oneOf[%d].const does not match its type", name, idx)
			}
			targetProperty.OneOf[idx].Type = optionType
		}

		targetProperty.Pattern = sourceProperty.Pattern
		targetProperty.Enum = cloneSchemaEnumValues(sourceProperty.Enum)
		targetProperty.Minimum = cloneFloat64(sourceProperty.Minimum)
		targetProperty.Maximum = cloneFloat64(sourceProperty.Maximum)
		targetProperty.MinLength = cloneInt(sourceProperty.MinLength)
		targetProperty.MaxLength = cloneInt(sourceProperty.MaxLength)
	}
	return nil
}

func humanTaskAuthoredScalarType(schema *jsonschema.Schema) (value string, authored, valid bool) {
	if schema == nil {
		return "", false, false
	}
	if schema.Type != "" {
		_, valid := humanTaskScalarTypes[schema.Type]
		return schema.Type, true, valid
	}
	if len(schema.Types) > 0 {
		if len(schema.Types) != 1 {
			return "", true, false
		}
		_, valid := humanTaskScalarTypes[schema.Types[0]]
		return schema.Types[0], true, valid
	}
	return "", false, false
}

var humanTaskScalarTypes = map[string]struct{}{
	core.ParamDefTypeString:  {},
	core.ParamDefTypeInteger: {},
	core.ParamDefTypeNumber:  {},
	core.ParamDefTypeBoolean: {},
}

func humanTaskScalarTypeAccepts(schemaType string, value any) bool {
	switch schemaType {
	case core.ParamDefTypeString:
		_, ok := value.(string)
		return ok
	case core.ParamDefTypeBoolean:
		_, ok := value.(bool)
		return ok
	case core.ParamDefTypeInteger:
		switch typed := value.(type) {
		case float64:
			return typed == math.Trunc(typed)
		case float32:
			return typed == float32(math.Trunc(float64(typed)))
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			return true
		default:
			return false
		}
	case core.ParamDefTypeNumber:
		switch value.(type) {
		case float32, float64, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func validateHumanTaskFormShape(form map[string]any) error {
	properties, err := validateHumanTaskFormRoot(form)
	if err != nil {
		return err
	}
	for name, rawProperty := range properties {
		if err := validateHumanTaskFormProperty(name, rawProperty); err != nil {
			return err
		}
	}
	return validateHumanTaskRequired(form, properties)
}

func validateHumanTaskFormRoot(form map[string]any) (map[string]any, error) {
	for name := range form {
		if _, ok := humanTaskFormRootFields[name]; !ok {
			return nil, fmt.Errorf("unsupported form schema field %q", name)
		}
	}
	if formType, ok := form["type"].(string); !ok || formType != "object" {
		return nil, fmt.Errorf("form type must be object")
	}
	if err := validateHumanTaskStringMetadata(form, "form"); err != nil {
		return nil, err
	}

	propertiesRaw, exists := form["properties"]
	if !exists {
		propertiesRaw = map[string]any{}
		form["properties"] = propertiesRaw
	}
	properties, ok := propertiesRaw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("form properties must be an object")
	}
	if value, ok := form["additionalProperties"]; ok {
		if _, ok := value.(bool); !ok {
			return nil, fmt.Errorf("form additionalProperties must be a boolean")
		}
	}
	return properties, nil
}

func validateHumanTaskFormProperty(name string, rawProperty any) error {
	if !declaredOutputNamePattern.MatchString(name) {
		return fmt.Errorf("form property name %q must match %q", name, declaredOutputNamePattern.String())
	}
	property, ok := rawProperty.(map[string]any)
	if !ok {
		return fmt.Errorf("form property %q must be an object", name)
	}
	for field := range property {
		if _, ok := humanTaskFormPropertyFields[field]; !ok {
			return fmt.Errorf("form property %q uses unsupported schema field %q", name, field)
		}
	}
	if _, hasType := property["type"]; !hasType {
		if _, hasEnum := property["enum"]; !hasEnum {
			if _, hasOneOf := property["oneOf"]; !hasOneOf {
				return fmt.Errorf("form property %q must define type, enum, or oneOf", name)
			}
		}
	}
	if propertyType, exists := property["type"]; exists {
		if err := validateHumanTaskScalarType(propertyType, fmt.Sprintf("form property %q type", name)); err != nil {
			return err
		}
	}
	if err := validateHumanTaskStringMetadata(property, fmt.Sprintf("form property %q", name)); err != nil {
		return err
	}
	if value, exists := property["default"]; exists && !isHumanTaskFormScalar(value) {
		return fmt.Errorf("form property %q default must be a scalar value", name)
	}
	if rawEnum, exists := property["enum"]; exists {
		if err := validateHumanTaskFormEnum(name, rawEnum); err != nil {
			return err
		}
	}
	for _, field := range []string{"minimum", "maximum"} {
		if value, exists := property[field]; exists && !isHumanTaskFormNumber(value) {
			return fmt.Errorf("form property %q %s must be a number", name, field)
		}
	}
	for _, field := range []string{"minLength", "maxLength"} {
		if value, exists := property[field]; exists && !isHumanTaskFormNonNegativeInteger(value) {
			return fmt.Errorf("form property %q %s must be a non-negative integer", name, field)
		}
	}
	if value, exists := property["pattern"]; exists {
		if _, ok := value.(string); !ok {
			return fmt.Errorf("form property %q pattern must be a string", name)
		}
	}
	if options, exists := property["oneOf"]; exists {
		if err := validateHumanTaskFormOneOf(name, options); err != nil {
			return err
		}
	}
	return nil
}

func validateHumanTaskFormEnum(name string, rawEnum any) error {
	items, ok := humanTaskFormItems(rawEnum)
	if !ok || len(items) == 0 {
		return fmt.Errorf("form property %q enum must be a non-empty array", name)
	}
	seen := make(map[string]struct{}, len(items))
	for idx, item := range items {
		if !isHumanTaskFormScalar(item) {
			return fmt.Errorf("form property %q enum[%d] must be a scalar value", name, idx)
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			return fmt.Errorf("form property %q enum[%d] is invalid: %w", name, idx, err)
		}
		key := string(encoded)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("form property %q enum contains duplicate value %s", name, key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateHumanTaskFormOneOf(name string, options any) error {
	items, ok := humanTaskFormItems(options)
	if !ok || len(items) == 0 {
		return fmt.Errorf("form property %q oneOf must be a non-empty array", name)
	}
	for idx, rawOption := range items {
		option, ok := rawOption.(map[string]any)
		if !ok {
			return fmt.Errorf("form property %q oneOf[%d] must be an object", name, idx)
		}
		for field := range option {
			if _, ok := humanTaskFormOneOfFields[field]; !ok {
				return fmt.Errorf("form property %q oneOf[%d] uses unsupported schema field %q", name, idx, field)
			}
		}
		label := fmt.Sprintf("form property %q oneOf[%d]", name, idx)
		if err := validateHumanTaskStringMetadata(option, label); err != nil {
			return err
		}
		if optionType, exists := option["type"]; exists {
			if err := validateHumanTaskScalarType(optionType, label+" type"); err != nil {
				return err
			}
		}
		constValue, ok := option["const"]
		if !ok {
			return fmt.Errorf("form property %q oneOf[%d].const is required", name, idx)
		}
		if !isHumanTaskFormScalar(constValue) {
			return fmt.Errorf("form property %q oneOf[%d].const must be a scalar value", name, idx)
		}
	}
	return nil
}

func validateHumanTaskRequired(form map[string]any, properties map[string]any) error {
	requiredRaw, hasRequired := form["required"]
	if hasRequired && requiredRaw == nil {
		return fmt.Errorf("form required must be an array of property names")
	}
	required, err := humanTaskRequiredNames(requiredRaw)
	if err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(required))
	for _, name := range required {
		if _, ok := properties[name]; !ok {
			return fmt.Errorf("required form property %q is not declared", name)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("required form property %q is duplicated", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func validateHumanTaskStringMetadata(value map[string]any, label string) error {
	for _, field := range []string{"title", "description"} {
		if raw, exists := value[field]; exists {
			if _, ok := raw.(string); !ok {
				return fmt.Errorf("%s %s must be a string", label, field)
			}
		}
	}
	return nil
}

func validateHumanTaskScalarType(raw any, label string) error {
	value, ok := raw.(string)
	if !ok {
		return fmt.Errorf("%s must be a scalar type", label)
	}
	if _, ok := humanTaskScalarTypes[value]; !ok {
		return fmt.Errorf("%s must be string, integer, number, or boolean", label)
	}
	return nil
}

func humanTaskFormItems(raw any) ([]any, bool) {
	if items, ok := raw.([]any); ok {
		return items, true
	}
	if strings, ok := raw.([]string); ok {
		items := make([]any, len(strings))
		for idx := range strings {
			items[idx] = strings[idx]
		}
		return items, true
	}
	return nil, false
}

func isHumanTaskFormScalar(value any) bool {
	switch value.(type) {
	case string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, json.Number:
		return true
	default:
		return false
	}
}

func isHumanTaskFormNumber(value any) bool {
	switch value.(type) {
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, json.Number:
		return true
	default:
		return false
	}
}

func isHumanTaskFormNonNegativeInteger(value any) bool {
	switch typed := value.(type) {
	case int:
		return typed >= 0
	case int8:
		return typed >= 0
	case int16:
		return typed >= 0
	case int32:
		return typed >= 0
	case int64:
		return typed >= 0
	case uint, uint8, uint16, uint32, uint64:
		return true
	case float32:
		return typed >= 0 && typed == float32(math.Trunc(float64(typed)))
	case float64:
		return typed >= 0 && typed == math.Trunc(typed)
	case json.Number:
		value, err := typed.Float64()
		return err == nil && value >= 0 && value == math.Trunc(value)
	default:
		return false
	}
}

func humanTaskRequiredNames(raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		if strings, ok := raw.([]string); ok {
			return strings, nil
		}
		return nil, fmt.Errorf("form required must be an array of property names")
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		name, ok := item.(string)
		if !ok || name == "" {
			return nil, fmt.Errorf("form required must contain property names")
		}
		result = append(result, name)
	}
	return result, nil
}

func deriveHumanTaskOutputs(root *jsonschema.Schema) ([]core.StepOutputDeclaration, error) {
	required := make(map[string]struct{}, len(root.Required))
	for _, name := range root.Required {
		required[name] = struct{}{}
	}
	outputs := make([]core.StepOutputDeclaration, 0, len(root.Properties))
	for _, name := range topLevelSchemaOrder(root) {
		property := root.Properties[name]
		_, isRequired := required[name]
		if !isRequired && (property == nil || len(property.Default) == 0) {
			continue
		}
		propertyType, ok := schemaScalarType(property)
		if !ok {
			return nil, fmt.Errorf("form property %q must have a scalar type", name)
		}
		outputType := core.StepDeclaredOutputTypeJSON
		if propertyType == core.ParamDefTypeString {
			outputType = core.StepDeclaredOutputTypeString
		}
		outputs = append(outputs, core.StepOutputDeclaration{Name: name, Type: outputType})
	}
	return outputs, nil
}

// ValidateHumanTaskInputs applies form defaults and validates completion input.
func ValidateHumanTaskInputs(form json.RawMessage, inputs map[string]any, coerceStrings bool) (*HumanTaskInputResult, error) {
	values := maps.Clone(inputs)
	if values == nil {
		values = map[string]any{}
	}
	if len(form) == 0 {
		if len(values) > 0 {
			return nil, fmt.Errorf("this human task does not accept input")
		}
		canonical, _ := json.Marshal(values)
		return &HumanTaskInputResult{Values: values, Canonical: canonical, Outputs: map[string]string{}}, nil
	}

	var schema jsonschema.Schema
	if err := json.Unmarshal(form, &schema); err != nil {
		return nil, fmt.Errorf("parse stored human task form: %w", err)
	}
	resolved, err := schema.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		return nil, fmt.Errorf("resolve stored human task form: %w", err)
	}
	for name, value := range values {
		property, declared := resolved.Schema().Properties[name]
		if !declared {
			continue
		}
		var raw string
		switch typed := value.(type) {
		case json.Number:
			if integer, err := typed.Int64(); err == nil {
				values[name] = integer
				continue
			}
			number, err := typed.Float64()
			if err != nil {
				return nil, fmt.Errorf("%s: invalid number %q", name, typed)
			}
			values[name] = number
			continue
		case string:
			if !coerceStrings {
				continue
			}
			raw = typed
		default:
			continue
		}
		coerced, err := coerceSchemaPairValue(name, raw, property, false)
		if err != nil {
			return nil, err
		}
		values[name] = coerced
	}
	values, err = validateSchemaMap(values, resolved, false)
	if err != nil {
		return nil, fmt.Errorf("human task form validation failed: %w", err)
	}
	canonical, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("marshal human task input: %w", err)
	}

	declarations, err := deriveHumanTaskOutputs(resolved.Schema())
	if err != nil {
		return nil, err
	}
	outputs := make(map[string]string, len(declarations))
	for _, declaration := range declarations {
		value, ok := values[declaration.Name]
		if !ok {
			continue
		}
		outputs[declaration.Name] = stringifyTypedValue(value)
	}
	return &HumanTaskInputResult{Values: values, Canonical: canonical, Outputs: outputs}, nil
}
