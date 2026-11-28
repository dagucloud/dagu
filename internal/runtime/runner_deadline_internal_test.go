// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runctx"
)

// The run loop can end on the last node without taking its context's
// deadline, as when the agent stops a run on that deadline and the node ends
// with it. The run still reports the deadline, unless all its nodes
// succeeded. Each case starts the loop with the deadline passed and the node
// done, so the loop never selects.
func TestRunGraphLoopDeadlineAfterLastNode(t *testing.T) {
	for _, tc := range []struct {
		name       string
		nodeStatus ir.NodeStatus
		wantErr    error
		wantStatus ir.Status
	}{
		{name: "AbortedNode", nodeStatus: ir.NodeAborted, wantErr: context.DeadlineExceeded, wantStatus: ir.Aborted},
		{name: "SucceededNode", nodeStatus: ir.NodeSucceeded, wantStatus: ir.Succeeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := NewPlan(ir.Step{Name: "1"})
			require.NoError(t, err)
			for _, node := range plan.Nodes() {
				node.SetStatus(tc.nodeStatus)
			}
			r := New(&Config{})
			if tc.nodeStatus == ir.NodeAborted {
				r.setCanceled()
			}
			dagCtx := runctx.NewContext(context.Background(), &ir.DAG{Name: "test"}, "run-1", "test.log")
			ctx, cancel := context.WithDeadline(dagCtx, time.Unix(0, 0))
			defer cancel()

			r.runGraphLoop(ctx, plan, plan.Nodes(), make(chan ProgressUpdate, 1))

			assert.ErrorIs(t, r.lastError, tc.wantErr)
			assert.Equal(t, tc.wantStatus, r.Status(ctx, plan))
		})
	}
}
