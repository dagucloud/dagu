// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
)

func TestParallelStatusDetailsIdentifyChildRuns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		runParamsList []executor.RunParams
		results       map[string]*ir.RunStatus
		want          []dagrun.NodeStatusDetail
	}{
		{
			name: "unique child names omit params",
			runParamsList: []executor.RunParams{
				{RunID: "run-a", DAGName: "bnci/_intraday.yaml", Params: "SUBDAG=bnci/_intraday.yaml RUN_MODE=test"},
				{RunID: "run-b", DAGName: "bnsu/_intraday.yaml", Params: "SUBDAG=bnsu/_intraday.yaml RUN_MODE=test"},
			},
			results: map[string]*ir.RunStatus{
				"run-a": {Name: "intraday-bnci", DAGRunID: "run-a", Status: ir.Failed},
				"run-b": {Name: "intraday-bnsu", DAGRunID: "run-b", Status: ir.Succeeded},
			},
			want: []dagrun.NodeStatusDetail{
				{Label: "intraday-bnci", Status: ir.NodeFailed},
				{Label: "intraday-bnsu", Status: ir.NodeSucceeded},
			},
		},
		{
			name: "resolved child names take precedence when checking uniqueness",
			runParamsList: []executor.RunParams{
				{RunID: "run-a", DAGName: "intraday", Params: "CUSTOMER=a"},
				{RunID: "run-b", DAGName: "intraday", Params: "CUSTOMER=b"},
			},
			results: map[string]*ir.RunStatus{
				"run-a": {Name: "intraday-a", DAGRunID: "run-a", Status: ir.Failed},
				"run-b": {Name: "intraday-b", DAGRunID: "run-b", Status: ir.Succeeded},
			},
			want: []dagrun.NodeStatusDetail{
				{Label: "intraday-a", Status: ir.NodeFailed},
				{Label: "intraday-b", Status: ir.NodeSucceeded},
			},
		},
		{
			name: "duplicate child names retain params",
			runParamsList: []executor.RunParams{
				{RunID: "run-a", DAGName: "child", Params: "CUSTOMER=a"},
				{RunID: "run-b", DAGName: "child", Params: "CUSTOMER=b"},
			},
			results: map[string]*ir.RunStatus{
				"run-a": {Name: "child", DAGRunID: "run-a", Params: "CUSTOMER=a", Status: ir.Failed},
				"run-b": {Name: "child", DAGRunID: "run-b", Params: "CUSTOMER=b", Status: ir.Succeeded},
			},
			want: []dagrun.NodeStatusDetail{
				{Label: "child (CUSTOMER=a)", Status: ir.NodeFailed},
				{Label: "child (CUSTOMER=b)", Status: ir.NodeSucceeded},
			},
		},
		{
			name: "missing child names use existing fallbacks",
			runParamsList: []executor.RunParams{
				{RunID: "run-a", Params: "CUSTOMER=a"},
				{RunID: "run-b"},
			},
			want: []dagrun.NodeStatusDetail{
				{Label: "CUSTOMER=a", Status: ir.NodeFailed},
				{Label: "run-b", Status: ir.NodeFailed},
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
