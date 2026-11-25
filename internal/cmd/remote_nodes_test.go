// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/nodeconn"
)

// Every nodes subcommand runs against a remote context.
func TestNodesCommandsAreContextAware(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"list", "show", "add", "remove", "test", "import", "export"} {
		assert.Equal(t, commandScopeContextAware, scopeForCommand("nodes "+name), name)
	}
}

func TestReadEvents(t *testing.T) {
	t.Parallel()

	t.Run("StopsAtDone", func(t *testing.T) {
		t.Parallel()
		var events []string
		err := readEvents(strings.NewReader(": keepalive\n\nevent: result\ndata: {\"node\":\"web01\"}\n\nevent: done\ndata: {}\n\nevent: result\ndata: late\n\n"),
			func(event string, data []byte) error {
				events = append(events, event+" "+string(data))
				return nil
			})
		require.NoError(t, err)
		assert.Equal(t, []string{`result {"node":"web01"}`, "done {}"}, events)
	})
	t.Run("EndsBeforeDone", func(t *testing.T) {
		t.Parallel()
		err := readEvents(strings.NewReader("event: result\ndata: {}\n\n"), func(string, []byte) error { return nil })
		require.ErrorContains(t, err, "closed the stream before every node finished")
	})
	t.Run("StopsAtHandlerError", func(t *testing.T) {
		t.Parallel()
		stop := errors.New("stop")
		err := readEvents(strings.NewReader("event: result\ndata: {}\n\n"), func(string, []byte) error { return stop })
		require.ErrorIs(t, err, stop)
	})
}

// A node of the API maps to the resolved node the local commands print.
func TestResolvedNodeOf(t *testing.T) {
	t.Parallel()
	keyPath, secret := "/etc/dagu/id", "ssh/db"
	node, err := resolvedNodeOf(api.InventoryNode{
		Name:      "db01",
		Inventory: &api.InventoryRef{Id: "inv_tokyo", Name: "Tokyo"},
		Host:      "db01.internal",
		Port:      new(2222),
		Auth:      &api.InventoryNodeAuth{KeyPath: &keyPath, PassphraseSecret: &secret},
		Timeout:   new("1m30s"),
		Labels:    []string{"role=db", "site=tokyo"},
	})
	require.NoError(t, err)
	assert.Equal(t, "inv_tokyo", node.InventoryID)
	assert.Equal(t, 90*time.Second, node.Timeout)
	assert.Equal(t, keyPath, node.Auth.Key)
	assert.Equal(t, secret, node.Auth.PassphraseSecret)
	assert.Equal(t, []string{"role=db", "site=tokyo"}, node.Labels.Strings())
}

// The rules a refused change lists print as the local commands print them.
func TestRuleErrorsOf(t *testing.T) {
	t.Parallel()
	inv, node, field, name := "Nagoya", "nagoya-web01", "host", "name"
	err := ruleErrorsOf(&remoteError{StatusCode: 400, Message: "nothing imported", Rules: []api.InventoryRuleError{
		{Inventory: &inv, Node: &node, Field: &field, Message: "is not a host name"},
		// The server leaves out an empty inventory name.
		{Field: &name, Message: "is required"},
		{Message: "an import holds no inventory"},
	}})

	require.ErrorIs(t, err, inventory.ErrInvalid)
	assert.Equal(t, "inventory \"Nagoya\": node \"nagoya-web01\": host: is not a host name\ninventory \"\": name: is required\nan import holds no inventory", err.Error())

	plain := &remoteError{StatusCode: 409, Message: "run it again"}
	assert.Same(t, plain, ruleErrorsOf(plain))
}

// A refused import lists every rule it breaks, even when the list is far
// longer than an error message the CLI prints.
func TestRemoteImportListsManyRules(t *testing.T) {
	t.Parallel()
	inv, field := "big", "host"
	rules := make([]api.InventoryRuleError, 1000)
	for i := range rules {
		node := fmt.Sprintf("web%04d", i)
		rules[i] = api.InventoryRuleError{Inventory: &inv, Node: &node, Field: &field, Message: "is required"}
	}
	body, err := json.Marshal(api.InventoryImportError{Code: api.ErrorCodeBadRequest, Message: "The import breaks 1000 rules, and nothing was stored", Errors: rules})
	require.NoError(t, err)
	require.Greater(t, len(body), maxRemoteErrorBodyBytes)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	client := &remoteClient{baseURL: server.URL, client: server.Client()}

	_, err = client.remoteImport(context.Background(), []byte("[]"), inventory.ImportOptions{})

	require.ErrorIs(t, err, inventory.ErrInvalid)
	lines := strings.Split(err.Error(), "\n")
	require.Len(t, lines, 1000)
	assert.Equal(t, `inventory "big": node "web0999": host: is required`, lines[999])
}

// A remote add takes a key path that is absolute on a POSIX or a Windows
// server, whichever platform the CLI runs on.
func TestServerKeyPath(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]bool{
		"/etc/dagu/id":          true,
		`C:\dagu\id`:            true,
		"c:/dagu/id":            true,
		`\\server\share\id`:     true,
		"~/.ssh/id_ed25519":     true,
		"$HOME/.ssh/id_ed25519": true,
		"id_ed25519":            false,
		"./id_ed25519":          false,
		"keys/id_ed25519":       false,
		"C:":                    false,
		"C:id":                  false,
	} {
		assert.Equal(t, want, serverKeyPath(path), path)
	}
}

// A remote --key path is stored as given, since the server reads it; a
// local one is made absolute.
func TestNodeKeyPath(t *testing.T) {
	t.Parallel()
	for _, path := range []string{`C:\dagu\id`, "/etc/dagu/id", "~/.ssh/id_ed25519"} {
		got, err := nodeKeyPath(path, true)
		require.NoError(t, err)
		assert.Equal(t, path, got)
	}
	_, err := nodeKeyPath("id_ed25519", true)
	require.ErrorContains(t, err, "the server reads the key on its own host")

	got, err := nodeKeyPath("id_ed25519", false)
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(got), got)
}

// eventServer answers each POST with a done event, and records the trust
// entries each request names.
func eventServer(t *testing.T, path string, status int, body string) (*remoteClient, *[]int) {
	t.Helper()
	var sizes []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Keys []json.RawMessage `json:"keys"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		sizes = append(sizes, len(req.Keys))
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return &remoteClient{baseURL: server.URL, client: server.Client()}, &sizes
}

// A remote trust names at most 1,000 keys per request, as the server allows.
func TestRemoteTrustBatches(t *testing.T) {
	t.Parallel()
	client, sizes := eventServer(t, "/node-host-keys", http.StatusOK, "event: done\ndata: {}\n\n")
	requests := make([]nodeconn.TrustRequest, 1001)
	for i := range requests {
		requests[i] = nodeconn.TrustRequest{Node: fmt.Sprintf("web%04d", i), Fingerprint: "SHA256:abc"}
	}

	require.NoError(t, remoteTester{client: client}.trust(context.Background(), requests, "", func(nodeconn.TrustResult) {}))
	assert.Equal(t, []int{1000, 1}, *sizes)
}

// When the server stores a node and then fails to trust its keys, the add
// reports the node as stored, and how to trust its keys.
func TestRemoteAddStoredBeforeFailure(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/inventories/inv_fleet/nodes":
			// A server error carries only its code.
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":"internal_error"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/inventories/inv_fleet":
			_, _ = w.Write([]byte(`{"id":"inv_fleet","name":"fleet","version":2,"nodes":[{"name":"web01","host":"h"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	adder := remoteAdder{
		client: &remoteClient{baseURL: server.URL, client: server.Client()},
		target: inventory.Stored{Inventory: inventory.Inventory{ID: "inv_fleet", Name: "fleet"}, Version: 1},
		node:   inventory.Node{Name: "web01", Host: "h"},
	}

	stored, _, err := adder.add(context.Background(), []nodeconn.Accepted{{Node: "web01", Fingerprint: "SHA256:abc"}}, "")

	assert.Equal(t, "fleet", stored.Name)
	_, partial := errors.AsType[partialAdd](err)
	assert.True(t, partial, "err: %v", err)
	assert.EqualError(t, err, `node "web01" was added, but its host keys were not trusted: 500 Internal Server Error (internal_error); trust them with 'dagu nodes test web01'`)
}

// When the server fails and its inventory cannot be read again, the add
// reports that the node may have been stored.
func TestRemoteAddUncertain(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":"internal_error"}`))
	}))
	t.Cleanup(server.Close)
	adder := remoteAdder{
		client: &remoteClient{baseURL: server.URL, client: server.Client()},
		target: inventory.Stored{Inventory: inventory.Inventory{ID: "inv_fleet", Name: "fleet"}, Version: 1},
		node:   inventory.Node{Name: "web01", Host: "h"},
	}

	stored, _, err := adder.add(context.Background(), nil, "")

	assert.Empty(t, stored.ID)
	_, uncertain := errors.AsType[uncertainAdd](err)
	assert.True(t, uncertain, "err: %v", err)
	assert.ErrorContains(t, err, `node "web01" may have been added`)
	assert.ErrorContains(t, err, "dagu nodes show web01")
}
