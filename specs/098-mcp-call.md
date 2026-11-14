# Spec: MCP Call Action

## Status

Implemented.

This spec defines the built-in `mcp.call` action.

## Scope

`mcp.call` calls one named tool on a remote MCP server over Streamable HTTP(S)
and publishes the result as step output.

This spec covers:

- the `with` fields and their validation
- the session lifecycle and the output shape
- error kinds, credentials, timeouts, cancellation, and retries

This spec does not define:

- local stdio servers or the legacy HTTP+SSE transport
- MCP resources, prompts, or tasks
- server-initiated requests (sampling, elicitation); the client advertises no
  capability for them
- connection pooling or a shared server registry
- provider-specific behavior; any protocol-compatible server works

## Goal

Workflow authors call a deterministic MCP tool from a DAG without an LLM and
without hand-written MCP session handling.

## Behavior

The step connects, initializes a session with the official MCP Go SDK, sends
one `tools/call`, closes the session, and exits. Each run makes exactly one
call; the action never retries. Only an explicit step `retry_policy` re-runs
it.

### with fields

| Field | Required | Meaning |
| --- | --- | --- |
| `url` | yes | Absolute `http` or `https` endpoint. User info in the URL is rejected. |
| `tool` | yes | Tool name. |
| `arguments` | no | JSON object passed as the tool arguments. Dagu references resolve as in other `with` values. |
| `headers` | no | Map of request headers. Use `${SECRET}` references for credentials. |
| `include_raw` | no | When true, adds the unmodified MCP result as `raw`. |

Unknown fields fail validation.

### Output

On a completed call, stdout is one JSON object:

```json
{"content": [{"type": "text", "text": "..."}], "structured_content": null, "is_error": false}
```

`content` is the tool's content array (`[]` when absent), `structured_content`
is the tool's structured result or `null`, and `is_error` is the MCP `isError`
flag. With `output: PAGE`, later steps read `${PAGE.content}` and
`${PAGE.structured_content}`.

### Errors

The step fails when the call fails or `is_error` is true. The error message
starts with `mcp.call: <kind> error:` and, when the call produced no result,
stdout is `{"error": {"kind": "...", "message": "..."}}`. Kinds:

| Kind | Meaning |
| --- | --- |
| `tool` | The tool ran and reported `isError: true`. The normal output is still written. |
| `auth` | The server answered HTTP 401 or 403. |
| `protocol` | A JSON-RPC error, a lost session, an invalid response, or an HTTP 3xx redirect. Redirects are not followed; configure the final URL. |
| `transport` | A network failure such as a refused connection. |
| `timeout` | The step timeout elapsed. |
| `canceled` | The run was stopped. |

### Credentials

Header values come from the DAG through value resolution, so secrets use the
existing `secrets` mechanism. Header values are masked in step logs and in
error messages and are never placed in the URL.

## Examples

See `examples/mcp-call.yaml`.
