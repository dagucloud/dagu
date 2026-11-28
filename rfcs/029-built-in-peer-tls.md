# RFC 029: Built-in Peer TLS and Worker Invites

- **Status:** Accepted
- **Date:** 2026-11-27
- **Accepted:** 2026-11-27
- **Affected areas:** `service/coordinator`, `service/worker`, `cmn/config`,
  `persis` (stores, service registry), `proto`, `cmd`, `api/v1`,
  `service/frontend` (API, UI, MCP), `charts`, `specs`, `conformance`,
  `docs`, and Dagu Cloud (console)

## Summary

Encrypt coordinator traffic by default and make adding a worker one copied
command. The coordinator runs its own certificate authority and issues its
own certificate. A worker on another host joins with an invite: one
string that names the coordinator's CA and lets the worker enroll for a
certificate of its own. Processes that share the coordinator's data directory
need no setup. Users never create, copy, renew, or match a certificate to an
address. Certificates that Dagu issues have no expiry date; removing a worker
is what ends its certificate.

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
   install and after an upgrade, wherever a process can get the CA without
   a setting and the network path carries TLS. Elsewhere it stays on h2c,
   and the UI says why and what to set.
2. A worker on another host joins with one copied command from the Web UI,
   the CLI, or Dagu Cloud. Until the coordinator can accept it, the dialog
   shows what to change instead of a command.
3. A worker that joined with an invite holds its own certificate, not the
   peer token. Removing it keeps that worker out across restarts, whichever
   kind of invite it used. A reusable invite still admits new hosts and new
   pods until it is deleted.
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
- Changing the default listen address, or changing it from the UI. See
  [Alternatives](#alternatives).

## User experience

### Same host or shared data directory

No setup. `start-all`, Compose with a shared volume, and workers that share
the data directory switch to TLS after the upgrade. The Workers section of
System status shows them as `TLS`.

### A worker on another host

The Workers section of System status gets **Add worker**, for admins. It shows a command only
when the coordinator can accept it, and until then shows what to change:

```text
┌ Add worker ─────────────────────────────────────────────┐
│ 1  Accept workers from other hosts         Needs change │
│    The coordinator listens on 127.0.0.1, so only this   │
│    machine can connect. Add this to                     │
│    /home/me/.config/dagu/config.yaml and restart Dagu:  │
│                                                         │
│      coordinator:                                       │
│        host: 0.0.0.0                          [Copy]    │
│                                                         │
│ 2  Address workers use to reach this machine            │
│    [ 192.168.1.20                 ▾]  port 50055        │
│                                                         │
│ 3  Worker                           Shown after 1 and 2 │
└─────────────────────────────────────────────────────────┘
```

Once the coordinator accepts workers from other hosts and an address is
chosen:

```text
┌ Add worker ─────────────────────────────────────────────┐
│ 1  Accept workers from other hosts                Ready │
│ 2  Address  [ 192.168.1.20            ▾]  port 50055    │
│ 3  Worker   [Linux] [Docker] [Kubernetes]               │
│    Labels (optional)  gpu=true                          │
│    Invite   (•) Single use, expires in 24h              │
│             ( ) Reusable (containers, autoscaling)      │
│                                                         │
│    dagu worker \                                        │
│      --worker.coordinators=192.168.1.20:50055 \         │
│      --worker.labels=gpu=true \                         │
│      --worker.invite=dagu1.k3f9...            [Copy]    │
│                                                         │
│    Waiting for the worker...                            │
│      -> worker-a1 connected (gpu=true)                  │
└─────────────────────────────────────────────────────────┘
```

- **Step 1** reads what the coordinator listens on. When it listens on a
  loopback address only, step 1 shows the change for how Dagu was started:
  - from a config file: the file's path and the lines to add;
  - from an environment variable or a flag: that variable or flag;
  - in a container: recreate it with
    `-p 50055:50055 -e DAGU_COORDINATOR_HOST=0.0.0.0`;
  - in Kubernetes: see [Kubernetes with the chart](#kubernetes-with-the-chart).

  The change needs a restart; the dialog checks again when the server is
  back. In a container, step 1 keeps the reminder to publish port 50055
  even when ready, since Dagu cannot see the published ports.
- **Step 2** is a required field. It starts with the address of the last
  invite, or with `coordinator.advertise` when that is set explicitly, and
  is empty otherwise. It suggests this host's non-loopback addresses. In a
  container it suggests none and asks for the address of the machine that
  runs the container. It never fills in a hostname, a container ID, or the
  browser's host.
- **Step 3:**
  - A single-use invite expires after 24 hours unless used, and enrolls one
    worker that keeps its data directory. A reusable invite lasts until
    deleted and enrolls any number of workers.
  - The tab sets the kind:

    | Tab | Invite | Why |
    | --- | --- | --- |
    | Linux | Single use by default | The host keeps its data directory |
    | Docker | Single use by default | The command mounts a named volume (`-v dagu-worker:/var/lib/dagu`), so a restarted container keeps its certificate. For several containers, choose reusable and give each container its own volume |
    | Kubernetes | Reusable only | Pods are replaced and scaled, and a new pod has no certificate, so a single-use invite would fail on the second pod |

  - The Docker tab passes the invite as `DAGU_WORKER_INVITE`. The
    Kubernetes tab creates a Secret and maps it to that variable in a
    Deployment whose data directory is an `emptyDir`. A container restarted
    in the same pod keeps its certificate; a new pod has none.
  - Labels and the address go into the command, not the invite.
- The dialog waits. When a worker enrolls with this invite and sends its
  first heartbeat, the dialog shows its ID and labels. Closing the dialog
  keeps the invite.
- When no worker arrives within 60 seconds, the dialog lists the usual
  causes: a firewall or security group closes port 50055, a container does
  not publish it, or the address is not reachable from the worker. It
  suggests `dagu worker --check` on the worker host, whose error names the
  cause. Only the worker can tell whether the address works.

`dagu worker invite --address <host:port> [--reusable] [--expires <duration>]`
prints the invite and the command. It runs on any host whose processes call
the coordinator with the peer token. While the coordinator listens on a
loopback address only, it prints the change from step 1 and exits with an
error.

### Kubernetes with the chart

- Distributed mode needs no TLS values. The coordinator, scheduler, and UI
  share the data volume, so they use built-in TLS from the CA the
  coordinator generates there.
- Workers in the cluster come from `workerPools`. They share no data
  directory, so they stay on h2c until `peer.existingCASecret` names a
  Secret with the CA, made with `dagu peer init` and `kubectl create
  secret`, or by a secret operator. System status shows them as
  `Plaintext · no TLS source` with that value.
- The chart does not generate the CA itself. Argo CD and Flux render charts
  with `helm template`, where `lookup` finds nothing, so a CA generated by
  the chart would change on every sync.
- Workers outside the cluster need the gRPC port exposed with
  `coordinator.service.type: LoadBalancer` or `NodePort`. The UI's Ingress
  does not carry gRPC. Dagu cannot see the Service type from inside the
  pod, so in a cluster step 1 states this for workers outside it.

### Dagu Cloud

- The Workers section of a server page has the same **Add worker**. The
  console asks the server through the relay as the signed-in person, under
  the same access rule as other console actions, and needs admin. It shows
  the invite once and keeps no copy; the call log records the invite ID.
- Each worker shows how it connects.
- Dagu Cloud never holds the CA or a private key, and worker traffic never
  passes through it.

### Without the copied command

Config files, images, and provisioning tools set the same keys the command
sets. Each process needs at most these settings:

| Process | Settings |
| --- | --- |
| Coordinator | `coordinator.host: 0.0.0.0` when workers on other hosts connect, plus a published port 50055 in a container; `peer.ca_cert_file` and `peer.ca_key_file` to use a CA made elsewhere |
| Scheduler, UI/API, CLI, or worker sharing the data directory | None |
| The same, without the data directory | `peer.token` and `peer.ca_cert_file` |
| Worker on another host, with an invite | `worker.coordinators` and `worker.invite` |
| Worker on another host, without an invite | `worker.coordinators`, `peer.token`, and `peer.ca_cert_file` |

```yaml
worker:
  coordinators: coord.example.com:50055
  invite: dagu1.k3f9...
```

The environment variables are `DAGU_WORKER_COORDINATORS` and
`DAGU_WORKER_INVITE`.

- **One config for several hosts:** use a reusable invite. A single-use
  invite enrolls the first host only.
- **Invites from automation:** `POST /api/v1/workers/invites` returns a new
  invite, and an admin API key can call it. See [Invites](#invites).
- **Before the coordinator exists:** an invite needs a running coordinator.
  When one apply creates the coordinator and its workers,
  `dagu peer init --out <dir>` first writes a CA certificate, its key, and a
  peer token. The coordinator gets all three; other processes get the
  certificate and the token, as in the table. Terraform's
  `tls_self_signed_cert` or the chart's `genCA` can make the CA instead.
  These workers authenticate with the peer token, not a certificate of
  their own, so **Remove** cannot keep them out; changing the peer token
  does.
- **Checking the result:** every process logs at start which transport it
  chose and the setting that chose it. See
  [Chosen transport](#chosen-transport).

### Workers section of System status

The workers list is the Workers section of System status; Dagu has no
separate Workers page.

- A `Connection` column: `Certificate`, `TLS`, or `Plaintext`. `Plaintext`
  links to the upgrade steps.
- **Remove** is on certificate workers. It revokes the certificate, and the
  worker stays out across restarts, on a single-use or a reusable invite,
  until it is given a new invite.
- When the worker has running runs, the confirmation lists them and
  offers:
  - **Remove after its runs finish**, the default: the worker gets no new
    runs from then on, and its certificate is revoked when its last run
    ends. The page shows `Removing (2 runs left)` and keeps **Stop runs
    now** available.
  - **Stop its runs and remove**: the runs are stopped as with **Stop**,
    and the certificate is revoked once they end.

  A worker that is not connected is removed at once. Its runs end as they
  do today when a worker stops sending heartbeats.
- A reusable invite still lets new hosts join: a new container without a
  volume, or a host whose data directory was wiped, has no certificate and
  enrolls as a new worker. For a worker on a reusable invite, the
  confirmation says so and offers to delete the invite too, naming the
  other workers that use it.
- A removed worker in Kubernetes exits, and Kubernetes restarts its
  container in the same pod. The `emptyDir` keeps the revoked certificate,
  so the container exits again and the pod shows `CrashLoopBackOff`. The
  worker writes the reason to its termination message, so
  `kubectl describe pod` shows it. For such a worker, the confirmation says
  the pod keeps restarting until the Deployment is scaled down. A deleted
  pod comes back with an empty `emptyDir` and enrolls again while its
  reusable invite exists, so the confirmation offers to delete the invite.
- A worker on the peer token has no **Remove**, since it would reconnect at
  once. The page says to stop it on its host, or to change the peer token.
- **Invites** lists each invite with its kind, creator, creation and expiry
  times, use count, and last use. **Delete** stops new enrollments with it;
  workers that already enrolled keep their certificates. An invite that was
  used up or expired shows as such for 7 days, then leaves the list.

### Failures

Worker messages name the cause and the next step:

```text
Cannot reach coordinator at coord.example.com:50055
  Check that the coordinator accepts workers from other hosts, that port
  50055 is open on the way, and that the address is right.
The invite is invalid, expired, or already used
  Get a new command from "Add worker". For several hosts, use a reusable invite.
coord.example.com:50055 belongs to a different Dagu (its CA does not match the invite)
  Check the coordinator address.
This worker was removed from the coordinator
  Get a new command from "Add worker" to add it again.
Another worker uses this data directory
  Give each worker its own data directory or volume.
The coordinator requires TLS
  Upgrade this process, or remove peer.insecure: true.
```

Settings that contradict each other stop the process at start. See
[Configuration conflicts](#configuration-conflicts).

`dagu worker --check` dials each coordinator, prints reachability, the
coordinator's identity, and the credential the worker would use, then exits.

## Design

### Overview

```text
                      coordinator
          ┌─────────────────────────────────────────┐
          │ peer CA: generated once, shared through │
          │   the data dir or the chart Secret      │
          │ server certificate: in memory           │
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
  `peer.key_file` generates an ECDSA P-256 CA with no expiry date. It stores
  the CA next to the peer token and creates it with the same exclusive
  write, so coordinators sharing a data directory share one CA.
- `peer.ca_cert_file` and `peer.ca_key_file` supply the CA instead: from
  the chart, from a company CA, or for coordinators that share no data
  directory. On a client, `peer.ca_cert_file` alone is the CA that signs
  coordinator certificates.
- `dagu peer init --out <dir>` writes `ca.crt`, `ca.key`, and `token` for
  setups that must exist before the coordinator starts. It needs no running
  Dagu and refuses to overwrite existing files.

### Coordinator certificate

- Each coordinator issues its own server certificate from the CA at each
  start and keeps it in memory. It has no expiry date, so nothing renews
  it; a restart picks up a changed host name.
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
| `peer.insecure: false` with `peer.cert_file`, `peer.key_file`, `peer.client_ca_file`, or `peer.skip_tls_verify` | TLS with those files, as today |
| A worker invite or certificate, `peer.ca_cert_file`, or the CA certificate a coordinator publishes in the client's data directory while the registry there lists a coordinator that reports built-in TLS | Built-in TLS |
| `peer.insecure: false` without those files or a built-in TLS source | Fails at start with `TLS enabled but no certificates provided`, as today |
| None of these | h2c, as today |

- `peer.insecure: true` remains for networks that already encrypt traffic,
  such as a service mesh.
- A TLS file without `peer.insecure: false` is still ignored, as today. A
  coordinator without certificates serves such a client over h2c now, and
  selecting TLS from the file would break that setup. The process logs at
  start which file it ignores and why, and `dagu worker --check` says the
  same. Whether v3.0 lets a TLS file select TLS is open question 1.
- Each coordinator adds `tls: builtin` to the entry it registers, and
  writes the certificate of the CA it uses, generated or from
  `peer.ca_cert_file`, to its data directory, without touching a generated
  CA. A client that shares that data directory reads that certificate,
  whether it finds coordinators in the registry or in
  `worker.coordinators`. A client's own `peer.ca_cert_file` takes
  precedence. Older processes ignore the field.
- An address in `worker.coordinators` that the registry does not list may
  belong to another Dagu, such as a team coordinator dialed from a laptop
  that runs `start-all`. When its certificate does not chain to the
  published certificate, the client has no TLS source for that address and
  uses h2c, as today, with the reason `no TLS source`. Addresses the
  registry lists never fall back on a certificate that does not verify.
- A coordinator that leaves built-in TLS, with own certificates, removes
  the published certificate. A certificate that a downgraded coordinator
  leaves behind selects nothing, since its registry entry no longer reports
  built-in TLS; a client on the peer token that dials before the entry
  changes falls back on the failed handshake, as the next bullet states.
- In v2.x, a client on the peer token or on no credential falls back to h2c
  when the TLS handshake fails at the transport level: the connection is
  reset, or the other side answers with something other than TLS. This is
  what a proxy that expects plaintext gRPC does, such as a service mesh
  sidecar on a port named `grpc`. The client keeps h2c for that address for
  10 minutes, then tries TLS again on its next connection, and sends the
  reason `tls failed on the network path`.
- A certificate that does not verify never falls back, except on an
  address the registry does not list, as above. A worker on an
  invite never falls back either: it has no other credential, so it needs
  TLS end to end. `coordinator.require_tls` refuses the fallback's calls.
  Whether v3.0 removes the fallback is open question 1.

### Configuration conflicts

A process refuses to start when its settings contradict each other, and
names the setting to change:

| Settings | Result |
| --- | --- |
| `worker.invite` with `peer.insecure: true` | Fails: `worker.invite needs TLS: remove peer.insecure: true` |
| `worker.invite` with `peer.cert_file` or `peer.key_file` | Fails: invites need the built-in CA; remove the certificate files or the invite |
| `peer.ca_cert_file` or `peer.ca_key_file` with `peer.cert_file`, `peer.key_file`, or `peer.client_ca_file` | Fails: use the built-in CA or your own certificates, not both |
| A single-use invite with a data directory the worker cannot write | Fails: use a reusable invite or a writable data directory |
| `worker.invite` with `peer.token` | Starts. The invite enrolls, and the worker stops sending the token once it holds a certificate. Logged once |

### Chosen transport

- At start, every peer process logs its transport and the setting that
  chose it, such as `Peer transport: built-in TLS (from worker.invite)` or
  `Peer transport: h2c (from peer.insecure: true)`.
- `dagu worker --check` prints the same line, followed by what each
  coordinator answered.
- A process on h2c sends the reason in call metadata: `peer.insecure`,
  `no TLS source`, or `tls failed on the network path`. An older process
  sends none, which reads as an older version.
- The workers list shows the reason in the `Connection` column, such as
  `Plaintext · peer.insecure: true in its config` or
  `Plaintext · older version`, with the change to make. A worker set to
  `peer.insecure: true` on purpose, such as behind a service mesh, gets the
  same neutral line and no alert.
- Other processes on h2c, such as a scheduler, a UI/API server, or a CLI,
  are not in the workers list. System status lists those that called the
  coordinator over h2c in the last 10 minutes, with host and reason. The
  coordinator keeps at most 64 such entries in memory.
- The configuration reference (`docs/server-admin/reference.md`) stops
  listing `insecure: true`, since configs copied from it would keep h2c
  after the upgrade.

### Invites

- An invite is `dagu1.<pin>.<secret>`: the pin is the base64url SHA-256 of
  the CA certificate (DER), and the secret is 32 random bytes in base64url.
- A new `worker_invites` collection keeps the SHA-256 of the secret, the
  kind, expiry, creator, use count, and last use. A single-use invite is
  consumed with a compare-and-swap, so two coordinators cannot both accept
  it.
- An invite ends when it is used up or expires. Each coordinator's daily
  prune deletes its record 7 days later, so ended invites do not pile up.
  A reusable invite without an expiry stays until it is deleted.
- `worker.invite` (`--worker.invite`, `DAGU_WORKER_INVITE`) carries the
  invite. Config output masks it like `peer.token`.
- New coordinator RPCs create, list, and delete invites and revoke
  certificates. They take the peer token and refuse worker certificates.
  The REST API, the CLI, and MCP call them.
- REST endpoints, admin only, with each call in the audit log. An admin API
  key can call them.

  | Endpoint | Effect |
  | --- | --- |
  | `POST /api/v1/workers/invites` | Body `{"reusable": false, "expiresIn": "24h"}`. Returns `{"id", "invite", "expiresAt"}`; only this response contains the invite |
  | `GET /api/v1/workers/invites` | Lists invites without their secrets |
  | `DELETE /api/v1/workers/invites/{id}` | Deletes an invite |
  | `DELETE /api/v1/workers/{workerId}/certificate` | Removes the worker. `?runs=finish`, the default, waits for its runs; `?runs=stop` stops them. Returns `removing` or `removed` |

### Join readiness

- A new coordinator RPC reports what **Add worker** and `dagu worker invite`
  need: the listen host and port, whether it listens on loopback addresses
  only, where `coordinator.host` came from (default, a config file and its
  path, an environment variable, or a flag), whether the coordinator runs in
  a container (`/.dockerenv`, `/run/.containerenv`) or in Kubernetes
  (`KUBERNETES_SERVICE_HOST`), and its non-loopback interface addresses
  outside a container. The coordinator reports them from its own host,
  since the API server may run on another.
- `GET /api/v1/workers/join-readiness`, admin only, returns the report of
  each coordinator, and the dialog shows any that is not ready. The dialog
  calls it when it opens and again after the server restarts; nothing polls
  it.
- `worker_invites` keeps the address each invite was made for, which step 2
  starts with next time.

### Enrollment

```text
worker                                         coordinator
  │ TLS dial; check the chain against the pin     │
  │ Enroll(invite, CSR, worker ID, host) ────────▶│ check the invite; consume it if single use
  │◀──────────────────────────────── certificate ─│ record it in worker_certificates
  │ later calls: TLS + certificate                │
```

- The worker generates its own key. The private key never leaves the host.
- The certificate names a random certificate ID and the invite ID, and
  allows client authentication only.
- The worker keeps the key and certificate in its data directory, readable
  by its user only, and reuses them across restarts. Run processes the
  worker starts use the same files. It also records which invite it
  enrolled with. A worker with a certificate ignores its invite, so
  restarts do not use up a single-use invite, unless the invite names a CA
  other than the one that signed its certificate. Then it enrolls with the
  invite, so a worker can join a coordinator set up again with a new CA.
- The worker holds a lock on its certificate while it runs. A second worker
  that finds the lock held exits with `Another worker uses this data
  directory`, so two containers on one volume never share a certificate. A
  lock whose holder is gone is taken over.
- The worker reports in its heartbeat whether it runs in a container or in
  Kubernetes, for the **Remove** confirmation.
- The certificate has no expiry date, so a worker that stays off for months
  reconnects when it starts. An expiry date would need renewal, and a
  copied key could renew just as the worker does; the date would only break
  workers that stay off past it. **Remove** ends a certificate at once.
- An unrevoked certificate counts as the peer token, including under
  `coordinator.require_token`.
- `worker_certificates` records each certificate at enrollment with its
  last heartbeat, which is written at most once a day. Once a day, each
  coordinator deletes the records of unrevoked certificates without a
  heartbeat for 90 days. Acceptance depends only on the chain to the peer
  CA and the revoked set, so a certificate whose record was deleted still
  connects, and its next heartbeat writes the record again. Containers on a
  reusable invite without a volume enroll at each start, and this keeps
  their records bounded.

### Revocation

- **Remove** marks the certificate revoked in `worker_certificates`. Every
  call checks the client certificate against the revoked set, so open
  connections are refused too.
- The coordinator that handles the removal applies it at once. Other
  coordinators reread the revoked set every 30 seconds.
- Removing a worker with running runs first marks its certificate
  `removing`, a stored state that survives a coordinator restart. `Poll`
  gives the worker no new tasks, and calls for the runs it already holds
  keep working. The certificate is revoked when the worker holds no task,
  at least 30 seconds after it became `removing`, or when the worker stops
  sending heartbeats. A task counts as held from
  the moment a coordinator hands it out until the worker reports its end
  or its lease expires, so a task another coordinator hands out before its
  next reread is waited for too. A connected worker without runs goes
  through `removing` the same way.
- Other coordinators apply a revocation at their next reread, within 30
  seconds. A task one of them hands out before then does not start: a
  worker accepts a task only while the stored certificate is `active`, so
  the task returns to the queue when its claim expires.
- **Stop its runs** sends the existing cancel request for each run. Runs
  that reach no final status within 2 minutes are left to the stale-lease
  check, and the certificate is revoked then.
- A worker refused as revoked stops the runs it still holds before it
  exits, since it can no longer report them.
- A worker whose certificate is refused as revoked keeps it and exits with
  the message above. At later starts it holds a certificate, so it does
  not enroll with the invite it joined with, and it exits again with the
  same message.
- Given a different invite, a removed worker enrolls again with a new key.
  This is how **Add worker** brings a removed worker back.
- Enrollment refuses a request whose key belongs to a revoked certificate.
- Revoked entries stay, one small record per removed worker.

### Workers list

- Each worker gains `connection` (`certificate`, `tls`, or `plaintext`);
  for certificates, the certificate ID, the invite ID, and `removing` when
  set; for plaintext, the reason. The coordinator takes them from the
  connection that carries the worker's heartbeat.
- `GET /workers` returns them. The **Add worker** dialog uses System
  status's existing `/workers` refresh and waits for a worker with its invite ID.
- The Dagu Cloud report adds `connection` to `health.workers[]`, and the
  reason for a plaintext worker, on paid plans as today.

### Bring your own certificates

The existing keys keep working, with three fixes:

- A TLS file that `peer.insecure` overrides is reported at start and by
  `dagu worker --check` instead of being ignored silently. It still selects
  no TLS in v2.x; see [Client transport](#client-transport).
- At start, the coordinator fails when the key does not match the
  certificate, and warns when the certificate does not name
  `coordinator.advertise` or expires within 14 days. A process that serves
  and dials with the same certificate under mTLS, such as `start-all`, warns
  at start when the certificate lacks client authentication: its local runs
  work, and dispatching to workers would fail. It does not fail, since such
  a setup with only local runs works today.
- `dagu worker --check` reports handshake failures in these terms: the names
  the certificate covers, the address dialed, and the CA it chains to.

### Chart

- The chart generates no CA. The coordinator generates it in the data
  volume, which the scheduler and UI share.
- `peer.existingCASecret` names a Secret with `ca.crt` and `ca.key`.
  Coordinator pods mount both as `peer.ca_cert_file` and
  `peer.ca_key_file`; scheduler, UI/API, and worker pods mount `ca.crt` as
  `peer.ca_cert_file`.
- The rendered config drops `peer.insecure: true`.
- `coordinator.service.type` (default `ClusterIP`) and
  `coordinator.service.annotations` set the coordinator Service, so a
  `LoadBalancer` or `NodePort` can expose port 50055 to workers outside the
  cluster. Clients check the fixed name `coordinator.dagu.invalid`, so the
  load balancer passes TCP through and needs no certificate.

### Dagu Cloud

- **Add worker** calls a new server MCP action, as the signed-in person. It
  returns the join readiness first, so the console shows the same steps,
  and creates the invite once the coordinator is ready.

## Compatibility

- **Config:** `peer.insecure` defaults to unset instead of `true`. A process
  behaves as today unless it has a built-in TLS source from the table above.
  New keys: `peer.ca_cert_file`, `peer.ca_key_file`, `worker.invite`, and
  `coordinator.require_tls`. Settings that conflict fail at start; none of
  them exist before this change, so no working config starts failing.
- **CLI and API:** new commands `dagu peer init`, `dagu worker invite`, and
  `dagu worker --check`; new endpoints under `/api/v1/workers/invites`,
  `/api/v1/workers/{workerId}/certificate`, and
  `/api/v1/workers/join-readiness`.
- **Chart:** new values `peer.existingCASecret`, `coordinator.service.type`,
  and `coordinator.service.annotations`. The default Service stays
  `ClusterIP`. After the chart upgrade, the coordinator, scheduler, and UI
  use TLS, and workers stay on h2c until `peer.existingCASecret` is set.
- **Service mesh:** a path that expects plaintext gRPC keeps working over
  h2c through the fallback, and shows why.
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
  until they get `peer.token` and `peer.insecure: true` and lose
  `worker.invite`. Processes that use the peer token keep working.
- **Bring your own certificates:** unchanged. A TLS file without
  `peer.insecure: false` is still ignored, now with a warning.

## Performance

- **Handshakes:** one TLS handshake per gRPC connection. Connections are
  long-lived, so the cost does not grow with runs.
- **Fallback:** a client that fell back tries TLS once per coordinator
  address every 10 minutes, on its next new connection.
- **Enrollment:** one RPC and one signature, well under a millisecond, per
  worker without a certificate. That is once per host, or once per start
  for containers on a reusable invite. Nothing renews.
- **Revocation:** each coordinator rereads the revoked set every 30 seconds,
  also when idle. It is one small collection read, independent of DAGs and
  runs, with one record per removed worker. Invites are read on enrollment
  only.
- **Invite records:** one per invite that has not ended or ended in the
  last 7 days, plus reusable invites without an expiry. Enrollment reads
  them all, so its cost follows that count, not DAGs, runs, or workers.
- **Certificate records:** one per certificate seen in the last 90 days,
  plus one per removed worker. A record's last heartbeat is written at most
  once a day, and each coordinator prunes the collection once a day.
- **Workers list:** up to three fields per worker, about 120 bytes, on the
  existing 2-second refresh of an open System status page. The Dagu Cloud
  report grows by about 25 bytes per worker, or up to about 80 for a
  plaintext worker with its reason, for at most 256 workers, within the
  64 KiB worker list cap.
- **Join readiness:** one RPC per coordinator when the dialog opens or the
  server restarts while it is open. No file reads and no polling.
- **Removal:** `Poll` checks an in-memory set of removing certificates,
  refreshed with the revoked set. Accepting a task reads the worker's
  certificate record before and after it creates the lease: two small
  reads per task, not per call.
- **h2c reasons:** one short metadata value per call over h2c, and at most
  64 in-memory entries for System status.
- **Upgrade:** generating the CA takes milliseconds, once.

## Rollout

1. Bring-your-own fixes: the ignored-file warning, start checks, the
   chosen-transport log, `dagu worker --check`, and the configuration
   reference without `insecure: true`.
2. Built-in CA, `dagu peer init`, coordinator certificate, one-port TLS and
   h2c, client transport rule, registry flag.
3. Invites, enrollment, revocation, configuration conflicts, join
   readiness, workers list, API, CLI, MCP.
4. Chart, including the coordinator Service type.
5. Dagu Cloud console.

Specs under `specs/` state the transport selection, enrollment, and
revocation contracts. Conformance tests cover h2c and TLS on one port, the
single-use invite, and revocation of an open connection.

## Alternatives

| Option | Why not |
| --- | --- |
| Listen on `0.0.0.0` by default | Every fresh install would open the gRPC port to the network, and macOS would ask to allow incoming connections on the first `start-all` |
| A button that changes the listen address | A web click would open a port, and a setting the UI stores would override the config file. A container still needs its port published |
| Fill the address from the hostname, container ID, or browser host | It shows a command that cannot work as if it would. In a container the hostname is the container ID, and the UI's Ingress does not carry gRPC |
| Dial the entered address from the coordinator | Reaching itself from its own host says nothing about the worker's network |
| Generate the CA in the chart with `genCA` and `lookup` | Under `helm template`, as Argo CD and Flux render, `lookup` finds nothing, so the CA would change on every sync and break every handshake and invite |
| Fail instead of falling back to h2c when TLS cannot cross the network path | A service mesh that expects plaintext gRPC would break on upgrade. The fallback keeps h2c, which v2.x accepts anyway, and shows why |

## Open questions

1. Should `coordinator.require_tls` default to `true` in v3.0, with
   `coordinator.require_token`? And from v3.0, should a TLS file select TLS
   without `peer.insecure: false`, and should the h2c fallback go?
2. Should a worker certificate be limited to the worker ID it enrolled with?
   The default ID (`hostname@PID`) changes at each start, so this needs a
   stable worker ID first.
3. Once sub-DAG dispatch from workers moves to a narrower RPC, should worker
   certificates lose `Dispatch`?
4. Replacing the CA: issue from a new CA while trusting both, then drop the
   old one. Nothing expires, so this is only for replacing a CA on purpose.
   Is it needed before v3.0?
5. Should **Add worker** in Dagu Cloud be paid only, like worker monitoring?
