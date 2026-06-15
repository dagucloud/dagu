# Spec: Step Run Script

## Implementation Status

Partially implemented. This spec defines script-form `run` behavior.

## Scope

This spec defines script-form `run`.

Script form is a multi-line string `run`.

Shared `run` behavior is defined by [Spec 013: Step Run](013-step-run.md).
Command-form `run` is defined by [Spec 014: Step Run Command](014-step-run-command.md).

This spec does not define:

- Single-line string `run`.
- Array-form `run`.
- Direct argv execution through `action: exec`.
- Non-local executor behavior.

## Goal

Workflow authors can write multi-line shell scripts inline in a workflow and
get predictable line preservation, shell behavior, and shebang behavior.

## Behavior

### Script Text

- Script-form `run` is shell script text.

- Script-form `run` must contain one or more line breaks.

- Dagu resolves Dagu-owned references before the script starts.

- Dagu preserves line breaks in the resolved script text.

- Leading and trailing line breaks are preserved.

- Dagu must run the resolved script text as one script.

- Dagu must not split script-form `run` into multiple command-form invocations.

### Script Preparation

- Dagu must prepare the resolved script text before user script code starts.

- If Dagu cannot prepare the script, the step fails before user script code starts.

- The script runs from the step process working directory.

- If the step process working directory is unavailable when Dagu prepares the script, script preparation fails.

- Script preparation fails when Dagu cannot make the resolved script text available to the selected shell or shebang interpreter.

- Dagu removes script preparation resources after script execution.

- Cleanup failure must not change a successful script exit into a failed step.

### Shebang

- If the first line of the script starts with `#!` and no step-level `with.shell` is explicitly specified, Dagu must invoke the shebang interpreter.

- A root shell does not count as an explicitly specified step-level shell for shebang suppression.

- If step-level `with.shell` is explicitly specified, Dagu must execute the script through that shell and must not use the shebang interpreter directly.

- Shebang behavior for an existing script file named inside command-form `run` is outside this spec.

### Shell Behavior

#### Unix-Like Shells

- Unix-like script execution includes `-e` when the selected shell is Unix-like, no step-level shell is explicitly specified, and configured shell arguments do not already include `-e`.

#### PowerShell

- PowerShell script execution includes `-ExecutionPolicy Bypass -File`.

- PowerShell script execution includes `-NoProfile` and `-NonInteractive` unless configured shell arguments already include them.

- PowerShell script execution normalizes error handling and UTF-8 encoding.

#### Windows cmd

- `cmd` script execution includes `/c` unless configured shell arguments already include `/c` or `/C`.

#### Nix Shell

- Nix shell script execution adds `-p <package>` for each configured shell package.

- Nix shell script execution includes `--pure` unless configured shell arguments already include `--pure` or `--impure`.

- Nix shell script execution includes `--run` unless configured shell arguments already include it.

## Errors

Runtime must fail when:

- Dagu cannot prepare the script before user script code starts.

- Shebang detection fails.

- The selected shell or shebang interpreter cannot be started.

- The selected shell or shebang interpreter cannot find the command path.

- The script exits with a non-zero code.

## Examples

Multi-line script:

```yaml
steps:
  - id: build
    run: |
      set -eu
      ./scripts/build.sh
      ./scripts/test.sh
```

Script with shebang:

```yaml
steps:
  - id: python_script
    run: |
      #!/usr/bin/env python3
      print("hello")
```

Step-level shell overrides shebang selection:

```yaml
steps:
  - id: force_sh
    run: |
      #!/usr/bin/env bash
      echo "$0"
    with:
      shell: sh
```

## Acceptance Criteria

- A black-box fixture verifies `dagu run` executes a multi-line `run` script.
- A black-box fixture verifies line breaks in script-form `run` are preserved.
- A black-box fixture verifies a block scalar with only one command line and a trailing line break is script form.
- A black-box fixture verifies leading blank lines are preserved before shebang evaluation.
- A black-box fixture verifies script-form `run` is executed as one script.
- A black-box fixture verifies script-form `run` is not split into command-form invocations.
- A black-box fixture verifies a script-form `run` shebang selects the shebang interpreter when no step-level shell is specified.
- A black-box fixture verifies root `shell` does not suppress direct shebang interpreter selection.
- A black-box fixture verifies a step-level `with.shell` suppresses direct shebang interpreter selection.
- A black-box fixture verifies prepared script resources are removed after successful script execution.
- A black-box fixture verifies missing working directories can fail before user script code starts.
- A black-box fixture verifies PowerShell script execution uses the expected non-interactive script invocation.
- A black-box fixture verifies non-zero script exit fails the step.
