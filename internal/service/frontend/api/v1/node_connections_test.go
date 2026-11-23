// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	apigen "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/crypto"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persiststore "github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/secret"
	apiv1 "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
)

const (
	fleetUser        = "tester"
	fleetPassword    = "fleet-password-3h8v"
	fleetPasswordRef = "ssh/fleet-password"
)

// nodeFleet is an inventory of SSH servers that accept fleetPassword, which
// only the secret store holds.
type nodeFleet struct {
	stores    []apiv1.APIOption
	servers   map[string]*harness.SSHServer
	inventory inventory.Stored
}

func newNodeFleet(t *testing.T, names ...string) nodeFleet {
	t.Helper()
	ctx := context.Background()
	backend := testutil.NewMemoryBackend()

	enc, err := crypto.NewEncryptor("test-key")
	require.NoError(t, err)
	secrets, err := persiststore.NewSecretStore(backend.Collection(persis.CollectionSecrets), enc)
	require.NoError(t, err)
	password, err := secret.New(secret.CreateInput{Ref: fleetPasswordRef, ProviderType: secret.ProviderDaguManaged}, time.Time{})
	require.NoError(t, err)
	require.NoError(t, secrets.Create(ctx, password, &secret.WriteValueInput{Value: fleetPassword}))

	fleet := nodeFleet{servers: map[string]*harness.SSHServer{}}
	var nodes []inventory.Node
	for _, name := range names {
		server := harness.StartSSHServer(t, harness.SSHServerOptions{User: fleetUser, Password: fleetPassword})
		port, err := strconv.Atoi(server.Port)
		require.NoError(t, err)
		fleet.servers[name] = server
		nodes = append(nodes, inventory.Node{Name: name, Host: "127.0.0.1", Connection: inventory.Connection{Port: port}, Labels: []string{"web"}})
	}
	inventories := newInventoryTestStore(t)
	fleet.inventory, err = inventories.Create(ctx, inventory.Inventory{
		Name:     "fleet",
		Defaults: inventory.Defaults{Connection: inventory.Connection{User: fleetUser, Password: map[string]any{"secret": fleetPasswordRef}}},
		Nodes:    nodes,
	})
	require.NoError(t, err)
	hostKeys, err := persiststore.NewHostKeyStore(backend.Collection(persis.CollectionHostKeys))
	require.NoError(t, err)

	fleet.stores = []apiv1.APIOption{
		apiv1.WithInventoryStore(inventories),
		apiv1.WithHostKeyStore(hostKeys),
		apiv1.WithSecretStore(secrets),
	}
	return fleet
}

func (f nodeFleet) fingerprint(name string) string {
	return ssh.FingerprintSHA256(f.servers[name].HostKey)
}

func (f nodeFleet) api(opts ...apiv1.APIOption) *apiv1.API {
	return apiv1.New(nil, nil, nil, nil, runtime.Manager{}, &config.Config{}, nil, nil, prometheus.NewRegistry(), nil, append(f.stores, opts...)...)
}

// sseEvent is one event of a node test or trust stream.
type sseEvent struct {
	name string
	data string
}

func readEvents(t *testing.T, body string) []sseEvent {
	t.Helper()
	var events []sseEvent
	var event sseEvent
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			event.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			event.data = strings.TrimPrefix(line, "data: ")
		case line == "" && event.name != "":
			events = append(events, event)
			event = sseEvent{}
		}
	}
	return events
}

// results decodes the result events of a stream, keyed by node, and the
// done event, which must come last.
func results[T, S any](t *testing.T, events []sseEvent, node func(T) string) (map[string]T, S) {
	t.Helper()
	require.NotEmpty(t, events)
	byNode := map[string]T{}
	for _, event := range events[:len(events)-1] {
		require.Equal(t, "result", event.name)
		var result T
		require.NoError(t, json.Unmarshal([]byte(event.data), &result))
		byNode[node(result)] = result
	}
	done := events[len(events)-1]
	require.Equal(t, "done", done.name)
	var summary S
	require.NoError(t, json.Unmarshal([]byte(done.data), &summary))
	return byNode, summary
}

func trustResults(t *testing.T, body string) (map[string]apigen.InventoryHostKeyTrust, apigen.InventoryHostKeyTrustSummary) {
	t.Helper()
	return results[apigen.InventoryHostKeyTrust, apigen.InventoryHostKeyTrustSummary](t, readEvents(t, body),
		func(r apigen.InventoryHostKeyTrust) string { return r.Node })
}

// stream writes a node test or trust response as the server would.
func stream(t *testing.T, resp any) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	switch resp := resp.(type) {
	case apigen.TestInventoryNodeConnectionsResponseObject:
		require.NoError(t, resp.VisitTestInventoryNodeConnectionsResponse(recorder))
	case apigen.TrustInventoryNodeHostKeysResponseObject:
		require.NoError(t, resp.VisitTrustInventoryNodeHostKeysResponse(recorder))
	}
	return recorder.Body.String()
}

func TestNodeConnectionAPI_RefusesBadRequests(t *testing.T) {
	api := newNodeFleet(t, "web01").api()
	ctx := context.Background()

	for name, body := range map[string]apigen.InventoryNodeConnectionTestRequest{
		"Neither":         {},
		"Both":            {Nodes: &[]string{"web01"}, Labels: new("web")},
		"UnknownNode":     {Nodes: &[]string{"web01", "web09"}},
		"InvalidSelector": {Labels: new("role=")},
		"NoMatch":         {Labels: new("db")},
	} {
		t.Run("Test"+name, func(t *testing.T) {
			resp, err := api.TestInventoryNodeConnections(ctx, apigen.TestInventoryNodeConnectionsRequestObject{Body: &body})
			require.NoError(t, err)
			require.IsType(t, apigen.TestInventoryNodeConnections400JSONResponse{}, resp)
		})
	}
	tooMany := make([]string, 1001)
	for i := range tooMany {
		tooMany[i] = "web01"
	}
	resp, err := api.TestInventoryNodeConnections(ctx, apigen.TestInventoryNodeConnectionsRequestObject{Body: &apigen.InventoryNodeConnectionTestRequest{Nodes: &tooMany}})
	require.NoError(t, err)
	require.IsType(t, apigen.TestInventoryNodeConnections400JSONResponse{}, resp, "a test of more than 1,000 nodes")

	for name, keys := range map[string][]apigen.InventoryHostKeyTrustEntry{
		"NoKeys":                 nil,
		"NoNode":                 {{Fingerprint: "SHA256:abc"}},
		"NotAFingerprint":        {{Node: "web01", Fingerprint: "MD5:ab:cd"}},
		"ReplaceNotAFingerprint": {{Node: "web01", Fingerprint: "SHA256:abc", Replace: new("MD5:ab:cd")}},
		"SameNodeTwice":          {{Node: "web01", Fingerprint: "SHA256:abc"}, {Node: "WEB01", Fingerprint: "SHA256:def"}},
		"TooMany":                trustEntries(1001),
	} {
		t.Run("Trust"+name, func(t *testing.T) {
			resp, err := api.TrustInventoryNodeHostKeys(ctx, apigen.TrustInventoryNodeHostKeysRequestObject{Body: &apigen.InventoryHostKeyTrustRequest{Keys: keys}})
			require.NoError(t, err)
			require.IsType(t, apigen.TrustInventoryNodeHostKeys400JSONResponse{}, resp)
		})
	}

	// A request of exactly 1,000 entries is taken. These name no node, so
	// each fails without connecting.
	trust, err := api.TrustInventoryNodeHostKeys(ctx, apigen.TrustInventoryNodeHostKeysRequestObject{Body: &apigen.InventoryHostKeyTrustRequest{Keys: trustEntries(1000)}})
	require.NoError(t, err)
	_, summary := trustResults(t, stream(t, trust))
	assert.Equal(t, apigen.InventoryHostKeyTrustSummary{Failed: 1000}, summary)
}

// newNodeRecord is the record of a node on a loopback port, as a request
// sends one that is not stored.
func newNodeRecord(t *testing.T, name, port string) map[string]any {
	t.Helper()
	n, err := strconv.Atoi(port)
	require.NoError(t, err)
	return map[string]any{"name": name, "host": "127.0.0.1", "port": n}
}

// A new node that breaks a rule, takes a name the inventory holds, or comes
// with a malformed key is refused by both its test and its add, and nothing
// is stored.
func TestNewNodeAPI_RefusesBadRequests(t *testing.T) {
	fleet := newNodeFleet(t, "web01")
	api := fleet.api()
	ctx := context.Background()
	id := fleet.inventory.ID

	for name, body := range map[string]apigen.InventoryNewNodeTestRequest{
		"UnknownField":    {Node: map[string]any{"name": "web02", "host": "h", "hostname": "h"}},
		"BrokenRule":      {Node: newNodeRecord(t, "-web02", "22")},
		"NameTaken":       {Node: newNodeRecord(t, "WEB01", "22")},
		"NotAFingerprint": {Node: newNodeRecord(t, "web02", "22"), Accept: &[]apigen.InventoryAcceptedHostKey{{Node: "web02", Fingerprint: "MD5:ab"}}},
	} {
		t.Run(name, func(t *testing.T) {
			test, err := api.TestNewInventoryNode(ctx, apigen.TestNewInventoryNodeRequestObject{InventoryId: id, Body: &body})
			require.NoError(t, err)
			require.IsType(t, apigen.TestNewInventoryNode400JSONResponse{}, test)

			add, err := api.AddInventoryNode(ctx, apigen.AddInventoryNodeRequestObject{
				InventoryId: id,
				Params:      apigen.AddInventoryNodeParams{Version: fleet.inventory.Version},
				Body:        &apigen.InventoryNodeAddRequest{Node: body.Node, Trust: body.Accept},
			})
			require.NoError(t, err)
			require.IsType(t, apigen.AddInventoryNode400JSONResponse{}, add)
		})
	}

	test, err := api.TestNewInventoryNode(ctx, apigen.TestNewInventoryNodeRequestObject{
		InventoryId: "inv_missing",
		Body:        &apigen.InventoryNewNodeTestRequest{Node: newNodeRecord(t, "web02", "22")},
	})
	require.NoError(t, err)
	require.IsType(t, apigen.TestNewInventoryNode404JSONResponse{}, test)
}

// A new node is tested as it would be in its inventory, with that
// inventory's credentials, and only by a developer. A key the user accepts
// lets it pass, and nothing is stored or trusted.
func TestNewNodeAPI_Test(t *testing.T) {
	fleet := newNodeFleet(t, "web01")
	api := fleet.api(apiv1.WithAuthService(stubAuthService{}))
	as := func(role auth.Role) context.Context {
		return auth.WithUser(context.Background(), &auth.User{Username: "u", Role: role})
	}
	server := harness.StartSSHServer(t, harness.SSHServerOptions{User: fleetUser, Password: fleetPassword})
	presented := ssh.FingerprintSHA256(server.HostKey)
	test := func(role auth.Role, accept ...apigen.InventoryAcceptedHostKey) apigen.InventoryNodeConnectionTest {
		t.Helper()
		body := apigen.InventoryNewNodeTestRequest{Node: newNodeRecord(t, "web02", server.Port)}
		if len(accept) > 0 {
			body.Accept = &accept
		}
		resp, err := api.TestNewInventoryNode(as(role), apigen.TestNewInventoryNodeRequestObject{InventoryId: fleet.inventory.ID, Body: &body})
		require.NoError(t, err)
		tests, _ := results[apigen.InventoryNodeConnectionTest, apigen.InventoryNodeConnectionTestSummary](t, readEvents(t, stream(t, resp)),
			func(r apigen.InventoryNodeConnectionTest) string { return r.Node })
		require.Contains(t, tests, "web02")
		return tests["web02"]
	}

	_, err := api.TestNewInventoryNode(as(auth.RoleOperator), apigen.TestNewInventoryNodeRequestObject{
		InventoryId: fleet.inventory.ID,
		Body:        &apigen.InventoryNewNodeTestRequest{Node: newNodeRecord(t, "web02", server.Port)},
	})
	require.Error(t, err, "an operator tested a node that is not stored")

	unknown := test(auth.RoleDeveloper)
	assert.False(t, unknown.Ok)
	require.NotNil(t, unknown.HostKey)
	assert.Equal(t, presented, valueOf(unknown.HostKey.Presented))
	assert.Zero(t, server.AuthAttempts(), "a credential reached a host whose key is unknown")

	wrong := test(auth.RoleDeveloper, apigen.InventoryAcceptedHostKey{Node: "web02", Fingerprint: "SHA256:not-the-key"})
	assert.False(t, wrong.Ok)
	assert.Contains(t, valueOf(wrong.Error), presented)

	// web01 is no hop of web02's connection.
	other := test(auth.RoleDeveloper, apigen.InventoryAcceptedHostKey{Node: "web01", Fingerprint: fleet.fingerprint("web01")})
	assert.False(t, other.Ok)
	assert.Contains(t, valueOf(other.Error), "nor its bastion")

	passed := test(auth.RoleDeveloper, apigen.InventoryAcceptedHostKey{Node: "web02", Fingerprint: presented})
	assert.True(t, passed.Ok, "test error: %v", passed.Error)
	assert.Len(t, server.Commands(), 1)

	// Nothing was stored or trusted.
	resp, err := api.TestInventoryNodeConnections(as(auth.RoleDeveloper), apigen.TestInventoryNodeConnectionsRequestObject{
		Body: &apigen.InventoryNodeConnectionTestRequest{Nodes: &[]string{"web02"}},
	})
	require.NoError(t, err)
	require.IsType(t, apigen.TestInventoryNodeConnections400JSONResponse{}, resp, "the node was stored")
	assert.False(t, test(auth.RoleDeveloper).Ok, "the accepted key was trusted")
}

// Adding a node reads the keys to trust before it stores anything, so a key
// the node does not present stores nothing. Once stored, the node passes a
// plain test with the trusted key.
func TestNewNodeAPI_Add(t *testing.T) {
	fleet := newNodeFleet(t, "web01")
	api := fleet.api()
	ctx := context.Background()
	server := harness.StartSSHServer(t, harness.SSHServerOptions{User: fleetUser, Password: fleetPassword})
	presented := ssh.FingerprintSHA256(server.HostKey)
	add := func(version int64, fingerprint string) apigen.AddInventoryNodeResponseObject {
		t.Helper()
		resp, err := api.AddInventoryNode(ctx, apigen.AddInventoryNodeRequestObject{
			InventoryId: fleet.inventory.ID,
			Params:      apigen.AddInventoryNodeParams{Version: version},
			Body: &apigen.InventoryNodeAddRequest{
				Node:  newNodeRecord(t, "web02", server.Port),
				Trust: &[]apigen.InventoryAcceptedHostKey{{Node: "web02", Fingerprint: fingerprint}},
			},
		})
		require.NoError(t, err)
		return resp
	}

	refused := add(fleet.inventory.Version, "SHA256:not-the-key")
	require.IsType(t, apigen.AddInventoryNode400JSONResponse{}, refused)
	assert.Contains(t, refused.(apigen.AddInventoryNode400JSONResponse).Message, presented)
	require.IsType(t, apigen.AddInventoryNode409JSONResponse{}, add(fleet.inventory.Version-1, presented))

	added := add(fleet.inventory.Version, presented)
	require.IsType(t, apigen.AddInventoryNode201JSONResponse{}, added, "the refused add stored the node")
	assert.Zero(t, server.AuthAttempts(), "adding sent the node a credential")
	require.IsType(t, apigen.AddInventoryNode400JSONResponse{}, add(added.(apigen.AddInventoryNode201JSONResponse).Version, presented), "a name the inventory holds was added")

	resp, err := api.TestInventoryNodeConnections(ctx, apigen.TestInventoryNodeConnectionsRequestObject{
		Body: &apigen.InventoryNodeConnectionTestRequest{Nodes: &[]string{"web02"}},
	})
	require.NoError(t, err)
	tests, _ := results[apigen.InventoryNodeConnectionTest, apigen.InventoryNodeConnectionTestSummary](t, readEvents(t, stream(t, resp)),
		func(r apigen.InventoryNodeConnectionTest) string { return r.Node })
	assert.True(t, tests["web02"].Ok, "the trusted key: %v", tests["web02"].Error)
}

func valueOf[T any](ptr *T) T {
	if ptr == nil {
		var zero T
		return zero
	}
	return *ptr
}

// trustEntries returns n entries for distinct nodes that do not exist.
func trustEntries(n int) []apigen.InventoryHostKeyTrustEntry {
	entries := make([]apigen.InventoryHostKeyTrustEntry, n)
	for i := range entries {
		entries[i] = apigen.InventoryHostKeyTrustEntry{Node: fmt.Sprintf("missing%04d", i), Fingerprint: "SHA256:abc"}
	}
	return entries
}

// A trust needs the developer role, and a test the operator role. Both are
// audited, a trust once per key it stores.
func TestNodeConnectionAPI_PermissionsAndAudit(t *testing.T) {
	fleet := newNodeFleet(t, "web01")
	auditStore := &recordingAuditStore{}
	api := fleet.api(apiv1.WithAuthService(stubAuthService{}), apiv1.WithAuditService(audit.New(auditStore)))
	as := func(name string, role auth.Role) context.Context {
		return auth.WithUser(context.Background(), &auth.User{Username: name, Role: role})
	}
	// A node named twice is tested once.
	testBody := &apigen.InventoryNodeConnectionTestRequest{Nodes: &[]string{"web01", "WEB01"}}
	trustBody := &apigen.InventoryHostKeyTrustRequest{Keys: []apigen.InventoryHostKeyTrustEntry{{Node: "web01", Fingerprint: fleet.fingerprint("web01")}}}

	_, err := api.TestInventoryNodeConnections(as("v", auth.RoleViewer), apigen.TestInventoryNodeConnectionsRequestObject{Body: testBody})
	require.Error(t, err)
	_, err = api.TrustInventoryNodeHostKeys(as("o", auth.RoleOperator), apigen.TrustInventoryNodeHostKeysRequestObject{Body: trustBody})
	require.Error(t, err)
	assert.Empty(t, auditStore.entries)

	trust, err := api.TrustInventoryNodeHostKeys(as("d", auth.RoleDeveloper), apigen.TrustInventoryNodeHostKeysRequestObject{Body: trustBody})
	require.NoError(t, err)
	trusts, summary := trustResults(t, stream(t, trust))
	assert.Equal(t, new(apigen.InventoryHostKeyTrustOutcomeTrusted), trusts["web01"].Outcome)
	assert.Equal(t, apigen.InventoryHostKeyTrustSummary{Trusted: 1}, summary)
	// Trusting the same key again stores nothing.
	trust, err = api.TrustInventoryNodeHostKeys(as("d", auth.RoleDeveloper), apigen.TrustInventoryNodeHostKeysRequestObject{Body: trustBody})
	require.NoError(t, err)
	trusts, _ = trustResults(t, stream(t, trust))
	assert.Equal(t, new(apigen.InventoryHostKeyTrustOutcomeUnchanged), trusts["web01"].Outcome)

	// The node signs in with the password from the secret store, which no
	// event carries.
	resp, err := api.TestInventoryNodeConnections(as("o", auth.RoleOperator), apigen.TestInventoryNodeConnectionsRequestObject{Body: testBody})
	require.NoError(t, err)
	body := stream(t, resp)
	assert.NotContains(t, body, fleetPassword)
	tests, testSummary := results[apigen.InventoryNodeConnectionTest, apigen.InventoryNodeConnectionTestSummary](t, readEvents(t, body),
		func(r apigen.InventoryNodeConnectionTest) string { return r.Node })
	assert.True(t, tests["web01"].Ok, "test error: %v", tests["web01"].Error)
	assert.Equal(t, apigen.InventoryNodeConnectionTestSummary{Passed: 1}, testSummary)

	require.Len(t, auditStore.entries, 2)
	assert.Equal(t, "node_host_key_trust", auditStore.entries[0].Action)
	assert.Equal(t, "d", auditStore.entries[0].Username)
	assert.Contains(t, auditStore.entries[0].Details, fleet.fingerprint("web01"))
	assert.Equal(t, "node_connection_test", auditStore.entries[1].Action)
	assert.Equal(t, "o", auditStore.entries[1].Username)
	assert.Contains(t, auditStore.entries[1].Details, "web01")
}
