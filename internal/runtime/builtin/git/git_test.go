// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package git

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func checkoutStep(config map[string]any) core.Step {
	return core.Step{
		Name: "checkout",
		ExecutorConfig: core.ExecutorConfig{
			Type:   executorType,
			Config: config,
		},
		Commands: []core.CommandEntry{{Command: opCheckout}},
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

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
