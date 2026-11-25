# RFC 024: Service DAGs in Reports to Dagu Console

- **Status:** Implemented
- **Date:** 2026-11-25
- **Affected areas:** `cloudreport`, `samples`, `service/scheduler`
  (service DAG state), `cmd` (reporter wiring), and `docs`

## Summary

Report a service DAG to Dagu Console as a service, the way Dagu already
models it, not as a stream of runs. A service DAG (`restart_policy: always`
or `unless-stopped`, Spec 080) starts a new run as soon as its previous run
ends. The server's reporter will stop sending its runs as finished or
in-progress runs. Instead, each report lists every service DAG with its
state, its open outage, its Spec 100 events since the last accepted report,
and its run history by hour.

Console's RFC 022 ("Service DAGs in Console", `dagucloud/cloud`) defines
what Console does with this list: it stores no service run, counts none
toward the workspace's daily run limit, alerts on outages, and shows a
service page.

## Problem statement

- **Every restart is sent as a run.** The reporter sends each top-level run
  that ends as a finished run (`internal/cloudreport/events.go`,
  `reportedEventTypes`). A service DAG with `restart_wait_sec: 1` and a short
  step starts a run every 3 to 6 seconds: 14,000 to 29,000 runs a day
  (Spec 100, Motivation). Console stores them against a daily run limit that
  covers the whole workspace, so one service DAG uses it up within hours and
  Console stores no other workflow's runs for the rest of the day.
- **Every restart sends an early report.** The reporter checks every
  `eventCheckInterval` of 5 seconds (`events.go:31`, `reporter.go:271`) and
  sends early when the runs in progress differ from the last accepted
  report (`activeChanged`, `events.go:691`). A service DAG changes them at
  every check, so the server reports every 5 seconds instead of every
  minute.
- **Console alerts on each failed run.** A service that fails now and then
  opens and resolves Console's failure alert on each failure.
- **Dagu already has the right model, and the reporter ignores it.** Spec 100
  records a sample per run, rolls the samples into tiers with count,
  failures, durations, and uptime, and records `failed` and `recovered`
  events. `GET /service-dags` gives each service's state
  (`DescribeServiceDAG`, `internal/service/scheduler/service_dag_state.go:46`).
  None of it reaches Console.

## Goals

1. A service DAG's runs never reach Console as runs, finished or in
   progress, from a server that has this change.
2. Console learns of a new outage and of a recovery within seconds, and of
   the rest of a service's history at the normal report pace.
3. The hours Console receives match what Dagu's own run history API gives
   for the same DAG and hour.
4. A server with service DAGs reports at its normal interval, except when an
   outage starts or ends.

## Non-goals

- Changing when or how the scheduler restarts a service DAG (Spec 080), or
  what Spec 100 records.
- Sending a service run's steps, log, or errors beyond the latest run's
  error and the event messages.
- Job runs (RFC 022): a service DAG needs the scheduler, so it runs on a
  server and never under a run key.
- Changing what an older Console does: it ignores the new field.

## Guarantees

Promised:

- **No service run in `finished` or `active`.** Runs whose events carry
  `service_dag` (`eventstore.IsServiceDAGEvent`,
  `internal/eventstore/dagrun_events.go:343`) are skipped, and so are runs of
  service DAGs in a rebuilt list of runs in progress.
- **Each new `failed` and `recovered` event is sent**, in the first report
  after the scheduler records it, and again until Console accepts a report
  that holds it.
- **Hours are full values.** An hour is sent with its totals as the run
  history has them, so sending it again is harmless.

Not promised:

- **Every hour after a long outage of reports.** Hours past the report's
  limits are dropped, oldest first (see What it sends).
- **More than 20 events per DAG in one report.** The oldest beyond 20 are
  dropped; the outage figures still give the current outage.

## Design

```text
scheduler (restart_scanner)
  run ends -> ServiceHistory.Observe: sample, failed/recovered events
  -> next run queued

reporter (each interval, or early on a new outage)
  finished, active  : runs of service DAGs left out
  service_dags      : for each service DAG
                        state, latest run     <- DescribeServiceDAG
                        outage                <- run history availability
                        events since ack      <- run history events
                        hours since ack       <- run history hour tier
```

### Where it runs

In the server process, by the reporter that holds the events lease, at the
runs level, as `schedules` are sent today (`reporter.go:795`). The reporter
gains read access to:

- the DAG store's service DAG names (`persis.DAGStore.ServiceDAGNames`,
  `internal/persis/dag.go:81`), to leave service runs out of a rebuilt list
  of runs in progress, whose statuses carry no service flag
  (`activeSetOf`, `internal/cloudreport/active.go:109`);
- the service DAG state that `GET /service-dags` gives, read by
  `scheduler.ListServiceDAGs`, which the API now calls too; the
  consecutive-failure count that the API computes (`failureStreak`) is not
  needed, since the outage figures replace it;
- the run history reader (`internal/samples`), for the availability, the
  events, and the hour tier of each service DAG, and `samples.ReadOutage`
  for the open outage.

### What it sends

A new top-level field `service_dags`, with `service_dags_total`. The report
protocol stays 2.

```json
{
  "workflow": "ingest-loop",
  "restart_policy": "always",
  "restart_wait_sec": 1,
  "state": "failing",
  "next_restart_at": "2026-11-25T10:42:40Z",
  "latest_run": {"dag_run_id": "019...", "status": "failed",
                 "started_at": "...", "finished_at": "...", "error": "exit status 1"},
  "outage": {"open": true, "failed_count": 12,
             "since": "2026-11-25T10:40:02Z", "last_failure_at": "2026-11-25T10:42:33Z"},
  "events": [{"type": "failed", "at": "2026-11-25T10:40:02Z",
              "dag_run_id": "019...", "failed_count": 1, "message": "exit status 1"}],
  "hours": [{"start": "2026-11-25T10:00:00Z", "count": 812, "failures": 12,
             "sum_ms": 1650000, "min_ms": 1800, "max_ms": 2400, "p95_ms": 2200,
             "up_ms": 2520000}]
}
```

- **Which DAGs.** Every service DAG the DAG store knows, suspended or not,
  with or without a run yet. A DAG whose run history cannot be read is still
  listed, with its state and latest run, and without outage, events, or
  hours.
- **State and latest run** are what `GET /service-dags` gives: `state` is one
  of `pending`, `running`, `waiting`, `restarting`, `failing`, `suspended`,
  `stopped`; the latest run's error is cut to 256 bytes.
- **Outage** comes from the run history's availability: `open` and
  `failed_count` describe the open outage, as Spec 100 counts it (no cap);
  `last_failure_at` is `Availability.LastFailureAt`
  (`internal/samples/contract.go:207`), open outage or not.
- **Events** are Spec 100's `failed`, `recovered`, and `gap` events recorded
  after the newest event in the last report Console accepted, oldest first,
  at most 20 per DAG. A `recovered` event carries `failed_count` and
  `outage_seconds`; a `gap` event carries `gap_seconds`.
- **Hours** are the run history's hour tier: the hours since the last
  report Console accepted (the last 24 for the first report), and the start
  hours of the run in progress then and now, since Spec 100 files a sample
  under the hour its run started, all within the 30 days the history keeps
  hours. Each bucket gives count, failures, min, max, and p95 duration, the
  sum of durations, and the uptime with the run in progress, as the run
  history API does with `WithActive`
  (`internal/service/frontend/api/v1/run_history.go:129`). `up_ms` is null
  for a history with no uptime record and for the hour it first recorded a
  run's active time in and before, so the time before a new service ran
  does not count as down. An hour with no run and no known uptime is left
  out. `samples.Bucket` (`internal/samples/contract.go:101`) gains `Sum`;
  the stored aggregate already keeps it.
- **Limits.** At most 500 entries and 256 KiB. Past them the reporter drops
  the oldest hours first, then the oldest events, then whole entries, and
  sets `service_dags_total` to the count of service DAGs. The report stays
  under the 2 MiB Console accepts with the finished runs (512 KiB) and the
  runs in progress it carries today.

### When it sends

- The list goes in every report sent at the runs level by the process that
  holds the events lease.
- A new `failed` event in the run history of a service DAG whose run just
  ended makes the next check send an early report, at most once per DAG in
  10 minutes: Console keeps the service's alert open until it goes that long
  without a failure, so a service that fails now and then waits for the next
  report. The reporter reads the newest event only of the DAGs whose run
  ended in the events it read, for the next three checks.
- A recovery waits for the next report, since Console resolves the alert
  only after the same 10 minutes.
- The reporter keeps, per DAG, the newest event time, the hour, and the
  start hour of the run then in progress that the last accepted report
  sent, with the events position, so a restart of the server resends no
  event and only the hours since.
- Nothing else about a service DAG sends early: a restart, a change from
  `running` to `waiting`, or a new hour waits for the next report.

### Where the runs go

- `eventFeed` skips finished events with `service_dag` set, and they never
  enter the runs in progress.
- A rebuilt list of runs in progress leaves out runs whose DAG name is a
  service DAG name.
- A DAG that is a service at the end of a run is a service for that run, as
  Spec 100 decides. A DAG that stops being a service is reported as a batch
  DAG from its next run on.

## Failure behavior

- A run history that cannot be read leaves that DAG's outage, events, and
  hours out of the report; the state and latest run are still sent. The
  error is logged at most once a minute per DAG.
- A report Console does not accept leaves the events and hours to be sent
  again in the next report.
- An older Console ignores `service_dags` and receives no service runs. Its
  operator sees service DAGs only in Dagu until Console is updated, which is
  why Console ships first.

## Performance

Per report and service DAG: one availability read, one event page since
the last accepted event, and one or two hour buckets, all from the run
history files Spec 100 already keeps small. 100 service DAGs add about
100 KB per report at most, and remove the 12 reports a minute and the
thousands of finished runs a service DAG causes today.

## Compatibility and rollout

1. Console accepts `service_dags` (Console's RFC 022) before any Dagu release
   sends it.
2. A Dagu release ships this change. Servers on Dagu v2.26 to the release
   before it keep sending service runs as runs.
3. Docs: the Console monitoring page says service DAGs are reported as
   services from this release on.

No configuration is added. The protocol number stays 2, since the field is
optional and Console ignores fields it does not know.

## Verification and acceptance criteria

- A server with a service DAG that restarts every 3 seconds sends no run of
  it in `finished` or `active`, and reports once a minute while the DAG
  succeeds.
- A failing run that starts an outage sends a report within one check
  (5 seconds) with a `failed` event and `outage.open` true; the run that
  recovers sends a `recovered` event with `failed_count` and
  `outage_seconds`.
- The hours in a report equal `GET /dags/{fileName}/run-history/samples` for
  the same hours, with the sum of durations added.
- After the server restarts, the next report sends no event Console already
  accepted, and resends the hours from the last accepted hour.
- A list past 256 KiB drops the oldest hours first and sets
  `service_dags_total`.
- A DAG with a schedule and no restart policy is reported exactly as today.

## Alternatives considered

- **Throttle early reports for service runs, and keep sending their runs.**
  Removes the 5-second reports, but every run still counts against
  Console's daily limit and every failure still alerts. Rejected as a patch
  over the wrong model.
- **Send only the failed run that starts an outage, as a finished run.**
  Needs outage tracking in the reporter that survives restarts and lease
  changes, disagrees with Spec 100 on which statuses fail, and still spends
  the daily limit when a service flaps. Spec 100's events already say when
  an outage starts and ends.
- **Let Console guess which workflows are services from their run rate.**
  A guess; Dagu knows.

## Decisions

- Service DAGs are reported from Spec 100's run history and the service DAG
  state, not from their runs.
- The list follows the `schedules` precedent: full list on each report,
  by the events-lease holder, at the runs level, with a total for
  truncation.
- Only a new outage sends a report early, at most once per DAG in 10
  minutes.
