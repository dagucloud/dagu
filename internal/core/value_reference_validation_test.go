// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package core_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var valueReferenceTestExec = core.ExecutorConfig{Type: "test-no-validator"}

func TestValidateStepsStrictReferencesInHandlers(t *testing.T) {
	t.Parallel()

	t.Run("handler can reference main step outputs", func(t *testing.T) {
		t.Parallel()
		dag := &core.DAG{
			Steps: []core.Step{
				{
					Name:           "build",
					ID:             "build",
					ExecutorConfig: valueReferenceTestExec,
					StructuredOutput: map[string]core.StepOutputEntry{
						"image": {HasValue: true, Value: "repo/api:v1"},
					},
				},
			},
			HandlerOn: core.HandlerOn{
				Success: &core.Step{
					Name:           "notify",
					ExecutorConfig: valueReferenceTestExec,
					Script:         "echo ${steps.build.outputs.image}",
				},
			},
		}

		require.NoError(t, core.ValidateSteps(dag))
	})

	t.Run("handler cannot reference handler outputs", func(t *testing.T) {
		t.Parallel()
		dag := &core.DAG{
			Steps: []core.Step{
				{
					Name:           "build",
					ID:             "build",
					ExecutorConfig: valueReferenceTestExec,
				},
			},
			HandlerOn: core.HandlerOn{
				Success: &core.Step{
					Name:           "notify",
					ID:             "notify",
					ExecutorConfig: valueReferenceTestExec,
					Script:         "echo ${steps.notify.outputs.message}",
					StructuredOutput: map[string]core.StepOutputEntry{
						"message": {HasValue: true, Value: "sent"},
					},
				},
			},
		}

		err := core.ValidateSteps(dag)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `unknown step "notify"`)
	})
}
