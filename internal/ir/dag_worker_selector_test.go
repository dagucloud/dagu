// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyWorkerSelector(t *testing.T) {
	t.Parallel()

	t.Run("ReplacesSelectorAndClearsForceLocal", func(t *testing.T) {
		t.Parallel()
		dag := &ir.DAG{WorkerSelector: map[string]string{"zone": "a"}, ForceLocal: true}
		given := map[string]string{"zone": "b", "gpu": "yes"}

		require.NoError(t, dag.ApplyWorkerSelector(given))

		assert.Equal(t, map[string]string{"zone": "b", "gpu": "yes"}, dag.WorkerSelector)
		assert.False(t, dag.ForceLocal)
		given["zone"] = "changed"
		assert.Equal(t, "b", dag.WorkerSelector["zone"], "the DAG keeps its own copy")
	})

	t.Run("EmptySelectorLeavesTheDAGAlone", func(t *testing.T) {
		t.Parallel()
		dag := &ir.DAG{WorkerSelector: map[string]string{"zone": "a"}, ForceLocal: true}

		require.NoError(t, dag.ApplyWorkerSelector(nil))
		require.NoError(t, dag.ApplyWorkerSelector(map[string]string{}))

		assert.Equal(t, map[string]string{"zone": "a"}, dag.WorkerSelector)
		assert.True(t, dag.ForceLocal)
	})

	t.Run("RefusesApprovalSteps", func(t *testing.T) {
		t.Parallel()
		dag := &ir.DAG{Steps: []ir.Step{{Name: "gate", Approval: &ir.ApprovalConfig{}}}}

		err := dag.ApplyWorkerSelector(map[string]string{"zone": "b"})

		require.ErrorIs(t, err, ir.ErrWorkerSelectorApproval)
		assert.Empty(t, dag.WorkerSelector)
	})

	t.Run("RefusesInvalidLabels", func(t *testing.T) {
		t.Parallel()
		for name, selector := range map[string]map[string]string{
			"empty value":  {"zone": " "},
			"bad key":      {"zo ne": "b"},
			"empty key":    {"": "b"},
			"bad value":    {"zone": "b c"},
			"one bad pair": {"zone": "b", "x": ""},
		} {
			t.Run(name, func(t *testing.T) {
				dag := &ir.DAG{}
				require.Error(t, dag.ApplyWorkerSelector(selector))
				assert.Empty(t, dag.WorkerSelector)
			})
		}
	})
}

func TestFormatWorkerSelector(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "{}", ir.FormatWorkerSelector(nil))
	assert.Equal(t, "{gpu: yes, zone: b}", ir.FormatWorkerSelector(map[string]string{"zone": "b", "gpu": "yes"}))
}
