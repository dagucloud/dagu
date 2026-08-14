// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package testutil

import (
	"github.com/dagucloud/dagu/v2/internal/persis"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
)

// NewFileDAGRunRepository constructs a file-backed repository for tests.
func NewFileDAGRunRepository(
	baseDir string,
	options persis.DAGRunRepositoryOptions,
	storeOptions ...filedagrun.StoreOption,
) *persis.DAGRunRepository {
	return persis.NewDAGRunRepository(
		filedagrun.NewStore(baseDir, storeOptions...),
		filedagrun.NewWorkDirStore(baseDir),
		options,
	)
}
