// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package projectregistry_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/stretchr/testify/require"
)

// A migrated project records the files that run in place as its own. A
// record stays while its file is absent, and ends once the project's DAG
// directory holds a DAG file of the same name.
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

	// The file in the project's DAG directory wins, whatever its extension.
	moved := filepath.Join(projects, "billing", "reports.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(moved), 0o750))
	require.NoError(t, os.WriteFile(moved, []byte("steps: []\n"), 0o600))
	files, err = records.InPlaceFiles(ctx, "billing")
	require.NoError(t, err)
	require.Equal(t, []string{invoices}, files)
	owner, err = records.InPlaceOwner(ctx, reports)
	require.NoError(t, err)
	require.Empty(t, owner)

	// The project keeps the records it migrated with.
	renamed, err := reg.SetDisplayName(ctx, created.ID, "Billing")
	require.NoError(t, err)
	require.Equal(t, created.MigratedDAGs, renamed.MigratedDAGs)
	owner, err = records.InPlaceOwner(ctx, invoices)
	require.NoError(t, err)
	require.Equal(t, "billing", owner)
}
