// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec089_node_targeting_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/secret"
)

const (
	// fleetPassword is the password the SSH fleet accepts. Runs read it only
	// from the secret fleetPasswordRef.
	fleetPassword    = "fleet-password-6t2w"
	fleetPasswordRef = "ssh/fleet-password"
)

// sshFleet starts an SSH server for each of web01, web02, and web03, which
// accept fleetPassword. It stores them as an inventory whose nodes pin the
// servers' host keys and give the password as a secret ref. It returns the
// environment that points dagu at a new home, and that home.
func sshFleet(t *testing.T) ([]string, string) {
	t.Helper()

	ctx := context.Background()
	home := t.TempDir()
	cfg := &config.Config{Paths: config.PathsConfig{DataDir: filepath.Join(home, "data")}}
	backend := file.NewBackend(cfg.Paths)

	secrets := file.NewSecretStore(ctx, cfg, backend.Collection(persis.CollectionSecrets))
	require.NotNil(t, secrets)
	password, err := secret.New(secret.CreateInput{Ref: fleetPasswordRef, ProviderType: secret.ProviderDaguManaged}, time.Time{})
	require.NoError(t, err)
	require.NoError(t, secrets.Create(ctx, password, &secret.WriteValueInput{Value: fleetPassword}))

	var nodes []inventory.Node
	for _, name := range []string{"web01", "web02", "web03"} {
		server := harness.StartSSHServer(t, harness.SSHServerOptions{User: "tester", Password: fleetPassword})
		port, err := strconv.Atoi(server.Port)
		require.NoError(t, err)
		nodes = append(nodes, inventory.Node{
			Name: name,
			Host: "127.0.0.1",
			Connection: inventory.Connection{
				Port:     port,
				User:     "tester",
				Password: map[string]any{"secret": fleetPasswordRef},
				HostKey:  strings.TrimSpace(string(ssh.MarshalAuthorizedKey(server.HostKey))),
			},
			Labels: []string{"web", "role=" + name},
		})
	}
	inventories, err := store.NewInventoryStore(
		backend.Collection(persis.CollectionInventories),
		backend.Lock(persis.CollectionInventories),
		nil,
	)
	require.NoError(t, err)
	_, err = inventories.Create(ctx, inventory.Inventory{Name: "fleet", Nodes: nodes})
	require.NoError(t, err)
	return []string{"DAGU_HOME=" + home, "DAGU_DATA_DIR=" + cfg.Paths.DataDir}, home
}

// requireNoSecret fails when the fleet password appears in dagu's output or
// in any file under home, which holds the logs and run status.
func requireNoSecret(t *testing.T, result *harness.Result, home string) {
	t.Helper()

	result.ExpectStdoutNotContains(fleetPassword)
	result.ExpectStderrNotContains(fleetPassword)
	require.NoError(t, filepath.WalkDir(home, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path) //nolint:gosec // path is under the test's home
		if err != nil {
			return err
		}
		require.NotContains(t, string(data), fleetPassword, path)
		return nil
	}))
}

// Bound runs reach their nodes over SSH, with the password from its secret.
func TestNodeStepsOverSSH(t *testing.T) {
	t.Parallel()

	// With max_concurrent: 1, every step of one node's run finishes before
	// the next node's run starts.
	t.Run("rolling.yaml", func(t *testing.T) {
		t.Parallel()

		env, home := sshFleet(t)
		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env, "start", "rolling.yaml", "--", "DIR="+filepath.ToSlash(dagu.ProjectPath(".")))
		result.ExpectExitCode(0)
		require.Equal(t, []string{"web01=succeeded", "web02=succeeded", "web03=succeeded"}, nodeStatuses(t, dagu))
		dagu.ExpectTextFileContent("order.log",
			"web01 drain\nweb01 restart\nweb02 drain\nweb02 restart\nweb03 drain\nweb03 restart\n")
		requireNoSecret(t, result, home)
	})

	// A command step runs once on each node, reading the node's fields.
	t.Run("command_step.yaml", func(t *testing.T) {
		t.Parallel()

		env, home := sshFleet(t)
		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env, "start", "command_step.yaml", "--", "DIR="+filepath.ToSlash(dagu.ProjectPath(".")))
		result.ExpectExitCode(0)
		require.Equal(t, []string{"web01=succeeded", "web02=succeeded", "web03=succeeded"}, nodeStatuses(t, dagu))
		dagu.ExpectTextFileContent("ran.log",
			"web01 role=web01,web\nweb02 role=web02,web\nweb03 role=web03,web\n")
		requireNoSecret(t, result, home)
	})
}
