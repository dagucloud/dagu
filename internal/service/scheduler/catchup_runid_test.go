// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package scheduler

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateCatchupRunID(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 3, 12, 14, 0, 0, 0, time.UTC)

	t.Run("normal name", func(t *testing.T) {
		id := GenerateCatchupRunID("etl-pipeline", ts)
		assert.Regexp(t, scheduledRunIDPattern(catchupPrefix, "20260312T140000"), id)
		require.NoError(t, ir.ValidateDAGRunID(id))
	})

	t.Run("deterministic", func(t *testing.T) {
		id1 := GenerateCatchupRunID("my-dag", ts)
		id2 := GenerateCatchupRunID("my-dag", ts)
		assert.Equal(t, id1, id2)
	})

	t.Run("different timestamps produce different IDs", func(t *testing.T) {
		ts2 := ts.Add(time.Hour)
		id1 := GenerateCatchupRunID("my-dag", ts)
		id2 := GenerateCatchupRunID("my-dag", ts2)
		assert.NotEqual(t, id1, id2)
	})

	t.Run("name with dots remains valid", func(t *testing.T) {
		id := GenerateCatchupRunID("my.dag.name", ts)
		assert.Regexp(t, scheduledRunIDPattern(catchupPrefix, "20260312T140000"), id)
		require.NoError(t, ir.ValidateDAGRunID(id))
	})

	t.Run("dot vs hyphen produce different IDs", func(t *testing.T) {
		id1 := GenerateCatchupRunID("my.dag", ts)
		id2 := GenerateCatchupRunID("my-dag", ts)
		assert.NotEqual(t, id1, id2, "dot and hyphen DAG names must produce different IDs due to hash")
	})

	t.Run("dot vs underscore produce different IDs", func(t *testing.T) {
		id1 := GenerateCatchupRunID("my.dag", ts)
		id2 := GenerateCatchupRunID("my_dag", ts)
		assert.NotEqual(t, id1, id2, "dot and underscore DAG names must produce different IDs due to hash")
	})

	t.Run("long name keeps fixed length", func(t *testing.T) {
		longName := "a-very-extremely-long-dag-name-that-exceeds-the-limit"
		id := GenerateCatchupRunID(longName, ts)
		assert.Len(t, id, len(catchupPrefix)+hashLen+1+len(timestampLayout))
		assert.LessOrEqual(t, len(id), maxRunIDLen)
		require.NoError(t, ir.ValidateDAGRunID(id))
	})

	t.Run("well below max length", func(t *testing.T) {
		name := "abcdefghijklmnopqrstuvwxyz12345"
		id := GenerateCatchupRunID(name, ts)
		assert.Len(t, id, len(catchupPrefix)+hashLen+1+len(timestampLayout))
		assert.LessOrEqual(t, len(id), maxRunIDLen)
		require.NoError(t, ir.ValidateDAGRunID(id))
	})

	t.Run("all outputs pass validation", func(t *testing.T) {
		names := []string{
			"simple",
			"with-hyphens",
			"with_underscores",
			"with.dots",
			"MixedCase",
			"a",
			"a-very-extremely-long-dag-name-that-definitely-exceeds",
		}
		for _, name := range names {
			id := GenerateCatchupRunID(name, ts)
			require.NoError(t, ir.ValidateDAGRunID(id), "failed for DAG name: %s, generated ID: %s", name, id)
		}
	})

	t.Run("UTC normalization", func(t *testing.T) {
		loc, _ := time.LoadLocation("America/New_York")
		tsLocal := ts.In(loc)
		id1 := GenerateCatchupRunID("my-dag", ts)
		id2 := GenerateCatchupRunID("my-dag", tsLocal)
		assert.Equal(t, id1, id2, "same instant in different timezones must produce same ID")
	})
}

func TestGenerateOneOffRunID(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 3, 29, 1, 10, 0, 0, time.UTC)
	fingerprint := "at:2026-03-29T02:10:00+01:00"

	t.Run("normal name", func(t *testing.T) {
		id := GenerateOneOffRunID("/data", "etl-pipeline", fingerprint, ts)
		assert.Regexp(t, scheduledRunIDPattern(oneOffPrefix, "20260329T011000"), id)
		require.NoError(t, ir.ValidateDAGRunID(id))
	})

	t.Run("deterministic", func(t *testing.T) {
		id1 := GenerateOneOffRunID("/data", "my-dag", fingerprint, ts)
		id2 := GenerateOneOffRunID("/data", "my-dag", fingerprint, ts)
		assert.Equal(t, id1, id2)
	})

	t.Run("fingerprint changes ID", func(t *testing.T) {
		id1 := GenerateOneOffRunID("/data", "my-dag", fingerprint, ts)
		id2 := GenerateOneOffRunID("/data", "my-dag", "at:2026-03-29T02:11:00+01:00", ts)
		assert.NotEqual(t, id1, id2)
	})

	t.Run("dag name changes ID", func(t *testing.T) {
		id1 := GenerateOneOffRunID("/data", "my-dag", fingerprint, ts)
		id2 := GenerateOneOffRunID("/data", "other-dag", fingerprint, ts)
		assert.NotEqual(t, id1, id2)
	})

	t.Run("seed changes ID", func(t *testing.T) {
		id1 := GenerateOneOffRunID("/data", "my-dag", fingerprint, ts)
		id2 := GenerateOneOffRunID("/other-data", "my-dag", fingerprint, ts)
		assert.NotEqual(t, id1, id2, "installations never share a run ID")
	})

	t.Run("UTC normalization", func(t *testing.T) {
		loc, _ := time.LoadLocation("America/New_York")
		tsLocal := ts.In(loc)
		id1 := GenerateOneOffRunID("/data", "my-dag", fingerprint, ts)
		id2 := GenerateOneOffRunID("/data", "my-dag", fingerprint, tsLocal)
		assert.Equal(t, id1, id2)
	})
}

func TestGenerateLegacyScheduledRunIDs(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 3, 12, 14, 0, 0, 0, time.UTC)

	t.Run("catchup", func(t *testing.T) {
		id := generateLegacyCatchupRunID("etl.pipeline", ts)
		assert.Regexp(t, fmt.Sprintf(`^catchup-etl_pipeline-[0-9a-f]{%d}-20260312T140000$`, legacyHashLen), id)
		require.NoError(t, ir.ValidateDAGRunID(id))
	})

	t.Run("one-off", func(t *testing.T) {
		id := generateLegacyOneOffRunID("etl.pipeline", "at:2026-03-12T14:00:00Z", ts)
		assert.Regexp(t, fmt.Sprintf(`^oneoff-etl_pipeline-[0-9a-f]{%d}-20260312T140000$`, legacyHashLen), id)
		require.NoError(t, ir.ValidateDAGRunID(id))
	})
}

func scheduledRunIDPattern(prefix, timestamp string) string {
	return fmt.Sprintf(`^%s[0-9a-f]{%d}-%s$`, prefix, hashLen, timestamp)
}

func TestGenerateSlotRunID(t *testing.T) {
	t.Parallel()

	slot := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)

	const dataDir = "/var/lib/dagu"
	id := GenerateSlotRunID(dataDir, "etl-pipeline", slot)
	// The ID v2 gives this slot. Any other value re-runs slots that ran
	// before an upgrade.
	assert.Equal(t, "sched-4afabf0185aa-20260207T120000", id)
	assert.LessOrEqual(t, len(id), maxRunIDLen)

	assert.Equal(t, id, GenerateSlotRunID(dataDir, "etl-pipeline", slot.In(time.FixedZone("JST", 9*3600))))
	assert.NotEqual(t, id, GenerateSlotRunID(dataDir, "other", slot))
	assert.NotEqual(t, id, GenerateSlotRunID("/srv/other-install", "etl-pipeline", slot))
	assert.NotEqual(t, id, GenerateSlotRunID(dataDir, "etl-pipeline", slot.Add(time.Minute)))
}

// v2 hashes paths.data_dir into each slot's run ID. Migration moves a
// project's runs under <data_dir>/projects/<name>/, and the registry seeds the
// default project and each migrated one with paths.data_dir, so a slot that
// ran on v2 keeps its run ID and is refused as a duplicate after the upgrade.
func TestSlotRunIDAcrossMigration(t *testing.T) {
	t.Parallel()

	slot := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	for name, register := range map[string]func(context.Context, project.Registry) (project.Project, error){
		"Default": func(ctx context.Context, reg project.Registry) (project.Project, error) {
			return reg.EnsureDefault(ctx)
		},
		"Migrated": func(ctx context.Context, reg project.Registry) (project.Project, error) {
			return reg.Create(ctx, project.CreateInput{Name: "billing", MigratedFrom: "billing"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			dataDir := t.TempDir()
			cfg := &config.Config{Paths: config.PathsConfig{
				DataDir:    dataDir,
				DAGRunsDir: filepath.Join(dataDir, "dag-runs"),
			}}
			dag := &ir.DAG{Name: "invoices", Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")}}

			v2Paths, err := cfg.ProjectPaths(project.DefaultName)
			require.NoError(t, err)
			ranID := planSlotRunID(t, cfg.Paths.DataDir, dag, slot)
			_, err = testutil.NewFileDAGRunRepository(v2Paths.DAGRunsDir, persis.DAGRunRepositoryOptions{}).
				CreateAttempt(ctx, dag, slot, ranID, persis.DAGRunCreateAttemptOptions{})
			require.NoError(t, err)

			// Migration registers the project and moves its runs.
			cfg.ProjectsEnabled = true
			registered, err := register(ctx, projectregistry.New(cfg.Paths.DataDir))
			require.NoError(t, err)
			paths, err := cfg.ProjectPaths(registered.Name)
			require.NoError(t, err)
			require.NoError(t, fileutil.MkdirAll(filepath.Dir(paths.DAGRunsDir), 0o750))
			require.NoError(t, fileutil.Rename(v2Paths.DAGRunsDir, paths.DAGRunsDir))

			restarted, err := projectregistry.New(cfg.Paths.DataDir).GetByID(ctx, registered.ID)
			require.NoError(t, err)
			runID := planSlotRunID(t, restarted.SlotSeed, dag, slot)
			require.Equal(t, ranID, runID)
			_, err = testutil.NewFileDAGRunRepository(paths.DAGRunsDir, persis.DAGRunRepositoryOptions{}).
				CreateAttempt(ctx, dag, slot, runID, persis.DAGRunCreateAttemptOptions{})
			require.ErrorIs(t, err, dagrun.ErrDAGRunAlreadyExists)
		})
	}
}

// A new project is seeded with its ID, so its slots never take the run IDs of
// a DAG of the same name in the default project.
func TestNewProjectSlotRunID(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	reg := projectregistry.New(t.TempDir())
	def, err := reg.EnsureDefault(ctx)
	require.NoError(t, err)
	created, err := reg.Create(ctx, project.CreateInput{Name: "weather"})
	require.NoError(t, err)

	slot := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	dag := &ir.DAG{Name: "forecast", Schedule: []ir.Schedule{mustParseSchedule(t, "0 * * * *")}}
	assert.NotEqual(t, planSlotRunID(t, def.SlotSeed, dag, slot), planSlotRunID(t, created.SlotSeed, dag, slot))
}

// planSlotRunID returns the run ID that a tick planner with seed gives the
// slot of dag at slot.
func planSlotRunID(t *testing.T, seed string, dag *ir.DAG, slot time.Time) string {
	t.Helper()
	tp, _ := newTestTickPlanner(&mockStateStore{})
	tp.cfg.SlotSeed = seed
	tp.cfg.Clock = func() time.Time { return slot }
	require.NoError(t, tp.Init(t.Context(), testDAGEntries(dag)))
	runs := tp.Plan(t.Context(), slot)
	require.Len(t, runs, 1)
	return runs[0].RunID
}
