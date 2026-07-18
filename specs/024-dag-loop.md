# Spec: DAG Loop

## Status

Not implemented.

This spec defines conformance behavior for the root `loop` field, which
repeats a whole DAG-run step body until a condition is reached.

## Scope

This spec defines the root `loop` field:

- `loop.while` and `loop.until` condition lists
- `loop.max_iterations`, `loop.interval_sec`, `loop.backoff`,
  `loop.max_interval_sec`, `loop.on_exhausted`
- iteration lifecycle and ordering
- continuation-decision timing, context, and captured feedback
- iteration-scoped environment variables
- state visibility across iterations
- iteration isolation contracts for LLM chat requests and harness
  invocations
- exhaustion behavior
- DAG-run status effects
- behavior when a looping DAG runs as a child DAG
- validation and runtime errors

This spec does not define:

- step-level `repeat_policy` or `retry_policy`
- run-level retry of a finished DAG run
- scheduler, queue, coordinator, API, or UI behavior, including how
  iterations are represented in run history
- base-config or workspace-level defaults for `loop`
- a `${loop.*}` value-reference namespace
- executor-specific feedback injection, such as automatic prompt or
  conversation amendment for LLM or harness steps
- recursion or nesting depth limits for sub-DAG composition
- whether loop environment variables are visible to lifecycle handler steps

## Goal

Workflow authors can repeat an entire workflow until a measurable condition
is reached, with the loop bounded by construction.

The primary use case is a convergence loop: steps attempt work, a
deterministic check decides whether the goal is reached, and the next
iteration can observe why the previous check did not pass. This supports
agent-style goal loops without giving any step an unbounded runtime.

Every loop must declare an iteration bound. A loop that needs to run
indefinitely belongs to the scheduler, not to one DAG run.

## Related Specs

- YAML schema: [Spec 002: YAML Schema](002-yaml-schema.md)
- Value resolution: [Spec 003: Value Resolution and Field Evaluation](003-value-resolution.md)
- Environment values: [Spec 006: Value Resolution Env](006-value-resolution-env.md)
- Step output references: [Spec 007: Value Resolution Steps](007-value-resolution-steps.md)
- Step outputs: [Spec 012: Step Outputs](012-step-outputs.md)
- Condition entries: [Spec 023: Preconditions](023-preconditions.md)

## Terms

A loop is the root `loop` object of one DAG document.

An iteration is one complete execution of the DAG's normal steps under the
loop.

The iteration index is the one-based ordinal of an iteration inside one
DAG-run attempt.

An iteration's terminal status is the DAG-run terminal status that the
iteration's completed step statuses would produce if the DAG run ended
after that iteration.

A loop condition list is the normalized ordered list of condition entries
under `loop.while` or `loop.until`.

A continuation decision is the evaluation performed after an iteration
completes that decides whether another iteration starts.

A loop condition list passes when every condition entry is met after
negation.

Exhaustion is the state where the continuation decision would start another
iteration but the iteration index has reached `max_iterations`.

The feedback file is the file that carries details of the most recent
continuation decision into the next iteration.

## Behavior

### Field Shape

`loop` is an optional root field.

| Field | Meaning |
| --- | --- |
| `while` | Loop condition list. Another iteration starts while the list passes. |
| `until` | Loop condition list. The loop ends when the list passes. |
| `max_iterations` | Required iteration bound. |
| `interval_sec` | Wait between iterations. |
| `backoff` | Multiplier applied to the wait for each later iteration. |
| `max_interval_sec` | Upper bound for the backoff wait. |
| `on_exhausted` | Run status policy when the loop ends by exhaustion. |

Rules:

- Omitted `loop` means the DAG has no loop and this spec does not apply.

- `loop` must be an object.

- `loop` must not contain fields other than `while`, `until`,
  `max_iterations`, `interval_sec`, `backoff`, `max_interval_sec`, and
  `on_exhausted`.

- `loop` must contain exactly one of `while` or `until`.

- `loop.while` and `loop.until` accept a non-empty string shortcut or an
  array of condition entries.

- A `loop.while` or `loop.until` array must contain at least one and at
  most `100` condition entries. An empty array is invalid.

- Condition entry shape, string-shortcut normalization, condition text,
  eval text, expected values, and negation follow the Spec 023 rules for
  condition entries, applied to loop condition entries.

- `loop.max_iterations` is required.

- `loop.max_iterations` must resolve to an integer from `1` through `1000`.

- `loop.interval_sec` is optional and defaults to `0`.

- `loop.interval_sec` must resolve to an integer from `0` through
  `604800`.

- `loop.backoff` is optional.

- `loop.backoff` accepts `true`, which means `2.0`, or a number greater
  than `1.0`.

- `loop.backoff: false` is valid and has the same behavior as an omitted
  `backoff`.

- `loop.max_interval_sec` is optional.

- `loop.max_interval_sec` is valid only when backoff is enabled, that is
  when `loop.backoff` is `true` or a number greater than `1.0`.

- `loop.max_interval_sec` is required when backoff is enabled.

- `loop.max_interval_sec` must resolve to an integer from `1` through
  `604800`.

- `loop.on_exhausted` is optional and defaults to `fail`.

- `loop.on_exhausted` must be `fail` or `succeed`.

- `loop.max_iterations`, `loop.interval_sec`, and `loop.max_interval_sec`
  are value-resolved once, when the DAG-run attempt starts, with the root
  run scope and no step-output scope.

- `loop` is independent of the DAG `type`. Both `graph` and `chain` step
  bodies may loop.

Example shape:

```yaml
loop:
  until:
    - condition: sh ./verify.sh
  max_iterations: 5
  interval_sec: 10
  backoff: 2.0
  max_interval_sec: 300
  on_exhausted: fail
steps:
  - id: attempt
    run: sh ./attempt.sh
```

### Iteration Lifecycle

Rules:

- Root preconditions are checked once for each DAG-run attempt, before the
  first iteration, following Spec 023.

- `handler_on.init` runs once for each DAG-run attempt, before the first
  iteration.

- Lifecycle handlers do not run per iteration. Terminal handlers are
  selected once, by the final DAG-run status after the loop ends.

- An iteration executes every normal step of the DAG under the DAG's
  normal scheduling semantics for its `type`.

- Every step status from an earlier iteration is discarded when a new
  iteration starts. Each iteration starts every normal step from a
  not-started state.

- Executor-held session state from an earlier iteration is discarded when
  a new iteration starts. The observable contract is defined under
  Iteration Isolation.

- Step-level preconditions are checked once per step start in every
  iteration.

- The first iteration always runs. Loop conditions are never evaluated
  before the first iteration, in both `while` and `until` modes.

- A waiting state, such as a step paused for approval, is not a terminal
  status. An iteration completes only when it reaches a terminal status.
  Waiting keeps its normal behavior and does not trigger a continuation
  decision.

- After an iteration completes, the loop proceeds in this order:

  1. If the iteration's terminal status is not `succeeded` and not
     `partially_succeeded`, the loop ends and the DAG run takes that
     status.

  2. The continuation decision evaluates the loop condition list.

  3. If the decision is to end the loop normally, the DAG run takes the
     final iteration's terminal status.

  4. If the decision is to continue and the iteration index equals
     `max_iterations`, the loop ends by exhaustion.

  5. Otherwise Dagu writes the feedback file, waits the iteration
     interval, and starts the next iteration.

- In `until` mode, the decision is to end the loop normally when the loop
  condition list passes.

- In `while` mode, the decision is to end the loop normally when the loop
  condition list does not pass.

### Continuation Decision Context

Rules:

- Loop condition entries are evaluated in source order after every
  completed iteration whose terminal status is `succeeded` or
  `partially_succeeded`.

- Loop condition entries use the root run environment, the root shell
  selection, and the root working directory that normal steps inherit when
  they do not set a step working directory.

- Loop condition entries receive the loop environment variables of the
  iteration that just completed.

- Value-match loop conditions may reference step outputs with
  `${steps.<step_id>.outputs.<output_name>}`. The reference resolves to
  the value most recently published across all completed iterations.

- Unlike Spec 023 command checks, the combined stdout and stderr of each
  command-check loop condition entry is captured for the feedback file.

- Captured command-check output is not published as step output and is not
  appended to any step's captured streams.

- A loop condition evaluation error ends the loop and the DAG run reaches
  terminal status `failed`. Evaluation errors follow the Spec 023
  definition: value-resolution failure, dynamic-evaluation failure, and
  runtime regex compilation failure are evaluation errors; a non-zero
  command-check exit code and a value mismatch are normal not-met results.

### Iteration Interval

Rules:

- Dagu waits before starting iteration `N + 1` only after the continuation
  decision for iteration `N` decides to continue.

- Without `backoff`, or with `backoff: false`, the wait is `interval_sec`
  seconds.

- With `backoff`, the wait before iteration `N + 1` is
  `interval_sec * backoff^(N - 1)` seconds.

- With `backoff`, the wait is capped at `max_interval_sec` seconds.

- The wait computation must saturate at the cap. A backoff term that
  exceeds any intermediate numeric representation must produce the capped
  wait, not an error, an overflow, or a shorter wait.

- Abort during the wait ends the loop without starting another iteration.

### Loop Environment

Dagu exposes these environment variables to every normal step in a looping
DAG:

| Variable | Meaning |
| --- | --- |
| `DAGU_LOOP_ITERATION` | One-based index of the current iteration. |
| `DAGU_LOOP_MAX_ITERATIONS` | Resolved `max_iterations` value. |
| `DAGU_LOOP_FEEDBACK_FILE` | Path of the feedback file. |

Rules:

- Loop environment variables are set only when the root `loop` field is
  present.

- Loop environment names are execution-attempt Dagu-managed values under
  Spec 006. Once set for an iteration, they take precedence over root
  `env`, step `env`, and container environment entries with the same
  names.

- Loop environment variables are not part of the environment a parent run
  passes to a child DAG run. A child DAG run has loop environment
  variables only when its own DAG defines `loop`.

- Loop environment variables are available wherever the step action
  environment is available, including step-level precondition checks under
  the Spec 023 context rules.

- Loop environment variable values are constant within one iteration.

- The feedback file exists and is empty when the first iteration starts.

- The feedback file path is stable for the whole DAG-run attempt.

- The feedback file path is meaningful to step processes that share the
  runner's filesystem. Executor specs define how executors that run step
  processes remotely or in containers receive the feedback file.

- Before each iteration after the first, the feedback file content is
  replaced with the most recent continuation decision. Content is not
  appended across iterations.

- For each loop condition entry, in source order, the feedback file
  includes the entry's captured combined stdout and stderr for command
  checks, or the entry's actual value and expected value for value
  matches.

- For each loop condition entry, the feedback file marks the entry's
  result with the token `met` or `not met`. The token reports the result
  after negation, the same value used to decide list passage.

- Per reported value, at most the last 64 KiB is retained. This applies
  to captured command-check output, value-match actual values, and
  expected values.

- The exact formatting around these values is not defined by this spec.
  Consumers may rely on the presence of the reported values and result
  tokens, not on surrounding layout.

### State Across Iterations

Rules:

- Published step outputs persist across iterations.

- Output variables captured through the singular step `output` field
  follow the same persistence: the variable keeps its most recently
  captured value across iterations until the producing step captures a
  new value.

- Step-output references in step fields keep the Spec 007
  dependency-ordering requirements. Loop iteration does not allow a step
  to reference an output whose producing step is not ordered before it.

- A step-output reference resolves to the most recently published value
  for that output.

- When the producing step has not published the output in the current
  iteration, for example because the producer was skipped or its failure
  was continued in the current iteration, an ordered reference resolves to
  the value published in an earlier iteration.

- Earlier-iteration values of steps that are not ordered before the
  consuming step flow only through the feedback file, the loop environment
  variables, and filesystem state.

- An output that has never been published behaves as an unresolved
  reference under Spec 003 and Spec 007.

- Files created by steps are ordinary filesystem state. Dagu does not
  reset filesystem state between iterations.

### Iteration Isolation

A step observes earlier iterations only through these channels:

- the loop environment variables
- the feedback file
- published step outputs
- filesystem state left by earlier iterations

Rules:

- Dagu must not carry executor-held session records from one iteration
  into a later iteration through any channel other than the four channels
  above.

- For an LLM chat step, no chat message produced during an earlier
  iteration may appear in any model request sent during a later iteration.

- For an LLM chat step, the message list of the first model request in
  any iteration is built only from the step's resolved configuration and
  from chat messages produced by steps during the same iteration.

- For an LLM chat step whose resolved configuration is identical across
  iterations, the first model request of a later iteration carries a
  message list identical to the first model request of the first
  iteration.

- For a harness step, the child process argument vector and stdin are
  built only from the step's resolved field values.

- For a harness step whose resolved field values are identical across
  iterations, a later iteration starts the child process with a
  byte-identical argument vector and byte-identical stdin compared to the
  first iteration.

- Dagu must not pass session-resume or conversation-continuation arguments
  that reference an earlier iteration's execution to a harness child
  process.

### Failure, Abort, and Timeout

Rules:

- If any step failure makes an iteration's terminal status a failure, the
  loop ends and the DAG run fails. Loop conditions are not evaluated after
  a failed iteration.

- `continue_on` inside the step body keeps its normal per-step behavior
  within each iteration.

- Aborting the DAG run stops the current iteration with normal abort
  behavior and must not start another iteration.

- A DAG-run timeout bounds the whole DAG-run attempt, including all
  iterations and all iteration intervals.

- A timed-out or aborted looping run must not be reported as `succeeded`.

### Exhaustion

Rules:

- Exhaustion occurs only when the continuation decision would start
  another iteration and the iteration index equals `max_iterations`.

- Ending the loop normally on the final allowed iteration is not
  exhaustion. `on_exhausted` does not apply.

- With `on_exhausted: fail`, an exhausted DAG run reaches terminal status
  `failed`, regardless of the final iteration's terminal status.

- With `on_exhausted: succeed`, an exhausted DAG run takes the final
  iteration's terminal status.

- Exhaustion with `on_exhausted: fail` is a failure event for lifecycle
  handler selection.

### Sub-DAG Composition

Rules:

- A `loop` on a DAG applies wherever that DAG runs, including when it runs
  as a child DAG through `dag.run`.

- The loop runs entirely inside the child DAG run. The parent step
  observes only the child run's terminal status and final outputs.

- Child outputs harvested by the parent reflect the values after the
  child's loop has ended.

- A parent step fan-out with `parallel` gives each represented child run
  its own independent loop state.

- A parent DAG with `loop` and a child DAG with `loop` compose. Each loop
  is bounded by its own `max_iterations`.

## Errors

### Validation Errors

Validation must fail when:

- `loop` is not an object.
- `loop` contains an unknown field.
- `loop` omits both `while` and `until`.
- `loop` contains both `while` and `until`.
- `loop.while` or `loop.until` is neither a non-empty string nor a
  non-empty array.
- a `loop.while` or `loop.until` array contains more than `100` condition
  entries.
- a loop condition entry violates the Spec 023 condition entry shape
  rules.
- `loop.max_iterations` is missing.
- a static `loop.max_iterations` is not an integer from `1` through
  `1000`.
- a static `loop.interval_sec` is not an integer from `0` through
  `604800`.
- `loop.backoff` is neither a boolean nor a number greater than `1.0`.
- `loop.max_interval_sec` is present while `loop.backoff` is omitted or
  `false`.
- `loop.backoff` enables backoff and `loop.max_interval_sec` is missing.
- a static `loop.max_interval_sec` is not an integer from `1` through
  `604800`.
- `loop.on_exhausted` is neither `fail` nor `succeed`.

Validation must not:

- Execute loop condition command checks.
- Require runtime parameter values to be available.

### Runtime Errors

The DAG run must fail when:

- value resolution of `loop.max_iterations`, `loop.interval_sec`, or
  `loop.max_interval_sec` fails or produces a value outside the field's
  allowed range.
- a loop condition entry has an evaluation error as defined by Spec 023.
- an iteration's terminal status is a failure.

The DAG run must not fail, and the loop must continue or end according to
the mode, when:

- a command-check loop condition exits with a non-zero exit code.
- a value-match loop condition does not match `expected`.
- a command-check loop condition process cannot be started.

## Examples

### Until Loop Reaching Its Goal

```yaml
loop:
  until:
    - condition: test "$(wc -l < count.txt | tr -d ' ')" -ge 3
  max_iterations: 5
steps:
  - id: work
    run: echo x >> count.txt
```

Expected behavior:

- `work` runs three times.
- The continuation decision after the third iteration passes, so the loop
  ends normally before exhaustion.
- The DAG run is `succeeded`.
- `count.txt` contains three lines.

### Until Loop With a Step Output Verdict

```yaml
loop:
  until:
    - condition: ${steps.review.outputs.verdict}
      expected: approved
  max_iterations: 3
steps:
  - id: work
    run: echo attempt >> attempts.txt

  - id: review
    depends: work
    run: |
      if test "$(wc -l < attempts.txt | tr -d ' ')" -ge 2; then
        echo "verdict=approved" >> "$DAGU_OUTPUT_FILE"
      else
        echo "verdict=rejected" >> "$DAGU_OUTPUT_FILE"
      fi
    outputs:
      - name: verdict
```

Expected behavior:

- The first continuation decision compares `rejected` to `approved` and
  does not pass.
- The second continuation decision compares `approved` to `approved` and
  passes.
- `work` and `review` each run twice.
- The DAG run is `succeeded`.

### Exhaustion With Default `on_exhausted`

```yaml
loop:
  until:
    - condition: test -f never.flag
  max_iterations: 2
steps:
  - id: work
    run: echo x >> count.txt
```

Expected behavior:

- `work` runs twice.
- Both continuation decisions do not pass.
- The loop ends by exhaustion.
- The DAG run is `failed` even though every iteration succeeded.

### Exhaustion With `on_exhausted: succeed`

```yaml
loop:
  until:
    - condition: test -f never.flag
  max_iterations: 2
  on_exhausted: succeed
steps:
  - id: work
    run: echo x >> count.txt
```

Expected behavior:

- `work` runs twice.
- The loop ends by exhaustion.
- The DAG run takes the final iteration's terminal status and is
  `succeeded`.

### While Mode Runs the Body First

```yaml
loop:
  while:
    - condition: test -f keep.flag
  max_iterations: 10
steps:
  - id: work
    run: echo ran >> ran.txt
```

Expected behavior:

- `keep.flag` does not exist, but `work` still runs once because loop
  conditions are never evaluated before the first iteration.
- The first continuation decision does not pass, so the loop ends
  normally.
- The DAG run is `succeeded`.
- `ran.txt` contains one line.

### Feedback File Carries Verifier Output

```yaml
loop:
  until:
    - condition: sh ./verify.sh
  max_iterations: 3
steps:
  - id: attempt
    run: |
      cp "$DAGU_LOOP_FEEDBACK_FILE" "feedback-${DAGU_LOOP_ITERATION}.txt"
      sh ./attempt.sh
```

With `verify.sh` printing `missing: integration test` and exiting `1`
until `attempt.sh` has run twice:

Expected behavior:

- `feedback-1.txt` is empty.
- `feedback-2.txt` contains `missing: integration test`.
- `attempt` runs twice and the DAG run is `succeeded`.

### Harness Invocation Is Rebuilt Each Iteration

```yaml
harnesses:
  recorder:
    binary: ./record-agent.sh
    prompt_mode: arg
loop:
  until:
    - condition: test -f never.flag
  max_iterations: 2
  on_exhausted: succeed
steps:
  - id: attempt
    action: harness.run
    with:
      provider: recorder
      prompt: fix the failing tests
```

With `record-agent.sh` appending its argument vector as one line to
`invocations.log` and exiting `0`:

Expected behavior:

- `invocations.log` contains two lines.
- The second line is byte-identical to the first line.
- Neither line contains a session-resume or continuation argument.

### Chat Requests Carry No Earlier-Iteration Messages

```yaml
loop:
  until:
    - condition: test "$DAGU_LOOP_ITERATION" -ge 2
  max_iterations: 3
steps:
  - id: ask
    action: chat.completion
    with:
      provider: local
      model: test-model
      base_url: http://127.0.0.1:18080/v1
      messages:
        - role: user
          content: say ok
```

With a recording endpoint at `base_url` that returns a fixed assistant
reply:

Expected behavior:

- The endpoint receives one request per iteration.
- Each request's message list contains the configured user message and no
  assistant message.
- The second request's message list is identical to the first request's
  message list.
- The DAG run is `succeeded` after two iterations.

### Failed Iteration Stops the Loop

```yaml
loop:
  until:
    - condition: test -f done.flag
  max_iterations: 5
steps:
  - id: work
    run: exit 1
```

Expected behavior:

- `work` runs once and fails.
- No continuation decision is made.
- The DAG run is `failed` after one iteration.

### Invalid Loop With Both Modes

```yaml
loop:
  while:
    - condition: test -f keep.flag
  until:
    - condition: test -f done.flag
  max_iterations: 3
steps:
  - id: work
    run: echo x
```

Expected behavior:

- Validation fails because `while` and `until` are mutually exclusive.

### Invalid Loop Without `max_iterations`

```yaml
loop:
  until:
    - condition: test -f done.flag
steps:
  - id: work
    run: echo x
```

Expected behavior:

- Validation fails because `max_iterations` is required.
