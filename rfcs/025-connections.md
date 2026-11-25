# RFC 025: Connections

- **Status:** Proposed
- **Date:** 2026-11-25
- **Affected areas:** a new `connection` package (records, providers, connect
  flow, token broker), `cmn/mailer/oauth` (refresh code moves out), `spec`
  and `ir` (`connection` fields, `mcp_servers`, declared connections),
  `runtime` (masking, agent loop), `runtime/builtin` (`mcp`, `http`, `mail`,
  `harness`, `chat`), `secret/providers` (a `connection` provider),
  `service/coordinator` and `service/worker` (token RPC), `service/frontend`
  (API, OAuth callback), `eventstore` and `service/chatbridge` (connection
  notifications), `license` (Cloud relay client), `service/mcp`, `cmd`,
  `api/v1`, `proto`, `ui`, `specs`, `conformance`, `docs`, and Dagu Cloud
  (Cloud relay endpoints)

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
      connect engine: state + PKCE; server callback | cloud relay | loopback
                      | paste | device code
                                  |
          +-----------------------+------------------------+
          v                       v                        v
     oauth-apps/            connections/<name>        connection-tokens/<name>
     client ID,             account, services,        refresh token, access token
     client secret,         workspaces, added         per resource, expiry, status,
     agent flag, status     scopes and hosts          generation, connect session
                                  |
     +--------------+-------------+-------------+---------------------------+
     v              v                           v                           v
  mcp.call     http.request               mail_accounts           MCP gateway, per step
  server: gmail  bound hosts only          connection:            harness, agent DAG, chat
     |              |                           |                           |
     +--------------+-- Token(conn, resource) --+---------------------------+
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
  the AI-powered agent flag, or the MCP servers reject it. Google requires
  a separate client for agent and non-agent use. An app left in Testing
  status issues refresh tokens that expire after seven days. Users find each
  of these by failing.

## Goals

1. On a fresh server, a user connects a Google account from the UI by
   following the wizard alone, without other docs. The UI guides the Google
   Cloud setup, checks it, and turns each setup error into its fix.
2. The CLI connects an account in one command, including over SSH with no
   local browser.
3. `mcp.call`, `http.request`, mail, harness, agent DAG, and chat steps refer
   to a connection by name. Google requires separate OAuth clients for agents
   and for other automation, so a Google user who needs both holds two
   connections.
4. No secret appears in YAML, logs, run status, API responses, MCP results,
   or audit entries.
5. Each connection refreshes at most once per token lifetime for each
   resource it serves, however many runs use it, and a rotated refresh token
   is saved. Token export, which needs a longer-lived token, refreshes more
   often, within a fixed bound.
6. An expired or revoked sign-in is reported once, names the connection, and
   offers Reconnect.
7. Connections work in a single process, behind several frontend replicas,
   and for runs on workers that share no filesystem with the server.
8. AI steps meet Google's MCP requirement without extra configuration.
9. DAGs that use no connection behave as before.

## Non-goals

- **A Dagu-hosted OAuth app, and connections held by Dagu Cloud.** The user
  brings their own client in this RFC, and the server keeps the tokens. The
  records reserve both, as [Dagu Cloud](#dagu-cloud) describes. Dagu Cloud
  takes part only in the connect flow, through the Cloud relay.
- **Personal connections and run-as-user identity.** Connections are
  server-wide. A user's own account, used by runs they start, needs its own
  design.
- **MCP sampling and URL-mode elicitation.** Dagu advertises form
  elicitation only, as [Confirmation requests](#confirmation-requests)
  describes.
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
  returned by the API, CLI, or MCP server, and it never reaches a step's
  process, a step's environment, or a worker.
- An access token is sent only to the hosts the connection is bound to. It
  does not follow a redirect to another host.
- A run receives a token only for a connection that its DAG declares, and
  only when the connection's allow-list includes the run's workspace.
- A refresh result never overwrites a newer reconnect.
- A connection becomes `needs_reconnect`, or an app `invalid`, once per
  failure. Only a confirmed reconnect, or an update to the app, clears it.
  Each such change sends one notification.
- An AI step never calls a write tool without a person's approval, unless
  the DAG lists that tool in `unattended`.
- Dagu never answers a server's confirmation request on its own. Only a
  person accepts one.
- Each connection refreshes each resource at most once per token lifetime
  across all processes on a host, except while runs export its token, as
  [Token broker](#token-broker) bounds. Workers never refresh.

Not promised:

- **Isolation from the run process in local mode.** The run process can
  read the encryption key, and it decrypts the refresh token in memory when
  it refreshes. So a workspace allow-list is a policy there, not an
  operating system boundary. In distributed mode the worker never holds
  the key.
- **Secrecy of an exported token.** A token exported to an environment
  variable can be printed by the script that receives it. Masking hides the
  exact value in Dagu's logs, nothing more. Export is off unless an admin
  enables it for the connection.
- **One refresh across hosts.** Hosts that share the data directory over NFS
  can read a stale token record and refresh again. Shared storage, under
  [Token broker](#token-broker), gives the cost.
- **Google quotas and billing.** Dagu reports the provider's errors as they
  come.

## Design

### Records

Server-wide records in three collections under the data directory, each
created with mode `0o700` as the license store's is
(`internal/persis/file/shared_stores.go:102`). Secret fields are encrypted
with `internal/cmn/crypto`, using the key from `DAGU_ENCRYPTION_KEY` or
`<data_dir>/auth/encryption_key`.

**OAuth app** (`<data_dir>/oauth-apps/`): a client registration that several
connections share.

| Field | Notes |
| --- | --- |
| `id`, `name` | Name shown in pickers |
| `provider` | `google`, `microsoft`, `oauth2`, or `mcp` |
| `kind` | Who owns the client: `byo` (the user), `dcr` (registered by Dagu through RFC 7591), or `hosted` (reserved: a client Dagu owns, for one-click connect) |
| `client_id` | |
| `client_secret` | Encrypted. Optional for public clients. For a service account, its private key |
| `client_type` | `web`, `desktop`, `public` (no secret, as the device code flow needs), or `service_account` (a Google service account key). Decides which grants and redirect modes work |
| `agent` | The user attests that the client has "Use this client for an AI-powered agent" set |
| `endpoints` | Only for `oauth2` and `mcp`: authorize, token, revoke, and device URLs |
| `status`, `status_reason` | `ok` or `invalid`, with the provider's error. The broker sets `invalid` with a CAS write when the provider rejects the client. An admin's update to the client ID or secret resets it to `ok` |

**Connection**: one authorized account, stored as two records in two
collections with different writers.

The metadata record (`<data_dir>/connections/`) is written by managers and
admins:

| Field | Notes |
| --- | --- |
| `name` | Lowercase letters, digits, and hyphens, up to 63 characters. Unique on the server |
| `provider`, `app` | The app holds the client, or, for a service account, its key. A key is rotated by updating the app |
| `grant` | `authorization_code`, `device_code`, `client_credentials`, or `service_account` |
| `services` | Presets such as `gmail` or `calendar`, each with `read` or `write` |
| `scopes` | Scopes added beyond the services' presets. The presets' own scopes are not stored |
| `account` | Identity read after consent, such as an email address or subject |
| `subject` | Service accounts only: the impersonated user, fixed by the admin |
| `bound_hosts` | Hosts added beyond the services' presets that may receive the token. Required for `oauth2` |
| `resource` | MCP connections: the canonical server URL (RFC 8707) |
| `workspaces` | The allow-list: workspace names, `.global` for DAGs without a workspace label as secrets name that scope (`internal/secret/secret.go:42`), or `*` for every workspace. Defaults to the workspace the creator has selected, or `.global` when none is |
| `allow_token_export` | Admin opt-in for the `connection` secret provider. Off by default |
| `holder` | Who keeps the tokens: `local`, this server. `cloud` is reserved for a connection Dagu Cloud keeps, which this server reads but does not change. The app's `kind` says who owns the client; `holder` says who keeps the tokens |

The token state record (`<data_dir>/connection-tokens/`) is written only by
the `connection` package: the connect flow and the token broker.

| Field | Notes |
| --- | --- |
| `refresh_token` | Encrypted |
| `access_tokens` | One encrypted access token and its expiry per resource. Google and MCP connections have one resource. A Microsoft connection has one per Microsoft resource it calls, such as Graph and Outlook |
| `refresh_token_expires_at` | Only when the provider reports it |
| `granted_scopes` | What the provider granted. It can be fewer than requested |
| `status` | `pending`, `connected`, `needs_reconnect`, or `revoked` |
| `status_reason` | The provider's error code and text, without secrets |
| `generation` | Incremented when a connect or reconnect is confirmed |
| `session` | The connect session in progress, if any. See [Connect flow](#connect-flow) |
| `refreshed_at` | |

Metadata edits and token refreshes touch different collections, so they
never conflict. The broker is the only writer of a connection's status and
of an app's status. Requested scopes and bound hosts are derived when read,
from the presets compiled into the binary plus the stored additions. An
upgrade that adds a scope to a preset therefore reaches every connection,
and the service shows the missing scope until the user reconnects. Neither
record is stored in the secret store; [Alternatives
considered](#alternatives-considered) gives the reasons.

### Providers and services

| Provider | Grants | Notes |
| --- | --- | --- |
| `google` | Authorization code, or a service account with domain-wide delegation | Google rejects MCP calls from a user-delegated client without the agent flag. Service accounts are accepted without it. Google requires separate clients for agent and other use |
| `microsoft` | Authorization code, device code, client credentials | `tenant` is `common`, `organizations`, or a tenant ID. Refresh tokens rotate. Each access token is valid for one resource |
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
so sending stays with `mail.send` on a mail account that uses a standard,
non-agent connection.

Microsoft presets are `mail`, `calendar`, and `files`, against Graph
(`graph.microsoft.com`), and `mailbox`, against Outlook
(`outlook.office.com`) for the IMAP and SMTP access that mail accounts use
(`internal/cmn/mailer/oauth/oauth.go:31`). Every Microsoft connection also
asks for `offline_access` and `User.Read`. A Microsoft access token is valid
for one resource, so the broker keeps one token per resource and refreshes
each with that resource's scopes.

### Connect flow

The UI, CLI, and API share one engine.

```text
UI or CLI                 Dagu server                         provider
   | POST .../authorization  |                                     |
   |------------------------>| session = {id, user, waiting}       |
   |<-- authorization URL ---| state = encrypt{conn, session id,   |
   |                         |   PKCE verifier, expiry}            |
   |  browser ---------------------------------------------------->| consent
   |                         |<--- GET /oauth/callback?code&state -|
   |                         |  exchange code with the verifier -->|
   |                         |  read identity, probe services ---->|
   |                         | session = {confirm, account, tokens}|
   | GET .../authorization   |                                     |
   |---- poll -------------->| confirm (account) | failed          |
   | POST .../confirm        |                                     |
   |------------------------>| tokens go live, generation + 1      |
```

- **Sessions.** The token state record holds the session in progress: its
  ID, the starting user, its phase (`waiting`, `confirm`, or `failed`), and,
  once consent completes, the account and the new tokens, encrypted. The
  `state` parameter carries the connection, the session ID, the PKCE
  verifier, and the expiry, encrypted, so any frontend replica completes a
  callback. Starting a session replaces an earlier one, which then fails. A
  session expires after 10 minutes.
- **Single-use callbacks.** The callback exchanges the code only while its
  session is `waiting`, and moves it to `confirm` with a CAS write. A second
  callback for the same session finds it past `waiting` and is refused.
- **Confirmed by the starting user.** The callback needs no Dagu login,
  because the browser that consents may have none, as when the CLI runs over
  SSH. The new tokens go live only when the user who started the session
  confirms the account. One CAS write moves the tokens into the live fields,
  increments `generation`, sets `connected`, and clears the session. Until
  then, runs keep the previous tokens. A caller may name the expected account
  when starting, and a matching account is then confirmed without asking.
  With authentication off, any caller of the API may confirm, as it may take
  any other API action.
- **PKCE (S256) everywhere.** Google requests also send
  `access_type=offline` and `prompt=consent`, so a refresh token is always
  returned.

Redirect modes:

| Mode | When | Client type |
| --- | --- | --- |
| Server callback | The server has a usable public URL: `server.public_url`, else the tunnel URL, else the UI's origin when the browser reaches Dagu at `localhost`, which Google accepts over HTTP. The redirect URI is `<public_url>/oauth/callback`, a public route beside `oidc-callback` (`internal/service/frontend/server.go:1445`) | `web` |
| Cloud relay | The server has an online license (RFC 002) but no usable public URL. The redirect URI is Dagu Cloud's fixed callback, `<cloud_url>/oauth/callback`, and the server fetches the code from Dagu Cloud | `web` |
| Loopback | The CLI runs where a browser can open. The CLI listens on `127.0.0.1` on a random port and sends the code to the server | `desktop` |
| Paste | None of the above works, as for an unlicensed server on a private address. The browser lands on a loopback URL that fails to load, and the user pastes that URL into the UI or CLI | `desktop` |
| Device code | Microsoft, over SSH. The CLI shows a code to enter on another device | `public` |

Google web clients accept only HTTPS or `localhost` redirect URIs, and
Google's device flow does not cover Gmail scopes, so a Google connection uses
one of the first four modes. The UI and CLI pick the first mode in the
table that works, given the server's public URL, its license, and the app's
client type, and show the exact redirect URI to register.

**Cloud relay.** A server without a public URL still gets a fixed HTTPS
callback, without paste mode and without Dagu Cloud holding any secret:

- Starting a session, the server registers a callback with Dagu Cloud under
  its license credentials, and gets a callback ID. The `state` parameter
  starts with that ID in clear, followed by the encrypted state, so Dagu
  Cloud routes the callback without reading it.
- Dagu Cloud's callback page stores the code and state under the ID, and
  tells the person they may close the page.
- While the session waits, the server polls Dagu Cloud for the callback
  every 3 seconds, as license activation polls (RFC 002). It then checks the
  encrypted state and completes the callback as if it had received it.
  Dagu Cloud deletes the code on the first fetch, or after 10 minutes.
- Dagu Cloud sees only the code. The PKCE verifier and the client secret
  stay on the server, so Dagu Cloud cannot exchange the code for tokens.
  RFC 002's rules hold: Dagu Cloud stores no secret, and runs never depend
  on it, since only connecting does.
- One redirect URI serves every server, so a team with several servers
  registers it once in each OAuth client.

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
  making it Internal, removes the seven-day limit. The Connections page then
  shows the time left on every read, and the navigation badge appears two
  days before the expiry. No ticker checks it, so no notification is sent
  before the token expires.
- **Duplicates.** A second connection for the same account and client
  triggers a warning. Google keeps only 100 refresh tokens per account per
  client and silently revokes the oldest.

**Consent errors.** Google's errors on the consent screen are mapped to the
fix, since the raw codes do not say what to do:

| Error | Shown |
| --- | --- |
| `access_denied` | Google does not say which cause applies, so both are shown: the user declined, or the app is in Testing status and the account is not a test user. The second comes with how to add a test user |
| `admin_policy_enforced` | The Workspace admin blocks this client. Ask them to trust the client ID, shown with a copy button, under API controls in the Admin console |
| `redirect_uri_mismatch` | The redirect URI to register, with a copy button |
| `invalid_client` | The client ID or secret is wrong, or the client was deleted. Upload the client JSON again |

**Reconnect** runs the same flow for an existing connection. The previous
tokens stay live until the user confirms, and confirming increments
`generation`. A different account than before is never confirmed without
asking.

**Remove** takes the connection's lock, revokes the refresh token where the
provider supports revocation, then deletes both records. A DAG that still
names the connection fails at run start, naming it. Creating a connection
replaces any token state record left under its name, so a new connection
never inherits an old one's tokens.

### Token broker

Every token passes through one interface:

```go
// Token returns an access token for conn, valid at resource, that stays
// valid for at least minTTL, and how long it stays valid. An empty
// resource selects the connection's only resource.
Token(ctx context.Context, conn, resource string, minTTL time.Duration) (token string, expiresIn time.Duration, err error)
```

The broker hides where a token comes from, so callers never change with the
source. This RFC has two sources: the local refresh below, and the
coordinator RPC that a worker uses. A connection whose `holder` is `cloud`
would add a third, a token request to Dagu Cloud, as [Dagu
Cloud](#dagu-cloud) describes.

Steps that use a token in process ask for a `minTTL` of 5 minutes, and ask
again when the token runs low. Only token export asks for more, 50 minutes,
because a script cannot ask again. A token refreshes when its remaining life
is shorter than the `minTTL` asked for. So a connection whose token runs keep
exporting refreshes about once per token lifetime minus 50 minutes: every 10
minutes for a Google token, however many runs start.

Local path, used by the server, the scheduler, and local runs:

1. Read the token state. If the resource's access token stays valid for
   longer than `minTTL`, return it.
2. Take the connection's lock: a `dirlock` per connection
   (`internal/cmn/dirlock/dirlock.go:168`), renewed as `persis.Backend.Lock`
   renews a collection's lock (`internal/persis/file/backend.go:103`). That
   lock covers a whole collection, so a token endpoint that hangs at one
   provider would hold back every other connection's refresh. The lock
   renews itself while it is held, and refuses writes once lost.
3. Read the state again. Another process may have refreshed it.
4. Refresh with the resource's scopes. The token endpoint call times out
   before three quarters of the lock's stale threshold.
5. Write the new access token, and any rotated refresh token, with a CAS
   write (`createOrSwap`, `internal/persis/store/cas_retry.go:15`). The
   connect flow writes `session` in the same record without the lock, so a
   conflict is expected. On a conflict, read the record again and reapply
   only the token fields, so a rotated refresh token is never dropped:
   - If `generation` changed, a reconnect was confirmed during the refresh.
     Drop the result and return the reconnected token.
   - If the record is gone, the connection was removed. Drop the result and
     fail. The broker never creates a token state record.

Each run process also caches tokens in memory, behind the single-flight gate
the mail code uses today (`oauth.go:261-330`). So a run reads the state at
most once per connection and resource per token lifetime, and a `foreach`
over many items costs no more than one step.

Refresh results:

| Result | Effect |
| --- | --- |
| Success | The access token is saved. A rotated refresh token replaces the old one |
| `invalid_grant` | Read the token state once more. If the refresh token or `generation` changed, another process rotated or reconnected it, so retry once with the new state. Otherwise `needs_reconnect`, set once. Later calls fail at once, without calling the provider |
| `invalid_client` or `unauthorized_client` | The app becomes `invalid`, set once. Every connection of the app then fails at once, without calling the provider, until an admin updates the app's client ID or secret |
| 5xx, network error, or `temporarily_unavailable` | Transient. The status stays. The call fails, and the step's retry policy applies |
| Service account key unknown, disabled, or deleted | The app becomes `invalid`, set once, as for `invalid_client`. An admin uploads a new key |
| Service account clock error | Transient. Reported on the connection, and the status stays |

A grant without a refresh token, a service account or client credentials,
never becomes `needs_reconnect`, since there is nothing to reconnect. Its
permanent failures mark the app `invalid`.

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
- The coordinator calls the local broker and returns the access token, its
  lifetime in seconds, and the connection's binding: provider, services,
  bound hosts, and resource URL. The binding holds no secret, and the worker
  enforces it as a local run does. A relative lifetime makes clock skew on
  the worker irrelevant.
- With `check_only`, as `ResolveSecretReference` has, the RPC runs the same
  checks and returns the binding without a token. A worker uses it for
  admission.

Workers never refresh, so a worker cannot send a refresh token that another
host has already rotated.

**Shared storage.** Every process that has the data directory may refresh:
the server, the scheduler, local run processes, and coordinators. Hosts that
share the data directory refresh through the same lock and need no separate
path. This includes the Kubernetes chart's distributed mode, which runs the
UI, scheduler, and coordinator as separate Deployments on one ReadWriteMany
volume (`charts/dagu/README.md:83`):

- **Locking.** `dirlock` takes a lock by creating a directory, which is
  atomic on NFS.
- **Stale reads.** NFS attribute caching can still return a token record
  older than another host's write.
- **Google.** Its refresh tokens do not rotate, so a stale read costs one
  extra refresh and nothing else.
- **Rotating providers.** The re-read on `invalid_grant` recovers from a
  stale read when another host has rotated the token.
- **Reuse detection.** A provider that revokes the whole token family when an
  old refresh token is reused is the remaining risk. The docs tell users of
  such providers to keep every process that has the data directory on one
  host, or to keep the data directory on local disk.

**Notifications.** A status change records one event in the event store,
written by the process whose CAS write made the change. The events have a
new kind, `connection`, and two types:

- `connection.needs_attention`, when a connection becomes `needs_reconnect`
  or its app becomes `invalid`. It carries the connection, or the app and
  its connections, the status, the provider's reason, and a Reconnect URL.
  Without a public URL, the message names the Connections page and the
  command `dagu connection reconnect <name>` instead.
- `connection.restored`, when it returns to `connected`.

The notification monitor reads only DAG-run events today. It decodes each
into a run status (`internal/service/chatbridge/monitor.go:517`) and keys
delivery by run and attempt
(`internal/service/chatbridge/notifications.go:551`). It gains a second
reader for `connection` events. Their delivery key is the event ID, and
their message is plain text, also on channels that format run events.

Each event goes to the notification settings of every workspace on the
allow-list, and to the server-wide settings for `*` and `.global`. It is in
the default event set for new channels. A failure produces one message,
however many runs then fail on it.

Runs that fail on a broken connection would still send their own
`dag.run.failed` messages, one per run. So a run that fails with
`reconnect_required`, or on an `invalid` app, records the connection as its
failure cause. A channel that receives `connection.needs_attention` skips the
run failure message for such a run while that connection stays broken. A
channel that does not receive connection events still gets every run
failure, so no failure goes unreported. The DAG's own failure handlers run
as before. Scheduled runs keep starting and fail at admission, so the run
history shows when the connection broke and when it was restored.

**Declared connections.** Building a DAG collects the connections it names
into `ir.DAG.Connections`, as `CredentialRefs` collects SSH refs
(`internal/ir/dag.go:298`). The sources are:

- step `with.connection`;
- `mail_accounts.*.connection`;
- `secrets` entries with `provider: connection`;
- `mcp_servers` on agent DAGs, chat steps, and harness steps.

Each name must be a literal. A name taken from a parameter or variable fails
validation, since the coordinator checks every token request against the
declared names.

The DAG index (`internal/persis/file/dag/dagindex/dagindex.go:27`) also
stores each DAG's connection names, so the Connections page lists the DAGs
that use a connection without reading DAG files. An index entry loads only
metadata today, so the names are read from the YAML's connection fields
without a full build.

**Run records.** A step that uses a connection records the connection's name
and account in its node status, and a harness or chat step records how many
calls the gateway refused. A run that fails on a broken connection records
that connection as its failure cause.

**Admission checks.** Spec validation stays offline: building a DAG only
collects names. Checks that need connection records run at run admission: in
the run process, before its first step, where secrets resolve. A local run
reads the records, and a run on a worker reads the binding through the token
RPC with `check_only`. The editor's server-side diagnostics run the same
checks:

- the connection exists, and its allow-list includes the run's workspace;
- `server` names one of the connection's services;
- a literal `http.request` URL is on the bound hosts;
- a Google AI step or Google MCP preset on a user connection, not a service
  account, uses an app with `agent` set.

A failed check fails the run before its first step, naming the connection,
as an unresolvable secret does.

**Masking.** `setupMasker` builds each attempt's masker from the scope's
secrets (`internal/runtime/output.go:78`). A step that uses a connection
mints its token before node setup and adds it with
`EnvScope.WithMaskedSecrets` (`internal/cmn/value/envscope.go:409`), which
masks a value without making it an environment variable. The masker gains a
way to add a value while the step runs, so a token minted later in the step,
by a forced refresh or during a long agent turn, is masked too. It also
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
- A confirmation request from the server is declined, and the call fails
  with the new kind `confirmation_required`. See [Confirmation
  requests](#confirmation-requests).
- HTTP 429 gets its own kind, `rate_limited`, with the provider's message and
  any `Retry-After`. Today it counts as `transport` (Spec 098). A user who
  hits a quota then sees a quota error, not a network failure.
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

- The token is added only to an HTTPS request whose host is in the
  connection's bound hosts. A URL with another host or scheme fails at
  admission when it is literal, and fails at run time when it comes from a
  reference.
- A redirect to another host, or to plain HTTP, is refused. Today resty
  follows redirects, and `oauth2.Transport` would add the token again on
  every hop.
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
A Google account needs the `gmail` service with `write`, since mail actions
use the Gmail API. A Microsoft account needs the `mailbox` service, since
mail actions use IMAP and SMTP. The existing `oauth` form keeps working,
unchanged.

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
use a connection-aware step instead. For a Microsoft connection,
`options: { service: mail }` selects the resource. The provider works only
when an admin has set `allow_token_export` on the connection. Its value is
masked like any secret.

### MCP servers for AI steps

`mcp_servers` gives an AI step tools from connections:

```yaml
mcp_servers:
  - work-google/calendar            # preset of a google connection
  - notion                          # an mcp connection
  - connection: work-google
    server: gmail
    tools: [search_threads, get_thread, create_draft, label_thread]
    unattended: [create_draft]      # label_thread still waits for a person
```

- `tools` is an allow-list. When it is left out, every tool listed when the
  run starts is offered.
- **Write tools wait for a person by default.** Every tool that is not
  read-only needs a person's approval before each call. `unattended` names
  write tools that run without one. Read-only tools always run without one.
- **Read-only, by server.** On the built-in presets, a tool is read-only when
  its MCP annotation says `readOnlyHint: true`. Those servers are run by the
  provider itself, so Dagu trusts their annotations. The MCP spec calls
  annotations from other servers untrusted, so on an `mcp` connection every
  tool counts as a write tool unless it is listed in `unattended`.
- **Why tool kind, not content.** The rule depends on what a tool does, not on
  what the agent read. Dagu cannot tell which tool output is untrusted:
  `search_threads` returns email bodies, which is exactly the content that can
  steer an agent. A write that stays inside the account, such as
  `create_draft`, is a good candidate for `unattended`, since a person still
  sends the draft.
- **Google agent flag.** A harness, agent DAG, or chat step that uses a
  Google connection needs an app with `agent` set. Without it the run fails
  at admission. A service account connection needs no flag.

#### Confirmation requests

Google's MCP servers "still return form elicitation prompts for
human-in-the-loop confirmation on sensitive tool calls", service accounts
included. Dagu's MCP clients advertise form elicitation, and handle a
request by the kind of step:

- **Agent DAGs.** The call stays open while a confirmation task shows the
  server's message and form. The run keeps its process and worker slot while
  it waits, because the request is part of a call in flight. The person's
  answer, accept with the form's values, decline, or cancel, goes back to
  the server. After `llm.confirmation_timeout`, 10 minutes by default, Dagu
  declines.
- **Delivering the answer.** A human task today is answered while its run
  has no process. A confirmation needs a path to a live process, and reuses
  the ones that stop a run:
  - a local run receives the answer on its run socket, beside `/stop`
    (`internal/runtime/agent/agent.go:2314`);
  - a run on a worker receives it in the `RunHeartbeat` response, as it
    receives cancellations (`proto/coordinator/v1/coordinator.proto:318`).

  Where the run socket is unavailable, Dagu declines at once.
- **Harness steps, chat steps, and `mcp.call`.** Dagu declines at once,
  since these steps cannot wait mid-call. A harness or chat agent receives a
  tool error that names the rule. `mcp.call` fails with kind
  `confirmation_required`.
- **Try it in the UI.** The person is present, so the form is shown inline.
  The call request returns the form and a call ID. The server holds the call
  open in memory, up to the timeout, and the page posts the answer to
  `POST /connections/{name}/calls/{id}/confirmation`, which returns the
  tool's result.

Dagu never accepts a request on its own, even for a tool in `unattended`.
The request is the provider's own check that a person is present, and
`unattended` waives only Dagu's.

#### MCP gateway

Each AI step that has `mcp_servers` gets its own MCP gateway: the tool
allow-list, pinned tools, approvals, and confirmation handling, in front of
an MCP client. Agent DAGs and chat steps run inside Dagu and call the
gateway in process. A harness step's agent is a separate process, so for it
the gateway also serves MCP on a loopback listener, built on go-sdk's server
as `internal/service/mcp/` is. It is not a byte-level proxy, so sessions,
server-sent events, and protocol versions work as each side expects.

- The listener binds `127.0.0.1` on a random port. It accepts only a random
  bearer token created for the step, and rejects a request whose `Host` or
  `Origin` is not a loopback address, which stops DNS rebinding.
- Upstream calls take tokens from the broker. Long agent turns therefore
  outlive any single access token, and the agent never holds a provider
  token.
- When the step starts, it records each allowed tool's name and a hash of
  its description, input schema, and annotations. A tool the upstream adds
  later is not offered, and a tool whose hash changes fails its calls until
  the next run. This limits tool poisoning by a changed upstream, and keeps
  a tool from becoming read-only mid-run. An agent DAG saves the hashes with
  its catalog, so a run that resumes after a human task keeps them.
- Each tool call is written to the step log with its arguments masked.
- The gateway closes when the step ends.

#### Harness steps

```yaml
- action: harness.run
  with:
    provider: claude
    prompt: Prepare notes for each of today's meetings.
    mcp_servers: [work-google/calendar, work-google/drive]
```

Three adapters point an agent at the gateway:

- **Claude Code:** a temporary `--mcp-config` file with mode `0600`, deleted
  after the step.
- **Codex:** `mcp_servers` config overrides, with the bearer token read from
  an environment variable through `bearer_token_env_var`. The overrides are
  process arguments (`codex exec -c`), so the token stays out of them.
- **ACP agents:** `McpServerHttpInline` entries in `session/new`, sent when
  the agent advertises HTTP MCP support. These replace the empty list at
  `acp.go:243`.

Other built-in providers, and custom ones, refuse `mcp_servers` until they
gain an adapter, as do container and remote ACP steps. Today a harness
option given inline lands in the process arguments (`configToFlags`,
`harness.go:965`), so a token passed that way would be visible in `ps`. No
adapter puts the gateway token in process arguments, because any local user
could read it there and call the gateway while the step runs. The gateway
token is the only credential the agent sees, and it lasts only as long as
the step.

Each adapter also pre-approves the gateway's tools, and only those, since
the gateway already enforces the allow-list and approvals. Without this the
agent's own permission check would block every gateway call:

- `claude -p` runs with no permission flags today
  (`internal/runtime/builtin/harness/provider.go:70`), and print mode denies
  an MCP tool that no rule allows. The adapter passes `--allowedTools` for
  the gateway server's tools.
- An ACP agent's permission request is denied unless `acp_permission:
  allow`, which accepts every tool, shell included (`acp.go:358`). A request
  for a gateway tool is accepted whatever `acp_permission` says. Other
  requests keep the policy.
- Codex gets the same pre-approval through its config overrides, where it
  asks for one.

The agent's other tools keep the step's permission settings.

A harness agent cannot pause in the middle of a turn to wait for a person.
So in a harness step, the gateway refuses a call to a write tool that is not
in `unattended`. The agent receives a tool error that names the rule, and
the step log records the refused call. Such a step can still succeed, so
the refusals are made visible:

- The editor warns when a harness or chat step's `mcp_servers` offers a
  write tool that is not in `unattended`, since the step can never call it.
  The warning reads the cached tool list and its annotations.
- The step records how many calls the gateway refused, and the run page
  shows the count beside the step's status.

#### Agent DAGs and chat steps

```yaml
type: agent
mcp_servers:
  - connection: work-google
    server: gmail
    tools: [search_threads, get_thread, create_draft, label_thread]
    unattended: [create_draft]
steps:
  - name: escalate
    action: dag.run
    with: { dag: open-incident }
tasks:
  - name: triaged
    description: Every unread support email has a draft reply or an incident.
```

- When the run starts, the agent loop lists each server's tools through the
  gateway. Each MCP tool becomes one function tool named
  `<connection>__<server>__<tool>`, or `<connection>__<tool>` for an `mcp`
  connection, so two accounts with the same service never collide. The name
  is sanitized and kept within the 64-character limit like step tool names,
  and the tool's input schema becomes its parameters. The catalog is saved
  with the `__agent__` node, as Spec 032 already requires, so a run's tools
  stay fixed while it runs.
- A call runs through the gateway, inside the agent loop. The result is
  added to the transcript under the observation limits that apply to step
  results.
- A write tool that is not in `unattended` opens an approval task before
  the call. It is a synthesized human task like `ask_user`, but `ask_user`'s
  rules do not apply to it (`specs/032-agent-dag.md:169`). It does not count
  toward the five-question limit.
  - The task shows the tool, its arguments, and any text the agent wrote
    with the call, so a person can judge why the agent wants it.
  - The person approves once, approves the tool for the rest of the run, or
    rejects. Approving for the rest of the run covers that tool only, is
    recorded on the run, and ends with it. Otherwise an earlier decision
    never answers a later call, even an identical one. Without this, an
    agent that labels 30 emails would wait for 30 approvals in turn, and
    authors would list every tool in `unattended` to avoid them.
  - The run waits without a process, and the existing `dag.run.waiting`
    event tells someone.
  - The run continues once a person decides. A rejection goes back to the
    model as the tool result.
- A child run cannot open a human task, as Spec 032 rules for `ask_user`. In
  a child agent DAG, a write tool that is not in `unattended` is refused, as
  in harness steps.
- Step `approval` cannot serve here. It waits after the step runs
  (`internal/ir/step.go:132`), and it keeps a DAG off workers
  (`internal/ir/dag.go:489`).
- Chat steps take the same field under `llm.mcp_servers`. The tool registry
  in `internal/runtime/builtin/chat/tools.go` gains MCP tools beside child
  DAGs. A chat step's tool loop cannot pause either, so it refuses write
  tools that are not in `unattended`, as harness steps do.
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
- Google requires a separate OAuth client for non-agent use. A connection
  uses one app, so the wizard offers a second connection, with its own app,
  for REST automation and mail. An `http.request` or mail step that uses an
  agent connection gets a lint warning.

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

### Dagu Cloud

Dagu Cloud's part has three layers. This RFC ships the first and reserves
room for the other two.

| Layer | Client owned by | Tokens kept by | In this RFC |
| --- | --- | --- | --- |
| Cloud relay | The user | Each server | Ships, as a redirect mode |
| Hosted app | Dagu | Each server | Reserved: app `kind: hosted` |
| Cloud-held connection | The user or Dagu | Dagu Cloud | Reserved: connection `holder: cloud` |

**Cloud-held connections** suit a team that runs several servers. Today each
server connects on its own, so a team with five servers consents five times,
reconnects five times, and holds five refresh tokens per account. If Dagu
Cloud kept the connection instead:

- one consent, one reconnect, and one notification would serve every
  server;
- an account would hold one refresh token per client, well under Google's
  limit of 100 per account and client;
- Dagu Cloud would be the one refresh owner, which removes the stale reads
  of shared storage ([Open questions](#open-questions), item 7);
- no server would hold a refresh token at all.

On a server, such a connection would appear in the list read-only, with its
name and binding from Dagu Cloud. A worker would still ask its coordinator,
and the coordinator would ask Dagu Cloud through the broker's third source.
Dagu Cloud would grant the connection to servers, and each server's admin
would map it to workspaces, as for a local connection. A cloud connection
whose name a local connection already uses would not be offered on that
server until one is renamed. Status and notifications would come from Dagu
Cloud once, not from each server, though each run would still record its
failure cause.

It breaks two of RFC 002's rules. Dagu Cloud would store refresh tokens for
users' accounts, a valuable target that also gives Dagu Cloud the ability to
read their data. And runs would depend on Dagu Cloud once their cached
access token runs out, within about an hour. So it needs its own RFC that
amends RFC 002, makes each cloud connection opt-in, and states the security
review that holding users' tokens calls for. A hosted Google app with Gmail
scopes also needs Google's verification and a yearly security assessment.

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
dagu connection call NAME --server gmail --tool search_threads --args '{"query":"is:unread"}' [--yes]
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
  `--no-browser`, it prints the URL, and in paste mode asks for the pasted
  redirect URL.
- `import` moves an existing `mail_accounts` refresh token into a
  connection, with no new consent. Its output says to replace the `oauth`
  block with `connection:`, since the copy in YAML goes stale at the first
  rotation.

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
| `POST /connections/{name}/authorization` | Start a connect or reconnect. Takes the redirect mode, the loopback redirect URI in CLI loopback mode, and optionally the expected account. Returns the authorization URL and a session handle |
| `GET /connections/{name}/authorization` | Poll: `waiting`, `confirm` (with the account), `connected`, or `failed` |
| `POST /connections/{name}/authorization/complete` | Paste mode and CLI loopback: the code or the redirect URL |
| `POST /connections/{name}/authorization/confirm` | Confirm the account. Only the user who started the session may |
| `DELETE /connections/{name}/authorization` | Cancel |
| `POST /connections/{name}/test` | Probe each service |
| `GET /connections/{name}/tools?server=` | MCP tools with their input schemas, for the UI and editor |
| `POST /connections/{name}/tools/{tool}/call` | Try a tool with arguments. Returns the result, or a confirmation form and a call ID |
| `POST /connections/{name}/calls/{id}/confirmation` | Answer a confirmation request for a call in flight. Returns the result |
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
2. **Agents.** "Who will use this connection?" with three answers: AI
   agents, other automation and mail, or both. The step says that Google
   requires separate clients for agents and for other automation. **Both**
   makes a pair from one pass through the next step, which then asks for two
   clients, one with the agent flag. The pair is named `<name>-agent` and
   `<name>`, and each connection's page links to the other.
3. **OAuth app.** The user picks an existing app, or follows a checklist:
   - **Enable APIs:** a button opens `console.cloud.google.com/flows/enableapi`
     with the APIs for the chosen services filled in.
   - **Consent screen:** a link, with a warning that an app in Testing
     status must be reconnected every seven days and accepts only the
     accounts listed as test users. Publishing to Production, or making the
     app Internal, avoids both. Internal needs a Workspace organization. An
     app published without Google's verification shows "Google hasn't
     verified this app" on the consent screen. The step shows that screen
     and says it is expected for the user's own app.
   - **Create OAuth client:** a link, the client type to choose, the
     redirect URI with a copy button, and, for agents, the checkbox to tick.
   - **Upload:** a drop zone for the downloaded `client_secret_*.json`. Dagu
     reads the client, checks its type, and checks that the redirect URI is
     registered.
4. **Consent.** A popup, opened before any request so that browsers do not
   block it, as `ui/src/pages/license/useLicenseConnect.ts` does. The page
   polls until the popup's callback completes. A blocked popup falls back to
   a link. In paste mode, the page says before opening the popup that the
   browser will end on an error page, and that its address is what to copy.
   It then shows a field for that address.
5. **Done.** The account to confirm, granted scopes, a result per service,
   and two buttons: **Start from a template** and **Browse tools**.

The connection page shows status and the reason for it, the account,
services and scopes, the workspace allow-list, and Reconnect, Test, and
Remove. It also shows:

- **Used by:** the DAGs that name the connection, read from the DAG index.
  Remove, a reconnect with a different account, and a change that drops a
  workspace from the allow-list each show this list before they apply.
- **Add service:** runs the wizard's Services, OAuth app, and Consent steps
  again for the new service only. The OAuth app step shows just the APIs to
  enable for it, and Consent reconnects with the added scopes.

It also has a tool browser:

- a list of each MCP service's tools;
- a form built from each tool's input schema;
- **Try it**, which shows the result. A tool that is not read-only acts on
  the real account, so Try it asks first. `dagu connection call` asks the
  same on a terminal, unless `--yes` is given;
- **Copy as step**, which copies an `mcp.call` step in YAML.

Elsewhere in the UI:

- **DAG editor.** `connection:` completes connection names. It uses a
  `connectionName` definition in `schemas/dag.schema.json` and the mechanism
  that completes calendar names (`ui/src/lib/calendarNameExamples.ts`,
  `ui/src/features/dags/components/dag-editor/DAGSpec.tsx`). `server`
  completes from the connection's presets. `tool` and argument keys complete
  from `GET /connections/{name}/tools`. Completion lists the connections
  that fit the field: agent connections for `mcp_servers` on AI steps, and
  the others for `mail_accounts` and `http.request`. The server also reports
  unknown connections and connections the DAG's workspace may not use. For
  a user allowed to change the connection, the second offers to add the
  DAG's workspace to the allow-list.
- **Run page.** A step that used a connection shows its name and account. A
  step that failed with `reconnect_required` shows **Reconnect** next to its
  error.
- **Navigation.** The Connections item shows a badge while any connection
  needs reconnecting, or has a refresh token that expires within two days.
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

### Quotas in the docs

The docs state no free-tier number. A third-party write-up reports 5,000 MCP
requests a day per project and product, but Google's own pages give none,
and the tiers are new. Instead, the docs:

- link Google's quota page, and the Workspace API usage dashboard, which
  Google says shows each project's usage against the free tier's limits,
  with estimated costs;
- state only what Google has announced:
  - the free tier has published limits;
  - paid usage tier billing starts on 2027-04-01;
  - projects marked internal keep higher limits within the Workspace
    subscription;
- show how to estimate usage. A DAG that calls one Gmail tool every minute
  makes about 1,440 requests a day. Each agent turn makes one request per
  tool call, and each AI step lists each server's tools once when it starts;
- explain the `rate_limited` error kind, so a user who reaches a limit
  recognizes it.

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

A service account key is an app, so adding a service account connection
needs `admin`. A manager's `mcp` connect may register a client through RFC
7591. The resulting `dcr` app belongs to that connection alone: it is created
and deleted with it, and no other connection may use it.

These follow the secret store, where managers manage a workspace's secrets
(`requireSecretManageForWorkspace`, `secrets.go:383`). OAuth apps follow
notification channels, which stay server-wide and admin-only under RFC 003.
`.global` is checked as the secret store checks it.

RFC 003 replaces workspaces with projects in v3.0. Its migration rewrites
each allow-list entry to the project made from that workspace, and
`.global` to the default project, so RFC 003's migration gains connections.
After that, allow-lists name projects by ID, so renaming a project keeps its
connections.

## Security

- **Secrets at rest.** Refresh tokens, access tokens, and client secrets are
  encrypted at rest with the server's key, in directories readable only by
  the Dagu user.
- **Audience binding.** A token goes only to its connection's bound hosts or
  resource, over HTTPS, and never follows a redirect off them. A worker
  enforces the binding it receives from the coordinator.
- **Allow-list enforcement.** It is checked twice: at run admission, and by
  the coordinator for each token. In local mode the run process can read
  the encryption key, so the check there is a policy, as
  [Guarantees](#guarantees) states.
- **Service accounts.** The admin fixes the impersonated user. No step,
  parameter, or tool argument can change it.
- **The connect flow.**
  - Every request carries an encrypted state and PKCE.
  - A callback is single-use and expires.
  - Only the user who started a session confirms it, in the UI or CLI that
    started it. Tokens go live only on confirmation.
  - The callback page shows no token, and closes the popup.
  - In Cloud relay mode, Dagu Cloud holds only the code, until the server
    fetches it. Without the server's PKCE verifier and client secret, the
    code is useless, and a forged callback fails the server's state check.
- **Agents.** Agents never hold provider tokens. The gateway accepts only
  its step's bearer token, offers only allowed tools, and refuses calls to
  tools whose description, schema, or annotations changed.
- **Prompt injection.** An agent that reads email or documents can be steered
  by their content. So write tools wait for a person by default, and only
  the DAG author can exempt a tool, by name, in `unattended`. Annotations from
  servers other than the built-in presets are not trusted. Dagu never accepts
  a provider's confirmation request on its own.
- **Masking.** Tokens are masked in local logs, status, and outputs.
  Rollout step 0 extends masking to log streams from workers, which are not
  masked today (`internal/runtime/output.go:484`). Token export waits for
  that fix.
- **SSRF.** Managers and admins set `oauth2` endpoints and `mcp` server URLs,
  but an `mcp` server's metadata names its authorization server, token, and
  registration URLs. Every URL Dagu fetches, and every redirect it follows,
  must be HTTPS. Plain HTTP to a loopback address, for local testing, is
  allowed only for a URL an admin enters. Authorization server metadata is
  accepted only when its `issuer` matches the URL it came from (RFC 8414).
- **Audit.** These are written to the audit log:
  - app and connection changes;
  - connects, reconnects, and removals;
  - status changes;
  - tool calls from the UI.

  Token issuance is not audited, since it happens once an hour per
  connection.

## Failure behavior

| Event | Behavior |
| --- | --- |
| The user denies consent or closes the popup | The session fails or expires. The connection stays `pending`, or keeps its previous tokens on a reconnect |
| Google returns a consent error, such as a missing test user or a Workspace admin's block | The session fails, and the UI or CLI shows the fix from [Consent errors](#connect-flow) |
| Dagu Cloud is unreachable during a Cloud relay connect | The session fails, saying so, and offers the next mode. Runs and existing connections are unaffected |
| Dagu Cloud lacks the relay endpoints | The server does not offer Cloud relay, and picks the next mode |
| The callback arrives after 10 minutes, twice, or for a session that a newer one replaced | Refused. Nothing changes |
| Another user tries to confirm the account | Refused. The session stays in `confirm` |
| Nobody confirms the account | The session expires. The previous tokens stay live |
| The user unticks a scope | The connection is created. Services missing a scope show it, and steps that need it fail with `insufficient_scope` |
| A Google MCP probe is rejected | The service shows the agent flag hint and a link. Other services are unaffected |
| The refresh token is revoked or expires | `needs_reconnect` once, one `connection.needs_attention` notification, a badge in the UI, and every step that uses the connection fails with `reconnect_required` and the connection's name. Channels that receive the connection event skip those runs' failure messages |
| A refresh token from an app in Testing status nears its seven-day expiry | The Connections page shows the time left, and the badge appears two days before. No notification is sent until it expires |
| A refresh fails with `invalid_grant` because another host on shared storage rotated the token | The re-read finds the new refresh token and the refresh is retried once. Status is unchanged |
| The provider answers HTTP 429 | The step fails with kind `rate_limited` and the provider's message. Status is unchanged |
| The OAuth app's secret is rotated or deleted at the provider | The app becomes `invalid`, and its connections fail at once without calling the provider. Updating the app restores them |
| The token endpoint is down | Steps fail as transient, and retry under their own policy. Status is unchanged |
| A reconnect is confirmed while a refresh is running | The refresh result is dropped. Both processes end with the reconnected token |
| A connect session starts while a refresh is running | The refresh's write conflicts, re-reads, and keeps both the session and any rotated refresh token |
| A connection is removed while a refresh is running | The refresh result is dropped and the call fails. The connection stays removed |
| Two processes refresh at once | One holds the lock. The other waits, reads the new token, and does not refresh |
| One provider's token endpoint hangs | Only that connection's refreshes wait, up to the token call's timeout. Other connections are unaffected |
| A service account key is deleted at Google | Its app becomes `invalid`, and its connections fail at once without calling Google. Uploading a new key restores them |
| A DAG names an unknown connection, or one its workspace may not use | The editor reports it. A run fails before its first step, naming the connection |
| A worker's coordinator is older and lacks the token RPC | The step fails, saying the coordinator must be upgraded |
| A worker is older than the token RPC | The coordinator does not dispatch a DAG that declares connections to it |
| A server sends a confirmation request | An agent DAG shows it to a person and waits up to the timeout. Other steps decline it, and the call fails |
| An upstream MCP tool's description changes during a run | Calls to that tool fail until the next run, which records the new description |
| A harness step in a container sets `mcp_servers` | Validation fails, naming the limit |
| A harness or chat step calls a write tool that is not in `unattended` | The gateway refuses the call. The model receives a tool error that names the rule, and the step continues. The run page shows the refused count |
| A harness agent's own permission policy would deny a gateway tool | The adapter's pre-approval lets the call through. The gateway's rules still apply |
| Nobody answers a write tool's approval in an agent DAG | The run stays waiting, as for any human task |

## Performance

- **Idle cost.** None. No ticker, watcher, or scan is added. Tokens refresh
  only when a step needs one.
- **Refresh rate.** At most once per connection and resource per token
  lifetime on a host, about one an hour, regardless of the number of runs.
  Today mail refreshes once per run, so a mail DAG that runs every minute
  drops from 60 refreshes an hour to one. Token export shortens this to
  about every 10 minutes for a Google connection, while runs keep starting
  that export it.
- **Refresh lock.** One lock per connection. Refreshes of different
  connections never wait for each other, and a slow provider delays only its
  own connections.
- **Per run.** A run that uses a connection checks it once at admission, and
  reads its token state at most once per token lifetime: small file reads
  locally, or two coordinator RPCs on a worker. Steps and `foreach` items
  share the run's cache, so the cost grows with runs that use connections,
  not with steps. At one run per second, that is at most two reads or RPCs
  per second for each connection a run uses.
- **Cloud relay.** One request to Dagu Cloud every 3 seconds per waiting
  session, for at most the session's 10 minutes. It stops when the session
  completes, fails, or is cancelled, and nothing polls outside a connect.
- **Requests.** Connection lists read one small record per connection. A
  server holds tens of connections, not thousands, and no request reads run
  or DAG directories.
- **Tool lists.** Cached in server memory for the UI and editor: 10 minutes,
  bounded in size, and fetched once per connection and server whatever the
  number of open tabs. Each AI step lists each server's tools once when it
  starts: one `tools/list` per server per run, counted against the
  provider's quota. Neither path polls.
- **Gateway.** One loopback listener per harness step with `mcp_servers`,
  tied to the step's context. It adds one local hop per tool call. Agent
  DAGs and chat steps add no listener.
- **Confirmation waits.** An agent DAG run that waits on a confirmation
  request holds its process and worker slot, for at most
  `llm.confirmation_timeout`. Approval tasks before a call release both.
  The answer arrives on the run socket or in the existing run heartbeat, so
  the wait adds no polling. Try it holds one call in server memory per open
  confirmation, for at most the same timeout.
- **Notifications.** One event per status change, not per failed run. A
  connection that stays broken while 1,000 runs fail sends one message. The
  monitor's reader for `connection` events runs on its existing poll interval
  (`internal/service/chatbridge/monitor.go:403`). It adds one cursor read of
  the event store per tick, which returns nothing between status changes.
  To skip run failures caused by a broken connection, the monitor keeps the
  set of broken connections in memory, bounded by the number of
  connections, and rebuilds it from the connection records when it starts.
- **Storage.** About 2 KB per connection, plus about 1 KB per extra resource
  of a Microsoft connection, and 1 KB per app. An agent DAG run with
  `mcp_servers` saves its MCP tools in the `__agent__` catalog: names,
  descriptions, input schemas, and hashes, roughly 1 to 2 KB per tool, so 10
  to 40 KB for two Google servers. A `tools` allow-list shrinks it. A step
  that uses a connection adds about 100 bytes to its node status: the
  connection's name and account, and for harness and chat steps the refused
  count. A run that fails on a broken connection adds about 70 bytes for its
  cause. The DAG index adds the connection names of each DAG that uses one,
  tens of bytes per entry. Audit grows only with management actions and tool
  calls from the UI.
- **Upgrade.** No migration. The DAG index version goes up, so the first
  start rebuilds the index once, reading each DAG file as a rebuild does
  today. RFC 003's v3.0 migration rewrites allow-lists, as
  [Permissions](#permissions) describes.

## Compatibility and rollout

- **Additive.** A DAG without connections behaves as before.
  `mail_accounts.oauth`, SMTP OAuth, and static `headers` keep working.
- **Older binaries.** A step with `connection` or `server`, or a DAG with
  `mcp_servers`, fails validation on an older binary, which rejects unknown
  keys there. An older binary silently ignores `connection` in
  `mail_accounts` (`internal/spec/mail_accounts.go:110`), so that account
  has no credentials and its mail steps fail. An older binary ignores the
  new collections and the new run status fields, so a downgrade drops the
  feature and keeps the rest working. An older binary finds a DAG index of
  a newer version and rebuilds its own, as on any version mismatch
  (`internal/persis/file/dag/dagindex/dagindex.go:94`).
- **Version skew in distributed mode.** A new worker under an old
  coordinator fails only the steps that use connections, as [Failure
  behavior](#failure-behavior) states. An old worker parses the DAG
  definition itself (`internal/service/worker/remote_handler.go:463`), so it
  would reject the new fields or call without a token. Workers report their
  Dagu version (`proto/coordinator/v1/coordinator.proto:233`), and the
  coordinator dispatches a DAG that declares connections only to workers new
  enough for the token RPC.
- **API.** New paths only. Existing schemas are unchanged.
- **Dagu Cloud.** Cloud relay needs two new Dagu Cloud endpoints: register
  a callback, and fetch it. A server checks for them and offers the mode
  only when they exist, so either side can upgrade first.
- **`mcp.call` errors.** HTTP 429 moves from kind `transport` to the new
  kind `rate_limited`. A DAG that branches on `transport` to catch rate
  limits must check `rate_limited` too. A declined confirmation request has
  the new kind `confirmation_required`.
- **Notifications.** The two `connection.*` events join the default event
  set for new channels only. Existing channels keep their event lists, and
  must opt in. Until they do, they keep receiving every run failure, since
  only a channel that receives connection events skips the failures they
  cause.
- **Specs.** 098 (`mcp.call`), 045 (`http.request`), 073 (mail), 032 (agent
  DAGs), 059 (chat), 067 (harness), and 069 (secret providers) gain the new
  fields. A new spec defines connections, the broker, and the connect flow.

Rollout, in pull requests that each work on their own. Part A, steps 0 to
6, ships before Google's enforcement on 2027-02-01. Part B, steps 7 and 8,
ships after confirmation requests are tested against Google's servers
([Open questions](#open-questions)).

0. **Mask worker log streams.** `setupRemoteWriters` writes step output to
   the coordinator without `MaskingWriter`. This is an existing bug that
   already exposes `secrets` values, fixed first and separately.
1. **Records and broker.**
   - The three collections, providers, and the broker with locking,
     generation checks, per-resource tokens, the re-read on
     `invalid_grant`, and saving rotated tokens.
   - The `connection` event kind, its two events, and the notification
     monitor's reader for them.
   - `dagu connection import`, plus `add` for service accounts and client
     credentials, which need no consent screen.
   - `mail_accounts.*.connection`, with the Microsoft `mailbox` preset. This
     alone fixes Microsoft rotation for mail.
2. **Connect flows.** All five redirect modes, Cloud relay once Dagu Cloud
   serves its endpoints, sessions and confirmation,
   the REST API, the Connections page, the Google wizard with paired
   connections and consent error fixes, the expiry display,
   `dagu connection add`, test, and reconnect.
3. **Steps.**
   - `ir.DAG.Connections`, admission checks, and the coordinator RPC with
     the binding, `check_only`, and the worker version check.
   - `http.request` with bound hosts.
   - `mcp.call` with connections, the Google presets, and the
     `rate_limited` and `confirmation_required` kinds.
   - Run records of the connection, account, and failure cause, and
     skipping the run failure messages a broken connection causes.
4. **Any MCP server.** Discovery, registration, and the gallery tile.
5. **Authoring.** Editor completion and the workspace quick fix, the tool
   browser and Try it with inline confirmation, the Used by list from the
   DAG index, Add service, templates, `dagu_read`, and the docs.
6. **Token export.** The `connection` secret provider, behind
   `allow_token_export`.
7. **Gateway and harness steps.** Claude Code, Codex, and ACP adapters with
   pre-approved gateway tools, `unattended`, pinned tools, and declining
   confirmation requests. Write tools not listed in `unattended` are
   refused, with the editor warning and the refused count.
8. **Agent DAGs and chat steps.** MCP tools, approval tasks before write
   tools with approval for the rest of a run, confirmation tasks with their
   delivery over the run socket and run heartbeat, and the Google policy
   lint.

## Verification and acceptance criteria

1. On a fresh server with a public URL, a user follows the Google wizard,
   uploads a client JSON, consents, and lists Gmail tools without leaving
   the UI except for the Google Cloud console and the consent screen.
2. `dagu connection add google` works in each mode: in a desktop session
   (loopback), over SSH to a server with a public URL (server callback), over
   SSH to a licensed server without one (Cloud relay), and over SSH to an
   unlicensed server without one (paste).
3. An `mcp.call` step with `connection` and `server: gmail` succeeds, both
   locally and on a worker with no shared filesystem.
4. With 100 concurrent runs using one connection whose access token has
   expired, the provider receives exactly one refresh request.
5. A rotated refresh token is saved, and the next refresh uses it, across
   process restarts.
6. Revoking access at the provider makes the connection `needs_reconnect`
   once. Every later step fails without calling the token endpoint.
   Reconnect restores it.
7. A reconnect confirmed during a refresh leaves the reconnected token in
   place. Runs keep the previous token until the reconnect is confirmed.
8. No refresh token, access token, or client secret appears in any API
   response, CLI output, MCP result, run status, step log, worker log
   stream, or audit entry. A test searches each of them for the values.
9. `http.request` with a connection, locally and on a worker, never sends
   the token to a host outside the bound hosts or over plain HTTP, including
   through a redirect.
10. A run on a worker cannot obtain a token for a connection its DAG does not
    declare, or one whose allow-list excludes its workspace.
11. A harness step with `mcp_servers` completes a turn longer than the
    access token's lifetime. The agent process's arguments contain neither a
    provider token nor the gateway token, and its environment contains no
    provider token.
12. An agent DAG calls an MCP tool with arguments. A write tool not in
    `unattended` waits for a person before each call, including after five
    such calls, and runs only after approval, unless a person approved it
    for the rest of the run. A harness step's or a child
    agent DAG's call to such a tool is refused.
13. A Gmail read-only tool runs unattended. Every tool of a generic `mcp`
    connection waits for a person unless `unattended` lists it.
14. A revoked connection that 100 runs then use sends exactly one
    `connection.needs_attention` notification, and a reconnect sends one
    `connection.restored`.
15. Two hosts sharing the data directory both keep working after one of them
    rotates a Microsoft refresh token.
16. Each role can do what [Permissions](#permissions) lists and nothing
    more.
17. One Microsoft connection serves a Graph `http.request` and an IMAP mail
    action in the same run, each with a token for its own resource.
18. A Google tool that sends a confirmation request waits for a person in an
    agent DAG, and fails with `confirmation_required` in `mcp.call`. No path
    accepts a request without a person.
19. In server-callback mode started from the CLI over SSH, a browser with no
    Dagu session completes consent, and only the user who started the
    session can confirm it.
20. A connect session written during a Microsoft refresh keeps the rotated
    refresh token. A connection removed during a refresh stays removed, and
    a new connection with its name starts without tokens.
21. A token endpoint that hangs for one connection does not delay another
    connection's refresh.
22. An agent DAG's confirmation answer reaches the waiting call both in a
    local run and on a worker.
23. A harness step with the step's default permission settings calls a
    gateway tool through Claude Code, Codex, and an ACP agent.
24. While 100 runs fail on a broken connection, a channel that receives
    connection events sends one message and no run failure messages. A
    channel that does not receive them sends every run failure.
25. An agent DAG approves a write tool for the rest of the run. Later calls
    to that tool run without a task, and other write tools still wait.
26. Each consent error in [Consent errors](#connect-flow) shows its fix.
27. A licensed server without a public URL connects a Google account
    through the Cloud relay, from the UI and from the CLI over SSH. Dagu
    Cloud's stored data then holds no token, PKCE verifier, or client
    secret, and the code is gone once the server fetched it.
28. Every case in [Failure behavior](#failure-behavior) ends as stated.

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
- **A separate session store for the connect flow.** Needs a store shared
  by frontend replicas. The token state record already is one, and the
  encrypted state carries the PKCE verifier.
- **Per-project connections only.** Simpler scoping. But one Google account
  often serves several teams, and RFC 003 keeps credential-holding services
  such as notification channels server-wide.
- **A Dagu-hosted Google app now.** One-click connect, without Google Cloud
  setup. But Gmail scopes are restricted, which takes Google verification
  and a yearly security assessment, and the app would depend on Dagu Cloud.
  The `hosted` app kind leaves room for it.
- **Dagu Cloud keeping every connection now.** One consent for every server,
  and one refresh owner. But it breaks RFC 002's rules against storing
  secrets in Dagu Cloud and making runs depend on it. The Cloud relay gives
  servers without a public URL the fixed callback, which is most of the
  connect-time benefit, without storing anything. `holder: cloud` leaves
  room for the rest.
- **Opt-in confirmation (`confirm: [tool]`).** The safe setting would depend
  on the author remembering it, and a scheduled agent that reads email would
  run every write unattended by default.
- **Requiring confirmation only when the agent reads untrusted content.**
  Dagu cannot tell which tool output is untrusted. Read tools return the
  email bodies and documents that carry injected instructions, so the rule
  could not be enforced.
- **Reusing `ask_user` for approvals.** No new task. But its five-question
  limit stalls an agent that writes more than five times, and its repeat
  rule would approve an identical second write without asking.
- **Approving each call only.** Simpler. But an agent that writes 30 times
  waits for 30 approvals in turn, which pushes authors to list every tool in
  `unattended` and lose the default.
- **Accepting confirmation requests for tools in `unattended`.** Harness and
  chat steps could then use Google's sensitive tools. But the request is
  Google's own check that a person is present, and answering it without one
  defeats that check.
- **One refresh owner for hosts that share storage,** such as the scheduler
  lock holder. It would remove stale reads across hosts, but every other
  host would need an RPC to it, and the owner a failover path. The re-read
  guard covers stale reads, and the docs cover providers that detect reuse.
  [Open questions](#open-questions) keeps it under review.
- **Naming the concept "accounts", "integrations", or "credentials".**
  "Accounts" clashes with `mail_accounts`, and does not fit client
  credentials or MCP servers. "Integrations" is broader than a sign-in.
  "Credentials" sounds like the secret store. "Connections" matches other
  workflow tools. SSH node connections and the Dagu Cloud connection keep
  their names.

## Open questions

1. Should an agent DAG list its tools when it is loaded, which needs network
   access during validation, or only when it runs, as this RFC proposes?
2. May any manager reconnect a connection with a different account, or only
   when the account stays the same?
3. Should a second connection for the same account and client be refused,
   rather than warned about?
4. Should a later Dagu-hosted app use Client ID Metadata Documents, which
   newer MCP authorization specs describe, rather than registration? This
   needs checking against the current MCP spec.
5. Does Microsoft keep a rotated-out refresh token valid until it expires?
   The re-read guard works either way, but the docs should state the
   provider's behavior only once it is confirmed.
6. Which Google tools send confirmation requests, and does Google refuse a
   call, or skip the check, from a client that does not advertise
   elicitation? The answer decides how much of Part B works in harness and
   chat steps, and needs testing against Google before Part B ships.
7. Should hosts that share storage pick one refresh owner, for providers
   that revoke a token family on reuse? Cloud-held connections would be one
   answer, for teams that accept them.
8. Do Google's Workspace MCP servers serve personal `@gmail.com` accounts,
   or only Workspace accounts? The wizard should say so before the user
   starts the Google Cloud setup.
9. Do ACP agents name the MCP server in a permission request? The adapter
   accepts only gateway tools, so an agent that does not name it falls back
   to `acp_permission` for every MCP call. This needs checking against each
   agent before Part B ships.

## Future enhancements

- **Cloud-held connections and a Dagu-hosted OAuth app,** as [Dagu
  Cloud](#dagu-cloud) describes. Both need their own RFC, and cloud-held
  connections amend RFC 002.
- **Confirmation requests in harness and chat steps.** The step would hold
  the call open while a person answers, as agent DAGs do.
- **Harness steps in containers.** The step would bind-mount a Unix socket
  for the gateway and run a stdio bridge inside the container. It would also
  work with `network: none`.
- **Personal connections,** with a run-as-user model.
- **Approval timeouts.** A write tool's approval would be rejected after a
  set time. Human tasks have no timeout today, and a run that waits without
  a process needs a scheduler check to enforce one.
- **Expiry notifications.** A message before a refresh token expires. It
  needs a periodic check, which this RFC avoids for idle cost.
- **More presets,** such as Slack, GitHub, and Salesforce, as their MCP
  servers settle.

## References

- Google Workspace Updates, "Simplifying agent access while keeping
  organizations in control" (2026-11-24):
  https://workspaceupdates.googleblog.com/2026/11/mcp-servers-cli-standardized-usage-tiers-now-available.html
- Configure the Google Workspace MCP servers, still marked Developer Preview
  on 2026-11-25 despite the GA announcement:
  https://developers.google.com/workspace/guides/configure-mcp-servers
- Configure OAuth clients for AI agents:
  https://support.google.com/cloud/answer/15549257
- MCP authorization: https://modelcontextprotocol.io/specification/latest/basic/authorization
- RFC 7591 (dynamic client registration), RFC 8414 (authorization server
  metadata), RFC 8707 (resource indicators), RFC 9728 (protected resource
  metadata)
