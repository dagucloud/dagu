// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"github.com/dagucloud/dagu/internal/cmn/config"
	fileproc "github.com/dagucloud/dagu/internal/persis/file/proc"
	"github.com/dagucloud/dagu/internal/persis/store"
)

// NewProcStore wires the collection-backed proc store with file sidecar compatibility.
func NewProcStore(cfg *config.Config, opts ...store.ProcStoreOption) *store.ProcStore {
	storeOpts := []store.ProcStoreOption{
		store.WithProcStaleThreshold(cfg.Proc.StaleThreshold),
		store.WithProcHeartbeatInterval(cfg.Proc.HeartbeatInterval),
		store.WithProcHeartbeatSyncInterval(cfg.Proc.HeartbeatSyncInterval),
		store.WithProcLegacyStore(fileproc.NewLegacyStore(cfg.Paths.ProcDir)),
	}
	storeOpts = append(storeOpts, opts...)
	return store.NewProcStore(NewCollection(cfg.Paths.ProcDir), storeOpts...)
}
