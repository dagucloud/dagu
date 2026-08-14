// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/persis"
	procstore "github.com/dagucloud/dagu/v2/internal/persis/file/proc"
)

// NewProcRepository connects the released .proc file layout to the shared repository.
func NewProcRepository(cfg *config.Config, opts ...procstore.StoreOption) *persis.ProcRepository {
	storeOpts := []procstore.StoreOption{
		procstore.WithStaleThreshold(cfg.Proc.StaleThreshold),
		procstore.WithHeartbeatInterval(cfg.Proc.HeartbeatInterval),
	}
	storeOpts = append(storeOpts, opts...)
	return persis.NewProcRepository(procstore.New(cfg.Paths.ProcDir, storeOpts...))
}
