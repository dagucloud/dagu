// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

func TestAPIRescheduleInlineStartUsesStoredSnapshot(t *testing.T) {
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Queues.Enabled = true
		cfg.Queues.Config = []config.QueueConfig{
			{Name: "intg_inline_reschedule_start", MaxActiveRuns: 1},
		}
	}))

	runID := test.CreateInlineDAGRunForReschedule(t, server, "intg_inline_reschedule_start", false)

	newRunID := rescheduleServerInlineRun(t, server, "intg_inline_reschedule_start", runID)
	test.ProcessQueuedInlineRun(t, server, "intg_inline_reschedule_start")
	test.AssertInlineRescheduledRunParams(t, server, "intg_inline_reschedule_start", newRunID)
}

func TestAPIRescheduleInlineEnqueueUsesStoredSnapshot(t *testing.T) {
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Queues.Enabled = true
		cfg.Queues.Config = []config.QueueConfig{
			{Name: "intg_inline_reschedule_enqueue", MaxActiveRuns: 1},
		}
	}))

	runID := test.CreateInlineDAGRunForReschedule(t, server, "intg_inline_reschedule_enqueue", true)

	newRunID := rescheduleServerInlineRun(t, server, "intg_inline_reschedule_enqueue", runID)
	test.ProcessQueuedInlineRun(t, server, "intg_inline_reschedule_enqueue")
	test.AssertInlineRescheduledRunParams(t, server, "intg_inline_reschedule_enqueue", newRunID)
}

func rescheduleServerInlineRun(t *testing.T, server test.Server, dagName, runID string) string {
	t.Helper()

	resp := server.Client().Post(
		fmt.Sprintf("/api/v1/dag-runs/%s/%s/reschedule", dagName, runID),
		api.RescheduleDAGRunJSONRequestBody{},
	).ExpectStatus(http.StatusOK).Send(t)

	var body api.RescheduleDAGRun200JSONResponse
	resp.Unmarshal(t, &body)
	require.NotEmpty(t, body.DagRunId)
	require.True(t, body.Queued)
	return body.DagRunId
}
