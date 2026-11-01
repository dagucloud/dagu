// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"path/filepath"
	"testing"

	openapiv1 "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
