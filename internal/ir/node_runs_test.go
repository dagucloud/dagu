// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/require"
)

// A child run that returned without finishing, as a queued child a retry
// reuses does, counts as failed, as the step counts it.
func TestNodeRunStatusOf(t *testing.T) {
	t.Parallel()

	for status, want := range map[ir.Status]ir.NodeRunStatus{
		ir.Succeeded:          ir.NodeRunSucceeded,
		ir.PartiallySucceeded: ir.NodeRunPartiallySucceeded,
		ir.Failed:             ir.NodeRunFailed,
		ir.Aborted:            ir.NodeRunAborted,
		ir.Waiting:            ir.NodeRunWaiting,
		ir.Rejected:           ir.NodeRunRejected,
		ir.Queued:             ir.NodeRunFailed,
		ir.Running:            ir.NodeRunFailed,
		ir.NotStarted:         ir.NodeRunFailed,
	} {
		require.Equal(t, want, ir.NodeRunStatusOf(status), status.String())
	}
}

func TestCountNodeRuns(t *testing.T) {
	t.Parallel()

	var runs []ir.NodeRun
	for _, status := range []ir.NodeRunStatus{
		ir.NodeRunNotStarted, ir.NodeRunRunning, ir.NodeRunWaiting,
		ir.NodeRunSucceeded, ir.NodeRunPartiallySucceeded,
		ir.NodeRunFailed, ir.NodeRunRejected, ir.NodeRunAborted, ir.NodeRunDisabled,
		ir.NodeRunRemoved,
	} {
		runs = append(runs, ir.NodeRun{Status: status})
	}
	require.Equal(t, ir.NodeRunCounts{
		Total: 10, NotStarted: 1, Running: 1, Waiting: 1, Succeeded: 2, Failed: 2, Aborted: 1, Disabled: 1, Removed: 1,
	}, ir.CountNodeRuns(runs))
}
