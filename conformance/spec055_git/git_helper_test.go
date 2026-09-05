// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec055_git_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// stdoutLogPattern matches the per-step captured-stdout log path dagu start
// prints in its tree render, e.g. "└─stdout: /path/to/step.<ts>.<run>.out".
var stdoutLogPattern = regexp.MustCompile(`stdout: (.+)`)

// lastStepStdout reads the exact bytes the last step in the run wrote to
// stdout, by locating that step's captured-output log file from dagu
// start's own tree render (the last "stdout:" line in a multi-step run)
// and reading it directly, since the tree render re-wraps long lines with
// its own indentation, which would corrupt a strict JSON parse.
func lastStepStdout(t *testing.T, daguStartOutput string) string {
	t.Helper()

	matches := stdoutLogPattern.FindAllStringSubmatch(daguStartOutput, -1)
	require.NotEmptyf(t, matches, "expected a stdout log path in output:\n%s", daguStartOutput)
	path := strings.TrimSpace(matches[len(matches)-1][1])
	data, err := os.ReadFile(path) // #nosec G304 -- path comes from the harness's own trusted output.
	require.NoError(t, err)
	return string(data)
}

// originRepo describes a local git repository this test suite builds from
// scratch (via the real git binary) to act as a checkout source, so tests
// run hermetically against a real repository without any network access.
type originRepo struct {
	Path        string
	FirstCommit string // tagged "first"; file.txt contains "v1"
	HeadCommit  string // main's HEAD; file.txt contains "v2"
}

// setupOriginRepo creates a two-commit git repository in a fresh temp
// directory: the first commit (tagged "first") writes file.txt="v1", the
// second (left as HEAD) writes file.txt="v2".
func setupOriginRepo(t *testing.T) originRepo {
	t.Helper()

	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "conformance@example.com")
	runGit(t, dir, "config", "user.name", "Conformance Test")

	writeAndCommit(t, dir, "v1", "commit1")
	first := runGit(t, dir, "rev-parse", "HEAD")
	runGit(t, dir, "tag", "first")

	writeAndCommit(t, dir, "v2", "commit2")
	head := runGit(t, dir, "rev-parse", "HEAD")

	return originRepo{Path: dir, FirstCommit: first, HeadCommit: head}
}

// addCommit appends a third commit to repo, simulating an upstream change
// that a later checkout should fetch, and returns the new commit hash.
func (r originRepo) addCommit(t *testing.T) string {
	t.Helper()

	writeAndCommit(t, r.Path, "v3", "commit3")
	return runGit(t, r.Path, "rev-parse", "HEAD")
}

func writeAndCommit(t *testing.T, dir, content, message string) {
	t.Helper()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte(content), 0o600))
	runGit(t, dir, "add", "file.txt")
	runGit(t, dir, "commit", "-q", "-m", message)
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...) // #nosec G204 -- fixed args/dir in test setup, not user input.
	cmd.Dir = dir
	out, err := cmd.Output()
	require.NoErrorf(t, err, "git %s: %v", strings.Join(args, " "), err)
	return strings.TrimSpace(string(out))
}
