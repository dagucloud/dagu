// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"os"
	"testing"

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/stretchr/testify/require"
)

func TestValidateCommand(t *testing.T) {
	th := test.SetupCommand(t)

	t.Run("ValidSpec", func(t *testing.T) {
		dag := th.DAG(t, `
steps:
  - run: echo ok
`)

		th.RunCommand(t, cmd.Validate(), test.CmdTest{
			Args:        []string{"validate", dag.Location},
			ExpectedOut: []string{"DAG spec is valid"},
		})
	})

	t.Run("BaseConfigStepTypes", func(t *testing.T) {
		require.NoError(t, os.WriteFile(th.Config.Paths.BaseConfig, []byte(`
step_types:
  greet:
    type: command
    input_schema:
      type: object
      additionalProperties: false
      required: [message]
      properties:
        message:
          type: string
    template:
      exec:
        command: /bin/echo
        args:
          - {$input: message}
`), 0600))

		dagFile := th.CreateDAGFile(t, "base_config_step_type.yaml", `
steps:
  - type: greet
    with:
      message: hello
`)

		th.RunCommand(t, cmd.Validate(), test.CmdTest{
			Args:        []string{"validate", dagFile},
			ExpectedOut: []string{"DAG spec is valid"},
		})
	})

	t.Run("LegacySyntaxWarnsButSucceeds", func(t *testing.T) {
		th.LoggingOutput.Reset()
		dagFile := th.CreateDAGFile(t, "legacy_syntax.yaml", `
steps:
  - command: echo legacy
`)

		th.RunCommand(t, cmd.Validate(), test.CmdTest{
			Args:        []string{"validate", dagFile},
			ExpectedOut: []string{"DAG spec is valid", "deprecated", "steps[0].command", "use run"},
		})
	})

	t.Run("V2SyntaxDoesNotWarn", func(t *testing.T) {
		th.LoggingOutput.Reset()
		dagFile := th.CreateDAGFile(t, "v2_syntax.yaml", `
steps:
  - run: echo ok
`)

		th.RunCommand(t, cmd.Validate(), test.CmdTest{
			Args:        []string{"validate", dagFile},
			ExpectedOut: []string{"DAG spec is valid"},
		})
		require.NotContains(t, th.LoggingOutput.String(), "deprecated")
	})

	t.Run("MixedRunAndLegacyFails", func(t *testing.T) {
		th.LoggingOutput.Reset()
		dagFile := th.CreateDAGFile(t, "mixed_v2_legacy.yaml", `
steps:
  - run: echo ok
    command: echo legacy
`)

		err := th.RunCommandWithError(t, cmd.Validate(), test.CmdTest{
			Args: []string{"validate", dagFile},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "run cannot be used together with command")
	})

	t.Run("InvalidDependency", func(t *testing.T) {
		// This DAG has a step depending on a non-existent step
		dagFile := th.CreateDAGFile(t, "invalid.yaml", `
type: graph
steps:
  - run: echo A
  - name: "b"
    run: echo B
    depends: ["missing_step"]
`)

		err := th.RunCommandWithError(t, cmd.Validate(), test.CmdTest{
			Args: []string{"validate", dagFile},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "Validation failed")
	})

	t.Run("InvalidYAML", func(t *testing.T) {
		// This DAG has invalid YAML syntax
		dagFile := th.CreateDAGFile(t, "invalid_yaml.yaml", `
steps:
  - name: "test"
    run: echo test
  invalid yaml here: [[[
`)

		err := th.RunCommandWithError(t, cmd.Validate(), test.CmdTest{
			Args: []string{"validate", dagFile},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "Validation failed")
	})

	t.Run("MissingFile", func(t *testing.T) {
		err := th.RunCommandWithError(t, cmd.Validate(), test.CmdTest{
			Args: []string{"validate", "/nonexistent/file.yaml"},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "Validation failed")
	})
}
