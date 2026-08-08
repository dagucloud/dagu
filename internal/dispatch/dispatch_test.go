// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dispatch_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
)

func TestShouldDispatchToCoordinator(t *testing.T) {
	tests := []struct {
		name           string
		dag            *ir.DAG
		hasCoordinator bool
		defaultMode    config.ExecutionMode
		want           bool
	}{
		{
			name:           "ForceLocal is true, always local",
			dag:            &ir.DAG{ForceLocal: true, WorkerSelector: map[string]string{"gpu": "true"}},
			hasCoordinator: true,
			defaultMode:    config.ExecutionModeDistributed,
			want:           false,
		},
		{
			name:           "no coordinator, always local",
			dag:            &ir.DAG{WorkerSelector: map[string]string{"gpu": "true"}},
			hasCoordinator: false,
			defaultMode:    config.ExecutionModeDistributed,
			want:           false,
		},
		{
			name:           "workerSelector present, dispatch",
			dag:            &ir.DAG{WorkerSelector: map[string]string{"gpu": "true"}},
			hasCoordinator: true,
			defaultMode:    config.ExecutionModeLocal,
			want:           true,
		},
		{
			name:           "defaultMode distributed, dispatch",
			dag:            &ir.DAG{},
			hasCoordinator: true,
			defaultMode:    config.ExecutionModeDistributed,
			want:           true,
		},
		{
			name:           "defaultMode local, no workerSelector, local",
			dag:            &ir.DAG{},
			hasCoordinator: true,
			defaultMode:    config.ExecutionModeLocal,
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dispatch.ShouldDispatchToCoordinator(tt.dag, tt.hasCoordinator, tt.defaultMode)
			assert.Equal(t, tt.want, got)
		})
	}
}
