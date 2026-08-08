// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package types

import (
	"fmt"

	"github.com/goccy/go-yaml"
)

// BackoffValue represents a backoff multiplier that can be specified as either
// a boolean (true = default 2.0 multiplier) or a numeric multiplier.
// The multiplier must be 0 or greater than 1.0 for exponential growth.
//
// YAML examples:
//
//	backoff: true
//	backoff: 1.5
//	backoff: 3
type BackoffValue struct {
	isSet      bool
	multiplier float64
}

// UnmarshalYAML implements BytesUnmarshaler for goccy/go-yaml.
func (b *BackoffValue) UnmarshalYAML(data []byte) error {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("BackoffValue unmarshal error: %w", err)
	}

	switch v := raw.(type) {
	case bool:
		b.isSet = true
		if v {
			b.multiplier = 2.0 // Default multiplier when true
		}
		return nil

	case int:
		b.isSet = true
		b.multiplier = float64(v)

	case int64:
		b.isSet = true
		b.multiplier = float64(v)

	case uint64:
		b.isSet = true
		b.multiplier = float64(v)

	case float64:
		b.isSet = true
		b.multiplier = v

	case nil:
		return nil

	default:
		return fmt.Errorf("invalid type for backoff: %T (must be boolean or number)", v)
	}

	// Validate: multiplier must be 0 or > 1.0
	if b.multiplier > 0 && b.multiplier <= 1.0 {
		return fmt.Errorf("backoff must be greater than 1.0 for exponential growth, got: %v", b.multiplier)
	}

	return nil
}

// IsZero returns true if the field was not set in YAML.
func (b BackoffValue) IsZero() bool { return !b.isSet }

// IsSet returns true if the field was explicitly set in YAML.
func (b BackoffValue) IsSet() bool { return b.isSet }

// Multiplier returns the backoff multiplier value.
func (b BackoffValue) Multiplier() float64 { return b.multiplier }

// BackoffValueFromFloat creates a BackoffValue from a float multiplier.
// No validation is performed; use only with known-valid values.
func BackoffValueFromFloat(f float64) BackoffValue {
	return BackoffValue{isSet: true, multiplier: f}
}
