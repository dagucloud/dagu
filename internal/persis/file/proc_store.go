// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/persis/file/proc"
)

// NewProcStore wires the file-backed proc store without changing the released
// .proc file layout under cfg.Paths.ProcDir.
func NewProcStore(cfg *config.Config, opts ...proc.StoreOption) *proc.Store {
	storeOpts := []proc.StoreOption{
		proc.WithStaleThreshold(cfg.Proc.StaleThreshold),
		proc.WithHeartbeatInterval(cfg.Proc.HeartbeatInterval),
		proc.WithHeartbeatSyncInterval(cfg.Proc.HeartbeatSyncInterval),
	}
	storeOpts = append(storeOpts, opts...)
	return proc.New(cfg.Paths.ProcDir, storeOpts...)
}
