// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	procstore "github.com/dagucloud/dagu/v2/internal/persis/file/proc"
	"github.com/dagucloud/dagu/v2/internal/proc"
)

// NewProcRepository creates a repository backed by the released .proc file
// layout under cfg.Paths.ProcDir.
func NewProcRepository(cfg *config.Config, opts ...procstore.StoreOption) *proc.Repository {
	storeOpts := []procstore.StoreOption{
		procstore.WithStaleThreshold(cfg.Proc.StaleThreshold),
		procstore.WithHeartbeatInterval(cfg.Proc.HeartbeatInterval),
	}
	storeOpts = append(storeOpts, opts...)
	return proc.NewRepository(procstore.New(cfg.Paths.ProcDir, storeOpts...))
}
