// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	localapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A caller sees the changes of the runs of its workspaces, in the response
// and in the topic alike.
func TestDAGRunChangesFollowsTheCaller(t *testing.T) {
	t.Parallel()
	helper := test.Setup(t)
	apiImpl := newDAGPinAPIWithStore(helper, nil, localapi.WithAuthService(stubAuthService{}),
		localapi.WithEventService(eventstore.New(nil)))
	// The first read of the run events starts the feed.
	apiImpl.DAGRunsChanged(nil)
	testutil.WaitForClockTick(t)
	apiImpl.DAGRunsChanged([]*eventstore.DAGRunStatusSnapshot{
		{Name: "ops_dag", DAGFile: "ops_dag", Labels: []string{"workspace=ops"}},
		{Name: "dev_dag", DAGFile: "dev_dag", Labels: []string{"workspace=dev"}},
	})
	ctx := auth.WithUser(context.Background(), &auth.User{
		Username: "viewer", Role: auth.RoleViewer,
		WorkspaceAccess: &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{{Workspace: "ops", Role: auth.RoleViewer}}},
	})

	resp, err := apiImpl.GetDAGRunChanges(ctx, api.GetDAGRunChangesRequestObject{})
	require.NoError(t, err)
	changes := api.DAGRunChanges(resp.(api.GetDAGRunChanges200JSONResponse))
	require.Len(t, changes.Changes, 1)
	assert.Equal(t, "ops_dag", changes.Changes[0].FileName)

	data, err := apiImpl.GetDAGRunChangesData(ctx, "all")
	require.NoError(t, err)
	assert.Equal(t, changes.Changes, data.(api.DAGRunChanges).Changes)
}

func TestDAGRunChangesEndpoint(t *testing.T) {
	server := test.SetupServer(t)
	var changes api.DAGRunChanges
	server.Client().Get("/api/v1/dag-run-changes").ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &changes)
	assert.False(t, changes.ReadAt.IsZero())
	assert.False(t, changes.Since.IsZero())
	assert.NotNil(t, changes.Changes)
}

// Only the run events feed the change feed: a server without the event store
// answers 501, and a list then draws no activity.
func TestDAGRunChangesNeedTheEventStore(t *testing.T) {
	t.Parallel()
	apiImpl := newDAGPinAPIWithStore(test.Setup(t), nil)

	_, err := apiImpl.GetDAGRunChanges(context.Background(), api.GetDAGRunChangesRequestObject{})

	requireAPIErrorStatus(t, err, http.StatusNotImplemented)
}
