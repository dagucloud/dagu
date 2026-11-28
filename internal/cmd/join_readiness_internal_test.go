// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCoordinatorJoinReadiness(t *testing.T) {
	envSource := config.ValueSource{Kind: config.SourceKindEnv, Name: "DAGU_COORDINATOR_HOST"}
	peerCA, err := coordinator.LoadOrCreatePeerCA(filepath.Join(t.TempDir(), "peer-ca.pem"))
	require.NoError(t, err)
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
		got := coordinatorJoinReadiness(newConfig("/etc/dagu/config.yaml"), peerCA)
		assert.Equal(t, "0.0.0.0", got.ListenHost)
		assert.Equal(t, 50055, got.Port)
		assert.Equal(t, "/etc/dagu/config.yaml", got.ConfigFile)
		assert.Equal(t, envSource, got.HostSource)
		assert.Equal(t, "dagu.example.com", got.Advertise)
		assert.True(t, got.BuiltinTLS)
		assert.Equal(t, coordinatorv1.BuiltinTLSReason_BUILTIN_TLS_REASON_UNSPECIFIED, got.BuiltinTLSReason)
	})

	t.Run("no config file", func(t *testing.T) {
		got := coordinatorJoinReadiness(newConfig(""), peerCA)
		assert.Equal(t, "/home/dagu/.config/dagu/config.yaml", got.ConfigFile)
	})

	t.Run("own certificates", func(t *testing.T) {
		cfg := newConfig("")
		cfg.Core.Peer.CertFile = "/etc/dagu/peer.crt"
		cfg.Core.Peer.KeyFile = "/etc/dagu/peer.key"
		got := coordinatorJoinReadiness(cfg, nil)
		assert.False(t, got.BuiltinTLS)
		assert.Equal(t, coordinatorv1.BuiltinTLSReason_BUILTIN_TLS_REASON_OWN_CERTIFICATES, got.BuiltinTLSReason)
	})

	// A coordinator that could not create or read its generated CA runs
	// without a peer CA and says why.
	t.Run("CA unavailable", func(t *testing.T) {
		got := coordinatorJoinReadiness(newConfig(""), nil)
		assert.False(t, got.BuiltinTLS)
		assert.Equal(t, coordinatorv1.BuiltinTLSReason_BUILTIN_TLS_REASON_CA_UNAVAILABLE, got.BuiltinTLSReason)
	})
}
