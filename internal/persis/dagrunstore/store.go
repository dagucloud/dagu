// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package dagrunstore keeps the narrow DAG-run store constructor while the
// broader control-plane store owns backend selection.
package dagrunstore

import (
	"context"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/controlplanestore"
)

type Options = controlplanestore.Options
type Option = controlplanestore.Option
type Role = controlplanestore.Role

const (
	RoleServer    = controlplanestore.RoleServer
	RoleScheduler = controlplanestore.RoleScheduler
	RoleAgent     = controlplanestore.RoleAgent
)

var (
	WithHistoryFileCache  = controlplanestore.WithHistoryFileCache
	WithLatestStatusToday = controlplanestore.WithLatestStatusToday
	WithLocation          = controlplanestore.WithLocation
	WithRole              = controlplanestore.WithRole
)

// New creates the configured DAG-run store.
func New(ctx context.Context, cfg *config.Config, opts ...Option) (exec.DAGRunStore, error) {
	return controlplanestore.NewDAGRunStore(ctx, cfg, opts...)
}
