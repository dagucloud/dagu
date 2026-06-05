// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/proto/convert"
	"github.com/dagucloud/dagu/internal/service/worker"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/require"
)

func TestRemoteRetryLoadDAGUsesPreviousStatusParamsList(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	workDir := filepath.Join(root, "zscores")
	require.NoError(t, os.MkdirAll(workDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, ".env.foo"), []byte("TARGET_TABLE=foo\n"), 0o600))

	definition := fmt.Sprintf(`
name: calculate_zscores
working_dir: %q
params:
  - name: COL
    type: string
    required: true
  - name: MESSAGE
    type: string
    required: true
dotenv:
  - ".env.${COL}"
steps:
  - name: assert_variables_defined
    run: echo "${TARGET_TABLE}"
`, workDir)
	previousStatus, err := convert.DAGRunStatusToProto(&exec.DAGRunStatus{
		Name:       "calculate_zscores",
		DAGRunID:   "run-1",
		ParamsList: []string{"COL=foo", "MESSAGE=hello world"},
	})
	require.NoError(t, err)

	task := &coordinatorv1.Task{
		Operation:      coordinatorv1.Operation_OPERATION_RETRY,
		Target:         "calculate_zscores",
		Definition:     definition,
		DagRunId:       "run-1",
		PreviousStatus: previousStatus,
	}
	dag, cleanup, err := worker.LoadRemoteTaskDAGForTest(context.Background(), &config.Config{}, task)
	require.NoError(t, err)
	if cleanup != nil {
		defer cleanup()
	}

	require.Contains(t, dag.Params, "COL=foo")
	require.Contains(t, dag.Params, "MESSAGE=hello world")
	dag.LoadDotEnv(context.Background())
	require.Equal(t, "foo", workerTestEnvValue(dag.Env, "TARGET_TABLE"))
}

func TestRemoteRetryLoadDAGCleansTempFileWhenPreviousStatusInvalid(t *testing.T) {
	target := "cleanup-retry-params"
	pattern := filepath.Join(os.TempDir(), "dagu", "worker-dags", target+"-*.yaml")
	before, err := filepath.Glob(pattern)
	require.NoError(t, err)

	task := &coordinatorv1.Task{
		Operation: coordinatorv1.Operation_OPERATION_RETRY,
		Target:    target,
		Definition: `
name: cleanup-retry-params
steps:
  - name: noop
    run: echo noop
`,
		DagRunId:       "run-1",
		PreviousStatus: &coordinatorv1.DAGRunStatusProto{JsonData: "{"},
	}

	dag, cleanup, err := worker.LoadRemoteTaskDAGForTest(context.Background(), &config.Config{}, task)
	require.Error(t, err)
	require.Nil(t, dag)
	require.Nil(t, cleanup)

	after, err := filepath.Glob(pattern)
	require.NoError(t, err)
	require.ElementsMatch(t, before, after)
}

func workerTestEnvValue(env []string, key string) string {
	for _, entry := range env {
		k, value, ok := strings.Cut(entry, "=")
		if ok && k == key {
			return value
		}
	}
	return ""
}
