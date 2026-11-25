# Spec 100: Service Run History

## Status

Partially implemented. The scheduler records the history of service DAGs,
and the API serves it. The UI that reads it is not implemented yet.

`conformance/spec100_service_run_history` checks the behavior of this spec with a local scheduler.
A run that a distributed worker executes has no conformance test. The scheduler records its sample
from the status that the coordinator writes, as for a local run.

This spec defines the long-term history of the runs of a service DAG. The history keeps one sample
for each run, also after the retention of the run records removed the run.

The history reuses the samples, the tiers, the events, the availability, the compact count and the
chart of [Spec 083](083-continuous-runs.md). This spec gives only the rules that differ for runs.

## Scope

This spec covers:

- which runs are service runs
- the sample that the scheduler records for each service run
- the runs that end while no scheduler records them
- the tiers, the events, the uptime and the success rate of the run history
- the bounds on disk, memory, API responses and page elements
- the run history queries of the HTTP API
- the activity of a service DAG in the DAG list
- the contract of the Activity panel and of the Workflows list for a service DAG
- distributed runs, and the history of a service DAG that existed before this spec

This spec does not define:

- when the scheduler starts a run of a service DAG. See [Spec 080](080-restart-policy.md).
- the retention of the run records. Spec 080 defines it, and this spec does not change it.
- the samples of a continuous step. See Spec 083.
- a run history for a DAG that is not a service DAG
- alerts and notifications

## Goal

An operator can see how a service DAG went over the last hour, day, month or years. The operator
sees the count of runs, the failures, the durations and the time with no run. The disk, the memory
and the pages stay small, also at tens of thousands of runs a day.

## Motivation

A service DAG starts a run each time its previous run ends. With `restart_wait_sec: 1` and a step of
a few milliseconds, one DAG starts a run each 3 or 6 seconds. This is 14 000 to 29 000 runs a day.
Each run writes a full run record: 11 files and directories, and 40 KiB on disk. The retention
keeps the latest 100 runs, which is 5 to 10 minutes of runs. After that time, the server has no
record of a run: no count, no failure and no duration.

The DAG list reads up to 60 full run records of each service DAG for each response. It draws them as
if they were spread over the last day, but they are the runs of the last few minutes. The Activity
panel shows the latest 30 runs, also in a window of a day or a week.

## Related Specs

- [Spec 080: DAG Restart Policy](080-restart-policy.md) defines service DAGs and the retention of
  their runs.
- [Spec 083: Continuous Runs](083-continuous-runs.md) defines the samples, the tiers, the events, the
  availability, the compact count and the chart that this spec reuses.

## Terms

A service DAG is a DAG whose `restart_policy` is `always` or `unless-stopped`, as Spec 080 defines.

A service run is a run of a service DAG that is not a sub-DAG run. The current definition of the DAG
at the end of the run decides whether the DAG is a service DAG.

A run sample is the record of one service run.

The run history of a DAG is the set of the run samples of the DAG, their aggregates and their events.

A failed run sample is the sample of a run whose status is `failed`.

A run is active from its start to its end. A run in progress is active up to now.

A restart gap is the time from the end of a run to the start of the next run, when it is not longer
than `restart_wait_sec` plus 10 seconds. The DAG restarts as designed in a restart gap, so a restart
gap is active.

The uptime of a window is the share of the window in which a run of the DAG was active, with the
restart gaps. A time between two runs that is longer than a restart gap is not active. This includes
the time of a stopped scheduler and of a suspended DAG.

The success rate of a window is the count of the runs that did not fail, divided by the count of the
runs that ended in the window.

The terms aggregate, tier, bucket, outage and streak are as Spec 083 defines them.

## Behavior

### Run Samples

The scheduler records one run sample each time a service run ends.

A run sample has:

- `time`: the start of the run, in UTC, with millisecond precision. A run that did not start has the
  time of its end.
- `duration`: the time from the start to the end of the run, in milliseconds. A run that did not
  start has the duration 0.
- `failed`: true when the run ended with the status `failed`. A run that succeeded, partially
  succeeded, was aborted or was rejected did not fail.
- `message`: the error of a failed run, 256 bytes at most. It is the error of the run, else the
  error of its first failed step. It is empty for a run that did not fail.

Rules:

- A run that ends again after a retry records one more sample. Each attempt that ends has one sample.
- The scheduler writes the sample to disk before it queues the next run of the DAG.
- While the scheduler runs, the API gives a sample at most 5 seconds after the end of its run.
- When two runs of the DAG overlap, the sample of the run that ends first can wait for the end of
  the other run.
- When a sample is older than the newest sample of the history, the history stores it at the time of
  the newest sample. This occurs when two runs of the DAG overlap.
- A run sample does not hold the run ID. The `failed` and `recovered` events hold it.
- The memory that the scheduler uses for the samples of a DAG does not grow with the count of runs.
- The scheduler also records the time of the run as active time. See Uptime.

### Runs That End While No Scheduler Records Them

The scheduler takes the runs of the history from the runs that it reads to restart the DAG: the
latest run and the 10 newest runs. It reads no other run for the history. A run that ended at or
before the newest sample of the history is already in it.

Rules:

- A service DAG that existed before this spec starts its history at its first run that the
  scheduler records. The runs that its retention kept get no sample.
- A scheduler that was stopped, or that took over from another scheduler, records the runs that
  ended meanwhile, when they are among the 10 newest runs of the DAG. A service DAG does not
  restart while no scheduler runs, so only the run in progress and the runs that a user starts can
  end meanwhile.
- A run that ended before the newest sample, and that the history does not hold, gets no sample.
- The scheduler does not change a run record.

### Retention of Run Records

The run history does not depend on the retention of the run records:

- A run that the retention, the `cleanup` command or a user removes keeps its sample.
- The scheduler records the sample of a run before it queues the next run. The retention applies
  when a run starts. Thus the retention never removes a run that the scheduler follows before its
  sample exists. This is also true with `hist_retention_runs: 1`.
- A run that a user starts while no scheduler runs gets its sample when the scheduler starts again,
  if it is still among the 10 newest runs of the DAG.

### Tiers

The run history keeps its samples in the four tiers of Spec 083:

| Tier | Bucket | Kept for |
| --- | --- | --- |
| raw | 1 second | 24 hours |
| minute | 1 minute | 7 days |
| hour | 1 hour | 30 days |
| day | 24 hours | no limit |

The aggregates, the histogram of the durations, the percentiles, the roll-up and the repair follow
the rules of Spec 083.

### Events

The run history records an event, at full resolution, when:

- a run fails while no outage is open: the first sample of the history, or a sample after a run
  that did not fail (`failed`).
- a run does not fail while an outage is open (`recovered`).
- a run starts more than `restart_wait_sec` plus 60 seconds after the end of the previous run,
  and the previous run succeeded or partially succeeded (`gap`).

Rules:

- The `failed` and `recovered` events follow the rules of Spec 083. A `failed` event also repeats
  after one hour of failed runs.
- A `failed` event and a `recovered` event hold the ID of their run.
- A `gap` event has the time of the end of the previous run and the length of the gap in seconds.
  It also has the ID of the run that ends the gap.
- The run history records no `started` event and no `stopped` event.
- The events of a run history use at most 4 MiB on disk. Above this size, the scheduler drops the
  oldest events.

A gap is a time with no run after a run that succeeded. A stopped scheduler, a suspended DAG, or a
queue that did not start the run can cause it. A loop of runs that fail is an outage, and its
`recovered` event gives its length. After a run that did not succeed, the scheduler can wait longer
than `restart_wait_sec`, so that time is not a gap.

### Availability

A service DAG has two figures for each window: the uptime and the success rate. The windows are
`24h`, `7d`, `30d` and `365d`, as in Spec 083.

The success rate is the availability of Spec 083: the count of runs that did not fail, divided by
the count of all the runs of the window. A window with no run has no success rate. The streak, the
last failure and the total count follow the rules of Spec 083. The total count is the count of all
the samples of the run history.

The uptime is the active time of the window divided by the length of the window. It is a number
from 0 to 1. Failures stay visible in the count, the last failure, the streak and the events. A run
that fails is active until its end, so a failure does not lower the uptime by itself.

Rules:

- The run in progress counts from its start, or from the start of the window, up to now. The latest
  run of the DAG gives it. A run that is queued and has not started does not count.
- A window that starts before the first recorded run counts from that run. The run in progress
  can be this first run.
- A window with no record of active time has no uptime. The API leaves the field out, and the UI
  shows a dash. The uptime is never 0 when it is unknown. A window with a record and no run in it
  has the uptime 0.
- The uptime is exact to the bucket of the tier that holds the window. The window starts on a bucket
  and ends on a bucket, so it can be shorter than its name by less than one bucket.

### Uptime

A run can last hours, and a sample is in the bucket of the start of its run. So the scheduler
records the active time of each run apart from the sample. It gives each tier the part of the run
that falls in each bucket. The tiers are the minute, the hour and the day. The raw tier has no
uptime.

Rules:

- The active time is in the directory `up` of the run history. It has one line for each bucket, with
  the start of the bucket and the active time in milliseconds. A bucket line holds the whole active
  time of the bucket, so a write replaces it. A file `up/since` holds the start of the first
  recorded run.
- A run of three hours that starts at 00:30 gives 30 minutes to the hour of 00:00, 60 minutes to
  each of the two next hours, and 30 minutes to the hour of 03:00. A run across midnight gives each
  day its own part.
- Each tier gets its parts when the run ends. A tier is not a roll-up of a finer tier. The sum of the
  buckets of a tier is the same in each tier that still keeps them.
- A tier keeps only the buckets that are inside its retention: 7 days for the minute tier and 30 days
  for the hour tier. The day tier has no limit.
- The scheduler counts the time that is not yet counted: a run counts from the later of its start
  and the end of the newest recorded run. Two runs that overlap count their union, unless the run
  that started later ends first. The run that started first then loses its time before the start
  of the other run. No time counts twice, and a bucket never holds more than its length.
- The scheduler counts a restart gap as active time when it records the run that ends the gap. It
  gives the gap to the buckets in the same way as the time of a run. The first run of the history
  has no gap before it.
- The stored active time is the result. The reader of the history applies no tolerance. The
  scheduler knows the end of the previous run and `restart_wait_sec` at the time of the write, and
  the reader does not know `restart_wait_sec`. This is why the tolerance is at the write.
- The margin of 10 seconds is a constant of the scheduler. It is not a configuration key. A run of
  a service with `restart_wait_sec: 1` starts up to 5 seconds after the end of the previous run, so
  the restart gap of 11 seconds is more than twice the longest gap that was measured.
- A gap longer than `restart_wait_sec` plus the margin is not active. A stop of the scheduler that
  lasts 20 seconds, with `restart_wait_sec: 1`, is not active.
- A gap after a run that failed is a restart gap in the same way.
- A run in progress counts from its start. The gap before it joins the active time when the run
  ends.
- A run history that has recorded active time before this rule keeps its stored values. The new rule
  applies to the runs that the scheduler records after the update.
- Each tier writes a bucket when the bucket ends, and its open bucket when the scheduler closes or
  opens the history. A reader takes the open hour and the open day from the minute tier, and keeps
  the larger of their line and the sum of their minutes. The history writes the open minute at once
  when a run covers more than one minute. A crash can lose the active time of the open minute.
- A run history that was written before this feature has no `up` directory. Its uptime is unknown.
  Its samples, aggregates and events do not change. The uptime starts at the first run that the
  scheduler records after the update, and the windows count from it.
- This feature adds no configuration key.

Rules for the reader:

- A samples query gives the active time of each bucket as `upMs`. It takes the value from the `up`
  tier that has the same width as the tier of the samples. The raw tier has no `upMs`.
- A bucket that has active time and no sample is in the response. It has `count` 0, `failures` 0
  and `upMs`. A history with no `up` directory gives no `upMs`.
- The run in progress adds its active time to the buckets that it covers, up to the end of the
  query. It starts at the later of its start and the end of the newest recorded run, as the
  scheduler counts it when the run ends. The stored time is not counted twice, and the uptime does
  not drop when the run ends. The store has no line for the run in progress, because the scheduler
  writes the active time when the run ends. The server adds the live part at the time of the query.
- The live part and the uptime of the window use the same start. `upMs` is never more than the
  width of the bucket.
- The response has at most `points` buckets. The buckets that have only `upMs` count in this limit.

### Life of a Run History

- The run history of a DAG starts at its first service run that the scheduler records.
- A DAG that is no longer a service DAG keeps its run history. It records no new sample, and the API
  still gives its run history.
- A DAG that becomes a service DAG again continues its run history.
- A deleted DAG keeps its run history, as it keeps its run records.

### Distributed Runs

A worker that executes a service run reports the status of the run through the coordinator, as for
any run. The scheduler records the sample from this status. The worker records no sample.

### API

`GET /dags/{fileName}/run-history/samples`

- The query and the response are the same as for the samples of a continuous step in Spec 083.
- The query has `from`, `to` and `points`. The response has `resolutionSeconds`, `from`, `to` and
  `buckets`.
- A bucket has `time`, `count`, `failures`, `minMs`, `maxMs`, `p50Ms` and `p95Ms`. It also has
  `upMs` when the history knows the active time of the bucket: the time in milliseconds in which a
  run was active. A bucket can have `upMs` and no run.
- The response has `uptime` for the window of the query, with the same rules as for the availability.
  It is absent when the uptime is unknown.

`GET /dags/{fileName}/run-history/events`

- The query is the same as for the events of a continuous step in Spec 083: `from`, `to`, `limit`
  and `before`.
- An event has `time`, `kind` (`failed`, `recovered` or `gap`) and `runId`.
- A `failed` event has `message` when the run gave one, and `failedCount` when it repeats.
- A `recovered` event has `failedCount` and `outageSeconds`.
- A `gap` event has `gapSeconds`.

`GET /dags/{fileName}/run-history/availability`

- The response is the same as for the availability of a continuous step in Spec 083: `windows`,
  `streak`, `lastFailureAt` and `totalCount`.
- A window has `availability`, which is the success rate, and `uptime`. The `uptime` is a number from
  0 to 1. It is absent when the uptime of the window is unknown. The step route of Spec 083 does not
  give `uptime`.

Rules:

- A service DAG that has no sample gets `200`, with no bucket, no event and counts of 0.
- A DAG that is not a service DAG and has no run history gets `404`, with the message
  `DAG <name> has no run history`.
- The response time does not depend on the count of samples of the history.

### Activity in the DAG List

`GET /dag-list-activity` gives the activity in the window that ends now (`window`, in seconds) of
each DAG that a repeated `fileName` names, at most 100, in the order of the request, each once. A
name that names no DAG of `GET /dags` that the caller can see is left out, with no error. `readAt`
is the server time when the activity was read, rounded down to the millisecond. For a service DAG,
the activity holds buckets of the run history instead of runs. A service DAG with no run history
has no bucket.

The `dagrunchanges:all` topic of the stream and `GET /dag-run-changes` give a change feed: `since`,
`readAt` and `changes`. Each change has a `fileName` and a `changedAt`, the newest first, each file
once. The feed lists every change of the state of a root run of a DAG that the caller can see, by
the workspace label of the run, after `since` and up to `readAt`. A list reads the activity again
of the rows whose file has a `changedAt` at or after the `readAt` of their activity, and of the
rows whose activity it read before `since`.

- The topic reads the feed at most once a minute, and at least once a minute. It sends each read,
  since its `readAt` moves, so that a list learns once a minute that its rows are still current.
- The server keeps a change for 3 minutes, and at most 500 files. `since` moves when the server
  starts or resumes reading run events, when its stream of app events resets, and when the feed
  drops a change.
- A server without the event store answers `unsupported_topic` for the topic, and its
  `GET /dag-run-changes` gives a `since` equal to `readAt`.
- `changedAt` and `since` are rounded up, and `readAt` down, to the millisecond.

The activity of a service DAG has:

- `resolutionSeconds`: the width of the tier of the buckets.
- `buckets`: at most 60 buckets, with the fields of a bucket of the samples query, `upMs` included.
  The server chooses the tier as the samples query does with `points` set to 60. A bucket with no
  run and no `upMs` is not in the list. The run in progress is in the buckets, as for the samples
  query.
- `runs`: an empty list, and `truncated`: `false`.
- `uptime`: the uptime of the window of the request, when it is known.

Rules:

- The cost of the activity of a service DAG does not depend on its count of runs.
- The activity of a service DAG does not depend on the status of its latest run. A queued run, or a
  run that has not started, does not empty it.
- A `window` that is missing, or outside 60 to 604800 seconds, gets `400`. No `fileName`, or more
  than 100, gets `400`.
- For a DAG that is not a service DAG, the activity lists the runs that started in the window, the
  last started first, at most 60, and the runs still in progress. It finds them among the runs
  created since one window before the window, at most the 1000 created last, so a run that waited
  in a queue for longer shows only while it is the latest run. When more runs were created, the
  activity is truncated. A run can start after runs created after it, so the order in which
  runs were created does not decide which runs it lists.
- `GET /dags` does not read the activity.

### Compact Count

A count of service runs shows in the compact form of Spec 083, in the Workflows list and in the
Activity panel. The tooltip and the accessible name give the whole count.

### User Interface

#### Activity Panel

For a service DAG, the Activity panel shows the run history:

- the uptime and the success rate of each window, as two rows, the current streak, the last failure
  and the compact count of all the runs. A window with no uptime shows a dash for it.
- a chart of the run history for a time window. The user chooses a window from one hour to three
  years, or zooms in the chart. A bar is a bucket of the tier of the window. The height of a bar
  shows the duration of the runs. A bucket with failed runs shows them in red. A click on a bucket
  lists the events of its window.
- the run in progress, as a band under the bars, from its start to now. It starts at the left end
  of the window when the run started before the window. A click on the band opens the run.
- the list of the events, with the cursor of the API. A `gap` event shows the time with no run.

The panel has no count of runs for a service DAG: its chart always shows a time window. The History
tab lists the retained runs, and the user opens one there as for any other DAG.

Rules:

- The panel opens on the run history with a window of 24 hours. The service DAGs keep their own
  choice of a window. It does not change the count of runs or the window that the other DAGs show,
  and their choice does not change it.
- The History tab of the panel shows the retained runs only: the run history is already above the
  tab.
- A service DAG has no view menu. The History tab lists all its retained runs, whatever another DAG
  hides, and the move to the previous or the next run goes through them, whatever the window of
  the chart.
- The chart of a time window shows the buckets of the run history, not the retained runs.
- The run in progress is the latest run of the DAG while it runs or waits. Its sample is recorded
  in the bucket of its start only when it ends, so no bar shows it while it runs.
- The chart tells that the window has no run only when it has no bar and no run in progress.
- When a time window starts before the oldest retained run, the panel tells that the runs before
  that time are not kept. It gives the time of the oldest retained run.
- The chart draws at most the `points` buckets of the response. It never draws one element for each
  run.
- The panel reads the samples of a one-hour window at most once in 10 seconds. It reads the samples
  of a longer window, the events and the availability at most once in 30 seconds.
- The panel reads the retained runs of a service DAG at most once in 10 seconds. Each run of the DAG
  sends changes, but the panel does not read the runs again for each change.
- While the stream of the DAG is connected, the panel takes the newest runs from the stream. It does
  not read them again for each event.
- The panel reads the retained runs of a service DAG when it opens, when the user changes the window,
  and after an event of the stream, at most once in 30 seconds. Without the stream, it reads them
  each 30 seconds.

#### Workflows List

For a service DAG, the row of the table and the card show:

- a strip of the buckets of the activity, with at most 60 cells. A cell is red or amber when its
  bucket has failed runs: the color shows the share of failed runs. A cell is green when its bucket
  has no failed run and has a run or an `upMs` more than 0. A cell is empty only when its bucket has
  no run and no `upMs`. So a service that did not crash is green on each interval, also where no run
  started, and the run in progress shows.
- the compact count of the runs of the window, and the uptime of the window. When the uptime is
  unknown, the list shows the success rate of the window in its place. The name of the strip gives
  both figures.
- the state of the service in the card, in the chip of the status of the latest run. The Last Run
  column of the table shows the state of the service, not a time. A card with no known state shows
  the status of the latest run.
- a service marker, in the row of the table and in the card.

Rules:

- The row and the card of a service DAG draw no element for each run.
- When the activity of a service DAG has no `buckets`, the list draws no strip and no bar for it.
- The list makes no request of its own for each DAG. The activity of its rows comes in
  `GET /dag-list-activity` requests that name the rows, at most 100 each. After the first read, the
  list reads only the rows that the `dagrunchanges` feed names, or the rows that it read before the
  `since` of the feed. Without the feed, it reads all its rows once a minute. The only exception
  is the detail of a strip that the user opens.
- The list adds the topic in a topic change of its own, so that the list does not wait for the
  activity.
- Until the activity of a row arrives, its strip shows a placeholder. While the activity refreshes,
  the strip keeps the last activity.
- A node that gives no activity, such as an older remote node that answers `404`, draws no strip
  and no placeholder.
- The Last Run column of a service DAG shows the state of the service in the chip of the Runs list:
  Healthy for the states running and restarting, because a service that restarts as designed is
  well. Pending, failing, suspended and stopped keep their names. A DAG that is not a service DAG
  keeps its time. A running run of such a DAG that has a continuous step reads Running, not
  Healthy. Only a service DAG reads Healthy.
- The states come from `GET /service-dags` and from its live topic. The table reads them once for
  all rows. A DAG with no known state shows the time of its last run.
- A row and a card of any workflow show Start, Retry and Stop in fixed places, in that order. While
  the scheduler starts the runs of a service DAG, an "Auto" mark takes the places of Start and Retry:
  while a run is active, and in the states restarting and failing. In the states pending, suspended
  and stopped, Start and Retry stay. So do they while the restarts are on hold (`restartHold`) and
  for a DAG whose state cannot be read (`readError`): the scheduler starts no run.
- The Schedule cell of a service DAG shows its toggle and "Service". A service DAG that is restarting
  or failing also says when the scheduler starts the next run, in words such as "Restart in 4
  minutes". It says nothing of it while the restarts are on hold.
- The list offers no Reject: a run that waits for an approval or a human task is decided on its run.
- The header of the page of a service DAG follows the same rule. It reads the state from the same
  `GET /service-dags` read as the list, with no extra request. A DAG that is restarting or failing,
  with no active run, has no Start button, no Retry button and no menu to run a draft. With no known
  state, the header keeps its buttons.
- The strip opens a detail when the user points at it, focuses it or clicks it. A key of the
  keyboard opens it from the focus, and Escape closes it.
- The detail shows the uptime and the success rate of 7 days, the count of runs of the window, the
  time of the last failure and the longest gap. The count and the gap come from the activity. The two
  figures and the last failure come from the availability query.
- The longest gap is the longest series of cells with no run after the first run, in the width of a
  cell. The `upMs` does not change the count of runs, the success rate or the gap.
- The title of a cell that has no run and has `upMs` gives the time and `Up`. The title of a cell
  with runs gives the time, the count of runs and the count of failures.
- The detail makes one availability request when it opens. A closed strip makes none, and the
  detail does not poll.
- The runs of service DAGs cause at most one update of the DAG list stream in 10 seconds. A change
  of a DAG that is not a service DAG still updates the list at once. The strips update at most once
  in 10 seconds.

#### Runs List

A service DAG can have up to 100 retained runs. The Runs list of the Workflows page does not show
them. It sends `excludeServiceDAGRuns=true`, so the server leaves out every run of a service DAG.
The list shows one row for each service DAG instead. The rows come from `GET /service-dags` and from
its live topic, `servicedags`.

The row of a service DAG shows:

- the name of the DAG with the service marker.
- a strip of at most 60 cells of the run history, with the colors of the Workflows list. The strip
  has the compact count of the runs of the window and the uptime of the window, or the success rate
  when the uptime is unknown. The samples query of the run history gives the uptime of the window.
- the state of the DAG: Healthy (running or restarting), pending, failing, suspended or stopped. A run that the scheduler queued counts as restarting, the first run included, unless the DAG is suspended. Pending means that the DAG is not suspended, and that it has no run or a run the scheduler did not queue waits to start.
  The row shows the restart wait in seconds next to it.
- the time of the last failure of the DAG.
- for the state failing: the count of failed runs in a row, and the error. The error is the
  `readError` of the DAG, or else the error of its latest run. The count shows `10+` at its limit.

Rules:

- The rows stand before the runs. A DAG that is failing goes first. The other DAGs keep the order
  of the server.
- A click on the row opens the page of the DAG, as a click on a row of the Workflows list does.
- The window of the strip is the date filter of the list. A date filter with no start date gives a
  window of three years. A filter with no end date gives a window that ends now.
- The strip counts the run history of the window, not the runs of the list. The run history has more
  runs than the retention keeps.
- A filter on the name, on the run ID, on the status or on the labels shows the runs as they are.
  The list then sends no `excludeServiceDAGRuns` and shows no service DAG row. A filter on the dates
  keeps the rows.
- The head page, its live topic and each next page of the list send the same value of
  `excludeServiceDAGRuns`. The cursor of the list does not change.
- The selection, the keys and the batch actions reach only the runs of the list. A row of a service
  DAG is not a run.
- The list reads the run history of a DAG once for each row, and again each minute. It reads the
  service DAGs once, and the live topic updates them at most every two seconds.
- The strip of a row draws no element for each run.

## Constraints

- The raw tier of a run history uses at most 3 MiB of disk for each 86 400 samples. Each failed
  sample adds 1 KiB at most. Each day older than the raw tier adds a constant that does not
  depend on the count of runs. The events add 4 MiB at most.
- The memory of the scheduler and of the server does not grow with the count of runs.
- A run history query answers in 500 ms at most on the reference machine of the tests. This is
  true for a history of three years at one run each second.
- The bounds of this spec are for one run each second. The loop of Spec 080 is slower: a run starts
  at least `restart_wait_sec` plus one second after the end of the previous run.
- The Activity panel draws at most `points` bars in its chart. The Workflows list draws at most 60
  cells for each service DAG.

## Errors

- A query parameter out of its range, or `from` after `to`, gets `400`.
- A `before` cursor that the server did not give, or that points to a dropped event, gets `400`.
- A request for a DAG that does not exist gets `404`.
- A request for a DAG that is not a service DAG and has no run history gets `404`.
- A run history that the scheduler cannot open or write does not stop the DAG. The scheduler starts
  the next run as usual and records no sample. It logs the error at most once in 30 seconds for each
  DAG.
- A run status that the scheduler cannot read gives no sample. A later read records the run, if it
  is still among the 10 newest runs of the DAG and no run that ended after it has a sample.

## Examples

A service DAG that checks a host each time its previous check ends:

```yaml
restart_policy: always
restart_wait_sec: 1
steps:
  - run: ./check.sh
```

A response of the samples query for the last 24 hours with `points=24`. The DAG ran 22 534 times,
and 3 runs failed in one hour:

```json
{
  "resolutionSeconds": 3600,
  "from": "2026-11-07T00:00:00Z",
  "to": "2026-11-08T00:00:00Z",
  "buckets": [
    {"time": "2026-11-07T00:00:00Z", "count": 941, "failures": 0,
     "minMs": 101, "maxMs": 162, "p50Ms": 118, "p95Ms": 139},
    {"time": "2026-11-07T01:00:00Z", "count": 938, "failures": 3,
     "minMs": 99, "maxMs": 2410, "p50Ms": 119, "p95Ms": 141}
  ]
}
```

The `gap` event of a scheduler that was stopped for 20 minutes:

```json
{"time": "2026-11-07T03:12:04.210Z", "kind": "gap", "runId": "restart-1d990191e433-20261107T031204",
 "gapSeconds": 1200}
```

The entry of a service DAG in `GET /dag-list-activity?fileName=service_ok&window=86400`:

```json
{
  "fileName": "service_ok",
  "activity": {
    "runs": [],
    "truncated": false,
    "resolutionSeconds": 3600,
    "buckets": [
      {"time": "2026-11-07T00:00:00Z", "count": 941, "failures": 0,
       "minMs": 101, "maxMs": 162, "p50Ms": 118, "p95Ms": 139},
      {"time": "2026-11-07T01:00:00Z", "count": 938, "failures": 3,
       "minMs": 99, "maxMs": 2410, "p50Ms": 119, "p95Ms": 141}
    ]
  }
}
```

The change feed of `GET /dag-run-changes` after a run of `plain` ended:

```json
{
  "since": "2026-11-07T01:58:12.041Z",
  "readAt": "2026-11-07T02:00:03.517Z",
  "changes": [{"fileName": "plain", "changedAt": "2026-11-07T01:59:47.209Z"}]
}
```

The fixtures in `conformance/spec100_service_run_history/testdata` give a service DAG that always
succeeds, and a service DAG whose runs fail when a file exists.

## Test Scenarios

- Each run of a service DAG with `restart_wait_sec: 1` has one sample. The `totalCount` and the sum
  of the counts of the buckets are equal to the count of the runs that ended.
- A failed run records a failed sample and a `failed` event with its run ID. The next run that does
  not fail records a `recovered` event.
- A stop of the scheduler for more than `restart_wait_sec` plus 60 seconds records a `gap` event at
  the next run.
- With `hist_retention_runs: 1`, the run history has one sample for each run.
- A run that a user starts while the scheduler is stopped has a sample after the scheduler starts
  again.
- A run of three hours gives its time to the buckets that it covers, in each tier. A run across
  midnight gives its time to two days. Two runs and a gap that is longer than the restart gap give
  an uptime below 1.
- A service DAG with `restart_wait_sec: 1` and a step that ends at once has an uptime of 0.9 or
  more after a full minute. A restart gap is active.
- A stop of the scheduler for 20 seconds, with `restart_wait_sec: 1`, is not active time. The
  uptime is below 1.
- A gap that is equal to the restart gap is active. A gap that is 1 second longer is not active.
  The first run of a history has no gap before it.
- The run in progress counts in the uptime at the time of the query. A service DAG whose step lasts
  longer than the restart wait has an uptime near 1 while a run is in progress.
- A run history that has no `up` directory gives no uptime, and its other figures do not change.
- The samples query and the activity of the DAG list give `upMs`. A bucket of a long run that has no
  sample has `count` 0 and `upMs` more than 0. The run in progress has `upMs` in the buckets that it
  covers, and no sample.
- The success rate does not change: a killed run is one failure.
- A DAG that is not a service DAG and has no run history gets `404` from the three queries.
- The activity of the DAG list gives at most 60 buckets for a service DAG, and runs for the other
  DAGs, for the DAGs that the request names, in its order.
- The response of the samples query for a window of three years has at most `points` buckets.
- The events of a run history stay under 4 MiB after 100 000 runs that fail one time in two.
- A service run that a worker executes has a sample.

## Conformance

`conformance/spec100_service_run_history/` checks the samples, the events and the retention.
It also checks the gap after a stop of the scheduler, and the three queries and their
errors. It checks `upMs` in the buckets, and the live part of the run in progress. It checks a window of three years and the activity of a service DAG that a request names. Each
test starts a scheduler and a server with static fixtures.
