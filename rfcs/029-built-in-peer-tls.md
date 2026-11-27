# RFC 029: Built-in Peer TLS and Worker Invites

- **Status:** Proposed
- **Date:** 2026-11-27
- **Affected areas:** `service/coordinator`, `service/worker`, `cmn/config`,
  `persis` (stores, service registry), `proto`, `cmd`, `service/frontend`
  (API, UI, MCP), `charts`, `specs`, `conformance`, `docs`, and Dagu Cloud
  (console)

## Summary

Encrypt coordinator traffic by default and make adding a worker one copied
command. The coordinator runs its own certificate authority, issues its own
certificate, and renews it. A worker on another host joins with an invite: one
string that names the coordinator's CA and lets the worker enroll for a
certificate of its own. Processes that share the coordinator's data directory
need no setup. Users never create, copy, renew, or match a certificate to an
address.

## Problem statement

Turning on TLS for coordinator traffic today takes a private CA, certificate
files on every host, and settings whose meaning depends on the process:

- `peer.insecure` defaults to `true` and wins over every TLS file. A worker
  given `peer.client_ca_file` without `peer.insecure: false` dials h2c and
  fails the handshake with an unhelpful error.
- `peer.client_ca_file` is the CA that checks clients on the coordinator, and
  the CA that checks the coordinator on a client.
- `start-all` reads one `peer` block for serving and for dialing. Under mTLS,
  one certificate must be valid for both server and client authentication,
  and one CA must sign every certificate.
- The coordinator certificate must name the address each client dials. A
  Kubernetes Service name, a load balancer, or a changed IP breaks the
  handshake.
- Nothing renews a certificate before it expires.

A worker on another host also needs the peer token, copied by hand from the
coordinator host. So TLS between peers is rare in practice, and adding a
remote worker stays a manual, error-prone step.

Docker Swarm, kubeadm, and k3s solve the same problem with join tokens that
carry a hash of the cluster CA. A joining node checks the manager without a CA
file, and the cluster issues and rotates node certificates.

## Goals

1. Coordinator traffic uses TLS by default, with no setting, on a fresh
   install and after an upgrade.
2. A worker on another host joins with one copied command from the Web UI,
   the CLI, or Dagu Cloud.
3. A worker that joined with an invite holds its own certificate, not the
   peer token. Removing it keeps it out.
4. No certificate to create, copy, renew, or match to an address.
5. Nothing that works today breaks on upgrade. Old and new workers,
   schedulers, and coordinators work together in either upgrade order.
6. Every failure says what is wrong and what to do.

## Non-goals

- Limiting which coordinator RPCs a worker certificate may call, beyond
  invite and certificate management. Workers call `Dispatch` and
  `GetWorkers` for sub-DAGs and placement today. See open question 3.
- Binding a worker ID to its certificate. See open question 2.
- Rotating the CA. See open question 4.
- Changing the meaning of `peer.cert_file`, `peer.key_file`,
  `peer.client_ca_file`, or `peer.skip_tls_verify`.
- Web UI/API HTTPS and the HTTP health endpoints.
- Reaching a coordinator behind NAT. Workers still need to reach its port.

## User experience

### Same host or shared data directory

No setup. `start-all`, Compose with a shared volume, and workers that share
the data directory switch to TLS after the upgrade. The Workers page shows
them as `TLS`.

### A worker on another host

The Workers page gets **Add worker**, for admins:

```text
┌ Add worker ───────────────────────────────────────┐
│ [Linux] [Docker] [Kubernetes]                      │
│                                                    │
│ Coordinator        coord.example.com:50055         │
│ Labels (optional)  gpu=true                        │
│ Invite             (•) Single use, expires in 24h  │
│                    ( ) Reusable (containers,       │
│                        autoscaling)                │
│                                                    │
│ dagu worker \                                      │
│   --worker.coordinators=coord.example.com:50055 \  │
│   --worker.labels=gpu=true \                       │
│   --worker.invite=dagu1.k3f9...           [Copy]   │
│                                                    │
│ Waiting for the worker...                          │
│   -> worker-a1 connected (gpu=true)                │
└────────────────────────────────────────────────────┘
```

- The coordinator address is the one the coordinator registered, and stays
  editable. A loopback or `0.0.0.0` address is flagged before the command
  can be copied, since workers on other hosts cannot reach it.
- A single-use invite expires after 24 hours unless used. A reusable invite
  lasts until deleted. It suits containers without a persistent volume and
  autoscaled workers, which enroll at each start.
- The dialog waits. When a worker enrolls with this invite and sends its
  first heartbeat, the dialog shows its ID and labels. Closing the dialog
  keeps the invite.
- The Docker tab passes the invite as `DAGU_WORKER_INVITE`. The Kubernetes
  tab creates a Secret and maps it to that variable.
- Labels and the coordinator address go into the command, not the invite.

`dagu worker invite [--reusable] [--expires <duration>]` prints the invite
and the command. It runs on any host whose processes call the coordinator
with the peer token.

### Kubernetes with the chart

Distributed mode needs no TLS values. The chart creates the CA as a Secret on
first install and keeps it on upgrade, like the planned peer token Secret.

### Dagu Cloud

- The Workers section of a server page has the same **Add worker**. The
  console asks the server through the relay as the signed-in person, under
  the same access rule as other console actions, and needs admin. It shows
  the invite once and keeps no copy; the call log records the invite ID.
- Each worker shows how it connects and when its certificate expires.
- Dagu Cloud never holds the CA or a private key, and worker traffic never
  passes through it.

### Workers page

- A `Connection` column: `Certificate`, `TLS`, or `Plaintext`. `Plaintext`
  links to the upgrade steps.
- **Remove** on a certificate worker revokes its certificate. The worker is
  refused within 30 seconds and cannot come back without a new invite. On a
  worker that holds the peer token, the confirmation says it can reconnect
  until the peer token changes.
- **Invites** lists each invite with its kind, creator, creation and expiry
  times, use count, and last use. **Delete** stops new enrollments with it;
  workers that already enrolled keep their certificates.

### Failures

Worker messages name the cause and the next step:

```text
Cannot reach coordinator at coord.example.com:50055
  Check that the port is open and coordinator.advertise is correct.
The invite is invalid, expired, or already used
  Get a new command from "Add worker".
coord.example.com:50055 belongs to a different Dagu (its CA does not match the invite)
  Check the coordinator address.
The worker certificate expired and the invite is no longer valid
  Get a new command from "Add worker".
The coordinator requires TLS
  Upgrade this process, or remove peer.insecure: true.
```

`dagu worker --check` dials each coordinator, prints reachability, the
coordinator's identity, and the credential the worker would use, then exits.

## Design

### Overview

```text
                      coordinator
          ┌─────────────────────────────────────────┐
          │ peer CA: generated once, shared through │
          │   the data dir or the chart Secret      │
          │ server certificate: in memory, renewed  │
          │ worker_invites, worker_certificates     │
          └───────▲──────────────────────▲──────────┘
                  │ TLS + peer token     │ TLS + worker certificate
                  │                      │
  scheduler, UI/API, CLI, and workers    remote worker
  sharing the data dir or chart Secrets  joined with an invite
```

Processes that can read the coordinator's data directory, or that the chart
gives its Secrets, already hold the peer token. They verify the coordinator
with the peer CA and keep sending the token. Only a worker that joins with an
invite enrolls for a certificate, and the certificate replaces the token for
that worker.

### Peer CA

- On first start, a coordinator without `peer.cert_file` and
  `peer.key_file` generates an ECDSA P-256 CA valid for 10 years. It stores
  the CA next to the peer token and creates it with the same exclusive
  write, so coordinators sharing a data directory share one CA.
- `peer.ca_cert_file` and `peer.ca_key_file` supply the CA instead: from
  the chart, from a company CA, or for coordinators that share no data
  directory. On a client, `peer.ca_cert_file` alone is the CA that signs
  coordinator certificates.

### Coordinator certificate

- Each coordinator issues its own server certificate from the CA at start,
  keeps it in memory, and replaces it at half of its 30-day life without a
  restart.
- The certificate names `coordinator.dagu.invalid`, plus the advertised
  host and the hostname for tools such as `grpcurl`.
- Dagu clients check the chain against the peer CA and the name
  `coordinator.dagu.invalid`, not the dialed address. Only coordinator
  certificates carry that name and server authentication, so a worker
  certificate cannot pass as a coordinator. The `.invalid` name never
  resolves, so it cannot clash with a real host.

### TLS and plaintext on one port

- The coordinator reads the first byte of each connection. A TLS handshake
  record starts TLS; anything else is served as h2c, as today. One gRPC
  server serves both through a transport credential that peeks.
- TLS requests a client certificate and accepts a connection without one. A
  certificate that does not chain to the peer CA fails the handshake.
- `coordinator.require_tls` (default `false`) refuses plaintext calls with
  `Unauthenticated` and the message above. It refuses the call rather than
  closing the connection, so the client can show the reason.
- With `peer.cert_file` and `peer.key_file` set, the coordinator behaves as
  today: TLS only, and client certificates checked against
  `peer.client_ca_file` when set. The built-in CA, invites, and plaintext on
  the same port are off. **Add worker** explains that invites need the
  built-in CA.

### Client transport

`peer.insecure` changes from default `true` to unset. A client picks its
transport in this order:

| Configuration | Transport |
| --- | --- |
| `peer.insecure: true` | h2c, as today |
| `peer.cert_file`, `peer.client_ca_file`, or `peer.skip_tls_verify` set | TLS with those files, as `peer.insecure: false` does today |
| A worker invite or certificate, `peer.ca_cert_file`, or a registry entry that reports built-in TLS | Built-in TLS |
| None of these | h2c, as today |

- `peer.insecure: true` remains for networks that already encrypt traffic,
  such as a service mesh.
- Each coordinator adds `tls: builtin` to the entry it registers. A client
  that finds coordinators in the registry reads the CA from the same data
  directory. Older processes ignore the field.

### Invites

- An invite is `dagu1.<pin>.<secret>`: the pin is the base64url SHA-256 of
  the CA certificate (DER), and the secret is 32 random bytes in base64url.
- A new `worker_invites` collection keeps the SHA-256 of the secret, the
  kind, expiry, creator, use count, and last use. A single-use invite is
  consumed with a compare-and-swap, so two coordinators cannot both accept
  it.
- `worker.invite` (`--worker.invite`, `DAGU_WORKER_INVITE`) carries the
  invite. Config output masks it like `peer.token`.
- New coordinator RPCs create, list, and delete invites and revoke
  certificates. They take the peer token and refuse worker certificates.
  The REST API, the CLI, and MCP call them. The API requires admin and
  records each call in the audit log.

### Enrollment and renewal

```text
worker                                         coordinator
  │ TLS dial; check the chain against the pin     │
  │ Enroll(invite, CSR, worker ID, host) ────────▶│ check the invite; consume it if single use
  │◀──────────────────── certificate, 30 days ────│ record it in worker_certificates
  │ later calls: TLS + certificate                │
  │ Renew(CSR) at half life ─────────────────────▶│ check the current certificate
```

- The worker generates its own key. The private key never leaves the host.
- The certificate names a random certificate ID and the invite ID, and
  allows client authentication only.
- The worker keeps the key and certificate in its data directory, readable
  by its user only, and reuses them across restarts. Run processes the
  worker starts use the same files. A worker with a valid certificate
  ignores its invite, so restarts do not use up a single-use invite.
- A worker whose certificate expired enrolls again with its invite when the
  invite is still valid, and otherwise exits with the message above.
- A valid, unrevoked certificate counts as the peer token, including under
  `coordinator.require_token`.

### Revocation

- **Remove** marks the certificate revoked in `worker_certificates`. Every
  call checks the client certificate against the revoked set, so open
  connections are refused too.
- The coordinator that handles the removal applies it at once. Other
  coordinators reread the revoked set every 30 seconds.
- A revoked entry is dropped once its certificate would have expired.

### Workers list

- Each worker gains `connection` (`certificate`, `tls`, or `plaintext`) and,
  for certificates, the certificate ID, the invite ID, and the expiry. The
  coordinator takes them from the connection that carries the worker's
  heartbeat.
- `GET /workers` returns them. The **Add worker** dialog uses the page's
  existing `/workers` refresh and waits for a worker with its invite ID.
- The Dagu Cloud report adds `connection` and the certificate expiry to
  `health.workers[]`, on paid plans as today.

### Bring your own certificates

The existing keys keep working, with three fixes:

- Setting any TLS file selects TLS, so `peer.insecure: false` is no longer
  needed. An explicit `peer.insecure: true` still selects h2c.
- At start, the coordinator fails when the key does not match the
  certificate, and warns when the certificate does not name
  `coordinator.advertise` or expires within 14 days. A process that serves
  and dials with the same certificate under mTLS, such as `start-all`, fails
  at start when the certificate lacks client authentication.
- `dagu worker --check` reports handshake failures in these terms: the names
  the certificate covers, the address dialed, and the CA it chains to.

### Chart

- On install, the chart generates the CA with `genCA` and keeps it on
  upgrade with `lookup`.
- Coordinator pods mount the CA certificate and key as `peer.ca_cert_file`
  and `peer.ca_key_file`. Scheduler, UI/API, and worker pods mount the
  certificate only.
- The rendered config drops `peer.insecure: true`.

### Dagu Cloud

- **Add worker** calls a new server MCP action that creates an invite, as
  the signed-in person.
- A `worker_certificate_expiring` alert, on by default, fires 3 days before
  a worker certificate expires. Renewal starts 15 days before expiry, so the
  alert means renewal keeps failing.

## Compatibility

- **Config:** `peer.insecure` defaults to unset instead of `true`. A process
  behaves as today unless it has a built-in TLS source from the table above.
  New keys: `peer.ca_cert_file`, `peer.ca_key_file`, `worker.invite`, and
  `coordinator.require_tls`.
- **Persisted data:** registry entries gain `tls`, which older processes
  ignore. `worker_invites` and `worker_certificates` are new collections.
  No run data, index, or migration changes.
- **Upgrade order:** with an old coordinator and new clients, there is no
  CA and no registry flag, so clients use h2c. With a new coordinator and
  old clients, the coordinator accepts h2c on the same port. Remote workers
  keep their config until they get an invite.
- **Mixed coordinators:** a worker with an invite treats a coordinator
  without built-in TLS as unavailable and tries the next one. Upgrade every
  coordinator before inviting workers.
- **Downgrade:** an older coordinator ignores the CA and the new
  collections and serves h2c only. Workers that joined with an invite fail
  until they get `peer.token`. Processes that use the peer token keep
  working.
- **Bring your own certificates:** unchanged, except that a TLS file now
  selects TLS without `peer.insecure: false`.

## Performance

- **Handshakes:** one TLS handshake per gRPC connection. Connections are
  long-lived, so the cost does not grow with runs.
- **Enrollment:** one RPC and one signature, well under a millisecond, per
  worker without a valid certificate. That is once per host, or once per
  start for containers on a reusable invite. Renewal runs every 15 days per
  worker.
- **Revocation:** each coordinator rereads the revoked set every 30 seconds,
  also when idle. It is one small collection read, independent of DAGs and
  runs, and it holds only revoked certificates that have not expired.
  Invites are read on enrollment only.
- **Workers list:** up to four fields per worker, about 150 bytes, on the
  existing 2-second refresh of an open Workers page. The Dagu Cloud report
  grows by about 60 bytes per worker, for at most 256 workers.
- **Upgrade:** generating the CA takes milliseconds, once.

## Rollout

1. Bring-your-own fixes: TLS selection, start checks, `dagu worker --check`.
2. Built-in CA, coordinator certificate, one-port TLS and h2c, client
   transport rule, registry flag.
3. Invites, enrollment, renewal, revocation, Workers page, API, CLI, MCP.
4. Chart.
5. Dagu Cloud console and alert.

Specs under `specs/` state the transport selection, enrollment, and
revocation contracts. Conformance tests cover h2c and TLS on one port, the
single-use invite, renewal, and revocation of an open connection.

## Open questions

1. Should `coordinator.require_tls` default to `true` in v3.0, with
   `coordinator.require_token`?
2. Should a worker certificate be limited to the worker ID it enrolled with?
   The default ID (`hostname@PID`) changes at each start, so this needs a
   stable worker ID first.
3. Once sub-DAG dispatch from workers moves to a narrower RPC, should worker
   certificates lose `Dispatch`?
4. CA rotation: issue from a new CA while trusting both, then drop the old
   one. Is it needed before v3.0?
5. Should **Add worker** in Dagu Cloud be paid only, like worker monitoring?
