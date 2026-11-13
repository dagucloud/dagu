// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/collections"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
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
			Step: ir.Step{Output: "RESULT"},
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
			Step: ir.Step{Output: "RESULT"},
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
		Step: ir.Step{
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
		Step: ir.Step{
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

// A restored captured output that masking changed counts as masked in the
// top-level fields that hold the mask, or as a whole when it is not a JSON
// object. Text without the mask is not masked, since the record does not say
// which captured output changed.
func TestDataStepInfoMasksCapturedOutput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		output string
		want   cmnvalue.StepMasks
	}{
		{name: "object", output: `{"token":"*******","region":"us"}`, want: cmnvalue.StepMasks{Output: []string{"token"}}},
		{name: "text", output: "Bearer *******", want: cmnvalue.StepMasks{AllOutput: true}},
		{name: "mask in a key", output: `{"*******":"x"}`, want: cmnvalue.StepMasks{AllOutput: true}},
		{name: "no mask", output: `{"region":"us"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data := newSafeData(NodeData{
				Step:  ir.Step{ID: "fetch", Name: "fetch"},
				State: NodeState{OutputValue: &tc.output, MaskedOutputs: &ir.MaskedOutputs{Captured: true}},
			})
			assert.Equal(t, tc.want, data.StepInfo().Masked)
		})
	}
}
