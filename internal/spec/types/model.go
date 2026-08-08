// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package types

import (
	"fmt"

	"github.com/goccy/go-yaml"
)

// ModelValue represents an LLM model configuration that can be specified as either
// a single string (model name) or an array of model entries (for fallback support).
//
// YAML examples:
//
//	model: gpt-4o
//	model:
//	  - provider: openai
//	    name: gpt-4o
//	    max_tokens: 2000
//	    top_p: 0.9
//	    base_url: https://api.example.com
//	    api_key_name: OPENAI_API_KEY
type ModelValue struct {
	raw     any          // Original value for error reporting
	isSet   bool         // Whether the field was set in YAML
	single  string       // When model is a string
	entries []ModelEntry // When model is an array
}

// ModelEntry represents a single model in the model array for fallback support.
type ModelEntry struct {
	Provider    string   // Required: LLM provider
	Name        string   // Required: Model name
	Temperature *float64 // Optional: Override temperature (0.0-2.0)
	MaxTokens   *int     // Optional: Override max tokens (>=1)
	TopP        *float64 // Optional: Override top-p (0.0-1.0)
	BaseURL     string   // Optional: Custom API endpoint
	APIKeyName  string   // Optional: API key env var name
}

// UnmarshalYAML implements BytesUnmarshaler for goccy/go-yaml.
func (m *ModelValue) UnmarshalYAML(data []byte) error {
	m.isSet = true

	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("model unmarshal error: %w", err)
	}
	m.raw = raw

	switch v := raw.(type) {
	case string:
		m.single = v
		return nil

	case []any:
		if len(v) == 0 {
			return fmt.Errorf("model array must have at least one entry")
		}
		entries := make([]ModelEntry, 0, len(v))
		for i, item := range v {
			entryMap, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("model[%d]: expected object, got %T", i, item)
			}
			entry, err := parseModelEntry(entryMap, i)
			if err != nil {
				return err
			}
			entries = append(entries, entry)
		}
		m.entries = entries
		return nil

	case nil:
		m.isSet = false
		return nil

	default:
		return fmt.Errorf("model must be string or array, got %T", v)
	}
}

// parseModelEntry parses a single model entry from a map.
func parseModelEntry(m map[string]any, index int) (ModelEntry, error) {
	entry := ModelEntry{}

	// Required: provider
	provider, ok := m["provider"].(string)
	if !ok || provider == "" {
		return entry, fmt.Errorf("model[%d].provider: required", index)
	}
	entry.Provider = provider

	// Required: name
	name, ok := m["name"].(string)
	if !ok || name == "" {
		return entry, fmt.Errorf("model[%d].name: required", index)
	}
	entry.Name = name

	// Optional: temperature
	if temp, ok := m["temperature"]; ok {
		v, ok := toFloat64(temp)
		if !ok {
			return entry, fmt.Errorf("model[%d].temperature: must be a number", index)
		}
		if v < 0.0 || v > 2.0 {
			return entry, fmt.Errorf("model[%d].temperature: must be between 0.0 and 2.0", index)
		}
		entry.Temperature = &v
	}

	// Optional: max_tokens
	if tokens, ok := m["max_tokens"]; ok {
		v, ok := toInt(tokens)
		if !ok {
			return entry, fmt.Errorf("model[%d].max_tokens: must be an integer", index)
		}
		if v < 1 {
			return entry, fmt.Errorf("model[%d].max_tokens: must be at least 1", index)
		}
		entry.MaxTokens = &v
	}

	// Optional: top_p
	if topP, ok := m["top_p"]; ok {
		v, ok := toFloat64(topP)
		if !ok {
			return entry, fmt.Errorf("model[%d].top_p: must be a number", index)
		}
		if v < 0.0 || v > 1.0 {
			return entry, fmt.Errorf("model[%d].top_p: must be between 0.0 and 1.0", index)
		}
		entry.TopP = &v
	}

	// Optional: base_url
	if baseURL, ok := m["base_url"].(string); ok {
		entry.BaseURL = baseURL
	}

	// Optional: api_key_name
	if apiKeyName, ok := m["api_key_name"].(string); ok {
		entry.APIKeyName = apiKeyName
	}

	return entry, nil
}

// toFloat64 converts various numeric types to float64.
func toFloat64(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int32:
		return float64(t), true
	case int64:
		return float64(t), true
	case uint:
		return float64(t), true
	case uint32:
		return float64(t), true
	case uint64:
		return float64(t), true
	default:
		return 0, false
	}
}

// toInt converts various numeric types to int.
func toInt(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int32:
		return int(t), true
	case int64:
		if t > int64(^uint(0)>>1) || t < -int64(^uint(0)>>1)-1 {
			return 0, false
		}
		return int(t), true
	case uint:
		if t > uint(^uint(0)>>1) {
			return 0, false
		}
		return int(t), true
	case uint32:
		return int(t), true
	case uint64:
		if t > uint64(^uint(0)>>1) {
			return 0, false
		}
		return int(t), true
	case float64:
		return int(t), true
	case float32:
		return int(t), true
	default:
		return 0, false
	}
}

// IsZero returns true if the value was not set in YAML.
func (m ModelValue) IsZero() bool { return !m.isSet }

// Value returns the original raw value for error reporting.
func (m ModelValue) Value() any { return m.raw }

// IsArray returns true if the model was specified as an array.
func (m ModelValue) IsArray() bool { return len(m.entries) > 0 }

// String returns the model name when specified as a string.
// Returns empty string if model was specified as an array.
func (m ModelValue) String() string { return m.single }

// Entries returns the model entries when specified as an array.
// Returns nil if model was specified as a string.
func (m ModelValue) Entries() []ModelEntry { return m.entries }
