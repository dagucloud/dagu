# Spec: MCP Execute Tool

Status: Implemented.

## Scope

This spec defines the `dagu_execute` MCP tool-call contract: input fields,
action contracts, wait behavior, successful output, resource links, and
tool-level errors.

This spec does not define the MCP endpoint, authentication, API-key surfaces,
`dagu://` URI grammar, MCP resource reads, `dagu_read`, `dagu_change`,
prompts, subscriptions, REST response schemas, queue selection policy, run
execution semantics, storage internals, or MCP SDK internals.

Related specs: [020: MCP Server](020-mcp-server.md) defines the MCP endpoint,
authentication, and `dagu://` resource URI model used by this tool.
[021: MCP Read Tool](021-mcp-read-tool.md) defines the run detail model
reused by wait output.

## Goal

`dagu_execute` gives MCP clients one way to start, enqueue, retry, and stop
DAG-runs, and optionally to wait for an identified run's result inside the
same tool call.

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
| `action` | string | Required. One of `start`, `enqueue`, `retry`, `stop`. | Execution action. |
| `targetType` | string | Optional. One of `dag`, `inline_spec`, `run`. | Target type. Defaults to `run` for `retry` and `stop`, `inline_spec` when `spec` is present, otherwise `dag`. |
| `name` | string | Required. | DAG name, including the identity used for inline runs. |
| `spec` | string | Required for `targetType=inline_spec`; otherwise forbidden. | Inline DAG YAML document for `start` and `enqueue`. |
| `dagRunId` | string | Required for `retry` and `stop`; optional override for `start` and `enqueue`. | DAG-run identifier. |
| `params` | string or object | Optional for `start` and `enqueue`. | Runtime parameters. An object is canonicalized to its compact JSON encoding. |
| `queue` | string | Optional for `enqueue`. | Queue override. |
| `singleton` | boolean | Optional for `start` and `enqueue`. | Reject duplicate running or queued DAG-runs. |
| `noReuse` | boolean | Optional for `start` and `enqueue`. | Execute eligible build steps without reusing prior materializations. |
| `labels` | array of strings | Optional for `start` and `enqueue`. | Additional labels, each `key=value` or key-only. |
| `stepName` | string | Optional for `retry`. | Step to retry. |
| `includeDownstream` | boolean | Optional for `retry`. Requires `stepName`. | Retry the selected step and every reachable descendant. |
| `wait` | boolean | Optional. | Wait for the identified run to reach a terminal state. |
| `waitTimeoutSeconds` | integer | Optional. Requires `wait`. From `1` through `300`; defaults to `60`. | Maximum wait duration. |

Rules:

- Unknown fields, non-null values of the wrong type, unknown `action` values,
  and unknown `targetType` values fail with `invalid_tool_input`.
- `null` field values are treated as absent.
- String values other than `spec` are trimmed of leading and trailing
  whitespace; a string that is empty after trimming is treated as absent.
- `targetType=run` is valid only for `retry` and `stop`.
- `params`, `singleton`, `noReuse`, and `labels` are valid only for `start`
  and `enqueue`; `queue` is valid only for `enqueue`; and `stepName` and
  `includeDownstream` are valid only for `retry`.
- Supplying a field for an action that does not support it fails with
  `invalid_tool_input`, even when a boolean is `false` or an array is empty.

### Action contracts

| Action | Effect |
| --- | --- |
| `start` | Runs a stored DAG (`targetType=dag`) or an inline spec (`targetType=inline_spec`). |
| `enqueue` | Enqueues a stored DAG or inline spec for queue-based execution. |
| `retry` | Retries an existing DAG-run, optionally scoped to `stepName` and its downstream steps. |
| `stop` | Requests termination of an existing DAG-run. |

### Wait behavior

Rules:

- Wait applies after the requested action succeeds and only when a run is
  identified by `name` and a DAG-run ID.
- The call returns when the run reaches a terminal state or the timeout
  elapses, whichever comes first.
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
- With `wait`, output has `completed` boolean plus the last observed `status`
  and `statusLabel` when at least one poll succeeded.
- With `wait`, a completed run's output has `run` holding the Spec 021 run
  detail model, including per-step statuses and errors.
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
| A singleton run is already running or queued, or the action conflicts with current run state. | `conflict` |
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
