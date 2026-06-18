// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package convert_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/proto/convert"
	"github.com/stretchr/testify/require"
)

func TestDAGRunStatusProtoRoundTripPreservesStepOutputsValue(t *testing.T) {
	t.Parallel()

	stepOutputsValue := `{"image_tag":"v1.2.3","metadata":"{\"image\":\"api\"}"}`
	original := &exec.DAGRunStatus{
		Name:     "build",
		DAGRunID: "run-1",
		Status:   core.Running,
		Nodes: []*exec.Node{
			{
				Step:             core.Step{Name: "publish", ID: "publish"},
				Status:           core.NodeSucceeded,
				StepOutputsValue: &stepOutputsValue,
			},
		},
	}

	protoStatus, err := convert.DAGRunStatusToProto(original)
	require.NoError(t, err)

	roundTripped, err := convert.ProtoToDAGRunStatus(protoStatus)
	require.NoError(t, err)
	require.Len(t, roundTripped.Nodes, 1)
	require.NotNil(t, roundTripped.Nodes[0].StepOutputsValue)
	require.JSONEq(t, stepOutputsValue, *roundTripped.Nodes[0].StepOutputsValue)
}
