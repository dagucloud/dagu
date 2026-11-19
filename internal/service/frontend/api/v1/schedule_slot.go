// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/rundates"
	"github.com/dagucloud/dagu/v2/internal/spec"
)

// requestedSlot is the schedule slot a start or enqueue request names.
type requestedSlot struct {
	// scheduleTime is the slot as the start subprocess takes it.
	scheduleTime string
	at           time.Time
	window       ir.ScheduleWindow
	warning      string
}

// resolveRequestedSlot resolves the slot a request's scheduleTime names among
// the DAG's start schedules for the profile, or returns nil when it names
// none.
func (a *API) resolveRequestedSlot(dag *ir.DAG, profile string, value *api.ScheduleSlotValue) (*requestedSlot, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil, nil
	}
	schedules := slices.Clone(dag.Schedule)
	spec.BindScheduleCalendars(schedules, a.calendarSource())
	loc := a.serverLocation()
	slot, err := ir.ResolveScheduleSlot(ir.ScheduleSlotRequest{
		Value:     *value,
		Schedules: schedules,
		Profile:   profile,
		Location:  loc,
		Now:       time.Now(),
	})
	if err != nil {
		return nil, &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    "invalid scheduleTime: " + err.Error(),
		}
	}
	return &requestedSlot{
		scheduleTime: stringutil.FormatTime(slot.At.In(loc)),
		at:           slot.At,
		window:       slot.Window,
		warning:      slot.Warning,
	}, nil
}

// runDates resolves the dates of a run created now for a slot.
func (a *API) runDates(dag *ir.DAG, profile, scheduleTime string, window ir.ScheduleWindow) ([]ir.DateValue, error) {
	if len(dag.Dates) == 0 {
		return nil, nil
	}
	return rundates.Resolve(rundates.Request{
		DAG:          dag,
		Profile:      profile,
		ScheduleTime: scheduleTime,
		Window:       window,
		CreatedAt:    time.Now(),
		Location:     a.serverLocation(),
		Calendars:    a.calendarSource(),
	})
}

// checkDates refuses a run whose dates cannot resolve for slot, or for the
// default slot when it names none, so that it is never created. The run
// resolves them again when it is created.
func (a *API) checkDates(dag *ir.DAG, profile string, slot *requestedSlot) error {
	if len(dag.Dates) == 0 {
		return nil
	}
	scheduleTime, window := slot.scheduleTimeOrEmpty(), slot.windowOrZero()
	if slot == nil {
		if def, ok := rundates.DefaultSlot(dag, profile, a.serverLocation(), a.calendarSource(), time.Now()); ok {
			scheduleTime, window = def.ScheduleTime, def.Window
		}
	}
	if _, err := a.runDates(dag, profile, scheduleTime, window); err != nil {
		return &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    err.Error(),
		}
	}
	return nil
}

// rescheduleSlot returns the slot of a run rescheduled from status: the one
// the request names, else the historic run's own.
func (a *API) rescheduleSlot(dag *ir.DAG, profile string, status *ir.DAGRunStatus, value *api.ScheduleSlotValue) (*requestedSlot, error) {
	if value != nil && strings.TrimSpace(*value) != "" {
		return a.resolveRequestedSlot(dag, profile, value)
	}
	if status.ScheduleTime == "" {
		return nil, nil
	}
	at, err := stringutil.ParseTime(status.ScheduleTime)
	if err != nil {
		return nil, fmt.Errorf("parse schedule time of the historic run: %w", err)
	}
	return &requestedSlot{scheduleTime: status.ScheduleTime, at: at.UTC(), window: status.ScheduleWindow()}, nil
}

// recentFiresLimit is how many past slots the run dialog offers.
const recentFiresLimit = 5

// recentFires lists the latest slots of the DAG's start schedules for its
// default runtime profile, latest first.
func (a *API) recentFires(ctx context.Context, fileName string, dag *ir.DAG) *[]api.RecentFire {
	if dag == nil || len(dag.Schedule) == 0 {
		return nil
	}
	profile, err := a.runProfileForDAG(ctx, fileName, dagWorkspaceName(dag), nil)
	if err != nil {
		profile = ""
	}
	schedules := slices.Clone(dag.Schedule)
	spec.BindScheduleCalendars(schedules, a.calendarSource())
	fires := ir.RecentScheduleFires(schedules, profile, a.serverLocation(), time.Now(), recentFiresLimit)
	if len(fires) == 0 {
		return nil
	}
	out := make([]api.RecentFire, 0, len(fires))
	for _, fire := range fires {
		out = append(out, api.RecentFire{Time: fire, Timezone: fireZoneLabel(fire)})
	}
	return &out
}

func (a *API) serverLocation() *time.Location {
	if a.config != nil && a.config.Core.Location != nil {
		return a.config.Core.Location
	}
	return time.Local
}

// slotResponse returns the resolved slot and its warnings for a response.
func (s *requestedSlot) slotResponse() (*time.Time, *[]string) {
	if s == nil {
		return nil, nil
	}
	at := s.at
	if s.warning == "" {
		return &at, nil
	}
	return &at, &[]string{s.warning}
}

func (s *requestedSlot) scheduleTimeOrEmpty() string {
	if s == nil {
		return ""
	}
	return s.scheduleTime
}

func (s *requestedSlot) windowOrZero() ir.ScheduleWindow {
	if s == nil {
		return ir.ScheduleWindow{}
	}
	return s.window
}

// PreviewDAGSlot resolves the slot a run would be started for, its window,
// and the dates its run would get, without starting a run.
func (a *API) PreviewDAGSlot(ctx context.Context, request api.PreviewDAGSlotRequestObject) (api.PreviewDAGSlotResponseObject, error) {
	dag, _, err := a.backfillDAG(ctx, request.FileName, valueOf(request.Params.Profile))
	if err != nil {
		return nil, err
	}
	// The profile a start would use: the one named, else the DAG's default.
	profile, err := a.runProfileForDAG(ctx, request.FileName, dagWorkspaceName(dag), request.Params.Profile)
	if err != nil {
		return nil, err
	}
	slot, err := a.resolveRequestedSlot(dag, profile, request.Params.ScheduledAt)
	if err != nil {
		return nil, err
	}
	preview := api.SlotPreview{Dates: []api.RunDateValue{}}
	scheduleTime, window := slot.scheduleTimeOrEmpty(), slot.windowOrZero()
	if slot != nil {
		preview.Warning = stringOrNil(slot.warning)
	} else if def, ok := rundates.DefaultSlot(dag, profile, a.serverLocation(), a.calendarSource(), time.Now()); ok {
		scheduleTime, window = def.ScheduleTime, def.Window
		preview.DefaultSlot = true
	}
	if scheduleTime != "" {
		at, err := stringutil.ParseTime(scheduleTime)
		if err != nil {
			return nil, fmt.Errorf("parse slot %q: %w", scheduleTime, err)
		}
		preview.ScheduledAt = &at
		preview.WindowStart = timeOrNil(window.Start)
		preview.NextScheduledAt = timeOrNil(window.Next)
		preview.Timezone = stringOrNil(window.Timezone)
	}
	dates, err := a.runDates(dag, profile, scheduleTime, window)
	if err != nil {
		return nil, &Error{HTTPStatus: http.StatusBadRequest, Code: api.ErrorCodeBadRequest, Message: err.Error()}
	}
	if converted := toAPIRunDates(dates); converted != nil {
		preview.Dates = *converted
	}
	preview.Params = slotParamValues(dag.ParamDefs, dates)
	return api.PreviewDAGSlot200JSONResponse(preview), nil
}

// slotParamValues returns the value each param whose eval is one date
// reference takes from a slot's dates. Other evals run only when the run
// starts.
func slotParamValues(defs []ir.ParamDef, dates []ir.DateValue) *[]api.SlotParamValue {
	var values []api.SlotParamValue
	for _, def := range defs {
		name, ok := def.DateName()
		if !ok {
			continue
		}
		if i := slices.IndexFunc(dates, func(date ir.DateValue) bool { return date.Name == name }); i >= 0 {
			values = append(values, api.SlotParamValue{Name: def.Name, Value: dates[i].Text()})
		}
	}
	if len(values) == 0 {
		return nil
	}
	return &values
}
