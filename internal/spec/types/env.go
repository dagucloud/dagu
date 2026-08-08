// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package types

import (
	"fmt"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

// EnvValue represents environment variable configuration that can be specified as:
// - A map of key-value pairs
// - An array of maps (for ordered definitions)
// - An array of "KEY=value" strings
// - A mix of maps and strings in an array
//
// YAML examples:
//
//	env:
//	  KEY1: value1
//	  KEY2: value2
//
//	env:
//	  - KEY1: value1
//	  - KEY2: value2
//
//	env:
//	  - KEY1=value1
//	  - KEY2=value2
type EnvValue struct {
	raw     any        // Original value for error reporting
	isSet   bool       // Whether the field was set in YAML
	entries []EnvEntry // Parsed entries in order
}

// EnvEntry represents a single environment variable entry.
type EnvEntry struct {
	Key   string
	Value string
}

// UnmarshalYAML implements BytesUnmarshaler for goccy/go-yaml.
func (e *EnvValue) UnmarshalYAML(data []byte) error {
	e.isSet = true

	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("env unmarshal error: %w", err)
	}
	e.raw = raw

	file, err := parser.ParseBytes(data, 0)
	if err != nil {
		return fmt.Errorf("env parse error: %w", err)
	}
	if len(file.Docs) == 0 || file.Docs[0].Body == nil {
		e.isSet = false
		return nil
	}

	switch node := file.Docs[0].Body.(type) {
	case *ast.MappingNode:
		return e.parseMappingNode(node)
	case *ast.SequenceNode:
		return e.parseSequenceNode(node)
	case *ast.NullNode:
		e.isSet = false
		return nil
	default:
		return fmt.Errorf("env must be map or array, got %T", raw)
	}
}

func (e *EnvValue) parseMappingNode(node *ast.MappingNode) error {
	for _, value := range node.Values {
		key, err := yamlNodeString(value.Key)
		if err != nil {
			return fmt.Errorf("env map key: %w", err)
		}
		val, err := yamlNodeString(value.Value)
		if err != nil {
			return fmt.Errorf("env map value: %w", err)
		}
		e.entries = append(e.entries, EnvEntry{Key: key, Value: val})
	}
	return nil
}

func (e *EnvValue) parseSequenceNode(node *ast.SequenceNode) error {
	for i, item := range node.Values {
		switch v := item.(type) {
		case *ast.MappingNode:
			if err := e.parseMappingNode(v); err != nil {
				return fmt.Errorf("env[%d]: %w", i, err)
			}
		case *ast.StringNode:
			key, val, found := strings.Cut(v.Value, "=")
			if !found {
				return fmt.Errorf("env[%d]: invalid format %q (expected KEY=value)", i, v.Value)
			}
			e.entries = append(e.entries, EnvEntry{Key: key, Value: val})
		default:
			return fmt.Errorf("env[%d]: expected map or string, got %T", i, item)
		}
	}
	return nil
}

func yamlNodeString(node ast.Node) (string, error) {
	if node == nil {
		return "", nil
	}
	if literal, ok := node.(*ast.LiteralNode); ok && literal.Value != nil {
		return literal.Value.Value, nil
	}
	var raw any
	data, err := node.MarshalYAML()
	if err != nil {
		return "", err
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return "", err
	}
	return stringifyValue(raw), nil
}

func stringifyValue(v any) string {
	switch val := v.(type) {
	case string:
		return val
	default:
		return fmt.Sprintf("%v", val)
	}
}

// Prepend returns a new EnvValue with other's entries before this value's entries.
func (e EnvValue) Prepend(other EnvValue) EnvValue {
	if other.IsZero() {
		return e
	}
	combined := make([]EnvEntry, 0, len(other.entries)+len(e.entries))
	combined = append(combined, other.entries...)
	combined = append(combined, e.entries...)
	return EnvValue{
		isSet:   true,
		entries: combined,
	}
}

// IsZero returns true if env was not set in YAML.
func (e EnvValue) IsZero() bool { return !e.isSet }

// Value returns the original raw value for error reporting.
func (e EnvValue) Value() any { return e.raw }

// Entries returns the parsed environment entries in order.
func (e EnvValue) Entries() []EnvEntry { return e.entries }
