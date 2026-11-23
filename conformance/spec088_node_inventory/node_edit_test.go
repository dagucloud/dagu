// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec088_node_inventory_test

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// fleetHome returns a Dagu home with one empty inventory, "fleet".
func fleetHome(t *testing.T, dagu *harness.Runner) []string {
	t.Helper()
	env := home(t)
	file := filepath.Join(t.TempDir(), "fleet.json")
	require.NoError(t, os.WriteFile(file, []byte(`[{"name": "fleet", "nodes": []}]`), 0o600))
	dagu.RunWithEnv(env, "nodes", "import", file).ExpectExitCode(0)
	return env
}

// add runs dagu nodes add, answering a prompt with answer.
func add(dagu *harness.Runner, env []string, answer string, args ...string) *harness.Result {
	return dagu.RunWithStdin(env, strings.NewReader(answer), append([]string{"nodes", "add"}, args...)...)
}

// dagu nodes add tests a node before it saves it, and asks before it trusts
// an unknown host key. Removing the node forgets the key, so the node added
// again is asked about again.
func TestAddAndRemoveNode(t *testing.T) {
	t.Parallel()

	keyPEM, publicKey := harness.NewSSHKey(t)
	keyFile := filepath.Join(t.TempDir(), "id_ed25519")
	require.NoError(t, os.WriteFile(keyFile, keyPEM, 0o600))
	server := harness.StartSSHServer(t, harness.SSHServerOptions{User: "tester", AuthorizedKeys: []ssh.PublicKey{publicKey}})
	fingerprint := ssh.FingerprintSHA256(server.HostKey)
	dagu := harness.NewRunner(t)
	env := fleetHome(t, dagu)
	web01 := []string{"web01", "--host", "127.0.0.1", "--port", server.Port, "--user", "tester", "--key", keyFile, "--labels", "role=web"}

	declined := add(dagu, env, "n\n", web01...)
	declined.ExpectNonZeroExitCode()
	assert.Contains(t, declined.Stdout(), `The host key of node "web01" is unknown: `+fingerprint)
	declined.ExpectStderrContains(`node "web01" not added`)
	assert.Empty(t, listNodes(t, dagu, env), "a declined node is not added")
	assert.Zero(t, server.AuthAttempts(), "a credential reached a host whose key is unknown")

	accepted := add(dagu, env, "y\n", web01...)
	accepted.ExpectExitCode(0)
	for _, line := range []string{"added web01 in fleet", "trusted host key " + fingerprint + " of web01", "connection test of web01 passed"} {
		assert.Contains(t, accepted.Stdout(), line)
	}
	assert.Equal(t, []string{"role=web"}, findNode(t, listNodes(t, dagu, env), "web01").Labels)

	// The key is trusted now, so adding the node again asks nothing.
	again := add(dagu, env, "", web01...)
	again.ExpectExitCode(0)
	assert.Contains(t, again.Stdout(), "replaced web01 in fleet")

	removed := dagu.RunWithEnv(env, "nodes", "remove", "WEB01")
	removed.ExpectExitCode(0)
	assert.Contains(t, removed.Stdout(), "removed web01 from fleet")
	assert.Empty(t, listNodes(t, dagu, env))

	readded := add(dagu, env, "", web01...)
	readded.ExpectNonZeroExitCode()
	assert.Contains(t, readded.Stdout(), "is unknown: "+fingerprint, "a removed node's key is forgotten")
}

// A key the user accepts is only staged: when the node then fails to
// authenticate, nothing is added and nothing is trusted.
func TestAddNodeAcceptedKeyFailsAuthentication(t *testing.T) {
	t.Parallel()

	keyPEM, _ := harness.NewSSHKey(t)
	keyFile := filepath.Join(t.TempDir(), "id_ed25519")
	require.NoError(t, os.WriteFile(keyFile, keyPEM, 0o600))
	_, otherKey := harness.NewSSHKey(t)
	server := harness.StartSSHServer(t, harness.SSHServerOptions{User: "tester", AuthorizedKeys: []ssh.PublicKey{otherKey}})
	dagu := harness.NewRunner(t)
	env := fleetHome(t, dagu)
	web01 := []string{"web01", "--host", "127.0.0.1", "--port", server.Port, "--user", "tester", "--key", keyFile}

	refused := add(dagu, env, "y\n", web01...)
	refused.ExpectNonZeroExitCode()
	refused.ExpectStderrContains(`node "web01" not added: its connection test failed`)
	assert.Empty(t, listNodes(t, dagu, env))

	again := add(dagu, env, "n\n", web01...)
	assert.Contains(t, again.Stdout(), "is unknown: "+ssh.FingerprintSHA256(server.HostKey), "the accepted key was not trusted")
}

// A node whose connection test fails is not added, unless --no-test skips
// the test.
func TestAddNodeThatCannotConnect(t *testing.T) {
	t.Parallel()

	// A port that refuses connections.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	_, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	require.NoError(t, listener.Close())
	dagu := harness.NewRunner(t)
	env := fleetHome(t, dagu)
	db01 := []string{"db01", "--host", "127.0.0.1", "--port", port, "--key-secret", "ssh/db"}

	failed := add(dagu, env, "", db01...)
	failed.ExpectNonZeroExitCode()
	failed.ExpectStderrContains(`node "db01" not added: its connection test failed`)
	assert.Empty(t, listNodes(t, dagu, env))

	skipped := add(dagu, env, "", append(db01, "--no-test")...)
	skipped.ExpectExitCode(0)
	assert.Equal(t, map[string]string{"secret": "ssh/db"}, findNode(t, listNodes(t, dagu, env), "db01").Key)

	dagu.RunWithEnv(env, "nodes", "remove", "db09").ExpectStderrContains(`node "db09" not found`)
}

// A relative --key is stored as an absolute path, since the node is later
// connected from processes with other working directories.
func TestAddNodeRelativeKeyPath(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := fleetHome(t, dagu)
	keyFile := filepath.Join("keys", "id_ed25519")

	add(dagu, env, "", "db01", "--host", "127.0.0.1", "--key", keyFile, "--no-test").ExpectExitCode(0)
	assert.Equal(t, map[string]string{"file": dagu.ProjectPath(keyFile)}, findNode(t, listNodes(t, dagu, env), "db01").Key)
}
