// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	openapitypes "github.com/oapi-codegen/runtime/types"
)

// ListCalendars lists the built-in calendars and the calendar files that
// start schedules can name.
func (a *API) ListCalendars(_ context.Context, _ api.ListCalendarsRequestObject) (api.ListCalendarsResponseObject, error) {
	summaries, err := spec.ListCalendars(a.config.Paths.CalendarsDir)
	if err != nil {
		return nil, err
	}
	calendars := make([]api.CalendarSummary, 0, len(summaries))
	for _, summary := range summaries {
		calendar := api.CalendarSummary{
			Name:    summary.Name,
			Builtin: summary.Builtin,
		}
		if summary.Description != "" {
			calendar.Description = ptrOf(summary.Description)
		}
		if !summary.Through.IsZero() {
			calendar.Through = ptrOf(toAPIDate(summary.Through))
		}
		if summary.Err != nil {
			calendar.Error = ptrOf(summary.Err.Error())
		}
		calendars = append(calendars, calendar)
	}
	return api.ListCalendars200JSONResponse{Calendars: calendars}, nil
}

func toAPIDate(date ir.CivilDate) openapitypes.Date {
	return openapitypes.Date{Time: time.Date(date.Year, date.Month, date.Day, 0, 0, 0, 0, time.UTC)}
}
