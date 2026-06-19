# Spec: Step Run Script

## Status

Implemented.

## Scope

This spec defines local script-form `run`.

Script form is a multi-line string `run`.

Shared `run` behavior is defined by [Spec 013: Step Run](013-step-run.md).
Command-form `run` is defined by [Spec 014: Step Run Command](014-step-run-command.md).

This spec does not define:

- Single-line string `run`.
- Array-form `run`.
- Direct argv execution through `action: exec`.
- Non-local executor behavior.
- The implementation mechanism used to prepare a script.
- The path, filename, extension, mode bits, or lifetime details of any internal
  script preparation resource beyond the observable guarantees below.

## Goal

Workflow authors can write multi-line shell scripts inline in a workflow and get
predictable value resolution, line preservation, interpreter selection, shell
argument behavior, secrecy, and failure behavior.

## Behavior

### Script Text

- Script-form `run` is shell script text.

- Script-form `run` must contain one or more line breaks.

- A line break is any line-break sequence preserved by YAML decoding.

- Dagu resolves Dagu-owned references before the script starts.

- Dagu preserves the resolved script text as script content.

- Dagu preserves line breaks, leading whitespace, trailing whitespace, leading
  line breaks, and trailing line breaks in the resolved script content.

- Dagu must run the resolved script text as one script.

- Dagu must not split script-form `run` into multiple command-form invocations.

- Value resolution must not reclassify script form as command form.

- If resolved script-form text no longer contains a line break, it is still
  script form.

- The selected shell or shebang interpreter receives the script content through
  a script input prepared by Dagu.

- The script input identity exposed to the interpreter, such as a script path,
  script name, or `$0` value, is not a stable workflow API.

- A workflow must not depend on the script input identity, file extension, or
  directory. Use `working_dir`, `DAG_RUN_WORK_DIR`, artifacts, or explicit files
  when a stable path is required.

### Shell Operators

- Script-form `run` is passed to the selected shell or shebang interpreter as one script.

- Dagu accepts shell operator text in script-form `run`, including pipes (`|`), redirects (`>`, `>>`, `<`, `2>`), command chaining (`&&`, `||`, `;`), background execution (`&`), grouping syntax, and shell-specific control syntax.

- The selected shell or shebang interpreter defines which operator forms are valid and what they do.

- Dagu must not split script-form `run` at shell operators.

- Shell state and control flow remain inside the script process according to the selected shell or shebang interpreter.

- Background execution syntax is owned by the selected shell or shebang
  interpreter.

- Dagu determines the step result from the selected shell or shebang interpreter
  process, not from detached background work.

- A script that starts background work must wait for that work itself when the
  step result should include that work.

### Script Preparation

- Dagu must prepare the resolved script text before user script code starts.

- If Dagu cannot prepare the script, the step fails before user script code starts.

- Dagu must prepare the script so the selected shell or shebang interpreter can
  consume it as one script.

- The script process runs from the step process working directory.

- Dagu must not require workflow authors to make the workflow file directory
  writable for script preparation.

- Dagu must not require workflow authors to make the script input path stable or
  addressable from the script process working directory.

- If the step process working directory cannot be used as a process working
  directory, the step fails before user script code starts.

- Script preparation fails when Dagu cannot make the resolved script text available to the selected shell or shebang interpreter.

- After script preparation succeeds, Dagu must attempt to remove script
  preparation resources when the step attempt no longer needs the prepared
  script input.

- Cleanup must be attempted after selected shell or shebang interpreter start
  failure, successful script exit, non-zero script exit, abort, and timeout.

- Cleanup failure must not replace the original step result.

- Cleanup failure may be reported as a diagnostic, but it must not be reported
  as the step's execution error when the script process succeeded.

- Dagu-generated diagnostics in normal step errors, run logs, status records,
  history records, workflow events, and DAG-run detail responses must not
  include the full resolved script text.

- An explicit inspection or debug surface may show resolved script text only
  when workflow authors opt in to that surface.

- Dagu-generated diagnostics must not expose values that are secret-backed under
  the environment and secret specs.

- Any explicit inspection or debug surface that shows resolved script text must
  apply secret masking owned by the environment and secret specs.

- User script stdout and stderr are user-controlled output. Dagu captures them
  as defined by the shared `run` spec.

### Shebang

- Shebang detection reads only the first line of the resolved script content.

- The first line starts at the first byte or character of the resolved script
  content.

- Dagu must not skip leading whitespace, leading line breaks, or a byte order
  mark before checking for `#!`.

- If the first line of the resolved script content starts with `#!` and no
  step-level `with.shell` is explicitly specified, Dagu must invoke the shebang
  interpreter.

- Dagu trims ASCII spaces and tabs after `#!` on the shebang line.

- The interpreter command is the first shebang word after that trim.

- Shebang words are separated by ASCII spaces and tabs outside quoted spans.

- Single-quoted spans group text. Dagu removes the surrounding single quotes and
  keeps the contained characters literally. A single quote cannot appear inside
  a single-quoted span.

- Double-quoted spans group text. Dagu removes the surrounding double quotes.
  Inside a double-quoted span, a backslash escapes the following character, and
  the escaped character is copied literally.

- Outside quoted spans, a backslash escapes the following character, and the
  escaped character is copied literally.

- Backslashes are ordinary characters inside single-quoted spans.

- An empty quoted span produces an empty shebang word.

- Dagu does not perform shell expansion, environment expansion, glob expansion,
  command substitution, pipe parsing, or redirect parsing while parsing a
  shebang line.

- Remaining shebang words on the first line are passed as interpreter
  arguments.

- If a shebang line is present but cannot be parsed into a non-empty interpreter
  command, the step fails before user script code starts.

- If a shebang line contains an unterminated single quote, an unterminated double
  quote, or a trailing unpaired backslash outside single quotes, the step fails
  before user script code starts.

- If the interpreter command contains a path separator, Dagu uses that command
  path as parsed.

- If the interpreter command does not contain a path separator, Dagu resolves it
  through the step process `PATH`.

- A root shell does not count as an explicitly specified step-level shell for shebang suppression.

- A runtime default shell does not count as an explicitly specified step-level
  shell for shebang suppression.

- When a shebang interpreter is used, root shell settings, runtime default shell
  settings, and shell packages do not wrap the script.

- If step-level `with.shell` is explicitly specified, Dagu must execute the script through that shell and must not use the shebang interpreter directly.

- Shebang behavior for an existing script file named inside command-form `run` is outside this spec.

### Shell Behavior

#### Common Rules

- Shell construction uses the selected shell, configured shell arguments, the
  prepared script input, and configured shell packages where the selected shell
  supports packages.

- Shell-specific defaults are part of the script-form contract and must be
  covered by black-box tests before being changed.

- Configured shell arguments are passed to the selected shell before the script
  input unless a shell-specific rule below defines a different position.

- If configured shell arguments conflict with the script carrier required by the
  selected shell, Dagu must fail before user script code starts.

- When Dagu needs to embed the prepared script input identity in shell-owned
  command text, Dagu must pass that identity so its characters are treated as
  data, not shell syntax.

- Shell metacharacters in the prepared script input identity must not trigger
  extra commands, redirection, expansion, globbing, or argument splitting.

- For a selected shell that is not identified below, Dagu treats the selected
  shell as a script interpreter and passes the prepared script input as an
  argument after configured shell arguments.

#### Unix-Like Shells

- Unix-like script execution passes the prepared script input to the selected
  shell as a script file argument.

- Unix-like script execution must reject configured shell arguments that select
  command-string or stdin script carriers, including `-c` and `-s`.

- Unix-like script execution includes `-e` when the selected shell is Unix-like,
  no step-level shell is explicitly specified, and configured shell arguments do
  not already include `-e`.

- Step-level `with.shell` suppresses Dagu's automatic `-e` addition. Workflow
  authors must include `-e` in `with.shell_args` when they want that behavior
  with an explicit step shell.

#### PowerShell

- PowerShell script execution passes the prepared script input through
  PowerShell file execution.

- PowerShell script execution includes `-ExecutionPolicy Bypass` unless
  configured shell arguments already specify an execution policy.

- PowerShell script execution includes `-File` unless configured shell
  arguments already specify the same file-execution carrier.

- PowerShell file-execution carriers are `-File` and `-F`.

- If configured shell arguments include a PowerShell file-execution carrier,
  that carrier must be the final configured shell argument.

- When configured shell arguments include a PowerShell file-execution carrier,
  Dagu appends the prepared script input immediately after that carrier.

- If any configured shell argument appears after `-File` or `-F`, Dagu must fail
  before user script code starts because that argument would be interpreted as an
  authored file path or script argument.

- PowerShell script execution must reject configured shell arguments that select
  command-string carriers, including `-Command`, `-C`, and `-EncodedCommand`.

- PowerShell script execution includes `-NoProfile` and `-NonInteractive` unless configured shell arguments already include them.

- PowerShell script execution normalizes PowerShell error handling and UTF-8
  text encoding before user script code starts.

- Under PowerShell normalization, a PowerShell error written by `Write-Error`
  fails the step unless the script handles the error.

- Dagu determines PowerShell script success from the PowerShell process exit
  code.

- Native executable error semantics inside the script remain PowerShell-owned
  unless Dagu's PowerShell normalization explicitly changes them.

#### Windows cmd

- `cmd` script execution includes `/c` unless configured shell arguments already include `/c` or `/C`.

- `cmd` script execution must reject configured shell arguments that provide an
  authored command body after `/c` or `/C`.

- `cmd` resolves the configured `COMSPEC` path when the selected shell is `cmd`
  or `cmd.exe` and `COMSPEC` points to an existing path.

#### Nix Shell

- Nix shell script execution adds `-p <package>` for each configured shell package.

- Nix shell script execution includes `--pure` unless configured shell arguments already include `--pure` or `--impure`.

- Nix shell script execution includes `--run` unless configured shell arguments already include it.

- Nix shell script execution must reject configured shell arguments that provide
  an authored command body after `--run`.

- Nix shell script execution runs the prepared script input inside the Nix shell
  command environment.

- Nix shell script execution includes fail-fast behavior when no step-level
  `with.shell` is explicitly specified.

- Under Nix shell fail-fast behavior, `echo before; false; echo after` must not
  execute `echo after` unless the script handles the failing command.

## Errors

Runtime must fail when:

- Dagu cannot prepare the script before user script code starts.

- Shebang detection fails.

- A shebang line is present but has no interpreter command.

- The selected shell or shebang interpreter cannot be started.

- The selected shell or shebang interpreter cannot find the command path.

- The selected `working_dir` value cannot be used as a process working directory.

- Configured shell arguments conflict with the script carrier required by the
  selected shell.

- The script exits with a non-zero code.

- The script process is terminated by abort or timeout.

- The script emits invalid declared outputs.

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

Explicit step shell with fail-fast Unix behavior:

```yaml
steps:
  - id: bash_script
    run: |
      echo before
      false
      echo after
    with:
      shell: bash
      shell_args: [-e]
```

Do not rely on the prepared script path:

```yaml
steps:
  - id: stable_paths
    run: |
      printf 'work dir: %s\n' "$PWD"
      printf 'run work dir: %s\n' "$DAG_RUN_WORK_DIR"
```

Wait for background work that belongs to the step result:

```yaml
steps:
  - id: background
    run: |
      ./long-task.sh &
      task_pid=$!
      wait "$task_pid"
```
