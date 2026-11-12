// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package backfill

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// Request is a backfill as an operator asks for it, with its times not yet
// resolved against the schedule.
type Request struct {
	// From and To bound a range: dates, times without an offset, or
	// RFC 3339 times. To defaults to now.
	From, To string
	// Slots names slots to run instead of a range.
	Slots []string
	// Rerun defaults to none, MaxConcurrency to 1, and OnFailure to stop.
	Rerun          string
	MaxConcurrency int
	OnFailure      string
	Params         string
	Profile        string
}

// ErrInvalidRequest marks an error in what an operator asked for.
var ErrInvalidRequest = errors.New("invalid backfill request")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidRequest, fmt.Sprintf(format, args...))
}

// Resolve checks a request and resolves its times against the DAG's
// schedule. It returns the plan request and the options to record.
func (r Request) Resolve(dag *ir.DAG, sched SlotSchedule, maxSlots int, now time.Time) (PlanRequest, Options, error) {
	if len(dag.Schedule) == 0 {
		return PlanRequest{}, Options{}, invalid("DAG %q has no schedule to backfill", dag.Name)
	}
	if !ir.HasRecurringSchedule(sched.Schedules, sched.Profile) {
		return PlanRequest{}, Options{}, invalid("DAG %q has no recurring schedule for profile %q to backfill", dag.Name, sched.Profile)
	}
	named := len(r.Slots) > 0
	switch {
	case named && (r.From != "" || r.To != "" || r.Rerun != ""):
		return PlanRequest{}, Options{}, invalid("slots cannot be combined with from, to, or rerun")
	case !named && r.From == "":
		return PlanRequest{}, Options{}, invalid("give from, or name slots")
	}
	rerun := RerunNone
	if r.Rerun != "" {
		parsed, err := ParseRerun(r.Rerun)
		if err != nil {
			return PlanRequest{}, Options{}, invalid("%s", err)
		}
		rerun = parsed
	}
	options := Options{Rerun: rerun, MaxConcurrency: 1, OnFailure: OnFailureStop, Params: r.Params, Profile: strings.TrimSpace(r.Profile)}
	if r.MaxConcurrency < 0 {
		return PlanRequest{}, Options{}, invalid("max concurrency must be a positive integer")
	}
	if r.MaxConcurrency > 0 {
		options.MaxConcurrency = r.MaxConcurrency
	}
	if r.OnFailure != "" {
		onFailure, err := ParseOnFailure(r.OnFailure)
		if err != nil {
			return PlanRequest{}, Options{}, invalid("%s", err)
		}
		options.OnFailure = onFailure
	}

	req := PlanRequest{DAG: dag, Schedule: sched, Rerun: rerun, MaxSlots: maxSlots, Now: now}
	if named {
		for _, value := range r.Slots {
			slot, err := ir.ResolveScheduleSlot(ir.ScheduleSlotRequest{
				Value:     value,
				Schedules: sched.Schedules,
				Profile:   sched.Profile,
				Location:  sched.Location,
				Now:       now,
			})
			if err != nil {
				return PlanRequest{}, Options{}, invalid("slot %s: %s", value, err)
			}
			if slot.Warning != "" {
				return PlanRequest{}, Options{}, invalid("slot %s: %s", value, slot.Warning)
			}
			req.Slots = append(req.Slots, slot.At)
		}
	} else {
		from, err := ir.ResolveRangeBound(r.From, sched.Schedules, sched.Profile, sched.Location, ir.RangeStart)
		if err != nil {
			return PlanRequest{}, Options{}, invalid("from: %s", err)
		}
		req.From = from
		if r.To != "" {
			if req.To, err = ir.ResolveRangeBound(r.To, sched.Schedules, sched.Profile, sched.Location, ir.RangeEnd); err != nil {
				return PlanRequest{}, Options{}, invalid("to: %s", err)
			}
		}
	}
	// The range as given identifies the backfill; To stays zero when it
	// defaulted to now.
	options.From, options.To, options.Slots = req.From, req.To, req.Slots
	return req, options, nil
}

// WarningInput is what the warnings of a plan depend on beyond the plan.
type WarningInput struct {
	SchedulerRunning bool
	Suspended        bool
	DAG              *ir.DAG
}

// Warnings lists what an operator should know before starting a plan.
func (p Plan) Warnings(in WarningInput) []string {
	var warnings []string
	if !in.SchedulerRunning {
		warnings = append(warnings, "No scheduler is running; the backfill starts when dagu scheduler or dagu start-all runs.")
	}
	if in.Suspended {
		warnings = append(warnings, "The DAG is suspended; its backfill runs still run.")
	}
	if handlers := handlerNames(in.DAG); len(handlers) > 0 {
		warnings = append(warnings, fmt.Sprintf("handler_on steps run once per slot: %s.", strings.Join(handlers, ", ")))
	}
	if p.OtherCadence > 0 {
		warnings = append(warnings, fmt.Sprintf("%d slots run because the schedule's cadence changed since their runs.", p.OtherCadence))
	}
	return warnings
}

func handlerNames(dag *ir.DAG) []string {
	if dag == nil {
		return nil
	}
	var names []string
	for _, step := range []*ir.Step{dag.HandlerOn.Init, dag.HandlerOn.Success, dag.HandlerOn.Failure, dag.HandlerOn.Abort, dag.HandlerOn.Exit, dag.HandlerOn.Wait} {
		if step != nil {
			names = append(names, step.Name)
		}
	}
	return names
}
