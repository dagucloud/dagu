// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package xlsx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

// resolvePath resolves a with.path the way the file actions do: absolute
// and ~ paths as written, relative paths against the working directory.
func resolvePath(workDir, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("%w: path must not be empty", errConfig)
	}
	if filepath.IsAbs(raw) || strings.HasPrefix(raw, "~") {
		return fileutil.ResolvePath(raw)
	}
	if strings.TrimSpace(workDir) == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve working directory: %w", err)
		}
	}
	return filepath.Clean(filepath.Join(workDir, raw)), nil
}
