// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// projectSettingsDAG returns a DAG that checks, in the run and in a sub-DAG
// run, that its secret and profile default have the values given, and
// prints the secrets so the test can see them masked.
func projectSettingsDAG(token, tier string) string {
	check := test.ForOS(
		fmt.Sprintf(`[ "$TOKEN" = %s ] && [ "$TIER" = %s ] && [ "$PROFILE_TOKEN" = profile-token ]`, token, tier),
		fmt.Sprintf(`if ($env:TOKEN -ne '%s' -or $env:TIER -ne '%s' -or $env:PROFILE_TOKEN -ne 'profile-token') { exit 1 }`, token, tier),
	)
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
// run, its sub-DAG run, a retry, and a queued run, and no secret value
// reaches the run's log.
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
			dag := th.DAG(t, projectSettingsDAG(tc.token, tc.tier))

			th.RunCommand(t, cmd.Start(), test.CmdTest{Args: []string{"start", "--profile=prod", dag.Location}})
			started := requireProjectSettingsRun(t, th, dag, "")

			th.RunCommand(t, cmd.Retry(), test.CmdTest{Args: []string{"retry", "--run-id=" + started.DAGRunID, dag.Location}})
			requireProjectSettingsRun(t, th, dag, started.DAGRunID)

			const queuedRunID = "queued-project-settings"
			th.RunCommand(t, cmd.Enqueue(), test.CmdTest{Args: []string{"enqueue", "--run-id=" + queuedRunID, "--profile=prod", dag.Location}})
			th.RunCommand(t, cmd.Retry(), test.CmdTest{Args: []string{"retry", "--run-id=" + queuedRunID, dag.Location}})
			requireProjectSettingsRun(t, th, dag, queuedRunID)
		})
	}
}

// seedProjectSettings gives the default project and workspace ops the same
// secret ref and profile default with different values, and the default
// project a profile with a secret.
func seedProjectSettings(t *testing.T, th test.Command) {
	t.Helper()

	secrets := file.NewSecretStore(th.Context, th.Config, th.Backend.Collection(persis.CollectionSecrets))
	require.NotNil(t, secrets)
	for workspace, value := range map[string]string{secret.GlobalWorkspace: "project-token", "ops": "workspace-token"} {
		sec, err := secret.New(secret.CreateInput{Workspace: workspace, Ref: "api/token", ProviderType: secret.ProviderDaguManaged}, time.Now())
		require.NoError(t, err)
		require.NoError(t, secrets.Create(th.Context, sec, &secret.WriteValueInput{Value: value}))
	}

	profiles := file.NewProfileStore(th.Context, th.Config, th.Backend.Collection(persis.CollectionProfiles))
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
	for _, value := range []string{"project-token", "workspace-token", "profile-token"} {
		assert.NotContains(t, string(log), value)
	}
	return status
}
