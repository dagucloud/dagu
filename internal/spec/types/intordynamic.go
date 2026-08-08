// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package types

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
)

// IntOrDynamic represents an integer field that may contain a variable reference
// (e.g., ${VAR}, $VAR, or backtick command substitutions). It validates eagerly
// and only defers genuinely dynamic references.
//
// YAML examples:
//
//	limit: 3
//	limit: "5"
//	limit: $REPEAT_LIMIT
//	limit: ${REPEAT_LIMIT}
type IntOrDynamic struct {
	isSet     bool
	intVal    int
	strVal    string
	isDynamic bool
}

// UnmarshalYAML implements BytesUnmarshaler for goccy/go-yaml.
func (d *IntOrDynamic) UnmarshalYAML(data []byte) error {
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("IntOrDynamic unmarshal error: %w", err)
	}

	switch v := raw.(type) {
	case int:
		d.isSet = true
		d.intVal = v
		return nil

	case int64:
		d.isSet = true
		d.intVal = int(v)
		return nil

	case uint64:
		if v > math.MaxInt {
			return fmt.Errorf("value %d exceeds maximum int", v)
		}
		d.isSet = true
		d.intVal = int(v)
		return nil

	case string:
		d.isSet = true
		if isDynamicString(v) {
			d.strVal = v
			d.isDynamic = true
			return nil
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("must be an integer or a variable reference like ${VAR}")
		}
		d.intVal = n
		return nil

	case nil:
		// Unset
		return nil

	default:
		return fmt.Errorf("invalid type for IntOrDynamic: %T", v)
	}
}

// IsZero returns true if the field was not set in YAML.
func (d IntOrDynamic) IsZero() bool { return !d.isSet }

// Int returns the resolved integer value.
func (d IntOrDynamic) Int() int { return d.intVal }

// Str returns the raw string if this is a dynamic reference.
func (d IntOrDynamic) Str() string { return d.strVal }

// IsDynamic returns true if the value is a variable reference
// that must be resolved at runtime.
func (d IntOrDynamic) IsDynamic() bool { return d.isDynamic }

// IntOrDynamicFromInt creates an IntOrDynamic from an integer value.
func IntOrDynamicFromInt(n int) IntOrDynamic {
	return IntOrDynamic{isSet: true, intVal: n}
}

// IntOrDynamicFromStr creates an IntOrDynamic from a dynamic string reference.
func IntOrDynamicFromStr(s string) IntOrDynamic {
	return IntOrDynamic{isSet: true, strVal: s, isDynamic: true}
}

// isDynamicString returns true if s contains variable references or command substitutions.
func isDynamicString(s string) bool {
	return strings.Contains(s, "${") || strings.Contains(s, "`") ||
		(strings.HasPrefix(s, "$") && len(s) > 1)
}
