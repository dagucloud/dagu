# Spec: Sub DAG-Run Visibility

## Status

Implemented. Conformance covers `dagu ls --last` and `dagu status` after a
parent runs a child with `dag.run`.

## Scope

This spec defines where a run that one DAG starts in another with `dag.run`
is reported for the child DAG. It does not define how the child run executes
or where its data is stored.

## Goal

A DAG that runs only under a parent does not look as if it never ran, while
everything that acts on a DAG's runs keeps acting on its own runs.

## Behavior

A child run is stored under its root run. For the child DAG, its latest sub
DAG-run is the newest such run that is not hidden and whose root run still
exists. A run nested under a root run of the same DAG is not one.

Where a DAG shows one run, it shows its own latest run or its latest sub
DAG-run: one that is still running, queued, or waiting when only one of the two
is, and otherwise the one that started later.

- `dagu ls --last` reports that run.
- The DAG list and DAG details APIs return the DAG's own latest run as
  `latestDAGRun` and its latest sub DAG-run as `latestSubDAGRun`, with the
  root and parent runs that open it. The list sorts by the run it shows.
- The Workflows list shows that run, and a sub DAG-run with a link to it
  under its root run.
- Scheduling, `skip_if_successful`, overlap policies, restart policies, the
  service-run guard, `dagu status`, run history, and run actions such as stop
  and retry consider only the DAG's own runs.
- When the root run is removed, the DAG has no latest sub DAG-run until
  another parent runs it.
- A run under an ephemeral root run is not reported.
- A sub DAG-run is shown with the status it last recorded. A run whose
  process died reads as running until the scheduler marks it failed.
- A sub DAG-run is reported for the DAG with its name. A local DAG defined
  inside a parent's file shares its name with a separate DAG file of the same
  name, so that DAG reports the local DAG's runs too.

## Conformance

`conformance/spec098_sub_dag_run_visibility/` runs a parent that runs a child
with `dag.run`, then checks that `dagu ls --last` reports the child's run and
that `dagu status` finds no run of the child's own.
