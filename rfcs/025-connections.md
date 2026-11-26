# RFC 025: Connections

- **Status:** Proposed
- **Date:** 2026-11-25
- **Affected areas:** a new `connection` package (records, providers, connect
  flow, token broker), `cmn/mailer` (refresh code moves out, SMTP takes a
  connection), `spec` and `ir` (`connection` fields, declared connections),
  `runtime` (masking), `runtime/builtin` (`mcp`, `http`, `mail`),
  `secret/providers` (a `connection` provider), `notification` (SMTP
  settings), `service/coordinator` and `service/worker` (token RPC),
  `service/frontend` (API, OAuth callback), `eventstore` and
  `service/chatbridge` (connection notifications), `license` (Cloud relay
  client), `service/mcp`, `cmd`,
  `api/v1`, `proto`, `ui`, `specs`, `conformance`, `docs`, and Dagu Cloud
  (Cloud relay endpoints)

## Summary

Let a user connect an account once and use it everywhere. A connection is a
named OAuth sign-in: a Google Workspace account, a Microsoft 365 account, any
OAuth2 API, or any remote MCP server. It belongs to one project, or, for an
account that several projects share, to the server, which lends it to the
projects an admin lists. A grant is never copied. The user creates one
from the UI, the CLI, or the REST API, and consents in a browser. Steps,
mail accounts, and email notifications then refer to it by name, and RFC 026
gives AI steps its MCP tools. Dagu
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
                                  v
  <data_dir>/oauth-apps/           client ID, client secret, agent flag, status
             connections/          scope, account, services, added scopes
                                   and hosts
             connection-tokens/    refresh token, access token per resource,
                                   expiry, status, generation, connect session
             connection-catalogs/  MCP tools per connection and service
                                  |
     +--------------+-------------+-------------+------------------+
     v              v                           v                  v
  mcp.call      http.request               mail and SMTP   AI steps (RFC 026)
  server: gmail bound hosts only           connection:     MCP gateway
     |              |                           |                  |
     +--------------+-- Token(conn, resource) --+------------------+
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
  scope for service accounts (`oauth.go:29`). Mail accounts, a DAG's `smtp`,
  and the notification SMTP settings each take their own OAuth block, with
  the refresh token in YAML, base config, or settings.
- **Getting a refresh token is manual.** Users copy a client ID, client
  secret, and refresh token from the OAuth Playground into YAML or base
  config. Spec 073 leaves the consent screen out of scope, and nothing in the
  CLI, UI, or API replaces it.
- **Google's new rules need specific setup.** The OAuth client must carry
  the AI-powered agent flag, or the MCP servers reject it. Google requires
  a separate client for agent and non-agent use. An app left in Testing
  status issues refresh tokens that expire after seven days. Users find each
  of these by failing.

## Goals

1. On a fresh server, an admin connects a Google account from the UI by
   following the wizard alone, without other docs. The UI guides the Google
   Cloud setup, checks it, and turns each setup error into its fix. A
   manager does the same with an app an admin registered.
2. The CLI connects an account in one command, including over SSH with no
   local browser.
3. `mcp.call`, `http.request`, and mail steps refer to a connection by
   name, and RFC 026 adds harness, agent DAG, and chat steps. Google
   requires separate OAuth clients for agents and for other automation, so a
   Google user who needs both holds two connections.
4. Every place that takes mail OAuth today takes a connection: mail
   accounts, a DAG's `smtp`, and the notification SMTP settings. Mail and
   everything else then share one refresh path.
5. No secret appears in YAML, logs, run status, API responses, MCP results,
   or audit entries.
6. Each connection refreshes at most once per token lifetime for each
   resource it serves, however many runs use it, and a rotated refresh token
   is saved. Token export, which needs a longer-lived token, refreshes more
   often, within a fixed bound.
7. An expired or revoked sign-in is reported once, names the connection, and
   offers Reconnect.
8. Connections work in a single process, behind several frontend replicas,
   and for runs on workers that share no filesystem with the server.
9. A connection belongs to one project, or to the server for an account that
   projects share. Sharing is by reference, so one grant has one record and
   one refresh token.
10. DAGs that use no connection behave as before.

## Non-goals

- **A Dagu-hosted OAuth app, and connections held by Dagu Cloud.** The user
  brings their own client in this RFC, and the server keeps the tokens. The
  records reserve both, as [Dagu Cloud](#dagu-cloud) describes. Dagu Cloud
  takes part only in the connect flow, through the Cloud relay.
- **Personal connections and run-as-user identity.** Connections belong to
  projects or to the server. A user's own account, used by runs they start,
  needs its own design.
- **Hiding tokens from DAG authors in local mode.** Steps run as Dagu's OS
  user. Key custody that changes this, for secrets and connections together,
  needs its own RFC. [Guarantees](#guarantees) states the limit.
- **MCP sampling and URL-mode elicitation.** Dagu advertises form
  elicitation only, as [Confirmation requests](#confirmation-requests)
  describes.
- **Inbound OAuth for Dagu's own MCP server.**
- **Creating Google Cloud projects or OAuth clients.** The wizard links to
  the Google Cloud console and checks the result.
- **A second secret store.** Static API keys stay in the secret store.
  Connections hold OAuth credentials only.

## Guarantees

Promised:

- A refresh token or client secret is stored only encrypted. It is never
  returned by the API, CLI, or MCP server, and it never reaches a step's
  process, a step's environment, or a worker.
- An access token is sent only to the hosts the connection is bound to, and
  only over HTTPS, or over TLS with a verified certificate to a bound IMAP or
  SMTP host. It does not follow a redirect to another host or to plain HTTP.
- A run receives a token only for a connection that its DAG declares, and
  only when the connection belongs to the run's project or is a server
  connection that lists it. The notification SMTP settings use only a server
  connection.
- A grant is never copied. A connection that several projects use is one
  record with one refresh token, so a provider that rotates refresh tokens
  never invalidates a copy.
- A refresh result never overwrites a newer reconnect.
- A connection becomes `needs_reconnect`, or an app `invalid`, once per
  failure. Only a confirmed reconnect, or an update to the app, clears it.
  Each such change sends one message to each channel it reaches.
- Dagu never answers a server's confirmation request on its own. Only a
  person accepts one.
- Each connection refreshes each resource at most once per token lifetime
  across all processes on a host, except while runs export its token, as
  [Token broker](#token-broker) bounds. Workers never refresh.

Not promised:

- **Isolation from DAG authors in local mode.** Steps run as Dagu's OS
  user, so a DAG author can read the encryption key and every connection's
  tokens, as they can read secrets today. Scopes, roles, and export settings
  prevent mistakes and govern the API and UI. They do not stop a malicious
  author. Workers are the isolation boundary: a worker never holds the key,
  and receives only the tokens its DAG declares. The wizard says this when a
  connection is created on a server that runs DAGs locally.
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

### Where it runs

Every process that has the data directory runs the token broker. A worker
has neither the data directory nor the encryption key, and asks its
coordinator. Dagu Cloud takes part only while a connect waits, through the
[Cloud relay](#cloud-relay).

```text
  Dagu Cloud (optional, RFC 002)
  Cloud relay: holds a callback's code until the server fetches it
     ^
     | outbound poll, only while a connect waits
 +---+-------------------------------------------------------------------+
 |  hosts that share <data_dir>                                          |
 |                                                                       |
 |  server              scheduler       local run        coordinator     |
 |  API, UI, connect    starts runs     steps            worker RPCs     |
 |  flow, tool browser                                                   |
 |  broker              broker          broker, cache    broker, cache   |
 |     |                   |               |                 |           |
 |     +-------------------+-------+-------+-----------------+           |
 |                                 | lock per connection, CAS writes     |
 |                                 v                                     |
 |  <data_dir>/  oauth-apps/  connections/  connection-tokens/           |
 |               connection-catalogs/        encryption key              |
 +--------------------------------------------------------^--------------+
                                                          |
     ResolveConnectionToken: token, lifetime, binding     | gRPC
     ResolveConnectionTools (RFC 026): tool catalog       |
                                                          |
 worker: no <data_dir>, no encryption key                 |
   run: steps, token cache; never refreshes --------------+
```

### Records

Records in four collections under the data directory, each
created with mode `0o700` as the license store's is
(`internal/persis/file/shared_stores.go:110`). Secret fields are encrypted
with `internal/cmn/crypto`, using the key from `DAGU_ENCRYPTION_KEY` or
`<data_dir>/auth/encryption_key`. Before v3.0 every connection lives here,
keyed by its scope and name. From v3.0, project connections move into their
project's state directory, as [Scopes](#scopes) describes.

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
| `tenant` | Only for `microsoft`: `common`, `organizations`, or a tenant ID. Client credentials need a tenant ID |
| `endpoints` | Only for `oauth2` and `mcp`: authorize, token, revoke, and device URLs |
| `status`, `status_reason` | `ok` or `invalid`, with the provider's error. The broker sets `invalid` with a CAS write when the provider rejects the client. An admin's update to the client ID or secret resets it to `ok` |

**Connection**: one authorized account, stored as two records in two
collections with different writers.

The metadata record (`<data_dir>/connections/`) is written by managers and
admins:

| Field | Notes |
| --- | --- |
| `name` | Lowercase letters, digits, and hyphens, up to 63 characters. Unique among the connections one project can use. See [Scopes](#scopes) |
| `scope` | `project`, with the project it belongs to, or `server`. See [Scopes](#scopes) |
| `provider`, `app` | The app holds the client, or, for a service account, its key. A key is rotated by updating the app |
| `grant` | `authorization_code`, `device_code`, `client_credentials`, or `service_account` |
| `services` | Presets such as `gmail` or `calendar`, each with `read` or `write` |
| `scopes` | Scopes added beyond the services' presets. The presets' own scopes are not stored |
| `account` | The account's name read after consent, such as an email address. Shown to people |
| `account_id` | The provider's immutable ID for the account: Google's `sub`, or Microsoft's tenant and object IDs. Accounts are compared by it, never by `account` |
| `subject` | For a service account, the impersonated user. For Microsoft client credentials, the mailbox that mail and SMTP use. Fixed by the admin |
| `bound_hosts` | Hosts added beyond the services' presets that may receive the token. Required for `oauth2` |
| `resource` | MCP connections: the canonical server URL (RFC 8707) |
| `projects` | Server connections only: the projects that may use it, or `*` for every project, `.global` included. Set by admins |
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
never conflict. Only the `connection` package writes a connection's status:
the broker on a refresh failure, and the connect flow on a confirmed
connect. The broker marks an app `invalid`, and an admin's update to the
app resets it. Requested scopes and bound hosts are derived when read,
from the presets compiled into the binary plus the stored additions. An
upgrade that adds a scope to a preset therefore reaches every connection,
and the service shows the missing scope until the user reconnects. A
refresh asks only for granted scopes, so such an upgrade never breaks a
refresh. Neither
record is stored in the secret store; [Alternatives
considered](#alternatives-considered) gives the reasons.

**Tool catalog** (`<data_dir>/connection-catalogs/`): for each connection
and MCP service, the tools it lists: names, descriptions, input schemas,
annotations, a hash of each, and when it was listed. It holds no secret.
It is the one copy of a tool list: the UI, the editor, and the AI steps of
RFC 026 all read it.

- A reader takes a catalog up to 10 minutes old. An older or missing one is
  listed again with `tools/list`, under a lock per catalog, separate from
  the connection's token lock because listing asks the broker for a token.
- Readers that find the lock held wait for its release and read the new
  catalog, as token readers do. So 100 runs that start together list a
  service once.
- A failed listing returns the provider's error and keeps the previous
  catalog for the next attempt.

### Scopes

A connection belongs to one project, or to the server:

| | Project connection | Server connection |
| --- | --- | --- |
| Belongs to | One project | The server |
| Managed by | Managers of that project | Admins |
| Used by | That project's DAGs | DAGs in the projects it lists, or every project for `*` |
| For | A team's own account | An account that several projects share, and every service account |

This RFC says project for the boundary. Before v3.0 that is a workspace, and
a project connection in `.global` serves DAGs without a workspace label.
Unlike a `.global` secret, which every workspace falls back to
(`internal/secret/resolver.go:82`), a `.global` connection serves no other
workspace.

- **Sharing is by reference.** A server connection is one record with one
  refresh token, however many projects use it. A grant is never copied,
  because each copy refreshes on its own: when a provider rotates refresh
  tokens, the first copy to refresh invalidates the others.
- **Name lookup.** A DAG's `connection: x` resolves to its project's
  connection `x`, or to a server connection `x` that lists its project.
  Creating a project connection, or listing a project on a server
  connection, fails when the name would collide in that project, so neither
  kind ever hides the other.
- **Service accounts and Microsoft client credentials are server
  connections only.** Domain-wide delegation lets a key act as any user, and
  an app permission can reach every mailbox in a tenant, so only an admin
  fixes the `subject`.
- **Moving to the server.** An admin can turn a project connection into a
  server connection that lists its project, keeping its tokens. DAGs keep
  working, since the name does not change. The move takes the connection's
  lock, as Remove does, so a refresh in flight finishes first and its
  rotated refresh token moves too.
- **OAuth apps stay server-wide and admin-only,** as notification channels
  do. A project connection uses an app an admin registered. Registering a
  client is a one-time task, and consent stays with each connection's owner.
  A manager who finds no fitting app is shown what to ask an admin for: the
  client type, the redirect URI, and whether the agent flag is needed.
- **Email notifications.** The notification SMTP settings are server-wide,
  as Spec 102 keeps them, so they use a server connection, which needs no
  `projects` entry for that use. A DAG's `smtp` resolves in the run's
  project, as a step does.
- **Deleting a project.** A project that owns connections, or that a
  server connection lists, cannot be deleted until they are removed or it
  is unlisted, as a workspace with wiki pages cannot today
  (`internal/service/frontend/api/v1/workspaces.go:246`). Before v3.0 a
  name is the key, so a project created later under a deleted one's name
  would otherwise inherit its connections.
- **v3.0.** RFC 003's migration moves each project connection, with its
  token state and catalogs, into its project's state directory, and a
  `.global` one into the default project. A server connection stays
  server-wide, and its `projects` entries become project IDs, so renaming a
  project keeps them. Nothing is copied, unlike global secrets, which the
  migration copies into each project (`rfcs/003-projects.md:737`).

This follows Spec 102: a project owns its connections as it owns its
secrets, and a server connection is admin-owned infrastructure that a
project uses only when listed, as projects use notification channels
(`specs/102-projects.md:402`).

### Providers and services

| Provider | Grants | Notes |
| --- | --- | --- |
| `google` | Authorization code, or a service account with domain-wide delegation | Google rejects MCP calls from a user-delegated client without the agent flag. Service accounts are accepted without it. Google requires separate clients for agent and other use |
| `microsoft` | Authorization code, device code, client credentials | `tenant` is `common`, `organizations`, or a tenant ID. Refresh tokens rotate. Each access token is valid for one resource |
| `oauth2` | Authorization code, client credentials | Endpoints, scopes, and `bound_hosts` are required. PKCE is on unless turned off |
| `mcp` | Authorization code | Discovery and registration follow the MCP authorization spec. See [Any MCP server](#any-mcp-server) |

A service preset names one endpoint of a provider. It gives the MCP URL,
the REST hosts, the IMAP and SMTP hosts for mail, the scope sets for read
and write, and, for Google, the API IDs to enable. The catalog is compiled
into the binary and served by `GET /connection-providers`.

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
non-agent connection. Google also has a `mailbox` preset, with no MCP
endpoint, for IMAP and SMTP at `imap.gmail.com` and `smtp.gmail.com`. Those
need the full `https://mail.google.com/` scope, which `gmail` does not ask
for.

Microsoft presets are `mail`, `calendar`, and `files`, against Graph
(`graph.microsoft.com`), and `mailbox`, against Outlook
(`outlook.office.com`) for the IMAP and SMTP access that mail accounts and
SMTP use (`internal/cmn/mailer/oauth/oauth.go:31`). Its token goes to the
IMAP and SMTP hosts, `outlook.office365.com` and `smtp.office365.com`.
Delegated and client credentials grants ask for different `mailbox`
scopes. Client credentials also need the app granted access to the
`subject` mailbox in Exchange Online, which the wizard shows. Every
Microsoft connection with a user also asks for `offline_access` and
`User.Read`. A Microsoft access token is valid for one resource, so the
broker keeps one token per resource and refreshes each with the scopes
granted for that resource.

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
  when starting, and a matching account is then confirmed without asking,
  but only when the provider vouches for the address: a verified Google
  email, or a Microsoft account in the tenant ID the app names. Otherwise
  the starter confirms as usual. With authentication off, any caller of the
  API may confirm, as it may take any other API action.
- **Sign-in by someone else.** The account's owner need not use Dagu. In
  server callback and Cloud relay modes, the starter can copy the
  authorization URL to the owner, who consents in their own browser. The
  starter then confirms the account, as for any session, so an admin can
  connect a shared mailbox that someone else owns.
- **PKCE (S256) everywhere.** Google requests also send
  `access_type=offline` and `prompt=consent`, so a refresh token is always
  returned.

Redirect modes:

| Mode | When | Client type |
| --- | --- | --- |
| Server callback | The server has a usable public URL: `server.public_url`, else the tunnel URL, else the UI's origin when the browser reaches Dagu at `localhost`, which Google accepts over HTTP. The redirect URI is `<public_url>/oauth/callback`, a public route beside `oidc-callback` (`internal/service/frontend/server.go:1513`) | `web` |
| Cloud relay | The server has an online license (RFC 002) but no usable public URL. The redirect URI is Dagu Cloud's fixed callback, `<cloud_url>/oauth/callback`, and the server fetches the code from Dagu Cloud, as [Cloud relay](#cloud-relay) describes | `web` |
| Loopback | The CLI runs where a browser can open. The CLI listens on `127.0.0.1` on a random port and sends the code to the server | `desktop` |
| Paste | None of the above works, as for an unlicensed server on a private address. The browser lands on a loopback URL that fails to load, and the user pastes that URL into the UI or CLI | `desktop` |
| Device code | Microsoft, over SSH. The CLI shows a code to enter on another device | `public` |

Google web clients accept only HTTPS or `localhost` redirect URIs, and
Google's device flow does not cover Gmail scopes, so a Google connection uses
one of the first four modes. The UI and CLI pick the first mode in the
table that works, given the server's public URL, its license, and the app's
client type, and show the exact redirect URI to register.

**Checks after consent:**

- **Granted scopes.** Google lets the user untick scopes. A missing scope is
  shown on the service that needs it, with Reconnect.
- **Identity.** Dagu reads the account and its `account_id`, and asks the
  user to confirm it in the UI or CLI that started the session, showing the
  Microsoft tenant too. This stops a consent completed in someone else's
  browser. Every comparison of accounts in this RFC compares `account_id`,
  since another Microsoft tenant can claim any email address.
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
  client and silently revokes the oldest. When the other connection belongs
  to another project, the warning suggests one server connection instead.

**Reconnect** runs the same flow for an existing connection. The previous
tokens stay live until the user confirms, and confirming increments
`generation`. A different account than before is never confirmed without
asking.

Each connect gets a new refresh token from the provider. The token that a
confirmed reconnect replaces, and the token of a session that expires
unconfirmed, are revoked where revoking one token leaves the rest of the
grant. Where it ends the whole grant, as M0 checks for Google, they are left
unused, and the docs state Google's limit of 100 refresh tokens per account
and client, past which Google revokes the oldest.

**Remove** takes the connection's lock, revokes the refresh token where the
provider supports revocation, then deletes both records. A provider may end
the account's whole grant to the client on revocation, so Dagu does not
revoke while another connection on this server holds the same account and
client. Connections on other servers that use that client can still lose
access, and the confirmation says so. A DAG that still names the
connection fails at run start, or for mail and SMTP when it uses the
account, naming it. Creating a connection
replaces any token state record left under its name, so a new connection
never inherits an old one's tokens.

#### Cloud relay

A server without a public URL still gets a fixed HTTPS callback, without
paste mode and without Dagu Cloud holding any secret:

```text
 server                          Dagu Cloud                  browser, Google
   |-- register callback ------->| callback ID                     |
   |   state = ID + enc{session, verifier, expiry}                 |
   |   client secret stays here; verifier leaves only encrypted    |
   |                             |<-- /oauth/callback?code&state --| consent
   |                             |    store code under the ID      |
   |-- poll every 3 s ---------->|                                 |
   |<-- code and state; deleted -|                                 |
   |-- check enc state, exchange code with verifier and secret --> Google
```

- Starting a session, the server registers a callback with Dagu Cloud under
  its license credentials, and gets a callback ID. The `state` parameter
  starts with that ID in clear, followed by the encrypted state, so Dagu
  Cloud routes the callback without reading it.
- Dagu Cloud's callback page stores the code and state under the ID, and
  tells the person they may close the page.
- While the session waits, the server polls Dagu Cloud for the callback
  every 3 seconds, as license activation polls (RFC 002). It then checks the
  encrypted state and completes the callback as if it had received it.
  Dagu Cloud deletes the code on the first fetch, or after 10 minutes. Only
  the license that registered a callback ID may fetch it.
- Dagu Cloud holds only the code and the encrypted state. It cannot read
  the PKCE verifier inside the state, and never receives the client secret,
  so it cannot exchange the code for tokens.
  RFC 002's rules hold: Dagu Cloud stores no secret, and runs never depend
  on it, since only connecting does.
- One redirect URI serves every server, so a team with several servers
  registers it once in each OAuth client.

#### Consent errors

Google's errors on the consent screen are mapped to the fix, since the raw
codes do not say what to do:

| Error | Shown |
| --- | --- |
| `access_denied` | Google does not say which cause applies, so both are shown: the user declined, or the app is in Testing status and the account is not a test user. The second comes with how to add a test user |
| `admin_policy_enforced` | The Workspace admin blocks this client. Ask them to trust the client ID, shown with a copy button, under API controls in the Admin console |
| `redirect_uri_mismatch` | The redirect URI to register, with a copy button |
| `invalid_client` | The client ID or secret is wrong, or the client was deleted. Upload the client JSON again |

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
is shorter than the `minTTL` asked for. So while runs keep exporting a
connection's token, it refreshes about once per token lifetime minus 50
minutes: every 10 minutes for a Google token, however many runs start. A
`minTTL` longer than half the provider's token lifetime is cut to half, so
a provider with short-lived tokens refreshes at most twice per lifetime, and
the caller learns the real lifetime from `expiresIn`.

A caller whose token the resource rejected reports that token. The broker
refreshes only while it is still the stored token, so one rejection that
many callers see causes one refresh. Every consumer does this: `mcp.call`,
`http.request`, and mail. A worker reports it through the coordinator RPC.

Local path, used by the server, the scheduler, and local runs:

1. Read the token state. If the resource's access token stays valid for
   longer than `minTTL`, return it.
2. Try the connection's lock once, without queuing: a `dirlock` per
   connection (`internal/cmn/dirlock/dirlock.go:168`), renewed as
   `persis.Backend.Lock` renews a collection's lock
   (`internal/persis/file/backend.go:132`). That lock covers a whole
   collection, so a token endpoint that hangs at one provider would hold
   back every other connection's refresh. The lock renews itself while it
   is held, and refuses writes once lost.
   - If another process holds it, a refresh is in flight. Wait for the lock
     to be released, checking every 50 ms, then read the state once. Return
     the new token, or, if the holder's refresh failed, fail with the error
     it recorded, without calling the provider. Start again at step 1 only
     if the holder died and its lock went stale.
   - So when 100 runs reach an expired token together, one refreshes and
     the other 99 each read the state once. `dirlock.Lock` would instead
     queue all 99 for the lock, each polling with `mkdir` every 50 ms and
     taking it in turn, which delays the last by seconds on NFS.
3. Holding the lock, read the state again. Another process may have
   refreshed it between steps 1 and 2.
4. Refresh with the scopes granted for the resource. The token endpoint
   call times out before three quarters of the lock's stale threshold.
5. Write the new access token, and any rotated refresh token, with a CAS
   write (`createOrSwap`, `internal/persis/store/cas_retry.go:15`). The
   connect flow writes `session` in the same record without the lock, so a
   conflict is expected. On a conflict, read the record again and reapply
   only the token fields, so a rotated refresh token is never dropped:
   - If `generation` changed, a reconnect was confirmed during the refresh.
     Drop the result and return the reconnected token.
   - If the record is gone, the connection was removed. Drop the result and
     fail. The broker never creates a token state record.

When many runs reach an expired token together, one refreshes and the
others only read:

```text
 run A                   runs B to Z           token state, lock       provider
   | token near expiry     | token near expiry       |                     |
   |-- try lock ---------------------------------->  | taken by A          |
   |                       |-- try lock ---------->  | held: do not queue  |
   |                       |   check every 50 ms     |                     |
   |-- read again -------------------------------->  |                     |
   |-- refresh ---------------------------------------------------------->|
   |<------------------------------------------- new token, maybe a new --|
   |                                                 |      refresh token  |
   |-- CAS write, unlock ------------------------->  | released            |
   |                       |-- read once --------->  | new token           |
```

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
| 5xx, network error, or `temporarily_unavailable` | Transient. The status stays. The error is recorded in the token state, so the processes waiting on the lock fail with it instead of refreshing again. The call fails, and the step's retry policy applies |
| Service account key unknown, disabled, or deleted | The app becomes `invalid`, set once, as for `invalid_client`. An admin uploads a new key |
| Service account clock error | Transient. Reported on the connection, and the status stays |

A grant without a refresh token, a service account or client credentials,
never becomes `needs_reconnect`, since there is nothing to reconnect. Its
permanent failures mark the app `invalid`.

The refresh code moves from `internal/cmn/mailer/oauth` into the
`connection` package without its mail-specific assumptions. That code is
`refreshTokenGrant`, `TokenError`, the scope rules for Microsoft, and the
gate. The deprecated mail `oauth` blocks keep calling it with the
configured refresh token, as [Mail](#mail) describes.

**Distributed runs.** A worker does not read the connection store. It calls
a new coordinator RPC, `ResolveConnectionToken`, modeled on
`ResolveSecretReference`:

- Authorization reuses `authorizedAttempt` and `authorizedDAG`
  (`internal/service/coordinator/secret_reference.go:233`, `:259`).
- The run's DAG snapshot must declare the connection, checked as
  `secretReferenceDeclared` checks secret refs (`:354`).
- The name must resolve for the run's project, as [Scopes](#scopes)
  describes.
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
  older than another host's write, and can show a released lock as held for
  a while, so a waiter on another host may wait longer.
- **Google.** Its refresh tokens do not rotate, so a stale read costs one
  extra refresh and nothing else.
- **Rotating providers.** The re-read on `invalid_grant` recovers from a
  stale read when another host has rotated the token.
- **Reuse detection.** A provider that revokes the whole token family when an
  old refresh token is reused is the remaining risk. The docs tell users of
  such providers to keep every process that has the data directory on one
  host, or to keep the data directory on local disk.

### Notifications

A status change records one event in the event store, written by the
process whose CAS write made the change. The events have a new kind,
`connection`, and two types:

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

A connection's event is recorded once, also for a server connection that
several projects use. It goes through the routes that a run event would
take in each project that can use the connection: its own project, or each
project a server connection lists. A channel that several of those routes
reach gets one message per event. Both events are in the default event set
for new channels. A failure produces one message per channel, however many
runs then fail on it.

A server connection that lists no project, such as one that only the
notification SMTP settings use, reaches no route. The navigation badge and
the notification settings page show its status to admins. A connection
that email notifications use cannot report its own failure by email, so
its event reaches only the other channels of its routes.

Runs that fail on a broken connection would still send their own
`dag.run.failed` messages, one per run. Such a run records the connection
as its failure cause ([Runs](#runs)). A channel that receives
`connection.needs_attention` skips the run failure message of a run whose
recorded cause is a broken connection. The decision reads only the run's
own record, so a monitor that catches up after a reconnect still skips
those runs. A channel that does not receive connection events still gets
every run failure, so no failure goes unreported. The DAG's own failure
handlers run as before. Scheduled runs keep starting, and their steps that
use the connection fail, so the run history shows when the connection broke
and when it was restored.

### Runs

**Declared connections.** Building a DAG collects the connections it names
into `ir.DAG.Connections`, as `CredentialRefs` collects SSH refs
(`internal/ir/dag.go:298`). The sources are:

- step `with.connection`;
- `mail_accounts.*.connection` and `smtp.connection`, from the DAG or its
  base config;
- `secrets` entries with `provider: connection`.

RFC 026 adds `mcp_servers` on AI steps.

Each name must be a literal. A name taken from a parameter or variable fails
validation, since the coordinator checks every token request against the
declared names.

The DAG index (`internal/persis/file/dag/dagindex/dagindex.go:27`) also
stores each DAG's connection names, so the Connections page lists the DAGs
that use a connection without reading DAG files. An index entry loads only
metadata today, so the names are read from the YAML's connection fields
without a full build. A connection that a base config names is listed as
used by the base config, once, not by every DAG.

**Admission checks.** Spec validation stays offline: building a DAG only
collects names. Checks that need connection records run at run admission: in
the run process, before its first step, where secrets resolve. A local run
reads the records, and a run on a worker reads the binding through the token
RPC with `check_only`. The editor's server-side diagnostics run the same
checks:

- the name resolves for the run's project, as [Scopes](#scopes) describes;
- `server` names one of the connection's services;
- a literal `http.request` URL is on the bound hosts;
- a Google MCP preset on a user connection, not a service account, uses an
  app with `agent` set.

A failed check fails the run before its first step, naming the connection,
as an unresolvable secret does.

Two kinds of use are checked later, when they happen:

- **Mail and SMTP.** A base config's mail accounts and `smtp` reach every
  DAG, including DAGs that send no mail. Their connections are checked when
  a step or a notification uses one, as mail accounts resolve today
  (`internal/runtime/env.go:608`), with the checks in [Mail](#mail). They
  stay declared, so a worker may still ask for their tokens.
- **Status.** A connection that needs reconnecting, or whose app is
  `invalid`, passes admission. Each step that uses it fails with
  `reconnect_required`, and the run's other steps run as before.

**Run records.** A step that uses a connection records the connection's name
and account in its node status. A run that fails with `reconnect_required`, or
on an `invalid` app, records the connection as its failure cause.

**Masking.** `setupMasker` builds each attempt's masker from the scope's
secrets (`internal/runtime/output.go:78`). A step that uses a connection mints
its token before node setup and adds it with `EnvScope.WithMaskedSecrets`
(`internal/cmn/value/envscope.go:410`), which masks a value without making it
an environment variable. The masker gains a way to add a value while the step
runs, so a token minted later in the step, by a forced refresh, is masked too.
It also gains provider token patterns (`ya29.` and `1//` for Google), as audit
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
- A 401 is handled as `mcp.call` handles one: one refresh and one retry
  when the rejected token is the stored one, and the current token
  otherwise.

#### Mail

Every place that takes mail OAuth today takes a connection:

| Where | Field | Resolves in |
| --- | --- | --- |
| A DAG's or base config's `mail_accounts` | `mail_accounts.<address>.connection` | The run's project |
| A DAG's or base config's `smtp` | `smtp.connection` | The run's project |
| The notification SMTP settings | `connection` | Server connections only |

```yaml
mail_accounts:
  me@example.com:
    connection: work-google
smtp:
  connection: ops-mail
```

- **Replaces credentials.** `connection` replaces `oauth` and `password`.
  It takes a `google` or `microsoft` connection, the providers mail OAuth
  supports today. The provider comes from the connection, so `provider` is
  not needed, and one that differs fails when the account is used.
- **Fixed hosts.** The token goes only to the preset's IMAP and SMTP hosts,
  with a verified certificate. With `connection`, a mail account takes no
  `imap` or `smtp` block, and a DAG's `smtp` and the notification SMTP
  settings take no host or port. A changed host or a relaxed certificate
  check would let the token reach another server.
- **Services.** Mail actions on a Google account use the Gmail API, as
  today, and need `gmail` with `write`. Google SMTP needs `mailbox`. A
  Microsoft account and Microsoft SMTP need `mailbox`. Mail should use a
  standard connection, not an agent one, and the editor warns otherwise, as
  [Google's agent policy](#googles-agent-policy) states.
- **Address and account.** A connection acts as one account, and the
  address must fit it:
  - A Google user connection acts as its own account, so the address, or
    the SMTP username, must be that account. A step's `from` may still name
    a send-as address that the account has.
  - A Microsoft delegated connection may serve another address, such as a
    shared mailbox that the account can open. Microsoft enforces the
    access.
  - A service account or client credentials connection acts as its
    `subject`, so the address must be the `subject`. A team with several
    mailboxes adds one connection per mailbox. They share one app, which
    holds the key, so rotating the key is one update.
- **When checked.** These checks run when a step or notification uses the
  account, as [Runs](#runs) describes. The editor reports them earlier.
- **Rejected tokens.** An IMAP or SMTP server that refuses the token, or a
  Gmail API 401, is handled as `mcp.call` handles a 401: one refresh and
  one retry when the rejected token is the stored one.
- **The old form.** The `oauth` blocks keep working, unchanged, and are
  deprecated. The editor warns on each one, and `dagu connection import`
  moves its refresh token into a connection.

#### Tokens for scripts

```yaml
secrets:
  - name: GOOGLE_TOKEN
    provider: connection
    key: work-google
```

A new secret provider puts a fresh access token in an environment variable.
It is for scripts and CLIs that take a bearer token. The token is minted at
run start and stays valid for at least 50 minutes, or half the provider's
token lifetime when that is shorter, so a longer run should use a
connection-aware step instead. For a Microsoft connection,
`options: { service: mail }` selects the resource. The provider works only
when an admin has set `allow_token_export` on the connection. Its value is
masked like any secret. It resolves at run start, as other secrets do, so in
a base config it fails the runs of every project that cannot use the
connection. It belongs in the DAG.

#### Confirmation requests

Google's MCP servers "still return form elicitation prompts for
human-in-the-loop confirmation on sensitive tool calls", service accounts
included. Dagu's MCP clients advertise form elicitation:

- **`mcp.call`** declines at once, since a step cannot wait mid-call, and
  fails with kind `confirmation_required`.
- **Try it in the UI.** The person is present, so the form is shown inline.
  The call request returns the form and a call ID. The server holds the call
  open in memory, for up to 10 minutes, and the page posts the answer to
  `POST /connections/{name}/calls/{id}/confirmation`, which returns the
  tool's result. Only the replica that holds the call can answer it. Behind
  several replicas without session affinity, an answer that reaches
  another replica fails with `call_not_found`, and the page offers to call
  the tool again.

Dagu never accepts a request on its own. The request is the provider's own
check that a person is present. RFC 026 lets agent DAGs wait for a person's
answer.

#### Google's agent policy

- Google's MCP presets need an app with `agent` set. An `mcp.call` step to
  one fails at admission without it. A service account connection needs no
  flag.
- Google requires a separate OAuth client for non-agent use. A connection
  uses one app, so the wizard offers a second connection, with its own app,
  for REST automation and mail. An `http.request` step, mail account, or
  `smtp` that uses an agent connection gets a lint warning.

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
  of shared storage ([Open questions](#open-questions), item 5);
- no server would hold a refresh token at all.

On a server, such a connection would appear in the list read-only, with its
name and binding from Dagu Cloud. A worker would still ask its coordinator,
and the coordinator would ask Dagu Cloud through the broker's third source.
Dagu Cloud would grant the connection to servers, where it would act as a
server connection: each server's admin would list the projects that may use
it. A cloud connection whose name collides with a connection on that server
would not be offered there until one is renamed. Status and notifications
would come from Dagu Cloud once, not from each server, though each run would
still record its failure cause.

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
    [--app APP | --client-file client_secret.json] [--no-browser] [--workspace ops | --server]
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
dagu connection share NAME --projects ops,billing
dagu connection tools NAME [--server gmail] [--json]
dagu connection call NAME --server gmail --tool search_threads --args '{"query":"is:unread"}' [--yes]
dagu connection apps list | remove APP
```

- A connection belongs to the context's workspace unless `--server` is
  given, which needs `admin`. Service account and client credentials
  connections are always server connections. `share` sets a server
  connection's projects.
- `--client-file` registers an OAuth app, which needs `admin`. A manager
  names an app an admin registered with `--app`.
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
- `import` moves an existing refresh token into a connection, with no new
  consent. The token may come from any mail `oauth` block, or from the
  secret the block references. Dagu reads the account and the granted
  scopes from the provider, so the connection is checked like any other.
  Its output says to replace every `oauth` block that holds the token with
  `connection:`. A copy left behind refreshes on its own, and with a
  provider that rotates refresh tokens, the two copies can break each
  other.
- `call` handles a server's confirmation request as Try it does. On a
  terminal it shows the form and sends the person's answer. Without one,
  it fails with `confirmation_required`. `--yes` skips only Dagu's own
  question before a tool that is not read-only, never the server's.

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
- A `scope` parameter, a project or `server`, qualifies `{name}` in these
  paths, since a project connection and a server connection that does not
  list that project may share a name. `POST /connections` takes the scope,
  and `PATCH` sets a server connection's `projects`.
- The notification settings API's SMTP block gains `connection`. Setting it
  needs `admin`, since email notifications then send as that account.
- Responses never include a secret. They report `clientSecretConfigured`,
  `refreshTokenConfigured`, and similar flags, as notification channels do
  (`internal/notification/model.go:253`). A `PATCH` with a blank secret keeps
  the stored one.
- Writes and tool calls are audited with `logAudit`, as secret writes are
  (`internal/service/frontend/api/v1/secrets.go:187`).
- A server answers these paths only for its own connections. A request
  whose `remoteNode` names another server is refused, as inventory requests
  are. Each server has its own callback URL.
- `GET /connections/{name}/tools` reads the tool catalog, so open tabs
  and editors never list a service more than once per 10 minutes.

### UI

A **Connections** page under Configure, also linked from the Integrations
page (`ui/src/pages/integrations/index.tsx`).

```text
┌ Connections ─────────────────────────────────────────────────────────┐
│ [+ New connection]                                                   │
│                                                                      │
│ name          scope     provider   account            status         │
│ work-google   project   Google     ops@example.com    ● connected    │
│ shared-mail   server    Google     mail@example.com   ● connected    │
│ notion        project   MCP        workspace "Ops"    ▲ reconnect    │
└──────────────────────────────────────────────────────────────────────┘
```

**New connection** opens a gallery with four tiles: Google Workspace,
Microsoft 365, Any MCP server, and Custom OAuth2. An admin also chooses
whether the connection belongs to the current project or to the server. The
Google wizard has five steps:

1. **Services.** Cards for Gmail, Calendar, Drive, Docs, Sheets, Slides,
   Chat, People, Search, and IMAP and SMTP mail, each with a read or
   read-and-write choice where it applies. The scopes they need are listed
   underneath.
2. **Agents.** "Who will use this connection?" with three answers: AI
   agents, other automation and mail, or both. The step says that Google
   requires separate clients for agents and for other automation. **Both**
   makes a pair from one pass through the next step, which then asks for two
   clients, one with the agent flag. The pair is named `<name>-agent` and
   `<name>`, so `<name>` has at most 57 characters, and each connection's
   page links to the other.
3. **OAuth app.** The user picks an existing app. An admin may instead
   follow a checklist that registers one. A manager who finds no fitting
   app sees what to ask an admin for, as [Scopes](#scopes) states. The
   checklist:
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
   a link, and **Copy sign-in link** gives the URL to the account's owner
   instead. In paste mode, the page says before opening the popup that the
   browser will end on an error page, and that its address is what to copy.
   It then shows a field for that address.
5. **Done.** The account to confirm, granted scopes, a result per service,
   and two buttons: **Start from a template** and **Browse tools**.

The connection page shows status and the reason for it, the account,
services and scopes, its project or, for a server connection, the projects
it lists, and Reconnect, Test, and Remove, with three panels:

- **Used by:** the DAGs that name the connection, read from the DAG index,
  and the notification SMTP settings when they use it. Remove, a reconnect
  with a different account, and dropping a project from a server
  connection each show this list before they apply, across projects for a
  server connection.
- **Add service:** runs the wizard's Services, OAuth app, and Consent steps
  again for the new service only. The OAuth app step shows just the APIs to
  enable for it, and Consent reconnects with the added scopes.
- **Tools:** each MCP service's tools, with a form built from each tool's
  input schema.
  - **Try it** shows the result. A tool that is not read-only acts on the
    real account, so Try it asks first. `dagu connection call` asks the same
    on a terminal, unless `--yes` is given.
  - **Copy as step** copies an `mcp.call` step in YAML.

Elsewhere in the UI:

- **DAG editor.** `connection:` completes connection names. It uses a
  `connectionName` definition in `schemas/dag.schema.json` and the mechanism
  that completes calendar names (`ui/src/lib/calendarNameExamples.ts`,
  `ui/src/features/dags/components/dag-editor/DAGSpec.tsx`). `server`
  completes from the connection's presets. `tool` and argument keys complete
  from `GET /connections/{name}/tools`. Completion lists the connections that
  fit the field: agent connections for `mcp.call` with a Google preset, and
  the others for `mail_accounts` and `http.request`. The server also reports
  unknown connections, and server connections that do not list the DAG's
  project. For an admin, the second offers to list the project.
- **Run page.** A step that used a connection shows its name and account.
  A step that failed with `reconnect_required` shows **Reconnect** next to its
  error.
- **Navigation.** The Connections item shows a badge while any connection
  needs reconnecting, or has a refresh token that expires within two days.
- **Notification settings.** The SMTP settings pick a server connection, and
  show its status beside it.
- **Getting started.** The checklist
  (`ui/src/features/activity/hooks/useGettingStarted.ts`) gains a "Connect
  an account" step.

### Templates

Each provider has a few starter DAGs:

- a daily Gmail digest sent to chat;
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
  tool call, and listing a service's tools costs one request per 10
  minutes per host, through the tool catalog;
- explain the `rate_limited` error kind, so a user who reaches a limit
  recognizes it;
- show how to cap concurrent use of one account: put the DAGs that use it
  in a queue whose `max_concurrency` limits how many run at once. Google's
  limits are per account as well as per project, so 100 runs on one account
  at once can reach them even within a daily quota.

## Permissions

| Action | Role |
| --- | --- |
| Create, change, or delete an OAuth app | `admin` |
| Set `allow_token_export` | `admin` |
| Create, connect, reconnect, change, or remove a project connection | `manager` or above in its project |
| The same for a server connection, and set its `projects` | `admin` |
| Add a service account or Microsoft client credentials connection | `admin`, as a server connection only |
| Choose the connection of the notification SMTP settings | `admin` |
| Test a connection, list its tools | `developer` or above in a project that can use it |
| Try a tool | `developer` or above in a project that can use it, audited |
| Use a connection in a DAG | Whoever may edit the DAG, if the name resolves for the DAG's project |
| See a connection: name, scope, provider, account, status | `viewer` in a project that can use it. Never its tokens |

A manager's `mcp` connect may register a client through RFC 7591. The
resulting `dcr` app belongs to that connection alone: it is created and
deleted with it, and no other connection may use it.

Project connections follow the secret store, where managers manage a
workspace's secrets (`requireSecretManageForWorkspace`, `secrets.go:414`).
Server connections and OAuth apps follow notification channels, which stay
server-wide and admin-only under RFC 003. A server connection's `projects`
list is set by admins, not by each project, because a connection opens an
account's data, while a route only sends to a channel.

## Security

- **Secrets at rest.** Refresh tokens, access tokens, and client secrets are
  encrypted at rest with the server's key, in directories readable only by
  the Dagu user.
- **Audience binding.** A token goes only to its connection's bound hosts or
  resource, over HTTPS or, for mail, over TLS with a verified certificate,
  and never follows a redirect off them. Mail fields that would change a
  host or relax certificate checks are refused with a connection. A worker
  enforces the binding it receives from the coordinator.
- **Scope enforcement.** It is checked twice: at run admission, and by the
  coordinator for each token. On workers it is a boundary. In local mode it
  prevents mistakes, not a malicious DAG author, as
  [Guarantees](#guarantees) states.
- **Service accounts and Microsoft client credentials.** Server connections
  only, so only an admin fixes the `subject`. No step, parameter, tool
  argument, or mail address can change it.
- **The connect flow.**
  - Every request carries an encrypted state and PKCE.
  - A callback is single-use and expires.
  - Only the user who started a session confirms it, in the UI or CLI that
    started it. Tokens go live only on confirmation.
  - Accounts are compared by the provider's immutable ID, never by email
    alone. An expected account is confirmed without asking only when the
    provider vouches for its address.
  - The callback page shows no token, and closes the popup.
  - In Cloud relay mode, Dagu Cloud holds only the code and the encrypted
    state, until the server fetches them. Without the server's PKCE
    verifier and client secret, the code is useless, and a forged callback
    fails the server's state check.
- **Masking.** Tokens are masked in local logs, status, and outputs.
  Rollout step 0 extends masking to log streams from workers, which are not
  masked today (`internal/runtime/output.go:484`). Token export waits for
  that fix.
- **SSRF.** Admins set `oauth2` endpoints on apps, and managers set `mcp`
  server URLs, but an `mcp` server's metadata names its authorization
  server, token, and registration URLs. Every URL Dagu fetches, and every
  redirect it follows, must be HTTPS. Plain HTTP to a loopback address, for
  local testing, is allowed only for a URL an admin enters. Authorization
  server metadata is accepted only when its `issuer` matches the URL it
  came from (RFC 8414).
- **Audit.** These are written to the audit log:
  - app and connection changes;
  - connects, reconnects, and removals;
  - status changes;
  - tool calls from the UI.

  Token issuance is not audited. It is routine, and each run already
  records the connection and account it used.

## Failure behavior

| Event | Behavior |
| --- | --- |
| The user denies consent or closes the popup | The session fails or expires. The connection stays `pending`, or keeps its previous tokens on a reconnect |
| Google returns a consent error, such as a missing test user or a Workspace admin's block | The session fails, and the UI or CLI shows the fix from [Consent errors](#consent-errors) |
| Dagu Cloud is unreachable during a Cloud relay connect | The session fails, saying so, and offers the next mode. Runs and existing connections are unaffected |
| Dagu Cloud lacks the relay endpoints | The server does not offer Cloud relay, and picks the next mode |
| The callback arrives after 10 minutes, twice, or for a session that a newer one replaced | Refused. Nothing changes |
| Another user tries to confirm the account | Refused. The session stays in `confirm` |
| Nobody confirms the account | The session expires. The previous tokens stay live |
| The user unticks a scope | The connection is created. Services missing a scope show it, and steps that need it fail with `insufficient_scope` |
| A Google MCP probe is rejected | The service shows the agent flag hint and a link. Other services are unaffected |
| The refresh token is revoked or expires | `needs_reconnect` once, one `connection.needs_attention` message per channel, a badge in the UI, and every step that uses the connection fails with `reconnect_required` and the connection's name. Channels that receive the connection event skip those runs' failure messages |
| A refresh token from an app in Testing status nears its seven-day expiry | The Connections page shows the time left, and the badge appears two days before. No notification is sent until it expires |
| A refresh fails with `invalid_grant` because another host on shared storage rotated the token | The re-read finds the new refresh token and the refresh is retried once. Status is unchanged |
| The provider answers HTTP 429 | The step fails with kind `rate_limited` and the provider's message. Status is unchanged |
| The OAuth app's secret is rotated or deleted at the provider | The app becomes `invalid`, and its connections fail at once without calling the provider. Updating the app restores them |
| The token endpoint is down | One process tries a refresh at a time. The processes waiting on it fail with its error, as transient, and retry under their own policy. Status is unchanged |
| A reconnect is confirmed while a refresh is running | The refresh result is dropped. Both processes end with the reconnected token |
| A connect session starts while a refresh is running | The refresh's write conflicts, re-reads, and keeps both the session and any rotated refresh token |
| A connection is removed while a refresh is running | The refresh result is dropped and the call fails. The connection stays removed |
| A connection is removed while another connection on this server holds the same account and client | The refresh token is not revoked, so the other connection keeps working |
| A project connection moves to the server while a refresh is running | The move waits for the refresh, and keeps any rotated refresh token |
| An upgrade adds a scope to a preset | Refreshes keep working with the granted scopes. The service shows the missing scope until a reconnect |
| A provider's tokens live shorter than the `minTTL` asked for | The caller gets a fresh token with at least half the provider's lifetime. The connection refreshes at most twice per lifetime |
| Many processes reach an expired token at once | One takes the lock and refreshes. The others wait for its release and read the new token once, without taking the lock |
| Many runs read a tool catalog older than 10 minutes at once | One lists the service. The others wait for its release and read the new catalog |
| Listing a service's tools fails | The reader gets the provider's error. The previous catalog stays for the next attempt |
| One provider's token endpoint hangs | Only that connection's callers wait, for one token call's timeout, and then fail with its error. Other connections are unaffected |
| A service account key is deleted at Google | Its app becomes `invalid`, and its connections fail at once without calling Google. Uploading a new key restores them |
| A DAG names an unknown connection, or a server connection that does not list its project | The editor reports it. A run fails before its first step, naming the connection |
| A base config's mail account or `smtp` names a connection that a project cannot use | That project's DAGs that never use the account run as before. A step or notification that uses it fails, naming the connection |
| A mail account with a connection sets an `imap` or `smtp` block, or a DAG's `smtp` with a connection sets a host or port | Validation fails, naming the field |
| A mail address does not fit the connection's account or `subject` | The step or notification fails, naming both |
| The connection that email notifications use breaks | Email notifications fail, and each failure is logged. Its event reaches the other channels of its routes, and the badge and the notification settings page show it to admins |
| A name would collide between a project connection and a server connection that lists the project | Creating the connection, or listing the project, fails and names both |
| An admin deletes a project that owns connections, or that a server connection lists | Refused, naming them |
| An account from another Microsoft tenant claims the expected email | It is not confirmed without asking. The starter sees its tenant and decides |
| A reconnect is confirmed, or a session expires unconfirmed | The unused refresh token is revoked where that leaves the rest of the grant, and left unused otherwise |
| The connection that the notification SMTP settings use is removed | The confirmation names the settings. Email notifications then fail until an admin picks another connection |
| An admin drops a project from a server connection | That project's runs that name it fail at admission, naming it. Other projects are unaffected |
| A worker's coordinator is older and lacks the token RPC | The step fails, saying the coordinator must be upgraded |
| A worker is older than the token RPC | The coordinator does not dispatch a DAG that declares connections to it |
| A server sends a confirmation request | `mcp.call` declines it and fails with `confirmation_required`. Try it shows the form to the person |
| A Try it answer reaches a replica that does not hold the call | Refused with `call_not_found`. The page offers to call the tool again |

## Performance

- **Idle cost.** None. No ticker, watcher, or scan is added. Tokens refresh
  only when a step needs one.
- **Refresh rate.** At most once per connection and resource per token
  lifetime on a host, about once an hour per resource, regardless of the
  number of runs.
  Today mail refreshes once per run, so a mail DAG that runs every minute
  drops from 60 refreshes an hour to one. Token export shortens this to
  about every 10 minutes for a Google connection, while runs keep starting
  that export it.
- **Refresh lock.** One lock per connection. Refreshes of different
  connections never wait for each other, and a slow provider delays only its
  own connections.
- **Per run.** A run that uses a connection checks it once, at admission or,
  for mail and SMTP, at first use, and reads its token state at most once
  per token lifetime: small file reads locally, or two coordinator RPCs on a
  worker. Steps and `foreach` items share the run's cache, so the cost grows
  with runs that use connections, not with steps. At one run per second,
  that is at most two reads or RPCs per second for each connection a run
  uses. Resolving a name reads at most two small records: the project's and
  the server's.
- **Email notifications.** The process that sends them takes tokens through
  the broker and caches them in memory, so a burst of notifications reads
  the token state once and refreshes at most once per token lifetime.
- **Cloud relay.** One request to Dagu Cloud every 3 seconds per waiting
  session, for at most the session's 10 minutes. It stops when the session
  completes, fails, or is cancelled, and nothing polls outside a connect.
- **Requests.** Connection lists read two small records per connection: its
  metadata and its token state. A
  server holds tens of connections, not thousands, and no request reads run
  or DAG directories.
- **Tool lists.** One `tools/list` per connection and service per 10
  minutes on a host, through the tool catalog, whatever the number of open
  tabs or runs. Nothing lists on a timer: a catalog is refreshed only when
  read after it is 10 minutes old.
- **Try it.** A confirmation holds one call in server memory, for at most
  10 minutes.
- **Notifications.** One event per status change, not per failed run. A
  connection that stays broken while 1,000 runs fail sends one message per
  channel. The monitor's reader for `connection` events runs on its existing
  poll interval (`internal/service/chatbridge/monitor.go:403`). It adds one
  cursor read of the event store per tick, which returns nothing between
  status changes.
  Skipping a run failure caused by a broken connection reads only the run
  status the event already carries, so the monitor keeps no extra state.
- **Storage.** About 2 KB per connection, plus about 1 KB per extra resource
  of a Microsoft connection, and 1 KB per app. A step that uses a connection
  adds about 100 bytes to its node status: the connection's name and account.
  A run that fails on a broken connection adds about 70 bytes for its cause.
  The DAG index adds the connection names of each DAG that uses one, tens of
  bytes per entry. The tool catalog holds roughly 1 to 2 KB per tool, so 20 to
  40 KB per Google service, bounded by connections times services and
  rewritten at most every 10 minutes. Audit grows only with management actions
  and tool calls from the UI.
- **Upgrade.** No migration. The DAG index version goes up, so the first
  start rebuilds the index once, reading each DAG file as a rebuild does
  today. RFC 003's v3.0 migration moves project connections, as
  [Scopes](#scopes) describes.

## Compatibility and rollout

- **Additive.** A DAG without connections behaves as before. The mail
  `oauth` blocks, now deprecated, and static `headers` keep working.
- **Older binaries.** A step with `connection` or `server` fails validation on
  an older binary, which rejects unknown keys there. It rejects
  `connection` in `mail_accounts` and `smtp` the same way
  (`internal/spec/mail_accounts.go:93`). Once a base config uses it, an
  older binary loads no DAG, so a downgrade first moves the base config
  back to `oauth`. An older binary ignores `connection` in the notification
  SMTP settings, so email notifications fail until an admin sets the
  credentials again. An older binary ignores the new collections and the
  new run status fields, so a downgrade drops the feature and keeps the rest
  working. An older binary finds a DAG index of a newer version and rebuilds
  its own, as on any version mismatch
  (`internal/persis/file/dag/dagindex/dagindex.go:94`).
- **Version skew in distributed mode.** A new worker under an old
  coordinator fails only the steps that use connections, as [Failure
  behavior](#failure-behavior) states. An old worker parses the DAG
  definition itself (`internal/service/worker/remote_handler.go:463`), so it
  would reject the new fields or call without a token. Workers report their
  Dagu version (`proto/coordinator/v1/coordinator.proto:233`), and the
  coordinator dispatches a DAG that declares connections only to workers new
  enough for the token RPC. A base config that names a connection makes
  every DAG declare it, so workers are upgraded before a base config uses
  one.
- **API.** New paths, and one new field: `connection` in the notification
  SMTP settings. Existing fields are unchanged.
- **v3.0.** RFC 003's migration gains connections. It moves them and never
  copies them, as [Scopes](#scopes) describes.
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
- **Specs.** 098 (`mcp.call`), 045 (`http.request`), 073 (mail), and 069
  (secret providers) gain the new fields, and the specs for a DAG's `smtp`
  and the notification settings gain `connection`. A new spec defines
  connections, the broker, and the connect flow.

Rollout, in milestones of pull requests that each work on their own. The
milestones span this RFC and RFC 026.

- **M0, about a week: test against Google.** Before building on them,
  confirm:
  - what Google enforces on 2027-02-01, and for which clients;
  - which tools send confirmation requests, and how long one stays open;
  - whether Google's MCP tools carry `readOnlyHint`;
  - whether a client that does not advertise elicitation is refused;
  - whether revoking one refresh token ends every token of the account's
    grant to the client.
- **M1, before 2027-02-01.**
  0. Mask worker log streams. `setupRemoteWriters` writes step output to
     the coordinator without `MaskingWriter`. This is an existing bug that
     already exposes `secrets` values, fixed first and separately.
  1. Records with both scopes, the broker, the coordinator RPC with the
     worker version check, and the tool catalog.
  2. Google authorization code in server callback, loopback, and paste
     modes: sessions and confirmation, sign-in by someone else, consent
     error fixes, the REST API, the Connections page, the Google wizard, and
     `dagu connection add`, test, reconnect, and share.
  3. `mcp.call` with connections and the Google presets, with the
     `rate_limited` and `confirmation_required` kinds.
  4. RFC 026's gateway and harness steps.
- **M2.**
  - Microsoft: device code and client credentials.
  - Service account connections.
  - Mail: `connection` in mail accounts, a DAG's `smtp`, and the
    notification SMTP settings, the Google and Microsoft `mailbox` presets,
    `dagu connection import`, and the deprecation of `oauth` blocks. This
    fixes Microsoft rotation for mail, and leaves mail with one refresh
    path.
  - `http.request` with bound hosts.
  - Token export, behind `allow_token_export`.
  - Authoring: editor completion, the tool browser and Try it, Used by from
    the DAG index, Add service, paired Google connections, the expiry
    display, templates, and `dagu_read`.
  - Notifications: the `connection.*` events, run records of the
    connection and account, and skipping the run failure messages a broken
    connection causes.
- **M3.**
  - Any MCP server: discovery, registration, and the gallery tile.
  - Cloud relay, once Dagu Cloud serves its endpoints.
  - RFC 026's agent DAGs and chat steps.

## Verification and acceptance criteria

Connect:

1. On a fresh server with a public URL, an admin follows the Google wizard,
   uploads a client JSON, consents, and lists Gmail tools without leaving
   the UI except for the Google Cloud console and the consent screen. A
   manager does the same with an app an admin registered.
2. `dagu connection add google` works in each mode: in a desktop session
   (loopback), over SSH to a server with a public URL (server callback), over
   SSH to a licensed server without one (Cloud relay), and over SSH to an
   unlicensed server without one (paste).
3. In server-callback mode started from the CLI over SSH, a browser with no
   Dagu session completes consent, and only the user who started the
   session can confirm it.
4. A licensed server without a public URL connects from the UI through the
   Cloud relay. Dagu Cloud's stored data then holds no token or client
   secret, only the code and the encrypted state, and nothing once the
   server fetched them. Another license cannot fetch the callback.
5. Each consent error in [Consent errors](#consent-errors) shows its fix.
6. A person without a Dagu login consents through a copied sign-in link,
   and the starter confirms the account.
7. A Microsoft account from another tenant that claims the expected email
   is not confirmed without asking.

Scopes:

8. A project connection serves only its project's DAGs, locally and on a
   worker.
9. A server connection that lists two projects serves both from one token
   state record. Dropping a project from it stops that project's runs at
   admission.
10. A name that would collide in a project is refused when the connection is
    created and when the project is listed.
11. After RFC 003's migration, each connection exists once, and its refresh
    keeps working.
12. Removing one of two connections that share an account and client leaves
    the other working.
13. A project that owns connections, or that a server connection lists,
    cannot be deleted.

Broker:

14. With 100 concurrent runs using one connection whose access token has
    expired, the provider receives exactly one refresh request, and only one
    process takes the connection's lock.
15. When that refresh fails transiently, the provider still receives one
    request, and the waiting runs fail with its error.
16. A rotated refresh token is saved, and the next refresh uses it, across
    process restarts.
17. A reconnect confirmed during a refresh leaves the reconnected token in
    place. Runs keep the previous token until the reconnect is confirmed.
18. A connect session written during a Microsoft refresh keeps the rotated
    refresh token. A connection removed during a refresh stays removed, and
    a new connection with its name starts without tokens.
19. A token endpoint that hangs for one connection does not delay another
    connection's refresh.
20. Two hosts sharing the data directory both keep working after one of them
    rotates a Microsoft refresh token.
21. One Microsoft connection serves a Graph `http.request` and an IMAP mail
    action in the same run, each with a token for its own resource.
22. A token that `mcp.call`, `http.request`, or a mail server rejects before
    it expires is refreshed once, and the call succeeds on the retry.
23. After an upgrade that adds a scope to a preset, existing connections keep
    refreshing.

Mail:

24. Mail accounts and a DAG's `smtp` send through a Google and a Microsoft
    connection, locally and on a worker, and the notification SMTP settings
    send through a server connection.
25. A base config whose mail account names a connection that one project
    cannot use leaves that project's DAGs that send no mail unaffected.
26. No mail account or `smtp` with a connection sends the token to a host
    outside its preset or without a verified certificate.
27. A mail address that does not fit a Google user connection's account, or
    a service account's `subject`, fails, naming both.
28. A Microsoft mail account imported with `dagu connection import` from a
    secret keeps working after the provider rotates the refresh token.

Failures and notifications:

29. Revoking access at the provider makes the connection `needs_reconnect`
    once. Every later step fails without calling the token endpoint.
    Reconnect restores it.
30. While 100 runs fail on a revoked connection, each channel receives
    exactly one `connection.needs_attention`, also when the routes of several
    projects reach it, and a reconnect sends one `connection.restored`. A
    channel that receives connection events sends no run failure messages
    for those runs, also when its monitor catches up after the reconnect. A
    channel that does not receive them sends every run failure.

Security:

31. No refresh token, access token, or client secret appears in any API
    response, CLI output, MCP result, run status, step log, worker log
    stream, or audit entry. A test searches each of them for the values.
32. `http.request` with a connection, locally and on a worker, never sends
    the token to a host outside the bound hosts or over plain HTTP,
    including through a redirect.
33. A run on a worker cannot obtain a token for a connection its DAG does
    not declare, or one its project may not use.
34. Each role can do what [Permissions](#permissions) lists and nothing
    more.

Steps:

35. An `mcp.call` step with `connection` and `server: gmail` succeeds, both
    locally and on a worker with no shared filesystem.
36. A Google tool that sends a confirmation request fails with
    `confirmation_required` in `mcp.call`, and Try it shows its form. No path
    accepts a request without a person.

All:

37. Every case in [Failure behavior](#failure-behavior) ends as stated.

## Alternatives considered

- **Storing tokens in the secret store.** It would give encryption, masking,
  and coordinator resolution for free. But every write adds a version, and
  Microsoft rotates refresh tokens on every refresh. The secret RPC returns
  a stored value and cannot refresh. And a secret shared across projects is
  copied into each one at the v3.0 migration, which a rotating refresh token
  cannot survive.
- **Refresh tokens in YAML or base config, as mail does today.** No consent
  flow, no place to save a rotated token, and a long-lived credential in a
  file that is often under version control.
- **Moving only mail accounts to connections.** Smaller. But a DAG's
  `smtp` and the notification SMTP settings would keep their own refresh
  path, with refresh tokens in YAML and settings, and Microsoft rotation
  would still break them.
- **Static headers with a token refreshed by a command step.** Works today,
  but the token passes through step output. It expires during long steps,
  and every DAG repeats the plumbing.
- **A separate session store for the connect flow.** Needs a store shared
  by frontend replicas. The token state record already is one, and the
  encrypted state carries the PKCE verifier.
- **Project connections only.** One kind instead of two. But an account
  that several projects share would need a copy, or a consent, per project.
  A copy breaks when the provider rotates the refresh token, and separate
  consents mean reconnecting each one.
- **Server-wide connections that managers manage, with an allow-list on the
  connection.** One kind instead of two. But a manager could change what
  another project's DAGs act as, which Spec 102 rules out, and the
  connection, not its owner, would decide where it is used.
- **The scope written in YAML, such as `server:shared-mail`.** Explicit. But
  moving a connection between kinds would mean editing every DAG that names
  it.
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

1. Should a project manager be able to ask for a server connection for
   their project in Dagu, for an admin to approve, rather than asking
   outside it?
2. Should a second connection for the same account and client be refused,
   rather than warned about?
3. Should a later Dagu-hosted app use Client ID Metadata Documents, which
   newer MCP authorization specs describe, rather than registration? This
   needs checking against the current MCP spec.
4. Does Microsoft keep a rotated-out refresh token valid until it expires?
   The re-read guard works either way, but the docs should state the
   provider's behavior only once it is confirmed.
5. Should hosts that share storage pick one refresh owner, for providers
   that revoke a token family on reuse? Cloud-held connections would be one
   answer, for teams that accept them.
6. Do Google's Workspace MCP servers serve personal `@gmail.com` accounts,
   or only Workspace accounts? The wizard should say so before the user
   starts the Google Cloud setup.
7. Which Google tools send confirmation requests? They fail in `mcp.call`
   with `confirmation_required`, so the docs should list them.

## Future enhancements

- **Cloud-held connections and a Dagu-hosted OAuth app,** as [Dagu
  Cloud](#dagu-cloud) describes. Both need their own RFC, and cloud-held
  connections amend RFC 002.
- **Personal connections,** with a run-as-user model.
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
