// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectrecord"
)

// TestProjectState proves Spec 102 "Changed on Disk": a project whose DAG
// directory is gone is missing, and one marked deleting says so.
func TestProjectState(t *testing.T) {
	ctx := context.Background()
	f := newDeleteFixture(t)
	state := func() persisfile.ProjectState {
		p, err := f.registry.Get(ctx, "weather")
		require.NoError(t, err)
		state, err := f.server.ProjectState(ctx, p)
		require.NoError(t, err)
		return state
	}

	assert.Equal(t, persisfile.ProjectStateActive, state())
	require.NoError(t, fileutil.RemoveAll(filepath.Join(f.cfg.Paths.ProjectsDir, "weather")))
	assert.Equal(t, persisfile.ProjectStateMissing, state())
	_, err := f.registry.SetDeleting(ctx, f.weather.ID, true)
	require.NoError(t, err)
	assert.Equal(t, persisfile.ProjectStateDeleting, state())
}

// TestUnattachedStateDirs proves that a state directory recording a project
// the registry lacks is listed, and one recording a registered project is
// not.
func TestUnattachedStateDirs(t *testing.T) {
	ctx := context.Background()
	f := newDeleteFixture(t)
	old := filepath.Join(f.cfg.Paths.DataDir, "projects", "intraday")
	require.NoError(t, projectrecord.Write(old, projectrecord.Record{ID: "an-older-id", Name: "intraday"}))
	require.NoError(t, os.MkdirAll(filepath.Join(f.cfg.Paths.DataDir, "projects", "no-record"), 0o750))

	dirs, err := f.server.UnattachedStateDirs(ctx, f.registry)
	require.NoError(t, err)
	assert.Equal(t, []persisfile.UnattachedStateDir{{Path: old, Name: "intraday", ID: "an-older-id"}}, dirs)
}
