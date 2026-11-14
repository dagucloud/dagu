// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workpath

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Rooted and drive-relative Windows paths are not relative to a directory, so
// they are not joined under the working directory.
func TestResolveWindowsRootedPaths(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	for _, path := range []string{`\data\in.json`, `C:data\in.json`, `\\server\share\in.json`} {
		assert.Equal(t, path, Resolve(workDir, path), path)
	}
}
