// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec089_node_targeting_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/stretchr/testify/require"
)

// startWithMarkerDir runs a DAG whose child runs write child-ran into the
// project directory, given to them as the param DIR.
func startWithMarkerDir(t *testing.T, dagu *harness.Runner, file string) *harness.Result {
	t.Helper()
	return dagu.RunWithEnv(nodesEnv(t), "start", file, "--", "DIR="+filepath.ToSlash(dagu.ProjectPath(".")))
}

// fleet is the inventory the runs target: three web nodes, one disabled.
var fleet = []inventory.Node{
	{Name: "web01", Host: "10.0.1.11", Labels: []string{"web"}},
	{Name: "web02", Host: "10.0.1.12", Labels: []string{"web"}, Disabled: true},
	{Name: "web03", Host: "10.0.1.13", Labels: []string{"web"}},
}

// nodesEnv stores fleet in a new data directory and returns the environment
// that points dagu at it. It stores through the inventory store, as the
// command that imports inventories lands separately.
func nodesEnv(t *testing.T) []string {
	t.Helper()

	dataDir := t.TempDir()
	backend := file.NewBackend(config.PathsConfig{DataDir: dataDir})
	inventories, err := store.NewInventoryStore(
		backend.Collection(persis.CollectionInventories),
		backend.Lock(persis.CollectionInventories),
		nil,
	)
	require.NoError(t, err)
	_, err = inventories.Create(context.Background(), inventory.Inventory{Name: "fleet", Nodes: fleet})
	require.NoError(t, err)
	return []string{"DAGU_DATA_DIR=" + dataDir}
}

// nodeStatuses reads the aggregate a node step wrote and returns each node's
// status, in the aggregate's order.
func nodeStatuses(t *testing.T, dagu *harness.Runner) []string {
	t.Helper()

	data, err := os.ReadFile(dagu.ProjectPath("nodes.json"))
	require.NoError(t, err)
	var aggregate struct {
		Results []struct {
			Node   string `json:"node"`
			Status string `json:"status"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal(data, &aggregate))
	var statuses []string
	for _, result := range aggregate.Results {
		statuses = append(statuses, result.Node+"="+result.Status)
	}
	return statuses
}

func TestNodeStepRuns(t *testing.T) {
	t.Parallel()

	t.Run("fanout.yaml", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		startWithMarkerDir(t, dagu, "fanout.yaml").ExpectExitCode(0)
		require.Equal(t, []string{"web01=succeeded", "web02=disabled", "web03=succeeded"}, nodeStatuses(t, dagu))
		dagu.ExpectFileContent("child-ran", "ran")
	})
	for _, tc := range []struct {
		file string
		want []string
	}{
		{"stop_at_failure.yaml", []string{"web01=failed", "web02=disabled", "web03=not_started"}},
		{"keep_going.yaml", []string{"web01=failed", "web02=disabled", "web03=failed"}},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			dagu.RunWithEnv(nodesEnv(t), "start", tc.file).ExpectNonZeroExitCode()
			require.Equal(t, tc.want, nodeStatuses(t, dagu))
		})
	}
}

func TestNodeStepFailsBeforeAnyNode(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		file      string
		wantError string
	}{
		{"no_match.yaml", "selector matched no nodes: db"},
		{"child_not_required.yaml", `DAG "deploy-one" does not declare node: required`},
		{"node_required_unbound.yaml", "runs only bound to a node"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := startWithMarkerDir(t, dagu, tc.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.wantError)
			dagu.ExpectNoFile("child-ran")
		})
	}
}
