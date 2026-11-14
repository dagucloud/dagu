// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cli_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// TestConfigShowsResolvedPaths proves `dagu config` resolves DAGU_HOME and
// DAGU_DAGS_DIR into the actual paths in effect, not just that it prints
// labeled lines: it asserts the DAGs-directory line names the project
// directory itself, and the data-directory line names DAGU_HOME's own
// "data" subdirectory, so a build that resolved either to the wrong path
// (or to another DAG-run's home) would fail this test.
func TestConfigShowsResolvedPaths(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	home := filepath.Join(t.TempDir(), "dagu")
	env := []string{
		"DAGU_HOME=" + home,
		"DAGU_DAGS_DIR=.",
	}

	result := dagu.RunWithEnv(env, "config")
	result.ExpectExitCode(0)

	requireLineContains(t, result.Stdout(), "DAGs directory:", dagu.ProjectPath("."))
	requireLineContains(t, result.Stdout(), "Data directory:", filepath.Join(home, "data"))
}

// TestConfigLoadsNamedTimeZone proves the tz setting accepts an IANA name on a
// host without a time zone database, such as Windows without Go installed.
// Pointing GOROOT at an empty directory removes the fallback to the build
// machine's Go installation.
func TestConfigLoadsNamedTimeZone(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := []string{"DAGU_TZ=Asia/Tokyo", "GOROOT=" + t.TempDir(), "ZONEINFO="}

	dagu.RunWithEnv(env, "config").ExpectExitCode(0)
}

// TestConfigValuesReportsSources proves `dagu config --values` names the
// source of each value and never prints a secret: the port comes from the
// file, the host from the environment, the token secret is masked, and
// auth.mode reports the builtin mode Dagu falls back to when none is set.
func TestConfigValuesReportsSources(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	dagu.WriteFile("config.yaml", "port: 9090\n")
	env := []string{
		"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu"),
		"DAGU_HOST=0.0.0.0",
		"DAGU_AUTH_TOKEN_SECRET=do-not-print",
		// The harness sets auth.mode to none; clear it to see the fallback.
		"DAGU_AUTH_MODE=",
	}

	result := dagu.RunWithEnv(env, "config", "--config", dagu.ProjectPath("config.yaml"), "--values", "--format", "json")
	result.ExpectExitCode(0)

	if strings.Contains(result.Stdout(), "do-not-print") {
		t.Fatalf("secret printed; output:\n%s", result.Stdout())
	}
	var settings []struct {
		Key    string `json:"key"`
		Value  string `json:"value"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal([]byte(result.Stdout()), &settings); err != nil {
		t.Fatalf("decoding output: %v; output:\n%s", err, result.Stdout())
	}
	got := make(map[string][2]string, len(settings))
	for _, s := range settings {
		got[s.Key] = [2]string{s.Value, s.Source}
	}

	want := map[string][2]string{
		"port":                      {"9090", "file"},
		"host":                      {"0.0.0.0", "env:DAGU_HOST"},
		"auth.builtin.token.secret": {"********", "env:DAGU_AUTH_TOKEN_SECRET"},
		"auth.mode":                 {"builtin", "default"},
	}
	for key, w := range want {
		if got[key] != w {
			t.Errorf("%s = %q, want %q", key, got[key], w)
		}
	}
}

// requireLineContains fails the test unless some line starts with label and
// its remainder, trimmed, equals value exactly. Matching the resolved path
// by exact equality (rather than substring) rejects a path that merely
// shares value as a prefix, such as a sibling directory named value+"-old".
func requireLineContains(t *testing.T, output, label, value string) {
	t.Helper()

	for line := range strings.SplitSeq(output, "\n") {
		rest, ok := strings.CutPrefix(line, label)
		if ok && strings.TrimSpace(rest) == value {
			return
		}
	}
	t.Fatalf("no line labeled %q has value %q; output:\n%s", label, value, output)
}
