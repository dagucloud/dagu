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

// TestLegacyProjectDAGSocketAddr pins the addresses of earlier versions byte
// for byte: a run that such a process started is reachable only there.
func TestLegacyProjectDAGSocketAddr(t *testing.T) {
	t.Parallel()

	dir := "/tmp"
	if runtime.GOOS == "windows" {
		dir = os.TempDir()
	}
	ref := ir.NewDAGRunRef("mydag", "run123")
	v2 := filepath.Join(dir, "@dagu_mydag_c2a8cc.sock")

	require.Equal(t, v2, LegacyProjectDAGSocketAddr("", ref))
	require.Equal(t, v2, LegacyProjectDAGSocketAddr("default", ref))
	require.Equal(t, v2, LegacyProjectDAGSocketAddr("Default", ref))
	require.Equal(t, filepath.Join(dir, "@dagu_weather_mydag_4eb4e3.sock"), LegacyProjectDAGSocketAddr("weather", ref))
}

// Two homes on one host can run a DAG of the same name under the same run ID,
// as a service DAG does on its first run. Their runs must not share a socket.
func TestDAGSocketAddrPerDataDir(t *testing.T) {
	t.Parallel()

	ref := ir.NewDAGRunRef("service", "restart-913b3b284de5")
	a := filepath.Join(t.TempDir(), "a", "data")
	b := filepath.Join(t.TempDir(), "b", "data")

	require.NotEqual(t, DAGSocketAddr(a, ref), DAGSocketAddr(b, ref))
	require.NotEqual(t, ProjectDAGSocketAddr(a, "weather", ref), ProjectDAGSocketAddr(b, "weather", ref))
	require.Equal(t, DAGSocketAddr(a, ref), DAGSocketAddr(a, ref))
	require.Equal(t, DAGSocketAddr(a, ref), ProjectDAGSocketAddr(a, "default", ref))
	require.NotEqual(t, DAGSocketAddr(a, ref), ProjectDAGSocketAddr(a, "weather", ref))
	require.NotEqual(t, DAGSocketAddr(a, ref), LegacyProjectDAGSocketAddr("", ref))
}
