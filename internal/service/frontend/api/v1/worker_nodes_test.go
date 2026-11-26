// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apigen "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persiststore "github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	apiv1 "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
)

var workerLastSeen = time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)

// newWorkerNodeTestAPI returns an API over the test inventories and three
// recorded workers: one online, one offline, and one whose ID names an
// inventory node.
func newWorkerNodeTestAPI(t *testing.T, opts ...apiv1.APIOption) *apiv1.API {
	t.Helper()
	ctx := context.Background()
	workers, err := persiststore.NewWorkerNodeStore(testutil.NewMemoryBackend().Collection(persis.CollectionWorkerNodes), nil)
	require.NoError(t, err)
	for _, w := range []inventory.WorkerNode{
		{Name: "tokyo-gw", Host: "gw.tokyo", Labels: ir.NewLabels([]string{"web", "site=tokyo"}), Online: true, LastSeenAt: workerLastSeen},
		{Name: "osaka-gw", Host: "gw.osaka", Labels: ir.NewLabels([]string{"site=osaka"}), LastSeenAt: workerLastSeen},
		{Name: "Tokyo-Web01", Host: "web01", Online: true, LastSeenAt: workerLastSeen},
	} {
		require.NoError(t, workers.Record(ctx, w))
	}
	api := newInventoryTestAPI(t, append([]apiv1.APIOption{apiv1.WithWorkerNodeStore(workers)}, opts...)...)
	admin := auth.WithUser(ctx, &auth.User{Username: "a", Role: auth.RoleAdmin})
	mustImportInventories(t, api, admin, testInventories, apigen.ImportInventoriesParams{})
	return api
}

func TestWorkerNodeAPI_List(t *testing.T) {
	api := newWorkerNodeTestAPI(t)

	resp := listNodes(t, api, apigen.ListInventoryNodesParams{})
	assert.Equal(t, []string{"osaka-gw", "osaka-web01", "tokyo-db01", "tokyo-gw", "tokyo-jump", "tokyo-web01", "tokyo-web02"}, listedNodeNames(t, resp))
	list := resp.(apigen.ListInventoryNodes200JSONResponse)
	assert.Equal(t, 7, list.NodeCount)
	assert.Equal(t, []apigen.InventoryWorkerConflict{{Worker: "Tokyo-Web01", Inventory: apigen.InventoryRef{Id: list.Nodes[5].Inventory.Id, Name: "Tokyo production"}}}, list.WorkerConflicts)

	for _, tc := range []struct {
		name   string
		params apigen.ListInventoryNodesParams
		want   []string
	}{
		{"Labels", apigen.ListInventoryNodesParams{Labels: new("web,site=tokyo")}, []string{"tokyo-gw", "tokyo-web01", "tokyo-web02"}},
		{"Name", apigen.ListInventoryNodesParams{Name: new("GW")}, []string{"osaka-gw", "tokyo-gw"}},
		{"Inventory", apigen.ListInventoryNodesParams{Inventory: new("Osaka staging")}, []string{"osaka-web01"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := listNodes(t, api, tc.params)
			assert.Equal(t, tc.want, listedNodeNames(t, resp))
			// A conflict is listed whatever the filters.
			assert.Len(t, resp.(apigen.ListInventoryNodes200JSONResponse).WorkerConflicts, 1)
		})
	}
}

func TestWorkerNodeAPI_Get(t *testing.T) {
	ctx := context.Background()
	api := newWorkerNodeTestAPI(t)

	resp, err := api.GetInventoryNode(ctx, apigen.GetInventoryNodeRequestObject{NodeName: "OSAKA-GW"})
	require.NoError(t, err)
	assert.Equal(t, apigen.GetInventoryNode200JSONResponse{
		Name:       "osaka-gw",
		Kind:       apigen.InventoryNodeKindWorker,
		Host:       "gw.osaka",
		Labels:     []string{"site=osaka"},
		Online:     new(false),
		LastSeenAt: new(workerLastSeen),
	}, resp)

	// The inventory node holds a name that a worker also has.
	resp, err = api.GetInventoryNode(ctx, apigen.GetInventoryNodeRequestObject{NodeName: "tokyo-web01"})
	require.NoError(t, err)
	assert.Equal(t, apigen.InventoryNodeKindSsh, resp.(apigen.GetInventoryNode200JSONResponse).Kind)

	labels, err := api.ListInventoryNodeLabels(ctx, apigen.ListInventoryNodeLabelsRequestObject{})
	require.NoError(t, err)
	assert.Contains(t, labels.(apigen.ListInventoryNodeLabels200JSONResponse).Labels, "site=osaka")
}

// Forgetting a worker needs the admin role, refuses an online worker and an
// inventory node, and is audited.
func TestWorkerNodeAPI_Forget(t *testing.T) {
	auditStore := &recordingAuditStore{}
	api := newWorkerNodeTestAPI(t, apiv1.WithAuthService(stubAuthService{}), apiv1.WithAuditService(audit.New(auditStore)))
	manager := auth.WithUser(context.Background(), &auth.User{Username: "m", Role: auth.RoleManager})
	admin := auth.WithUser(context.Background(), &auth.User{Username: "a", Role: auth.RoleAdmin})
	forget := func(ctx context.Context, name string) apigen.ForgetWorkerNodeResponseObject {
		t.Helper()
		resp, err := api.ForgetWorkerNode(ctx, apigen.ForgetWorkerNodeRequestObject{NodeName: name})
		require.NoError(t, err)
		return resp
	}

	_, err := api.ForgetWorkerNode(manager, apigen.ForgetWorkerNodeRequestObject{NodeName: "osaka-gw"})
	require.Error(t, err)

	assert.IsType(t, apigen.ForgetWorkerNode409JSONResponse{}, forget(admin, "tokyo-gw"))
	assert.IsType(t, apigen.ForgetWorkerNode400JSONResponse{}, forget(admin, "tokyo-db01"))
	assert.IsType(t, apigen.ForgetWorkerNode404JSONResponse{}, forget(admin, "nagoya-gw"))
	assert.IsType(t, apigen.ForgetWorkerNode204Response{}, forget(admin, "OSAKA-GW"))
	assert.IsType(t, apigen.ForgetWorkerNode404JSONResponse{}, forget(admin, "osaka-gw"))

	// The import that sets up the inventories is audited first.
	require.Len(t, auditStore.entries, 2)
	assert.Equal(t, "worker_node_forget", auditStore.entries[1].Action)
	assert.Equal(t, "a", auditStore.entries[1].Username)
	assert.Contains(t, auditStore.entries[1].Details, "OSAKA-GW")
}
