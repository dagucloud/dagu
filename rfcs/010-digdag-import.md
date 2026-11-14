# RFC 010: Digdag Import

- **Status:** Proposed; starts only after the gate under
  [Compatibility and rollout](#compatibility-and-rollout) is met
- **Date:** 2026-11-10
- **Affected areas:** a new `dagucloud/digdag-import` repository, `docs`
  (migration guide), and the Dagu agent skill. No change to the `dagu`
  binary beyond RFCs 007, 008, and 009

## Summary

Convert a Digdag project to Dagu:

```sh
digdag-import ./my-project --out ./dags
```

The tool reads the project's `.dig` files and writes one Dagu DAG per
workflow, plus a report. The report lists every construct it converted, every
construct it converted with a caveat, and every construct it left for a
person, with the file and line of each.

Schedules, session time, backfill, and `sla:` map onto RFCs 007, 008, and
009. Operators that Dagu has no action for become commands that call the same
CLI, or are reported. The tool never drops a construct silently.

## Problem statement

Digdag is a YAML workflow engine with a single server and a PostgreSQL
database. Its users write the same kind of file Dagu users write, and many
run it on their own servers. Teams have been moving off it, to managed
Airflow and to Dagster in the published cases, and each migration is a
rewrite.

Dagu is the closest target in shape: YAML, self-hosted, no database. But a
Digdag workflow cannot be moved by hand without deciding, task by task:

- **Session time.** Almost every workflow uses `${session_date}`,
  `${last_session_time}`, or Moment.js arithmetic. Dagu has had no
  equivalent.
- **Backfill.** `digdag backfill` is part of normal operations.
- **`sla:`.** Late-run alerts have had no counterpart.
- **Operators.** Digdag ships about 40, from `sh>` to `bq>` and `td>`.

RFCs 007, 008, and 009 close the first three for every Dagu user. This RFC is
the part that is specific to Digdag.

## Goals

1. Convert the constructs that have an exact Dagu equivalent without review.
2. Report everything else with its location and a suggested rewrite.
3. Produce DAGs that pass `dagu validate`.
4. Let a team run the converted DAGs beside Digdag and compare, before
   switching.
5. Give coding agents the same mapping, so an agent can finish what the tool
   reports.

## Non-goals

- Importing Digdag's run history, sessions, or attempts.
- Importing secrets. The tool lists the secret names a project needs.
- A Digdag compatibility mode in Dagu. Converted DAGs are ordinary DAGs.
- The Treasure Data operators (`td>`, `td_run>`, `td_load>`, and the rest).
  Their users run hosted Treasure Workflow.
- Digdag plugins loaded from Maven. Their tasks are reported.

## What the tool does

```text
project dir ─▶ parse .dig (with !include) ─▶ map ─▶ DAG YAML per workflow
                                              │
                                              └────▶ report.md, report.json
```

- **Input.** A project directory. Each `NAME.dig` becomes `NAME.yaml`.
- **Scripts and queries** the tasks reference are copied beside the DAGs
  unchanged, and their paths are kept.
- **Output is deterministic.** The same input gives the same files, so a
  team can commit the result and rerun the tool after changing the source.
- **Exit code.** `0` when every construct converted, `1` when the report has
  items for a person, `2` when a file could not be parsed.
- **`--check`** writes nothing and prints the report, for sizing a migration
  before starting it.

Each reported item reads:

```text
etl.dig:42  +load  bq_load>  not converted
  Dagu has no BigQuery load action. Suggested: run `bq load` in a command
  step. See https://docs.dagu.sh/migration/from-digdag#bq-load
```

## Mapping

Levels: **exact** converts without review; **caveat** converts and is listed
in the report; **manual** is left as a commented stub and reported.

### Workflow structure

| Digdag | Dagu | Level |
| --- | --- | --- |
| `+task` in order | A step; order kept by dependencies | exact |
| Nested `+group` | Steps of one graph, with the group's settings applied to each | caveat |
| `_parallel: true` | Steps with no dependency between them | exact |
| `_parallel: {limit: N}` | The same, with the DAG's concurrency limit | caveat |
| `_background: true` | A step the next step does not depend on | exact |
| `_retry: N`, `_retry: {limit, interval, interval_type}` on a task | `retry_policy` | exact |
| `_retry` on a group | Reported: Dagu retries a step, not a group. Suggested: move the group to a sub-DAG | manual |
| `_error:` | `handler_on.failure` | exact at workflow level; manual on a task |
| `_check:` | A step that depends on the checked step | exact |
| `_export:` | `env` for the step or the DAG | exact |
| `!include` | Inlined | exact |
| `call>` | `action: dag.run` | exact |
| `require>` | `action: dag.run` with the same scheduled time | caveat: see below |
| `for_each>` | `foreach` | exact for one list; caveat for a product of lists |
| `for_range>`, `loop>` | `foreach` over a generated list | caveat |
| `if>` | Step preconditions | caveat |
| `fail>` | A step that exits non-zero with the message | exact |
| `echo>` | `action: log.write` | exact |
| `wait>` | `action: wait.duration` | exact |
| `docker: {image}` | `container` | caveat |

`require>` starts the other workflow only if it has not already run for the
same session time. `dag.run` always starts a child run. The converter emits
`dag.run` and reports the difference. Open question 2 asks whether Dagu
should gain the check.

### Schedule

| Digdag | Dagu | Level |
| --- | --- | --- |
| `timezone:` | `CRON_TZ=` prefix on each schedule | exact |
| `daily>`, `hourly>`, `weekly>`, `monthly>`, `minutes_interval>` | The equivalent cron expression | exact |
| `cron>` | The same expression | exact |
| `skip_on_overtime: true` | `overlap_policy: skip` | exact |
| `skip_delayed_by:` | Reported; `catchup_window` is the nearest control | manual |
| `start:`, `end:` | Reported | manual |
| `sla: {time}` | `deadline: {at}` (RFC 009) | exact |
| `sla: {duration}` | `deadline: {within}` | exact |
| `sla: {fail: true}` | `fail: true` | exact |
| `sla: {alert: true}` | `notify: true`; the route is configured on the server | caveat |
| Tasks under `sla:` | Reported. Suggested: a DAG started by `dag.run.deadline_missed` | manual |

### Session time

Digdag's session time is not the fire time. For `daily>: 07:00:00` the
session time is 00:00 of that day; for `hourly>: 30:00` it is the top of the
hour. Dagu's `scheduled_at` is the fire time. The converter writes `dates`
entries (RFC 007) that reproduce Digdag's values:

| Digdag | `dates` entry | Level |
| --- | --- | --- |
| `session_time` | `{ shift: day_start, format: rfc3339 }` (`hour_start` for hourly) | exact |
| `session_date` | `{ format: date }` | exact |
| `session_date_compact` | `{ format: date_compact }` | exact |
| `session_local_time` | `{ shift: day_start, format: datetime }` | exact |
| `session_unixtime` | `{ shift: day_start, format: unix }` | exact |
| `session_tz_offset` | `{ format: "%z" }` | exact |
| `last_session_*` | The same, from `interval_start` | exact |
| `next_session_*` | The same, from `next_scheduled_at` | exact |
| `last_executed_session_time` | Reported: it depends on run history | manual |
| `moment(session_time).add(...)`, `.subtract(...)`, `.startOf(...)`, `.format(...)` chains | `shift` and `format` | exact for the listed calls with literal arguments; manual otherwise |
| Any other JavaScript in `${...}` | Reported. Suggested: a `js.run` step | manual |
| `session_id`, `session_uuid`, `attempt_id` | `context.run.id`, `context.attempt.id` | caveat: the values differ in form |
| `task_name` | `context.step.name` | caveat |
| `project_id` | Reported | manual |

Each generated entry is named after the expression it replaces, and
references in commands, queries, and params are rewritten to `${dates.NAME}`.

A manual Digdag run uses the current time as its session time. A manual Dagu
run without `--scheduled-at` uses the DAG's latest past slot (RFC 007), so its
`dates` follow that slot rather than the current time.

### Operators

| Digdag | Dagu | Level |
| --- | --- | --- |
| `sh>` | A command step | exact |
| `py>`, `rb>` | Reported with a stub that calls the function from a command | manual |
| `embulk>` | `embulk run <file>` in a command step | exact; Embulk must be installed |
| `http>` | `action: http.request` | caveat |
| `mail>` | `action: mail.send` | caveat |
| `pg>` | `action: postgres.query` | exact for a query; manual for `insert_into` and `create_table` |
| `redshift>` | `action: postgres.query` | caveat: to verify against Redshift |
| `redshift_load>`, `redshift_unload>` | Reported with the `COPY` or `UNLOAD` statement to run | manual |
| `bq>`, `bq_ddl>`, `bq_load>`, `bq_extract>` | `bq` in a command step | caveat: the `bq` CLI and credentials must be present |
| `s3_wait>`, `gcs_wait>` | A polling command step with `repeat_policy` | caveat |
| `param_get>`, `param_set>` | Reported. Suggested: `state` | manual |
| `http_call>`, `emr>` | Reported | manual |
| `td>` and the other `td_*>` operators | Reported | manual; out of scope |
| `${secret:NAME}` and operator secrets | A Dagu secret reference; the name is added to `secrets.txt` | caveat |

### Operations

The migration guide maps the commands. The tool does not convert them.

| Digdag | Dagu |
| --- | --- |
| `digdag push` and revisions | Git sync |
| `digdag backfill --from` | `dagu backfill start --from` (RFC 008) |
| `digdag retry --resume` | Retry of the failed run |
| `digdag retry --all` | A new run with `--scheduled-at` |
| `digdag reschedule --skip-to` | Suspend the DAG until then |
| `digdag secrets --set` | Dagu secrets |

## Running both side by side

The guide describes a parallel run, since no team switches a scheduler in
one step:

1. Convert, and resolve the report's items.
2. Point the converted DAGs at a scratch dataset or schema through params.
3. Run both schedulers on the same schedule for a week.
4. Compare outputs, then compare `dagu backfill start --dry-run` with the
   Digdag sessions of that week.
5. Disable the Digdag schedule and repoint the DAGs.

## For coding agents

The mapping tables ship as a page in the Dagu docs and as part of the Dagu
agent skill. An agent given a Digdag project runs `digdag-import`, reads
`report.json`, and rewrites the manual items with the suggested forms. The
JSON report carries, for each item, the source location, the construct, the
level, the suggested rewrite, and the path of the generated stub.

## Compatibility and rollout

| Step | Ships |
| --- | --- |
| Gate | Interviews with five teams that run Digdag in production today. The RFC proceeds only if at least three would move to a self-hosted successor, and their workflows fit the mapping above. |
| First | `--check`, structure, schedule, session time, `sh>`, `call>`, `embulk>`. Requires RFC 007. |
| Second | The remaining operators, `sla:` (RFC 009), `report.json`, and the migration guide with the backfill mapping (RFC 008). |
| Third | The agent skill section. |

- The gate comes first because the demand is not established. The teams
  whose migrations are published chose managed services, not another
  self-hosted scheduler.
- The tool is versioned on its own. It tracks a foreign format and should
  not wait for a Dagu release.
- It targets the syntax documented for Digdag 0.10.5.

## Verification and acceptance criteria

1. Every example workflow in Digdag's documentation converts, or is reported
   with a location and a suggestion.
2. Every generated DAG passes `dagu validate`.
3. For `daily>: 07:00:00` in `Asia/Tokyo`, the generated `session_date`,
   `session_time`, and `last_session_time` entries equal Digdag's values for
   the same session, checked for a month that contains a month end.
4. The same holds for `hourly>` and `weekly>`.
5. `moment(session_time).add(-1, 'days').format('YYYY-MM-DD')` converts to a
   `dates` entry with the same value.
6. A workflow that uses `td>` converts its other tasks and exits `1`.
7. Running the tool twice gives identical files.
8. An agent given `report.json` and the skill resolves the manual items of a
   sample project without reading the tool's source.

## Alternatives considered

- **A `dagu import digdag` subcommand.** Ties a foreign format's parser to
  Dagu's release cycle and binary, for a migration each team runs once.
- **A guide without a tool.** The session-time and schedule mappings are
  mechanical and easy to get wrong by hand.
- **A Digdag compatibility layer** that runs `.dig` files directly. Dagu
  would own Digdag's semantics forever, including JavaScript evaluation.
- **Leave it to coding agents** with only a guide. An agent's conversion
  has no report and is not repeatable. The tool gives the deterministic
  part; the agent finishes the rest.
- **Native `bq` and Embulk actions first.** They help every user, but the
  CLIs already work from a command step. They can follow demand.

## Consequences

- Dagu gains a documented path from the nearest self-hosted YAML scheduler.
- The mapping becomes a public statement of what Dagu does not do. Each
  manual row is a visible gap.
- A second repository to maintain, whose users arrive once and leave.

## Open questions

1. Is the gate's threshold right, and who runs the interviews?
2. Should Dagu add "run this DAG for a slot unless it already ran", so
   `require>` converts exactly? RFC 008's slot lookup makes it possible.
3. Should `py>` convert through a small shim that reproduces `digdag.env`,
   or stay manual?
4. Should the tool also read a running Digdag server, to list which
   workflows are scheduled and which secrets are set?
