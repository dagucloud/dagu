// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/internal/test"
)

func TestShellExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping Unix shell tests on Windows")
	}
	t.Parallel()

	th := test.Setup(t)

	t.Run("DAGLevelShellWithArgs", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `
shell: "/bin/bash -e"
steps:
  - name: test
    run: |
      echo "hello"
    output: OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT": "hello",
		})
	})

	t.Run("StepLevelShellOverride", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `
shell: /bin/sh
steps:
  - name: test
    run: |
      echo "from bash"
    with:
      shell: "/bin/bash -e"
    output: OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT": "from bash",
		})
	})

	t.Run("ErrexitBehavior", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `
shell: "/bin/bash -e"
steps:
  - name: test
    run: |
      false
      echo "should not reach"
    output: OUT
`)
		agent := dag.Agent()
		agent.RunError(t)
	})

	t.Run("ShellCmdArgsWithPipe", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `
shell: /bin/bash
steps:
  - name: test
    run: echo hello | tr 'h' 'H'
    output: OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT": "Hello",
		})
	})

	t.Run("ShellWithCommandSubstitution", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `
shell: /bin/sh
steps:
  - name: test
    run: echo "date is $(date +%Y)"
    output: OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		// Just verify it runs - date will vary
	})

	t.Run("ShellWithEnvironmentVariable", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `
shell: /bin/sh
env:
  - MY_VAR: "test_value"
steps:
  - name: test
    run: echo "$MY_VAR"
    output: OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT": "test_value",
		})
	})

	t.Run("ShellPreservesBackslashDollar", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `
shell: /bin/sh
steps:
  - name: test
    run: 'echo "\$HOME"'
    output: OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT": "$HOME",
		})
	})

	t.Run("ShellWithMultipleCommands", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `
shell: /bin/sh
steps:
  - name: test
    run: VAR=hello && echo "$VAR world"
    output: OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT": "hello world",
		})
	})

	t.Run("ShellScriptWithMultilineCommands", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `
shell: /bin/sh
steps:
  - name: test
    run: |
      VAR="hello"
      echo "$VAR world"
    output: OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT": "hello world",
		})
	})

	t.Run("ShellWithRedirection", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `
shell: /bin/sh
steps:
  - name: test
    run: echo "test" | cat
    output: OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT": "test",
		})
	})

	t.Run("DefaultShellFallback", func(t *testing.T) {
		t.Parallel()

		// When no shell is specified, should use system default
		dag := th.DAG(t, `
steps:
  - name: test
    run: echo "default shell"
    output: OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT": "default shell",
		})
	})

	t.Run("ShellWithGlobExpansion", func(t *testing.T) {
		t.Parallel()

		// Shell should expand globs
		dag := th.DAG(t, `
shell: /bin/sh
steps:
  - name: test
    run: echo /bin/ech*
    output: OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT": "/bin/echo",
		})
	})

	t.Run("ShellScriptWithShebang", func(t *testing.T) {
		t.Parallel()

		// Script with shebang should use the shebang interpreter
		dag := th.DAG(t, `
shell: /bin/sh
steps:
  - name: test
    run: |
      #!/bin/bash
      echo "bash via shebang"
    output: OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT": "bash via shebang",
		})
	})
}
