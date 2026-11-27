// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/stretchr/testify/assert"
)

func TestCoordinatorJoinReadiness(t *testing.T) {
	envSource := config.ValueSource{Kind: config.SourceKindEnv, Name: "DAGU_COORDINATOR_HOST"}
	newConfig := func(used string) *config.Config {
		cfg := &config.Config{Sources: map[string]config.ValueSource{"coordinator.host": envSource}}
		cfg.Coordinator.Host = "0.0.0.0"
		cfg.Coordinator.Port = 50055
		cfg.Coordinator.Advertise = "dagu.example.com"
		cfg.Paths.ConfigFileUsed = used
		cfg.Paths.DefaultConfigFile = "/home/dagu/.config/dagu/config.yaml"
		return cfg
	}

	t.Run("config file read", func(t *testing.T) {
		got := coordinatorJoinReadiness(newConfig("/etc/dagu/config.yaml"))
		assert.Equal(t, "0.0.0.0", got.ListenHost)
		assert.Equal(t, 50055, got.Port)
		assert.Equal(t, "/etc/dagu/config.yaml", got.ConfigFile)
		assert.Equal(t, envSource, got.HostSource)
		assert.Equal(t, "dagu.example.com", got.Advertise)
	})

	t.Run("no config file", func(t *testing.T) {
		got := coordinatorJoinReadiness(newConfig(""))
		assert.Equal(t, "/home/dagu/.config/dagu/config.yaml", got.ConfigFile)
	})
}
