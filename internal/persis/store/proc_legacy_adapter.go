// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"time"

	"github.com/dagucloud/dagu/internal/core/exec"
)

// ProcLegacyStore adapts file-based .proc sidecar compatibility for ProcStore.
type ProcLegacyStore interface {
	SetStaleThreshold(time.Duration)
	FilePath(groupName string, meta exec.ProcMeta, t time.Time) string
	Write(path string, heartbeatUnix int64, meta exec.ProcMeta) error
	Remove(path string) error
	ListEntries(groupName string) ([]exec.ProcEntry, error)
	ListAllEntries() ([]exec.ProcEntry, error)
	LatestHeartbeat(groupName string, dagRun exec.DAGRunRef) (*exec.ProcHeartbeat, error)
	RemoveIfStale(ctx context.Context, entry exec.ProcEntry) error
}

// WithProcLegacyStore enables transitional read/write compatibility through a
// backend-specific adapter.
func WithProcLegacyStore(legacy ProcLegacyStore) ProcStoreOption {
	return func(s *ProcStore) {
		s.legacy = legacy
	}
}
