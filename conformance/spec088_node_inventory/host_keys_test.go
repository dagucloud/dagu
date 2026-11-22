// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec088_node_inventory_test

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/test"
)

// streamEvent is one event of a test or trust stream.
type streamEvent struct {
	Name string
	Data map[string]any
}

func readStream(t *testing.T, body string) []streamEvent {
	t.Helper()
	var events []streamEvent
	var name string
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		if event, ok := strings.CutPrefix(line, "event: "); ok {
			name = event
		}
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			event := streamEvent{Name: name}
			require.NoError(t, json.Unmarshal([]byte(data), &event.Data))
			events = append(events, event)
		}
	}
	require.NotEmpty(t, events, body)
	require.Equal(t, "done", events[len(events)-1].Name, "the stream ends with done")
	return events
}

// result returns the data of the one result event of a stream.
func result(t *testing.T, events []streamEvent) map[string]any {
	t.Helper()
	require.Len(t, events, 2)
	require.Equal(t, "result", events[0].Name)
	return events[0].Data
}

// The server tests a node without sending a credential to a host whose key
// is unknown, trusts the key only when the node presents the fingerprint the
// user saw, and then connects (Spec 088 §Host keys).
func TestHostKeysOverAPI(t *testing.T) {
	keyPEM, publicKey := harness.NewSSHKey(t)
	keyFile := filepath.Join(t.TempDir(), "id_ed25519")
	require.NoError(t, os.WriteFile(keyFile, keyPEM, 0o600))
	node := harness.StartSSHServer(t, harness.SSHServerOptions{User: "tester", AuthorizedKeys: []ssh.PublicKey{publicKey}})
	port, err := strconv.Atoi(node.Port)
	require.NoError(t, err)
	fingerprint := ssh.FingerprintSHA256(node.HostKey)

	server := test.SetupServer(t)
	server.Client().Post("/api/v1/inventory-imports", []map[string]any{{
		"name":  "fleet",
		"nodes": []map[string]any{{"name": "web01", "host": "127.0.0.1", "port": port, "user": "tester", "key": keyFile}},
	}}).ExpectStatus(http.StatusOK).Send(t)
	testNode := func() map[string]any {
		resp := server.Client().Post("/api/v1/node-connection-tests", map[string]any{"nodes": []string{"web01"}}).
			ExpectStatus(http.StatusOK).Send(t)
		return result(t, readStream(t, resp.Body))
	}
	trust := func(fingerprint string) map[string]any {
		resp := server.Client().Post("/api/v1/node-host-keys", map[string]any{
			"keys": []map[string]any{{"node": "web01", "fingerprint": fingerprint}},
		}).ExpectStatus(http.StatusOK).Send(t)
		return result(t, readStream(t, resp.Body))
	}

	unknown := testNode()
	assert.Equal(t, false, unknown["ok"])
	assert.Equal(t, map[string]any{
		"hop": "node", "node": "web01", "status": "unknown", "presented": fingerprint, "expected": []any{},
	}, unknown["hostKey"])
	assert.Zero(t, node.AuthAttempts(), "a credential reached a host whose key is unknown")

	wrong := trust("SHA256:not-the-key")
	assert.Contains(t, wrong["error"], fingerprint, "the error names the key the node presents")
	assert.Equal(t, "unknown", testNode()["hostKey"].(map[string]any)["status"], "a wrong fingerprint trusts nothing")

	assert.Equal(t, "trusted", trust(fingerprint)["outcome"])
	assert.Zero(t, node.AuthAttempts(), "trusting sent the node a credential")

	passed := testNode()
	assert.Equal(t, true, passed["ok"], passed["error"])
	assert.Equal(t, fingerprint, passed["fingerprint"])
	assert.Len(t, node.Commands(), 1)
}
