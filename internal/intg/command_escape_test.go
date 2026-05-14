// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"os"
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

func TestCommandExecution_DollarEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping Unix command tests on Windows")
	}
	t.Parallel()

	th := test.Setup(t)

	t.Run("WithShell", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `
shell: /bin/sh
steps:
  - name: test
    run: echo "\$HOME"
    output: OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT": "$HOME",
		})
	})

	t.Run("ScriptWithShell", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `
shell: /bin/sh
steps:
  - name: test
    run: |
      echo "\$HOME"
    output: OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT": "$HOME",
		})
	})

	t.Run("WithoutShell_Direct", func(t *testing.T) {
		t.Parallel()

		dag := th.DAG(t, `
shell: direct
steps:
  - name: test
    run: /bin/echo '\$HOME'
    output: OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT": "$HOME",
		})
	})

	t.Run("ScriptWithoutShell_Direct", func(t *testing.T) {
		t.Parallel()

		homeDir, err := os.UserHomeDir()
		require.NoError(t, err)

		dag := th.DAG(t, `
steps:
  - name: test
    action: exec
    with:
      command: /bin/sh
      args:
        - -c
        - 'echo "$HOME"'
    output: OUT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)
		dag.AssertOutputs(t, map[string]any{
			"OUT": homeDir,
		})
	})
}
