# Spec: Preconditions

## Status

Not implemented.

This spec defines target conformance behavior for DAG-level and step-level
`preconditions`.

## Scope

This spec defines preconditions at these workflow surfaces:

- Root `preconditions`, which gate a DAG run before normal step execution.
- Step `preconditions`, which gate one step before its action starts.

This spec covers:

- accepted field shapes
- condition entry normalization
- value matching and command-check modes
- negation
- value resolution timing
- shell, environment, and working-directory context
- DAG-run and step lifecycle effects
- validation and runtime errors

This spec does not define:

- scheduler, queue, API, UI, or distributed worker behavior
- base-config or workspace-level global preconditions
- defaults expansion, except that expanded step preconditions follow this spec
- lifecycle handler field syntax
- full graph scheduling semantics outside the status effects named here
- `continue_on` syntax, except for the skipped status produced by unmet step
  preconditions
- legacy aliases such as `command`

## Goal

Workflow authors can gate DAG runs and individual steps with predictable,
testable conditions.

Preconditions must be clear about whether Dagu is comparing a value or running
a command, which shell and environment are used, and what status is produced
when a condition is not met.

## Related Specs

- YAML schema: [Spec 002: YAML Schema](002-yaml-schema.md)
- Value resolution: [Spec 003: Value Resolution and Field Evaluation](003-value-resolution.md)
- Environment values: [Spec 006: Value Resolution Env](006-value-resolution-env.md)
- Step output references: [Spec 007: Value Resolution Steps](007-value-resolution-steps.md)
- Step identity: [Spec 009: Step Reference](009-step-reference.md)
- Step run: [Spec 013: Step Run](013-step-run.md)

## Terms

A precondition list is the normalized ordered list of condition entries for one
DAG or step.

A condition entry is one object with required `condition` and optional
`expected` and `negate` fields.

The condition text is the value of `condition` after Dagu-owned value
references are resolved.

A value-match condition is a condition entry with `expected`.

A command-check condition is a condition entry without `expected`.

A condition is met when it passes before negation is applied.

A condition is not met when it produces a normal negative result before
negation is applied.

An evaluation error is a failure to prepare or evaluate the precondition
itself, not a normal negative result.

## Behavior

### Field Shape

`preconditions` is optional at the DAG root and on steps.

Rules:

- Omitted `preconditions` means the DAG or step has no preconditions.
- An empty `preconditions` array is valid and has the same behavior as an
  omitted field.
- `preconditions` accepts a non-empty string shortcut.
- `preconditions` accepts an array of condition entries.
- Each array item must be a non-empty string shortcut or an object condition
  entry.
- A string shortcut normalizes to an object with only `condition`.
- An object condition entry must contain `condition`.
- Object condition entries may contain `expected`.
- Object condition entries may contain `negate`.
- `negate` defaults to `false` when omitted.
- Object condition entries must not contain fields other than `condition`,
  `expected`, and `negate`.
- The `command` alias is not part of this spec.

Valid string shortcut:

```yaml
preconditions: test -f ready.flag
```

Equivalent normalized condition entry:

```yaml
preconditions:
  - condition: test -f ready.flag
```

Valid value-match condition:

```yaml
preconditions:
  - condition: ${params.environment}
    expected: production
```

Valid negated command-check condition:

```yaml
preconditions:
  - condition: test -f maintenance.lock
    negate: true
```

### Condition Text

Rules:

- `condition` must be a string with at least one non-whitespace character.
- `condition` is value-resolved before the condition is checked.
- Value resolution follows Spec 003.
- Dagu-owned references in `condition` use the normal `${consts.*}`,
  `${params.*}`, `${env.*}`, and `${steps.*.outputs.*}` forms.
- Unqualified environment references in `condition`, such as `$NAME` and
  `${NAME}`, resolve according to Spec 006 for precondition condition fields.
- Unresolved supported references are preserved and reported as passive notices
  by inspection surfaces as defined by Spec 003 and Spec 007.
- `condition` does not run dynamic evaluation.
- Value-match conditions execute command substitutions in `condition` after
  Dagu-owned value resolution and before matching.
- Command-check conditions do not execute command substitutions as Dagu field
  evaluation. Shell command checks may still execute command substitutions
  through the selected shell.
- Escaped Dagu-looking text follows Spec 003 escape behavior.

### Expected Value

Rules:

- `expected` is optional.
- `expected` must be a string with at least one non-whitespace character when
  present.
- `expected` is literal.
- Dagu must not value-resolve `expected`.
- Dagu must not run dynamic evaluation or command substitution in `expected`.
- A value reference written in `expected` is ordinary expected text.
- Literal matching is case-sensitive.
- Regex matching is selected only when `expected` starts with `re:`.
- The regex pattern is the text after `re:`.
- Regex patterns use Go regular expression syntax.
- Regex matching is case-sensitive unless the pattern uses a Go regexp flag
  such as `(?i)`.
- Regex patterns are not implicitly anchored.

### Negation

Rules:

- `negate: false` leaves the condition result unchanged.
- `negate: true` inverts only the normal met/not-met result.
- If a condition is met before negation, `negate: true` makes it not met.
- If a condition is not met before negation, `negate: true` makes it met.
- `negate: true` must not convert an evaluation error into success.

### Multiple Conditions

Rules:

- A precondition list is a logical AND.
- The list passes only when every condition entry is met after negation.
- Dagu evaluates condition entries in source order.
- Dagu must not start the gated DAG or step action until every condition entry
  has been evaluated and every entry has passed.
- Command checks may have external side effects; authors must not rely on a
  later condition being skipped only because an earlier condition is not met.
- Output produced by one command-check condition is not captured as data for a
  later condition entry.
- Output produced by command substitution in one value-match condition is not
  captured as data for a later condition entry.

### Value-Match Conditions

A value-match condition evaluates command substitutions in the resolved
condition text, then compares the resulting text to `expected`.

Rules:

- Value-match mode is selected when an object condition entry contains
  `expected`.
- The condition text is not executed as a command.
- Dagu executes command substitutions written in backtick form or `$()` form.
- Command substitution syntax follows Spec 011.
- Dagu-owned value references are resolved before command substitutions run.
- Command substitutions run through the selected shell for the owning
  precondition context.
- Command substitutions use the environment and working directory for the
  owning precondition context.
- Dagu inserts command stdout into the condition text after trimming
  surrounding whitespace.
- Successful command-substitution stderr is captured and ignored.
- A command substitution that exits with a non-zero status is an evaluation
  error.
- A command substitution that cannot start is an evaluation error.
- A command substitution that times out is an evaluation error.
- Command substitutions are the only shell syntax that Dagu executes in
  value-match condition text.
- Shell operators, redirects, glob characters, quotes, and other shell syntax
  outside command-substitution forms are ordinary text after Dagu-owned value
  and environment resolution.
- Literal `expected` passes when at least one line in the condition text exactly
  equals `expected`.
- `expected: re:<pattern>` passes when `<pattern>` matches at least one line in
  the condition text.
- Matching against an empty condition text is allowed only when value resolution
  produced an empty string.
- An invalid regex pattern is a validation error when it is known before
  runtime.
- If an invalid regex pattern is not detected until runtime, checking the
  condition is an evaluation error.

Example:

```yaml
params:
  - name: environment
    type: string
    required: true
preconditions:
  - condition: ${params.environment}
    expected: re:^(staging|production)$
steps:
  - id: deploy
    run: ./deploy.sh ${params.environment}
```

### Command-Check Conditions

A command-check condition runs condition text and checks only whether the
command exits successfully.

Rules:

- Command-check mode is selected when `expected` is omitted.
- The resolved condition text is the command text.
- Dagu does not execute command substitutions in the command text before
  starting the command check.
- Dagu ignores stdout and stderr produced by the command check.
- Dagu must not publish command-check stdout or stderr as step output.
- Dagu must not append command-check stdout or stderr to the gated step's
  captured stdout or stderr streams.
- Exit code `0` means the condition is met.
- A non-zero exit code means the condition is not met.
- If the command process cannot be started, the condition is not met.
- If the command is terminated by workflow abort or timeout, the owning DAG or
  step follows the abort or timeout behavior instead of treating the condition
  as a normal not-met result.

#### Shell Command Checks

Rules:

- A command check with a selected shell is executed by that shell.
- DAG-level command checks use the DAG-level shell selection.
- Step-level command checks use the same shell selection that the step action
  would use.
- Dagu passes the resolved condition text to the selected shell as command text.
- Shell variable syntax is interpreted by the selected shell, not by Dagu value
  resolution.
- Shell command substitution syntax is interpreted by the selected shell, not by
  Dagu value resolution.
- Shell operators such as pipes, redirects, command chaining, and grouping are
  interpreted by the selected shell.

Example:

```yaml
steps:
  - id: report
    with:
      shell: bash
    preconditions:
      - condition: test -f "$READY_FILE" && test "${env.MODE}" = "daily"
    run: ./report.sh
```

#### Direct Command Checks

Rules:

- Direct command checks are used only when no shell is selected for the
  precondition.
- In direct command checks, the entire resolved condition text is the executable
  path.
- Dagu must not split direct command text into arguments.
- Dagu must not interpret shell syntax in direct command text.
- Shell operators, `$()`, backticks, spaces, and redirects are ordinary
  executable-path text in direct command checks.

### Execution Context

#### DAG-Level Context

Rules:

- DAG-level preconditions are checked once for each DAG-run attempt.
- DAG-level preconditions are checked after runtime parameter values are
  selected and the root environment scope is prepared.
- DAG-level preconditions are checked before `handler_on.init`.
- DAG-level preconditions are checked before any normal step starts.
- DAG-level value-match conditions have no step-output lookup scope.
- DAG-level value-match command substitutions use the DAG-level shell
  selection.
- DAG-level value-match command substitutions use the root working directory
  that normal steps would inherit when they do not set a step working directory.
- DAG-level value-match command substitutions receive the root run environment.
- DAG-level command checks use the root working directory that normal steps
  would inherit when they do not set a step working directory.
- DAG-level command checks receive the root run environment.
- DAG-level command checks do not receive step-specific environment variables.

#### Step-Level Context

Rules:

- Step-level preconditions are checked when the step first becomes eligible to
  start.
- A step is eligible to start only after its dependencies have reached a status
  that allows the step to start.
- Step-level preconditions are checked before the step action starts.
- Step-level preconditions are checked before retry or repeat behavior is
  considered for the step action.
- Step-level preconditions are checked once for the step start, not once per
  retry attempt and not once per repeat iteration.
- Step-level command checks use the same working directory that the step action
  would use.
- Step-level command checks receive the same runtime environment that the step
  action would receive at start time.
- Step-level value-match command substitutions use the same shell, working
  directory, and runtime environment that the step action would use at start
  time.
- Step-specific Dagu environment variables, such as the step name and step
  stream file paths, are available to step-level command checks when they are
  available to the step action.
- Step-specific Dagu environment variables are available to step-level
  value-match command substitutions under the same rule.
- Step-level value-match conditions may resolve step-output references only
  when Spec 007 permits the owning step to read those outputs.

### DAG-Level Status Effects

Rules:

- If all DAG-level preconditions pass, the DAG run proceeds to init handler and
  normal step execution.
- If any DAG-level precondition is not met, the DAG run must not start
  `handler_on.init`.
- If any DAG-level precondition is not met, the DAG run must not start any
  normal step.
- If any DAG-level precondition is not met, the DAG run reaches terminal status
  `aborted`.
- A DAG-level precondition not-met result is an abort event for lifecycle
  handler selection.
- If a DAG-level precondition has an evaluation error, the DAG run reaches
  terminal status `failed`.
- A DAG-level precondition evaluation error is a failure event for lifecycle
  handler selection.

### Step-Level Status Effects

Rules:

- If all step-level preconditions pass, the step action may start.
- If any step-level precondition is not met, the step action must not start.
- If any step-level precondition is not met, the step reaches terminal status
  `skipped`.
- A step skipped by preconditions must not publish step outputs.
- A step skipped by preconditions must not run `retry_policy`.
- A step skipped by preconditions must not run `repeat_policy`.
- A dependent step must not treat a skipped dependency as successful unless an
  owning continuation spec explicitly allows it.
- If a step-level precondition has an evaluation error, the step reaches
  terminal status `failed`.
- A step-level precondition evaluation error is a step failure for DAG-run
  status calculation.

## Errors

### Validation Errors

Validation must fail when:

- `preconditions` is neither a string nor an array.
- A `preconditions` array item is neither a string nor an object.
- A string shortcut is empty or whitespace only.
- An object condition entry omits `condition`.
- An object condition entry has empty or whitespace-only `condition`.
- An object condition entry has non-string `condition`.
- An object condition entry has non-string `expected`.
- An object condition entry has empty or whitespace-only `expected`.
- An object condition entry has non-boolean `negate`.
- An object condition entry contains an unknown field.
- An object condition entry contains legacy `command`.
- `expected` starts with `re:` and the remaining text is empty, whitespace
  only, or not a valid Go regexp pattern.

Validation must not:

- Execute command-check conditions.
- Execute runtime `$()` or backtick command substitution while validating
  `condition`.
- Check whether a command-check executable path exists.
- Check whether shell syntax in command-check text is valid for the selected
  shell.
- Require runtime parameter values to be available.
- Require referenced step outputs to be available.

### Runtime Errors

Runtime checking must fail the owning DAG or step when:

- Value resolution of `condition` returns an error.
- The selected shell cannot be resolved.
- The selected working directory cannot be used.
- A value-match command substitution exits with a non-zero status.
- A value-match command substitution command cannot be started.
- A value-match command substitution times out.
- A regex pattern reaches runtime and cannot be compiled.
- Workflow abort interrupts precondition checking.
- Workflow timeout interrupts precondition checking.

Runtime checking must produce a not-met condition, not an evaluation error, when:

- A value-match condition does not match `expected`.
- A command-check condition exits with a non-zero exit code.
- A command-check condition process cannot be started.

## Examples

### DAG-Level Gate

```yaml
params:
  - name: enabled
    type: string
    required: true
preconditions:
  - condition: ${params.enabled}
    expected: "true"
steps:
  - id: main
    run: touch main-ran
```

Expected behavior:

- With `enabled=true`, `main` starts.
- With any other `enabled` value, the DAG run is `aborted`.
- With any other `enabled` value, `main-ran` is not created.

### DAG-Level Command Check

```yaml
working_dir: ${env.WORK_DIR}
preconditions:
  - condition: test -f ready.flag
steps:
  - id: main
    run: touch main-ran
```

Expected behavior:

- Dagu checks `ready.flag` in the resolved root working directory.
- If `ready.flag` exists, `main` starts.
- If `ready.flag` does not exist, the DAG run is `aborted` and `main` does not
  start.

### Step-Level Skip

```yaml
steps:
  - id: optional
    preconditions:
      - condition: ${env.FEATURE_ENABLED}
        expected: "true"
    run: touch optional-ran

  - id: after_optional
    depends: optional
    run: touch after-ran
```

Expected behavior:

- With `FEATURE_ENABLED=true`, both steps can run.
- With any other `FEATURE_ENABLED` value, `optional` is `skipped`.
- With any other `FEATURE_ENABLED` value, `optional-ran` is not created.
- `after_optional` must not start unless another spec defines skipped
  dependency continuation for this workflow.

### Step-Level Command Context

```yaml
steps:
  - id: check_context
    working_dir: ${env.WORK_DIR}
    env:
      READY_FILE: ready.flag
    preconditions:
      - condition: test -f "$READY_FILE"
    run: touch checked-ran
```

Expected behavior:

- The precondition command runs in the resolved step working directory.
- The precondition command receives `READY_FILE=ready.flag`.
- If `${env.WORK_DIR}/ready.flag` exists, the step action starts.
- If `${env.WORK_DIR}/ready.flag` does not exist, the step is `skipped`.
- Command-check stdout and stderr are not published as step output.

### Negated Condition

```yaml
steps:
  - id: maintenance
    preconditions:
      - condition: test -f maintenance.lock
        negate: true
    run: touch maintenance-ran
```

Expected behavior:

- If `maintenance.lock` does not exist, the step action starts.
- If `maintenance.lock` exists, the step is `skipped`.

### Value Match Command Substitution

```yaml
steps:
  - id: midnight_job
    preconditions:
      - condition: "`printf 00`"
        expected: "00"
    run: touch midnight-ran
```

Expected behavior:

- Dagu executes `printf 00` before matching.
- The resolved condition text is `00`.
- The precondition passes.
- The step action starts.

### Value Match With `$()` Command Substitution

```yaml
steps:
  - id: morning_job
    preconditions:
      - condition: "$(printf 08)"
        expected: "re:0[8-9]"
    run: touch morning-ran
```

Expected behavior:

- Dagu executes `printf 08` before matching.
- The resolved condition text is `08`.
- The regex matches.
- The step action starts.
