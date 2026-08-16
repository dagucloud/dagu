// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/build"
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

// runtimeStores creates the runtime store bundle for this command context.
func (c *Context) runtimeStores() executionStores {
	return executionStores{
		SecretStore:          file.NewSecretStore(c.Context, c.Config, c.backend.Collection(persis.CollectionSecrets)),
		ProfileStore:         file.NewProfileStore(c.Context, c.Config, c.backend.Collection(persis.CollectionProfiles)),
		MaterializationStore: filematerialization.New(filepath.Join(c.Config.Paths.DataDir, "materializations")),
	}
}
