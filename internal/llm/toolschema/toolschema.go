// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package toolschema derives LLM function-calling parameter schemas from DAG
// parameter definitions.
package toolschema

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/core"
)

// Param is a single tool parameter.
type Param struct {
	Name     string
	Type     string // "string", "integer", "number", "boolean", "array", "object"
	Default  any
	Required bool
}

// paramRegex matches "name" or "name=value" patterns in param strings.
var paramRegex = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)(?:=(.*))?`)

// ForDAG returns the JSON Schema describing the parameters a DAG accepts when
// invoked as a tool. Rich parameter definitions take precedence over the
// positional default-params string.
func ForDAG(dag *core.DAG) (map[string]any, error) {
	params, err := ParamsForDAG(dag)
	if err != nil {
		return nil, err
	}
	return Build(params), nil
}

// ParamsForDAG lists the parameters a DAG accepts, preferring its rich
// definitions and falling back to its default-params string.
func ParamsForDAG(dag *core.DAG) ([]Param, error) {
	if dag == nil {
		return nil, nil
	}

	params := ParamsFromDefs(dag.ParamDefs)
	if len(params) == 0 {
		var err error
		params, err = ParseParams(dag.DefaultParams)
		if err != nil {
			return nil, err
		}
	}
	return params, nil
}

// ParamsFromDefs converts rich parameter definitions into tool parameters.
func ParamsFromDefs(defs []core.ParamDef) []Param {
	if len(defs) == 0 {
		return nil
	}

	params := make([]Param, 0, len(defs))
	for _, def := range defs {
		if def.Name == "" {
			continue
		}
		param := Param{
			Name:     def.Name,
			Type:     def.Type,
			Required: def.Required,
			Default:  def.Default,
		}
		if param.Type == "" {
			param.Type = "string"
		}
		params = append(params, param)
	}
	return params
}

// ParseParams parses a default-params string such as
// "param1 param2=default2 param3=10" into tool parameters. Parameters without a
// default are required.
func ParseParams(defaultParams string) ([]Param, error) {
	if defaultParams == "" {
		return nil, nil
	}

	parts := SplitParams(defaultParams)
	params := make([]Param, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		match := paramRegex.FindStringSubmatch(part)
		if match == nil {
			continue
		}

		name := match[1]
		defaultValue := ""
		if len(match) > 2 {
			defaultValue = match[2]
		}

		param := Param{
			Name:     name,
			Required: defaultValue == "",
		}

		if defaultValue != "" {
			param.Default, param.Type = InferTypeFromDefault(defaultValue)
		} else {
			param.Type = "string"
		}

		params = append(params, param)
	}

	return params, nil
}

// SplitParams splits a param string on whitespace, respecting quotes.
func SplitParams(s string) []string {
	var parts []string
	var current strings.Builder
	inQuote := false
	quoteChar := rune(0)

	for _, ch := range s {
		switch {
		case (ch == '"' || ch == '\'') && !inQuote:
			inQuote = true
			quoteChar = ch
			current.WriteRune(ch)
		case ch == quoteChar && inQuote:
			inQuote = false
			quoteChar = 0
			current.WriteRune(ch)
		case ch == ' ' && !inQuote:
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(ch)
		}
	}

	if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	return parts
}

// InferTypeFromDefault infers a JSON Schema type from a default value literal,
// returning the decoded value alongside the type name.
func InferTypeFromDefault(value string) (any, string) {
	// Strip surrounding quotes. The length guard matters: a lone quote satisfies
	// both prefix and suffix, and slicing it would panic.
	if len(value) >= 2 &&
		((strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`)) ||
			(strings.HasPrefix(value, `'`) && strings.HasSuffix(value, `'`))) {
		return value[1 : len(value)-1], "string"
	}

	if i, err := strconv.ParseInt(value, 10, 64); err == nil {
		return i, "integer"
	}

	if f, err := strconv.ParseFloat(value, 64); err == nil {
		return f, "number"
	}

	if value == "true" {
		return true, "boolean"
	}
	if value == "false" {
		return false, "boolean"
	}

	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		var arr []any
		if err := json.Unmarshal([]byte(value), &arr); err == nil {
			return arr, "array"
		}
	}

	if strings.HasPrefix(value, "{") && strings.HasSuffix(value, "}") {
		var obj map[string]any
		if err := json.Unmarshal([]byte(value), &obj); err == nil {
			return obj, "object"
		}
	}

	return value, "string"
}

// Build assembles a JSON Schema object from tool parameters.
func Build(params []Param) map[string]any {
	properties := make(map[string]any)
	var required []string

	for _, param := range params {
		prop := map[string]any{
			"type":        param.Type,
			"description": fmt.Sprintf("%s parameter", param.Name),
		}

		if param.Default != nil {
			prop["default"] = param.Default
		}

		properties[param.Name] = prop

		if param.Required {
			required = append(required, param.Name)
		}
	}

	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}

	if len(required) > 0 {
		schema["required"] = required
	}

	return schema
}
