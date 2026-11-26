// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/require"
)

func TestReadDAGKeepsOutputs(t *testing.T) {
	t.Parallel()

	th := setupTestRepository(t)
	dag := th.DAG("outputs").DAG
	dag.Outputs = map[string]ir.DAGOutput{"summary": {Step: "diagnose", Output: "summary"}}
	rec := th.CreateAttemptWithDAG(t, time.Now(), "run-1", ir.Succeeded, dag)

	// Editing the source definition after the run must not change the stored contract.
	dag.Outputs["summary"] = ir.DAGOutput{Step: "other", Output: "x"}
	dag.Outputs["extra"] = ir.DAGOutput{Step: "other", Output: "y"}

	stored, err := rec.ReadDAG(th.Context)
	require.NoError(t, err)
	require.Equal(t, map[string]ir.DAGOutput{"summary": {Step: "diagnose", Output: "summary"}}, stored.Outputs)
}
