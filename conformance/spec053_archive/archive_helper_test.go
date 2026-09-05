// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec053_archive_test

import (
	"archive/zip"
	"bytes"
	"os"
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

// evilZipBytes builds (in memory, using Go's own archive/zip rather than
// dagu's archive.create, which sanitizes entry names) a zip archive
// containing a single entry named "../escape.txt", to prove archive.extract
// rejects an entry that would escape its destination directory.
func evilZipBytes(t *testing.T) string {
	t.Helper()

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	entry, err := w.Create("../escape.txt")
	require.NoError(t, err)
	_, err = entry.Write([]byte("evil content"))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return buf.String()
}
