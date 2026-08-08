// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package registry

import (
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
