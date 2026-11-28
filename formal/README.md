# Formal models

TLA+ models of scheduler behavior that unit tests rarely reach: orderings of
ticks, restarts, suspensions, catch-up, and backfills. TLC checks every
ordering within a model's bounds, so a model finds the case nobody thought to
write a test for.

## Run

```sh
make formal                     # every check, about two minutes
formal/check.sh missedgap       # one check
```

`formal/check.sh` needs Java 11 or later. It downloads `tla2tools.jar` once,
pinned by version and checksum, into `.local/formal`. CI runs the checks when a
model or the code it covers changes.

## Checks

Each model holds the invariants its `.cfg` names. Each `Mutation` of a model
puts back one bug that was fixed, and its check must fail on the invariant
named for it in `check.sh`. A passing model whose mutations also pass proves
nothing, so a mutation that stops failing is itself a failure.

## missedgap

`missedgap/MissedGap.tla` models a DAG's missed gap (`DAGWatermark.Missed`) and
hold (`HeldSince`) for one DAG with a `* * * * *` schedule, through ticks,
crashes, including one between planning a slot and starting its run, restarts,
startups that run into the next minute, suspend and resume at any moment,
catch-up, and backfills.

It covers these functions in `internal/service/scheduler`:

- `missed_gap.go`: `holdDAG`, `releaseDAG`, `settleRestoredHold`,
  `endGapBeforeTick`, `recordDownGap`, `recordClampedGap`, `recordGap`,
  `coverGap`, `coverMissed`, `coverHold`, `lastCovered`
- `tick_planner.go`: `Init`, `initBuffers`, `Plan`, `resume`,
  `dropSuspendedCatchupState`, `noteCatchupThrough`, `Advance`
- `catchup.go`: `ComputeReplayFrom`, `pendingCatchup`
- `scheduler.go`: `cronLoop`, `runTick`

The model follows that code as of #3952, #3955, and #3956.

Its invariants, from `specs/086-backfill.md`:

- `GapHasNoRuns`: no slot the gap counts has a run or a queued catch-up.
- `GapCountExact`: the gap's count is the number of slots it spans, up to the
  cap.
- `HoldGapComplete`: when a hold ends, it counts every slot of its span that no
  tick decided, no run took, no catch-up holds, and no other gap counts or
  counted, also when it counts none and so makes no gap.

And from `specs/085-scheduler-ha.md`:

- `CatchupKept`: catch-up a scheduler queued stays queued until it runs,
  across a restart, unless a suspension drops it.
- `LateTickKept`: a first tick later than the minute `Init` read keeps the
  slots between: run, queued, or counted.

It leaves out the failover recovery window, overlap policies, schedule edits,
late ticks other than the first, and backfills whose range starts inside a gap
or a hold, which leave the gap as it is by design. It caps a gap at 2 slots in
place of 10,000. Catch-up a stopped scheduler queued that is before the window
when the next one starts is dropped uncounted, which `CatchupKept` allows.

## Change covered code

1. Change the model to match the code, in the same pull request.
2. Run `make formal`.
3. On a failure, read the trace. Each state names the action that led to it.
   Minute `m` starts at instant `4*m`, and `4*m - 1` is the second before it.
4. Decide whether the code or the model is wrong. Fix that one. Do not weaken
   an invariant to make a check pass.
5. When the change fixes a bug the model can express, add a `Mutation` that
   puts it back and a row in `check.sh` with the invariant it breaks.
6. Update "as of" above.
