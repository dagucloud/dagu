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
			active: map[string]bool{}, resultless: map[string]bool{},
			maxOutputSize: limit,
		},
	}
	e.SetNodes(matched)
	for runID := range results {
		e.nodes.started[runID] = time.Now()
	}
	return e
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

			var aggregate struct {
				Summary struct{ Total, Succeeded, Failed int } `json:"summary"`
				Results []struct {
					Node    string            `json:"node"`
					Status  string            `json:"status"`
					Outputs map[string]string `json:"outputs"`
				} `json:"results"`
				Outputs []map[string]string `json:"outputs"`
			}
			require.NoError(t, json.Unmarshal(out.Bytes(), &aggregate))
			require.Equal(t, 3, aggregate.Summary.Total)
			require.Equal(t, 1, aggregate.Summary.Succeeded)
			require.Equal(t, 0, aggregate.Summary.Failed)
			var statuses []string
			for _, r := range aggregate.Results {
				statuses = append(statuses, r.Node+"="+r.Status)
			}
			require.Equal(t, []string{"web01=succeeded", "web02=disabled", "web03=not_started"}, statuses)
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
