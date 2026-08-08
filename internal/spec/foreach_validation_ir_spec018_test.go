// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateStepsSpec018ForeachBodyDependencyByID(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{
		Steps: []ir.Step{
			{
				ID:             "loop",
				Name:           "loop",
				ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeForeach},
				Foreach: &ir.ForeachConfig{
					Items: []any{"one"},
					Steps: []ir.Step{
						{
							ID:             "first",
							Name:           "First",
							ExecutorConfig: ir.ExecutorConfig{Type: "test-no-validator"},
						},
						{
							ID:             "second",
							Name:           "Second",
							Depends:        []string{"first"},
							ExecutorConfig: ir.ExecutorConfig{Type: "test-no-validator"},
						},
					},
				},
			},
		},
	}

	require.NoError(t, spec.ValidateSteps(dag))
	assert.Equal(t, []string{"First"}, dag.Steps[0].Foreach.Steps[1].Depends)
}

func TestValidateStepsSpec018ForeachBodyApprovalRewindByID(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{
		Steps: []ir.Step{
			{
				ID:             "loop",
				Name:           "loop",
				ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeForeach},
				Foreach: &ir.ForeachConfig{
					Items: []any{"one"},
					Steps: []ir.Step{
						{
							ID:             "prepare_id",
							Name:           "prepare",
							ExecutorConfig: ir.ExecutorConfig{Type: "test-no-validator"},
						},
						{
							ID:             "review_id",
							Name:           "review",
							Depends:        []string{"prepare"},
							ExecutorConfig: ir.ExecutorConfig{Type: "test-no-validator"},
							Approval: &ir.ApprovalConfig{
								RewindTo: "prepare_id",
							},
						},
					},
				},
			},
		},
	}

	require.NoError(t, spec.ValidateSteps(dag))
	require.NotNil(t, dag.Steps[0].Foreach.Steps[1].Approval)
	assert.Equal(t, "prepare", dag.Steps[0].Foreach.Steps[1].Approval.RewindTo)
}

func TestValidateStepsSpec018ForeachRejectsVisibleIdentityCollision(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{
		Steps: []ir.Step{
			{
				ID:             "setup",
				Name:           "setup",
				ExecutorConfig: ir.ExecutorConfig{Type: "test-no-validator"},
			},
			{
				ID:             "loop",
				Name:           "loop",
				ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeForeach},
				Foreach: &ir.ForeachConfig{
					Items: []any{"one"},
					Steps: []ir.Step{
						{
							ID:             "setup",
							Name:           "write",
							ExecutorConfig: ir.ExecutorConfig{Type: "test-no-validator"},
						},
					},
				},
			},
		},
	}

	err := spec.ValidateSteps(dag)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "collides with a visible step")
}

func TestValidateStepsSpec018ForeachRejectsVisibleApprovalRewindTarget(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{
		Steps: []ir.Step{
			{
				ID:             "setup",
				Name:           "setup",
				ExecutorConfig: ir.ExecutorConfig{Type: "test-no-validator"},
			},
			{
				ID:             "loop",
				Name:           "loop",
				ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeForeach},
				Foreach: &ir.ForeachConfig{
					Items: []any{"one"},
					Steps: []ir.Step{
						{
							ID:             "review",
							Name:           "review",
							ExecutorConfig: ir.ExecutorConfig{Type: "test-no-validator"},
							Approval: &ir.ApprovalConfig{
								RewindTo: "setup",
							},
						},
					},
				},
			},
		},
	}

	err := spec.ValidateSteps(dag)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "approval.rewind_to")
}

func TestValidateStepsSpec018ForeachRejectsTopLevelBodyDependency(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{
		Steps: []ir.Step{
			{
				ID:             "setup",
				Name:           "setup",
				ExecutorConfig: ir.ExecutorConfig{Type: "test-no-validator"},
			},
			{
				ID:             "loop",
				Name:           "loop",
				Depends:        []string{"setup"},
				ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeForeach},
				Foreach: &ir.ForeachConfig{
					Items: []any{"one"},
					Steps: []ir.Step{
						{
							ID:             "write",
							Name:           "write",
							Depends:        []string{"setup"},
							ExecutorConfig: ir.ExecutorConfig{Type: "test-no-validator"},
						},
					},
				},
			},
		},
	}

	err := spec.ValidateSteps(dag)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "body dependencies must stay inside foreach.steps")
}

func TestValidateStepsForeachRejectsHumanTaskBody(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{
		Steps: []ir.Step{
			{
				ID:             "loop",
				Name:           "loop",
				ExecutorConfig: ir.ExecutorConfig{Type: ir.ExecutorTypeForeach},
				Foreach: &ir.ForeachConfig{
					Items: []any{"one"},
					Steps: []ir.Step{
						{
							ID:        "review",
							Name:      "review",
							HumanTask: &ir.HumanTaskConfig{Prompt: "Review"},
						},
					},
				},
			},
		},
	}

	err := spec.ValidateSteps(dag)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "human.task cannot be used inside foreach.steps")
}
