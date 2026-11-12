// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec007_value_resolution_steps_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

func TestValidateStepOutputReferenceNotices(t *testing.T) {
	t.Parallel()

	validCases := []string{
		"valid_reference.yaml",
		"quiet_unsupported_escaped.yaml",
		"stdout_outputs_publish_step_outputs.yaml",
		"output_schema_publishes_step_outputs.yaml",
		"object_output_publishes_step_outputs.yaml",
		"outputs_write_publishes_step_outputs.yaml",
	}
	for _, file := range validCases {
		t.Run(file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", file)
			result.ExpectExitCode(0)
			result.ExpectStdout("")
			result.ExpectStderr("")
		})
	}

	noticeCases := []struct {
		file        string
		stderrParts []string
	}{
		{
			file:        "unknown_step_id.yaml",
			stderrParts: []string{"${steps.build.outputs.image}", "reason=unknown_step_id", "steps[0].run"},
		},
		{
			file:        "self_reference.yaml",
			stderrParts: []string{"${steps.build.outputs.image}", "reason=self_reference", "steps[0].run"},
		},
		{
			file:        "unknown_output_name.yaml",
			stderrParts: []string{"${steps.build.outputs.tag}", "reason=unknown_output_name", "steps[1].run"},
		},
		{
			file:        "namespace_root_env.yaml",
			stderrParts: []string{"${steps.build.outputs.image}", "reason=namespace_unavailable", "env[0]"},
		},
		{
			file:        "namespace_handler.yaml",
			stderrParts: []string{"${steps.build.outputs.image}", "reason=namespace_unavailable", "handler_on.success.run"},
		},
	}
	for _, tc := range noticeCases {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.file)
			result.ExpectExitCode(0)
			result.ExpectStdout("")
			result.ExpectStderrContains(tc.stderrParts...)
		})
	}
}

func TestRuntimeStepOutputReferenceResolution(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		file    string
		output  string
		content string
	}{
		{
			name:    "valid dependency resolves declared output",
			file:    "valid_reference.yaml",
			output:  "resolved-valid.txt",
			content: "v1.2.3\n",
		},
		{
			name:    "reference without depends infers the dependency and resolves",
			file:    "inferred_dependency.yaml",
			output:  "inferred-dependency.txt",
			content: "v1.2.3\n",
		},
		{
			name:    "unknown output preserves literal",
			file:    "unknown_output_name.yaml",
			output:  "unknown-output-name.txt",
			content: "${steps.build.outputs.tag}\n",
		},
		{
			name:    "root env has no step output scope",
			file:    "namespace_root_env.yaml",
			output:  "namespace-root-env.txt",
			content: "${steps.build.outputs.image}\n",
		},
		{
			name:    "handler has no step output scope",
			file:    "namespace_handler.yaml",
			output:  "handler-output.txt",
			content: "${steps.build.outputs.image}\n",
		},
		{
			name:    "stdout outputs publish step outputs",
			file:    "stdout_outputs_publish_step_outputs.yaml",
			output:  "stdout-outputs.txt",
			content: "published-v1\n",
		},
		{
			name:    "output schema publishes step outputs",
			file:    "output_schema_publishes_step_outputs.yaml",
			output:  "output-schema.txt",
			content: "schema-v1\n",
		},
		{
			name:    "object form output publishes step outputs",
			file:    "object_output_publishes_step_outputs.yaml",
			output:  "object-output.txt",
			content: "object-v1\n",
		},
		{
			name:    "outputs write publishes step outputs",
			file:    "outputs_write_publishes_step_outputs.yaml",
			output:  "outputs-write.txt",
			content: "write-v1\n",
		},
		{
			name:    "escaped and unsupported text is preserved",
			file:    "quiet_unsupported_escaped.yaml",
			output:  "quiet.txt",
			content: "${steps.build.outputs.image}\n${steps.build.outputs.meta.tag}\n${steps.build-step.outputs.image}\n${build.output.image}\n${step.xxx.foo}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("start", tc.file)
			result.ExpectExitCode(0)
			dagu.ExpectFileContent(tc.output, tc.content)
		})
	}
}

// A preserved reference explains itself in the run log, so the failure a shell
// reports later is traceable to the reference that never resolved.
func TestRuntimeStepOutputReferenceNotice(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("start", "runtime_notice_unresolved.yaml")
	result.ExpectExitCode(0)
	result.ExpectStderrContains("${steps.build.outputs.tag}", "was left unchanged", "step=deploy")
	dagu.ExpectFileContent("runtime-notice.txt", "${steps.build.outputs.tag}\n")
}
