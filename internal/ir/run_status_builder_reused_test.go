// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/require"
)

// The provenance of reused inputs is set by the builder, copied (not shared)
// and survives the persisted JSON form.
func TestStatusReusedInputs(t *testing.T) {
	t.Parallel()

	inputs := []ir.ReusedInput{
		{Step: "build", RunID: "run-1", CapturedAt: "2026-10-26T10:00:00Z", Via: ir.ReusedInputViaRun},
		{Step: "login", RunID: "run-2", Via: ir.ReusedInputViaCache},
	}
	dag := &ir.DAG{Name: "reused", Steps: []ir.Step{{Name: "build"}}}
	status := ir.NewStatusBuilder(dag).Create("run-3", ir.Queued, 0, time.Time{}, ir.WithReusedInputs(inputs))
	inputs[0].Step = "changed"
	require.Equal(t, "build", status.ReusedInputs[0].Step)

	raw, err := json.Marshal(status)
	require.NoError(t, err)
	var decoded ir.DAGRunStatus
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, status.ReusedInputs, decoded.ReusedInputs)
	require.Contains(t, string(raw), `"via":"cache"`)
	require.NotContains(t, string(raw), `"capturedAt":""`)

	// A run without the record leaves the key out.
	plain, err := json.Marshal(ir.NewStatusBuilder(dag).Create("run-4", ir.Queued, 0, time.Time{}))
	require.NoError(t, err)
	require.NotContains(t, string(plain), "reusedInputs")
}
