// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"encoding/json"
	"fmt"
	"sort"
)

// maxAgentSchemaBytes is the size limit of an output_schema as one line of JSON.
// The schema goes to the command line of a CLI agent as one argument.
const maxAgentSchemaBytes = 100000

// validateAgentOutputSchema checks that an output_schema is closed, as an
// agent.run step needs. The error names the path of the schema node.
//
// The rules are: type object with properties, additionalProperties false in
// each object schema, each name of required in properties, no property named
// tool_calls at the top level, and at most maxAgentSchemaBytes bytes.
func validateAgentOutputSchema(schema map[string]any) error {
	data, err := json.Marshal(schema)
	if err != nil {
		return fmt.Errorf("output_schema cannot be encoded as JSON: %w", err)
	}
	if len(data) > maxAgentSchemaBytes {
		return fmt.Errorf("output_schema is %d bytes as one line of JSON, above the limit of %d bytes",
			len(data), maxAgentSchemaBytes)
	}
	if schema["type"] != "object" {
		return fmt.Errorf("output_schema must declare type: object, got %v", schema["type"])
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok || len(properties) == 0 {
		return fmt.Errorf("output_schema must list at least one property under properties")
	}
	if _, clash := properties["tool_calls"]; clash {
		return fmt.Errorf("output_schema must not have a property named tool_calls; the step publishes it")
	}
	return validateClosedSchemaNode(schema, "output_schema")
}

// validateClosedSchemaNode walks a schema node and the schemas below it.
func validateClosedSchemaNode(node map[string]any, path string) error {
	if isObjectSchema(node) {
		if additional, ok := node["additionalProperties"].(bool); !ok || additional {
			return fmt.Errorf("%s is an object schema and must set additionalProperties: false, got %v",
				path, node["additionalProperties"])
		}
		properties, _ := node["properties"].(map[string]any)
		for _, name := range requiredNameList(node["required"]) {
			if _, listed := properties[name]; !listed {
				return fmt.Errorf("%s.required names %q, which is not listed under properties", path, name)
			}
		}
	}

	// Keywords whose value is a map from a name to a schema.
	for _, keyword := range []string{"properties", "patternProperties", "definitions", "$defs", "dependencies"} {
		children, ok := node[keyword].(map[string]any)
		if !ok {
			continue
		}
		names := make([]string, 0, len(children))
		for name := range children {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if child, ok := children[name].(map[string]any); ok {
				if err := validateClosedSchemaNode(child, path+"."+keyword+"."+name); err != nil {
					return err
				}
			}
		}
	}
	// Keywords whose value is one schema or a list of schemas.
	for _, keyword := range []string{"items", "additionalItems", "contains", "not", "if", "then", "else", "propertyNames", "anyOf", "allOf", "oneOf"} {
		switch child := node[keyword].(type) {
		case map[string]any:
			if err := validateClosedSchemaNode(child, path+"."+keyword); err != nil {
				return err
			}
		case []any:
			for i, item := range child {
				if sub, ok := item.(map[string]any); ok {
					if err := validateClosedSchemaNode(sub, fmt.Sprintf("%s.%s[%d]", path, keyword, i)); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// isObjectSchema reports whether a schema node describes an object: it types
// the value as object, or it lists properties.
func isObjectSchema(node map[string]any) bool {
	switch typed := node["type"].(type) {
	case string:
		if typed == "object" {
			return true
		}
	case []any:
		for _, item := range typed {
			if item == "object" {
				return true
			}
		}
	}
	_, hasProperties := node["properties"]
	return hasProperties
}

func requiredNameList(value any) []string {
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(list))
	for _, item := range list {
		if name, ok := item.(string); ok {
			names = append(names, name)
		}
	}
	return names
}
