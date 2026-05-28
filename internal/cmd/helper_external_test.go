// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dagucmd "github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/require"
)

func TestRestoreDAGFromStatusIncludesDotenvFromResolvedWorkingDir(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	workDir := filepath.Join(root, "work", "quant-signal")
	require.NoError(t, os.MkdirAll(workDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, ".env"), []byte("PYTHON_BIN=/usr/local/bin/python\nPROJECT_DIR=/work/quant-signal\n"), 0o600))

	dag := &core.DAG{
		Name:               "signal",
		Location:           filepath.Join(root, "dags", "signal.yaml"),
		WorkingDir:         "${QUANT_SIGNAL_DIR}",
		Dotenv:             []string{".env"},
		PresolvedBuildEnv:  map[string]string{"QUANT_SIGNAL_DIR": workDir},
		BaseConfigData:     []byte("env:\n  - QUANT_SIGNAL_DIR: ${QUANT_SIGNAL_DIR}\n"),
		WorkingDirExplicit: true,
		YamlData: []byte(`
working_dir: ${QUANT_SIGNAL_DIR}
steps:
  - name: run_signals
    run: ${PYTHON_BIN} ${PROJECT_DIR}/signals/run_signals.py
`),
	}
	status := &exec.DAGRunStatus{}

	restored, err := dagucmd.RestoreDAGFromStatusForTest(context.Background(), dag, status)
	require.NoError(t, err)

	envMap := envSliceMap(restored.Env)
	require.Equal(t, workDir, envMap["QUANT_SIGNAL_DIR"])
	require.Equal(t, "/usr/local/bin/python", envMap["PYTHON_BIN"])
	require.Equal(t, "/work/quant-signal", envMap["PROJECT_DIR"])
}

func envSliceMap(envs []string) map[string]string {
	envMap := make(map[string]string)
	for _, env := range envs {
		key, value, ok := strings.Cut(env, "=")
		if ok {
			envMap[key] = value
		}
	}
	return envMap
}
