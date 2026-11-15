# Formal models

TLA+ models of scheduler behavior that unit tests rarely reach: orderings of
ticks, restarts, suspensions, catch-up, and backfills. TLC checks every
ordering within a model's bounds, so a model finds the case nobody thought to
write a test for.

## Run

```sh
make formal                     # every check, about 30 seconds
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
hold (`HeldSince`) for one DAG with a `* * * * *` schedule.

It covers these functions in `internal/service/scheduler`:

- `missed_gap.go`: `holdDAG`, `releaseDAG`, `settleRestoredHold`,
  `endGapBeforeTick`, `recordDownGap`, `recordClampedGap`, `recordGap`,
  `coverGap`, `coverMissed`, `coverHold`, `lastCovered`
- `tick_planner.go`: `Init`, `initBuffers`, `Plan`,
  `dropSuspendedCatchupState`, `Advance`
- `scheduler.go`: `cronLoop`, `runTick`

The model follows that code as of #3952.

Its invariants, from `specs/086-backfill.md`:

- `GapHasNoRuns`: no slot the gap counts has a run or a queued catch-up.
- `GapCountExact`: the gap's count is the number of slots it spans, up to the
  cap.
- `HoldGapComplete`: a hold's gap counts every slot of its span that no tick
  decided, no run took, and no catch-up holds.

It leaves out the failover recovery window, overlap policies, schedule edits,
and backfills whose range starts inside a gap or a hold, which leave the gap as
it is by design. It caps a gap at 2 slots in place of 10,000, and takes a
startup to finish within a minute.

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
