# Spec 095: MCP Servers

## Status

Not implemented.

This spec describes planned behavior. No version of Dagu accepts a static
credential for an `mcp` connection yet.

**Open items.** These points are not decided:

- Whether the inline form of `tools.mcp` of
  [Spec 090](090-agent-tools.md) folds into `mcp_servers`, so that a step
  names every MCP server with one key.
- The route by which the MCP gateway of a step on an agent worker reaches
  a server: through the egress proxy with the proxy credential, as
  [On an Agent Worker](#on-an-agent-worker) states, or through the
  coordinator.
- An adapter for the `opencode` CLI outside ACP.

This spec extends the provider `mcp` of
[Spec 107: Connections](107-connections.md). Steps use an MCP server
through `mcp_servers` and the MCP gateway of
[RFC 026](../rfcs/026-mcp-tools-for-ai-steps.md).
[Spec 093](093-model-connections.md) gives the same steps their model.

## Scope

This spec defines:

- the grant `static` of an `mcp` connection: a bearer token or headers
- the one key that names an MCP server with a credential in a step
- which CLIs can use MCP servers, and the error for one that cannot
- the UI that adds an MCP server with a static credential

This spec does not define:

- the MCP gateway, the tool allow-list, approvals, confirmations, and
  tool catalogs. See RFC 026 and Spec 107.
- the connect flow of an `mcp` connection with OAuth. See Spec 107.
- the inline form of `tools.mcp`, for a server that needs no credential.
  See Spec 090.
- the model of a step. See Spec 093.

## Goal

- A workflow author names an MCP server the same way in every AI step and
  for every CLI that supports MCP.
- A manager adds a server that takes a static token once. No DAG, file, or
  agent holds the token.
- The agent sees only the tools that the step allows.

## Static Credentials

Spec 107 gives the provider `mcp` the grant `authorization_code`. This
spec adds the grant `static`, for a server that takes a fixed token or
fixed headers.

| Field | Values and rules |
| --- | --- |
| `resource` | The server's URL, as in Spec 107. `https`, or `http` to a loopback address that an admin entered, as Spec 107 states. A query is valid. No user information and no fragment |
| `token` | Optional. Sent as `authorization: Bearer <token>` |
| `headers` | Optional. A map from a header name to a write-only value. At most 16 entries |

- A static connection has no `app`. Spec 107 lists the grant `static`.
- A connection has `token`, `headers`, or neither. It does not have both
  `token` and a header `authorization`.
- A header name matches `^[A-Za-z][A-Za-z0-9-]{0,63}$`, and Dagu keeps it
  in lower case. Two names that differ by case are an error. A name is not
  `host`, `content-length`, `content-type`, `accept`, `accept-encoding`,
  `content-encoding`, `mcp-session-id`, `mcp-protocol-version`, or a
  hop-by-hop header.
- A value is not empty, has no control character, and has at most 8192
  bytes.
- The values are write-only. Pages show each header name, and a prefix and
  a fingerprint for each value, by the rule of Spec 093
  [Credentials](093-model-connections.md#credentials).
- The values are stored as Spec 107 token state: encrypted, and never in
  the secret store. `allow_token_export` is always off.
- A rotation replaces the whole set, since no route returns a value. A
  header that the new set lacks is deleted.
- The bound host is the host of `resource`. The gateway sends the values
  only there, and follows no redirect.

### Connection Status

| From | To | When |
| --- | --- | --- |
| none | `connected` | The connection is created, and its first `tools/list` succeeds |
| none | `pending` | The connection is created, and its first `tools/list` fails. `status_reason` holds the error |
| `pending` | `connected` | A rotation, or a Test, lists the tools |
| `connected` | `needs_reconnect` | The server answers 401 to a request that carried the stored values |
| `needs_reconnect` | `connected` | A rotation lists the tools |

- A 401 causes no refresh and no retry.
- The change to `needs_reconnect` records the event
  `connection.needs_attention` of Spec 107, once. The UI calls this
  status "Credential rejected".
- A step that uses a connection that is not `connected` fails with
  `reconnect_required`, as Spec 107 states.

## Steps

### One Key

`mcp_servers` of RFC 026 is the one key that names an MCP server with a
credential, in `chat.completion`, `agent.run`, `harness.run`, and agent
DAGs. A static connection is named as an OAuth one is:

```yaml
mcp_servers:
  - docs
  - connection: tracker
    tools: [search_issues, get_issue, add_comment]
    unattended: [add_comment]
```

- The gateway of RFC 026 offers the agent only the tools in `tools`, or
  every listed tool when `tools` is left out. It refuses a call of another
  tool. The agent never sees a tool that `tools` leaves out.
- A server of a static connection is not a preset, so RFC 026 counts each
  of its tools as a write tool. A write tool waits for a person unless
  `unattended` names it. A `harness.run` step does not wait in RFC 026: it
  refuses a write tool that `unattended` does not name, and the editor
  warns about it.
- A step never names an MCP credential in `tools.mcp`. The inline form of
  `tools.mcp`, with `url`, stays for a server that needs no credential, as
  Spec 090 states. A header value of an inline entry is visible to the
  agent.

### CLIs

| CLI | MCP servers |
| --- | --- |
| `claude` | A temporary `--mcp-config` file, as RFC 026 states |
| `codex` | `-c mcp_servers` overrides, with `bearer_token_env_var`, as RFC 026 states |
| An ACP agent, such as `opencode acp` | HTTP MCP entries in `session/new`, as RFC 026 states |
| `opencode`, outside ACP | No adapter yet. See [Status](#status) |
| `pi` | None. pi has no MCP support by design |
| Another provider | None until it gains an adapter, as RFC 026 states |

`dagu validate` rejects `mcp_servers` on a step whose CLI has no MCP
adapter:

```text
step "triage": harness provider "pi" cannot use mcp_servers: pi has no MCP support; remove mcp_servers, or use claude, codex, or an ACP agent
```

### On an Agent Worker

- The gateway runs in the Dagu worker process, which Spec 092 trusts, and
  serves the agent on `127.0.0.1` only. The agent holds only the gateway
  token of its step.
- The gateway takes the values from the coordinator, as RFC 026 takes a
  token on a worker. It reaches the server through the egress proxy, with
  the proxy credential, so the proxy MUST allow the server's host. The
  agent cannot use the proxy credential.

## User Interface

### Connections Page

- Add offers MCP server. After the URL, the credential choice is Sign in
  (Spec 107), Token, Headers, or None. A token is one password field.
  Headers are rows of a name and a password field, at most 16.
- An `http` URL shows a warning.
- The password fields are not controlled. Dagu clears them after each
  request, and keeps no value in a state, a URL, a storage, or an error
  text.
- A create lists the tools at once, and the row shows their count or the
  error.
- A row shows the name, the host of the URL, the credential (each header
  name, prefix, and fingerprint), the status, the tool count, and the DAGs
  that name the connection. The list never shows a full URL, since a
  query can hold a secret.
- Rotate credentials asks for every value again, with the names of the
  current headers.

### Step Forms

- The Tools section lists the project's MCP connections. A pick adds an
  `mcp_servers` entry.
- The entry shows the catalog's tools with check boxes. Each write tool
  has a toggle "Run without a person", which writes `unattended`.
- On a step whose CLI has no MCP adapter, the section says why, and offers
  no pick.

## Errors

`dagu validate` rejects:

- `mcp_servers` on a step whose CLI has no MCP adapter

The routes refuse, with the status 400:

- `token` with a header `authorization`
- a header name or value that breaks a rule of
  [Static Credentials](#static-credentials)

A step fails at run time with:

- `reconnect_required`: the connection is not `connected`
- the failures of the MCP gateway of RFC 026

## Examples

### A Static MCP Connection

A create request. The values are only in the request.

```json
{
  "name": "docs",
  "provider": "mcp",
  "grant": "static",
  "resource": "https://docs.example.com/mcp",
  "headers": { "x-api-key": "<the value>" }
}
```

A read of it:

```json
{
  "name": "docs",
  "provider": "mcp",
  "grant": "static",
  "host": "docs.example.com",
  "headers": [
    { "name": "x-api-key", "keyPrefix": "sk-doc", "keyFingerprint": "a1b2c3d4e5f6", "keyUpdatedAt": "2026-11-04T10:00:00Z" }
  ],
  "status": "connected",
  "tools": 12
}
```

### A Step

The tools of a static server count as write tools, so a harness step lists
the ones it calls in `unattended`:

```yaml
steps:
  - id: answer
    action: harness.run
    with:
      provider: claude
      prompt: Answer the question from the docs.
      mcp_servers:
        - connection: docs
          tools: [search, read_page]
          unattended: [search, read_page]
```

## Conformance

No test exists yet. Each change that implements a part of this spec adds
its tests here and updates [Status](#status).
