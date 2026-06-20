// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"testing"

	apigen "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/cmn/config"
	persiststore "github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	"github.com/dagucloud/dagu/internal/runtime"
	apiv1 "github.com/dagucloud/dagu/internal/service/frontend/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubAuthService satisfies apiv1.AuthService via an embedded nil interface so
// the API treats auth as enabled. Its methods are never invoked by the RBAC
// checks under test.
type stubAuthService struct{ apiv1.AuthService }

func newViewsTestAPI(t *testing.T, opts ...apiv1.APIOption) *apiv1.API {
	t.Helper()
	vs, err := persiststore.NewViewStore(testutil.NewMemoryBackend().Collection("views"))
	require.NoError(t, err)
	cfg := &config.Config{}
	allOpts := append([]apiv1.APIOption{apiv1.WithViewStore(vs)}, opts...)
	return apiv1.New(nil, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil, allOpts...)
}

func mustCreateView(t *testing.T, api *apiv1.API, ctx context.Context, spec apigen.ViewSpec) apigen.View {
	t.Helper()
	resp, err := api.CreateView(ctx, apigen.CreateViewRequestObject{Body: &spec})
	require.NoError(t, err)
	created, ok := resp.(apigen.CreateView201JSONResponse)
	require.True(t, ok, "expected 201, got %T", resp)
	return apigen.View(created)
}

func TestViewsAPI_CreateDefaultsAndActor(t *testing.T) {
	ctx := context.Background()
	api := newViewsTestAPI(t)

	created := mustCreateView(t, api, ctx, apigen.ViewSpec{
		Name:         "Prod",
		IntervalDays: 3,
		Labels:       &[]string{"team=platform"},
	})

	assert.NotEmpty(t, created.Id)
	assert.Equal(t, "Prod", created.Name)
	assert.Equal(t, "kanban", created.Type, "type defaults to kanban")
	require.NotNil(t, created.CreatedBy)
	assert.Equal(t, "admin", *created.CreatedBy, "no-auth actor is admin")
	require.NotNil(t, created.Labels)
	assert.Equal(t, []string{"team=platform"}, *created.Labels)
}

func TestViewsAPI_CreateValidation(t *testing.T) {
	ctx := context.Background()
	api := newViewsTestAPI(t)

	tests := []struct {
		name string
		spec apigen.ViewSpec
	}{
		{"empty name", apigen.ViewSpec{Name: "", IntervalDays: 3}},
		{"interval too large", apigen.ViewSpec{Name: "x", IntervalDays: 31}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := api.CreateView(ctx, apigen.CreateViewRequestObject{Body: &tt.spec})
			require.NoError(t, err)
			_, ok := resp.(apigen.CreateView400JSONResponse)
			assert.True(t, ok, "expected 400, got %T", resp)
		})
	}
}

func TestViewsAPI_CreateMissingBody(t *testing.T) {
	resp, err := newViewsTestAPI(t).CreateView(context.Background(), apigen.CreateViewRequestObject{})
	require.NoError(t, err)
	_, ok := resp.(apigen.CreateView400JSONResponse)
	assert.True(t, ok)
}

func TestViewsAPI_GetAndList(t *testing.T) {
	ctx := context.Background()
	api := newViewsTestAPI(t)
	created := mustCreateView(t, api, ctx, apigen.ViewSpec{Name: "V", IntervalDays: 5})

	getResp, err := api.GetView(ctx, apigen.GetViewRequestObject{ViewId: created.Id})
	require.NoError(t, err)
	got, ok := getResp.(apigen.GetView200JSONResponse)
	require.True(t, ok)
	assert.Equal(t, created.Id, got.Id)

	listResp, err := api.ListViews(ctx, apigen.ListViewsRequestObject{})
	require.NoError(t, err)
	listed, ok := listResp.(apigen.ListViews200JSONResponse)
	require.True(t, ok)
	require.Len(t, listed.Views, 1)
	assert.Equal(t, created.Id, listed.Views[0].Id)
}

func TestViewsAPI_GetNotFound(t *testing.T) {
	resp, err := newViewsTestAPI(t).GetView(context.Background(), apigen.GetViewRequestObject{ViewId: "missing"})
	require.NoError(t, err)
	_, ok := resp.(apigen.GetView404JSONResponse)
	assert.True(t, ok)
}

func TestViewsAPI_UpdatePreservesCreator(t *testing.T) {
	ctx := context.Background()
	api := newViewsTestAPI(t)
	created := mustCreateView(t, api, ctx, apigen.ViewSpec{Name: "Before", IntervalDays: 3})

	pinned := true
	resp, err := api.UpdateView(ctx, apigen.UpdateViewRequestObject{
		ViewId: created.Id,
		Body:   &apigen.ViewSpec{Name: "After", IntervalDays: 10, Pinned: &pinned},
	})
	require.NoError(t, err)
	updated, ok := resp.(apigen.UpdateView200JSONResponse)
	require.True(t, ok)
	assert.Equal(t, "After", updated.Name)
	assert.Equal(t, 10, updated.IntervalDays)
	require.NotNil(t, updated.Pinned)
	assert.True(t, *updated.Pinned)
	assert.Equal(t, created.CreatedAt, updated.CreatedAt, "CreatedAt preserved")
	require.NotNil(t, updated.CreatedBy)
	assert.Equal(t, *created.CreatedBy, *updated.CreatedBy, "CreatedBy preserved")
}

func TestViewsAPI_UpdateNotFound(t *testing.T) {
	resp, err := newViewsTestAPI(t).UpdateView(context.Background(), apigen.UpdateViewRequestObject{
		ViewId: "missing",
		Body:   &apigen.ViewSpec{Name: "x", IntervalDays: 3},
	})
	require.NoError(t, err)
	_, ok := resp.(apigen.UpdateView404JSONResponse)
	assert.True(t, ok)
}

func TestViewsAPI_Delete(t *testing.T) {
	ctx := context.Background()
	api := newViewsTestAPI(t)
	created := mustCreateView(t, api, ctx, apigen.ViewSpec{Name: "V", IntervalDays: 3})

	resp, err := api.DeleteView(ctx, apigen.DeleteViewRequestObject{ViewId: created.Id})
	require.NoError(t, err)
	_, ok := resp.(apigen.DeleteView204Response)
	assert.True(t, ok)

	getResp, err := api.GetView(ctx, apigen.GetViewRequestObject{ViewId: created.Id})
	require.NoError(t, err)
	_, ok = getResp.(apigen.GetView404JSONResponse)
	assert.True(t, ok)
}

func TestViewsAPI_DeleteNotFound(t *testing.T) {
	resp, err := newViewsTestAPI(t).DeleteView(context.Background(), apigen.DeleteViewRequestObject{ViewId: "missing"})
	require.NoError(t, err)
	_, ok := resp.(apigen.DeleteView404JSONResponse)
	assert.True(t, ok)
}

func TestViewsAPI_RBAC_WriteRequiresDeveloper(t *testing.T) {
	api := newViewsTestAPI(t, apiv1.WithAuthService(stubAuthService{}))
	spec := apigen.ViewSpec{Name: "x", IntervalDays: 3}

	viewerCtx := auth.WithUser(context.Background(), &auth.User{Username: "v", Role: auth.RoleViewer})
	_, err := api.CreateView(viewerCtx, apigen.CreateViewRequestObject{Body: &spec})
	require.Error(t, err, "viewer must be denied write")

	devCtx := auth.WithUser(context.Background(), &auth.User{Username: "d", Role: auth.RoleDeveloper})
	resp, err := api.CreateView(devCtx, apigen.CreateViewRequestObject{Body: &spec})
	require.NoError(t, err)
	_, ok := resp.(apigen.CreateView201JSONResponse)
	assert.True(t, ok, "developer is allowed to write")
}

func TestViewsAPI_WorkspaceScopedVisibility(t *testing.T) {
	api := newViewsTestAPI(t, apiv1.WithAuthService(stubAuthService{}))

	// Seed three views as a full-access admin.
	adminCtx := auth.WithUser(context.Background(), &auth.User{
		Username:        "admin",
		Role:            auth.RoleAdmin,
		WorkspaceAccess: &auth.WorkspaceAccess{All: true},
	})
	wsA := "a"
	wsB := "b"
	viewA := mustCreateView(t, api, adminCtx, apigen.ViewSpec{Name: "A", IntervalDays: 1, Workspace: &wsA})
	viewB := mustCreateView(t, api, adminCtx, apigen.ViewSpec{Name: "B", IntervalDays: 1, Workspace: &wsB})
	mustCreateView(t, api, adminCtx, apigen.ViewSpec{Name: "All", IntervalDays: 1}) // empty workspace

	// Restricted developer with access to workspace "a" only.
	devCtx := auth.WithUser(context.Background(), &auth.User{
		Username: "dev",
		Role:     auth.RoleDeveloper,
		WorkspaceAccess: &auth.WorkspaceAccess{
			Grants: []auth.WorkspaceGrant{{Workspace: "a", Role: auth.RoleDeveloper}},
		},
	})

	// List shows workspace "a" + all-workspace views, hides workspace "b".
	listResp, err := api.ListViews(devCtx, apigen.ListViewsRequestObject{})
	require.NoError(t, err)
	listed, ok := listResp.(apigen.ListViews200JSONResponse)
	require.True(t, ok)
	names := map[string]bool{}
	for _, v := range listed.Views {
		names[v.Name] = true
	}
	assert.True(t, names["A"], "accessible workspace view visible")
	assert.True(t, names["All"], "all-workspace view visible")
	assert.False(t, names["B"], "inaccessible workspace view must be hidden")

	// GetView on the inaccessible-workspace view returns 404 (existence hidden).
	getB, err := api.GetView(devCtx, apigen.GetViewRequestObject{ViewId: viewB.Id})
	require.NoError(t, err)
	_, ok = getB.(apigen.GetView404JSONResponse)
	assert.True(t, ok, "get of inaccessible-workspace view must be 404")

	getA, err := api.GetView(devCtx, apigen.GetViewRequestObject{ViewId: viewA.Id})
	require.NoError(t, err)
	_, ok = getA.(apigen.GetView200JSONResponse)
	assert.True(t, ok, "get of accessible-workspace view must succeed")

	// Creating a view in an inaccessible workspace is denied.
	_, err = api.CreateView(devCtx, apigen.CreateViewRequestObject{
		Body: &apigen.ViewSpec{Name: "X", IntervalDays: 1, Workspace: &wsB},
	})
	require.Error(t, err, "create in inaccessible workspace must be denied")

	// Deleting the inaccessible-workspace view returns 404.
	delB, err := api.DeleteView(devCtx, apigen.DeleteViewRequestObject{ViewId: viewB.Id})
	require.NoError(t, err)
	_, ok = delB.(apigen.DeleteView404JSONResponse)
	assert.True(t, ok, "delete of inaccessible-workspace view must be 404")
}

func TestViewsAPI_StoreUnavailable(t *testing.T) {
	// Constructed without WithViewStore: the store is nil.
	api := apiv1.New(nil, nil, nil, nil, runtime.Manager{}, &config.Config{}, nil, nil, prometheus.NewRegistry(), nil)
	_, err := api.ListViews(context.Background(), apigen.ListViewsRequestObject{})
	assert.Error(t, err)
}
