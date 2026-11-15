----------------------------- MODULE MissedGap -----------------------------
(***************************************************************************)
(* The scheduler's missed gap for one DAG with one "* * * * *" start      *)
(* schedule: holds while the DAG is suspended, scheduler outages, startup  *)
(* catch-up, and backfills that take slots from the gap or the hold.      *)
(*                                                                         *)
(* It follows, in internal/service/scheduler:                              *)
(*   missed_gap.go    releaseDAG, settleRestoredHold, recordDownGap,      *)
(*                    recordClampedGap, recordGap, coverGap, coverMissed,  *)
(*                    coverHold, lastCovered, gapStart, gapEnd, countSlots *)
(*   tick_planner.go  Init, initBuffers, Plan, dropSuspendedCatchupState,  *)
(*                    Advance                                              *)
(*   scheduler.go     runTick: a tick plans, advances, and flushes the     *)
(*                    watermarks before the next one                       *)
(*                                                                         *)
(* The rules, from specs/086-backfill.md:                                  *)
(*   GapHasNoRuns   no slot the gap counts has a run or a queued catch-up. *)
(*   GapCountExact  the gap's count is the number of slots it spans, up to *)
(*                  the cap.                                               *)
(*   HoldGapComplete a hold's gap counts every slot of its span that no   *)
(*                  tick decided, no run took, and no catch-up holds.      *)
(*                                                                         *)
(* Each Mutation other than "none" puts back one bug that was fixed, so a  *)
(* check of it shows the rules catch that bug.                             *)
(***************************************************************************)
EXTENDS Integers, Sequences, FiniteSets, TLC

CONSTANTS
    Minutes,       \* the clock runs from minute 0 to minute Minutes
    Window,        \* catchup_window in minutes; 0 is a DAG without catch-up
    Cap,           \* stands in for schedulerstate.MaxGapSlots (10,000)
    MaxToggles,    \* bound on suspend and resume
    MaxRestarts,   \* bound on scheduler crashes
    MaxBackfills,  \* bound on backfills
    Mutation       \* "none", or the bug to put back

Mutations == {
    "none",
    \* before a79c6dddd7: a hold that ended during an outage ended at the
    \* first tick after it and counted the slots catch-up replays.
    "restored-hold-at-first-tick",
    \* 5734c3c722: a hold ended at a tick counted its queued catch-up.
    "hold-ignores-queued-catchup",
    \* 565b6f15a5: a shortened gap was walked and recounted to To.
    "recount-through-to",
    \* #3952: a restored hold counted the slots of the last tick's minute,
    \* which the first tick decides again after a restart within it.
    "repeated-tick-keeps-gap",
    \* a first try at that fix: Init ended a restored hold before the minute
    \* its clock read, which is not the first tick's when startup runs into
    \* the next minute.
    "settle-assumes-first-tick",
    \* #3952: a backfill picked up during a hold left the hold as it was.
    "backfill-skips-hold",
    \* #3952: the first tick after a restart within the last tick's minute
    \* held the DAG from that minute's slot, which had run.
    "rehold-last-tick-minute"
}
ASSUME Mutation \in Mutations

(* An instant is an integer. Minute m starts at 4*m, 4*m + 2 is the middle *)
(* of minute m, and x - 1 is one second before x.                         *)
Sec == 1
Zero == -1000                         \* time.Time{}
MinuteOf(x) == x - (x % 4)            \* Truncate(time.Minute), for x >= 0
MaxClock == 4 * Minutes + 2
SlotTimes == {4 * m : m \in 0..Minutes}

Max2(a, b) == IF a >= b THEN a ELSE b
Min2(a, b) == IF a <= b THEN a ELSE b
MinOf(S) == CHOOSE x \in S : \A y \in S : x <= y
MaxOf(S) == CHOOSE x \in S : \A y \in S : x >= y

RECURSIVE SortSet(_)
SortSet(S) == IF S = {} THEN <<>> ELSE <<MinOf(S)>> \o SortSet(S \ {MinOf(S)})

Range(s) == {s[i] : i \in DOMAIN s}

(* countSlots: the fire times after from and by to, up to the cap. *)
FiresIn(from, to) == {s \in SlotTimes : s > from /\ s <= to}
CountSlots(from, to) == Min2(Cap, Cardinality(FiresIn(from, to)))

NoGap == [valid |-> FALSE, from |-> Zero, to |-> Zero, through |-> Zero,
          reason |-> "none", slots |-> 0]
Gap(from, to, through, reason, n) ==
    [valid |-> TRUE, from |-> from, to |-> to, through |-> through,
     reason |-> reason, slots |-> n]

(* gapStart: a hold's gap holds the slot of the tick that found the DAG   *)
(* held, while an outage's starts after the last tick.                    *)
GapStart(g) == IF g.reason = "down" THEN g.from ELSE g.from - Sec

(* The slots a gap counts, through the last instant it counts. *)
Counted(g) == IF g.valid THEN FiresIn(GapStart(g), g.through) ELSE {}

VARIABLES
    clock,       \* the wall clock
    phase,       \* "down", "initA", "initB", or "up"
    nextTick,    \* the tick time the scheduler runs next
    susp,        \* the DAG is suspended
    heldSince,   \* DAGWatermark.HeldSince
    missed,      \* DAGWatermark.Missed
    buf,         \* the catch-up buffer, kept in memory only
    lastTick,    \* State.LastTick
    lastSched,   \* DAGWatermark.LastScheduledTime
    lastSlot,    \* DAGWatermark.LastSlot, the latest slot dispatched
    starting,    \* 1 before the startup's first tick, 2 when it runs late
    handled,     \* slots that ran, or that a backfill took
    decided,     \* slots of minutes a tick found the DAG not held in
    abandoned,   \* catch-up slots a suspension or a crash dropped
    toggles, restarts, backfills

vars == <<clock, phase, nextTick, susp, heldSince, missed, buf, lastTick,
          lastSched, lastSlot, starting, handled, decided, abandoned,
          toggles, restarts, backfills>>

(* The slots a hold under way has held, before the current minute. *)
HeldSlots == IF heldSince = Zero THEN {} ELSE FiresIn(heldSince - Sec, MinuteOf(clock) - Sec)

(* releaseDAG(to, through) *)
Release(hs, m, to, through) ==
    IF hs = Zero THEN [held |-> hs, gap |-> m]
    ELSE LET n == CountSlots(hs - Sec, through)
         IN [held |-> Zero,
             gap  |-> IF n > 0 THEN Gap(hs, to, through, "hold", n) ELSE m]

(* endGapBeforeTick(limit) *)
EndGapBefore(g, limit) ==
    IF ~g.valid \/ g.through <= limit THEN g
    ELSE LET n == CountSlots(GapStart(g), limit)
         IN IF n > 0 THEN [g EXCEPT !.through = limit, !.slots = n] ELSE NoGap

(* recordGap(from, to, through), with s the suspension it reads *)
RecordGap(m, s, from, to, through) ==
    IF from = Zero \/ ~(to > from) THEN m
    ELSE LET n == CountSlots(from, through)
         IN IF n = 0 \/ s THEN m ELSE Gap(from, to, through, "down", n)

(* The longest run of fire times after from and by to that the backfill's *)
(* slots S hold, walked oldest first: lastCovered.                        *)
CoveredRun(from, to, S) ==
    LET f == SortSet(FiresIn(from, to))
        k == CHOOSE k \in 0..Len(f) :
                /\ \A j \in 1..k : f[j] \in S
                /\ (k = Len(f) \/ f[k + 1] \notin S)
    IN [count |-> k, last |-> IF k = 0 THEN Zero ELSE f[k]]

(* coverMissed *)
CoverMissedNow(g, S) ==
    LET start == GapStart(g)
        end   == g.through                                    \* gapEnd
    IN IF ~(MaxOf(S) > start) \/ MinOf(S) > end THEN g
       ELSE LET last == CoveredRun(start, end, S).last
            IN IF last = Zero THEN g
               ELSE LET g2  == [g EXCEPT !.from = IF g.reason = "down" THEN last ELSE last + Sec]
                        rem == CountSlots(GapStart(g2), end)
                    IN IF rem > 0 THEN [g2 EXCEPT !.slots = rem] ELSE NoGap

(* coverMissed before 565b6f15a5: walked and recounted the gap to To. *)
CoverMissedToTo(g, S) ==
    LET start == GapStart(g)
    IN IF ~(MaxOf(S) > start) \/ MinOf(S) > g.to THEN g
       ELSE LET run == CoveredRun(start, g.to, S)
            IN IF run.count = 0 THEN g
               ELSE LET g2  == [g EXCEPT !.from = IF g.reason = "down" THEN run.last ELSE run.last + Sec]
                        rem == IF g.slots >= Cap
                               THEN CountSlots(GapStart(g2), g.to)
                               ELSE g.slots - run.count
                    IN IF rem > 0 THEN [g2 EXCEPT !.slots = rem] ELSE NoGap

CoverMissed(g, S) ==
    IF ~g.valid THEN g
    ELSE IF Mutation = "recount-through-to" THEN CoverMissedToTo(g, S)
    ELSE CoverMissedNow(g, S)

(* coverHold: the hold starts the second after its leading backfilled slots. *)
CoverHold(hs, S) ==
    IF hs = Zero \/ Mutation = "backfill-skips-hold" THEN hs
    ELSE LET last == CoveredRun(hs - Sec, MaxOf(S), S).last
         IN IF last = Zero THEN hs ELSE last + Sec

(* A backfill whose range starts inside a gap or a hold leaves it as it is *)
(* by design (spec 086), so the model leaves such ranges out.             *)
StartsInside(a, S) == S /= {} /\ a > MinOf(S) /\ a <= MaxOf(S)

-----------------------------------------------------------------------------

Init ==
    /\ clock = 0
    /\ phase = "up"
    /\ nextTick = 0
    /\ susp = FALSE
    /\ heldSince = Zero
    /\ missed = NoGap
    /\ buf = <<>>
    /\ lastTick = Zero
    /\ lastSched = Zero
    /\ lastSlot = Zero
    /\ starting = 0
    /\ handled = {}
    /\ decided = {}
    /\ abandoned = {}
    /\ toggles = 0 /\ restarts = 0 /\ backfills = 0

(* TickPlanner.Init up to initBuffers, with no failover recovery window. *)
StartupSettle ==
    /\ phase = "initA"
    /\ LET firstTick == MinuteOf(clock)
           through   == IF Mutation = "settle-assumes-first-tick"
                        THEN Min2(lastTick, firstTick - Sec) ELSE lastTick
           settled   == IF Mutation /= "restored-hold-at-first-tick" /\ heldSince /= Zero
                           /\ lastTick /= Zero /\ ~susp
                        THEN Release(heldSince, missed, lastTick, through)
                        ELSE [held |-> heldSince, gap |-> missed]
           \* recordDownGap leaves a DAG with catch-up to initBuffers.
           gap == IF Window > 0 THEN settled.gap
                  ELSE RecordGap(settled.gap, susp, lastTick, firstTick, firstTick - Sec)
       IN /\ heldSince' = settled.held
          /\ missed' = gap
    /\ phase' = "initB"
    /\ UNCHANGED <<clock, nextTick, susp, buf, lastTick, lastSched, lastSlot,
                   starting, handled, decided, abandoned, toggles, restarts,
                   backfills>>

(* initBuffers, then the first tick for the current minute. *)
StartupBuffers ==
    /\ phase = "initB"
    /\ IF Window > 0
       THEN LET rf   == Max2(clock - 4 * Window, Max2(lastTick, lastSched)) \* ComputeReplayFrom
                from == Max2(lastTick, lastSched)                          \* recordClampedGap
            IN /\ missed' = IF rf > from THEN RecordGap(missed, susp, from, rf, rf) ELSE missed
               /\ buf' = SortSet(FiresIn(rf, clock))
       ELSE UNCHANGED <<missed, buf>>
    /\ phase' = "up"
    /\ nextTick' = MinuteOf(clock)
    /\ starting' = 1
    /\ UNCHANGED <<clock, susp, heldSince, lastTick, lastSched, lastSlot,
                   handled, decided, abandoned, toggles, restarts, backfills>>

(* Plan(T), then Advance(T) and Flush. Catch-up runs before live slots.   *)
(* Runs finish before the next tick, so no overlap policy applies.        *)
Tick ==
    /\ phase = "up"
    /\ nextTick <= clock
    /\ LET T == nextTick
           \* The first tick after a restart within the last tick's minute.
           repeated == lastTick /= Zero /\ T <= lastTick
       IN
       /\ IF susp
          THEN \* holdDAG, which leaves out a slot this minute's last tick
               \* ran, and dropSuspendedCatchupState
               /\ heldSince' = IF heldSince /= Zero THEN heldSince
                               ELSE IF Mutation /= "rehold-last-tick-minute" /\ lastSlot >= T
                               THEN lastSlot + Sec ELSE T
               /\ buf' = <<>>
               /\ abandoned' = abandoned \cup Range(buf)
               /\ lastSched' = IF Len(buf) > 0 \/ Window > 0 THEN T ELSE lastSched
               /\ UNCHANGED <<missed, handled, lastSlot, decided>>
          ELSE LET through0 == MinuteOf(T) - Sec
                   clamp    == Mutation \notin {"restored-hold-at-first-tick", "hold-ignores-queued-catchup"}
                   through  == IF clamp /\ Len(buf) > 0 /\ Head(buf) - Sec < through0
                               THEN Head(buf) - Sec ELSE through0
                   rel      == Release(heldSince, missed, T, through)
                   trim     == repeated /\ Mutation \notin {"repeated-tick-keeps-gap", "settle-assumes-first-tick"}
               IN /\ heldSince' = rel.held
                  /\ missed' = IF trim THEN EndGapBefore(rel.gap, MinuteOf(T) - Sec) ELSE rel.gap
                  /\ decided' = IF T \in SlotTimes THEN decided \cup {T} ELSE decided
                  /\ UNCHANGED abandoned
                  /\ IF Len(buf) > 0
                     THEN /\ handled' = handled \cup {Head(buf)}
                          /\ lastSched' = Head(buf)
                          /\ lastSlot' = Max2(lastSlot, Head(buf))
                          /\ buf' = Tail(buf)
                     ELSE /\ handled' = IF T \in SlotTimes THEN handled \cup {T} ELSE handled
                          /\ lastSched' = IF T \in SlotTimes THEN T ELSE lastSched
                          /\ lastSlot' = IF T \in SlotTimes THEN Max2(lastSlot, T) ELSE lastSlot
                          /\ UNCHANGED buf
       /\ lastTick' = T
       /\ nextTick' = T + 4
       /\ starting' = 0
    /\ UNCHANGED <<clock, phase, susp, toggles, restarts, backfills>>

(* The scheduler dies. The watermarks were flushed, and the catch-up      *)
(* buffer is lost. The next startup replays only slots after the last     *)
(* tick, so a queued slot a tick passed is dropped, as catch-up does.      *)
Crash ==
    /\ phase = "up"
    /\ restarts < MaxRestarts
    /\ phase' = "down"
    /\ buf' = <<>>
    /\ abandoned' = abandoned \cup Range(buf)
    /\ restarts' = restarts + 1
    /\ UNCHANGED <<clock, nextTick, susp, heldSince, missed, lastTick, lastSched,
                   lastSlot, starting, handled, decided, toggles, backfills>>

Restart ==
    /\ phase = "down"
    /\ phase' = "initA"
    /\ UNCHANGED <<clock, nextTick, susp, heldSince, missed, buf, lastTick,
                   lastSched, lastSlot, starting, handled, decided, abandoned,
                   toggles, restarts, backfills>>

(* A running scheduler ticks before the clock leaves the tick's minute. *)
Clock ==
    /\ clock < MaxClock
    /\ phase \in {"down", "up"}
    /\ ~(phase = "up" /\ nextTick <= clock)
    /\ clock' = clock + 2
    /\ UNCHANGED <<phase, nextTick, susp, heldSince, missed, buf, lastTick,
                   lastSched, lastSlot, starting, handled, decided, abandoned,
                   toggles, restarts, backfills>>

(* Startup runs into the next minute before cronLoop, which starts after  *)
(* Init returns, reads the clock for its first tick. A startup is taken   *)
(* to finish within a minute.                                             *)
StartLate ==
    /\ phase = "up"
    /\ starting = 1
    /\ MinuteOf(clock) + 4 <= MaxClock
    /\ clock' = MinuteOf(clock) + 4
    /\ nextTick' = MinuteOf(clock) + 4
    /\ starting' = 2
    /\ UNCHANGED <<phase, susp, heldSince, missed, buf, lastTick, lastSched,
                   lastSlot, handled, decided, abandoned, toggles, restarts,
                   backfills>>

(* An operator suspends or resumes the DAG at any moment, also mid-startup. *)
Toggle ==
    /\ toggles < MaxToggles
    /\ susp' = ~susp
    /\ toggles' = toggles + 1
    /\ UNCHANGED <<clock, phase, nextTick, heldSince, missed, buf, lastTick,
                   lastSched, lastSlot, starting, handled, decided, abandoned,
                   restarts, backfills>>

(* The scheduler picks up a backfill of the range [a, b]: the backfill    *)
(* takes its slots, then coverGap. --rerun all runs every slot; otherwise *)
(* slots that ran are skipped. A backfill is picked up only after the     *)
(* startup's first tick, which runs right after Init.                     *)
Backfill ==
    /\ phase = "up"
    /\ nextTick > clock
    /\ backfills < MaxBackfills
    /\ missed.valid \/ heldSince /= Zero
    /\ \E a \in SlotTimes, b \in SlotTimes, rerunAll \in BOOLEAN :
         /\ a <= b
         /\ b < MinuteOf(clock)    \* fires of the current minute are left to live
         /\ ~StartsInside(a, Counted(missed))
         /\ ~StartsInside(a, HeldSlots)
         /\ LET rng   == {s \in SlotTimes : a <= s /\ s <= b}
                slots == IF rerunAll THEN rng ELSE rng \ handled
            IN /\ slots /= {}
               /\ handled' = handled \cup slots
               /\ missed' = CoverMissed(missed, slots)
               /\ heldSince' = CoverHold(heldSince, slots)
    /\ backfills' = backfills + 1
    /\ UNCHANGED <<clock, phase, nextTick, susp, buf, lastTick, lastSched,
                   lastSlot, starting, decided, abandoned, toggles, restarts>>

Next == Tick \/ Crash \/ Restart \/ StartupSettle \/ StartupBuffers \/ Clock
        \/ StartLate \/ Toggle \/ Backfill

Spec == Init /\ [][Next]_vars

-----------------------------------------------------------------------------

GapHasNoRuns == Counted(missed) \cap (handled \cup Range(buf)) = {}

GapCountExact == missed.valid => missed.slots = Min2(Cap, Cardinality(Counted(missed)))

(* A hold's span runs from its first slot to the tick that ended it, whose *)
(* own slots that tick decided, or to the last tick for a hold settled at  *)
(* startup.                                                               *)
HoldSpan(g) == {s \in SlotTimes : s > GapStart(g) /\ s <= g.to}

HoldGapComplete ==
    missed.valid /\ missed.reason = "hold" =>
        HoldSpan(missed) \ (handled \cup decided \cup abandoned \cup Range(buf))
            \subseteq Counted(missed)

=============================================================================
