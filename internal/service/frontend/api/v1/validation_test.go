// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"testing"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/stretchr/testify/assert"
)

func TestValidateRequiredInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		step      core.Step
		body      *api.ApproveStepRequest
		expectErr bool
		errMsg    string
	}{
		{
			name: "no approval config - always valid",
			step: core.Step{
				Name: "test",
			},
			body:      nil,
			expectErr: false,
		},
		{
			name: "approval with no required fields - always valid",
			step: core.Step{
				Name: "test",
				Approval: &core.ApprovalConfig{
					Input: []string{"reason", "approver"},
				},
			},
			body:      nil,
			expectErr: false,
		},
		{
			name: "required fields provided",
			step: core.Step{
				Name: "test",
				Approval: &core.ApprovalConfig{
					Input:    []string{"reason", "approver"},
					Required: []string{"reason"},
				},
			},
			body: &api.ApproveStepRequest{
				Inputs: &map[string]string{
					"reason": "approved for testing",
				},
			},
			expectErr: false,
		},
		{
			name: "required fields missing - no body",
			step: core.Step{
				Name: "test",
				Approval: &core.ApprovalConfig{
					Required: []string{"reason"},
				},
			},
			body:      nil,
			expectErr: true,
			errMsg:    "missing required inputs: [reason]",
		},
		{
			name: "required fields missing - empty inputs",
			step: core.Step{
				Name: "test",
				Approval: &core.ApprovalConfig{
					Required: []string{"reason", "approver"},
				},
			},
			body: &api.ApproveStepRequest{
				Inputs: &map[string]string{},
			},
			expectErr: true,
			errMsg:    "missing required inputs: [reason approver]",
		},
		{
			name: "partial required fields provided",
			step: core.Step{
				Name: "test",
				Approval: &core.ApprovalConfig{
					Required: []string{"reason", "approver"},
				},
			},
			body: &api.ApproveStepRequest{
				Inputs: &map[string]string{
					"reason": "approved",
				},
			},
			expectErr: true,
			errMsg:    "missing required inputs: [approver]",
		},
		{
			name: "all required fields provided with extras",
			step: core.Step{
				Name: "test",
				Approval: &core.ApprovalConfig{
					Required: []string{"reason"},
				},
			},
			body: &api.ApproveStepRequest{
				Inputs: &map[string]string{
					"reason":  "approved",
					"comment": "extra field",
				},
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateRequiredInputs(tt.step, tt.body)
			if tt.expectErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Equal(t, tt.errMsg, err.Error())
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
