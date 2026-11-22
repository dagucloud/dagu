// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The steps of a report's runs in progress stay within maxActiveStepsBytes:
// the runs listed first carry theirs, and the runs past the budget carry
// none.
func TestRunsInProgressShareTheirStepsBudget(t *testing.T) {
	t.Parallel()

	statuses := map[string]*ir.DAGRunStatus{}
	var active []activeRun
	for i := range 30 {
		id := fmt.Sprint("run-", i)
		status := run("etl", id, ir.Running)
		for j := range 250 {
			name := fmt.Sprintf("%03d%s", j, strings.Repeat("\u0001", maxStepNameBytes))
			status.Nodes = append(status.Nodes, &ir.Node{Step: ir.Step{Name: name}, Status: ir.NodeNotStarted})
		}
		statuses[id] = status
		active = append(active, newActiveRun(status))
	}
	reader := &stepReader{runs: &fakeRuns{statuses: statuses}}

	entries := reader.entries(t.Context(), active, testClockStart)

	require.Len(t, entries, len(active))
	size, carried := 0, 0
	for _, entry := range entries {
		if len(entry.Steps) == 0 {
			continue
		}
		data, err := json.Marshal(entry.Steps)
		require.NoError(t, err)
		size += len(data)
		carried++
	}
	assert.LessOrEqual(t, size, maxActiveStepsBytes)
	assert.NotEmpty(t, entries[0].Steps, "the runs listed first carry their steps")
	assert.Empty(t, entries[len(entries)-1].Steps, "the runs past the budget carry none")
	assert.Less(t, carried, len(entries))
}
