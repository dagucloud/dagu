// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/stretchr/testify/assert"
)

func TestSimpleProgressDisplay_New(t *testing.T) {
	dag := &core.DAG{
		Name: "test-dag",
		Steps: []core.Step{
			{Name: "step1"},
			{Name: "step2"},
		},
	}

	display := NewSimpleProgressDisplay(dag)
	assert.NotNil(t, display)
	assert.Equal(t, 2, display.total)
	assert.Equal(t, 0, display.completed)
}

func TestSimpleProgressDisplay_UpdateNode(t *testing.T) {
	dag := &core.DAG{
		Name: "test-dag",
		Steps: []core.Step{
			{Name: "step1"},
			{Name: "step2"},
		},
	}

	display := NewSimpleProgressDisplay(dag)

	// Update with running node - should not increment completed
	display.UpdateNode(&exec.Node{
		Step:   core.Step{Name: "step1"},
		Status: core.NodeRunning,
	})
	assert.Equal(t, 0, display.completed)

	// Update with succeeded node - should increment completed
	display.UpdateNode(&exec.Node{
		Step:   core.Step{Name: "step1"},
		Status: core.NodeSucceeded,
	})
	assert.Equal(t, 1, display.completed)

	// Update with failed node - should increment completed
	display.UpdateNode(&exec.Node{
		Step:   core.Step{Name: "step2"},
		Status: core.NodeFailed,
	})
	assert.Equal(t, 2, display.completed)
}

func TestSimpleProgressDisplay_SetDAGRunInfo(t *testing.T) {
	dag := &core.DAG{Name: "test-dag"}
	display := NewSimpleProgressDisplay(dag)

	display.SetDAGRunInfo("run-123", "param1=value1")
	assert.Equal(t, "run-123", display.dagRunID)
	assert.Equal(t, "param1=value1", display.params)
}

func TestSimpleProgressDisplay_UpdateStatus(t *testing.T) {
	dag := &core.DAG{Name: "test-dag"}
	display := NewSimpleProgressDisplay(dag)

	display.UpdateStatus(&exec.DAGRunStatus{
		Status: core.Succeeded,
	})
	assert.Equal(t, core.Succeeded, display.status)

	display.UpdateStatus(&exec.DAGRunStatus{
		Status: core.Failed,
	})
	assert.Equal(t, core.Failed, display.status)
}

func TestSimpleProgressDisplay_NoDuplicateCounting(t *testing.T) {
	dag := &core.DAG{
		Name: "test-dag",
		Steps: []core.Step{
			{Name: "step1"},
			{Name: "step2"},
		},
	}

	display := NewSimpleProgressDisplay(dag)

	// Update same node multiple times - should only count once
	display.UpdateNode(&exec.Node{
		Step:   core.Step{Name: "step1"},
		Status: core.NodeSucceeded,
	})
	assert.Equal(t, 1, display.completed)

	display.UpdateNode(&exec.Node{
		Step:   core.Step{Name: "step1"},
		Status: core.NodeSucceeded,
	})
	assert.Equal(t, 1, display.completed) // Still 1, not 2

	display.UpdateNode(&exec.Node{
		Step:   core.Step{Name: "step1"},
		Status: core.NodeSucceeded,
	})
	assert.Equal(t, 1, display.completed) // Still 1, not 3
}

func TestSimpleProgressDisplay_PartiallySucceeded(t *testing.T) {
	dag := &core.DAG{
		Name: "test-dag",
		Steps: []core.Step{
			{Name: "step1"},
			{Name: "step2"},
			{Name: "step3"},
		},
	}

	display := NewSimpleProgressDisplay(dag)

	// NodePartiallySucceeded should count as completed
	display.UpdateNode(&exec.Node{
		Step:   core.Step{Name: "step1"},
		Status: core.NodePartiallySucceeded,
	})
	assert.Equal(t, 1, display.completed)

	display.UpdateNode(&exec.Node{
		Step:   core.Step{Name: "step2"},
		Status: core.NodeSucceeded,
	})
	assert.Equal(t, 2, display.completed)

	display.UpdateNode(&exec.Node{
		Step:   core.Step{Name: "step3"},
		Status: core.NodePartiallySucceeded,
	})
	assert.Equal(t, 3, display.completed)
}
