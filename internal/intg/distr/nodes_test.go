// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package distr_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	persiststore "github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	secretpkg "github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/test"
)

const (
	nodeUser   = "tester"
	nodeKeyRef = "ssh/fleet-key"
)

// fleetDAG runs, from a worker, a command step on every web node and then a
// child bound to each. Each appends a line to the file journal names. The
// test servers run commands on this host, so the file shows what each node
// ran.
func fleetDAG(journal string) string {
	return fmt.Sprintf(`
worker_selector:
  test: "true"
steps:
  - id: check
    run: echo checked ${node.name} >> %[1]s
    nodes: role=web
  - id: deploy
    depends: [check]
    action: dag.run
    with:
      dag: deploy-one
    nodes: role=web
---
name: deploy-one
node: required
steps:
  - run: echo deployed ${node.name} >> %[1]s
`, test.ShellQuote(journal))
}

// fleetNode is an inventory node that a test SSH server stands for.
type fleetNode struct {
	name  string
	label string
	// trust is the host key trusted for the node: nil trusts the server's
	// own key, and untrusted trusts none.
	trust     ssh.PublicKey
	untrusted bool
}

// startFleet starts an SSH server for each node, imports the nodes into the
// coordinator's inventory with a key read from the secret nodeKeyRef, and
// trusts their host keys as each node asks.
func startFleet(t *testing.T, f *testFixture, nodes ...fleetNode) (map[string]*harness.SSHServer, string) {
	t.Helper()
	ctx := f.coord.Context

	privateKey, publicKey := harness.NewSSHKey(t)
	secrets := persisfile.NewSecretStore(ctx, f.coord.Config, f.coord.Backend.Collection(persis.CollectionSecrets))
	now := time.Now()
	sec, err := secretpkg.New(secretpkg.CreateInput{Ref: nodeKeyRef, ProviderType: secretpkg.ProviderDaguManaged}, now)
	require.NoError(t, err)
	require.NoError(t, secrets.Create(ctx, sec, &secretpkg.WriteValueInput{Value: string(privateKey), CreatedBy: "test", CreatedAt: now}))

	hostKeys, err := persiststore.NewHostKeyStore(f.coord.Backend.Collection(persis.CollectionHostKeys))
	require.NoError(t, err)
	inventories, err := persiststore.NewInventoryStore(
		f.coord.Backend.Collection(persis.CollectionInventories), f.coord.Backend.Lock(persis.CollectionInventories), hostKeys)
	require.NoError(t, err)

	servers := make(map[string]*harness.SSHServer, len(nodes))
	inv := inventory.Inventory{Name: "fleet"}
	for _, node := range nodes {
		server := harness.StartSSHServer(t, harness.SSHServerOptions{User: nodeUser, AuthorizedKeys: []ssh.PublicKey{publicKey}})
		port, err := strconv.Atoi(server.Port)
		require.NoError(t, err)
		servers[node.name] = server
		inv.Nodes = append(inv.Nodes, inventory.Node{
			Name:   node.name,
			Host:   "127.0.0.1",
			Labels: []string{node.label},
			Connection: inventory.Connection{
				Port: port,
				User: nodeUser,
				Key:  map[string]any{"secret": nodeKeyRef},
			},
		})
	}
	_, err = inventories.Create(ctx, inv)
	require.NoError(t, err)
	for _, node := range nodes {
		if node.untrusted {
			continue
		}
		key := node.trust
		if key == nil {
			key = servers[node.name].HostKey
		}
		_, err := hostKeys.Trust(ctx, node.name, key, "admin", "")
		require.NoError(t, err)
	}
	return servers, string(privateKey)
}

// journalLines returns the lines of journal, sorted.
func journalLines(t *testing.T, journal string) []string {
	t.Helper()
	data, err := os.ReadFile(journal)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	slices.Sort(lines)
	return lines
}

// A run on a worker reaches SSH nodes with a key from a secret and host keys
// trusted on the server, from a command step with nodes and from children
// bound to each node (RFC 012 acceptance item 5). The key never reaches the
// run's status.
func TestNodes_RunOnWorker(t *testing.T) {
	t.Parallel()

	journal := filepath.Join(t.TempDir(), "journal")
	f := newTestFixture(t, fleetDAG(journal))
	servers, privateKey := startFleet(t, f,
		fleetNode{name: "web01", label: "role=web"},
		fleetNode{name: "web02", label: "role=web"},
	)

	_, err := f.dispatchDirect()
	require.NoError(t, err)
	status := f.waitForStatus(ir.Succeeded, executionStatusTimeout())
	f.assertWorkerID(status, "worker-1")
	for name, server := range servers {
		require.Len(t, server.Commands(), 2, "%s runs the command step and its bound child", name)
	}
	require.Equal(t, []string{"checked web01", "checked web02", "deployed web01", "deployed web02"}, journalLines(t, journal))

	statusJSON, err := json.Marshal(status)
	require.NoError(t, err)
	require.NotContains(t, string(statusJSON), strings.TrimSpace(privateKey))
}

// A worker checks host keys as the server does: a node whose key is not
// trusted, or whose key changed since it was trusted, fails, and no
// credential reaches it.
func TestNodes_WorkerRefusesUntrustedHostKey(t *testing.T) {
	t.Parallel()

	f := newTestFixture(t, `
worker_selector:
  test: "true"
steps:
  - id: check
    run: echo checked ${node.name}
    nodes:
      labels: role=web
      keep_going: true
`)
	_, otherKey := harness.NewSSHKey(t)
	servers, _ := startFleet(t, f,
		fleetNode{name: "web-changed", label: "role=web", trust: otherKey},
		fleetNode{name: "web-new", label: "role=web", untrusted: true},
	)

	_, err := f.dispatchDirect()
	require.NoError(t, err)
	status := f.waitForStatus(ir.Failed, executionStatusTimeout())
	f.assertWorkerID(status, "worker-1")
	require.Contains(t, status.Nodes[0].Error, `host key of node "web-changed" has changed`)
	for name, server := range servers {
		require.Zero(t, server.AuthAttempts(), "%s was sent a credential", name)
		require.Empty(t, server.Commands(), "%s ran a command", name)
	}
}

// A narrowed run dispatched to a worker keeps its node list there, for its
// own steps and for its child runs (RFC 013 §Narrowing a run).
func TestNodes_NarrowedRunOnWorker(t *testing.T) {
	t.Parallel()

	journal := filepath.Join(t.TempDir(), "journal")
	f := newTestFixture(t, fleetDAG(journal))
	servers, _ := startFleet(t, f,
		fleetNode{name: "web01", label: "role=web"},
		fleetNode{name: "web02", label: "role=web"},
	)

	_, err := f.dispatchDirect(executor.WithTargetNodes([]string{"web01"}))
	require.NoError(t, err)
	status := f.waitForStatus(ir.Succeeded, executionStatusTimeout())
	f.assertWorkerID(status, "worker-1")
	require.Equal(t, []string{"web01"}, status.TargetNodes)
	require.Equal(t, []string{"checked web01", "deployed web01"}, journalLines(t, journal))
	require.Len(t, servers["web01"].Commands(), 2)
	require.Empty(t, servers["web02"].Commands(), "web02 is not listed")
}

// A narrowed run that fails before its agent writes a status keeps its list,
// so its retry on a worker stays narrowed. Here the coordinator fails the run
// when no worker matches it in time.
func TestNodes_NarrowedRetryKeepsList(t *testing.T) {
	t.Parallel()

	journal := filepath.Join(t.TempDir(), "journal")
	f := newTestFixture(t, fleetDAG(journal),
		withWorkerCount(0), withWorkerWaitTimeout(2*time.Second), withZombieDetectionInterval(500*time.Millisecond))
	servers, _ := startFleet(t, f,
		fleetNode{name: "web01", label: "role=web"},
		fleetNode{name: "web02", label: "role=web"},
	)

	runID, err := f.dispatchDirect(executor.WithTargetNodes([]string{"web01"}))
	require.NoError(t, err)
	failed := f.waitForStatus(ir.Failed, distrTestTimeout(30*time.Second))
	require.Equal(t, []string{"web01"}, failed.TargetNodes)

	f.workers = append(f.workers, f.setupWorker("worker-1", map[string]string{"test": "true"}, ""))
	dag := f.dagWrapper.DAG
	retry := executor.CreateTask(dag.Name, string(dag.YamlData), dispatch.DispatchOperationRetry, runID,
		executor.WithWorkerSelector(dag.WorkerSelector), executor.WithPreviousStatus(&failed))
	require.NoError(t, f.coordinatorClient.Dispatch(f.coord.Context, dispatch.DispatchRequest{Task: retry}))

	status := f.waitForStatus(ir.Succeeded, executionStatusTimeout())
	require.Equal(t, []string{"web01"}, status.TargetNodes)
	require.Equal(t, []string{"checked web01", "deployed web01"}, journalLines(t, journal))
	require.Empty(t, servers["web02"].Commands(), "web02 is not listed")
}
