// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// Rerun decides which slots that already have a run are run again.
type Rerun string

const (
	// RerunNone runs only slots with no run.
	RerunNone Rerun = "none"
	// RerunFailed also runs slots whose latest run did not succeed.
	RerunFailed Rerun = "failed"
	// RerunAll runs every slot.
	RerunAll Rerun = "all"
)

// Reruns lists every Rerun value.
var Reruns = []Rerun{RerunNone, RerunFailed, RerunAll}

// ParseRerun parses a --rerun value.
func ParseRerun(value string) (Rerun, error) {
	rerun := Rerun(value)
	if !slices.Contains(Reruns, rerun) {
		return "", fmt.Errorf("invalid rerun %q: use none, failed, or all", value)
	}
	return rerun, nil
}

// Action is what a backfill does with a slot.
type Action string

const (
	ActionRun           Action = "run"
	ActionSkipSucceeded Action = "skip: succeeded"
	ActionSkipFailed    Action = "skip: failed"
	ActionSkipActive    Action = "skip: active"
)

// DefaultMaxSlots bounds the slots of one backfill when the scheduler sets
// no backfill_max_slots.
const DefaultMaxSlots = 10000

// PlanRequest is what a plan is made from.
type PlanRequest struct {
	DAG      *ir.DAG
	Schedule SlotSchedule
	// From and To bound a range of slots, both inclusive. To is clamped to
	// now. Both are unset when Slots names the slots.
	From, To time.Time
	// Slots names the slots to run, instead of a range.
	Slots []time.Time
	Rerun Rerun
	// Resume plans named slots by Rerun, as a resume does, rather than run
	// them whatever their history.
	Resume   bool
	MaxSlots int
	Now      time.Time
}

// Plan is what a backfill would do.
type Plan struct {
	DAGName string
	// From and To are the range the plan covers, To after clamping.
	From, To time.Time
	// Clamped reports that To was later than now.
	Clamped bool
	Rerun   Rerun
	Slots   []PlanSlot
	// RunCounts is how many slots each Rerun value would run.
	RunCounts map[Rerun]int
	// OtherCadence counts slots that run because their nearest run fired
	// under a schedule of another cadence.
	OtherCadence int
	// Revision identifies the plan, so a submit can refuse a plan that
	// changed after it was shown.
	Revision string
}

// PlanSlot is one slot of a plan.
type PlanSlot struct {
	At     time.Time
	Window ir.ScheduleWindow
	Action Action
	// RunID names the run a skip relies on.
	RunID string
	// CoveredAt is the scheduled time of a run under an earlier schedule
	// that a skip relies on, or zero.
	CoveredAt time.Time
}

// Runs returns the slots the plan runs.
func (p Plan) Runs() []PlanSlot {
	var runs []PlanSlot
	for _, slot := range p.Slots {
		if slot.Action == ActionRun {
			runs = append(runs, slot)
		}
	}
	return runs
}

// SlotRange is the slots a request covers.
type SlotRange struct {
	Slots []time.Time
	// To is the end of a range after clamping, or zero for named slots.
	To time.Time
	// Clamped reports that the range ended later than now.
	Clamped bool
}

// SlotsOf lists the slots a request covers, and checks the request against
// now and the bound on slots.
func SlotsOf(req PlanRequest) (SlotRange, error) {
	if len(req.Slots) > 0 {
		for _, slot := range req.Slots {
			if slot.After(req.Now) {
				return SlotRange{}, invalid("slot %s is later than now", slot.Format(time.RFC3339))
			}
		}
		slots := slices.Clone(req.Slots)
		slices.SortFunc(slots, func(a, b time.Time) int { return a.Compare(b) })
		slots = slices.CompactFunc(slots, func(a, b time.Time) bool { return a.Equal(b) })
		for i, slot := range slots {
			slots[i] = inScheduleZone(req.Schedule, slot)
		}
		return SlotRange{Slots: slots}, checkMaxSlots(len(slots), req.MaxSlots)
	}
	if req.From.After(req.Now) {
		return SlotRange{}, invalid("from %s is later than now", req.From.Format(time.RFC3339))
	}
	rng := SlotRange{To: req.To}
	// Fires in the current minute or later are left to the live schedule.
	liveFrom := req.Now.Truncate(time.Minute)
	if rng.To.IsZero() || !rng.To.Before(liveFrom) {
		rng.Clamped = !rng.To.IsZero()
		rng.To = liveFrom.Add(-time.Nanosecond)
	}
	if rng.To.Before(req.From) {
		return SlotRange{}, invalid("to is before from")
	}
	slots, more := ir.ScheduleFiresBetween(req.Schedule.Schedules, req.Schedule.Profile, req.Schedule.Location, req.From, rng.To, maxSlotsOf(req.MaxSlots))
	if more {
		return SlotRange{}, invalid("the range has more than %d slots, the bound set by scheduler.backfill_max_slots", maxSlotsOf(req.MaxSlots))
	}
	rng.Slots = slots
	return rng, nil
}

func maxSlotsOf(bound int) int {
	if bound <= 0 {
		return DefaultMaxSlots
	}
	return bound
}

// inScheduleZone returns a slot in the zone of the schedule that fires at
// it, as the slots of a range read, or else in the zone its window names. A
// resumed slot whose schedule has moved fires under no current schedule.
func inScheduleZone(sched SlotSchedule, slot time.Time) time.Time {
	if fires, _ := ir.ScheduleFiresBetween(sched.Schedules, sched.Profile, sched.Location, slot, slot, 1); len(fires) == 1 {
		return fires[0]
	}
	if zone := ir.ScheduleWindowAt(sched.Schedules, sched.Profile, sched.Location, slot).Timezone; zone != "" {
		if loc, err := time.LoadLocation(zone); err == nil {
			return slot.In(loc)
		}
	}
	if sched.Location != nil {
		return slot.In(sched.Location)
	}
	return slot
}

func checkMaxSlots(count, bound int) error {
	if count > maxSlotsOf(bound) {
		return invalid("%d slots exceed %d, the bound set by scheduler.backfill_max_slots", count, maxSlotsOf(bound))
	}
	return nil
}

// MakePlan decides what a backfill does with each slot. Pending holds the
// slots that open backfills of the DAG have yet to run.
func MakePlan(req PlanRequest, rng SlotRange, states []Slot, pending map[time.Time]bool) Plan {
	plan := Plan{
		DAGName:   req.DAG.Name,
		From:      req.From,
		To:        rng.To,
		Clamped:   rng.Clamped,
		Rerun:     req.Rerun,
		RunCounts: make(map[Rerun]int, len(Reruns)),
	}
	named := len(req.Slots) > 0 && !req.Resume
	for i, at := range rng.Slots {
		state := states[i]
		if pending[at.UTC()] {
			// An open backfill holds the slot, not the slot's past runs.
			state = Slot{At: at, Status: SlotActive}
		}
		slot := PlanSlot{
			At:     at,
			Window: ir.ScheduleWindowAt(req.Schedule.Schedules, req.Schedule.Profile, req.Schedule.Location, at),
			Action: actionFor(state.Status, req.Rerun, named),
		}
		if slot.Action != ActionRun {
			slot.RunID = state.RunID
			slot.CoveredAt = state.CoveredAt
		} else if state.OtherCadence {
			plan.OtherCadence++
		}
		for _, rerun := range Reruns {
			if actionFor(state.Status, rerun, named) == ActionRun {
				plan.RunCounts[rerun]++
			}
		}
		plan.Slots = append(plan.Slots, slot)
	}
	plan.Revision = plan.revision()
	return plan
}

// actionFor applies --rerun to a slot's state. A slot named with --slot runs
// whatever its history, unless it is active.
func actionFor(status SlotStatus, rerun Rerun, named bool) Action {
	switch status {
	case SlotActive:
		return ActionSkipActive
	case SlotMissing:
		return ActionRun
	case SlotSucceeded:
		if named || rerun == RerunAll {
			return ActionRun
		}
		return ActionSkipSucceeded
	case SlotFailed:
		if named || rerun != RerunNone {
			return ActionRun
		}
		return ActionSkipFailed
	}
	return ActionRun
}

// revision hashes what the plan does, so equal plans share a revision.
func (p Plan) revision() string {
	var b strings.Builder
	b.WriteString(p.DAGName)
	b.WriteString("|")
	b.WriteString(string(p.Rerun))
	for _, slot := range p.Slots {
		b.WriteString("|")
		b.WriteString(slot.At.UTC().Format(time.RFC3339))
		b.WriteString("=")
		b.WriteString(string(slot.Action))
		// A submit records each slot's window, so a changed window is a
		// changed plan.
		b.WriteString("@")
		b.WriteString(slot.Window.Start.UTC().Format(time.RFC3339))
		b.WriteString(",")
		b.WriteString(slot.Window.Next.UTC().Format(time.RFC3339))
		b.WriteString(",")
		b.WriteString(slot.Window.Timezone)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

// MarshalJSON encodes the plan with times in RFC 3339.
func (p Plan) MarshalJSON() ([]byte, error) {
	type slotJSON struct {
		At          string `json:"slot"`
		WindowStart string `json:"windowStart,omitempty"`
		WindowEnd   string `json:"windowEnd"`
		Action      Action `json:"action"`
		RunID       string `json:"runId,omitempty"`
		CoveredAt   string `json:"coveredBy,omitempty"`
	}
	slots := make([]slotJSON, 0, len(p.Slots))
	for _, slot := range p.Slots {
		item := slotJSON{
			At:          slot.At.Format(time.RFC3339),
			WindowStart: formatOptional(slot.Window.Start),
			WindowEnd:   slot.At.Format(time.RFC3339),
			Action:      slot.Action,
			RunID:       slot.RunID,
			CoveredAt:   formatOptional(slot.CoveredAt),
		}
		slots = append(slots, item)
	}
	return json.Marshal(struct {
		DAG          string        `json:"dag"`
		From         string        `json:"from,omitempty"`
		To           string        `json:"to,omitempty"`
		Clamped      bool          `json:"clamped,omitempty"`
		Rerun        Rerun         `json:"rerun"`
		RunCounts    map[Rerun]int `json:"runCounts"`
		OtherCadence int           `json:"otherCadence,omitempty"`
		Revision     string        `json:"revision"`
		Slots        []slotJSON    `json:"slots"`
	}{
		DAG:          p.DAGName,
		From:         formatOptional(p.From),
		To:           formatOptional(p.To),
		Clamped:      p.Clamped,
		Rerun:        p.Rerun,
		RunCounts:    p.RunCounts,
		OtherCadence: p.OtherCadence,
		Revision:     p.Revision,
		Slots:        slots,
	})
}

func formatOptional(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
