// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseOutputConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    any
		expected *outputConfig
		wantErr  string
	}{
		{
			name:     "NilOutput",
			input:    nil,
			expected: nil,
		},
		{
			name:  "StringOutput",
			input: "RESULT",
			expected: &outputConfig{
				Name: "RESULT",
			},
		},
		{
			name: "StructuredLiteralObject",
			input: map[string]any{
				"meta": map[string]any{
					"version": "v1.2.3",
					"env":     "stg",
				},
			},
			expected: &outputConfig{
				StructuredOutput: map[string]ir.StepOutputEntry{
					"meta": {
						HasValue: true,
						Value: map[string]any{
							"version": "v1.2.3",
							"env":     "stg",
						},
					},
				},
			},
		},
		{
			name: "StructuredSourceEntry",
			input: map[string]any{
				"version": map[string]any{
					"from":   "stdout",
					"decode": "json",
					"select": ".version",
				},
			},
			expected: &outputConfig{
				StructuredOutput: map[string]ir.StepOutputEntry{
					"version": {
						From:   ir.StepOutputSourceStdout,
						Decode: ir.StepOutputDecodeJSON,
						Select: ".version",
					},
				},
			},
		},
		{
			name: "StructuredValueEntry",
			input: map[string]any{
				"label": map[string]any{
					"value": "ver - ${build.output.version}",
				},
			},
			expected: &outputConfig{
				StructuredOutput: map[string]ir.StepOutputEntry{
					"label": {
						HasValue: true,
						Value:    "ver - ${build.output.version}",
					},
				},
			},
		},
		{
			name:    "InvalidOutputType",
			input:   123,
			wantErr: "output must be a string or object",
		},
		{
			name: "InvalidSourceField",
			input: map[string]any{
				"version": map[string]any{
					"from": "network",
				},
			},
			wantErr: `output.version: from must be one of "stdout", "stderr", or "file"`,
		},
		{
			name: "SelectRequiresStructuredDecode",
			input: map[string]any{
				"version": map[string]any{
					"from":   "stdout",
					"decode": "text",
					"select": ".version",
				},
			},
			wantErr: `output.version: select requires decode to be "json" or "yaml"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := parseOutputConfig(tt.input)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, cfg)
		})
	}
}

func TestBuildStepStructuredOutput(t *testing.T) {
	t.Parallel()

	s := &step{
		Output: map[string]any{
			"meta": map[string]any{
				"from":   "stdout",
				"decode": "json",
			},
			"label": "ver - ${build.output.version}",
		},
	}

	result, err := buildStepStructuredOutput(testStepBuildContext(), s)
	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.Equal(t, ir.StepOutputSourceStdout, result["meta"].From)
	assert.Equal(t, ir.StepOutputDecodeJSON, result["meta"].Decode)
	assert.True(t, result["label"].HasValue)
	assert.Equal(t, "ver - ${build.output.version}", result["label"].Value)
}

func TestBuildStepExecutorInfersNoopForStructuredOutput(t *testing.T) {
	t.Parallel()

	s := &step{
		Output: map[string]any{
			"versionLabel": "ver - ${build.output.version}",
		},
	}
	result := &ir.Step{
		ExecutorConfig: ir.ExecutorConfig{Config: make(map[string]any)},
		StructuredOutput: map[string]ir.StepOutputEntry{
			"versionLabel": {HasValue: true, Value: "ver - ${build.output.version}"},
		},
	}

	err := buildStepExecutor(testStepBuildContext(), s, result)
	require.NoError(t, err)
	assert.Equal(t, "noop", result.ExecutorConfig.Type)
}

func TestBuildStepExecutorInfersNoopForStructuredOutputWithWhitespaceScript(t *testing.T) {
	t.Parallel()

	s := &step{
		Script: "  \n\t  ",
		Output: map[string]any{
			"versionLabel": "ver - ${build.output.version}",
		},
	}
	result := &ir.Step{
		ExecutorConfig: ir.ExecutorConfig{Config: make(map[string]any)},
		StructuredOutput: map[string]ir.StepOutputEntry{
			"versionLabel": {HasValue: true, Value: "ver - ${build.output.version}"},
		},
	}

	err := buildStepExecutor(testStepBuildContext(), s, result)
	require.NoError(t, err)
	assert.Equal(t, "noop", result.ExecutorConfig.Type)
}

func TestBuildStepExecutorDoesNotInferNoopForStructuredStreamOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
	}{
		{name: "Stdout", source: ir.StepOutputSourceStdout},
		{name: "Stderr", source: ir.StepOutputSourceStderr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := &step{
				Output: map[string]any{
					"captured": map[string]any{"from": string(tt.source)},
				},
			}
			result := &ir.Step{
				ExecutorConfig: ir.ExecutorConfig{Config: make(map[string]any)},
				StructuredOutput: map[string]ir.StepOutputEntry{
					"captured": {From: tt.source},
				},
			}

			err := buildStepExecutor(testStepBuildContext(), s, result)
			require.NoError(t, err)
			assert.Empty(t, result.ExecutorConfig.Type)
		})
	}
}

func TestParseStructuredOutputEmpty(t *testing.T) {
	t.Parallel()

	result, err := parseStructuredOutput(map[string]any{})
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestBuildStepStructuredOutputError(t *testing.T) {
	t.Parallel()

	s := &step{
		Output: map[string]any{
			"meta": map[string]any{
				"from": "network",
			},
		},
	}

	_, err := buildStepStructuredOutput(testStepBuildContext(), s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `output.meta: from must be one of "stdout", "stderr", or "file"`)
}

func TestParseStructuredOutputEntryValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   any
		wantErr string
	}{
		{
			name: "FromMustBeString",
			input: map[string]any{
				"from": 123,
			},
			wantErr: "from must be a string",
		},
		{
			name: "PathMustBeString",
			input: map[string]any{
				"from": "file",
				"path": 123,
			},
			wantErr: "path must be a string",
		},
		{
			name: "DecodeMustBeString",
			input: map[string]any{
				"from":   "stdout",
				"decode": 123,
			},
			wantErr: "decode must be a string",
		},
		{
			name: "SelectMustBeString",
			input: map[string]any{
				"from":   "stdout",
				"decode": "json",
				"select": 123,
			},
			wantErr: "select must be a string",
		},
		{
			name: "UnknownField",
			input: map[string]any{
				"from":    "stdout",
				"invalid": true,
			},
			wantErr: `unknown field "invalid"`,
		},
		{
			name: "ValueAndFromConflict",
			input: map[string]any{
				"value": "hello",
				"from":  "stdout",
			},
			wantErr: "value and from cannot be used together",
		},
		{
			name: "MissingValueAndFrom",
			input: map[string]any{
				"decode": "json",
			},
			wantErr: "entry must specify either a literal value or from",
		},
		{
			name: "ValueCannotUseSourceFields",
			input: map[string]any{
				"value":  "hello",
				"decode": "json",
			},
			wantErr: "path, decode, and select are only valid with from",
		},
		{
			name: "PathOnlyValidForFile",
			input: map[string]any{
				"from": "stdout",
				"path": "meta.json",
			},
			wantErr: "path is only valid when from is file",
		},
		{
			name: "FileSourceRequiresPath",
			input: map[string]any{
				"from": "file",
			},
			wantErr: "path is required when from is file",
		},
		{
			name: "DecodeMustBeSupported",
			input: map[string]any{
				"from":   "stdout",
				"decode": "xml",
			},
			wantErr: `decode must be one of "text", "json", or "yaml"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseStructuredOutputEntry(tt.input)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
