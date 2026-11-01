# Spec: MCP Execute Tool

Status: Implemented.

## Scope

This spec defines the `dagu_execute` MCP tool-call contract: input fields,
action contracts, step decisions, wait behavior, successful output, resource
links, and tool-level errors.

This spec does not define the MCP endpoint, authentication, API-key surfaces,
`dagu://` URI grammar, MCP resource reads, `dagu_read`, `dagu_change`,
prompts, subscriptions, REST response schemas, queue selection policy, run
execution semantics, storage internals, or MCP SDK internals.

Related specs: [020: MCP Server](020-mcp-server.md) defines the MCP endpoint,
authentication, and `dagu://` resource URI model used by this tool.
[021: MCP Read Tool](021-mcp-read-tool.md) defines the run detail model
reused by wait output and read before a step decision.
[031: Human Task](031-human-task.md) defines human-task completion and
push-back.

## Goal

`dagu_execute` gives MCP clients one way to start, enqueue, retry, and stop
DAG-runs, to decide steps that wait for a person, and optionally to wait for an
identified run's result inside the same tool call.

## Behavior

### Tool Identity

Rules:

- The tool name is `dagu_execute`.
- A successful call performs exactly the requested execution action. It must
  not create, update, delete, or rename DAG definitions or Wiki pages.
- Audit, metrics, logging, and transport side effects are allowed.

### Input

Tool input is a JSON object. Fields outside this table fail with
`invalid_tool_input`.

| Field | Type | Required rule | Meaning |
| --- | --- | --- | --- |
| `action` | string | Required. One of `start`, `enqueue`, `retry`, `stop`, `approve`, `reject`, `push_back`. | Execution action. |
| `targetType` | string | Optional. One of `dag`, `inline_spec`, `run`. | Target type. Defaults to `run` for `retry`, `stop`, `approve`, `reject`, and `push_back`, `inline_spec` when `spec` is present, otherwise `dag`. |
| `name` | string | Required. | DAG name, including the identity used for inline runs. |
| `spec` | string | Required for `targetType=inline_spec`; otherwise forbidden. | Inline DAG YAML document for `start` and `enqueue`. |
| `dagRunId` | string | Required for `retry`, `stop`, `approve`, `reject`, and `push_back`; optional override for `start` and `enqueue`. | DAG-run identifier. |
| `params` | string or object | Optional for `start` and `enqueue`. | Runtime parameters. An object is canonicalized to its compact JSON encoding. |
| `queue` | string | Optional for `enqueue`. | Queue override. |
| `singleton` | boolean | Optional for `start` and `enqueue`. | Reject duplicate running or queued DAG-runs. |
| `noReuse` | boolean | Optional for `start` and `enqueue`. | Execute eligible build steps without reusing prior materializations. |
| `labels` | array of strings | Optional for `start` and `enqueue`. | Additional labels, each `key=value` or key-only. |
| `stepName` | string | Optional for `retry`; required for `approve`, `reject`, and `push_back`. | Step to retry, or the waiting step to decide. |
| `includeDownstream` | boolean | Optional for `retry`. Requires `stepName`. | Retry the selected step and every reachable descendant. |
| `subDAGRunId` | string | Optional for `approve`, `reject`, and `push_back`. | Sub-DAG run of `dagRunId` that owns the waiting step. |
| `inputs` | object | Optional for `approve` and `push_back`. | Values for the waiting step. |
| `reason` | string | Optional for `reject`. | Reason recorded with the rejection. |
| `attemptId` | string | Optional for `approve`, `reject`, and `push_back`. | Attempt the decision is based on. |
| `wait` | boolean | Optional. | Wait for the identified run to reach a terminal state. |
| `waitTimeoutSeconds` | integer | Optional. Requires `wait`. From `1` through `300`; defaults to `60`. | Maximum wait duration. |

Rules:

- Unknown fields, non-null values of the wrong type, unknown `action` values,
  and unknown `targetType` values fail with `invalid_tool_input`.
- `null` field values are treated as absent.
- String values other than `spec` are trimmed of leading and trailing
  whitespace; a string that is empty after trimming is treated as absent.
- `targetType=run` is valid only for `retry`, `stop`, `approve`, `reject`, and
  `push_back`, and those actions require it.
- `params`, `singleton`, `noReuse`, and `labels` are valid only for `start`
  and `enqueue`; `queue` is valid only for `enqueue`; `includeDownstream` is
  valid only for `retry`; `stepName` is valid only for `retry`, `approve`,
  `reject`, and `push_back`; `subDAGRunId` and `attemptId` are valid only for
  `approve`, `reject`, and `push_back`; `inputs` is valid only for `approve`
  and `push_back`; and `reason` is valid only for `reject`.
- `inputs` that is not a JSON object fails with `invalid_tool_input`.
- Supplying a field for an action that does not support it fails with
  `invalid_tool_input`, even when a boolean is `false` or an array is empty.

### Action contracts

| Action | Effect |
| --- | --- |
| `start` | Runs a stored DAG (`targetType=dag`) or an inline spec (`targetType=inline_spec`). |
| `enqueue` | Enqueues a stored DAG or inline spec for queue-based execution. |
| `retry` | Retries an existing DAG-run, optionally scoped to `stepName` and its downstream steps. |
| `stop` | Requests termination of an existing DAG-run. |
| `approve` | Approves a waiting approval gate, or completes a waiting human task. |
| `reject` | Rejects a waiting approval gate. |
| `push_back` | Pushes a waiting approval gate or human task back for re-execution. |

### Step decisions

`approve`, `reject`, and `push_back` decide one step that waits for a person:
an approval gate or a human task, as reported under `steps` by the Spec 021 run
detail model.

Rules:

- The decided step is the step named `stepName` in the run that `name` and
  `dagRunId` identify, or in its sub-DAG run `subDAGRunId` when given.
- A run or sub-DAG run that does not exist, or that has no step named
  `stepName`, fails with `resource_not_found`.
- When `attemptId` is given and differs from the decided run's current
  attempt, which the Spec 021 run detail reports as `attemptId`, the call fails
  with `conflict`, changes nothing, and reports the current attempt as
  `details.attemptId`. A client passes the `attemptId` it read, so a decision
  based on a read of an earlier attempt is refused.
- A step that is not waiting fails with `conflict`, changes nothing, and the
  message names the step's current status.
- A decision requires the same authorization as the equivalent REST operation;
  a caller without it fails with `unauthorized` and changes nothing.
- A decision that leaves the run ready to continue resumes it exactly as the
  equivalent REST operation does.

Approval gates:

| Action | Effect |
| --- | --- |
| `approve` | The step succeeds. `inputs` supplies approval input values, each a string. |
| `reject` | The step and the run are rejected, recording `reason` when given. |
| `push_back` | The step is pushed back for re-execution with `inputs` as feedback values, each a string. |

- A non-string `inputs` value fails with `invalid_tool_input` and changes
  nothing.
- An `approve` that omits a name the gate lists as required fails with
  `invalid_tool_input` and changes nothing.

Human tasks:

| Action | Effect |
| --- | --- |
| `approve` | Completes the task with `inputs` as its input object. |
| `push_back` | Pushes the task back with `inputs` as its feedback object. |
| `reject` | Fails with `invalid_tool_input` naming field `action`; human tasks cannot be rejected. Nothing changes. |

- `inputs` follows the typed JSON input rules of Spec 031 `--inputs-json`; an
  omitted `inputs` supplies `{}`.
- Input that the task's form or feedback form refuses fails with
  `invalid_tool_input` and changes nothing.

### Wait behavior

Rules:

- Wait applies after the requested action succeeds and only when a run is
  identified by `name` and a DAG-run ID.
- The call returns when the run reaches a terminal state, stops at a waiting
  checkpoint, or the timeout elapses, whichever comes first.
- A waiting checkpoint is a run with at least one step waiting on manual
  action. A run that reports the waiting status with no such step resumes on
  its own, and the wait continues.
- A run stopped at a waiting checkpoint makes no further progress without an
  operator; the output reports `completed=false`.
- On timeout the run keeps executing; the output reports `completed=false`.
- A wait failure after a successful action is reported inside the successful
  output, not as a tool error.

## Output

A successful result has a text content item describing the outcome and, when a
run is identified, resource-link content items for the run detail and run log
resources.

Structured output rules:

- Output always has `action`, `targetType`, `dagName`, `dagRunId`, and
  `references`.
- When a run is identified, output has `runUri` and `logsUri` with canonical
  `dagu://runs/...` URIs.
- Without `wait`, output for an identified run has `subscribe` guidance text.
- Step decision output also has `stepName`, and `subDAGRunId` when given.
- Without `wait`, step decision output has the `status` and `statusLabel` of
  the run that `name` and `dagRunId` identify, observed after the decision
  when that run could be read.
- With `wait`, output has `completed` boolean plus the last observed `status`
  and `statusLabel` when at least one poll succeeded.
- With `wait`, a completed run's output has `run` holding the Spec 021 run
  detail model, including per-step statuses and errors.
- With `wait`, a run stopped at a waiting checkpoint has `run` holding the same
  model, including each waiting step's human-task or approval prompt and, when
  the step declares one, its normalized form.
- With `wait`, a wait interruption or persistent poll failure is reported as
  `waitError` text alongside `completed=false`.

## Errors

Failed tool calls return an MCP tool result with `isError=true` and structured
output holding the error object:

```json
{
  "code": "invalid_tool_input",
  "message": "The dagRunId field is required.",
  "action": "retry",
  "targetType": "run",
  "dagName": "nightly-report",
  "field": "dagRunId"
}
```

`code` and `message` are required. Clients must branch on `code`, not parse
`message`. `action`, `targetType`, `dagName`, `dagRunId`, `field`, and
`details` are present when they apply, with `field` naming the single input
field that caused an input failure.

Common conditions map to codes as follows:

| Condition | Code |
| --- | --- |
| Authentication is required and the request is not authenticated. | `unauthenticated` |
| The accepted credential is not authorized for the requested execution. | `unauthorized` |
| Tool input is malformed, misses a required field, or uses unknown values. | `invalid_tool_input` |
| The named DAG or DAG-run does not exist. | `resource_not_found` |
| A singleton run is already running or queued, the step to decide is not waiting, `attemptId` names another attempt, or the action otherwise conflicts with current run state. | `conflict` |
| The action target exists but cannot currently be acted on. | `resource_unavailable` |
| The server fails unexpectedly while handling the action. | `internal_error` |

## Examples

Start a stored DAG and wait up to 30 seconds for its result:

```json
{
  "action": "start",
  "name": "nightly-report",
  "params": {"TARGET": "orders"},
  "wait": true,
  "waitTimeoutSeconds": 30
}
```

Retry one step and its downstream steps:

```json
{
  "action": "retry",
  "name": "nightly-report",
  "dagRunId": "20260701T010000Z",
  "stepName": "load",
  "includeDownstream": true
}
```

Stop a run:

```json
{
  "action": "stop",
  "name": "nightly-report",
  "dagRunId": "20260701T010000Z"
}
```

Approve a waiting approval gate based on a read of attempt `att-2`, and wait
for the run to continue:

```json
{
  "action": "approve",
  "name": "nightly-report",
  "dagRunId": "20260701T010000Z",
  "stepName": "publish_gate",
  "inputs": {"ticket": "CHG-42"},
  "attemptId": "att-2",
  "wait": true
}
```
