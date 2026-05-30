// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dispatch_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/dispatch"
	"github.com/stretchr/testify/assert"
)

func TestShouldDispatchToCoordinator(t *testing.T) {
	tests := []struct {
		name           string
		dag            *core.DAG
		hasCoordinator bool
		defaultMode    config.ExecutionMode
		want           bool
	}{
		{
			name:           "ForceLocal is true, always local",
			dag:            &core.DAG{ForceLocal: true, WorkerSelector: map[string]string{"gpu": "true"}},
			hasCoordinator: true,
			defaultMode:    config.ExecutionModeDistributed,
			want:           false,
		},
		{
			name:           "no coordinator, always local",
			dag:            &core.DAG{WorkerSelector: map[string]string{"gpu": "true"}},
			hasCoordinator: false,
			defaultMode:    config.ExecutionModeDistributed,
			want:           false,
		},
		{
			name:           "workerSelector present, dispatch",
			dag:            &core.DAG{WorkerSelector: map[string]string{"gpu": "true"}},
			hasCoordinator: true,
			defaultMode:    config.ExecutionModeLocal,
			want:           true,
		},
		{
			name:           "defaultMode distributed, dispatch",
			dag:            &core.DAG{},
			hasCoordinator: true,
			defaultMode:    config.ExecutionModeDistributed,
			want:           true,
		},
		{
			name:           "defaultMode local, no workerSelector, local",
			dag:            &core.DAG{},
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
