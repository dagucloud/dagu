// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	openapiv1 "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// schedulerMembers reports fixed scheduler instances.
type schedulerMembers struct {
	serviceregistry.ServiceRegistry
	members []serviceregistry.HostInfo
}

func (r schedulerMembers) GetServiceMembers(context.Context, serviceregistry.ServiceName) ([]serviceregistry.HostInfo, error) {
	return r.members, nil
}

func TestSchedulerPauseStateReportsWhetherASchedulerRuns(t *testing.T) {
	pauseState := func(t *testing.T, registry serviceregistry.ServiceRegistry) openapiv1.SchedulerPauseState {
		t.Helper()
		a := &API{
			schedulerPauseStore: store.NewSchedulerPauseStore(file.NewCollection(filepath.Join(t.TempDir(), "pause"))),
			serviceRegistry:     registry,
		}
		resp, err := a.GetSchedulerPauseState(t.Context(), openapiv1.GetSchedulerPauseStateRequestObject{})
		require.NoError(t, err)
		state, ok := resp.(openapiv1.GetSchedulerPauseState200JSONResponse)
		require.True(t, ok)
		return openapiv1.SchedulerPauseState(state)
	}

	running := pauseState(t, schedulerMembers{members: []serviceregistry.HostInfo{
		{ID: "standby", Status: serviceregistry.ServiceStatusInactive},
		{ID: "leader", Status: serviceregistry.ServiceStatusActive},
	}})
	require.NotNil(t, running.SchedulerRunning)
	assert.True(t, *running.SchedulerRunning)

	stopped := pauseState(t, schedulerMembers{members: []serviceregistry.HostInfo{
		{ID: "standby", Status: serviceregistry.ServiceStatusInactive},
	}})
	require.NotNil(t, stopped.SchedulerRunning)
	assert.False(t, *stopped.SchedulerRunning)

	assert.Nil(t, pauseState(t, nil).SchedulerRunning, "unknown without a registry")

	// A member whose state the registry cannot tell may still be scheduling.
	unknown := pauseState(t, schedulerMembers{members: []serviceregistry.HostInfo{
		{ID: "standby", Status: serviceregistry.ServiceStatusInactive},
		{ID: "unreadable", Status: serviceregistry.ServiceStatusUnknown},
	}})
	assert.Nil(t, unknown.SchedulerRunning, "unknown while a member's state is unknown")
}

// h2cCallersClient answers the h2c callers of each coordinator by ID, and
// fails for the others as a coordinator of an earlier release does.
type h2cCallersClient struct {
	stubCoordinatorClient
	callers map[string][]*coordinatorv1.H2CCaller
	asked   sync.Map
}

func (c *h2cCallersClient) GetH2CCallers(_ context.Context, member serviceregistry.HostInfo) ([]*coordinatorv1.H2CCaller, error) {
	c.asked.Store(member.ID, true)
	callers, ok := c.callers[member.ID]
	if !ok {
		return nil, status.Error(codes.Unimplemented, "unknown method GetH2CCallers")
	}
	return callers, nil
}

// GET /services/coordinator lists each active coordinator's h2c callers, and
// leaves them out for one that cannot report them (Spec 108 §h2c Callers).
func TestCoordinatorStatusListsH2CCallers(t *testing.T) {
	client := &h2cCallersClient{callers: map[string][]*coordinatorv1.H2CCaller{
		"current": {{Host: "10.0.0.5", Reason: "peer.insecure", LastCallAt: 1_800_000_000}},
	}}
	a := &API{
		coordinatorCli: client,
		serviceRegistry: schedulerMembers{members: []serviceregistry.HostInfo{
			{ID: "current", Status: serviceregistry.ServiceStatusActive},
			{ID: "earlier-release", Status: serviceregistry.ServiceStatusActive},
			{ID: "stopped", Status: serviceregistry.ServiceStatusInactive},
		}},
	}

	resp, err := a.GetCoordinatorStatus(t.Context(), openapiv1.GetCoordinatorStatusRequestObject{})
	require.NoError(t, err)
	okResp, ok := resp.(openapiv1.GetCoordinatorStatus200JSONResponse)
	require.True(t, ok)
	require.Len(t, okResp.Coordinators, 3)

	require.NotNil(t, okResp.Coordinators[0].H2cCallers)
	assert.Equal(t, []openapiv1.H2CCaller{
		{Host: "10.0.0.5", Reason: openapiv1.PlaintextReasonPeerInsecure, LastCallAt: "2027-01-15T08:00:00Z"},
	}, *okResp.Coordinators[0].H2cCallers)
	assert.Nil(t, okResp.Coordinators[1].H2cCallers)
	assert.Nil(t, okResp.Coordinators[2].H2cCallers)
	_, asked := client.asked.Load("stopped")
	assert.False(t, asked, "an inactive coordinator is not asked")
}
