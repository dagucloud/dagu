// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"fmt"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core/exec"
)

// NewRuntimeDispatcher creates a coordinator-backed dispatcher for runtime DAG execution.
func NewRuntimeDispatcher(registry exec.ServiceRegistry, peerConfig config.Peer) (exec.Dispatcher, error) {
	if registry == nil {
		return nil, nil
	}

	cfg := DefaultConfig()
	cfg.MaxRetries = 50
	cfg.CAFile = peerConfig.ClientCaFile
	cfg.CertFile = peerConfig.CertFile
	cfg.KeyFile = peerConfig.KeyFile
	cfg.SkipTLSVerify = peerConfig.SkipTLSVerify
	cfg.Insecure = peerConfig.Insecure
	if peerConfig.MaxRetries > 0 {
		cfg.MaxRetries = peerConfig.MaxRetries
	}
	if peerConfig.RetryInterval > 0 {
		cfg.RetryInterval = peerConfig.RetryInterval
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate runtime dispatcher config: %w", err)
	}
	return New(registry, cfg), nil
}
