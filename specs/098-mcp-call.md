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

When the server ends the call's SSE stream after an event ID, the client
resumes the stream with `Last-Event-ID`. Resumption replays events the server
stored and does not send `tools/call` again.

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

Numbers in `structured_content` and `raw` are decoded as 64-bit floating
point, so integers beyond 2^53 lose precision. Tools that return such IDs
should return them as strings.

### Errors

The step fails when the call fails or `is_error` is true. When the call
produced no result, stdout is `{"error": {"kind": "...", "message": "..."}}`.
The step error message starts with `mcp.call: <kind> error:`, except when the
step's own `timeout_sec` elapses: Dagu then reports its step timeout error, and
stdout still carries the kind. Kinds:

| Kind | Meaning |
| --- | --- |
| `tool` | The tool ran and reported `isError: true`. The normal output is still written. |
| `auth` | The server answered HTTP 401 or 403. |
| `protocol` | A JSON-RPC error, a lost session, an invalid response, or an HTTP 3xx redirect. Redirects are not followed; configure the final URL. |
| `transport` | A network failure, such as a refused connection or a connection lost mid-response. |
| `timeout` | The step or DAG timeout elapsed. |
| `canceled` | The run was stopped. |

### Credentials

Header values come from the DAG through value resolution, so secrets use the
existing `secrets` mechanism. Credentials are never placed in the URL.

The step masks these values with `***` in its output and error messages:

- the value of a header whose name contains `auth`, `cookie`, `key`, `token`,
  `secret`, or `password` (case-insensitive), and the part after the first
  space, such as the token in `Bearer <token>`
- URL query values of four or more characters, decoded and as written

Other header values are not masked, so they never alter the tool result.
Masking applies to string values in the output; object keys are not masked.
Values declared under `secrets` are also masked in step logs by Dagu.

## Examples

See `examples/mcp-call.yaml`.
