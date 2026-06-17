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
	outputFile         string
	outputContent      string
	missingParam       string
	missingOutputFile  string
	missingOutput      *string
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
			dagu.ExpectFileContent(tc.outputFile, tc.outputContent)
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

			result := dagu.Run("start", tc.file)
			result.ExpectExitCode(0)
			result.ExpectStderrNotContains("preserving literal text")
			result.ExpectStderrNotContains("has no runtime value")
			outputFile := tc.outputFile
			if tc.missingOutputFile != "" {
				outputFile = tc.missingOutputFile
			}
			outputContent := tc.outputContent
			if tc.missingOutput != nil {
				outputContent = *tc.missingOutput
			}
			dagu.ExpectFileContent(outputFile, outputContent)
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
			name:               "root precondition resolves named params",
			file:               "root_precondition.yaml",
			outputFile:         "root-precondition.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			missingOutput:      new("prod\n"),
			successAbsentFiles: []string{"root-precondition-literal.txt"},
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

func setupSpec005Dotenv(t *testing.T, dagu *harness.Runner) {
	t.Helper()
	dagu.WriteFile(".env.prod", "DOTENV_VALUE=prod\n")
	dagu.WriteFile(".env.${params.environment}", "DOTENV_VALUE=literal\n")
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
