// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cli_test

import (
	"encoding/pem"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestSyncStatusNotEnabledByDefault(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	result := dagu.RunWithEnv(env, "sync", "status")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("git sync is not enabled")
}

// TestSyncPullFetchesDAGFromRemote proves `dagu sync pull` actually fetches
// DAG definitions from a real Git remote into the local DAGs directory.
//
// gitsync's repository config only recognizes https://, http://, git@, and
// ssh:// URLs (see normalizeRepoURL / isFullURL in internal/gitsync/git.go),
// so a bare local filesystem path cannot be used directly. This serves a
// real bare repository over Git's smart HTTP protocol via the git-http-
// backend CGI binary, so the test exercises the same transport a real
// GitHub-hosted repository would use.
//
// The server requires TLS: gitsync's config always defaults auth.type to
// "token" once git sync is enabled (see setGitSyncDefaults in
// internal/cmn/config/loader.go), so this fixture must configure a real
// token, and Basic auth would send that token in a cleartext Authorization
// header over plain http://. The server's certificate is self-signed, so
// its PEM is written out and trusted via the SSL_CERT_FILE env var, which
// Go's crypto/x509 honors on Unix -- Windows does not, hence the skip below.
func TestSyncPullFetchesDAGFromRemote(t *testing.T) {
	t.Parallel()
	requireGitHTTPBackend(t)
	if runtime.GOOS == "windows" {
		t.Skip("trusting the fixture's self-signed certificate via SSL_CERT_FILE only works on Unix")
	}

	dagu := harness.NewRunner(t)
	env := sharedEnv(t)

	remotePath := dagu.ProjectPath("remote.git")
	initBareRepoWithDAG(t, remotePath, "synced.yaml", "working_dir: .\nsteps:\n  - id: hello\n    run: \"true\"\n")

	const token = "dummy-token-value"
	serverURL, certPath := startGitHTTPSServer(t, filepath.Dir(remotePath), token)
	repoURL := serverURL + "/remote.git"

	syncEnv := append(env,
		"DAGU_GITSYNC_ENABLED=true",
		"DAGU_GITSYNC_REPOSITORY="+repoURL,
		"DAGU_GITSYNC_BRANCH=main",
		// The server requires Basic auth with this exact token, so a
		// successful pull proves dagu actually sent it, not merely that
		// auth.type=token was accepted as configuration.
		"DAGU_GITSYNC_AUTH_TOKEN="+token,
		"SSL_CERT_FILE="+certPath,
	)

	status := dagu.RunWithEnv(syncEnv, "sync", "status")
	status.ExpectExitCode(0)
	require.Contains(t, status.Stdout(), "Repository:")

	pull := dagu.RunWithEnv(syncEnv, "sync", "pull")
	pull.ExpectExitCode(0)

	ls := dagu.RunWithEnv(syncEnv, "ls")
	ls.ExpectExitCode(0)
	require.Contains(t, ls.Stdout(), "synced")
}

func requireGitHTTPBackend(t *testing.T) {
	t.Helper()
	if _, ok := resolveGitHTTPBackend(); !ok {
		t.Skip("git-http-backend is not available in this environment")
	}
}

func gitHTTPBackendPath(t *testing.T) string {
	t.Helper()
	p, ok := resolveGitHTTPBackend()
	if !ok {
		t.Fatal("git-http-backend not found")
	}
	return p
}

// resolveGitHTTPBackend locates the git-http-backend CGI binary: first via
// PATH, then via `git --exec-path`, Git's own reported libexec directory
// (where package managers such as Homebrew and most Linux distributions
// install it even when it isn't on PATH), falling back to a couple of
// common hard-coded locations as a last resort.
func resolveGitHTTPBackend() (string, bool) {
	if p, err := exec.LookPath("git-http-backend"); err == nil {
		return p, true
	}
	if out, err := exec.Command("git", "--exec-path").Output(); err == nil {
		candidate := filepath.Join(strings.TrimSpace(string(out)), "git-http-backend")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, true
		}
	}
	for _, candidate := range []string{"/usr/lib/git-core/git-http-backend", "/usr/libexec/git-core/git-http-backend"} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, true
		}
	}
	return "", false
}

// startGitHTTPSServer serves every bare repository directly under
// projectRoot over Git's smart HTTP protocol via TLS, using the real
// git-http-backend CGI program via net/http/cgi, and returns the server's
// base URL together with a PEM file path for its self-signed certificate.
// It requires Git Basic authentication with username "git" and the given
// token -- the same convention gitsync's token auth uses (see
// GitClient.getAuth in internal/gitsync/git.go) -- rejecting any other
// request before it ever reaches the CGI backend, so a successful sync
// proves the token was sent. TLS (not plain HTTP) keeps that token out of a
// cleartext Authorization header on the wire.
func startGitHTTPSServer(t *testing.T, projectRoot, token string) (serverURL, certPath string) {
	t.Helper()

	handler := &cgi.Handler{
		Path: gitHTTPBackendPath(t),
		Env: []string{
			"GIT_PROJECT_ROOT=" + projectRoot,
			"GIT_HTTP_EXPORT_ALL=1",
		},
		Dir: projectRoot,
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "git" || password != token {
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	certFile := filepath.Join(t.TempDir(), "git-http-server-ca.pem")
	require.NoError(t, os.WriteFile(certFile, certPEM, 0o600))

	return server.URL, certFile
}

// initBareRepoWithDAG creates a bare Git repository at repoPath whose main
// branch contains one file (fileName) with the given content, by committing
// it in a temporary working clone and pushing it into the bare repo.
func initBareRepoWithDAG(t *testing.T, repoPath, fileName, content string) {
	t.Helper()

	runGit(t, "", "init", "--bare", "-b", "main", repoPath)

	workDir := t.TempDir()
	runGit(t, "", "clone", repoPath, workDir)
	require.NoError(t, os.WriteFile(filepath.Join(workDir, fileName), []byte(content), 0o644))
	runGit(t, workDir, "add", "--", fileName)
	runGit(t, workDir, "commit", "-m", "add "+fileName)
	runGit(t, workDir, "push", "origin", "main")
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()

	gitPath, err := exec.LookPath("git")
	require.NoError(t, err, "Git is required by the sync conformance fixture setup")

	commandArgs := append([]string(nil), args...)
	if dir != "" {
		commandArgs = append([]string{"-C", dir}, commandArgs...)
	}
	cmd := exec.Command(gitPath, commandArgs...) //nolint:gosec // Arguments are controlled by conformance tests.
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Dagu Conformance",
		"GIT_AUTHOR_EMAIL=dagu-conformance@example.com",
		"GIT_COMMITTER_NAME=Dagu Conformance",
		"GIT_COMMITTER_EMAIL=dagu-conformance@example.com",
	)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s\n%s", strings.Join(args, " "), output)
}
