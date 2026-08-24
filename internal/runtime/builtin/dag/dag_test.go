// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dag

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDAGExecutorDetermineNodeStatusAborted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		preconditionNotMet bool
		wantStatus         ir.NodeStatus
		wantError          bool
	}{
		{
			name:               "root precondition not met",
			preconditionNotMet: true,
			wantStatus:         ir.NodeSkipped,
		},
		{
			name:       "run aborted",
			wantStatus: ir.NodeFailed,
			wantError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			executor := &dagExecutor{
				result: &ir.RunStatus{
					DAGRunID:           "child-1",
					Status:             ir.Aborted,
					PreconditionNotMet: tt.preconditionNotMet,
				},
			}

			status, err := executor.DetermineNodeStatus()
			assert.Equal(t, tt.wantStatus, status)
			if tt.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
