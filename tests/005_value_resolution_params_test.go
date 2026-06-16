// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package tests_test

import (
	"os"
	"path/filepath"
	"testing"
)

const spec005RuntimeParams = "environment=prod"

type spec005StartCase struct {
	name               string
	file               string
	args               []string
	outputFile         string
	outputContent      string
	missingParam       string
	missingOutput      *string
	setup              func(*testing.T, *Runner)
	successAbsentFiles []string
	missingAbsentFiles []string
}

func Test005ValueResolutionParamsValidate(t *testing.T) {
	t.Parallel()

	validCases := []string{
		"declared_reference_fields.yaml",
		"non_dagu_braced_text_is_preserved.yaml",
		"params_eval_declared.yaml",
		"runtime_resolution.yaml",
		"unbraced_params_text_is_preserved.yaml",
	}
	for _, file := range validCases {
		t.Run(file, func(t *testing.T) {
			t.Parallel()

			dagu := newRunner(t, "005_value_resolution_params")

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
			name:        "params reference unavailable from consts",
			file:        "params_reference_in_consts.yaml",
			stderrParts: []string{"consts", "${params.environment}"},
		},
		{
			name:        "params defaults do not support references",
			file:        "params_reference_in_params_declaration.yaml",
			stderrParts: []string{"params", "${params.environment}"},
		},
		{
			name:        "positional params are not addressable by name",
			file:        "positional_only_params_reference.yaml",
			stderrParts: []string{"params", "${params.environment}"},
		},
		{
			name:        "invalid declaration name",
			file:        "invalid_param_declaration_name.yaml",
			stderrParts: []string{"params", "1environment"},
		},
		{
			name:        "undeclared params eval reference is forbidden",
			file:        "params_eval_undeclared.yaml",
			stderrParts: []string{"params", "${params.missing}"},
		},
		{
			name:        "undeclared workflow reference is forbidden",
			file:        "undeclared_reference_in_run.yaml",
			stderrParts: []string{"steps[0].run", "${params.missing}"},
		},
	}
	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := newRunner(t, "005_value_resolution_params")

			result := dagu.Run("validate", tc.file)
			result.ExpectExitCode(1)
			result.ExpectStdout("")
			result.ExpectStderrContains(tc.stderrParts...)
			result.ExpectStderrNotContains("Usage:")
			dagu.ExpectNoFile("executed.txt")
		})
	}
}

func Test005ValueResolutionParamsStart(t *testing.T) {
	t.Parallel()

	for _, tc := range spec005StartCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := newRunner(t, "005_value_resolution_params")
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

func Test005ValueResolutionParamsStartRejectsMissingRuntimeValues(t *testing.T) {
	t.Parallel()

	for _, tc := range spec005MissingRuntimeCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := newRunner(t, "005_value_resolution_params")
			if tc.setup != nil {
				tc.setup(t, dagu)
			}

			result := dagu.Run("start", tc.file)
			result.ExpectExitCode(1)
			result.ExpectStderrContains("params." + tc.missingParam)
			result.ExpectStderrNotContains("Usage:")
			if tc.missingOutput != nil {
				dagu.ExpectFileContent(tc.outputFile, *tc.missingOutput)
			} else {
				dagu.ExpectNoFile(tc.outputFile)
			}
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
			name:          "params eval resolves earlier named params",
			file:          "params_eval_runtime.yaml",
			outputFile:    "params-eval.txt",
			outputContent: "prod-api\n",
			missingParam:  "environment",
		},
		{
			name:          "root env resolves named params",
			file:          "root_env.yaml",
			outputFile:    "root-env.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
		},
		{
			name:          "dotenv path resolves named params",
			file:          "dotenv.yaml",
			outputFile:    "dotenv.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
			setup:         setupSpec005Dotenv,
		},
		{
			name:               "root shell array args resolve named params",
			args:               []string{"start", "--params", "shell_arg=-c", "root_shell_array_args.yaml"},
			file:               "root_shell_array_args.yaml",
			outputFile:         "root-shell-array-args.txt",
			outputContent:      "prod\n",
			missingParam:       "shell_arg",
			setup:              setupSpec005ShellArgsWrapper,
			missingAbsentFiles: []string{"root-shell-array-args-marker.txt"},
		},
		{
			name:               "root precondition resolves named params",
			file:               "root_precondition.yaml",
			outputFile:         "root-precondition.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			successAbsentFiles: []string{"root-precondition-literal.txt"},
			missingAbsentFiles: []string{"root-precondition-literal.txt"},
		},
		{
			name:          "step with resolves named params",
			file:          "step_with.yaml",
			outputFile:    "with-content.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
		},
		{
			name:          "step env resolves named params",
			file:          "step_env.yaml",
			outputFile:    "step-env.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
		},
		{
			name:               "step working dir resolves named params",
			file:               "step_working_dir.yaml",
			outputFile:         "step-work-prod/step-working-dir.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			setup:              setupSpec005StepWorkingDir,
			successAbsentFiles: []string{"step-work-${params.environment}/step-working-dir.txt"},
			missingAbsentFiles: []string{"step-work-${params.environment}/step-working-dir.txt"},
		},
		{
			name:               "step precondition resolves named params",
			file:               "step_precondition.yaml",
			outputFile:         "step-precondition.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			successAbsentFiles: []string{"step-precondition-literal.txt"},
			missingAbsentFiles: []string{"step-precondition-literal.txt"},
		},
		{
			name:               "step stdout path resolves named params",
			file:               "step_stdout_path.yaml",
			outputFile:         "stdout-prod.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			successAbsentFiles: []string{"stdout-${params.environment}.txt"},
			missingAbsentFiles: []string{"stdout-${params.environment}.txt"},
		},
		{
			name:               "step stderr path resolves named params",
			file:               "step_stderr_path.yaml",
			outputFile:         "stderr-prod.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			successAbsentFiles: []string{"stderr-${params.environment}.txt"},
			missingAbsentFiles: []string{"stderr-${params.environment}.txt"},
		},
		{
			name:          "step output value resolves named params",
			file:          "step_output_value.yaml",
			outputFile:    "output-value.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
		},
		{
			name:          "step output path resolves named params",
			file:          "step_output_path.yaml",
			outputFile:    "output-path.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
			setup:         setupSpec005OutputPath,
		},
		{
			name:          "handler env resolves named params",
			file:          "handler_env.yaml",
			outputFile:    "handler-env.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
		},
		{
			name:          "handler with params resolves named params",
			file:          "handler_with_params.yaml",
			outputFile:    "handler-with-params.txt",
			outputContent: "{\"environment\":\"prod\"}\n",
			missingParam:  "environment",
			missingOutput: ptrTo(""),
		},
		{
			name:               "handler working dir resolves named params",
			file:               "handler_working_dir.yaml",
			outputFile:         "handler-work-prod/handler-working-dir.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			setup:              setupSpec005HandlerWorkingDir,
			successAbsentFiles: []string{"handler-work-${params.environment}/handler-working-dir.txt"},
			missingAbsentFiles: []string{"handler-work-${params.environment}/handler-working-dir.txt"},
		},
		{
			name:               "handler precondition resolves named params",
			file:               "handler_precondition.yaml",
			outputFile:         "handler-precondition.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			successAbsentFiles: []string{"handler-precondition-literal.txt"},
			missingAbsentFiles: []string{"handler-precondition-literal.txt"},
		},
		{
			name:               "handler stdout path resolves named params",
			file:               "handler_stdout_path.yaml",
			outputFile:         "handler-stdout-prod.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			successAbsentFiles: []string{"handler-stdout-${params.environment}.txt"},
			missingAbsentFiles: []string{"handler-stdout-${params.environment}.txt"},
		},
		{
			name:               "handler stderr path resolves named params",
			file:               "handler_stderr_path.yaml",
			outputFile:         "handler-stderr-prod.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			successAbsentFiles: []string{"handler-stderr-${params.environment}.txt"},
			missingAbsentFiles: []string{"handler-stderr-${params.environment}.txt"},
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

func ptrTo(value string) *string {
	return &value
}

func setupSpec005Dotenv(t *testing.T, dagu *Runner) {
	t.Helper()
	writeSpec005ProjectFile(t, dagu, ".env.prod", "DOTENV_VALUE=prod\n")
	writeSpec005ProjectFile(t, dagu, ".env.${params.environment}", "DOTENV_VALUE=literal\n")
}

func setupSpec005ShellArgsWrapper(t *testing.T, dagu *Runner) {
	t.Helper()
	writeSpec005ProjectExecutable(t, dagu, "shell-args-wrapper", `#!/bin/sh
printf '%s\n' "$1" > root-shell-array-args-marker.txt
exec /bin/sh "$@"
`)
}

func setupSpec005StepWorkingDir(t *testing.T, dagu *Runner) {
	t.Helper()
	mkdirSpec005ProjectDir(t, dagu, "step-work-prod")
	mkdirSpec005ProjectDir(t, dagu, "step-work-${params.environment}")
}

func setupSpec005HandlerWorkingDir(t *testing.T, dagu *Runner) {
	t.Helper()
	mkdirSpec005ProjectDir(t, dagu, "handler-work-prod")
	mkdirSpec005ProjectDir(t, dagu, "handler-work-${params.environment}")
}

func setupSpec005OutputPath(t *testing.T, dagu *Runner) {
	t.Helper()
	writeSpec005ProjectFile(t, dagu, "outputs/prod/report.txt", "prod")
	writeSpec005ProjectFile(t, dagu, "outputs/${params.environment}/report.txt", "literal")
}

func mkdirSpec005ProjectDir(t *testing.T, dagu *Runner, name string) {
	t.Helper()

	path := filepath.Join(dagu.dir, filepath.FromSlash(name))
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}
}

func writeSpec005ProjectExecutable(t *testing.T, dagu *Runner, name string, content string) {
	t.Helper()

	path := filepath.Join(dagu.dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating parent for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func writeSpec005ProjectFile(t *testing.T, dagu *Runner, name string, content string) {
	t.Helper()

	path := filepath.Join(dagu.dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating parent for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}
