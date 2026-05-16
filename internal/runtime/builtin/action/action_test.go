// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package action

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestResolvePackageActionFromRegistryDir(t *testing.T) {
	t.Parallel()

	registryDir := t.TempDir()
	actionDir := filepath.Join(registryDir, "dagu-actions", "slack.notify", "1.2.3")
	writeManifestOnly(t, actionDir, "pkg-action", "v2.5.2")

	bundle, err := resolveBundle(context.Background(), "pkg:dagu-actions/slack.notify@1.2.3", resolveOptions{
		RegistryDir: registryDir,
	})
	require.NoError(t, err)

	assert.Equal(t, bundleModePackage, bundle.Mode)
	assert.Equal(t, actionDir, bundle.RootDir)
	assert.Equal(t, "dagu-actions/slack.notify", bundle.ResolvedRef)
	assert.Equal(t, "1.2.3", bundle.Version)
}

func TestResolvePackageActionDefaultsRegistryUnderToolsDir(t *testing.T) {
	t.Parallel()

	toolsDir := t.TempDir()
	actionDir := filepath.Join(toolsDir, "actions", "registry", "dagu-actions", "slack.notify", "1.2.3")
	writeManifestOnly(t, actionDir, "pkg-action", "v2.5.2")

	bundle, err := resolveBundle(context.Background(), "pkg:dagu-actions/slack.notify@1.2.3", resolveOptions{
		ToolsDir: toolsDir,
	})
	require.NoError(t, err)

	assert.Equal(t, actionDir, bundle.RootDir)
}

func TestResolvePackageActionRejectsTraversal(t *testing.T) {
	t.Parallel()

	_, err := resolveBundle(context.Background(), "pkg:../bad@1.0.0", resolveOptions{
		RegistryDir: t.TempDir(),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid package name")
}

func TestWritePermissionsResolveExtrasFromActionRoot(t *testing.T) {
	t.Parallel()

	rootDir := filepath.Join(t.TempDir(), "action")
	outputDir := filepath.Join(t.TempDir(), "output")

	got := writePermissions(rootDir, outputDir, []string{"cache/state.json", "../secret", "/tmp/abs", `C:\tmp\abs`})

	assert.Equal(t, []string{
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

func writeTestAction(t *testing.T, name, denoVersion string) string {
	t.Helper()
	dir := t.TempDir()
	writeManifestOnly(t, dir, name, denoVersion)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mod.ts"), []byte("export {};\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "deno.lock"), []byte("{}\n"), 0o600))
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
