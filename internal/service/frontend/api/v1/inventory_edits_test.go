// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	apigen "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persiststore "github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	apiv1 "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
)

// tokyoRecord is an inventory record with two nodes.
const tokyoRecord = `{
  "name": "Tokyo production",
  "defaults": {"user": "deploy", "key": {"secret": "ssh/tokyo"}},
  "nodes": [
    {"name": "web01", "host": "10.0.1.11", "labels": ["role=web"]},
    {"name": "web02", "host": "10.0.1.12", "labels": ["role=web"]}
  ]
}`

func newEditTestAPI(t *testing.T, opts ...apiv1.APIOption) (*apiv1.API, inventory.HostKeyStore) {
	t.Helper()
	hostKeys, err := persiststore.NewHostKeyStore(testutil.NewMemoryBackend().Collection(persis.CollectionHostKeys))
	require.NoError(t, err)
	opts = append([]apiv1.APIOption{apiv1.WithHostKeyStore(hostKeys)}, opts...)
	return newInventoryTestAPI(t, opts...), hostKeys
}

// record decodes raw as the body of a request of type T.
func record[T ~map[string]any](t *testing.T, raw string) *T {
	t.Helper()
	var body T
	require.NoError(t, json.Unmarshal([]byte(raw), &body))
	return &body
}

func createTokyo(t *testing.T, api *apiv1.API, ctx context.Context) apigen.Inventory {
	t.Helper()
	resp, err := api.CreateInventory(ctx, apigen.CreateInventoryRequestObject{Body: record[apigen.CreateInventoryJSONRequestBody](t, tokyoRecord)})
	require.NoError(t, err)
	created, ok := resp.(apigen.CreateInventory201JSONResponse)
	require.True(t, ok, "expected 201, got %#v", resp)
	return apigen.Inventory(created)
}

func trustKey(t *testing.T, hostKeys inventory.HostKeyStore, node string) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	key, err := ssh.NewPublicKey(pub)
	require.NoError(t, err)
	_, err = hostKeys.Trust(context.Background(), node, key, "alice", "")
	require.NoError(t, err)
}

func nodeNamesOf(inv apigen.Inventory) []string {
	names := make([]string, 0, len(inv.Nodes))
	for _, node := range inv.Nodes {
		names = append(names, node.Name)
	}
	return names
}

// An inventory is created, replaced, and deleted, each change naming the
// version it is based on.
func TestInventoryEditAPI_InventoryLifecycle(t *testing.T) {
	ctx := context.Background()
	api, _ := newEditTestAPI(t)

	created := createTokyo(t, api, ctx)
	assert.Equal(t, int64(1), created.Version)
	assert.Equal(t, []string{"web01", "web02"}, nodeNamesOf(created))

	body := record[apigen.ReplaceInventoryJSONRequestBody](t, tokyoRecord)
	(*body)["description"] = "Racks A and B"
	resp, err := api.ReplaceInventory(ctx, apigen.ReplaceInventoryRequestObject{
		InventoryId: created.Id, Params: apigen.ReplaceInventoryParams{Version: 1}, Body: body,
	})
	require.NoError(t, err)
	replaced, ok := resp.(apigen.ReplaceInventory200JSONResponse)
	require.True(t, ok, "expected 200, got %#v", resp)
	assert.Equal(t, int64(2), replaced.Version)
	assert.Equal(t, created.Id, replaced.Id)
	require.NotNil(t, replaced.Description)
	assert.Equal(t, "Racks A and B", *replaced.Description)

	deleted, err := api.DeleteInventory(ctx, apigen.DeleteInventoryRequestObject{
		InventoryId: created.Id, Params: apigen.DeleteInventoryParams{Version: 2},
	})
	require.NoError(t, err)
	assert.IsType(t, apigen.DeleteInventory204Response{}, deleted)
	assert.Empty(t, listInventorySummaries(t, api))
}

// A change based on an old version is refused, and the response carries the
// inventory as it is now.
func TestInventoryEditAPI_RefusesStaleVersions(t *testing.T) {
	ctx := context.Background()
	api, _ := newEditTestAPI(t)
	created := createTokyo(t, api, ctx)
	_, err := api.PutInventoryNode(ctx, apigen.PutInventoryNodeRequestObject{
		InventoryId: created.Id, NodeName: "web03", Params: apigen.PutInventoryNodeParams{Version: 1},
		Body: record[apigen.PutInventoryNodeJSONRequestBody](t, `{"host": "10.0.1.13"}`),
	})
	require.NoError(t, err)

	resp, err := api.ReplaceInventory(ctx, apigen.ReplaceInventoryRequestObject{
		InventoryId: created.Id, Params: apigen.ReplaceInventoryParams{Version: 1},
		Body: record[apigen.ReplaceInventoryJSONRequestBody](t, tokyoRecord),
	})
	require.NoError(t, err)
	conflict, ok := resp.(apigen.ReplaceInventory409JSONResponse)
	require.True(t, ok, "expected 409, got %#v", resp)
	assert.Equal(t, int64(2), conflict.Inventory.Version)
	assert.Equal(t, []string{"web01", "web02", "web03"}, nodeNamesOf(conflict.Inventory))

	removed, err := api.DeleteInventoryNode(ctx, apigen.DeleteInventoryNodeRequestObject{
		InventoryId: created.Id, NodeName: "web01", Params: apigen.DeleteInventoryNodeParams{Version: 1},
	})
	require.NoError(t, err)
	assert.IsType(t, apigen.DeleteInventoryNode409JSONResponse{}, removed)
	gone, err := api.DeleteInventory(ctx, apigen.DeleteInventoryRequestObject{
		InventoryId: created.Id, Params: apigen.DeleteInventoryParams{Version: 1},
	})
	require.NoError(t, err)
	assert.IsType(t, apigen.DeleteInventory409JSONResponse{}, gone)
}

// A node is added, changed, and removed one at a time. Removing a node, or
// adding one, forgets the host key trusted for that name.
func TestInventoryEditAPI_Nodes(t *testing.T) {
	ctx := context.Background()
	api, hostKeys := newEditTestAPI(t)
	created := createTokyo(t, api, ctx)
	trustKey(t, hostKeys, "web01")
	trustKey(t, hostKeys, "web02")
	trustKey(t, hostKeys, "db01") // left over from a node removed before

	put := func(name string, version int64, node apigen.PutInventoryNodeJSONRequestBody) apigen.PutInventoryNodeResponseObject {
		t.Helper()
		resp, err := api.PutInventoryNode(ctx, apigen.PutInventoryNodeRequestObject{
			InventoryId: created.Id, NodeName: name, Params: apigen.PutInventoryNodeParams{Version: version}, Body: &node,
		})
		require.NoError(t, err)
		return resp
	}

	added, ok := put("db01", 1, apigen.PutInventoryNodeJSONRequestBody{"host": "db01.internal", "port": 2222}).(apigen.PutInventoryNode201JSONResponse)
	require.True(t, ok, "a new node is created")
	assert.Equal(t, []string{"web01", "web02", "db01"}, nodeNamesOf(apigen.Inventory(added)))

	changed, ok := put("WEB02", 2, apigen.PutInventoryNodeJSONRequestBody{"name": "web02", "host": "10.0.9.12"}).(apigen.PutInventoryNode200JSONResponse)
	require.True(t, ok, "an existing node is replaced")
	assert.Equal(t, "10.0.9.12", changed.Nodes[1].Host)

	assert.IsType(t, apigen.PutInventoryNode400JSONResponse{}, put("web03", 3, apigen.PutInventoryNodeJSONRequestBody{"name": "web04", "host": "x"}),
		"the record names another node")
	assert.IsType(t, apigen.PutInventoryNode400JSONResponse{}, put("web03", 3, apigen.PutInventoryNodeJSONRequestBody{"host": "10.0.1.13", "hots": "typo"}),
		"an unknown field is refused")
	assert.IsType(t, apigen.PutInventoryNode400JSONResponse{}, put("web03", 3, apigen.PutInventoryNodeJSONRequestBody{"host": "10.0.1.13", "bastion": "nowhere"}),
		"a rule is broken")

	resp, err := api.DeleteInventoryNode(ctx, apigen.DeleteInventoryNodeRequestObject{
		InventoryId: created.Id, NodeName: "web01", Params: apigen.DeleteInventoryNodeParams{Version: 3},
	})
	require.NoError(t, err)
	removed, ok := resp.(apigen.DeleteInventoryNode200JSONResponse)
	require.True(t, ok, "expected 200, got %#v", resp)
	assert.Equal(t, []string{"web02", "db01"}, nodeNamesOf(apigen.Inventory(removed)))

	missing, err := api.DeleteInventoryNode(ctx, apigen.DeleteInventoryNodeRequestObject{
		InventoryId: created.Id, NodeName: "web01", Params: apigen.DeleteInventoryNodeParams{Version: 4},
	})
	require.NoError(t, err)
	assert.IsType(t, apigen.DeleteInventoryNode404JSONResponse{}, missing)

	for node, want := range map[string]bool{"web01": false, "web02": true, "db01": false} {
		_, err := hostKeys.Get(ctx, node)
		assert.Equal(t, want, err == nil, "a key is trusted for %s", node)
	}
}

// A change breaks a rule against the other inventories, naming it.
func TestInventoryEditAPI_RefusesBrokenRules(t *testing.T) {
	ctx := context.Background()
	api, _ := newEditTestAPI(t)
	createTokyo(t, api, ctx)

	resp, err := api.CreateInventory(ctx, apigen.CreateInventoryRequestObject{
		Body: record[apigen.CreateInventoryJSONRequestBody](t, `{"name": "osaka", "nodes": [{"name": "WEB01", "host": "10.1.0.11"}]}`),
	})
	require.NoError(t, err)
	refused, ok := resp.(apigen.CreateInventory400JSONResponse)
	require.True(t, ok, "expected 400, got %#v", resp)
	require.Len(t, refused.Errors, 1)
	assert.Contains(t, refused.Errors[0].Message, "Tokyo production")
	assert.Len(t, listInventorySummaries(t, api), 1, "nothing was stored")
}

// Changes need the developer role, and each is audited.
func TestInventoryEditAPI_PermissionsAndAudit(t *testing.T) {
	auditStore := &recordingAuditStore{}
	api, _ := newEditTestAPI(t, apiv1.WithAuthService(stubAuthService{}), apiv1.WithAuditService(audit.New(auditStore)))
	operator := auth.WithUser(context.Background(), &auth.User{Username: "o", Role: auth.RoleOperator})
	developer := auth.WithUser(context.Background(), &auth.User{Username: "d", Role: auth.RoleDeveloper})

	_, err := api.CreateInventory(operator, apigen.CreateInventoryRequestObject{Body: record[apigen.CreateInventoryJSONRequestBody](t, tokyoRecord)})
	require.Error(t, err)
	_, err = api.DeleteInventoryNode(operator, apigen.DeleteInventoryNodeRequestObject{InventoryId: "x", NodeName: "web01"})
	require.Error(t, err)

	created := createTokyo(t, api, developer)
	_, err = api.DeleteInventoryNode(developer, apigen.DeleteInventoryNodeRequestObject{
		InventoryId: created.Id, NodeName: "web01", Params: apigen.DeleteInventoryNodeParams{Version: 1},
	})
	require.NoError(t, err)

	require.Len(t, auditStore.entries, 2)
	assert.Equal(t, "inventory_create", auditStore.entries[0].Action)
	assert.Equal(t, "inventory_node_delete", auditStore.entries[1].Action)
	assert.Equal(t, "d", auditStore.entries[1].Username)
	assert.Contains(t, auditStore.entries[1].Details, "web01")
}

// Over HTTP, a change's body is decoded as sent, so a field the generated
// types would drop is refused.
func TestInventoryEditAPI_StrictBodyOverHTTP(t *testing.T) {
	cfg := &config.Config{Server: config.Server{APIBasePath: "/api/v1", Auth: config.Auth{Mode: config.AuthModeNone}}}
	api := apiv1.New(nil, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil,
		apiv1.WithInventoryStore(newInventoryTestStore(t)))
	router := chi.NewRouter()
	router.Route("/api/v1", func(r chi.Router) {
		require.NoError(t, api.ConfigureRoutes(t.Context(), r, time.Second))
	})
	send := func(method, path, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}

	resp := send(http.MethodPost, "/inventories", `{"name": "tokyo", "nodes": [{"name": "web01", "host": "10.0.1.11", "lables": ["web"]}]}`)
	assert.Equal(t, http.StatusBadRequest, resp.Code)
	assert.Contains(t, resp.Body.String(), "lables")

	resp = send(http.MethodPost, "/inventories", `{"name": "tokyo", "nodes": []}`)
	require.Equal(t, http.StatusCreated, resp.Code, resp.Body.String())
	var created apigen.Inventory
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &created))

	resp = send(http.MethodPut, "/inventories/"+created.Id+"/nodes/web01?version=1", `{"host": "10.0.1.11"} {}`)
	assert.Equal(t, http.StatusBadRequest, resp.Code)
	assert.Contains(t, resp.Body.String(), "unexpected data after the value")
}
