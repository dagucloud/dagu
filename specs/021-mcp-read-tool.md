# Spec: MCP Read Tool

Status: Draft.

## Scope

This spec defines the `dagu_read` MCP tool-call contract: input fields, target
selection, successful output, resource links, read-only effects, and tool-level
errors.

This spec does not define the MCP endpoint, authentication, API-key surfaces,
`dagu://` URI grammar, MCP resource reads, `dagu_change`, `dagu_execute`,
prompts, subscriptions, REST response schemas, Web UI behavior, storage
internals, or MCP SDK internals.

Related specs: [020: MCP Server](020-mcp-server.md) defines the MCP endpoint,
authentication, and `dagu://` resource URI model used by this tool.

## Goal

`dagu_read` gives MCP clients one read-only way to inspect Dagu state and
built-in MCP reference content while preserving the resource URI model from
Spec 020.

## Behavior

### Tool Identity

Rules:

- The tool name is `dagu_read`.
- The tool is read-only from the Dagu domain perspective.
- A successful call must not create, update, delete, start, enqueue, retry, or
  stop DAGs or DAG runs.
- Audit, metrics, logging, and transport side effects are allowed.

### Input

Tool input is a JSON object. Supported fields are:

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `target` | string | Required when `uri` is absent. | Named read target. |
| `name` | string | Target-specific. | DAG name or reference topic name. |
| `dagRunId` | string | Target-specific. | DAG-run identifier for run reads. |
| `query` | string | Optional. | Query string for supported list or log targets, without a leading `?`. |
| `uri` | string | Alternative to `target`. | Direct `dagu://` resource URI. |

Target selection rules:

- A request identifies the read by either `uri` or `target`.
- When `uri` is present, the tool reads that `dagu://` resource according to
  Spec 020.
- Direct URI query parameters must be encoded in `uri`, not `query`.
- `query` is valid only for `dags`, `runs`, and `run_logs`.
- `query` does not include a leading `?`.
- `target=reference` defaults to `authoring` when both `uri` and `name` are
  absent.

Supported targets:

| Target | Required fields | Optional fields | Content |
| --- | --- | --- | --- |
| `reference` | None. | `name` or `uri`. | Built-in MCP reference text. |
| `dags` | None. | `query`. | DAG summaries visible to the caller. |
| `dag` | `name`. | None. | DAG details. |
| `dag_spec` | `name`. | None. | Current YAML spec data. |
| `runs` | None. | `query`. | DAG-run summaries visible to the caller. |
| `run` | `name`, `dagRunId`. | None. | DAG-run details. |
| `run_logs` | `name`, `dagRunId`. | `query`. | DAG-run logs. |

### Output

A successful MCP tool result includes human-readable result content. Its
structured output uses this JSON object shape:

```json
{
  "target": "run_logs",
  "uri": "dagu://runs/nightly-report/20260701T010000Z/logs?tail=100",
  "data": {
    "message": "Read run logs.",
    "content": {}
  },
  "references": [
    "dagu://reference/authoring"
  ]
}
```

Rules:

- `target` is the selected read target.
- `uri` is omitted when no concrete resource URI is known.
- Result content includes a human-readable completion message and may include a
  resource link when a concrete URI is known.
- `data` contains the read result, and the exact JSON field set inside `data` is
  not frozen by this spec.
- `references` contains built-in reference resource URIs useful to clients.

Resource link rules:

- `dag` and `dag_spec` use canonical URI `dagu://dags/{name}/spec`.
- `run` uses canonical URI `dagu://runs/{name}/{dagRunId}`.
- `run_logs` uses canonical URI `dagu://runs/{name}/{dagRunId}/logs`,
  including query parameters when supplied.
- `reference` uses the reference URI read.
- List targets are not required to return a singular `uri`.
- URI path segments are encoded according to Spec 020.

## Errors

Tool-level failures return an error JSON object with this shape:

```json
{
  "code": "invalid_tool_input",
  "message": "The dagRunId field is required for target run_logs.",
  "target": "run_logs",
  "field": "dagRunId",
  "details": {}
}
```

`target`, `field`, `uri`, and `details` are omitted when they do not apply.
Clients must branch on `code`, not parse `message`.

Common conditions map to codes as follows:

| Condition | Code |
| --- | --- |
| Authentication is required and the request is not authenticated. | `unauthenticated` |
| The accepted credential is not authorized for the requested read. | `unauthorized` |
| The tool input is not a JSON object, omits required fields, combines fields invalidly, or provides an invalid `query`. | `invalid_tool_input` |
| `target` is present but is not a supported read target. | `unsupported_read_target` |
| `uri` is malformed, uses the wrong scheme, has an unsupported path shape, or contains malformed query parameters. | `invalid_resource_uri` |
| `uri` identifies a resource family that this tool cannot read. | `unsupported_resource` |
| The named DAG, DAG run, log stream, or reference topic does not exist. | `resource_not_found` |
| The resource exists but cannot currently be read. | `resource_unavailable` |
| The server fails unexpectedly while handling the read. | `internal_error` |

## Examples

List DAGs:

```json
{
  "target": "dags",
  "query": "name=nightly&perPage=20"
}
```

Read the default reference topic:

```json
{
  "target": "reference"
}
```

Read a named reference topic:

```json
{
  "target": "reference",
  "name": "authoring"
}
```

Read current DAG spec data:

```json
{
  "target": "dag_spec",
  "name": "nightly-report"
}
```

Read run logs by resource URI:

```json
{
  "uri": "dagu://runs/nightly-report/20260701T010000Z/logs?tail=100"
}
```
