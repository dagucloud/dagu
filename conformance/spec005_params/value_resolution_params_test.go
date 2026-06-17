// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec005_params_test

import (
	"testing"

	"github.com/dagucloud/dagu/conformance/harness"
)

const spec005RuntimeParams = "environment=prod"

type spec005StartCase struct {
	name               string
	file               string
	args               []string
	missingArgs        []string
	outputFile         string
	outputGlob         string
	outputContent      string
	outputContains     []string
	missingParam       string
	missingOutputFile  string
	missingOutputGlob  string
	missingOutput      *string
	missingContains    []string
	setup              func(*testing.T, *harness.Runner)
	successAbsentFiles []string
	missingAbsentFiles []string
}

func TestValidate(t *testing.T) {
	t.Parallel()

	validCases := []string{
		"declared_reference_fields.yaml",
		"non_dagu_braced_text_is_preserved.yaml",
		"params_eval_declared.yaml",
		"params_eval_undeclared.yaml",
		"params_reference_in_params_declaration.yaml",
		"params_reference_in_consts.yaml",
		"positional_only_params_reference.yaml",
		"undeclared_reference_in_run.yaml",
		"runtime_resolution.yaml",
		"unbraced_params_text_is_preserved.yaml",
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

	invalidCases := []struct {
		name        string
		file        string
		stderrParts []string
	}{
		{
			name:        "invalid declaration name",
			file:        "invalid_param_declaration_name.yaml",
			stderrParts: []string{"params", "1environment"},
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

	for _, tc := range spec005StartCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			if tc.setup != nil {
				tc.setup(t, dagu)
			}

			args := tc.args
			if len(args) == 0 {
				args = []string{"start", "--params", spec005RuntimeParams, tc.file}
			}
			result := dagu.Run(args...)
			result.ExpectExitCode(0)
			expectSpec005Output(t, dagu, tc.outputFile, tc.outputGlob, tc.outputContent, tc.outputContains)
			for _, file := range tc.successAbsentFiles {
				dagu.ExpectNoFile(file)
			}
		})
	}
}

func TestMissingRuntimeValues(t *testing.T) {
	t.Parallel()

	for _, tc := range spec005MissingRuntimeCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			if tc.setup != nil {
				tc.setup(t, dagu)
			}

			args := tc.missingArgs
			if len(args) == 0 {
				args = []string{"start", tc.file}
			}
			result := dagu.Run(args...)
			result.ExpectExitCode(0)
			result.ExpectStderrNotContains("preserving literal text")
			result.ExpectStderrNotContains("has no runtime value")
			outputFile := tc.outputFile
			if tc.missingOutputFile != "" {
				outputFile = tc.missingOutputFile
			}
			outputGlob := tc.outputGlob
			if tc.missingOutputGlob != "" {
				outputGlob = tc.missingOutputGlob
			}
			outputContent := tc.outputContent
			if tc.missingOutput != nil {
				outputContent = *tc.missingOutput
			}
			contains := tc.outputContains
			if len(tc.missingContains) > 0 {
				contains = tc.missingContains
			}
			expectSpec005Output(t, dagu, outputFile, outputGlob, outputContent, contains)
			for _, file := range tc.missingAbsentFiles {
				dagu.ExpectNoFile(file)
			}
		})
	}
}

func spec005StartCases() []spec005StartCase {
	return []spec005StartCase{
		{
			name: "named runtime params resolve in string fields",
			args: []string{
				"start",
				"--params",
				"environment=prod tag=v1 enabled=true replicas=3 ratio=1.25",
				"runtime_resolution.yaml",
			},
			outputFile: "resolved.txt",
			outputContent: "" +
				"environment=prod\n" +
				"tag=v1\n" +
				"enabled=true\n" +
				"replicas=3\n" +
				"ratio=1.25\n",
		},
		{
			name:          "params reference in consts stays literal at runtime",
			args:          []string{"start", "--params", spec005RuntimeParams, "params_reference_in_consts_runtime.yaml"},
			outputFile:    "params-in-consts.txt",
			outputContent: "${params.environment}\n",
		},
		{
			name:          "params reference in default stays literal at runtime",
			args:          []string{"start", "params_default_literal.yaml"},
			outputFile:    "params-default-literal.txt",
			outputContent: "${params.environment}\n",
		},
		{
			name:          "non matching params braced text is preserved",
			args:          []string{"start", "non_dagu_braced_text_is_preserved.yaml"},
			outputFile:    "preserved.txt",
			outputContent: "${params...}\n",
		},
		{
			name:          "unbraced params text is preserved",
			args:          []string{"start", "unbraced_params_text_is_preserved.yaml"},
			outputFile:    "unbraced-params.txt",
			outputContent: "$params.environment\n",
		},
		{
			name:          "missing runtime param preserves literal and continues",
			args:          []string{"start", "missing_runtime_literal_run.yaml"},
			file:          "missing_runtime_literal_run.yaml",
			outputFile:    "missing-runtime-literal.txt",
			outputContent: "${params.environment}\n",
			missingParam:  "environment",
			missingOutput: new("${params.environment}\n"),
		},
		{
			name:          "params eval resolves earlier named params",
			file:          "params_eval_runtime.yaml",
			outputFile:    "params-eval.txt",
			outputContent: "prod-api\n",
			missingParam:  "environment",
			missingOutput: new("${params.environment}-api\n"),
		},
		{
			name:          "root env resolves named params",
			file:          "root_env.yaml",
			outputFile:    "root-env.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
			missingOutput: new("${params.environment}\n"),
		},
		{
			name:          "env list forms resolve named params",
			file:          "env_list_forms.yaml",
			outputFile:    "env-list-forms.txt",
			outputContent: "prod\nprod\nprod\nprod\n",
			missingParam:  "environment",
			missingOutput: new("${params.environment}\n${params.environment}\n${params.environment}\n${params.environment}\n"),
		},
		{
			name:          "dotenv path resolves named params",
			file:          "dotenv.yaml",
			outputFile:    "dotenv.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
			missingOutput: new("literal\n"),
			setup:         setupSpec005Dotenv,
		},
		{
			name:          "root shell array args resolve named params",
			args:          []string{"start", "--params", "shell_arg=-c", "root_shell_array_args.yaml"},
			file:          "root_shell_array_args.yaml",
			outputFile:    "root-shell-array-args.txt",
			outputContent: "prod\n",
		},
		{
			name:          "root shell string resolves named params",
			args:          []string{"start", "--params", "shell_arg=-c", "root_shell_string.yaml"},
			file:          "root_shell_string.yaml",
			outputFile:    "root-shell-string.txt",
			outputContent: "prod\n",
		},
		{
			name:               "root working dir resolves named params",
			file:               "root_working_dir.yaml",
			outputFile:         "root-work-prod/root-working-dir.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			missingOutputFile:  "root-work-${params.environment}/root-working-dir.txt",
			missingOutput:      new("prod\n"),
			setup:              setupSpec005RootWorkingDir,
			successAbsentFiles: []string{"root-work-${params.environment}/root-working-dir.txt"},
		},
		{
			name:               "root precondition resolves named params",
			file:               "root_precondition.yaml",
			outputFile:         "root-precondition.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			missingOutput:      new("prod\n"),
			successAbsentFiles: []string{"root-precondition-literal.txt"},
		},
		{
			name:          "step run array resolves named params",
			file:          "step_run_array.yaml",
			outputFile:    "step-run-array.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
			missingOutput: new("${params.environment}\n"),
		},
		{
			name:          "step with resolves named params",
			file:          "step_with.yaml",
			outputFile:    "with-content.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
			missingOutput: new("${params.environment}\n"),
		},
		{
			name:          "step with nested values resolve named params",
			file:          "step_with_nested.yaml",
			outputFile:    "step-with-nested.txt",
			outputContent: "{\"environment\":\"prod\"}\n",
			missingParam:  "environment",
			missingOutput: new("{\"environment\":\"${params.environment}\"}\n"),
		},
		{
			name:          "step env resolves named params",
			file:          "step_env.yaml",
			outputFile:    "step-env.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
			missingOutput: new("${params.environment}\n"),
		},
		{
			name:               "step working dir resolves named params",
			file:               "step_working_dir.yaml",
			outputFile:         "step-work-prod/step-working-dir.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			missingOutputFile:  "step-work-${params.environment}/step-working-dir.txt",
			missingOutput:      new("prod\n"),
			setup:              setupSpec005StepWorkingDir,
			successAbsentFiles: []string{"step-work-${params.environment}/step-working-dir.txt"},
		},
		{
			name:               "step precondition resolves named params",
			file:               "step_precondition.yaml",
			outputFile:         "step-precondition.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			missingOutput:      new("prod\n"),
			successAbsentFiles: []string{"step-precondition-literal.txt"},
		},
		{
			name:          "step repeat condition resolves named params",
			file:          "step_repeat_policy_condition.yaml",
			outputFile:    "repeat-condition.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
			missingOutput: new("${params.environment}\n"),
		},
		{
			name:       "parallel variable resolves named params",
			args:       []string{"start", "--params", "items=prod", "parallel_variable.yaml"},
			file:       "parallel_variable.yaml",
			outputFile: "parallel-variable.txt",
			outputContains: []string{
				`"succeeded": 1`,
				`"CHILD_VALUE": "prod"`,
			},
			missingParam: "items",
			missingContains: []string{
				`"succeeded": 1`,
				`"CHILD_VALUE": "${params.items}"`,
			},
		},
		{
			name:       "parallel item value resolves named params",
			file:       "parallel_items_value.yaml",
			outputFile: "parallel-items-value.txt",
			outputContains: []string{
				`"succeeded": 1`,
				`"CHILD_VALUE": "prod"`,
			},
			missingParam: "environment",
			missingContains: []string{
				`"succeeded": 1`,
				`"CHILD_VALUE": "${params.environment}"`,
			},
		},
		{
			name:       "parallel item params resolve named params",
			file:       "parallel_items_params.yaml",
			outputFile: "parallel-items-params.txt",
			outputContains: []string{
				`"succeeded": 1`,
				`"CHILD_VALUE": "prod"`,
			},
			missingParam: "environment",
			missingContains: []string{
				`"succeeded": 1`,
				`"CHILD_VALUE": "${params.environment}"`,
			},
		},
		{
			name:               "step stdout path resolves named params",
			file:               "step_stdout_path.yaml",
			outputFile:         "stdout-prod.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			missingOutputFile:  "stdout-${params.environment}.txt",
			missingOutput:      new("prod\n"),
			successAbsentFiles: []string{"stdout-${params.environment}.txt"},
		},
		{
			name:               "step stderr path resolves named params",
			file:               "step_stderr_path.yaml",
			outputFile:         "stderr-prod.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			missingOutputFile:  "stderr-${params.environment}.txt",
			missingOutput:      new("prod\n"),
			successAbsentFiles: []string{"stderr-${params.environment}.txt"},
		},
		{
			name:              "step stdout artifact resolves named params",
			args:              []string{"start", "--run-id", "spec005_stdout_artifact", "--params", spec005RuntimeParams, "step_stdout_artifact.yaml"},
			missingArgs:       []string{"start", "--run-id", "spec005_stdout_artifact_missing", "step_stdout_artifact.yaml"},
			file:              "step_stdout_artifact.yaml",
			outputGlob:        "artifacts/spec005-step-stdout-artifact/dag-run_*_spec005_stdout_artifact/reports/prod/stdout.txt",
			outputContent:     "prod\n",
			missingParam:      "environment",
			missingOutputGlob: "artifacts/spec005-step-stdout-artifact/dag-run_*_spec005_stdout_artifact_missing/reports/${params.environment}/stdout.txt",
			missingOutput:     new("prod\n"),
		},
		{
			name:              "step stderr artifact resolves named params",
			args:              []string{"start", "--run-id", "spec005_stderr_artifact", "--params", spec005RuntimeParams, "step_stderr_artifact.yaml"},
			missingArgs:       []string{"start", "--run-id", "spec005_stderr_artifact_missing", "step_stderr_artifact.yaml"},
			file:              "step_stderr_artifact.yaml",
			outputGlob:        "artifacts/spec005-step-stderr-artifact/dag-run_*_spec005_stderr_artifact/reports/prod/stderr.txt",
			outputContent:     "prod\n",
			missingParam:      "environment",
			missingOutputGlob: "artifacts/spec005-step-stderr-artifact/dag-run_*_spec005_stderr_artifact_missing/reports/${params.environment}/stderr.txt",
			missingOutput:     new("prod\n"),
		},
		{
			name:          "stdout outputs fields resolve named params",
			file:          "step_stdout_outputs.yaml",
			outputFile:    "stdout-outputs.txt",
			outputContent: "prod\nprod-from-stdout\n",
			missingParam:  "environment",
			missingOutput: new("${params.environment}\nprod-from-stdout\n"),
		},
		{
			name:          "step output value resolves named params",
			file:          "step_output_value.yaml",
			outputFile:    "output-value.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
			missingOutput: new("${params.environment}\n"),
		},
		{
			name:          "step output path resolves named params",
			file:          "step_output_path.yaml",
			outputFile:    "output-path.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
			missingOutput: new("literal\n"),
			setup:         setupSpec005OutputPath,
		},
		{
			name:          "step output nested values resolve named params",
			file:          "step_output_nested.yaml",
			outputFile:    "output-nested.txt",
			outputContent: "prod\nenv-prod\n",
			missingParam:  "environment",
			missingOutput: new("${params.environment}\nenv-${params.environment}\n"),
		},
		{
			name:          "handler run array resolves named params",
			file:          "handler_run_array.yaml",
			outputFile:    "handler-run-array.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
			missingOutput: new("${params.environment}\n"),
		},
		{
			name:          "handler env resolves named params",
			file:          "handler_env.yaml",
			outputFile:    "handler-env.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
			missingOutput: new("${params.environment}\n"),
		},
		{
			name:          "handler with params resolves named params",
			file:          "handler_with_params.yaml",
			outputFile:    "handler-with-params.txt",
			outputContent: "{\"environment\":\"prod\"}\n",
			missingParam:  "environment",
			missingOutput: new("{\"environment\":\"${params.environment}\"}\n"),
		},
		{
			name:               "handler working dir resolves named params",
			file:               "handler_working_dir.yaml",
			outputFile:         "handler-work-prod/handler-working-dir.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			missingOutputFile:  "handler-work-${params.environment}/handler-working-dir.txt",
			missingOutput:      new("prod\n"),
			setup:              setupSpec005HandlerWorkingDir,
			successAbsentFiles: []string{"handler-work-${params.environment}/handler-working-dir.txt"},
		},
		{
			name:               "handler precondition resolves named params",
			file:               "handler_precondition.yaml",
			outputFile:         "handler-precondition.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			missingOutput:      new("prod\n"),
			successAbsentFiles: []string{"handler-precondition-literal.txt"},
		},
		{
			name:               "handler stdout path resolves named params",
			file:               "handler_stdout_path.yaml",
			outputFile:         "handler-stdout-prod.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			missingOutputFile:  "handler-stdout-${params.environment}.txt",
			missingOutput:      new("prod\n"),
			successAbsentFiles: []string{"handler-stdout-${params.environment}.txt"},
		},
		{
			name:               "handler stderr path resolves named params",
			file:               "handler_stderr_path.yaml",
			outputFile:         "handler-stderr-prod.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			missingOutputFile:  "handler-stderr-${params.environment}.txt",
			missingOutput:      new("prod\n"),
			successAbsentFiles: []string{"handler-stderr-${params.environment}.txt"},
		},
		{
			name:              "handler stdout artifact resolves named params",
			args:              []string{"start", "--run-id", "spec005_handler_stdout_artifact", "--params", spec005RuntimeParams, "handler_stdout_artifact.yaml"},
			missingArgs:       []string{"start", "--run-id", "spec005_handler_stdout_artifact_missing", "handler_stdout_artifact.yaml"},
			file:              "handler_stdout_artifact.yaml",
			outputGlob:        "artifacts/spec005-handler-stdout-artifact/dag-run_*_spec005_handler_stdout_artifact/reports/prod/stdout.txt",
			outputContent:     "prod\n",
			missingParam:      "environment",
			missingOutputGlob: "artifacts/spec005-handler-stdout-artifact/dag-run_*_spec005_handler_stdout_artifact_missing/reports/${params.environment}/stdout.txt",
			missingOutput:     new("prod\n"),
		},
		{
			name:              "handler stderr artifact resolves named params",
			args:              []string{"start", "--run-id", "spec005_handler_stderr_artifact", "--params", spec005RuntimeParams, "handler_stderr_artifact.yaml"},
			missingArgs:       []string{"start", "--run-id", "spec005_handler_stderr_artifact_missing", "handler_stderr_artifact.yaml"},
			file:              "handler_stderr_artifact.yaml",
			outputGlob:        "artifacts/spec005-handler-stderr-artifact/dag-run_*_spec005_handler_stderr_artifact/reports/prod/stderr.txt",
			outputContent:     "prod\n",
			missingParam:      "environment",
			missingOutputGlob: "artifacts/spec005-handler-stderr-artifact/dag-run_*_spec005_handler_stderr_artifact_missing/reports/${params.environment}/stderr.txt",
			missingOutput:     new("prod\n"),
		},
	}
}

func spec005MissingRuntimeCases() []spec005StartCase {
	cases := make([]spec005StartCase, 0)
	for _, tc := range spec005StartCases() {
		if tc.missingParam == "" {
			continue
		}
		cases = append(cases, tc)
	}
	return cases
}

func expectSpec005Output(t *testing.T, dagu *harness.Runner, file string, glob string, content string, contains []string) {
	t.Helper()
	if len(contains) > 0 {
		dagu.ExpectFileContains(file, contains...)
		return
	}
	if glob != "" {
		dagu.ExpectGlobFileContent(glob, content)
		return
	}
	dagu.ExpectFileContent(file, content)
}

func setupSpec005Dotenv(t *testing.T, dagu *harness.Runner) {
	t.Helper()
	dagu.WriteFile(".env.prod", "DOTENV_VALUE=prod\n")
	dagu.WriteFile(".env.${params.environment}", "DOTENV_VALUE=literal\n")
}

func setupSpec005RootWorkingDir(t *testing.T, dagu *harness.Runner) {
	t.Helper()
	dagu.Mkdir("root-work-prod")
	dagu.Mkdir("root-work-${params.environment}")
}

func setupSpec005StepWorkingDir(t *testing.T, dagu *harness.Runner) {
	t.Helper()
	dagu.Mkdir("step-work-prod")
	dagu.Mkdir("step-work-${params.environment}")
}

func setupSpec005HandlerWorkingDir(t *testing.T, dagu *harness.Runner) {
	t.Helper()
	dagu.Mkdir("handler-work-prod")
	dagu.Mkdir("handler-work-${params.environment}")
}

func setupSpec005OutputPath(t *testing.T, dagu *harness.Runner) {
	t.Helper()
	dagu.WriteFile("outputs/prod/report.txt", "prod")
	dagu.WriteFile("outputs/${params.environment}/report.txt", "literal")
}
