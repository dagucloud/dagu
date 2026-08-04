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

	exec := &parallelExecutor{
		runParamsList: []executor.RunParams{
			{RunID: "run-a", DAGName: "child", Params: "CUSTOMER=a"},
			{RunID: "run-b", DAGName: "child", Params: "CUSTOMER=b"},
		},
		results: map[string]*exec1.RunStatus{
			"run-a": {Name: "child", DAGRunID: "run-a", Params: "CUSTOMER=a", Status: core.Failed},
			"run-b": {Name: "child", DAGRunID: "run-b", Params: "CUSTOMER=b", Status: core.Succeeded},
		},
	}

	assert.Equal(t, []exec1.NodeStatusDetail{
		{Label: "child (CUSTOMER=a)", Status: core.NodeFailed},
		{Label: "child (CUSTOMER=b)", Status: core.NodeSucceeded},
	}, exec.GetStatusDetails())
}
