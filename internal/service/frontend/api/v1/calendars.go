// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/pagination"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	openapitypes "github.com/oapi-codegen/runtime/types"
)

// calendarInUseListLimit bounds the DAGs a refused delete names.
const calendarInUseListLimit = 5

var (
	errCalendarsNotAvailable = &Error{
		HTTPStatus: http.StatusForbidden,
		Code:       api.ErrorCodeForbidden,
		Message:    "Calendar files are not available: paths.calendars_dir is not set",
	}
	errBuiltinCalendar = &Error{
		HTTPStatus: http.StatusBadRequest,
		Code:       api.ErrorCodeBadRequest,
		Message:    "Built-in calendars cannot be changed",
	}
)

// ListCalendars lists the built-in calendars and the calendar files that
// start schedules can name.
func (a *API) ListCalendars(ctx context.Context, request api.ListCalendarsRequestObject) (api.ListCalendarsResponseObject, error) {
	summaries, err := a.calendarSource().List()
	if err != nil {
		return nil, err
	}
	var usage calendarUsage
	if request.Params.IncludeUsage != nil && *request.Params.IncludeUsage {
		if usage, err = a.calendarUsage(ctx, summaries, a.workspaceFilterForContext(ctx)); err != nil {
			return nil, err
		}
	}
	today := a.calendarToday()
	calendars := make([]api.CalendarSummary, 0, len(summaries))
	for _, summary := range summaries {
		calendar := api.CalendarSummary{
			Name:     summary.Name,
			Builtin:  summary.Builtin,
			Includes: ptrOf(nonNil(summary.Includes)),
		}
		if summary.Description != "" {
			calendar.Description = ptrOf(summary.Description)
		}
		if !summary.Through.IsZero() {
			calendar.Through = ptrOf(toAPIDate(summary.Through))
		}
		if warning := spec.CalendarExpiryWarning(summary.Name, summary.Through, today); warning != "" {
			calendar.Warning = ptrOf(warning)
		}
		if summary.Err != nil {
			calendar.Error = ptrOf(summary.Err.Error())
		}
		// A file named after a built-in calendar is never used; the name
		// reaches the built-in one.
		if _, shadowed := ir.BuiltinCalendar(summary.Name); usage != nil && (summary.Builtin || !shadowed) {
			calendar.DagCount = new(len(usage.users(summary.Name)))
		}
		calendars = append(calendars, calendar)
	}
	return api.ListCalendars200JSONResponse{Calendars: calendars}, nil
}

// GetCalendar returns a calendar's file content, its problems, and what
// depends on it.
func (a *API) GetCalendar(ctx context.Context, request api.GetCalendarRequestObject) (api.GetCalendarResponseObject, error) {
	source := a.calendarSource()
	summaries, err := source.List()
	if err != nil {
		return nil, err
	}
	usage, err := a.calendarUsage(ctx, summaries, a.workspaceFilterForContext(ctx))
	if err != nil {
		return nil, err
	}
	details := api.GetCalendar200JSONResponse{
		Name:       request.Name,
		Errors:     []string{},
		Warnings:   []string{},
		Includes:   []string{},
		IncludedBy: nonNil(usage.dependents(request.Name)),
		UsedBy:     nonNil(usage.users(request.Name)),
	}

	if builtin, ok := ir.BuiltinCalendar(request.Name); ok {
		details.Builtin = true
		details.Description = ptrOf(builtin.Description)
		return details, nil
	}
	data, err := a.getCalendarFile(ctx, request.Name)
	if err != nil {
		return nil, err
	}
	cal, errs := source.Check(request.Name, data)
	details.Spec = string(data)
	details.Errors = nonNil(spec.ErrorMessages(data, errs))
	if cal != nil {
		if cal.Description != "" {
			details.Description = ptrOf(cal.Description)
		}
		if !cal.Through.IsZero() {
			details.Through = ptrOf(toAPIDate(cal.Through))
		}
		details.Includes = nonNil(cal.Includes)
		details.Warnings = calendarWarnings(cal, a.calendarToday())
	}
	return details, nil
}

// CreateCalendar creates a calendar file, from a template when no content
// is given.
func (a *API) CreateCalendar(ctx context.Context, request api.CreateCalendarRequestObject) (api.CreateCalendarResponseObject, error) {
	if err := a.requireCalendarWrite(ctx); err != nil {
		return nil, err
	}
	name := request.Body.Name
	data := spec.CalendarTemplate(a.calendarToday().Year)
	if request.Body.Spec != nil {
		data = []byte(*request.Body.Spec)
	}
	if _, errs := a.calendarSource().Check(name, data); len(errs) > 0 {
		return nil, &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    strings.Join(spec.ErrorMessages(data, errs), "; "),
		}
	}
	if err := a.calendarStore.Create(ctx, name, data); err != nil {
		if errors.Is(err, persis.ErrCalendarAlreadyExists) {
			return nil, &Error{
				HTTPStatus: http.StatusConflict,
				Code:       api.ErrorCodeAlreadyExists,
				Message:    fmt.Sprintf("calendar %q already exists", name),
			}
		}
		return nil, err
	}
	a.logCalendarAudit(ctx, "calendar_create", name)
	return api.CreateCalendar201JSONResponse{Name: name}, nil
}

// UpdateCalendar replaces a calendar file's content, unless the content has
// problems, which are returned instead.
func (a *API) UpdateCalendar(ctx context.Context, request api.UpdateCalendarRequestObject) (api.UpdateCalendarResponseObject, error) {
	if err := a.requireCalendarWrite(ctx); err != nil {
		return nil, err
	}
	if _, ok := ir.BuiltinCalendar(request.Name); ok {
		return nil, errBuiltinCalendar
	}
	if _, err := a.getCalendarFile(ctx, request.Name); err != nil {
		return nil, err
	}
	data := []byte(request.Body.Spec)
	cal, errs := a.calendarSource().Check(request.Name, data)
	if len(errs) > 0 {
		return api.UpdateCalendar200JSONResponse{Errors: spec.ErrorMessages(data, errs), Warnings: []string{}}, nil
	}
	if err := a.calendarStore.Update(ctx, request.Name, data); err != nil {
		return nil, a.calendarStoreError(request.Name, err)
	}
	a.logCalendarAudit(ctx, "calendar_update", request.Name)
	return api.UpdateCalendar200JSONResponse{Errors: []string{}, Warnings: calendarWarnings(cal, a.calendarToday())}, nil
}

// DeleteCalendar deletes a calendar file that nothing uses.
func (a *API) DeleteCalendar(ctx context.Context, request api.DeleteCalendarRequestObject) (api.DeleteCalendarResponseObject, error) {
	if err := a.requireCalendarWrite(ctx); err != nil {
		return nil, err
	}
	if _, ok := ir.BuiltinCalendar(request.Name); ok {
		return nil, errBuiltinCalendar
	}
	if _, err := a.getCalendarFile(ctx, request.Name); err != nil {
		return nil, err
	}
	summaries, err := a.calendarSource().List()
	if err != nil {
		return nil, err
	}
	// Every DAG counts, so a calendar is not deleted from under DAGs the
	// caller cannot see; only the visible ones are named.
	usage, err := a.calendarUsage(ctx, summaries, nil)
	if err != nil {
		return nil, err
	}
	if reason := usage.inUse(request.Name, a.workspaceFilterForContext(ctx)); reason != "" {
		return nil, &Error{
			HTTPStatus: http.StatusConflict,
			Code:       api.ErrorCodeConflict,
			Message:    fmt.Sprintf("calendar %q is in use: %s", request.Name, reason),
		}
	}
	if err := a.calendarStore.Delete(ctx, request.Name); err != nil {
		return nil, a.calendarStoreError(request.Name, err)
	}
	a.logCalendarAudit(ctx, "calendar_delete", request.Name)
	return api.DeleteCalendar204Response{}, nil
}

// PreviewCalendar checks unsaved calendar content and describes each date
// of a year under it.
func (a *API) PreviewCalendar(_ context.Context, request api.PreviewCalendarRequestObject) (api.PreviewCalendarResponseObject, error) {
	today := a.calendarToday()
	preview := api.PreviewCalendar200JSONResponse{
		Year:     request.Body.Year,
		Today:    toAPIDate(today),
		Errors:   []string{},
		Warnings: []string{},
		Days:     []api.CalendarDay{},
		Changes: api.CalendarChanges{
			WeekdaysAdded:   []api.CalendarWeekday{},
			WeekdaysRemoved: []api.CalendarWeekday{},
			Dates:           []api.CalendarDateChange{},
		},
	}

	source := a.calendarSource()
	draft, saved := (*ir.Calendar)(nil), (*ir.Calendar)(nil)
	if builtin, ok := ir.BuiltinCalendar(request.Name); ok {
		draft, saved = builtin, builtin
	} else {
		data := []byte(request.Body.Spec)
		var errs []error
		draft, errs = source.Check(request.Name, data)
		preview.Errors = nonNil(spec.ErrorMessages(data, errs))
		// A saved calendar that does not load has nothing to compare with.
		saved, _ = source.CalendarFile(request.Name)
	}
	if draft == nil {
		return preview, nil
	}
	if !draft.Through.IsZero() {
		preview.Through = ptrOf(toAPIDate(draft.Through))
	}
	preview.Warnings = calendarWarnings(draft, today)
	for month := time.January; month <= time.December; month++ {
		days := draft.Month(request.Body.Year, month)
		var before []ir.CalendarDay
		if saved != nil {
			before = saved.Month(request.Body.Year, month)
		}
		for i, day := range days {
			changed := before != nil && !sameCalendarDay(before[i], day)
			preview.Days = append(preview.Days, toAPICalendarDay(day, changed))
		}
	}
	if saved != nil {
		preview.Changes = toAPICalendarChanges(ir.DiffCalendars(saved, draft))
	}
	return preview, nil
}

// calendarSource returns the repository's calendar source, which caches
// parsed calendars for every request, or a source for the configured
// directory when the repository has none.
func (a *API) calendarSource() *spec.CalendarSource {
	if source := a.dagRepository.Calendars(); source != nil {
		return source
	}
	return spec.NewCalendarSource(a.config.Paths.CalendarsDir)
}

// requireCalendarWrite checks that the caller may change calendar files,
// which change the business days of DAGs in every workspace.
func (a *API) requireCalendarWrite(ctx context.Context) error {
	if err := a.isAllowed(config.PermissionWriteDAGs); err != nil {
		return err
	}
	if err := a.requireDeveloperOrAbove(ctx); err != nil {
		return err
	}
	if a.calendarStore == nil {
		return errCalendarsNotAvailable
	}
	return nil
}

// getCalendarFile returns the content of a calendar file, or a not-found
// error.
func (a *API) getCalendarFile(ctx context.Context, name string) ([]byte, error) {
	if err := ir.ValidateCalendarName(name); err != nil {
		return nil, NewAPIError(http.StatusBadRequest, api.ErrorCodeBadRequest, err)
	}
	if a.calendarStore == nil {
		return nil, a.calendarStoreError(name, persis.ErrCalendarNotFound)
	}
	data, err := a.calendarStore.Get(ctx, name)
	if err != nil {
		return nil, a.calendarStoreError(name, err)
	}
	return data, nil
}

func (a *API) calendarStoreError(name string, err error) error {
	if errors.Is(err, persis.ErrCalendarNotFound) {
		return &Error{
			HTTPStatus: http.StatusNotFound,
			Code:       api.ErrorCodeNotFound,
			Message:    fmt.Sprintf("calendar %q not found", name),
		}
	}
	return err
}

func (a *API) logCalendarAudit(ctx context.Context, action, name string) {
	a.logAudit(ctx, audit.CategorySystem, action, map[string]any{
		"resource_type": "calendar",
		"resource_id":   name,
	})
}

// calendarToday returns today's date in the server's time zone, which
// calendar expiry is measured from.
func (a *API) calendarToday() ir.CivilDate {
	location := time.Local
	if a.config != nil && a.config.Core.Location != nil {
		location = a.config.Core.Location
	}
	return ir.DateOf(time.Now().In(location))
}

func calendarWarnings(cal *ir.Calendar, today ir.CivilDate) []string {
	if warning := spec.CalendarExpiryWarning(cal.Name, cal.Through, today); warning != "" {
		return []string{warning}
	}
	return []string{}
}

// calendarUsage records which DAGs use which calendars, and which calendars
// include which.
type calendarUsage map[string]*calendarUse

type calendarUse struct {
	// includes are the calendars whose holidays this one adds.
	includes []string
	// dags name the calendar in a start schedule.
	dags []persis.DAGListItem
}

// calendarUsage reads the DAG catalog, with the DAGs of the extra search
// directories and limited by filter, and the include lists of summaries.
func (a *API) calendarUsage(ctx context.Context, summaries []spec.CalendarSummary, filter *workspace.WorkspaceFilter) (calendarUsage, error) {
	usage := calendarUsage{}
	use := func(name string) *calendarUse {
		if usage[name] == nil {
			usage[name] = &calendarUse{}
		}
		return usage[name]
	}
	for _, summary := range summaries {
		use(summary.Name).includes = summary.Includes
	}
	paginator := pagination.NewPaginator(1, math.MaxInt)
	result, _, err := a.dagRepository.ListIncludingSearchPaths(ctx, persis.DAGListOptions{Paginator: &paginator, WorkspaceFilter: filter})
	if err != nil {
		return nil, err
	}
	for _, item := range result.Items {
		var named []string
		for _, schedule := range item.Schedule {
			if schedule.HasCalendar() && !slices.Contains(named, schedule.Calendar) {
				named = append(named, schedule.Calendar)
				use(schedule.Calendar).dags = append(use(schedule.Calendar).dags, item)
			}
		}
	}
	return usage, nil
}

// dependents returns, sorted, the calendars that add name's holidays,
// directly or through other calendars.
func (u calendarUsage) dependents(name string) []string {
	seen := map[string]bool{name: true}
	var dependents []string
	for queue := []string{name}; len(queue) > 0; queue = queue[1:] {
		for other, use := range u {
			if !seen[other] && slices.Contains(use.includes, queue[0]) {
				seen[other] = true
				dependents = append(dependents, other)
				queue = append(queue, other)
			}
		}
	}
	slices.Sort(dependents)
	return dependents
}

// users returns the DAGs that use calendar name, sorted by file name. A
// DAG that uses it only through an including calendar names that calendar
// as via.
func (u calendarUsage) users(name string) []api.CalendarUser {
	seen := map[string]bool{}
	var users []api.CalendarUser
	add := func(calendar, via string) {
		use := u[calendar]
		if use == nil {
			return
		}
		for _, dag := range use.dags {
			if seen[dag.ID] {
				continue
			}
			seen[dag.ID] = true
			user := api.CalendarUser{FileName: dag.ID, Name: dag.Name}
			if ws := dagWorkspaceName(dag.DAG); ws != "" {
				user.Workspace = ptrOf(ws)
			}
			if via != "" {
				user.Via = ptrOf(via)
			}
			users = append(users, user)
		}
	}
	add(name, "")
	for _, dependent := range u.dependents(name) {
		add(dependent, dependent)
	}
	slices.SortFunc(users, func(a, b api.CalendarUser) int { return strings.Compare(a.FileName, b.FileName) })
	return users
}

// inUse explains why calendar name cannot be deleted, naming only the DAGs
// that visible matches, or returns "".
func (u calendarUsage) inUse(name string, visible *workspace.WorkspaceFilter) string {
	var reasons []string
	if dependents := u.dependents(name); len(dependents) > 0 {
		reasons = append(reasons, "included by calendar "+strings.Join(dependents, ", "))
	}
	users := u.users(name)
	if len(users) > 0 {
		var names []string
		hidden := 0
		for _, user := range users {
			if !visible.MatchesLabels(u.labels(user.FileName)) || len(names) == calendarInUseListLimit {
				hidden++
				continue
			}
			names = append(names, user.FileName)
		}
		reason := "used by DAG " + strings.Join(names, ", ")
		switch {
		case len(names) == 0:
			reason = fmt.Sprintf("used by %d DAGs", hidden)
		case hidden > 0:
			reason += fmt.Sprintf(" and %d more", hidden)
		}
		reasons = append(reasons, reason)
	}
	return strings.Join(reasons, "; ")
}

func (u calendarUsage) labels(fileName string) ir.Labels {
	for _, use := range u {
		for _, dag := range use.dags {
			if dag.ID == fileName {
				return dag.Labels
			}
		}
	}
	return nil
}

func sameCalendarDay(a, b ir.CalendarDay) bool {
	return a.Status == b.Status && a.HolidayName == b.HolidayName
}

func toAPICalendarDay(day ir.CalendarDay, changed bool) api.CalendarDay {
	result := api.CalendarDay{
		Date:    toAPIDate(day.Date),
		Status:  toAPICalendarDayStatus(day.Status),
		Changed: changed,
	}
	if day.HolidayName != "" {
		result.HolidayName = ptrOf(day.HolidayName)
	}
	if day.FromStart != 0 {
		result.Ordinal = ptrOf(day.FromStart)
		result.OrdinalFromEnd = ptrOf(day.FromEnd)
	}
	return result
}

func toAPICalendarDayStatus(status ir.DayStatus) api.CalendarDayStatus {
	switch status {
	case ir.DayBusiness:
		return api.CalendarDayStatusBusiness
	case ir.DayNonBusiness:
		return api.CalendarDayStatusNonBusiness
	case ir.DayHoliday:
		return api.CalendarDayStatusHoliday
	case ir.DayExtraBusiness:
		return api.CalendarDayStatusExtraBusinessDay
	default:
		return api.CalendarDayStatusNonBusiness
	}
}

func toAPICalendarChanges(changes ir.CalendarChanges) api.CalendarChanges {
	result := api.CalendarChanges{
		WeekdaysAdded:   toAPICalendarWeekdays(changes.WeekdaysAdded),
		WeekdaysRemoved: toAPICalendarWeekdays(changes.WeekdaysRemoved),
		Dates:           make([]api.CalendarDateChange, 0, len(changes.Dates)),
	}
	for _, change := range changes.Dates {
		dateChange := api.CalendarDateChange{
			Date:   toAPIDate(change.After.Date),
			Before: toAPICalendarDayStatus(change.Before.Status),
			After:  toAPICalendarDayStatus(change.After.Status),
		}
		if change.Before.HolidayName != "" {
			dateChange.BeforeHolidayName = ptrOf(change.Before.HolidayName)
		}
		if change.After.HolidayName != "" {
			dateChange.AfterHolidayName = ptrOf(change.After.HolidayName)
		}
		result.Dates = append(result.Dates, dateChange)
	}
	return result
}

func toAPICalendarWeekdays(weekdays []time.Weekday) []api.CalendarWeekday {
	result := make([]api.CalendarWeekday, 0, len(weekdays))
	for _, weekday := range weekdays {
		result = append(result, api.CalendarWeekday(strings.ToUpper(weekday.String()[:3])))
	}
	return result
}

func toAPIDate(date ir.CivilDate) openapitypes.Date {
	return openapitypes.Date{Time: time.Date(date.Year, date.Month, date.Day, 0, 0, 0, 0, time.UTC)}
}

// nonNil returns items, or an empty slice for nil, so required lists encode
// as [] rather than null.
func nonNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}
