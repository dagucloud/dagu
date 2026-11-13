// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package subflow_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator/subflow"
	"github.com/stretchr/testify/assert"
)

// Spec 092, "Dispatch": a child that needs a CLI provider always goes to the
// coordinator, also with worker_selector: local. A process with no coordinator
// runs it here (development mode).
func TestAgentChildRouting(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	agentReq := validSubWorkflowRequest()
	agentReq.WorkerSelector = map[string]string{"agent.provider.claude": "true"}
	localAgentReq := validSubWorkflowRequest()
	localAgentReq.DAG.ForceLocal = true
	localAgentReq.WorkerSelector = map[string]string{"agent.provider.claude": "true"}
	zoneAgentReq := validSubWorkflowRequest()
	zoneAgentReq.WorkerSelector = map[string]string{"agent.provider.claude": "true", "zone": "eu"}

	distributed := subflow.New(&mockDispatcher{}, config.ExecutionModeLocal)
	local := subflow.NewLocal(runtime.Manager{}, nil)
	assert.True(t, distributed.ShouldRun(ctx, agentReq))
	assert.True(t, distributed.ShouldRun(ctx, localAgentReq))
	assert.False(t, local.ShouldRun(ctx, agentReq))
	assert.False(t, local.ShouldRun(ctx, localAgentReq), "worker_selector: local does not keep an agent child here")

	devDistributed := subflow.New(&mockDispatcher{}, config.ExecutionModeLocal, subflow.WithLocalAgentRuns())
	devLocal := subflow.NewLocal(runtime.Manager{}, nil, subflow.WithLocalAgentRunsAllowed())
	assert.False(t, devDistributed.ShouldRun(ctx, agentReq))
	assert.True(t, devLocal.ShouldRun(ctx, agentReq))
	assert.True(t, devDistributed.ShouldRun(ctx, zoneAgentReq), "another label still sends the child to the coordinator")
	assert.False(t, devLocal.ShouldRun(ctx, zoneAgentReq))
}
