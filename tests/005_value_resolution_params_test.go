// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package tests_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func Test005ValueResolutionParamsValidateDeclaredReferences(t *testing.T) {
	t.Parallel()

	for i, tc := range spec005ValueFieldCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := newRunner(t, "005_value_resolution_params")
			file := writeSpec005DAG(t, dagu, fmt.Sprintf("valid_%03d.yaml", i), tc.yaml(`${params.environment}`))

			result := dagu.Run("validate", file)
			result.ExpectExitCode(0)
			result.ExpectStdout("")
			result.ExpectStderr("")
			dagu.ExpectNoFile("executed.txt")
		})
	}
}

func Test005ValueResolutionParamsValidateRejectsUndeclaredReferencesByField(t *testing.T) {
	t.Parallel()

	for i, tc := range spec005ValueFieldCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := newRunner(t, "005_value_resolution_params")
			file := writeSpec005DAG(t, dagu, fmt.Sprintf("undeclared_%03d.yaml", i), tc.yaml(`${params.missing}`))

			result := dagu.Run("validate", file)
			result.ExpectExitCode(1)
			result.ExpectStdout("")
			result.ExpectStderrContains(tc.context, "${params.missing}")
			result.ExpectStderrNotContains("Usage:")
			dagu.ExpectNoFile("executed.txt")
		})
	}
}

func Test005ValueResolutionParamsValidatePreservesUnbracedNamespaceTextByField(t *testing.T) {
	t.Parallel()

	for i, tc := range spec005ValueFieldCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := newRunner(t, "005_value_resolution_params")
			file := writeSpec005DAG(t, dagu, fmt.Sprintf("unbraced_%03d.yaml", i), tc.yaml(`$params.environment`))

			result := dagu.Run("validate", file)
			result.ExpectExitCode(0)
			result.ExpectStdout("")
			result.ExpectStderr("")
			dagu.ExpectNoFile("executed.txt")
		})
	}
}

func Test005ValueResolutionParamsValidateParamsEvalReferences(t *testing.T) {
	t.Parallel()

	t.Run("declared", func(t *testing.T) {
		t.Parallel()

		dagu := newRunner(t, "005_value_resolution_params")
		file := writeSpec005DAG(t, dagu, "valid_params_eval.yaml", spec005ParamsEvalDAG(`${params.environment}`))

		result := dagu.Run("validate", file)
		result.ExpectExitCode(0)
		result.ExpectStdout("")
		result.ExpectStderr("")
		dagu.ExpectNoFile("executed.txt")
	})

	t.Run("undeclared", func(t *testing.T) {
		t.Parallel()

		dagu := newRunner(t, "005_value_resolution_params")
		file := writeSpec005DAG(t, dagu, "undeclared_params_eval.yaml", spec005ParamsEvalDAG(`${params.missing}`))

		result := dagu.Run("validate", file)
		result.ExpectExitCode(1)
		result.ExpectStdout("")
		result.ExpectStderrContains("params", "${params.missing}")
		result.ExpectStderrNotContains("Usage:")
		dagu.ExpectNoFile("executed.txt")
	})
}

func Test005ValueResolutionParamsValidateUnsupportedLocations(t *testing.T) {
	t.Parallel()

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

	cases := []struct {
		name          string
		args          []string
		outputFile    string
		outputContent string
	}{
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := newRunner(t, "005_value_resolution_params")

			result := dagu.Run(tc.args...)
			result.ExpectExitCode(0)
			dagu.ExpectFileContent(tc.outputFile, tc.outputContent)
		})
	}
}

func Test005ValueResolutionParamsStartResolvesFieldSurfaces(t *testing.T) {
	t.Parallel()

	for i, tc := range spec005RuntimeFieldCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := newRunner(t, "005_value_resolution_params")
			if tc.setup != nil {
				tc.setup(t, dagu)
			}
			file := writeSpec005DAG(t, dagu, fmt.Sprintf("runtime_%03d.yaml", i), tc.yaml)

			result := dagu.Run("start", "--params", tc.runtimeParams(), file)
			result.ExpectExitCode(tc.exitCode)
			dagu.ExpectFileContent(tc.outputFile, tc.outputContent)
			for _, file := range tc.successAbsentFiles {
				dagu.ExpectNoFile(file)
			}
		})
	}
}

func Test005ValueResolutionParamsStartRejectsMissingRuntimeValues(t *testing.T) {
	t.Parallel()

	for i, tc := range spec005RuntimeFieldCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := newRunner(t, "005_value_resolution_params")
			if tc.setup != nil {
				tc.setup(t, dagu)
			}
			file := writeSpec005DAG(t, dagu, fmt.Sprintf("missing_%03d.yaml", i), tc.yaml)

			result := dagu.Run("start", file)
			result.ExpectExitCode(1)
			result.ExpectStderrContains("params." + tc.missingParam)
			result.ExpectStderrNotContains("Usage:")
			dagu.ExpectNoFile(tc.outputFile)
			for _, file := range tc.missingAbsentFiles {
				dagu.ExpectNoFile(file)
			}
		})
	}
}

func Test005ValueResolutionParamsRuntimeCoverageExceptions(t *testing.T) {
	t.Parallel()

	exceptions := []struct {
		field  string
		reason string
	}{
		{
			field:  "root container, steps[].container, and handler container fields",
			reason: "successful and missing-value runtime checks would cross into Docker or existing-container state before producing a stable local side effect; validation covers declared and undeclared authoring, unbraced namespace-looking text is preserved as ordinary content, and runtime coverage is intentionally excepted",
		},
		{
			field:  "steps[].parallel and handler parallel variable/items/items[].params",
			reason: "successful and missing-value runtime checks require sub-DAG fan-out orchestration; validation covers declared and undeclared authoring, unbraced namespace-looking text is preserved as ordinary content, and local command runtime coverage is kept separate",
		},
		{
			field:  "steps[].repeat_policy.condition and handler repeat_policy.condition",
			reason: "successful and missing-value runtime checks are coupled to repeat-count semantics after command execution; validation covers declared and undeclared authoring, and unbraced namespace-looking text is preserved as ordinary content without binding this spec to repeat behavior",
		},
		{
			field:  "steps[] and handler stdout/stderr artifact paths",
			reason: "artifact paths resolve inside run-managed artifact storage whose concrete path includes run identity; stdout and stderr path cases cover local stream redirection, and artifact runtime coverage is intentionally excepted",
		},
		{
			field:  "steps[].stdout.outputs.fields.*.path and handler stdout.outputs.fields.*.path",
			reason: "current YAML grammar does not accept file path sources under stdout.outputs; file path output resolution is covered by steps[].output.*.path",
		},
		{
			field:  "handler stdout.outputs.fields.*.value and handler output.*",
			reason: "handler output values are terminal handler metadata with no downstream step that can consume them as a local file side effect; validation covers every handler event, and normal step output runtime cases cover observable output resolution timing",
		},
		{
			field:  "handler_on.abort and handler_on.wait runtime execution",
			reason: "abort requires external cancellation and wait requires an approval transition; validation crosses handler steps with step-owned fields, while init/success/failure/exit have runtime assertions",
		},
	}
	for _, exception := range exceptions {
		t.Run(exception.field, func(t *testing.T) {
			t.Parallel()

			if exception.reason == "" {
				t.Fatalf("runtime coverage exception for %s must explain why it is accepted", exception.field)
			}
		})
	}
}

type spec005ValueFieldCase struct {
	name    string
	context string
	yaml    func(ref string) string
}

type spec005RuntimeFieldCase struct {
	name               string
	yaml               string
	outputFile         string
	outputContent      string
	missingParam       string
	params             string
	exitCode           int
	setup              func(*testing.T, *Runner)
	successAbsentFiles []string
	missingAbsentFiles []string
}

const spec005RuntimeParams = "environment=prod shell=sh shell_arg=-c"

func (tc spec005RuntimeFieldCase) runtimeParams() string {
	if tc.params != "" {
		return tc.params
	}
	return spec005RuntimeParams
}

func spec005RuntimeFieldCases() []spec005RuntimeFieldCase {
	cases := spec005RootRuntimeFieldCases()
	cases = append(cases, spec005StepRuntimeFieldCases()...)
	cases = append(cases, spec005HandlerRuntimeFieldCases()...)
	return cases
}

func spec005RootRuntimeFieldCases() []spec005RuntimeFieldCase {
	return []spec005RuntimeFieldCase{
		{
			name:          "params_eval",
			yaml:          spec005ParamsEvalRuntimeDAG(),
			params:        "environment=prod",
			outputFile:    "params-eval.txt",
			outputContent: "prod-api\n",
			missingParam:  "environment",
		},
		{
			name: "root_env_map",
			yaml: spec005RuntimeDAG(`
working_dir: .
env:
  ROOT_VALUE: "${params.environment}"
steps:
  - name: write
    run: |
      printf '%s\n' "$ROOT_VALUE" > root-env-map.txt
`),
			outputFile:    "root-env-map.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
		},
		{
			name: "root_env_array_map",
			yaml: spec005RuntimeDAG(`
working_dir: .
env:
  - ROOT_VALUE: "${params.environment}"
steps:
  - name: write
    run: |
      printf '%s\n' "$ROOT_VALUE" > root-env-array-map.txt
`),
			outputFile:    "root-env-array-map.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
		},
		{
			name: "root_env_key_value",
			yaml: spec005RuntimeDAG(`
working_dir: .
env:
  - ROOT_VALUE=${params.environment}
steps:
  - name: write
    run: |
      printf '%s\n' "$ROOT_VALUE" > root-env-key-value.txt
`),
			outputFile:    "root-env-key-value.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
		},
		{
			name: "dotenv",
			yaml: spec005RuntimeDAG(`
working_dir: .
dotenv:
  - ".env.${params.environment}"
steps:
  - name: write
    run: |
      printf '%s\n' "$DOTENV_VALUE" > dotenv.txt
`),
			outputFile:    "dotenv.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
			setup: func(t *testing.T, dagu *Runner) {
				t.Helper()
				writeSpec005ProjectFile(t, dagu, ".env.prod", "DOTENV_VALUE=prod\n")
				writeSpec005ProjectFile(t, dagu, ".env.${params.environment}", "DOTENV_VALUE=literal\n")
			},
		},
		{
			name: "root_shell",
			yaml: spec005RuntimeDAG(`
working_dir: .
shell: "./shell-${params.environment}"
steps:
  - name: write
    run: |
      test -f root-shell-marker.txt
      printf '%s\n' prod > root-shell.txt
`),
			outputFile:         "root-shell.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			successAbsentFiles: []string{"root-shell-literal-marker.txt"},
			missingAbsentFiles: []string{"root-shell-marker.txt", "root-shell-literal-marker.txt"},
			setup: func(t *testing.T, dagu *Runner) {
				t.Helper()
				writeSpec005ProjectExecutable(t, dagu, "shell-prod", `#!/bin/sh
printf '%s\n' used > root-shell-marker.txt
exec /bin/sh "$@"
`)
				writeSpec005ProjectExecutable(t, dagu, "shell-${params.environment}", `#!/bin/sh
printf '%s\n' used > root-shell-literal-marker.txt
exec /bin/sh "$@"
`)
			},
		},
		{
			name: "root_shell_array_args",
			yaml: spec005RuntimeDAG(`
working_dir: .
shell:
  - ./shell-args-wrapper
  - "${params.shell_arg}"
steps:
  - name: write
    run: |
      test "$(cat root-shell-array-args-marker.txt)" = "-c"
      printf '%s\n' prod > root-shell-array-args.txt
`),
			outputFile:         "root-shell-array-args.txt",
			outputContent:      "prod\n",
			missingParam:       "shell_arg",
			missingAbsentFiles: []string{"root-shell-array-args-marker.txt"},
			setup: func(t *testing.T, dagu *Runner) {
				t.Helper()
				writeSpec005ProjectExecutable(t, dagu, "shell-args-wrapper", `#!/bin/sh
printf '%s\n' "$1" > root-shell-array-args-marker.txt
exec /bin/sh "$@"
`)
			},
		},
		{
			name: "root_working_dir",
			yaml: spec005RuntimeDAG(`
working_dir: "work-${params.environment}"
steps:
  - name: write
    run: |
      printf '%s\n' prod > root-working-dir.txt
`),
			outputFile:         "work-prod/root-working-dir.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			successAbsentFiles: []string{"work-${params.environment}/root-working-dir.txt"},
			missingAbsentFiles: []string{"work-${params.environment}/root-working-dir.txt"},
			setup: func(t *testing.T, dagu *Runner) {
				t.Helper()
				mkdirSpec005ProjectDir(t, dagu, "work-prod")
				mkdirSpec005ProjectDir(t, dagu, "work-${params.environment}")
			},
		},
		{
			name: "root_precondition",
			yaml: spec005RuntimeDAG(`
working_dir: .
preconditions:
  - condition: |
      case '${params.environment}' in
        prod) exit 0;;
        \${params.environment}) printf '%s\n' literal > root-precondition-literal.txt; exit 1;;
        *) exit 1;;
      esac
steps:
  - name: write
    run: |
      printf '%s\n' prod > root-precondition.txt
`),
			outputFile:         "root-precondition.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			successAbsentFiles: []string{"root-precondition-literal.txt"},
			missingAbsentFiles: []string{"root-precondition-literal.txt"},
		},
	}
}

func spec005StepRuntimeFieldCases() []spec005RuntimeFieldCase {
	return []spec005RuntimeFieldCase{
		{
			name: "step_run_string",
			yaml: spec005RuntimeDAG(`
working_dir: .
steps:
  - name: write
    run: |
      printf '%s\n' '${params.environment}' > step-run-string.txt
`),
			outputFile:    "step-run-string.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
		},
		{
			name: "step_run_array",
			yaml: spec005RuntimeDAG(`
working_dir: .
steps:
  - name: write
    run:
      - "printf '%s\n' '${params.environment}' > step-run-array.txt"
`),
			outputFile:    "step-run-array.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
		},
		{
			name: "step_with_path",
			yaml: spec005RuntimeDAG(`
working_dir: .
steps:
  - name: write
    action: file.write
    with:
      path: "with-${params.environment}.txt"
      content: |
        prod
`),
			outputFile:         "with-prod.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			successAbsentFiles: []string{"with-${params.environment}.txt"},
			missingAbsentFiles: []string{"with-${params.environment}.txt"},
		},
		{
			name: "step_with_content",
			yaml: spec005RuntimeDAG(`
working_dir: .
steps:
  - name: write
    action: file.write
    with:
      path: with-content.txt
      content: |
        ${params.environment}
`),
			outputFile:    "with-content.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
		},
		{
			name: "step_with_nested_values",
			yaml: spec005RuntimeDAG(`
working_dir: .
steps:
  - id: publish
    action: outputs.write
    with:
      values:
        status: "${params.environment}"
  - name: consume
    depends: publish
    run: |
      printf '%s\n' '${steps.publish.outputs.status}' > with-nested-values.txt
`),
			outputFile:    "with-nested-values.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
		},
		{
			name: "step_working_dir",
			yaml: spec005RuntimeDAG(`
working_dir: .
steps:
  - name: write
    working_dir: "step-work-${params.environment}"
    run: |
      printf '%s\n' prod > step-working-dir.txt
`),
			outputFile:         "step-work-prod/step-working-dir.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			successAbsentFiles: []string{"step-work-${params.environment}/step-working-dir.txt"},
			missingAbsentFiles: []string{"step-work-${params.environment}/step-working-dir.txt"},
			setup: func(t *testing.T, dagu *Runner) {
				t.Helper()
				mkdirSpec005ProjectDir(t, dagu, "step-work-prod")
				mkdirSpec005ProjectDir(t, dagu, "step-work-${params.environment}")
			},
		},
		{
			name: "step_env_map",
			yaml: spec005RuntimeDAG(`
working_dir: .
steps:
  - name: write
    env:
      STEP_VALUE: "${params.environment}"
    run: |
      printf '%s\n' "$STEP_VALUE" > step-env-map.txt
`),
			outputFile:    "step-env-map.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
		},
		{
			name: "step_env_array_map",
			yaml: spec005RuntimeDAG(`
working_dir: .
steps:
  - name: write
    env:
      - STEP_VALUE: "${params.environment}"
    run: |
      printf '%s\n' "$STEP_VALUE" > step-env-array-map.txt
`),
			outputFile:    "step-env-array-map.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
		},
		{
			name: "step_env_key_value",
			yaml: spec005RuntimeDAG(`
working_dir: .
steps:
  - name: write
    env:
      - STEP_VALUE=${params.environment}
    run: |
      printf '%s\n' "$STEP_VALUE" > step-env-key-value.txt
`),
			outputFile:    "step-env-key-value.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
		},
		{
			name: "step_precondition",
			yaml: spec005RuntimeDAG(`
working_dir: .
steps:
  - name: write
    preconditions:
      - condition: |
          case '${params.environment}' in
            prod) exit 0;;
            \${params.environment}) printf '%s\n' literal > step-precondition-literal.txt; exit 1;;
            *) exit 1;;
          esac
    run: |
      printf '%s\n' prod > step-precondition.txt
`),
			outputFile:         "step-precondition.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			successAbsentFiles: []string{"step-precondition-literal.txt"},
			missingAbsentFiles: []string{"step-precondition-literal.txt"},
		},
		{
			name: "step_stdout_path",
			yaml: spec005RuntimeDAG(`
working_dir: .
steps:
  - name: write
    run: |
      printf '%s\n' prod
    stdout: "stdout-${params.environment}.txt"
`),
			outputFile:         "stdout-prod.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			successAbsentFiles: []string{"stdout-${params.environment}.txt"},
			missingAbsentFiles: []string{"stdout-${params.environment}.txt"},
		},
		{
			name: "step_stderr_path",
			yaml: spec005RuntimeDAG(`
working_dir: .
steps:
  - name: write
    run: |
      printf '%s\n' prod >&2
    stderr: "stderr-${params.environment}.txt"
`),
			outputFile:         "stderr-prod.txt",
			outputContent:      "prod\n",
			missingParam:       "environment",
			successAbsentFiles: []string{"stderr-${params.environment}.txt"},
			missingAbsentFiles: []string{"stderr-${params.environment}.txt"},
		},
		{
			name: "step_stdout_outputs_value",
			yaml: spec005RuntimeDAG(`
working_dir: .
steps:
  - id: produce
    run: "printf 'ignored\n'"
    stdout:
      outputs:
        fields:
          status:
            value: "${params.environment}"
  - name: consume
    depends: produce
    run: |
      printf '%s\n' '${steps.produce.outputs.status}' > stdout-outputs-value.txt
`),
			outputFile:    "stdout-outputs-value.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
		},
		{
			name: "step_output_value",
			yaml: spec005RuntimeDAG(`
working_dir: .
steps:
  - id: produce
    run: "true"
    output:
      result:
        value: "${params.environment}"
  - name: consume
    depends: produce
    run: |
      printf '%s\n' '${steps.produce.outputs.result}' > output-value.txt
`),
			outputFile:    "output-value.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
		},
		{
			name: "step_output_path",
			yaml: spec005RuntimeDAG(`
working_dir: .
steps:
  - id: produce
    run: "true"
    output:
      report:
        from: file
        path: "outputs/${params.environment}/report.txt"
  - name: consume
    depends: produce
    run: |
      printf '%s\n' '${steps.produce.outputs.report}' > output-path.txt
`),
			outputFile:    "output-path.txt",
			outputContent: "prod\n",
			missingParam:  "environment",
			setup: func(t *testing.T, dagu *Runner) {
				t.Helper()
				writeSpec005ProjectFile(t, dagu, "outputs/prod/report.txt", "prod")
				writeSpec005ProjectFile(t, dagu, "outputs/${params.environment}/report.txt", "literal")
			},
		},
	}
}

func spec005HandlerRuntimeFieldCases() []spec005RuntimeFieldCase {
	handlers := []struct {
		name     string
		exitCode int
	}{
		{name: "init"},
		{name: "success"},
		{name: "failure", exitCode: 1},
		{name: "exit"},
	}

	var cases []spec005RuntimeFieldCase
	for _, handler := range handlers {
		handler := handler
		prefix := "handler-" + handler.name
		cases = append(cases,
			spec005RuntimeFieldCase{
				name: prefix + "_run_string",
				yaml: spec005HandlerRuntimeDAG(handler.name, `
run: |
  printf '%s\n' '${params.environment}' > `+prefix+`-run-string.txt
`),
				outputFile:    prefix + "-run-string.txt",
				outputContent: "prod\n",
				missingParam:  "environment",
				exitCode:      handler.exitCode,
			},
			spec005RuntimeFieldCase{
				name: prefix + "_run_array",
				yaml: spec005HandlerRuntimeDAG(handler.name, `
run:
  - "printf '%s\n' '${params.environment}' > `+prefix+`-run-array.txt"
`),
				outputFile:    prefix + "-run-array.txt",
				outputContent: "prod\n",
				missingParam:  "environment",
				exitCode:      handler.exitCode,
			},
			spec005RuntimeFieldCase{
				name: prefix + "_with_content",
				yaml: spec005HandlerRuntimeDAG(handler.name, `
action: file.write
with:
  path: "`+prefix+`-with-content.txt"
  content: |
    ${params.environment}
`),
				outputFile:    prefix + "-with-content.txt",
				outputContent: "prod\n",
				missingParam:  "environment",
				exitCode:      handler.exitCode,
			},
			spec005RuntimeFieldCase{
				name: prefix + "_with_args",
				yaml: spec005HandlerRuntimeDAG(handler.name, `
action: exec
with:
  command: /bin/sh
  args:
    - -c
    - "printf '%s\n' '${params.environment}' > `+prefix+`-with-args.txt"
`),
				outputFile:    prefix + "-with-args.txt",
				outputContent: "prod\n",
				missingParam:  "environment",
				exitCode:      handler.exitCode,
			},
			spec005RuntimeFieldCase{
				name: prefix + "_with_params",
				yaml: spec005HandlerRuntimeDAG(handler.name, `
action: sqlite.query
with:
  dsn: ":memory:"
  query: "SELECT :environment AS environment"
  output_format: jsonl
  params:
    environment: "${params.environment}"
stdout: "`+prefix+`-with-params.txt"
`),
				outputFile:    prefix + "-with-params.txt",
				outputContent: "{\"environment\":\"prod\"}\n",
				missingParam:  "environment",
				exitCode:      handler.exitCode,
			},
			spec005RuntimeFieldCase{
				name: prefix + "_working_dir",
				yaml: spec005HandlerRuntimeDAG(handler.name, `
working_dir: "`+prefix+`-work-${params.environment}"
run: |
  printf '%s\n' prod > `+prefix+`-working-dir.txt
`),
				outputFile:         prefix + "-work-prod/" + prefix + "-working-dir.txt",
				outputContent:      "prod\n",
				missingParam:       "environment",
				exitCode:           handler.exitCode,
				successAbsentFiles: []string{prefix + "-work-${params.environment}/" + prefix + "-working-dir.txt"},
				missingAbsentFiles: []string{prefix + "-work-${params.environment}/" + prefix + "-working-dir.txt"},
				setup: func(t *testing.T, dagu *Runner) {
					t.Helper()
					mkdirSpec005ProjectDir(t, dagu, prefix+"-work-prod")
					mkdirSpec005ProjectDir(t, dagu, prefix+"-work-${params.environment}")
				},
			},
			spec005RuntimeFieldCase{
				name: prefix + "_env_map",
				yaml: spec005HandlerRuntimeDAG(handler.name, `
env:
  HANDLER_VALUE: "${params.environment}"
run: |
  printf '%s\n' "$HANDLER_VALUE" > `+prefix+`-env-map.txt
`),
				outputFile:    prefix + "-env-map.txt",
				outputContent: "prod\n",
				missingParam:  "environment",
				exitCode:      handler.exitCode,
			},
			spec005RuntimeFieldCase{
				name: prefix + "_env_array_map",
				yaml: spec005HandlerRuntimeDAG(handler.name, `
env:
  - HANDLER_VALUE: "${params.environment}"
run: |
  printf '%s\n' "$HANDLER_VALUE" > `+prefix+`-env-array-map.txt
`),
				outputFile:    prefix + "-env-array-map.txt",
				outputContent: "prod\n",
				missingParam:  "environment",
				exitCode:      handler.exitCode,
			},
			spec005RuntimeFieldCase{
				name: prefix + "_env_key_value",
				yaml: spec005HandlerRuntimeDAG(handler.name, `
env:
  - HANDLER_VALUE=${params.environment}
run: |
  printf '%s\n' "$HANDLER_VALUE" > `+prefix+`-env-key-value.txt
`),
				outputFile:    prefix + "-env-key-value.txt",
				outputContent: "prod\n",
				missingParam:  "environment",
				exitCode:      handler.exitCode,
			},
			spec005RuntimeFieldCase{
				name: prefix + "_precondition",
				yaml: spec005HandlerRuntimeDAG(handler.name, `
preconditions:
  - condition: |
      case '${params.environment}' in
        prod) exit 0;;
        \${params.environment}) printf '%s\n' literal > `+prefix+`-precondition-literal.txt; exit 1;;
        *) exit 1;;
      esac
run: |
  printf '%s\n' prod > `+prefix+`-precondition.txt
`),
				outputFile:         prefix + "-precondition.txt",
				outputContent:      "prod\n",
				missingParam:       "environment",
				exitCode:           handler.exitCode,
				successAbsentFiles: []string{prefix + "-precondition-literal.txt"},
				missingAbsentFiles: []string{prefix + "-precondition-literal.txt"},
			},
			spec005RuntimeFieldCase{
				name: prefix + "_stdout_path",
				yaml: spec005HandlerRuntimeDAG(handler.name, `
run: |
  printf '%s\n' prod
stdout: "`+prefix+`-stdout-${params.environment}.txt"
`),
				outputFile:         prefix + "-stdout-prod.txt",
				outputContent:      "prod\n",
				missingParam:       "environment",
				exitCode:           handler.exitCode,
				missingAbsentFiles: []string{prefix + "-stdout-${params.environment}.txt"},
			},
			spec005RuntimeFieldCase{
				name: prefix + "_stderr_path",
				yaml: spec005HandlerRuntimeDAG(handler.name, `
run: |
  printf '%s\n' prod >&2
stderr: "`+prefix+`-stderr-${params.environment}.txt"
`),
				outputFile:         prefix + "-stderr-prod.txt",
				outputContent:      "prod\n",
				missingParam:       "environment",
				exitCode:           handler.exitCode,
				missingAbsentFiles: []string{prefix + "-stderr-${params.environment}.txt"},
			},
		)
	}
	return cases
}

func spec005ValueFieldCases() []spec005ValueFieldCase {
	cases := spec005RootValueFieldCases()
	cases = append(cases, spec005StepValueFieldCases()...)
	cases = append(cases, spec005RootContainerObjectFieldCases()...)
	cases = append(cases, spec005StepContainerObjectFieldCases()...)
	cases = append(cases, spec005HandlerValueFieldCases()...)
	return cases
}

func spec005RootValueFieldCases() []spec005ValueFieldCase {
	return []spec005ValueFieldCase{
		{
			name:    "root_env_map",
			context: "env",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
env:
  ROOT_VALUE: "%s"
steps:
  - name: ok
    run: "true"
`, ref))
			},
		},
		{
			name:    "root_env_array_map",
			context: "env",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
env:
  - ROOT_VALUE: "%s"
steps:
  - name: ok
    run: "true"
`, ref))
			},
		},
		{
			name:    "root_env_key_value",
			context: "env",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
env:
  - ROOT_VALUE=%s
steps:
  - name: ok
    run: "true"
`, ref))
			},
		},
		{
			name:    "dotenv",
			context: "dotenv",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
dotenv:
  - ".env.%s"
steps:
  - name: ok
    run: "true"
`, ref))
			},
		},
		{
			name:    "root_shell",
			context: "shell",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
shell: "%s"
steps:
  - name: ok
    run: "true"
`, ref))
			},
		},
		{
			name:    "root_shell_array_args",
			context: "shell",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
shell:
  - sh
  - "%s"
steps:
  - name: ok
    run: "true"
`, ref))
			},
		},
		{
			name:    "root_working_dir",
			context: "working_dir",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
working_dir: "./%s"
steps:
  - name: ok
    run: "true"
`, ref))
			},
		},
		{
			name:    "root_precondition",
			context: "preconditions",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
preconditions:
  - condition: "test '%s' = prod"
steps:
  - name: ok
    run: "true"
`, ref))
			},
		},
		{
			name:    "root_container_string",
			context: "container",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
container: "%s"
steps:
  - name: ok
    run: "true"
`, ref))
			},
		},
		{
			name:    "root_container_exec",
			context: "container.exec",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
container:
  exec: "%s"
steps:
  - name: ok
    run: "true"
`, ref))
			},
		},
		{
			name:    "root_container_image",
			context: "container.image",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
container:
  image: "example/%s:latest"
steps:
  - name: ok
    run: "true"
`, ref))
			},
		},
	}
}

func spec005StepValueFieldCases() []spec005ValueFieldCase {
	return []spec005ValueFieldCase{
		{
			name:    "step_run_string",
			context: "steps[0].run",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
steps:
  - name: run-string
    run: "echo '%s'"
`, ref))
			},
		},
		{
			name:    "step_run_array",
			context: "steps[0].run",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
steps:
  - name: run-array
    run:
      - "echo '%s'"
`, ref))
			},
		},
		{
			name:    "step_with_nested",
			context: "steps[0].with.headers.authorization",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
steps:
  - name: with-nested
    action: http.request
    with:
      method: GET
      url: "https://example.test"
      headers:
        authorization: "Bearer %s"
`, ref))
			},
		},
		{
			name:    "step_working_dir",
			context: "steps[0].working_dir",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
steps:
  - name: step-working-dir
    working_dir: "./%s"
    run: "true"
`, ref))
			},
		},
		{
			name:    "step_env_map",
			context: "steps[0].env",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
steps:
  - name: step-env-map
    env:
      STEP_VALUE: "%s"
    run: "true"
`, ref))
			},
		},
		{
			name:    "step_env_array_map",
			context: "steps[0].env",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
steps:
  - name: step-env-array-map
    env:
      - STEP_VALUE: "%s"
    run: "true"
`, ref))
			},
		},
		{
			name:    "step_env_key_value",
			context: "steps[0].env",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
steps:
  - name: step-env-key-value
    env:
      - STEP_VALUE=%s
    run: "true"
`, ref))
			},
		},
		{
			name:    "step_precondition",
			context: "steps[0].preconditions",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
steps:
  - name: step-precondition
    preconditions:
      - condition: "test '%s' = prod"
    run: "true"
`, ref))
			},
		},
		{
			name:    "step_repeat_condition",
			context: "steps[0].repeat_policy.condition",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
steps:
  - name: step-repeat-condition
    repeat_policy:
      repeat: while
      condition: "test '%s' = prod"
      interval_sec: 1
      limit: 1
    run: "true"
`, ref))
			},
		},
		{
			name:    "step_parallel_variable",
			context: "steps[0].parallel",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
steps:
  - name: parallel-variable
    action: dag.run
    parallel: "%s"
    with:
      dag: child
`, ref))
			},
		},
		{
			name:    "step_parallel_items_value",
			context: "steps[0].parallel.items",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
steps:
  - name: parallel-items-value
    action: dag.run
    parallel:
      items:
        - "%s"
      max_concurrent: 1
    with:
      dag: child
`, ref))
			},
		},
		{
			name:    "step_parallel_items_params",
			context: "steps[0].parallel.items",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
steps:
  - name: parallel-items-params
    action: dag.run
    parallel:
      items:
        - target: "%s"
      max_concurrent: 1
    with:
      dag: child
`, ref))
			},
		},
		{
			name:    "step_stdout_path",
			context: "steps[0].stdout",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
steps:
  - name: stdout-path
    run: "echo ok"
    stdout: "stdout-%s.txt"
`, ref))
			},
		},
		{
			name:    "step_stdout_artifact",
			context: "steps[0].stdout.artifact",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
steps:
  - name: stdout-artifact
    run: "echo ok"
    stdout:
      artifact: "artifacts/%s/stdout.txt"
`, ref))
			},
		},
		{
			name:    "step_stderr_path",
			context: "steps[0].stderr",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
steps:
  - name: stderr-path
    run: "echo ok"
    stderr: "stderr-%s.txt"
`, ref))
			},
		},
		{
			name:    "step_stderr_artifact",
			context: "steps[0].stderr.artifact",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
steps:
  - name: stderr-artifact
    run: "echo ok"
    stderr:
      artifact: "artifacts/%s/stderr.txt"
`, ref))
			},
		},
		{
			name:    "step_stdout_outputs_value",
			context: "steps[0].stdout.outputs.fields.status.value",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
steps:
  - name: stdout-outputs-value
    run: "echo ok"
    stdout:
      outputs:
        fields:
          status:
            value: "%s"
`, ref))
			},
		},
		{
			name:    "step_output_value",
			context: "steps[0].output.result.value",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
steps:
  - name: output-value
    run: "echo ok"
    output:
      result:
        value: "%s"
`, ref))
			},
		},
		{
			name:    "step_output_path",
			context: "steps[0].output.report.path",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
steps:
  - name: output-path
    run: "echo ok"
    output:
      report:
        from: file
        path: "outputs/%s/report.json"
        decode: json
`, ref))
			},
		},
		{
			name:    "step_container_string",
			context: "steps[0].container",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
steps:
  - name: step-container-string
    container: "%s"
    run: "true"
`, ref))
			},
		},
		{
			name:    "step_container_exec",
			context: "steps[0].container.exec",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
steps:
  - name: step-container-exec
    container:
      exec: "%s"
    run: "true"
`, ref))
			},
		},
		{
			name:    "step_container_image",
			context: "steps[0].container.image",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
steps:
  - name: step-container-image
    container:
      image: "example/%s:latest"
    run: "true"
`, ref))
			},
		},
	}
}

type spec005ContainerObjectFieldCase struct {
	name    string
	context string
	body    func(ref string) string
}

func spec005RootContainerObjectFieldCases() []spec005ValueFieldCase {
	objectFields := spec005ContainerObjectFieldCases("ROOT_CONTAINER_VALUE")
	cases := make([]spec005ValueFieldCase, 0, len(objectFields))
	for _, field := range objectFields {
		field := field
		cases = append(cases, spec005ValueFieldCase{
			name:    "root_container_" + field.name,
			context: "container." + field.context,
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
container:
%s
steps:
  - name: ok
    run: "true"
`, indentSpec005(field.body(ref), "  ")))
			},
		})
	}
	return cases
}

func spec005StepContainerObjectFieldCases() []spec005ValueFieldCase {
	objectFields := spec005ContainerObjectFieldCases("STEP_CONTAINER_VALUE")
	cases := make([]spec005ValueFieldCase, 0, len(objectFields))
	for _, field := range objectFields {
		field := field
		cases = append(cases, spec005ValueFieldCase{
			name:    "step_container_" + field.name,
			context: "steps[0].container." + field.context,
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
steps:
  - name: step-container-%s
    container:
%s
    run: "true"
`, field.name, indentSpec005(field.body(ref), "      ")))
			},
		})
	}
	return cases
}

func spec005ContainerObjectFieldCases(envName string) []spec005ContainerObjectFieldCase {
	return []spec005ContainerObjectFieldCase{
		{
			name:    "name",
			context: "name",
			body: func(ref string) string {
				return fmt.Sprintf(`image: "alpine:3.20"
name: "dagu-%s"`, ref)
			},
		},
		{
			name:    "user",
			context: "user",
			body: func(ref string) string {
				return fmt.Sprintf(`image: "alpine:3.20"
user: "%s"`, ref)
			},
		},
		{
			name:    "working_dir",
			context: "working_dir",
			body: func(ref string) string {
				return fmt.Sprintf(`image: "alpine:3.20"
working_dir: "/work/%s"`, ref)
			},
		},
		{
			name:    "network",
			context: "network",
			body: func(ref string) string {
				return fmt.Sprintf(`image: "alpine:3.20"
network: "%s"`, ref)
			},
		},
		{
			name:    "volumes",
			context: "volumes",
			body: func(ref string) string {
				return fmt.Sprintf(`image: "alpine:3.20"
volumes:
  - "/tmp/%s:/tmp/%s"`, ref, ref)
			},
		},
		{
			name:    "ports",
			context: "ports",
			body: func(ref string) string {
				return fmt.Sprintf(`image: "alpine:3.20"
ports:
  - "%s:80"`, ref)
			},
		},
		{
			name:    "env",
			context: "env",
			body: func(ref string) string {
				return fmt.Sprintf(`image: "alpine:3.20"
env:
  %s: "%s"`, envName, ref)
			},
		},
		{
			name:    "command",
			context: "command",
			body: func(ref string) string {
				return fmt.Sprintf(`image: "alpine:3.20"
command:
  - "%s"`, ref)
			},
		},
		{
			name:    "shell",
			context: "shell",
			body: func(ref string) string {
				return fmt.Sprintf(`image: "alpine:3.20"
shell:
  - "%s"`, ref)
			},
		},
	}
}

type spec005HandlerOwnedFieldCase struct {
	name    string
	context string
	body    func(ref string) string
}

func spec005HandlerValueFieldCases() []spec005ValueFieldCase {
	handlers := []string{"init", "success", "failure", "abort", "exit", "wait"}
	ownedFields := spec005HandlerOwnedFieldCases()
	cases := make([]spec005ValueFieldCase, 0, len(handlers)*len(ownedFields))
	for _, handler := range handlers {
		handler := handler
		for _, owned := range ownedFields {
			owned := owned
			cases = append(cases, spec005ValueFieldCase{
				name:    "handler_" + handler + "_" + owned.name,
				context: "handler_on." + handler + "." + owned.context,
				yaml: func(ref string) string {
					return spec005HandlerDAG(handler, owned.body(ref))
				},
			})
		}
	}
	return cases
}

func spec005HandlerOwnedFieldCases() []spec005HandlerOwnedFieldCase {
	cases := []spec005HandlerOwnedFieldCase{
		{
			name:    "run_string",
			context: "run",
			body: func(ref string) string {
				return fmt.Sprintf(`run: "echo '%s'"`, ref)
			},
		},
		{
			name:    "run_array",
			context: "run",
			body: func(ref string) string {
				return fmt.Sprintf(`run:
  - "echo '%s'"`, ref)
			},
		},
		{
			name:    "with_nested",
			context: "with.headers.authorization",
			body: func(ref string) string {
				return fmt.Sprintf(`action: http.request
with:
  method: GET
  url: "https://example.test"
  headers:
    authorization: "Bearer %s"`, ref)
			},
		},
		{
			name:    "working_dir",
			context: "working_dir",
			body: func(ref string) string {
				return fmt.Sprintf(`working_dir: "./%s"
run: "true"`, ref)
			},
		},
		{
			name:    "env_map",
			context: "env",
			body: func(ref string) string {
				return fmt.Sprintf(`env:
  HANDLER_VALUE: "%s"
run: "true"`, ref)
			},
		},
		{
			name:    "env_array_map",
			context: "env",
			body: func(ref string) string {
				return fmt.Sprintf(`env:
  - HANDLER_VALUE: "%s"
run: "true"`, ref)
			},
		},
		{
			name:    "env_key_value",
			context: "env",
			body: func(ref string) string {
				return fmt.Sprintf(`env:
  - HANDLER_VALUE=%s
run: "true"`, ref)
			},
		},
		{
			name:    "precondition",
			context: "preconditions",
			body: func(ref string) string {
				return fmt.Sprintf(`preconditions:
  - condition: "test '%s' = prod"
run: "true"`, ref)
			},
		},
		{
			name:    "repeat_condition",
			context: "repeat_policy.condition",
			body: func(ref string) string {
				return fmt.Sprintf(`repeat_policy:
  repeat: while
  condition: "test '%s' = prod"
  interval_sec: 1
  limit: 1
run: "true"`, ref)
			},
		},
		{
			name:    "parallel_variable",
			context: "parallel",
			body: func(ref string) string {
				return fmt.Sprintf(`action: dag.run
parallel: "%s"
with:
  dag: child`, ref)
			},
		},
		{
			name:    "parallel_items_value",
			context: "parallel.items",
			body: func(ref string) string {
				return fmt.Sprintf(`action: dag.run
parallel:
  items:
    - "%s"
  max_concurrent: 1
with:
  dag: child`, ref)
			},
		},
		{
			name:    "parallel_items_params",
			context: "parallel.items",
			body: func(ref string) string {
				return fmt.Sprintf(`action: dag.run
parallel:
  items:
    - target: "%s"
  max_concurrent: 1
with:
  dag: child`, ref)
			},
		},
		{
			name:    "stdout_path",
			context: "stdout",
			body: func(ref string) string {
				return fmt.Sprintf(`run: "echo ok"
stdout: "stdout-%s.txt"`, ref)
			},
		},
		{
			name:    "stdout_artifact",
			context: "stdout.artifact",
			body: func(ref string) string {
				return fmt.Sprintf(`run: "echo ok"
stdout:
  artifact: "artifacts/%s/stdout.txt"`, ref)
			},
		},
		{
			name:    "stderr_path",
			context: "stderr",
			body: func(ref string) string {
				return fmt.Sprintf(`run: "echo ok"
stderr: "stderr-%s.txt"`, ref)
			},
		},
		{
			name:    "stderr_artifact",
			context: "stderr.artifact",
			body: func(ref string) string {
				return fmt.Sprintf(`run: "echo ok"
stderr:
  artifact: "artifacts/%s/stderr.txt"`, ref)
			},
		},
		{
			name:    "stdout_outputs_value",
			context: "stdout.outputs.fields.status.value",
			body: func(ref string) string {
				return fmt.Sprintf(`run: "echo ok"
stdout:
  outputs:
    fields:
      status:
        value: "%s"`, ref)
			},
		},
		{
			name:    "output_value",
			context: "output.result.value",
			body: func(ref string) string {
				return fmt.Sprintf(`run: "echo ok"
output:
  result:
    value: "%s"`, ref)
			},
		},
		{
			name:    "output_path",
			context: "output.report.path",
			body: func(ref string) string {
				return fmt.Sprintf(`run: "echo ok"
output:
  report:
    from: file
    path: "outputs/%s/report.json"
    decode: json`, ref)
			},
		},
		{
			name:    "container_string",
			context: "container",
			body: func(ref string) string {
				return fmt.Sprintf(`container: "%s"
run: "true"`, ref)
			},
		},
		{
			name:    "container_exec",
			context: "container.exec",
			body: func(ref string) string {
				return fmt.Sprintf(`container:
  exec: "%s"
run: "true"`, ref)
			},
		},
		{
			name:    "container_image",
			context: "container.image",
			body: func(ref string) string {
				return fmt.Sprintf(`container:
  image: "example/%s:latest"
run: "true"`, ref)
			},
		},
	}

	cases = append(cases, spec005HandlerContainerObjectOwnedFieldCases()...)
	return cases
}

func spec005HandlerContainerObjectOwnedFieldCases() []spec005HandlerOwnedFieldCase {
	objectFields := spec005ContainerObjectFieldCases("HANDLER_CONTAINER_VALUE")
	cases := make([]spec005HandlerOwnedFieldCase, 0, len(objectFields))
	for _, field := range objectFields {
		field := field
		cases = append(cases, spec005HandlerOwnedFieldCase{
			name:    "container_" + field.name,
			context: "container." + field.context,
			body: func(ref string) string {
				return fmt.Sprintf(`container:
%s
run: "true"`, indentSpec005(field.body(ref), "  "))
			},
		})
	}
	return cases
}

func spec005RuntimeDAG(body string) string {
	return fmt.Sprintf(`params:
  - name: environment
    description: Runtime environment.
  - name: shell
    description: Runtime shell.
  - name: shell_arg
    description: Runtime shell argument.

%s
`, body)
}

func spec005ParamsEvalDAG(ref string) string {
	return fmt.Sprintf(`params:
  - name: environment
    description: Runtime environment.
  - name: service
    eval: $(printf '%%s-api' %s)

working_dir: .
steps:
  - name: ok
    run: "true"
`, ref)
}

func spec005ParamsEvalRuntimeDAG() string {
	return `params:
  - name: environment
    description: Runtime environment.
  - name: service
    eval: $(printf '%s-api' ${params.environment})

working_dir: .
steps:
  - name: write
    run: |
      printf '%s\n' '${params.service}' > params-eval.txt
`
}

func spec005DAG(body string) string {
	return fmt.Sprintf(`params:
  - name: environment
    default: prod

%s
`, body)
}

func spec005HandlerDAG(handler string, body string) string {
	return spec005DAG(fmt.Sprintf(`handler_on:
  %s:
%s
steps:
  - name: ok
    run: "true"
`, handler, indentSpec005(body, "    ")))
}

func spec005HandlerRuntimeDAG(handler string, body string) string {
	steps := `  - name: ok
    run: "true"`
	if handler == "failure" {
		steps = `  - name: fail
    run: "false"`
	}

	return spec005RuntimeDAG(fmt.Sprintf(`working_dir: .
handler_on:
  %s:
%s
steps:
%s
`, handler, indentSpec005(body, "    "), steps))
}

func indentSpec005(value string, prefix string) string {
	var out strings.Builder
	for i, ch := range value {
		if i == 0 {
			out.WriteString(prefix)
		}
		out.WriteRune(ch)
		if ch == '\n' {
			out.WriteString(prefix)
		}
	}
	return out.String()
}

func mkdirSpec005ProjectDir(t *testing.T, dagu *Runner, name string) {
	t.Helper()

	path := filepath.Join(dagu.dir, filepath.FromSlash(name))
	if err := os.MkdirAll(path, 0o750); err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}
}

func writeSpec005ProjectExecutable(t *testing.T, dagu *Runner, name string, content string) {
	t.Helper()

	path := filepath.Join(dagu.dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("creating parent for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func writeSpec005ProjectFile(t *testing.T, dagu *Runner, name string, content string) {
	t.Helper()

	path := filepath.Join(dagu.dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("creating parent for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func writeSpec005DAG(t *testing.T, dagu *Runner, name string, content string) string {
	t.Helper()

	path := filepath.Join(dagu.dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return name
}
