# Spec: Step Run Script

## Status

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

### Shell Operators

- Script-form `run` is passed to the selected shell or shebang interpreter as one script.

- Dagu accepts shell operator text in script-form `run`, including pipes (`|`), redirects (`>`, `>>`, `<`, `2>`), command chaining (`&&`, `||`, `;`), background execution (`&`), grouping syntax, and shell-specific control syntax.

- The selected shell or shebang interpreter defines which operator forms are valid and what they do.

- Dagu must not split script-form `run` at shell operators.

- Shell state and control flow remain inside the script process according to the selected shell or shebang interpreter.

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
