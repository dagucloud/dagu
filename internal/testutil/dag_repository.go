// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package testutil

import (
	"github.com/dagucloud/dagu/v2/internal/persis"
	filedag "github.com/dagucloud/dagu/v2/internal/persis/file/dag"
)

// NewFileDAGRepository constructs a file-backed DAG repository for tests.
func NewFileDAGRepository(baseDir string, options ...filedag.Option) *persis.DAGRepository {
	return persis.NewDAGRepository(filedag.NewStore(baseDir, options...), persis.DAGRepositoryOptions{})
}
