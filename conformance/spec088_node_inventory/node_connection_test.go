// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec088_node_inventory_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// sshFleet is a Dagu home whose nodes accept one key.
type sshFleet struct {
	dagu    *harness.Runner
	env     []string
	keyFile string
	key     ssh.PublicKey
}

func newSSHFleet(t *testing.T) sshFleet {
	t.Helper()
	keyPEM, publicKey := harness.NewSSHKey(t)
	keyFile := filepath.Join(t.TempDir(), "id_ed25519")
	require.NoError(t, os.WriteFile(keyFile, keyPEM, 0o600))
	return sshFleet{dagu: harness.NewRunner(t), env: home(t), keyFile: keyFile, key: publicKey}
}

// server starts an SSH server that accepts the fleet's key.
func (f sshFleet) server(t *testing.T) *harness.SSHServer {
	t.Helper()
	return harness.StartSSHServer(t, harness.SSHServerOptions{User: "tester", AuthorizedKeys: []ssh.PublicKey{f.key}})
}

// node is the record of a node on server, labeled web.
func (f sshFleet) node(t *testing.T, name string, server *harness.SSHServer) map[string]any {
	t.Helper()
	port, err := strconv.Atoi(server.Port)
	require.NoError(t, err)
	return map[string]any{"name": name, "host": "127.0.0.1", "port": port, "user": "tester", "key": f.keyFile, "labels": []string{"web"}}
}

// importFleet imports inventory "fleet" with nodes, replacing it.
func (f sshFleet) importFleet(t *testing.T, nodes ...map[string]any) {
	t.Helper()
	data, err := json.Marshal([]map[string]any{{"name": "fleet", "nodes": nodes}})
	require.NoError(t, err)
	file := filepath.Join(t.TempDir(), "fleet.json")
	require.NoError(t, os.WriteFile(file, data, 0o600))
	f.dagu.RunWithEnv(f.env, "nodes", "import", file, "--replace").ExpectExitCode(0)
}

// test runs dagu nodes test, answering its prompts with answers.
func (f sshFleet) test(answers string, args ...string) *harness.Result {
	return f.dagu.RunWithStdin(f.env, strings.NewReader(answers), append([]string{"nodes", "test"}, args...)...)
}

// dagu nodes test asks before it trusts an unknown key, and a declined key
// leaves the node failing. A changed key shows the trusted and presented
// fingerprints, and is replaced only on yes.
func TestNodesTest(t *testing.T) {
	t.Parallel()

	fleet := newSSHFleet(t)
	first := fleet.server(t)
	fleet.importFleet(t, fleet.node(t, "web01", first))
	fingerprint := ssh.FingerprintSHA256(first.HostKey)

	declined := fleet.test("n\n", "--labels", "web")
	declined.ExpectNonZeroExitCode()
	assert.Contains(t, declined.Stdout(), "web01  failed  ")
	assert.Contains(t, declined.Stdout(), `host key of node "web01" is unknown`)
	assert.Contains(t, declined.Stdout(), "Unknown host keys:\n  web01  "+fingerprint)
	declined.ExpectStderrContains("1 of 1 nodes failed the connection test")
	assert.Zero(t, first.AuthAttempts(), "a credential reached a host whose key is unknown")

	accepted := fleet.test("y\n", "WEB01")
	accepted.ExpectExitCode(0)
	for _, line := range []string{"trusted host key " + fingerprint + " of web01", "web01  passed  " + fingerprint, "1 passed, 0 failed"} {
		assert.Contains(t, accepted.Stdout(), line)
	}
	assert.Len(t, first.Commands(), 1, "the node is tested again once its key is trusted")

	// The node keeps its name and key, and moves to a host with another key.
	second := fleet.server(t)
	fleet.importFleet(t, fleet.node(t, "web01", second))
	changed := fleet.test("n\n", "web01")
	changed.ExpectNonZeroExitCode()
	assert.Contains(t, changed.Stdout(), "Changed host keys:\n  web01  trusted "+fingerprint+", presents "+ssh.FingerprintSHA256(second.HostKey))

	replaced := fleet.test("y\n")
	replaced.ExpectExitCode(0)
	assert.Contains(t, replaced.Stdout(), "replaced host key "+fingerprint+" of web01 with "+ssh.FingerprintSHA256(second.HostKey))
	assert.Equal(t, 1, second.AuthAttempts(), "only the test after the replace authenticates")

	fleet.test("", "web09").ExpectStderrContains(`node "web09" not found`)
}

// A node behind a bastion whose key is unknown takes two answers: one for
// the bastion's key, then one for the node's.
func TestNodesTestThroughBastion(t *testing.T) {
	t.Parallel()

	fleet := newSSHFleet(t)
	jump, target := fleet.server(t), fleet.server(t)
	web01 := fleet.node(t, "web01", target)
	web01["bastion"] = "jump"
	fleet.importFleet(t, fleet.node(t, "jump", jump), web01)

	result := fleet.test("y\ny\n", "web01")
	result.ExpectExitCode(0)
	out := result.Stdout()
	for _, line := range []string{
		`host key of bastion "jump" is unknown`,
		"trusted host key " + ssh.FingerprintSHA256(jump.HostKey) + " of jump",
		`host key of node "web01" is unknown`,
		"trusted host key " + ssh.FingerprintSHA256(target.HostKey) + " of web01",
		"web01  passed",
	} {
		assert.Contains(t, out, line)
	}
	assert.Less(t, strings.Index(out, "of jump"), strings.Index(out, "of web01"), "the bastion's key is asked about first")
}
