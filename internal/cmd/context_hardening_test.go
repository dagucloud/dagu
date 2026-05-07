// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/clicontext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
)

func TestNewContext_StaticCommandIgnoresBrokenContextStore(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	configPath := writeTestConfig(t, home, filepath.Join(home, "blocked-contexts"))
	require.NoError(t, os.WriteFile(filepath.Join(home, "blocked-contexts"), []byte("x"), 0o600))

	command := &cobra.Command{Use: "schema"}
	initFlags(command)
	command.Flags().String("context", "", "")
	command.SetContext(context.Background())
	require.NoError(t, command.Flags().Set("dagu-home", home))
	require.NoError(t, command.Flags().Set("config", configPath))

	ctx, err := NewContext(command, nil)
	require.NoError(t, err)
	assert.Nil(t, ctx.ContextStore)
	assert.Equal(t, clicontext.LocalContextName, ctx.ContextName)
}

func TestNewContext_CommandWithoutContextFlagDefaultsToLocal(t *testing.T) {
	t.Parallel()

	home := t.TempDir()

	command := &cobra.Command{Use: "status"}
	initFlags(command)
	command.SetContext(context.Background())
	require.NoError(t, command.Flags().Set("dagu-home", home))

	ctx, err := NewContext(command, nil)
	require.NoError(t, err)
	assert.Equal(t, clicontext.LocalContextName, ctx.ContextName)
	assert.False(t, ctx.IsRemote())
}

func TestNewContext_ContextSubcommandInitializesContextStore(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	parent := &cobra.Command{Use: "context"}
	command := &cobra.Command{Use: "list"}
	parent.AddCommand(command)
	initFlags(command)
	command.SetContext(context.Background())
	require.NoError(t, command.Flags().Set("dagu-home", home))

	ctx, err := NewContext(command, nil)
	require.NoError(t, err)
	require.NotNil(t, ctx.ContextStore)
	assert.Equal(t, clicontext.LocalContextName, ctx.ContextName)
}

func TestNewContext_FallsBackToLocalWhenCurrentContextCannotResolve(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	contextsDir := filepath.Join(home, "contexts")
	require.NoError(t, os.MkdirAll(contextsDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(contextsDir, "current"), []byte("missing"), 0o600))

	command := &cobra.Command{Use: "status"}
	initFlags(command)
	command.Flags().String("context", "", "")
	command.SetContext(context.Background())
	require.NoError(t, command.Flags().Set("dagu-home", home))

	ctx, err := NewContext(command, nil)
	require.NoError(t, err)
	assert.Equal(t, clicontext.LocalContextName, ctx.ContextName)
	assert.False(t, ctx.IsRemote())
}

func TestNewContext_LocalExplicitSurvivesBrokenContextStore(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	configPath := writeTestConfig(t, home, filepath.Join(home, "blocked-contexts"))
	require.NoError(t, os.WriteFile(filepath.Join(home, "blocked-contexts"), []byte("x"), 0o600))

	command := &cobra.Command{Use: "status"}
	initFlags(command)
	command.Flags().String("context", "", "")
	command.SetContext(context.Background())
	require.NoError(t, command.Flags().Set("dagu-home", home))
	require.NoError(t, command.Flags().Set("config", configPath))
	require.NoError(t, command.Flags().Set("context", "local"))

	ctx, err := NewContext(command, nil)
	require.NoError(t, err)
	assert.Equal(t, clicontext.LocalContextName, ctx.ContextName)
	assert.False(t, ctx.IsRemote())
}

func TestNewContext_RemoteExplicitFailsWhenContextStoreUnavailable(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	configPath := writeTestConfig(t, home, filepath.Join(home, "blocked-contexts"))
	require.NoError(t, os.WriteFile(filepath.Join(home, "blocked-contexts"), []byte("x"), 0o600))

	command := &cobra.Command{Use: "status"}
	initFlags(command)
	command.Flags().String("context", "", "")
	command.SetContext(context.Background())
	require.NoError(t, command.Flags().Set("dagu-home", home))
	require.NoError(t, command.Flags().Set("config", configPath))
	require.NoError(t, command.Flags().Set("context", "prod"))

	_, err := NewContext(command, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to initialize context store")
}

func TestNewContext_DefersPostgresControlPlaneStoreForStaticCommands(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	configPath := writeRawTestConfig(t, `
control_plane_store:
  backend: postgres
  postgres:
    scheduler:
      dsn: postgres://dagu:dagu@127.0.0.1:1/dagu?sslmode=disable
`)

	for _, name := range []string{"config"} {
		t.Run(name, func(t *testing.T) {
			command := &cobra.Command{Use: name}
			initFlags(command)
			command.SetContext(context.Background())
			require.NoError(t, command.Flags().Set("dagu-home", home))
			require.NoError(t, command.Flags().Set("config", configPath))

			ctx, err := NewContext(command, nil)
			require.NoError(t, err)
			assert.Nil(t, ctx.ControlPlaneStore)
			assert.Nil(t, ctx.DAGRunStore)
			assert.Zero(t, ctx.DAGRunMgr)
		})
	}
}

func TestNewContext_UsesFileDAGRunStoreWithoutAggregateForDAGRunCommands(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	command := &cobra.Command{Use: "status"}
	initFlags(command)
	command.SetContext(context.Background())
	require.NoError(t, command.Flags().Set("dagu-home", home))

	ctx, err := NewContext(command, nil)
	require.NoError(t, err)
	assert.Nil(t, ctx.ControlPlaneStore)
	assert.NotNil(t, ctx.DAGRunStore)
	assert.NotNil(t, ctx.DAGRunMgr)
}

func TestNewContext_InitializesFileControlPlaneStoreForServer(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	command := &cobra.Command{Use: "server"}
	initFlags(command)
	command.SetContext(context.Background())
	require.NoError(t, command.Flags().Set("dagu-home", home))

	ctx, err := NewContext(command, nil)
	require.NoError(t, err)
	assert.NotNil(t, ctx.ControlPlaneStore)
	assert.NotNil(t, ctx.DAGRunStore)
	assert.NotNil(t, ctx.DAGRunMgr)
}

func TestNewContext_DefersPostgresAgentStoreForDispatchableCommands(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	configPath := writeRawTestConfig(t, `
control_plane_store:
  backend: postgres
  postgres:
    agent:
      dsn: postgres://dagu:dagu@127.0.0.1:1/dagu?sslmode=disable
`)

	for _, name := range []string{"start", "exec"} {
		t.Run(name, func(t *testing.T) {
			command := &cobra.Command{Use: name}
			initFlags(command)
			command.SetContext(context.Background())
			require.NoError(t, command.Flags().Set("dagu-home", home))
			require.NoError(t, command.Flags().Set("config", configPath))

			ctx, err := NewContext(command, nil)
			require.NoError(t, err)
			assert.Nil(t, ctx.ControlPlaneStore)
			assert.Nil(t, ctx.DAGRunStore)
			assert.Zero(t, ctx.DAGRunMgr)
		})
	}
}

func TestNewContext_PostgresSharedNothingWorkerSkipsAgentStore(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	configPath := writeRawTestConfig(t, `
worker:
  coordinators:
    - 127.0.0.1:50055
control_plane_store:
  backend: postgres
  postgres:
    agent:
      dsn: postgres://dagu:dagu@127.0.0.1:1/dagu?sslmode=disable
`)
	command := &cobra.Command{Use: "worker"}
	initFlags(command)
	command.SetContext(context.Background())
	require.NoError(t, command.Flags().Set("dagu-home", home))
	require.NoError(t, command.Flags().Set("config", configPath))

	ctx, err := NewContext(command, nil)

	require.NoError(t, err)
	assert.Nil(t, ctx.ControlPlaneStore)
	assert.Nil(t, ctx.DAGRunStore)
	assert.Zero(t, ctx.DAGRunMgr)
}

func TestTryExecuteDAGRejectsLocalPostgresWithoutDirectAccess(t *testing.T) {
	t.Parallel()

	command := &cobra.Command{Use: "start"}
	ctx := &Context{
		Context: context.Background(),
		Command: command,
		Config: &config.Config{
			ControlPlaneStore: config.ControlPlaneStoreConfig{
				Backend: config.ControlPlaneStoreBackendPostgres,
			},
		},
	}

	err := tryExecuteDAG(
		ctx,
		&core.DAG{Name: "example"},
		"run-1",
		exec.NewDAGRunRef("example", "run-1"),
		"local",
		"",
		core.TriggerTypeManual,
		"",
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "control_plane_store.postgres.agent.direct_access=true")
}

func writeTestConfig(t *testing.T, home, contextsDir string) string {
	t.Helper()
	configPath := filepath.Join(home, "config.yaml")
	content := "paths:\n  contexts_dir: " + contextsDir + "\n"
	require.NoError(t, os.WriteFile(configPath, []byte(content), 0o600))
	return configPath
}

func writeRawTestConfig(t *testing.T, content string) string {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(content), 0o600))
	return configPath
}
