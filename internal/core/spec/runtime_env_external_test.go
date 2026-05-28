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
