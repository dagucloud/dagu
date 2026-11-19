// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package rundates resolves the dates a run of a DAG gets when it is
// created, from the DAG's dates block and the run's schedule slot.
package rundates

import (
	"fmt"
	"slices"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

// Request describes a run being created.
type Request struct {
	DAG *ir.DAG
	// Profile is the run's runtime profile, which selects the schedules that
	// own its slot.
	Profile string
	// ScheduleTime is the run's slot in RFC 3339, or empty when it has none.
	ScheduleTime string
	// Window is the window the slot owns.
	Window ir.ScheduleWindow
	// Inherited reports a slot taken from a parent run, which no schedule of
	// the DAG owns.
	Inherited bool
	// CreatedAt is when the run is created.
	CreatedAt time.Time
	// Location is the server's zone.
	Location *time.Location
	// Calendars loads the calendars business days read.
	Calendars *spec.CalendarSource
}

// Resolve returns the run's dates, or nil when the DAG declares none. An
// error names the date that cannot resolve.
func Resolve(req Request) ([]ir.DateValue, error) {
	if req.DAG == nil || len(req.DAG.Dates) == 0 {
		return nil, nil
	}
	var slot time.Time
	if req.ScheduleTime != "" {
		var err error
		if slot, err = stringutil.ParseTime(req.ScheduleTime); err != nil {
			return nil, fmt.Errorf("dates: invalid schedule time %q: %w", req.ScheduleTime, err)
		}
	}
	dc := ir.DateContext{
		ScheduledAt: slot,
		Window:      req.Window,
		CreatedAt:   req.CreatedAt,
		Location:    req.Location,
	}
	// Only business days read a calendar, so other dates load no file.
	if len(ir.DatesUsingBusinessDays(req.DAG.Dates)) > 0 {
		cal, err := calendar(req, slot)
		if err != nil {
			return nil, err
		}
		dc.Calendar = cal
	}
	dates, err := ir.ResolveDates(req.DAG.Dates, dc)
	if err != nil && slot.IsZero() && !ir.HasRecurringSchedule(req.DAG.Schedule, req.Profile) {
		return nil, fmt.Errorf("%w; the DAG has no schedule", err)
	}
	return dates, err
}

// calendar returns the business days of the slot: the calendar of the
// schedule that owns it, else the DAG's, or nil when there is neither.
func calendar(req Request, slot time.Time) (*ir.Calendar, error) {
	source := req.Calendars
	if source == nil {
		source = spec.NewCalendarSource("")
	}
	if !slot.IsZero() && !req.Inherited {
		schedules := slices.Clone(req.DAG.Schedule)
		spec.BindScheduleCalendars(schedules, source)
		if cal, ok := ir.SlotCalendar(schedules, req.Profile, req.Location, slot); ok {
			return cal, nil
		}
	}
	name := req.DAG.Calendar
	if name == "" || name == ir.CalendarNone {
		return nil, nil
	}
	cal, err := source.Calendar(name)
	if err != nil {
		return nil, fmt.Errorf("dates: calendar %q: %w", name, err)
	}
	return cal, nil
}

// Slot is a run's schedule slot: its time in RFC 3339 and its window.
type Slot struct {
	ScheduleTime string
	Window       ir.ScheduleWindow
}

// DefaultSlot returns the slot a run gets when it names none: the latest fire
// of the DAG's recurring start schedules for the profile at or before
// createdAt. Only a DAG with dates gets one, so that its dates follow the
// schedule. ok is false when there is no such fire.
func DefaultSlot(dag *ir.DAG, profile string, loc *time.Location, calendars *spec.CalendarSource, createdAt time.Time) (Slot, bool) {
	if dag == nil || len(dag.Dates) == 0 || !ir.HasRecurringSchedule(dag.Schedule, profile) {
		return Slot{}, false
	}
	if loc == nil {
		loc = time.Local
	}
	if calendars == nil {
		calendars = spec.NewCalendarSource("")
	}
	schedules := slices.Clone(dag.Schedule)
	spec.BindScheduleCalendars(schedules, calendars)
	slot, err := ir.ResolveScheduleSlot(ir.ScheduleSlotRequest{
		Value:     ir.ScheduleSlotLatest,
		Schedules: schedules,
		Profile:   profile,
		Location:  loc,
		Now:       createdAt,
	})
	if err != nil {
		return Slot{}, false
	}
	return Slot{ScheduleTime: stringutil.FormatTime(slot.At.In(loc)), Window: slot.Window}, true
}
