# RFC 021: Audit Logs in Dagu Console

- **Status:** Draft
- **Date:** 2026-11-22
- **Affected areas:** `audit`, `persis/file/audit`, `cmn/masking`,
  `cloudreport`, `cmd` (`server`, `start-all`, `secret`), `service/frontend`
  (sign-in, MCP audit, settings), `service/mcp`, `cmn/config`, and `docs`

## Summary

Let a Dagu server send its audit entries to Dagu Console, so the record of
who did what outlives the server, its disk, and its 7-day local retention.
One setting, `cloud.audit`, turns it on. By default the server sends what
changed and what failed, not successful reads. It keeps writing its local
audit files and serving its own Audit page.

Every audit entry the server writes is also masked: known secret values and
common token formats are replaced before the entry reaches disk, whether or
not it is ever sent.

## Problem statement

- **The only copy is on the server.** Audit entries live in daily JSON Lines
  files under `<admin_logs_dir>/audit` (`internal/persis/file/audit/store.go`).
  Deleting the server, or losing its disk, deletes them.
- **Local retention is short.** `audit.retention_days` defaults to 7
  (`internal/cmn/config/loader.go:2387`). Raising it costs disk on a small
  host.
- **The person who changed something can erase the record.** A server admin
  with shell access can edit or delete the local files.
- **Reports leave audit out.** RFC 002 sends health, runs, and errors.
- **Audit entries can hold secrets.** A terminal command typed with a token
  carries the token, and a failed sign-in records the name typed, which can
  be a password typed in the wrong box. Nothing masks them today.

## Goals

1. Send audit entries to Console at least once while the server's disk
   survives.
2. Record the losses the server can see as gaps, so Console shows them.
3. Keep known secrets out of every audit entry, local and sent.
4. Cost no file I/O while no entry is written, and none at all when off.

## Non-goals

- Making Console the primary store. The local files and the Audit page stay
  as they are, apart from masking.
- Sending to anything but Console, such as a SIEM.
- Tamper-proof records, such as signatures or hash chains.
- Masking secrets from external providers, such as Vault.
- How Console stores, limits, and shows entries. This RFC defines the wire
  contract Console serves.

## Guarantees

What this design promises, and what it does not. A case outside the promises
is a limitation listed here, not a defect.

Promised:

- **No silent loss while the disk survives.** Every entry the shipper reads
  at the chosen level reaches Console, or is recorded as a `rejected` gap,
  provided the audit files and the shipper's state survive until Console
  accepts it. Delivery is at least once.
- **Losses the server can see are reported while Console is reachable.**
  Local retention deleting unsent entries, a stop with entries unsent, and a
  line the server cannot parse or send each become a gap.
- **Known secrets stay masked.** The value of a `dagu-managed` secret that is
  current when an entry is sent, and tokens in common formats, never reach
  Console. The local file holds them masked too, except in an entry written
  within a minute of a change made by another process, or while the secret
  values fail to load. An entry of the second kind lacks `"masked": true`.

Not promised:

- **Entries on a disk that dies before Console accepts them.** A kill with no
  stop signal, an ephemeral disk, or a graceful stop while Console is
  unreachable loses them, and no gap arrives.
- **The `stopped` gap.** It is best effort.
- **Other secrets.** Secrets from external providers, and values the store
  did not hold when the entry was written or sent, can still go out.
- **Trust in the server.** A server admin can forge or drop entries before
  they leave.

## Design

```text
audit store Append            masks, sets "masked": true
      |
      v
<admin_logs_dir>/audit/2026-11-20.jsonl
      |  read after the saved offsets
      v
audit shipper (own lease)     filters by level, masks again, cuts to limits
      |
      |  POST {cloud_url}/api/v1/servers/audit
      v
Dagu Console  ---- 200 ---->  shipper saves its offsets
```

### Setting

`cloud.audit` (`DAGU_CLOUD_AUDIT`) is `off`, `changes`, or `all`, and
defaults to `off`. It takes effect only when local audit is on
(`audit.enabled`), the server is connected to Console (RFC 002), and the
license has the `audit` feature. The settings page shows the level, lists
the fields that leave the server, and says where Console stores them.

- `changes` sends every entry except successful reads. A successful read is
  an MCP call to a read-only tool, such as `dagu_read`, or an MCP resource
  read, that succeeded. Failed and denied reads, sign-ins, terminal commands,
  and every change are sent. Dagu marks which tools and actions are reads.
- `all` sends every entry.
- At both levels, the shipper sends one entry per MCP call: the result
  (`succeeded`, `failed`, or `denied`). It skips `received` and `started`,
  which the result entry repeats.
- An action Dagu has not marked as a read is sent, so a new tool or action
  is never left out by mistake.

On one server used heavily through MCP, `changes` sent about 15% of the local
entries, and `all` about 40%.

### Writers

Two kinds of process write audit entries: the web server (`dagu server`,
`dagu start-all`), for the UI, REST, MCP, terminal, and webhooks, and the
`dagu secret` CLI (`internal/cmd/secret.go`). Workers, the coordinator, and
the runtime write none, so a worker pod or VM that ends after each run has
nothing to send. Only the web server runs the shipper
(`internal/cmd/server.go:213`, `internal/cmd/startall.go:237`).

### Results

Console tells failures apart by `result` alone. Dagu sets `result` on every
entry that records a failure. `login_failed` leaves it empty today
(`internal/service/frontend/api/v1/auth.go:134`), and now gets `failed`. Only
masked entries are sent, and the same release sets both, so every sent entry
carries it.

### Masking

The audit store's `Append` masks each entry before it writes it, so the local
file and the server's Audit page hold the masked entry too. Masking there
covers every producer, including the two that call `Append` without
`audit.Service.Log`: the file store's cleaner
(`internal/persis/file/audit/cleaner.go:138`) and `dagu secret`
(`internal/cmd/secret.go:161`). A PostgreSQL audit store masks in its
`Append` the same way.

- **Values.** It replaces the values of the secrets in Dagu's own store
  (`dagu-managed`), with the masker run output already uses
  (`masking.Masker`), and common token formats, such as GitHub, AWS, and
  Slack tokens. It reloads the values on the next entry after this process
  changes a secret, and at most a minute after another process, such as
  `dagu profile` or another server, does. After a failed load it tries
  again 10 seconds later, or on the next entry after this process changes
  a secret.
- **One pass.** The masker builds a trie of the values once per reload and
  finds the longest value at each offset, so a field takes one pass however
  many secrets there are. Matches that overlap, token formats included, are
  masked as one span, so no match leaves part of another in the entry.
  Today `MaskString` sorts the values with nested loops on every call
  (`internal/cmn/masking/masker.go:101`), about 35 ms for one entry with
  5,000 secrets, and an MCP call writes three entries.
- **`details` stays JSON.** A match inside a string is masked in place. Any
  other match, such as a secret that is a number or a whole object, masks
  the smallest value that holds it, which becomes the masked string.
- **Control fields stay.** It leaves as written only the fields Dagu
  generates or picks from a fixed set: `id`, `timestamp`, `category`,
  `action`, `source`, `surface`, `result`, `resource_type`,
  `credential_type`, `credential_id`, and `correlation_id`, and from v3
  `project_id` (RFC 003), which the server generates. A secret whose value
  is `denied` therefore cannot rewrite a result.
- **Everything else is masked,** and so is any field added later. Today that
  is `details`, `username`, `user_id`, `resource_id`, `workspace`,
  `mcp_tool`, and `ip_address`, and from v3 `project`. Each can carry text from a request: a failed
  sign-in records the name typed, basic authentication sets `user_id` to the
  username sent (`internal/service/frontend/auth/middleware.go:205`),
  `resource_id` can hold a Git remote with a token, and MCP copies a
  requested workspace name into `workspace`.
- **No cuts before the last mask.** A cut through a secret leaves a prefix no
  later mask can match. Producers stop cutting fields: MCP cuts
  `resource_id` and error text before `Append` today
  (`internal/service/mcp/audit.go:55`, `:199`). `Append` does not cut
  either, since a long secret added after an entry was written must still
  match at upload. The local file keeps fields at full length, as it keeps
  `details`, and only the upload cuts them.
- **Why at write.** The store resolves only a secret's current value, and a
  deleted secret is gone (`internal/persis/store/secret.go:359`), so masking
  only before upload would let a rotated or deleted secret through. The
  shipper masks once more before sending, which catches a value added to the
  store after its entry was written.
- **Marker.** A masked entry carries `"masked": true`. The shipper sends only
  those, so entries written before this release never leave the server.
- **External providers.** Secrets from Vault, AWS Secrets Manager, and the
  other external providers resolve only when a run starts, and Dagu does not
  fetch them for masking. Doing so would keep every secret in the web
  server's memory.

### Shipper

A loop in `internal/cloudreport` with its own lease and state file,
`<data_dir>/cloud/audit-state.json`, so one process per installation sends,
whatever `cloud.report` is set to. It cannot share the DAG-run events lease:
the reporter gives that up whenever `cloud.report` is below `runs`
(`internal/cloudreport/reporter.go:312`). While `cloud.audit` is `off`, the
loop does no file I/O.

**Position.** For the file store, the shipper keeps a byte offset per day
file, as Filebeat's registry does.

- The store picks a file by the entry's timestamp, not by when it writes
  (`internal/persis/file/audit/store.go:92`), so an entry made just before
  UTC midnight, or any entry after the clock is set back, can land in an
  older file.
- The shipper therefore keeps an offset for every audit file still on disk,
  and removes one only when local retention deletes the file. No file is
  ever finished, so an entry that lands in an older file is still sent.
- It checks with one `stat` whether a file grew past its offset: today's
  file every 5 seconds, older files once a minute.
- It reads only lines that end in a newline, so a line still being written
  waits for the next pass. A line that does not parse, as when appends from
  two hosts interleave on shared storage, or a line over 1 MiB, becomes a
  `rejected` gap, and the shipper moves past it.
- With no saved state, it starts at the oldest masked entry in the local
  files, so the history the server still holds reaches Console.

**Sending.** When a file grew, the shipper sends up to 1,000 entries that pass
the level, and after a full batch the next one right away, as fast as
Console's pace allows. Entries the level skips still move the offset.

- It saves its offsets after Console answers `200`. Delivery is at least
  once; Console stores each entry once.
- A `413` halves the batch. A single entry that still gets `413` becomes a
  `rejected` gap, and the offset moves past it.
- `429` and `503` wait for `Retry-After`. `401` and `410` stop sending until
  the license changes. `402` asks again hourly.

**Stopping.** On a graceful stop the shipper drains the backlog, batch after
batch within Console's burst.

- The drain starts once the web server has stopped taking requests and the
  requests in progress have ended, or 10 seconds after the stop signal,
  whichever comes first, so the last entries are on disk before it reads.
- It sends entries until 22 seconds after the signal, then uses its last 3
  seconds to report what is left as one `stopped` gap, best effort. If
  Console is unreachable, the gap is lost too.
- It runs on its own deadline, not the server's context, which the signal
  cancels (`internal/cmd/server.go:116`). In `dagu start-all` it runs
  alongside the scheduler's and coordinator's shutdown instead of after it
  (`internal/cmd/startall.go:238`), so the whole drain fits in a 30-second
  termination grace period.
- Unsent entries stay on disk and go out after the next start, if the disk
  survives.

### Local retention

An offset alone cannot tell whether a deleted file held unsent bytes:
`dagu secret` can append, or start a new day's file, while the web server is
stopped. So the cleaner writes its `audit_cleanup` entry before it deletes
anything, listing each file and its size. Today it writes the entry
afterwards, with dates only (`internal/persis/file/audit/cleaner.go:138`).

The shipper reads every cleanup record, once it has read today's file to its
end, and treats a file it never tracked as having offset 0. A recorded size
past the offset sends a `deleted` gap. A tracked file gone with no record, as
after a crash in the middle of cleanup or a manual delete, sends one too.

### Upload format

The shipper prepares each entry in this order:

1. Masks it again with the current values.
2. Replaces each NUL character with U+FFFD, since Console's database cannot
   store one.
3. Cuts each field to its limit, and lists the cut fields in `"truncated"`,
   such as `["username"]`.
4. Encodes it as JSON and measures it, since escaping can grow it: a terminal
   command full of backslashes nearly doubles. While the encoded entry passes
   16 KiB, it cuts `details` by the excess and encodes again.

Masking comes first, so a cut never leaves part of a secret unmasked.

### Wire contract

`POST {license.cloud_url}/api/v1/servers/audit`, with the activation's
credentials as headers, so Console checks them before it reads the body:

```text
Authorization: Bearer <heartbeat_secret>
Dagu-License-ID: <license_id>
Dagu-Server-ID: <server_id>
Content-Encoding: gzip
```

```json
{
  "protocol": 1,
  "entries": [
    {
      "id": "4f1c...",
      "timestamp": "2026-11-20T09:14:03Z",
      "category": "terminal",
      "action": "command",
      "result": "succeeded",
      "user_id": "u1",
      "username": "ops",
      "details": "{\"command\":\"ls\"}",
      "ip_address": "10.0.0.4",
      "masked": true
    }
  ],
  "gap": {"id": "9b2e...", "reason": "deleted",
          "from": "2026-11-02T09:00:00Z", "to": "2026-11-09T09:00:00Z"}
}
```

| Limit | Value |
| --- | --- |
| Request | gzip. 1 MiB compressed, 4 MiB decompressed, 1,000 entries |
| Entry | 16 KiB as encoded JSON |
| Identifiers and names, such as `id`, `user_id`, `username`, `resource_id`, `workspace`, `project_id`, `project`, and `ip_address` | 256 bytes each |
| `category` and `action` | 64 bytes each |

From v3, an entry carries `project_id` and `project`, its project's ID and
its name at the time (RFC 003), and Console filters by them. Entries written
before v3 carry `workspace` instead, and Console shows it as its own choice.

The answer is `{"stored": n, "duplicates": n, "rejected": [...]}`. Console
refuses an entry it cannot store on its own, lists its ID in `rejected`, and
records a `rejected` gap; the shipper logs the IDs and moves on.

The report (RFC 002) gains an `audit` object, so Console can show what waits
on the server:

```json
"audit": {"level": "changes", "unsent_bytes": 18234,
          "oldest_unsent_at": "2026-11-20T09:14:03Z"}
```

At `off`, it carries the level alone.

| Status | Meaning | Server |
| --- | --- | --- |
| `200` | Batch stored | Saves its offsets |
| `401`, `410` | License unknown, released, or revoked | Stops until the license changes |
| `402` | Plan has no audit logs | Asks again hourly |
| `413` | Body over a request limit | Halves the batch. A single entry becomes a `rejected` gap |
| `429` | Over Console's pace or daily quota | Waits for `Retry-After` |
| `503` | Console is busy | Waits for `Retry-After` |

### Gaps

A gap records entries that did not arrive:

| Reason | Sent by | When |
| --- | --- | --- |
| `deleted` | Server | Local retention deleted unsent bytes, or a tracked file vanished with no record of its size. The gap runs from the last sent entry to the oldest entry still on disk |
| `stopped` | Server | The server stopped with entries unsent. The gap covers their range |
| `rejected` | Server, or Console | The server could not parse or send a line, or Console refused an entry |

The server derives a gap's ID from the reason and the range, so a resend
repeats it and Console stores the gap once. It merges its unsent gaps of one
reason into one range. A gap counts as an entry toward Console's pace and
quota. A server held back keeps its gaps with its entries and sends them
once Console accepts them.

### Ephemeral servers and `dagu secret`

Holding entries back, instead of dropping them, needs the audit files to
outlive the process. A web server without a persistent volume, such as a
spot VM or an autoscaled container, keeps `<admin_logs_dir>/audit` and
`<data_dir>/cloud` on a persistent volume, or uses a PostgreSQL audit store.

`dagu secret` writes entries in its own process, which runs no shipper. Its
entries go out when it shares `DAGU_HOME` with a running web server. In a
throwaway container, change secrets through the server's API, which records
the change on the server.

### PostgreSQL audit store

A PostgreSQL audit store keeps no files, so the shipper's position is a
cursor. It cannot be a bare sequence: sequence values commit out of order,
and a cursor past a later commit would skip an earlier one. The store saves
`pg_current_xact_id()` with each entry and reads only rows whose transaction
is older than `pg_snapshot_xmin(pg_current_snapshot())`, ordered by
`(xid, seq)`. The cursor is that pair, and every row behind it is committed,
so none is skipped. A PostgreSQL event store needs the same cursor.

## Failure behavior

| Case | Behavior |
| --- | --- |
| Console unreachable | The shipper backs off. Local files hold the entries, and sending resumes at the offsets |
| The answer is lost after Console stored the batch | The shipper resends it, and Console counts its entries as duplicates |
| Console holds the server back for hours | Each file keeps its offset until Console accepts everything in it, or local retention deletes it |
| Server offline longer than local retention | The shipper sends a `deleted` gap that covers the missing range |
| An entry lands in an older file, as one made just before UTC midnight, or after the clock is set back | The shipper still holds that file's offset, and sends the entry within a minute |
| A line does not parse, or is over 1 MiB | It becomes a `rejected` gap, and the shipper moves on |
| A `dagu-managed` secret is rotated or deleted after an entry carries it | The entry was masked when written |
| The secret values fail to load, or a secret record cannot be read | The entry is masked with the values loaded last and lacks `"masked": true`, so it is not sent. The values load again 10 seconds later, or after this server changes a secret |
| A long secret is added after an entry that carries it was written | The entry was not cut on disk, so the upload mask matches it |
| Entries written before this release | Not sent. They lack `"masked": true` |
| `dagu secret` appends or starts a file while the web server is stopped, and retention deletes it before the shipper starts | The cleanup entry records the file's size, so the shipper sends a `deleted` gap |
| A server on an ephemeral disk stops with entries held | It drains for 22 seconds and tries to send a `stopped` gap. The held entries die with the disk |
| A server is killed with no stop signal | No gap. The entries of the last 5 seconds, and anything held, are lost if the disk goes too |
| MCP call ends without a result, as when the process crashes | Nothing is sent for it. The local log keeps `received` and `started` |
| Distributed install | Every process appends to the shared audit files. Only the lease holder sends |
| `cloud.audit` turned off, then on | Sending resumes at the offsets. Files deleted in between become a `deleted` gap |
| `cloud.audit` changed between `changes` and `all` | Entries after the offsets follow the new level |
| License released or revoked | `401` or `410`. Sending stops until the license changes |

## Performance

- **Ticker.** One `stat` of today's audit file every 5 seconds, and one of
  each older file still on disk once a minute: 6 at the default 7-day
  retention. It runs on the lease holder only, one per installation, and
  not at all while `cloud.audit` is `off`. Nothing grows with DAGs, runs,
  steps, workers, or browser tabs: audit entries come from user actions, not
  runs.
- **Reads.** Only bytes past the offsets, once.
- **Requests.** At most one every 5 seconds while entries arrive, none when
  idle. An entry averages about 1.6 KB as JSON and about 80 bytes after
  gzip.
- **Stored.** The state file holds one offset per audit file, under 1 KB.
- **Masking.** On every audit write, on or off, one pass per masked field
  with a trie built once per secret reload. With no `dagu-managed` secrets,
  the token patterns alone.
- **Upgrade.** Nothing changes until `cloud.audit` is turned on. Then the
  server sends its local history, 7 days by default, at Console's pace.

## Compatibility and rollout

- **Local entries change.** They are masked, carry `"masked": true`, and
  `login_failed` gets `result: failed`. MCP stops cutting `resource_id` and
  error text, so those fields can be longer on disk.
- **The cleanup entry changes.** It is written before the deletion, and its
  `details` gains each file's size.
- **Config.** `cloud.audit` is new and defaults to `off`.
- **Report.** The `audit` object is a new field in the report. Console
  ignores it from older servers, and older Consoles ignore it.
- **Downgrade.** An older binary reads the new entries and ignores the extra
  fields. It sends nothing and ignores `audit-state.json`. Entries it writes
  lack `"masked": true` and are never sent after an upgrade.
- **Distributed.** Workers and the coordinator are unaffected, since they
  write no entries.

Ship in one release: masking at write, `result` on failures, the cleaner
change, and the shipper. Console's endpoint ships first, so a server that
turns sending on sees its entries at once.

## Verification and acceptance criteria

Delivery:

1. A server at `changes` makes 100 MCP calls, 80 reads and 20 changes, which
   write 300 local entries. It sends 20. At `all`, it sends 100.
2. A server stopped for 10 days with 7-day local retention resumes and sends
   a `deleted` gap that covers the missing days. So does one whose file
   `dagu secret` appended to, or started on a later day, while it was
   stopped, and that retention deleted on the next start.
3. An entry stamped 23:59:59.9 UTC and appended at 00:00:01 is sent. So are
   entries written after the clock is set back across midnight by 20
   minutes, into yesterday's file.
4. A server writes no entry for an hour, then one more, which is sent within
   5 seconds.
5. A server held by `429` for an hour keeps its offsets, and the entries go
   out once Console accepts them.
6. A garbled line between two good ones becomes a `rejected` gap, and both
   good entries are sent.
7. A server at `cloud.report: health` and `cloud.audit: changes` sends its
   entries.
8. With `cloud.audit: off`, the shipper makes no `stat` call.

Stopping:

9. A stopping server with 8,000 unsent entries and a reachable Console sends
   them all before it exits.
10. A stopping server over Console's quota keeps its entries, and on a
    persistent disk they go out after the next start.
11. With Console unreachable, a stopping server still exits 25 seconds after
    the signal.
12. A terminal command runs while `dagu start-all` stops, and its entry is
    sent.

Masking:

13. A terminal command with a `dagu-managed` secret's value is masked on disk
    and when sent. So is one whose secret is rotated, or deleted, before the
    entry is sent, and one whose 300-byte secret is added after the entry was
    written.
14. A failed MCP call that names a `dagu-managed` secret as its workspace is
    sent with `workspace` masked. A basic-auth username that contains one is
    masked in both `username` and `user_id`. An MCP URI of 300 bytes with a
    secret across byte 256 is sent with the whole secret masked.
15. With a secret whose value is `denied`, a denied call keeps
    `"result": "denied"`.
16. A failed sign-in has `"result": "failed"`.
17. The `audit_cleanup` entry and an entry from `dagu secret` carry
    `"masked": true` and are sent. Entries written before this release are
    not.
18. Masking one entry with 5,000 `dagu-managed` secrets takes under 1 ms.

Upload format:

19. A failed sign-in with a 300 KiB username is sent with `username` cut to
    256 bytes and `"truncated": ["username"]`. One with a 2 MiB username
    becomes a `rejected` gap, and the entries after it are sent. One with a
    NUL in its username is sent with U+FFFD in its place.
20. A terminal command of 8 KiB of backslashes is sent with `details` cut so
    the encoded entry is at most 16 KiB.
21. A single entry that gets `413` becomes a `rejected` gap, and shipping
    continues.

Completeness:

22. The report carries the level, the unsent bytes, and the oldest unsent
    entry's time while entries wait, and the level alone at `off`.

PostgreSQL:

23. Transaction A writes an entry, then B writes and commits one, then A
    commits. The shipper sends both.

## Alternatives considered

- **Mask only at upload.** A secret rotated or deleted before its entry is
  sent would go out, since the store resolves only current values.
- **Name the fields to mask.** Every new field that carries request text
  would leak until someone added it. Listing the fields to keep instead
  makes a new field masked by default.
- **A log agent or a broker such as Kafka.** The daily files already are a
  durable outbox, and an offset per file gives at-least-once delivery. An
  agent is another process to deploy beside every server, and a broker is
  infrastructure Console does not need for this volume.
- **Finish a file once its day is over.** A clock set back, or a late
  append, would write into a file the shipper no longer reads. Keeping every
  file costs one `stat` a minute each.
- **A bare sequence for the PostgreSQL store.** It skips entries whose
  transaction commits after a later one.
