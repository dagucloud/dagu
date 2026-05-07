// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func structuredOutputTestContext(t *testing.T, dag *core.DAG, workDir string, envs ...string) context.Context {
	t.Helper()

	if dag == nil {
		dag = &core.DAG{Name: "structured-output-test"}
	}

	opts := []ContextOption{WithWorkDir(workDir)}
	if len(envs) > 0 {
		opts = append(opts, WithEnvVars(envs...))
	}

	return NewContext(context.Background(), dag, "run-id", filepath.Join(workDir, "dag.log"), opts...)
}

func TestNodeResolveStructuredOutputEntry(t *testing.T) {
	t.Parallel()

	releaseRef := "${RELEASE}"

	tests := []struct {
		name    string
		dag     *core.DAG
		entry   core.StepOutputEntry
		prepare func(*testing.T, string, *Node)
		want    any
		wantErr string
	}{
		{
			name: "LiteralValuePreservesShellSyntax",
			entry: core.StepOutputEntry{
				HasValue: true,
				Value:    "`date +%Y` $HOME " + releaseRef,
			},
			want: "`date +%Y` $HOME v1.2.3",
		},
		{
			name: "LiteralTypedMapRecurses",
			entry: core.StepOutputEntry{
				HasValue: true,
				Value: map[string]string{
					"version": releaseRef,
				},
			},
			want: map[string]any{
				"version": "v1.2.3",
			},
		},
		{
			name: "LiteralTypedSliceRecurses",
			entry: core.StepOutputEntry{
				HasValue: true,
				Value:    []string{releaseRef, "stable"},
			},
			want: []any{"v1.2.3", "stable"},
		},
		{
			name: "LiteralPointerRecurses",
			entry: core.StepOutputEntry{
				HasValue: true,
				Value:    &releaseRef,
			},
			want: "v1.2.3",
		},
		{
			name: "JSONDecodeFailure",
			entry: core.StepOutputEntry{
				From:   core.StepOutputSourceStdout,
				Decode: core.StepOutputDecodeJSON,
			},
			prepare: func(_ *testing.T, _ string, node *Node) {
				node.outputs.outputCaptured = true
				node.outputs.outputData = "not-json"
			},
			wantErr: "result: failed to decode JSON",
		},
		{
			name: "StdoutWithoutCaptureReturnsEmpty",
			entry: core.StepOutputEntry{
				From: core.StepOutputSourceStdout,
			},
			want: "",
		},
		{
			name: "TextFromStdoutIsTrimmed",
			entry: core.StepOutputEntry{
				From: core.StepOutputSourceStdout,
			},
			prepare: func(_ *testing.T, _ string, node *Node) {
				node.outputs.outputCaptured = true
				node.outputs.outputData = "  release-ready \n"
			},
			want: "release-ready",
		},
		{
			name: "JSONWithoutSelectReturnsObject",
			entry: core.StepOutputEntry{
				From:   core.StepOutputSourceStdout,
				Decode: core.StepOutputDecodeJSON,
			},
			prepare: func(_ *testing.T, _ string, node *Node) {
				node.outputs.outputCaptured = true
				node.outputs.outputData = `{"version":"v1.2.3"}`
			},
			want: map[string]any{
				"version": "v1.2.3",
			},
		},
		{
			name: "YAMLDecodeFailure",
			entry: core.StepOutputEntry{
				From:   core.StepOutputSourceStdout,
				Decode: core.StepOutputDecodeYAML,
			},
			prepare: func(_ *testing.T, _ string, node *Node) {
				node.outputs.outputCaptured = true
				node.outputs.outputData = "foo: [bar"
			},
			wantErr: "result: failed to decode YAML",
		},
		{
			name: "YAMLWithoutSelectReturnsObject",
			entry: core.StepOutputEntry{
				From:   core.StepOutputSourceStdout,
				Decode: core.StepOutputDecodeYAML,
			},
			prepare: func(_ *testing.T, _ string, node *Node) {
				node.outputs.outputCaptured = true
				node.outputs.outputData = "artifact:\n  path: build/report.md\n"
			},
			want: map[string]any{
				"artifact": map[string]any{
					"path": "build/report.md",
				},
			},
		},
		{
			name: "YAMLSelectSuccess",
			entry: core.StepOutputEntry{
				From:   core.StepOutputSourceStdout,
				Decode: core.StepOutputDecodeYAML,
				Select: ".artifact.path",
			},
			prepare: func(_ *testing.T, _ string, node *Node) {
				node.outputs.outputCaptured = true
				node.outputs.outputData = "artifact:\n  path: build/report.md\n"
			},
			want: "build/report.md",
		},
		{
			name: "SelectFailure",
			entry: core.StepOutputEntry{
				From:   core.StepOutputSourceStdout,
				Decode: core.StepOutputDecodeJSON,
				Select: ".artifact[",
			},
			prepare: func(_ *testing.T, _ string, node *Node) {
				node.outputs.outputCaptured = true
				node.outputs.outputData = `{"version":"v1.2.3"}`
			},
			wantErr: `result: failed to resolve select path ".artifact["`,
		},
		{
			name: "TextFromStderrIsTrimmed",
			entry: core.StepOutputEntry{
				From: core.StepOutputSourceStderr,
			},
			prepare: func(_ *testing.T, _ string, node *Node) {
				node.outputs.stderrOutputCaptured = true
				node.outputs.stderrOutputData = "  retry required \n"
			},
			want: "retry required",
		},
		{
			name: "StderrWithoutCaptureReturnsEmpty",
			entry: core.StepOutputEntry{
				From: core.StepOutputSourceStderr,
			},
			want: "",
		},
		{
			name: "JSONFromStderrSelectSuccess",
			entry: core.StepOutputEntry{
				From:   core.StepOutputSourceStderr,
				Decode: core.StepOutputDecodeJSON,
				Select: ".warning",
			},
			prepare: func(_ *testing.T, _ string, node *Node) {
				node.outputs.stderrOutputCaptured = true
				node.outputs.stderrOutputData = `{"warning":"retry required"}`
			},
			want: "retry required",
		},
		{
			name: "UnsupportedDecode",
			entry: core.StepOutputEntry{
				From:   core.StepOutputSourceStdout,
				Decode: "xml",
			},
			prepare: func(_ *testing.T, _ string, node *Node) {
				node.outputs.outputCaptured = true
				node.outputs.outputData = `{}`
			},
			wantErr: `result: unsupported decode "xml"`,
		},
		{
			name: "FileSourceSuccess",
			entry: core.StepOutputEntry{
				From:   core.StepOutputSourceFile,
				Path:   "meta.json",
				Decode: core.StepOutputDecodeJSON,
				Select: ".artifact.path",
			},
			prepare: func(t *testing.T, workDir string, _ *Node) {
				t.Helper()
				require.NoError(t, os.WriteFile(filepath.Join(workDir, "meta.json"), []byte(`{"artifact":{"path":"build/report.md"}}`), 0o600))
			},
			want: "build/report.md",
		},
		{
			name: "FileSourceTextTrimmed",
			entry: core.StepOutputEntry{
				From: core.StepOutputSourceFile,
				Path: "meta.txt",
			},
			prepare: func(t *testing.T, workDir string, _ *Node) {
				t.Helper()
				require.NoError(t, os.WriteFile(filepath.Join(workDir, "meta.txt"), []byte("  release-ready \n"), 0o600))
			},
			want: "release-ready",
		},
		{
			name: "FileSourceMissing",
			entry: core.StepOutputEntry{
				From: core.StepOutputSourceFile,
				Path: "missing.json",
			},
			wantErr: `result: failed to read file`,
		},
		{
			name: "FileSourceReadError",
			entry: core.StepOutputEntry{
				From: core.StepOutputSourceFile,
				Path: "meta.json",
			},
			prepare: func(t *testing.T, workDir string, _ *Node) {
				t.Helper()
				require.NoError(t, os.Mkdir(filepath.Join(workDir, "meta.json"), 0o755))
			},
			wantErr: `result: failed to read file`,
		},
		{
			name: "FileSourceAtLimit",
			dag: &core.DAG{
				Name:          "structured-output-test",
				MaxOutputSize: 8,
			},
			entry: core.StepOutputEntry{
				From: core.StepOutputSourceFile,
				Path: "meta.txt",
			},
			prepare: func(t *testing.T, workDir string, _ *Node) {
				t.Helper()
				require.NoError(t, os.WriteFile(filepath.Join(workDir, "meta.txt"), []byte("12345678"), 0o600))
			},
			want: "12345678",
		},
		{
			name: "FileSourceTooLarge",
			dag: &core.DAG{
				Name:          "structured-output-test",
				MaxOutputSize: 8,
			},
			entry: core.StepOutputEntry{
				From: core.StepOutputSourceFile,
				Path: "meta.json",
			},
			prepare: func(t *testing.T, workDir string, _ *Node) {
				t.Helper()
				require.NoError(t, os.WriteFile(filepath.Join(workDir, "meta.json"), []byte("too-large-payload"), 0o600))
			},
			wantErr: "output exceeded maximum size limit",
		},
		{
			name: "UnsupportedSource",
			entry: core.StepOutputEntry{
				From: "network",
			},
			wantErr: `result: unsupported output source "network"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			workDir := t.TempDir()
			ctx := structuredOutputTestContext(t, tt.dag, workDir, "RELEASE=v1.2.3")
			node := NodeWithData(NodeData{})
			if tt.prepare != nil {
				tt.prepare(t, workDir, node)
			}

			got, err := node.resolveStructuredOutputEntry(ctx, "result", tt.entry, "", false)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNodeEvaluateStructuredLiteral(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	ctx := structuredOutputTestContext(t, nil, workDir, "RELEASE=v1.2.3")
	node := NodeWithData(NodeData{})

	tests := []struct {
		name  string
		value any
		want  any
	}{
		{
			name:  "Nil",
			value: nil,
			want:  nil,
		},
		{
			name:  "Primitive",
			value: true,
			want:  true,
		},
		{
			name:  "NilPointer",
			value: (*string)(nil),
			want:  nil,
		},
		{
			name: "SliceShortcut",
			value: []any{
				"${RELEASE}",
				true,
				nil,
			},
			want: []any{"v1.2.3", true, nil},
		},
		{
			name: "MapShortcut",
			value: map[string]any{
				"version":  "${RELEASE}",
				"approved": true,
			},
			want: map[string]any{
				"version":  "v1.2.3",
				"approved": true,
			},
		},
		{
			name:  "TypedArray",
			value: [2]string{"${RELEASE}", "stable"},
			want:  []any{"v1.2.3", "stable"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := node.evaluateStructuredLiteral(ctx, tt.value)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNodeCaptureOutputSchema(t *testing.T) {
	t.Parallel()

	validSchema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"category", "confidence"},
		"properties": map[string]any{
			"category": map[string]any{"type": "string"},
			"confidence": map[string]any{
				"type":    "number",
				"minimum": float64(0),
				"maximum": float64(1),
			},
		},
	}

	t.Run("PublishesValidatedStdoutWhenNoOutputMapping", func(t *testing.T) {
		t.Parallel()

		workDir := t.TempDir()
		ctx := structuredOutputTestContext(t, nil, workDir)
		node := NodeWithData(NodeData{
			Step: core.Step{OutputSchema: validSchema},
		})
		node.outputs.outputCaptured = true
		node.outputs.outputData = `{"category":"bug","confidence":0.9}`

		require.NoError(t, node.captureOutput(ctx))
		state := node.State()
		require.NotNil(t, state.OutputValue)
		assert.JSONEq(t, `{"category":"bug","confidence":0.9}`, *state.OutputValue)
	})

	t.Run("InvalidJSONFails", func(t *testing.T) {
		t.Parallel()

		workDir := t.TempDir()
		ctx := structuredOutputTestContext(t, nil, workDir)
		node := NodeWithData(NodeData{
			Step: core.Step{OutputSchema: validSchema},
		})
		node.outputs.outputCaptured = true
		node.outputs.outputData = `not-json secret-value`

		err := node.captureOutput(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to decode stdout JSON for output_schema")
		assert.NotContains(t, err.Error(), "secret-value")
	})

	t.Run("EmptyStdoutFailsWithContractError", func(t *testing.T) {
		t.Parallel()

		workDir := t.TempDir()
		ctx := structuredOutputTestContext(t, nil, workDir)
		node := NodeWithData(NodeData{
			Step: core.Step{OutputSchema: validSchema},
		})
		node.outputs.outputCaptured = true
		node.outputs.outputData = "  \n\t  "

		err := node.captureOutput(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "output_schema requires stdout to contain a JSON value matching the schema")
	})

	t.Run("SchemaMismatchFails", func(t *testing.T) {
		t.Parallel()

		workDir := t.TempDir()
		ctx := structuredOutputTestContext(t, nil, workDir)
		node := NodeWithData(NodeData{
			Step: core.Step{OutputSchema: validSchema},
		})
		node.outputs.outputCaptured = true
		node.outputs.outputData = `{"category":"leak-sentinel-bug","confidence":2}`

		err := node.captureOutput(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "stdout JSON does not match output_schema")
		assert.NotContains(t, err.Error(), "leak-sentinel-bug")
	})

	t.Run("PreservesExecutionErrorOverSchemaError", func(t *testing.T) {
		t.Parallel()

		workDir := t.TempDir()
		ctx := structuredOutputTestContext(t, nil, workDir)
		node := NodeWithData(NodeData{
			Step: core.Step{OutputSchema: validSchema},
		})
		node.outputs.outputCaptured = true
		node.outputs.outputData = `not-json`
		execErr := errors.New("executor failed")
		node.SetError(execErr)

		require.NoError(t, node.captureOutput(ctx))
		assert.ErrorIs(t, node.Error(), execErr)
	})

	t.Run("ValidatesBeforeExplicitOutputMapping", func(t *testing.T) {
		t.Parallel()

		workDir := t.TempDir()
		ctx := structuredOutputTestContext(t, nil, workDir)
		node := NodeWithData(NodeData{
			Step: core.Step{
				OutputSchema: validSchema,
				StructuredOutput: map[string]core.StepOutputEntry{
					"category": {
						From:   core.StepOutputSourceStdout,
						Decode: core.StepOutputDecodeJSON,
						Select: ".category",
					},
				},
			},
		})
		node.outputs.outputCaptured = true
		node.outputs.outputData = `{"category":"bug","confidence":0.9}`

		require.NoError(t, node.captureOutput(ctx))
		state := node.State()
		require.NotNil(t, state.OutputValue)
		assert.JSONEq(t, `{"category":"bug"}`, *state.OutputValue)
	})

	t.Run("LegacyOutputVariableDoesNotOverrideSchemaOutput", func(t *testing.T) {
		t.Parallel()

		workDir := t.TempDir()
		ctx := structuredOutputTestContext(t, nil, workDir)
		node := NodeWithData(NodeData{
			Step: core.Step{
				Output:       "CLASSIFY_RAW",
				OutputSchema: validSchema,
			},
		})
		node.outputs.outputCaptured = true
		node.outputs.outputData = `{"category":"bug","confidence":0.9}`

		require.NoError(t, node.captureOutput(ctx))
		state := node.State()
		require.NotNil(t, state.OutputValue)
		assert.JSONEq(t, `{"category":"bug","confidence":0.9}`, *state.OutputValue)
		assert.Equal(t, `{"category":"bug","confidence":0.9}`, node.OutputVariablesMap()["CLASSIFY_RAW"])
	})
}

func TestNodeEvaluateStructuredOutput(t *testing.T) {
	t.Parallel()

	t.Run("PublishesCompactJSON", func(t *testing.T) {
		t.Parallel()

		workDir := t.TempDir()
		ctx := structuredOutputTestContext(t, nil, workDir, "CHANNEL=stable")
		node := NodeWithData(NodeData{
			Step: core.Step{
				StructuredOutput: map[string]core.StepOutputEntry{
					"version": {
						HasValue: true,
						Value:    "v1.2.3",
					},
					"meta": {
						HasValue: true,
						Value: map[string]any{
							"channel":  "${CHANNEL}",
							"approved": true,
						},
					},
				},
			},
		})

		got, err := node.evaluateStructuredOutput(ctx, "", false)
		require.NoError(t, err)
		assert.JSONEq(t, `{"version":"v1.2.3","meta":{"channel":"stable","approved":true}}`, got)
	})

	t.Run("EnforcesMaxOutputSize", func(t *testing.T) {
		t.Parallel()

		workDir := t.TempDir()
		ctx := structuredOutputTestContext(t, &core.DAG{
			Name:          "structured-output-test",
			MaxOutputSize: 32,
		}, workDir)
		node := NodeWithData(NodeData{
			Step: core.Step{
				StructuredOutput: map[string]core.StepOutputEntry{
					"payload": {
						HasValue: true,
						Value:    strings.Repeat("x", 64),
					},
				},
			},
		})

		_, err := node.evaluateStructuredOutput(ctx, "", false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "output exceeded maximum size limit")
	})

	t.Run("ReturnsEntryError", func(t *testing.T) {
		t.Parallel()

		workDir := t.TempDir()
		ctx := structuredOutputTestContext(t, nil, workDir)
		node := NodeWithData(NodeData{
			Step: core.Step{
				StructuredOutput: map[string]core.StepOutputEntry{
					"payload": {
						From: "network",
					},
				},
			},
		})

		_, err := node.evaluateStructuredOutput(ctx, "", false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `payload: unsupported output source "network"`)
	})
}
