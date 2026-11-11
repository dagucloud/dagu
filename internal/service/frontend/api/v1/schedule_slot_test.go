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
