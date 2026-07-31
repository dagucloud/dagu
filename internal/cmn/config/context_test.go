// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config_test

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/stretchr/testify/assert"
)

func TestWithConfigAndGetConfig(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cfg := &config.Config{
		Server: config.Server{
			Host: "localhost",
			Port: 8080,
		},
	}

	// Store config in context
	ctx = config.WithConfig(ctx, cfg)

	// Retrieve config from context
	retrieved := config.GetConfig(ctx)

	if retrieved != cfg {
		t.Errorf("expected config to be %v, got %v", cfg, retrieved)
	}
	if retrieved.Server.Host != "localhost" {
		t.Errorf("expected Host to be 'localhost', got %s", retrieved.Server.Host)
	}
	if retrieved.Server.Port != 8080 {
		t.Errorf("expected Port to be 8080, got %d", retrieved.Server.Port)
	}
}

func TestGetConfig_NoConfigInContext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Get config from context without setting it
	cfg := config.GetConfig(ctx)

	if cfg == nil {
		t.Error("expected non-nil config, got nil")
	}
	// Should return empty config with zero values
	if cfg.Server.Host != "" || cfg.Server.Port != 0 {
		t.Errorf("expected empty config, got Server: %+v", cfg.Server)
	}
}

func TestConfigFileUsed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	assert.Equal(t, "", config.ConfigFileUsed(ctx))
	ctx = config.WithConfig(context.Background(), &config.Config{
		Paths: config.PathsConfig{ConfigFileUsed: "/path/to/config.yaml"},
	})
	assert.Equal(t, "/path/to/config.yaml", config.ConfigFileUsed(ctx))
}

func TestBaseEnvVars(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	assert.Empty(t, config.GetBaseEnv(ctx), 0)
	baseEnv := config.NewBaseEnv([]string{"A=1", "B=2"})
	ctx = config.WithConfig(context.Background(), &config.Config{
		Core: config.Core{BaseEnv: baseEnv},
	})
	assert.Equal(t, []string{"A=1", "B=2"}, config.GetBaseEnv(ctx).AsSlice())
}
