// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package migration

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// v2Install returns the configuration of a v2 install in root, with the
// paths v2 derives by default and a queue defined in config.yaml, after the
// switch to projects.
func v2Install(root string) *config.Config {
	data := filepath.Join(root, "data")
	logs := filepath.Join(root, "logs")
	admin := filepath.Join(logs, "admin")
	cfg := &config.Config{ProjectsEnabled: true}
	cfg.Queues.Config = []config.QueueConfig{{Name: "shared"}}
	cfg.Paths = config.PathsConfig{
		DAGsDir:            filepath.Join(root, "dags"),
		ProjectsDir:        filepath.Join(root, "projects"),
		DataDir:            data,
		LogDir:             logs,
		AdminLogsDir:       admin,
		EventStoreDir:      filepath.Join(admin, "events"),
		ArtifactDir:        filepath.Join(data, "artifacts"),
		DAGRunsDir:         filepath.Join(data, "dag-runs"),
		DAGRunWorkDir:      filepath.Join(data, "dag-run-work"),
		DAGStateDir:        filepath.Join(data, "dag-state"),
		QueueDir:           filepath.Join(data, "queue"),
		ProcDir:            filepath.Join(data, "proc"),
		SuspendFlagsDir:    filepath.Join(data, "suspend"),
		ViewsDir:           filepath.Join(data, "views"),
		ServiceRegistryDir: filepath.Join(data, "service-registry"),
		UsersDir:           filepath.Join(data, "users"),
		APIKeysDir:         filepath.Join(data, "apikeys"),
		WebhooksDir:        filepath.Join(data, "webhooks"),
	}
	return cfg
}

// v2State maps each file of a v2 install, relative to its root, to where
// the default project's stores read it once migration is done. A file
// mapped to itself is server-wide state, or state the migration does not
// own, and stays.
var v2State = map[string]string{
	"data/dag-runs/etl/2026/01/02/run_1/status.jsonl":                  "data/projects/default/runs/etl/2026/01/02/run_1/status.jsonl",
	"data/dag-runs-ephemeral/etl/run_2/status.jsonl":                   "data/projects/default/state/dag-runs-ephemeral/etl/run_2/status.jsonl",
	"data/dag-run-work/etl/run_1/work.txt":                             "data/projects/default/state/dag-run-work/etl/run_1/work.txt",
	"logs/etl/run_1.log":                                               "data/projects/default/logs/etl/run_1.log",
	"logs/admin/audit/2026-01-02.jsonl":                                "logs/admin/audit/2026-01-02.jsonl",
	"logs/admin/events/2026-01-02.jsonl":                               "data/projects/default/state/events/2026-01-02.jsonl",
	"data/artifacts/etl/run_1/report.md":                               "data/projects/default/artifacts/etl/run_1/report.md",
	"data/proc/etl/etl/proc_1":                                         "data/projects/default/state/proc/etl/etl/proc_1",
	"data/queue/etl/item_low_20260102_000000_000000000Z_run_3.json":    "data/projects/default/state/queue/etl/item_low_20260102_000000_000000000Z_run_3.json",
	"data/queue/shared/item_low_20260102_000000_000000000Z_run_4.json": "data/server/queue/shared/item_low_20260102_000000_000000000Z_run_4.json",
	"data/suspend/etl.suspend":                                         "data/projects/default/state/suspend/etl.suspend",
	"data/dag-state/etl.json":                                          "data/projects/default/state/dag-state/etl.json",
	"data/views/v1.json":                                               "data/projects/default/state/views/v1.json",
	"data/dag-settings/etl.json":                                       "data/projects/default/state/dag-settings/etl.json",
	"data/dag-pins/u1.json":                                            "data/projects/default/state/dag-pins/u1.json",
	"data/secrets/token.json":                                          "data/projects/default/settings/secrets/token.json",
	"data/profiles/prod.json":                                          "data/projects/default/settings/profiles/prod.json",
	"data/backfills/b1.json":                                           "data/projects/default/state/backfills/b1.json",
	"data/cache/dag-index/index.json":                                  "data/projects/default/state/cache/dag-index/index.json",
	"data/notifications/routes/etl.json":                               "data/projects/default/settings/notifications/routes/etl.json",
	"data/notifications/channels/slack.json":                           "data/notifications/channels/slack.json",
	"data/notifications/monitor-state.json":                            "data/projects/default/state/notifications/monitor-state.json",
	"data/incidents/policies/etl.json":                                 "data/projects/default/settings/incidents/policies/etl.json",
	"data/incidents/providers/pager.json":                              "data/incidents/providers/pager.json",
	"data/scheduler/dags/etl.json":                                     "data/projects/default/state/scheduler/dags/etl.json",
	"data/scheduler/paused.json":                                       "data/scheduler/paused.json",
	"data/scheduler/locks/.dagu_lock/heartbeat":                        "data/scheduler/locks/.dagu_lock/heartbeat",
	"data/users/u1.json":                                               "data/users/u1.json",
	"data/projects/default/library/runs/lib/status.jsonl":              "data/projects/default/library/runs/lib/status.jsonl",
}

// TestRunMovesV2State proves that every store of the default project finds
// its v2 state after migration, the queues defined in config.yaml move to
// the server, and server-wide state and the default project's library stay.
// Migration moves state by rename, by copy to another file system, and
// resumes a plan that a killed process left.
func TestRunMovesV2State(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		run  func(t *testing.T, cfg *config.Config)
	}{
		{name: "Rename", run: func(t *testing.T, cfg *config.Config) {
			require.NoError(t, newStateMigrator(t, cfg).Run(context.Background()))
		}},
		{name: "CopyToOtherFileSystem", run: func(t *testing.T, cfg *config.Config) {
			m := newStateMigrator(t, cfg)
			m.mover.sameFileSystem = otherFileSystem
			require.NoError(t, m.Run(context.Background()))
		}},
		{name: "ResumeAfterKill", run: func(t *testing.T, cfg *config.Config) {
			first := newStateMigrator(t, cfg)
			first.afterMove = func(index int) error {
				if index == 3 {
					return errKilled
				}
				return nil
			}
			require.ErrorIs(t, first.Run(context.Background()), errKilled)
			resumed := newStateMigrator(t, cfg)
			resumed.planMoves = refusePlan(t)
			require.NoError(t, resumed.Run(context.Background()))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			cfg := v2Install(root)
			for from := range v2State {
				writeFile(t, filepath.Join(root, from))
			}

			tc.run(t, cfg)

			for from, to := range v2State {
				assert.FileExists(t, filepath.Join(root, to))
				if from != to {
					assert.NoFileExists(t, filepath.Join(root, from))
				}
			}
			for _, dir := range []string{"data/dag-runs", "data/artifacts", "data/secrets"} {
				assert.NoDirExists(t, filepath.Join(root, dir))
			}
		})
	}
}

// With paths.log_dir set, the default project's logs move into
// <log_dir>/.projects/default/, and the library's directory set apart
// there stays.
func TestRunMovesWithinSetApartDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cfg := v2Install(root)
	cfg.Paths.Explicit.LogDir = true
	writeFile(t, filepath.Join(root, "logs", "etl", "run_1.log"))
	writeFile(t, filepath.Join(root, "logs", ".projects", "default.library", "lib", "run.log"))

	require.NoError(t, newStateMigrator(t, cfg).Run(context.Background()))

	assert.FileExists(t, filepath.Join(root, "logs", ".projects", "default", "etl", "run_1.log"))
	assert.FileExists(t, filepath.Join(root, "logs", ".projects", "default.library", "lib", "run.log"))
	assert.NoDirExists(t, filepath.Join(root, "logs", "etl"))
}

// A v2 directory that is a symbolic link to another disk moves as a link,
// on any file system, so its state stays on that disk and the project's
// stores reach it. A relative link keeps reaching its target from its new
// place.
func TestRunMovesSymlinkedDirAsLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symbolic links needs a privilege on Windows")
	}
	t.Parallel()
	for _, tc := range []struct {
		name      string
		otherDisk bool
	}{
		{name: "SameFileSystem"},
		{name: "OtherFileSystem", otherDisk: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			cfg := v2Install(root)
			disk := filepath.Join(root, "disk", "dag-runs")
			writeFile(t, filepath.Join(disk, "etl", "status.jsonl"))
			require.NoError(t, os.MkdirAll(cfg.Paths.DataDir, 0o750))
			require.NoError(t, os.Symlink(filepath.Join("..", "disk", "dag-runs"), cfg.Paths.DAGRunsDir))

			m := newStateMigrator(t, cfg)
			if tc.otherDisk {
				m.mover.sameFileSystem = otherFileSystem
			}
			require.NoError(t, m.Run(context.Background()))

			runs := filepath.Join(cfg.Paths.DataDir, "projects", "default", "runs")
			assert.FileExists(t, filepath.Join(runs, "etl", "status.jsonl"))
			info, err := os.Lstat(runs)
			require.NoError(t, err)
			assert.NotZero(t, info.Mode()&os.ModeSymlink, "the runs stay on their disk")
			assert.FileExists(t, filepath.Join(disk, "etl", "status.jsonl"))
			assert.NoFileExists(t, cfg.Paths.DAGRunsDir)
		})
	}
}

// Migration cancels the live runs and waits for their processes to exit
// before it plans or moves anything, and reports the runs it waits for.
func TestRunWaitsForCancelledRuns(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cfg := v2Install(root)
	writeFile(t, filepath.Join(root, "data", "dag-runs", "etl", "status.jsonl"))
	m := newStateMigrator(t, cfg)
	ticks := make(chan time.Time)
	m.pollTicks = func() (<-chan time.Time, func()) { return ticks, func() {} }
	var exited atomic.Bool
	m.stopRuns = func(context.Context) ([]string, error) {
		if exited.Load() {
			return nil, nil
		}
		return []string{"etl:run_1"}, nil
	}
	planned := m.planMoves
	m.planMoves = func(ctx context.Context) ([]move, error) {
		assert.True(t, exited.Load(), "planned while a run was live")
		return planned(ctx)
	}

	done := runAsync(m)
	tickWaiting(t, ticks, done)
	assert.Equal(t, Progress{Phase: phaseStoppingRuns, WaitingFor: []string{"etl:run_1"}}, m.Progress())
	assert.DirExists(t, filepath.Join(root, "data", "dag-runs"))

	exited.Store(true)
	require.NoError(t, tickUntilDone(ticks, done))
	assert.FileExists(t, filepath.Join(root, "data", "projects", "default", "runs", "etl", "status.jsonl"))
}

func newStateMigrator(t *testing.T, cfg *config.Config) *Migrator {
	t.Helper()
	return New(cfg, &fakeServices{}, noLiveRuns)
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(path), 0o600))
}
