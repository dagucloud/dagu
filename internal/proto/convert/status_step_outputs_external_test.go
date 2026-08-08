// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package convert_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/proto/convert"
	"github.com/stretchr/testify/require"
)

func TestDAGRunStatusProtoRoundTripPreservesStepOutputsValue(t *testing.T) {
	t.Parallel()

	stepOutputsValue := `{"image_tag":"v1.2.3","metadata":"{\"image\":\"api\"}"}`
	original := &dagrun.DAGRunStatus{
		Name:     "build",
		DAGRunID: "run-1",
		Status:   ir.Running,
		Nodes: []*dagrun.Node{
			{
				Step:             ir.Step{Name: "publish", ID: "publish"},
				Status:           ir.NodeSucceeded,
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
