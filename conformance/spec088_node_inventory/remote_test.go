// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec088_node_inventory_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/test"
)

// remoteHome returns a Dagu home whose context "remote" points at a server
// that holds its own inventories.
func remoteHome(t *testing.T, dagu *harness.Runner) []string {
	t.Helper()
	server := test.SetupServer(t)
	env := home(t)
	url := fmt.Sprintf("http://%s:%d/api/v1", server.Config.Server.Host, server.Config.Server.Port)
	dagu.RunWithEnv(env, "context", "add", "remote", "--server="+url, "--api-key=dagu_remotenodes12345678").ExpectExitCode(0)
	return env
}

// inventoryIDs matches the inventory IDs in JSON output, which each
// installation assigns.
var inventoryIDs = regexp.MustCompile(`"id": "inv_[a-z0-9]+"`)

// The nodes commands print the same output and errors against a remote
// context as against the local installation, given the same inventories
// (Spec 088 §Commands).
func TestNodesCommandsOverRemoteContext(t *testing.T) {
	dagu := harness.NewRunner(t)
	local := home(t)
	remote := remoteHome(t, dagu)
	remotely := func(args ...string) []string { return append(args, "--context=remote") }

	imported := dagu.RunWithEnv(local, "nodes", "import", "inventories.json")
	imported.ExpectExitCode(0)
	assert.Equal(t, imported.Stdout(), dagu.RunWithEnv(remote, remotely("nodes", "import", "inventories.json")...).Stdout())
	// The import went to the server: the home of the remote context holds no
	// inventory of its own.
	assert.Empty(t, listNodes(t, dagu, remote))

	for _, args := range [][]string{
		{"nodes", "list"},
		{"nodes", "list", "--labels", "site=tokyo,!db", "--format", "json"},
		{"nodes", "list", "--inventory", "osaka STAGING"},
		{"nodes", "show", "TOKYO-DB01"},
		{"nodes", "show", "tokyo-db01", "--format", "json"},
		{"nodes", "export"},
		{"nodes", "export", "--inventory", "Osaka staging"},
		{"nodes", "show", "web99"},
		{"nodes", "list", "--inventory", "nowhere"},
		{"nodes", "list", "--labels", ","},
		{"nodes", "import", "inventories.json"},
		{"nodes", "import", "broken.json"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			want := dagu.RunWithEnv(local, args...)
			got := dagu.RunWithEnv(remote, remotely(args...)...)
			assert.Equal(t, want.ExitCode(), got.ExitCode())
			assert.Equal(t, inventoryIDs.ReplaceAllString(want.Stdout(), ""), inventoryIDs.ReplaceAllString(got.Stdout(), ""))
			assert.Equal(t, want.Stderr(), got.Stderr())
		})
	}

	removed := dagu.RunWithEnv(remote, remotely("nodes", "remove", "TOKYO-WEB02")...)
	removed.ExpectExitCode(0)
	assert.Contains(t, removed.Stdout(), "removed tokyo-web02 from Tokyo production")
	dagu.RunWithEnv(remote, remotely("nodes", "show", "tokyo-web02")...).ExpectStderrContains(`node "tokyo-web02" not found`)
}

// dagu nodes add and test reach the nodes from the server of a remote
// context: add tests a new node there before it stores it, and test offers
// to trust an unknown key (Spec 088 §Host keys).
func TestNodesAddAndTestOverRemoteContext(t *testing.T) {
	keyPEM, publicKey := harness.NewSSHKey(t)
	keyFile := filepath.Join(t.TempDir(), "id_ed25519")
	require.NoError(t, os.WriteFile(keyFile, keyPEM, 0o600))
	web01 := harness.StartSSHServer(t, harness.SSHServerOptions{User: "tester", AuthorizedKeys: []ssh.PublicKey{publicKey}})
	web02 := harness.StartSSHServer(t, harness.SSHServerOptions{User: "tester", AuthorizedKeys: []ssh.PublicKey{publicKey}})
	dagu := harness.NewRunner(t)
	remote := remoteHome(t, dagu)
	run := func(stdin string, args ...string) *harness.Result {
		return dagu.RunWithStdin(remote, strings.NewReader(stdin), append(args, "--context=remote")...)
	}

	fleet := filepath.Join(t.TempDir(), "fleet.json")
	data, err := json.Marshal([]map[string]any{{"name": "fleet", "defaults": map[string]any{"user": "tester", "key": keyFile}, "nodes": []any{}}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(fleet, data, 0o600))
	run("", "nodes", "import", fleet).ExpectExitCode(0)

	added := run("y\n", "nodes", "add", "web01", "--host", "127.0.0.1", "--port", web01.Port)
	added.ExpectExitCode(0)
	for _, line := range []string{
		`The host key of node "web01" is unknown: ` + ssh.FingerprintSHA256(web01.HostKey),
		"connection test of web01 passed",
		"added web01 in fleet",
		"trusted host key " + ssh.FingerprintSHA256(web01.HostKey) + " of web01",
	} {
		assert.Contains(t, added.Stdout(), line)
	}

	again := run("", "nodes", "add", "web01", "--host", "127.0.0.1", "--port", web01.Port)
	again.ExpectNonZeroExitCode()
	again.ExpectStderrContains(`node "web01" not added: inventory "fleet" already has a node "web01"`)
	run("", "nodes", "add", "web03", "--host", "127.0.0.1", "--key", "id_ed25519").
		ExpectStderrContains(`invalid --key "id_ed25519": the server reads the key on its own host`)

	run("", "nodes", "add", "web02", "--host", "127.0.0.1", "--port", web02.Port, "--no-test").ExpectExitCode(0)
	// Declining every key trusts nothing, and the summary still prints.
	declined := run("n\n", "nodes", "test")
	declined.ExpectNonZeroExitCode()
	assert.Contains(t, declined.Stdout(), "1 passed, 1 failed")
	declined.ExpectStderrContains("1 of 2 nodes failed the connection test")
	tested := run("y\n", "nodes", "test")
	tested.ExpectExitCode(0)
	for _, line := range []string{
		"Unknown host keys:\n  web02  " + ssh.FingerprintSHA256(web02.HostKey),
		"trusted host key " + ssh.FingerprintSHA256(web02.HostKey) + " of web02",
		"web02  passed",
		"2 passed, 0 failed",
	} {
		assert.Contains(t, tested.Stdout(), line)
	}
}
