// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// projectSettingsCheck returns a command that fails unless the secret and
// the profile default have the values given.
func projectSettingsCheck(token, tier string) string {
	return test.ForOS(
		fmt.Sprintf(`[ "$TOKEN" = %s ] && [ "$TIER" = %s ] && [ "$PROFILE_TOKEN" = profile-token ]`, token, tier),
		fmt.Sprintf(`if ($env:TOKEN -ne '%s' -or $env:TIER -ne '%s' -or $env:PROFILE_TOKEN -ne 'profile-token') { exit 1 }`, token, tier),
	)
}

// projectSettingsDAG returns a DAG that runs check in the run and in a
// sub-DAG run, and prints the secrets so the test can see them masked.
func projectSettingsDAG(check string) string {
	return fmt.Sprintf(`name: project-settings
labels: [workspace=ops]
secrets:
  - name: TOKEN
    ref: api/token
steps:
  - name: check
    run: |
      %[1]s
  - name: print
    run: echo "token=$TOKEN profile=$PROFILE_TOKEN"
  - name: child
    action: dag.run
    with:
      dag: project-settings-child
---
name: project-settings-child
labels: [workspace=ops]
secrets:
  - name: TOKEN
    ref: api/token
steps:
  - name: check
    run: |
      %[1]s
`, check)
}

// With projects on, a run reads its project's secret and profile defaults,
// never those of the workspace its DAG's label names. With projects off, the
// workspace's layers apply as in v2. Either way this holds for a started
// run, its sub-DAG run, a step retry, a queued run, a restarted run, and a
// dry run, and no secret value reaches the run's log.
func TestProjectSettingsSkipWorkspaceLabel(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		projectsOn  bool
		token, tier string
	}{
		{name: "ProjectsOn", projectsOn: true, token: "project-token", tier: "project"},
		{name: "ProjectsOff", projectsOn: false, token: "workspace-token", tier: "workspace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			th := test.SetupCommand(t, test.WithConfigMutator(func(cfg *config.Config) {
				cfg.ProjectsEnabled = tc.projectsOn
			}))
			seedProjectSettings(t, th)
			check := projectSettingsCheck(tc.token, tc.tier)
			dag := th.DAG(t, projectSettingsDAG(check))

			th.RunCommand(t, cmd.Start(), test.CmdTest{Args: []string{"start", "--profile=prod", dag.Location}})
			started := requireProjectSettingsRun(t, th, dag, "")

			// A retry of a run that succeeded runs only the step it names.
			th.RunCommand(t, cmd.Retry(), test.CmdTest{Args: []string{"retry", "--run-id=" + started.DAGRunID, "--step=check", dag.Location}})
			requireProjectSettingsRun(t, th, dag, started.DAGRunID)

			// A run's control socket is named by its DAG and run ID and sits
			// outside the test's directories, so each case needs its own ID.
			queuedRunID := "queued-" + strings.ToLower(tc.name)
			th.RunCommand(t, cmd.Enqueue(), test.CmdTest{Args: []string{"enqueue", "--run-id=" + queuedRunID, "--profile=prod", dag.Location}})
			th.RunCommand(t, cmd.Retry(), test.CmdTest{Args: []string{"retry", "--run-id=" + queuedRunID, dag.Location}})
			requireProjectSettingsRun(t, th, dag, queuedRunID)

			requireProjectSettingsRestart(t, th, check)

			// A dry run runs no command, but it resolves the DAG's secrets and
			// lists each that does not resolve: with projects on, a ref that
			// only the workspace holds.
			dry := th.DAG(t, `name: project-settings-dry
labels: [workspace=ops]
secrets:
  - name: TOKEN
    ref: api/ops-only
steps:
  - run: echo dry
`)
			var checks bytes.Buffer
			dryCmd := cmd.Dry()
			dryCmd.SetOut(&checks)
			th.RunCommand(t, dryCmd, test.CmdTest{Args: []string{"dry", "--format=json", dry.Location}})
			if tc.projectsOn {
				assert.Contains(t, checks.String(), "api/ops-only")
			} else {
				assert.NotContains(t, checks.String(), "api/ops-only")
			}
		})
	}
}

// requireProjectSettingsRestart restarts a running DAG whose first step runs
// check, and requires the restarted run to succeed.
func requireProjectSettingsRestart(t *testing.T, th test.Command, check string) {
	t.Helper()

	release := newHoldFile(t)
	dag := th.DAG(t, fmt.Sprintf(`labels: [workspace=ops]
secrets:
  - name: TOKEN
    ref: api/token
steps:
  - name: check
    run: |
      %s
  - name: hold
    run: %q
`, check, holdUntilFileExistsCommand(release)))
	started := make(chan struct{})
	go func() {
		defer close(started)
		_ = th.RunCommandWithError(t, cmd.Start(), test.CmdTest{Args: []string{"start", "--profile=prod", dag.Location}})
	}()
	dag.AssertCurrentStatus(t, ir.Running)

	restarted := make(chan error, 1)
	go func() {
		restarted <- th.RunCommandWithError(t, cmd.Restart(), test.CmdTest{Args: []string{"restart", dag.Location}})
	}()
	require.NoError(t, <-releaseHoldFileWhenRecentStatusCountAtLeast(t, th, dag.Name, 2, release))
	require.NoError(t, <-restarted)
	<-started

	latest, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.Equal(t, ir.Succeeded, latest.Status, "the restarted run read a value from the wrong settings")
}

// seedProjectSettings gives the default project and workspace ops the same
// secret ref and profile default with different values, workspace ops a
// secret of its own, and the default project a profile with a secret.
func seedProjectSettings(t *testing.T, th test.Command) {
	t.Helper()

	settings, err := file.NewProjectSettings(th.Config, file.NewServerStores(th.Config), project.DefaultName)
	require.NoError(t, err)
	secrets := settings.SecretStore(th.Context)
	require.NotNil(t, secrets)
	for _, seed := range []struct{ workspace, ref, value string }{
		{workspace: secret.GlobalWorkspace, ref: "api/token", value: "project-token"},
		{workspace: "ops", ref: "api/token", value: "workspace-token"},
		{workspace: "ops", ref: "api/ops-only", value: "ops-only-token"},
	} {
		sec, err := secret.New(secret.CreateInput{Workspace: seed.workspace, Ref: seed.ref, ProviderType: secret.ProviderDaguManaged}, time.Now())
		require.NoError(t, err)
		require.NoError(t, secrets.Create(th.Context, sec, &secret.WriteValueInput{Value: seed.value}))
	}

	profiles := settings.ProfileStore(th.Context)
	require.NotNil(t, profiles)
	opsRef, err := profile.WorkspaceInheritedRef("ops")
	require.NoError(t, err)
	for ref, value := range map[profile.InheritedRef]string{profile.GlobalInheritedRef(): "project", opsRef: "workspace"} {
		defaults, err := profile.NewInherited(ref, profile.InheritedCreateInput{}, time.Now())
		require.NoError(t, err)
		require.NoError(t, defaults.SetVariable("TIER", value, "test", time.Now()))
		require.NoError(t, profiles.Create(th.Context, defaults))
	}
	prod, err := profile.New(profile.CreateInput{Name: "prod"}, time.Now())
	require.NoError(t, err)
	require.NoError(t, profiles.Create(th.Context, prod))
	_, err = profile.NewManager(profiles, secrets).SetSecret(th.Context, prod, "PROFILE_TOKEN", "profile-token", "test")
	require.NoError(t, err)
}

// requireProjectSettingsRun requires the latest attempt of the run with
// runID, or of the latest run when runID is empty, to have succeeded with
// its secrets masked, and returns its status.
func requireProjectSettingsRun(t *testing.T, th test.Command, dag test.DAG, runID string) *ir.DAGRunStatus {
	t.Helper()

	var status *ir.DAGRunStatus
	if runID == "" {
		latest, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
		require.NoError(t, err)
		status = &latest
	} else {
		attempt, err := th.DAGRunRepository.FindAttempt(th.Context, ir.NewDAGRunRef(dag.Name, runID))
		require.NoError(t, err)
		status, err = attempt.ReadStatus(th.Context)
		require.NoError(t, err)
	}
	require.Equal(t, ir.Succeeded, status.Status, "a step read a value from the wrong settings")
	require.Len(t, status.Nodes, 3)
	log, err := os.ReadFile(status.Nodes[1].Stdout)
	require.NoError(t, err)
	assert.Contains(t, string(log), "token=******* profile=*******")
	stored, err := json.Marshal(status)
	require.NoError(t, err)
	for _, value := range []string{"project-token", "workspace-token", "profile-token"} {
		assert.NotContains(t, string(log), value)
		assert.NotContains(t, string(stored), value)
	}
	return status
}
