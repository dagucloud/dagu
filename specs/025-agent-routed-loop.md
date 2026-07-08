# Spec: Agent-Routed Loop

## Status

Not implemented.

## Scope

This spec defines an agent-routed loop definition: a top-level orchestration
document that repeatedly asks a controller to choose the next tool call until
the controller stops the loop or a configured limit is reached.

This spec covers:

- accepted field shape for agent-routed loop definitions
- routed DAG declaration
- natural-language completion criteria
- structured controller decisions
- DAG routing behavior
- human-input waiting behavior
- loop status, history, and criteria summaries
- validation and runtime errors

This spec does not define:

- model, provider, token, cost, or credential configuration for the controller
- internal behavior of any routed DAG
- deterministic condition expressions such as `passWhen`
- direct step execution by the loop controller
- scheduler, queue worker, coordinator, API, or UI internals
- automatic creation of pull requests, commits, or other external side effects
- resumable loops after process restart
- parallel DAG routing from one controller decision

## Goal

Workflow authors can describe an iterative process where the routing and
completion judgment are not fully deterministic.

The loop itself is only an orchestration layer. It may ask a controller for a
decision, start one routed DAG, wait for user input, stop, and record history.
Actual work such as specification writing, implementation, review, testing, and
security analysis happens inside ordinary DAGs.

The controller owns two forms of judgment:

- which routed DAG should run next
- which completion criteria are currently satisfied

The controller must report that judgment as structured data so the loop remains
auditable and inspectable.

## Related Specs

- YAML schema: [Spec 002: YAML Schema](002-yaml-schema.md)
- Value resolution: [Spec 003: Value Resolution and Field Evaluation](003-value-resolution.md)
- Step output references: [Spec 007: Value Resolution Steps](007-value-resolution-steps.md)
- Step outputs: [Spec 012: Step Outputs](012-step-outputs.md)
- Step run: [Spec 013: Step Run](013-step-run.md)
- Sub-DAG working directory: [Spec 019: Sub-DAG Working Directory](019-sub-dag-working-directory.md)
- DAG loop step: [Spec 024: DAG Loop](024-dag-loop.md)

## Terms

An agent-routed loop is a top-level YAML document that conforms to this spec.

A controller is the decision-making agent used by the loop runtime. The
controller is not one of the routed DAGs.

A routed DAG is one entry in the root `dags` list. It names a DAG that the
controller may select.

A criterion is one natural-language completion requirement in the root
`criteria` list.

A controller decision is the structured object returned by the controller after
it receives the current loop context.

An iteration is one routed DAG run started by a controller `run_dag` tool call.

A decision turn is one request to the controller and one accepted controller
decision.

The active criteria summary is the latest controller-reported status for every
declared criterion.

## Behavior

### Field Shape

An agent-routed loop definition is a top-level YAML document.

Rules:

- `name` is required.
- `description` is optional.
- `limits` is required.
- `dags` is required.
- `criteria` is required.
- `controller` is required.
- `name` must be a non-empty string.
- `description`, when present, must be a non-empty string.
- The document must not contain root fields other than `name`, `description`,
  `limits`, `dags`, `criteria`, and `controller`.
- The document must not contain root `kind`, `metadata`, or `spec` fields.
- The document must not contain root `steps`.

Valid minimal shape:

```yaml
name: feat-implementation-loop
description: Implement a feature until the declared criteria are satisfied.

limits:
  max_iterations: 8

dags:
  - spec-creating
  - implement
  - review

criteria:
  - id: implementation_matches_spec
    description: The implementation matches the approved specification.

  - id: review_passed
    description: Review reports no blocking findings.

controller:
  instructions: |
    Choose the next DAG to run, ask for user input, or stop the loop.
    Stop successfully only when every required criterion is passed or waived.
  tools:
    - run_dag
    - ask_user
    - stop_success
    - stop_blocked
    - stop_failure
```

### Limits

`limits` bounds one loop run.

Rules:

- `limits` must be an object.
- `limits.max_iterations` is required.
- `limits.max_iterations` must be an integer from `1` through `1000`.
- `limits.max_decisions` is optional.
- `limits.max_decisions`, when present, must be an integer from `1` through
  `10000`.
- `limits.timeout` is optional.
- `limits.timeout`, when present, must be a positive duration string.
- `limits` must not contain fields other than `max_iterations`,
  `max_decisions`, and `timeout`.
- Dagu must not start more than `limits.max_iterations` routed DAG runs for one
  loop run.
- Dagu must not accept more than `limits.max_decisions` controller decisions
  for one loop run when `limits.max_decisions` is present.
- A routed DAG run that fails, aborts, or times out still counts as a started
  iteration.

### Routed DAGs

`dags` declares the only DAGs that the controller may route to.

Rules:

- `dags` must be a non-empty array.
- Each item must be either a child DAG target or an object.
- A child DAG target item normalizes to an object with only `dag`.
- An object item must contain `dag`.
- `dag` follows the same child DAG target rules as `action: dag.run`.
- `params` is optional.
- `params`, when present, must be an object.
- Object items must not contain fields other than `dag` and `params`.
- Normalized `dag` values must be unique within the loop definition.

Before each controller decision, Dagu loads metadata for every declared routed
DAG that can be loaded. The controller sees the declared DAG target, loaded DAG
name, and loaded DAG description when those fields are available.

When the controller returns a `run_dag` tool call:

- `dag` in the controller decision must match one declared routed DAG target.
- Dagu starts the selected DAG as a normal DAG run.
- Dagu waits for the selected DAG run to reach a terminal status before asking
  the controller for the next decision.
- Dagu must not inspect individual steps inside the selected DAG to decide
  whether the loop continues.
- A selected DAG run failure is recorded as history and returned to the
  controller. It is not by itself a loop failure.
- A missing, invalid, or unloadable selected DAG is a loop runtime failure.

### Routed DAG Params

The params passed to a routed DAG are the merge of static routed DAG params and
controller decision params.

Rules:

- If the selected routed DAG omits `params`, the static params object is
  empty.
- If the controller decision omits `params`, the decision params object is
  empty.
- Static routed DAG params are applied first.
- Controller decision params are applied second.
- When both objects contain the same top-level key, the controller decision
  value wins.
- The merged params object is passed to the selected DAG run.
- Value resolution for static routed DAG params follows the same rules as
  `dag.run` params.
- Controller decision params are already resolved controller output and do not
  receive field-level value resolution.

### Criteria

`criteria` defines the requirements that the controller must judge.

Rules:

- `criteria` must be a non-empty array.
- Each item must be an object.
- Each item must contain `id`.
- Each item must contain `description`.
- `id` must match `^[A-Za-z][A-Za-z0-9_]*$`.
- `id` values must be unique within the loop definition.
- `description` must be a non-empty string.
- `required` is optional.
- `required`, when present, must be a boolean.
- `required` defaults to `true`.
- Criterion entries must not contain fields other than `id`, `description`,
  and `required`.
- Criterion entries must not contain deterministic expression fields such as
  `condition`, `expected`, or `passWhen`.

The controller must report a status for every declared criterion in every
accepted controller decision.

Supported criterion statuses:

| Status | Meaning |
| --- | --- |
| `passed` | The controller judges that the criterion is satisfied. |
| `failed` | The controller judges that the criterion is not satisfied. |
| `unknown` | The controller does not have enough evidence to judge the criterion. |
| `blocked` | The criterion is not satisfied and progress needs external input or a different tool outside the declared controller tools. |
| `waived` | The controller judges that the criterion does not need to be satisfied for this loop run. |

Rules:

- A `waived` criterion status must include a non-empty `summary`.
- A required criterion may be `waived`.
- A successful stop requires every required criterion to be `passed` or
  `waived` in the same accepted controller decision.

### Controller

`controller` defines the controller instructions and available tools. It does
not define model or provider configuration.

Rules:

- `controller` must be an object.
- `controller.instructions` is required.
- `controller.instructions` must be a non-empty string.
- `controller.tools` is required.
- `controller.tools` must be a non-empty array.
- Each `controller.tools` item must be one of `run_dag`, `ask_user`,
  `stop_success`, `stop_blocked`, or `stop_failure`.
- `controller.tools` values must be unique.
- `controller.tools` must include at least one stop tool.
- `controller` must not contain fields other than `instructions` and
  `tools`.

Before each decision turn, Dagu gives the controller a loop context that
contains:

- loop name and description
- limits and current limit usage
- declared routed DAGs and their loaded metadata when available
- declared criteria
- controller instructions
- accepted decision history
- routed DAG run history, including DAG target, run id, terminal
  status, outputs, and available artifacts
- latest user response when the previous accepted decision was `ask_user`
- active criteria summary from the latest accepted decision, when one exists

Dagu must not require the controller to use a fixed prompt format beyond the
observable decision contract defined by this spec.

### Controller Decision Contract

The controller must return one structured decision object.

Rules:

- The decision object must contain `tool`.
- The decision object must contain `reason`.
- The decision object must contain `criteria`.
- `tool` must be one of the configured `controller.tools`.
- `reason` must be a non-empty string.
- `criteria` must be an array with exactly one entry for every declared
  criterion.
- Decision objects must not contain fields other than `tool`, `dag`,
  `params`, `question`, `reason`, and `criteria`.

Each decision criterion entry follows this shape:

```json
{
  "id": "review_passed",
  "status": "failed",
  "summary": "Review reported one blocking validation issue.",
  "evidence": [
    "review DAG run review:abc"
  ]
}
```

Rules:

- `id` must match one declared criterion id.
- `status` must be one of `passed`, `failed`, `unknown`, `blocked`, or
  `waived`.
- `summary` is optional except when `status` is `waived`.
- `summary`, when present, must be a non-empty string.
- `evidence` is optional.
- `evidence`, when present, must be an array of strings.
- Criterion decision entries must not contain fields other than `id`, `status`,
  `summary`, and `evidence`.

`run_dag` tool rules:

- `dag` is required.
- `dag` must match one declared routed DAG target.
- `params` is optional.
- `params`, when present, must be an object.
- `question` must be omitted.
- If `limits.max_iterations` has already been reached, `run_dag` is invalid.

`ask_user` tool rules:

- `question` is required.
- `question` must be a non-empty string.
- `dag` must be omitted.
- `params` must be omitted.
- The loop enters waiting status until a user response is supplied.
- After a user response is supplied, Dagu starts a new decision turn.

`stop_success` tool rules:

- `dag`, `params`, and `question` must be omitted.
- Every required criterion status in the same decision must be `passed` or
  `waived`.
- The loop finishes with status `succeeded`.

`stop_blocked` tool rules:

- `dag`, `params`, and `question` must be omitted.
- The loop finishes with status `failed` and stop reason `blocked`.

`stop_failure` tool rules:

- `dag`, `params`, and `question` must be omitted.
- The loop finishes with status `failed` and stop reason `controller_failure`.

### Loop Execution

Rules:

- Dagu asks the controller for an initial decision when the loop run starts.
- Dagu records every accepted controller decision in decision order.
- Dagu records every routed DAG run in iteration order.
- A `run_dag` tool call starts exactly one routed DAG run.
- Routed DAG runs execute sequentially.
- Dagu asks the controller for another decision after a routed DAG run reaches
  a terminal status.
- Dagu asks the controller for another decision after user input resolves an
  `ask_user` waiting state.
- The loop succeeds only through an accepted `stop_success` tool call.
- The loop fails through an accepted `stop_blocked` or `stop_failure`
  tool call.
- The loop fails when the controller returns an invalid decision and Dagu
  cannot obtain a valid replacement decision.
- The loop fails with stop reason `max_iterations` when the controller returns
  `run_dag` after `limits.max_iterations` routed DAG runs have already started.
- The loop fails with stop reason `max_decisions` when
  `limits.max_decisions` is present and another controller decision would
  exceed it.
- The loop fails with stop reason `timeout` when `limits.timeout` is reached.
- The loop cancels according to normal cancellation behavior when the loop run
  is cancelled.

The loop runtime must not:

- execute implementation, review, test, or security work directly
- infer completion from fixed routed DAG targets
- infer completion from fixed step ids inside routed DAGs
- start undeclared DAGs
- start more than one routed DAG from one controller decision

### Loop Run Summary

After a loop run reaches a terminal status, Dagu records a summary object.

Rules:

- The summary must contain `name`.
- The summary must contain `status`.
- The summary must contain `stopReason`.
- The summary must contain `iterations`.
- The summary must contain `decisions`.
- The summary must contain `criteria`.
- `iterations` must be an array in iteration order.
- `decisions` must be an array in decision order.
- `criteria` must be the active criteria summary from the final accepted
  controller decision, or an empty array when no accepted decision exists.

Required `stopReason` values:

| Stop reason | Meaning |
| --- | --- |
| `success` | The controller returned the `stop_success` tool. |
| `blocked` | The controller returned the `stop_blocked` tool. |
| `controller_failure` | The controller returned the `stop_failure` tool. |
| `invalid_decision` | No valid controller decision could be accepted. |
| `max_iterations` | The controller attempted another DAG run after the iteration limit. |
| `max_decisions` | The decision limit was reached. |
| `timeout` | The loop timeout was reached. |
| `cancelled` | The loop run was cancelled. |
| `runtime_error` | Dagu could not load or start a selected DAG, persist state, or perform another required runtime operation. |

The summary provides the data needed to render loop status, criteria status,
finished DAG history, decision history, and evidence.

## Errors

Validation must fail when:

- a required root field is missing
- a root field uses the wrong type
- the root document contains unknown fields
- the root document contains `kind`, `metadata`, `spec`, or `steps`
- `limits.max_iterations` is missing or outside the valid range
- `limits.max_decisions` is outside the valid range
- `limits.timeout` is not a positive duration string
- `dags` is empty
- a routed DAG entry is neither a child DAG target nor an object
- a routed DAG object omits `dag`
- a routed DAG `dag` is duplicated
- a routed DAG `dag` is invalid under child DAG target rules
- a routed DAG object contains unknown fields
- `criteria` is empty
- a criterion omits `id` or `description`
- a criterion `id` is invalid or duplicated
- a criterion contains deterministic expression fields
- a criterion contains unknown fields
- `controller.instructions` is missing or empty
- `controller.tools` is missing, empty, duplicated, or contains an unsupported
  tool
- `controller.tools` does not contain a stop tool
- `controller` contains unknown fields

Runtime must fail the loop when:

- the controller cannot be invoked
- the controller returns an invalid decision and no valid replacement decision
  is accepted
- the controller selects an undeclared DAG
- the controller selects a DAG after the iteration limit is exhausted
- the selected DAG cannot be loaded
- the selected DAG params cannot be prepared
- the selected DAG run cannot be started
- loop state or summary cannot be recorded
- a configured loop timeout is reached

Routed DAG run failure is not a loop runtime failure. It is an observed result
that the next controller decision receives as context.

Cancellation behavior:

- Cancelling the loop run must cancel the active routed DAG run when one is
  active.
- Cancelling the loop run while waiting for user input must leave the loop in a
  terminal cancelled state.
- Cancellation records stop reason `cancelled`.

## Examples

### Feature Implementation Loop

```yaml
name: feat-implementation-loop
description: Implement a feature through specification, approval, implementation, review, and verification.

limits:
  max_iterations: 8
  max_decisions: 24
  timeout: 6h

dags:
  - spec-creating
  - ask-approval
  - implement
  - review
  - test
  - security-check

criteria:
  - id: spec_approved
    description: The user has approved the implementation specification.

  - id: implementation_matches_spec
    description: The implementation matches the approved specification.

  - id: review_passed
    description: Review reports no blocking findings.

  - id: tests_accepted
    description: Relevant tests pass, or remaining failures are explicitly unrelated.

  - id: security_passed
    description: Security review reports no blocking concerns.

controller:
  instructions: |
    Route to the most useful next DAG based on the current loop history.
    Judge criteria from DAG outputs, artifacts, user responses, and prior decisions.
    Stop successfully only when every required criterion is passed or waived.
    Ask the user when product, design, or approval judgment is required.
    Stop as blocked when progress requires an unavailable DAG or repeated attempts do not change the result.
  tools:
    - run_dag
    - ask_user
    - stop_success
    - stop_blocked
    - stop_failure
```

Expected behavior:

- The loop starts by asking the controller for a decision.
- The controller may route to `spec-creating` first, but that order is not fixed
  by the loop schema.
- The controller may route to `ask-approval`, or use `ask_user` if the runtime
  supports direct human input.
- The controller evaluates every criterion after each decision turn.
- Failed test or review DAG runs are returned to the controller as evidence.
- The loop succeeds only when the controller returns `stop_success` with all
  required criteria marked `passed` or `waived`.

### Controller Decision

Example `run_dag` decision:

```json
{
  "tool": "run_dag",
  "dag": "implement",
  "params": {
    "task": "Fix the input validation issue reported by the review DAG."
  },
  "reason": "Review found a blocking validation issue and implement is the declared DAG for code changes.",
  "criteria": [
    {
      "id": "spec_approved",
      "status": "passed",
      "summary": "Approval DAG recorded user approval."
    },
    {
      "id": "implementation_matches_spec",
      "status": "failed",
      "summary": "Review reported missing validation."
    },
    {
      "id": "review_passed",
      "status": "failed",
      "summary": "Review DAG reported one blocking issue.",
      "evidence": [
        "review DAG run review:abc"
      ]
    },
    {
      "id": "tests_accepted",
      "status": "unknown",
      "summary": "Tests have not been rerun after the review finding."
    },
    {
      "id": "security_passed",
      "status": "unknown",
      "summary": "Security check has not run after the review finding."
    }
  ]
}
```

Expected behavior:

- Dagu validates that `implement` is a declared routed DAG target.
- Dagu starts the `implement` DAG with the supplied `task` param.
- Dagu records the accepted decision.
- After the `implement` DAG reaches a terminal status, Dagu asks the controller
  for another decision with the new DAG run in history.

Example successful stop decision:

```json
{
  "tool": "stop_success",
  "reason": "All required criteria are passed.",
  "criteria": [
    {
      "id": "spec_approved",
      "status": "passed"
    },
    {
      "id": "implementation_matches_spec",
      "status": "passed"
    },
    {
      "id": "review_passed",
      "status": "passed"
    },
    {
      "id": "tests_accepted",
      "status": "passed"
    },
    {
      "id": "security_passed",
      "status": "passed"
    }
  ]
}
```

Expected behavior:

- Dagu validates that every required criterion is `passed` or `waived`.
- The loop finishes with status `succeeded`.
- The summary stop reason is `success`.
