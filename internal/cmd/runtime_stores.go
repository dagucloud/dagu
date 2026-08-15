// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/build"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	filematerialization "github.com/dagucloud/dagu/v2/internal/persis/file/materialization"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/secret"
)

type executionStores struct {
	SecretStore          secret.Store
	ProfileStore         profile.Store
	MaterializationStore build.MaterializationStore
}

func newExecutionStores(ctx context.Context, cfg *config.Config, backend persis.Backend) executionStores {
	return executionStores{
		SecretStore:          file.NewSecretStore(ctx, cfg, backend.Collection(persis.CollectionSecrets)),
		ProfileStore:         file.NewProfileStore(ctx, cfg, backend.Collection(persis.CollectionProfiles)),
		MaterializationStore: filematerialization.New(filepath.Join(cfg.Paths.DataDir, "materializations")),
	}
}
