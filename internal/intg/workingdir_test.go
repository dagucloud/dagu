// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWorkingDirectoryResolution verifies working directory resolution:
//  1. DAG-level working_dir sets the working directory for steps
//  2. Step-level relative dir resolves against DAG's working_dir
//  3. SubDAG with explicit working_dir uses its own context
//  4. SubDAG without working_dir uses its own DAG-run work directory
func TestWorkingDirectoryResolution(t *testing.T) {
	th := test.Setup(t)

	// Create test directories
	dagsDir := th.Config.Paths.DAGsDir
	parentDir := filepath.Join(dagsDir, "parent_scripts")
	childDir := filepath.Join(dagsDir, "child_scripts")
	require.NoError(t, os.MkdirAll(parentDir, 0755))
	require.NoError(t, os.MkdirAll(childDir, 0755))

	// Platform-specific configuration
	shell, pwdCmd := "bash", "pwd"
	if runtime.GOOS == "windows" {
		shell, pwdCmd = "powershell", "(Get-Location).Path"
	}

	dag := th.DAG(t, `
shell: `+shell+`
working_dir: `+parentDir+`
steps:
  - name: parent_pwd
    run: `+pwdCmd+`
    output: PARENT_DIR

  - name: parent_relative_step
    working_dir: ../child_scripts
    run: `+pwdCmd+`
    output: PARENT_STEP_DIR

  - name: call_child_with_wd
    action: dag.run
    with:
      dag: child_with_wd

  - name: call_child_no_wd
    action: dag.run
    with:
      dag: child_no_wd

---

name: child_with_wd
shell: `+shell+`
working_dir: `+childDir+`
steps:
  - name: child_pwd
    run: `+pwdCmd+`

---

name: child_no_wd
shell: `+shell+`
steps:
  - name: child_pwd
    run: `+pwdCmd+`
`)

	dag.Agent().RunSuccess(t)

	// Verify parent DAG outputs
	dag.AssertOutputs(t, map[string]any{
		"PARENT_DIR":      test.Contains(parentDir),
		"PARENT_STEP_DIR": test.Contains(childDir),
	})

	// Verify subDAG working directories
	status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.Equal(t, core.Succeeded, status.Status)

	ref := exec.NewDAGRunRef(status.Name, status.DAGRunID)

	for _, node := range status.Nodes {
		if len(node.SubRuns) == 0 {
			continue
		}

		subAttempt, err := th.DAGRunStore.FindSubAttempt(th.Context, ref, node.SubRuns[0].DAGRunID)
		require.NoError(t, err)
		subDir := getSubDAGWorkingDir(t, th.Context, subAttempt)

		switch node.Step.Name {
		case "call_child_with_wd":
			assertSameWorkingDir(t, childDir, subDir,
				"SubDAG with explicit workingDir should run in childDir")
		case "call_child_no_wd":
			subWorkDir := subAttempt.WorkDir()
			assertSameWorkingDir(t, subWorkDir, subDir,
				"SubDAG without workingDir should run in its own DAG-run work directory")
		}
	}
}

// getSubDAGWorkingDir retrieves the working directory from a subDAG's stdout log.
func getSubDAGWorkingDir(t *testing.T, ctx context.Context, subAttempt exec.DAGRunAttempt) string {
	t.Helper()

	subStatus, err := subAttempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, subStatus.Nodes)

	logContent, err := os.ReadFile(subStatus.Nodes[0].Stdout)
	require.NoError(t, err)

	return strings.TrimSpace(string(logContent))
}

func assertSameWorkingDir(t *testing.T, expected, actual, message string) {
	t.Helper()

	expected = filepath.Clean(expected)
	actual = filepath.Clean(actual)
	if runtime.GOOS != "windows" {
		assert.Equal(t, expected, actual, message)
		return
	}

	expectedInfo, err := os.Stat(expected)
	require.NoError(t, err)
	actualInfo, err := os.Stat(actual)
	require.NoError(t, err)
	assert.Truef(t, os.SameFile(expectedInfo, actualInfo),
		"%s\nexpected: %s\nactual: %s", message, expected, actual)
}
