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

	successOnlyCases := []string{
		"direct_execution_env_expansion.yaml",
	}
	for _, file := range successOnlyCases {
		t.Run(file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", file)
			result.ExpectExitCode(0)
			result.ExpectStdout("")
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
		{
			name:        "validate does not require current step managed source",
			file:        "current_step_managed_env.yaml",
			stderrParts: []string{"${env.DAG_RUN_STEP_NAME}", "was left unchanged", "steps[0].env"},
		},
		{
			name:        "validate does not require current step stdout source",
			file:        "current_step_managed_env.yaml",
			stderrParts: []string{"${env.DAG_RUN_STEP_STDOUT_FILE}", "was left unchanged", "steps[0].env"},
		},
		{
			name:        "validate does not require current step output file source",
			file:        "current_step_managed_env.yaml",
			stderrParts: []string{"${env.DAGU_OUTPUT_FILE}", "was left unchanged", "steps[0].env"},
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
		{
			name:        "invalid secret managed name",
			file:        "invalid_secret_managed_name.yaml",
			stderrParts: []string{"secrets", "DAG_RUN_ID", "collides"},
		},
		{
			name:        "invalid secret execution attempt name",
			file:        "invalid_secret_execution_attempt_name.yaml",
			stderrParts: []string{"secrets", "DAG_RUN_STEP_STDOUT_FILE", "collides"},
		},
		{
			name:        "invalid secret DAGU output file name",
			file:        "invalid_secret_dagu_output_file_name.yaml",
			stderrParts: []string{"secrets", "DAGU_OUTPUT_FILE"},
		},
		{
			name:        "invalid secret DAGU prefix name",
			file:        "invalid_secret_dagu_prefix_name.yaml",
			stderrParts: []string{"secrets", "DAGU_CUSTOM", "must not start with DAGU_"},
		},
		{
			name:        "invalid secret pushback managed name",
			file:        "invalid_secret_pushback_name.yaml",
			stderrParts: []string{"secrets", "DAG_PUSHBACK", "collides"},
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
			content: "'$SERVICE'\n'${SERVICE}'\n'api'\n",
		},
		{
			name:    "shell style expressions resolve or stay literal",
			file:    "shell_style_env_expressions.yaml",
			output:  "shell-style.txt",
			content: "api\nfallback\nxx\nalternate\nxx\npi\n3\nteam/api.tar.gz\napi.tar.gz\nregistry/team/api.tar\nregistry\napi\nregistry/team/api.tar.gz\napi.tar.gz\nregistry/team/api.tar\napi\napi\n${MISSING:-default}\n${MISSING:4:10}\n${MISSING:=default}\n",
		},
		{
			name:    "assignment-like shell expression preserves shell-style field when assignment would be required",
			file:    "shell_style_assignment_preserves_field.yaml",
			output:  "shell-style-assignment.txt",
			content: "api\napi\napi\n${SERVICE:-fallback}\n${EMPTY:=fallback}\n${EMPTY:-fallback}\nxx\n",
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
			content: "shell api\n",
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

	t.Run("protected run env is restored before step local shadowing", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "protected_env_shadowing.yaml")
		result.ExpectExitCode(0)
		dagu.ExpectFileContent(
			"protected-shadowing.txt",
			"root-env-read-workflow-shadow\nDAG_RUN_ID=step-shadow\nSTEP_COPY=step-shadow\n",
		)
		dagu.ExpectFileContent("protected-final.txt", "ROOT_COPY=root-shadow\nfinal-protected\nresolved-matches-process\n")
	})

	t.Run("duplicate predecessor outputs follow authored dependency order", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "duplicate_predecessor_outputs.yaml")
		result.ExpectExitCode(0)
		dagu.ExpectFileContent("duplicate-output-first-second.txt", "second\n")
		dagu.ExpectFileContent("duplicate-output-second-first.txt", "first\n")
		dagu.ExpectFileContent("duplicate-output-transitive.txt", "ancestor\n")
		dagu.ExpectFileContent("duplicate-output-visit-once.txt", "later\n")
	})

	t.Run("current step managed env precedence is explicit", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "current_step_managed_env.yaml")
		result.ExpectExitCode(0)
		dagu.ExpectFileContent(
			"current-step-managed-env.txt",
			"STEP_NAME_COPY=managed\nSTEP_NAME_PROCESS=authored-step-name\nSTEP_STDOUT_COPY=${env.DAG_RUN_STEP_STDOUT_FILE}\nOUTPUT_FILE_COPY=${env.DAGU_OUTPUT_FILE}\nmanaged-stdout-file\nmanaged-output-file\n",
		)
		dagu.ExpectFileContent("current-step-managed-output.txt", "published\n")
	})

	t.Run("secret env boundary distinguishes Dagu and shell expansion", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(
			[]string{"SPEC006_API_TOKEN=spec006-secret-value"},
			"start",
			"secret_env_boundary.yaml",
		)
		result.ExpectExitCode(0)
		dagu.ExpectFileContent(
			"secret-env-boundary.txt",
			"PROCESS_ENV_BEFORE=spec006-secret-value\nUNQUALIFIED_AFTER_ASSIGN=shell-local\nNAMESPACED_INSERTION=spec006-secret-value\n",
		)
		dagu.ExpectFileContent(
			"secret-action-boundary.txt",
			"UNQUALIFIED_ACTION=spec006-secret-value\nNAMESPACED_ACTION=spec006-secret-value\n",
		)
	})

	t.Run("direct execution can use inherited process env fallback", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(
			[]string{"DIRECT_PROCESS_ONLY=from-process"},
			"start",
			"direct_execution_process_env_fallback.yaml",
		)
		result.ExpectExitCode(0)
		dagu.ExpectFileContent("direct-process-env.txt", "from-process\n")
	})

	t.Run("action inputs do not use inherited process env fallback", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(
			[]string{"ACTION_PROCESS_ONLY=from-process"},
			"start",
			"action_with_no_process_env_fallback.yaml",
		)
		result.ExpectExitCode(0)
		dagu.ExpectFileContent("action-with-env.txt", "$ACTION_PROCESS_ONLY\n${ACTION_PROCESS_ONLY}\n")
	})
}
