# Spec: MCP Server

Status: Draft.

## Scope

This spec defines the MCP endpoint, authentication contract, API-key surface
acceptance, and `dagu://` resource reads exposed by Dagu.

This spec does not define the MCP tool specs (`dagu_read`, `dagu_change`, or
`dagu_execute`), prompts, subscriptions, audit events, Web UI behavior, storage
internals, or MCP SDK internals.

## Goal

Dagu exposes MCP through one authenticated endpoint. MCP clients read Dagu
objects through `dagu://` resource URIs instead of depending on REST paths,
local files, or generated API structs.

## Behavior

### Endpoint

Dagu exposes MCP over Streamable HTTP.

Rules:

- The MCP endpoint path is `/mcp` when the Dagu HTTP server has no base path.
- When the Dagu HTTP server is mounted under a base path, the MCP endpoint path
  is that base path followed by `/mcp`.
- The MCP endpoint belongs to the same Dagu server origin as the REST API and
  Web UI.
- Requests sent to any other path are outside this spec.
- The endpoint must not expose filesystem paths or storage paths as MCP route
  structure.

### Authentication

MCP requests use the Dagu server's configured authentication mode.

Rules:

- MCP requests are authenticated by the Dagu HTTP server before MCP request
  handling.
- When authentication is disabled for the Dagu server, the MCP endpoint accepts
  requests without credentials.
- `Authorization` is the canonical credential carrier.
- Built-in authentication uses `Authorization: Bearer <token>`, where
  `<token>` is a Dagu login token or a Dagu API key.
- Basic authentication uses
  `Authorization: Basic <base64(username:password)>`.
- For clients that cannot send headers, the server may accept `?token=<token>`
  as a bearer-token fallback when no `Authorization` header is present.
- An accepted credential establishes identity only. Requested Dagu operations
  still apply normal authorization checks.

This spec does not define token issuance, token internals, API-key storage,
OIDC login flow, or exact HTTP challenge bodies.

### API-Key Surface

API keys can restrict which server surfaces accept them.

Rules:

- The MCP endpoint accepts an API key only when that key is valid and includes
  the `mcp` surface.
- An API key that includes only the `rest_api` surface is not accepted by the
  MCP endpoint.
- An API key that includes only the `mcp` surface is not accepted by REST API
  routes solely because it is valid for MCP.
- Surface acceptance is checked by the Dagu HTTP server before an MCP request
  is handled.

### Resource URI Model

Dagu MCP resources use the `dagu` URI scheme.

URI identity rules:

- Resource URIs must use the `dagu` scheme.
- The URI host identifies the resource family.
- Path segments identify a resource inside that family.
- DAG names, DAG-run IDs, and reference topic names that contain URI-reserved
  characters must be percent-encoded as path segments.
- A resource URI identifies current Dagu state at read time. It is not a
  historical snapshot unless the named Dagu object is itself historical.
- Resource reads are read-only. Resource URIs do not define mutation or run
  control behavior.

Collection resource rules:

- Collection resource reads enumerate resources visible to the caller. They
  remain resource reads and are not tool calls.
- Collection resources may accept query parameters supported by the
  corresponding Dagu list API.

Query parameter rules:

- Query parameters are unsupported unless a resource family explicitly allows
  them.
- Query strings use normal URL query encoding.
- Unknown or malformed query parameters fail with `invalid_resource_uri`.
- Query parameters must not change the identity of a singular resource.

Invalid URI rules:

- Unsupported URI hosts, unsupported path shapes, and malformed URIs fail with
  `invalid_resource_uri`.

Supported resource families:

| Family | URI shape | MIME type | Content |
| --- | --- | --- | --- |
| Reference collection | `dagu://reference` | `application/json` | Available built-in MCP reference resources. |
| Reference topic | `dagu://reference/{topic}` | `text/markdown` | Built-in MCP reference text for a server-advertised topic. |
| DAG collection | `dagu://dags` | `application/json` | DAG summaries visible to the caller. |
| DAG spec | `dagu://dags/{name}/spec` | `application/yaml` | Current YAML specification for the named DAG. |
| Run collection | `dagu://runs` | `application/json` | DAG-run summaries visible to the caller. |
| Run details | `dagu://runs/{name}/{dagRunId}` | `application/json` | Current DAG-run details for the named DAG run. |
| Run logs | `dagu://runs/{name}/{dagRunId}/logs` | `application/json` | Current log data for the named DAG run. |

### Resource Content

Rules:

- Reference collection resources return the available reference resource URIs
  and titles or descriptions when available.
- Reference topic resources return Markdown text owned by the Dagu MCP server.
- DAG collection resources return a paginated or bounded list of DAG summaries
  visible to the caller.
- DAG spec resources return a YAML document representing the current stored DAG
  specification.
- Run collection resources return a paginated or bounded list of DAG-run
  summaries visible to the caller.
- Run detail resources return JSON using Dagu's run-inspection model.
- Run log resources return JSON using Dagu's run-log model.
- This spec does not freeze the exact JSON field set or generated schema for
  collection, run detail, or run log resources.
- Run log resources may accept query parameters supported by the Dagu log
  reader.

## Errors

Server-level failures and resource-read failures return a Dagu MCP error
object, not resource content:

```json
{
  "code": "invalid_resource_uri",
  "message": "Resource URI is malformed.",
  "resourceUri": "dagu://runs/example/run-id/logs",
  "operation": "resources/read",
  "details": {
    "field": "uri"
  }
}
```

`resourceUri`, `operation`, and `details` are omitted when they do not apply.

Clients must branch on `code`, not parse `message`.

Required error codes for this spec:

`unauthenticated`, `unauthorized`, `invalid_api_key_surface`,
`invalid_resource_uri`, `unsupported_resource`, `resource_not_found`,
`resource_unavailable`, `internal_error`.

Common conditions map to codes as follows:

| Condition | Code |
| --- | --- |
| Authentication is required and the request is not authenticated. | `unauthenticated` |
| The accepted credential is not authorized for the requested operation. | `unauthorized` |
| A valid API key is not accepted for the `mcp` surface. | `invalid_api_key_surface` |
| The resource URI is malformed, uses the wrong scheme, or has an unsupported path shape. | `invalid_resource_uri` |
| The resource URI host is not a supported resource family. | `unsupported_resource` |
| The named resource does not exist. | `resource_not_found` |
| The resource exists but cannot currently be read. | `resource_unavailable` |
| The server fails unexpectedly while handling the request. | `internal_error` |

## Examples

Default endpoint:

```text
http://localhost:8080/mcp
```

Endpoint under a server base path:

```text
https://dagu.example.com/dagu/mcp
```

Built-in reference collection:

```text
dagu://reference
```

Built-in reference topic:

```text
dagu://reference/authoring
```

DAG collection with list filters:

```text
dagu://dags?name=nightly&perPage=20
```

DAG spec resource:

```text
dagu://dags/nightly-report/spec
```

Run collection with list filters:

```text
dagu://runs?name=nightly-report&limit=20
```

Run detail resource:

```text
dagu://runs/nightly-report/20260701T010000Z
```

Run log resource with a log-reader query:

```text
dagu://runs/nightly-report/20260701T010000Z/logs?tail=100
```

An API key with only this accepted surface can be used at the MCP endpoint:

```json
["mcp"]
```

An API key with only this accepted surface is rejected by the MCP endpoint:

```json
["rest_api"]
```
