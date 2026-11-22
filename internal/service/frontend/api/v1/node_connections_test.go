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
	stores  []apiv1.APIOption
	servers map[string]*harness.SSHServer
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
	_, err = inventories.Create(ctx, inventory.Inventory{
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
		"NoKeys":          nil,
		"NoNode":          {{Fingerprint: "SHA256:abc"}},
		"NotAFingerprint": {{Node: "web01", Fingerprint: "MD5:ab:cd"}},
		"SameNodeTwice":   {{Node: "web01", Fingerprint: "SHA256:abc"}, {Node: "WEB01", Fingerprint: "SHA256:def"}},
		"TooMany":         trustEntries(1001),
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
