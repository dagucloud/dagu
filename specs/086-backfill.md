# Spec: Backfill

## Status

Partially implemented. `slot_retention_days` is implemented, and conformance
covers it. Slot history is recorded but nothing reads it yet. Backfill
commands, planning, and dispatch are not implemented. Further sections are
added as the behavior they describe lands.

## Scope

This spec defines how Dagu knows what happened to each schedule slot of a
DAG, which a backfill reads to choose the slots that still need a run. It does
not define what a slot or its window is (spec 017), how history retention picks
the runs it removes, or how a run executes.

## Goal

An operator can backfill a range of past slots and have only the slots that
need a run start, even after history retention has removed the runs that show
a slot is done.

## Behavior

### Slot history

- Each DAG keeps a slot history. For each slot it holds the runs that slot had
  and, when the scheduler did not run the slot, why.
- A run belongs to a slot when it is a top-level run with a scheduled time,
  whatever started it: the scheduler, catchup, or a manual run on a chosen
  slot. Its slot is its `context.run.scheduled_at`.
- These runs do not belong to a slot of their own:
  - A sub-DAG run, including one that `dag.enqueue` starts. It takes its
    parent's slot.
  - An ephemeral run.
  - A retry. It is the same run of the same slot.
- For each run, the history holds its run ID, its trigger type, and the
  recurring schedule that fires at the slot. That schedule is read from the
  definition the run was created with, so it stays known after the schedule
  changes. A run on a time that no recurring schedule fires at has none.
- While a run exists, its status is read from the run. When history retention
  removes a finished run, the slot keeps that run and its final status.
- When an operator removes a run, the run no longer counts for its slot. This
  covers `dagu rm --history`, `dagu cleanup`, and deleting a run from the API
  or the UI. A removal refused because the run is active changes nothing.
- The scheduler records a slot that comes due and that it does not run, with
  one of these reasons:

  | Reason | When |
  | --- | --- |
  | `succeeded` | `skip_if_successful` held the slot back. |
  | `running` | A run of the DAG was running. |
  | `queued` | A run of the DAG was queued. |
  | `overlap` | The catchup overlap policy `skip` dropped the slot. |

  A slot whose checks failed is not recorded as skipped. A slot that already
  has a run is recorded through that run.
- Runs that were in history before slot history existed are added to it the
  first time the DAG's slot history is read.
- A slot stays in the history for at least `slot_retention_days` days after
  its scheduled time, whatever `hist_retention_days` or `hist_retention_runs`
  remove.

## Configuration

| Key | Default | Meaning |
| --- | --- | --- |
| `slot_retention_days` | `730` | Days a slot stays in the slot history. A positive integer. |

## Errors

- `slot_retention_days` that is zero or negative: validation fails with an
  error naming `slot_retention_days`.

## Examples

```yaml
schedule: "0 2 * * *"
hist_retention_days: 30
slot_retention_days: 730
steps:
  - run: ./load.sh
```

The run for 2026-10-01 02:00 succeeds. History retention removes it in
November. Its slot still counts as succeeded until at least 2028-09-30.

## Conformance

`conformance/spec086_backfill` asserts that `dagu validate` accepts a positive
`slot_retention_days` and rejects zero.
