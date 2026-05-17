// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package action

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	dagutools "github.com/dagucloud/dagu/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutorRunsLocalSourceActionWithDeclaredDeno(t *testing.T) {
	if os.Getenv("DAGU_FAKE_DENO") == "1" {
		fakeDenoProcess()
		return
	}
	t.Parallel()

	actionDir := writeTestAction(t, "source-action", "v2.5.2")
	manifestPath := writeToolsManifest(t, os.Args[0], "v2.5.2")
	argsPath := filepath.Join(t.TempDir(), "deno-args.txt")
	artifactDir := t.TempDir()

	step := core.Step{
		Name: "notify",
		ExecutorConfig: core.ExecutorConfig{
			Type: executorType,
			Config: map[string]any{
				"ref": "source:" + actionDir + "@local",
				"input": map[string]any{
					"channel": "#ops",
					"text":    "done",
				},
			},
		},
	}
	ctx := runtime.NewContext(
		context.Background(),
		&core.DAG{Name: "action-test"},
		"run-1",
		"",
		runtime.WithEnvVars(
			dagutools.EnvManifest+"="+manifestPath,
			"DAGU_FAKE_DENO=1",
			"DAGU_FAKE_DENO_ARGS_FILE="+argsPath,
			"DAGU_FAKE_DENO_LOG_STDOUT=1",
		),
		runtime.WithArtifactDir(artifactDir),
	)

	exec, err := newAction(ctx, step)
	require.NoError(t, err)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exec.SetStdout(&stdout)
	exec.SetStderr(&stderr)

	require.NoError(t, exec.Run(ctx))
	assert.JSONEq(t, `{"ok":true,"mode":"run"}`, strings.TrimSpace(stdout.String()))
	assert.Contains(t, stderr.String(), "action log")

	argsData, err := os.ReadFile(argsPath)
	require.NoError(t, err)
	argsLog := string(argsData)
	assert.Contains(t, argsLog, "install")
	assert.Contains(t, argsLog, "--entrypoint "+filepath.Join(actionDir, "mod.ts"))
	assert.Contains(t, argsLog, "run --cached-only --no-prompt")
	assert.Contains(t, argsLog, "--lock="+filepath.Join(actionDir, "deno.lock"))
	assert.Contains(t, argsLog, "--frozen")
	assert.Contains(t, argsLog, "--allow-env=DAGU_ACTION_INPUT,DAGU_ACTION_OUTPUT,DAGU_ACTION_OUTPUT_DIR,DAGU_ACTION_DIR,DAGU_ACTION_REF,SLACK_BOT_TOKEN")
	assert.Contains(t, argsLog, "--allow-net=slack.com")
	assert.NotContains(t, argsLog, "--allow-run")
	assert.NotContains(t, argsLog, "--allow-all")
}

func TestExecutorKeepsControlFilesOutOfArtifactDir(t *testing.T) {
	if os.Getenv("DAGU_FAKE_DENO") == "1" {
		fakeDenoProcess()
		return
	}
	t.Parallel()

	actionDir := writeTestAction(t, "source-action", "v2.5.2")
	manifestPath := writeToolsManifest(t, os.Args[0], "v2.5.2")
	argsPath := filepath.Join(t.TempDir(), "deno-args.txt")
	runDir := filepath.Join(t.TempDir(), "run")
	artifactDir := filepath.Join(t.TempDir(), "artifacts")
	require.NoError(t, os.MkdirAll(runDir, 0o750))
	require.NoError(t, os.MkdirAll(artifactDir, 0o750))

	step := core.Step{
		Name: "notify",
		ExecutorConfig: core.ExecutorConfig{
			Type: executorType,
			Config: map[string]any{
				"ref": "source:" + actionDir + "@local",
				"input": map[string]any{
					"channel": "#ops",
					"text":    "done",
				},
			},
		},
	}
	ctx := runtime.NewContext(
		context.Background(),
		&core.DAG{Name: "action-test"},
		"run-1",
		filepath.Join(runDir, "dag-run.log"),
		runtime.WithWorkDir(runDir),
		runtime.WithEnvVars(
			dagutools.EnvManifest+"="+manifestPath,
			"DAGU_FAKE_DENO=1",
			"DAGU_FAKE_DENO_ARGS_FILE="+argsPath,
		),
		runtime.WithArtifactDir(artifactDir),
	)
	ctx = runtime.WithEnv(ctx, runtime.NewEnv(ctx, step))

	exec, err := newAction(ctx, step)
	require.NoError(t, err)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exec.SetStdout(&stdout)
	exec.SetStderr(&stderr)

	require.NoError(t, exec.Run(ctx))
	assert.JSONEq(t, `{"ok":true,"mode":"run"}`, strings.TrimSpace(stdout.String()))
	assert.Empty(t, findFilesNamed(t, artifactDir, "input.json", "output.json"))

	controlFiles := findFilesNamed(t, runDir, "input.json", "output.json")
	require.Len(t, controlFiles, 2)
	for _, path := range controlFiles {
		assert.Contains(t, path, filepath.Join(runDir, ".dagu-actions"))
	}
}

func TestResolveLocalSourceActionUsesWorkDir(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	actionDir := filepath.Join(workDir, "actions", "notify")
	writeManifestOnly(t, actionDir, "source-action", "v2.5.2")

	bundle, err := resolveBundle(context.Background(), "source:actions/notify@local", resolveOptions{
		WorkDir: workDir,
	})
	require.NoError(t, err)

	assert.Equal(t, bundleModeSource, bundle.Mode)
	assert.Equal(t, actionDir, bundle.RootDir)
	assert.Equal(t, actionDir, bundle.ResolvedRef)
	assert.Equal(t, "local", bundle.Version)
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

func TestWritePermissionsResolveExtrasFromActionRoot(t *testing.T) {
	t.Parallel()

	rootDir := filepath.Join(t.TempDir(), "action")
	outputPath := filepath.Join(t.TempDir(), "control", "output.json")
	outputDir := filepath.Join(t.TempDir(), "output")

	got := writePermissions(rootDir, outputPath, outputDir, []string{"cache/state.json", "../secret", "/tmp/abs", `C:\tmp\abs`})

	assert.Equal(t, []string{
		filepath.Clean(outputPath),
		filepath.Clean(outputDir),
		filepath.Join(rootDir, "cache", "state.json"),
	}, got)
}

func TestValidateRelativePermissionPathRejectsAbsoluteLikePaths(t *testing.T) {
	t.Parallel()

	rootDir := t.TempDir()
	for _, path := range []string{
		"/tmp/abs",
		`\tmp\abs`,
		`C:\tmp\abs`,
		"C:/tmp/abs",
		"C:tmp",
		"../secret",
		"cache/../../secret",
	} {
		t.Run(path, func(t *testing.T) {
			require.Error(t, validateRelativePermissionPath(rootDir, path, "read"))
		})
	}
}

func TestLoadManifestRejectsEscapingPermissionPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mod.ts"), []byte("export {};\n"), 0o600))
	manifest := `apiVersion: dagu.dev/v1alpha1
name: bad-action
runtime:
  type: deno
  deno: v2.5.2
  entrypoint: mod.ts
permissions:
  read: ["../secret"]
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, manifestFileName), []byte(manifest), 0o600))

	_, err := loadManifest(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid action read permission path")
}

func TestWriteActionOutputRequiresDeclaredOutputs(t *testing.T) {
	t.Parallel()

	exec := &Executor{}
	m := &manifest{
		Outputs: map[string]any{
			"type":     "object",
			"required": []any{"ok"},
			"properties": map[string]any{
				"ok": map[string]any{"type": "boolean"},
			},
		},
	}

	err := exec.writeActionOutput(filepath.Join(t.TempDir(), "missing.json"), m)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "action output is required")
}

func writeTestAction(t *testing.T, name, denoVersion string) string {
	t.Helper()
	dir := t.TempDir()
	writeManifestOnly(t, dir, name, denoVersion)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mod.ts"), []byte("export {};\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "deno.lock"), []byte("{}\n"), 0o600))
	return dir
}

func writeGitActionRepo(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	require.NoError(t, runGit(ctx, dir, "init"))
	writeManifestOnly(t, dir, "git-action", "v2.5.2")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mod.ts"), []byte("export {};\n"), 0o600))
	require.NoError(t, runGit(ctx, dir, "add", "."))
	require.NoError(t, runGit(ctx, dir,
		"-c", "user.name=Dagu Test",
		"-c", "user.email=dagu-test@example.com",
		"commit", "-m", "initial action",
	))
	require.NoError(t, runGit(ctx, dir, "tag", "v1"))
	return dir
}

func writeManifestOnly(t *testing.T, dir, name, denoVersion string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o750))
	manifest := `apiVersion: dagu.dev/v1alpha1
name: ` + name + `
runtime:
  type: deno
  deno: ` + denoVersion + `
  entrypoint: mod.ts
inputs:
  type: object
  additionalProperties: true
permissions:
  net: [slack.com]
  env: [SLACK_BOT_TOKEN]
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, manifestFileName), []byte(manifest), 0o600))
}

func writeToolsManifest(t *testing.T, denoPath, version string) string {
	t.Helper()
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.json")
	manifest := dagutools.Manifest{
		Provider: "aqua",
		Commands: map[string]dagutools.Command{
			"deno": {
				Name:    "deno",
				Path:    denoPath,
				Package: denoAquaPackage,
				Version: version,
			},
		},
	}
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(manifestPath, data, 0o600))
	return manifestPath
}

func findFilesNamed(t *testing.T, root string, names ...string) []string {
	t.Helper()
	want := make(map[string]struct{}, len(names))
	for _, name := range names {
		want[name] = struct{}{}
	}

	var found []string
	require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if _, ok := want[entry.Name()]; ok && !entry.IsDir() {
			found = append(found, path)
		}
		return nil
	}))
	return found
}

func fakeDenoProcess() {
	argsPath := os.Getenv("DAGU_FAKE_DENO_ARGS_FILE")
	if argsPath != "" {
		line := strings.Join(os.Args[1:], " ") + "\n"
		f, err := os.OpenFile(argsPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err == nil {
			_, _ = f.WriteString(line)
			_ = f.Close()
		}
	}
	if len(os.Args) > 1 && os.Args[1] == "run" {
		if os.Getenv("DAGU_FAKE_DENO_LOG_STDOUT") == "1" {
			_, _ = os.Stdout.WriteString("action log\n")
		}
		output := os.Getenv(envActionOutput)
		if output != "" {
			_ = os.WriteFile(output, []byte(`{"ok":true,"mode":"run"}`), 0o600)
		}
	}
	os.Exit(0)
}
