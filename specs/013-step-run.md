# Spec: Step Run

## Implementation Status

Partially implemented. This spec describes target conformance behavior and
compatibility behavior that implementations must preserve while those forms
remain accepted.

## Scope

This spec defines local command execution through the step `run` field.

The canonical target form is string `run`.

This spec also defines compatibility behavior for accepted `run` forms that
normalize through command and script execution paths.

This spec does not define:

- The `action: exec` direct argv execution contract.
- Non-`run` execution field syntax, except where `run` compatibility normalization makes the behavior observable through `run`.
- Action-specific `with` inputs.
- Executor-specific container or remote process models.

Executor-specific behavior, such as Docker command wrapping or SSH remote script
construction, must be defined by the owning executor spec when those executors
reuse command parsing.

## Goal

Workflow authors can use `run` for local shell commands with a predictable boundary between Dagu value resolution and shell evaluation.

## Motivation

The `run` field contains shell command text. Dagu must be explicit about which syntax it owns and which syntax remains owned by the shell.

This prevents ambiguity between:

- Dagu-owned references, such as `${consts.name}`, `${params.name}`, `${env.NAME}`, and `${steps.build.outputs.image}`.
- Shell syntax, such as `$NAME`, `${NAME}`, `$()`, backticks, pipes, redirects, and command chaining.
- Direct argv execution, which belongs to an exec action spec, not the canonical `run` string contract.

## Related Specs

- YAML schema: [Spec 002: YAML Schema](002-yaml-schema.md)
- Value resolution: [Spec 003: Value Resolution](003-value-resolution.md)
- Field evaluation: [Spec 010: Field Evaluation](010-field-evaluation.md)
- Dynamic evaluation: [Spec 011: Dynamic Evaluation](011-dynamic-evaluation.md)
- Step outputs: [Spec 012: Step Outputs](012-step-outputs.md)

## Behavior

### Field Shape

- `run` is optional.

- The canonical target form of `run` is a string.

- When `run` is a string, the decoded string must contain at least one non-whitespace character.

- Multi-line `run` strings are valid.

- Line breaks in multi-line `run` strings are preserved.

- Compatibility array form is accepted.

- Compatibility array-form `run` reuses existing command-array normalization.

- Compatibility array-form `run` accepts string items.

- Compatibility array-form `run` accepts primitive scalar items and converts them to strings.

- Compatibility array-form `run` accepts single-key mapping items whose values are primitive scalars and converts them to command strings in `key: value` form.

- Empty or whitespace-only string entries are ignored.

- Multi-key mapping items, nested mapping items, and nested array items must fail validation.

- Compatibility array form must continue to be accepted until a migration explicitly removes it.

### Execution Model

- A step with `run` uses the local command executor.

- The command runs on the data-plane runner host.

- `run` is shell command text, not an argv list.

- For command-string execution, Dagu passes the resolved `run` string to the selected shell as one command string.

- Dagu must not expose canonical string `run` as a user-authored argv list.

- Canonical direct process execution without shell parsing is outside this spec and belongs to an exec action spec.

- Command path lookup is performed by the shell or operating system from the step process working directory.

### Compatibility Execution Forms

Compatibility execution normalizes `run` through command and script execution paths:

- A single-line string `run` is parsed into a command entry while preserving the original command string for shell execution.

- A multi-line string `run` is materialized as a temporary executable script file.

- An array-form `run` is a compatibility form for multiple sequential command entries.

- Empty normalized array entries are ignored.

- If all array entries are empty, validation must fail.

- Multiple command entries run sequentially.

- Multiple command execution stops at the first failed command.

- Direct execution compatibility forms outside canonical `run` are outside this spec but must not be silently reinterpreted as Dagu command substitution.

### Shell Selection

#### Selection Order

- Shell selection order is:

  1. Step `with.shell`.
  2. Root `shell`.
  3. Runtime default shell configuration, including `DAGU_DEFAULT_SHELL`.
  4. Platform discovery fallback.

#### Run Shell Inputs

- `with.shell` selects a different shell for a `run` step.

- `with.shell_args` selects shell arguments for command-string execution and for shell builders that consume shell arguments during script-file execution.

- `with.shell_packages` selects shell packages for a `run` step when the selected shell supports packages.

- Accepted `with` keys for `run` are `shell`, `shell_args`, and `shell_packages`.

- A `run` step must not use any other `with` key.

#### Platform Fallback

- On Unix-like platforms, platform discovery uses `$SHELL` when set and falls back to `sh` when available.

- On Windows, platform discovery prefers PowerShell Core, then Windows PowerShell, then `cmd`.

### Shell Construction

#### Common Rules

- Dagu constructs the final process argv with shell-specific builders.

- Shell-specific builders assemble process argv from the selected shell, configured shell arguments, `run` form, and configured shell packages.

- Shell-specific builders include compatibility defaults required for reliable non-interactive execution.

- Shell builder defaults are part of runtime compatibility and must be covered by black-box tests before being changed.

#### Unix-Like Shells

- Unix-like shell command-string execution includes `-c`; if configured shell arguments already include `-c`, that existing flag is used.

- Unix-like shell command-string and script-file execution include `-e` when the selected shell is Unix-like, no step-level shell is explicitly specified, and configured shell arguments do not already include `-e`.

#### PowerShell

- PowerShell command-string execution includes `-NoProfile` and `-NonInteractive` unless configured shell arguments already include them.

- PowerShell command-string execution includes `-Command` unless configured shell arguments already include `-Command` or `-C`.

- PowerShell script-file execution invokes the script with `-ExecutionPolicy Bypass -File` and includes the missing non-interactive and no-profile flags.

- PowerShell script content includes a preamble that normalizes error handling and UTF-8 encoding.

#### Windows cmd

- `cmd` command-string and script-file execution include `/c` unless configured shell arguments already include `/c` or `/C`.

- `cmd` resolves the configured `COMSPEC` path when the selected shell is `cmd` or `cmd.exe` and `COMSPEC` points to an existing path.

#### Nix Shell

- Nix shell execution adds `-p <package>` for each configured shell package.

- Nix shell execution includes `--pure` unless configured shell arguments already include `--pure` or `--impure`.

- Nix shell execution includes `--run` unless configured shell arguments already include it.

### Script Files And Shebang

#### Script File Creation

- Compatibility script-file execution normalizes multi-line `run` values through a script-file execution path.

- In that path, Dagu writes the resolved script text to a temporary executable file.

- Script-file compatibility creates the temporary script in the resolved working directory when available.

- If that working directory does not exist, script-file setup fails before process startup.

- Temporary script file create, write, sync, or permission failures must fail setup.

- The temporary script file extension is selected from the shell when shell-specific script execution requires an extension.

- Shell-specific preprocessing is applied before writing the temporary script file when the selected shell requires it and the script is not delegated directly to a shebang interpreter.

#### Shebang Selection

- If the first line of the temporary script file contains a shebang and no step-level shell is explicitly specified, Dagu must invoke the shebang interpreter directly with the temporary script path.

- A root shell does not count as an explicitly specified step-level shell for shebang suppression.

- If a step-level shell is explicitly specified, Dagu must execute the temporary script through that shell and must not use the shebang interpreter directly.

- Shebang behavior for an existing script file named inside a single-line `run` command is delegated to the selected shell or operating system.

#### Cleanup

- Temporary script cleanup is best-effort.

### Value Resolution

- Dagu resolves Dagu-owned references in `run` before command execution.

- Value resolution for `run` follows the value resolution spec and field evaluation spec.

- If value resolution fails, the command must not start.

- Dagu must not resolve shell-style `$NAME` or `${NAME}` environment variable syntax in `run`.

- Dagu-owned environment references in `run` must use the namespaced `${env.NAME}` form.

- Shell variable syntax remains in the command string passed to the shell.

- Shell command-substitution syntax remains in the command string passed to the shell.

### Command Substitution

- Dagu must not execute `$()` command substitution while evaluating `run`.

- Dagu must not execute backtick command substitution while evaluating `run`.

- Dagu must leave `$()` text unchanged in `run`.

- Dagu must leave backtick text unchanged in `run`.

- The selected shell owns `$()` and backtick execution after Dagu hands off the command string.

- Direct argv execution has no shell handoff, so `$()` and backticks remain literal argv text unless the invoked program interprets them.

- Dagu dynamic evaluation is available only where the dynamic evaluation spec explicitly allows it, such as `params[].eval`.

### Working Directory

#### Precedence

- Step-level `working_dir` has highest precedence for the step process working directory.

- Explicit root `working_dir` changes the default step process working directory.

#### Fallbacks

- If neither step-level `working_dir` nor explicit root `working_dir` is set, the per-run work directory exposed as `DAG_RUN_WORK_DIR` is used when available.

- If `DAG_RUN_WORK_DIR` is unavailable, the runtime falls back to the resolved non-explicit DAG working directory.

- If no DAG working directory can be resolved, the runtime falls back to the process working directory, then to the user home directory.

- Relative root working directories are resolved against the workflow file directory when the workflow is loaded.

#### Value Resolution

- The working directory path is value-resolved before the command starts.

#### Creation Timing

- For command-string execution, Dagu must ensure the step process working directory exists before starting the command.

- For script-file compatibility execution, temporary script setup occurs before process-start working directory creation.

### Environment

#### Runtime Environment

- The command inherits the runtime environment assembled for the step.

- Dagu-defined environment variables override inherited variables with the same name.

- Shell variables are expanded by the selected shell, not by Dagu value resolution.

#### Target Output File

- Under the target top-level `outputs` model defined by the step outputs spec, Dagu sets `DAGU_OUTPUT_FILE` before starting a command that declares outputs requiring that file.

- Under the target top-level `outputs` model, if the step does not declare outputs that require `DAGU_OUTPUT_FILE`, `DAGU_OUTPUT_FILE` must be absent from the command environment.

#### Compatibility Stream Files

- Compatibility behavior does not set `DAGU_OUTPUT_FILE`.

- Compatibility behavior exposes step stream file paths through `DAG_RUN_STEP_STDOUT_FILE` and `DAG_RUN_STEP_STDERR_FILE`.

### Runtime Results

#### Captured Streams

- Dagu captures command stdout and stderr.

- Captured stdout and stderr are runtime streams.

#### Target Outputs

- Under the target top-level `outputs` model, step outputs are published through the step outputs spec.

- Captured stdout is not automatically a step output under the target top-level `outputs` model.

#### Compatibility Outputs

- Compatibility output surfaces and stream redirects are outside this target `run` spec unless their owning specs include them.

### Exit, Abort, And Timeout

- Exit code `0` means command execution succeeded.

- A non-zero exit code means command execution failed.

- Termination by signal or platform termination request means command execution failed.

- If output parsing fails after the command exits successfully, the step attempt fails as defined by the step outputs spec.

- If the workflow run is aborted while the command is running, Dagu must terminate the command.

- If runtime timeout behavior stops the step while the command is running, Dagu must terminate the command.

- A step must not reach a terminal state while its command process is still running.

- Outputs from an aborted or timed-out command attempt must not be published.

### Validation Errors

Validation must fail when:

- `run` is neither a string nor a supported compatibility form.

- string `run` is empty or whitespace only.

- compatibility array-form `run` has no non-empty command entries.

- `run` contains a malformed Dagu-owned value reference that is statically checkable.

- `run` contains an invalid Dagu-looking shorthand reference.

- `run` contains an output reference that violates the step outputs spec.

- A `run` step contains a `with` key that is not accepted for `run`.

Validation must not:

- Execute `run`.

- Execute `$()` or backtick command substitution in `run`.

- Check whether the command path exists.

### Runtime Errors

Runtime must fail when:

- Value resolution in `run` fails.

- The shell cannot be started.

- The command cannot be started by the shell.

- The shell cannot find the command path.

- The command exits with a non-zero code.

- The command is terminated by abort or timeout.

- The command emits invalid declared outputs.

- A temporary script file cannot be created, written, synced, or permissioned when script-file execution is used.

- Shebang detection fails when script-file execution needs it.

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

Multi-line command with shebang:

```yaml
steps:
  - id: python_script
    run: |
      #!/usr/bin/env python3
      print("hello")
```

Compatibility array-form commands:

```yaml
steps:
  - id: checks
    run:
      - go test ./internal/core/...
      - go test ./internal/runtime/...
```

Dagu references are resolved before the shell runs:

```yaml
params:
  - name: version
    type: string
    required: true
steps:
  - id: build
    run: ./scripts/build.sh ${params.version}
```

Shell variables are expanded by the shell, not by Dagu:

```yaml
steps:
  - id: home
    run: echo "$HOME"
```

Backticks are preserved for the shell:

```yaml
steps:
  - id: today
    run: echo "Today is `date +%Y-%m-%d`"
```

Use `params[].eval` when Dagu should compute a value before `run`:

```yaml
params:
  - name: today
    type: string
    eval: `date +%Y-%m-%d`
  - name: branch
    type: string
    eval: $(git branch --show-current)
steps:
  - id: print
    run: echo ${params.today} ${params.branch}
```

The `$()` form in `params[].eval` is target behavior defined by the dynamic evaluation spec.

Use `action: exec` for direct argv execution:

```yaml
steps:
  - id: process
    action: exec
    with:
      command: /usr/bin/python3
      args:
        - -u
        - app.py
```

Target command with declared step output:

```yaml
steps:
  - id: build
    run: |
      printf 'image_tag=v1.2.3\n' >> "$DAGU_OUTPUT_FILE"
    outputs:
      - name: image_tag
```

Compatibility behavior does not set `DAGU_OUTPUT_FILE`; this example describes the target top-level `outputs` model from the step outputs spec.

Custom shell for a `run` step:

```yaml
steps:
  - id: bash_example
    run: printf "%s\n" "$BASH_VERSION"
    with:
      shell: bash
      shell_args: [-e, -o, pipefail]
```

Invalid empty command:

```yaml
steps:
  - id: bad
    run: ""
```

Invalid unsupported command shape:

```yaml
steps:
  - id: bad
    run:
      command: echo hello
```

## Acceptance Criteria

Validation:

- A black-box fixture verifies `dagu validate` accepts a step with a simple `run` string.
- A black-box fixture verifies `dagu validate` accepts a multi-line `run` string.
- A black-box fixture verifies `dagu validate` accepts compatibility array-form `run`.
- A black-box fixture verifies `dagu validate` accepts primitive scalar entries in compatibility array-form `run`.
- A black-box fixture verifies `dagu validate` accepts single-key mapping entries with primitive scalar values in compatibility array-form `run`.
- A black-box fixture verifies `dagu validate` rejects unsupported `run` shapes.
- A black-box fixture verifies `dagu validate` rejects an empty `run`.
- A black-box fixture verifies `dagu validate` rejects compatibility array-form `run` with no non-empty command entries.
- A black-box fixture verifies `dagu validate` rejects multi-key mapping entries in compatibility array-form `run`.
- A black-box fixture verifies `dagu validate` rejects nested mapping or nested array entries in compatibility array-form `run`.
- A black-box fixture verifies `dagu validate` accepts `$()` text in `run` and does not execute it.
- A black-box fixture verifies `dagu validate` accepts backtick text in `run` and does not execute it.
- A black-box fixture verifies `dagu validate` does not execute `run`.
- A black-box fixture verifies `dagu validate` does not require the command path to exist.
- A black-box fixture verifies `dagu validate` rejects unsupported `with` keys on `run`.

Value resolution:

- A black-box fixture verifies `dagu run` resolves Dagu-owned references before command execution.
- A black-box fixture verifies `$NAME` remains available for shell expansion.
- A black-box fixture verifies `$()` remains available for shell execution.
- A black-box fixture verifies backticks remain available for shell execution.
- A black-box fixture verifies a value-resolution failure prevents the command from starting.

Shell handoff:

- A black-box fixture verifies `dagu run` executes a simple `run` command.
- A black-box fixture verifies `dagu run` executes multi-line `run` strings with line breaks preserved.
- A black-box fixture verifies `dagu run` uses `with.shell` and `with.shell_args` for the selected shell.
- A black-box fixture verifies `dagu run` uses root `shell` when step `with.shell` is omitted.
- A black-box fixture verifies `dagu run` uses the runtime default shell when neither step nor root shell is configured.
- A black-box fixture verifies canonical string `run` is not exposed as a user-authored argv list.
- A black-box fixture verifies direct argv execution is covered by `action: exec`, not `run`.
- A black-box fixture verifies Unix shell command construction uses the expected shell command-string invocation.
- A black-box fixture verifies PowerShell command construction uses the expected non-interactive command invocation.
- A black-box fixture verifies `cmd` command construction uses the expected command-string invocation.

Compatibility:

- A black-box fixture verifies compatibility array-form `run` executes command entries sequentially.
- A black-box fixture verifies compatibility array-form `run` stops at the first failed command.
- A black-box fixture verifies multi-line `run` can execute through a temporary script-file path.
- A black-box fixture verifies a multi-line `run` shebang selects the shebang interpreter when no step-level shell is specified.
- A black-box fixture verifies root `shell` does not suppress direct shebang interpreter selection.
- A black-box fixture verifies a step-level `with.shell` suppresses direct shebang interpreter selection.
- A black-box fixture verifies temporary script files are removed after successful script-file execution.
- A black-box fixture verifies missing working directories can fail before process startup in the script-file path.

Runtime behavior:

- A black-box fixture verifies `dagu run` executes from `DAG_RUN_WORK_DIR` when neither root nor step working directory is set.
- A black-box fixture verifies `dagu run` respects root `working_dir`.
- A black-box fixture verifies `dagu run` respects step `working_dir`.
- A black-box fixture verifies command-string execution creates the step working directory before starting the command.
- A black-box fixture verifies command stdout is captured.
- A black-box fixture verifies command stderr is captured.
- A black-box fixture verifies stdout is not automatically treated as a step output under the target top-level `outputs` model.
- A target-conformance fixture verifies `DAGU_OUTPUT_FILE` is set when the step declares target top-level `outputs` that require it.
- A target-conformance fixture verifies `DAGU_OUTPUT_FILE` is absent when the step does not declare target top-level `outputs` that require it.
- A compatibility fixture verifies `DAG_RUN_STEP_STDOUT_FILE` and `DAG_RUN_STEP_STDERR_FILE` are exposed.
- A black-box fixture verifies non-zero command exit fails the step.
- A black-box fixture verifies a command path that the shell cannot find fails the step.
- A black-box fixture verifies abort terminates the running command.
- A black-box fixture verifies timeout termination fails the running command.
