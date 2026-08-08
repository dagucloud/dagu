// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"encoding/json"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/google/jsonschema-go/jsonschema"
)

func buildRenderableParamSchema(resolved *jsonschema.Resolved) (json.RawMessage, error) {
	if resolved == nil || resolved.Schema() == nil {
		return nil, nil
	}

	sanitized, ok := sanitizeRenderableParamSchema(resolved.Schema())
	if !ok {
		return nil, nil
	}

	data, err := json.Marshal(sanitized)
	if err != nil {
		return nil, fmt.Errorf("marshal renderable param schema: %w", err)
	}

	return json.RawMessage(data), nil
}

func sanitizeRenderableParamSchema(root *jsonschema.Schema) (*jsonschema.Schema, bool) {
	if root == nil {
		return nil, false
	}
	if root.Type != "" && root.Type != "object" {
		return nil, false
	}
	if len(root.Types) > 0 && (len(root.Types) != 1 || root.Types[0] != "object") {
		return nil, false
	}
	if len(root.Properties) == 0 {
		return nil, false
	}
	if hasUnsupportedRootConstruct(root) {
		return nil, false
	}

	order := topLevelSchemaOrder(root)
	properties := make(map[string]*jsonschema.Schema, len(order))
	for _, name := range order {
		property := root.Properties[name]
		sanitized, ok := sanitizeRenderableParamProperty(property)
		if !ok {
			return nil, false
		}
		properties[name] = sanitized
	}

	result := &jsonschema.Schema{
		Type:        "object",
		Title:       root.Title,
		Description: root.Description,
		Required:    append([]string(nil), root.Required...),
		Properties:  properties,
	}
	result.PropertyOrder = append([]string(nil), order...)
	return result, true
}

func sanitizeRenderableParamProperty(schema *jsonschema.Schema) (*jsonschema.Schema, bool) {
	if schema == nil {
		return nil, false
	}
	if hasUnsupportedPropertyConstruct(schema) {
		return nil, false
	}

	if len(schema.OneOf) > 0 {
		return sanitizeRenderableParamOneOf(schema)
	}

	paramType, ok := schemaScalarType(schema)
	if !ok {
		return nil, false
	}

	result := &jsonschema.Schema{
		Type:        paramType,
		Title:       schema.Title,
		Description: schema.Description,
		Pattern:     schema.Pattern,
		Format:      "",
	}
	if len(schema.Default) > 0 {
		result.Default = append(json.RawMessage(nil), schema.Default...)
	}
	if len(schema.Enum) > 0 {
		result.Enum = cloneSchemaEnumValues(schema.Enum)
	}
	if schema.Minimum != nil {
		minimum := *schema.Minimum
		result.Minimum = &minimum
	}
	if schema.Maximum != nil {
		maximum := *schema.Maximum
		result.Maximum = &maximum
	}
	if schema.MinLength != nil {
		minLength := *schema.MinLength
		result.MinLength = &minLength
	}
	if schema.MaxLength != nil {
		maxLength := *schema.MaxLength
		result.MaxLength = &maxLength
	}
	if result.Type != ir.ParamDefTypeString {
		result.Pattern = ""
	}
	if result.Type != ir.ParamDefTypeInteger && result.Type != ir.ParamDefTypeNumber {
		result.Minimum = nil
		result.Maximum = nil
	}
	if result.Type != ir.ParamDefTypeString {
		result.MinLength = nil
		result.MaxLength = nil
	}
	return result, true
}

func sanitizeRenderableParamOneOf(schema *jsonschema.Schema) (*jsonschema.Schema, bool) {
	result := &jsonschema.Schema{
		Title:       schema.Title,
		Description: schema.Description,
		OneOf:       make([]*jsonschema.Schema, 0, len(schema.OneOf)),
	}
	if len(schema.Default) > 0 {
		result.Default = append(json.RawMessage(nil), schema.Default...)
	}

	var expectedType string
	for _, option := range schema.OneOf {
		if option == nil || option.Const == nil {
			return nil, false
		}
		if len(option.AllOf) > 0 || len(option.AnyOf) > 0 || len(option.OneOf) > 0 || option.Not != nil || option.If != nil || option.Then != nil || option.Else != nil || len(option.Properties) > 0 || option.Items != nil || len(option.PrefixItems) > 0 {
			return nil, false
		}

		optionType, ok := inferScalarType(*option.Const)
		if !ok {
			return nil, false
		}
		if expectedType == "" {
			expectedType = optionType
		} else if expectedType != optionType {
			return nil, false
		}

		constValue := cloneAny(*option.Const)
		result.OneOf = append(result.OneOf, &jsonschema.Schema{
			Type:        optionType,
			Title:       option.Title,
			Description: option.Description,
			Const:       &constValue,
		})
	}

	if expectedType == "" {
		return nil, false
	}
	result.Type = expectedType
	return result, true
}

func cloneParamSchema(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func hasUnsupportedRootConstruct(schema *jsonschema.Schema) bool {
	return len(schema.PatternProperties) > 0 ||
		len(schema.AllOf) > 0 ||
		len(schema.AnyOf) > 0 ||
		len(schema.OneOf) > 0 ||
		schema.Not != nil ||
		schema.If != nil ||
		schema.Then != nil ||
		schema.Else != nil ||
		schema.Items != nil ||
		len(schema.PrefixItems) > 0 ||
		schema.Contains != nil
}

func hasUnsupportedPropertyConstruct(schema *jsonschema.Schema) bool {
	return len(schema.AllOf) > 0 ||
		len(schema.AnyOf) > 0 ||
		schema.Not != nil ||
		schema.If != nil ||
		schema.Then != nil ||
		schema.Else != nil ||
		schema.Items != nil ||
		len(schema.PrefixItems) > 0 ||
		len(schema.Properties) > 0 ||
		len(schema.PatternProperties) > 0 ||
		schema.Contains != nil
}

func cloneSchemaEnumValues(values []any) []any {
	cloned := make([]any, 0, len(values))
	for _, value := range values {
		cloned = append(cloned, cloneAny(value))
	}
	return cloned
}
