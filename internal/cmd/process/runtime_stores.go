// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package process

import (
	"context"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/profile"
	"github.com/dagucloud/dagu/internal/secret"
)

// RuntimeStores contains runtime stores used by DAG execution.
type RuntimeStores struct {
	SecretStore  secret.Store
	ProfileStore profile.Store
}

// NewRuntimeStores creates the runtime store bundle for a command process role.
func NewRuntimeStores(ctx context.Context, cfg *config.Config) RuntimeStores {
	return NewRuntimeStoresForConfig(ctx, cfg)
}

// NewRuntimeStoresForConfig creates the stores used by worker/runtime execution.
func NewRuntimeStoresForConfig(ctx context.Context, cfg *config.Config) RuntimeStores {
	return RuntimeStores{
		SecretStore:  file.NewSecretStore(ctx, cfg),
		ProfileStore: file.NewProfileStore(ctx, cfg),
	}
}
