// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuoteParamValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     []string
		paramDefs []ir.ParamDef
		expect    []string
	}{
		{
			name:   "named param with spaces",
			input:  []string{"topic=hello world"},
			expect: []string{`topic="hello world"`},
		},
		{
			name:   "named param without spaces",
			input:  []string{"topic=hello"},
			expect: []string{`topic="hello"`},
		},
		{
			name:   "positional param with spaces",
			input:  []string{"hello world"},
			expect: []string{`"hello world"`},
		},
		{
			name:   "positional param without spaces",
			input:  []string{"hello"},
			expect: []string{`"hello"`},
		},
		{
			name:   "multiple params",
			input:  []string{"topic=hello world", "count=42", "greeting"},
			expect: []string{`topic="hello world"`, `count="42"`, `"greeting"`},
		},
		{
			name:   "empty slice",
			input:  []string{},
			expect: []string{},
		},
		{
			name:   "param with quotes in value",
			input:  []string{`msg=say "hi"`},
			expect: []string{`msg="say \"hi\""`},
		},
		{
			name:      "positional params stored with numeric placeholders",
			input:     []string{"1=hello world", "2=42"},
			paramDefs: []ir.ParamDef{{Name: ""}, {Name: ""}},
			expect:    []string{`"hello world"`, `"42"`},
		},
		{
			name:      "numeric named params stay named",
			input:     []string{"1=hello"},
			paramDefs: []ir.ParamDef{{Name: "1"}},
			expect:    []string{`1="hello"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := spec.QuoteRuntimeParams(tt.input, tt.paramDefs)
			assert.Equal(t, tt.expect, result)
		})
	}
}

func TestRestoreDAGFromStatus_ParamsWithSpaces(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{
		Name:     "test-dag",
		YamlData: []byte("params:\n  - topic: \"\"\nsteps:\n  - name: test\n    command: echo $topic"),
	}

	status := &ir.DAGRunStatus{
		ParamsList: []string{"topic=hello world"},
	}

	result, err := restoreDAGFromStatus(context.Background(), dag, status)
	require.NoError(t, err)

	// The restored params should preserve "hello world" as a single value
	found := slices.Contains(result.Params, "topic=hello world")
	assert.True(t, found, "expected 'topic=hello world' in params, got: %v", result.Params)
}

func TestRestoreDAGFromStatus_PositionalParamsRemainOverrides(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{
		Name:     "test-dag",
		YamlData: []byte("params: \"default\"\nsteps:\n  - name: test\n    command: echo $1"),
		ParamDefs: []ir.ParamDef{
			{Name: ""},
		},
	}

	status := &ir.DAGRunStatus{
		ParamsList: []string{"1=override"},
	}

	result, err := restoreDAGFromStatus(context.Background(), dag, status)
	require.NoError(t, err)
	assert.Equal(t, []string{"1=override"}, result.Params)
}

func TestRestoreDAGFromStatus_PreservesExplicitWorkingDirFromYAML(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	dag := &ir.DAG{
		Name:       "test-dag",
		WorkingDir: workDir,
		YamlData: fmt.Appendf(nil, `
working_dir: %q
steps:
  - name: test
    run: pwd
`, workDir),
	}
	status := &ir.DAGRunStatus{}

	result, err := restoreDAGFromStatus(context.Background(), dag, status)
	require.NoError(t, err)
	assert.Equal(t, workDir, result.WorkingDir)
	assert.True(t, result.WorkingDirExplicit)
}

func TestRestoreDAGFromStatus_PreservesBaseConfigWorkingDirAsExplicit(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	dag := &ir.DAG{
		Name:       "test-dag",
		WorkingDir: workDir,
		YamlData: []byte(`
steps:
  - name: test
    run: pwd
`),
		BaseConfigData: fmt.Appendf(nil, "working_dir: %q\n", workDir),
	}
	status := &ir.DAGRunStatus{}

	result, err := restoreDAGFromStatus(context.Background(), dag, status)
	require.NoError(t, err)
	assert.Equal(t, workDir, result.WorkingDir)
	assert.True(t, result.WorkingDirExplicit)
}

func TestRestoreDAGFromStatus_PrefersPersistedRunWorkingDir(t *testing.T) {
	t.Parallel()

	persistedWorkDir := t.TempDir()
	dag := &ir.DAG{
		Name:       "test-dag",
		WorkingDir: "/changed-work-dir",
		YamlData: []byte(`
working_dir: /changed-work-dir
steps:
  - name: test
    run: pwd
`),
	}
	status := &ir.DAGRunStatus{WorkingDir: persistedWorkDir}

	result, err := restoreDAGFromStatus(context.Background(), dag, status)
	require.NoError(t, err)
	assert.Equal(t, persistedWorkDir, result.WorkingDir)
	assert.True(t, result.WorkingDirExplicit)
}

func TestRestoreDAGFromStatus_RestoresRegistryAuthsFromYAML(t *testing.T) {
	dag := &ir.DAG{
		Name: "test-dag",
		YamlData: []byte(`
registry_auths:
  registry.example.com:
    username: ${REGISTRY_USER}
    password: ${REGISTRY_PASSWORD}
steps:
  - name: test
    run: echo hello
`),
	}
	status := &ir.DAGRunStatus{}

	result, err := restoreDAGFromStatus(context.Background(), dag, status)
	require.NoError(t, err)
	require.Contains(t, result.RegistryAuths, "registry.example.com")
	require.Equal(t, "${REGISTRY_USER}", result.RegistryAuths["registry.example.com"].Username)
	require.Equal(t, "${REGISTRY_PASSWORD}", result.RegistryAuths["registry.example.com"].Password)
}

func TestRestoreDAGFromStatus_RestoresRegistryAuthsFromBaseConfig(t *testing.T) {
	dag := &ir.DAG{
		Name: "test-dag",
		YamlData: []byte(`
steps:
  - name: test
    run: echo hello
`),
		BaseConfigData: []byte(`
registry_auths:
  registry.example.com:
    username: ${REGISTRY_USER}
    password: ${REGISTRY_PASSWORD}
`),
	}
	status := &ir.DAGRunStatus{}

	result, err := restoreDAGFromStatus(context.Background(), dag, status)
	require.NoError(t, err)
	require.Contains(t, result.RegistryAuths, "registry.example.com")
	require.Equal(t, "${REGISTRY_USER}", result.RegistryAuths["registry.example.com"].Username)
	require.Equal(t, "${REGISTRY_PASSWORD}", result.RegistryAuths["registry.example.com"].Password)
}

func TestRestoreDAGFromStatus_RestoresHarnessConfigFromBaseConfig(t *testing.T) {
	dag := &ir.DAG{
		Name: "test-dag",
		YamlData: []byte(`
steps:
  - run: Review the repository
`),
		BaseConfigData: []byte(`
harnesses:
  passthrough:
    binary: cat
    prompt_mode: stdin
harness:
  provider: passthrough
`),
	}
	status := &ir.DAGRunStatus{}

	result, err := restoreDAGFromStatus(context.Background(), dag, status)
	require.NoError(t, err)
	require.NotNil(t, result.Harness)
	assert.Equal(t, "passthrough", result.Harness.Config["provider"])
	require.NotNil(t, result.Harnesses)
	require.Contains(t, result.Harnesses, "passthrough")
	assert.Equal(t, "cat", result.Harnesses["passthrough"].Binary)
}
