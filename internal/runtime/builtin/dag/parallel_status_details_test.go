// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core"
	exec1 "github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
)

func TestParallelStatusDetailsIdentifyChildRuns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		runParamsList []executor.RunParams
		results       map[string]*exec1.RunStatus
		want          []exec1.NodeStatusDetail
	}{
		{
			name: "unique child names omit params",
			runParamsList: []executor.RunParams{
				{RunID: "run-a", DAGName: "bnci/_intraday.yaml", Params: "SUBDAG=bnci/_intraday.yaml RUN_MODE=test"},
				{RunID: "run-b", DAGName: "bnsu/_intraday.yaml", Params: "SUBDAG=bnsu/_intraday.yaml RUN_MODE=test"},
			},
			results: map[string]*exec1.RunStatus{
				"run-a": {Name: "intraday-bnci", DAGRunID: "run-a", Status: core.Failed},
				"run-b": {Name: "intraday-bnsu", DAGRunID: "run-b", Status: core.Succeeded},
			},
			want: []exec1.NodeStatusDetail{
				{Label: "intraday-bnci", Status: core.NodeFailed},
				{Label: "intraday-bnsu", Status: core.NodeSucceeded},
			},
		},
		{
			name: "duplicate child names retain params",
			runParamsList: []executor.RunParams{
				{RunID: "run-a", DAGName: "child", Params: "CUSTOMER=a"},
				{RunID: "run-b", DAGName: "child", Params: "CUSTOMER=b"},
			},
			results: map[string]*exec1.RunStatus{
				"run-a": {Name: "child", DAGRunID: "run-a", Params: "CUSTOMER=a", Status: core.Failed},
				"run-b": {Name: "child", DAGRunID: "run-b", Params: "CUSTOMER=b", Status: core.Succeeded},
			},
			want: []exec1.NodeStatusDetail{
				{Label: "child (CUSTOMER=a)", Status: core.NodeFailed},
				{Label: "child (CUSTOMER=b)", Status: core.NodeSucceeded},
			},
		},
		{
			name: "missing child names use existing fallbacks",
			runParamsList: []executor.RunParams{
				{RunID: "run-a", Params: "CUSTOMER=a"},
				{RunID: "run-b"},
			},
			want: []exec1.NodeStatusDetail{
				{Label: "CUSTOMER=a", Status: core.NodeFailed},
				{Label: "run-b", Status: core.NodeFailed},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &parallelExecutor{
				runParamsList: tt.runParamsList,
				results:       tt.results,
			}

			assert.Equal(t, tt.want, exec.GetStatusDetails())
		})
	}
}
