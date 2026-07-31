// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/stretchr/testify/assert"
)

func TestWorkerReportsClaim(t *testing.T) {
	t.Parallel()

	record := &exec.WorkerHeartbeatRecord{Stats: &exec.WorkerStats{
		RunningTasks: []*exec.RunningTask{{
			DAGRunID:   "parent-run",
			DAGName:    "parent",
			AttemptKey: "owner-key",
		}},
	}}
	childStatus := &exec.DAGRunStatus{Name: "child", DAGRunID: "child-run"}

	assert.True(t, workerReportsClaim(record, childStatus, "child-key", "owner-key"))
	assert.False(t, workerReportsClaim(record, childStatus, "child-key", "different-claim"))
}
