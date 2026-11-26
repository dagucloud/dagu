// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagu_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/stretchr/testify/require"
)

func engineTestTimeout(timeout time.Duration) time.Duration {
	if runtime.GOOS == "windows" {
		return timeout * 3
	}
	return timeout
}

func TestEngineRunFile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), engineTestTimeout(20*time.Second))
	defer cancel()

	home := t.TempDir()
	dagFile := filepath.Join(home, "embedded-file.yaml")
	testBinary, err := os.Executable()
	require.NoError(t, err, "Executable()")
	writeDAG(t, dagFile, fmt.Sprintf(`
name: embedded-file
steps:
  - name: check-param
    exec:
      command: %q
      args:
        - -test.run=TestEngineRunFileParamHelper
    env:
      DAGU_ENGINE_PARAM_HELPER: "1"
`, testBinary))

	engine, err := dagu.New(ctx, dagu.Options{HomeDir: home})
	require.NoError(t, err, "New()")
	defer func() {
		require.NoError(t, engine.Close(context.Background()))
	}()

	run, err := engine.RunFile(ctx, dagFile, dagu.WithParams(map[string]string{"FOO": "bar"}))
	require.NoError(t, err, "RunFile()")
	status, err := run.Wait(ctx)
	require.NoError(t, err, "Wait()")
	require.Equal(t, "succeeded", status.Status)
	require.Equal(t, "embedded-file", status.Name)
	require.NotEmpty(t, status.RunID)
}

func TestEngineRunFileParamHelper(t *testing.T) {
	if os.Getenv("DAGU_ENGINE_PARAM_HELPER") != "1" {
		return
	}
	require.Equal(t, "bar", os.Getenv("FOO"))
}

func TestEngineRunYAML(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), engineTestTimeout(20*time.Second))
	defer cancel()

	originalWorkingDir, err := os.Getwd()
	require.NoError(t, err, "Getwd()")

	engine, err := dagu.New(ctx, dagu.Options{HomeDir: t.TempDir()})
	require.NoError(t, err, "New()")
	defer func() {
		require.NoError(t, engine.Close(context.Background()))
	}()

	run, err := engine.RunYAML(ctx, []byte(`
name: embedded-yaml
steps:
  - name: hello
    action: noop
`))
	require.NoError(t, err, "RunYAML()")
	status, err := run.Wait(ctx)
	require.NoError(t, err, "Wait()")
	require.Equal(t, "succeeded", status.Status)
	require.Equal(t, "embedded-yaml", status.Name)
	currentWorkingDir, err := os.Getwd()
	require.NoError(t, err, "Getwd() after run")
	require.Equal(t, originalWorkingDir, currentWorkingDir)
}

func TestEngineDistributedRequiresCoordinator(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), engineTestTimeout(20*time.Second))
	defer cancel()

	engine, err := dagu.New(ctx, dagu.Options{HomeDir: t.TempDir()})
	require.NoError(t, err, "New()")
	defer func() {
		require.NoError(t, engine.Close(context.Background()))
	}()

	_, err = engine.RunYAML(ctx, []byte(`
name: embedded-distributed
steps:
  - name: hello
    command: echo hello
`), dagu.WithMode(dagu.ExecutionModeDistributed))
	require.Error(t, err)
	require.Contains(t, err.Error(), "coordinator")
}

// With projects on, an embedded run reads the secret and profile default of
// its project, never those of the workspace its DAG's label names. With
// projects off, the workspace's layers apply as in v2.
func TestEngineRunProjectSettings(t *testing.T) {
	testBinary, err := os.Executable()
	require.NoError(t, err, "Executable()")
	for _, tc := range []struct {
		name       string
		projectsOn bool
		scope      string
	}{
		{name: "ProjectsOn", projectsOn: true, scope: "project"},
		{name: "ProjectsOff", projectsOn: false, scope: "workspace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), engineTestTimeout(20*time.Second))
			defer cancel()

			home := t.TempDir()
			dataDir := filepath.Join(home, "data")
			seedEngineSettings(t, dataDir)
			configFile := filepath.Join(home, "config.yaml")
			require.NoError(t, os.WriteFile(configFile, fmt.Appendf(nil, "experimental_projects: %t\n", tc.projectsOn), 0o600))

			engine, err := dagu.New(ctx, dagu.Options{HomeDir: home, ConfigFile: configFile, DataDir: dataDir})
			require.NoError(t, err, "New()")
			defer func() {
				require.NoError(t, engine.Close(context.Background()))
			}()

			run, err := engine.RunYAML(ctx, fmt.Appendf(nil, `
name: embedded-settings
labels: [workspace=ops]
secrets:
  - name: TOKEN
    ref: api/token
steps:
  - name: check
    exec:
      command: %q
      args:
        - -test.run=TestEngineRunProjectSettingsHelper
    env:
      DAGU_ENGINE_SETTINGS_HELPER: %q
`, testBinary, tc.scope))
			require.NoError(t, err, "RunYAML()")
			status, err := run.Wait(ctx)
			require.NoError(t, err, "Wait()")
			require.Equal(t, "succeeded", status.Status, "a step read a value from the wrong settings")
		})
	}
}

// TestEngineRunProjectSettingsHelper is the step of
// TestEngineRunProjectSettings. It fails unless the secret and the profile
// default come from the scope the test names.
func TestEngineRunProjectSettingsHelper(t *testing.T) {
	scope := os.Getenv("DAGU_ENGINE_SETTINGS_HELPER")
	if scope == "" {
		return
	}
	require.Equal(t, scope+"-token", os.Getenv("TOKEN"))
	require.Equal(t, scope, os.Getenv("TIER"))
}

// seedEngineSettings gives the global scope, which is the default project's
// with projects on, and workspace ops the secret api/token and the profile
// default TIER, each with a value that names its scope.
func seedEngineSettings(t *testing.T, dataDir string) {
	t.Helper()
	ctx := context.Background()
	cfg := &config.Config{}
	cfg.Paths.DataDir = dataDir
	backend := file.NewBackend(cfg.Paths)
	secrets := file.NewSecretStore(ctx, cfg, backend.Collection(persis.CollectionSecrets))
	require.NotNil(t, secrets)
	profiles := file.NewProfileStore(ctx, cfg, backend.Collection(persis.CollectionProfiles))
	require.NotNil(t, profiles)
	opsRef, err := profile.WorkspaceInheritedRef("ops")
	require.NoError(t, err)
	for _, layer := range []struct {
		workspace string
		defaults  profile.InheritedRef
		scope     string
	}{
		{workspace: secret.GlobalWorkspace, defaults: profile.GlobalInheritedRef(), scope: "project"},
		{workspace: "ops", defaults: opsRef, scope: "workspace"},
	} {
		sec, err := secret.New(secret.CreateInput{Workspace: layer.workspace, Ref: "api/token", ProviderType: secret.ProviderDaguManaged}, time.Now())
		require.NoError(t, err)
		require.NoError(t, secrets.Create(ctx, sec, &secret.WriteValueInput{Value: layer.scope + "-token"}))
		defaults, err := profile.NewInherited(layer.defaults, profile.InheritedCreateInput{}, time.Now())
		require.NoError(t, err)
		require.NoError(t, defaults.SetVariable("TIER", layer.scope, "test", time.Now()))
		require.NoError(t, profiles.Create(ctx, defaults))
	}
}

func writeDAG(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o600); err != nil {
		t.Fatalf("write DAG: %v", err)
	}
}
