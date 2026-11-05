// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package git

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	cryptossh "golang.org/x/crypto/ssh"
)

func TestGitCheckoutClonesRepositoryToRelativePath(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	commit := createTestRepository(t, source, "hello.txt", "hello\n")
	workDir := t.TempDir()

	exec, err := newExecutor(testContext(workDir), checkoutStep(map[string]any{
		"repository": source,
		"ref":        commit,
		"path":       "workspace/repo",
		"depth":      1,
	}))
	require.NoError(t, err)

	var stdout bytes.Buffer
	exec.SetStdout(&stdout)
	require.NoError(t, exec.Run(context.Background()))

	require.FileExists(t, filepath.Join(workDir, "workspace/repo/hello.txt"))
	assert.Equal(t, "hello\n", readFile(t, filepath.Join(workDir, "workspace/repo/hello.txt")))

	var result checkoutResult
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &result))
	assert.Equal(t, opCheckout, result.Operation)
	assert.Equal(t, filepath.Join(workDir, "workspace/repo"), result.Path)
	assert.Equal(t, commit, result.Commit)
	assert.Equal(t, commit, result.Ref)
	assert.True(t, result.Cloned)
	assert.True(t, result.Changed)
}

func TestGitCheckoutUpdatesExistingRepository(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	firstCommit := createTestRepository(t, source, "version.txt", "one\n")
	secondCommit := commitFile(t, source, "version.txt", "two\n")
	workDir := t.TempDir()

	first, err := newExecutor(testContext(workDir), checkoutStep(map[string]any{
		"repository": source,
		"ref":        firstCommit,
		"path":       "repo",
	}))
	require.NoError(t, err)
	require.NoError(t, first.Run(context.Background()))
	assert.Equal(t, "one\n", readFile(t, filepath.Join(workDir, "repo/version.txt")))

	second, err := newExecutor(testContext(workDir), checkoutStep(map[string]any{
		"repository": source,
		"ref":        secondCommit,
		"path":       "repo",
	}))
	require.NoError(t, err)

	var stdout bytes.Buffer
	second.SetStdout(&stdout)
	require.NoError(t, second.Run(context.Background()))

	assert.Equal(t, "two\n", readFile(t, filepath.Join(workDir, "repo/version.txt")))

	var result checkoutResult
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &result))
	assert.False(t, result.Cloned)
	assert.True(t, result.Changed)
	assert.Equal(t, secondCommit, result.Commit)
}

func TestGitCheckoutUpdatesExistingRepositoryWithoutRef(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	firstCommit := createTestRepository(t, source, "version.txt", "one\n")
	workDir := t.TempDir()

	first, err := newExecutor(testContext(workDir), checkoutStep(map[string]any{
		"repository": source,
		"ref":        "",
		"path":       "repo",
	}))
	require.NoError(t, err)

	var firstStdout bytes.Buffer
	first.SetStdout(&firstStdout)
	require.NoError(t, first.Run(context.Background()))
	assert.Equal(t, "one\n", readFile(t, filepath.Join(workDir, "repo/version.txt")))

	var firstResult checkoutResult
	require.NoError(t, json.Unmarshal(firstStdout.Bytes(), &firstResult))
	assert.Equal(t, firstCommit, firstResult.Commit)

	secondCommit := commitFile(t, source, "version.txt", "two\n")
	second, err := newExecutor(testContext(workDir), checkoutStep(map[string]any{
		"repository": source,
		"ref":        "",
		"path":       "repo",
	}))
	require.NoError(t, err)

	var stdout bytes.Buffer
	second.SetStdout(&stdout)
	require.NoError(t, second.Run(context.Background()))

	assert.Equal(t, "two\n", readFile(t, filepath.Join(workDir, "repo/version.txt")))

	var result checkoutResult
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &result))
	assert.False(t, result.Cloned)
	assert.True(t, result.Changed)
	assert.Equal(t, secondCommit, result.Commit)
}

func TestGitCheckoutSSHUser(t *testing.T) {
	t.Parallel()

	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	writeTestSSHKey(t, keyPath)

	for _, tc := range []struct {
		name, repository, username, want string
	}{
		{"Username", "ssh://other@git.example.com/repo.git", "deploy", "deploy"},
		{"URLUser", "ssh://deploy@git.example.com/repo.git", "", "deploy"},
		{"SCPUser", "builder@git.example.com:org/repo.git", "", "builder"},
		{"Default", "ssh://git.example.com/repo.git", "", "git"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			exec, err := newExecutor(testContext(t.TempDir()), checkoutStep(map[string]any{
				"repository":   tc.repository,
				"path":         "repo",
				"username":     tc.username,
				"ssh_key_path": keyPath,
			}))
			require.NoError(t, err)
			auth, err := exec.(*executorImpl).auth()
			require.NoError(t, err)
			keys, ok := auth.(*gitssh.PublicKeys)
			require.True(t, ok)
			assert.Equal(t, tc.want, keys.User)
		})
	}
}

// A branch ref on an existing clone checks out the fetched branch, not the
// local branch the clone created, which stays at the commit cloned.
func TestGitCheckoutUpdatesExistingRepositoryToBranch(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	createTestRepository(t, source, "version.txt", "one\n")
	branch := currentBranch(t, source)
	workDir := t.TempDir()
	step := checkoutStep(map[string]any{
		"repository": source,
		"ref":        branch,
		"path":       "repo",
	})

	first, err := newExecutor(testContext(workDir), step)
	require.NoError(t, err)
	require.NoError(t, first.Run(context.Background()))
	assert.Equal(t, "one\n", readFile(t, filepath.Join(workDir, "repo/version.txt")))

	secondCommit := commitFile(t, source, "version.txt", "two\n")
	second, err := newExecutor(testContext(workDir), step)
	require.NoError(t, err)

	var stdout bytes.Buffer
	second.SetStdout(&stdout)
	require.NoError(t, second.Run(context.Background()))

	assert.Equal(t, "two\n", readFile(t, filepath.Join(workDir, "repo/version.txt")))

	var result checkoutResult
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &result))
	assert.True(t, result.Changed)
	assert.Equal(t, secondCommit, result.Commit)
}

// ref HEAD keeps the commit the existing checkout is on, even after a fetch
// moves the remote default branch.
func TestGitCheckoutHeadRefKeepsCurrentCommit(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	firstCommit := createTestRepository(t, source, "version.txt", "one\n")
	workDir := t.TempDir()

	first, err := newExecutor(testContext(workDir), checkoutStep(map[string]any{
		"repository": source,
		"ref":        currentBranch(t, source),
		"path":       "repo",
	}))
	require.NoError(t, err)
	require.NoError(t, first.Run(context.Background()))

	commitFile(t, source, "version.txt", "two\n")
	second, err := newExecutor(testContext(workDir), checkoutStep(map[string]any{
		"repository": source,
		"ref":        "HEAD",
		"path":       "repo",
	}))
	require.NoError(t, err)

	var stdout bytes.Buffer
	second.SetStdout(&stdout)
	require.NoError(t, second.Run(context.Background()))

	assert.Equal(t, "one\n", readFile(t, filepath.Join(workDir, "repo/version.txt")))
	var result checkoutResult
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &result))
	assert.False(t, result.Changed)
	assert.Equal(t, firstCommit, result.Commit)
}

func TestGitCheckoutRejectsMixedHTTPSAuth(t *testing.T) {
	t.Parallel()

	_, err := newExecutor(testContext(t.TempDir()), checkoutStep(map[string]any{
		"repository": "https://example.com/repo.git",
		"path":       "repo",
		"token":      "token",
		"username":   "user",
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token cannot be combined with username/password")
}

func checkoutStep(config map[string]any) ir.Step {
	return ir.Step{
		Name: "checkout",
		ExecutorConfig: ir.ExecutorConfig{
			Type:   executorType,
			Config: config,
		},
		Commands: []ir.CommandEntry{{Command: opCheckout}},
	}
}

func testContext(workDir string) context.Context {
	ctx := context.Background()
	return runtime.WithEnv(ctx, runtime.Env{WorkingDir: workDir})
}

func createTestRepository(t *testing.T, path, name, content string) string {
	t.Helper()
	_, err := gogit.PlainInit(path, false)
	require.NoError(t, err)
	return commitFile(t, path, name, content)
}

func commitFile(t *testing.T, repoPath, name, content string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, name), []byte(content), 0o600))

	repo, err := gogit.PlainOpen(repoPath)
	require.NoError(t, err)
	wt, err := repo.Worktree()
	require.NoError(t, err)
	_, err = wt.Add(name)
	require.NoError(t, err)
	hash, err := wt.Commit("update "+name, &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Dagu Test",
			Email: "dagu-test@example.com",
			When:  time.Now(),
		},
	})
	require.NoError(t, err)
	return hash.String()
}

func writeTestSSHKey(t *testing.T, path string) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	block, err := cryptossh.MarshalPrivateKey(key, "")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(block), 0o600))
}

func currentBranch(t *testing.T, repoPath string) string {
	t.Helper()
	repo, err := gogit.PlainOpen(repoPath)
	require.NoError(t, err)
	head, err := repo.Head()
	require.NoError(t, err)
	return head.Name().Short()
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
