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
(*   tick_planner.go  Init, initBuffers, Plan, resume,                     *)
(*                    dropSuspendedCatchupState, Advance                   *)
(*   catchup.go       ComputeReplayFrom, pendingCatchup                    *)
(*   scheduler.go     runTick: a tick plans, advances, and flushes the     *)
(*                    watermarks before the next one                       *)
(*                                                                         *)
(* The rules, from specs/086-backfill.md:                                  *)
(*   GapHasNoRuns   no slot the gap counts has a run or a queued catch-up. *)
(*   GapCountExact  the gap's count is the number of slots it spans, up to *)
(*                  the cap.                                               *)
(*   HoldGapComplete a hold that ends counts every slot of its span that  *)
(*                  no tick decided, no run took, and no catch-up holds.   *)
(* And from specs/085-scheduler-ha.md:                                     *)
(*   CatchupKept    catch-up a scheduler queued stays queued until it runs, *)
(*                  across a restart, unless a suspension drops it.        *)
(*   LateTickKept   a first tick later than the minute Init read keeps the *)
(*                  slots between: run, queued, or counted.                *)
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
    "rehold-last-tick-minute",
    \* the same tick taking a slot the last tick only planned as settled,
    \* though no run of it started.
    "rehold-skips-planned",
    \* #3955: startup replayed from the last tick, which the ticks of a
    \* catch-up under way had run past.
    "catchup-lost-on-stop",
    \* #3956: a late first tick planned its own minute only.
    "late-first-tick-skips",
    \* #3955: a late tick counted as missed the catch-up it had queued
    \* before its window.
    "late-tick-gap-over-queue"
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

(* The latest hold that ended: the tick that started it, the instant it   *)
(* ended at, and the slots its release counted, which may be none.        *)
NoRelease == [valid |-> FALSE, start |-> Zero, to |-> Zero, counted |-> {}]

(* gapStart: a hold's gap holds the slot of the tick that found the DAG   *)
(* held, while an outage's starts after the last tick.                    *)
GapStart(g) == IF g.reason = "down" THEN g.from ELSE g.from - Sec

(* The slots a gap counts, through the last instant it counts. *)
Counted(g) == IF g.valid THEN FiresIn(GapStart(g), g.through) ELSE {}

VARIABLES
    clock,          \* the wall clock
    phase,          \* "down", "initA", "initB", or "up"
    nextTick,       \* the tick time the scheduler runs next
    starting,       \* 1 before the startup's first tick, 2 when it runs late
    susp,           \* the DAG is suspended
    heldSince,      \* DAGWatermark.HeldSince
    missed,         \* DAGWatermark.Missed
    lastTick,       \* State.LastTick
    lastSched,      \* DAGWatermark.LastScheduledTime
    lastSlot,       \* DAGWatermark.LastSlot.At
    lastPlanned,    \* DAGWatermark.LastSlot.Outcome is planned
    catchupThrough, \* DAGWatermark.CatchupThrough
    buf,            \* the catch-up buffer, kept in memory only
    holdStart,      \* ghost: the tick that started the hold under way
    released,       \* ghost: the latest hold that ended
    handled,        \* ghost: slots that ran, or that a backfill took
    decided,        \* ghost: slots of the latest tick of their minute, which
                    \* found the DAG not held
    abandoned,      \* ghost: catch-up a suspension dropped, or that was before
                    \* the window when the next scheduler started
    owed,           \* ghost: catch-up slots queued and not run
    superseded,     \* ghost: slots a gap counted before a newer one replaced it
    initClock,      \* ghost: the clock Init read
    lateLost,       \* ghost: slots a late first tick left out
    toggles, restarts, backfills

scheduler == <<clock, phase, nextTick, starting>>
watermark == <<heldSince, missed, lastTick, lastSched, lastSlot, lastPlanned, catchupThrough>>
ghosts    == <<holdStart, released, handled, decided, abandoned, owed, superseded,
              initClock, lateLost>>
bounds    == <<toggles, restarts, backfills>>
vars      == <<scheduler, susp, watermark, buf, ghosts, bounds>>

(* The slots a hold under way has held, before the current minute. *)
HeldSlots == IF heldSince = Zero THEN {} ELSE FiresIn(heldSince - Sec, MinuteOf(clock) - Sec)

(* releaseDAG(to, through) *)
Release(hs, m, to, through) ==
    IF hs = Zero THEN [held |-> hs, gap |-> m, released |-> released]
    ELSE LET n == CountSlots(hs - Sec, through)
         IN [held     |-> Zero,
             gap      |-> IF n > 0 THEN Gap(hs, to, through, "hold", n) ELSE m,
             released |-> [valid |-> TRUE, start |-> holdStart, to |-> to,
                           counted |-> FiresIn(hs - Sec, through)]]

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

(* A gap keeps the slots it no longer counts, which a newer gap replaced. *)
Supersede == superseded' = superseded \cup (Counted(missed) \ Counted(missed'))

(* The latest slot a buffer holds, or Zero. *)
LastOf(b) == IF Len(b) = 0 THEN Zero ELSE b[Len(b)]

-----------------------------------------------------------------------------

Init ==
    /\ clock = 0
    /\ phase = "up"
    /\ nextTick = 0
    /\ starting = 0
    /\ susp = FALSE
    /\ heldSince = Zero
    /\ missed = NoGap
    /\ lastTick = Zero
    /\ lastSched = Zero
    /\ lastSlot = Zero
    /\ lastPlanned = FALSE
    /\ catchupThrough = Zero
    /\ buf = <<>>
    /\ holdStart = Zero
    /\ released = NoRelease
    /\ handled = {}
    /\ decided = {}
    /\ abandoned = {}
    /\ owed = {}
    /\ superseded = {}
    /\ initClock = 0
    /\ lateLost = {}
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
                        ELSE [held |-> heldSince, gap |-> missed, released |-> released]
           \* recordDownGap leaves a DAG with catch-up to initBuffers.
           gap == IF Window > 0 THEN settled.gap
                  ELSE RecordGap(settled.gap, susp, lastTick, firstTick, firstTick - Sec)
       IN /\ heldSince' = settled.held
          /\ holdStart' = IF settled.held = Zero THEN Zero ELSE holdStart
          /\ released' = settled.released
          /\ missed' = gap
    /\ Supersede
    /\ phase' = "initB"
    /\ UNCHANGED <<clock, nextTick, starting, susp, lastTick, lastSched, lastSlot,
                   lastPlanned, catchupThrough, buf, handled, decided, abandoned,
                   owed, initClock, lateLost, bounds>>

(* initBuffers: replay the window, and the catch-up the last scheduler    *)
(* queued and did not run (pendingCatchup).                               *)
StartupBuffers ==
    /\ phase = "initB"
    /\ IF Window > 0
       THEN LET earliest == clock - 4 * Window
                rf       == Max2(earliest, Max2(lastTick, lastSched))      \* ComputeReplayFrom
                from     == Max2(lastTick, lastSched)                      \* recordClampedGap
                pending  == IF Mutation = "catchup-lost-on-stop" THEN {}
                            ELSE FiresIn(Max2(lastSched, earliest), Min2(catchupThrough, rf))
                queued   == pending \cup FiresIn(rf, clock)
                \* Pending catch-up before the window is dropped uncounted.
                lapsed   == {s \in owed : s <= earliest}
            IN /\ missed' = IF rf > from THEN RecordGap(missed, susp, from, rf, rf) ELSE missed
               /\ buf' = SortSet(queued)
               /\ catchupThrough' = IF queued = {} THEN catchupThrough
                                    ELSE Max2(catchupThrough, MaxOf(queued))
               /\ owed' = (owed \ lapsed) \cup queued
               /\ abandoned' = abandoned \cup lapsed
       ELSE UNCHANGED <<missed, buf, catchupThrough, owed, abandoned>>
    /\ phase' = "up"
    /\ nextTick' = MinuteOf(clock)
    /\ starting' = 1
    /\ initClock' = clock
    /\ Supersede
    /\ UNCHANGED <<clock, susp, heldSince, lastTick, lastSched, lastSlot, lastPlanned,
                   holdStart, released, handled, decided, lateLost, bounds>>

(* Startup runs past the minute Init read before cronLoop, which starts    *)
(* after Init returns, reads the clock for its first tick.                *)
StartLate ==
    /\ phase = "up"
    /\ starting > 0
    /\ MinuteOf(clock) + 4 <= MaxClock
    /\ clock' = MinuteOf(clock) + 4
    /\ nextTick' = MinuteOf(clock) + 4
    /\ starting' = 2
    /\ UNCHANGED <<phase, susp, watermark, buf, ghosts, bounds>>

(* resume(T): what a late tick missed. A DAG without catch-up counts it in *)
(* its gap; one with catch-up queues it after what it has queued.          *)
Resume(T) ==
    \* A first scheduler follows no last tick: its slots start with the
    \* minute Init read.
    LET last == IF lastTick = Zero THEN MinuteOf(initClock) - Sec ELSE lastTick IN
    IF Window = 0
    THEN [gap |-> RecordGap(missed, susp, last, T, T - Sec), buf |-> buf]
    ELSE LET rf   == Max2(T - 4 * Window, Max2(last, lastSched))
             from == IF Mutation = "late-tick-gap-over-queue" THEN Max2(last, lastSched)
                     ELSE Max2(Max2(last, lastSched), LastOf(buf))
             gap  == IF rf > from THEN RecordGap(missed, susp, from, rf, rf) ELSE missed
             new  == FiresIn(Max2(rf, LastOf(buf)), T)
         IN [gap |-> gap, buf |-> buf \o SortSet(new)]

(* The first tick after a restart within the last tick's minute. *)
Repeated(T) == lastTick /= Zero /\ T <= lastTick

(* Plan(T) for a DAG not held: releaseDAG, then endGapBeforeTick. *)
PlanGap(T, b, m) ==
    LET through0 == MinuteOf(T) - Sec
        clamp    == Mutation \notin {"restored-hold-at-first-tick", "hold-ignores-queued-catchup"}
        through  == IF clamp /\ Len(b) > 0 /\ Head(b) - Sec < through0
                    THEN Head(b) - Sec ELSE through0
        rel      == Release(heldSince, m, T, through)
        trim     == Repeated(T) /\ Mutation \notin {"repeated-tick-keeps-gap", "settle-assumes-first-tick"}
    IN [held     |-> rel.held,
        released |-> rel.released,
        gap      |-> IF trim THEN EndGapBefore(rel.gap, MinuteOf(T) - Sec) ELSE rel.gap]

(* holdDAG: a hold leaves out a slot the last tick of its minute settled. *)
HoldFrom(T) ==
    LET settled == Mutation /= "rehold-last-tick-minute" /\ lastSlot >= T
                   /\ (~lastPlanned \/ Mutation = "rehold-skips-planned")
    IN IF heldSince /= Zero THEN heldSince ELSE IF settled THEN lastSlot + Sec ELSE T

(* recordSlot: a later slot replaces the latest, and a dispatch replaces *)
(* the same slot planned; nothing replaces the same slot dispatched.      *)
RecordSlot(at, planned) ==
    IF at > lastSlot \/ (at = lastSlot /\ lastPlanned /\ ~planned)
    THEN lastSlot' = at /\ lastPlanned' = planned
    ELSE UNCHANGED <<lastSlot, lastPlanned>>

(* Plan(T), then Advance(T) and Flush. A late first tick first recovers   *)
(* what it missed. Catch-up runs before live slots, and runs finish       *)
(* before the next tick, so no overlap policy applies.                    *)
Tick ==
    /\ phase = "up"
    /\ nextTick <= clock
    /\ LET T    == nextTick
           late == starting = 2 /\ Mutation /= "late-first-tick-skips"
           r    == IF late THEN Resume(T) ELSE [gap |-> missed, buf |-> buf]
           b    == r.buf
           new  == Range(b) \ Range(buf)
       IN
       /\ catchupThrough' = IF new = {} THEN catchupThrough ELSE Max2(catchupThrough, MaxOf(new))
       /\ IF susp
          THEN \* holdDAG and dropSuspendedCatchupState. A tick that finds
               \* the DAG held takes its minute's slot into the hold, also
               \* when it repeats a minute an earlier tick decided.
               /\ heldSince' = HoldFrom(T)
               /\ holdStart' = IF heldSince = Zero THEN T ELSE holdStart
               /\ missed' = r.gap
               /\ decided' = decided \ {T}
               /\ buf' = <<>>
               /\ abandoned' = abandoned \cup Range(b)
               /\ owed' = (owed \cup new) \ Range(b)
               /\ lastSched' = IF Len(b) > 0 \/ Window > 0 THEN T ELSE lastSched
               /\ UNCHANGED <<released, handled, lastSlot, lastPlanned>>
          ELSE LET plan == PlanGap(T, b, r.gap) IN
               /\ heldSince' = plan.held
               /\ holdStart' = Zero
               /\ released' = plan.released
               /\ missed' = plan.gap
               /\ decided' = IF T \in SlotTimes THEN decided \cup {T} ELSE decided
               /\ UNCHANGED abandoned
               /\ IF Len(b) > 0
                  THEN /\ handled' = handled \cup {Head(b)}
                       /\ owed' = (owed \cup new) \ {Head(b)}
                       /\ lastSched' = Head(b)
                       /\ RecordSlot(Head(b), FALSE)
                       /\ buf' = Tail(b)
                  ELSE /\ handled' = IF T \in SlotTimes THEN handled \cup {T} ELSE handled
                       /\ owed' = owed \cup new
                       /\ lastSched' = IF T \in SlotTimes THEN T ELSE lastSched
                       /\ IF T \in SlotTimes THEN RecordSlot(T, FALSE)
                          ELSE UNCHANGED <<lastSlot, lastPlanned>>
                       /\ buf' = b
       \* resume may record a gap that the hold's release then replaces.
       /\ superseded' = superseded \cup ((Counted(missed) \cup Counted(r.gap)) \ Counted(missed'))
       \* A suspended DAG records no outage.
       /\ lateLost' = IF starting = 2 /\ ~susp
                      THEN FiresIn(initClock, MinuteOf(T) - Sec)
                           \ (Counted(missed') \cup superseded' \cup Range(buf') \cup handled' \cup owed')
                      ELSE lateLost
       /\ lastTick' = T
       /\ nextTick' = T + 4
       /\ starting' = 0
    /\ UNCHANGED <<clock, phase, susp, initClock, bounds>>

(* A tick plans a live slot, and runTick flushes the slot as planned. The *)
(* scheduler dies before the run starts.                                  *)
TickThenCrash ==
    /\ phase = "up"
    /\ nextTick <= clock
    /\ starting < 2
    /\ restarts < MaxRestarts
    /\ ~susp
    /\ Len(buf) = 0
    /\ nextTick \in SlotTimes
    /\ LET T    == nextTick
           plan == PlanGap(T, buf, missed)
       IN /\ heldSince' = plan.held
          /\ holdStart' = Zero
          /\ released' = plan.released
          /\ missed' = plan.gap
          /\ decided' = decided \cup {T}
          /\ lastSched' = T
          /\ RecordSlot(T, TRUE)
          /\ lastTick' = T
          /\ nextTick' = T + 4
    /\ phase' = "down"
    /\ starting' = 0
    /\ restarts' = restarts + 1
    /\ Supersede
    /\ UNCHANGED <<clock, susp, catchupThrough, buf, handled, abandoned, owed,
                   initClock, lateLost, toggles, backfills>>

(* The scheduler dies. The watermarks were flushed, and the catch-up      *)
(* buffer, kept in memory, is lost.                                       *)
Crash ==
    /\ phase = "up"
    /\ restarts < MaxRestarts
    /\ phase' = "down"
    /\ buf' = <<>>
    /\ starting' = 0
    /\ restarts' = restarts + 1
    /\ UNCHANGED <<clock, nextTick, susp, watermark, ghosts, toggles, backfills>>

Restart ==
    /\ phase = "down"
    /\ phase' = "initA"
    /\ UNCHANGED <<clock, nextTick, starting, susp, watermark, buf, ghosts, bounds>>

(* A running scheduler ticks before the clock leaves the tick's minute. *)
Clock ==
    /\ clock < MaxClock
    /\ phase \in {"down", "up"}
    /\ ~(phase = "up" /\ nextTick <= clock)
    /\ clock' = clock + 2
    /\ UNCHANGED <<phase, nextTick, starting, susp, watermark, buf, ghosts, bounds>>

(* An operator suspends or resumes the DAG at any moment, also mid-startup. *)
Toggle ==
    /\ toggles < MaxToggles
    /\ susp' = ~susp
    /\ toggles' = toggles + 1
    /\ UNCHANGED <<scheduler, watermark, buf, ghosts, restarts, backfills>>

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
    /\ Supersede
    /\ UNCHANGED <<scheduler, susp, lastTick, lastSched, lastSlot, lastPlanned,
                   catchupThrough, buf, holdStart, released, decided, abandoned,
                   owed, initClock, lateLost, toggles, restarts>>

Next == Tick \/ TickThenCrash \/ Crash \/ Restart \/ StartupSettle \/ StartupBuffers
        \/ StartLate \/ Clock \/ Toggle \/ Backfill

Spec == Init /\ [][Next]_vars

-----------------------------------------------------------------------------

GapHasNoRuns == Counted(missed) \cap (handled \cup owed \cup Range(buf)) = {}

GapCountExact == missed.valid => missed.slots = Min2(Cap, Cardinality(Counted(missed)))

(* The slots of the hold under way, which its release will count. *)
OpenHold == IF holdStart = Zero THEN {} ELSE {s \in SlotTimes : s >= holdStart}

(* A hold's span runs from the tick that started it to the tick that ended *)
(* it, whose own slots that tick decided, or to the last tick for a hold  *)
(* settled at startup. Its release counts every slot of the span that no  *)
(* tick decided, no run took, no catch-up holds, and no other gap counts  *)
(* or counted, whether or not that makes a gap.                           *)
HoldGapComplete ==
    released.valid =>
        {s \in SlotTimes : s >= released.start /\ s <= released.to}
            \ (handled \cup decided \cup abandoned \cup owed \cup superseded
               \cup Counted(missed) \cup Range(buf) \cup OpenHold)
            \subseteq released.counted

CatchupKept == phase = "up" => owed \subseteq Range(buf)

LateTickKept == lateLost = {}

=============================================================================
