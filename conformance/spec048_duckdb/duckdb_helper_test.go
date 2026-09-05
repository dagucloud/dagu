// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec048_duckdb_test

import (
	"net"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// requireGitHubAccess skips the test when github.com is not reachable, so
// the one test that resolves the real dagucloud/duckdb official action
// (over a real network clone, plus a real tool download) degrades
// gracefully in an offline or network-restricted environment instead of
// failing.
func requireGitHubAccess(t *testing.T) {
	t.Helper()

	conn, err := net.DialTimeout("tcp", "github.com:443", 3*time.Second)
	if err != nil {
		t.Skipf("Skipping network-backed conformance test: github.com unreachable: %v", err)
	}
	_ = conn.Close()
}

// stdoutLogPattern matches the per-step captured-stdout log path dagu start
// prints in its tree render, e.g. "└─stdout: /path/to/step.<ts>.<run>.out".
var stdoutLogPattern = regexp.MustCompile(`stdout: (.+)`)

// stepStdout reads the exact bytes a step wrote to stdout, by locating its
// captured-output log file from dagu start's own tree render and reading it
// directly. The tree render re-wraps long lines with its own indentation,
// which would corrupt a strict JSON parse, so assertions on precise action
// output read this file instead of result.Stdout().
func stepStdout(t *testing.T, daguStartOutput string) string {
	t.Helper()

	match := stdoutLogPattern.FindStringSubmatch(daguStartOutput)
	if match == nil {
		t.Fatalf("expected a stdout log path in output:\n%s", daguStartOutput)
	}
	path := strings.TrimSpace(match[1])
	data, err := os.ReadFile(path) // #nosec G304 -- path comes from the harness's own trusted output.
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}
