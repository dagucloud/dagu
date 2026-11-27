# Spec 108: Peer Transport and Worker Invites

## Status

Partially implemented. Implemented: the unset default of `peer.insecure`,
[Client Transport](#client-transport) and the
[Start Line](#start-line) except worker invites and certificates, the
[Per Coordinator](#per-coordinator) line, the warnings of
[Ignored Settings](#ignored-settings) except in `dagu worker --check`,
[Bring Your Own Certificates](#bring-your-own-certificates), the
[Peer CA](#peer-ca) and the
[Published CA Certificate](#published-ca-certificate), the rows of
[Configuration Conflicts](#configuration-conflicts) for `peer.ca_cert_file`
and `peer.ca_key_file`, [`dagu peer init`](#dagu-peer-init), the
[Coordinator Line](#coordinator-line), the
[Coordinator Certificate](#coordinator-certificate),
[One Port](#one-port) except the revoked set,
[Require TLS](#require-tls), the coordinator call of
[Join Readiness](#join-readiness), the [Invites](#invites) and their
[Coordinator Calls](#coordinator-calls) except removal and the daily
deletion of ended [Invite Records](#invite-records), and the
[Coordinator Side](#coordinator-side) of enrollment with the
[Worker Certificate](#worker-certificate) and its record, except refusing
the key of a revoked certificate. Clients that share a coordinator's data
directory use built-in TLS. No client enrolls or sends an h2c reason yet.

Behavior lands in parts, tracked in #4292. Each part that implements a
section of this spec updates this status and adds conformance tests for what
the binary shows.

Until a part lands, its keys have no effect, and peer processes connect as
they do without this spec: an unset `peer.insecure` selects h2c as `true`
does, the coordinator serves h2c, or TLS when `peer.cert_file` and
`peer.key_file` are set, and every peer authenticates with the peer token.

## Scope

This spec defines:

- the keys `peer.ca_cert_file`, `peer.ca_key_file`, `worker.invite`, and
  `coordinator.require_tls`, and the unset default of `peer.insecure`
- how a client picks its transport, and the precedence of its settings
- the chosen-transport log and the warning for an ignored TLS setting
- the peer CA, the CA certificate a coordinator publishes in its data
  directory, and `dagu peer init`
- the coordinator certificate, and TLS and h2c on one port
- `coordinator.require_tls`
- the h2c fallback: which clients fall back, when, and for how long
- the h2c reasons a client sends, and how the coordinator reports them
- invites, their kinds, and their records
- enrollment, the worker's certificate, its files, and its lock
- revocation, removal with running runs, and certificate record pruning
- join readiness
- configuration conflicts
- the start checks of bring-your-own certificates
- `dagu worker --check` and `dagu worker invite`
- the REST endpoints for invites, removal, and join readiness
- the contracts of the **Add worker** dialog and the Workers page
- failure messages, and the values that never appear in output
- behavior across Dagu versions

This spec does not define:

- the meaning of `peer.cert_file`, `peer.key_file`, `peer.client_ca_file`,
  and `peer.skip_tls_verify`, which stays as it is
- the peer token itself and `coordinator.require_token`, which stay as they
  are, except that an unrevoked worker certificate counts as the peer token
- worker pools and agent workers. [Spec 092](092-agent-workers.md) defines
  them; an agent worker still needs its own certificate files, not an
  invite.
- Web UI and API HTTPS, and the HTTP health endpoints
- reaching a coordinator behind NAT
- the default listen address of the coordinator
- binding a certificate to its worker ID, narrowing what a worker
  certificate may call, and replacing the CA (RFC 029 open questions 2 to 4)
- the Helm chart's values, and Dagu Cloud's console
- the name of the MCP tool and action behind **Add worker**, which the part
  that adds it sets in the MCP reference and in this spec

## Goal

Coordinator traffic uses TLS by default wherever a process can get the CA
without a setting and the network path carries TLS. A worker on another
host joins with one copied command. **Remove** keeps that worker out.
Nothing that works today breaks on upgrade, and every failure says what is
wrong and what to do.

## Gates

| ID | State | Decision |
| --- | --- | --- |
| G1 | Open | RFC 029 open question 1: whether v3.0 defaults `coordinator.require_tls` to `true`, whether a TLS file selects TLS without `peer.insecure: false`, and whether the h2c fallback goes. Until G1 closes, every rule of this spec applies to v3.0 as written. |

## Related Specs

- [RFC 029: Built-in Peer TLS and Worker Invites](../rfcs/029-built-in-peer-tls.md)
  gives the design and its reasons.
- [Spec 092: Agent Workers](092-agent-workers.md) requires certificate files
  for agent workers.

## Terms

The data directory is `paths.data_dir`.

A peer process is a process that serves or calls the coordinator's gRPC
port: the coordinator, the scheduler, the server, a worker, `start-all`, a
CLI command that calls the coordinator, and a run process.

A client is a peer process when it calls a coordinator.

The peer token is `peer.token`, or the token the coordinator generates in
its data directory.

h2c is HTTP/2 over plaintext TCP.

The peer CA is the CA that signs coordinator and worker certificates: the
one a coordinator generates, or the one `peer.ca_cert_file` and
`peer.ca_key_file` supply.

Built-in TLS is TLS whose server certificate the peer CA signs and whose
client checks the name `coordinator.dagu.invalid`.

Own certificates are `peer.cert_file` and `peer.key_file` on a coordinator.
A coordinator with own certificates does not use built-in TLS.

An invite is the string `worker.invite` carries. A worker certificate is a
certificate a coordinator issues to a worker at enrollment. An invite worker
is a worker that has `worker.invite` set or holds a worker certificate.

The published CA certificate is the copy of the peer CA certificate that a
coordinator keeps in its data directory for the clients that share it.

A TLS source is what lets a client use built-in TLS: a worker invite or
certificate, `peer.ca_cert_file`, or the published CA certificate while the
service registry in the client's data directory lists a coordinator that
reports built-in TLS.

A TLS setting is `peer.cert_file`, `peer.key_file`, `peer.client_ca_file`,
`peer.skip_tls_verify: true`, or `peer.ca_cert_file`.

A transport-level handshake failure is a TLS handshake whose connection is
reset or closed before the server's first handshake message, or whose first
bytes from the server are not a TLS record. A certificate that does not
verify, a name that does not match, a pin that does not match, and a TLS
alert from the server are not transport-level failures.

## Behavior

### Keys

| Key | Flag | Environment variable | Default |
| --- | --- | --- | --- |
| `peer.insecure` | `--peer.insecure` | `DAGU_PEER_INSECURE` | Unset |
| `peer.ca_cert_file` | `--peer.ca-cert-file` | `DAGU_PEER_CA_CERT_FILE` | Unset |
| `peer.ca_key_file` | `--peer.ca-key-file` | `DAGU_PEER_CA_KEY_FILE` | Unset |
| `worker.invite` | `--worker.invite` | `DAGU_WORKER_INVITE` | Unset |
| `coordinator.require_tls` | None | `DAGU_COORDINATOR_REQUIRE_TLS` | `false` |

- `peer.insecure` takes `true`, `false`, or no value. Unset is not `false`.
- `peer.ca_cert_file` is a PEM file with one CA certificate. On a
  coordinator it is the peer CA, with `peer.ca_key_file`. On a client it is
  the CA that signs coordinator certificates.
- `peer.ca_key_file` is the PEM private key of `peer.ca_cert_file`. Only a
  coordinator reads it.
- Config output masks `worker.invite` as it masks `peer.token`.

### Client Transport

A client picks its transport from the first row that matches:

| Configuration | Transport |
| --- | --- |
| `peer.insecure: true` | h2c |
| `peer.insecure: false` with `peer.cert_file`, `peer.key_file`, `peer.client_ca_file`, or `peer.skip_tls_verify: true` | TLS with those settings, verified against `peer.client_ca_file` or the system roots |
| A TLS source | Built-in TLS |
| `peer.insecure: false` | None: the process exits with code 1 at start with `TLS enabled but no certificates provided` |
| None of these | h2c |

- Rows 1, 2, 4, and 5 behave as a Dagu that predates this spec does.
- A TLS setting that no matching row uses has no effect, and the process
  warns at start, as [Ignored Settings](#ignored-settings) states. In
  particular, `peer.cert_file`, `peer.key_file`, or `peer.client_ca_file`
  without `peer.insecure: false` selects no TLS, as before this spec.
- A client on built-in TLS presents a client certificate only when it is an
  invite worker that holds a worker certificate. Every other client on
  built-in TLS presents none and sends the peer token.
- A worker's run processes use the worker's transport and credential.

#### Built-in TLS Sources

- An invite worker without a worker certificate verifies the coordinator
  against the invite's pin. One with a worker certificate verifies it
  against the CA certificate it stored at enrollment.
- A client with `peer.ca_cert_file` verifies every coordinator against that
  certificate.
- A client with neither of the above that can read the published CA
  certificate in its own data directory, while the service registry there
  lists a live coordinator entry with `"tls": "builtin"`, uses built-in TLS
  and verifies against that certificate. This holds however the client
  finds coordinators:
  - A client that finds coordinators in the service registry uses built-in
    TLS for each entry with `"tls": "builtin"`, and h2c for an entry
    without it.
  - A client with `worker.coordinators`, such as a worker, uses built-in TLS
    for each of those addresses. An address need not match a registry
    entry: in Compose, the worker dials the service name while the entry
    holds the advertised host. An address that a live entry holds follows
    that entry, as for a client that finds coordinators in the registry.
- The client checks the published CA certificate and the registry when it
  opens a connection. Without both, it has no TLS source, and it uses h2c
  as before this spec. A client with `peer.insecure: false` never uses h2c:
  such a connection fails with `TLS enabled but no certificates provided`.
- A connection on h2c without a TLS source checks again at most every 10
  seconds. When a new connection would use built-in TLS, as once a
  coordinator that registered after the client connected reports it, the
  client opens one for its next call. Calls in flight end on the old
  connection.
- At start, the [Start Line](#start-line) and row 4 of
  [Client Transport](#client-transport) count the published CA certificate
  as a TLS source when the client can read it, since a coordinator that
  shares the data directory may register after the client starts. Each
  connection still checks the registry.
- Built-in TLS verifies the chain against the CA certificate, and requires
  the server certificate to name `coordinator.dagu.invalid` and to allow
  server authentication. It does not compare the dialed address with the
  certificate.

#### Unlisted Addresses

- An address that no live registry entry lists, dialed with the published
  CA certificate as the only TLS source, may belong to a coordinator of
  another Dagu, such as a team coordinator dialed from a laptop that runs
  `start-all`.
- When the server certificate on such an address does not chain to the
  published CA certificate, the client has no TLS source for that address.
  It uses h2c, as before this spec, and sends the reason `no TLS source`.
  A client with `peer.insecure: false` fails the call instead.
- It keeps h2c for that address for 10 minutes, as a window that
  [Fallback to h2c](#fallback-to-h2c) describes.
- An address that a live registry entry lists, a worker certificate, an
  invite, and `peer.ca_cert_file` never fall back on a certificate that
  does not verify.

#### Fallback to h2c

- A client on built-in TLS with `peer.insecure` unset that holds no worker
  certificate falls back to h2c for a coordinator address when its TLS
  handshake to that address ends in a transport-level failure.
- A fallback opens a window: the client uses h2c for that address for 10
  minutes. When the window ends, the client opens a new connection for its
  next call, which tries built-in TLS again.
- The clients of a process share the window, and keep it in the data
  directory, where processes that start later on the host read it. Only the
  client that opens a window pays the failed handshake and logs its
  warning, which names the address and the handshake error. A data
  directory that hosts share keeps each host's windows apart, and a window
  read there lasts at most 10 minutes from when it is read.
- A call whose attempt failed before it got a connection is sent once more
  over h2c while the window is open, so a fallback fails no call. A call
  that reached a coordinator is never sent again. The same holds for a call
  that met a certificate on an [unlisted address](#unlisted-addresses).
- These never fall back:
  - an invite worker, with or without a worker certificate
  - a client on TLS from row 2 of [Client Transport](#client-transport)
  - a client with `peer.insecure: false`
  - a handshake that fails for any reason other than a transport-level
    failure, such as a certificate that does not verify, except as
    [Unlisted Addresses](#unlisted-addresses) states
- A coordinator with `coordinator.require_tls: true` refuses the calls a
  client makes after a fallback, as [Require TLS](#require-tls) states. The
  first refusal ends the window, and the client's next call opens a new
  connection that tries built-in TLS again.
- The fallback also covers a coordinator that a downgrade left on h2c only
  while its published CA certificate remains: the handshake fails at the
  transport level, and the client keeps working over h2c.

### Chosen Transport

#### Start Line

The coordinator, the scheduler, the server, a worker, and `start-all` log
one line at start, at level info, for the transport they use as clients. A
CLI command and a run process log the line, and the warnings of
[Ignored Settings](#ignored-settings), at level debug, so their terminal
output does not change.

| Case | Line |
| --- | --- |
| Row 1 | `Peer transport: h2c (from peer.insecure: true)` |
| Row 2 | `Peer transport: TLS (from peer.insecure: false)` |
| Invite worker with a worker certificate | `Peer transport: built-in TLS (from worker certificate)` |
| Invite worker without one | `Peer transport: built-in TLS (from worker.invite)` |
| `peer.ca_cert_file` | `Peer transport: built-in TLS (from peer.ca_cert_file)` |
| The published CA certificate | `Peer transport: built-in TLS (from the coordinator's data directory)` |
| None of these | `Peer transport: h2c (from no TLS source)` |

A coordinator that does not call other coordinators logs no start line. A
scheduler, a server, or `start-all` with `coordinator.enabled: false` calls
no coordinator, so it logs no start line and no warning of
[Ignored Settings](#ignored-settings).

#### Per Coordinator

When a client's transport to one coordinator address differs from its
start line, it logs a line for that address, at level info, once each time
the transport to that address changes:

```text
Peer transport to <host>:<port>: <transport> (from <source>)
```

`<source>` is `the coordinator's data directory`, `no TLS source`, or
`tls failed on the network path`.

#### Coordinator Line

A coordinator logs one line at start for what it serves:

| Case | Line |
| --- | --- |
| Generated CA | `Coordinator transport: built-in TLS and h2c (from the generated CA)` |
| `peer.ca_cert_file` | `Coordinator transport: built-in TLS and h2c (from peer.ca_cert_file)` |
| Either, with `coordinator.require_tls: true` | `Coordinator transport: built-in TLS only (from coordinator.require_tls)` |
| Own certificates | `Coordinator transport: TLS (from peer.cert_file)` |
| A generated CA that cannot be created or read | `Coordinator transport: h2c (from no peer CA)` |

`start-all` logs both its coordinator line and its start line.

#### Ignored Settings

- At start, a client logs one warning per TLS setting that no matching row
  of [Client Transport](#client-transport) uses. The warning names the
  setting, its value, and the change that would use it:

  ```text
  Peer TLS setting ignored: peer.cert_file (/etc/dagu/client.crt) has no effect while peer.insecure is not false; set peer.insecure: false to use it
  Peer TLS setting ignored: peer.ca_cert_file (/etc/dagu/ca.crt) has no effect while peer.insecure is true; remove peer.insecure: true to use it
  ```

- A coordinator uses `peer.cert_file`, `peer.key_file`, and
  `peer.client_ca_file` to serve whatever `peer.insecure` says, so a process
  that only serves logs no such warning.
- `dagu worker --check` prints the same warnings.

#### h2c Reasons

- A client sends a reason in the call metadata key `dagu-peer-h2c-reason` on
  every call over h2c. It sends no reason over TLS.

  | Value | When |
  | --- | --- |
  | `peer.insecure` | Row 1 |
  | `no TLS source` | Row 5, a registry entry without `"tls": "builtin"`, a published CA certificate the client cannot read, or an [unlisted address](#unlisted-addresses) whose certificate does not chain to it |
  | `tls failed on the network path` | After a fallback |

- A call over h2c without the key comes from a Dagu that predates this
  spec, and the coordinator reports it as `older version`.

### Peer CA

- A coordinator without own certificates and without `peer.ca_cert_file`
  uses a generated CA:
  - On its first start, it generates an ECDSA P-256 CA and keeps it in its
    data directory, readable by its user only. Coordinators that start
    together on one data directory end with one CA.
  - The CA certificate's `notAfter` is `99991231235959Z`, the value RFC 5280
    gives for a certificate with no expiry date.
  - Later starts reuse it. It is never regenerated, also not while the
    coordinator runs for a time with `peer.ca_cert_file` or with own
    certificates.
- `peer.ca_cert_file` with `peer.ca_key_file` supplies the CA instead. The
  certificate must be a CA certificate whose key usage, when present,
  allows certificate signing. The key must match it, and the coordinator
  generates nothing.
- A coordinator with own certificates has no peer CA.
- A coordinator that cannot create or read its generated CA, such as one
  whose data directory it cannot write, logs a warning and starts without a
  peer CA: it serves h2c only, publishes no CA certificate, and registers no
  `"tls": "builtin"`, so it starts as it did before this spec. With
  `coordinator.require_tls: true`, it exits with code 1 at start instead.

#### Published CA Certificate

- A coordinator with a peer CA writes the certificate of the CA it uses,
  generated or from `peer.ca_cert_file`, to its data directory as the
  published CA certificate, at start when it is missing or differs, and
  again whenever it refreshes its registry entry and finds it missing.
  After a failed write at start, each refresh retries it over what the
  file held then, until it succeeds or another coordinator publishes.
  Publishing never changes the generated CA.
- A coordinator that fails to start after it wrote the file removes it
  while it still holds its certificate. A running coordinator then writes
  its own again on its next refresh.
- A coordinator with own certificates removes the published CA certificate
  at start, so clients that share its data directory stay off built-in
  TLS. A coordinator that only failed to create or read its generated CA
  leaves it in place for the other coordinators that share the data
  directory.
- The coordinator adds `"tls": "builtin"` to the service registry entry it
  registers. A coordinator without a peer CA does not.
- Older processes ignore the field and the file. A client uses the
  published CA certificate only while the registry lists a live entry with
  `"tls": "builtin"`, so a certificate that an older coordinator leaves
  behind selects nothing.

#### `dagu peer init`

```text
dagu peer init --out <dir>
```

- It writes `ca.crt`, `ca.key`, and `token` into `<dir>`, each readable by
  its user only, and creates `<dir>` when it is missing.
- `ca.crt` and `ca.key` are a CA as the coordinator generates one. `token`
  is a peer token in the format the coordinator generates: 64 lowercase hex
  characters and a newline.
- It reads no config file, needs no running Dagu, and prints the three
  paths.
- When any of the three files exists, it writes none, names the file, and
  exits with code 1.

### Coordinator Certificate

- At each start, a coordinator with a peer CA issues itself a server
  certificate from the CA. It keeps the certificate and its key in memory
  and never writes them.
- The certificate's `notAfter` is `99991231235959Z`. Its subject alternative
  names are the DNS name `coordinator.dagu.invalid`, the advertised host as
  a DNS name or an IP address, and the host name. Its extended key usage is
  server authentication only.
- A name that is not ASCII appears in its IDNA form. A name without one is
  left out, and the coordinator still starts.
- The coordinator's TLS handshake sends the server certificate followed by
  the CA certificate.
- No worker certificate names `coordinator.dagu.invalid` or allows server
  authentication.

### One Port

- A coordinator with a peer CA reads the first byte of each connection. A
  TLS handshake record (`0x16`) starts TLS. Anything else is served as h2c.
  One gRPC server serves both, on `coordinator.port`.
- Built-in TLS needs TLS 1.3 or later.
- TLS requests a client certificate and accepts a connection without one. A
  client certificate that does not chain to the peer CA fails the
  handshake.
- The peer token rules, including `coordinator.require_token`, apply alike
  to calls over TLS and h2c.
- A call over TLS with an unrevoked worker certificate counts as a call with
  the peer token, including under `coordinator.require_token`, except for
  the calls that [Coordinator Calls](#coordinator-calls) restrict.
- A coordinator with own certificates serves TLS only, and checks client
  certificates against `peer.client_ca_file` when it is set. It serves no
  h2c, no built-in TLS, and no invite or enrollment calls.

### Require TLS

- With `coordinator.require_tls: true`, a coordinator refuses every call over
  h2c, except the gRPC health service, with `Unauthenticated` and the
  message:

  ```text
  the coordinator requires TLS: upgrade this process, or remove peer.insecure: true
  ```

- It refuses the call and keeps the connection open, so the client can show
  the reason.
- A refused client logs, at level error, once until one of its calls to
  that coordinator succeeds:

  ```text
  The coordinator requires TLS. Upgrade this process, or remove peer.insecure: true.
  ```
- `coordinator.require_tls` has no effect on a coordinator with own
  certificates, which serves no h2c.

### Invites

#### Format

- An invite is `dagu1.<pin>.<secret>`.
- `<pin>` is the base64url SHA-256 of the DER encoding of the peer CA
  certificate, without padding: 43 characters.
- `<secret>` is 32 random bytes in base64url without padding: 43
  characters.
- An invite names no coordinator address, no label, and no worker ID.

#### Kinds

| Kind | Enrolls | Expiry when not given | Ends |
| --- | --- | --- | --- |
| `single_use` | One worker | 24 hours after creation | At its first enrollment, at expiry, or when deleted |
| `reusable` | Any number of workers | None | At expiry, if any, or when deleted |

- An invite created with an expiry ends at that expiry, whatever its kind.
- A deleted invite enrolls no worker. Workers that enrolled with it keep
  their certificates.

#### Invite Records

The `worker_invites` collection keeps one record per invite:

| Field | Value |
| --- | --- |
| `id` | A random ID, assigned on create |
| `kind` | `single_use` or `reusable` |
| `expires_at` | The expiry, or none |
| `address` | The `host:port` the invite was made for, or none |
| `created_by` | The user, or `peer token` for a call that carried only the peer token |
| `created_at` | The creation time |
| `use_count` | The number of enrollments it admitted |
| `last_used_at` | The time of its last enrollment, or none |

- No record holds the invite or its secret.
- Two coordinators never both accept one single-use invite.
- An invite ends when it is used up or expires. Once a day, each
  coordinator deletes the records of invites that ended more than 7 days
  ago. A reusable invite without an expiry never ends, and its record stays
  until the invite is deleted.

### Coordinator Calls

New coordinator calls create, list, and delete invites, remove a worker, and
report join readiness.

- Each takes the peer token, whatever `coordinator.require_token` says. A
  call without it fails with `Unauthenticated`. A call with a worker
  certificate fails with `PermissionDenied`.
- On a coordinator without a peer CA, each call except join readiness
  fails with `FailedPrecondition`. With own certificates:

  ```text
  invites need the built-in CA: this coordinator uses peer.cert_file and peer.key_file
  ```

  After a failure to create or read its generated CA:

  ```text
  invites need the built-in CA: this coordinator could not create or read its CA; see its start warning
  ```

  Join readiness still answers, and reports the reason in
  `builtinTLSReason`.
- The REST API, `dagu worker invite`, and the MCP action call them.

### Enrollment

```text
worker                                          coordinator
  | TLS dial; check the chain against the pin     |
  | Enroll(invite, CSR, worker ID, host,          |
  |        keeps certificate) ------------------->| check the invite; consume it if single use
  |<------------------ certificate, CA certificate | record it in worker_certificates
  | later calls: TLS + worker certificate         |
```

#### Worker Side

- An invite worker without a worker certificate dials over TLS and accepts
  the coordinator only when the CA certificate in the handshake has the
  invite's pin and the server certificate chains to it, names
  `coordinator.dagu.invalid`, and allows server authentication.
- It generates an ECDSA P-256 key. The private key never leaves the host.
- It sends `Enroll` with the invite, a certificate signing request for the
  key, its worker ID, its host name, and whether it can keep its
  certificate. It can keep it when its data directory is writable.
- On built-in TLS with a worker certificate, it does not send the peer
  token. A worker with `worker.invite` and `peer.token` logs this once at
  start.

#### Coordinator Side

`Enroll` needs no peer token. It succeeds when:

- it arrives over TLS
- the secret matches an invite that has not ended
- the certificate signing request verifies and holds an ECDSA P-256 key
- the invite is reusable, or the worker can keep its certificate
- the request's key is not the key of a revoked certificate

On success the coordinator, in this order, consumes a single-use invite,
increments `use_count` and sets `last_used_at`, records the certificate,
and returns the certificate and the CA certificate.

A worker that loses the response holds no certificate. Its retry with the
consumed single-use invite is refused like any used invite, and its message
asks for a new command from **Add worker**. A reusable invite enrolls the
retry as a new certificate.

#### Worker Certificate

- The subject common name is a random certificate ID.
- A URI subject alternative name `urn:dagu:invite:<invite ID>` names the
  invite.
- The extended key usage is client authentication only. It is not a CA.
- `notAfter` is `99991231235959Z`.
- The coordinator takes only the key from the request. Names and extensions
  the request asks for have no effect.

#### Certificate Records

The `worker_certificates` collection keeps one record per certificate:

| Field | Value |
| --- | --- |
| `id` | The certificate ID |
| `invite_id` | The invite it enrolled with |
| `state` | `active`, `removing`, or `revoked` |
| `worker_id` | The worker ID at its last heartbeat |
| `host` | The host name the worker reported |
| `environment` | `host`, `container`, or `kubernetes`, from the worker's heartbeat |
| `created_at` | The enrollment time |
| `last_heartbeat_at` | The time of its last recorded heartbeat, or none before the first |

- A record identifies its certificate's key, so `Enroll` can refuse that
  key after revocation.
- A heartbeat with a worker certificate that is not newer than the stored
  `last_heartbeat_at` writes nothing. Otherwise it writes the record when
  the stored `last_heartbeat_at` is more than 24 hours old, or none, or
  when the worker ID, host, or environment it reports changed and the
  stored `last_heartbeat_at` is at least a minute old. It writes the
  record again when it was pruned. It keeps the stored state.
- Once a day, each coordinator deletes the records in state `active` whose
  `last_heartbeat_at`, or `created_at` when it is none, is more than 90
  days old.
- Records in state `revoked` stay.
- A coordinator accepts a worker certificate when it chains to the peer CA
  and its ID is not in the revoked set. A certificate whose record was
  pruned still connects.

#### Worker Files

- An invite worker keeps its key, its worker certificate, the CA
  certificate from `Enroll`, and which invite it enrolled with in its data
  directory, readable by its user only, and reuses them across restarts.
  It holds its [lock](#worker-lock) there too.
- Run processes the worker starts use the same key and certificate.
- A worker that cannot write its data directory holds its key and
  certificate in memory only, takes no lock, and enrolls again at each
  start. Run processes it starts use the same credentials.

#### Worker Start

| Data directory at start | Behavior |
| --- | --- |
| No certificate | Enrolls with `worker.invite` |
| A certificate whose CA has the pin of `worker.invite`, or no `worker.invite` | Connects with it and ignores `worker.invite`, so a restart does not use up a single-use invite |
| A certificate whose CA does not have the pin of `worker.invite` | Generates a new key, enrolls with the invite, and replaces its files. A restart with the same invite then keeps the new certificate |
| A certificate the coordinator refuses as revoked, and an invite other than the one it enrolled with | Generates a new key, enrolls with the new invite, and replaces its files |
| A certificate the coordinator refuses as revoked, and the same invite or none | Exits with the message of [Messages](#messages) |
| A certificate, and `peer.insecure: true` | Row 1 of [Client Transport](#client-transport) applies: the worker uses h2c and the peer token, and warns at start that its certificate is unused |

#### Worker Lock

- Only a worker that holds or obtains a worker certificate takes the lock.
  Workers that share a data directory on the peer token, as in
  shared-filesystem mode, take none.
- The worker takes the lock before it enrolls or connects, holds it while
  it runs, and refreshes it at an interval shorter than 30 seconds.
- A lock not refreshed for 30 seconds is stale.
- A worker that finds the lock held waits at most 30 seconds. When the
  lock is released or goes stale within that wait, the worker takes it, so
  a restarted container whose previous process held the lock starts within
  30 seconds.
- When the 30 seconds pass with the lock still refreshed by another holder,
  the worker exits with code 1 and the message of [Messages](#messages).
  It never waits longer.

#### Environment

- A worker's heartbeat reports `kubernetes` when `KUBERNETES_SERVICE_HOST`
  is set, `container` when `/.dockerenv` or `/run/.containerenv` exists, and
  `host` otherwise.

### Revocation

- Revoking a certificate sets its record's state to `revoked` and adds its
  ID to the revoked set.
- Every call with a worker certificate is checked against the revoked set,
  so calls on open connections are refused too. A refused call fails with
  `Unauthenticated` and the message
  `this worker was removed from the coordinator`.
- The coordinator that revokes applies it at once. Every other coordinator
  rereads the revoked set, and the set of `removing` certificates, every 30
  seconds, also when idle.
- `Enroll` refuses a request whose key is the key of a revoked certificate.

#### Removal

A removal names a worker by its worker ID and takes `finish`, the default,
or `stop`:

| Worker | `finish` | `stop` |
| --- | --- | --- |
| Not connected: its last heartbeat is stale | Revoked at once | Revoked at once |
| Connected, no running tasks | `removing` | `removing` |
| Connected, with running tasks | `removing` | `removing`, and each run is canceled |

- In state `removing`:
  - `Poll` gives the worker no new tasks.
  - Calls for the runs the worker already holds keep working.
  - A task counts as held by the worker from the moment a coordinator hands
    it out until the worker reports its end or its lease expires. A task
    whose `Poll` response the worker never received counts until its lease
    expires.
  - The certificate is revoked when the worker holds no task, at least 30
    seconds after the certificate became `removing`, or when the worker's
    heartbeat goes stale.
  - The state survives a coordinator restart.
  - Every coordinator sees `removing` within 30 seconds, at its next reread,
    and gives the worker no task after that. A task one hands out before its
    reread is held, so it is waited for like the others.
- Other coordinators apply a revocation at their next reread, within 30
  seconds. A task one of them hands out before then ends through the
  stale-lease check, since the worker is refused at its next call to a
  coordinator that knows, as [Removed Worker](#removed-worker) states.
- With `stop`, each run gets the existing cancel request. The certificate is
  revoked when the runs end, but not before 30 seconds in `removing`, or 2
  minutes after the cancel requests, whichever comes first. Runs without a
  final status by then are left to the stale-lease check.
- A removal with `stop` of a worker in `removing` cancels its runs as above.
- A worker on the peer token cannot be removed. The removal fails and says
  to stop the worker on its host, or to change the peer token.

#### Removed Worker

- A worker whose certificate is refused as revoked stops the runs it holds,
  since it can no longer report them, keeps its files, and exits with code
  1 and the message of [Messages](#messages).
- In Kubernetes, it also writes the message to `/dev/termination-log`.
- Its runs end on the coordinator as they do when a worker stops sending
  heartbeats.

### Workers List

- Each worker in `GetWorkers` and `GET /api/v1/workers` has `connection`:
  `certificate`, `tls`, or `plaintext`. The coordinator takes it from the
  connection that carries the worker's heartbeat.
  - `certificate`: TLS with a worker certificate.
  - `tls`: TLS without one.
  - `plaintext`: h2c.
- A `certificate` worker also has `certificateId`, `inviteId`, and
  `removing: true` while its certificate is `removing`.
- A `plaintext` worker also has `plaintextReason`: `peer.insecure`,
  `no TLS source`, `tls failed on the network path`, or `older version`.
- A worker whose heartbeat reports an environment also has `environment`.
- The Dagu Cloud report adds `connection` to each entry of
  `health.workers[]`, on the plans that get the worker list today, within
  its size cap.

#### h2c Callers

- A coordinator keeps, in memory, the hosts that called it over h2c in the
  last 10 minutes, other than workers' calls, keyed by remote host (without
  the port) and reason, with the time of the last call. It keeps at most 64
  entries and drops the oldest first.
- `GET /api/v1/services/coordinator` returns them for each coordinator, with
  host, reason, and last call time.

### Join Readiness

A coordinator reports, from its own host:

| Field | Value |
| --- | --- |
| `listenHost`, `port` | `coordinator.host` and `coordinator.port` |
| `configFile` | The config file the coordinator read, or the default path it would read |
| `loopbackOnly` | True when `coordinator.host` is `localhost`, an address in `127.0.0.0/8`, or `[::1]` |
| `hostSource` | Where `coordinator.host` came from: `default`, `file` with its path, `env` with the variable, or `flag` with the flag |
| `advertise` | `coordinator.advertise` when set explicitly, else none |
| `environment` | `host`, `container`, or `kubernetes`, by the rule of [Environment](#environment) |
| `addresses` | When `environment` is `host`: for `coordinator.host` empty, `0.0.0.0`, or `[::]`, its interface addresses other than loopback and link-local ones; for another IP address that is not loopback, that address. Else none |
| `builtinTLS` | True only when the coordinator has a usable peer CA. False for a coordinator with own certificates, and for one that serves h2c only because it cannot create or read its generated CA |
| `builtinTLSReason` | Why `builtinTLS` is false: `ownCertificates` for a coordinator with own certificates, `caUnavailable` for one that cannot create or read its generated CA. None when `builtinTLS` is true |
| `ready` | True when `loopbackOnly` is false and `builtinTLS` is true |

- The report reads no file and starts nothing periodic.
- `GET /api/v1/workers/join-readiness` returns the report of each
  coordinator in the registry, and `lastInviteAddress`: the `address` of the
  newest invite that has one.

### REST API

| Endpoint | Effect |
| --- | --- |
| `POST /api/v1/workers/invites` | Body `{"reusable": false, "expiresIn": "24h", "address": "192.168.1.20:50055"}`, every field optional. Returns `201` with `{"id", "invite", "expiresAt"}`. Only this response contains the invite |
| `GET /api/v1/workers/invites` | Lists invites with `id`, `kind`, `createdBy`, `createdAt`, `expiresAt`, `useCount`, `lastUsedAt`, and `address`. No secret |
| `DELETE /api/v1/workers/invites/{id}` | Deletes the invite. `404` when none has the ID |
| `DELETE /api/v1/workers/{workerId}/certificate` | Removes the worker. `?runs=finish`, the default, or `?runs=stop`. Returns `{"state": "removing"}` or `{"state": "removed"}`. `404` when no worker has the ID |
| `GET /api/v1/workers/join-readiness` | Returns the reports of [Join Readiness](#join-readiness) |

- Each endpoint is admin only. An API key with the admin role can call it.
- `expiresIn` is a positive Go duration.
- `POST /api/v1/workers/invites` does not check `loopbackOnly`, since
  automation may reach a loopback listener through a tunnel. It refuses
  only a coordinator without built-in TLS.
- When the coordinator refuses or cannot be reached, the server answers:

  | Case | Status |
  | --- | --- |
  | A coordinator with own certificates, or one that could not create or read its CA (`FailedPrecondition`) | `409`, with the coordinator's message |
  | A worker on the peer token, for a removal | `409` |
  | No coordinator is running or reachable | `503` |
  | The server lacks the peer token (`Unauthenticated`) | `503`, saying the server needs `peer.token` or the coordinator's data directory |
- Each create, delete, and removal writes an audit entry with the invite ID
  or the worker and certificate IDs: `worker_invite_create`,
  `worker_invite_delete`, and `worker_remove`. No audit entry holds an
  invite.

### `dagu worker invite`

```text
dagu worker invite --address <host:port> [--reusable] [--expires <duration>]
```

- `--address` is required. It is stored as the invite's `address` and used
  in the printed command.
- The command calls the coordinator with the peer token, so it runs on any
  host whose processes have the peer token.
- It first reads join readiness. While a coordinator is not ready, it
  prints the change to make for each such coordinator, creates no invite,
  and exits with code 1. For `loopbackOnly`, the change follows
  `hostSource`, and without `builtinTLS`, it follows `builtinTLSReason`,
  as the dialog's step 1 does.
- On success it prints the invite kind and expiry, the invite, and the
  command to run on the worker host, and exits with code 0:

  ```text
  Invite (single use, expires 2026-11-28T10:00:00Z):
    dagu1.<pin>.<secret>
  Run on the worker host:
    dagu worker --worker.coordinators=192.168.1.20:50055 --worker.invite=dagu1.<pin>.<secret>
  ```

### `dagu worker --check`

- It loads the worker's configuration, prints the start line and the
  warnings of [Ignored Settings](#ignored-settings), and then for each
  address in `worker.coordinators`:
  - whether the address is reachable
  - the handshake result
  - the credential the worker would use
  - what the coordinator answered
- An invite worker also gets whether the coordinator's CA matches the
  invite's pin, and, with a worker certificate, its certificate ID, invite
  ID, and whether the coordinator accepts it.
- A failed handshake states the names the server certificate covers, the
  address dialed, and the CA the certificate chains to.
- It never enrolls and never consumes an invite.
- It exits with code 0 when every coordinator passes, and with code 1
  otherwise.

### Configuration Conflicts

A process whose settings contradict each other exits with code 1 at start
and prints the message:

| Settings | Message |
| --- | --- |
| `worker.invite` with `peer.insecure: true` | `worker.invite needs TLS: remove peer.insecure: true` |
| `worker.invite` with `peer.cert_file`, `peer.key_file`, `peer.client_ca_file`, or `peer.skip_tls_verify: true` | `worker.invite needs the built-in CA: remove <setting> or the invite` |
| `worker.invite` with a `peer.ca_cert_file` whose pin differs from the invite's | `peer.ca_cert_file is not the CA that worker.invite names: remove peer.ca_cert_file or use an invite from this CA` |
| `peer.ca_cert_file` or `peer.ca_key_file` with `peer.cert_file`, `peer.key_file`, or `peer.client_ca_file` | `use the built-in CA (peer.ca_cert_file, peer.ca_key_file) or your own certificates (peer.cert_file, peer.key_file, peer.client_ca_file), not both` |
| `peer.ca_cert_file` with `peer.skip_tls_verify: true` | `peer.skip_tls_verify turns off the check that peer.ca_cert_file makes: remove one` |
| `peer.ca_key_file` without `peer.ca_cert_file` on a coordinator | `peer.ca_key_file needs peer.ca_cert_file` |
| `peer.ca_cert_file` without `peer.ca_key_file` on a coordinator | `peer.ca_cert_file needs peer.ca_key_file on a coordinator` |
| A single-use invite with a data directory the worker cannot write | `a single-use invite needs a writable data directory: use a reusable invite, or make <path> writable` |

- `worker.invite` with `peer.token` is not a conflict. The worker starts,
  enrolls, and stops sending the token once it holds a certificate.
- A single-use invite's kind is known only to the coordinator. A worker that
  cannot write its data directory says so in `Enroll`, and the coordinator
  refuses a single-use invite without consuming it. The worker then exits
  with the message above.
- Each row involves a key that is new with this spec, so no configuration
  that starts today fails.
- A worker that holds a certificate and has `peer.insecure: true`, without
  `worker.invite`, is not a conflict: row 1 applies, as
  [Worker Start](#worker-start) states.

### Bring Your Own Certificates

- A coordinator with own certificates exits with code 1 at start when the
  key does not match the certificate, naming both files.
- It warns at start when the certificate does not name the address it
  advertises: `coordinator.advertise`, else the detected hostname, else
  `coordinator.host` when no hostname is detected. It also warns when the certificate has expired or
  expires within 14 days.
- A process that serves and calls with the same `peer.cert_file` while
  `peer.client_ca_file` is set, such as `start-all`, warns at start when the
  certificate does not allow client authentication. It does not fail: its
  local runs work, and dispatching to workers would fail.

### User Interface

#### Add Worker

- **Add worker** is on the Workers page, for admins.
- Step 1 shows each coordinator whose join readiness is not ready. `<port>`
  below is the reported `port`:
  - While `loopbackOnly`, the change for how Dagu was started:
    - in Kubernetes: set `coordinator.host` to `0.0.0.0` through the
      chart's values
    - in a container: recreate it with
      `-p <port>:<port> -e DAGU_COORDINATOR_HOST=0.0.0.0`
    - for `hostSource` `env` or `flag`: that variable or flag with the
      value `0.0.0.0`
    - otherwise: `configFile` and the lines `coordinator:` and
      `  host: 0.0.0.0` to add to it
  - Without `builtinTLS`, that invites need the built-in CA, and for
    `builtinTLSReason` `ownCertificates`, that the coordinator uses
    `peer.cert_file` and `peer.key_file`, or for `caUnavailable`, that it
    could not create or read its CA and to see its start warning.
  - Each change needs a restart.
- Step 1 keeps, even when ready, the reminder to publish port `<port>` in a
  container, and in Kubernetes the statement that workers outside the
  cluster need the coordinator Service to be of type `LoadBalancer` or
  `NodePort`.
- The dialog reads join readiness when it opens and again when the server
  is back after a restart. It does not poll.
- Step 2 is a required address field. It starts with `lastInviteAddress`, or
  with `advertise`, or empty. It suggests `addresses`, and none in a
  container, where it asks for the address of the machine that runs the
  container. It never fills in a host name, a container ID, or the
  browser's host.
- Step 3 shows only after steps 1 and 2. Its tab sets the invite kind:

  | Tab | Invite | Command |
  | --- | --- | --- |
  | Linux | Single use by default | `dagu worker` with `--worker.coordinators`, `--worker.labels` when given, and `--worker.invite` |
  | Docker | Single use by default | `docker run` with `-v dagu-worker:/var/lib/dagu` and `DAGU_WORKER_INVITE` |
  | Kubernetes | Reusable only | A Secret with the invite, and a Deployment that maps it to `DAGU_WORKER_INVITE` and whose data directory is an `emptyDir` |

- Labels and the address go into the command, not the invite.
- The dialog waits on the page's existing `/workers` refresh for a worker
  with the invite's ID, and then shows its ID and labels. Closing the dialog
  keeps the invite.
- When no worker arrives within 60 seconds, it lists the usual causes (a
  firewall or security group closes the port, a container does not
  publish it, the address is not reachable from the worker) and suggests
  `dagu worker --check` on the worker host.

#### Workers Page

- The `Connection` column shows `Certificate`, `TLS`, or
  `Plaintext · <reason>`, with these reasons:

  | `plaintextReason` | Shown |
  | --- | --- |
  | `peer.insecure` | `Plaintext · peer.insecure: true in its config` |
  | `no TLS source` | `Plaintext · no TLS source` |
  | `tls failed on the network path` | `Plaintext · TLS failed on the network path` |
  | `older version` | `Plaintext · older version` |

  `Plaintext` links to the change to make. It is a neutral line with no
  alert.
- **Remove** is on `certificate` workers only. A worker on the peer token
  shows how to stop it instead.
- With running runs, the confirmation lists them and offers
  **Remove after its runs finish** (`finish`, the default) and
  **Stop its runs and remove** (`stop`). A `removing` worker shows
  `Removing (<n> runs left)` and **Stop runs now**.
- For a worker on a reusable invite, the confirmation says the invite still
  admits new workers, names the other workers that use it, and offers to
  delete it.
- For a worker with `environment: kubernetes`, the confirmation says the pod
  keeps restarting until the Deployment is scaled down. A deleted pod comes
  back as a new pod with an empty data directory, which enrolls again
  while its reusable invite exists, so the confirmation offers to delete
  the invite first.
- **Invites** lists invites with kind, creator, creation and expiry times,
  use count, and last use, and deletes one.
- System status lists the [h2c callers](#h2c-callers).

#### MCP Action

- An admin-only server MCP action returns join readiness, and creates an
  invite when the coordinator is ready, with the same body as
  `POST /api/v1/workers/invites`.
- Only the result that creates the invite contains it. Each call is in the
  audit log.

### Secrets in Output

- No CA key, worker key, or peer token appears in an API response, CLI
  output, MCP result, log line, audit entry, or Dagu Cloud report.
- An invite appears only in the output of the call that creates it: the
  `POST /api/v1/workers/invites` response, the `dagu worker invite` output,
  or the MCP result that creates it. That output may show it more than
  once, such as the invite and the command that carries it. No later
  response, listing, log line, audit entry, or report contains it.

### Versions

| Coordinator | Client | Result |
| --- | --- | --- |
| Predates this spec | Any client without an invite | No CA and no registry field, so the client uses h2c |
| This spec | Predates this spec | The coordinator serves h2c on the same port |
| Predates this spec, beside one of this spec | Invite worker | The worker counts the older coordinator as unavailable and tries the next |
| Downgraded | Invite worker | The worker fails until it gets `peer.token` and `peer.insecure: true`, and loses `worker.invite`. Clients on the peer token keep working |
| Downgraded, its published CA certificate left behind | Client on the peer token | The older coordinator's registry entry has no `"tls": "builtin"`, so the client uses h2c. A client that dials before the entry changes falls back on the failed handshake |

- A remote worker keeps its configuration until it gets an invite.
- No run data, index, or migration changes.

## Constraints

- A coordinator rereads the revoked and `removing` sets every 30 seconds and
  prunes certificate and ended invite records once a day. A worker with a
  certificate refreshes its lock at an interval shorter than 30 seconds.
  Nothing else in this spec runs on a timer.
- The reread reads the records of `removing` and `revoked` certificates
  only, so its cost grows with removed workers, not with enrollments.
- A client that uses the published CA certificate reads it and the registry
  when it opens a connection. Connections are long-lived, so this does not
  grow with runs.
- A coordinator with a peer CA checks that the published CA certificate
  exists each time it refreshes its registry entry, which it does already.
  It adds no timer.
- Join readiness is read only when the dialog opens, when the server comes
  back while it is open, and by `dagu worker invite`.
- The clients on a host that fell back try TLS again at most once per
  coordinator address per 10 minutes, or after a refusal of
  `coordinator.require_tls`. They write the window record only when a
  window opens or ends, and keep at most 64 windows per host.
- A connection on h2c without a TLS source reads the published CA
  certificate and the registry at most every 10 seconds.
- A heartbeat writes a certificate record at most once a day, or once a
  minute while the worker ID, host, or environment it reports changes.
- Invites are read only at enrollment and when listed.
- The h2c callers take at most 64 entries per coordinator.

## Errors

### Messages

A worker prints the cause and the next step:

| Case | Message |
| --- | --- |
| The address does not answer | `Cannot reach coordinator at <host>:<port>`<br>`Check that the coordinator accepts workers from other hosts, that port <port> is open on the way, and that the address is right.` |
| `Enroll` refuses the invite | `The invite is invalid, expired, or already used`<br>`Get a new command from "Add worker". For several hosts, use a reusable invite.` |
| The CA in the handshake does not have the invite's pin | `<host>:<port> belongs to a different Dagu (its CA does not match the invite)`<br>`Check the coordinator address.` |
| A worker with a certificate and no `worker.invite` meets a coordinator whose CA did not sign that certificate | `<host>:<port> belongs to a different Dagu than the one this worker joined`<br>`Get a new command from "Add worker" to join it.` |
| The worker certificate is refused as revoked | `This worker was removed from the coordinator`<br>`Get a new command from "Add worker" to add it again.` |
| The lock is held by a live worker | `Another worker uses this data directory`<br>`Give each worker its own data directory or volume.` |
| The coordinator refuses h2c | `The coordinator requires TLS`<br>`Upgrade this process, or remove peer.insecure: true.` |

A client refused by `coordinator.require_tls` after a fallback also logs the
handshake error that caused the fallback.

### Cases

| Case | Result |
| --- | --- |
| A configuration conflict | The process exits with code 1 at start, with the message of [Configuration Conflicts](#configuration-conflicts) |
| `peer.ca_cert_file` that is not a CA certificate, whose key usage does not allow certificate signing, or a CA key that does not match it | The coordinator exits with code 1 at start, naming the files |
| Own certificates whose key does not match | The coordinator exits with code 1 at start, naming both files |
| `dagu peer init` into a directory with one of its files | Exits with code 1, writing nothing |
| A server certificate that does not verify, or lacks `coordinator.dagu.invalid` | The call fails. The client does not fall back. The only exception is a certificate on an [unlisted address](#unlisted-addresses) that does not chain to the published CA certificate. One that chains to it but lacks `coordinator.dagu.invalid` fails |
| A transport-level handshake failure of a worker certificate client or an invite worker | The call fails. The worker tries the next coordinator |
| An invite worker and a coordinator without built-in TLS | The worker counts it as unavailable and tries the next |
| A coordinator that cannot create or read its generated CA | A warning, and the coordinator serves h2c only, as [Peer CA](#peer-ca) states. With `coordinator.require_tls: true`, it exits with code 1 at start |
| No coordinator reachable for an invite worker | The worker keeps retrying as it does today, logging the message of the last failure |
| `Enroll` refuses the invite, no reachable coordinator has the invite's pin, or no reachable coordinator's CA signed the worker's certificate | The worker exits with code 1 and the message of [Messages](#messages) |
| `peer.insecure: false` without a TLS setting or a TLS source | The process exits with code 1 at start with `TLS enabled but no certificates provided`, as before this spec |
| `Enroll` over h2c | `Unauthenticated`: `enrollment needs TLS` |
| `Enroll` with an invite that has ended or does not exist | `PermissionDenied`: `the invite is invalid, expired, or already used` |
| `Enroll` with a single-use invite from a worker that cannot keep its certificate | `FailedPrecondition`; the invite is not consumed |
| `Enroll` with a certificate signing request that cannot be read, whose signature does not verify, or whose key is not ECDSA P-256 | `InvalidArgument`; the invite is not consumed |
| `Enroll` with the key of a revoked certificate | `PermissionDenied`: `this key belongs to a removed worker` |
| A call with a revoked worker certificate | `Unauthenticated`: `this worker was removed from the coordinator` |
| A call over h2c with `coordinator.require_tls: true` | `Unauthenticated`, with the message of [Require TLS](#require-tls) |
| An invite, removal, or join readiness call without the peer token | `Unauthenticated` |
| An invite, removal, or join readiness call with a worker certificate | `PermissionDenied` |
| An invite call or `Enroll` on a coordinator without a peer CA | `FailedPrecondition`, with the message of [Coordinator Calls](#coordinator-calls) |
| `POST /api/v1/workers/invites` with an invalid `expiresIn` or `address` | `400`, naming the field |
| An invite or removal endpoint called by a non-admin | `403` |
| `dagu worker invite` without the peer token | Exits with code 1, saying to set `peer.token` or to run on a host that reads the coordinator's data directory |
| `dagu worker invite` while a coordinator is not ready | Exits with code 1 and prints the change to make |

## Examples

### `start-all` After the Upgrade

A `start-all` from a config without `peer` keys:

```text
Coordinator transport: built-in TLS and h2c (from the generated CA)
Peer transport: built-in TLS (from the coordinator's data directory)
```

The data directory holds the generated CA and the published CA
certificate. The scheduler, the server, and local runs call the coordinator
over TLS. The same data directory started again keeps the same CA.

### Two Transports on One Port

A coordinator of this spec on port 50055. A worker that predates this spec,
with `peer.insecure: true`, and a worker of this spec that shares the data
directory, with `worker.coordinators: coordinator:50055`, both connect.

- `GET /api/v1/workers` shows the old worker with `connection: plaintext`
  and `plaintextReason: older version`, and the new one with
  `connection: tls`.
- With `coordinator.require_tls: true`, the old worker's calls fail with the
  [Require TLS](#require-tls) message, and the new one keeps working.

### Ignored File

A worker on another host, sharing no data directory:

```yaml
peer:
  client_ca_file: /etc/dagu/ca.crt
worker:
  coordinators: coord.example.com:50055
```

```text
Peer TLS setting ignored: peer.client_ca_file (/etc/dagu/ca.crt) has no effect while peer.insecure is not false; set peer.insecure: false to use it
Peer transport: h2c (from no TLS source)
```

The worker connects over h2c, as before the upgrade.

### Plaintext Proxy

A worker shares the data directory, and its path to the coordinator passes
a proxy that expects plaintext gRPC.

```text
Peer transport: built-in TLS (from the coordinator's data directory)
Peer transport to coordinator:50055: h2c (from tls failed on the network path)
```

The worker keeps working over h2c and sends
`dagu-peer-h2c-reason: tls failed on the network path`. The Workers page
shows `Plaintext · TLS failed on the network path`. After 10 minutes, its
next call opens a new connection that tries TLS again.

### Single-Use Invite

1. `dagu worker invite --address 192.168.1.20:50055` prints an invite.
2. `dagu worker --worker.coordinators=192.168.1.20:50055 --worker.invite=<invite>`
   on host A logs `Peer transport: built-in TLS (from worker.invite)`,
   enrolls, and keeps its certificate in its data directory. The invite's
   `use_count` is 1.
3. Host A restarts the worker. It logs
   `Peer transport: built-in TLS (from worker certificate)` and does not
   call `Enroll`.
4. Host B runs the same command and exits with
   `The invite is invalid, expired, or already used`.

### Remove With Running Runs

Worker `worker-a1` holds a certificate and runs two tasks.

1. `DELETE /api/v1/workers/worker-a1/certificate` returns
   `{"state": "removing"}`. The Workers page shows `Removing (2 runs left)`.
2. `Poll` gives `worker-a1` no task. Both runs finish and report their
   status.
3. Once `worker-a1` holds no task, by the coordinator's count, and at
   least 30 seconds have passed since the removal, the certificate is
   revoked. The worker's next call fails, and it exits with
   `This worker was removed from the coordinator`.
4. A restart of `worker-a1` with the same invite exits with the same
   message. A restart with a new invite enrolls with a new key.

### Two Workers on One Volume

Two containers mount `dagu-worker:/var/lib/dagu`, with one reusable invite.
The first enrolls and holds the lock. The second waits 30 seconds while the
first keeps refreshing the lock, then exits with code 1 and
`Another worker uses this data directory`.

### Configuration Conflicts

```text
$ dagu worker --worker.invite=dagu1.<pin>.<secret> --peer.insecure=true
worker.invite needs TLS: remove peer.insecure: true

$ dagu coordinator --peer.ca-cert-file=ca.crt --peer.ca-key-file=ca.key --peer.cert-file=server.crt --peer.key-file=server.key
use the built-in CA (peer.ca_cert_file, peer.ca_key_file) or your own certificates (peer.cert_file, peer.key_file, peer.client_ca_file), not both
```

### `dagu peer init`

```text
$ dagu peer init --out ./peer
peer/ca.crt
peer/ca.key
peer/token

$ dagu peer init --out ./peer
peer/ca.crt exists; dagu peer init writes no file over an existing one
```

The second run exits with code 1.

## Conformance

Black-box tests go in `conformance/spec108_peer_transport/`. Each part of
#4292 adds the tests for the behavior it implements: h2c and TLS on one
port, `start-all` on TLS after the upgrade with no setting, the fallback
behind a plaintext proxy, enrollment, a restart that does not consume a
single-use invite, **Remove** holding across a restart, revocation of an
open connection, and a second worker refused on one data directory.
