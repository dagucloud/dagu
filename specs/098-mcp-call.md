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
- sampling, and elicitation without a connection: such a client advertises no
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
| `url` | yes, without `connection` | Absolute `http` or `https` endpoint. User info in the URL is rejected. With `connection`, it must be HTTPS on the connection's bound hosts. |
| `tool` | yes | Tool name. |
| `connection` | no | A connection of [Spec 107](107-connections.md), by name. The name is a literal. |
| `server` | no | The connection's service to call, such as `gmail`. A literal. Requires `connection`. |
| `arguments` | no | JSON object passed as the tool arguments. Dagu references resolve as in other `with` values. |
| `headers` | no | Map of request headers. Use `${SECRET}` references for credentials. With `connection`, `Authorization` is not allowed. |
| `include_raw` | no | When true, adds the full MCP result as `raw`, masked like the rest of the output. |
| `save_binary` | no | When true, saves binary content as run artifacts instead of writing it as base64. See [Binary content](#binary-content). |

Unknown fields fail validation.

A run checks each `connection` and `server` before its first step, as
Spec 107 [Admission Checks](107-connections.md#admission-checks) states.

### Connections

With `connection`, the call sends the connection's access token in place of
an `Authorization` header, following Spec 107
[Using a Connection](107-connections.md#using-a-connection).

- The endpoint is `url` when set. Otherwise, a `google` or `microsoft`
  connection calls the MCP server of the preset that `server` names, such
  as `https://gmailmcp.googleapis.com/mcp/v1` for `gmail`, and an `mcp`
  connection calls its own server, which takes no `server`. The token goes
  only over HTTPS to a bound host. An endpoint that is not one fails the
  step with kind `auth` before any request.
- The token stays valid for at least 5 minutes when the call takes it. It is
  masked in the step's logs from the step's start and redacted from its
  output, as is a token taken later in the step.
- A 401 reports the token to the token source once and sends the refused
  request again with the token the source returns. The source refreshes
  only while the refused token is the stored one. A second 401 fails with
  kind `auth`.
- A 403 fails with kind `auth`, without a refresh. With an
  `insufficient_scope` challenge, the error's `detail` is
  `insufficient_scope` and its `scope` the missing scope.
- A connection that needs reconnecting, is pending, or uses an invalid app
  fails with kind `auth` and `detail` `reconnect_required`, before any
  request. Any other failure to get the token, such as a coordinator that
  cannot hand out tokens to workers, fails with kind `auth` without that
  detail.
- The client advertises form elicitation, as Spec 107
  [Confirmation Requests](107-connections.md#confirmation-requests)
  requires. It declines a confirmation request at once, and the call fails
  with kind `confirmation_required`.
- On a worker, the token and the binding come from the coordinator
  (Spec 107 [Workers](107-connections.md#workers)).

```yaml
steps:
  - id: unread
    action: mcp.call
    with:
      connection: work-google
      server: gmail
      tool: search_threads
      arguments: { query: "is:unread newer_than:1d" }
```

### Output

On a completed call, stdout is one JSON object:

```json
{"content": [{"type": "text", "text": "..."}], "structured_content": null, "json": null, "is_error": false}
```

`content` is the tool's content array (`[]` when absent), `structured_content`
is the tool's structured result or `null`, and `is_error` is the MCP `isError`
flag. With `output: PAGE`, later steps read `${PAGE.content}` and
`${PAGE.structured_content}`.

`json` serves tools that return JSON as text instead of structured content.
When `structured_content` is `null` and `content` has exactly one text item
whose text is one JSON object or array, `json` is that value, so later steps
read `${PAGE.json.title}`. Otherwise `json` is `null`. Because `json` repeats
the data of that text, such a result counts about twice toward the DAG's
`max_output_size` (1MB by default) when the step sets `output`.

Numbers in `structured_content` and `raw` are decoded as 64-bit floating
point, so integers beyond 2^53 lose precision. Tools that return such IDs
should return them as strings. Numbers in `json` keep every digit.

### Binary content

Image and audio items carry their data as base64 in `data`, and embedded
resources in `resource.blob`. By default the output keeps them, so a large
image can exceed the DAG's `max_output_size` when the step sets `output`.

With `save_binary: true`, each such item's data is written to
`mcp/<step>/<call>/content-<index><ext>` in the run artifacts directory.
`<step>` is the step id, or its name when it has no id. `<call>` is a
directory each call creates, so retries and `foreach` items that run the same
step keep separate files. `<index>` is the item's position in `content`, and
`<ext>` follows the MIME type (`.bin` when unknown). The item drops `data` or
`resource.blob` and gains `path`, the file's path relative to the artifacts
directory; its other fields are kept. Later steps read the file at
`$DAG_RUN_ARTIFACTS_DIR/${PAGE.content[1].path}`. The data is written as
received and is not masked. `raw` is not changed.

`save_binary` enables artifacts for the DAG. When the DAG sets
`artifacts.enabled: false`, the step fails before connecting.

### Errors

The step fails when the call fails or `is_error` is true. When the call
produced no result, stdout is `{"error": {"kind": "...", "message": "..."}}`.
An error of a call through a connection adds `connection`, its name, to an
`auth` or `confirmation_required` error, and `detail` and `scope` as
[Connections](#connections) states. A `rate_limited` error adds
`retry_after`, the server's `Retry-After` as sent, when it sent one.
The step error message starts with `mcp.call: <kind> error:`, except when the
step's own `timeout_sec` elapses: Dagu then reports its step timeout error, and
stdout still carries the kind. Kinds:

| Kind | Meaning |
| --- | --- |
| `tool` | The tool ran and reported `isError: true`. The normal output is still written. |
| `auth` | The server answered HTTP 401 or 403, or a connection could not give the call a token. |
| `protocol` | A JSON-RPC error, a lost session, an invalid response, or an HTTP 3xx redirect. Redirects are not followed; configure the final URL. |
| `transport` | A network failure, such as a refused connection or a connection lost mid-response, or HTTP 500, 502, 503, or 504. |
| `rate_limited` | The server answered HTTP 429. The message carries the provider's message and any `Retry-After`. |
| `confirmation_required` | Through a connection, the server asked to confirm the action. The message names the ways to let it through: approve it once in Try it and save the approval where the provider's form offers that, ask the Workspace admin to change the confirmation rules, or use `http.request` with a standard connection. |
| `timeout` | The step or DAG timeout elapsed. |
| `canceled` | The run was stopped. |

When the call succeeds but the step cannot write its output or a saved file,
the step fails with `mcp.call: writing output:` or
`mcp.call: saving binary content:` and no kind.

On a stop or timeout, the client notifies the server that the request is
canceled and then closes the session. Against a server that does not answer,
the step can take up to 10 seconds longer to return.

### Credentials

Header values come from the DAG through value resolution, so secrets use the
existing `secrets` mechanism. Credentials are never placed in the URL.

The step masks these values with `***` in its output and error messages:

- the value of a header whose name contains `auth`, `cookie`, `key`, `token`,
  `secret`, or `password` (case-insensitive), and the part after the first
  space, such as the token in `Bearer <token>`
- the value of a URL query parameter whose name contains one of those words,
  decoded and as written

Other header and query values are not masked, so they never alter the tool
result.
Masking applies to string values in the output; object keys are not masked.
Values declared under `secrets` are also masked in step logs by Dagu.

## Examples

See `examples/mcp-call.yaml`.
