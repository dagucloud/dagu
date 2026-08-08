// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package types

import (
	"fmt"
	"strings"

	"github.com/goccy/go-yaml"
)

// ShellValue represents a shell configuration that can be specified as either
// a string (e.g., "bash -e") or an array (e.g., ["bash", "-e"]).
//
// YAML examples:
//
//	shell: "bash -e"
//	shell: bash
//	shell: ["bash", "-e"]
//	shell:
//	  - bash
//	  - -e
type ShellValue struct {
	raw       any      // Original value for error reporting
	isSet     bool     // Whether the field was set in YAML
	command   string   // The shell command (first element)
	arguments []string // Additional arguments
}

// UnmarshalYAML implements BytesUnmarshaler for goccy/go-yaml.
func (s *ShellValue) UnmarshalYAML(data []byte) error {
	s.isSet = true

	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("shell unmarshal error: %w", err)
	}
	s.raw = raw

	switch v := raw.(type) {
	case string:
		// String value: "bash -e" or "bash"
		s.command = strings.TrimSpace(v)
		return nil

	case []any:
		// Array value: ["bash", "-e"]
		if len(v) > 0 {
			if cmd, ok := v[0].(string); ok {
				s.command = cmd
			} else {
				s.command = fmt.Sprintf("%v", v[0])
			}
			for i := 1; i < len(v); i++ {
				if arg, ok := v[i].(string); ok {
					s.arguments = append(s.arguments, arg)
				} else {
					s.arguments = append(s.arguments, fmt.Sprintf("%v", v[i]))
				}
			}
		}
		return nil

	case []string:
		// Array of strings (from Go types)
		if len(v) > 0 {
			s.command = v[0]
			s.arguments = v[1:]
		}
		return nil

	case nil:
		s.isSet = false
		return nil

	default:
		return fmt.Errorf("shell must be string or array, got %T", v)
	}
}

// IsZero returns true if the shell value was not set in YAML.
func (s ShellValue) IsZero() bool { return !s.isSet }

// Value returns the original raw value for error reporting.
func (s ShellValue) Value() any { return s.raw }

// Command returns the shell command (first element or full string).
func (s ShellValue) Command() string { return s.command }

// Arguments returns the additional shell arguments (only set for array form).
func (s ShellValue) Arguments() []string { return s.arguments }

// IsArray returns true if the value was specified as an array.
func (s ShellValue) IsArray() bool {
	switch s.raw.(type) {
	case []any, []string:
		return true
	default:
		return false
	}
}
