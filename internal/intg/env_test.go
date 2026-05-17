// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/core"
	exec1 "github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnv(t *testing.T) {
	t.Parallel()

	t.Run("EnvVariables", func(t *testing.T) {
		th := test.Setup(t)
		dag := th.DAG(t, `
env:
  - TEST_ENV_1: "env_value_1"

params:
  - TEST_PARAM_1: my_param

steps:
  - run: echo "${TEST_PARAM_1}"
    output: PARAM_OUTPUT
  - env:
      - STEP_ENV_1: "${TEST_PARAM_1}_step1"
    run: echo "${STEP_ENV_1}"
    output: STEP1_OUTPUT
  - env:
      - STEP_ENV_1: "${TEST_ENV_1:0:3}_step2"
    run: echo "${STEP_ENV_1}"
    output: STEP2_OUTPUT
`)
		agent := dag.Agent()

		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"PARAM_OUTPUT": "my_param",
			"STEP1_OUTPUT": "my_param_step1",
			"STEP2_OUTPUT": "env_step2",
		})
	})

	t.Run("Derivatives", func(t *testing.T) {
		th := test.Setup(t)
		dag := th.DAG(t, `
params:
  - UID: HBL01_22OCT2025_0536

steps:
  - name: step1
    run: echo $SEN
    env:
       - SEN: ${UID:0:5}
    output: STEP1_OUTPUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"STEP1_OUTPUT": "HBL01",
		})
	})

	t.Run("ShellFallbacks", func(t *testing.T) {
		th := test.Setup(t)
		fallbackCommand := `echo "${UNSET_OPTIONAL:-default_value}"`
		if runtime.GOOS == "windows" {
			fallbackCommand = `
if ([string]::IsNullOrEmpty($env:UNSET_OPTIONAL)) {
  Write-Output "default_value"
} else {
  Write-Output $env:UNSET_OPTIONAL
}`
		}
		dag := th.DAG(t, `
steps:
  - name: default-env
    run: |
`+indentTestScript(fallbackCommand, 6)+`
    output: FALLBACK_OUTPUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"FALLBACK_OUTPUT": "default_value",
		})
	})

	t.Run("DAGRunWorkDir", func(t *testing.T) {
		t.Parallel()

		writeCommand := `echo "hello" > "${DAG_RUN_WORK_DIR}/test.txt"`
		readCommand := `cat "${DAG_RUN_WORK_DIR}/test.txt"`
		if runtime.GOOS == "windows" {
			writeCommand = `Set-Content -Path "${DAG_RUN_WORK_DIR}/test.txt" -Value "hello" -NoNewline`
			readCommand = `Get-Content -Raw -Path "${DAG_RUN_WORK_DIR}/test.txt"`
		}

		th := test.Setup(t)
		dag := th.DAG(t, `
steps:
  - name: write-to-workdir
    run: |
`+indentTestScript(writeCommand, 6)+`
  - name: read-from-workdir
    depends: write-to-workdir
    run: |
`+indentTestScript(readCommand, 6)+`
    output: WORKDIR_OUTPUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"WORKDIR_OUTPUT": "hello",
		})
	})

	t.Run("DAGRunWorkDirWithExplicitWorkingDir", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		explicitDir := mustTempDirWithRetryCleanup(t)
		if resolved, err := filepath.EvalSymlinks(explicitDir); err == nil {
			explicitDir = resolved
		}
		explicitDirForYAML := filepath.ToSlash(explicitDir)
		pwdCommand := test.ForOS("pwd", "(Get-Location).Path")
		writeCommand := `echo "from-workdir" > "${DAG_RUN_WORK_DIR}/data.txt"`
		readCommand := `cat "${DAG_RUN_WORK_DIR}/data.txt"`
		if runtime.GOOS == "windows" {
			writeCommand = `Set-Content -Path "${DAG_RUN_WORK_DIR}/data.txt" -Value "from-workdir" -NoNewline`
			readCommand = `Get-Content -Raw -Path "${DAG_RUN_WORK_DIR}/data.txt"`
		}
		dag := th.DAG(t, `
working_dir: `+explicitDirForYAML+`
steps:
  - name: check-pwd
    run: |
`+indentTestScript(pwdCommand, 6)+`
    output: PWD_OUTPUT
  - name: write-to-workdir
    run: |
`+indentTestScript(writeCommand, 6)+`
  - name: read-from-workdir
    depends: write-to-workdir
    run: |
`+indentTestScript(readCommand, 6)+`
    output: WORKDIR_OUTPUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"WORKDIR_OUTPUT": "from-workdir",
		})

		outputs := dag.ReadOutputs(t)
		require.Contains(t, outputs, "pwdOutput")
		require.Equal(t, canonicalTestPath(explicitDir), canonicalTestPath(outputs["pwdOutput"]))
	})

	t.Run("EnvReferencesParams", func(t *testing.T) {
		th := test.Setup(t)
		dag := th.DAG(t, `
params:
  data_dir: /tmp/foo
env:
  - FULL_PATH: "${data_dir}/output"
steps:
  - run: echo "${FULL_PATH}"
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": "/tmp/foo/output",
		})
	})

	t.Run("EnvReferencesParamsChained", func(t *testing.T) {
		th := test.Setup(t)
		dag := th.DAG(t, `
params:
  base: /data
env:
  - DIR: "${base}/subdir"
  - FULL: "${DIR}/file.txt"
steps:
  - run: echo "${FULL}"
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": "/data/subdir/file.txt",
		})
	})

	t.Run("StepOutputSubstrings", func(t *testing.T) {
		th := test.Setup(t)
		validateScript := `
if [ "${producer.stdout:0:5}${producer.stdout:5}" = "${producer.stdout}" ]; then
  echo OK
else
  echo FAIL
  exit 1
fi
`
		if runtime.GOOS == "windows" {
			validateScript = `
if (("${producer.stdout:0:5}" + "${producer.stdout:5}") -eq "${producer.stdout}") {
  Write-Output "OK"
} else {
  Write-Output "FAIL"
  exit 1
}
`
		}
		dag := th.DAG(t, `
type: graph
steps:
  - id: producer
    name: producer
    run: echo "HBL01_22OCT2025_0536"
    output: PRODUCER_OUTPUT
  - id: substring_validate
    name: substring-validate
    depends: producer
    run: |
`+indentTestScript(validateScript, 6)+`
    output: SUBSTRING_VALIDATION
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"PRODUCER_OUTPUT":      "HBL01_22OCT2025_0536",
			"SUBSTRING_VALIDATION": "OK",
		})
	})
}

func mustTempDirWithRetryCleanup(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "dagu-explicit-working-dir-*")
	require.NoError(t, err)

	waitFor := 2 * time.Second
	if runtime.GOOS == "windows" {
		waitFor = 10 * time.Second
	}

	t.Cleanup(func() {
		var removeErr error
		deadline := time.Now().Add(waitFor)
		for {
			removeErr = os.RemoveAll(dir)
			if removeErr == nil {
				return
			}
			if time.Now().After(deadline) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if runtime.GOOS == "windows" {
			t.Logf("explicit working dir cleanup is still pending on Windows: %v", removeErr)
			return
		}
		require.NoError(t, removeErr, "explicit working dir should be removable")
	})

	return dir
}

func TestSubDAGParamsReferencedInChildEnv(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	dagFile := th.CreateDAGFile(t, "subdag-env-from-params.yaml", `
name: subdag-env-parent
steps:
  - name: invoke-child
    action: dag.run
    with:
      dag: subdag-env-child
      params: "data_dir=/mnt/data"

---
name: subdag-env-child
params:
  - name: data_dir
    type: string
    required: true
env:
  - OUTPUT_PATH: "${data_dir}/results"
steps:
  - name: check-env
    command: echo "${OUTPUT_PATH}"
    output: RESULT
`)

	runID := uuid.Must(uuid.NewV7()).String()
	th.RunCommand(t, cmd.Start(), test.CmdTest{
		Args:        []string{"start", "--run-id", runID, dagFile},
		ExpectedOut: []string{"DAG run finished"},
	})

	rootRef := exec1.NewDAGRunRef("subdag-env-parent", runID)
	parentAttempt, err := th.DAGRunStore.FindAttempt(th.Context, rootRef)
	require.NoError(t, err)

	parentStatus, err := parentAttempt.ReadStatus(th.Context)
	require.NoError(t, err)
	require.Equal(t, core.Succeeded, parentStatus.Status)
	require.Len(t, parentStatus.Nodes, 1)
	require.Len(t, parentStatus.Nodes[0].SubRuns, 1)

	subRunID := parentStatus.Nodes[0].SubRuns[0].DAGRunID
	subStatus, subOutputs := readSubAttemptStatusAndOutputs(t, th, rootRef, subRunID)
	require.Equal(t, core.Succeeded, subStatus.Status)

	require.Contains(t, subOutputs.Outputs, "result")
	assert.Equal(t, "/mnt/data/results", subOutputs.Outputs["result"])
}
