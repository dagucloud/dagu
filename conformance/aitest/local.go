// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aitest

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// LocalProject runs `dagu start` with no coordinator, on the data directory of a
// server, with the fake claude of the shared fixtures.
type LocalProject struct {
	// Runner is the runner of the project.
	Runner *harness.Runner
	// Home is the data directory of the runs. It is the data directory of a server.
	Home string
	// Env is the environment of a local run.
	Env []string

	dir string
}

// NewLocalProject installs the shared fixtures in the project of the runner and
// returns a project that runs on the data directory home.
func NewLocalProject(t *testing.T, runner *harness.Runner, home string) *LocalProject {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake CLI of the fixtures is a POSIX script")
	}
	InstallFixtures(t, runner)
	dir := t.TempDir()
	// The local process has no restricted user, so its untrusted steps need isolation: off.
	harness.WriteIsolationOff(t, home)
	return &LocalProject{
		Runner: runner,
		Home:   home,
		dir:    dir,
		Env: []string{
			"PATH=" + runner.ProjectPath("bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
			"DAGU_HOME=" + home,
			"DAGU_COORDINATOR_ENABLED=false",
			"AITEST_DIR=" + dir,
		},
	}
}

// Scenario tells the fake CLI what to do, and clears the findings of the last run.
func (p *LocalProject) Scenario(t *testing.T, name string) {
	t.Helper()
	entries, err := os.ReadDir(p.dir)
	require.NoError(t, err)
	for _, entry := range entries {
		if entry.Name() != "ran" {
			require.NoError(t, os.RemoveAll(filepath.Join(p.dir, entry.Name())))
		}
	}
	require.NoError(t, os.WriteFile(filepath.Join(p.dir, "scenario"), []byte(name), 0o600))
}

// Start runs `dagu start` on a DAG file of the project.
func (p *LocalProject) Start(dagFile string) *harness.Result {
	return p.Runner.RunWithEnv(p.Env, "start", dagFile)
}

// Read returns a file that the fake CLI wrote in its folder.
func (p *LocalProject) Read(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(p.dir, name)) // #nosec G304 -- the path is in the folder of the test.
	require.NoError(t, err, "the CLI wrote no file %s", name)
	return string(data)
}

// RanCount returns how many times the fake CLI started.
func (p *LocalProject) RanCount() int {
	data, err := os.ReadFile(filepath.Join(p.dir, "ran")) // #nosec G304 -- the path is in the folder of the test.
	if err != nil {
		return 0
	}
	return strings.Count(string(data), "ran")
}

// Fingerprint is the fingerprint of a key: the first 12 hexadecimal digits of its
// SHA-256 (Spec 093, "The key").
func Fingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:12]
}
