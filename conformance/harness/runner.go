// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Runner executes Dagu commands against an isolated conformance test project.
type Runner struct {
	t   *testing.T
	dir string
}

// Result captures a completed Dagu command invocation.
type Result struct {
	t        *testing.T
	exitCode int
	stdout   string
	stderr   string
}

// NewRunner creates an isolated project seeded with package-local testdata.
func NewRunner(t *testing.T) *Runner {
	t.Helper()

	r := &Runner{
		t:   t,
		dir: t.TempDir(),
	}
	if err := os.CopyFS(r.dir, os.DirFS("testdata")); err != nil {
		r.t.Fatalf("copying testdata: %v", err)
	}
	return r
}

// Run executes the configured Dagu binary inside the isolated project.
func (r *Runner) Run(args ...string) *Result {
	r.t.Helper()
	return r.run(nil, args...)
}

// RunWithEnv executes the configured Dagu binary with extra environment entries.
func (r *Runner) RunWithEnv(env []string, args ...string) *Result {
	r.t.Helper()
	return r.run(env, args...)
}

func (r *Runner) run(extraEnv []string, args ...string) *Result {
	r.t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	// Binary-level tests intentionally execute the configured Dagu binary.
	cmd := exec.CommandContext(ctx, daguBinary(r.t), args...) //nolint:gosec
	cmd.Dir = r.dir
	cmd.Env = appendEnv(append(isolatedEnv(r.t), "PWD="+r.dir), extraEnv...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() != nil {
		r.t.Fatalf(
			"dagu command timed out: dagu %s\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "),
			stdout.String(),
			stderr.String(),
		)
	}

	exitCode := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			r.t.Fatalf("running dagu %s: %v", strings.Join(args, " "), err)
		}
		exitCode = exitErr.ExitCode()
	}

	return &Result{
		t:        r.t,
		exitCode: exitCode,
		stdout:   stdout.String(),
		stderr:   stderr.String(),
	}
}

func appendEnv(env []string, extra ...string) []string {
	result := append([]string(nil), env...)
	for _, entry := range extra {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			result = removeEnvKey(result, key)
		}
		result = append(result, entry)
	}
	return result
}

func removeEnvKey(env []string, key string) []string {
	result := env[:0]
	for _, entry := range env {
		entryKey, _, ok := strings.Cut(entry, "=")
		if ok && sameEnvKey(entryKey, key) {
			continue
		}
		result = append(result, entry)
	}
	return result
}

func sameEnvKey(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// ExpectNoFile fails the test when name exists in the isolated project.
func (r *Runner) ExpectNoFile(name string) {
	r.t.Helper()

	path := r.projectPath(name)
	if _, err := os.Stat(path); err == nil {
		r.t.Fatalf("expected %s to be absent", name)
	} else if !os.IsNotExist(err) {
		r.t.Fatalf("checking %s: %v", name, err)
	}
}

// ExpectFileContent fails the test when name does not contain content.
func (r *Runner) ExpectFileContent(name string, content string) {
	r.t.Helper()

	path := r.projectPath(name)
	actual, err := os.ReadFile(path) // #nosec G304 -- projectPath confines test fixture paths to the temp project.
	if err != nil {
		r.t.Fatalf("reading %s: %v", name, err)
	}
	require.Equal(r.t, content, string(actual))
}

// ExpectFileContains fails the test when name lacks any required text.
func (r *Runner) ExpectFileContains(name string, parts ...string) {
	r.t.Helper()

	path := r.projectPath(name)
	actual, err := os.ReadFile(path) // #nosec G304 -- projectPath confines test fixture paths to the temp project.
	if err != nil {
		r.t.Fatalf("reading %s: %v", name, err)
	}
	for _, part := range parts {
		require.Contains(r.t, string(actual), part)
	}
}

// ExpectGlobFileContent fails unless exactly one project-local file matches pattern and contains content.
func (r *Runner) ExpectGlobFileContent(pattern string, content string) {
	r.t.Helper()

	matches, err := r.projectGlob(pattern)
	if err != nil {
		r.t.Fatalf("matching %s: %v", pattern, err)
	}
	if len(matches) != 1 {
		relMatches := make([]string, 0, len(matches))
		for _, match := range matches {
			rel, relErr := filepath.Rel(r.dir, match)
			if relErr != nil {
				relMatches = append(relMatches, match)
				continue
			}
			relMatches = append(relMatches, filepath.ToSlash(rel))
		}
		r.t.Fatalf("expected one match for %s, got %d: %s", pattern, len(matches), strings.Join(relMatches, ", "))
	}

	actual, err := os.ReadFile(matches[0]) // #nosec G304 -- projectGlob confines test fixture paths to the temp project.
	if err != nil {
		r.t.Fatalf("reading %s: %v", pattern, err)
	}
	require.Equal(r.t, content, string(actual))
}

// Mkdir creates a directory in the isolated project.
func (r *Runner) Mkdir(name string) {
	r.t.Helper()

	path := r.projectPath(name)
	if err := os.MkdirAll(path, 0o750); err != nil {
		r.t.Fatalf("creating %s: %v", name, err)
	}
}

// WriteFile writes a regular file in the isolated project.
func (r *Runner) WriteFile(name string, content string) {
	r.writeFile(name, content, 0o644)
}

// WriteExecutable writes an executable file in the isolated project.
func (r *Runner) WriteExecutable(name string, content string) {
	r.writeFile(name, content, 0o755)
}

func (r *Runner) writeFile(name string, content string, perm os.FileMode) {
	r.t.Helper()

	path := r.projectPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		r.t.Fatalf("creating parent for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		r.t.Fatalf("writing %s: %v", name, err)
	}
}

func (r *Runner) projectPath(name string) string {
	r.t.Helper()

	cleaned := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
		r.t.Fatalf("project path %q escapes test project", name)
	}
	return filepath.Join(r.dir, cleaned)
}

func (r *Runner) projectGlob(pattern string) ([]string, error) {
	r.t.Helper()

	cleaned := filepath.Clean(filepath.FromSlash(pattern))
	if filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
		r.t.Fatalf("project glob %q escapes test project", pattern)
	}
	return filepath.Glob(filepath.Join(r.dir, cleaned))
}

// ExpectExitCode fails the test when the command exit code differs.
func (r *Result) ExpectExitCode(code int) {
	r.t.Helper()

	require.Equal(r.t, code, r.exitCode, "stdout:\n%s\nstderr:\n%s", r.stdout, r.stderr)
}

// ExpectStdout fails the test when stdout differs.
func (r *Result) ExpectStdout(stdout string) {
	r.t.Helper()

	require.Equal(r.t, stdout, r.stdout)
}

// ExpectStderr fails the test when stderr differs.
func (r *Result) ExpectStderr(stderr string) {
	r.t.Helper()

	require.Equal(r.t, stderr, r.stderr)
}

// ExpectStderrContains fails the test when stderr lacks any required text.
func (r *Result) ExpectStderrContains(parts ...string) {
	r.t.Helper()

	for _, part := range parts {
		require.Contains(r.t, r.stderr, part)
	}
}

// ExpectStderrNotContains fails the test when stderr contains forbidden text.
func (r *Result) ExpectStderrNotContains(parts ...string) {
	r.t.Helper()

	for _, part := range parts {
		require.NotContains(r.t, r.stderr, part)
	}
}

func daguBinary(t *testing.T) string {
	t.Helper()

	bin := os.Getenv("DAGU_BIN")
	if bin == "" {
		t.Fatal("DAGU_BIN is required to run binary-level conformance tests")
	}

	if filepath.IsAbs(bin) {
		return statBinary(t, bin)
	}

	if !hasPathSeparator(bin) {
		path, err := exec.LookPath(bin)
		if err != nil {
			t.Fatalf("resolving DAGU_BIN %q: %v", bin, err)
		}
		return path
	}

	return resolveRelativeBinary(t, bin)
}

func hasPathSeparator(path string) bool {
	return strings.Contains(path, "/") || strings.Contains(path, `\`)
}

func statBinary(t *testing.T, path string) string {
	t.Helper()

	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("resolving DAGU_BIN %q: %v", path, err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("checking DAGU_BIN %q: %v", abs, err)
	}
	return abs
}

func resolveRelativeBinary(t *testing.T, path string) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolving working directory: %v", err)
	}

	for {
		candidate := filepath.Join(wd, path)
		if _, err := os.Stat(candidate); err == nil { // #nosec G703 -- DAGU_BIN is the configured test binary path.
			return statBinary(t, candidate)
		}

		parent := filepath.Dir(wd)
		if parent == wd {
			break
		}
		wd = parent
	}

	return statBinary(t, path)
}

func isolatedEnv(t *testing.T) []string {
	t.Helper()

	root := t.TempDir()
	home := filepath.Join(root, "home")
	config := filepath.Join(root, "xdg")
	if err := os.MkdirAll(config, 0o750); err != nil {
		t.Fatalf("creating config dir: %v", err)
	}

	env := make([]string, 0, len(os.Environ())+8)
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if strings.HasPrefix(key, "DAGU_") || isolatedEnvKey(key) {
			continue
		}
		env = append(env, entry)
	}

	env = append(env,
		"CI=1",
		"NO_COLOR=1",
		"DAGU_AUTH_MODE=none",
		"DAGU_HOME="+filepath.Join(root, "dagu"),
		"DAGU_SKIP_EXAMPLES=true",
		"HOME="+home,
		"XDG_CONFIG_HOME="+config,
		"APPDATA="+filepath.Join(root, "appdata"),
		"USERPROFILE="+home,
	)
	if runtime.GOOS == "windows" {
		// Most conformance fixtures use POSIX shell snippets. GitHub-hosted
		// Windows runners provide Bash through Git for Windows; Windows-specific
		// suites can override this default with RunWithEnv.
		env = append(env, "DAGU_DEFAULT_SHELL=bash")
	}

	return env
}

func isolatedEnvKey(key string) bool {
	switch key {
	case "APPDATA", "HOME", "PWD", "USERPROFILE", "XDG_CONFIG_HOME":
		return true
	default:
		return false
	}
}
