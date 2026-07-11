// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/assert"
)

func TestLivenessAttemptLeaseFresh(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0).UTC()
	lease := &exec.DAGRunLease{
		AttemptKey:      "owner-key",
		WorkerID:        "worker-1",
		LastHeartbeatAt: now.Add(-time.Second).UnixMilli(),
	}

	assert.True(t, livenessAttemptLeaseFresh(lease, "owner-key", "worker-1", now, 3*time.Second))
	assert.False(t, livenessAttemptLeaseFresh(lease, "different-claim", "worker-1", now, 3*time.Second))
	assert.False(t, livenessAttemptLeaseFresh(lease, "owner-key", "worker-2", now, 3*time.Second))
	assert.False(t, livenessAttemptLeaseFresh(lease, "owner-key", "worker-1", now, 500*time.Millisecond))
}

func TestWorkerHeartbeatReportsLivenessAttempt(t *testing.T) {
	t.Parallel()

	record := &exec.WorkerHeartbeatRecord{Stats: &exec.WorkerStats{
		RunningTasks: []*exec.RunningTask{{
			DAGRunID:   "parent-run",
			DAGName:    "parent",
			AttemptKey: "owner-key",
		}},
	}}
	childStatus := &exec.DAGRunStatus{Name: "child", DAGRunID: "child-run"}

	assert.True(t, workerHeartbeatReportsAttempt(record, childStatus, "child-key", "owner-key"))
	assert.False(t, workerHeartbeatReportsAttempt(record, childStatus, "child-key", "different-claim"))
}
