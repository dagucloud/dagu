# Spec: Step Run

Scope: local command execution through the step `run` field.

This spec defines only `run`. It does not define `command`, `script`, `exec`, executor `with` fields, or the step-level `shell` field.

## Objective

Define how Dagu runs a local command on the data-plane runner host.

A step with `run` is a shell command. Dagu value-resolves the command string, then passes it to the platform shell.

## Input

Input is a workflow YAML file accepted by the YAML schema spec.

Step `run` validation extends:

```sh
dagu validate <path/to/dag_file>
```

Workflow execution uses:

```sh
dagu run <workflow_target>
```

Rules:

- Validation checks the shape of `run` and statically checkable value references.
- Validation must not execute commands.
- Validation must not check whether the command path exists.
- `dagu run` accepts project workflow targets as defined by the project spec.
- Dagu must not append `.yaml` or `.yml` to extensionless workflow targets.
- Dagu must not fall back from an unknown project workflow target to another filesystem path.
- `dagu run` executes a step command only when that step is ready to start.

Example:

```yaml
steps:
  - id: build
    run: ./scripts/build.sh ${params.version}
```

## Field shape

| Field | Required | Rule |
| --- | --- | --- |
| `run` | No | Must be a string when present. The decoded string must contain at least one non-whitespace character. |

Dagu does not run command-substitution syntax while evaluating this field.

## Command execution

Rules:

- A step with `run` uses the local command executor.
- The command runs on the data-plane runner host.
- Dagu passes the command string to the platform shell as one command string.
- Dagu must not split `run` into argv.
- Multi-line `run` strings are valid.
- Line breaks in a multi-line `run` string are preserved.
- Command path lookup is done by the shell or operating system from the step process working directory.
- This spec does not define a step-level `shell` field.
- To use a different shell, invoke that shell inside `run`.

Platform shell:

| Platform | Shell |
| --- | --- |
| POSIX | `/bin/sh -c` |
| Windows | `%ComSpec% /S /C`, or `cmd.exe /S /C` when `ComSpec` is unset. |

## Value resolution

Dagu resolves Dagu references before command execution.

Rules:

- Value resolution for `run` follows the value resolution spec and field evaluation spec.
- If value resolution fails, the command must not start.
- Dagu must not resolve shell-style `$NAME` syntax.
- Dagu must leave `$()` and backtick text unchanged.
- Shell variables and command-substitution text remain in the command string passed to the platform shell.

Example:

```yaml
params:
  - name: version
    type: string
    required: true
steps:
  - id: build
    run: ./scripts/build.sh ${params.version}
```

## Working directory

The project spec defines the step process working directory.

Rules:

- In project mode, the default step process working directory is `project_root`.
- Root `working_dir` changes the default step process working directory as defined by the project spec.
- This spec does not define a step-level `working_dir` field.

## Environment

Rules:

- The command inherits the Dagu process environment.
- Dagu-defined environment variables override inherited variables with the same name.
- If the step declares top-level `outputs`, Dagu sets `DAGU_OUTPUT_FILE` to the output file path defined by the step outputs spec.
- If the step does not declare top-level `outputs`, `DAGU_OUTPUT_FILE` must be absent from the command environment.

## Exit, abort, and timeout

Rules:

- Exit code `0` means command execution succeeded.
- A non-zero exit code means command execution failed.
- Termination by signal or platform termination request means command execution failed.
- If output parsing fails after the command exits successfully, the step attempt fails as defined by the step outputs spec.
- If the workflow run is aborted while the command is running, Dagu must terminate the command.
- If runtime timeout behavior stops the step while the command is running, Dagu must terminate the command.
- A step must not reach a terminal state while its command process is still running.
- Outputs from an aborted or timed-out command attempt must not be published.

## Outputs

Dagu captures command stdout and stderr.

Captured stdout and stderr are runtime outputs. They are not step outputs under the step outputs spec.

Step outputs are published only through the step outputs spec. This spec does not define durable result storage.

## Errors

Validation must fail when:

- `run` is not a string.
- `run` is empty or whitespace only.
- `run` contains a malformed Dagu value reference that is statically checkable.
- `run` contains an output reference that violates the step outputs spec.

Runtime must fail when:

- Value resolution in `run` fails.
- The command cannot be started.
- The shell cannot find the command path.
- The command exits with a non-zero code.
- The command is terminated by abort or timeout.
- The command emits invalid declared outputs.

## Examples

Simple command:

```yaml
steps:
  - id: hello
    run: echo hello
```

Multi-line command:

```yaml
steps:
  - id: build
    run: |
      set -eu
      ./scripts/build.sh
      ./scripts/test.sh
```

Shell variables are expanded by the shell, not by Dagu:

```yaml
steps:
  - id: home
    run: echo "$HOME"
```

Command with declared step output:

```yaml
steps:
  - id: build
    run: |
      printf 'image_tag=v1.2.3\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: image_tag
```

Use another shell explicitly:

```yaml
steps:
  - id: bash_example
    run: bash -lc 'printf "%s\n" "$BASH_VERSION"'
```

Invalid empty command:

```yaml
steps:
  - id: bad
    run: ""
```

Invalid non-string command:

```yaml
steps:
  - id: bad
    run: [echo, hello]
```

## Acceptance criteria

- A black-box fixture verifies `dagu validate` accepts a step with a simple `run` string.
- A black-box fixture verifies `dagu validate` accepts a multi-line `run` string.
- A black-box fixture verifies `dagu validate` rejects a non-string `run`.
- A black-box fixture verifies `dagu validate` rejects an empty `run`.
- A black-box fixture verifies `dagu validate` accepts `$()` text in `run` and does not execute it.
- A black-box fixture verifies `dagu validate` accepts backtick text in `run` and does not execute it.
- A black-box fixture verifies `dagu validate` does not execute `run`.
- A black-box fixture verifies `dagu validate` does not require the command path to exist.
- A black-box fixture verifies `dagu run` accepts a discovered project workflow target.
- A black-box fixture verifies `dagu run` does not append `.yaml` or `.yml` to an extensionless workflow target.
- A black-box fixture verifies `dagu run` does not fall back from an unknown project workflow target to any other filesystem path.
- A black-box fixture verifies `dagu run` executes a simple `run` command.
- A black-box fixture verifies `dagu run` executes from `project_root` by default.
- A black-box fixture verifies `dagu run` respects root `working_dir`.
- A black-box fixture verifies `dagu run` resolves Dagu references before command execution.
- A black-box fixture verifies `$NAME` remains available for shell expansion.
- A black-box fixture verifies command stdout is captured.
- A black-box fixture verifies command stderr is captured.
- A black-box fixture verifies stdout is not treated as a step output.
- A black-box fixture verifies non-zero command exit fails the step.
- A black-box fixture verifies a command path that the shell cannot find fails the step.
- A black-box fixture verifies `DAGU_OUTPUT_FILE` is set when the step declares top-level `outputs`.
- A black-box fixture verifies `DAGU_OUTPUT_FILE` is absent when the step does not declare top-level `outputs`.
- A black-box fixture verifies abort terminates the running command.
- A black-box fixture verifies timeout termination fails the running command.
