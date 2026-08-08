// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package types

import (
	"fmt"

	"github.com/goccy/go-yaml"
)

// StringOrArray represents a value that can be specified as either a single
// string or an array of strings.
//
// YAML examples:
//
//	depends: "step1"
//	depends: ["step1", "step2"]
//	dotenv: ".env"
//	dotenv: [".env", ".env.local"]
type StringOrArray struct {
	raw    any      // Original value for error reporting
	isSet  bool     // Whether the field was set in YAML
	values []string // Parsed values
}

// UnmarshalYAML implements BytesUnmarshaler for goccy/go-yaml.
func (s *StringOrArray) UnmarshalYAML(data []byte) error {
	s.isSet = true

	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("unmarshal error: %w", err)
	}
	s.raw = raw

	switch v := raw.(type) {
	case string:
		// Single string value - preserve empty strings for validation layer to handle
		s.values = []string{v}
		return nil

	case []any:
		// Array of values - convert non-strings to strings for compatibility
		for _, item := range v {
			if str, ok := item.(string); ok {
				s.values = append(s.values, str)
			} else {
				// Stringify non-string items (e.g., numeric labels)
				s.values = append(s.values, fmt.Sprintf("%v", item))
			}
		}
		return nil

	case []string:
		// Array of strings (from Go types)
		s.values = v
		return nil

	case nil:
		s.isSet = false
		return nil

	default:
		return fmt.Errorf("must be string or array, got %T", v)
	}
}

// IsZero returns true if the value was not set in YAML.
func (s StringOrArray) IsZero() bool { return !s.isSet }

// Value returns the original raw value for error reporting.
func (s StringOrArray) Value() any { return s.raw }

// Values returns the parsed string values.
func (s StringOrArray) Values() []string { return s.values }

// IsEmpty returns true if set but contains no values (empty array).
func (s StringOrArray) IsEmpty() bool { return s.isSet && len(s.values) == 0 }

// MailToValue is an alias for StringOrArray used for email recipients.
// YAML examples:
//
//	to: user@example.com
//	to: ["user1@example.com", "user2@example.com"]
type MailToValue = StringOrArray
