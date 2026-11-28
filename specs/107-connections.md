# Spec 107: Connections

## Status

Partly implemented.

Behavior lands in parts, tracked in #4217. Each part that implements a
section of this spec updates this status, adds conformance tests for what
the binary shows, and updates the spec that owns the step or setting it
changes (Specs 044, 045, 069, 073, and 098).

Implemented so far:

- [Declared Connections](#declared-connections) for `mcp.call`'s
  `connection` and `server`, and for `secrets` entries with
  `provider: connection`.
- The `connection` secret provider
  ([Rules per Consumer](#rules-per-consumer)), on a local run and on a run
  on a worker. Its `options.service` must name one of the connection's
  services. A Microsoft connection, whose service selects its resource,
  exports no token until the Microsoft presets land.
- [Admission Checks](#admission-checks), except the literal `http.request`
  URL check, on a local run from the CLI or the embedded engine, and on a
  run on a worker, including the embedded engine's worker, through the
  check-only request.
- [Workers](#workers): the coordinator answers token, check-only, and
  rejected-token requests, and dispatches a DAG that declares connections
  only to workers that make them.
- [Using a Connection](#using-a-connection),
  [Confirmation Requests](#confirmation-requests), and [Masking](#masking)
  for `mcp.call`, in a local run and on a worker. Its
  [Run Records](#run-records) are not written yet. Until Try it and
  `http.request` with `connection` land, the `confirmation_required` error
  names only the Workspace admin's confirmation rules and `http.request`
  with a token from a `provider: connection` secret.
- The [Connect Flow](#connect-flow) for `google` inside the server: the
  authorization request, the server callback, confirmation, the
  [checks after consent](#checks-after-consent), and the
  [consent errors](#consent-errors), in server callback, loopback, and
  paste modes.
- [REST API](#rest-api): `/connection-providers`, `/oauth-apps`,
  `/connections`, `/connections/{name}/authorization` with its complete,
  confirm, and cancel, `POST /connections/{name}/test`, and
  `GET /connections/{name}/tools`, with their
  [Permissions](#permissions) and [Audit](#audit), the audit of
  revocations, and [Deleting a Project](#deleting-a-project) for a
  workspace, which also cannot be renamed while it owns connections. The
  API takes only `google` and `microsoft` apps, since no request sets an
  app's `endpoints` or a connection's `resource` yet, and creates a
  connection only for the `authorization_code` grant. A server callback
  connect confirmed at once is audited only at its start, since the
  callback has no Dagu user.

The tests against Google also settle whether Docs, Sheets, and Slides
answer `NOT_FOUND` rather than `PERMISSION_DENIED` for a resource ID that
does not exist, which their probes rely on.

The tests against Google settle one provider fact this spec depends on
(RFC 025, criterion 42): whether revoking one Google refresh token ends the
account's whole grant to the client. Google documents that revoking a
token ends the account's grant to every client of the project, so until
they record otherwise,
[Token Cleanup](#token-cleanup) leaves Google's unused tokens unrevoked, and
[Remove](#remove) revokes a Google refresh token at
`https://oauth2.googleapis.com/revoke`. A token that Google answers with
`invalid_token` is already unusable and counts as revoked.

[Service Presets](#service-presets) lists the scope sets of the Google
presets only. The parts that compile the Microsoft presets (C20 and C21)
add their scope sets to that section.

A DAG that names no connection behaves as it does without this spec, with
these exceptions:

- An `mcp.call` that receives HTTP 429 fails with kind `rate_limited`, not
  `transport`, whether or not it uses a connection. A DAG that branches on
  `transport` to catch rate limits must also check `rate_limited`.
- The stdout and stderr logs of every step of a run with at least one
  secret mask text that matches a Google token pattern. See
  [Masking](#masking).

An `mcp.call` without a connection advertises no elicitation capability, as
Spec 098 states.

## Scope

This spec covers:

- the records of OAuth apps, connections, token state, and tool catalogs,
  and their fields
- connection names, and which project owns a connection
- the notification connection, which belongs to no project
- providers and service presets, and how a connection's scopes and bound
  hosts are derived
- the connect flow: sessions, callbacks, confirmation, redirect modes, the
  Cloud relay, checks after consent, reconnect, import, and remove
- the token broker's guarantees, refresh results, and token delivery to
  workers
- the binding: which hosts receive a token, and over what transport
- declared connections, run admission checks, checks at use, run records,
  and masking
- the rules every step that uses a connection follows, and confirmation
  requests
- the `connection.*` events and how they reach notification channels
- the REST paths and CLI commands that manage connections
- permissions and audit
- failure behavior

This spec does not define:

- the fields of `mcp.call`, `http.request`, mail accounts, `smtp`, and the
  `connection` secret provider. Specs 098, 045, 044, 073, and 069 define
  them. This spec defines only the connection rules those fields follow.
- RFC 026's MCP gateway, its harness, agent DAG, and chat steps, and the
  tool catalog RPC for workers
- Dagu Cloud's relay endpoints. Dagu Cloud defines them; this spec defines
  only what a server sends and expects.
- the wizard's pages and wording beyond the contracts in
  [User Interface](#user-interface)
- projects themselves, and the v3.0 migration beyond connections (Spec 102)
- users, authentication methods, and what each role may do outside
  connections
- a Dagu-hosted OAuth app, connections that Dagu Cloud keeps, personal
  connections, sharing a connection across projects, MCP sampling and
  URL-mode elicitation, inbound OAuth for Dagu's own MCP server, and
  creating Google Cloud projects or OAuth clients

## Goal

A user connects an account once, from the UI, the CLI, or the API, and
consents in a browser. Steps, mail accounts, and email notifications then
refer to the connection by name. YAML never holds a secret.

Dagu stores the tokens encrypted, refreshes a token once on a host however
many runs reach its expiry together, saves a rotated refresh token,
and sends a token only to the hosts the connection is bound to. An expired
or revoked sign-in is reported once per channel, names the connection, and
offers Reconnect. Connections work in one process, behind several frontend
replicas, and for runs on workers that share no filesystem with the server.

## Decisions

| ID | Closed | Decision |
| --- | --- | --- |
| G1 | 2026-11-26 | `mcp.call` declines a server's confirmation request at once and fails with kind `confirmation_required`. Holding the call open for a person's answer is a future enhancement. See [Confirmation Requests](#confirmation-requests). |
| G2 | 2026-11-26 | A service account or client credentials connection serves one mailbox, its `subject`. Only admins add Microsoft client credentials connections. The notification connection is managed by whoever may change the notification settings. See [Ownership](#ownership) and [Permissions](#permissions). |

## Related Specs

- [RFC 025: Connections](../rfcs/025-connections.md) gives the design and
  its reasons.
- [RFC 026: MCP Tools for AI Steps](../rfcs/026-mcp-tools-for-ai-steps.md)
  adds AI steps that use connections.
- [RFC 002: Dagu Cloud Connector](../rfcs/002-dagu-cloud-connector.md)
  defines the license that the Cloud relay uses.
- MCP read tool: [Spec 021](021-mcp-read-tool.md)
- Mail send: [Spec 044](044-mail-send.md)
- HTTP request: [Spec 045](045-http-request.md)
- Secrets providers: [Spec 069](069-secrets-providers.md)
- Mailbox actions: [Spec 073](073-mailbox-actions.md)
- MCP call: [Spec 098](098-mcp-call.md)
- Projects: [Spec 102](102-projects.md)

## Terms

`<data_dir>` is `paths.data_dir`.

A project is the boundary that owns connections. Before v3.0 a project is
a workspace, and the `.global` workspace serves DAGs without a `workspace`
label. From v3.0 a project is a project of Spec 102.

An OAuth app, or app, is a client registration at a provider that several
connections share.

A connection is one authorized account at a provider, used by name. It is
stored as a metadata record and a token state record.

The notification connection is the one connection that the notification
SMTP settings own. It belongs to no project.

A grant is the authorization a provider issues to one connect: its refresh
token, or, for a service account or client credentials, the app's key.

A resource is a server that accepts a connection's access tokens. A Google
or `mcp` connection has one. A Microsoft connection has one per Microsoft
resource it calls, such as Graph and Outlook.

A connection's bound hosts are the hosts that may receive its access
tokens: its presets' hosts plus the hosts it adds. Its binding is its
provider, services, bound hosts, resource URL, and whether it allows token
export. A binding holds no secret.

A session is a connect or reconnect in progress. Its starter is the user
who started it.

The broker is the part of Dagu that hands out access tokens. A host is one
machine. A local process is a process that has `<data_dir>` and the
encryption key: the server, the scheduler, a local run, or a coordinator.
A worker has neither.

## Behavior

### Records

#### Storage

- Dagu keeps four collections under `<data_dir>`: `oauth-apps/`,
  `connections/`, `connection-tokens/`, and `connection-catalogs/`.
- Each collection directory is created with mode `0700`, readable only by
  the Dagu user.
- Client secrets, service account keys, the API keys and static
  credentials of Specs 093 and 095, refresh tokens, access tokens, and
  a session's tokens are stored only encrypted, with the server's key:
  `DAGU_ENCRYPTION_KEY`, or `auth/encryption_key` in `<data_dir>`.
- No record of this spec is stored in the secret store.
- Before v3.0 every connection is in these collections, keyed by its
  project and name. From v3.0 a project's connections are in its state
  directory, as [v3.0](#v30) describes. The notification connection stays
  in `<data_dir>`.

#### OAuth App

| Field | Values and rules |
| --- | --- |
| `id` | Assigned on create. Never changes |
| `name` | Shown in pickers |
| `provider` | `google`, `microsoft`, `oauth2`, `mcp`, or `model`. A `model` app is a shared key of [Spec 093](093-model-connections.md#shared-keys) |
| `kind` | `byo`: a client the user registered. `cimd`: this server's Client ID Metadata Document. `dcr`: a client Dagu registered through RFC 7591. `hosted` is reserved and refused |
| `client_id` | Required, except for a `model` app |
| `client_secret` | Encrypted. Optional for a `public` client. For `service_account`, the service account's private key |
| `client_type` | `web`, `desktop`, `public`, or `service_account`. Decides the grants and redirect modes that work |
| `agent` | True when the user attests that the client has Google's "Use this client for an AI-powered agent" set |
| `tenant` | `microsoft` only: `common`, `organizations`, or a tenant ID. Client credentials need a tenant ID |
| `endpoints` | `oauth2` and `mcp` only: the authorize, token, revoke, and device URLs |
| `pkce` | `oauth2` only. On by default. An admin may turn it off for a provider that rejects PKCE |
| `status` | `ok` or `invalid` |
| `status_reason` | The provider's error code and text when `invalid`, without secrets |

- A new app has `status: ok`.
- Only the broker sets `invalid`, as [Refresh Results](#refresh-results)
  states. Only an admin's change to `client_id` or `client_secret` sets it
  back to `ok`. A `model` app follows the 401 and rotation rules of
  [Spec 093](093-model-connections.md#connection-status) instead.
- An app that a connection uses cannot be deleted.
- A `dcr` app belongs to the one connection that registered it. It is
  created and deleted with that connection, and no other connection may use
  it.
- A server has at most one `cimd` app. It holds no secret.

#### Connection Metadata

| Field | Values and rules |
| --- | --- |
| `name` | See [Names](#names) |
| `project` | The owning project. Empty for the notification connection |
| `provider` | The app's provider. A connection with no app names its own: `model` or `mcp` |
| `app` | The app's `id`. Empty for an `api_key` connection with its own key, and for the grants `static` and `none` |
| `grant` | `authorization_code`, `device_code`, `client_credentials`, or `service_account`. [Spec 093](093-model-connections.md#credentials) adds `api_key` and `none`, and [Spec 095](095-mcp-servers.md#static-credentials) adds `static` |
| `services` | Presets, such as `gmail`, each with `read` or `write` |
| `scopes` | Scopes added beyond the presets' scopes. The presets' own scopes are not stored |
| `subject` | Service account: the impersonated user. Microsoft client credentials: the mailbox. Set only by an admin |
| `bound_hosts` | Hosts added beyond the presets' hosts. Required for `oauth2` |
| `resource` | `mcp` only: the server's canonical URL (RFC 8707) |
| `allow_token_export` | Off by default. Set only by an admin |
| `holder` | `local`. `cloud` is reserved and refused |

- A metadata change by a person never changes token state, except a
  change of `subject`, as [Status Transitions](#status-transitions) states,
  and a URL change that Specs 093 and 095 pair with a new credential.
  A token refresh never changes metadata.

#### Token State

| Field | Values and rules |
| --- | --- |
| `refresh_token` | Encrypted. Absent for `service_account` and `client_credentials` |
| `access_tokens` | Per resource: one encrypted access token and its expiry |
| `refresh_token_expires_at` | Only when the provider reports it |
| `granted_scopes` | The scopes the provider granted. May be fewer than requested |
| `account` | The account's display name, such as an email address. Shown to people |
| `account_id` | The provider's immutable account ID: Google's `sub`, or Microsoft's tenant and object IDs |
| `status` | `pending`, `connected`, or `needs_reconnect`. See [Status Transitions](#status-transitions) |
| `status_reason` | The provider's error code and text, without secrets |
| `generation` | Starts at 0. Increases by 1 on each confirm, on an import, and on each successful token fetch without a session: at a create, a reconnect, or a change of `subject` |
| `session` | The session in progress, if any. See [Sessions](#sessions) |
| `confirmed_session` | The ID of the last confirmed session, so that its handle reads `connected` after the confirm clears the session |
| `refreshed_at` | Time of the last successful refresh |
| `import_hash` | Only for an imported connection: a hash of the refresh token the import was given. See [Import](#import) |

- Only the connect flow and the broker write token state.
- `account` and `account_id` are written only with tokens, in the same
  write: by a confirm, from the session; by an import, from the provider;
  and by a `service_account` or `client_credentials` token fetch, at
  create, reconnect, or a change of `subject`, where both are the
  connection's `subject`. A refresh never changes them.
- A connection's account is compared with a session's, or with another
  connection's, by `account_id`, never by `account`. An expected account is
  compared with the email the provider vouches for, as
  [Confirmation](#confirmation) states.
- Creating a connection replaces any token state and tool catalogs left
  under its name, so a new connection never has an earlier connection's
  tokens and never reads its catalogs.
- The broker never creates a token state record.

#### Status Transitions

| From | To | When |
| --- | --- | --- |
| none | `pending` | The connection is created. It has no tokens |
| none | `connected` | An import succeeds, as [Import](#import) states. `generation` is 1 |
| `pending` | `connected` | A session is confirmed, for `authorization_code` and `device_code`. For `service_account` and `client_credentials`, the first token fetch succeeds |
| `connected` | `connected` | A reconnect is confirmed, or its token fetch succeeds for a grant without a refresh token. `generation` increases |
| `connected` | `needs_reconnect` | A refresh fails with `invalid_grant`, as [Refresh Results](#refresh-results) states |
| `needs_reconnect` | `connected` | A reconnect is confirmed |
| `pending` or `connected` | `connected` | An admin changes the `subject` of a `service_account` or `client_credentials` connection, and the new token fetch succeeds. `generation` increases |
| `pending` or `connected` | `pending` | An admin changes the `subject`, and the new token fetch fails. `status_reason` holds the provider's error |

- No other transition exists, except those that Spec 093 and Spec 095 add
  for the grants `api_key`, `static`, and `none`. Remove deletes the
  records and tool catalogs in any status.
- A connection whose token state is missing or cannot be decoded or
  decrypted has no status of its own. The API shows it as `broken`, as
  [REST API](#rest-api) states.
- A `service_account` or `client_credentials` connection fetches a token
  when it is created, with no session. When the fetch fails, the
  connection stays `pending` with the provider's error in
  `status_reason`. A reconnect of such a connection repeats the fetch. A
  failed fetch on reconnect changes no status.
- A change of `subject` discards the stored access tokens and repeats the
  token fetch for the new `subject`, as a reconnect replaces tokens. The
  account fields then name the new `subject`. After the change, neither the
  broker nor the coordinator hands out a token minted for the old
  `subject`. A run that already holds one, locally or on a worker, keeps
  using it until the token falls below the `minTTL` asked for or is
  rejected, as runs do after a reconnect.
- A `device_code` connect runs a session and needs the starter's confirm,
  as the other modes do.
- A grant without a refresh token never becomes `needs_reconnect`, except
  `api_key` and `static`, whose stored credential a provider can reject.
  A rotation of the credential clears it, as Specs 093 and 095 state.
- A step that uses a `pending` connection fails at once, without calling
  the provider, with `reconnect_required` and a reason saying the
  connection was never connected.

#### Tool Catalog

- For each connection and MCP service, a catalog holds the tools the
  service lists: each tool's name, description, input schema,
  annotations, and hash, and the time of the listing. It holds no secret.
- The UI, the editor, the API, and AI steps read tools from the catalog.
- A reader takes a catalog up to 10 minutes old. A reader that finds the
  catalog older, or missing, lists the service again with `tools/list`.
- On one host, at most one listing of a catalog runs at a time. Readers that
  arrive during a listing wait for it and read its result.
- A failed listing returns the provider's error to the reader and keeps the
  previous catalog. For 30 seconds, the error answers the readers of the
  listing's process without a new listing. A reader in another process
  that waited for the listing finds no new catalog and lists again, one
  process at a time.
- A listing fails, and keeps the previous catalog, when the service lists
  more than 1,000 tools or 1,000 pages, or describes one tool in more than
  64 KiB.
- A listing follows [Binding](#binding): it sends the token only over
  HTTPS to a bound host, and follows a redirect only to HTTPS on the same
  host and port. A 401 and a 403 are handled as
  [Rejected Tokens](#rejected-tokens) states.

### Names

- A connection name matches `^[a-z0-9][a-z0-9-]{0,62}$`: 1 to 63
  lowercase ASCII letters, digits, and hyphens, starting with a letter or
  digit, so a name is never read as a CLI flag.
- A name is unique in its project. Two projects may each have a connection
  of the same name.
- A name never changes.

| Name | Valid | Reason |
| --- | --- | --- |
| `work-google` | yes | |
| `ops-mail-2` | yes | |
| `2fa-backup` | yes | |
| `-ops` | no | starts with `-` |
| `Work-Google` | no | uppercase |
| `work_google` | no | `_` is not allowed |
| `work.google` | no | `.` is not allowed |
| 64 letters | no | longer than 63 characters |

### Ownership

- A connection belongs to exactly one project, or is the notification
  connection.
- A name in a DAG resolves to its run's project's connection of that name,
  and to nothing else.
- A `.global` connection serves only DAGs without a `workspace` label. It
  is not a fallback for any other workspace, unlike a `.global` secret.
- A connection is never shared or copied between projects. A project that
  needs an account another project connected connects it again, with its
  own consent and refresh token. A reconnect or removal in one project
  leaves the other working.
- Service account and Microsoft client credentials connections are added
  only by admins, in any project. Their key stays in the app, so connections
  in several projects can share one key, and rotating it is one change to
  the app.
- A service account or client credentials connection acts as one mailbox,
  its `subject`. A second mailbox needs a second connection on the same app.
- OAuth apps are server-wide. A connection in any project may use any app
  of its provider, except a `dcr` app of another connection, and a `model`
  app, which only Share of [Spec 093](093-model-connections.md#shared-keys)
  attaches to a project.

#### The Notification Connection

- The notification SMTP settings own one connection. It belongs to no
  project.
- No DAG can name it. A DAG's `smtp` resolves in its run's project, as a
  step does.
- Whoever may change the notification settings manages it: `developer` or
  above before v3.0, and `admin` from v3.0.

#### Deleting a Project

- Deleting a project that owns connections is refused, and the error names
  them. Removing them first allows the delete.

#### v3.0

- The v3.0 migration moves each connection, with its token state and
  catalogs, into its project's state directory. A `.global` connection
  moves into the default project.
- The migration copies no connection. Each connection exists once after it,
  and its refresh keeps working.
- The notification connection stays server-wide.

### Providers

| Provider | Grants | Rules |
| --- | --- | --- |
| `google` | `authorization_code`, `service_account` | A service account uses domain-wide delegation and needs no agent flag |
| `microsoft` | `authorization_code`, `device_code`, `client_credentials` | `tenant` on the app. Refresh tokens rotate. Each access token serves one resource |
| `oauth2` | `authorization_code`, `client_credentials` | Endpoints, scopes, and `bound_hosts` are required. PKCE is on unless the app's `pkce` turns it off |
| `mcp` | `authorization_code`, `static` | Discovery and client registration as [Any MCP Server](#any-mcp-server) states. A fixed token or headers as [Spec 095](095-mcp-servers.md#static-credentials) states |
| `model` | `api_key`, `none` | A model API, as [Spec 093](093-model-connections.md) states |

### Service Presets

A preset names one service of a provider. It gives the service's MCP URL,
REST hosts, IMAP and SMTP hosts, scope sets for `read` and `write`, and,
for Google, the API IDs to enable. Presets are compiled into the binary.
`GET /connection-providers` lists them.

Google presets:

| Service | MCP URL | REST hosts | APIs to enable |
| --- | --- | --- | --- |
| `gmail` | `https://gmailmcp.googleapis.com/mcp/v1` | `gmail.googleapis.com` | `gmail.googleapis.com`, `gmailmcp.googleapis.com` |
| `calendar` | `https://calendarmcp.googleapis.com/mcp/v1` | `www.googleapis.com` | `calendar-json.googleapis.com`, `calendarmcp.googleapis.com` |
| `drive` | `https://drivemcp.googleapis.com/mcp/v1` | `www.googleapis.com` | `drive.googleapis.com`, `drivemcp.googleapis.com` |
| `docs` | `https://docsmcp.googleapis.com/mcp/v1` | `docs.googleapis.com` | `docs.googleapis.com`, `docsmcp.googleapis.com` |
| `sheets` | `https://sheetsmcp.googleapis.com/mcp/v1` | `sheets.googleapis.com` | `sheets.googleapis.com`, `sheetsmcp.googleapis.com` |
| `slides` | `https://slidesmcp.googleapis.com/mcp/v1` | `slides.googleapis.com` | `slides.googleapis.com`, `slidesmcp.googleapis.com` |
| `chat` | `https://chatmcp.googleapis.com/mcp/v1` | `chat.googleapis.com` | `chat.googleapis.com`, `chatmcp.googleapis.com` |
| `people` | `https://people.googleapis.com/mcp/v1` | `people.googleapis.com` | `people.googleapis.com` |
| `search` | `https://workspacemcp.googleapis.com/mcp/v1` | none | `workspacemcp.googleapis.com` |
| `mailbox` | none | none. IMAP `imap.gmail.com`, SMTP `smtp.gmail.com` | none |

- A preset's hosts are its MCP URL's host, its REST hosts, and its IMAP
  and SMTP hosts.
- Scope sets follow each product's MCP setup guide, which lists every
  scope its tools use. `read` asks for the scopes that allow no change.
  `write` asks for those and adds the rest. Each scope below is under
  `https://www.googleapis.com/auth/`:

| Service | `read` | `write` adds |
| --- | --- | --- |
| `gmail` | `gmail.readonly` | `gmail.compose`, `gmail.modify` |
| `calendar` | `calendar.calendarlist.readonly`, `calendar.events.freebusy`, `calendar.events.readonly` | `calendar.events` |
| `drive` | `drive.readonly` | `drive.file` |
| `docs` | `drive.readonly`, `documents.readonly` | `drive.file`, `documents` |
| `sheets` | `drive.readonly`, `spreadsheets.readonly` | `drive.file`, `spreadsheets` |
| `slides` | `drive.readonly`, `presentations.readonly` | `drive.file`, `presentations` |
| `chat` | `chat.spaces.readonly`, `chat.memberships.readonly`, `chat.messages.readonly` | `chat.messages.create`, `chat.users.readstate`, `chat.users.spacesettings` |
| `people` | `directory.readonly`, `userinfo.profile`, `contacts.readonly` | none |
| `search` | `gmail.readonly`, `drive.readonly`, `calendar.readonly`, `chat.messages.readonly`, `chat.spaces.readonly`, `chat.memberships.readonly` | none |

- `chat` with `read` cannot read notification settings: Google's
  `get_notification_settings` tool needs `chat.users.spacesettings`, which
  also allows changing them.
- `mailbox` asks for `https://mail.google.com/`. `gmail` does not.
- Every Google `authorization_code` connection also asks for `openid` and
  `email`, so Dagu can show the account. A `service_account` connection
  asks for neither: its account is its `subject`, and domain-wide
  delegation fails the token request for any scope the admin did not
  authorize for the client.
- `people` and `search` are in Google's Workspace Developer Preview. They
  work only for an account in Google's preview program, and the presets
  mark them as preview.

Microsoft presets:

| Service | Resource | Hosts that receive the token |
| --- | --- | --- |
| `mail` | Graph | `graph.microsoft.com` |
| `calendar` | Graph | `graph.microsoft.com` |
| `files` | Graph | `graph.microsoft.com` |
| `mailbox` | Outlook | IMAP `outlook.office365.com`, SMTP `smtp.office365.com` |

- `mailbox` asks for different scopes for a delegated grant and for client
  credentials.
- Every Microsoft connection with a user also asks for `offline_access` and
  `User.Read`.
- The broker keeps one access token per Microsoft resource, and refreshes
  each with the scopes granted for that resource.

#### Derived Scopes and Hosts

- A connection's requested scopes are its presets' scopes for the chosen
  `read` or `write`, plus its `scopes`. Its bound hosts are its presets'
  hosts, plus its `bound_hosts`, plus the host of its `resource`, as
  `host:port` when the resource URL names a port. Both are
  derived each time they are read.
- A `bound_hosts` entry is a hostname, or `host:port`. It has no scheme,
  path, or wildcard.
- An upgrade that adds a scope to a preset reaches every connection on
  read. The service then shows the missing scope until a reconnect.
- A refresh asks only for granted scopes, so such an upgrade never breaks a
  refresh.

### Connect Flow

The UI, the CLI, and the API run the same connect flow.

```text
starter (UI or CLI)          Dagu server                       provider
   | POST .../authorization    |                                   |
   |-------------------------->| session: waiting                  |
   |<-- authorization URL -----| state = enc{conn, session, PKCE   |
   |                           |   verifier, expiry}               |
   |  browser ------------------------------------------------------>| consent
   |                           |<-- GET /oauth/callback?code&state-|
   |                           |--- exchange code with verifier -->|
   |                           |--- identity, service probes ----->|
   |                           | session: confirm, with account    |
   | GET .../authorization     |                                   |
   |-------------------------->| confirm (account) | failed        |
   | POST .../confirm          |                                   |
   |-------------------------->| tokens live, generation + 1       |
```

#### Sessions

- A session has an ID, its starter, and a stored phase: `waiting`,
  `confirm`, or `failed`. In `confirm` it also holds the account,
  `account_id`, and the new tokens, encrypted.
- In `confirm` a session also holds `probes`, each service's result of the
  [checks after consent](#checks-after-consent), and `duplicates`, the
  names of the connections that the duplicates check found. Its status
  also carries the provider's advice on the refresh token's expiry, when
  the provider reports one. While `waiting`, it holds
  `claimed_at`, the time a callback began to complete it. Other callbacks
  are refused until that claim ends, so one code is exchanged per session.
- A session lives in the connection's token state, so any frontend replica
  serves any request of it.
- Starting a session replaces an earlier one. The earlier session fails.
- A session expires 10 minutes after it starts. An expired session changes
  nothing.
- A session in progress never changes the live tokens. Runs keep using the
  previous tokens until a confirm.

#### Authorization Request

- Every authorization request carries a `state` parameter that holds the
  connection, the session ID, the PKCE verifier, and the expiry, encrypted
  with the server's key.
- Every authorization request uses PKCE with `S256`, except for an
  `oauth2` app whose `pkce` is off. Its `state` then holds no verifier.
- A Google request also sends `access_type=offline` and `prompt=consent`,
  so the provider returns a refresh token on every connect.
- An `mcp` request also sends `resource` (RFC 8707).

#### Callback

- `GET /oauth/callback` is a public route. It needs no Dagu login, since the
  browser that consents may have none.
- The callback exchanges the code only while the session named in `state`
  is `waiting`, and moves the session to `confirm` in one atomic write. A
  second callback for the same session is refused.
- A callback with a `state` that fails to decrypt, that has expired, or
  that names a session other than the current one is refused, and changes
  nothing.
- The callback page shows no token, and closes the popup that opened it.
- `GET /oauth/callback` completes only a server callback session. A
  loopback or paste session completes only through
  `POST .../authorization/complete` for its own connection.

#### Confirmation

- Only the starter confirms a session. A confirm by another user is refused,
  and the session stays in `confirm`.
- With authentication off, any API caller may confirm.
- A confirm is one atomic write of the token state record. It makes the
  session's tokens live, sets `account` and `account_id` to the session's,
  increases `generation` by 1, sets `status: connected`, and clears the
  session. No reader sees the new tokens with the previous account.
- A session that nobody confirms expires, and the previous tokens stay
  live.
- The starter may name an expected account, an email address, when
  starting. It is compared, without regard to case, with the email that the
  provider vouches for: a Google email that Google reports as verified, or
  the email of a Microsoft account in the tenant ID that the app names. A
  session whose vouched email matches is confirmed without asking.
  Otherwise the starter confirms as usual. The comparison folds the case
  of ASCII letters only.
- A session confirmed without asking shows no checks of its own, and its
  duplicates warning is not shown. Its
  services' results come from `POST .../test`, and its granted scopes from
  the connection.
- The confirmation shows the account, and for Microsoft its tenant.

#### Sign-In by Someone Else

- In server callback and Cloud relay modes, the starter may give the
  authorization URL to the account's owner. The owner consents in their own
  browser without a Dagu login, and the starter confirms.

#### Redirect Modes

| Mode | Used when | Redirect URI | Client type |
| --- | --- | --- | --- |
| Server callback | The server has a usable public URL | `<public_url>/oauth/callback` | `web` |
| Cloud relay | No usable public URL, and an online license whose Dagu Cloud serves the relay | `<cloud_url>/oauth/callback` | `web` |
| Loopback | The CLI runs where a browser can open | An `http://127.0.0.1` URL on a random port that the CLI listens on | `desktop` |
| Paste | None of the above | A loopback URL that fails to load. The user pastes it into the UI or CLI | `desktop` |
| Device code | Microsoft only, when the caller asks for it | None. The CLI shows a code to enter on another device | `public` |

- The usable public URL is `server.public_url`, else the tunnel URL, else
  the UI's origin when the browser reaches Dagu at `localhost` or a
  loopback address, which Google accepts over HTTP. The server reads that
  origin from the request's host, so a CLI that reaches Dagu at
  `localhost`, and a browser at a published container port, get the same
  URL. A request with a forwarding header, or whose `Origin` or `Referer`
  names another origin, does not count, so a server behind a proxy needs
  `server.public_url`.
- Unless the caller names a mode, the UI and the CLI pick the first of
  server callback, Cloud relay, loopback, and paste that works for the
  server's public URL, its license, and the app's client type.
- Device code is used only when the caller asks for it: `--device-code`
  in the CLI, or the mode in `POST .../authorization`.
- A mode named in `POST .../authorization` replaces the automatic choice.
  A mode that does not work for the server or the app's client type is
  refused, and no session starts.
- A device code session waits while the user enters the code, and then
  needs the starter's confirm, as other sessions do.
- Before consent, the UI and the CLI show the exact redirect URI to
  register in the client.
- A Google connection uses server callback, Cloud relay, loopback, or
  paste. It never uses device code.
- In loopback mode, the CLI sends the code to the server. In paste mode,
  the user sends the pasted URL. Both go to
  `POST .../authorization/complete`, which follows the
  [Callback](#callback) rules.

#### Cloud Relay

- Starting a session in Cloud relay mode registers a callback with Dagu
  Cloud under the license's credentials, and gets a callback ID.
- The `state` parameter starts with the callback ID in clear, followed by
  the encrypted state.
- While the session waits, the server asks Dagu Cloud for the callback
  every 3 seconds. It stops when the session completes, fails, expires, or
  is cancelled.
- A fetched callback is checked and completed by the [Callback](#callback)
  rules, as if the server had received it.
- The server sends Dagu Cloud no client secret and no PKCE verifier in
  clear. Dagu Cloud holds only the code and the encrypted state.
- The server offers Cloud relay only when Dagu Cloud serves the relay
  endpoints.
- No run depends on Dagu Cloud. Only connecting does.

#### Checks After Consent

- **Granted scopes.** A service whose scopes the provider did not grant
  shows each missing scope, with Reconnect. The connection is still
  created.
- **Identity.** Dagu reads `account` and `account_id` and shows them for
  confirmation.
- **Refresh token.** A consent whose access token expires needs a refresh
  token. Without one, the session fails. An access token without an expiry
  serves without one.
- **Service probes.** Each service gets one probe: `tools/list` on an MCP
  service, and a profile read on a REST service. Each service reports its
  own result. A service with a missing scope reports the scope without a
  probe call.
  - The app's `agent` flag picks a Google probe. With `agent`, each service
    with an MCP server runs `tools/list` and saves the listing as its tool
    catalog. Without it, each service reads its REST API, and a service
    with no REST API, such as `search`, fails its probe, saying it needs an
    app with `agent`.
  - Docs, Sheets, and Slides have no profile read. Their probe reads a
    resource ID that does not exist, and passes only when the API answers
    with its JSON error whose `status` is `NOT_FOUND`.
  - A rejected Google MCP probe says that the client may lack the agent
    flag, with a link to the client's page in the Google Cloud console.
  - A Google probe refused with the `SERVICE_DISABLED` reason instead names
    the API to enable in the client's project, with a link to enable it.
- **Refresh token expiry.** When the provider reports
  `refresh_token_expires_in`, Dagu stores and shows the date. Google reports
  it for time-based access, which the account chose at consent, and Dagu
  says to reconnect before the date. When the date is within seven days,
  Dagu also says that publishing an app in Testing status to Production,
  or making it Internal, removes Google's seven-day limit.
- **Duplicates.** A second connection in the same project for the same
  `account_id` and client is created with a warning. Apps with the same
  provider and `client_id` count as one client. A connection in another
  project for the same account produces no warning. A connection whose
  token state cannot be read is skipped, so this check never fails a
  consent.

#### Consent Errors

| Provider error | Shown |
| --- | --- |
| `access_denied` | Both causes: the user declined, or the app is in Testing status and the account is not a test user, with how to add a test user |
| `admin_policy_enforced` | The Workspace admin blocks the client. The client ID, with a copy button, and where the admin trusts it under API controls |
| `redirect_uri_mismatch` | The redirect URI to register, with a copy button |
| `invalid_client` | The client ID or secret is wrong, or the client was deleted. Upload the client file again |

Each of these fails the session.

#### Reconnect

- A reconnect runs the connect flow on an existing connection.
- The previous tokens stay live until the starter confirms.
- A session whose `account_id` differs from the connection's is never
  confirmed without asking, even when an expected account matches.
- A confirmed reconnect sets `account` and `account_id` from the session,
  clears `needs_reconnect`, and increases `generation`, in the one atomic
  write of [Confirmation](#confirmation).

#### Token Cleanup

- Each connect gets a new refresh token from the provider.
- These refresh tokens are unused: the one that a confirmed reconnect
  replaces, the one that a session holds in `confirm` when it expires, is
  cancelled, is replaced, or its connection is removed, and the one that a
  callback receives after its session ended or its connection was removed.
- Dagu revokes an unused token when revoking one token leaves the rest of
  the grant. When revoking would end the account's whole grant to the
  client, or when that is unknown for the provider, the token is left
  unused.
- The revocation happens at the confirm, the cancel, the start of the
  replacing session, the remove, or the callback that finds its session
  ended. For an expired session, it happens at the first request that
  reads the session after its expiry: a poll, a confirm, a cancel, or a
  new session. If no such request comes, the token is not revoked.

#### Import

- `dagu connection import` and `POST /connections/{name}/import` create a
  connection from an existing refresh token, with no consent. The import
  takes an existing app, or registers one from a client file, which needs
  `admin`.
- The import fetches a token with the imported refresh token. When the
  provider accepts it, the connection is created `connected`, with
  `generation` 1, and `account`, `account_id`, and granted scopes read from
  the provider. Dagu then runs the
  [checks after consent](#checks-after-consent).
- When the provider rejects the refresh token, or the fetch fails for a
  transient reason, the import fails and nothing is stored.
- An imported connection records `import_hash`, a hash of the refresh
  token it was given.
- An import is refused when its refresh token equals a refresh token that
  a connection on the server stores, or matches a connection's
  `import_hash`. A token that a provider rotated after an import therefore
  still counts as held.
- The output says to replace every mail `oauth` block that holds the token
  with `connection:`.

#### Remove

- Remove deletes the metadata and token state records and the
  connection's tool catalogs, then revokes the refresh token where the
  provider supports revocation.
- Remove does not revoke while another connection on this server, in any
  project or for notifications, holds the same `account_id` on the same
  client: an app of the same provider with the same client ID. For
  `google`, whose revocation ends the account's tokens for every client of
  a Google Cloud project, the same client is any app in the same project,
  read from the project number that starts its client ID. Apps whose
  project cannot be read count as the same project. That
  connection keeps working. The check follows the delete, so of two
  connections of one grant removed at once, at least one revokes. When a
  record the check needs cannot be read, Remove does not revoke.
- Remove deletes a connection whose token state is missing or cannot be
  decoded, without revoking. Any other failure to read it fails the
  remove, which changes nothing.
- Revocation is best-effort. A failure is logged and audited without the
  token, and never fails the remove, confirm, cancel, or new session.
- Remove is exclusive with a refresh of the same connection. A refresh that
  races a remove drops its result, and the connection stays removed.
- The confirmation says that connections on other servers that use the
  same client may lose access, and lists where the connection is used.
  For `google`, it says the same Google Cloud project instead of the same
  client.
- After a remove, a DAG that names the connection fails at run start, or,
  for mail and SMTP, when it uses the account. The error names the
  connection.

#### Any MCP Server

An `mcp` connection connects a remote MCP server that follows the MCP
authorization spec:

1. Dagu calls the server without a token, and reads `resource_metadata`
   from the `WWW-Authenticate` header, or fetches the protected resource
   metadata (RFC 9728).
2. Dagu fetches the authorization server metadata (RFC 8414). It accepts
   the metadata only when its `issuer` matches the URL it came from.
3. Dagu picks a client in this order:
   - the server's `cimd` app, when the authorization server supports Client
     ID Metadata Documents and the server has a public HTTPS URL. The client
     ID is `<public_url>/oauth/client-metadata.json`, a public route that
     describes Dagu as a public client whose redirect URI is the server
     callback;
   - a `dcr` app, registered through RFC 7591 when the authorization server
     offers it;
   - otherwise an app the user registers.
4. Dagu runs the connect flow with PKCE and `resource`. The connection's
   `resource` is the server's canonical URL.

### Token Broker

#### Contract

Every token that Dagu uses comes from the broker:

```text
Token(connection, resource, minTTL) -> (access token, expiresIn)
```

- The returned token stays valid at `resource` for at least `minTTL`.
  `expiresIn` is how long it stays valid.
- An empty resource selects the connection's only resource.
- A `minTTL` longer than half the provider's token lifetime is cut to half.
  The caller learns the real lifetime from `expiresIn`.
- A step that uses a token in process asks for a `minTTL` of 5 minutes, and
  asks again when the token runs low.
- Token export asks for 50 minutes.
- A token refreshes only when its remaining life is shorter than the
  effective `minTTL`, or after a rejection of the stored token.
- A caller whose token a resource rejected reports that token. The broker
  refreshes only while the reported token is still the stored one.
  Otherwise the report causes no refresh: it returns the stored token, as
  any token request does, refreshing only when that token's remaining life
  is shorter than the effective `minTTL`.
- A connection in `pending` or `needs_reconnect`, or whose app is
  `invalid`, fails at once, without calling the provider. A run process
  keeps using a token it already holds, as
  [One Refresh per Host](#one-refresh-per-host) states.

#### One Refresh per Host

- On one host, at most one process refreshes a connection at a time.
- Callers on one host that reach the same expiry of a connection's
  resource together cause one refresh request to the provider, however
  many they are.
- A process that finds a refresh of the connection in flight does not call
  the provider while it runs. When it ends, the process reads the token
  state once. It returns the token for its own resource when that token
  meets its `minTTL`, or the error the refresh recorded for its resource.
  Otherwise, as when the refresh was for another resource or the refreshing
  process died, it starts again.
- A refresh of one connection never waits for a refresh of another. A
  token endpoint that hangs delays only its own connection's callers, for
  one token call's timeout.
- Within one run, steps and `foreach` items that use the same connection
  and resource cause no more provider requests than one step does.
- A run process keeps the tokens it receives in memory, and counts each as
  a token it holds. Every other process reads the token state on each
  token request, so it hands out no token after a reconnect, a status
  change, or a change of `subject` replaced it.

#### Writes

- A refresh writes the new access token, and any rotated refresh token,
  without overwriting a concurrent change to the same record. A rotated
  refresh token is never lost, also when a session starts during the
  refresh.
- A refresh that finds `generation` changed drops its result and returns
  the reconnected token. A refresh result never overwrites a newer
  reconnect.
- A refresh that finds the record removed drops its result and fails.
- A rotated refresh token is used by the next refresh, across process
  restarts.

#### Refresh Results

| Result | Effect |
| --- | --- |
| Success | The access token is saved, with its expiry. A rotated refresh token replaces the old one. `refreshed_at` is set |
| `invalid_grant` | Dagu reads the token state once more. When the refresh token or `generation` changed, it retries once with the new state. Otherwise the connection becomes `needs_reconnect` |
| `invalid_client` or `unauthorized_client` | When the app's client ID and secret are the ones the refresh used, the app becomes `invalid`, and every connection of the app fails at once until an admin changes the app's client ID or secret. When an admin changed them during the refresh, the app keeps its status and the refresh is retried once with the current app |
| HTTP 5xx, a network error, or `temporarily_unavailable` | Transient. The status stays. The error is recorded for the refreshed resource. Processes waiting on this refresh for the same resource fail with it. Waiters for another resource start again, as [One Refresh per Host](#one-refresh-per-host) states. The step's retry policy applies |
| Service account key unknown, disabled, or deleted | As for `invalid_client`, with the key in place of the secret |
| Service account clock error | Transient. Reported on the connection. The status stays |

- A connection becomes `needs_reconnect`, or an app `invalid`, once per
  failure. Later failures of the same kind change nothing.
- Only a confirmed reconnect clears `needs_reconnect`, or, for `api_key`
  and `static`, a rotation. Only an admin's change to the app clears
  `invalid`.
- A grant without a refresh token, `service_account` or
  `client_credentials`, never becomes `needs_reconnect`. Its permanent
  failures make the app `invalid`.

#### Workers

- A worker never reads connection records, never holds the encryption key,
  and never refreshes.
- A worker asks its coordinator for a token. The coordinator answers only
  when:
  - the request comes from an attempt the coordinator dispatched to that
    worker, for that DAG;
  - the run's DAG snapshot declares the connection;
  - the name resolves in the run's project.
- The notification connection never resolves for a worker.
- The answer holds the access token, its lifetime in seconds, and the
  binding. A lifetime in seconds makes the worker's clock irrelevant.
  A binding from a coordinator that does not send whether the connection
  allows token export allows none.
- A check-only request runs the same checks and returns the binding
  without a token. A worker uses it for admission.
- A worker reports a rejected token through the coordinator, which applies
  the broker's rejected-token rule.
- A run on a worker makes one check-only request per connection that
  admission checks, one token request per connection and resource each time
  its token falls below the `minTTL`, and one request per rejected token.
- A worker enforces the binding it receives as a local run does.
- The coordinator dispatches a DAG that declares connections only to a
  worker that reports, with its polls and heartbeats, that it makes token
  requests. A run that only older workers match waits or fails, saying the
  matching workers must be upgraded.
- The coordinator answers a run's DAG and the child DAGs the run names as
  written, in its steps, `foreach` bodies, and handlers. A child DAG file
  whose name holds a reference, such as `dag: mail-${params.team}`, that
  runs on the worker that runs its parent cannot use connections: its
  check fails, saying so. A child named as written, or one dispatched to a
  worker of its own, such as one with `worker_selector`, can.

#### Shared Storage

- Hosts that share `<data_dir>` refresh through the same per-connection
  exclusion.
- A host may read a token record older than another host's write. With
  Google, whose refresh tokens do not rotate, a stale read costs one extra
  refresh. With Microsoft, the `invalid_grant` re-read recovers, and a stale
  read costs one extra refresh.
- One refresh per token lifetime across hosts is not guaranteed.

### Binding

- An access token is sent only to a URL whose host matches a bound host:
  - the URL's hostname equals the entry's hostname, compared without
    regard to case, after one trailing `.` is removed from either;
  - a subdomain does not match, and an entry has no wildcard;
  - an IP literal matches only the same IP literal;
  - an entry without a port matches only the default port of the URL's
    scheme: 443 for HTTPS, 993 for IMAP, and 465 or 587 for SMTP. An entry
    `host:port` matches only that port.
- Over HTTP, a token is sent only over HTTPS. Over IMAP or SMTP, it is sent
  only over TLS with a verified certificate.
- A redirect from a request that carries a token is followed only to an
  HTTPS URL on the same host and port. Any other redirect fails the
  request, and its target receives no token. A consumer may refuse every
  redirect, as `mcp.call` does (Spec 098).
- No step field, parameter, tool argument, or mail address changes a
  connection's bound hosts or `subject`.
- Every URL that Dagu fetches for discovery, registration, or tokens, and
  every redirect it follows, is HTTPS. Plain HTTP to a loopback address is
  allowed only in a URL that an admin entered.

### Runs

#### Declared Connections

- Building a DAG collects every connection it names. The sources are step
  `with.connection`, except on a custom harness of Spec 067, which takes
  it as a CLI flag, `mail_accounts.<address>.connection` and
  `smtp.connection` in the DAG or its base config, and `secrets` entries
  with `provider: connection`. RFC 026 and
  [Spec 093](093-model-connections.md#keys) add sources.
- A connection name in these fields is a literal. A name taken from a
  parameter, variable, or other reference fails validation. So does a
  step's `server` beside it, which admission checks too.
- A DAG declares each connection once, with every `server` its steps name
  beside it.
- Validation stays offline. Building a DAG reads no connection record.
- A run receives tokens only for connections its DAG declares.

#### Admission Checks

A run checks each declared connection before its secrets resolve, so a
failed check is reported before a `connection` secret fails. A local run
reads the records. A run on a worker uses a check-only request. The
editor's server-side validation runs the same checks.

- The name resolves in the run's project.
- A step's `server` names one of the connection's services. For a provider
  with presets, that service's preset has an MCP URL.
- A literal `http.request` URL is HTTPS and on the bound hosts.
- A Google MCP preset used by a user connection, not a service account,
  uses an app with `agent` set.

A failed check fails the run before its first step, and the error names the
connection. Mail accounts and `smtp` are not checked here; see
[Checks at Use](#checks-at-use).

- A run checks every declared connection and reports every failed check
  together.
- Admission reads a connection's metadata, and its app only for the agent
  check and for the protocols of a `model` connection on a shared key
  (Spec 093). It reads no token state, so a `pending` or `needs_reconnect`
  connection passes, as [Checks at Use](#checks-at-use) states.
- A DAG that declares no connection reads no connection record.
- A DAG whose `workspace` labels are malformed or name two workspaces fails
  each check, since it names no project. It never resolves in `.global`.
- A workflow test case (Spec 101) runs no admission check and reads no
  connection record, as no secret resolves through its provider under test.

Under a coordinator that does not support token requests, a worker skips
the check-only request. Each step that uses a connection then fails, saying
the coordinator must be upgraded, and the run's other steps run.

#### Checks at Use

- A mail account's or `smtp`'s connection is checked when a step or a
  notification uses it. A DAG that never uses the account runs as before,
  even when its project lacks the connection.
- A connection in `pending` or `needs_reconnect`, or whose app is
  `invalid`, passes admission. Each step that uses it fails with
  `reconnect_required` and the connection's name. The run's other steps run
  as before. A `connection` secret is the exception: secrets resolve once,
  before the first step, so such a connection fails the whole run there.
- A step's `http.request` URL that comes from a reference is checked
  against the binding when the step sends it.

#### Run Records

- A step that uses a connection records the connection's name and account
  in its node status.
- A failed run records a broken connection as its failure cause only when
  every failed step of the run failed on a connection in `needs_reconnect`
  or on an `invalid` app, the states that send
  `connection.needs_attention`. The cause names those steps' connections. A
  run with any other failed step records no connection as its cause.
- A step that fails on a `pending` connection fails with
  `reconnect_required`, but its run records no connection as its cause, so
  every channel still sends the run's failure message.

#### Masking

- A token that a step uses is masked in the step's logs, run status, and
  outputs, without becoming an environment variable.
- A token minted while the step runs, such as by a forced refresh, is
  masked from then on.
- The stdout and stderr logs of every step of a run with at least one
  secret, and of a step that uses a connection, mask text that matches a
  Google access token, `ya29\.[0-9A-Za-z._-]{20,}`, wherever it occurs, or
  a Google refresh token, `1//[0-9A-Za-z_-]{40,}`, unless the refresh token
  match follows a base64 or token character. This includes the output that
  a worker streams for such a step. The patterns apply to those logs only,
  not to outputs, run status, other masked text, or the logs of a step that
  uses no connection in a run with no secret.
- Step output that a worker streams to the coordinator is masked as local
  output is.

### Using a Connection

The owning specs define each field. Every use follows these rules.

#### Rejected Tokens

- A 401 whose token is the stored one causes one refresh and one retry.
- A 401 whose token is older than the stored one takes the stored token and
  retries once, without a refresh.
- The grants `api_key` and `static` neither refresh nor retry. Specs 093
  and 095 state what their 401 does.
- An IMAP or SMTP server that refuses the token is handled as a 401.
- A 403 with `insufficient_scope` fails with detail `insufficient_scope`
  and the missing scope, and does not refresh.

#### Rules per Consumer

| Consumer | Rules |
| --- | --- |
| `mcp.call` | `connection` replaces `url` and `headers.Authorization`. With a `google` or `microsoft` connection, `server` names a preset. With an `mcp` connection, `server` is not allowed. A `url` must be on the bound hosts. `headers` may not set `Authorization`. An `auth` error carries the connection's name, and `reconnect_required` when it needs reconnecting |
| `http.request` | The token is added only to an HTTPS request to a bound host. `connection` cannot be combined with `debug: true` or with `headers.Authorization` |
| Mail accounts, a DAG's `smtp`, and the notification SMTP settings | `connection` replaces `oauth` and `password`, and takes a `google` or `microsoft` connection. A `provider` that differs from the connection's fails when the account is used. With `connection`, a mail account takes no `imap` or `smtp` block, and `smtp` takes no host or port. Gmail API actions need `gmail` with `write`. Google SMTP, Microsoft mail, and Microsoft SMTP need `mailbox` |
| `connection` secret provider | Works only when `allow_token_export` is set. The token stays valid for at least 50 minutes, or half the provider's token lifetime when that is shorter. Its value is masked like any secret. `options.service`, when set, names one of the connection's services; for a Microsoft connection it selects the resource. No other option is allowed |

- Every `mcp.call`, with or without a connection, fails on HTTP 429 with
  kind `rate_limited`, the provider's message, and any `Retry-After`.
- A mail address fits a connection when it is the account of a Google user
  connection, the `subject` of a service account or client credentials
  connection, or any address a Microsoft delegated connection can open,
  which Microsoft enforces. A step's `from` on a Google user connection may
  name a send-as address of the account.
- An address that does not fit fails the step or notification, and the
  error names the address and the account or `subject`.

#### Confirmation Requests

- An MCP client that uses a connection, in `mcp.call`, Try it, or a tool
  catalog listing, advertises form elicitation, and calls Google's
  `/mcp/v1` endpoints. An `mcp.call` without a connection advertises no
  elicitation capability.
- Dagu never accepts a server's confirmation request on its own. Only a
  person accepts one.
- `mcp.call` declines a confirmation request at once and fails with kind
  `confirmation_required` (G1). The error names the ways to let the action
  through: approve it once in Try it and save the approval where the
  provider's form offers that; ask the Workspace admin to change the
  confirmation rules; or use `http.request` with a standard connection.
- Try it shows the form to the person, as [REST API](#rest-api) and
  [CLI](#cli) state.

### Events

Each transition in the table records exactly one event of kind
`connection`, however many processes saw it. No other transition records
an event, so a first connect from `pending` sends none.

| Type | Recorded when | Carries |
| --- | --- | --- |
| `connection.needs_attention` | A connection becomes `needs_reconnect`, or its app becomes `invalid` | The connection, or the app and its connections; the status; the provider's reason; a Reconnect URL |
| `connection.restored` | A connection goes from `needs_reconnect` to `connected`, or an app from `invalid` to `ok` | The connection, or the app and its connections; the status |

- Without a usable public URL, `connection.needs_attention` names the
  Connections page and `dagu connection reconnect <name>` in place of the
  URL.
- A connection's event goes through its project's notification routes. An
  app's event goes through the routes of each project with a connection on
  the app.
- A channel gets one message per event, also when several routes reach it.
  A connection that stays broken while many runs fail sends one message per
  channel.
- Messages are plain text, also on channels that format run events.
- Both types are in the default event set for new channels. Existing
  channels keep their event lists until changed.
- The notification connection's events go through the global routes before
  v3.0, and through the default project's routes from v3.0. They reach
  every channel of those routes except email.
- A channel that receives `connection.needs_attention` sends no run failure
  message for a run whose recorded failure cause is a broken connection.
  The decision reads only the run's record, so it holds when the monitor
  catches up after a reconnect.
- A channel that does not receive connection events sends every run
  failure message.
- A DAG's own failure handlers run as before. Scheduled runs keep starting.
- No event is sent before a refresh token expires.

### REST API

| Method and path | Purpose |
| --- | --- |
| `GET /connection-providers` | Providers, presets, scope sets, setup links, and this server's redirect URIs and modes |
| `GET`, `POST /oauth-apps` | List apps, or create one. A Google client JSON file is accepted as downloaded. When the server has a public URL, a web client's file is refused unless its redirect URIs list the server callback, and the error names that URI. A desktop client's file is not checked, since Google accepts any loopback port |
| `GET`, `PATCH`, `DELETE /oauth-apps/{id}` | Read, change, or delete an app that no connection uses |
| `GET`, `POST /connections` | List connections with their status, or create one in `pending` |
| `GET`, `PATCH`, `DELETE /connections/{name}` | Read, change metadata, or remove |
| `POST /connections/{name}/authorization` | Start a connect or reconnect. Takes the redirect mode, the loopback redirect URI, and an optional expected account. Returns the authorization URL and a session handle |
| `GET /connections/{name}/authorization` | The result of the session that a handle names. See below |
| `POST /connections/{name}/authorization/complete` | Paste and loopback modes: the code or the redirect URL |
| `POST /connections/{name}/authorization/confirm` | Confirm the account. Only the starter may |
| `DELETE /connections/{name}/authorization` | Cancel the session |
| `POST /connections/{name}/import` | Create a connection from an existing refresh token. See [Import](#import) |
| `POST /connections/{name}/test` | Probe each service |
| `GET /connections/{name}/tools?server=` | MCP tools with their input schemas, read from the tool catalog |
| `POST /connections/{name}/tools/{tool}/call` | Try a tool. Returns the result, or a confirmation form and a call ID |
| `POST /connections/{name}/calls/{id}/confirmation` | Answer a confirmation request. Returns the tool's result |
| `GET /connection-templates?provider=` | Starter DAGs for a provider |

- `GET /oauth/callback` and `GET /oauth/client-metadata.json` are public
  routes outside the generated API.
- A `workspace` parameter qualifies `{name}`. The notification connection
  has the same paths under `/notification-settings/connection` in place of
  `/connections/{name}`.
- The notification settings' SMTP block takes `connection`, with the role
  the rest of the settings need.
- No response includes a client secret, key, refresh token, or access
  token. Responses report `clientSecretConfigured`,
  `refreshTokenConfigured`, and similar flags. A `PATCH` with a blank secret
  keeps the stored one.
- A request whose `remoteNode` names another server is refused.
- A connection whose token state is missing or cannot be decoded or
  decrypted, as a create that stopped halfway leaves it, is shown with
  status `broken` and a reason that says to remove it and add it again. It
  is still listed and can be removed. Any other failure to read the token
  state fails the request.
- `POST .../authorization` also returns the redirect mode, the exact
  redirect URI to register, and the app's client ID, which the UI and the
  CLI show before consent. `GET /connection-providers` lists each redirect
  mode the server offers with its client type and redirect URI.
- `GET .../tools` without `server` reads the catalog of every MCP service
  of the connection, and reports a listing that fails per service.
- `GET .../authorization` with a session handle returns `waiting`, or
  `confirm` with the account, while that session is in progress. After it
  ends, it returns `connected` when the session was confirmed, and
  otherwise `failed` with a reason: the provider's error, `expired`,
  `cancelled`, or `replaced`.
- A Try it call that waits for a confirmation answer is held for up to 10
  minutes. Only the replica that received the call can answer it. An answer
  that reaches another replica, or arrives later, fails with
  `call_not_found`.
- Only the user who started a Try it call may answer its confirmation.

### CLI

```sh
dagu connection providers
dagu connection add google [NAME] --services gmail,calendar:write [--agent] \
    [--app APP | --client-file FILE] [--no-browser] [--workspace W | --notifications]
dagu connection add google NAME --service-account KEY --subject ADDRESS
dagu connection add google NAME --app APP --subject ADDRESS
dagu connection add microsoft NAME --services mail,calendar [--device-code]
dagu connection add mcp NAME --url URL
dagu connection add oauth2 NAME --authorize-url URL --token-url URL \
    --scopes a,b --bound-host HOST
dagu connection import NAME --provider P --client-file FILE --refresh-token-stdin
dagu connection list
dagu connection show NAME
dagu connection test NAME
dagu connection reconnect NAME
dagu connection remove NAME
dagu connection tools NAME [--server S] [--json]
dagu connection call NAME --server S --tool T --args JSON [--yes]
dagu connection apps list
dagu connection apps remove APP
```

- `connections` is an alias of `connection`.
- A command acts in the context's workspace, or in the one `--workspace`
  names. `--notifications` acts on the notification connection.
- The group works against a remote server through a context.
- `add` without enough flags runs an interactive wizard on a terminal, and
  reads secrets without echo.
- `add` and `reconnect` pick the redirect mode as the UI does. They open a
  browser when they can, and print the authorization URL. With
  `--no-browser` they only print it. In paste mode they ask for the pasted
  redirect URL. They wait for the session and ask the user to confirm the
  account.
- `--client-file` registers an app and needs `admin`. A manager names an
  existing app with `--app`.
- `call` shows a server's confirmation form on a terminal and sends the
  person's answer. Without a terminal it fails with
  `confirmation_required`. `--yes` skips only Dagu's own question before a
  tool that is not read-only, never the server's.
- No command prints a client secret, key, refresh token, or access token.

### User Interface

- A Connections page lists each connection the user can see in the current
  project, with its name, provider, account, and status.
- A connection page shows the status and its reason, the account, services
  and granted scopes, the project, and Reconnect, Test, and Remove. It lists
  the DAGs that name the connection, or the notification SMTP settings for
  the notification connection. A connection that a base config names is
  listed once, as used by the base config.
- Remove, and a reconnect with a different account, show where the
  connection is used before they apply.
- The consent popup opens before any request, so a browser does not block
  it. A blocked popup falls back to a link. Copy sign-in link gives the
  authorization URL to the account's owner.
- Try it asks before calling a tool that is not marked read-only.
- The Connections navigation item shows a badge while any connection the
  user can see needs reconnecting, or has a refresh token that expires
  within two days.
- On a server that runs DAGs locally, creating a connection says that DAG
  authors can read its tokens.
- Choosing both agent and other use in the Google wizard creates two
  connections, `<name>-agent` with an agent app and `<name>` with a
  standard app. `<name>` then has at most 57 characters.
- Add mailbox creates another connection on the same app with a new
  `subject`, and needs `admin`.
- The notification settings page connects, reconnects, and removes the
  notification connection, and shows its status.
- The DAG editor reports the [admission checks](#admission-checks) and
  connections that the DAG's project lacks. It warns on an `http.request`,
  mail account, or `smtp` that uses a connection whose app has `agent`
  set, and on each mail `oauth` block, which is deprecated and keeps
  working.
- The run page shows the connection and account of each step that used
  one, and Reconnect next to a step that failed with
  `reconnect_required`.

### MCP Read Targets

- `dagu_read` takes `target=connections` and `target=connection`. Both
  return names, providers, services, status, and tools, and never a secret.

### Permissions

| Action | Role |
| --- | --- |
| Create, change, or delete an OAuth app | `admin` |
| List and read OAuth apps, without secrets | `manager` or above in any project, or whoever may change the notification settings |
| Set `allow_token_export` | `admin` |
| Create, connect, reconnect, change, or remove a connection | `manager` or above in its project |
| Add a service account or Microsoft client credentials connection, or change its `subject` | `admin` |
| Create, connect, reconnect, or remove the notification connection | Whoever may change the notification settings: `developer` or above before v3.0, `admin` from v3.0 |
| Test a connection, list its tools | `developer` or above in its project |
| Try a tool | `developer` or above in its project |
| Answer a Try it confirmation | The user who started the call |
| Name a connection in a DAG | Whoever may edit the DAG. The name resolves only in the DAG's project |
| See a connection: name, project, provider, account, status | `viewer` or above in its project |

- No role sees a token, client secret, or key.
- A service account or client credentials notification connection needs
  `admin`, before and after v3.0. The `admin` row wins over the
  notification connection's row.
- A manager's `mcp` connect may use the server's `cimd` app, or register a
  `dcr` app that belongs to that connection alone.
- A manager who finds no fitting app is shown what to ask an admin for: the
  client type, the redirect URI, and whether the agent flag is needed.

### Audit

The audit log records:

- app and connection changes;
- connects, reconnects, imports, and removals;
- status changes;
- tool calls from Try it.

Token issuance is not audited. No audit entry holds a secret.

## Constraints

- While no connect session waits, nothing runs on a timer: no ticker, file
  watcher, or periodic scan. Tokens refresh only when a caller needs one,
  and catalogs list only when read stale.
- The Cloud relay sends one request to Dagu Cloud every 3 seconds per
  waiting session, for at most 10 minutes.
- A service is listed with `tools/list` at most once per connection per 10
  minutes on a host, whatever the number of tabs or runs, while its
  listings succeed. While they fail, each process lists it at most once
  per 30 seconds.
- A Try it call waits for a confirmation answer for at most 10 minutes.
- Listing connections reads two records per connection, and no run or DAG
  directory.

## Errors

### Names and Records

- Creating a connection with a name that breaks the [name rules](#names),
  or that exists in its project, is refused and changes nothing.
- Before v3.0, creating a connection in a workspace that does not exist,
  or whose name has an uppercase letter, is refused. A workspace created
  later under the name would inherit the connection, and a run finds its
  connections by the DAG's `workspace` label, which is lowercased.
- Creating an app with `kind: hosted`, or a connection with
  `holder: cloud`, is refused.
- Creating an `oauth2` connection without `bound_hosts`, or an `oauth2` app
  without endpoints, is refused.
- Deleting an app that a connection uses is refused, naming the
  connections.
- Deleting a project that owns connections is refused, naming them.

### Connect Flow

- A callback after its session expired, a second callback for a session,
  and a callback for a replaced session are refused. Nothing changes.
- A confirm by a user other than the starter is refused. The session stays
  in `confirm`.
- A denied consent, a closed popup, and a consent error fail or expire the
  session. A new connection stays `pending`. A reconnected one keeps its
  previous tokens.
- When Dagu Cloud is unreachable during a Cloud relay connect, the session
  fails, saying so, and the UI or CLI offers the next mode. Runs and
  existing connections are unaffected.
- An import of a refresh token that another connection stores, or was
  imported from, is refused.
- An import whose refresh token the provider rejects, or whose token fetch
  fails for a transient reason, fails, and nothing is stored.
- Authorization server metadata whose `issuer` does not match its URL is
  refused, and the connect fails.
- A redirect mode that does not work for the server or the app's client
  type is refused, and no session starts.
- A service account or client credentials connection whose first token
  fetch fails stays `pending`, with the provider's error.

### Tokens

- A step that uses a connection in `needs_reconnect`, or whose app is
  `invalid`, fails with `reconnect_required` and the connection's name.
- A step whose token lacks a scope fails with `insufficient_scope` and the
  missing scope.
- A transient refresh failure fails the call with the provider's error. The
  status stays, and the step's retry policy applies.
- A step that uses a `pending` connection fails with `reconnect_required`
  and a reason saying the connection was never connected.
- On a worker whose coordinator does not support token requests, a step
  that uses a connection fails, saying the coordinator must be upgraded.
  Other steps run, as [Admission Checks](#admission-checks) states.
- A token request from a worker for a connection its DAG does not declare,
  for one of another project, or for the notification connection is
  refused, and the step fails.

### Admission and Use

- A DAG whose connection name is not a literal fails validation.
- A run whose DAG names a connection its project lacks fails before its
  first step, naming the connection.
- A `server` that is not one of the connection's services, a literal
  `http.request` URL off the bound hosts or not HTTPS, or a Google MCP
  preset on a user connection whose app lacks `agent`, fails the run before
  its first step, naming the connection.
- A mail account with `connection` and an `imap` or `smtp` block, or an
  `smtp` with `connection` and a host or port, fails validation, naming the
  field.
- An `mcp.call` with `connection` and a `headers.Authorization`, or an
  `http.request` with `connection` and `debug: true` or
  `headers.Authorization`, fails validation.
- An `http.request` URL from a reference that is off the bound hosts or not
  HTTPS fails the step before any request.
- A redirect that [Binding](#binding) does not allow fails the request.
- A `connection` secret on a connection without `allow_token_export` fails
  the run at secret resolution, naming the connection. So does one whose
  `options.service` is not one of the connection's services, and one on a
  connection in `pending` or `needs_reconnect` or whose app is `invalid`,
  with `reconnect_required`. No step of such a run starts.

## Examples

Each example runs on a server with workspaces `ops` and `sales`, a Google
app `google-agent` with `agent: true`, a Google app `google-std` without
it, and the connections that [Names in Two Projects](#names-in-two-projects)
lists. Every connection is `connected`. A DAG in `ops` carries the label
`workspace=ops`.

### Names in Two Projects

| Project | Connection | App | Services | Account |
| --- | --- | --- | --- | --- |
| `ops` | `work-google` | `google-agent` | `gmail` read | `ops@example.com` |
| `sales` | `work-google` | `google-agent` | `gmail` read | `ops@example.com` |
| `ops` | `ops-mail` | `google-std` | `gmail` write, `mailbox` | `ops@example.com` |

- Each `work-google` has its own refresh token. Reconnecting the one in
  `sales` leaves `ops` unchanged.
- Removing `work-google` in `ops` does not revoke its refresh token, since
  `sales` holds the same `account_id` and app.
- Creating a second connection in `ops` for `ops@example.com` on
  `google-agent` warns. The `sales` connection caused no warning.

### `mcp.call` With a Connection

```yaml
labels:
  - workspace=ops
steps:
  - id: unread
    action: mcp.call
    with:
      connection: work-google
      server: gmail
      tool: search_threads
      arguments: { query: "is:unread newer_than:1d" }
```

The run admits. The step calls `https://gmailmcp.googleapis.com/mcp/v1`
with the access token of `ops`'s `work-google`, and its node status records
`work-google` and `ops@example.com`.

### Admission Failures

| DAG in `ops` | Result |
| --- | --- |
| `mcp.call` with `connection: missing` | Fails before its first step, naming `missing` |
| `mcp.call` with `connection: ops-mail` and `server: gmail` | Fails before its first step: the app of `ops-mail` lacks `agent` |
| `mcp.call` with `connection: work-google` and `server: drive` | Fails before its first step: `drive` is not one of the connection's services |
| `mcp.call` with `connection: ${CONN}` | Fails validation: not a literal |
| `mcp.call` with `connection: work-google` and `headers: { Authorization: x }` | Fails validation |
| `http.request` with `connection: ops-mail` and `url: http://gmail.googleapis.com/...` | Fails before its first step: not HTTPS |

A DAG in a workspace without `work-google`, or a DAG without a `workspace`
label when `.global` has no `work-google`, fails before its first step,
naming `work-google`.

### A Broken Connection

`ops`'s `work-google` has its refresh token revoked at Google. 100
scheduled runs then reach an expired token together.

- Google receives one refresh request, which fails with `invalid_grant`.
- The connection becomes `needs_reconnect` once. Each run's step that uses
  it fails with `reconnect_required`, naming `work-google`. Other steps run.
- Each channel that `ops`'s routes reach and that receives connection
  events gets one `connection.needs_attention` message, and no run failure
  message for those runs.
- A reconnect confirmed by the starter sets `connected`, increases
  `generation`, and sends one `connection.restored` per channel.

### Concurrent Refresh

100 runs on one host reach an expired token of `work-google` together.

- One process refreshes. Google receives one refresh request.
- The other 99 do not call Google, and each reads the new token once.
- When Google answers HTTP 503, Google still receives one request, the 99
  fail with that error, and the status stays `connected`.

### Connect From the CLI Over SSH

On a server with `server.public_url: https://dagu.example.com`:

```text
$ dagu connection add google team-google --workspace ops --services gmail \
    --agent --app google-agent --no-browser
Open this URL to sign in:
https://accounts.google.com/o/oauth2/v2/auth?...
Register this redirect URI in the client: https://dagu.example.com/oauth/callback
Waiting for consent (Ctrl-C to cancel)...
Signed in as ops@example.com. Is this the right account? [Y/n] y
Warning: work-google in ops already holds ops@example.com on google-agent.
  gmail  read  ok  (10 MCP tools)
Connection team-google is ready.
```

`team-google` is created, with the warning, since `work-google` in `ops`
holds the same `account_id` on the same app. A browser with no Dagu session
completes consent. A confirm sent by another
user is refused, and the session stays in `confirm`.

### Token Export

```yaml
labels:
  - workspace=ops
secrets:
  - name: GOOGLE_TOKEN
    provider: connection
    key: work-google
steps:
  - id: list
    run: |
      curl -H "Authorization: Bearer $GOOGLE_TOKEN" https://gmail.googleapis.com/gmail/v1/users/me/profile
```

Without `allow_token_export` on `work-google`, the run fails at secret
resolution, naming `work-google`. With it, `GOOGLE_TOKEN` holds a token
valid for at least 50 minutes, and its value is masked in the step's log.

### Mail

```yaml
labels:
  - workspace=ops
mail_accounts:
  ops@example.com:
    connection: ops-mail
smtp:
  connection: ops-mail
```

- With `ops-mail` holding `gmail` with `write` and `mailbox`, mail actions
  and SMTP sends for `ops@example.com` use its tokens, sent only to
  `gmail.googleapis.com`, `imap.gmail.com`, and `smtp.gmail.com`, with
  verified certificates.
- A mail action for `other@example.com` through `ops-mail` fails, naming
  `other@example.com` and `ops@example.com`.
- In a base config, a project without `ops-mail` runs its DAGs that send no
  mail as before. A step there that uses the account fails, naming
  `ops-mail`.
