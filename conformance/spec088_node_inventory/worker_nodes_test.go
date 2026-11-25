// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec088_node_inventory_test

import (
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// workerConflict is what `dagu nodes list` reports for the worker whose ID
// names tokyo-web01 of the imported inventories.
const workerConflict = `worker "tokyo-web01" is not a node: inventory "Tokyo production" has a node of that name`

// Workers that set worker.id are listed as nodes, beside the imported
// inventories' nodes, and a worker whose ID an inventory node holds is
// reported instead of listed. The commands print the same through a remote
// context (Spec 088 §Worker nodes).
func TestWorkerNodes(t *testing.T) {
	dagu := harness.NewRunner(t)
	env := append(home(t), "DAGU_AUTH_MODE=none")
	dagu.RunWithEnv(env, "nodes", "import", "inventories.json").ExpectExitCode(0)

	serverPort, coordinatorPort := harness.FreePort(t), harness.FreePort(t)
	services := dagu.StartWithEnv(env,
		"start-all",
		"--host=127.0.0.1",
		fmt.Sprintf("--port=%d", serverPort),
		"--dags="+dagu.ProjectPath("."),
		"--coordinator.host=127.0.0.1",
		"--coordinator.advertise=127.0.0.1",
		fmt.Sprintf("--coordinator.port=%d", coordinatorPort),
	)
	defer services.Stop()
	waitForListener(t, services, fmt.Sprintf("127.0.0.1:%d", coordinatorPort))
	for _, args := range [][]string{
		{"--worker.id=tokyo-gw", "--worker.labels=web,site=tokyo"},
		{"--worker.id=tokyo-web01"},
	} {
		worker := dagu.StartWithEnv(env, append([]string{
			"worker",
			"--worker.health-port=0",
			fmt.Sprintf("--worker.coordinators=127.0.0.1:%d", coordinatorPort),
		}, args...)...)
		defer worker.Stop()
	}

	nodes := waitForWorkerNodes(t, dagu, env)
	gw := findNode(t, nodes, "tokyo-gw")
	assert.Equal(t, "worker", gw.Kind)
	assert.True(t, gw.Online)
	assert.NotEmpty(t, gw.LastSeenAt)
	assert.NotEmpty(t, gw.Host)
	assert.Subset(t, gw.Labels, []string{"site=tokyo", "web"})
	assert.True(t, slices.IsSorted(gw.Labels))
	assert.Equal(t, "ssh", findNode(t, nodes, "tokyo-web01").Kind)

	assert.Equal(t, []string{"tokyo-gw", "tokyo-web01", "tokyo-web02"}, nodeNames(listNodes(t, dagu, env, "--labels=web,site=tokyo")))
	assert.NotContains(t, nodeNames(listNodes(t, dagu, env, "--inventory=Tokyo production")), "tokyo-gw")

	table := dagu.RunWithEnv(env, "nodes", "list")
	table.ExpectExitCode(0)
	gwRow := lineFields(t, table.Stdout(), "tokyo-gw")
	assert.Equal(t, []string{"tokyo-gw", "-", "worker", gw.Host, strings.Join(gw.Labels, ","), "online"}, gwRow)

	shown := dagu.RunWithEnv(env, "nodes", "show", "TOKYO-GW")
	shown.ExpectExitCode(0)
	assert.Equal(t, []string{"Kind", "worker"}, lineFields(t, shown.Stdout(), "Kind"))
	assert.Equal(t, []string{"Status", "online"}, lineFields(t, shown.Stdout(), "Status"))

	online := dagu.RunWithEnv(env, "nodes", "forget", "tokyo-gw")
	online.ExpectNonZeroExitCode()
	online.ExpectStderrContains(`worker "tokyo-gw" is online; a worker is forgotten once it is offline`)
	sshNode := dagu.RunWithEnv(env, "nodes", "forget", "tokyo-db01")
	sshNode.ExpectNonZeroExitCode()
	sshNode.ExpectStderrContains(`node "tokyo-db01" is a node of inventory "Tokyo production"; remove it from its inventory`)

	url := fmt.Sprintf("http://127.0.0.1:%d/api/v1", serverPort)
	dagu.RunWithEnv(env, "context", "add", "remote", "--server="+url, "--api-key=dagu_workernodes12345678").ExpectExitCode(0)
	for _, args := range [][]string{
		{"nodes", "list"},
		{"nodes", "list", "--labels", "web", "--format", "json"},
		{"nodes", "list", "--inventory", "Tokyo production"},
		{"nodes", "show", "TOKYO-GW"},
		{"nodes", "show", "tokyo-gw", "--format", "json"},
		{"nodes", "forget", "tokyo-gw"},
		{"nodes", "forget", "tokyo-db01"},
		{"nodes", "forget", "nagoya-gw"},
		{"nodes", "remove", "tokyo-gw"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			want := dagu.RunWithEnv(env, args...)
			got := dagu.RunWithEnv(env, append(args, "--context=remote")...)
			assert.Equal(t, want.ExitCode(), got.ExitCode())
			assert.Equal(t, want.Stdout(), got.Stdout())
			assert.Equal(t, want.Stderr(), got.Stderr())
		})
	}
}

// waitForWorkerNodes lists the nodes until both workers are recorded: one
// listed, and one reported as no node.
func waitForWorkerNodes(t *testing.T, dagu *harness.Runner, env []string) []node {
	t.Helper()
	deadline := time.Now().Add(harness.WaitTimeout(t))
	var last *harness.Result
	for time.Now().Before(deadline) {
		last = dagu.RunWithEnv(env, "nodes", "list", "--format=json")
		var nodes []node
		if last.ExitCode() == 0 && json.Unmarshal([]byte(last.Stdout()), &nodes) == nil &&
			slices.ContainsFunc(nodes, func(n node) bool { return n.Name == "tokyo-gw" }) &&
			strings.Contains(last.Stderr(), workerConflict) {
			return nodes
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the workers were not recorded:\nstdout:\n%s\nstderr:\n%s", last.Stdout(), last.Stderr())
	return nil
}

func waitForListener(t *testing.T, process *harness.Process, address string) {
	t.Helper()
	deadline := time.Now().Add(harness.WaitTimeout(t))
	for time.Now().Before(deadline) {
		select {
		case <-process.Done():
			t.Fatalf("service exited during startup: %s", process.FailureOutput())
		default:
		}
		if connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond); err == nil {
			require.NoError(t, connection.Close())
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("service did not listen on %s", address)
}
