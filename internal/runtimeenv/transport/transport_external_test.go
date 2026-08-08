// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package transport_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/buildenv"
	"github.com/dagucloud/dagu/v2/internal/ir"
	_ "github.com/dagucloud/dagu/v2/internal/runtime/builtin"
	"github.com/dagucloud/dagu/v2/internal/runtimeenv"
	"github.com/dagucloud/dagu/v2/internal/runtimeenv/transport"
	"github.com/dagucloud/dagu/v2/internal/spec"
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
	result, err := transport.Resolve(context.Background(), dag, spec.QuoteRuntimeParams(nil, dag.ParamDefs), transport.Options{
		BaseConfig: baseConfig,
	})
	require.NoError(t, err)

	envMap := runtimeEnvSliceMap(result.Env)
	require.Equal(t, workDir, envMap["QUANT_SIGNAL_DIR"])
	require.Equal(t, "/usr/local/bin/python", envMap["PYTHON_BIN"])
	require.Equal(t, "/work/quant-signal", envMap["PROJECT_DIR"])
}

func TestResolveEnvConstDotenvPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, ".env.const"), []byte("CONST_DOTENV=ready\n"), 0o600))

	dag, err := spec.LoadYAML(context.Background(), fmt.Appendf(nil, `
consts:
  - env_file: .env.const
  - root: %s
working_dir: ${consts.root}
dotenv: ${consts.env_file}
steps:
  - name: print
    run: echo ${CONST_DOTENV}
`, root))
	require.NoError(t, err)

	resolveDAGRuntimeEnv(t, dag)
	require.Equal(t, "ready", runtimeEnvSliceMap(dag.Env)["CONST_DOTENV"])
}

func TestLoadPreservesRuntimeEnvironmentSnapshotState(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(context.Background(), []byte(`
dotenv: .env
env:
  - VALUE: ${VALUE}
steps:
  - run: echo hello
`), spec.WithBuildEnvSnapshot(buildenv.NewSnapshot([]string{"VALUE=transported"}, true)))
	require.NoError(t, err)
	require.True(t, dag.RuntimeResolved)
	require.Equal(t, "transported", runtimeEnvSliceMap(dag.Env)["VALUE"])
}

func TestResolveRuntimeEnvReturnsDotenvWarnings(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, ".env"), []byte("INVALID LINE\n"), 0o600))

	dag, err := spec.LoadYAML(context.Background(), fmt.Appendf(nil, `
working_dir: %s
dotenv: .env
steps:
  - run: echo hello
`, root), spec.WithoutEval())
	require.NoError(t, err)

	result, err := transport.Resolve(context.Background(), dag, nil, transport.Options{})
	require.NoError(t, err)
	require.Empty(t, result.Env)
	require.Len(t, result.Warnings, 1)
	require.Contains(t, result.Warnings[0], "failed to load .env file")
}

func TestResolveRuntimeEnvLoadsDotenvWithRuntimeParams(t *testing.T) {
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
	resolveDAGRuntimeEnv(t, dag)
	require.Equal(t, "foo", runtimeEnvSliceMap(dag.Env)["TARGET_TABLE"])

	persisted := dag.Clone()
	persisted.Env = nil
	persisted.Params = nil
	result, err := transport.Resolve(context.Background(), persisted, []string{"COL=foo"}, transport.Options{})
	require.NoError(t, err)
	require.Equal(t, "foo", runtimeEnvSliceMap(result.Env)["TARGET_TABLE"])
}

func TestResolveRuntimeEnvLoadsDotenvWithParamsReference(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	workDir := filepath.Join(root, "zscores")
	require.NoError(t, os.MkdirAll(workDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, ".env.prod"), []byte("TARGET_TABLE=prod\n"), 0o600))

	yamlData := fmt.Appendf(nil, `
name: calculate_zscores
working_dir: %q
params:
  - name: ENVIRONMENT
    type: string
dotenv:
  - ".env.${params.ENVIRONMENT}"
steps:
  - name: assert_variables_defined
    run: echo "${TARGET_TABLE}"
`, workDir)
	dag, err := spec.LoadYAML(context.Background(), yamlData, spec.WithParams("ENVIRONMENT=prod"))
	require.NoError(t, err)
	require.Contains(t, dag.Params, "ENVIRONMENT=prod")
	resolveDAGRuntimeEnv(t, dag)
	require.Empty(t, dag.BuildErrors)
	require.Equal(t, "prod", runtimeEnvSliceMap(dag.Env)["TARGET_TABLE"])

	persisted := dag.Clone()
	persisted.Env = nil
	persisted.Params = nil
	result, err := transport.Resolve(context.Background(), persisted, []string{"ENVIRONMENT=prod"}, transport.Options{})
	require.NoError(t, err)
	require.Equal(t, "prod", runtimeEnvSliceMap(result.Env)["TARGET_TABLE"])
}

func TestResolveRuntimeEnvPreservesMissingDotenvParamReference(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	workDir := filepath.Join(root, "zscores")
	require.NoError(t, os.MkdirAll(workDir, 0o750))

	yamlData := fmt.Appendf(nil, `
name: calculate_zscores
working_dir: %q
params:
  - name: ENVIRONMENT
    type: string
dotenv:
  - ".env.${params.ENVIRONMENT}"
steps:
  - name: assert_variables_defined
    run: echo "${TARGET_TABLE}"
`, workDir)
	dag, err := spec.LoadYAML(context.Background(), yamlData)
	require.NoError(t, err)
	resolveDAGRuntimeEnv(t, dag)
	require.Empty(t, dag.BuildErrors)
	require.Empty(t, dag.Env)

	persisted, err := spec.LoadYAML(context.Background(), yamlData)
	require.NoError(t, err)
	persisted.Env = nil
	persisted.EnvEvaluated = false
	result, err := transport.Resolve(context.Background(), persisted, nil, transport.Options{})
	require.NoError(t, err)
	require.Empty(t, result.Env)
}

func TestResolveRuntimeEnvDoesNotMutateDAGBackingSlices(t *testing.T) {
	t.Parallel()

	t.Run("env", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, ".env"), []byte("DOTENV_VALUE=ready\n"), 0o600))

		dag, err := spec.LoadYAML(context.Background(), fmt.Appendf(nil, `
working_dir: %s
dotenv: .env
steps:
  - run: echo hello
`, root), spec.WithoutEval())
		require.NoError(t, err)

		dag.Env = make([]string, 0, 1)

		result, err := transport.Resolve(context.Background(), dag, nil, transport.Options{})
		require.NoError(t, err)
		require.Contains(t, result.Env, "DOTENV_VALUE=ready")
		require.Empty(t, dag.Env)
		require.Empty(t, dag.Env[:cap(dag.Env)][0])
	})

	t.Run("build warnings", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, ".env"), []byte("INVALID LINE\n"), 0o600))

		dag, err := spec.LoadYAML(context.Background(), fmt.Appendf(nil, `
working_dir: %s
dotenv: .env
steps:
  - run: echo hello
`, root), spec.WithoutEval())
		require.NoError(t, err)

		dag.BuildWarnings = make([]string, 1, 2)
		dag.BuildWarnings[0] = "existing warning"

		result, err := transport.Resolve(context.Background(), dag, nil, transport.Options{})
		require.NoError(t, err)
		require.Len(t, result.Warnings, 1)
		require.Len(t, dag.BuildWarnings, 1)
		require.Empty(t, dag.BuildWarnings[:cap(dag.BuildWarnings)][1])
	})
}

func TestResolveRuntimeEnvReloadsNoEvalMetadataEnvFromSource(t *testing.T) {
	ctx := context.Background()
	t.Setenv("ISSUE_2268_TOKEN", "secret123")

	dagPath := filepath.Join(t.TempDir(), "issue2268.yaml")
	require.NoError(t, os.WriteFile(dagPath, []byte(`
name: issue2268
schedule:
  - "*/5 * * * *"
env:
  - TOKEN: ${ISSUE_2268_TOKEN}
steps:
  - name: check
    run: echo "$TOKEN"
`), 0o600))

	metadata, err := spec.Load(
		ctx,
		dagPath,
		spec.OnlyMetadata(),
		spec.WithoutEval(),
		spec.SkipSchemaValidation(),
	)
	require.NoError(t, err)
	require.Equal(t, "${ISSUE_2268_TOKEN}", runtimeEnvSliceMap(metadata.Env)["TOKEN"])

	result, err := transport.Resolve(ctx, metadata, nil, transport.Options{})
	require.NoError(t, err)
	require.Equal(t, "secret123", runtimeEnvSliceMap(result.Env)["TOKEN"])
	require.Equal(t, "${ISSUE_2268_TOKEN}", runtimeEnvSliceMap(metadata.Env)["TOKEN"])
}

func TestResolveRuntimeEnvReloadsNoEvalMetadataEnvFromSourceFile(t *testing.T) {
	ctx := context.Background()
	t.Setenv("ISSUE_2268_TOKEN", "secret123")

	dagPath := filepath.Join(t.TempDir(), "issue2268.yaml")
	require.NoError(t, os.WriteFile(dagPath, []byte(`
name: issue2268
schedule:
  - "*/5 * * * *"
env:
  - TOKEN: ${ISSUE_2268_TOKEN}
steps:
  - name: check
    run: echo "$TOKEN"
`), 0o600))

	metadata, err := spec.Load(
		ctx,
		dagPath,
		spec.OnlyMetadata(),
		spec.WithoutEval(),
		spec.SkipSchemaValidation(),
	)
	require.NoError(t, err)
	require.NotEmpty(t, metadata.SourceFile)
	require.Equal(t, "${ISSUE_2268_TOKEN}", runtimeEnvSliceMap(metadata.Env)["TOKEN"])

	metadata.Location = ""
	metadata.YamlData = nil

	result, err := transport.Resolve(ctx, metadata, nil, transport.Options{})
	require.NoError(t, err)
	require.Equal(t, "secret123", runtimeEnvSliceMap(result.Env)["TOKEN"])
	require.Equal(t, "${ISSUE_2268_TOKEN}", runtimeEnvSliceMap(metadata.Env)["TOKEN"])
}

func TestLoadWithoutEvalDoesNotCaptureRawEnvAsPresolvedBuildEnv(t *testing.T) {
	dag, err := spec.LoadYAML(context.Background(), []byte(`
name: raw-metadata-env
env:
  - TOKEN: ${ISSUE_2268_TOKEN}
steps:
  - name: check
    run: echo "$TOKEN"
`), spec.OnlyMetadata(), spec.WithoutEval())
	require.NoError(t, err)

	require.Equal(t, "${ISSUE_2268_TOKEN}", runtimeEnvSliceMap(dag.Env)["TOKEN"])
	require.Empty(t, dag.PresolvedBuildEnv)
}

func TestResolveRuntimeEnvReusesResolvedSourceEnv(t *testing.T) {
	ctx := context.Background()
	t.Setenv("ISSUE_2268_TOKEN", "old-value")

	dag, err := spec.LoadYAML(ctx, []byte(`
name: evaluated-source-env
env:
  - TOKEN: ${ISSUE_2268_TOKEN}
steps:
  - name: check
    run: echo "$TOKEN"
`))
	require.NoError(t, err)
	require.Equal(t, "old-value", runtimeEnvSliceMap(dag.Env)["TOKEN"])

	t.Setenv("ISSUE_2268_TOKEN", "new-value")

	result, err := transport.Resolve(ctx, dag, nil, transport.Options{})
	require.NoError(t, err)
	require.Equal(t, "old-value", runtimeEnvSliceMap(result.Env)["TOKEN"])
}

func TestResolveRuntimeEnvReusesResolvedEmptySourceEnv(t *testing.T) {
	dag := &ir.DAG{
		Name:            "evaluated-empty-source-env",
		Env:             []string{},
		EnvEvaluated:    true,
		RuntimeResolved: true,
		YamlData:        []byte("invalid: ["),
	}

	result, err := transport.Resolve(context.Background(), dag, nil, transport.Options{})
	require.NoError(t, err)
	require.Empty(t, result.Env)
}

func TestResolveRuntimeEnvKeepsProgrammaticEnvWithoutSource(t *testing.T) {
	dag := &ir.DAG{
		Name: "programmatic-env",
		Env:  []string{"TOKEN=${ISSUE_2268_TOKEN}"},
	}

	result, err := transport.Resolve(context.Background(), dag, nil, transport.Options{})
	require.NoError(t, err)
	require.Equal(t, "${ISSUE_2268_TOKEN}", runtimeEnvSliceMap(result.Env)["TOKEN"])
}

func resolveDAGRuntimeEnv(t *testing.T, dag *ir.DAG) runtimeenv.Result {
	t.Helper()
	result, err := runtimeenv.Resolve(context.Background(), dag)
	require.NoError(t, err)
	dag.Env = result.Env
	dag.RuntimeResolved = true
	return result
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
