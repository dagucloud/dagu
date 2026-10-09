// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dagpin"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	localapi "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newDAGPinAPI returns an API whose pins are kept in memory.
func newDAGPinAPI(t *testing.T, helper test.Helper, opts ...localapi.APIOption) (*localapi.API, dagpin.Store) {
	t.Helper()
	pins, err := store.NewDAGPinStore(testutil.NewMemoryBackend().Collection(persis.CollectionDAGPins))
	require.NoError(t, err)
	return newDAGPinAPIWithStore(helper, pins, opts...), pins
}

func newDAGPinAPIWithStore(helper test.Helper, pins dagpin.Store, opts ...localapi.APIOption) *localapi.API {
	return localapi.New(
		helper.DAGRepository,
		helper.DAGRunRepository,
		helper.QueueStore,
		helper.ProcRepository,
		helper.DAGRunMgr,
		helper.Config,
		nil,
		helper.ServiceRegistry,
		nil,
		nil,
		append([]localapi.APIOption{localapi.WithDAGPinStore(pins)}, opts...)...,
	)
}

func writePinTestDAGs(t *testing.T, helper test.Helper, names ...string) {
	t.Helper()
	for _, name := range names {
		helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, name, []byte("steps:\n  - run: echo "+name+"\n"))
	}
}

func listedFileNames(dags []api.DAGFile) []string {
	names := make([]string, 0, len(dags))
	for _, dag := range dags {
		names = append(names, dag.FileName)
	}
	return names
}

func listedPinned(dags []api.DAGFile) []bool {
	pinned := make([]bool, 0, len(dags))
	for _, dag := range dags {
		pinned = append(pinned, dag.Pinned)
	}
	return pinned
}

func listDAGPage(t *testing.T, apiImpl *localapi.API, page, perPage int) *api.ListDAGs200JSONResponse {
	t.Helper()
	resp, err := apiImpl.ListDAGs(context.Background(), api.ListDAGsRequestObject{
		Params: api.ListDAGsParams{Page: &page, PerPage: &perPage},
	})
	require.NoError(t, err)
	list, ok := resp.(*api.ListDAGs200JSONResponse)
	require.True(t, ok, "expected 200 response, got %T", resp)
	return list
}

func requireAPIErrorStatus(t *testing.T, err error, status int) {
	t.Helper()
	apiErr, ok := errors.AsType[*localapi.Error](err)
	require.True(t, ok, "expected API error, got %v", err)
	assert.Equal(t, status, apiErr.HTTPStatus)
}

// The pin is stored under the file name without its extension, so pinning
// "gamma.yaml" pins the listed "gamma".
func TestListDAGsPinnedFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	helper := test.Setup(t, test.WithStatusPersistence())
	writePinTestDAGs(t, helper, "alpha", "beta", "gamma")
	apiImpl, _ := newDAGPinAPI(t, helper)

	_, err := apiImpl.PinDAG(ctx, api.PinDAGRequestObject{FileName: "gamma.yaml"})
	require.NoError(t, err)

	first := listDAGPage(t, apiImpl, 1, 2)
	assert.Equal(t, []string{"gamma", "alpha"}, listedFileNames(first.Dags))
	assert.Equal(t, []bool{true, false}, listedPinned(first.Dags))
	assert.Equal(t, 3, first.Pagination.TotalRecords)
	second := listDAGPage(t, apiImpl, 2, 2)
	assert.Equal(t, []string{"beta"}, listedFileNames(second.Dags))

	sseAny, err := apiImpl.GetDAGsListData(ctx, "page=1&perPage=2")
	require.NoError(t, err)
	sse, ok := sseAny.(api.ListDAGs200JSONResponse)
	require.True(t, ok)
	assert.Equal(t, listedFileNames(first.Dags), listedFileNames(sse.Dags))
	assert.Equal(t, listedPinned(first.Dags), listedPinned(sse.Dags))

	// MCP lists through the alternate-directory path, whose order ignores
	// pins while still reporting them.
	mcpAny, err := apiImpl.GetDAGsListDataIncludingAltDirs(ctx, "perPage=3")
	require.NoError(t, err)
	mcp, ok := mcpAny.(api.ListDAGs200JSONResponse)
	require.True(t, ok)
	assert.Equal(t, []string{"alpha", "beta", "gamma"}, listedFileNames(mcp.Dags))
	assert.Equal(t, []bool{false, false, true}, listedPinned(mcp.Dags))

	details, err := apiImpl.GetDAGDetails(ctx, api.GetDAGDetailsRequestObject{FileName: "gamma"})
	require.NoError(t, err)
	assert.True(t, details.(api.GetDAGDetails200JSONResponse).Pinned)

	for range 2 {
		resp, err := apiImpl.UnpinDAG(ctx, api.UnpinDAGRequestObject{FileName: "gamma"})
		require.NoError(t, err)
		assert.IsType(t, api.UnpinDAG204Response{}, resp)
	}
	assert.Equal(t, []string{"alpha", "beta", "gamma"}, listedFileNames(listDAGPage(t, apiImpl, 1, 3).Dags))
}

func TestPinDAGNotFound(t *testing.T) {
	t.Parallel()
	helper := test.Setup(t)
	apiImpl, _ := newDAGPinAPI(t, helper)

	_, err := apiImpl.PinDAG(context.Background(), api.PinDAGRequestObject{FileName: "missing"})
	requireAPIErrorStatus(t, err, http.StatusNotFound)
}

func TestPinDAGNotifiesDAGMutation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	helper := test.Setup(t)
	writePinTestDAGs(t, helper, "alpha")
	var notified []string
	apiImpl, _ := newDAGPinAPI(t, helper, localapi.WithDAGMutationNotifier(func(fileName string) {
		notified = append(notified, fileName)
	}))

	_, err := apiImpl.PinDAG(ctx, api.PinDAGRequestObject{FileName: "alpha"})
	require.NoError(t, err)
	_, err = apiImpl.UnpinDAG(ctx, api.UnpinDAGRequestObject{FileName: "alpha"})
	require.NoError(t, err)

	assert.Equal(t, []string{"alpha", "alpha"}, notified)
}

func TestPinDAGFollowsRenameAndDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	helper := test.Setup(t, test.WithStatusPersistence())
	writePinTestDAGs(t, helper, "alpha")
	apiImpl, pins := newDAGPinAPI(t, helper)
	_, err := apiImpl.PinDAG(ctx, api.PinDAGRequestObject{FileName: "alpha"})
	require.NoError(t, err)

	_, err = apiImpl.RenameDAG(ctx, api.RenameDAGRequestObject{
		FileName: "alpha",
		Body:     &api.RenameDAGJSONRequestBody{NewFileName: "omega"},
	})
	require.NoError(t, err)
	got, err := pins.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, map[string]struct{}{"omega": {}}, got)

	_, err = apiImpl.DeleteDAG(ctx, api.DeleteDAGRequestObject{FileName: "omega"})
	require.NoError(t, err)
	got, err = pins.List(ctx)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// Hidden DAGs answer 404 before the write check, so the caller cannot tell
// they exist.
func TestPinDAGWorkspaceAccess(t *testing.T) {
	t.Parallel()
	helper := test.Setup(t)
	helper.CreateDAGFile(t, helper.Config.Paths.DAGsDir, "ops-dag", []byte("labels: [workspace=ops]\nsteps:\n  - run: echo ops\n"))
	apiImpl, _ := newDAGPinAPI(t, helper, localapi.WithAuthService(stubAuthService{}))

	tests := []struct {
		name   string
		user   *auth.User
		status int
	}{
		{
			name:   "Viewer",
			user:   &auth.User{Username: "viewer", Role: auth.RoleViewer},
			status: http.StatusForbidden,
		},
		{
			name: "DeveloperInOtherWorkspace",
			user: &auth.User{
				Username: "other",
				Role:     auth.RoleDeveloper,
				WorkspaceAccess: &auth.WorkspaceAccess{
					Grants: []auth.WorkspaceGrant{{Workspace: "other", Role: auth.RoleDeveloper}},
				},
			},
			status: http.StatusNotFound,
		},
		{
			name: "DeveloperInWorkspace",
			user: &auth.User{
				Username: "ops",
				Role:     auth.RoleDeveloper,
				WorkspaceAccess: &auth.WorkspaceAccess{
					Grants: []auth.WorkspaceGrant{{Workspace: "ops", Role: auth.RoleDeveloper}},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := auth.WithUser(context.Background(), tt.user)
			_, err := apiImpl.PinDAG(ctx, api.PinDAGRequestObject{FileName: "ops-dag"})
			if tt.status == 0 {
				require.NoError(t, err)
				return
			}
			requireAPIErrorStatus(t, err, tt.status)
		})
	}
}

type failingDAGPinStore struct{ dagpin.Store }

func (failingDAGPinStore) List(context.Context) (map[string]struct{}, error) {
	return nil, errors.New("pin store unavailable")
}

func TestListDAGsIgnoresPinStoreError(t *testing.T) {
	t.Parallel()
	helper := test.Setup(t, test.WithStatusPersistence())
	writePinTestDAGs(t, helper, "alpha", "beta")
	apiImpl := newDAGPinAPIWithStore(helper, failingDAGPinStore{})

	list := listDAGPage(t, apiImpl, 1, 10)
	assert.Equal(t, []string{"alpha", "beta"}, listedFileNames(list.Dags))
	assert.Equal(t, []bool{false, false}, listedPinned(list.Dags))
}

// Pins never change DAG files, so Git sync read-only mode allows them.
func TestPinDAGAllowedInGitSyncReadOnlyMode(t *testing.T) {
	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.GitSync.Enabled = true
		cfg.GitSync.PushEnabled = false
	}))
	writePinTestDAGs(t, server.Helper, "alpha")

	server.Client().Put("/api/v1/dags/alpha/pin", nil).ExpectStatus(http.StatusNoContent).Send(t)

	var details api.GetDAGDetails200JSONResponse
	server.Client().Get("/api/v1/dags/alpha").ExpectStatus(http.StatusOK).Send(t).Unmarshal(t, &details)
	assert.True(t, details.Pinned)
}
