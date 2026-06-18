// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec006_env_test

import (
	"testing"

	"github.com/dagucloud/dagu/conformance/harness"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	validCases := []string{
		"env_forms_order.yaml",
		"single_quoted_env_references.yaml",
		"shell_style_env_expressions.yaml",
		"braced_non_env_text.yaml",
		"shell_run_boundary.yaml",
		"direct_execution_env_expansion.yaml",
	}
	for _, file := range validCases {
		t.Run(file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", file)
			result.ExpectExitCode(0)
			result.ExpectStdout("")
			result.ExpectStderr("")
			dagu.ExpectNoFile("executed.txt")
		})
	}

	noticeCases := []struct {
		name        string
		file        string
		stderrParts []string
	}{
		{
			name:        "self env reference notice",
			file:        "env_top_to_bottom.yaml",
			stderrParts: []string{"${env.SELF}", "was left unchanged", "env"},
		},
		{
			name:        "later env reference notice",
			file:        "env_top_to_bottom.yaml",
			stderrParts: []string{"$AFTER", "was left unchanged", "env"},
		},
		{
			name:        "container self env reference notice",
			file:        "container_env_ordering.yaml",
			stderrParts: []string{"${env.SELF}", "was left unchanged", "container.env"},
		},
		{
			name:        "missing Dagu env reference notice",
			file:        "missing_env_references.yaml",
			stderrParts: []string{"${env.MISSING}", "was left unchanged", "steps[0].run"},
		},
		{
			name:        "root env runtime param source notice",
			file:        "root_env_sources.yaml",
			stderrParts: []string{"${params.target}", "was left unchanged", "env"},
		},
		{
			name:        "step env runtime param source notice",
			file:        "step_env_sources.yaml",
			stderrParts: []string{"${params.target}", "was left unchanged", "steps[1].env"},
		},
		{
			name:        "step env predecessor output source notice",
			file:        "step_env_sources.yaml",
			stderrParts: []string{"${steps.produce.outputs.VALUE}", "was left unchanged", "steps[1].env"},
		},
		{
			name:        "validate does not require runtime env source",
			file:        "validate_missing_runtime_sources.yaml",
			stderrParts: []string{"${env.RUNTIME_ONLY}", "was left unchanged", "steps[1].run"},
		},
		{
			name:        "validate does not require process env source",
			file:        "validate_missing_runtime_sources.yaml",
			stderrParts: []string{"${env.PROCESS_ONLY}", "was left unchanged", "steps[1].run"},
		},
		{
			name:        "validate does not require dotenv env source",
			file:        "validate_missing_runtime_sources.yaml",
			stderrParts: []string{"${env.DOTENV_ONLY}", "was left unchanged", "steps[1].run"},
		},
		{
			name:        "validate does not require predecessor output source",
			file:        "validate_missing_runtime_sources.yaml",
			stderrParts: []string{"${steps.previous.outputs.VALUE}", "was left unchanged", "steps[1].env"},
		},
	}
	for _, tc := range noticeCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.file)
			result.ExpectExitCode(0)
			result.ExpectStdout("")
			result.ExpectStderrContains(tc.stderrParts...)
			dagu.ExpectNoFile("executed.txt")
		})
	}

	invalidCases := []struct {
		name        string
		file        string
		stderrParts []string
	}{
		{
			name:        "invalid env declaration name",
			file:        "invalid_env_declaration_name.yaml",
			stderrParts: []string{"env", "1INVALID"},
		},
		{
			name:        "invalid env declaration shape",
			file:        "invalid_env_declaration_shape.yaml",
			stderrParts: []string{"env", "map or array"},
		},
		{
			name:        "invalid env list entry",
			file:        "invalid_env_list_entry.yaml",
			stderrParts: []string{"env", "MISSING_EQUALS"},
		},
	}
	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.file)
			result.ExpectExitCode(1)
			result.ExpectStdout("")
			result.ExpectStderrContains(tc.stderrParts...)
			result.ExpectStderrNotContains("Usage:")
			dagu.ExpectNoFile("executed.txt")
		})
	}
}

func TestRuntime(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		file    string
		args    []string
		output  string
		content string
	}{
		{
			name:    "env declaration forms preserve order",
			file:    "env_forms_order.yaml",
			output:  "env-forms.txt",
			content: "SERVICE=api\nHOST=api.internal\n",
		},
		{
			name:    "env entries evaluate top to bottom",
			file:    "env_top_to_bottom.yaml",
			output:  "env-order.txt",
			content: "SERVICE=api\nHOST=api.internal\nSELF=${env.SELF}\nLATER=$AFTER\n",
		},
		{
			name:    "missing env references stay literal",
			file:    "missing_env_references.yaml",
			output:  "missing-env.txt",
			content: "${env.MISSING}\n$MISSING\n${MISSING}\n",
		},
		{
			name:    "root env reads consts and runtime params",
			file:    "root_env_sources.yaml",
			args:    []string{"start", "--params", "target=prod", "root_env_sources.yaml"},
			output:  "root-sources.txt",
			content: "COLOR=blue\nTARGET=prod\n",
		},
		{
			name:    "step env reads all allowed sources",
			file:    "step_env_sources.yaml",
			args:    []string{"start", "--params", "target=prod", "step_env_sources.yaml"},
			output:  "step-env-sources.txt",
			content: "CONST=blue\nPARAM=prod\nROOT=api\nEARLIER=seed\nPREVIOUS=step-value\n",
		},
		{
			name:    "single quoted env references stay literal",
			file:    "single_quoted_env_references.yaml",
			output:  "single-quoted.txt",
			content: "$SERVICE\n${SERVICE}\n",
		},
		{
			name:    "shell style expressions resolve or stay literal",
			file:    "shell_style_env_expressions.yaml",
			output:  "shell-style.txt",
			content: "api\n${MISSING:-default}\n${MISSING:4:10}\n",
		},
		{
			name:    "unsupported braced text stays ordinary content",
			file:    "braced_non_env_text.yaml",
			output:  "braced-text.txt",
			content: "${not.env}\n${env}\n",
		},
		{
			name:    "shell backed run preserves shell owned env syntax",
			file:    "shell_run_boundary.yaml",
			output:  "shell-boundary.txt",
			content: "api api\n",
		},
		{
			name:    "direct execution has no later shell expansion",
			file:    "direct_execution_env_expansion.yaml",
			output:  "direct-exec.txt",
			content: "api ${MISSING}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			args := tc.args
			if len(args) == 0 {
				args = []string{"start", tc.file}
			}
			result := dagu.Run(args...)
			result.ExpectExitCode(0)
			dagu.ExpectFileContent(tc.output, tc.content)
		})
	}
}
