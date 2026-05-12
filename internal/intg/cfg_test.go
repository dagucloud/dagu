// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

func TestDAGExecution(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)

	t.Run("NoName", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `steps:
  - command: echo 1
    output: NO_NAME_STEP_OUT
  - command: echo ${NO_NAME_STEP_OUT}=1
    output: NO_NAME_STEP_OUT2
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"NO_NAME_STEP_OUT2": "1=1",
		})
	})
	t.Run("Depends", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `type: graph
steps:
  - name: "1"
    command: echo 1
  - name: "2"
    depends: "1"
    command: echo 2
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
	})

	t.Run("Pipe", func(t *testing.T) {
		t.Parallel()

		pipeCommand := test.LabeledExpandedOutput("hello ", "$NAME")
		dag := th.DAG(t, `params:
  - NAME: "foo"
steps:
  - command: |
`+indentTestScript(pipeCommand, 6)+`
    output: OUT1
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT1": "hello foo",
		})
	})

	t.Run("DotEnv", func(t *testing.T) {
		t.Parallel()

		// dotenv1 contains: ENV1=123, ENV2=456
		// dotenv2 contains: ENV2=abc
		// All dotenv files are loaded in order, later files override earlier ones
		dotenv1Path := test.TestdataPath(t, "integration/dotenv1")
		dotenv2Path := test.TestdataPath(t, "integration/dotenv2")

		dag := th.DAG(t, `dotenv:
  - `+dotenv1Path+`
  - `+dotenv2Path+`
steps:
  - command: echo "${ENV1} ${ENV2}"
    output: OUT1
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		// ENV1=123 from dotenv1, ENV2=abc from dotenv2 (overrides dotenv1's ENV2=456)
		dag.AssertOutputs(t, map[string]any{
			"OUT1": "123 abc",
		})
	})

	t.Run("CommandErrorIncludesLastStderrLine", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `steps:
  - command: bash
    script: |
      echo first 1>&2
      echo second 1>&2
      exit 7
`)
		agent := dag.Agent()
		err := agent.Run(agent.Context)
		require.Error(t, err)
		// Should contain the last stderr line
		require.Contains(t, err.Error(), "second")
	})

	t.Run("StepTimeout", func(t *testing.T) {
		t.Parallel()
		dag := th.DAG(t, `type: graph
steps:
  - name: slow
    command: sleep 2
    timeout_sec: 1
  - name: after
    depends: slow
    command: echo after
`)
		agent := dag.Agent()
		err := agent.Run(agent.Context)
		require.Error(t, err)
		require.Contains(t, err.Error(), "step timed out")
		dag.AssertLatestStatus(t, core.Failed)
	})

	t.Run("NamedParams", func(t *testing.T) {
		t.Parallel()

		nameCommand := test.ExpandedOutput("$NAME")
		greetingCommand := test.LabeledExpandedOutput("Hello, ", "$NAME")
		dag := th.DAG(t, `type: graph
params:
  NAME: "Dagu"
  AGE: 30

steps:
  - name: Hello
    command: |
`+indentTestScript(nameCommand, 6)+`
    output: OUT1
  - name: Name
    command: |
`+indentTestScript(greetingCommand, 6)+`
    depends: Hello
    output: OUT2
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT1": "Dagu",
			"OUT2": "Hello, Dagu",
		})
	})

	t.Run("NamedParamsList", func(t *testing.T) {
		t.Parallel()

		nameCommand := test.ExpandedOutput("$NAME")
		greetingCommand := test.LabeledExpandedOutput("Hello, ", "$NAME")
		dag := th.DAG(t, `params:
  - NAME: "Dagu"
  - AGE: 30

steps:
  - command: |
`+indentTestScript(nameCommand, 6)+`
    output: OUT1
  - command: |
`+indentTestScript(greetingCommand, 6)+`
    output: OUT2
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT1": "Dagu",
			"OUT2": "Hello, Dagu",
		})
	})

	t.Run("PositionalParams", func(t *testing.T) {
		t.Parallel()

		positionalCommand := `echo '$1' is $1, '$2' is $2`
		if runtime.GOOS == "windows" {
			positionalCommand = `Write-Output ("$1 is {0}, $2 is {1}" -f "${1}", "${2}")`
		}

		dag := th.DAG(t, `params: "foo bar"

steps:
  - output: OUT1
    command: |
`+indentTestScript(positionalCommand, 6)+`
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		expectedPositional := []test.Contains{
			"$1 is foo",
			"$2 is bar",
		}
		if runtime.GOOS == "windows" {
			expectedPositional = []test.Contains{
				"foo is foo",
				"bar is bar",
			}
		}
		dag.AssertOutputs(t, map[string]any{
			"OUT1": expectedPositional,
		})
	})

	t.Run("PositionalParamsScript", func(t *testing.T) {
		t.Parallel()

		positionalScript := `echo '$1' is $1, '$2' is ${2}`
		if runtime.GOOS == "windows" {
			positionalScript = `Write-Output ("$1 is {0}, $2 is {1}" -f "${1}", "${2}")`
		}

		dag := th.DAG(t, `params: "foo bar"

steps:
  - output: OUT1
    script: |
`+indentTestScript(positionalScript, 6)+`
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		expectedPositional := []test.Contains{
			"$1 is foo",
			"$2 is bar",
		}
		if runtime.GOOS == "windows" {
			expectedPositional = []test.Contains{
				"foo is foo",
				"bar is bar",
			}
		}
		dag.AssertOutputs(t, map[string]any{
			"OUT1": expectedPositional,
		})
	})

	t.Run("Script", func(t *testing.T) {
		t.Parallel()

		scriptCommand := `echo 1 2 3`
		if runtime.GOOS == "windows" {
			scriptCommand = `Write-Output "1 2 3"`
		}

		dag := th.DAG(t, `params:
  - NAME: "foo"
steps:
  - script: |
`+indentTestScript(scriptCommand, 6)+`
    output: OUT1
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT1": "1 2 3",
		})
	})

	t.Run("RegexPrecondition", func(t *testing.T) {
		t.Parallel()

		out1Command := test.Output("abc run def")
		out2Command := test.Output("match")
		dag := th.DAG(t, `steps:
  - command: |
`+indentTestScript(out1Command, 6)+`
    output: OUT1
  - command: |
`+indentTestScript(out2Command, 6)+`
    output: OUT2
    preconditions:
      - condition: "$OUT1"
        expected: "re:^abc.*def$"
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT1": "abc run def",
			"OUT2": "match",
		})
	})

	t.Run("JSON", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `steps:
  - command: |
      echo '{"port": 8080, "host": "localhost"}'
    output: CONFIG

  - command: echo "Starting server at ${CONFIG.host}:${CONFIG.port}"
    output: OUT1
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT1": "Starting server at localhost:8080",
		})
	})

	t.Run("EnvVar", func(t *testing.T) {
		t.Parallel()

		dataPrefix := filepath.ToSlash(filepath.Join(t.TempDir(), "dagu_test_integration"))
		dag := th.DAG(t, fmt.Sprintf(`env:
  - DATA_DIR: %q
  - PROCESS_DATE: "`+"`"+`date '+%%Y%%m%%d_%%H%%M%%S'`+"`"+`"

steps:
  - command: echo foo
    stdout: "${DATA_DIR}_${PROCESS_DATE}"
  - command: cat ${DATA_DIR}_${PROCESS_DATE}
    output: OUT1
    preconditions:
      - condition: "${DATA_DIR}_${PROCESS_DATE}"
        expected: "re:[0-9]{8}_[0-9]{6}"
  - command: rm ${DATA_DIR}_${PROCESS_DATE}
`, dataPrefix))
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT1": "foo",
		})
	})

	t.Run("EnvScript", func(t *testing.T) {
		t.Parallel()

		envScript := test.JoinLines(
			test.LabeledExpandedOutput("E1 is ", "$E1"),
			test.LabeledExpandedOutput("E2 is ", "$E2"),
		)
		dag := th.DAG(t, `env:
  - "E1": foo
  - "E2": bar

steps:
  - output: OUT1
    script: |
`+indentTestScript(envScript, 6)+`
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT1": []test.Contains{
				"E1 is foo",
				"E2 is bar",
			},
		})
	})

	t.Run("SpecialVars", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `steps:
  - command: echo $DAG_RUN_LOG_FILE
    output: OUT1
  - command: echo $DAG_RUN_STEP_STDOUT_FILE
    output: OUT2
  - command: echo $DAG_RUN_STEP_NAME
    output: OUT3
  - command: sh
    output: OUT4
    script: |
      echo $DAG_NAME
  - command: bash
    output: OUT5
    script: |
      echo $DAG_RUN_ID
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT1": test.NotEmpty{},
			"OUT2": test.NotEmpty{},
			"OUT3": test.NotEmpty{},
			"OUT4": test.NotEmpty{},
			"OUT5": test.NotEmpty{},
		})
	})

	t.Run("StdoutAndStderrPathsExpandPrepareTimeVars", func(t *testing.T) {
		t.Parallel()

		tempDir := t.TempDir()
		stdoutPath := filepath.Join(tempDir, "dag_${DAG_RUN_STEP_NAME}.out")
		stderrPath := filepath.Join(tempDir, "dag_${LOG_SUFFIX}.err")
		stdoutPathForYAML := filepath.ToSlash(stdoutPath)
		stderrPathForYAML := filepath.ToSlash(stderrPath)
		secondCommand := test.JoinLines(
			test.Output("meh"),
			test.Stderr("oops"),
		)

		dag := th.DAG(t, `steps:
  - name: first
    command: echo $DAG_RUN_STEP_NAME
    output: FIRST_OUT

  - name: second
    env:
      - LOG_SUFFIX=custom-error
    stdout: "`+stdoutPathForYAML+`"
    stderr: "`+stderrPathForYAML+`"
    command: |
`+indentTestScript(secondCommand, 6)+`
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		status := agent.Status(th.Context)
		require.Len(t, status.Nodes, 2)

		firstOutput, ok := status.Nodes[0].OutputVariables.Load("FIRST_OUT")
		require.True(t, ok)
		require.Equal(t, "FIRST_OUT=first", firstOutput)
		require.Equal(t, canonicalTestPath(filepath.Join(tempDir, "dag_second.out")), canonicalTestPath(status.Nodes[1].Step.Stdout))
		require.Equal(t, canonicalTestPath(filepath.Join(tempDir, "dag_custom-error.err")), canonicalTestPath(status.Nodes[1].Step.Stderr))

		stdoutContent, err := os.ReadFile(status.Nodes[1].Step.Stdout)
		require.NoError(t, err)
		require.Contains(t, string(stdoutContent), "meh")

		stderrContent, err := os.ReadFile(status.Nodes[1].Step.Stderr)
		require.NoError(t, err)
		require.Contains(t, string(stderrContent), "oops")
	})

	t.Run("JQ", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `steps:
  - type: jq
    command: .user.name # Get user name from JSON
    output: NAME
    script: |
      {
        "user": {
          "name": "John",
          "age": 30
        }
      }
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"NAME": `"John"`,
		})
	})

	t.Run("JSONVar", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `steps:
  - command: |
      echo '{"port": 8080, "host": "localhost"}'
    output: CONFIG

  - name: start_server
    command: echo "Starting server at ${CONFIG.host}:${CONFIG.port}"
    output: OUT1
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT1": "Starting server at localhost:8080",
		})
	})

	t.Run("PerlScript", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `env:
  - LC_ALL: C
  - LC_CTYPE: C
  - LANG: C
steps:
  - command: perl
    script: |
      use strict;
      use warnings;
      print("Hello World\n");
    output: OUT1
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT1": "Hello World",
		})
	})

	t.Run("Shebang", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `env:
  - LC_ALL: C
  - LC_CTYPE: C
  - LANG: C
steps:
  - script: |
      #!env perl
      use strict;
      use warnings;
      print("Hello World\n");
    output: OUT1
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT1": "Hello World",
		})
	})

	t.Run("ShebangInCommand", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `env:
  - LC_ALL: C
  - LC_CTYPE: C
  - LANG: C
steps:
  - |
      #!env perl
      use strict;
      use warnings;
      print("Hello World\n");
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
	})

	t.Run("Workdir", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `env:
  - WORKDIR: $HOME
  - TILDE: ~/
steps:
  - working_dir: $TILDE
    command: echo $PWD
    output: OUT1

  - working_dir: $WORKDIR
    command: echo $PWD
    output: OUT2
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		outputs := dag.ReadOutputs(t)
		require.Equal(t, canonicalTestPath(os.ExpandEnv("$HOME")), canonicalTestPath(outputs["out1"]))
		require.Equal(t, canonicalTestPath(os.ExpandEnv("$HOME")), canonicalTestPath(outputs["out2"]))
	})

	t.Run("Issue810", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `type: graph
params: bar
steps:
  - name: step1
    command: echo start
    output: OUT1 # "start"
  - name: foo
    command: echo foo
    depends:
      - step1
    output: OUT2 # "foo"
    preconditions:
      - condition: $OUT1 # should be "start"
        expected: start
  - name: bar
    command: echo bar
    depends:
      - step1
    output: OUT3 # "bar"
    preconditions:
      - condition: "$1" # should be "bar"
        expected: bar
  - name: baz
    command: echo baz
    depends:
      - foo
      - bar
    output: OUT4 # "baz"
    preconditions:
      - condition: "$1" # should be "bar"
        expected: bar
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT1": "start",
			"OUT2": "foo",
			"OUT3": "bar",
			"OUT4": "baz",
		})
	})

	t.Run("ShellOptions", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `steps:
  - command: |
      echo 'hello world' && ls -al /
    shell: bash -o errexit -o xtrace -o pipefail -c
    output: OUT1
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT1": []test.Contains{
				"hello world",
			},
		})
	})

	t.Run("Issue1203ScriptWithCarriageReturn", func(t *testing.T) {
		t.Parallel()

		// Issue #1203: Scripts with trailing \r cause file path errors
		// Example: "can't open file '/path/to/file.py\r': [Errno 2] No such file or directory"
		tmpFile := th.TempFile(t, "script-trimming-issue", nil)
		tmpFileForShell := filepath.ToSlash(tmpFile)

		// Create a DAG with script containing \r - this should fail
		dag := th.DAG(t, "steps:\n"+
			"  - command: bash\n"+
			"    script: \"test -f "+tmpFileForShell+"\\r\"\n")

		agent := dag.Agent()

		// This should fail because bash tries to execute "test -f /etc/passwd\r"
		// The \r becomes part of the filename, so it looks for "/etc/passwd\r" which doesn't exist
		err := agent.Run(agent.Context)
		require.NoError(t, err)

		// The test should fail with the current implementation
		dag.AssertLatestStatus(t, core.Succeeded)
	})
}

func TestDotEnvUsesResolvedWorkingDirFromBaseEnv(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	workDir := filepath.Join(baseDir, "signals")
	require.NoError(t, os.MkdirAll(workDir, 0750))
	if resolved, err := filepath.EvalSymlinks(workDir); err == nil {
		workDir = resolved
	}
	workDirForYAML := filepath.ToSlash(workDir)

	require.NoError(t, os.WriteFile(filepath.Join(workDir, ".env"), []byte("PYTHON_BIN=/usr/local/bin/python\n"), 0600))

	baseConfigPath := filepath.Join(baseDir, "base.yaml")
	require.NoError(t, os.WriteFile(baseConfigPath, []byte(fmt.Sprintf(`env:
  - QUANT_SIGNAL_DIR: %q
`, workDirForYAML)), 0600))

	th := test.Setup(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Paths.BaseConfig = baseConfigPath
	}))

	require.NoError(t, os.MkdirAll(th.Config.Paths.DAGsDir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(th.Config.Paths.DAGsDir, ".env"), []byte("PYTHON_BIN=wrong-from-dag-dir\n"), 0600))

	pwdCommand := test.ForOS("pwd", "(Get-Location).Path")
	dag := th.DAG(t, `dotenv: .env
working_dir: ${QUANT_SIGNAL_DIR}
steps:
  - name: check-env
    command: |
`+indentTestScript(test.EnvOutput("QUANT_SIGNAL_DIR", "PYTHON_BIN"), 6)+`
    output: ENV_RESULT
  - name: check-pwd
    command: |
`+indentTestScript(pwdCommand, 6)+`
    output: PWD_RESULT
`)

	dag.Agent().RunSuccess(t)

	outputs := dag.ReadOutputs(t)
	require.Equal(t, workDirForYAML+"|/usr/local/bin/python", outputs["envResult"])
	require.Equal(t, canonicalTestPath(workDir), canonicalTestPath(outputs["pwdResult"]))
}

func TestCallSubDAG(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)

	// Use multi-document YAML to include both parent and sub DAG
	dagContent := `steps:
  - call: sub
    params: "SUB_P1=foo"
    output: OUT1
  - command: echo "${OUT1.outputs.OUT}"
    output: OUT2
---
name: sub
params:
  SUB_P1: xyz
steps:
  - name: step1
    command: echo $SUB_P1
    output: OUT
`
	dag := th.DAG(t, dagContent)
	agent := dag.Agent()
	agent.RunSuccess(t)
	dag.AssertOutputs(t, map[string]any{
		"OUT2": "foo",
	})
}

func TestNestedThreeLevelDAG(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)

	// Create the grandsub DAG as a separate file
	th.CreateDAGFile(t, th.Config.Paths.DAGsDir, "nested_grand_child", []byte(`
params:
  PARAM: VALUE
steps:
  - command: echo "value is ${PARAM}"
    output: OUTPUT
`))

	// Create parent and sub DAGs using multi-document YAML
	dagContent := `steps:
  - call: nested_child
    params: "PARAM=123"
    output: SUB_OUTPUT
  - command: echo "${SUB_OUTPUT.outputs.OUTPUT}"
    output: OUT1
---
name: nested_child
params:
  PARAM: VALUE
steps:
  - call: nested_grand_child
    params: "PARAM=${PARAM}"
    output: GRAND_SUB_OUTPUT
  - command: echo "${GRAND_SUB_OUTPUT.outputs.OUTPUT}"
    output: OUTPUT
`
	dag := th.DAG(t, dagContent)
	agent := dag.Agent()
	agent.RunSuccess(t)
	dag.AssertOutputs(t, map[string]any{
		"OUT1": "value is 123",
	})
}

// TestSkippedPreconditions verifies that steps with unmet preconditions are skipped.
func TestSkippedPreconditions(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	runCommand := test.Output("executed")
	skipCommand := test.Output("should not execute")
	executeCommand := test.Output("should execute")
	dag := th.DAG(t, fmt.Sprintf(`type: graph
steps:
  - name: run
    command: |
%s
    output: OUT_RUN
  - name: skipped
    depends:
      - run
    command: |
%s
    preconditions:
      - condition: "$OUT_RUN"
        expected: "nope"
    output: OUT_SKIP1
  - name: executed
    depends:
      - run
    command: |
%s
    preconditions:
      - condition: "$OUT_RUN"
        expected: "executed"
    output: OUT_SKIP2
`, indentTestScript(runCommand, 6), indentTestScript(skipCommand, 6), indentTestScript(executeCommand, 6)))
	agent := dag.Agent()
	agent.RunSuccess(t)
	dag.AssertOutputs(t, map[string]any{
		"OUT_RUN":   "executed",
		"OUT_SKIP1": "",
		"OUT_SKIP2": "should execute",
	})
}

// TestComplexDependencies verifies that a DAG with complex dependencies executes steps in the correct order.
func TestComplexDependencies(t *testing.T) {
	t.Parallel()

	// Setup the test helper with the integration DAGs directory.
	th := test.Setup(t)
	// Load the DAG from inline YAML.
	dag := th.DAG(t, `type: graph
steps:
  - name: start
    command: echo "start"
    output: START
  - name: branch1
    command: echo "branch1"
    depends: start
    output: BRANCH1
  - name: branch2
    command: echo "branch2"
    depends: start
    output: BRANCH2
  - name: merge
    command: echo "merge"
    depends:
      - branch1
      - branch2
    output: MERGE
  - name: final
    command: echo "final"
    depends: merge
    output: FINAL
`)
	agent := dag.Agent()
	agent.RunSuccess(t)
	dag.AssertOutputs(t, map[string]any{
		"START":   "start",
		"BRANCH1": "branch1",
		"BRANCH2": "branch2",
		"MERGE":   "merge",
		"FINAL":   "final",
	})
}

func TestStepLevelEnvVars(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)

	// Load chain DAG
	dag := th.DAG(t, `
env:
  - MY_VAR: "dag_value"
  - MY_VAR2: "dag_value2"

steps:
  - env:
      MY_VAR: "step1_value"
    command: `+test.ExpandedOutput("${MY_VAR}")+`
    output: OUT1

  - env:
      MY_VAR: $MY_VAR2
    command: `+test.ExpandedOutput("${MY_VAR}")+`
    output: OUT2

  - env:
      MY_VAR: "dynamic value"
    command: `+test.ExpandedOutput("${MY_VAR}")+`
    output: OUT3
`)

	// Run the DAG
	agent := dag.Agent()
	require.NoError(t, agent.Run(agent.Context))

	// Verify successful completion
	dag.AssertLatestStatus(t, core.Succeeded)

	// Assert the output contains the step-level environment variable
	dag.AssertOutputs(t, map[string]any{
		"OUT1": "step1_value",
		"OUT2": "dag_value2",
		"OUT3": "dynamic value",
	})
}

func TestStepWorkingDir(t *testing.T) {
	t.Parallel()

	// Create temp directories for testing
	tempDir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(tempDir); err == nil {
		tempDir = resolved
	}
	stepWorkDir := tempDir + "/step"

	// Create directories
	require.NoError(t, os.MkdirAll(stepWorkDir, 0755))

	th := test.Setup(t)

	// Test that step workingDir works
	dag := th.DAG(t, `
steps:
  - working_dir: `+stepWorkDir+`
    command: `+test.ForOS("pwd", "(Get-Location).Path")+`
    output: STEP_DIR
`)

	agent := dag.Agent()
	agent.RunSuccess(t)
	outputs := dag.ReadOutputs(t)
	require.Contains(t, outputs, "stepDir")
	require.Equal(t, canonicalTestPath(stepWorkDir), canonicalTestPath(outputs["stepDir"]))
}

// TestPreconditionNegate verifies that preconditions with negate:true work correctly.
// When negate is true, the step runs when the condition does NOT match.
func TestPreconditionNegate(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)

	t.Run("NegateSkipsWhenConditionMatches", func(t *testing.T) {
		t.Parallel()

		// When negate:true and condition matches expected, step should be skipped
		dag := th.DAG(t, `type: graph
env:
  - STATUS: success
steps:
  - command: echo "always runs"
    output: OUT1
  - command: echo "should skip"
    output: OUT2
    preconditions:
      - condition: "${STATUS}"
        expected: "success"
        negate: true
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT1": "always runs",
			"OUT2": "", // Should be empty because step was skipped
		})
	})

	t.Run("NegateRunsWhenConditionDoesNotMatch", func(t *testing.T) {
		t.Parallel()

		// When negate:true and condition does NOT match expected, step should run
		dag := th.DAG(t, `type: graph
env:
  - STATUS: failure
steps:
  - command: echo "always runs"
    output: OUT1
  - command: echo "should run"
    output: OUT2
    preconditions:
      - condition: "${STATUS}"
        expected: "success"
        negate: true
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT1": "always runs",
			"OUT2": "should run",
		})
	})

	t.Run("NegateWithCommandExitCode", func(t *testing.T) {
		t.Parallel()

		// When negate:true with a command, step runs when command fails (non-zero exit)
		dag := th.DAG(t, `type: graph
steps:
  - command: echo "should run"
    output: OUT1
    preconditions:
      - condition: "false"
        negate: true
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT1": "should run",
		})
	})

	t.Run("DAGLevelNegate", func(t *testing.T) {
		t.Parallel()

		// DAG-level precondition with negate - should run when condition doesn't match
		dag := th.DAG(t, `
env:
  - ENV_TYPE: development
preconditions:
  - condition: "${ENV_TYPE}"
    expected: "production"
    negate: true
steps:
  - command: echo "dag ran"
    output: OUT1
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT1": "dag ran",
		})
	})
}
