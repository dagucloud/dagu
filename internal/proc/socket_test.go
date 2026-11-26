// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package proc

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/require"
)

// TestDAGSocketAddr pins the addresses byte for byte: a process of an older
// version, or one with projects off, reaches a run of the default project
// only at the address it has always used.
func TestDAGSocketAddr(t *testing.T) {
	t.Parallel()

	dir := "/tmp"
	if runtime.GOOS == "windows" {
		dir = os.TempDir()
	}
	ref := ir.NewDAGRunRef("mydag", "run123")
	v2 := filepath.Join(dir, "@dagu_mydag_c2a8cc.sock")

	require.Equal(t, v2, DAGSocketAddr(ref))
	require.Equal(t, v2, ProjectDAGSocketAddr("", ref))
	require.Equal(t, v2, ProjectDAGSocketAddr("default", ref))
	require.Equal(t, v2, ProjectDAGSocketAddr("Default", ref))
	require.Equal(t, filepath.Join(dir, "@dagu_weather_mydag_4eb4e3.sock"), ProjectDAGSocketAddr("weather", ref))
}
