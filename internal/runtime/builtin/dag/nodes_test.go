// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/stretchr/testify/require"
)

func nodesExecutorWith(limit int, matched []executor.NodeRun, results map[string]*ir.RunStatus) *parallelExecutor {
	e := &parallelExecutor{
		results: results,
		nodes: &nodeDispatch{
			started: map[string]time.Time{}, finished: map[string]time.Time{},
			active: map[string]bool{}, retries: map[string]int{}, resultless: map[string]bool{},
			maxOutputSize: limit,
		},
	}
	e.SetNodes(matched)
	for runID := range results {
		e.nodes.started[runID] = time.Now()
	}
	return e
}

// nodeAggregate is the aggregate a step with nodes writes to its stdout.
type nodeAggregate struct {
	Summary struct{ Total, Succeeded, Failed int } `json:"summary"`
	Results []struct {
		Node     string            `json:"node"`
		Status   string            `json:"status"`
		DAGRunID string            `json:"dagRunId"`
		Outputs  map[string]string `json:"outputs"`
	} `json:"results"`
	Outputs []map[string]string `json:"outputs"`
}

func readNodeAggregate(t *testing.T, data []byte) nodeAggregate {
	t.Helper()

	var aggregate nodeAggregate
	require.NoError(t, json.Unmarshal(data, &aggregate))
	return aggregate
}

// statuses returns each node with its status, in the aggregate's order.
func (a nodeAggregate) statuses() []string {
	statuses := make([]string, 0, len(a.Results))
	for _, result := range a.Results {
		statuses = append(statuses, result.Node+"="+result.Status)
	}
	return statuses
}

// An aggregate over max_output_size keeps every node's status and leaves out
// the outputs, which each child run keeps.
func TestNodeAggregateLimit(t *testing.T) {
	t.Parallel()

	big := strings.Repeat("x", 512)
	matched := []executor.NodeRun{
		{Node: "web01", Run: executor.RunParams{RunID: "a"}},
		{Node: "web02", Disabled: true},
		{Node: "web03", Run: executor.RunParams{RunID: "c"}},
	}
	results := map[string]*ir.RunStatus{
		"a": {DAGRunID: "a", Status: ir.Succeeded, Outputs: map[string]string{"OUT": big}},
	}

	for _, tc := range []struct {
		name        string
		limit       int
		wantOutputs bool
	}{
		{"WithinLimit", 4096, true},
		{"OverLimit", 512, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer
			e := nodesExecutorWith(tc.limit, matched, results)
			e.SetStdout(&out)
			require.NoError(t, e.outputNodeResults())

			aggregate := readNodeAggregate(t, out.Bytes())
			require.Equal(t, 3, aggregate.Summary.Total)
			require.Equal(t, 1, aggregate.Summary.Succeeded)
			require.Equal(t, 0, aggregate.Summary.Failed)
			require.Equal(t, []string{"web01=succeeded", "web02=disabled", "web03=not_started"}, aggregate.statuses())
			if tc.wantOutputs {
				require.Equal(t, big, aggregate.Results[0].Outputs["OUT"])
				require.Len(t, aggregate.Outputs, 1)
			} else {
				require.Nil(t, aggregate.Results[0].Outputs)
				require.Empty(t, aggregate.Outputs)
			}
		})
	}
}
