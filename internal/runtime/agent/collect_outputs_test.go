// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/collections"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectOutputsUsesCanonicalStringOutputValue(t *testing.T) {
	t.Parallel()

	t.Run("PrefersOutputValue", func(t *testing.T) {
		t.Parallel()

		value := "canonical"
		vars := &collections.SyncMap{}
		vars.Store("RESULT", "RESULT=legacy")

		plan, err := runtime.NewPlanFromNodes(
			runtime.NodeWithData(runtime.NodeData{
				Step: core.Step{Name: "publish", Output: "RESULT"},
				State: runtime.NodeState{
					OutputValue:     &value,
					OutputVariables: vars,
				},
			}),
		)
		require.NoError(t, err)

		a := &Agent{plan: plan}
		assert.Equal(t, map[string]string{"result": "canonical"}, a.collectOutputs(context.Background()))
	})

	t.Run("FallsBackToLegacyOutputVariables", func(t *testing.T) {
		t.Parallel()

		vars := &collections.SyncMap{}
		vars.Store("RESULT", "RESULT=legacy")

		plan, err := runtime.NewPlanFromNodes(
			runtime.NodeWithData(runtime.NodeData{
				Step: core.Step{Name: "publish", Output: "RESULT"},
				State: runtime.NodeState{
					OutputVariables: vars,
				},
			}),
		)
		require.NoError(t, err)

		a := &Agent{plan: plan}
		assert.Equal(t, map[string]string{"result": "legacy"}, a.collectOutputs(context.Background()))
	})

	t.Run("IncludesPublishedOutputs", func(t *testing.T) {
		t.Parallel()

		outputsValue := `{"messageId":"msg-123","accepted":true}`
		plan, err := runtime.NewPlanFromNodes(
			runtime.NodeWithData(runtime.NodeData{
				Step: core.Step{Name: "publish"},
				State: runtime.NodeState{
					OutputsValue: &outputsValue,
				},
			}),
		)
		require.NoError(t, err)

		a := &Agent{plan: plan}
		assert.Equal(t, map[string]string{
			"messageId": "msg-123",
			"accepted":  "true",
		}, a.collectOutputs(context.Background()))
	})
}
