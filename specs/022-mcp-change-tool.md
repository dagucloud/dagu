# Spec: MCP Change Tool

Status: Implemented.

## Scope

This spec defines the `dagu_change` MCP tool-call contract: input fields,
preview behavior, apply behavior, successful output, resource links, mutation
effects, and tool-level errors.

This spec does not define the MCP endpoint, authentication, API-key surfaces,
`dagu://` URI grammar, DAG YAML language semantics, `dagu_read`,
`dagu_execute`, prompts, subscriptions, Web UI behavior, storage internals,
workspace selection, or MCP SDK internals.

Related specs:

- [020: MCP Server](020-mcp-server.md) defines the MCP endpoint,
  authentication, and `dagu://` resource URI model used by this tool.
- [021: MCP Read Tool](021-mcp-read-tool.md) defines how clients read the DAG
  spec resource linked from this tool.

## Goal

`dagu_change` gives MCP clients one safe way to validate a DAG YAML change
before writing it. Preview is the default behavior. Apply is explicit and
writes only after the same validation succeeds.

## Behavior

### Tool Identity

Rules:

- The tool name is `dagu_change`.
- The tool supports DAG YAML upsert only.
- A preview call must not create, update, delete, start, enqueue, retry, or
  stop any DAG or DAG run.
- An apply call may create or update one stored DAG only when validation
  succeeds.
- Audit, metrics, logging, and transport side effects are allowed.

### Input

Tool input is a JSON object. Fields outside this table fail with
`invalid_tool_input`.

| Field | Type | Required rule | Meaning |
| --- | --- | --- | --- |
| `mode` | string | Optional. Defaults to `preview`. | Change execution mode. Supported values are `preview` and `apply`. |
| `type` | string | Optional. Defaults to `upsert_dag`. | Change type. The only supported value is `upsert_dag`. |
| `name` | string | Required. | Target DAG name. |
| `spec` | string | Required. | DAG YAML document to validate and optionally store. |

Rules:

- Supported fields, when present and not `null`, must be strings.
- `null` is treated as absent.
- `mode`, `type`, and `name` are trimmed of leading and trailing whitespace
  before validation.
- `spec` is not trimmed or normalized. It fails only when it is empty or all
  whitespace.
- Supported values are case-sensitive.
- A missing or empty `name` fails with `invalid_tool_input`.
- A missing or all-whitespace `spec` fails with `invalid_tool_input`.
- An unsupported `mode` fails with `unsupported_change_mode`.
- An unsupported `type` fails with `unsupported_change_type`.

### Change Type

`upsert_dag` validates a DAG YAML document for the supplied DAG name.

Rules:

- Validation uses Dagu's DAG validation rules.
- A validation failure is not a tool-level error.
- When validation fails, the tool returns a successful MCP tool result with
  `valid=false`, `applied=false`, and validation errors in the structured
  output.
- This spec defines the MCP tool contract. The DAG YAML fields and validation
  rules are owned by the DAG YAML specs.

### Preview Mode

Rules:

- `mode=preview` validates the supplied DAG YAML and returns the validation
  result.
- `mode=preview` must not write the DAG even when validation succeeds.
- `mode=preview` must not write the DAG when validation fails.
- `applied` is `false` in preview output.
- `created` and `updated` are omitted in preview output.

### Apply Mode

Rules:

- `mode=apply` validates the supplied DAG YAML before writing.
- If validation fails, the call must not write the DAG and returns
  `valid=false` with `applied=false`.
- If validation succeeds and no stored DAG exists for `name`, the call creates
  the DAG and returns `created=true`, `updated=false`, and `applied=true`.
- If validation succeeds and a stored DAG exists for `name`, the call updates
  that DAG and returns `created=false`, `updated=true`, and `applied=true`.
- A successful apply affects only the DAG identified by `name`.
- A successful apply must not start, enqueue, retry, or stop a DAG run.

### Output

A successful MCP tool result has one text content item and structured output.

Text content is mode and validation dependent:

| Condition | Text |
| --- | --- |
| Validation failed. | `DAG spec is not valid; no changes were applied.` |
| Preview validation succeeded. | `DAG spec is valid. Re-run with mode=apply to write it.` |
| Apply validation succeeded and the DAG was written. | `DAG change applied.` |

Structured output has this top-level envelope:

```json
{
  "mode": "preview",
  "type": "upsert_dag",
  "dagName": "nightly-report",
  "valid": true,
  "errors": [],
  "applied": false,
  "dag": {},
  "dagUri": "dagu://dags/nightly-report/spec",
  "references": [
    "dagu://reference/authoring",
    "dagu://reference/tools",
    "dagu://reference/notifications"
  ]
}
```

Required fields:

| Field | Required shape |
| --- | --- |
| `mode` | Effective mode: `preview` or `apply`. |
| `type` | Effective type: `upsert_dag`. |
| `dagName` | Effective DAG name. |
| `valid` | Boolean validation result. |
| `errors` | Array of validation errors. Empty when `valid=true`. |
| `applied` | Boolean. `true` only when the DAG was written. |
| `dagUri` | Canonical `dagu://dags/{name}/spec` URI for the target DAG. |
| `references` | Exactly `dagu://reference/authoring`, `dagu://reference/tools`, and `dagu://reference/notifications`; order is not normative. |

Conditional fields:

| Field | Required when |
| --- | --- |
| `dag` | Validation succeeds and Dagu can return a normalized DAG model. |
| `created` | `applied=true`. |
| `updated` | `applied=true`. |

Implementations may add fields inside the structured output. Conformance tests
must not require absence of additional fields.

The successful result includes a resource-link content item for `dagUri` when
`dagUri` is present.

Resource-link content item rules:

- MCP content type is `resource_link`.
- `uri` is the canonical `dagu://dags/{name}/spec` URI.
- `name` is `dag_spec`.
- `mimeType` is `application/yaml`.

URI path segments in returned URIs follow Spec 020 escaping.

## Errors

Failed tool calls return an MCP tool result with `isError=true`. When the
transport supports structured output, the structured output is the error object.
Text content may include the error message as a fallback.

The error object has this shape:

```json
{
  "code": "invalid_tool_input",
  "message": "The spec field is required.",
  "mode": "preview",
  "type": "upsert_dag",
  "dagName": "nightly-report",
  "field": "spec",
  "dagUri": "dagu://dags/nightly-report/spec",
  "details": {}
}
```

`code` and `message` are required. Clients must branch on `code`, not parse
`message`.

Optional field rules:

| Field | Required when |
| --- | --- |
| `mode` | A valid mode has been resolved, or an unsupported mode value was supplied. |
| `type` | A valid type has been resolved, or an unsupported type value was supplied. |
| `dagName` | A non-empty DAG name has been resolved. |
| `field` | Exactly one input field caused the failure. For an unknown field, this is the unknown field name. |
| `dagUri` | A non-empty DAG name has been resolved. |
| `details` | More than one field caused the failure, or the server needs to expose structured error details. |

Common conditions map to codes as follows:

| Condition | Code |
| --- | --- |
| Authentication is required and the request is not authenticated. | `unauthenticated` |
| The accepted credential is not authorized for the requested change. | `unauthorized` |
| Tool input is not a JSON object. | `invalid_tool_input` |
| Tool input contains an unknown field. | `invalid_tool_input` |
| A supported field has a non-string, non-null value. | `invalid_tool_input` |
| `name` is absent or empty after trimming. | `invalid_tool_input` |
| `spec` is absent, empty, or all whitespace. | `invalid_tool_input` |
| `mode` is present but is not `preview` or `apply`. | `unsupported_change_mode` |
| `type` is present but is not `upsert_dag`. | `unsupported_change_type` |
| DAG validation fails. | No tool error. Return `valid=false`. |
| The target DAG cannot currently be read or written during apply. | `resource_unavailable` |
| The server fails unexpectedly while handling the change. | `internal_error` |

## Examples

Preview a valid DAG:

```json
{
  "mode": "preview",
  "type": "upsert_dag",
  "name": "nightly-report",
  "spec": "steps:\n  - name: hello\n    run: echo hello\n"
}
```

Expected structured output subset:

```json
{
  "mode": "preview",
  "type": "upsert_dag",
  "dagName": "nightly-report",
  "valid": true,
  "applied": false,
  "dagUri": "dagu://dags/nightly-report/spec"
}
```

Apply a valid DAG:

```json
{
  "mode": "apply",
  "type": "upsert_dag",
  "name": "nightly-report",
  "spec": "steps:\n  - name: hello\n    run: echo hello\n"
}
```

Expected structured output subset:

```json
{
  "mode": "apply",
  "type": "upsert_dag",
  "dagName": "nightly-report",
  "valid": true,
  "applied": true,
  "created": true,
  "updated": false,
  "dagUri": "dagu://dags/nightly-report/spec"
}
```

Preview an invalid DAG:

```json
{
  "name": "broken",
  "spec": "steps:\n  - name: [\n"
}
```

Expected structured output subset:

```json
{
  "mode": "preview",
  "type": "upsert_dag",
  "dagName": "broken",
  "valid": false,
  "applied": false
}
```

Unsupported mode:

```json
{
  "mode": "write",
  "name": "nightly-report",
  "spec": "steps:\n  - name: hello\n    run: echo hello\n"
}
```

Expected error code:

```json
{
  "code": "unsupported_change_mode"
}
```
