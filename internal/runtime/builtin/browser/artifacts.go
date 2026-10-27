// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
)

const (
	artifactsSubdir = "browser"
	downloadsSubdir = "downloads"
	downloadDirMode = 0o755
)

// downloadsDir returns where the browser saves downloads, or "" when
// artifact storage is disabled.
func downloadsDir(store *agentstep.ArtifactStore) (string, error) {
	if !store.Enabled() {
		return "", nil
	}
	dir := filepath.Join(store.Dir(), downloadsSubdir)
	if err := os.MkdirAll(dir, downloadDirMode); err != nil {
		return "", fmt.Errorf("create downloads directory: %w", err)
	}
	return dir, nil
}

// downloadPath returns where a finished download is stored, relative to the
// run artifacts directory.
func downloadPath(store *agentstep.ArtifactStore, name string) string {
	return store.RelPath(downloadsSubdir, name)
}
