// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package types

import (
	"fmt"

	"github.com/goccy/go-yaml"
)

// RepeatMode represents the repeat mode field that accepts either a boolean
// (legacy: true maps to "while") or a string ("while" or "until").
//
// YAML examples:
//
//	repeat: true
//	repeat: "while"
//	repeat: "until"
type RepeatMode struct {
	isSet  bool
	isBool bool   // true if the original YAML value was a boolean
	mode   string // "while", "until", or ""
}

// UnmarshalYAML implements BytesUnmarshaler for goccy/go-yaml.
func (r *RepeatMode) UnmarshalYAML(data []byte) error {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("RepeatMode unmarshal error: %w", err)
	}

	switch v := raw.(type) {
	case bool:
		if v {
			r.isSet = true
			r.isBool = true
			r.mode = "while"
		}
		// false leaves isSet=false (unset)
		return nil

	case string:
		switch v {
		case "while", "until":
			r.isSet = true
			r.mode = v
			return nil
		default:
			return fmt.Errorf("invalid value for repeat: '%s'. It must be 'while', 'until', or a boolean", v)
		}

	case nil:
		return nil

	default:
		return fmt.Errorf("invalid value for repeat: '%v'. It must be 'while', 'until', or a boolean", v)
	}
}

// IsZero returns true if the field was not set in YAML.
func (r RepeatMode) IsZero() bool { return !r.isSet }

// IsSet returns true if the field was explicitly set in YAML.
func (r RepeatMode) IsSet() bool { return r.isSet }

// String returns the repeat mode ("while" or "until").
func (r RepeatMode) String() string { return r.mode }

// IsBool returns true if the original YAML value was a boolean true.
// This is used for backward compatibility validation (bool true skips
// the condition requirement check).
func (r RepeatMode) IsBool() bool { return r.isBool }

// RepeatModeFromString creates a RepeatMode from a string value ("while" or "until").
func RepeatModeFromString(s string) RepeatMode {
	return RepeatMode{isSet: true, mode: s}
}

// RepeatModeFromBool creates a RepeatMode from a boolean value.
// true maps to "while" mode; false returns an unset RepeatMode.
func RepeatModeFromBool(b bool) RepeatMode {
	if b {
		return RepeatMode{isSet: true, isBool: true, mode: "while"}
	}
	return RepeatMode{}
}
