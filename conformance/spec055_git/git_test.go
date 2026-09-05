// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec055_git holds black-box conformance tests for
// Spec 055: Git Checkout Action.
package spec055_git_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

type checkoutResult struct {
	Operation string `json:"operation"`
	Path      string `json:"path"`
	Ref       string `json:"ref,omitempty"`
	Commit    string `json:"commit"`
	Cloned    bool   `json:"cloned"`
	Changed   bool   `json:"changed"`
}

func parseCheckoutResult(t *testing.T, raw string) checkoutResult {
	t.Helper()

	var result checkoutResult
	require.NoError(t, json.Unmarshal([]byte(raw), &result))
	return result
}

func TestGitCheckoutLive(t *testing.T) {
	t.Run("clones fresh with no ref, checking out the repository's default HEAD", func(t *testing.T) {
		t.Parallel()

		origin := setupOriginRepo(t)
		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv([]string{"GIT_ORIGIN=" + origin.Path}, "start", "clone_no_ref.yaml")
		result.ExpectExitCode(0)

		checkout := parseCheckoutResult(t, lastStepStdout(t, result.Stdout()))
		require.True(t, checkout.Cloned)
		require.True(t, checkout.Changed)
		require.Equal(t, origin.HeadCommit, checkout.Commit)

		data, err := os.ReadFile(dagu.ProjectPath("work/file.txt")) // #nosec G304 -- fixed test path.
		require.NoError(t, err)
		require.Equal(t, "v2", string(data))
	})

	t.Run("with.ref checks out the given tag instead of HEAD", func(t *testing.T) {
		t.Parallel()

		origin := setupOriginRepo(t)
		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv([]string{"GIT_ORIGIN=" + origin.Path}, "start", "clone_ref_tag.yaml")
		result.ExpectExitCode(0)

		checkout := parseCheckoutResult(t, lastStepStdout(t, result.Stdout()))
		require.Equal(t, origin.FirstCommit, checkout.Commit)

		data, err := os.ReadFile(dagu.ProjectPath("work/file.txt")) // #nosec G304 -- fixed test path.
		require.NoError(t, err)
		require.Equal(t, "v1", string(data))
	})

	t.Run("with.depth performs a shallow clone", func(t *testing.T) {
		t.Parallel()

		origin := setupOriginRepo(t)
		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv([]string{"GIT_ORIGIN=" + origin.Path}, "start", "shallow_clone.yaml")
		result.ExpectExitCode(0)

		_, err := os.Stat(dagu.ProjectPath("work/.git/shallow"))
		require.NoErrorf(t, err, "expected a shallow-clone marker file for depth: 1")
	})

	t.Run("re-checking out the same ref against an existing target reports no change", func(t *testing.T) {
		t.Parallel()

		origin := setupOriginRepo(t)
		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv([]string{"GIT_ORIGIN=" + origin.Path}, "start", "idempotent_recheckout.yaml")
		result.ExpectExitCode(0)

		checkout := parseCheckoutResult(t, lastStepStdout(t, result.Stdout()))
		require.False(t, checkout.Cloned)
		require.False(t, checkout.Changed)
		require.Equal(t, origin.FirstCommit, checkout.Commit)
	})

	t.Run("checking out a different ref against an existing target fetches and reports a change", func(t *testing.T) {
		t.Parallel()

		origin := setupOriginRepo(t)
		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv([]string{"GIT_ORIGIN=" + origin.Path}, "start", "switch_ref.yaml")
		result.ExpectExitCode(0)

		checkout := parseCheckoutResult(t, lastStepStdout(t, result.Stdout()))
		require.False(t, checkout.Cloned)
		require.True(t, checkout.Changed)
		require.Equal(t, origin.HeadCommit, checkout.Commit)
	})

	t.Run("a new upstream commit is fetched on the next checkout", func(t *testing.T) {
		t.Parallel()

		origin := setupOriginRepo(t)
		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv([]string{"GIT_ORIGIN=" + origin.Path}, "start", "new_upstream_commit.yaml")
		result.ExpectExitCode(0)

		checkout := parseCheckoutResult(t, lastStepStdout(t, result.Stdout()))
		require.True(t, checkout.Changed)
		require.NotEqual(t, origin.HeadCommit, checkout.Commit, "expected the third commit the DAG's own mutate step added, not the second")

		data, err := os.ReadFile(dagu.ProjectPath("work/file.txt")) // #nosec G304 -- fixed test path.
		require.NoError(t, err)
		require.Equal(t, "v3\n", string(data), "the DAG's own mutate step writes file.txt via echo, which appends a newline")
	})

	t.Run("local modifications block a checkout without with.force", func(t *testing.T) {
		t.Parallel()

		origin := setupOriginRepo(t)
		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv([]string{"GIT_ORIGIN=" + origin.Path}, "start", "dirty_worktree_blocks_checkout.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("unstaged changes")
	})

	t.Run("with.force discards local modifications to complete the checkout", func(t *testing.T) {
		t.Parallel()

		origin := setupOriginRepo(t)
		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv([]string{"GIT_ORIGIN=" + origin.Path}, "start", "dirty_worktree_force.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "v1", lastStepStdout(t, result.Stdout()))
	})

	t.Run("a target path that is an existing file fails", func(t *testing.T) {
		t.Parallel()

		origin := setupOriginRepo(t)
		dagu := harness.NewRunner(t)
		dagu.WriteFile("target_is_file", "not a directory")

		result := dagu.RunWithEnv([]string{"GIT_ORIGIN=" + origin.Path}, "start", "target_is_file.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("not a directory")
	})

	t.Run("a target directory that is non-empty and not a git repository fails", func(t *testing.T) {
		t.Parallel()

		origin := setupOriginRepo(t)
		dagu := harness.NewRunner(t)
		dagu.WriteFile("target_nongit/somefile.txt", "not a repo")

		result := dagu.RunWithEnv([]string{"GIT_ORIGIN=" + origin.Path}, "start", "target_nongit_nonempty.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("target directory is not a git repository and is not empty")
	})

	t.Run("a ref that does not exist in the repository fails", func(t *testing.T) {
		t.Parallel()

		origin := setupOriginRepo(t)
		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv([]string{"GIT_ORIGIN=" + origin.Path}, "start", "bad_ref.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains(`ref "does-not-exist-branch" not found`)
	})

	t.Run("a repository that cannot be reached fails", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "bad_repository.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("clone failed")
	})
}

// TestGitCheckoutValidation proves that git.checkout's configuration
// errors are enforced at DAG-build time, ahead of any step running.
func TestGitCheckoutValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		fixture  string
		contains string
	}{
		{"with.repository missing", "missing_repository.yaml", "repository is required"},
		{"with.path missing", "missing_path.yaml", "path is required"},
		{"a negative with.depth", "negative_depth.yaml", "minimum"},
		{"with.ssh_key_path combined with with.token", "ssh_key_and_token.yaml", "ssh_key_path cannot be combined with token or password"},
		{"with.token combined with with.username", "token_and_username.yaml", "token cannot be combined with username/password"},
		{"a with: field not supported by git.checkout", "unsupported_field.yaml", "invalid keys: branch"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.fixture)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.contains)
		})
	}
}
