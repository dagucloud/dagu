// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package projectregistry_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/stretchr/testify/require"
)

// A migrated project records the files that run in place as its own. A
// record stays while its file is absent, and ends for good once the
// project's DAG directory holds a DAG file of the same name.
func TestInPlaceRecords(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	root := t.TempDir()
	dags, projects := filepath.Join(root, "dags"), filepath.Join(root, "projects")
	invoices, reports := filepath.Join(dags, "invoices.yaml"), filepath.Join(dags, "reports.yml")
	reg := projectregistry.New(filepath.Join(root, "data"))
	created, err := reg.Create(ctx, project.CreateInput{
		Name:         "billing",
		MigratedFrom: "billing",
		MigratedDAGs: []string{"invoices", "reports"},
		InPlaceFiles: []string{invoices, reports},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"invoices", "reports"}, created.MigratedDAGs)
	got, err := projectregistry.New(filepath.Join(root, "data")).Get(ctx, "billing")
	require.NoError(t, err)
	require.Equal(t, created, got)

	records := reg.InPlace(projects)
	files, err := records.InPlaceFiles(ctx, "BILLING")
	require.NoError(t, err)
	require.Equal(t, []string{invoices, reports}, files, "a record stays while its file is absent")
	owner, err := records.InPlaceOwner(ctx, invoices)
	require.NoError(t, err)
	require.Equal(t, "billing", owner)
	owner, err = records.InPlaceOwner(ctx, filepath.Join(dags, "other.yaml"))
	require.NoError(t, err)
	require.Empty(t, owner)
	files, err = records.InPlaceFiles(ctx, "unknown")
	require.NoError(t, err)
	require.Empty(t, files)

	// The file in the project's DAG directory wins, whatever its extension,
	// and the record stays ended once that file is gone again.
	moved := filepath.Join(projects, "billing", "reports.yaml")
	writeDAGFile(t, moved)
	files, err = records.InPlaceFiles(ctx, "billing")
	require.NoError(t, err)
	require.Equal(t, []string{invoices}, files)
	require.NoError(t, os.Remove(moved))
	for _, other := range []project.InPlaceRecords{records, reg.InPlace(projects)} {
		files, err = other.InPlaceFiles(ctx, "billing")
		require.NoError(t, err)
		require.Equal(t, []string{invoices}, files)
		owner, err = other.InPlaceOwner(ctx, reports)
		require.NoError(t, err)
		require.Empty(t, owner)
	}

	// A lookup by path that finds a record ended ends it as well.
	moved = filepath.Join(projects, "billing", "invoices.yml")
	writeDAGFile(t, moved)
	owner, err = records.InPlaceOwner(ctx, invoices)
	require.NoError(t, err)
	require.Empty(t, owner)
	require.NoError(t, os.Remove(moved))
	files, err = records.InPlaceFiles(ctx, "billing")
	require.NoError(t, err)
	require.Empty(t, files)

	// The project keeps the DAGs it migrated with.
	renamed, err := reg.SetDisplayName(ctx, created.ID, "Billing")
	require.NoError(t, err)
	require.Equal(t, created.MigratedDAGs, renamed.MigratedDAGs)
}

// Registering a workspace's project again keeps its ID and name and
// replaces its records with the new ones.
func TestRegisterMigrated(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	root := t.TempDir()
	reg := projectregistry.New(filepath.Join(root, "data"))
	first, err := reg.RegisterMigrated(ctx, project.CreateInput{
		Name: "billing", MigratedFrom: "billing",
		MigratedDAGs: []string{"invoices"}, InPlaceFiles: []string{filepath.Join(root, "invoices.yaml")},
	})
	require.NoError(t, err)

	again, err := reg.RegisterMigrated(ctx, project.CreateInput{
		Name: "billing-2", MigratedFrom: "billing",
		MigratedDAGs: []string{"reports"}, InPlaceFiles: []string{filepath.Join(root, "reports.yaml")},
	})
	require.NoError(t, err)
	require.Equal(t, first.ID, again.ID)
	require.Equal(t, "billing", again.Name)
	require.Equal(t, []string{"reports"}, again.MigratedDAGs)
	files, err := reg.InPlace(filepath.Join(root, "projects")).InPlaceFiles(ctx, "billing")
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(root, "reports.yaml")}, files)

	_, err = reg.Create(ctx, project.CreateInput{Name: "ops"})
	require.NoError(t, err)
	_, err = reg.RegisterMigrated(ctx, project.CreateInput{Name: "OPS", MigratedFrom: "OPS"})
	require.ErrorIs(t, err, project.ErrNameTaken)
}

// A lookup that cannot store the end of a record still treats the record
// as ended, and a later lookup stores it.
func TestInPlaceEndRetried(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a read-only directory still takes new files on Windows")
	}
	t.Parallel()
	ctx := t.Context()
	root := t.TempDir()
	data, projects := filepath.Join(root, "data"), filepath.Join(root, "projects")
	invoices := filepath.Join(root, "dags", "invoices.yaml")
	reg := projectregistry.New(data)
	_, err := reg.Create(ctx, project.CreateInput{Name: "billing", MigratedFrom: "billing", InPlaceFiles: []string{invoices}})
	require.NoError(t, err)
	moved := filepath.Join(projects, "billing", "invoices.yaml")
	writeDAGFile(t, moved)
	records := reg.InPlace(projects)

	require.NoError(t, os.Chmod(data, 0o500))
	files, err := records.InPlaceFiles(ctx, "billing")
	require.NoError(t, os.Chmod(data, 0o700))
	require.NoError(t, err)
	require.Empty(t, files)
	stored, err := os.ReadFile(filepath.Join(data, "projects.json"))
	require.NoError(t, err)
	require.Contains(t, string(stored), "invoices.yaml", "the end could not be stored")

	files, err = records.InPlaceFiles(ctx, "billing")
	require.NoError(t, err)
	require.Empty(t, files)
	stored, err = os.ReadFile(filepath.Join(data, "projects.json"))
	require.NoError(t, err)
	require.NotContains(t, string(stored), "invoices.yaml", "the next lookup stored the end")
}

func writeDAGFile(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte("steps: []\n"), 0o600))
}
