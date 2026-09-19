// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package types

import (
	"fmt"

	"github.com/goccy/go-yaml"
)

// PassEnvValue represents the opt-in set of parent environment values a
// sub-DAG step hands to the child run it starts.
//
// YAML examples:
//
//	pass_env: true                  # pass the parent run environment
//	pass_env: [TODAY, GH_USER]      # pass only the listed variables
//	pass_env: false                 # pass nothing (default)
type PassEnvValue struct {
	raw   any      // Original value for error reporting
	isSet bool     // Whether the field was set in YAML
	all   bool     // Whether to pass the whole parent run environment
	names []string // Variable names to pass
}

// UnmarshalYAML implements BytesUnmarshaler for goccy/go-yaml.
func (s *PassEnvValue) UnmarshalYAML(data []byte) error {
	s.isSet = true

	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("unmarshal error: %w", err)
	}
	s.raw = raw

	switch v := raw.(type) {
	case bool:
		s.all = v
		return nil

	case []any:
		for _, item := range v {
			name, ok := item.(string)
			if !ok {
				return fmt.Errorf("list entries must be environment variable names, got %T", item)
			}
			s.names = append(s.names, name)
		}
		return nil

	case []string:
		s.names = v
		return nil

	case nil:
		s.isSet = false
		return nil

	default:
		return fmt.Errorf("must be a boolean or a list of environment variable names, got %T", v)
	}
}

// IsZero returns true if the value was not set in YAML.
func (s PassEnvValue) IsZero() bool { return !s.isSet }

// Enabled returns true when the value requests passing values (all or names).
func (s PassEnvValue) Enabled() bool { return s.isSet && (s.all || len(s.names) > 0) }

// All returns true when the value requests passing every parent variable.
func (s PassEnvValue) All() bool { return s.all }

// Names returns the listed variable names.
func (s PassEnvValue) Names() []string { return s.names }

// Value returns the original raw value for error reporting.
func (s PassEnvValue) Value() any { return s.raw }
