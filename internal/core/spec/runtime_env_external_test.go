// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/stretchr/testify/require"
)

func TestResolveEnvIncludesDotenvFromResolvedWorkingDir(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	workDir := filepath.Join(root, "work", "quant-signal")
	dagDir := filepath.Join(root, "dags")
	require.NoError(t, os.MkdirAll(workDir, 0o750))
	require.NoError(t, os.MkdirAll(dagDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, ".env"), []byte("PYTHON_BIN=/usr/local/bin/python\nPROJECT_DIR=/work/quant-signal\n"), 0o600))

	baseConfig := filepath.Join(root, "base.yaml")
	require.NoError(t, os.WriteFile(baseConfig, fmt.Appendf(nil, "env:\n  - QUANT_SIGNAL_DIR: %q\n", workDir), 0o600))

	dagFile := filepath.Join(dagDir, "signal.yaml")
	require.NoError(t, os.WriteFile(dagFile, []byte(`
working_dir: ${QUANT_SIGNAL_DIR}
steps:
  - name: run_signals
    run: ${PYTHON_BIN} ${PROJECT_DIR}/signals/run_signals.py
`), 0o600))

	dag, err := spec.Load(context.Background(), dagFile, spec.WithBaseConfig(baseConfig))
	require.NoError(t, err)

	dag.Env = nil
	env, err := spec.ResolveEnv(context.Background(), dag, spec.QuoteRuntimeParams(nil, dag.ParamDefs), spec.ResolveEnvOptions{
		BaseConfig: baseConfig,
	})
	require.NoError(t, err)

	envMap := runtimeEnvSliceMap(env)
	require.Equal(t, workDir, envMap["QUANT_SIGNAL_DIR"])
	require.Equal(t, "/usr/local/bin/python", envMap["PYTHON_BIN"])
	require.Equal(t, "/work/quant-signal", envMap["PROJECT_DIR"])
}

func TestResolveEnvWithWarningsReturnsDotenvWarnings(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, ".env"), []byte("INVALID LINE\n"), 0o600))

	dag, err := spec.LoadYAMLWithOpts(context.Background(), fmt.Appendf(nil, `
working_dir: %s
dotenv: .env
steps:
  - run: echo hello
`, root), spec.BuildOpts{Flags: spec.BuildFlagNoEval})
	require.NoError(t, err)

	result, err := spec.ResolveEnvWithWarnings(context.Background(), dag, nil, spec.ResolveEnvOptions{})
	require.NoError(t, err)
	require.Empty(t, result.Env)
	require.Len(t, result.BuildWarnings, 1)
	require.Contains(t, result.BuildWarnings[0], "failed to load .env file")
}

func TestResolveEnvWithWarningsLoadsDotenvWithRuntimeParams(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	workDir := filepath.Join(root, "zscores")
	require.NoError(t, os.MkdirAll(workDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, ".env.foo"), []byte("TARGET_TABLE=foo\n"), 0o600))

	yamlData := fmt.Appendf(nil, `
name: calculate_zscores
working_dir: %q
params:
  - name: COL
    type: string
    required: true
dotenv:
  - ".env.${COL}"
steps:
  - name: assert_variables_defined
    run: echo "${TARGET_TABLE}"
`, workDir)
	dag, err := spec.LoadYAML(context.Background(), yamlData, spec.WithParams("COL=foo"))
	require.NoError(t, err)
	dag.LoadDotEnv(context.Background())
	require.Equal(t, "foo", runtimeEnvSliceMap(dag.Env)["TARGET_TABLE"])

	persisted := dag.Clone()
	persisted.Env = nil
	persisted.Params = nil
	result, err := spec.ResolveEnvWithWarnings(context.Background(), persisted, []string{"COL=foo"}, spec.ResolveEnvOptions{})
	require.NoError(t, err)
	require.Equal(t, "foo", runtimeEnvSliceMap(result.Env)["TARGET_TABLE"])
}

func TestResolveEnvWithWarningsDoesNotMutateDAGBackingSlices(t *testing.T) {
	t.Parallel()

	t.Run("env", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, ".env"), []byte("DOTENV_VALUE=ready\n"), 0o600))

		dag, err := spec.LoadYAMLWithOpts(context.Background(), fmt.Appendf(nil, `
working_dir: %s
dotenv: .env
steps:
  - run: echo hello
`, root), spec.BuildOpts{Flags: spec.BuildFlagNoEval})
		require.NoError(t, err)

		dag.Env = make([]string, 0, 1)

		result, err := spec.ResolveEnvWithWarnings(context.Background(), dag, nil, spec.ResolveEnvOptions{})
		require.NoError(t, err)
		require.Contains(t, result.Env, "DOTENV_VALUE=ready")
		require.Empty(t, dag.Env)
		require.Empty(t, dag.Env[:cap(dag.Env)][0])
	})

	t.Run("build warnings", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, ".env"), []byte("INVALID LINE\n"), 0o600))

		dag, err := spec.LoadYAMLWithOpts(context.Background(), fmt.Appendf(nil, `
working_dir: %s
dotenv: .env
steps:
  - run: echo hello
`, root), spec.BuildOpts{Flags: spec.BuildFlagNoEval})
		require.NoError(t, err)

		dag.BuildWarnings = make([]string, 1, 2)
		dag.BuildWarnings[0] = "existing warning"

		result, err := spec.ResolveEnvWithWarnings(context.Background(), dag, nil, spec.ResolveEnvOptions{})
		require.NoError(t, err)
		require.Len(t, result.BuildWarnings, 1)
		require.Len(t, dag.BuildWarnings, 1)
		require.Empty(t, dag.BuildWarnings[:cap(dag.BuildWarnings)][1])
	})
}

func runtimeEnvSliceMap(envs []string) map[string]string {
	envMap := make(map[string]string)
	for _, env := range envs {
		key, value, ok := strings.Cut(env, "=")
		if ok {
			envMap[key] = value
		}
	}
	return envMap
}
