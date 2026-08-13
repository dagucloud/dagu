// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
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
			err := registry.ValidateExecutorConfig("docker", tt.config)
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
		input    ir.Container
		wantTest []string
		wantNil  bool
	}{
		{
			name: "with CMD healthcheck",
			input: ir.Container{
				Image: "postgres:alpine",
				Healthcheck: &ir.Healthcheck{
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
			input: ir.Container{
				Image: "mysql:8",
				Healthcheck: &ir.Healthcheck{
					Test:     []string{"CMD-SHELL", "mysqladmin ping -h localhost"},
					Interval: 2 * time.Second,
					Retries:  3,
				},
			},
			wantTest: []string{"CMD-SHELL", "mysqladmin ping -h localhost"},
		},
		{
			name: "without healthcheck",
			input: ir.Container{
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
	input := ir.Container{
		Image: "postgres:alpine",
		Healthcheck: &ir.Healthcheck{
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
	limits, err := ir.NewResourceLimits("1500m", "512Mi")
	require.NoError(t, err)

	cfg, err := LoadConfig("", ir.Container{Image: "alpine"}, nil)
	require.NoError(t, err)

	ApplyResourceLimits(cfg.Host, limits)

	require.NotNil(t, cfg.Host)
	require.Equal(t, int64(1_500_000_000), cfg.Host.Resources.NanoCPUs)
	require.Equal(t, int64(512*1024*1024), cfg.Host.Resources.Memory)
}

func TestLoadConfigFromMap_HostEmbeddedResources(t *testing.T) {
	// container.HostConfig EMBEDS container.Resources (Memory, CPUShares,
	// NanoCPUs, PidsLimit, Devices, …). The docs show the flat form —
	// host: {Memory: 536870912} — which requires DecoderConfig.Squash:
	// without it, every Resources field silently decoded to its zero value
	// while sibling direct fields (NetworkMode, SecurityOpt) worked, so
	// containers ran WITHOUT their configured resource limits.
	cfg, err := LoadConfigFromMap(map[string]any{
		"image": "alpine",
		"host": map[string]any{
			"NetworkMode": "bridge",
			"SecurityOpt": []string{"seccomp=unconfined"},
			"Memory":      536870912,
			"CPUShares":   512,
			"PidsLimit":   128,
			"Devices": []map[string]any{{
				"PathOnHost":        "/dev/fuse",
				"PathInContainer":   "/dev/fuse",
				"CgroupPermissions": "rwm",
			}},
		},
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, cfg.Host)
	require.Equal(t, int64(536870912), cfg.Host.Memory)
	require.Equal(t, int64(512), cfg.Host.CPUShares)
	require.NotNil(t, cfg.Host.PidsLimit)
	require.Equal(t, int64(128), *cfg.Host.PidsLimit)
	require.Len(t, cfg.Host.Devices, 1)
	require.Equal(t, "/dev/fuse", cfg.Host.Devices[0].PathOnHost)
	// direct HostConfig fields keep working alongside the squash
	require.Equal(t, "bridge", string(cfg.Host.NetworkMode))
	require.Equal(t, []string{"seccomp=unconfined"}, cfg.Host.SecurityOpt)
}
