// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/require"
)

func TestDockerConfigSchema(t *testing.T) {
	tests := []struct {
		name    string
		config  map[string]any
		wantErr bool
	}{
		{
			name:    "image only",
			config:  map[string]any{"image": "alpine"},
			wantErr: false,
		},
		{
			name:    "container_name only",
			config:  map[string]any{"container_name": "my-container"},
			wantErr: false,
		},
		{
			name:    "both image and container_name",
			config:  map[string]any{"image": "alpine", "container_name": "my-container"},
			wantErr: false,
		},
		{
			name:    "image with exec requires container_name",
			config:  map[string]any{"image": "alpine", "exec": map[string]any{"user": "root"}},
			wantErr: true,
		},
		{
			name:    "container_name with exec",
			config:  map[string]any{"container_name": "my-container", "exec": map[string]any{"user": "root"}},
			wantErr: false,
		},
		{
			name:    "empty config",
			config:  map[string]any{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := core.ValidateExecutorConfig("docker", tt.config)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestDockerConfig_Healthcheck(t *testing.T) {
	tests := []struct {
		name     string
		input    core.Container
		wantTest []string
		wantNil  bool
	}{
		{
			name: "with CMD healthcheck",
			input: core.Container{
				Image: "postgres:alpine",
				Healthcheck: &core.Healthcheck{
					Test:        []string{"CMD", "pg_isready"},
					Interval:    5 * time.Second,
					Timeout:     3 * time.Second,
					StartPeriod: 10 * time.Second,
					Retries:     5,
				},
			},
			wantTest: []string{"CMD", "pg_isready"},
		},
		{
			name: "with CMD-SHELL healthcheck",
			input: core.Container{
				Image: "mysql:8",
				Healthcheck: &core.Healthcheck{
					Test:     []string{"CMD-SHELL", "mysqladmin ping -h localhost"},
					Interval: 2 * time.Second,
					Retries:  3,
				},
			},
			wantTest: []string{"CMD-SHELL", "mysqladmin ping -h localhost"},
		},
		{
			name: "without healthcheck",
			input: core.Container{
				Image: "alpine:3",
			},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := LoadConfig("", tt.input, nil)
			require.NoError(t, err)

			if tt.wantNil {
				require.Nil(t, cfg.Container.Healthcheck)
			} else {
				require.NotNil(t, cfg.Container.Healthcheck)
				require.Equal(t, tt.wantTest, cfg.Container.Healthcheck.Test)
			}
		})
	}
}

func TestDockerConfig_Healthcheck_DurationsPreserved(t *testing.T) {
	input := core.Container{
		Image: "postgres:alpine",
		Healthcheck: &core.Healthcheck{
			Test:        []string{"CMD", "pg_isready"},
			Interval:    5 * time.Second,
			Timeout:     3 * time.Second,
			StartPeriod: 10 * time.Second,
			Retries:     5,
		},
	}

	cfg, err := LoadConfig("", input, nil)
	require.NoError(t, err)
	require.NotNil(t, cfg.Container.Healthcheck)

	require.Equal(t, 5*time.Second, cfg.Container.Healthcheck.Interval)
	require.Equal(t, 3*time.Second, cfg.Container.Healthcheck.Timeout)
	require.Equal(t, 10*time.Second, cfg.Container.Healthcheck.StartPeriod)
	require.Equal(t, 5, cfg.Container.Healthcheck.Retries)
}

func TestApplyResourceLimits(t *testing.T) {
	limits, err := core.NewResourceLimits("1500m", "512Mi")
	require.NoError(t, err)

	cfg, err := LoadConfig("", core.Container{Image: "alpine"}, nil)
	require.NoError(t, err)

	ApplyResourceLimits(cfg.Host, limits)

	require.NotNil(t, cfg.Host)
	require.Equal(t, int64(1_500_000_000), cfg.Host.Resources.NanoCPUs)
	require.Equal(t, int64(512*1024*1024), cfg.Host.Resources.Memory)
}
