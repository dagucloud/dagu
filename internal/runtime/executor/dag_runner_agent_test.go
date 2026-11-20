// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
)

// A child that needs a CLI provider carries the implied label, also when the step
// gives its own selector (Spec 092, "Dispatch").
func TestEffectiveWorkerSelectorAddsTheImpliedLabel(t *testing.T) {
	t.Parallel()

	child := &ir.DAG{
		Name:           "child",
		WorkerSelector: map[string]string{"zone": "eu"},
		Steps:          []ir.Step{{Name: "code", ID: "code", Agent: &ir.AgentRun{Provider: "claude"}}},
	}
	e := &SubDAGExecutor{DAG: child}
	assert.Equal(t, map[string]string{"zone": "eu", "agent.provider.claude": "true"}, e.effectiveWorkerSelector())

	e.SetWorkerSelector(map[string]string{"gpu": "true"})
	assert.Equal(t, map[string]string{"gpu": "true", "agent.provider.claude": "true"}, e.effectiveWorkerSelector())

	plain := &SubDAGExecutor{DAG: &ir.DAG{Name: "plain"}}
	assert.Empty(t, plain.effectiveWorkerSelector())
}
