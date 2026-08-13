// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagsettings

import "context"

// BaseConfigStore provides access to the base DAG configuration inherited by all DAGs.
// Implementations must be safe for concurrent use.
type BaseConfigStore interface {
	// GetSpec returns the raw YAML content, or an empty string when no configuration exists.
	GetSpec(ctx context.Context) (string, error)
	// UpdateSpec writes the raw YAML content.
	UpdateSpec(ctx context.Context, spec []byte) error
}

// BaseConfigProvider returns the base configuration store for a workspace.
type BaseConfigProvider func(workspaceName string) (BaseConfigStore, error)

type Store interface {
	Get(ctx context.Context, dagName string) (*Settings, error)
	Upsert(ctx context.Context, settings *Settings) error
	Delete(ctx context.Context, dagName string) error
}
