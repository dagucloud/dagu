# Spec: MCP Read Tool

Status: Implemented.

## Scope

This spec defines the `dagu_read` MCP tool-call contract: input fields,
addressing modes, target contracts, successful output, resource links,
read-only effects, and tool-level errors.

This spec does not define the MCP endpoint, authentication, API-key surfaces,
`dagu://` URI grammar, MCP resource reads, `dagu_change`, `dagu_execute`,
prompts, subscriptions, REST response schemas, Web UI behavior, storage
internals, or MCP SDK internals.

Related specs: [020: MCP Server](020-mcp-server.md) defines the MCP endpoint,
authentication, and `dagu://` resource URI model used by this tool.

The `data` object returned by this tool is part of the `dagu_read` contract.
REST API response schemas may be used by an implementation, but they are not
the MCP tool output contract.

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

Tool input is a JSON object. Fields outside this table fail with
`invalid_tool_input`.

| Field | Type | Allowed in mode | Required rule | Meaning |
| --- | --- | --- | --- | --- |
| `target` | string | Target mode only. | Required for target mode. | Case-sensitive read target literal. |
| `name` | string | Target mode only. | Required for `dag`, `dag_spec`, `run`, and `run_logs`; optional for `reference`; forbidden for `references`, `dags`, and `runs`. | DAG name or reference topic name. |
| `dagRunId` | string | Target mode only. | Required for `run` and `run_logs`; forbidden for all other targets. | DAG-run identifier. |
| `query` | string | Target mode only. | Optional only for `dags`, `runs`, and `run_logs`; forbidden for all other targets. | URL query string without a leading `?`. |
| `uri` | string | URI mode only. | Required for URI mode. | Direct `dagu://` resource URI. Query parameters for URI mode live inside this value. |

Supported fields, when present and not `null`, must be strings. `null` is
treated as absent. String field values are trimmed of leading and trailing
whitespace. The trimmed value is the effective value used for mode selection,
resource lookup, returned `target`, returned `uri`, and error fields. A string
that is empty after trimming is treated as absent. A forbidden field fails when
its trimmed value is non-empty. Supported target names are case-sensitive.

### Addressing modes

| Mode | Required fields | Forbidden fields | Read identity | Resolved output `target` |
| --- | --- | --- | --- | --- |
| Target mode | `target` | `uri` | Reads one named target using target-specific fields. | The supplied `target` value. |
| URI mode | `uri` | `target`, `name`, `dagRunId`, `query` | Reads the MCP resource identified by `uri`. URI query parameters live inside `uri`. | Derived from the URI family and path. |

Rules:

- A request with neither addressing mode fails with `invalid_tool_input`.
- A request with fields from both addressing modes fails with
  `invalid_tool_input`.
- In target mode, `target=reference` defaults `name` to `authoring` when
  `name` is absent.
- In URI mode, output `target` is derived as follows:
  - `dagu://reference` resolves to `references`.
  - `dagu://reference/{topic}` resolves to `reference`.
  - `dagu://dags` resolves to `dags`.
  - `dagu://dags/{name}/spec` resolves to `dag_spec`.
  - `dagu://runs` resolves to `runs`.
  - `dagu://runs/{name}/{dagRunId}` resolves to `run`.
  - `dagu://runs/{name}/{dagRunId}/logs` resolves to `run_logs`.
  - Query parameters do not affect target derivation.

### Target contracts

| Target | Required fields | Optional fields | Output `uri` | Minimum `data` contract |
| --- | --- | --- | --- | --- |
| `references` | None. | None. | Omitted in target mode; `dagu://reference` in URI mode. | Reference collection model. |
| `reference` | None. | `name`. | `dagu://reference/{name}` after applying the default topic when needed. | `data.mimeType` is `text/markdown`; `data.text` is a string. |
| `dags` | None. | `query`. | Omitted in target mode; `dagu://dags` plus query in URI mode. | DAG collection model. |
| `dag` | `name`. | None. | Omitted. Spec 020 does not define a DAG-details resource URI. | DAG detail model. |
| `dag_spec` | `name`. | None. | `dagu://dags/{name}/spec`. | DAG spec model. |
| `runs` | None. | `query`. | Omitted in target mode; `dagu://runs` plus query in URI mode. | Run collection model. |
| `run` | `name`, `dagRunId`. | None. | `dagu://runs/{name}/{dagRunId}`. | Run detail model. |
| `run_logs` | `name`, `dagRunId`. | `query`. | `dagu://runs/{name}/{dagRunId}/logs`, with the supplied query appended when present. | Run-log model. |

Minimum `data` models:

| Model | Required shape |
| --- | --- |
| Reference collection | `data.items` is an array. Each item has `name` string, `uri` string, and `mimeType` string. The array includes `authoring`, `tools`, and `notifications`. |
| Reference topic | `data.mimeType` is `text/markdown`; `data.text` is the Markdown content string. |
| DAG collection | `data.items` is an array. Each item has `name` string and `uri` string. `uri` is the canonical `dagu://dags/{name}/spec` URI for that DAG. |
| DAG detail | `data.name` is the DAG name string. `data.specUri` is the canonical `dagu://dags/{name}/spec` URI. |
| DAG spec | `data.name` is the DAG name string. `data.mimeType` is `application/yaml`. `data.spec` is the YAML document string. `data.errors` is an array of strings. |
| Run collection | `data.items` is an array. Each item has `name` string, `dagRunId` string, `uri` string, `status` number, and `statusLabel` string. `uri` is the canonical `dagu://runs/{name}/{dagRunId}` URI. |
| Run detail | `data.name` is the DAG name string. `data.dagRunId` is the DAG-run ID string. `data.uri` is the canonical `dagu://runs/{name}/{dagRunId}` URI. `data.status` is a number. `data.statusLabel` is a string. |
| Run logs | `data.schedulerLog` is an object with `content` string, `lineCount` number, `totalLines` number, and `hasMore` boolean. `data.stepLogs` is an array. Each step-log item has `stepName` string, `status` number, `statusLabel` string, `hasStdout` boolean, and `hasStderr` boolean. |

Implementations may add fields inside `data`. Conformance tests must not require
absence of additional fields.

### Query handling

Rules:

- The `query` field is allowed only in target mode for `dags`, `runs`, and
  `run_logs`.
- The `query` field must not start with `?`.
- The same parameter names are allowed in URI-mode collection and log URIs.
- Query parameter order is not normative.

The effective query string is parsed as URL query data. An empty effective
query string is treated as absent. Invalid percent-encoding, unknown
parameters, repeated parameters not marked repeatable, empty values, and values
outside the table below fail with `invalid_tool_input` in target mode and
`invalid_resource_uri` in URI mode.

| Target | Parameter | Value contract |
| --- | --- | --- |
| `dags` | `page` | Integer greater than or equal to `1`. |
| `dags` | `perPage` | Integer from `1` through `1000`. |
| `dags` | `name` | Non-empty string. |
| `dags` | `labels` | Comma-separated non-empty label strings. |
| `dags` | `sort` | One of `name` or `nextRun`. |
| `dags` | `order` | One of `asc` or `desc`. |
| `runs` | `name` | Non-empty string. |
| `runs` | `dagRunId` | Non-empty string. The value `latest` is allowed. |
| `runs` | `status` | Repeatable integer enum value from `0` through `8`. |
| `runs` | `fromDate` | Unix timestamp in seconds. |
| `runs` | `toDate` | Unix timestamp in seconds. |
| `runs` | `limit` | Integer from `1` through `500`. |
| `runs` | `cursor` | Non-empty opaque string. |
| `runs` | `labels` | Comma-separated non-empty label strings. |
| `run_logs` | `tail` | Integer greater than or equal to `1`. |

### Output

A successful MCP tool result has:

- A first text content item whose text is exactly `Dagu read completed.`.
- Structured output with this exact top-level envelope:

```json
{
  "target": "run_logs",
  "uri": "dagu://runs/nightly-report/20260701T010000Z/logs?tail=100",
  "data": {},
  "references": [
    "dagu://reference/authoring",
    "dagu://reference/tools",
    "dagu://reference/notifications"
  ]
}
```

Rules:

- `target` is the resolved read target.
- `uri` is present when the read has a canonical `dagu://` resource URI. URI
  mode always returns the validated resource URI, including collection URIs.
- `data` contains the read result and must satisfy the target contract above.
- `references` contains exactly `dagu://reference/authoring`,
  `dagu://reference/tools`, and `dagu://reference/notifications`; order is not
  normative.
- A result with `uri` has exactly two content items: `content[0]` is a text
  content item with text `Dagu read completed.`, and `content[1]` is a
  resource-link content item for that URI.
- A result without `uri` has exactly one content item: `content[0]` is a text
  content item with text `Dagu read completed.`.
- URI path segments in returned URIs follow Spec 020 escaping.

Resource-link content items have MCP content type `resource_link`. The required
fields are `uri`, `name`, and `mimeType`. Optional MCP resource-link fields,
such as `title` and `description`, may be present.

| URI shape | Resource-link `name` | Resource-link `mimeType` |
| --- | --- | --- |
| `dagu://reference` | `dagu_references` | `application/json` |
| `dagu://reference/{topic}` | `dagu_reference` | `text/markdown` |
| `dagu://dags` | `dags` | `application/json` |
| `dagu://dags/{name}/spec` | `dag_spec` | `application/yaml` |
| `dagu://runs` | `dag_runs` | `application/json` |
| `dagu://runs/{name}/{dagRunId}` | `dag_run` | `application/json` |
| `dagu://runs/{name}/{dagRunId}/logs` | `dag_run_logs` | `application/json` |

## Errors

Failed tool calls return an MCP tool result with `isError=true`. When the
transport supports structured output, the structured output is the error object.
Text content may include the error message as a fallback.

The error object has this shape:

```json
{
  "code": "invalid_tool_input",
  "message": "The dagRunId field is required for target run_logs.",
  "target": "run_logs",
  "field": "dagRunId",
  "uri": "dagu://runs/nightly-report/20260701T010000Z/logs",
  "details": {}
}
```

`code` and `message` are required. Clients must branch on `code`, not parse
`message`.

Optional field rules:

| Field | Required when |
| --- | --- |
| `target` | A valid target has been resolved, or an unsupported target value was supplied. |
| `field` | Exactly one input field caused the failure. For an unknown field, this is the unknown field name. |
| `uri` | URI mode supplied a non-empty `uri`, or target mode resolved a singular resource URI before lookup failed. |
| `details` | More than one field caused the failure, or the server needs to expose structured validation details. |

Common conditions map to codes as follows:

| Condition | Code |
| --- | --- |
| Authentication is required and the request is not authenticated. | `unauthenticated` |
| The accepted credential is not authorized for the requested read. | `unauthorized` |
| Tool input is not a JSON object. | `invalid_tool_input` |
| Tool input contains an unknown field. | `invalid_tool_input` |
| A supported field has a non-string, non-null value. | `invalid_tool_input` |
| The request provides neither `target` nor `uri` after trimming. | `invalid_tool_input` |
| The request combines URI mode with any non-empty target-mode field. | `invalid_tool_input` |
| A required target-mode field is absent after trimming. | `invalid_tool_input` |
| A field is supplied for a target that forbids it. | `invalid_tool_input` |
| `target` is present but is not a supported case-sensitive read target. | `unsupported_read_target` |
| The `query` field is supplied in URI mode, supplied for a target that does not allow it, starts with `?`, contains malformed URL query encoding, or contains unsupported parameters. | `invalid_tool_input` |
| `uri` is malformed, uses the wrong scheme, has an unsupported path shape, or contains malformed query parameters. | `invalid_resource_uri` |
| `uri` identifies a resource family that this tool cannot read. | `unsupported_resource` |
| The named DAG, DAG run, log stream, or reference topic does not exist. | `resource_not_found` |
| The resource exists but cannot currently be read. | `resource_unavailable` |
| The server fails unexpectedly while handling the read. | `internal_error` |

## Examples

URI mode direct reference read:

```json
{
  "uri": "dagu://reference/authoring"
}
```

Target mode default reference:

```json
{
  "target": "reference"
}
```

Target mode DAG spec:

```json
{
  "target": "dag_spec",
  "name": "nightly-report"
}
```

Target mode run logs with query:

```json
{
  "target": "run_logs",
  "name": "nightly-report",
  "dagRunId": "20260701T010000Z",
  "query": "tail=100"
}
```

Invalid mixed-mode request:

```json
{
  "uri": "dagu://reference/authoring",
  "target": "reference"
}
```

Expected error code:

```json
{
  "code": "invalid_tool_input"
}
```
