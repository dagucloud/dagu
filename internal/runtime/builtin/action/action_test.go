// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package action

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveLocalSourceActionUsesWorkDir(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	actionDir := filepath.Join(workDir, "actions", "notify")
	writeManifestOnly(t, actionDir, "source-action")

	bundle, err := resolveBundle(context.Background(), "source:actions/notify@local", resolveOptions{
		WorkDir: workDir,
	})
	require.NoError(t, err)

	assert.Equal(t, actionDir, bundle.RootDir)
}

func TestGitHubRepoURLForBareActionRef(t *testing.T) {
	t.Parallel()

	repoURL, err := githubRepoURL("acme/dagu-actions-slack")

	require.NoError(t, err)
	assert.Equal(t, "https://github.com/acme/dagu-actions-slack.git", repoURL)
}

func TestGitHubRepoURLForOfficialActionShorthand(t *testing.T) {
	t.Parallel()

	repoURL, err := githubRepoURL("slack")

	require.NoError(t, err)
	assert.Equal(t, "https://github.com/dagucloud/action-slack.git", repoURL)
}

func TestGitHubRepoURLRejectsInvalidTargets(t *testing.T) {
	t.Parallel()

	for _, target := range []string{
		"acme/dagu-actions/slack",
		"acme/../slack",
		"-acme/slack",
		"acme-/slack",
		"acme/slack repo",
		"github.com/acme/slack",
		".hidden",
		"../slack",
	} {
		t.Run(target, func(t *testing.T) {
			_, err := githubRepoURL(target)
			require.Error(t, err)
		})
	}
}

func TestResolveSourceBundleCachesByResolvedSHA(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoDir := writeGitActionRepo(t)
	sha, err := gitRevParse(ctx, repoDir)
	require.NoError(t, err)

	toolsDir := t.TempDir()
	root, resolved, err := cloneGitSource(ctx, repoDir, "v1", resolveOptions{
		ToolsDir: toolsDir,
	})
	require.NoError(t, err)

	repoKey := hashRef(gitURL(repoDir))
	assert.Equal(t, filepath.Join(toolsDir, "actions", "source", repoKey, sha), root)
	assert.Equal(t, sha, resolved)
	assert.FileExists(t, filepath.Join(root, manifestFileName))
}

func TestResolvePackagePrefixRejectsTraversal(t *testing.T) {
	t.Parallel()

	_, err := resolveBundle(context.Background(), "pkg:../bad@1.0.0", resolveOptions{
		ToolsDir: t.TempDir(),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "owner/repo@version")
}

func TestValidateGitRefRejectsUnsafeRefs(t *testing.T) {
	t.Parallel()

	for _, ref := range []string{
		"",
		"-main",
		"feature/../main",
		"feature//main",
		"feature.lock/main",
		"main.lock",
		"main with space",
		"main~1",
		"main@{1}",
	} {
		t.Run(ref, func(t *testing.T) {
			require.Error(t, validateGitRef(ref))
		})
	}

	require.NoError(t, validateGitRef("v1.2.3"))
	require.NoError(t, validateGitRef("release/v1"))
}

func TestParseConfigRejectsUnsafeRefs(t *testing.T) {
	t.Parallel()

	for _, ref := range []string{
		"source:github.com/acme/action",
		"source:github.com/acme/action@-main",
		"acme/action@feature/../main",
		"pkg:acme/action@v1",
	} {
		t.Run(ref, func(t *testing.T) {
			t.Parallel()

			_, err := parseConfig(map[string]any{"ref": ref})
			require.Error(t, err)
		})
	}

	cfg, err := parseConfig(map[string]any{"ref": "source:./actions/notify@local"})
	require.NoError(t, err)
	assert.Equal(t, "source:./actions/notify@local", cfg.Ref)
}

func TestLoadManifestAcceptsDAG(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeManifestOnly(t, dir, "source-action")

	m, err := loadManifest(dir)

	require.NoError(t, err)
	assert.Equal(t, "source-action", m.Name)
	assert.Equal(t, "action.yaml", m.DAG)
}

func TestLoadManifestRejectsMissingDAG(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, manifestFileName), []byte(`apiVersion: v1alpha1
name: bad-action
`), 0o600))

	_, err := loadManifest(dir)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "action dag is required")
}

func TestLoadManifestRejectsUnsupportedFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, manifestFileName), []byte(`apiVersion: v1alpha1
name: bad-action
dag: action.yaml
entrypoint: main.ts
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "action.yaml"), []byte(`name: child
steps:
  - run: echo ok
`), 0o600))

	_, err := loadManifest(dir)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `action manifest field "entrypoint" is not supported`)
}

func TestLoadManifestRejectsUnsupportedAPIVersion(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, manifestFileName), []byte(`apiVersion: dagu.dev/v2
name: bad-action
dag: action.yaml
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "action.yaml"), []byte(`name: child
steps:
  - run: echo ok
`), 0o600))

	_, err := loadManifest(dir)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `apiVersion must be "v1alpha1"`)
}

func TestValidateActionDAGRejectsExplicitWorkingDir(t *testing.T) {
	t.Parallel()

	err := validateActionDAG(&core.DAG{
		Name:               "child",
		WorkingDir:         "/tmp/source",
		WorkingDirExplicit: true,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not set working_dir")
}

func TestWriteJSONOutputValidatesDeclaredOutputs(t *testing.T) {
	t.Parallel()

	exec := &Executor{stdout: &bytes.Buffer{}}
	m := &manifest{
		Outputs: map[string]any{
			"type":     "object",
			"required": []any{"ok"},
			"properties": map[string]any{
				"ok": map[string]any{"type": "boolean"},
			},
		},
	}

	err := exec.writeJSONOutput(map[string]any{}, m)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "action output does not match outputs schema")
}

func writeGitActionRepo(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	require.NoError(t, runGit(ctx, dir, "init"))
	writeManifestOnly(t, dir, "git-action")
	require.NoError(t, runGit(ctx, dir, "add", "."))
	require.NoError(t, runGit(ctx, dir,
		"-c", "user.name=Dagu Test",
		"-c", "user.email=dagu-test@example.com",
		"commit", "-m", "initial action",
	))
	require.NoError(t, runGit(ctx, dir, "tag", "v1"))
	return dir
}

func writeManifestOnly(t *testing.T, dir, name string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o750))
	manifest := `apiVersion: v1alpha1
name: ` + name + `
dag: action.yaml
inputs:
  type: object
  additionalProperties: true
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, manifestFileName), []byte(manifest), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "action.yaml"), []byte(`name: source-action-child
steps:
  - run: echo ok
`), 0o600))
}
