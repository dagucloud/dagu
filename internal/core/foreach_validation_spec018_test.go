// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateStepsSpec018ForeachBodyDependencyByID(t *testing.T) {
	t.Parallel()

	dag := &core.DAG{
		Steps: []core.Step{
			{
				ID:             "loop",
				Name:           "loop",
				ExecutorConfig: core.ExecutorConfig{Type: core.ExecutorTypeForeach},
				Foreach: &core.ForeachConfig{
					Items: []any{"one"},
					Steps: []core.Step{
						{
							ID:             "first",
							Name:           "First",
							ExecutorConfig: core.ExecutorConfig{Type: "test-no-validator"},
						},
						{
							ID:             "second",
							Name:           "Second",
							Depends:        []string{"first"},
							ExecutorConfig: core.ExecutorConfig{Type: "test-no-validator"},
						},
					},
				},
			},
		},
	}

	require.NoError(t, core.ValidateSteps(dag))
	assert.Equal(t, []string{"First"}, dag.Steps[0].Foreach.Steps[1].Depends)
}

func TestValidateStepsSpec018ForeachRejectsVisibleIdentityCollision(t *testing.T) {
	t.Parallel()

	dag := &core.DAG{
		Steps: []core.Step{
			{
				ID:             "setup",
				Name:           "setup",
				ExecutorConfig: core.ExecutorConfig{Type: "test-no-validator"},
			},
			{
				ID:             "loop",
				Name:           "loop",
				ExecutorConfig: core.ExecutorConfig{Type: core.ExecutorTypeForeach},
				Foreach: &core.ForeachConfig{
					Items: []any{"one"},
					Steps: []core.Step{
						{
							ID:             "setup",
							Name:           "write",
							ExecutorConfig: core.ExecutorConfig{Type: "test-no-validator"},
						},
					},
				},
			},
		},
	}

	err := core.ValidateSteps(dag)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "collides with a visible step")
}

func TestValidateStepsSpec018ForeachRejectsTopLevelBodyDependency(t *testing.T) {
	t.Parallel()

	dag := &core.DAG{
		Steps: []core.Step{
			{
				ID:             "setup",
				Name:           "setup",
				ExecutorConfig: core.ExecutorConfig{Type: "test-no-validator"},
			},
			{
				ID:             "loop",
				Name:           "loop",
				Depends:        []string{"setup"},
				ExecutorConfig: core.ExecutorConfig{Type: core.ExecutorTypeForeach},
				Foreach: &core.ForeachConfig{
					Items: []any{"one"},
					Steps: []core.Step{
						{
							ID:             "write",
							Name:           "write",
							Depends:        []string{"setup"},
							ExecutorConfig: core.ExecutorConfig{Type: "test-no-validator"},
						},
					},
				},
			},
		},
	}

	err := core.ValidateSteps(dag)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "body dependencies must stay inside foreach.steps")
}
