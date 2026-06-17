// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/google/jsonschema-go/jsonschema"
)

type inlineParamDefinition struct {
	name   string
	fields map[string]any
}

type compiledInlineParamSchema struct {
	resolved   *jsonschema.Resolved
	properties map[string]*jsonschema.Schema
	order      []string
}

func detectInlineParamDefinition(item map[string]any) (*inlineParamDefinition, error) {
	if isLegacyInlineParamDefinition(item) {
		return nil, inlineParamLegacyShapeError(item)
	}

	nameValue, hasName := item["name"]
	if !hasName {
		return nil, nil
	}

	name, ok := nameValue.(string)
	if !ok {
		return nil, fmt.Errorf(`inline parameter definition field "name" must be a string`)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf(`inline parameter definition field "name" must not be empty`)
	}
	if len(item) == 1 {
		return nil, fmt.Errorf("parameter %q must define at least one field in addition to name", name)
	}

	fields := make(map[string]any, len(item)-1)
	for key, value := range item {
		if key == "name" {
			continue
		}
		fields[key] = value
	}

	return &inlineParamDefinition{name: name, fields: fields}, nil
}

func isLegacyInlineParamDefinition(item map[string]any) bool {
	for _, value := range item {
		if _, ok := value.(map[string]any); ok {
			return true
		}
	}
	return false
}

func inlineParamLegacyShapeError(item map[string]any) error {
	for name, value := range item {
		if _, ok := value.(map[string]any); !ok {
			continue
		}
		return fmt.Errorf(
			"inline parameter definitions must use object form with name; rewrite `- %s: { ... }` as `- name: %s` with the definition fields on the same object",
			name,
			name,
		)
	}
	return fmt.Errorf(`inline parameter definitions must use object form with "name"`)
}

func parseInlineParamDefinition(name string, raw map[string]any) (core.ParamDef, dagParamEntry, error) {
	def := core.ParamDef{
		Name: name,
		Type: core.ParamDefTypeString,
	}
	entry := dagParamEntry{Name: name}

	allowedKeys := map[string]struct{}{
		"default":     {},
		"eval":        {},
		"description": {},
		"type":        {},
		"required":    {},
		"enum":        {},
		"minimum":     {},
		"maximum":     {},
		"min_length":  {},
		"max_length":  {},
		"pattern":     {},
	}

	if len(raw) == 0 {
		return def, entry, fmt.Errorf("parameter %q must define at least one field in addition to name", name)
	}

	for key := range raw {
		if _, ok := allowedKeys[key]; ok {
			continue
		}
		switch key {
		case "minLength":
			return def, entry, fmt.Errorf("invalid inline param field %q; use min_length", key)
		case "maxLength":
			return def, entry, fmt.Errorf("invalid inline param field %q; use max_length", key)
		default:
			return def, entry, fmt.Errorf("invalid inline param field %q", key)
		}
	}

	if value, ok := raw["type"]; ok {
		typeName, ok := value.(string)
		if !ok {
			return def, entry, fmt.Errorf("parameter %q type must be a string", name)
		}
		typeName = strings.TrimSpace(typeName)
		switch typeName {
		case "", core.ParamDefTypeString:
			def.Type = core.ParamDefTypeString
		case core.ParamDefTypeInteger, core.ParamDefTypeNumber, core.ParamDefTypeBoolean:
			def.Type = typeName
		default:
			return def, entry, fmt.Errorf("parameter %q has unsupported type %q", name, typeName)
		}
	}

	if value, ok := raw["description"]; ok {
		description, ok := value.(string)
		if !ok {
			return def, entry, fmt.Errorf("parameter %q description must be a string", name)
		}
		def.Description = description
	}

	if value, ok := raw["eval"]; ok {
		expr, ok := value.(string)
		if !ok {
			return def, entry, fmt.Errorf("parameter %q eval must be a string", name)
		}
		expr = strings.TrimSpace(expr)
		if expr == "" {
			return def, entry, fmt.Errorf("parameter %q eval must not be empty", name)
		}
		entry.Eval = expr
	}

	if value, ok := raw["required"]; ok {
		required, ok := value.(bool)
		if !ok {
			return def, entry, fmt.Errorf("parameter %q required must be a boolean", name)
		}
		def.Required = required
	}

	if value, ok := raw["minimum"]; ok {
		number, err := toFloat64(value)
		if err != nil {
			return def, entry, fmt.Errorf("parameter %q minimum must be numeric: %w", name, err)
		}
		def.Minimum = &number
	}

	if value, ok := raw["maximum"]; ok {
		number, err := toFloat64(value)
		if err != nil {
			return def, entry, fmt.Errorf("parameter %q maximum must be numeric: %w", name, err)
		}
		def.Maximum = &number
	}

	if value, ok := raw["min_length"]; ok {
		number, err := toInt(value)
		if err != nil {
			return def, entry, fmt.Errorf("parameter %q min_length must be an integer: %w", name, err)
		}
		if number < 0 {
			return def, entry, fmt.Errorf("parameter %q min_length must be non-negative", name)
		}
		def.MinLength = &number
	}

	if value, ok := raw["max_length"]; ok {
		number, err := toInt(value)
		if err != nil {
			return def, entry, fmt.Errorf("parameter %q max_length must be an integer: %w", name, err)
		}
		if number < 0 {
			return def, entry, fmt.Errorf("parameter %q max_length must be non-negative", name)
		}
		def.MaxLength = &number
	}

	if value, ok := raw["pattern"]; ok {
		pattern, ok := value.(string)
		if !ok {
			return def, entry, fmt.Errorf("parameter %q pattern must be a string", name)
		}
		def.Pattern = &pattern
	}

	if err := validateInlineConstraintCompatibility(def); err != nil {
		return def, entry, err
	}

	if value, ok := raw["enum"]; ok {
		rawItems, ok := value.([]any)
		if !ok {
			return def, entry, fmt.Errorf("parameter %q enum must be a list", name)
		}
		def.Enum = make([]any, 0, len(rawItems))
		for _, item := range rawItems {
			normalized, err := normalizeTypedParamValue(item, def.Type)
			if err != nil {
				return def, entry, fmt.Errorf("parameter %q enum contains an invalid value: %w", name, err)
			}
			def.Enum = append(def.Enum, normalized)
		}
	}

	if value, ok := raw["default"]; ok {
		normalized, err := normalizeTypedParamValue(value, def.Type)
		if err != nil {
			return def, entry, fmt.Errorf("parameter %q default is invalid: %w", name, err)
		}
		def.Default = normalized
		entry.HasValue = true
		entry.Value = stringifyTypedValue(normalized)
	}

	compiledPattern, err := compileInlinePattern(def.Name, def.Pattern)
	if err != nil {
		return def, entry, err
	}

	if err := validateInlineDefault(def, compiledPattern); err != nil {
		return def, entry, err
	}

	return def, entry, nil
}

func validateInlineConstraintCompatibility(def core.ParamDef) error {
	isString := def.Type == core.ParamDefTypeString
	isNumeric := def.Type == core.ParamDefTypeInteger || def.Type == core.ParamDefTypeNumber

	if !isNumeric && (def.Minimum != nil || def.Maximum != nil) {
		return fmt.Errorf("parameter %q uses minimum/maximum but type is %q", def.Name, def.Type)
	}
	if !isString && (def.MinLength != nil || def.MaxLength != nil || def.Pattern != nil) {
		return fmt.Errorf("parameter %q uses string constraints but type is %q", def.Name, def.Type)
	}
	if def.Minimum != nil && def.Maximum != nil && *def.Minimum > *def.Maximum {
		return fmt.Errorf("parameter %q minimum must be less than or equal to maximum", def.Name)
	}
	if def.MinLength != nil && def.MaxLength != nil && *def.MinLength > *def.MaxLength {
		return fmt.Errorf("parameter %q min_length must be less than or equal to max_length", def.Name)
	}

	return nil
}

func compileInlinePattern(name string, pattern *string) (*regexp.Regexp, error) {
	if pattern == nil {
		return nil, nil
	}

	re, err := regexp.Compile(*pattern)
	if err != nil {
		return nil, fmt.Errorf("parameter %q has invalid pattern: %w", name, err)
	}
	return re, nil
}

func validateInlineDefault(def core.ParamDef, compiledPattern *regexp.Regexp) error {
	if def.Default == nil {
		return nil
	}

	if len(def.Enum) > 0 && !containsTypedValue(def.Enum, def.Default) {
		return fmt.Errorf("parameter %q default must be one of enum values", def.Name)
	}

	switch value := def.Default.(type) {
	case string:
		if def.MinLength != nil && len(value) < *def.MinLength {
			return fmt.Errorf("parameter %q default is shorter than min_length", def.Name)
		}
		if def.MaxLength != nil && len(value) > *def.MaxLength {
			return fmt.Errorf("parameter %q default is longer than max_length", def.Name)
		}
		if compiledPattern != nil && !compiledPattern.MatchString(value) {
			return fmt.Errorf("parameter %q default does not match pattern", def.Name)
		}
	case int64:
		number := float64(value)
		if def.Minimum != nil && number < *def.Minimum {
			return fmt.Errorf("parameter %q default is below minimum", def.Name)
		}
		if def.Maximum != nil && number > *def.Maximum {
			return fmt.Errorf("parameter %q default is above maximum", def.Name)
		}
	case float64:
		if def.Minimum != nil && value < *def.Minimum {
			return fmt.Errorf("parameter %q default is below minimum", def.Name)
		}
		if def.Maximum != nil && value > *def.Maximum {
			return fmt.Errorf("parameter %q default is above maximum", def.Name)
		}
	}

	return nil
}

func compileInlineParamSchema(defs []core.ParamDef) (*compiledInlineParamSchema, error) {
	root := &jsonschema.Schema{
		Type:                 "object",
		Properties:           map[string]*jsonschema.Schema{},
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	}
	properties := map[string]*jsonschema.Schema{}
	order := make([]string, 0, len(defs))

	for _, def := range defs {
		if def.Name == "" {
			continue
		}

		property := &jsonschema.Schema{
			Type:        def.Type,
			Description: def.Description,
		}
		if def.Default != nil {
			data, err := json.Marshal(def.Default)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal default for parameter %q: %w", def.Name, err)
			}
			property.Default = data
		}
		if len(def.Enum) > 0 {
			property.Enum = append([]any(nil), def.Enum...)
		}
		if def.Minimum != nil {
			minimum := *def.Minimum
			property.Minimum = &minimum
		}
		if def.Maximum != nil {
			maximum := *def.Maximum
			property.Maximum = &maximum
		}
		if def.MinLength != nil {
			minLength := *def.MinLength
			property.MinLength = &minLength
		}
		if def.MaxLength != nil {
			maxLength := *def.MaxLength
			property.MaxLength = &maxLength
		}
		if def.Pattern != nil {
			property.Pattern = *def.Pattern
		}

		root.Properties[def.Name] = property
		properties[def.Name] = property
		order = append(order, def.Name)
		if def.Required {
			root.Required = append(root.Required, def.Name)
		}
	}

	root.PropertyOrder = append([]string(nil), order...)

	resolved, err := root.Resolve(&jsonschema.ResolveOptions{
		ValidateDefaults: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to resolve inline parameter schema: %w", err)
	}

	return &compiledInlineParamSchema{
		resolved:   resolved,
		properties: properties,
		order:      order,
	}, nil
}
