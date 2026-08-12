// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package testutil

import (
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
)

// NewFileDAGRunRepository constructs a file-backed repository for tests.
func NewFileDAGRunRepository(
	baseDir string,
	options dagrun.RepositoryOptions,
	storeOptions ...filedagrun.StoreOption,
) *dagrun.Repository {
	return dagrun.NewRepository(
		filedagrun.NewStore(baseDir, storeOptions...),
		filedagrun.NewDAGRunWorkspaceStore(baseDir),
		options,
	)
}
