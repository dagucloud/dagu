# Spec 083: Continuous Runs

## Status

Partially implemented. This spec defines the behavior of a step that repeats
without end, such as a heartbeat that checks a service every second for
years.

Covered: which steps are continuous, the samples and events of a step run by
the local runner, the tiers and their retention, the three API queries and
their errors, the status writes and the compaction of the status file, and the
bounded step log. `conformance/spec083_continuous_runs` checks this behavior.

Not covered yet: runs that a distributed worker executes, the status writes of
a continuous sub-DAG step, a retry of a run (it starts a new store), and the
time range of the step log.

## Scope

This spec covers:

- which steps are continuous steps
- the sample that a continuous step records for each execution
- the tiers of samples and aggregates, and their retention
- the events that a continuous step records
- the bounds on the status record, the step log and the scheduler log
- the sample, event and availability queries of the HTTP API
- the compact display of an execution count

This spec does not define:

- `repeat_policy` field shapes, repeat modes and the repeat decision. See
  [Spec 075: Repeat Policy](075-repeat-policy.md).
- any change to a step that repeats with a `limit`
- alerts and notifications on a failed sample

## Goal

A workflow author can run a check every second for years. The disk, the
memory and the pages stay small, and the page of the run shows how the check
went over any period.

## Motivation

A run that repeats without a limit writes the whole run status to its status
file, with a sync, after each execution. It also appends the step output and
two scheduler log lines. At one execution per second, a status of 4 KB makes
more than 100 GB in a year. A page that holds each execution in memory cannot
show 31 million of them.

## Terms

A continuous step is a step whose `repeat_policy` has a `repeat` mode and no
`limit`.

A sample is the record of one execution of a continuous step.

An aggregate is the summary of the samples of one time bucket.

A tier is a bucket width with a retention period.

A failed sample is a sample whose exit code is not 0.

## Behavior

### Continuous Steps

- A step is continuous when its `repeat_policy` has a `repeat` mode and no
  `limit`.
- A step with a `limit` is not continuous. Its behavior does not change.
- The rules of this spec apply to each continuous step of a run. A run can
  have more than one.

### Samples

The runner records one sample after each execution of a continuous step.

A sample has:

- `time`: the start of the execution, in UTC, with millisecond precision.
- `duration`: the length of the execution, in milliseconds.
- `exitCode`: the exit code of the execution. A step that did not start has
  the code `-1`.
- `message`: an optional text of 256 bytes at most. It is the last line of
  the standard error of a failed execution. It is empty for an execution that
  succeeded.

When the clock steps back before the newest sample, a sample is stored at the
time of the newest sample, so that the samples of a step stay in time order.

The runner never holds more than one minute of samples in memory.

### Tiers

The samples of a continuous step are kept in four tiers:

| Tier | Bucket | Kept for |
| --- | --- | --- |
| raw | 1 second | 24 hours |
| minute | 1 minute | 7 days |
| hour | 1 hour | 30 days |
| day | 24 hours | no limit |

Rules:

- A bucket starts at a multiple of its width, in UTC. A day bucket starts at
  00:00 UTC.
- The raw tier has one record for each sample.
- An aggregate has: the start of the bucket, the count of samples, the count
  of failed samples, the least, the greatest and the sum of the durations, and
  a duration histogram. The histogram uses fixed buckets of a logarithmic
  scale, so that two histograms can be added. The 50th and the 95th percentile
  come from the histogram, and are exact to the width of a histogram bucket.
- The runner builds the aggregates of the minute tier from the raw tier, the
  hour tier from the minute tier, and the day tier from the hour tier. It
  builds a bucket only when the bucket has ended.
- A tier drops a record only when the next tier holds its aggregate. The
  roll-up of a bucket gives the same result when it runs more than once.
- A run that stops or crashes loses at most the samples of the last minute.
  The next start of the run builds the missing aggregates before it records a
  new sample.

### Events

A continuous step records an event, at full resolution and with no expiry, when:

- the step starts (`started`) and stops (`stopped`).
- a sample fails while no outage is open: the first sample of the step, or a
  sample after one that succeeded (`failed`).
- a sample succeeds while an outage is open (`recovered`).

An outage that is open when the step stops or crashes goes on at the next
start: the count of failed samples continues, and the next start records no
`failed` event of its own. Event times never decrease.

An event has: `time`, `kind`, `exitCode`, and `message`. A `recovered` event
also has the count of failed samples and the length of the outage. A repeated
`failed` event, and a `stopped` event during an outage, also have the count of
failed samples.

A `failed` event is also recorded after one hour of failed samples, with the
count so far, so that a long outage has more than one record.

### Availability

The availability of a window is the count of samples that succeeded divided by
the count of all samples of the window. A window with no sample has no
availability.

The windows are the last 24 hours, 7 days, 30 days and 365 days. The server
computes them from the finest tier that covers each part of the window.

### Status Record

- The status record of a run stays the single source of truth for the state of
  the nodes. A continuous step does not write the status record after each
  execution.
- While a continuous step repeats, the runner writes the status record at
  most once every 60 seconds, and at once for a change of state: a failed or a
  recovered execution, a stop, a retry, or the end of the run.
- The `doneCount` of the node is the count of executions at the last write.
- The status file of a run is appended to. When it is larger than 1 MiB, the
  runner replaces it with a file that holds only the last record. The
  replacement is atomic: a reader sees the old file or the new file.
- A reader of the status file is not affected by the replacement.

### Step Log

- The standard output and the standard error of a continuous step go to a log
  that is bounded in size: 4 segments of 2 MiB. When the last segment is full,
  the oldest segment is dropped.
- The runner keeps the output of the last execution, and the output of each
  failed execution, as long as its segment is kept. It discards the output of
  an execution that succeeded, once the next execution has started.
- The log API reads the kept segments. The rules of the tail and of the range
  stay the same.

### Scheduler Log

- A continuous step logs at the info level only: its start, its stop, a
  `failed` event, a `recovered` event, and a summary line once an hour that
  gives the count of samples and of failed samples.
- It logs each execution at the debug level.

### Compact Count

A count of executions that has more than three digits shows in a compact form:

| Count | Display |
| --- | --- |
| 0 to 999 | the number: `742` |
| 1 000 to 9 999 | one decimal and `k`: `1.2k` |
| 10 000 to 999 999 | the whole number of thousands and `k`: `34k`, `345k` |
| 1 000 000 and more | one decimal and `M` up to 9.9M, then the whole number: `1.2M`, `31M` |

Rules:

- The compact form rounds down. It never shows more than the real count.
- The decimal separator is a point. The display has at most five characters
  after the `×` mark.
- The tooltip and the accessible name give the whole count, with the
  separators of the language.
- Every place that shows a count of executions uses the compact form: the node
  of the graph, the header, the list of steps, and the details of the run.

### API

A request for a step that is not continuous gets the response `404` with the
message `step X is not a continuous step`.

`GET /dag-runs/{name}/{dagRunId}/steps/{stepName}/samples`

- Query: `from` and `to` (RFC 3339, `to` defaults to now, `from` defaults to
  24 hours before `to`), `points` (an integer from 10 to 2000, default 600).
- The server chooses the finest tier whose retention covers `from` and whose
  bucket count in the window is not more than `points`. When no tier fits,
  it uses the coarsest tier and cuts the window to `points` buckets from `to`.
- The response gives `resolutionSeconds` (the width of the tier),
  `from`, `to`, and the `buckets`. A bucket has `time`, `count`, `failures`,
  `minMs`, `maxMs`, `p50Ms`, `p95Ms`. A bucket with no sample is not in the
  list. A response has at most `points` buckets.
- The response time is bounded: it does not depend on the count of samples of
  the run.

`GET /dag-runs/{name}/{dagRunId}/steps/{stepName}/events`

- Query: `from`, `to`, `limit` (1 to 500, default 100), `before` (an opaque
  cursor).
- The response lists the events, the newest first, with `nextCursor` when
  there are more.

`GET /dag-runs/{name}/{dagRunId}/steps/{stepName}/availability`

- The response gives, for each window of the Availability section: `window`
  (`24h`, `7d`, `30d`, `365d`), `count`, `failures` and `availability` (a number
  from 0 to 1, absent when `count` is 0), and the current streak: the count of
  samples since the last failed sample and the time of the last failed sample.
  Once the raw tier no longer holds that sample, the time is the start of the
  aggregate bucket that holds it, and the count is a lower bound: it leaves out
  the samples that succeeded after the failure in the same bucket.

The run details give `continuous: true` on the node of a continuous step.

### User Interface

The page of a run shows a continuous step with:

- the availability of each window, the current streak, the last failure, and
  the compact count of executions.
- a chart of the samples with a time axis that zooms from one hour to years.
  A bar is a bucket of the tier of the zoom. A bucket that has failures shows
  them in red. A click on a bucket lists the events of its window.
- the list of events, with the cursor of the API.
- the step log, read in the range of time that the chart shows.

The chart draws at most the `points` buckets of the response. It never draws
one element for each sample.

## Constraints

- The raw tier of one continuous step uses at most 3 MiB of disk for each
  86 400 samples that it holds, which is one day at one sample each second,
  plus 1 KiB for each failed sample. Each day older than the raw tier adds a
  constant that does not depend on the sample rate. Events are outside this
  bound.
- Memory use of the runner and of the server does not grow with the count of
  samples.
- An API request of this spec answers in 500 ms at most for a run of three
  years at one sample each second, on the reference machine of the tests.

## Errors

- A query parameter out of its range, `from` after `to`, or a `before` cursor
  that the server did not give gets `400`.
- A request for a run or a step that does not exist, or for a step that is not
  continuous, gets `404`.
- A store that cannot open does not stop the step: the step keeps running and
  records no sample.

## Examples

A step that repeats until a file exists is a continuous step:

```yaml
steps:
  - id: beat
    run: sh beat.sh
    repeat_policy:
      repeat: until
      condition: test -f done
      interval_sec: 0
```

The same step with `limit` is a repeated step, not a continuous one. The
fixtures in `conformance/spec083_continuous_runs/testdata` give both.

## Test Scenarios

- A step with `limit` records no sample and keeps the status behavior of today.
- A step with no `limit` records one sample for each execution.
- The roll-up of a tier gives the same aggregates when it runs twice.
- The status file of a run with a continuous step stays under 2 MiB after
  100 000 executions.
- The step log of a continuous step stays under 8 MiB after 100 000
  executions, and keeps the output of the failed executions that are in the
  kept segments.
- The three queries return the documented fields, and the response of
  `samples` has at most `points` buckets for a window of three years.
- A request for a step that is not continuous gets `404`.
- The compact count rounds down, and the tooltip gives the whole count.
