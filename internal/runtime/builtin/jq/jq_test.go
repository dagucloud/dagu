// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package jq

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJQExecutor_RawOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		query          string
		script         string
		raw            bool
		expectedOutput string
		compareJSON    bool
	}{
		{
			name:           "SingleStringWithRawTrue",
			query:          ".foo",
			script:         `{"foo": "bar"}`,
			raw:            true,
			expectedOutput: "bar\n",
		},
		{
			name:           "SingleStringWithRawFalse",
			query:          ".foo",
			script:         `{"foo": "bar"}`,
			raw:            false,
			expectedOutput: "\"bar\"\n",
		},
		{
			name:           "SingleNumberWithRawTrue",
			query:          ".number",
			script:         `{"number": 42}`,
			raw:            true,
			expectedOutput: "42\n",
		},
		{
			name:           "SingleNumberWithRawFalse",
			query:          ".number",
			script:         `{"number": 42}`,
			raw:            false,
			expectedOutput: "42\n",
		},
		{
			name:           "SingleBooleanTrueWithRawTrue",
			query:          ".flag",
			script:         `{"flag": true}`,
			raw:            true,
			expectedOutput: "true\n",
		},
		{
			name:           "SingleBooleanFalseWithRawTrue",
			query:          ".flag",
			script:         `{"flag": false}`,
			raw:            true,
			expectedOutput: "false\n",
		},
		{
			name:           "NullValueWithRawTrue",
			query:          ".null_value",
			script:         `{"null_value": null}`,
			raw:            true,
			expectedOutput: "\n",
		},
		{
			name:           "ObjectWithRawTrue",
			query:          ".",
			script:         `{"foo": "bar", "baz": 123}`,
			raw:            true,
			expectedOutput: `{"foo":"bar","baz":123}`,
			compareJSON:    true,
		},
		{
			name:           "ObjectWithRawFalse",
			query:          ".",
			script:         `{"foo": "bar", "baz": 123}`,
			raw:            false,
			expectedOutput: `{"foo":"bar","baz":123}`,
			compareJSON:    true,
		},
		{
			name:           "ArrayFromObjectWithRawTrue",
			query:          ".items",
			script:         `{"items": [1, 2, 3]}`,
			raw:            true,
			expectedOutput: "[1,2,3]\n",
		},
		{
			name:           "ArrayFromObjectWithRawFalse",
			query:          ".items",
			script:         `{"items": [1, 2, 3]}`,
			raw:            false,
			expectedOutput: "[\n    1,\n    2,\n    3\n]\n",
		},
		{
			name:           "TopLevelArrayWithRawTrue",
			query:          ".",
			script:         `["foo", "bar"]`,
			raw:            true,
			expectedOutput: "[\"foo\",\"bar\"]\n",
		},
		{
			name:           "TopLevelArrayWithRawFalse",
			query:          ".",
			script:         `["foo", "bar"]`,
			raw:            false,
			expectedOutput: "[\n    \"foo\",\n    \"bar\"\n]\n",
		},
		{
			name:           "TopLevelStringWithRawTrue",
			query:          ".",
			script:         `"bar"`,
			raw:            true,
			expectedOutput: "bar\n",
		},
		{
			name:           "TopLevelStringWithRawFalse",
			query:          ".",
			script:         `"bar"`,
			raw:            false,
			expectedOutput: "\"bar\"\n",
		},
		{
			name:           "StringWithSpecialCharsWithRawTrue",
			query:          ".message",
			script:         `{"message": "hello\nworld"}`,
			raw:            true,
			expectedOutput: "hello\nworld\n",
		},
		{
			name:           "StringWithTabsWithRawTrue",
			query:          ".data",
			script:         `{"data": "a\tb\tc"}`,
			raw:            true,
			expectedOutput: "a\tb\tc\n",
		},
		{
			name:           "FloatNumberWithRawTrue",
			query:          ".pi",
			script:         `{"pi": 3.14159}`,
			raw:            true,
			expectedOutput: "3.14159\n",
		},
		{
			name:           "NegativeNumberWithRawTrue",
			query:          ".temp",
			script:         `{"temp": -10}`,
			raw:            true,
			expectedOutput: "-10\n",
		},
		{
			name:           "LargeFloatWithRawTrue",
			query:          ".big",
			script:         `{"big": 13786123706}`,
			raw:            true,
			expectedOutput: "13786123706\n",
		},
		{
			name:           "LargeFloatWithRawTrue",
			query:          ".big",
			script:         `{"big": 13786123706.101}`,
			raw:            true,
			expectedOutput: "13786123706.101\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			step := ir.Step{
				Commands: []ir.CommandEntry{{CmdWithArgs: tt.query}},
				Script:   tt.script,
				ExecutorConfig: ir.ExecutorConfig{
					Type: "jq",
					Config: map[string]any{
						"raw": tt.raw,
					},
				},
			}

			ctx := context.Background()
			executor, err := newJQ(ctx, step)
			require.NoError(t, err)

			executor.SetStdout(&stdout)
			executor.SetStderr(&stderr)

			err = executor.Run(ctx)
			require.NoError(t, err)

			output := stdout.String()
			if tt.compareJSON {
				require.JSONEq(t, tt.expectedOutput, strings.TrimSpace(output))
				return
			}
			assert.Equal(t, tt.expectedOutput, output)
		})
	}
}

func TestJQExecutor_MultipleOutputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		query          string
		script         string
		raw            bool
		expectedOutput string
	}{
		{
			name:           "MultipleStringsWithRawTrue",
			query:          ".items[]",
			script:         `{"items": ["foo", "bar", "baz"]}`,
			raw:            true,
			expectedOutput: "foo\nbar\nbaz\n",
		},
		{
			name:           "MultipleStringsWithRawFalse",
			query:          ".items[]",
			script:         `{"items": ["foo", "bar", "baz"]}`,
			raw:            false,
			expectedOutput: "\"foo\"\n\"bar\"\n\"baz\"\n",
		},
		{
			name:           "MultipleNumbersWithRawTrue",
			query:          ".numbers[]",
			script:         `{"numbers": [1, 2, 3]}`,
			raw:            true,
			expectedOutput: "1\n2\n3\n",
		},
		{
			name:           "MixedTypesWithRawTrue",
			query:          ".values[]",
			script:         `{"values": ["text", 42, true, null]}`,
			raw:            true,
			expectedOutput: "text\n42\ntrue\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			step := ir.Step{
				Commands: []ir.CommandEntry{{CmdWithArgs: tt.query}},
				Script:   tt.script,
				ExecutorConfig: ir.ExecutorConfig{
					Type: "jq",
					Config: map[string]any{
						"raw": tt.raw,
					},
				},
			}

			ctx := context.Background()
			executor, err := newJQ(ctx, step)
			require.NoError(t, err)

			executor.SetStdout(&stdout)
			executor.SetStderr(&stderr)

			err = executor.Run(ctx)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedOutput, stdout.String())
		})
	}
}

func TestJQExecutor_InvalidQuery(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	step := ir.Step{
		Commands: []ir.CommandEntry{{CmdWithArgs: "invalid jq syntax {"}},
		Script:   `{"foo": "bar"}`,
		ExecutorConfig: ir.ExecutorConfig{
			Type: "jq",
			Config: map[string]any{
				"raw": true,
			},
		},
	}

	ctx := context.Background()
	executor, err := newJQ(ctx, step)
	require.NoError(t, err)

	executor.SetStdout(&stdout)
	executor.SetStderr(&stderr)

	err = executor.Run(ctx)
	assert.Error(t, err)
}

func TestJQExecutor_InvalidJSON(t *testing.T) {
	t.Parallel()

	step := ir.Step{
		Commands: []ir.CommandEntry{{CmdWithArgs: ".foo"}},
		Script:   `invalid json`,
		ExecutorConfig: ir.ExecutorConfig{
			Type: "jq",
			Config: map[string]any{
				"raw": true,
			},
		},
	}

	ctx := context.Background()
	_, err := newJQ(ctx, step)
	assert.Error(t, err)
}

func TestJQExecutor_NoConfig(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer

	step := ir.Step{
		Commands: []ir.CommandEntry{{CmdWithArgs: ".foo"}},
		Script:   `{"foo": "bar"}`,
		ExecutorConfig: ir.ExecutorConfig{
			Type:   "jq",
			Config: nil, // No config, should default to raw: false
		},
	}

	ctx := context.Background()
	executor, err := newJQ(ctx, step)
	require.NoError(t, err)

	executor.SetStdout(&stdout)

	err = executor.Run(ctx)
	require.NoError(t, err)

	// Without raw config, should output JSON formatted
	assert.Contains(t, stdout.String(), "\"bar\"")
}

func TestJQExecutor_Kill(t *testing.T) {
	t.Parallel()

	step := ir.Step{
		Commands: []ir.CommandEntry{{CmdWithArgs: ".foo"}},
		Script:   `{"foo": "bar"}`,
		ExecutorConfig: ir.ExecutorConfig{
			Type: "jq",
		},
	}

	ctx := context.Background()
	executor, err := newJQ(ctx, step)
	require.NoError(t, err)

	// Kill should return nil (jq executor doesn't need cleanup)
	err = executor.Kill(nil)
	assert.NoError(t, err)
}

func TestJQExecutor_InputFromFile(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	// Write valid JSON to a temp file
	dir := t.TempDir()
	filePath := filepath.Join(dir, "input.json")
	err := os.WriteFile(filePath, []byte(`{"items": [{"name": "a"}, {"name": "b"}]}`), 0o600)
	require.NoError(t, err)

	step := ir.Step{
		Commands: []ir.CommandEntry{{CmdWithArgs: `.items[] | .name`}},
		Script:   "file://" + filePath,
		ExecutorConfig: ir.ExecutorConfig{
			Type: "jq",
			Config: map[string]any{
				"raw": true,
			},
		},
	}

	ctx := context.Background()
	executor, err := newJQ(ctx, step)
	require.NoError(t, err)

	executor.SetStdout(&stdout)
	executor.SetStderr(&stderr)

	err = executor.Run(ctx)
	require.NoError(t, err)

	assert.Equal(t, "a\nb\n", stdout.String())
}

func TestJQExecutor_ConfigInput(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	dir := t.TempDir()
	filePath := filepath.Join(dir, "input.json")
	err := os.WriteFile(filePath, []byte(`{"items": [{"name": "a"}, {"name": "b"}]}`), 0o600)
	require.NoError(t, err)

	step := ir.Step{
		Commands: []ir.CommandEntry{{CmdWithArgs: `.items[] | .name`}},
		ExecutorConfig: ir.ExecutorConfig{
			Type: "jq",
			Config: map[string]any{
				"raw":   true,
				"input": filePath,
			},
		},
	}

	ctx := context.Background()
	executor, err := newJQ(ctx, step)
	require.NoError(t, err)

	executor.SetStdout(&stdout)
	executor.SetStderr(&stderr)

	err = executor.Run(ctx)
	require.NoError(t, err)

	assert.Equal(t, "a\nb\n", stdout.String())
}

func TestJQExecutor_ConfigInputWithRawFalse(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	dir := t.TempDir()
	filePath := filepath.Join(dir, "input.json")
	err := os.WriteFile(filePath, []byte(`{"items": [{"name": "a"}, {"name": "b"}]}`), 0o600)
	require.NoError(t, err)

	step := ir.Step{
		Commands: []ir.CommandEntry{{CmdWithArgs: `.items[] | .name`}},
		ExecutorConfig: ir.ExecutorConfig{
			Type: "jq",
			Config: map[string]any{
				"raw":   false,
				"input": filePath,
			},
		},
	}

	ctx := context.Background()
	executor, err := newJQ(ctx, step)
	require.NoError(t, err)

	executor.SetStdout(&stdout)
	executor.SetStderr(&stderr)

	err = executor.Run(ctx)
	require.NoError(t, err)

	assert.Equal(t, "\"a\"\n\"b\"\n", stdout.String())
}

func TestJQExecutor_ConfigInputMutualExclusion(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "input.json")
	err := os.WriteFile(filePath, []byte(`{"foo": "bar"}`), 0o600)
	require.NoError(t, err)

	step := ir.Step{
		Commands: []ir.CommandEntry{{CmdWithArgs: ".foo"}},
		Script:   `{"foo": "bar"}`,
		ExecutorConfig: ir.ExecutorConfig{
			Type: "jq",
			Config: map[string]any{
				"input": filePath,
			},
		},
	}

	ctx := context.Background()
	_, err = newJQ(ctx, step)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestJQExecutor_ConfigInputFileNotFound(t *testing.T) {
	t.Parallel()

	step := ir.Step{
		Commands: []ir.CommandEntry{{CmdWithArgs: "."}},
		ExecutorConfig: ir.ExecutorConfig{
			Type: "jq",
			Config: map[string]any{
				"input": "/nonexistent/path/input.json",
			},
		},
	}

	ctx := context.Background()
	_, err := newJQ(ctx, step)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading input file")
	assert.Contains(t, err.Error(), "/nonexistent/path/input.json")
}

func TestJQExecutor_ConfigInputInvalidJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "bad.json")
	err := os.WriteFile(filePath, []byte(`not valid json`), 0o600)
	require.NoError(t, err)

	step := ir.Step{
		Commands: []ir.CommandEntry{{CmdWithArgs: "."}},
		ExecutorConfig: ir.ExecutorConfig{
			Type: "jq",
			Config: map[string]any{
				"input": filePath,
			},
		},
	}

	ctx := context.Background()
	_, err = newJQ(ctx, step)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parsing JSON from input file")
}

func TestJQExecutor_InputFromFile_InvalidJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "bad.json")
	err := os.WriteFile(filePath, []byte(`not valid json`), 0o600)
	require.NoError(t, err)

	step := ir.Step{
		Commands: []ir.CommandEntry{{CmdWithArgs: "."}},
		Script:   "file://" + filePath,
		ExecutorConfig: ir.ExecutorConfig{
			Type: "jq",
		},
	}

	ctx := context.Background()
	_, err = newJQ(ctx, step)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parsing JSON from file")
}

func TestJQExecutor_InputFromFile_NotFound(t *testing.T) {
	t.Parallel()

	step := ir.Step{
		Commands: []ir.CommandEntry{{CmdWithArgs: "."}},
		Script:   "file:///nonexistent/path/input.json",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "jq",
		},
	}

	ctx := context.Background()
	_, err := newJQ(ctx, step)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading input file")
}

func TestJQExecutor_NoInput(t *testing.T) {
	t.Parallel()

	step := ir.Step{
		Commands: []ir.CommandEntry{{CmdWithArgs: "."}},
		Script:   "",
		ExecutorConfig: ir.ExecutorConfig{
			Type: "jq",
		},
	}

	ctx := context.Background()
	_, err := newJQ(ctx, step)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no input provided")
}

func TestDecodeJqConfig_WithInput(t *testing.T) {
	t.Parallel()

	cfg := map[string]any{
		"raw":   true,
		"input": "/some/path.json",
	}

	var jqCfg jqConfig
	err := decodeJqConfig(cfg, &jqCfg)
	require.NoError(t, err)
	assert.True(t, jqCfg.Raw)
	assert.Equal(t, "/some/path.json", jqCfg.Input)
}

func TestDecodeJqConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		config   map[string]any
		expected jqConfig
	}{
		{
			name: "RawTrue",
			config: map[string]any{
				"raw": true,
			},
			expected: jqConfig{
				Raw: true,
			},
		},
		{
			name: "RawFalse",
			config: map[string]any{
				"raw": false,
			},
			expected: jqConfig{
				Raw: false,
			},
		},
		{
			name: "RawAsString",
			config: map[string]any{
				"raw": "true",
			},
			expected: jqConfig{
				Raw: true,
			},
		},
		{
			name:     "EmptyConfig",
			config:   map[string]any{},
			expected: jqConfig{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var cfg jqConfig
			err := decodeJqConfig(tt.config, &cfg)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, cfg)
		})
	}
}
