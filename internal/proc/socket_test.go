// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package proc

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/sock"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/require"
)

func TestDAGSocketAddr(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{Name: "mydag", Location: "path/to/dag.yml"}
	require.Equal(t, sock.Addr(dag.Location, "run123"), DAGSocketAddr(dag, "run123"))
	require.Equal(t, sock.Addr(dag.Name, "child456"), SubDAGSocketAddr(dag, "child456"))

	dag.Location = ""
	require.Equal(t, sock.Addr(dag.Name, "run123"), DAGSocketAddr(dag, "run123"))
}
