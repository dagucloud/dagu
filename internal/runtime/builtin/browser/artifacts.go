// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

// artifactKey names the step's artifacts directory. Inside a foreach body each
// item gets its own directory, so items running side by side or one after
// another do not overwrite each other's screenshots, downloads and write gate
// report. The path holds every enclosing item, so nested foreach steps stay
// apart too.
func artifactKey(stepKey string, foreachPath []int) string {
	if len(foreachPath) == 0 {
		return stepKey
	}
	parts := make([]string, len(foreachPath))
	for i, index := range foreachPath {
		parts[i] = strconv.Itoa(index)
	}
	return stepKey + "-item-" + strings.Join(parts, "-")
}
