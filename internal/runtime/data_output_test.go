// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/collections"
	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNodeDataStringFormOutputValue(t *testing.T) {
	t.Parallel()

	t.Run("PrefersOutputValue", func(t *testing.T) {
		t.Parallel()

		value := "canonical"
		vars := &collections.SyncMap{}
		vars.Store("RESULT", "RESULT=legacy")

		data := NodeData{
			Step: core.Step{Output: "RESULT"},
			State: NodeState{
				OutputValue:     &value,
				OutputVariables: vars,
			},
		}

		got, ok := data.StringFormOutputValue()
		require.True(t, ok)
		assert.Equal(t, "canonical", got)
	})

	t.Run("FallsBackToLegacyOutputVariables", func(t *testing.T) {
		t.Parallel()

		vars := &collections.SyncMap{}
		vars.Store("RESULT", "RESULT=legacy")

		data := NodeData{
			Step: core.Step{Output: "RESULT"},
			State: NodeState{
				OutputVariables: vars,
			},
		}

		got, ok := data.StringFormOutputValue()
		require.True(t, ok)
		assert.Equal(t, "legacy", got)
	})
}

func TestDataStepInfoUsesStringFormOutputValue(t *testing.T) {
	t.Parallel()

	value := "canonical"
	vars := &collections.SyncMap{}
	vars.Store("RESULT", "RESULT=legacy")

	data := newSafeData(NodeData{
		Step: core.Step{
			ID:     "publish",
			Name:   "publish",
			Output: "RESULT",
		},
		State: NodeState{
			ExitCode:        7,
			Stdout:          "/tmp/publish.out",
			Stderr:          "/tmp/publish.err",
			OutputValue:     &value,
			OutputVariables: vars,
		},
	})

	info := data.StepInfo()
	require.NotNil(t, info.Output)
	assert.Equal(t, "canonical", *info.Output)
	assert.Equal(t, "7", info.ExitCode)
	assert.Equal(t, "/tmp/publish.out", info.Stdout)
	assert.Equal(t, "/tmp/publish.err", info.Stderr)
}

func TestDataStepInfoUsesStructuredOutputValue(t *testing.T) {
	t.Parallel()

	value := `{"version":"v1.2.3"}`
	data := newSafeData(NodeData{
		Step: core.Step{
			ID:   "publish",
			Name: "publish",
		},
		State: NodeState{
			OutputValue: &value,
		},
	})

	info := data.StepInfo()
	require.NotNil(t, info.Output)
	assert.Equal(t, value, *info.Output)
}
