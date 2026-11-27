// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec107_connections_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/secret"
)

const missingConnection = `connection not found: "missing" in workspace "ops"`

// connections stores the records of Spec 107's examples in a new home: in
// ops, work-google on an app with the agent flag and ops-mail on one
// without, and shared-google for DAGs without a workspace label. It returns
// the environment that points dagu at that home, and the store.
func connections(t *testing.T) ([]string, connection.Store) {
	t.Helper()

	ctx := context.Background()
	_, env, store := newHome(t)
	newApp := func(name string, agent bool) string {
		app, err := store.CreateApp(ctx, &connection.App{
			Name:       name,
			Provider:   connection.ProviderGoogle,
			Kind:       connection.AppKindBYO,
			ClientID:   name + "-client",
			ClientType: connection.ClientTypeWeb,
			Agent:      agent,
		})
		require.NoError(t, err)
		return app.ID
	}
	agentApp, stdApp := newApp("google-agent", true), newApp("google-std", false)
	gmail := []connection.Service{{Name: "gmail", Access: connection.AccessRead}}
	for _, conn := range []connection.Connection{
		{Workspace: "ops", Name: "work-google", App: agentApp, Services: gmail},
		{Workspace: "ops", Name: "ops-mail", App: stdApp, Services: []connection.Service{
			{Name: "gmail", Access: connection.AccessWrite},
			{Name: "mailbox", Access: connection.AccessWrite},
		}},
		{Workspace: secret.GlobalWorkspace, Name: "shared-google", App: agentApp, Services: gmail},
	} {
		conn.Provider = connection.ProviderGoogle
		conn.Grant = connection.GrantAuthorizationCode
		_, err := store.Create(ctx, &conn)
		require.NoError(t, err)
	}
	return env, store
}

// newHome returns a new dagu home, the environment that points dagu at it,
// and its connection store.
func newHome(t *testing.T) (string, []string, connection.Store) {
	t.Helper()

	home := t.TempDir()
	cfg := &config.Config{Paths: config.PathsConfig{DataDir: filepath.Join(home, "data")}}
	store, err := file.NewServerStores(cfg).ConnectionStore()
	require.NoError(t, err)
	return home, []string{"DAGU_HOME=" + home, "DAGU_DATA_DIR=" + cfg.Paths.DataDir}, store
}

// A run whose connection check fails stops before its first step, and the
// error names the connection.
func TestAdmissionFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		file   string
		stderr string
	}{
		{"missing.yaml", missingConnection},
		{"no_agent.yaml", `connection "ops-mail": app "google-std" lacks the agent flag`},
		{"not_a_service.yaml", `connection "work-google": server "drive" is not one of its services`},
		{"no_mcp_server.yaml", `connection "ops-mail": service "mailbox" has no MCP server`},
		// A .global connection is no fallback for a DAG in a workspace.
		{"global_no_fallback.yaml", `connection not found: "shared-google" in workspace "ops"`},
		// A workspace's connection never serves a DAG without a label.
		{"workspace_no_fallback.yaml", `connection not found: "work-google"`},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			t.Parallel()

			env, _ := connections(t)
			dagu := harness.NewRunner(t)
			result := dagu.RunWithEnv(env, "start", tt.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tt.stderr)
			dagu.ExpectNoFile("executed.txt")
		})
	}
}

// A run whose connections pass the checks reaches its first step.
func TestAdmitted(t *testing.T) {
	t.Parallel()

	for _, file := range []string{"admitted.yaml", "global_admitted.yaml"} {
		t.Run(file, func(t *testing.T) {
			t.Parallel()

			env, _ := connections(t)
			dagu := harness.NewRunner(t)
			dagu.RunWithEnv(env, "start", file)
			dagu.ExpectFileContains("executed.txt", "executed")
		})
	}
}

// A retry checks the connections again before its first step.
func TestRetryChecksConnections(t *testing.T) {
	t.Parallel()

	env, _ := connections(t)
	dagu := harness.NewRunner(t)
	dagu.RunWithEnv(env, "start", "--run-id=admission-retry", "missing.yaml").ExpectNonZeroExitCode()

	retry := dagu.RunWithEnv(env, "retry", "--run-id=admission-retry", "missing.yaml")
	retry.ExpectNonZeroExitCode()
	retry.ExpectStderrContains(missingConnection)
	dagu.ExpectNoFile("executed.txt")
}

// A restarted run checks its connections again, so one removed while the
// first run was active stops the new run before its first step.
func TestRestartChecksConnections(t *testing.T) {
	t.Parallel()

	env, store := connections(t)
	dagu := harness.NewRunner(t)
	proc := dagu.StartWithEnv(env, "start", "restart.yaml")
	defer proc.Stop()
	waitForFile(t, dagu, proc, "marks.txt")

	require.NoError(t, store.Delete(context.Background(), connection.Key{Workspace: "ops", Name: "work-google"}))
	restart := dagu.RunWithEnv(env, "restart", "restart")
	restart.ExpectStderrContains(`connection not found: "work-google" in workspace "ops"`)

	select {
	case <-proc.Done():
	case <-time.After(harness.WaitTimeout(t)):
		t.Fatalf("the restarted run's original did not stop: %s", proc.FailureOutput())
	}
	dagu.ExpectTextFileContent("marks.txt", "started\n")
}

// A dry run reports a failed connection check as a check that may fail on
// this host.
func TestDryRunReportsConnections(t *testing.T) {
	t.Parallel()

	env, _ := connections(t)
	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(env, "dry", "missing.yaml")
	result.ExpectExitCode(0)
	// The warning is a logfmt line, which escapes the quotes in the error.
	result.ExpectStderrContains("Dry run: run may fail on this host", `connection not found: \"missing\" in workspace \"ops\"`)
}

// A sub-DAG that runs in the parent's process checks its own connections
// before its first step.
func TestSubDAGChecksConnections(t *testing.T) {
	t.Parallel()

	env, _ := connections(t)
	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(env, "start", "sub_dag.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains(missingConnection)
	dagu.ExpectNoFile("executed.txt")
}

// A connection name must be a literal, so a DAG that takes it from a
// parameter fails validation.
func TestNameFromParameter(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	result := dagu.Run("validate", "reference.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("steps.unread.with.connection", "must be a literal name, not a reference")
}

// waitForFile waits until proc's run writes name in the project.
func waitForFile(t *testing.T, dagu *harness.Runner, proc *harness.Process, name string) {
	t.Helper()

	deadline := time.Now().Add(harness.WaitTimeout(t))
	for {
		if _, err := os.Stat(dagu.ProjectPath(name)); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never appeared: %s", name, proc.FailureOutput())
		}
		select {
		case <-proc.Done():
			t.Fatalf("process exited before %s appeared: %s", name, proc.FailureOutput())
		case <-time.After(50 * time.Millisecond):
		}
	}
}
