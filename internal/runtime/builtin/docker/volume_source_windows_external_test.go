// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package docker_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	dockerruntime "github.com/dagucloud/dagu/internal/runtime/builtin/docker"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigPreservesWindowsDriveLetterBindSource(t *testing.T) {
	cfg, err := dockerruntime.LoadConfig("", core.Container{
		Image:   "alpine",
		Volumes: []string{`C:\temp\data:/data:ro`},
	}, nil)

	require.NoError(t, err)
	require.Equal(t, []string{`C:\temp\data:/data:ro`}, cfg.Host.Binds)
}

func TestLoadConfigPreservesWindowsForwardSlashDriveLetterBindSource(t *testing.T) {
	cfg, err := dockerruntime.LoadConfig("", core.Container{
		Image:   "alpine",
		Volumes: []string{"C:/temp/data:/data"},
	}, nil)

	require.NoError(t, err)
	require.Equal(t, []string{"C:/temp/data:/data:rw"}, cfg.Host.Binds)
}

func TestLoadConfigPreservesDockerToolboxStyleBindSource(t *testing.T) {
	cfg, err := dockerruntime.LoadConfig("", core.Container{
		Image:   "alpine",
		Volumes: []string{"//C:/temp/data:/data:rw"},
	}, nil)

	require.NoError(t, err)
	require.Equal(t, []string{"//C:/temp/data:/data:rw"}, cfg.Host.Binds)
}
