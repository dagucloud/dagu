// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"context"
	"testing"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
)

func TestDockerExecutorCommandResolution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		step            core.Step
		wantShellConfig bool
	}{
		{
			name: "StepContainerShell",
			step: core.Step{
				ExecutorConfig: core.ExecutorConfig{Type: "docker"},
				Container: &core.Container{
					Image: "alpine",
					Shell: []string{"/bin/sh", "-c"},
				},
			},
			wantShellConfig: true,
		},
		{
			name: "ExecutorConfigShell",
			step: core.Step{
				ExecutorConfig: core.ExecutorConfig{
					Type:   "docker",
					Config: map[string]any{"image": "alpine", "shell": "/bin/bash"},
				},
			},
			wantShellConfig: true,
		},
		{
			name: "NoShell",
			step: core.Step{
				ExecutorConfig: core.ExecutorConfig{Type: "docker"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			command := tt.step.CommandResolution(context.Background())
			assert.Equal(t, cmnvalue.CommandTargetDocker, command.Target)
			assert.Equal(t, tt.wantShellConfig, command.ShellConfigured)
		})
	}
}
