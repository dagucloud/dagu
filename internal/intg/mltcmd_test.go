// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestMultipleCommands_Shell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping Unix shell tests on Windows")
	}
	t.Parallel()

	th := test.Setup(t)

	t.Run("TwoCommands", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `
steps:
  - name: multi-cmd
    run:
      - echo hello
      - echo world
    output: OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		// Output is concatenated from all commands
		dag.AssertOutputs(t, map[string]any{
			"OUT": "hello\nworld",
		})
	})

	t.Run("ThreeCommandsWithArgs", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `
steps:
  - name: multi-cmd
    run:
      - echo "first command"
      - echo "second command"
      - echo "third command"
    output: OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		// Output is concatenated from all commands
		dag.AssertOutputs(t, map[string]any{
			"OUT": "first command\nsecond command\nthird command",
		})
	})

	t.Run("CommandsWithEnvVars", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `
env:
  - MY_VAR: hello
steps:
  - name: multi-cmd
    run:
      - echo $MY_VAR
      - echo "${MY_VAR} world"
    output: OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		// Output is concatenated from all commands
		dag.AssertOutputs(t, map[string]any{
			"OUT": "hello\nhello world",
		})
	})

	t.Run("FirstCommandFails", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `
steps:
  - name: multi-cmd
    run:
      - "false"
      - echo "should not run"
    output: OUT
`)
		agent := dag.Agent()
		agent.RunError(t)
		dag.AssertLatestStatus(t, core.Failed)
	})

	t.Run("SecondCommandFails", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `
steps:
  - name: multi-cmd
    run:
      - echo "first runs"
      - "false"
      - echo "should not run"
    output: OUT
`)
		agent := dag.Agent()
		agent.RunError(t)
		dag.AssertLatestStatus(t, core.Failed)
	})

	t.Run("CommandsWithPipes", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `
shell: /bin/bash
steps:
  - name: multi-cmd
    run:
      - echo "hello world" | tr 'h' 'H'
      - echo "foo bar" | tr 'f' 'F'
    output: OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		// Output is concatenated from all commands
		dag.AssertOutputs(t, map[string]any{
			"OUT": "Hello world\nFoo bar",
		})
	})

	t.Run("CommandsWithWorkingDir", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `
steps:
  - name: multi-cmd
    working_dir: /tmp
    run:
      - pwd
      - echo "done"
    output: OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		// Output is concatenated from all commands
		dag.AssertOutputs(t, map[string]any{
			"OUT": "/tmp\ndone",
		})
	})

	t.Run("DependsOnPreviousStep", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `
type: graph
steps:
  - name: step1
    run: echo "step1"
    output: STEP1_OUT
  - name: step2
    depends:
      - step1
    run:
      - echo "from step2"
      - echo "done"
    output: STEP2_OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		// Output is concatenated from all commands
		dag.AssertOutputs(t, map[string]any{
			"STEP1_OUT": "step1",
			"STEP2_OUT": "from step2\ndone",
		})
	})

	t.Run("CommandWithColonInArray", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `
steps:
  - name: colon-test
    run:
      - echo SATID: 123
    output: OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"OUT": "SATID: 123",
		})
	})
}

func TestMultipleCommands_Docker(t *testing.T) {
	requireDockerDaemon(t)
	t.Parallel()
	requireLinuxContainerRuntime(t)

	const testImage = "alpine:3"

	t.Run("TwoCommandsInContainer", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		// Use startup: command to keep container running for multiple commands
		dag := th.DAG(t, fmt.Sprintf(`
steps:
  - name: multi-cmd
    container:
      image: %s
      startup: command
      command: ["sh", "-c", "while true; do sleep 3600; done"]
    run:
      - echo hello
      - echo world
    output: OUT
`, testImage))
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		// Output is concatenated from all commands
		dag.AssertOutputs(t, map[string]any{
			"OUT": "hello\nworld",
		})
	})

	t.Run("CommandsWithEnvInContainer", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		// Use startup: command to keep container running for multiple commands
		dag := th.DAG(t, fmt.Sprintf(`
steps:
  - name: multi-cmd
    container:
      image: %s
      startup: command
      command: ["sh", "-c", "while true; do sleep 3600; done"]
      env:
        - MY_VAR=hello
    run:
      - printenv MY_VAR
      - echo "done"
    output: OUT
`, testImage))
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		// Output is concatenated from all commands
		dag.AssertOutputs(t, map[string]any{
			"OUT": "hello\ndone",
		})
	})

	t.Run("FirstCommandFailsInContainer", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		// Use startup: command to keep container running for multiple commands
		dag := th.DAG(t, fmt.Sprintf(`
steps:
  - name: multi-cmd
    container:
      image: %s
      startup: command
      command: ["sh", "-c", "while true; do sleep 3600; done"]
    run:
      - "false"
      - echo "should not run"
    output: OUT
`, testImage))
		agent := dag.Agent()
		agent.RunError(t)
		dag.AssertLatestStatus(t, core.Failed)
	})

	t.Run("SecondCommandFailsInContainer", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		// Use startup: command to keep container running for multiple commands
		dag := th.DAG(t, fmt.Sprintf(`
steps:
  - name: multi-cmd
    container:
      image: %s
      startup: command
      command: ["sh", "-c", "while true; do sleep 3600; done"]
    run:
      - echo "first runs"
      - "false"
      - echo "should not run"
    output: OUT
`, testImage))
		agent := dag.Agent()
		agent.RunError(t)
		dag.AssertLatestStatus(t, core.Failed)
	})

	t.Run("DAGLevelContainerWithMultipleCommands", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, fmt.Sprintf(`
container:
  image: %s
steps:
  - name: multi-cmd
    run:
      - echo hello
      - echo world
    output: OUT
`, testImage))
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		// Output is concatenated from all commands
		dag.AssertOutputs(t, map[string]any{
			"OUT": "hello\nworld",
		})
	})

	t.Run("MultipleStepsWithMultipleCommands", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, fmt.Sprintf(`
type: graph
container:
  image: %s
steps:
  - name: step1
    run:
      - echo "step1-cmd1"
      - echo "step1-cmd2"
    output: STEP1_OUT
  - name: step2
    depends:
      - step1
    run:
      - echo "step2-cmd1"
      - echo "step2-cmd2"
    output: STEP2_OUT
`, testImage))
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		// Output is concatenated from all commands
		dag.AssertOutputs(t, map[string]any{
			"STEP1_OUT": "step1-cmd1\nstep1-cmd2",
			"STEP2_OUT": "step2-cmd1\nstep2-cmd2",
		})
	})

	// Test step-level container without startup:command - uses default keepalive mode
	t.Run("StepContainerWithDefaultKeepalive", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		// No startup:command - should use default keepalive mode
		dag := th.DAG(t, fmt.Sprintf(`
steps:
  - name: multi-cmd
    container:
      image: %s
    run:
      - echo hello
      - echo world
    output: OUT
`, testImage))
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertLatestStatus(t, core.Succeeded)
		// Output is concatenated from all commands
		dag.AssertOutputs(t, map[string]any{
			"OUT": "hello\nworld",
		})
	})
}

func TestMultipleCommands_Validation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping Unix shell tests on Windows")
	}
	t.Parallel()

	th := test.Setup(t)

	t.Run("JQExecutorRejectsMultipleCommands", func(t *testing.T) {
		t.Parallel()

		// Create a temp file with the DAG content
		tempDir := t.TempDir()
		filename := fmt.Sprintf("%s.yaml", uuid.New().String())
		testFile := filepath.Join(tempDir, filename)
		yamlContent := `
steps:
  - name: jq-multi
    action: jq.filter
    with:
      filter:
        - ".foo"
        - ".bar"
      data: '{"foo": "bar"}'
`
		err := os.WriteFile(testFile, []byte(yamlContent), 0600)
		require.NoError(t, err)

		_, err = spec.Load(th.Context, testFile)
		require.Error(t, err, "expected error for multiple commands with jq executor")
		require.Contains(t, err.Error(), `step type "jq" supports only one command`)
		require.NotContains(t, err.Error(), "executor")
	})

	t.Run("HTTPExecutorRejectsNonStringMethod", func(t *testing.T) {
		t.Parallel()

		// Create a temp file with the DAG content
		tempDir := t.TempDir()
		filename := fmt.Sprintf("%s.yaml", uuid.New().String())
		testFile := filepath.Join(tempDir, filename)
		yamlContent := `
steps:
  - name: http-multi
    action: http.request
    with:
      method: [GET, POST]
      url: https://example.com
`
		err := os.WriteFile(testFile, []byte(yamlContent), 0600)
		require.NoError(t, err)

		_, err = spec.Load(th.Context, testFile)
		require.Error(t, err, "expected error for invalid http.request method")
		require.Contains(t, err.Error(), `with.method must be a non-empty string`)
		require.NotContains(t, err.Error(), "executor")
	})

	t.Run("RunAllowsMultipleCommands", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `
steps:
  - name: shell-multi
    run:
      - echo hello
      - echo world
    output: OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		// Output is concatenated from all commands
		dag.AssertOutputs(t, map[string]any{
			"OUT": "hello\nworld",
		})
	})

	t.Run("CommandExecutorAllowsMultipleCommands", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `
steps:
  - name: cmd-multi
    run:
      - echo hello
      - echo world
    output: OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		// Output is concatenated from all commands
		dag.AssertOutputs(t, map[string]any{
			"OUT": "hello\nworld",
		})
	})
}
