// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logpath"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/secret"
)

const sampleDAG = "etl"

var sampleState = dagrun.StateRef{Scope: dagrun.StateScopeGlobal, Namespace: sampleDAG, Key: "cursor"}

// With projects off, the default project's stores write each record where
// v2 does, and the v2 stores read it back. Nothing is written for projects.
func TestProjectStoresOffKeepV2Layout(t *testing.T) {
	ctx := context.Background()
	cfg := projectStoresConfig(t, false)
	server := persisfile.NewServerStores(cfg)
	stores, err := persisfile.NewProjectStores(ctx, cfg, server, project.Project{Name: project.DefaultName}, persisfile.ProjectStoreOptions{})
	require.NoError(t, err)
	secretID := writeSample(t, stores, "v2")

	assertSample(t, v2Readers(ctx, cfg), secretID, "v2")
	assert.NoDirExists(t, filepath.Join(cfg.Paths.DataDir, "projects"))
	assert.NoFileExists(t, filepath.Join(cfg.Paths.DataDir, "projects.json"))
}

// With projects on, a project keeps the same DAG's records apart from the
// default project's, in its own state directory. The default project's stay
// where v2 keeps them, where the server and the scheduler still read and
// write them.
func TestProjectStoresOnKeepProjectsApart(t *testing.T) {
	ctx := context.Background()
	cfg := projectStoresConfig(t, true)
	server := persisfile.NewServerStores(cfg)

	secretIDs := map[string]string{}
	opened := map[string]*persisfile.ProjectStores{}
	for _, name := range []string{project.DefaultName, "weather"} {
		stores, err := persisfile.NewProjectStores(ctx, cfg, server, project.Project{Name: name}, persisfile.ProjectStoreOptions{})
		require.NoError(t, err)
		opened[name] = stores
		secretIDs[name] = writeSample(t, stores, name)
	}

	for name, stores := range opened {
		assertSample(t, readersOf(ctx, stores), secretIDs[name], name)
	}
	assertSample(t, v2Readers(ctx, cfg), secretIDs[project.DefaultName], project.DefaultName)
	weather := filepath.Join(cfg.Paths.DataDir, "projects", "weather")
	for _, dir := range []string{"runs", "settings/secrets", "state/dag-state", "state/queue"} {
		assert.DirExists(t, filepath.Join(weather, filepath.FromSlash(dir)))
	}
	assert.DirExists(t, filepath.Join(cfg.Paths.ProjectsDir, "weather"))
	assert.NoDirExists(t, filepath.Join(cfg.Paths.DataDir, "projects", project.DefaultName))
}

// Every project encrypts its secrets with the server's key and keeps no key
// of its own, so its secrets decrypt on any server with that key.
func TestProjectStoresEncryptWithServerKey(t *testing.T) {
	ctx := context.Background()
	cfg := projectStoresConfig(t, true)
	stores, err := persisfile.NewProjectStores(ctx, cfg, persisfile.NewServerStores(cfg), project.Project{Name: "weather"}, persisfile.ProjectStoreOptions{})
	require.NoError(t, err)
	secretID := writeSample(t, stores, "hunter2")

	assert.FileExists(t, filepath.Join(cfg.Paths.DataDir, "auth", "encryption_key"))
	require.NoError(t, filepath.WalkDir(stores.Paths.StateDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		assert.NotEqual(t, "encryption_key", d.Name(), "key in project state: %s", path)
		return nil
	}))

	elsewhere, err := persisfile.NewProjectSettings(cfg, persisfile.NewServerStores(cfg), "weather")
	require.NoError(t, err)
	value, _, err := elsewhere.SecretStore(ctx).ReadValue(ctx, secretID)
	require.NoError(t, err)
	assert.Equal(t, "hunter2", value)

	t.Setenv("DAGU_ENCRYPTION_KEY", "another server's key")
	other, err := persisfile.NewProjectSettings(cfg, persisfile.NewServerStores(cfg), "weather")
	require.NoError(t, err)
	_, _, err = other.SecretStore(ctx).ReadValue(ctx, secretID)
	assert.Error(t, err)
}

// A run record that holds an absolute path under the v2 log directory reads
// it under the project's log directory for each migrated project. A new
// project had no runs on v2, so it reads the path as stored.
func TestProjectStoresMapV2RunPaths(t *testing.T) {
	ctx := context.Background()
	cfg := projectStoresConfig(t, true)
	server := persisfile.NewServerStores(cfg)
	v2Log := filepath.Join(cfg.Paths.LogDir, sampleDAG, "run.log")

	for _, tc := range []struct {
		project project.Project
		mapped  bool
	}{
		{project: project.Project{Name: "billing", MigratedFrom: "billing"}, mapped: true},
		{project: project.Project{Name: "weather"}, mapped: false},
	} {
		t.Run(tc.project.Name, func(t *testing.T) {
			stores, err := persisfile.NewProjectStores(ctx, cfg, server, tc.project, persisfile.ProjectStoreOptions{})
			require.NoError(t, err)
			writeRun(t, stores.DAGRunRepository, ir.DAGRunStatus{Log: v2Log})

			want := v2Log
			if tc.mapped {
				want = filepath.Join(stores.Paths.LogDir, sampleDAG, "run.log")
			}
			assert.Equal(t, want, readRun(t, stores.DAGRunRepository).Log)
		})
	}
}

// With projects on, a run of the default project writes its log where a
// command puts it, in paths.log_dir, and its record reads that file back.
func TestProjectStoresOnDefaultRunLog(t *testing.T) {
	ctx := context.Background()
	cfg := projectStoresConfig(t, true)
	stores, err := persisfile.NewProjectStores(ctx, cfg, persisfile.NewServerStores(cfg), project.Project{Name: project.DefaultName}, persisfile.ProjectStoreOptions{})
	require.NoError(t, err)

	logFile, err := logpath.Generate(ctx, cfg.Paths.LogDir, "", sampleDAG, "run-1")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(logFile), 0o750))
	require.NoError(t, os.WriteFile(logFile, []byte("hello"), 0o600))
	writeRun(t, stores.DAGRunRepository, ir.DAGRunStatus{Log: logFile})

	assert.FileExists(t, readRun(t, stores.DAGRunRepository).Log)
}

// With projects on, a DAG edit in the default project bumps the change
// marker that the scheduler polls.
func TestProjectStoresOnDAGChangeMarker(t *testing.T) {
	ctx := context.Background()
	cfg := projectStoresConfig(t, true)
	stores, err := persisfile.NewProjectStores(ctx, cfg, persisfile.NewServerStores(cfg), project.Project{Name: project.DefaultName}, persisfile.ProjectStoreOptions{})
	require.NoError(t, err)
	marker := defaultDAGChangeMarkerPath(t, cfg)
	require.NotEmpty(t, marker)
	require.NoFileExists(t, marker)

	require.NoError(t, stores.DAGRepository.Create(ctx, "beta", []byte("steps:\n  - name: s\n    command: echo hi\n")))
	assert.FileExists(t, marker)
}

// With projects on, a project other than the default one keeps its run
// events, the state of its notification and incident monitors, and its DAG
// change marker in its own state directory, apart from the default
// project's.
func TestProjectSettingsOnKeepProjectState(t *testing.T) {
	cfg := projectStoresConfig(t, true)
	cfg.Paths.EventStoreDir = filepath.Join(t.TempDir(), "events")
	server := persisfile.NewServerStores(cfg)
	weather, err := persisfile.NewProjectSettings(cfg, server, "weather")
	require.NoError(t, err)
	defaults, err := persisfile.NewProjectSettings(cfg, server, project.DefaultName)
	require.NoError(t, err)

	state := filepath.Join(cfg.Paths.DataDir, "projects", "weather", "state")
	assert.Equal(t, filepath.Join(state, "events"), weather.Paths.EventStoreDir)
	assert.Equal(t, cfg.Paths.EventStoreDir, defaults.Paths.EventStoreDir)
	for _, files := range [][2]string{
		{weather.NotificationStateFile(), defaults.NotificationStateFile()},
		{weather.IncidentStateFile(), defaults.IncidentStateFile()},
		{weather.DAGChangeMarkerPath(), defaults.DAGChangeMarkerPath()},
	} {
		assert.True(t, strings.HasPrefix(files[0], state+string(filepath.Separator)), files[0])
		assert.NotEqual(t, files[0], files[1])
	}
	assert.Equal(t, filepath.Join(cfg.Paths.DataDir, "notifications", "monitor-state.json"), defaults.NotificationStateFile())
	assert.Equal(t, filepath.Join(cfg.Paths.DataDir, "incidents", "monitor-state.json"), defaults.IncidentStateFile())
}

// With projects on, the default project and another project each hold a
// webhook for a DAG of the same name. The default project's is the one the
// v2 webhook store reads; the other project's lies in its own settings.
func TestProjectSettingsOnKeepWebhooksApart(t *testing.T) {
	ctx := context.Background()
	cfg := projectStoresConfig(t, true)
	server := persisfile.NewServerStores(cfg)

	ids := map[string]string{}
	for _, name := range []string{project.DefaultName, "weather"} {
		settings, err := persisfile.NewProjectSettings(cfg, server, name)
		require.NoError(t, err)
		webhooks, err := settings.WebhookStore(nil)
		require.NoError(t, err)
		webhook, err := auth.NewWebhook(sampleDAG, "hash-"+name, "dagu_wh_", "test")
		require.NoError(t, err)
		require.NoError(t, webhooks.Create(ctx, webhook))
		ids[name] = webhook.ID
	}

	v2 := persisfile.NewBackend(cfg.Paths)
	v2Webhooks, err := store.NewWebhookStore(v2.Collection(persis.CollectionWebhooks), v2.Collection(persis.CollectionWebhookDAGClaims), nil)
	require.NoError(t, err)
	got, err := v2Webhooks.GetByDAGName(ctx, sampleDAG)
	require.NoError(t, err)
	assert.Equal(t, ids[project.DefaultName], got.ID)
	assert.FileExists(t, filepath.Join(cfg.Paths.WebhooksDir, ids[project.DefaultName]+".json"))
	assert.FileExists(t, filepath.Join(cfg.Paths.DataDir, "projects", "weather", "settings", "webhooks", ids["weather"]+".json"))
}

// projectStoresConfig sets apart every path that places a project store, as
// an operator may, so a store that ignores the project's paths shows.
func projectStoresConfig(t *testing.T, projectsOn bool) *config.Config {
	t.Helper()
	t.Setenv("DAGU_ENCRYPTION_KEY", "")
	root := t.TempDir()
	data := filepath.Join(root, "data")
	cfg := &config.Config{ProjectsEnabled: projectsOn}
	cfg.Core.SkipExamples = true
	cfg.Paths = config.PathsConfig{
		DAGsDir:            filepath.Join(root, "dags"),
		ProjectsDir:        filepath.Join(root, "projects"),
		DataDir:            data,
		LogDir:             filepath.Join(root, "logs"),
		ArtifactDir:        filepath.Join(data, "artifacts"),
		DAGRunsDir:         filepath.Join(root, "dag-runs"),
		DAGRunWorkDir:      filepath.Join(data, "dag-run-work"),
		DAGStateDir:        filepath.Join(root, "dag-state"),
		QueueDir:           filepath.Join(root, "queue"),
		ProcDir:            filepath.Join(root, "proc"),
		SuspendFlagsDir:    filepath.Join(data, "suspend"),
		ViewsDir:           filepath.Join(data, "views"),
		WebhooksDir:        filepath.Join(root, "webhooks"),
		ServiceRegistryDir: filepath.Join(data, "service-registry"),
	}
	return cfg
}

// writeSample writes a run, a DAG state entry, a queue item, and a secret
// that carry value, and returns the secret's ID.
func writeSample(t *testing.T, stores *persisfile.ProjectStores, value string) string {
	t.Helper()
	ctx := context.Background()
	writeRun(t, stores.DAGRunRepository, ir.DAGRunStatus{Params: value})
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	_, err = stores.StateStore.Put(ctx, sampleState, raw, dagrun.StatePutOptions{})
	require.NoError(t, err)
	require.NoError(t, stores.QueueStore.Enqueue(ctx, sampleDAG, queue.QueuePriorityLow, ir.NewDAGRunRef(sampleDAG, value)))

	secrets := stores.SecretStore(ctx)
	require.NotNil(t, secrets)
	sec, err := secret.New(secret.CreateInput{Ref: "db", ProviderType: secret.ProviderDaguManaged, CreatedBy: "test"}, time.Now())
	require.NoError(t, err)
	require.NoError(t, secrets.Create(ctx, sec, &secret.WriteValueInput{Value: value, CreatedBy: "test"}))
	return sec.ID
}

// sampleReaders reads back what writeSample wrote.
type sampleReaders struct {
	runs    *persis.DAGRunRepository
	state   dagrun.StateStore
	queue   queue.QueueStore
	secrets secret.Store
}

// v2Readers reads through the stores that open where v2 keeps the records,
// as the server and the scheduler do.
func v2Readers(ctx context.Context, cfg *config.Config) sampleReaders {
	v2 := persisfile.NewBackend(cfg.Paths)
	return sampleReaders{
		runs:    persisfile.NewDAGRunRepository(cfg),
		state:   store.NewDAGStateStore(v2.Collection(persis.CollectionDAGState)),
		queue:   store.NewQueueStore(v2.Collection(persis.CollectionQueue)),
		secrets: persisfile.NewSecretStore(ctx, cfg, v2.Collection(persis.CollectionSecrets)),
	}
}

func readersOf(ctx context.Context, stores *persisfile.ProjectStores) sampleReaders {
	return sampleReaders{
		runs:    stores.DAGRunRepository,
		state:   stores.StateStore,
		queue:   stores.QueueStore,
		secrets: stores.SecretStore(ctx),
	}
}

func assertSample(t *testing.T, r sampleReaders, secretID, value string) {
	t.Helper()
	ctx := context.Background()
	assert.Equal(t, value, readRun(t, r.runs).Params)

	entry, err := r.state.Get(ctx, sampleState)
	require.NoError(t, err)
	assert.JSONEq(t, `"`+value+`"`, string(entry.Value))

	items, err := r.queue.List(ctx, sampleDAG)
	require.NoError(t, err)
	require.Len(t, items, 1)
	ref, err := items[0].Data()
	require.NoError(t, err)
	assert.Equal(t, value, ref.ID)

	require.NotNil(t, r.secrets)
	got, _, err := r.secrets.ReadValue(ctx, secretID)
	require.NoError(t, err)
	assert.Equal(t, value, got)
}

func writeRun(t *testing.T, runs *persis.DAGRunRepository, status ir.DAGRunStatus) {
	t.Helper()
	ctx := context.Background()
	status.Name, status.DAGRunID, status.Status = sampleDAG, "run-1", ir.Succeeded
	attempt, err := runs.CreateAttempt(ctx, &ir.DAG{Name: sampleDAG}, time.Now(), status.DAGRunID, persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))
}

func readRun(t *testing.T, runs *persis.DAGRunRepository) *ir.DAGRunStatus {
	t.Helper()
	ctx := context.Background()
	attempt, err := runs.FindAttempt(ctx, ir.NewDAGRunRef(sampleDAG, "run-1"))
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	return status
}
