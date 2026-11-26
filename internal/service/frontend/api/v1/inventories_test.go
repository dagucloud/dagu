// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testInventories is an export of two inventories. Every credential is a
// secret ref or a key path, as an inventory requires.
const testInventories = `[
  {
    "name": "Tokyo production",
    "description": "Racks A and B",
    "defaults": {
      "user": "deploy",
      "key": {"secret": "ssh/tokyo"},
      "labels": ["site=tokyo", "env=prod"]
    },
    "nodes": [
      {"name": "tokyo-web01", "host": "10.0.1.11", "labels": ["web"]},
      {"name": "tokyo-web02", "host": "10.0.1.12", "labels": ["web", "canary"]},
      {
        "name": "tokyo-db01",
        "host": "db01.internal",
        "port": 2222,
        "user": "postgres",
        "key": {"file": "/etc/dagu/keys/db"},
        "passphrase": {"secret": "ssh/db-passphrase"},
        "bastion": "tokyo-jump",
        "labels": ["db"]
      },
      {"name": "tokyo-jump", "host": "jump.tokyo.example.com"}
    ]
  },
  {
    "name": "Osaka staging",
    "defaults": {
      "user": "deploy",
      "password": {"secret": "ssh/osaka"},
      "labels": ["site=osaka"]
    },
    "nodes": [
      {"name": "osaka-web01", "host": "10.1.0.11", "labels": ["web"], "disabled": true}
    ]
  }
]`

type recordingAuditStore struct{ entries []*audit.Entry }

func (s *recordingAuditStore) Append(_ context.Context, entry *audit.Entry) error {
	s.entries = append(s.entries, entry)
	return nil
}

func (s *recordingAuditStore) Query(context.Context, audit.QueryFilter) (*audit.QueryResult, error) {
	return &audit.QueryResult{Entries: s.entries, Total: len(s.entries)}, nil
}

func newInventoryTestStore(t *testing.T) inventory.Store {
	t.Helper()
	backend := testutil.NewMemoryBackend()
	store, err := persiststore.NewInventoryStore(
		backend.Collection(persis.CollectionInventories),
		backend.Lock(persis.CollectionInventories),
		nil,
	)
	require.NoError(t, err)
	return store
}

func newInventoryTestAPI(t *testing.T, opts ...apiv1.APIOption) *apiv1.API {
	t.Helper()
	allOpts := append([]apiv1.APIOption{apiv1.WithInventoryStore(newInventoryTestStore(t))}, opts...)
	return apiv1.New(nil, nil, nil, nil, runtime.Manager{}, &config.Config{}, nil, nil, prometheus.NewRegistry(), nil, allOpts...)
}

func importInventories(t *testing.T, api *apiv1.API, ctx context.Context, body string, params apigen.ImportInventoriesParams) apigen.ImportInventoriesResponseObject {
	t.Helper()
	var items apigen.ImportInventoriesJSONRequestBody
	require.NoError(t, json.Unmarshal([]byte(body), &items))
	resp, err := api.ImportInventories(ctx, apigen.ImportInventoriesRequestObject{Params: params, Body: &items})
	require.NoError(t, err)
	return resp
}

func mustImportInventories(t *testing.T, api *apiv1.API, ctx context.Context, body string, params apigen.ImportInventoriesParams) apigen.ImportInventoriesResponseObject {
	t.Helper()
	resp := importInventories(t, api, ctx, body, params)
	require.IsType(t, apigen.ImportInventories200JSONResponse{}, resp)
	return resp
}

func listNodes(t *testing.T, api *apiv1.API, params apigen.ListInventoryNodesParams) apigen.ListInventoryNodesResponseObject {
	t.Helper()
	resp, err := api.ListInventoryNodes(context.Background(), apigen.ListInventoryNodesRequestObject{Params: params})
	require.NoError(t, err)
	return resp
}

func listedNodeNames(t *testing.T, resp apigen.ListInventoryNodesResponseObject) []string {
	t.Helper()
	list, ok := resp.(apigen.ListInventoryNodes200JSONResponse)
	require.True(t, ok, "expected 200, got %T", resp)
	names := make([]string, 0, len(list.Nodes))
	for _, n := range list.Nodes {
		names = append(names, n.Name)
	}
	return names
}

func listInventorySummaries(t *testing.T, api *apiv1.API) []apigen.InventorySummary {
	t.Helper()
	resp, err := api.ListInventories(context.Background(), apigen.ListInventoriesRequestObject{})
	require.NoError(t, err)
	list, ok := resp.(apigen.ListInventories200JSONResponse)
	require.True(t, ok, "expected 200, got %T", resp)
	return list.Inventories
}

// recordOf returns the part of an inventory that an export holds.
func recordOf(t *testing.T, inv apigen.Inventory) map[string]any {
	t.Helper()
	data, err := json.Marshal(inv)
	require.NoError(t, err)
	var record map[string]any
	require.NoError(t, json.Unmarshal(data, &record))
	for _, field := range []string{"id", "version", "createdAt", "updatedAt"} {
		delete(record, field)
	}
	return record
}

func TestInventoryAPI_ImportAndRead(t *testing.T) {
	ctx := context.Background()
	api := newInventoryTestAPI(t)

	dryRun := mustImportInventories(t, api, ctx, testInventories, apigen.ImportInventoriesParams{DryRun: new(true)})
	assert.Equal(t, apigen.ImportInventories200JSONResponse{
		Created:  []string{"Tokyo production", "Osaka staging"},
		Replaced: []string{},
		DryRun:   true,
	}, dryRun)
	assert.Empty(t, listInventorySummaries(t, api), "a dry run stores nothing")

	mustImportInventories(t, api, ctx, testInventories, apigen.ImportInventoriesParams{})
	summaries := listInventorySummaries(t, api)
	require.Len(t, summaries, 2)
	assert.Equal(t, "Osaka staging", summaries[0].Name, "inventories are listed in name order")
	assert.Equal(t, 1, summaries[0].NodeCount)
	assert.Equal(t, "Tokyo production", summaries[1].Name)
	assert.Equal(t, 4, summaries[1].NodeCount)
	assert.Equal(t, int64(1), summaries[1].Version)

	resp, err := api.GetInventory(ctx, apigen.GetInventoryRequestObject{InventoryId: summaries[1].Id})
	require.NoError(t, err)
	inv, ok := resp.(apigen.GetInventory200JSONResponse)
	require.True(t, ok, "expected 200, got %T", resp)
	assert.Equal(t, int64(1), inv.Version)

	// The record comes back as it was imported, so an export of it imports
	// back unchanged.
	var imported []map[string]any
	require.NoError(t, json.Unmarshal([]byte(testInventories), &imported))
	assert.Equal(t, imported[0], recordOf(t, apigen.Inventory(inv)))

	missing, err := api.GetInventory(ctx, apigen.GetInventoryRequestObject{InventoryId: "inv_missing"})
	require.NoError(t, err)
	assert.IsType(t, apigen.GetInventory404JSONResponse{}, missing)
}

func TestInventoryAPI_Nodes(t *testing.T) {
	api := newInventoryTestAPI(t)
	mustImportInventories(t, api, context.Background(), testInventories, apigen.ImportInventoriesParams{})
	tokyoID := listInventorySummaries(t, api)[1].Id

	tests := []struct {
		name   string
		params apigen.ListInventoryNodesParams
		want   []string
	}{
		{name: "All", want: []string{"osaka-web01", "tokyo-db01", "tokyo-jump", "tokyo-web01", "tokyo-web02"}},
		{name: "Selector", params: apigen.ListInventoryNodesParams{Labels: new("web,!canary")}, want: []string{"osaka-web01", "tokyo-web01"}},
		{name: "DefaultLabels", params: apigen.ListInventoryNodesParams{Labels: new("site=tokyo")}, want: []string{"tokyo-db01", "tokyo-jump", "tokyo-web01", "tokyo-web02"}},
		{name: "InventoryName", params: apigen.ListInventoryNodesParams{Labels: new("web"), Inventory: new("TOKYO production")}, want: []string{"tokyo-web01", "tokyo-web02"}},
		{name: "InventoryID", params: apigen.ListInventoryNodesParams{Inventory: &tokyoID, Name: new("WEB")}, want: []string{"tokyo-web01", "tokyo-web02"}},
		{name: "Page", params: apigen.ListInventoryNodesParams{Page: new(2), PerPage: new(2)}, want: []string{"tokyo-jump", "tokyo-web01"}},
		{name: "PastLastPage", params: apigen.ListInventoryNodesParams{Page: new(9), PerPage: new(2)}, want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, listedNodeNames(t, listNodes(t, api, tt.params)))
		})
	}

	page, ok := listNodes(t, api, apigen.ListInventoryNodesParams{Page: new(2), PerPage: new(2)}).(apigen.ListInventoryNodes200JSONResponse)
	require.True(t, ok)
	assert.Equal(t, 5, page.Pagination.TotalRecords)
	assert.Equal(t, 3, page.Pagination.TotalPages)
}

func TestInventoryAPI_NodesRefusesBadFilters(t *testing.T) {
	api := newInventoryTestAPI(t)
	mustImportInventories(t, api, context.Background(), testInventories, apigen.ImportInventoriesParams{})

	resp := listNodes(t, api, apigen.ListInventoryNodesParams{Labels: new("web,env==prod")})
	refused, ok := resp.(apigen.ListInventoryNodes400JSONResponse)
	require.True(t, ok, "expected 400, got %T", resp)
	assert.Contains(t, refused.Message, `"env==prod"`, "the error names the term")

	resp = listNodes(t, api, apigen.ListInventoryNodesParams{Inventory: new("Nagoya")})
	refused, ok = resp.(apigen.ListInventoryNodes400JSONResponse)
	require.True(t, ok, "expected 400, got %T", resp)
	assert.Contains(t, refused.Message, `"Nagoya"`)
}

func TestInventoryAPI_Node(t *testing.T) {
	ctx := context.Background()
	api := newInventoryTestAPI(t)
	mustImportInventories(t, api, ctx, testInventories, apigen.ImportInventoriesParams{})

	resp, err := api.GetInventoryNode(ctx, apigen.GetInventoryNodeRequestObject{NodeName: "TOKYO-DB01"})
	require.NoError(t, err)
	node, ok := resp.(apigen.GetInventoryNode200JSONResponse)
	require.True(t, ok, "expected 200, got %T", resp)
	assert.Equal(t, apigen.GetInventoryNode200JSONResponse{
		Name:      "tokyo-db01",
		Kind:      apigen.InventoryNodeKindSsh,
		Inventory: &apigen.InventoryRef{Id: node.Inventory.Id, Name: "Tokyo production"},
		Host:      "db01.internal",
		Port:      new(2222),
		User:      new("postgres"),
		Auth: &apigen.InventoryNodeAuth{
			KeyPath:          new("/etc/dagu/keys/db"),
			PassphraseSecret: new("ssh/db-passphrase"),
		},
		Bastion:       new("tokyo-jump"),
		StrictHostKey: new(true),
		Timeout:       new("30s"),
		Labels:        []string{"db", "env=prod", "site=tokyo"},
		Disabled:      false,
	}, node)

	resp, err = api.GetInventoryNode(ctx, apigen.GetInventoryNodeRequestObject{NodeName: "tokyo-web01"})
	require.NoError(t, err)
	node, ok = resp.(apigen.GetInventoryNode200JSONResponse)
	require.True(t, ok, "expected 200, got %T", resp)
	assert.Equal(t, &apigen.InventoryNodeAuth{KeySecret: new("ssh/tokyo")}, node.Auth, "a default key reaches the node")

	resp, err = api.GetInventoryNode(ctx, apigen.GetInventoryNodeRequestObject{NodeName: "nagoya-web01"})
	require.NoError(t, err)
	assert.IsType(t, apigen.GetInventoryNode404JSONResponse{}, resp)
}

func TestInventoryAPI_NodeLabels(t *testing.T) {
	ctx := context.Background()
	api := newInventoryTestAPI(t)
	mustImportInventories(t, api, ctx, testInventories, apigen.ImportInventoriesParams{})

	resp, err := api.ListInventoryNodeLabels(ctx, apigen.ListInventoryNodeLabelsRequestObject{})
	require.NoError(t, err)
	assert.Equal(t, apigen.ListInventoryNodeLabels200JSONResponse{
		Labels: []string{"canary", "db", "env=prod", "site=osaka", "site=tokyo", "web"},
		Errors: []string{},
	}, resp)
}

func TestInventoryAPI_ImportReplace(t *testing.T) {
	ctx := context.Background()
	api := newInventoryTestAPI(t)
	mustImportInventories(t, api, ctx, testInventories, apigen.ImportInventoriesParams{})
	osakaID := listInventorySummaries(t, api)[0].Id

	again := `[{"name": "OSAKA staging", "nodes": [{"name": "osaka-web02", "host": "10.1.0.12"}]}]`
	resp := importInventories(t, api, ctx, again, apigen.ImportInventoriesParams{})
	refused, ok := resp.(apigen.ImportInventories400JSONResponse)
	require.True(t, ok, "expected 400, got %T", resp)
	require.NotEmpty(t, refused.Errors)
	for _, ruleErr := range refused.Errors {
		assert.Equal(t, "OSAKA staging", *ruleErr.Inventory)
		assert.Equal(t, "name", *ruleErr.Field)
	}

	resp = mustImportInventories(t, api, ctx, again, apigen.ImportInventoriesParams{Replace: new(true)})
	assert.Equal(t, []string{"OSAKA staging"}, resp.(apigen.ImportInventories200JSONResponse).Replaced)
	assert.Equal(t, osakaID, listInventorySummaries(t, api)[0].Id, "a replaced inventory keeps its ID")
	assert.Equal(t, []string{"osaka-web02"}, listedNodeNames(t, listNodes(t, api, apigen.ListInventoryNodesParams{Labels: new("!site=tokyo")})))
}

// A refused import lists every broken rule and stores nothing, so the literal
// password it holds reaches no response.
func TestInventoryAPI_ImportRefusesBrokenRules(t *testing.T) {
	ctx := context.Background()
	api := newInventoryTestAPI(t)

	broken := `[
	  {"name": "A", "nodes": [{"name": "web01", "host": "10.0.0.1", "password": "hunter2"}]},
	  {"name": "B", "nodes": [{"name": "WEB01", "host": "10.0.0.2"}]}
	]`
	resp := importInventories(t, api, ctx, broken, apigen.ImportInventoriesParams{})
	refused, ok := resp.(apigen.ImportInventories400JSONResponse)
	require.True(t, ok, "expected 400, got %T", resp)
	require.Len(t, refused.Errors, 2)
	assert.Equal(t, "A", *refused.Errors[0].Inventory)
	assert.Equal(t, "web01", *refused.Errors[0].Node)
	assert.Equal(t, "password", *refused.Errors[0].Field)
	assert.Equal(t, "B", *refused.Errors[1].Inventory)
	assert.Equal(t, "WEB01", *refused.Errors[1].Node)
	assert.Contains(t, refused.Message, "2 rules")
	assert.Empty(t, listInventorySummaries(t, api))

	data, err := json.Marshal(refused)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "hunter2")

	tests := []struct {
		name string
		body string
	}{
		{name: "UnknownField", body: `[{"name": "A", "nodes": [{"name": "web01", "hots": "10.0.0.1"}]}]`},
		{name: "IDGiven", body: `[{"id": "inv_abc", "name": "A", "nodes": []}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := importInventories(t, api, ctx, tt.body, apigen.ImportInventoriesParams{})
			assert.IsType(t, apigen.ImportInventories400JSONResponse{}, resp)
			assert.Empty(t, listInventorySummaries(t, api))
		})
	}
}

// Reads take any role. An import takes the role that edits base config, and
// is audited when it stores something.
func TestInventoryAPI_ImportPermissionsAndAudit(t *testing.T) {
	auditStore := &recordingAuditStore{}
	api := newInventoryTestAPI(t,
		apiv1.WithAuthService(stubAuthService{}),
		apiv1.WithAuditService(audit.New(auditStore)),
	)
	viewerCtx := auth.WithUser(context.Background(), &auth.User{Username: "v", Role: auth.RoleViewer})
	operatorCtx := auth.WithUser(context.Background(), &auth.User{Username: "o", Role: auth.RoleOperator})
	devCtx := auth.WithUser(context.Background(), &auth.User{Username: "d", Role: auth.RoleDeveloper})

	for _, ctx := range []context.Context{viewerCtx, operatorCtx} {
		var items apigen.ImportInventoriesJSONRequestBody
		require.NoError(t, json.Unmarshal([]byte(testInventories), &items))
		_, err := api.ImportInventories(ctx, apigen.ImportInventoriesRequestObject{Body: &items})
		require.Error(t, err)
	}

	mustImportInventories(t, api, devCtx, testInventories, apigen.ImportInventoriesParams{DryRun: new(true)})
	mustImportInventories(t, api, devCtx, `[]`, apigen.ImportInventoriesParams{})
	assert.Empty(t, auditStore.entries, "an import that stores nothing is not audited")
	mustImportInventories(t, api, devCtx, testInventories, apigen.ImportInventoriesParams{})
	require.Len(t, auditStore.entries, 1)
	assert.Equal(t, "inventory_import", auditStore.entries[0].Action)
	assert.Equal(t, "d", auditStore.entries[0].Username)

	resp, err := api.ListInventoryNodes(viewerCtx, apigen.ListInventoryNodesRequestObject{})
	require.NoError(t, err)
	assert.Len(t, resp.(apigen.ListInventoryNodes200JSONResponse).Nodes, 5)
}

// The import refuses a body that holds more than the array, though the
// generated handler would decode the array alone, and a body over the limit.
// Strict validation stays off, so no OpenAPI validator stands in front of the
// handler.
func TestInventoryAPI_ImportOverHTTP(t *testing.T) {
	cfg := &config.Config{Server: config.Server{
		APIBasePath: "/api/v1",
		Auth:        config.Auth{Mode: config.AuthModeNone},
	}}
	api := apiv1.New(nil, nil, nil, nil, runtime.Manager{}, cfg, nil, nil, prometheus.NewRegistry(), nil,
		apiv1.WithInventoryStore(newInventoryTestStore(t)))
	router := chi.NewRouter()
	router.Route("/api/v1", func(r chi.Router) {
		require.NoError(t, api.ConfigureRoutes(t.Context(), r, time.Second))
	})
	post := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/inventory-imports", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}

	resp := post(testInventories + ` {}`)
	assert.Equal(t, http.StatusBadRequest, resp.Code)
	assert.Contains(t, resp.Body.String(), "unexpected data after the value")
	assert.Empty(t, listInventorySummaries(t, api))

	// The body is read whole before the role is checked, so it is bounded:
	// 32 MiB of whitespace inside an array is one byte too many.
	resp = post("[" + strings.Repeat(" ", 32<<20-1) + "]")
	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.Code)

	resp = post(testInventories)
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	assert.Len(t, listInventorySummaries(t, api), 2)
}

// failingImportStore stores the first inventory of an import and then fails,
// as an import does when the disk fills up partway.
type failingImportStore struct{ inventory.Store }

var errDiskFull = errors.New("disk full")

func (failingImportStore) Import(context.Context, []inventory.Inventory, inventory.ImportOptions) (inventory.ImportResult, error) {
	return inventory.ImportResult{Created: []string{"Tokyo production"}}, errDiskFull
}

func TestInventoryAPI_ImportStoppedPartwayIsAudited(t *testing.T) {
	auditStore := &recordingAuditStore{}
	api := apiv1.New(nil, nil, nil, nil, runtime.Manager{}, &config.Config{}, nil, nil, prometheus.NewRegistry(), nil,
		apiv1.WithInventoryStore(failingImportStore{newInventoryTestStore(t)}),
		apiv1.WithAuditService(audit.New(auditStore)),
	)
	var items apigen.ImportInventoriesJSONRequestBody
	require.NoError(t, json.Unmarshal([]byte(testInventories), &items))
	_, err := api.ImportInventories(context.Background(), apigen.ImportInventoriesRequestObject{Body: &items})
	require.ErrorIs(t, err, errDiskFull)
	require.Len(t, auditStore.entries, 1)
	assert.Contains(t, auditStore.entries[0].Details, "Tokyo production")
}

func TestInventoryAPI_StoreUnavailable(t *testing.T) {
	api := apiv1.New(nil, nil, nil, nil, runtime.Manager{}, &config.Config{}, nil, nil, prometheus.NewRegistry(), nil)
	_, err := api.ListInventories(context.Background(), apigen.ListInventoriesRequestObject{})
	require.Error(t, err)
}
