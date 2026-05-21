// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package process

import (
	"context"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/coordinator"
)

// NewCoordinatorClient creates a coordinator client for a command process role.
func NewCoordinatorClient(ctx context.Context, cfg *config.Config, registry exec.ServiceRegistry) coordinator.Client {
	if !cfg.Coordinator.Enabled {
		return nil
	}

	coordinatorCliCfg := coordinator.DefaultConfig()
	coordinatorCliCfg.CAFile = cfg.Core.Peer.ClientCaFile
	coordinatorCliCfg.CertFile = cfg.Core.Peer.CertFile
	coordinatorCliCfg.KeyFile = cfg.Core.Peer.KeyFile
	coordinatorCliCfg.SkipTLSVerify = cfg.Core.Peer.SkipTLSVerify
	coordinatorCliCfg.Insecure = cfg.Core.Peer.Insecure

	if cfg.Core.Peer.MaxRetries > 0 {
		coordinatorCliCfg.MaxRetries = cfg.Core.Peer.MaxRetries
	}
	if cfg.Core.Peer.RetryInterval > 0 {
		coordinatorCliCfg.RetryInterval = cfg.Core.Peer.RetryInterval
	}

	if err := coordinatorCliCfg.Validate(); err != nil {
		logger.Error(ctx, "Invalid coordinator client configuration", tag.Error(err))
		return nil
	}
	return coordinator.New(registry, coordinatorCliCfg)
}
