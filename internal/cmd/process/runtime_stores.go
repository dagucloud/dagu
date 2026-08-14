// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package process

import (
	"context"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/build"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	filematerialization "github.com/dagucloud/dagu/v2/internal/persis/file/materialization"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/secret"
)

// RuntimeStores contains runtime stores used by DAG execution.
type RuntimeStores struct {
	SecretStore          secret.Store
	ProfileStore         profile.Store
	MaterializationStore build.MaterializationStore
}

// NewFileRuntimeStores creates the file-backed stores used by workflow execution.
func NewFileRuntimeStores(ctx context.Context, cfg *config.Config) RuntimeStores {
	return RuntimeStores{
		SecretStore:          file.NewSecretStore(ctx, cfg),
		ProfileStore:         file.NewProfileStore(ctx, cfg),
		MaterializationStore: filematerialization.New(filepath.Join(cfg.Paths.DataDir, "materializations")),
	}
}
