# Spec: Preconditions

## Status

Implemented.

This spec defines conformance behavior for DAG-level and step-level
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

A condition entry is one object with exactly one value source, optional
`expected`, and optional `negate`.

The value source is either `condition` or `eval`.

The condition text is the value of `condition` after Dagu-owned value
references are resolved.

The eval text is the value of `eval` after dynamic evaluation.

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
- An object condition entry must contain exactly one of `condition` or `eval`.
- Object condition entries may contain `expected`.
- Object condition entries may contain `negate`.
- `negate` defaults to `false` when omitted.
- Object condition entries must not contain fields other than `condition`,
  `eval`, `expected`, and `negate`.
- `eval` is valid only when `expected` is present.
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

Valid dynamic value-match condition:

```yaml
preconditions:
  - eval: $(printf production)
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

- `condition` is optional.
- `condition` must be a string with at least one non-whitespace character when
  present.
- `condition` is value-resolved before the condition is checked.
- Value resolution follows Spec 003.
- Dagu-owned references in `condition` use the normal `${consts.*}`,
  `${params.*}`, `${env.*}`, and `${steps.*.outputs.*}` forms.
- Unqualified environment references in `condition`, such as `$NAME` and
  `${NAME}`, resolve according to Spec 006 for precondition condition fields.
- Unresolved supported references are preserved and reported as passive notices
  by inspection surfaces as defined by Spec 003 and Spec 007.
- `condition` does not run dynamic evaluation or Dagu command substitution.
- In value-match conditions, backtick text and `$()` text are ordinary
  condition text after value resolution.
- Command-check conditions do not execute command substitutions as Dagu field
  evaluation. Shell command checks may still execute command substitutions
  through the selected shell.
- Escaped Dagu-looking text follows Spec 003 escape behavior.

### Eval Text

Rules:

- `eval` is optional.
- `eval` must be a string with at least one non-whitespace character when
  present.
- `eval` is mutually exclusive with `condition`.
- `eval` may be present only when `expected` is present.
- `eval` is dynamic-evaluated before the condition is checked.
- Dynamic evaluation follows Spec 011.
- Dagu-owned references in `eval` are resolved before command substitution.
- Backtick and `$()` command substitutions in `eval` are executed by Dagu.
- Root precondition `eval` command substitutions use the root precondition shell,
  environment, and working directory.
- Step precondition `eval` command substitutions use the step precondition shell,
  environment, and working directory.
- A dynamic-evaluation failure in `eval` is a precondition evaluation error, not
  a not-met condition.

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
- Value-match conditions do not publish command output.

### Value-Match Conditions

A value-match condition compares the actual value from `condition` or `eval` to
`expected`.

When the source is `condition`, the condition text is data, not a shell command.
When the source is `eval`, command substitution is explicit dynamic evaluation.

Rules:

- Value-match mode is selected when an object condition entry contains
  `expected`.
- A value-match condition must contain exactly one value source: `condition` or
  `eval`.
- `condition` source text is not executed as a command.
- Dagu-owned value references are resolved before matching.
- Dagu does not execute command substitutions written in backtick form or `$()`
  form in `condition`.
- Dagu does execute command substitutions written in backtick form or `$()` form
  in `eval`.
- Shell operators, redirects, glob characters, quotes, command substitutions,
  and other shell syntax in `condition` are ordinary text after Dagu-owned value
  and environment resolution.
- Command-generated values can be computed directly in `eval`, or in
  `params[].eval` and referenced from `condition`.
- Literal `expected` passes when at least one line in the actual value exactly
  equals `expected`.
- `expected: re:<pattern>` passes when `<pattern>` matches at least one line in
  the actual value.
- Matching against an empty actual value is allowed only when value resolution or
  dynamic evaluation produced an empty string.
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

- Command-check mode is selected when `expected` is omitted and the entry uses
  `condition`.
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
- DAG-level value-match conditions use the root run environment for value
  resolution and dynamic evaluation.
- DAG-level `eval` command substitutions use the root working directory that
  normal steps would inherit when they do not set a step working directory.
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
- Step-level value-match conditions use the same runtime environment that the
  step action would receive at start time for value resolution and dynamic
  evaluation.
- Step-level `eval` command substitutions use the same working directory that
  the step action would use.
- Step-specific Dagu environment variables, such as the step name and step
  stream file paths, are available to step-level command checks when they are
  available to the step action.
- Step-specific Dagu environment variables are available to step-level
  value-match condition value resolution under the same rule.
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
- When a DAG invoked by a `dag.run` step is aborted because a DAG-level
  precondition is not met, the child run remains `aborted` and the invoking
  step reaches terminal status `skipped`.
- A skipped `dag.run` invocation follows the normal skipped-step continuation
  rules, including `continue_on: skipped`.
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
- An object condition entry omits both `condition` and `eval`.
- An object condition entry contains both `condition` and `eval`.
- An object condition entry has empty or whitespace-only `condition`.
- An object condition entry has non-string `condition`.
- An object condition entry has empty or whitespace-only `eval`.
- An object condition entry has non-string `eval`.
- An object condition entry contains `eval` without `expected`.
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
- Execute runtime `$()` or backtick command substitution while validating
  `eval`.
- Check whether a command-check executable path exists.
- Check whether shell syntax in command-check text is valid for the selected
  shell.
- Require runtime parameter values to be available.
- Require referenced step outputs to be available.

### Runtime Errors

Runtime checking must fail the owning DAG or step when:

- Value resolution of `condition` returns an error.
- Dynamic evaluation of `eval` returns an error.
- The selected shell cannot be resolved.
- The selected working directory cannot be used.
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

### Value Match With Eval

```yaml
steps:
  - id: midnight_job
    preconditions:
      - eval: `date +%H`
        expected: "00"
    run: touch midnight-ran
```

Expected behavior:

- Dagu dynamically evaluates the precondition `eval` field before matching.
- The precondition compares the evaluated value to `expected`.
- If the value is `00`, the step action starts.
- If the value is not `00`, the step is `skipped`.

### Value Match Preserves Command Substitution Text

```yaml
steps:
  - id: literal_text
    preconditions:
      - condition: "$(printf ready)"
        expected: "$(printf ready)"
    run: touch literal-ran
```

Expected behavior:

- Dagu does not execute `printf ready` while checking the value-match
  condition.
- The condition text matches literally.
- The step action starts.

### Command Check With Shell Command Substitution

```yaml
steps:
  - id: shell_check
    preconditions:
      - condition: test "$(printf ready)" = ready
    run: touch shell-check-ran
```

Expected behavior:

- Dagu starts the command check through the selected shell.
- The shell interprets `$(printf ready)`.
- If the shell command exits `0`, the step action starts.
