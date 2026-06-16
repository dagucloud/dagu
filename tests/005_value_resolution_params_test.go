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

	for _, tc := range spec005ValueFieldCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := newRunner(t, "005_value_resolution_params")
			file := writeSpec005DAG(t, dagu, "valid_"+tc.name+".yaml", tc.yaml(`${params.environment}`))

			result := dagu.Run("validate", file)
			result.ExpectExitCode(0)
			result.ExpectStdout("")
			result.ExpectStderr("")
			dagu.ExpectNoFile("executed.txt")
		})
	}
}

func Test005ValueResolutionParamsValidateRejectsInvalidReferencesByField(t *testing.T) {
	t.Parallel()

	for _, tc := range spec005ValueFieldCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			t.Run("undeclared", func(t *testing.T) {
				t.Parallel()

				dagu := newRunner(t, "005_value_resolution_params")
				file := writeSpec005DAG(t, dagu, "undeclared_"+tc.name+".yaml", tc.yaml(`${params.missing}`))

				result := dagu.Run("validate", file)
				result.ExpectExitCode(1)
				result.ExpectStdout("")
				result.ExpectStderrContains(tc.context, "${params.missing}")
				result.ExpectStderrNotContains("Usage:")
				dagu.ExpectNoFile("executed.txt")
			})

			t.Run("shorthand", func(t *testing.T) {
				t.Parallel()

				dagu := newRunner(t, "005_value_resolution_params")
				file := writeSpec005DAG(t, dagu, "shorthand_"+tc.name+".yaml", tc.yaml(`$params.environment`))

				result := dagu.Run("validate", file)
				result.ExpectExitCode(1)
				result.ExpectStdout("")
				result.ExpectStderrContains(tc.context, "$params.environment")
				result.ExpectStderrNotContains("Usage:")
				dagu.ExpectNoFile("executed.txt")
			})
		})
	}
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
			name:        "params declarations do not support references",
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

func Test005ValueResolutionParamsStartRejectsMissingRuntimeValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		file        string
		absentFile  string
		stderrParts []string
	}{
		{
			name:        "step run reference fails before step starts",
			file:        "missing_runtime_value_step_run.yaml",
			absentFile:  "should-not-exist-run.txt",
			stderrParts: []string{"params.environment"},
		},
		{
			name:        "root env reference fails before step starts",
			file:        "missing_runtime_value_root_env.yaml",
			absentFile:  "should-not-exist-root-env.txt",
			stderrParts: []string{"params.environment"},
		},
		{
			name:        "action input reference fails before action writes",
			file:        "missing_runtime_value_with_action.yaml",
			absentFile:  "should-not-exist-with-action.txt",
			stderrParts: []string{"params.environment"},
		},
		{
			name:        "stdout path reference fails before command starts",
			file:        "missing_runtime_value_stdout_path.yaml",
			absentFile:  "should-not-exist-stdout.txt",
			stderrParts: []string{"params.environment"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := newRunner(t, "005_value_resolution_params")

			result := dagu.Run("start", tc.file)
			result.ExpectExitCode(1)
			result.ExpectStderrContains(tc.stderrParts...)
			result.ExpectStderrNotContains("Usage:")
			dagu.ExpectNoFile(tc.absentFile)
		})
	}
}

type spec005ValueFieldCase struct {
	name    string
	context string
	yaml    func(ref string) string
}

func spec005ValueFieldCases() []spec005ValueFieldCase {
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
			name:    "root_shell_args",
			context: "shell_args",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
shell: sh
shell_args:
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
		{
			name:    "root_container_object_fields",
			context: "container",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
container:
  image: "alpine:3.20"
  name: "dagu-%s"
  user: "%s"
  working_dir: "/work/%s"
  network: "%s"
  volumes:
    - "/tmp/%s:/tmp/%s"
  ports:
    - "%s:80"
  env:
    ROOT_CONTAINER_VALUE: "%s"
  command:
    - "%s"
  shell:
    - "%s"
steps:
  - name: ok
    run: "true"
`, ref, ref, ref, ref, ref, ref, ref, ref, ref, ref))
			},
		},
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
			name:    "step_stdout_outputs_path",
			context: "steps[0].stdout.outputs.fields.report.path",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
steps:
  - name: stdout-outputs-path
    run: "echo ok"
    stdout:
      outputs:
        fields:
          report:
            from: file
            path: "outputs/%s/report.json"
            decode: json
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
		{
			name:    "step_container_object_fields",
			context: "steps[0].container",
			yaml: func(ref string) string {
				return spec005DAG(fmt.Sprintf(`
steps:
  - name: step-container-object-fields
    container:
      image: "alpine:3.20"
      name: "dagu-%s"
      user: "%s"
      working_dir: "/work/%s"
      network: "%s"
      volumes:
        - "/tmp/%s:/tmp/%s"
      ports:
        - "%s:80"
      env:
        STEP_CONTAINER_VALUE: "%s"
      command:
        - "%s"
      shell:
        - "%s"
    run: "true"
`, ref, ref, ref, ref, ref, ref, ref, ref, ref, ref))
			},
		},
		{
			name:    "handler_init_run",
			context: "handler_on.init",
			yaml: func(ref string) string {
				return spec005HandlerDAG("init", fmt.Sprintf(`run: "echo '%s'"`, ref))
			},
		},
		{
			name:    "handler_success_env",
			context: "handler_on.success",
			yaml: func(ref string) string {
				return spec005HandlerDAG("success", fmt.Sprintf(`run: "true"
env:
  HANDLER_VALUE: "%s"`, ref))
			},
		},
		{
			name:    "handler_failure_precondition",
			context: "handler_on.failure",
			yaml: func(ref string) string {
				return spec005HandlerDAG("failure", fmt.Sprintf(`run: "true"
preconditions:
  - condition: "test '%s' = prod"`, ref))
			},
		},
		{
			name:    "handler_abort_repeat_condition",
			context: "handler_on.abort",
			yaml: func(ref string) string {
				return spec005HandlerDAG("abort", fmt.Sprintf(`run: "true"
repeat_policy:
  repeat: while
  condition: "test '%s' = prod"
  interval_sec: 1
  limit: 1`, ref))
			},
		},
		{
			name:    "handler_exit_stdout_artifact",
			context: "handler_on.exit",
			yaml: func(ref string) string {
				return spec005HandlerDAG("exit", fmt.Sprintf(`run: "true"
stdout:
  artifact: "handlers/%s/exit.out"`, ref))
			},
		},
		{
			name:    "handler_wait_output",
			context: "handler_on.wait",
			yaml: func(ref string) string {
				return spec005HandlerDAG("wait", fmt.Sprintf(`run: "true"
output:
  result:
    value: "%s"`, ref))
			},
		},
	}
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

func writeSpec005DAG(t *testing.T, dagu *Runner, name string, content string) string {
	t.Helper()

	path := filepath.Join(dagu.dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return name
}
