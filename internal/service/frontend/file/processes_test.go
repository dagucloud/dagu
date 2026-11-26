// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dagsettings"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/incident"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/notification"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	fileeventstore "github.com/dagucloud/dagu/v2/internal/persis/file/eventstore"
	filemonitor "github.com/dagucloud/dagu/v2/internal/persis/file/monitor"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/secret"
	schedulerfile "github.com/dagucloud/dagu/v2/internal/service/scheduler/file"
)

const sharedDAG = "etl"

// Each process opens the default project's stores on its own, and they
// agree: what the server writes, the scheduler, runs, and workers read, and
// the server reads the run events that runs write. With projects off or on,
// the default project's state stays where v2 keeps it.
func TestStoresShareDefaultProject(t *testing.T) {
	// Without a key in the environment, each process reads the key file
	// in paths.data_dir, so decrypting what another wrote shows they share
	// it.
	t.Setenv("DAGU_ENCRYPTION_KEY", "")
	for _, projectsOn := range []bool{false, true} {
		t.Run(map[bool]string{false: "Off", true: "On"}[projectsOn], func(t *testing.T) {
			cfg := sharedStoresConfig(t, projectsOn)
			assertProcessesAgree(t, cfg)

			data := cfg.Paths.DataDir
			assertState(t, filemonitor.NewStateStore(filepath.Join(data, "notifications", "monitor-state.json")).Load, `"notified"`)
			assertState(t, filemonitor.NewStateStore(filepath.Join(data, "incidents", "monitor-state.json")).Load, `"opened"`)
			v2Events, err := fileeventstore.New(cfg.Paths.EventStoreDir)
			require.NoError(t, err)
			result, err := v2Events.Query(t.Context(), eventstore.QueryFilter{DAGName: sharedDAG})
			require.NoError(t, err)
			assert.Len(t, result.Entries, 1)
			assert.NoDirExists(t, filepath.Join(cfg.Paths.DataDir, "projects", project.DefaultName))
		})
	}
}

// assertProcessesAgree writes the default project's settings and state
// through the server's stores and reads them back through the scheduler's,
// a command's, and a worker's.
func assertProcessesAgree(t *testing.T, cfg *config.Config) {
	t.Helper()
	ctx := t.Context()
	server, err := newStores(t, cfg)
	require.NoError(t, err)
	schedulerShared := persisfile.NewServerStores(cfg)
	schedulerSettings := defaultSettings(t, cfg, schedulerShared)
	scheduler, err := schedulerfile.NewDependencies(ctx, cfg, schedulerShared, schedulerSettings)
	require.NoError(t, err)
	// A command or the coordinator opens every store; a worker opens only
	// settings.
	runs, err := persisfile.NewProjectStores(ctx, cfg, persisfile.NewServerStores(cfg),
		project.Project{Name: project.DefaultName}, persisfile.ProjectStoreOptions{})
	require.NoError(t, err)
	worker := defaultSettings(t, cfg, persisfile.NewServerStores(cfg))

	secretID := createSecret(t, server.Secret, "hunter2")
	for name, secrets := range map[string]secret.Store{"runs": runs.SecretStore(ctx), "worker": worker.SecretStore(ctx)} {
		require.NotNil(t, secrets, name)
		value, _, err := secrets.ReadValue(ctx, secretID)
		require.NoError(t, err, name)
		assert.Equal(t, "hunter2", value, name)
	}

	require.NoError(t, server.DAGSettings.Upsert(ctx, &dagsettings.Settings{DAGName: sharedDAG, Profile: "prod"}))
	settings, err := scheduler.DAGSettingsStore.Get(ctx, sharedDAG)
	require.NoError(t, err)
	assert.Equal(t, "prod", settings.Profile)

	require.NoError(t, server.Notification.Save(ctx, &notification.Settings{DAGName: sharedDAG, Enabled: true}))
	routes, err := scheduler.NotificationStore.GetByDAGName(ctx, sharedDAG)
	require.NoError(t, err)
	assert.True(t, routes.Enabled)

	require.NoError(t, server.Incident.SavePolicySet(ctx, &incident.PolicySet{
		Scope: incident.PolicyScopeDAG, DAGName: sharedDAG, Enabled: true,
	}))
	policies, err := scheduler.IncidentStore.GetPolicySet(ctx, incident.PolicyScopeDAG, "", sharedDAG)
	require.NoError(t, err)
	assert.True(t, policies.Enabled)

	require.NoError(t, server.NotificationState.Save(ctx, []byte(`"notified"`)))
	require.NoError(t, server.IncidentState.Save(ctx, []byte(`"opened"`)))
	assertState(t, scheduler.NotificationState.Load, `"notified"`)
	assertState(t, scheduler.IncidentState.Load, `"opened"`)

	// A command writes run events in the default project's event directory.
	cliEvents, err := fileeventstore.New(runs.Paths.EventStoreDir)
	require.NoError(t, err)
	event := eventstore.NewDAGRunEvent(eventstore.Source{Service: eventstore.SourceServiceCLI}, eventstore.TypeDAGRunSucceeded,
		&ir.DAGRunStatus{Name: sharedDAG, DAGRunID: "run-1", AttemptID: "attempt-1", Status: ir.Succeeded}, nil)
	require.NoError(t, eventstore.New(cliEvents).Emit(ctx, event))
	// The collector drains once before it waits, so a done context makes
	// it collect exactly once.
	collectCtx, stop := context.WithCancel(ctx)
	stop()
	scheduler.EventCollector(collectCtx)
	result, err := server.Event.Query(ctx, eventstore.QueryFilter{DAGName: sharedDAG})
	require.NoError(t, err)
	require.Len(t, result.Entries, 1)
	assert.Equal(t, event.ID, result.Entries[0].ID)

	marker := schedulerSettings.DAGChangeMarkerPath()
	require.NoError(t, server.BaseConfig.UpdateSpec(ctx, []byte("env:\n  - A=1\n")))
	assert.FileExists(t, marker, "a base config edit signals the scheduler")
	require.NoError(t, os.Remove(marker))
	require.NoError(t, runs.DAGRepository.Create(ctx, "beta", []byte("steps:\n  - name: s\n    command: echo hi\n")))
	assert.FileExists(t, marker, "a DAG edit signals the scheduler")
}

// sharedStoresConfig sets apart every path that places a store of the
// default project, as an operator may, so a process that ignores the
// bundle's paths shows.
func sharedStoresConfig(t *testing.T, projectsOn bool) *config.Config {
	t.Helper()
	root := t.TempDir()
	data := filepath.Join(root, "data")
	cfg := &config.Config{ProjectsEnabled: projectsOn}
	cfg.Core.SkipExamples = true
	cfg.EventStore.Enabled = true
	cfg.Server.Auth.Mode = config.AuthModeNone
	cfg.Paths = config.PathsConfig{
		DAGsDir:            filepath.Join(root, "dags"),
		ProjectsDir:        filepath.Join(root, "projects"),
		DataDir:            data,
		BaseConfig:         filepath.Join(root, "base.yaml"),
		LogDir:             filepath.Join(root, "logs"),
		ArtifactDir:        filepath.Join(root, "artifacts"),
		DAGRunsDir:         filepath.Join(root, "dag-runs"),
		DAGStateDir:        filepath.Join(root, "dag-state"),
		QueueDir:           filepath.Join(root, "queue"),
		ProcDir:            filepath.Join(root, "proc"),
		SuspendFlagsDir:    filepath.Join(root, "suspend"),
		ViewsDir:           filepath.Join(root, "views"),
		EventStoreDir:      filepath.Join(root, "events"),
		AdminLogsDir:       filepath.Join(root, "admin"),
		RemoteNodesDir:     filepath.Join(data, "remote-nodes"),
		WorkspacesDir:      filepath.Join(data, "workspaces"),
		ServiceRegistryDir: filepath.Join(data, "service-registry"),
	}
	return cfg
}

func createSecret(t *testing.T, secrets secret.Store, value string) string {
	t.Helper()
	require.NotNil(t, secrets)
	sec, err := secret.New(secret.CreateInput{Ref: "db", ProviderType: secret.ProviderDaguManaged, CreatedBy: "test"}, time.Now())
	require.NoError(t, err)
	require.NoError(t, secrets.Create(t.Context(), sec, &secret.WriteValueInput{Value: value, CreatedBy: "test"}))
	return sec.ID
}

func assertState(t *testing.T, load func(context.Context) ([]byte, bool, error), want string) {
	t.Helper()
	data, found, err := load(t.Context())
	require.NoError(t, err)
	require.True(t, found)
	assert.JSONEq(t, want, string(data))
}
