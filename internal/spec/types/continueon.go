// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package types

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
)

// ContinueOnValue represents a continue-on configuration that can be specified as:
// - A string shorthand: "skipped" or "failed"
// - A detailed map with configuration options
//
// YAML examples:
//
//	continue_on: skipped
//	continue_on: failed
//	continue_on:
//	  skipped: true
//	  failed: true
//	  exit_code: [0, 1]
//	  output: ["pattern1", "pattern2"]
//	  mark_success: true
type ContinueOnValue struct {
	raw         any      // Original value for error reporting
	isSet       bool     // Whether the field was set in YAML
	skipped     bool     // Continue on skipped
	failed      bool     // Continue on failed
	exitCode    []int    // Specific exit codes to continue on
	output      []string // Output patterns to match
	markSuccess bool     // Mark step as success when condition is met
}

// UnmarshalYAML implements BytesUnmarshaler for goccy/go-yaml.
func (c *ContinueOnValue) UnmarshalYAML(data []byte) error {
	c.isSet = true

	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("continue_on unmarshal error: %w", err)
	}
	c.raw = raw

	switch v := raw.(type) {
	case string:
		// String shorthand: "skipped" or "failed"
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "skipped":
			c.skipped = true
		case "failed":
			c.failed = true
		default:
			return fmt.Errorf("continue_on: expected 'skipped' or 'failed', got %q", v)
		}
		return nil

	case map[string]any:
		// Detailed configuration
		return c.parseMap(v)

	case nil:
		c.isSet = false
		return nil

	default:
		return fmt.Errorf("continue_on must be string or map, got %T", v)
	}
}

func (c *ContinueOnValue) parseMap(m map[string]any) error {
	for key, v := range m {
		switch key {
		case "skipped":
			if b, ok := v.(bool); ok {
				c.skipped = b
			} else {
				return fmt.Errorf("continue_on.skipped: expected bool, got %T", v)
			}
		case "failed", "failure":
			if b, ok := v.(bool); ok {
				c.failed = b
			} else {
				return fmt.Errorf("continue_on.%s: expected bool, got %T", key, v)
			}
		case "exit_code":
			codes, err := parseIntArray(v)
			if err != nil {
				return fmt.Errorf("continue_on.exit_code: %w", err)
			}
			c.exitCode = codes
		case "output":
			outputs, err := parseStringArray(v)
			if err != nil {
				return fmt.Errorf("continue_on.output: %w", err)
			}
			c.output = outputs
		case "mark_success":
			if b, ok := v.(bool); ok {
				c.markSuccess = b
			} else {
				return fmt.Errorf("continue_on.mark_success: expected bool, got %T", v)
			}
		default:
			return fmt.Errorf("continue_on: unknown key %q", key)
		}
	}
	return nil
}

func parseStringArray(v any) ([]string, error) {
	switch val := v.(type) {
	case nil:
		return nil, nil
	case string:
		if val == "" {
			return nil, nil
		}
		return []string{val}, nil
	case []any:
		var result []string
		for i, item := range val {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("[%d]: expected string, got %T", i, item)
			}
			result = append(result, s)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("expected string or array of strings, got %T", v)
	}
}

func parseIntArray(v any) ([]int, error) {
	switch val := v.(type) {
	case []any:
		var result []int
		for i, item := range val {
			n, err := parseIntValue(item)
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			result = append(result, n)
		}
		return result, nil
	case int:
		return []int{val}, nil
	case int64:
		return []int{int(val)}, nil
	case float64:
		return []int{int(val)}, nil
	case uint64:
		// Exit codes are small numbers, overflow won't happen in practice
		return []int{int(val)}, nil //nolint:gosec // Exit codes are small numbers
	case string:
		n, err := strconv.Atoi(val)
		if err != nil {
			return nil, fmt.Errorf("cannot parse %q as int: %w", val, err)
		}
		return []int{n}, nil
	default:
		return nil, fmt.Errorf("expected int or array of ints, got %T", v)
	}
}

func parseIntValue(item any) (int, error) {
	switch v := item.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	case uint64:
		return int(v), nil //nolint:gosec // Exit codes are small numbers
	case string:
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("cannot parse %q as int: %w", v, err)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("expected int, got %T", item)
	}
}

// IsZero returns true if continueOn was not set in YAML.
func (c ContinueOnValue) IsZero() bool { return !c.isSet }

// Value returns the original raw value for error reporting.
func (c ContinueOnValue) Value() any { return c.raw }

// Skipped returns true if should continue on skipped.
func (c ContinueOnValue) Skipped() bool { return c.skipped }

// Failed returns true if should continue on failed.
func (c ContinueOnValue) Failed() bool { return c.failed }

// ExitCode returns exit codes to continue on.
func (c ContinueOnValue) ExitCode() []int { return c.exitCode }

// Output returns output patterns to match.
func (c ContinueOnValue) Output() []string { return c.output }

// MarkSuccess returns true if step should be marked as success when condition is met.
func (c ContinueOnValue) MarkSuccess() bool { return c.markSuccess }
