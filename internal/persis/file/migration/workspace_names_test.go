// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package migration

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/persis/file/projectrecord"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A workspace keeps its name when it keeps the name rules and is free.
// Otherwise its project gets a valid name made from it, with a number
// added while that is taken, and valid names choose first.
func TestProjectNames(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cfg := v2Install(root)
	long := strings.Repeat("a", 70)
	// A state directory of a deleted project keeps its name taken.
	require.NoError(t, projectrecord.Write(filepath.Join(cfg.Paths.DataDir, "projects", "kept"),
		projectrecord.Record{ID: "6a1b2c3d-4e5f-4a7b-8c9d-0e1f2a3b4c5d", Name: "kept"}))
	registered := []project.Project{
		{Name: project.DefaultName},
		{Name: "Weather"},
		{Name: "billing-2", MigratedFrom: "billing"},
	}
	workspaces := []string{"-ops", "Ops", "_x_", "billing", "con", "kept", "ops", "weather", long}

	names, err := projectNames(cfg, workspaces, registered)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"-ops":    "ops-2",
		"Ops":     "Ops",
		"_x_":     "x_",
		"billing": "billing-2",
		"con":     "con-2",
		"kept":    "kept-2",
		"ops":     "ops-3",
		"weather": "weather-2",
		long:      strings.Repeat("a", 64),
	}, names)
}
