# RFC 014: Site Relay

- **Status:** Proposed
- **Date:** 2026-11-12
- **Affected areas:** a new `siterelay` package, `cloudrelay`,
  `service/coordinator`, `service/worker`, `inventory` (RFC 012), `runtime`
  (RFC 013 node dispatch), `service/frontend` (API, UI), `audit`,
  `cmn/config`, `cmd`, `ui`, and `docs`. The Dagu Cloud relay changes too,
  outside this repository

## Summary

Let a worker in a network the Dagu server cannot reach, and that cannot reach
the server, take work from that server through Dagu Cloud. Such a network is a
**site**. Both ends open outbound connections on port 443, and Dagu Cloud
joins them:

```text
Home network                 Dagu Cloud               Site "osaka"
┌──────────────────┐        ┌───────────┐        ┌─────────────────────┐
│ scheduler        │        │           │        │ site worker         │
│ coordinator ─────┼─ wss ─▶│   relay   │◀─ wss ─┼── dagu worker       │
│ stores, secrets  │        │ (opaque   │        │    + listener ◀─────┼ other
└──────────────────┘        │  bytes)   │        │    │                │ workers
                            └───────────┘        │    └─ SSH ─▶ nodes  │
                                                 └─────────────────────┘
```

A **site worker** is `dagu worker` started with a site token in place of
coordinator addresses. It speaks the coordinator protocol, unchanged, over a
TLS session that ends at the coordinator. Dagu Cloud carries the bytes and
cannot read them. The scheduler, run state, logs, and secrets stay on the
server.

A site worker can also listen on its own network, so other workers at the
site reach the coordinator through it with no token and no route out.

With [RFC 012](012-node-inventory.md), site workers are nodes, and an SSH
node can name the workers that reach it. A step of
[RFC 013](013-node-targeting.md) then runs on hosts in several sites at once.

## Problem statement

A worker reaches its coordinator at the addresses in `worker.coordinators`.
That needs a route from every worker to the coordinator's gRPC port.

| Situation | What happens today |
| --- | --- |
| A branch office, a plant, or a customer network behind NAT | The coordinator needs a public address and an open port, or a VPN joins the networks |
| A DMZ that may open connections only to the internet | No worker there can connect |
| A fleet of SSH hosts in such a network (RFC 012) | Every run that targets them must run on a worker there, and no worker there can connect |
| One step over hosts in two such networks (RFC 013) | Not possible. SSH opens from the one host that runs the run |

Rundeck's paid tier sells this as the Runner: one agent per network that
dials out to the server and runs node steps there. Teams that would move to
Dagu from it have no equivalent.

[RFC 002](002-dagu-cloud-connector.md) already lets a server open an outbound
connection to Dagu Cloud's relay for remote access, and
[RFC 004](004-triggers.md) uses the relay for webhook deliveries. Neither
carries work to a worker.

## Goals

1. A worker joins a server with one token, and with outbound 443 only at both
   ends.
2. Dagu Cloud can read nothing it carries: no DAG, parameter, log, artifact,
   or secret.
3. The coordinator protocol does not change. A site worker runs the code
   `dagu worker` runs today.
4. A site cannot take work meant for another site or for the home network.
5. Other workers at a site need no token and no route to the internet.
6. A node step reaches SSH hosts in several sites in one run.
7. Nothing changes for an install that defines no site.

## Non-goals

- Running a scheduler or coordinator in Dagu Cloud. The server keeps every
  store.
- A coordinator at the site. Coordinators share the server's file-backed
  stores, which a site does not have.
- Defining or editing inventories in Dagu Cloud. A `url` source (RFC 012)
  can read one later. See [Open questions](#open-questions).
- A fleet level inside the inventory, with its own name scope and grants.
  See [Open questions](#open-questions).
- Joining two Dagu servers. Remote nodes (`internal/remotenode`) do that.
- Replacing a direct connection. A worker that can reach the coordinator
  keeps using `worker.coordinators`.

## Design

### Terms

| Term | Meaning |
| --- | --- |
| Home | The install that holds the scheduler, coordinators, and stores |
| Site | A named network whose workers reach home only through the relay |
| Site worker | A `dagu worker` process that holds a site token |
| Listener | The coordinator endpoint a site worker serves to other workers at its site |
| Tunnel | One TLS session between a site worker and one coordinator, carried by the relay |

Sites are server-wide, as workers are in [RFC 003](003-remove-workspaces.md).

### Turning it on

Sites are set on the server, never from Dagu Cloud, as remote access is in
RFC 002.

- **UI.** The Workers page gets a **Sites** section for the server's admins:
  a switch, the list of sites, and **Add site**. The switch starts off.
- **Config.** For servers managed as code, `cloud.site_workers` fixes the
  switch, and the UI shows it read-only.

```yaml
cloud:
  site_workers: false            # omit to choose in the UI
```

While the switch is on and the license is online, each coordinator process
asks Dagu Cloud for a relay ticket with `purpose: "workers"` and connects, as
each server process does for remote access. Dagu Cloud refuses the ticket
while the server's workspace has neither a plan nor a trial.

### Sites and tokens

```sh
dagu site add osaka              # prints the token once
dagu site list
dagu site token osaka            # adds a second token, for rotation
dagu site revoke osaka [--token <id>]
```

- Site names match `^[a-z0-9][a-z0-9-]*$`.
- A site has one or more tokens. Each is shown once, when it is made.
- The server makes the token: a 32-byte seed, the site ID, the server ID, the
  relay host, and the SHA-256 fingerprint of the server's site certificate.
- Two keys are derived from the seed with HKDF-SHA-256 under different
  labels:

| Key | Shown to | Proves |
| --- | --- | --- |
| Relay key | The relay | That the connection may be joined to this server |
| Site secret | The coordinator only, inside the tunnel | That the worker belongs to this site |

- The server stores the SHA-256 of each key, never the seed. The relay stores
  the SHA-256 of the relay key only, as it stores webhook tokens (RFC 004).
- The coordinator sends the relay the whole set of site IDs and relay key
  hashes in a `sites` frame, and again when the set changes. It sends the set
  only from a complete read of the site store, as RFC 004 requires for
  `hooks`.
- The site certificate is self-signed, made once per server, and kept in the
  data directory that coordinators share. Replacing it needs new tokens for
  every site.

Adding, revoking, and rotating need the admin role and are written to the
audit log.

### Site worker

```yaml
worker:
  id: osaka-1
  site_token: ${DAGU_SITE_TOKEN}   # in place of worker.coordinators
  labels:
    zone: dmz
  listen: 0.0.0.0:50055            # optional. See "Listener"
```

Exactly one of `worker.coordinators` and `worker.site_token` is set. `dagu
worker --site-token` sets it on the command line. A site worker needs no
license and no other Dagu process.

The worker connects to `wss://<relay host>/site` with the relay key, and
opens one tunnel to each coordinator it uses:

1. The relay joins the stream to a coordinator connection of the token's
   server. The worker names the coordinator for calls bound to an owner
   coordinator, and names none otherwise.
2. The worker starts TLS 1.3 over the stream and accepts only the certificate
   whose fingerprint the token holds. The relay holds no key for it, so the
   relay can neither read the session nor answer in the server's place.
3. The worker runs gRPC over the session and sends the site secret as call
   credentials on every RPC. The coordinator compares its hash in constant
   time and refuses every call without a match.

Everything a worker sends today rides the tunnel: polls, heartbeats, status,
logs, artifacts, workspace bundles, DAG fetches, and secret resolution.

### What the coordinator does with a site

The coordinator knows the site of every call from the tunnel it arrived on,
not from anything the worker says.

- **Label.** Every worker of a site has the label `site` with the site's
  name. `site` is a reserved label: the coordinator drops it from the labels
  a worker claims, on a tunnel or on a direct connection.
- **Selection.** A site's worker receives a task only when the task's
  `worker_selector` names that `site`, or its `target_worker_id` is that
  worker. A task that names no site never leaves the home network, whatever
  other labels a site worker claims.
- **Worker IDs.** An ID in use by a worker of another site, or by a direct
  worker, is refused on the tunnel. A site cannot take over another worker's
  leases.
- **Calls.** A tunnel reaches only the RPCs `dagu worker` calls. `Dispatch`
  and the other RPCs that only servers call answer `PermissionDenied`.
- **Leases.** The lease checks on `GetDAG`, `ResolveSecretReference`, and
  status and log writes apply unchanged. A site worker reads a DAG or a
  secret only for an attempt it holds.

```yaml
steps:
  - id: restart
    run: systemctl restart app
    worker_selector:
      site: osaka
```

### Listener

With `worker.listen` set, the site worker serves the coordinator protocol on
that address. Another worker at the site sets it as its coordinator:

```yaml
worker:
  id: osaka-2
  coordinators: [osaka-1.internal:50055]
```

- The listener uses the `peer` TLS settings a coordinator uses.
- It forwards each RPC, unary or streamed, over the site worker's tunnels,
  and adds nothing to it. The coordinator treats a forwarded worker as a
  worker of the site.
- A call bound to an owner coordinator goes to that coordinator's tunnel.
  The listener gives the workers behind it its own address as the owner's.
- The listener holds no state. A second site worker with a listener gives
  the site a second way out.
- The listener does not need Dagu Cloud. A worker with `coordinators` and
  `listen` both set forwards to those coordinators directly, as one way out
  of a network that can reach home.

The site worker is the trust boundary of its site. Whoever can reach the
listener can run as a worker of that site, so the listener's `peer` settings
matter as a coordinator's do.

### Nodes at a site

**Workers.** With `workers: true` in `nodes.yaml` (RFC 012), a site's workers
are nodes like any other worker, and `site` is one of their attributes:

```yaml
nodes: "site: osaka tags: web"
```

RFC 013 sends each such item to its worker, so one step already covers
workers in several sites.

**SSH hosts.** RFC 013 opens SSH from the host that runs the run. A host at a
site is reachable only from that site's workers, so this RFC adds one field
to RFC 012's SSH settings:

| Field | Meaning |
| --- | --- |
| `via` | A worker selector. The connection opens from a worker that matches it |

```yaml
sources:
  - name: osaka
    file: rundeck/osaka.yaml
    format: rundeck
    defaults:
      ssh:
        via: { site: osaka }
```

- A run already on a matching worker opens the connection itself.
- Otherwise the step groups its matched nodes by `via`. Each group runs as
  one child run sent to a matching worker, with one step that targets the
  group's nodes by exact name, as RFC 012's `node-test` run does. Items
  without `via` run where the run is.
- The child reads its nodes with `ResolveNodes`, so credentials and trusted
  host keys reach the site as RFC 012 defines. A `file` key is read on the
  worker at the site.
- Per-node records, logs, and retry are RFC 013's. The parent step shows one
  item per node, not per group.
- A bastion's own `via` is ignored. The connection to the bastion opens from
  the worker the target node's `via` selects.
- Test connection and Trust use a node's `via` as the default origin.

`via` is not tied to sites. `via: { zone: dmz }` routes through a direct
worker with that label.

### Relay frames

JSON control frames and binary data frames over one WebSocket, as in RFC 002.

| Direction | Frame |
| --- | --- |
| Coordinator → relay | `hello{protocol, version, name}`, with the coordinator ID in the `Dagu-Instance` header |
| Relay → coordinator | `limits{sites, tunnels}` |
| Coordinator → relay | `sites{[{id, key_sha256}]}`, replacing the whole set |
| Site worker → relay | `hello{protocol, version, server_id, site_id}`, with the relay key as the bearer of the WebSocket request |
| Site worker → relay | `open{id, coordinator}`; `coordinator` is empty for any |
| Relay → coordinator | `open{id, site_id}` |
| Coordinator → relay → site worker | `opened{id, coordinator}`, or `refused{id, reason}` |
| Both | binary chunks for `id`: a version byte (`1`), the 16 bytes of the tunnel's UUID, and up to 64 KiB |
| Both | `credit{id, bytes}`, so a slow end holds back the other |
| Both | `close{id, reason}` |
| Both | `"ping"` every 15 seconds; the relay answers `"pong"` |

The relay closes a site's connections when a `sites` frame leaves the site
or its key out. The coordinator also refuses the site secret from then on, so
revoking does not depend on the relay.

## Security

- Dagu Cloud carries ciphertext. It learns the server ID, the site ID, when
  each end is connected, and how many bytes pass. RFC 002's rule that Dagu
  Cloud stores no log, output, parameter, or secret still holds.
- The token pins the server's certificate, so a relay that is wrong or
  compromised cannot read a tunnel or pose as the server. It can drop or
  delay tunnels.
- A token is a credential for its site. It is shown once, stored as hashes,
  and revoked on the server. A leaked token lets its holder run as a worker
  of that one site.
- A site worker runs whatever the server sends it, as the OS user it runs
  as. Anyone who may write or run a DAG that selects the site runs commands
  there. Turning sites on is therefore an admin's choice on the server, and a
  site is joined only by someone who holds its token.
- A site receives only tasks that name it, and resolves secrets only for
  attempts it holds. A compromised site worker reads the DAGs and secrets of
  runs sent to its site, and no others.
- The `site` label comes from the tunnel. No worker can claim it.
- A `cloud:` identity (RFC 002) cannot add, revoke, or rotate a site, or
  change `cloud.site_workers`.

## Failure behavior

| Event | Behavior |
| --- | --- |
| Dagu Cloud or the relay is unreachable | Runs that use no site are unaffected. Each end reconnects with backoff, from 1 second to 1 minute. Site workers miss heartbeats and are treated as workers that lost their coordinator are today |
| A tunnel drops during a task | The worker retries its calls on a new tunnel, within the `peer` retry settings. Past them, the attempt ends as a lost worker's does today |
| No worker of the named site is connected | The dispatch waits `coordinator.worker_wait_timeout`, then fails as `NoMatchingWorker`, naming the site |
| The site worker with the listener stops | Workers behind it lose their coordinator. A second listener, listed in their `coordinators`, takes over |
| A token is revoked | The relay closes its connections, and the coordinator refuses its calls. Attempts it held end as a lost worker's do |
| The certificate fingerprint does not match | The worker stops before any RPC and logs both fingerprints. It does not retry until restarted |
| Dagu Cloud refuses the ticket | The Sites section says why. Site workers stay disconnected |
| The license goes offline or is released | Tunnels close. Direct workers and local runs are unaffected |
| A node's `via` matches no worker | Its group's items fail with `NoMatchingWorker`. Other groups follow `keep_going` |

RFC 002's goal that no workflow depends on Dagu Cloud is narrowed: a run
depends on Dagu Cloud only for the tasks it sends to a site.

## Compatibility and rollout

- Additive. An install with no site behaves as before.
- `site` becomes a reserved worker label. `dagu validate` gives a notice for
  a `worker_selector` that uses it while no such site exists. An existing
  worker that claims a `site` label loses it, which the release notes say.
- The coordinator protocol gains no RPC and no field.
- RFC 002 is amended: the frames above, the `workers` ticket purpose, the
  narrowed goal, and `cloud.site_workers` in the `cloud` section.
- RFC 012 is amended: `via` in SSH settings.
- RFC 013 is amended: grouping node items by `via`.

Rollout:

1. **Sites on the server**: the site store, tokens, `dagu site`,
   `cloud.site_workers`, the coordinator's relay connection, and the `sites`
   frame. The Dagu Cloud relay must accept `purpose: "workers"` and
   `/site` connections first.
2. **Site worker**: `worker.site_token`, the tunnel with the pinned
   certificate and site secret, the `site` label, the selection rule, and the
   RPC allowlist.
3. **Listener**: `worker.listen`, with a tunnel or with direct coordinators.
4. **UI**: the Sites section, each site's workers and last contact, and the
   site in the Workers list.
5. **`via`** for SSH nodes, once RFC 013 runs node items on workers.

## Verification and acceptance criteria

1. With inbound traffic blocked at both ends and outbound limited to 443, a
   site worker started with only a token runs a task whose `worker_selector`
   names its site, and its status, logs, and artifacts reach the server.
2. A capture at the relay shows no DAG text, parameter, log line, or secret
   value from that run.
3. A relay that presents another certificate is refused before any RPC.
4. A task with `worker_selector: { gpu: "true" }` is never given to a site
   worker that claims `gpu: "true"`. A task that names another site is never
   given to it either.
5. A direct worker that claims `site: osaka` is not selected for that site.
6. A site worker cannot register an ID that a direct worker or another site
   holds, and cannot call `Dispatch`.
7. A site worker cannot resolve a secret for an attempt it does not hold.
8. A worker with no token and no route out runs a task through another
   worker's listener, including a task bound to an owner coordinator, with
   two coordinators running.
9. Revoking a token ends its worker's tunnels within 10 seconds, with the
   relay's help and without it.
10. With Dagu Cloud unreachable, local runs and direct workers run on time,
    and a task for a site fails as `NoMatchingWorker` after the wait.
11. After the relay drops every connection, a running site task finishes and
    reports once tunnels return, when they return within the retry settings.
12. With `workers: true`, a site worker is a node with attribute `site`, and
    `nodes: "site: osaka"` selects it.
13. A step over SSH nodes whose `via` names two sites, run from the server,
    reaches every node, shows one item per node, and retries only the failed
    nodes. A host key trusted in the UI holds at both sites.
14. A workspace without a plan or a trial gets no ticket, and the Sites
    section says so.

## Alternatives considered

- **A VPN or a mesh network between sites.** Works today and needs no Dagu
  change. It needs the network team of every site, and joins networks where
  only one port of one process is wanted.
- **A public coordinator address.** Works today with `worker.coordinators`,
  and stays supported. Many servers may not open an inbound port.
- **A coordinator in Dagu Cloud.** The simplest setup: run only workers. Dagu
  Cloud would then hold DAGs, logs, and secrets, which RFC 002 rules out, and
  every run would depend on it.
- **A coordinator at each site.** Coordinators read and write the server's
  stores, so a site would need them too.
- **The relay forwarding RPCs, not bytes.** Simpler to meter and to debug,
  but Dagu Cloud would read secrets and logs.
- **A site attribute on nodes in place of `via`.** `site` is already a free
  attribute in imported Rundeck and Ansible data, and would start routing
  runs on upgrade.
- **One child run per node for `via`.** Reuses worker-node dispatch as it
  is, but 500 hosts would be 500 dispatched runs.

## Consequences

- A team reaches hosts in networks it cannot join, with one process per
  network and no inbound port.
- Dagu Cloud carries run traffic for the first time. Its relay becomes part
  of the availability of runs that use sites.
- Logs, artifacts, and workspace bundles of site runs pass through the
  relay, which costs bandwidth.
- `worker_selector` gains a label with rules of its own.

## Open questions

1. Should the relay limit or meter bytes per site, since logs, artifacts, and
   workspace bundles pass through it? `limits` can carry such a cap.
2. Should a relay that a customer hosts be supported, for installs that may
   not use Dagu Cloud?
3. Should the inventory gain a fleet level, with node names scoped to a fleet
   and grants per fleet? `via` in a source's `defaults` covers routing only.
4. Should Dagu Cloud hold an inventory that servers read through a `url`
   source?
5. How does `max_concurrent` apply across `via` groups: one budget shared by
   the groups, or one per group?
6. Should a token expire, or be bound to one worker ID on first use?
