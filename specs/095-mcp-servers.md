# Spec 095: MCP Servers

## Status

Implemented.

This spec is the companion of [Spec 059: Chat Completion](059-chat-completion.md), [Spec 090: Agent
Tools](090-agent-tools.md), [Spec 091: Agent Node](091-agent-node.md), [Spec 092: Agent
Workers](092-agent-workers.md), [Spec 093: AI Endpoints](093-ai-endpoints.md), [Spec 094: AI
Gateway](094-ai-gateway.md) and [Spec 096: Skills](096-skills.md). It also uses [Spec 069: Secrets
Providers](069-secrets-providers.md).

Spec 093 defines the rules of the endpoints, which the entries of this spec follow for their scope,
their name and their secrets. Spec 094 defines the gateway that carries the MCP traffic.

## Scope

This spec defines:

- the MCP server entry: a named entry of the AI page that holds a URL and credentials
- the tab MCP of the AI page, its routes and its audit actions
- the named form of the key `tools.mcp`
- the MCP relay
- the effects of the key `allow` for a named entry

This spec does not define:

- the AI page, the endpoints, the key rules, the scope rules and the block `ai`. See [Spec
  093](093-ai-endpoints.md).
- the gateway: the stream to the coordinator, the identity checks, the limits, the failure kinds and
  the audit line. See [Spec 094](094-ai-gateway.md).
- the key `allow`, the key `dags` and the tool map. See Spec 090.
- the allow rules of the CLI and the tool bridge. See Spec 091.
- the agent worker, the pools, the restricted user and the egress proxy. See Spec 092.
- the secret store. See [Spec 069](069-secrets-providers.md).

## Goal

A workflow author names a MCP server in one key of a step. The author never writes the credentials of
the server in a DAG, in a file of the worker or in the environment of an agent. An administrator adds
the credentials one time, on the AI page, and chooses which DAGs may use the server.

The credentials stay in the coordinator. The MCP config of the attempt has the address and the secret
of the relay only.

## Terms

A MCP server entry is a named entry of the MCP tab. It holds a URL and credentials.

A named entry is an entry of `tools.mcp` with no `url`. It names a MCP server entry.

An endpoint, a scope and the rules of a key are as in Spec 093. The relay, the relay secret, the
gateway and a target are as in Spec 094.

## Threat model

Spec 094 gives the assets, the trust levels and the threats of the gateway. See [Threat
model](094-ai-gateway.md#threat-model). This table gives the threats of the MCP servers with
credentials.

| Threat | Control | What remains |
| --- | --- | --- |
| The agent reads the credentials of a MCP server. | The credentials stay in the coordinator. The MCP config has the address and the secret of the relay only. | None. |
| The agent calls a tool that `allow` does not list. | The coordinator parses each `tools/call` of a named server. It takes `allow` from the DAG that it holds. | The agent can call each tool in `allow`. |
| The agent calls a MCP server that its step does not name. | The relay serves the names of the step only. The coordinator checks the name against the DAG. | None. |

## MCP servers with credentials

### The MCP tab

A MCP server entry has these fields:

| Field | Type | Rule |
| --- | --- | --- |
| `name` | string | Required. Matches `^[a-z][a-z0-9-]{0,63}$`. Not `dagu`. Unique in its scope. |
| `url` | string | Required. The scheme is `https`, or `http` with a warning. No user information and no fragment. A query is valid. |
| `token` | secret | Optional. The coordinator sends `authorization: Bearer <token>`. |
| `headers` | map | Optional. A header name and a write-only value. At most 16 headers. |
| `scope`, `allow.workspaces`, `allow.dags` | As for an endpoint | The same rules. |

An entry has a `token`, or `headers`, or none. It does not have a `token` and a header
`authorization`. A header name matches `^[A-Za-z][A-Za-z0-9-]{0,63}$`. The server keeps it in lower
case. A header name is not `host`, `content-length`, `content-type`, `accept`, `mcp-session-id`,
`mcp-protocol-version` or a hop-by-hop header. Two names that differ by case only are an error. A value
is not empty, has no control character and has at most 8192 bytes.

The credentials follow the display rule of [The key](093-ai-endpoints.md#the-key). The page shows the header names, and a
prefix and a fingerprint for each value. Each value is a managed secret with the ref
`ai/mcp/<name>/<header>`. The token has the ref `ai/mcp/<name>/authorization`, because an entry has no
token and header `authorization` together. Only an administrator can change or delete a secret with a
ref that starts with `ai/mcp/`. The prefix `ai/` is reserved, as [Storage](093-ai-endpoints.md#storage) says.

The routes and the audit actions follow the pattern of the endpoint. All the routes need the role admin,
except the route of choices.

| Route | Effect |
| --- | --- |
| `GET /ai/mcp-servers` | List the entries with their fields, and the prefix and the fingerprint of each value. The field `token` is an object with `keyPrefix`, `keyFingerprint` and `keyUpdatedAt`. The field `headers` is a list of objects with `name` and the same three fields. |
| `POST /ai/mcp-servers` | Create an entry with its credentials. The scope is a field of the body. |
| `PATCH /ai/mcp-servers/{name}` | Change the fields `url` and `allow`. A change of `allow` replaces the whole block. |
| `PUT /ai/mcp-servers/{name}/credentials` | Replace the whole set of credentials: a `token`, or `headers`, or none. |
| `DELETE /ai/mcp-servers/{name}` | Delete the entry and its credentials. |
| `GET /ai/mcp-server-choices?dag=<name>&workspace=<name>` | List the entries that the DAG may use. Roles that edit DAGs in the workspace can call it. |

The routes that name an entry read the scope from the query `workspace`, as for an endpoint. A rotation
replaces the whole set, because no route returns a value. A header that the new set does not have is
deleted with its secret. A request with no `token` and no `headers` removes the credentials. The route
of choices gives the name and the scope, and no URL and no credential data.

Each create, change, rotation and delete writes one line to the audit log, with the category `secret`.
The actions are `ai_mcp_server_create`, `ai_mcp_server_update`, `ai_mcp_server_key_rotate` and
`ai_mcp_server_delete`. The line has the user, the name and the scope. For a create and a rotation it
has the fingerprint of each value, in the fields `token_fingerprint` and `header_fingerprints`. It never
has a value or a prefix. A call that fails writes no line.

An entry in use can be deleted. The next relay request of a run that names it fails with
`mcp_server_unknown`.

### The key tools.mcp

A step names a MCP server entry by its name. The entry has no `url`:

```yaml
tools:
  mcp:
    - name: docs
      allow: [search]
```

The inline form with `url` stays valid for a server that needs no credential. An entry has `url`, or it
has no `url` and names a server entry. The name of a named entry matches `^[a-z][a-z0-9-]{0,63}$`, the
rule of a MCP server entry. An entry with `headers` and no `url` is a validation error. The key `allow`
stays required. Only a `chat.completion` step and an `agent.run` step can have a named
entry. The `llm` block of a DAG cannot have one.

A header value in an inline entry is visible to the agent, in the MCP config of the attempt. Use a
named entry for a credential.

Spec 090, Spec 091 and Spec 092 use both forms:

| Form | Path | Credentials |
| --- | --- | --- |
| Inline, with `url` | The CLI connects to the server. | None, or visible to the agent. |
| Named, with no `url` | The CLI connects to the relay. The coordinator connects to the server. | In the coordinator only. |

### The MCP relay

For a named entry, Dagu writes a server in the MCP config of the attempt. Its URL is
`http://127.0.0.1:<port>/mcp/<name>`, with the header `authorization: Bearer <relay secret>`. The
same relay serves the endpoints and the MCP servers of the step. It uses the same stream and the same
identity checks. The target has the kind `mcp`.

The relay allows the MCP streamable HTTP transport only:

| Method | Path | Use |
| --- | --- | --- |
| `POST` | `/mcp/<name>` | A JSON-RPC message. The response is JSON or SSE. |
| `GET` | `/mcp/<name>` | An SSE stream of messages from the server. |
| `DELETE` | `/mcp/<name>` | The end of a session. |

The query is not allowed. The coordinator sends each request to the URL of the entry. A path of the
client after `/mcp/<name>` is refused. The coordinator sends these request headers, and no other:
`content-type`, `accept`, `mcp-session-id`, `mcp-protocol-version`, `last-event-id` and `user-agent`.
It adds the credentials of the entry. It returns the response headers as for an endpoint.

The rules of the stream, the limits, the timeouts, the redirect rule and the audit line are the rules of
the model traffic. The route name in the audit line is `mcp`, and the line adds the fields `rpc_method`
and `tool`. They hold the JSON-RPC methods and the tool names of a `POST` body, joined with a comma. The
field `key_fingerprint` holds the fingerprint of the token, or the pairs `<header>:<fingerprint>`.

A stream of the method `GET` has no data while the server has nothing to say. The idle timeout
`ai.relay.idle_timeout_sec` ends such a stream. The MCP client then opens a new stream.

The relay of a step serves a path `/mcp/<name>` for the names of the step only. A request for another
name gets the reason `mcp_server_forbidden` from the relay, with no call to the coordinator. A relay of
a step that names no endpoint serves no model request. It answers `path_not_allowed`.

An `agent.run` step has one relay for its endpoint and its MCP servers. A chat step has one relay for
its MCP servers, which lives until the step ends, and one relay for each attempt of a model. An error
of the relay on a MCP path is a JSON-RPC error with the HTTP status of the reason.

The old HTTP+SSE transport is not supported.

### The key allow

For a named entry, the key `allow` of Spec 090 has three effects:

1. The CLI gets the allow rules `mcp__<name>__<tool>`, as in Spec 091.
2. The coordinator parses each `POST` body. A `tools/call` with a tool name that is not in `allow`
   gets a JSON-RPC error and no call to the server. A JSON-RPC batch is parsed item by item. A body
   that is not valid JSON-RPC is refused.
3. For `chat.completion`, the check of Spec 090 stays: each name in `allow` must be in the list of
   the server. Dagu reads the list through the relay when the step starts.

The coordinator does not change `tools/list`. The CLI hides the tools that it may not call.

The coordinator takes `allow` from the DAG that it holds, and never from the request. A `tools/call`
with no tool name is not valid. The error of a refused tool has the code -32001 and the id of the
request. A refused item of a batch refuses the whole batch: no item runs, and each request of the
batch gets an error with its id. A refused call with no id has the status 202 and no body. A response
of the client to a request of the server is valid.

The coordinator and the server must read the same message. The coordinator reads the keys with their
exact case, and refuses a body with two equal keys at any depth. It also refuses a key of a message, or
of its `params`, that differs only by case from one of these keys: `jsonrpc`, `id`, `method`,
`params`, `result`, `error`, `name` and `arguments`. The reason is `mcp_request_invalid`. It refuses a body that is not UTF-8
or has more than 64 levels. It refuses a `method` with a character other than ASCII letters,
digits and `/ _ - .`. It also refuses a `method` that is `tools/call` in other case. It sends to the
server the bytes that it read, and never a new encoding of them.

For an inline entry, only the effects 1 and 3 exist. The coordinator is not on the path.

## The UI contract

### The tab MCP

The tab MCP is one of the three tabs of the AI page. See [The AI page](093-ai-endpoints.md#the-ai-page).
It has the same shape as the tab Endpoints:

- The list has one row for each entry. The columns are Name, Server, Scope, Credentials, Updated and
  Allowed. The column Server shows the host of the URL. The column Credentials shows the prefix and the
  fingerprint of each credential. The page never shows a full URL, a value or a "show" button.
- The dialog has the name, the URL, the credentials, the scope and the allow lists. The field
  Credentials is a choice: None, Bearer token or Headers. A token has one password field. The form of
  headers has one row for each header, with a name and a password field, and at most 16 rows. A URL with
  the scheme `http` shows a warning.
- The password fields are not controlled. Dagu clears them after each request, and keeps no value in a
  state, a URL, a storage or an error text. A dialog that changes an entry has the URL and the allow
  lists only.
- The action "Rotate credentials" opens a dialog with the form of the credentials that the entry has,
  with the names of its headers. The dialog asks for every value again, because the new set replaces the
  old set.
- A delete asks for a confirmation that names the entry.

### The step forms

The step forms of Spec 093 have these rules for the named MCP entries. See [The step
forms](093-ai-endpoints.md#the-step-forms).

- The Tools section of the form lists the named MCP servers next to the inline entries. A named entry
  has no URL field.
- The picker of a tool, kind MCP server, lists the entries that the DAG may use. It reads them from the
  route of choices. It also offers "New MCP server with a URL". With no entry to offer, the pick adds the
  inline form at once. The picker adds a named entry with no `url` and an empty `allow`.
- The panel of a named entry has a select of the entries that the DAG may use. It warns about a name
  that the list does not have. It has no Connection section. The canvas shows the name of the entry.

## Errors

`dagu validate` rejects these cases with a nonzero exit code:

- a named MCP entry in the `llm` block of a DAG. The same applies to the `llm` block of a step of
  another action. The error is `an mcp entry with no url names a MCP server entry of the AI page; only
  the actions chat.completion and agent.run can use it, and the llm block of a DAG or of another
  action cannot`.
- an `mcp` entry with no `url` and with `headers`

These cases fail a step at run time, with the reasons of [Failure kinds](094-ai-gateway.md#failure-kinds):

- a MCP server entry that does not exist, or that the DAG may not use: `mcp_server_unknown` and
  `mcp_server_forbidden`
- a `tools/call` of a tool that `allow` does not list: `mcp_tool_not_allowed`
- a MCP request body that the coordinator cannot read as the server reads it: `mcp_request_invalid`

## Examples

### A named MCP entry

The step names the MCP server entry `docs` and allows the tool `search`. The DAG that holds the step
must be in `allow.dags` of the entry `docs`.

```yaml
steps:
  - id: code
    action: agent.run
    with:
      endpoint:
        name: cle1
        model: claude-sonnet-4-6
      provider: claude
      prompt: "Fix the failing test."
      tools:
        builtin: { read: true, write: true, edit: true, search: true }
        mcp:
          - name: docs
            allow: [search]
```

The agent step of [Spec 094](094-ai-gateway.md#an-agent-step-with-an-endpoint) uses the same entry.

## Conformance

The tests run the Dagu binary on static fixtures in `conformance/spec095_mcp_servers/`. They use
three fakes. The fake upstream is a local HTTP server that records each request and serves SSE. The
fake `claude` is an executable that calls `ANTHROPIC_BASE_URL` with `ANTHROPIC_API_KEY`, and the MCP
servers of the MCP config of its attempt. The fake MCP server records the method, the path, the
headers and the body of each request, and streams events on a `GET`. No test needs an external
service. The fixtures name the endpoint `test-endpoint` and the model `claude-test`, except where the
name of a model is the subject of the test.

The tests of a worker run a coordinator with mutual TLS and the pools of Spec 092. A test of an agent
worker changes to the restricted user. It needs Linux, the user ID 0 and the flag
no-new-privileges. It skips on another host. The table marks these tests "root". A test of a
general worker runs with each user ID.

Each test that has a key checks that the key is in no response, no output, no log and no file of the
worker.

| Section | Tests |
| --- | --- |
| The MCP tab | `TestMCPCredentialsAreNeverShown`, `TestMCPScopeAndInvalidRequests`, `TestMCPRoutesNeedAuthentication`. |
| The key tools.mcp | `TestValidateTheNamedFormOfTheMCPEntry`. |
| The MCP relay, the key allow | `TestMCPRelayOnAnAgentWorker` (root), `TestMCPRelayLocalMode`, `TestChatNamedMCPLocalMode`, `TestChatNamedMCPOnAWorker`. Go tests of the coordinator check the limits of the relay on a MCP request. They also check the filter of the request headers and the use rule of the entries. |
| The UI contract | Not covered here. The tests of the UI cover it. |

## Open items

Each item has a default. The default applies until the owner decides.

### Measured with Claude Code

The author ran the real Claude Code 2.1.311 on 2026-11-04. The model was `claude-haiku-4-5`. The
endpoint held an OAuth token of a subscription, with the form `anthropic-oauth`. The agent worker
held no model key, and the proxy had no model route. The demo `examples/agent-worker-demo` holds the
scripts and the evidence format. These facts are measured.

| Subject | Fact |
| --- | --- |
| Named MCP server | The CLI connected to the relay path `/mcp/<name>`. It sent the methods `server/discover`, `initialize`, `notifications/initialized` and `tools/list`, then a `GET` for a stream. The server got its token on each request. The token was not in the worker. |
| MCP tools | The CLI lists each tool of the server, also a tool that `allow` does not name. The coordinator refuses a call of such a tool. The tests with the fake CLI show this. |

### Decisions to confirm

| Item | Default |
| --- | --- |
| A MCP server entry has `allow.workspaces` and `allow.dags`, like an endpoint. The owner named a name, a URL and credentials. | As in this spec. |
| A role other than admin needs a license. The suite cannot test the refusal of a role at the binary level. | A Go test of the handlers covers the roles. The suite checks the refusal with no token. |
| The audit log needs a license. The suite cannot read the audit lines at the binary level. | A Go test of the handlers covers the lines. |
| A warning for a header value in an inline `mcp` entry of an agent step. | No warning. The spec tells the author to use a named entry. |
