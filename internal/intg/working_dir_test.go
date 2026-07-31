// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

// A sub-workflow written in the same document runs from a temporary copy of
// that document. Its relative working_dir still means the directory beside the
// file the author wrote, which is where the files it refers to actually are.
func TestSubDAGRelativeWorkingDirResolvesAgainstAuthoredFile(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	dag := th.DAG(t, `
steps:
  - name: call
    action: dag.run
    with:
      dag: reader
    output: CHILD
---
name: reader
working_dir: ./checkout
steps:
  - name: read
    run: cat marker.txt
    output: MARKER
`)

	checkout := filepath.Join(filepath.Dir(dag.Location), "checkout")
	require.NoError(t, os.MkdirAll(checkout, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "marker.txt"), []byte("beside the author's file"), 0o600))

	dag.Agent().RunSuccess(t)
	dag.AssertLatestStatus(t, core.Succeeded)

	status, err := th.DAGRunMgr.GetLatestStatus(th.Context, dag.DAG)
	require.NoError(t, err)
	require.Len(t, status.Nodes, 1)
	require.NotNil(t, status.Nodes[0].OutputVariables)

	value, ok := status.Nodes[0].OutputVariables.Load("CHILD")
	require.True(t, ok)
	require.Contains(t, value, "beside the author's file",
		"the child read the file beside the authored DAG")
}
