// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/aigateway"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// relayCapableClient is a coordinator client that has the calls of the relay.
type relayCapableClient struct {
	coordinator.Client
	check *coordinatorv1.CheckAIEndpointRequest
}

func (c *relayCapableClient) CheckAIEndpointTo(_ context.Context, _ serviceregistry.HostInfo, req *coordinatorv1.CheckAIEndpointRequest) (*coordinatorv1.CheckAIEndpointResponse, error) {
	c.check = req
	return &coordinatorv1.CheckAIEndpointResponse{Protocol: "anthropic"}, nil
}

func (c *relayCapableClient) RelayAIRequestTo(context.Context, serviceregistry.HostInfo) (coordinatorv1.CoordinatorService_RelayAIRequestClient, error) {
	return nil, assert.AnError
}

// The handler gives the steps of a run the client of the gateway, bound to the
// worker and to the attempt of the task.
func TestRemoteTaskHandlerGivesTheRunTheRelayClient(t *testing.T) {
	t.Parallel()

	client := &relayCapableClient{}
	h := &remoteTaskHandler{workerID: "worker-1", coordinatorClient: client}
	ctx := h.withAIRelay(context.Background(), &coordinatorv1.Task{AttemptKey: "key-1", AttemptId: "attempt-1"}, serviceregistry.HostInfo{})

	relay := aigateway.ClientFrom(ctx)
	require.NotNil(t, relay)
	_, err := relay.Check(ctx, aigateway.StepRef{DAGName: "fix-bug", StepName: "code"}, aigateway.EndpointTarget("cle1"), "claude")
	require.NoError(t, err)
	require.NotNil(t, client.check)
	assert.Equal(t, "worker-1", client.check.GetWorkerId())
	assert.Equal(t, "key-1", client.check.GetAttemptKey())
	assert.Equal(t, "attempt-1", client.check.GetAttemptId())
	assert.Equal(t, "fix-bug", client.check.GetDagName())
	assert.Equal(t, "code", client.check.GetStepName())
	assert.Equal(t, "endpoint", client.check.GetTarget().GetKind())
	assert.Equal(t, "cle1", client.check.GetTarget().GetName())
	assert.Equal(t, "claude", client.check.GetProvider())

	// A MCP server entry is a target of the kind mcp.
	_, err = relay.Check(ctx, aigateway.StepRef{DAGName: "fix-bug", StepName: "code"}, aigateway.MCPTarget("docs"), "")
	require.NoError(t, err)
	assert.Equal(t, "mcp", client.check.GetTarget().GetKind())
	assert.Equal(t, "docs", client.check.GetTarget().GetName())

	// A client with no call of the relay gives no gateway: the step fails with a clear reason.
	plain := &remoteTaskHandler{workerID: "worker-1", coordinatorClient: &captureCoordinatorClientForTest{}}
	assert.Nil(t, aigateway.ClientFrom(plain.withAIRelay(context.Background(), &coordinatorv1.Task{}, serviceregistry.HostInfo{})))
}

// Spec 094, "Version check": a worker announces the capability when its self-test
// passes. A worker of a test can announce another set.
func TestWorkerAnnouncesTheRelayCapability(t *testing.T) {
	t.Parallel()

	w := NewWorker("w1", 1, nil, nil, nil)
	w.capabilities = detectCapabilities(context.Background())
	assert.Equal(t, []string{ir.CapabilityAIRelay}, w.Capabilities())

	old := NewWorker("w2", 1, nil, nil, nil)
	old.SetCapabilities(nil)
	assert.Empty(t, old.Capabilities())
	assert.True(t, old.capabilitiesSet)
}
