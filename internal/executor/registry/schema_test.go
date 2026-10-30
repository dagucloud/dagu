// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package registry

import (
	"strings"
	"sync"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/require"
)

func TestValidateExecutorConfig_ValidConfig(t *testing.T) {
	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"name": {Type: "string"},
		},
	}
	RegisterExecutorConfigSchema("test_valid", schema)

	err := ValidateExecutorConfig("test_valid", map[string]any{"name": "foo"})
	require.NoError(t, err)
}

func TestValidateExecutorConfig_InvalidType(t *testing.T) {
	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"name": {Type: "string"},
		},
	}
	RegisterExecutorConfigSchema("test_invalid_type", schema)

	// Name should be string, not int
	err := ValidateExecutorConfig("test_invalid_type", map[string]any{"name": 123})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid test_invalid_type config")
}

func TestValidateExecutorConfig_NoSchemaRegistered(t *testing.T) {
	// Unregistered executor should pass (backward compatible)
	err := ValidateExecutorConfig("unregistered_executor", map[string]any{"any": "thing"})
	require.NoError(t, err)
}

func TestValidateExecutorConfig_EmptyConfig(t *testing.T) {
	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"optional": {Type: "string"},
		},
	}
	RegisterExecutorConfigSchema("test_empty", schema)

	// Empty config should be valid for optional-only fields
	err := ValidateExecutorConfig("test_empty", map[string]any{})
	require.NoError(t, err)
}

func TestValidateExecutorConfig_ConcurrentAccess(_ *testing.T) {
	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"value": {Type: "integer"},
		},
	}
	RegisterExecutorConfigSchema("test_concurrent", schema)

	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			_ = ValidateExecutorConfig("test_concurrent", map[string]any{"value": 42})
		})
	}
	wg.Wait()
}

func TestValidateExecutorConfig_EnumValidation(t *testing.T) {
	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"pull": {
				Type: "string",
				Enum: []any{"always", "never", "missing", "fallback"},
			},
		},
	}
	RegisterExecutorConfigSchema("test_enum", schema)

	// Valid enum value
	err := ValidateExecutorConfig("test_enum", map[string]any{"pull": "always"})
	require.NoError(t, err)

	// Valid fallback enum value
	err = ValidateExecutorConfig("test_enum", map[string]any{"pull": "fallback"})
	require.NoError(t, err)

	// Invalid enum value
	err = ValidateExecutorConfig("test_enum", map[string]any{"pull": "invalid"})
	require.Error(t, err)
}

func TestValidateExecutorConfig_RequiredFields(t *testing.T) {
	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"image": {Type: "string"},
		},
		Required: []string{"image"},
	}
	RegisterExecutorConfigSchema("test_required", schema)

	// Missing required field
	err := ValidateExecutorConfig("test_required", map[string]any{})
	require.Error(t, err)

	// With required field
	err = ValidateExecutorConfig("test_required", map[string]any{"image": "alpine"})
	require.NoError(t, err)
}

func TestValidateExecutorConfig_UnknownKeyHint(t *testing.T) {
	t.Parallel()

	properties := map[string]*jsonschema.Schema{
		"input":         {},
		"format":        {Type: "string"},
		"stop_at_blank": {},
		"data":          {},
		"date":          {},
	}
	RegisterExecutorConfigSchema("test_closed", &jsonschema.Schema{
		Type:                 "object",
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
		Properties:           properties,
	})
	// An open schema accepts undefined keys, so none of them is a misspelling
	// even when the config fails for another reason.
	RegisterExecutorConfigSchema("test_open", &jsonschema.Schema{
		Type:       "object",
		Properties: properties,
	})

	for _, tc := range []struct {
		name         string
		executorType string
		config       map[string]any
		hint         string // empty when nothing is close enough to suggest
	}{
		{"misspelled key", "test_closed", map[string]any{"format": "json", "inputs": 1}, `did you mean "input" instead of "inputs"?`},
		{"camel case key", "test_closed", map[string]any{"stopAtBlank": true}, `did you mean "stop_at_blank" instead of "stopAtBlank"?`},
		{"several keys", "test_closed", map[string]any{"inputs": 1, "fromat": "json"},
			`did you mean "format" instead of "fromat", "input" instead of "inputs"?`},
		{"equally close keys", "test_closed", map[string]any{"dat": 1}, `did you mean "data" instead of "dat"?`},
		{"unrelated key", "test_closed", map[string]any{"zzbogus": 1}, ""},
		{"open schema", "test_open", map[string]any{"format": 1, "inputs": 1}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateExecutorConfig(tc.executorType, tc.config)
			require.Error(t, err)
			if tc.hint == "" {
				require.NotContains(t, err.Error(), "did you mean")
				return
			}
			require.True(t, strings.HasSuffix(err.Error(), "; "+tc.hint), "error %q does not end with %q", err, tc.hint)
		})
	}
}
