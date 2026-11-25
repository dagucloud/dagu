# RFC 025: Connections

- **Status:** Proposed
- **Date:** 2026-11-25
- **Affected areas:** a new `connection` package (records, providers, connect
  flow, token broker), `cmn/mailer/oauth` (refresh code moves out), `spec`
  and `ir` (`connection` fields, `mcp_servers`, declared connections),
  `runtime` (masking, agent loop), `runtime/builtin` (`mcp`, `http`, `mail`,
  `harness`, `chat`), `secret/providers` (a `connection` provider),
  `service/coordinator` and `service/worker` (token RPC), `service/frontend`
  (API, OAuth callback), `service/mcp`, `cmd`, `api/v1`, `proto`, `ui`,
  `specs`, `conformance`, and `docs`

## Summary

Let a user connect an account once and use it everywhere. A connection is a
named, server-managed OAuth sign-in: a Google Workspace account, a Microsoft
365 account, any OAuth2 API, or any remote MCP server. The user creates one
from the UI, the CLI, or the REST API, and consents in a browser. Steps,
harness agents, agent DAGs, and chat steps then refer to it by name. Dagu
stores the tokens encrypted, refreshes them when they expire, and sends them
only to the hosts the connection is bound to. YAML never holds a secret.

Google's Workspace MCP servers made this urgent. They became generally
available on 2026-11-24. From 2027-02-01, Google requires AI agents to reach
Workspace data through them, with an OAuth client marked "Use this client for
an AI-powered agent". Dagu can call these servers today only with a static
bearer token, which expires after an hour.

```text
 dagu connection add google            Connections page: Connect Google
                 \                         /
                  v                       v
      connect engine: state + PKCE; server callback | loopback | paste | device code
                                  |
          +-----------------------+------------------------+
          v                       v                        v
     oauth-apps/            connections/<name>        token state
     client ID,             account, services,        refresh and access token,
     client secret,         workspaces, bound         expiry, status, generation
     agent flag             hosts                     (written by the broker only)
                                  |
     +--------------+-------------+-------------+---------------------------+
     v              v                           v                           v
  mcp.call     http.request               mail_accounts           MCP gateway, per step
  server: gmail  bound hosts only          connection:            harness, agent DAG, chat
     |              |                           |                           |
     +--------------+------ Token(conn) --------+---------------------------+
                     local: lock and refresh; worker: coordinator RPC
```

## Problem statement

- **Tokens expire, and only mail refreshes them.** `mcp.call` and
  `http.request` take static `headers`
  (`internal/runtime/builtin/mcp/config.go:25`,
  `internal/runtime/builtin/http/http.go:44`). A Google access token lasts
  one hour.
- **Mail refresh is weak.** `internal/cmn/mailer/oauth/oauth.go` refreshes
  once per run because each run is a separate process. It keeps a rotated
  refresh token "for the life of the process only" (`oauth.go:73`), so a
  Microsoft account that rotates tokens can break. It hardcodes the Gmail
  scope for service accounts (`oauth.go:29`).
- **Getting a refresh token is manual.** Users copy a client ID, client
  secret, and refresh token from the OAuth Playground into YAML or base
  config. Spec 073 leaves the consent screen out of scope, and nothing in the
  CLI, UI, or API replaces it.
- **AI steps cannot use MCP tools with arguments.** Agent DAGs offer steps as
  tools, and "only a step that launches a child DAG accepts arguments"
  (`specs/032-agent-dag.md:153`). Chat steps accept only child DAGs as tools.
  The ACP harness sends an empty MCP server list
  (`internal/runtime/builtin/harness/acp.go:243`).
- **Google's new rules need specific setup.** The OAuth client must carry
  the AI-powered agent flag, or the MCP servers reject it. Google recommends
  a separate client for agent and non-agent use. An app left in Testing
  status issues refresh tokens that expire after seven days. Users find each
  of these by failing.

## Goals

1. On a fresh server, a user connects a Google account from the UI in under
   five minutes. The UI guides the Google Cloud setup and checks it.
2. The CLI connects an account in one command, including over SSH with no
   local browser.
3. One connection serves `mcp.call`, `http.request`, mail, harness, agent
   DAG, and chat steps, by name.
4. No secret appears in YAML, logs, run status, API responses, MCP results,
   or audit entries.
5. Each connection refreshes at most once an hour, however many runs use it,
   and a rotated refresh token is saved.
6. An expired or revoked sign-in is reported once, names the connection, and
   offers Reconnect.
7. Connections work in a single process, behind several frontend replicas,
   and for runs on workers that share no filesystem with the server.
8. AI steps meet Google's MCP requirement without extra configuration.
9. DAGs that use no connection behave as before.

## Non-goals

- **A Dagu-hosted OAuth app.** The user brings their own client in this RFC.
  The records reserve a `hosted` app kind for a later app relayed through
  Dagu Cloud.
- **Personal connections and run-as-user identity.** Connections are
  server-wide. A user's own account, used by runs they start, needs its own
  design.
- **MCP elicitation and sampling.** Dagu does not advertise either
  capability in this RFC.
- **Inbound OAuth for Dagu's own MCP server.**
- **MCP servers for harness steps in a container or on a remote ACP agent.**
  Such a step with `mcp_servers` is refused. [Future
  enhancements](#future-enhancements) describes a socket bridge.
- **Creating Google Cloud projects or OAuth clients.** The wizard links to
  the Google Cloud console and checks the result.
- **A second secret store.** Static API keys stay in the secret store.
  Connections hold OAuth credentials only.

## Guarantees

Promised:

- A refresh token or client secret is stored only encrypted. It is never
  returned by the API, CLI, or MCP server, and it never reaches a run or a
  worker.
- An access token is sent only to the hosts the connection is bound to. It
  does not follow a redirect to another host.
- A run receives a token only for a connection that its DAG declares, and
  only when the connection's allow-list includes the run's workspace.
- A refresh result never overwrites a newer reconnect.
- A connection becomes `needs_reconnect` once per failure, and only a
  successful reconnect clears it.
- Each connection refreshes at most once per token lifetime across all
  processes on a host, and across workers in distributed mode.

Not promised:

- **Isolation from the run process in local mode.** The run process can
  read the encryption key, so a workspace allow-list is a policy there, not
  an operating system boundary. In distributed mode the worker never holds
  the key.
- **Secrecy of an exported token.** A token exported to an environment
  variable can be printed by the script that receives it. Masking hides the
  exact value in Dagu's logs, nothing more. Export is off unless an admin
  enables it for the connection.
- **Google quotas and billing.** Dagu reports the provider's errors as they
  come.

## Design

### Records

Two kinds of server-wide records, each in its own collection under the data
directory, created with mode `0o700` as the license store's is
(`internal/persis/file/shared_stores.go:102`). Secret fields are encrypted
with `internal/cmn/crypto`, using the key from `DAGU_ENCRYPTION_KEY` or
`<data_dir>/auth/encryption_key`.

**OAuth app** (`<data_dir>/oauth-apps/`): a client registration that several
connections share.

| Field | Notes |
| --- | --- |
| `id`, `name` | Name shown in pickers |
| `provider` | `google`, `microsoft`, `oauth2`, or `mcp` |
| `kind` | `byo` (user's own client), `dcr` (registered by Dagu through RFC 7591), or `hosted` (reserved) |
| `client_id` | |
| `client_secret` | Encrypted. Optional for public clients |
| `client_type` | `web` or `desktop`. Decides which redirect modes work |
| `agent` | The user attests that the client has "Use this client for an AI-powered agent" set |
| `endpoints` | Only for `oauth2` and `mcp`: authorize, token, revoke, and device URLs |

**Connection** (`<data_dir>/connections/`): one authorized account, stored as
two records with different writers.

The metadata record is written by managers and admins:

| Field | Notes |
| --- | --- |
| `name` | Lowercase letters, digits, and hyphens, up to 63 characters. Unique on the server |
| `provider`, `app` | The app is absent for a service account |
| `grant` | `authorization_code`, `device_code`, `client_credentials`, or `service_account` |
| `services` | Presets such as `gmail` or `calendar`, each with `read` or `write` |
| `scopes` | Requested scopes, from the services plus any extras |
| `account` | Identity read after consent, such as an email address or subject |
| `subject` | Service accounts only: the impersonated user, fixed by the admin |
| `bound_hosts` | Hosts that may receive the token. Presets fill this in |
| `resource` | MCP connections: the canonical server URL (RFC 8707) |
| `workspaces` | The allow-list. `*` means every workspace |
| `allow_token_export` | Admin opt-in for the `connection` secret provider. Off by default |

The token state record is written only by the token broker:

| Field | Notes |
| --- | --- |
| `refresh_token`, `access_token` | Encrypted |
| `expires_at`, `refresh_token_expires_at` | The second only when the provider reports it |
| `granted_scopes` | What the provider granted. It can be fewer than requested |
| `status` | `pending`, `connected`, `needs_reconnect`, `app_invalid`, or `revoked` |
| `status_reason` | The provider's error code and text, without secrets |
| `generation` | Incremented by every connect and reconnect |
| `refreshed_at` | |

Metadata edits and token refreshes touch different records, so they never
conflict, and `status` has one writer. Neither record is stored in the
secret store; [Alternatives considered](#alternatives-considered) gives the
reasons.

### Providers and services

| Provider | Grants | Notes |
| --- | --- | --- |
| `google` | Authorization code, or a service account with domain-wide delegation | Google rejects MCP calls from a user-delegated client without the agent flag. Service accounts are accepted without it |
| `microsoft` | Authorization code, device code, client credentials | `tenant` is `common`, `organizations`, or a tenant ID. Refresh tokens rotate |
| `oauth2` | Authorization code, client credentials | Endpoints, scopes, and `bound_hosts` are required. PKCE is on unless turned off |
| `mcp` | Authorization code | Discovery and registration follow the MCP authorization spec. See [Any MCP server](#any-mcp-server) |

A service preset names one endpoint of a provider. It gives the MCP URL,
the REST hosts, the scope sets for read and write, and, for Google, the API
IDs to enable. The catalog is compiled into the binary and served by `GET
/connection-providers`.

| Google service | MCP endpoint | APIs to enable |
| --- | --- | --- |
| `gmail` | `https://gmailmcp.googleapis.com/mcp/v1` | `gmail.googleapis.com`, `gmailmcp.googleapis.com` |
| `calendar` | `https://calendarmcp.googleapis.com/mcp/v1` | `calendar-json.googleapis.com`, `calendarmcp.googleapis.com` |
| `drive` | `https://drivemcp.googleapis.com/mcp/v1` | `drive.googleapis.com`, `drivemcp.googleapis.com` |
| `docs` | `https://docsmcp.googleapis.com/mcp/v1` | `docs.googleapis.com`, `docsmcp.googleapis.com` |
| `sheets` | `https://sheetsmcp.googleapis.com/mcp/v1` | `sheets.googleapis.com`, `sheetsmcp.googleapis.com` |
| `slides` | `https://slidesmcp.googleapis.com/mcp/v1` | `slides.googleapis.com`, `slidesmcp.googleapis.com` |
| `chat` | `https://chatmcp.googleapis.com/mcp/v1` | `chat.googleapis.com`, `chatmcp.googleapis.com` |
| `people` | `https://people.googleapis.com/mcp/v1` | `people.googleapis.com` |
| `search` | `https://workspacemcp.googleapis.com/mcp/v1` | `workspacemcp.googleapis.com` |

The scope sets follow Google's MCP setup guide. For example, `gmail` with
`read` asks for `gmail.readonly`, and with `write` it adds `gmail.compose`
and `gmail.modify`. Every Google connection also asks for `openid` and
`email`, so Dagu can show the account. Gmail's MCP server has no send tool,
so sending stays with `mail.send` on a mail account that uses the
connection.

Microsoft presets are `mail`, `calendar`, and `files`, against
`graph.microsoft.com`, plus `offline_access` and `User.Read`.

### Connect flow

The UI, CLI, and API share one engine.

```text
UI or CLI                 Dagu server                         provider
   | POST .../authorization  |                                     |
   |------------------------>| state = encrypt{conn, PKCE verifier,|
   |<-- authorization URL ---|   user, generation, expiry}         |
   |  browser ---------------------------------------------------->| consent
   |                         |<--- GET /oauth/callback?code&state -|
   |                         |  exchange code with the verifier -->|
   |                         |  read identity, probe services ---->|
   | GET .../authorization   |                                     |
   |---- poll -------------->| connected | failed                  |
```

- **Stateless sessions.** The `state` parameter carries the session itself,
  encrypted, so any frontend replica completes a callback and no session
  store is needed. Exchanging a code is single-use: it is a CAS write that
  moves the token state from `pending` to `connected` for the generation in
  the state. A session expires after 10 minutes.
- **Bound to the starting user.** The callback completes only for the user
  who started the session, checked against the encrypted state.
- **PKCE (S256) everywhere.** Google requests also send
  `access_type=offline` and `prompt=consent`, so a refresh token is always
  returned.

Redirect modes:

| Mode | When | Client type |
| --- | --- | --- |
| Server callback | The server has a usable public URL: `server.public_url`, else the tunnel URL. The redirect URI is `<public_url>/oauth/callback`, a public route beside `oidc-callback` (`internal/service/frontend/server.go:1445`) | `web` |
| Loopback | The CLI runs where a browser can open. The CLI listens on `127.0.0.1` on a random port and sends the code to the server | `desktop` |
| Paste | Neither works, as for a server on a private address. The browser lands on a loopback URL that fails to load, and the user pastes that URL into the UI or CLI | `desktop` |
| Device code | Microsoft, over SSH. The CLI shows a code to enter on another device | Public client |

Google web clients accept only HTTPS or `localhost` redirect URIs, and
Google's device flow does not cover Gmail scopes, so a Google connection uses
one of the first three modes. The UI picks the mode from the server's public
URL and the app's client type, and shows the exact redirect URI to register.

**Checks after consent:**

- **Granted scopes.** Google lets the user untick scopes. A missing scope is
  shown on the service that needs it, with Reconnect.
- **Identity.** Dagu reads the account, and asks the user to confirm it in
  the UI or CLI that started the session. This stops a consent completed in
  someone else's browser.
- **Service probes.** One cheap call per service: `tools/list` on each MCP
  service, and a profile read on REST services. A rejected MCP probe says
  that the client may lack the agent flag, and links to the client's page in
  the Google Cloud console.
- **Refresh token expiry.** When the provider reports
  `refresh_token_expires_in`, as Google does for apps in Testing status, Dagu
  shows the date. It explains that publishing the app to Production, or
  making it Internal, removes the seven-day limit.
- **Duplicates.** A second connection for the same account and client
  triggers a warning. Google keeps only 100 refresh tokens per account per
  client and silently revokes the oldest.

**Reconnect** runs the same flow for an existing connection and increments
`generation`. A different account than before needs confirmation.

**Remove** revokes the refresh token where the provider supports revocation,
then deletes both records. A DAG that still names the connection fails at
run start, naming it.

### Token broker

Every token passes through one interface:

```go
// Token returns an access token for conn that stays valid for at least
// minTTL, and how long it stays valid.
Token(ctx context.Context, conn string, minTTL time.Duration) (token string, expiresIn time.Duration, err error)
```

Local path, used by the server, the scheduler, and local runs:

1. Read the token state. If the access token stays valid for longer than
   `minTTL`, return it.
2. Take the connection's lock through `persis.Backend.Lock`
   (`internal/persis/file/backend.go:103`). That lock renews itself while it
   is held, and refuses writes once lost.
3. Read the state again. Another process may have refreshed it.
4. Refresh. The token endpoint call times out before three quarters of the
   lock's stale threshold.
5. Write the new access token, and any rotated refresh token, with a CAS
   write (`createOrSwap`, `internal/persis/store/cas_retry.go:15`). Apply the
   write only if `generation` is unchanged. If a reconnect happened during
   the refresh, drop the result and return the reconnected token.

Each run process also caches tokens in memory, behind the single-flight gate
the mail code uses today (`oauth.go:261-330`). So a run reads the state at
most once per connection per token lifetime, and a `foreach` over many items
costs no more than one step.

Refresh results:

| Result | Effect |
| --- | --- |
| Success | The access token is saved. A rotated refresh token replaces the old one |
| `invalid_grant` | `needs_reconnect`, set once. Later calls fail at once, without calling the provider |
| `invalid_client` or `unauthorized_client` | `app_invalid` on every connection of the app. Fixing the app returns them to `connected` on their next refresh |
| 5xx, network error, or `temporarily_unavailable` | Transient. The status stays. The call fails, and the step's retry policy applies |
| Service account clock or signature error | Reported on the connection. The status stays, since a reconnect would not help |

The refresh code moves from `internal/cmn/mailer/oauth` into the
`connection` package without its mail-specific assumptions. That code is
`refreshTokenGrant`, `TokenError`, the scope rules for Microsoft, and the
gate. `mail_accounts.oauth` and SMTP OAuth keep calling it with the
configured refresh token.

**Distributed runs.** A worker does not read the connection store. It calls
a new coordinator RPC, `ResolveConnectionToken`, modeled on
`ResolveSecretReference`:

- Authorization reuses `authorizedAttempt` and `authorizedDAG`
  (`internal/service/coordinator/secret_reference.go:233`, `:259`).
- The run's DAG snapshot must declare the connection, checked as
  `secretReferenceDeclared` checks secret refs (`:354`).
- The connection's allow-list must include the run's workspace.
- The coordinator calls the local broker and returns only the access token
  and its lifetime in seconds. A relative lifetime makes clock skew on the
  worker irrelevant.

Only coordinators refresh in distributed mode. Workers on a shared NFS
mount could otherwise read a stale refresh token after another host rotated
it, and a provider that detects reuse revokes the whole token family.

**Declared connections.** Building a DAG collects the connections it names
into `ir.DAG.Connections`, as `CredentialRefs` collects SSH refs
(`internal/ir/dag.go:298`). The sources are:

- step `with.connection`;
- `mail_accounts.*.connection`;
- `secrets` entries with `provider: connection`;
- `mcp_servers` on agent DAGs, chat steps, and harness steps.

At run start, an unknown connection, or one whose allow-list excludes the
run's workspace, fails the run before any step starts, as an unresolvable
secret does.

**Masking.** `setupMasker` builds each attempt's masker from the scope's
secrets (`internal/runtime/output.go:78`). A step that uses a connection
mints its token before node setup and adds it to that scope. The masker also
gains provider token patterns (`ya29.` and `1//` for Google), as audit
already masks GitHub and Slack tokens (`internal/audit/mask.go:67`). Steps
that use a token in process, such as `mcp.call` and `http.request`, never
print it. Masking is the second line of defense.

### Using connections in steps

#### `mcp.call`

```yaml
- id: unread
  action: mcp.call
  with:
    connection: work-google
    server: gmail
    tool: search_threads
    arguments: { query: "is:unread newer_than:1d" }
```

- `connection` takes the place of `url` and `headers.Authorization`. With a
  `google` or `microsoft` connection, `server` names the preset. An `mcp`
  connection has its own URL, and `server` is not allowed. `url` is still
  allowed, but must match the connection's bound hosts.
- The client sets go-sdk's `StreamableClientTransport.OAuthHandler`. Its
  token source asks the broker, and its authorize hook handles a rejection:
  - a 401 whose rejected token is the stored one forces one refresh and one
    retry;
  - a 401 with an older token takes the current token without a refresh, so
    many runs cannot start a refresh storm;
  - a 403 with `insufficient_scope` fails as `auth` with detail
    `insufficient_scope` and the missing scope, and does not refresh.
- An error of kind `auth` also carries the connection name and, when the
  connection needs reconnecting, `reconnect_required`.
- Static `headers` stay. They may not set `Authorization` when `connection`
  is set. Today `observingTransport` applies static headers after the SDK,
  so such a header would replace the connection's token.

#### `http.request`

```yaml
- action: http.request
  with:
    connection: work-google
    url: https://www.googleapis.com/calendar/v3/calendars/primary/events
```

- The token is added only when the request host is in the connection's
  bound hosts. A URL with another host fails validation when it is literal,
  and fails at run time when it comes from a reference.
- A redirect to another host is refused. Today resty follows redirects, and
  `oauth2.Transport` would add the token again on every hop.
- `connection` cannot be combined with `debug: true`, because resty's debug
  output prints request headers. It also cannot be combined with
  `headers.Authorization`.

#### Mail accounts

```yaml
mail_accounts:
  me@example.com:
    provider: google
    connection: work-google
```

`connection` replaces `oauth` and `password`, and works in base config too.
The connection must grant a mail scope. The existing `oauth` form keeps
working, unchanged.

#### Tokens for scripts

```yaml
secrets:
  - name: GOOGLE_TOKEN
    provider: connection
    key: work-google
```

A new secret provider puts a fresh access token in an environment variable.
It is for scripts and CLIs that take a bearer token. The token is minted at
run start and stays valid for at least 50 minutes, so a longer run should
use a connection-aware step instead. The provider works only when an admin
has set `allow_token_export` on the connection. Its value is masked like any
secret.

### MCP servers for AI steps

`mcp_servers` gives an AI step tools from connections:

```yaml
mcp_servers:
  - work-google/calendar            # preset of a google connection
  - notion                          # an mcp connection
  - connection: work-google
    server: gmail
    tools: [search_threads, get_thread, create_draft]
    confirm: [create_draft]
```

- `tools` is an allow-list. When it is left out, every tool listed when the
  run starts is offered.
- `confirm` names tools that wait for a person before each call.
- A harness, agent DAG, or chat step that uses a Google connection needs an
  app with `agent` set. Without it the step fails validation. A service
  account connection needs no flag.

#### MCP gateway

Each AI step that has `mcp_servers` gets its own MCP gateway. The gateway is
an MCP server in front of an MCP client, built on the server code in
`internal/service/mcp/`. It is not a byte-level proxy, so sessions,
server-sent events, and protocol versions work as each side expects.

- It listens on `127.0.0.1` on a random port, and accepts only a random
  bearer token created for the step.
- Upstream calls take tokens from the broker. Long agent turns therefore
  outlive any single access token, and the agent never holds a provider
  token.
- When the step starts, it records each allowed tool's name and a hash of
  its description. A tool the upstream adds later is not offered, and a
  tool whose description changes fails its calls until the next run. This
  limits tool-poisoning by a changed upstream.
- Each tool call is written to the step log with its arguments masked, and
  calls from harness agents go to the audit log.
- The gateway closes when the step ends.

#### Harness steps

```yaml
- action: harness.run
  with:
    provider: claude
    prompt: Prepare notes for each of today's meetings.
    mcp_servers: [work-google/calendar, work-google/drive]
```

Each built-in provider has an adapter that points the agent at the gateway:

- **Claude Code:** a temporary `--mcp-config` file with mode `0600`, deleted
  after the step.
- **Codex:** config overrides.
- **ACP agents:** `McpServerHttpInline` entries in `session/new`, sent when
  the agent advertises HTTP MCP support. These replace the empty list at
  `acp.go:243`.

A custom provider without an adapter refuses `mcp_servers`, as do container
and remote ACP steps. Today a harness option given inline lands in the
process arguments (`configToFlags`, `harness.go:965`), so a token passed that
way would be visible in `ps`. The gateway token is the only credential the agent sees,
and it lasts only as long as the step.

#### Agent DAGs and chat steps

```yaml
type: agent
mcp_servers:
  - connection: work-google
    server: gmail
    tools: [search_threads, get_thread, create_draft]
    confirm: [create_draft]
steps:
  - name: escalate
    action: dag.run
    with: { dag: open-incident }
tasks:
  - name: triaged
    description: Every unread support email has a draft reply or an incident.
```

- When the run starts, the agent loop lists each server's tools through the
  gateway. Each MCP tool becomes one function tool named `<server>__<tool>`,
  sanitized and kept within the 64-character limit like step tool names, and
  its input schema becomes the tool's parameters. The catalog is saved with
  the `__agent__` node, as Spec 032 already requires, so a run's tools stay
  fixed while it runs.
- A call runs through the gateway, inside the agent loop. The result is
  added to the transcript under the observation limits that apply to step
  results.
- `confirm` opens an `ask_user` human task before the call. The task shows
  the tool and its arguments, and the run continues once a person decides.
  Step `approval` cannot serve here. It waits after the step runs
  (`internal/ir/step.go:132`), and it keeps a DAG off workers
  (`internal/ir/dag.go:489`).
- Chat steps take the same field under `llm.mcp_servers`. The tool registry
  in `internal/runtime/builtin/chat/tools.go` gains MCP tools beside child
  DAGs.
- The integration points are `NewCatalog`
  (`internal/runtime/agentloop/catalog.go:43`), the planner's decision
  mapping, and `prepareAgentActionNode`
  (`internal/runtime/agent_batch.go:123`).

#### Google's agent policy

- Google MCP presets and AI steps that use a Google user connection need an
  app with `agent` set.
- An agent DAG gets a lint warning when its catalog includes a step that
  calls Google REST APIs through a connection, such as `http.request` or a
  mail action. The warning cites Google's 2027-02-01 requirement that AI
  agents use MCP.
- Google recommends a separate OAuth client for non-agent use. The wizard
  explains this, and offers to create a second app for REST automation.

### Any MCP server

An `mcp` connection connects any remote MCP server that follows the MCP
authorization spec:

1. Call the server without a token. Read `resource_metadata` from the
   `WWW-Authenticate` header, or fetch the protected resource metadata
   (RFC 9728).
2. Fetch the authorization server metadata (RFC 8414).
3. Register a client (RFC 7591) when the server supports it, and store the
   result as an app of kind `dcr`. Otherwise ask for a client ID and secret.
4. Run the connect flow with PKCE and the `resource` parameter (RFC 8707).
   The connection is bound to the server's canonical URL.

go-sdk v1.7.0 already implements these calls in its `oauthex` package, and
Dagu reuses it.

## Surfaces

### CLI

```sh
dagu connection providers
dagu connection add google [NAME] --services gmail,calendar:write --agent \
    [--client-file client_secret.json] [--no-browser] [--workspace ops]
dagu connection add google NAME --service-account key.json --subject ops@example.com
dagu connection add microsoft NAME --services mail,calendar [--device-code]
dagu connection add mcp NAME --url https://mcp.example.com/mcp
dagu connection add oauth2 NAME --authorize-url URL --token-url URL \
    --scopes a,b --bound-host api.example.com
dagu connection import NAME --provider google --client-file c.json --refresh-token-stdin
dagu connection list
dagu connection show NAME
dagu connection test NAME
dagu connection reconnect NAME
dagu connection remove NAME
dagu connection tools NAME [--server gmail] [--json]
dagu connection call NAME --server gmail --tool search_threads --args '{"query":"is:unread"}'
dagu connection apps list | remove APP
```

- `connections` is an alias. The group follows
  `internal/cmd/context_command.go`, and is context-aware in
  `internal/cmd/command_scope.go`, so it works against a remote server
  through a context.
- `add` without enough flags runs an interactive wizard on a TTY. It asks
  the questions the UI wizard asks, and reads secrets with
  `term.ReadPassword`.
- `add` picks a redirect mode as the UI does. It opens a browser when it
  can, prints the URL, and polls like `dagu license activate`. With
  `--no-browser`, it prints the URL and asks for the pasted redirect URL.
- `import` moves an existing `mail_accounts` refresh token into a
  connection, with no new consent.

A first Google connection, from a laptop:

```text
$ dagu connection add google work-google --services gmail,calendar --agent \
    --client-file ~/Downloads/client_secret_123.json
Opening https://accounts.google.com/o/oauth2/v2/auth?... in your browser.
Waiting for consent (Ctrl-C to cancel)...
Signed in as ops@example.com. Is this the right account? [Y/n] y
  gmail     read   ok  (10 MCP tools)
  calendar  read   ok  (9 MCP tools)
Connection work-google is ready. Try: dagu connection tools work-google --server gmail
```

### REST API

| Method and path | Purpose |
| --- | --- |
| `GET /connection-providers` | Providers, services, scope sets, setup links, and this server's redirect URI and modes |
| `GET /oauth-apps`, `POST /oauth-apps` | List apps, or create one. A Google client JSON file is accepted as is |
| `GET`, `PATCH`, `DELETE /oauth-apps/{id}` | Read, change, or delete an app that no connection uses |
| `GET /connections`, `POST /connections` | List connections with their status, or create one in `pending` |
| `GET`, `PATCH`, `DELETE /connections/{name}` | Read, change metadata, or remove |
| `POST /connections/{name}/authorization` | Start a connect or reconnect. Returns the authorization URL, the mode, and a session handle |
| `GET /connections/{name}/authorization` | Poll: `pending`, `confirm` (with the account), `connected`, or `failed` |
| `POST /connections/{name}/authorization/complete` | Paste mode and CLI loopback: the code or the redirect URL |
| `POST /connections/{name}/authorization/confirm` | Confirm the account |
| `DELETE /connections/{name}/authorization` | Cancel |
| `POST /connections/{name}/test` | Probe each service |
| `GET /connections/{name}/tools?server=` | MCP tools with their input schemas, for the UI and editor |
| `POST /connections/{name}/tools/{tool}/call` | Try a tool with arguments |
| `GET /connection-templates?provider=` | Starter DAGs for a provider |

- `GET /oauth/callback` sits outside the generated API, as `oidc-callback`
  does.
- Responses never include a secret. They report `clientSecretConfigured`,
  `refreshTokenConfigured`, and similar flags, as notification channels do
  (`internal/notification/model.go:253`). A `PATCH` with a blank secret keeps
  the stored one.
- Writes and tool calls are audited with `logAudit`, as secret writes are
  (`internal/service/frontend/api/v1/secrets.go:180`).
- A server answers these paths only for its own connections. A request
  whose `remoteNode` names another server is refused, as inventory requests
  are. Each server has its own callback URL.
- The tool list is cached in server memory per connection and server, for
  10 minutes, with a bounded size. Runs never read this cache.

### UI

A **Connections** page under Configure, also linked from the Integrations
page (`ui/src/pages/integrations/index.tsx`).

```text
┌ Connections ─────────────────────────────────────────────────────────┐
│ [+ New connection]                                                   │
│                                                                      │
│ name          provider   account            services     status      │
│ work-google   Google     ops@example.com    gmail, cal   ● connected │
│ m365          Microsoft  ops@example.com    mail         ● connected │
│ notion        MCP        workspace "Ops"    —            ▲ reconnect │
└──────────────────────────────────────────────────────────────────────┘
```

**New connection** opens a gallery with four tiles: Google Workspace,
Microsoft 365, Any MCP server, and Custom OAuth2. The Google wizard has five
steps:

1. **Services.** Cards for Gmail, Calendar, Drive, Docs, Sheets, Slides,
   Chat, People, and Search, each with a read or read-and-write choice. The
   scopes they need are listed underneath.
2. **Agents.** "Will AI agents use this connection?" Yes sets the app's
   agent flag. The step says why Google wants separate clients for agents
   and for other automation.
3. **OAuth app.** The user picks an existing app, or follows a checklist:
   - **Enable APIs:** a button opens `console.cloud.google.com/flows/enableapi`
     with the APIs for the chosen services filled in.
   - **Consent screen:** a link, with a warning that an app in Testing
     status must be reconnected every seven days. Publishing to Production,
     or making the app Internal, avoids this.
   - **Create OAuth client:** a link, the client type to choose, the
     redirect URI with a copy button, and, for agents, the checkbox to tick.
   - **Upload:** a drop zone for the downloaded `client_secret_*.json`. Dagu
     reads the client, checks its type, and checks that the redirect URI is
     registered.
4. **Consent.** A popup, opened before any request so that browsers do not
   block it, as `ui/src/pages/license/useLicenseConnect.ts` does. The page
   polls until the popup's callback completes. A blocked popup falls back to
   a link. Paste mode shows a field for the redirect URL.
5. **Done.** The account to confirm, granted scopes, a result per service,
   and two buttons: **Start from a template** and **Browse tools**.

The connection page shows status and the reason for it, the account,
services and scopes, the workspace allow-list, and Reconnect, Test, and
Remove. It also has a tool browser:

- a list of each MCP service's tools;
- a form built from each tool's input schema;
- **Try it**, which shows the result;
- **Copy as step**, which copies an `mcp.call` step in YAML.

Elsewhere in the UI:

- **DAG editor.** `connection:` completes connection names. It uses a
  `connectionName` definition in `schemas/dag.schema.json` and the mechanism
  that completes calendar names (`ui/src/lib/calendarNameExamples.ts`,
  `ui/src/features/dags/components/dag-editor/DAGSpec.tsx`). `server`
  completes from the connection's presets. `tool` and argument keys complete
  from `GET /connections/{name}/tools`. The server also reports unknown
  connections and connections the DAG's workspace may not use.
- **Run page.** A step that failed with `reconnect_required` shows
  **Reconnect** next to its error.
- **Navigation.** The Connections item shows a badge while any connection
  needs reconnecting.
- **Getting started.** The checklist
  (`ui/src/features/activity/hooks/useGettingStarted.ts`) gains a "Connect
  an account" step.

### Templates

Each provider has a few starter DAGs:

- a daily Gmail digest sent to chat;
- an agent that prepares notes for today's Calendar meetings from Drive;
- a weekly report written to Sheets;
- an `mcp.call` example for any MCP server.

A template creates a DAG with the connection name filled in. The same
templates appear in `dagu example` (`internal/cmd/example.go`), and the docs
show them.

### MCP

- `dagu_read` gains `target=connections` and `target=connection`, following
  `internal/service/mcp/read_nodes.go`. They return names, providers,
  services, status, and tools, and never a secret.
- The authoring reference (`internal/service/mcp/reference.go`) gains a
  connections section. An agent that writes DAGs then uses `connection:
  <name>` instead of asking for a token.

## Permissions

| Action | Role |
| --- | --- |
| Create, change, or delete an OAuth app | `admin` |
| Set `allow_token_export` | `admin` |
| Create, connect, reconnect, change, or remove a connection | `manager` or above in every workspace on its allow-list. A `*` allow-list needs `admin` |
| Test a connection, list its tools | `developer` or above |
| Try a tool | `developer` or above, audited |
| Use a connection in a DAG | Whoever may edit the DAG, if the DAG's workspace is on the allow-list |
| See connections: name, provider, account, status | `viewer` |

These follow the secret store, where managers manage a workspace's secrets
(`requireSecretManageForWorkspace`, `secrets.go:383`). OAuth apps follow
notification channels, which stay server-wide and admin-only under RFC 003.
Once RFC 003 ships, allow-lists name projects by ID, so renaming a project
keeps its connections.

## Security

- **Secrets at rest.** Refresh tokens, access tokens, and client secrets are
  encrypted at rest with the server's key, in directories readable only by
  the Dagu user.
- **Audience binding.** A token goes only to its connection's bound hosts or
  resource, and never follows a redirect off them.
- **Allow-list enforcement.** It is checked twice: when the run starts, and
  by the coordinator for each token. In local mode the run process can read
  the encryption key, so the check there is a policy, as
  [Guarantees](#guarantees) states.
- **Service accounts.** The admin fixes the impersonated user. No step,
  parameter, or tool argument can change it.
- **The connect flow.**
  - Every request carries an encrypted state and PKCE.
  - A callback is single-use and expires.
  - A callback completes only for the user who started it, and the account
    must be confirmed in the UI or CLI that started it.
  - The callback page shows no token, and closes the popup.
- **Agents.** Agents never hold provider tokens. The gateway accepts only
  its step's bearer token, offers only allowed tools, and refuses calls to
  tools whose descriptions changed.
- **Prompt injection.** An agent that reads email or documents can be steered
  by their content. `confirm` puts a person in front of write tools, and the
  docs recommend it for every tool that sends, shares, or deletes. [Open
  questions](#open-questions) asks whether to require it.
- **Masking.** Tokens are masked in local logs, status, and outputs.
  Rollout step 0 extends masking to log streams from workers, which are not
  masked today (`internal/runtime/output.go:484`). Token export waits for
  that fix.
- **SSRF.** `oauth2` and `mcp` endpoints come only from managers and admins.
  Endpoints other than HTTPS are refused unless the host is a loopback
  address.
- **Audit.** These are written to the audit log:
  - app and connection changes;
  - connects, reconnects, and removals;
  - status changes;
  - tool calls from the UI and from harness agents.

  Token issuance is not audited, since it happens once an hour per
  connection.

## Failure behavior

| Event | Behavior |
| --- | --- |
| The user denies consent or closes the popup | The session fails or expires. The connection stays `pending`, or keeps its previous tokens on a reconnect |
| The callback arrives after 10 minutes, twice, or for another user | Refused. Nothing changes |
| The user unticks a scope | The connection is created. Services missing a scope show it, and steps that need it fail with `insufficient_scope` |
| A Google MCP probe is rejected | The service shows the agent flag hint and a link. Other services are unaffected |
| The refresh token is revoked or expires | `needs_reconnect` once, a badge in the UI, and every step that uses the connection fails with `reconnect_required` and the connection's name |
| The OAuth app's secret is rotated or deleted at the provider | `app_invalid` on the app's connections. Updating the app restores them |
| The token endpoint is down | Steps fail as transient, and retry under their own policy. Status is unchanged |
| A reconnect happens while a refresh is running | The refresh result is dropped. Both processes end with the reconnected token |
| Two processes refresh at once | One holds the lock. The other waits, reads the new token, and does not refresh |
| A DAG names an unknown connection, or one its workspace may not use | The editor reports it. A run fails before its first step, naming the connection |
| A worker's coordinator is older and lacks the token RPC | The step fails, saying the coordinator must be upgraded |
| An upstream MCP tool's description changes during a run | Calls to that tool fail until the next run, which records the new description |
| A harness step in a container sets `mcp_servers` | Validation fails, naming the limit |

## Performance

- **Idle cost.** None. No ticker, watcher, or scan is added. Tokens refresh
  only when a step needs one.
- **Refresh rate.** At most once per connection per token lifetime, about
  one an hour, regardless of the number of runs. Today mail refreshes once
  per run, so a mail DAG that runs every minute drops from 60 refreshes an
  hour to one.
- **Per run.** A run that uses a connection reads its token state at most
  once per token lifetime: one small file read locally, or one coordinator
  RPC on a worker. Steps and `foreach` items share the run's cache, so the
  cost grows with runs that use connections, not with steps. At one run per
  second, that is at most one read or RPC per second.
- **Requests.** Connection lists read one small record per connection. A
  server holds tens of connections, not thousands, and no request reads run
  or DAG directories.
- **Tool lists.** Cached in server memory for the UI and editor: 10 minutes,
  bounded in size, and fetched once per connection and server whatever the
  number of open tabs. Runs fetch their own list once at start. Neither
  path polls.
- **Gateway.** One loopback listener per AI step with `mcp_servers`, tied to
  the step's context. It adds one local hop per tool call.
- **Storage.** About 2 KB per connection and 1 KB per app. Nothing is added
  to run status, logs, or indexes. Audit grows only with management actions
  and tool calls from the UI.
- **Upgrade.** No migration and no index rebuild.

## Compatibility and rollout

- **Additive.** A DAG without connections behaves as before.
  `mail_accounts.oauth`, SMTP OAuth, and static `headers` keep working.
- **Older binaries.** A DAG that uses `connection`, `server`, or
  `mcp_servers` fails validation on an older binary, which rejects unknown
  keys. An older binary ignores the new collections, so a downgrade is safe
  and drops the feature.
- **Version skew in distributed mode.** A new worker under an old
  coordinator fails only the steps that use connections, as [Failure
  behavior](#failure-behavior) states. An old worker under a new coordinator
  is unaffected, since it never sees connection fields it would need to
  resolve.
- **API.** New paths only. Existing schemas are unchanged.
- **Specs.** 098 (`mcp.call`), 045 (`http.request`), 073 (mail), 032 (agent
  DAGs), 059 (chat), 067 (harness), and 069 (secret providers) gain the new
  fields. A new spec defines connections, the broker, and the connect flow.

Rollout, in pull requests that each work on their own:

0. **Mask worker log streams.** `setupRemoteWriters` writes step output to
   the coordinator without `MaskingWriter`. This is an existing bug, fixed
   first and separately.
1. **Records and broker.**
   - The two collections, providers, and the broker with locking,
     generation checks, and saving rotated tokens.
   - `dagu connection import`, plus `add` for service accounts and client
     credentials, which need no consent screen.
   - `mail_accounts.*.connection`. This alone fixes Microsoft rotation for
     mail.
2. **Connect flows.** All four redirect modes, the REST API, the
   Connections page, the Google wizard, `dagu connection add`, test, and
   reconnect.
3. **Steps.**
   - `ir.DAG.Connections`, and the coordinator RPC.
   - `http.request` with bound hosts.
   - `mcp.call` with connections and the Google presets.
4. **Any MCP server.** Discovery, registration, and the gallery tile.
5. **Authoring.** Editor completion, the tool browser and Try it,
   templates, `dagu_read`, and the docs.
6. **Token export.** The `connection` secret provider, behind
   `allow_token_export`.
7. **Gateway and harness steps.** Claude Code, Codex, and ACP adapters.
8. **Agent DAGs and chat steps.** MCP tools, `confirm`, and the Google
   policy lint.

## Verification and acceptance criteria

1. On a fresh server with a public URL, a user follows the Google wizard,
   uploads a client JSON, consents, and lists Gmail tools without leaving
   the UI except for the Google Cloud console and the consent screen.
2. `dagu connection add google` works in each mode: in a desktop session
   (loopback), over SSH to a server with a public URL (server callback), and
   over SSH to a server without one (paste).
3. An `mcp.call` step with `connection` and `server: gmail` succeeds, both
   locally and on a worker with no shared filesystem.
4. With 100 concurrent runs using one connection whose access token has
   expired, the provider receives exactly one refresh request.
5. A rotated refresh token is saved, and the next refresh uses it, across
   process restarts.
6. Revoking access at the provider makes the connection `needs_reconnect`
   once. Every later step fails without calling the token endpoint.
   Reconnect restores it.
7. A reconnect during a refresh leaves the reconnected token in place.
8. No refresh token, access token, or client secret appears in any API
   response, CLI output, MCP result, run status, step log, worker log
   stream, or audit entry. A test searches each of them for the values.
9. `http.request` with a connection never sends the token to a host outside
   the bound hosts, including through a redirect.
10. A run on a worker cannot obtain a token for a connection its DAG does not
    declare, or one whose allow-list excludes its workspace.
11. A harness step with `mcp_servers` completes a turn longer than the
    access token's lifetime. The agent process's arguments and environment
    contain no provider token.
12. An agent DAG calls an MCP tool with arguments. A `confirm` tool waits
    for a person, and runs only after approval.
13. Each role can do what [Permissions](#permissions) lists and nothing
    more.
14. Every case in [Failure behavior](#failure-behavior) ends as stated.

## Alternatives considered

- **Storing tokens in the secret store.** It would give encryption, masking,
  and coordinator resolution for free. But every write adds a version, and
  Microsoft rotates refresh tokens on every refresh. The secret RPC returns
  a stored value and cannot refresh. RFC 003 also moves secrets into
  projects, while connections are server-wide.
- **Refresh tokens in YAML or base config, as mail does today.** No consent
  flow, no place to save a rotated token, and a long-lived credential in a
  file that is often under version control.
- **Static headers with a token refreshed by a command step.** Works today,
  but the token passes through step output. It expires during long steps,
  and every DAG repeats the plumbing.
- **Giving agents the provider token in their MCP config.** Simpler than a
  gateway. But the token sits in the agent's files or arguments, expires
  during long turns, and allows every tool.
- **A byte-level reverse proxy as the gateway.** Less code, but it cannot
  enforce a tool allow-list, pin tool descriptions, or audit calls without
  parsing MCP anyway.
- **Server-side sessions for the connect flow.** Needs a store shared by
  frontend replicas. An encrypted state carries the same data.
- **Per-project connections only.** Simpler scoping. But one Google account
  often serves several teams, and RFC 003 keeps credential-holding services
  such as notification channels server-wide.
- **A Dagu-hosted Google app now.** One-click connect, without Google Cloud
  setup. But Gmail scopes are restricted, which takes Google verification
  and a yearly security assessment, and the app would depend on Dagu Cloud.
  The `hosted` app kind leaves room for it.
- **Naming the concept "accounts", "integrations", or "credentials".**
  "Accounts" clashes with `mail_accounts`, and does not fit client
  credentials or MCP servers. "Integrations" is broader than a sign-in.
  "Credentials" sounds like the secret store. "Connections" matches other
  workflow tools. SSH node connections and the Dagu Cloud connection keep
  their names.

## Open questions

1. Should `needs_reconnect` send a notification through the server's
   channels, once per change?
2. Should `confirm` be required for write tools when an agent's catalog also
   reads untrusted content, such as email or shared documents?
3. Should an agent DAG list its tools when it is loaded, which needs network
   access during validation, or only when it runs, as this RFC proposes?
4. Can a host without a coordinator, sharing the data directory over NFS,
   refresh safely, or must such deployments run a coordinator?
5. May any manager reconnect a connection with a different account, or only
   when the account stays the same?
6. Should a second connection for the same account and client be refused,
   rather than warned about?
7. Which free-tier limits should the docs state? A third-party write-up
   reports 5,000 MCP requests a day per project and product. Google's own
   pages do not state a number.
8. Should a later Dagu-hosted app use Client ID Metadata Documents, which
   newer MCP authorization specs describe, rather than registration? This
   needs checking against the current MCP spec.

## Future enhancements

- **A Dagu-hosted OAuth app,** relayed through Dagu Cloud, for one-click
  Google and Microsoft connections.
- **MCP elicitation.** A tool that asks for confirmation mid-call would
  wait for an answer in the UI, using go-sdk's elicitation handler.
- **Harness steps in containers.** The step would bind-mount a Unix socket
  for the gateway and run a stdio bridge inside the container. It would also
  work with `network: none`.
- **Personal connections,** with a run-as-user model.
- **More presets,** such as Slack, GitHub, and Salesforce, as their MCP
  servers settle.

## References

- Google Workspace Updates, "Simplifying agent access while keeping
  organizations in control" (2026-11-24):
  https://workspaceupdates.googleblog.com/2026/11/mcp-servers-cli-standardized-usage-tiers-now-available.html
- Configure the Google Workspace MCP servers:
  https://developers.google.com/workspace/guides/configure-mcp-servers
- Configure OAuth clients for AI agents:
  https://support.google.com/cloud/answer/15549257
- MCP authorization: https://modelcontextprotocol.io/specification/latest/basic/authorization
- RFC 7591 (dynamic client registration), RFC 8414 (authorization server
  metadata), RFC 8707 (resource indicators), RFC 9728 (protected resource
  metadata)
