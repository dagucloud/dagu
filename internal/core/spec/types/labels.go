// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package types

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/goccy/go-yaml"
)

// LabelEntry represents a single label entry with key and optional value.
type LabelEntry struct {
	key   string
	value string
}

// Key returns the label key.
func (e LabelEntry) Key() string { return e.key }

// Value returns the label value.
func (e LabelEntry) Value() string { return e.value }

// String returns the canonical string representation.
// Format: "key=value" or "key" if value is empty.
func (e LabelEntry) String() string {
	if e.value == "" {
		return e.key
	}
	return e.key + "=" + e.value
}

// LabelsValue represents label configuration that can be specified as:
//   - A space-separated string: "foo=bar zoo=baz"
//   - A map of key-value pairs: { foo: bar, zoo: baz }
//   - An array of strings: ["foo=bar", "simple-label"]
//   - An array of maps: [{ foo: bar }, { zoo: baz }]
//   - Backward compatible: ["tag1", "tag2"] or "tag1, tag2"
//
// YAML examples:
//
//	labels: "foo=bar zoo=baz"
//
//	labels:
//	  foo: bar
//	  zoo: baz
//
//	labels:
//	  - foo=bar
//	  - simple-label
//
//	labels:
//	  - production
//	  - critical
type LabelsValue struct {
	raw     any          // Original value for error reporting
	isSet   bool         // Whether the field was set in YAML
	entries []LabelEntry // Parsed entries in order
}

// UnmarshalYAML implements BytesUnmarshaler for goccy/go-yaml.
func (t *LabelsValue) UnmarshalYAML(data []byte) error {
	*t = LabelsValue{isSet: true}

	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("labels unmarshal error: %w", err)
	}
	t.raw = raw

	switch v := raw.(type) {
	case string:
		// Space-separated "key=value" or comma-separated simple labels
		return t.parseString(v)

	case map[string]any:
		// Map of key-value pairs
		return t.parseMap(v)

	case []any:
		// Array of maps or strings
		return t.parseArray(v)

	case nil:
		t.isSet = false
		return nil

	default:
		return fmt.Errorf("labels must be string, map, or array, got %T", v)
	}
}

func (t *LabelsValue) parseString(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}

	// Determine separator based on content:
	// - If contains "=" and spaces but no commas, use space separation (key=value format)
	// - Otherwise use comma separation (backward compatible)
	var parts []string
	if strings.Contains(s, "=") && strings.Contains(s, " ") && !strings.Contains(s, ",") {
		// Space-separated key=value format: "foo=bar zoo=baz"
		parts = strings.Fields(s)
	} else {
		// Comma-separated format (backward compatible): "label1, label2" or "label1,label2"
		parts = strings.Split(s, ",")
	}

	for _, part := range parts {
		entry, err := parseLabelEntry(part)
		if err != nil {
			return err
		}
		if entry.key != "" {
			t.entries = append(t.entries, entry)
		}
	}
	return nil
}

func (t *LabelsValue) parseMap(m map[string]any) error {
	return t.parseMapEntries(-1, m)
}

func (t *LabelsValue) parseArray(arr []any) error {
	for i, item := range arr {
		switch v := item.(type) {
		case map[string]any:
			if err := t.parseMapEntries(i, v); err != nil {
				return err
			}
		case string:
			entry, err := parseLabelEntry(v)
			if err != nil {
				return fmt.Errorf("labels[%d]: %w", i, err)
			}
			if entry.key != "" {
				t.entries = append(t.entries, entry)
			}
		default:
			return fmt.Errorf("labels[%d]: expected map or string, got %T", i, item)
		}
	}
	return nil
}

// parseMapEntries parses key-value pairs from a map.
// If index >= 0, errors are prefixed with "labels[index]: ".
func (t *LabelsValue) parseMapEntries(index int, m map[string]any) error {
	// Sort keys for deterministic ordering
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		k := strings.TrimSpace(key)
		v := strings.TrimSpace(stringifyValue(m[key]))

		if err := validateLabelEntry(k, v); err != nil {
			if index >= 0 {
				return fmt.Errorf("labels[%d]: %w", index, err)
			}
			return err
		}

		t.entries = append(t.entries, LabelEntry{key: k, value: v})
	}
	return nil
}

// validateLabelEntry validates a label key-value pair using core.ValidateLabel.
func validateLabelEntry(key, value string) error {
	return core.ValidateLabel(core.Label{Key: key, Value: value})
}

// parseLabelEntry parses a single label string into LabelEntry with validation.
func parseLabelEntry(s string) (LabelEntry, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return LabelEntry{}, nil
	}

	key, value, hasValue := strings.Cut(s, "=")
	key = strings.TrimSpace(key)

	if hasValue {
		value = strings.TrimSpace(value)
	}

	if err := validateLabelEntry(key, value); err != nil {
		return LabelEntry{}, err
	}

	return LabelEntry{key: key, value: value}, nil
}

// IsZero returns true if labels were not set in YAML.
func (t LabelsValue) IsZero() bool {
	return !t.isSet
}

// Value returns the original raw value for error reporting.
func (t LabelsValue) Value() any {
	return t.raw
}

// Entries returns the parsed label entries in order.
func (t LabelsValue) Entries() []LabelEntry {
	return t.entries
}

// IsEmpty returns true if labels were set but contain no entries.
func (t LabelsValue) IsEmpty() bool {
	return t.isSet && len(t.entries) == 0
}

// Values returns label entries as strings for backward compatibility.
// Key-only entries return just the key, key-value entries return "key=value".
func (t LabelsValue) Values() []string {
	if t.entries == nil {
		return nil
	}
	values := make([]string, len(t.entries))
	for i, entry := range t.entries {
		values[i] = entry.String()
	}
	return values
}

// Deprecated compatibility aliases. Prefer LabelsValue/LabelEntry in new code.
type TagsValue = LabelsValue
type TagEntry = LabelEntry
