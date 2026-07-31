// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"os"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/test"
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
			Args: []string{"validate", dag.Location},
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
			Args: []string{"validate", dagFile},
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
			ExpectedOut: []string{"deprecated", "steps[0].command", "use run"},
		})
	})

	t.Run("ValueReferenceNotices", func(t *testing.T) {
		th.LoggingOutput.Reset()
		dagFile := th.CreateDAGFile(t, "value_resolution_notice.yaml", `
consts:
  - image: ${consts.missing}
steps:
  - run: echo ok
`)

		th.RunCommand(t, cmd.Validate(), test.CmdTest{
			Args:        []string{"validate", dagFile},
			ExpectedOut: []string{"${consts.missing}", "was left unchanged", "consts.image"},
		})
	})

	t.Run("RuntimeOnlyNoticesAreHiddenByDefault", func(t *testing.T) {
		th.LoggingOutput.Reset()
		dagFile := th.CreateDAGFile(t, "runtime_only_notice.yaml", `
steps:
  - id: archive
    run: cp report.csv "${context.paths.artifacts_dir}/"
`)

		th.RunCommand(t, cmd.Validate(), test.CmdTest{
			Args: []string{"validate", dagFile},
		})
		require.NotContains(t, th.LoggingOutput.String(), "context.paths.artifacts_dir")
	})

	t.Run("RuntimeOnlyNoticesAreShownOnRequest", func(t *testing.T) {
		th.LoggingOutput.Reset()
		dagFile := th.CreateDAGFile(t, "runtime_only_notice_shown.yaml", `
steps:
  - id: archive
    run: cp report.csv "${context.paths.artifacts_dir}/"
`)

		th.RunCommand(t, cmd.Validate(), test.CmdTest{
			Args:        []string{"validate", "--show-unresolved", dagFile},
			ExpectedOut: []string{"${context.paths.artifacts_dir}", "reason=namespace_unavailable"},
		})
	})

	t.Run("UnknownContextFieldIsReported", func(t *testing.T) {
		th.LoggingOutput.Reset()
		dagFile := th.CreateDAGFile(t, "unknown_context_field.yaml", `
steps:
  - id: archive
    run: cp report.csv "${context.paths.artifact_dir}/"
`)

		th.RunCommand(t, cmd.Validate(), test.CmdTest{
			Args:        []string{"validate", dagFile},
			ExpectedOut: []string{"${context.paths.artifact_dir}", "reason=unknown_context_field"},
		})
	})

	t.Run("ForeachReferenceOutsideItemScopeIsReported", func(t *testing.T) {
		th.LoggingOutput.Reset()
		dagFile := th.CreateDAGFile(t, "foreach_reference_outside_item_scope.yaml", `
steps:
  - run: echo ${foreach.item}
`)

		th.RunCommand(t, cmd.Validate(), test.CmdTest{
			Args:        []string{"validate", dagFile},
			ExpectedOut: []string{"${foreach.item}", "reason=namespace_unavailable"},
		})
	})

	t.Run("StepEnvIsInScopeForOtherStepFields", func(t *testing.T) {
		th.LoggingOutput.Reset()
		dagFile := th.CreateDAGFile(t, "step_env_scope.yaml", `
steps:
  - id: call
    env:
      - MY_ENDPOINT: https://example.internal/api
    action: http.request
    with:
      method: GET
      url: ${env.MY_ENDPOINT}
`)

		th.RunCommand(t, cmd.Validate(), test.CmdTest{
			Args: []string{"validate", "--show-unresolved", dagFile},
		})
		require.NotContains(t, th.LoggingOutput.String(), "MY_ENDPOINT")
	})

	t.Run("StepOutputValueReferenceNoticeReason", func(t *testing.T) {
		th.LoggingOutput.Reset()
		dagFile := th.CreateDAGFile(t, "step_value_resolution_notice.yaml", `
steps:
  - id: build
    run: printf 'image=v1\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: image
  - id: deploy
    run: echo ${steps.build.outputs.image}
`)

		th.RunCommand(t, cmd.Validate(), test.CmdTest{
			Args:        []string{"validate", dagFile},
			ExpectedOut: []string{"${steps.build.outputs.image}", "was left unchanged", "reason=missing_dependency"},
		})
	})

	t.Run("V2SyntaxDoesNotWarn", func(t *testing.T) {
		th.LoggingOutput.Reset()
		dagFile := th.CreateDAGFile(t, "v2_syntax.yaml", `
steps:
  - run: echo ok
`)

		th.RunCommand(t, cmd.Validate(), test.CmdTest{
			Args: []string{"validate", dagFile},
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

	t.Run("ResolvedPathValidationErrorUsesResolvedLocation", func(t *testing.T) {
		dagFile := th.CreateDAGFile(t, "resolved_path.yaml", `
name: invalid-entrypoint-name
steps:
  - run: echo ok
`)

		err := th.RunCommandWithError(t, cmd.Validate(), test.CmdTest{
			Args: []string{"validate", "resolved_path.yaml"},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "Validation failed for "+dagFile)
		require.NotContains(t, err.Error(), "Validation failed for resolved_path.yaml")
	})

	t.Run("MissingFile", func(t *testing.T) {
		err := th.RunCommandWithError(t, cmd.Validate(), test.CmdTest{
			Args: []string{"validate", "/nonexistent/file.yaml"},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "Validation failed")
	})
}
