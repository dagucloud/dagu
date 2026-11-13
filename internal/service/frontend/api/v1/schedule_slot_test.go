// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartForScheduleSlot(t *testing.T) {
	server := test.SetupServer(t)

	spec := `
schedule: "CRON_TZ=Asia/Tokyo */30 * * * *"
steps:
  - name: noop
    run: "true"
`
	_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: "slot_dag",
		Spec: &spec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	slotOf := func(value string) *api.ScheduleSlotValue { return &value }
	runDetails := func(t *testing.T, dagRunID string) api.DAGRunDetails {
		t.Helper()
		var details api.GetDAGDAGRunDetails200JSONResponse
		server.Client().Get(fmt.Sprintf("/api/v1/dags/slot_dag/dag-runs/%s", dagRunID)).
			ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &details)
		return details.DagRun
	}

	t.Run("StartRecordsTheSlotWindow", func(t *testing.T) {
		var resp api.ExecuteDAG200JSONResponse
		server.Client().Post("/api/v1/dags/slot_dag/start", api.ExecuteDAGJSONRequestBody{
			ScheduleTime: slotOf("2026-03-13T12:00"),
		}).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &resp)

		require.NotNil(t, resp.ScheduleTime)
		assert.True(t, resp.ScheduleTime.Equal(time.Date(2026, 3, 13, 3, 0, 0, 0, time.UTC)))
		assert.Nil(t, resp.Warnings)

		waitForDAGRunStatus(t, server, "slot_dag", resp.DagRunId, 10*time.Second, func(status *ir.DAGRunStatus) bool {
			return status.Status == ir.Succeeded
		})
		details := runDetails(t, resp.DagRunId)
		assert.Equal(t, "2026-03-13T02:30:00Z", valueOrEmpty(details.IntervalStart))
		assert.Equal(t, "2026-03-13T03:30:00Z", valueOrEmpty(details.NextScheduledAt))
		assert.Equal(t, "Asia/Tokyo", valueOrEmpty(details.ScheduleTimezone))
	})

	t.Run("StartWarnsOnTimeNoScheduleFires", func(t *testing.T) {
		var resp api.ExecuteDAG200JSONResponse
		server.Client().Post("/api/v1/dags/slot_dag/start", api.ExecuteDAGJSONRequestBody{
			ScheduleTime: slotOf("2026-03-13T12:10"),
		}).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &resp)

		require.NotNil(t, resp.Warnings)
		require.Len(t, *resp.Warnings, 1)
		assert.Contains(t, (*resp.Warnings)[0], "is not a fire time of the schedule")
		waitForDAGRunStatus(t, server, "slot_dag", resp.DagRunId, 10*time.Second, func(status *ir.DAGRunStatus) bool {
			return status.Status == ir.Succeeded
		})
	})

	t.Run("StartRejectsFutureSlot", func(t *testing.T) {
		server.Client().Post("/api/v1/dags/slot_dag/start", api.ExecuteDAGJSONRequestBody{
			ScheduleTime: slotOf("2999-01-01T00:00"),
		}).ExpectStatus(http.StatusBadRequest).Send(t)
	})

	t.Run("DetailsListRecentFires", func(t *testing.T) {
		var details api.GetDAGDetails200JSONResponse
		server.Client().Get("/api/v1/dags/slot_dag").ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &details)

		require.NotNil(t, details.Dag)
		require.NotNil(t, details.Dag.RecentFires)
		fires := *details.Dag.RecentFires
		require.Len(t, fires, 5)
		assert.Equal(t, "Asia/Tokyo", fires[0].Timezone)
		assert.False(t, fires[0].Time.After(time.Now()))
		assert.Equal(t, 30*time.Minute, fires[0].Time.Sub(fires[1].Time))
	})

	t.Run("RescheduleKeepsOrReplacesTheSlot", func(t *testing.T) {
		var started api.ExecuteDAG200JSONResponse
		server.Client().Post("/api/v1/dags/slot_dag/start", api.ExecuteDAGJSONRequestBody{
			ScheduleTime: slotOf("2026-03-13T12:00"),
		}).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &started)
		waitForDAGRunStatus(t, server, "slot_dag", started.DagRunId, 10*time.Second, func(status *ir.DAGRunStatus) bool {
			return status.Status == ir.Succeeded
		})
		reschedule := func(t *testing.T, body api.RescheduleDAGRunJSONRequestBody) api.DAGRunDetails {
			t.Helper()
			var resp api.RescheduleDAGRun200JSONResponse
			server.Client().Post(fmt.Sprintf("/api/v1/dag-runs/slot_dag/%s/reschedule", started.DagRunId), body).
				ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &resp)
			return runDetails(t, resp.DagRunId)
		}

		kept := reschedule(t, api.RescheduleDAGRunJSONRequestBody{})
		assert.Equal(t, "2026-03-13T02:30:00Z", valueOrEmpty(kept.IntervalStart))
		assert.Equal(t, "Asia/Tokyo", valueOrEmpty(kept.ScheduleTimezone))

		replaced := reschedule(t, api.RescheduleDAGRunJSONRequestBody{ScheduleTime: slotOf("2026-03-13T11:00")})
		assert.Equal(t, "2026-03-13T01:30:00Z", valueOrEmpty(replaced.IntervalStart))
	})

	// A dry run reports the new run's slot and creates no run, so a remote
	// restart can check it before stopping the running run.
	t.Run("RescheduleDryRunCreatesNoRun", func(t *testing.T) {
		var started api.ExecuteDAG200JSONResponse
		server.Client().Post("/api/v1/dags/slot_dag/start", api.ExecuteDAGJSONRequestBody{
			ScheduleTime: slotOf("2026-03-13T12:00"),
		}).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &started)
		waitForDAGRunStatus(t, server, "slot_dag", started.DagRunId, 10*time.Second, func(status *ir.DAGRunStatus) bool {
			return status.Status == ir.Succeeded
		})
		path := fmt.Sprintf("/api/v1/dag-runs/slot_dag/%s/reschedule", started.DagRunId)
		dryRun := true

		var plan api.RescheduleDAGRun200JSONResponse
		server.Client().Post(path, api.RescheduleDAGRunJSONRequestBody{
			ScheduleTime: slotOf("2026-03-13T11:00"),
			DryRun:       &dryRun,
		}).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &plan)

		require.NotNil(t, plan.ScheduleTime)
		assert.True(t, plan.ScheduleTime.Equal(time.Date(2026, 3, 13, 2, 0, 0, 0, time.UTC)))
		assert.False(t, plan.Queued)
		server.Client().Get(fmt.Sprintf("/api/v1/dags/slot_dag/dag-runs/%s", plan.DagRunId)).
			ExpectStatus(http.StatusNotFound).Send(t)

		server.Client().Post(path, api.RescheduleDAGRunJSONRequestBody{
			ScheduleTime: slotOf("2999-01-01T00:00"),
			DryRun:       &dryRun,
		}).ExpectStatus(http.StatusBadRequest).Send(t)
	})

	t.Run("EnqueueRecordsTheSlotWindow", func(t *testing.T) {
		var resp api.EnqueueDAGDAGRun200JSONResponse
		server.Client().Post("/api/v1/dags/slot_dag/enqueue", api.EnqueueDAGDAGRunJSONRequestBody{
			ScheduleTime: slotOf("2026-03-13T12:00"),
		}).ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &resp)

		require.NotNil(t, resp.ScheduleTime)
		assert.True(t, resp.ScheduleTime.Equal(time.Date(2026, 3, 13, 3, 0, 0, 0, time.UTC)))
		details := runDetails(t, resp.DagRunId)
		assert.Equal(t, "2026-03-13T02:30:00Z", valueOrEmpty(details.IntervalStart))
		assert.Equal(t, "Asia/Tokyo", valueOrEmpty(details.ScheduleTimezone))
	})
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// The API shows a slot's dates before a run exists: for a slot named or the
// default one, beside each upcoming run, and on the run once it records them.
func TestSlotDates(t *testing.T) {
	server := test.SetupServer(t)

	dated := `
schedule: "CRON_TZ=Asia/Tokyo 0 2 * * *"
dates:
  target_day: -1d
  window_days: { each: 1d }
params:
  - name: date
    eval: "${dates.target_day}"
  - name: target_dir
    eval: "/data/${dates.target_day}"
steps:
  - name: noop
    run: "true"
`
	profiled := `
schedule:
  - expression: "CRON_TZ=UTC 0 2 * * *"
    profile: night
dates:
  since: { from: interval_start }
steps:
  - name: noop
    run: "true"
`
	unscheduled := `
dates:
  since: { from: interval_start }
steps:
  - name: noop
    run: "true"
`
	for name, spec := range map[string]string{"dated_dag": dated, "unscheduled_dag": unscheduled, "profiled_dag": profiled} {
		_ = server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{Name: name, Spec: &spec}).
			ExpectStatus(http.StatusCreated).Send(t)
	}
	targetDay := func(dates *[]api.RunDateValue) string {
		t.Helper()
		require.NotNil(t, dates)
		for _, date := range *dates {
			if date.Name == "target_day" {
				return valueOrEmpty(date.Value)
			}
		}
		t.Fatalf("no target_day in %+v", *dates)
		return ""
	}

	t.Run("PreviewNamedSlot", func(t *testing.T) {
		var preview api.PreviewDAGSlot200JSONResponse
		server.Client().Get("/api/v1/dags/dated_dag/slot-preview?scheduledAt=2026-11-10").
			ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &preview)

		require.NotNil(t, preview.ScheduledAt)
		assert.True(t, preview.ScheduledAt.Equal(time.Date(2026, 11, 9, 17, 0, 0, 0, time.UTC)))
		assert.Equal(t, "Asia/Tokyo", valueOrEmpty(preview.Timezone))
		assert.False(t, preview.DefaultSlot)
		assert.Equal(t, []api.RunDateValue{
			{Name: "target_day", Value: new("2026-11-09")},
			{Name: "window_days", List: new(true), Values: &[]string{"2026-11-09"}},
		}, preview.Dates)
		// Only a param whose eval is one date reference shows a value.
		assert.Equal(t, &[]api.SlotParamValue{{Name: "date", Value: "2026-11-09"}}, preview.Params)
	})

	t.Run("PreviewDefaultSlot", func(t *testing.T) {
		var preview api.PreviewDAGSlot200JSONResponse
		server.Client().Get("/api/v1/dags/dated_dag/slot-preview").
			ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &preview)

		assert.True(t, preview.DefaultSlot)
		require.NotNil(t, preview.ScheduledAt)
		assert.False(t, preview.ScheduledAt.After(time.Now()))
	})

	t.Run("PreviewRefusesADateWithoutAWindow", func(t *testing.T) {
		server.Client().Get("/api/v1/dags/unscheduled_dag/slot-preview").
			ExpectStatus(http.StatusBadRequest).Send(t)
	})

	t.Run("UpcomingRunsCarryDates", func(t *testing.T) {
		var details api.GetDAGDetails200JSONResponse
		server.Client().Get("/api/v1/dags/dated_dag").ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &details)

		require.NotNil(t, details.Dag)
		require.NotNil(t, details.Dag.UpcomingRuns)
		next := (*details.Dag.UpcomingRuns)[0]
		tokyo, err := time.LoadLocation("Asia/Tokyo")
		require.NoError(t, err)
		assert.Equal(t, next.Time.In(tokyo).AddDate(0, 0, -1).Format(time.DateOnly), targetDay(next.Dates))
	})

	// A profile's schedule fires runs of that profile, so their dates come
	// from that profile's window.
	t.Run("UpcomingRunsOfAProfileSchedule", func(t *testing.T) {
		var details api.GetDAGDetails200JSONResponse
		server.Client().Get("/api/v1/dags/profiled_dag").ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &details)

		require.NotNil(t, details.Dag)
		require.NotNil(t, details.Dag.UpcomingRuns)
		next := (*details.Dag.UpcomingRuns)[0]
		require.NotNil(t, next.Dates, "the window of the night schedule gives since")
		assert.Equal(t, next.Time.AddDate(0, 0, -1).UTC().Format(time.DateOnly), valueOrEmpty((*next.Dates)[0].Value))
	})

	t.Run("RunRecordsItsDates", func(t *testing.T) {
		slot := "2026-11-10"
		var resp api.ExecuteDAG200JSONResponse
		server.Client().Post("/api/v1/dags/dated_dag/start", api.ExecuteDAGJSONRequestBody{ScheduleTime: &slot}).
			ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &resp)
		waitForDAGRunStatus(t, server, "dated_dag", resp.DagRunId, 10*time.Second, func(status *ir.DAGRunStatus) bool {
			return status.Status == ir.Succeeded
		})

		var details api.GetDAGDAGRunDetails200JSONResponse
		server.Client().Get(fmt.Sprintf("/api/v1/dags/dated_dag/dag-runs/%s", resp.DagRunId)).
			ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &details)
		assert.Equal(t, "2026-11-09", targetDay(details.DagRun.Dates))
		assert.Nil(t, details.DagRun.DefaultSlot)
	})
}
