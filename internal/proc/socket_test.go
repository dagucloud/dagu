// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package proc

import (
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/sock"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/require"
)

// Two installs on one host can run a DAG of the same name under the same run
// ID, as a service DAG does on its first run. Their runs must not share a
// socket.
func TestDAGSocketAddrPerDataDir(t *testing.T) {
	t.Parallel()

	ref := ir.NewDAGRunRef("service", "restart-913b3b284de5")
	a := filepath.Join(t.TempDir(), "a", "data")
	b := filepath.Join(t.TempDir(), "b", "data")

	require.NotEqual(t, DAGSocketAddr(a, ref), DAGSocketAddr(b, ref))
	require.NotEqual(t, ProjectDAGSocketAddr(a, "weather", ref), ProjectDAGSocketAddr(b, "weather", ref))
	require.Equal(t, DAGSocketAddr(a, ref), DAGSocketAddr(a, ref))
	require.Equal(t, DAGSocketAddr(a, ref), ProjectDAGSocketAddr(a, "", ref))
	require.Equal(t, DAGSocketAddr(a, ref), ProjectDAGSocketAddr(a, "default", ref))
	require.Equal(t, DAGSocketAddr(a, ref), ProjectDAGSocketAddr(a, "Default", ref))
	require.NotEqual(t, DAGSocketAddr(a, ref), ProjectDAGSocketAddr(a, "weather", ref))
	require.NotEqual(t, DAGSocketAddr(a, ref), sock.LegacyAddr(ref.Name, ref.ID))
}
