// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aitest

import (
	"embed"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// The shared fixtures are the fake claude CLI (bin/claude and bin/relay_cli.py) and the
// configuration files of the coordinator and of the workers of a stack.
//
//go:embed fixtures
var fixtures embed.FS

// RealKey is the key of the provider. Only the coordinator and the fake provider
// know it. It must reach the provider, and no other place.
const RealKey = "sk-ant-api03-REAL-KEY-0123456789abcdefghijklmnop"

// InstallFixtures writes the shared fixtures in the project of the runner. The files of
// bin/ are executable, because the fake CLI runs as the restricted user.
func InstallFixtures(t *testing.T, r *harness.Runner) {
	t.Helper()
	require.NoError(t, fs.WalkDir(fixtures, "fixtures", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(path, "fixtures"), "/")
		if rel == "" {
			return nil
		}
		dst := r.ProjectPath(rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o750) // #nosec G122 -- the folder is the project of the test.
		}
		data, readErr := fixtures.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		mode := os.FileMode(0o644)
		if strings.HasPrefix(rel, "bin/") {
			mode = 0o755
		}
		return os.WriteFile(dst, data, mode) // #nosec G306 G122 -- the restricted user must read and run the file.
	}))
}

// UnixNanoFile reads a time that the fake CLI wrote, in nanoseconds since the epoch.
func UnixNanoFile(t *testing.T, path string) time.Time {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- the path is in the shared folder of the test.
	require.NoError(t, err)
	nanos, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	require.NoError(t, err)
	return time.Unix(0, nanos)
}
