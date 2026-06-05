// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cmdpkg "github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/require"
)

func TestRestoreDAGFromStatusLoadsDotenvWithPersistedParamsList(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	workDir := filepath.Join(root, "zscores")
	require.NoError(t, os.MkdirAll(workDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, ".env.foo"), []byte("TARGET_TABLE=foo\n"), 0o600))

	yamlData := []byte(`
name: calculate_zscores
working_dir: zscores
params:
  - name: COL
    type: string
    required: true
dotenv:
  - ".env.${COL}"
steps:
  - name: assert_variables_defined
    run: echo "${TARGET_TABLE}"
`)
	dag := &core.DAG{
		Name:       "calculate_zscores",
		Location:   filepath.Join(root, "calculate_zscores.yaml"),
		WorkingDir: workDir,
		Dotenv:     []string{".env.${COL}"},
		YamlData:   yamlData,
		ParamDefs: []core.ParamDef{
			{Name: "COL", Type: core.ParamDefTypeString, Required: true},
		},
	}
	status := &exec.DAGRunStatus{ParamsList: []string{"COL=foo"}}

	restored, err := cmdpkg.RestoreDAGFromStatusForTest(context.Background(), dag, status)
	require.NoError(t, err)

	require.Contains(t, restored.Params, "COL=foo")
	require.Equal(t, "foo", envValue(restored.Env, "TARGET_TABLE"))
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if after, ok := strings.CutPrefix(entry, prefix); ok {
			return after
		}
	}
	return ""
}
