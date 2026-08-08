// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package types

import (
	"fmt"

	"github.com/goccy/go-yaml"
)

// PortValue represents a port number that can be specified as either
// a string or an integer.
//
// YAML examples:
//
//	port: 22
//	port: "22"
type PortValue struct {
	raw   any    // Original value for error reporting
	isSet bool   // Whether the field was set in YAML
	value string // Normalized string value
}

// UnmarshalYAML implements BytesUnmarshaler for goccy/go-yaml.
func (p *PortValue) UnmarshalYAML(data []byte) error {
	p.isSet = true

	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("port unmarshal error: %w", err)
	}
	p.raw = raw

	switch v := raw.(type) {
	case string:
		p.value = v
		return nil

	case int:
		p.value = fmt.Sprintf("%d", v)
		return nil

	case float64:
		if v != float64(int(v)) {
			return fmt.Errorf("port must be an integer, got %v", v)
		}
		p.value = fmt.Sprintf("%d", int(v))
		return nil

	case uint64:
		p.value = fmt.Sprintf("%d", v)
		return nil

	case nil:
		p.isSet = false
		return nil

	default:
		return fmt.Errorf("port must be string or number, got %T", v)
	}
}

// IsZero returns true if the port was not set in YAML.
func (p PortValue) IsZero() bool { return !p.isSet }

// Value returns the original raw value for error reporting.
func (p PortValue) Value() any { return p.raw }

// String returns the port as a string.
func (p PortValue) String() string { return p.value }
