// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	filecalendar "github.com/dagucloud/dagu/v2/internal/persis/file/calendar"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	localapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/dagucloud/dagu/v2/internal/test"
	openapitypes "github.com/oapi-codegen/runtime/types"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newCalendarAPI(t *testing.T, helper test.Helper, opts ...localapi.APIOption) *localapi.API {
	t.Helper()
	store, err := filecalendar.New(helper.Config.Paths.CalendarsDir)
	require.NoError(t, err)
	return localapi.New(
		helper.DAGRepository,
		helper.DAGRunRepository,
		helper.QueueStore,
		helper.ProcRepository,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
		append([]localapi.APIOption{localapi.WithCalendarStore(store)}, opts...)...,
	)
}

func toDate(year int, month time.Month, day int) openapitypes.Date {
	return openapitypes.Date{Time: time.Date(year, month, day, 0, 0, 0, 0, time.UTC)}
}

func writeCalendarFile(t *testing.T, helper test.Helper, name, content string) {
	t.Helper()
	dir := helper.Config.Paths.CalendarsDir
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(content), 0o600))
}

func getCalendar(t *testing.T, ctx context.Context, apiImpl *localapi.API, name string) api.GetCalendar200JSONResponse {
	t.Helper()
	resp, err := apiImpl.GetCalendar(ctx, api.GetCalendarRequestObject{Name: name})
	require.NoError(t, err)
	details, ok := resp.(api.GetCalendar200JSONResponse)
	require.True(t, ok)
	return details
}

func updateCalendar(t *testing.T, ctx context.Context, apiImpl *localapi.API, name, content string) api.UpdateCalendar200JSONResponse {
	t.Helper()
	resp, err := apiImpl.UpdateCalendar(ctx, api.UpdateCalendarRequestObject{
		Name: name,
		Body: &api.UpdateCalendarJSONRequestBody{Spec: content},
	})
	require.NoError(t, err)
	result, ok := resp.(api.UpdateCalendar200JSONResponse)
	require.True(t, ok)
	return result
}

func TestCalendarLifecycle(t *testing.T) {
	t.Parallel()
	helper := test.Setup(t)
	apiImpl := newCalendarAPI(t, helper)
	ctx := context.Background()
	create := func(name string, content *string) error {
		_, err := apiImpl.CreateCalendar(ctx, api.CreateCalendarRequestObject{
			Body: &api.CreateCalendarJSONRequestBody{Name: name, Spec: content},
		})
		return err
	}

	require.NoError(t, create("ops", nil))
	requireAPIErrorStatus(t, create("ops", nil), http.StatusConflict)
	requireAPIErrorStatus(t, create("weekdays", nil), http.StatusBadRequest)
	broken := "holidays: [2027-13-01]\n"
	err := create("broken", &broken)
	requireAPIErrorStatus(t, err, http.StatusBadRequest)
	assert.ErrorContains(t, err, `[1:12] field 'holidays[0]': invalid date "2027-13-01"`)

	template := string(spec.CalendarTemplate(time.Now().In(helper.Config.Core.Location).Year()))
	details := getCalendar(t, ctx, apiImpl, "ops")
	assert.Equal(t, template, details.Spec)
	assert.Empty(t, details.Errors)

	// Content with problems is returned with them and not saved.
	result := updateCalendar(t, ctx, apiImpl, "ops", broken)
	assert.Equal(t, []string{`[1:12] field 'holidays[0]': invalid date "2027-13-01": use YYYY-MM-DD`}, result.Errors)
	assert.Equal(t, template, getCalendar(t, ctx, apiImpl, "ops").Spec)

	jpNational := "holidays:\n  - {date: 2027-02-11, name: National Foundation Day}\n"
	require.NoError(t, create("jp-national", &jpNational))
	result = updateCalendar(t, ctx, apiImpl, "ops", "description: Ops\ninclude: [jp-national]\nthrough: 2099-12-31\n")
	assert.Empty(t, result.Errors)
	details = getCalendar(t, ctx, apiImpl, "ops")
	assert.Equal(t, "Ops", *details.Description)
	assert.Equal(t, []string{"jp-national"}, details.Includes)
	assert.Equal(t, []string{"ops"}, getCalendar(t, ctx, apiImpl, "jp-national").IncludedBy)

	_, err = apiImpl.UpdateCalendar(ctx, api.UpdateCalendarRequestObject{
		Name: "missing", Body: &api.UpdateCalendarJSONRequestBody{Spec: "holidays: []\n"},
	})
	requireAPIErrorStatus(t, err, http.StatusNotFound)
	_, err = apiImpl.UpdateCalendar(ctx, api.UpdateCalendarRequestObject{
		Name: "weekdays", Body: &api.UpdateCalendarJSONRequestBody{Spec: "holidays: []\n"},
	})
	requireAPIErrorStatus(t, err, http.StatusBadRequest)
	_, err = apiImpl.GetCalendar(ctx, api.GetCalendarRequestObject{Name: "missing"})
	requireAPIErrorStatus(t, err, http.StatusNotFound)
	assert.True(t, getCalendar(t, ctx, apiImpl, "weekdays").Builtin)
}

// A DAG uses a calendar through its start schedules, directly or through a
// calendar that includes it. Callers see only the DAGs of their workspaces,
// but a calendar in use by any DAG cannot be deleted.
func TestCalendarUsage(t *testing.T) {
	t.Parallel()
	helper := test.Setup(t)
	apiImpl := newCalendarAPI(t, helper, localapi.WithAuthService(stubAuthService{}))
	writeCalendarFile(t, helper, "ops", "holidays: [2027-01-11]\n")
	writeCalendarFile(t, helper, "wrapper", "include: [ops]\n")
	writeCalendarFile(t, helper, "spare", "holidays: []\n")
	// A calendar that does not load still names what it includes.
	writeCalendarFile(t, helper, "broken", "include: [ops, missing]\n")
	dags := map[string]string{
		"direct": "schedule:\n  - expression: \"0 9 * * *\"\n    calendar: ops\n",
		"nested": "calendar: wrapper\nschedule: \"0 9 * * *\"\n",
		"hidden": "labels: [workspace=secret]\ncalendar: ops\nschedule: \"0 9 * * *\"\n",
		"plain":  "schedule: \"0 9 * * *\"\n",
	}
	for name, definition := range dags {
		helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, name, []byte(definition+"steps:\n  - run: echo hi\n"))
	}
	admin := auth.WithUser(context.Background(), &auth.User{Username: "admin", Role: auth.RoleAdmin, WorkspaceAccess: auth.AllWorkspaceAccess()})
	scoped := auth.WithUser(context.Background(), &auth.User{
		Username: "dev",
		Role:     auth.RoleDeveloper,
		WorkspaceAccess: &auth.WorkspaceAccess{
			Grants: []auth.WorkspaceGrant{{Workspace: "team", Role: auth.RoleDeveloper}},
		},
	})

	details := getCalendar(t, admin, apiImpl, "ops")
	assert.Equal(t, []string{"broken", "wrapper"}, details.IncludedBy)
	assert.Equal(t, []api.CalendarUser{
		{FileName: "direct", Name: "direct"},
		{FileName: "hidden", Name: "hidden", Workspace: new("secret")},
		{FileName: "nested", Name: "nested", Via: new("wrapper")},
	}, details.UsedBy)

	users := getCalendar(t, scoped, apiImpl, "ops").UsedBy
	require.Len(t, users, 2)
	assert.Equal(t, "direct", users[0].FileName)
	assert.Equal(t, "nested", users[1].FileName)

	resp, err := apiImpl.ListCalendars(admin, api.ListCalendarsRequestObject{
		Params: api.ListCalendarsParams{IncludeUsage: new(true)},
	})
	require.NoError(t, err)
	counts := map[string]int{}
	for _, calendar := range resp.(api.ListCalendars200JSONResponse).Calendars {
		require.NotNil(t, calendar.DagCount, calendar.Name)
		counts[calendar.Name] = *calendar.DagCount
	}
	assert.Equal(t, map[string]int{"ops": 3, "wrapper": 1, "spare": 0, "broken": 0, "weekdays": 0}, counts)

	_, err = apiImpl.DeleteCalendar(scoped, api.DeleteCalendarRequestObject{Name: "ops"})
	requireAPIErrorStatus(t, err, http.StatusConflict)
	assert.ErrorContains(t, err, `calendar "ops" is in use: included by calendar broken, wrapper; used by DAG direct, nested and 1 more`)

	_, err = apiImpl.DeleteCalendar(scoped, api.DeleteCalendarRequestObject{Name: "spare"})
	require.NoError(t, err)
	_, err = apiImpl.GetCalendar(admin, api.GetCalendarRequestObject{Name: "spare"})
	requireAPIErrorStatus(t, err, http.StatusNotFound)
}

// DAGs found through the extra DAG search directories count as users too,
// since the UI lists them.
func TestCalendarUsageIncludesSearchPaths(t *testing.T) {
	t.Parallel()
	baseDir, altDir, calendarsDir := t.TempDir(), t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(altDir, "alt-dag.yaml"),
		[]byte("calendar: ops\nschedule: \"0 9 * * *\"\nsteps:\n  - run: echo hi\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(calendarsDir, "ops.yaml"), []byte("holidays: []\n"), 0o600))
	cfg := &config.Config{}
	cfg.Paths.DAGsDir = baseDir
	cfg.Paths.AltDAGsDir = altDir
	cfg.Paths.CalendarsDir = calendarsDir
	cfg.Core.SkipExamples = true
	cfg.Server.Permissions = map[config.Permission]bool{config.PermissionWriteDAGs: true}
	repo, err := persisfile.NewDAGRepository(cfg, persisfile.WithDAGSearchPaths([]string{altDir}))
	require.NoError(t, err)
	store, err := filecalendar.New(calendarsDir)
	require.NoError(t, err)
	apiImpl := localapi.New(repo, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil,
		localapi.WithCalendarStore(store))

	_, err = apiImpl.DeleteCalendar(context.Background(), api.DeleteCalendarRequestObject{Name: "ops"})
	requireAPIErrorStatus(t, err, http.StatusConflict)
	assert.ErrorContains(t, err, "used by DAG alt-dag")
}

func TestCalendarWritePermissions(t *testing.T) {
	t.Parallel()

	t.Run("viewers read but do not write", func(t *testing.T) {
		t.Parallel()
		helper := test.Setup(t)
		apiImpl := newCalendarAPI(t, helper, localapi.WithAuthService(stubAuthService{}))
		writeCalendarFile(t, helper, "ops", "holidays: []\n")
		viewer := auth.WithUser(context.Background(), &auth.User{Username: "viewer", Role: auth.RoleViewer, WorkspaceAccess: auth.AllWorkspaceAccess()})

		getCalendar(t, viewer, apiImpl, "ops")
		_, err := apiImpl.PreviewCalendar(viewer, api.PreviewCalendarRequestObject{
			Name: "ops", Body: &api.PreviewCalendarJSONRequestBody{Spec: "holidays: []\n", Year: 2027},
		})
		require.NoError(t, err)

		_, err = apiImpl.CreateCalendar(viewer, api.CreateCalendarRequestObject{Body: &api.CreateCalendarJSONRequestBody{Name: "new"}})
		requireAPIErrorStatus(t, err, http.StatusForbidden)
		_, err = apiImpl.UpdateCalendar(viewer, api.UpdateCalendarRequestObject{Name: "ops", Body: &api.UpdateCalendarJSONRequestBody{Spec: "holidays: []\n"}})
		requireAPIErrorStatus(t, err, http.StatusForbidden)
		_, err = apiImpl.DeleteCalendar(viewer, api.DeleteCalendarRequestObject{Name: "ops"})
		requireAPIErrorStatus(t, err, http.StatusForbidden)
	})

	t.Run("the write_dags permission is required", func(t *testing.T) {
		t.Parallel()
		helper := test.Setup(t, test.WithConfigMutator(func(cfg *config.Config) {
			cfg.Server.Permissions[config.PermissionWriteDAGs] = false
		}))
		apiImpl := newCalendarAPI(t, helper)

		_, err := apiImpl.CreateCalendar(context.Background(), api.CreateCalendarRequestObject{Body: &api.CreateCalendarJSONRequestBody{Name: "new"}})
		requireAPIErrorStatus(t, err, http.StatusForbidden)
	})
}

func TestPreviewCalendar(t *testing.T) {
	t.Parallel()
	helper := test.Setup(t)
	apiImpl := newCalendarAPI(t, helper)
	writeCalendarFile(t, helper, "ops", "holidays:\n  - {date: 2027-01-11, name: Coming of Age Day}\nthrough: 2099-12-31\n")
	preview := func(name, content string, year int) api.PreviewCalendar200JSONResponse {
		t.Helper()
		resp, err := apiImpl.PreviewCalendar(context.Background(), api.PreviewCalendarRequestObject{
			Name: name, Body: &api.PreviewCalendarJSONRequestBody{Spec: content, Year: year},
		})
		require.NoError(t, err)
		result, ok := resp.(api.PreviewCalendar200JSONResponse)
		require.True(t, ok)
		return result
	}
	day := func(result api.PreviewCalendar200JSONResponse, month time.Month, dayOfMonth int) api.CalendarDay {
		t.Helper()
		for _, d := range result.Days {
			if d.Date.Month() == month && d.Date.Day() == dayOfMonth {
				return d
			}
		}
		t.Fatalf("no day %d-%d", month, dayOfMonth)
		return api.CalendarDay{}
	}

	// The draft moves the holiday a day later and has one bad entry, which
	// is reported while the valid entries still apply.
	result := preview("ops", "holidays:\n  - {date: 2027-01-12, name: Moved}\n  - 2027-13-01\nthrough: 2099-12-31\n", 2027)
	assert.Equal(t, []string{`[3:5] field 'holidays[1]': invalid date "2027-13-01": use YYYY-MM-DD`}, result.Errors)
	assert.Empty(t, result.Warnings)
	require.Len(t, result.Days, 365)
	assert.Equal(t, api.CalendarDay{
		Date: toDate(2027, 1, 11), Status: api.CalendarDayStatusBusiness, Changed: true,
		Ordinal: new(7), OrdinalFromEnd: new(-14),
	}, day(result, time.January, 11))
	assert.Equal(t, api.CalendarDay{
		Date: toDate(2027, 1, 12), Status: api.CalendarDayStatusHoliday, HolidayName: new("Moved"), Changed: true,
	}, day(result, time.January, 12))
	assert.False(t, day(result, time.January, 13).Changed)
	assert.Equal(t, []api.CalendarDateChange{
		{Date: toDate(2027, 1, 11), Before: api.CalendarDayStatusHoliday, BeforeHolidayName: new("Coming of Age Day"), After: api.CalendarDayStatusBusiness},
		{Date: toDate(2027, 1, 12), Before: api.CalendarDayStatusBusiness, After: api.CalendarDayStatusHoliday, AfterHolidayName: new("Moved")},
	}, result.Changes.Dates)

	result = preview("ops", "weekdays: [MON, TUE, WED, THU, FRI, SAT]\nthrough: 2000-12-31\n", 2028)
	require.Len(t, result.Days, 366)
	assert.Equal(t, []api.CalendarWeekday{api.CalendarWeekdaySAT}, result.Changes.WeekdaysAdded)
	require.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "lists holidays through 2000-12-31")

	result = preview("weekdays", "ignored: true\n", 2027)
	assert.Empty(t, result.Errors)
	assert.Equal(t, api.CalendarDayStatusNonBusiness, day(result, time.January, 2).Status)
	assert.Empty(t, result.Changes.Dates)

	// The request schema bounds the year, but the handler holds without
	// strict validation too.
	for _, year := range []int{0, 10000} {
		_, err := apiImpl.PreviewCalendar(context.Background(), api.PreviewCalendarRequestObject{
			Name: "ops", Body: &api.PreviewCalendarJSONRequestBody{Spec: "holidays: []\n", Year: year},
		})
		requireAPIErrorStatus(t, err, http.StatusBadRequest)
	}

	result = preview("ops", "- 2027-01-01\n", 2027)
	assert.Len(t, result.Errors, 1)
	assert.Empty(t, result.Days)
}

// The server wires the calendar store and routes, so a calendar created
// over HTTP is readable and previewable.
func TestCalendarHTTP(t *testing.T) {
	server := test.SetupServer(t)

	server.Client().Post("/api/v1/calendars", api.CreateCalendarJSONRequestBody{Name: "ops"}).
		ExpectStatus(http.StatusCreated).Send(t)

	var details api.GetCalendar200JSONResponse
	server.Client().Get("/api/v1/calendars/ops").ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &details)
	assert.NotEmpty(t, details.Spec)

	var preview api.PreviewCalendar200JSONResponse
	server.Client().Post("/api/v1/calendars/ops/preview", api.PreviewCalendarJSONRequestBody{Spec: details.Spec, Year: 2027}).
		ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &preview)
	assert.Len(t, preview.Days, 365)

	server.Client().Get("/api/v1/calendars/no.such").ExpectStatus(http.StatusBadRequest).Send(t)
	server.Client().Delete("/api/v1/calendars/ops").ExpectStatus(http.StatusNoContent).Send(t)
}
