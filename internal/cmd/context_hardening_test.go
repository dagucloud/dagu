// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	assert.Equal(t, localContextName, ctx.ContextName)
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
	assert.Equal(t, localContextName, ctx.ContextName)
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
	assert.Equal(t, localContextName, ctx.ContextName)
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
	assert.Equal(t, localContextName, ctx.ContextName)
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
	assert.Equal(t, localContextName, ctx.ContextName)
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

func TestNewContext_RemoteSkipsLocalEventStore(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	contextsDir := filepath.Join(home, "contexts")
	eventStoreDir := filepath.Join(home, "events")
	store, err := newCLIContextStore(filepath.Join(home, "data"), contextsDir)
	require.NoError(t, err)
	require.NoError(t, store.Create(t.Context(), &cliContext{
		Name:      "prod",
		ServerURL: "https://example.com",
		APIKey:    "dagu_test_123",
	}))

	configPath := filepath.Join(home, "config.yaml")
	configData := "paths:\n  contexts_dir: " + contextsDir + "\n  event_store_dir: " + eventStoreDir + "\nevent_store:\n  enabled: true\n"
	require.NoError(t, os.WriteFile(configPath, []byte(configData), 0o600))

	command := &cobra.Command{Use: "status"}
	initFlags(command)
	command.Flags().String("context", "", "")
	command.SetContext(context.Background())
	require.NoError(t, command.Flags().Set("dagu-home", home))
	require.NoError(t, command.Flags().Set("config", configPath))
	require.NoError(t, command.Flags().Set("context", "prod"))

	ctx, err := NewContext(command, nil)
	require.NoError(t, err)
	require.True(t, ctx.IsRemote())
	require.NoDirExists(t, eventStoreDir)
}

func writeTestConfig(t *testing.T, home, contextsDir string) string {
	t.Helper()
	configPath := filepath.Join(home, "config.yaml")
	content := "paths:\n  contexts_dir: " + contextsDir + "\n"
	require.NoError(t, os.WriteFile(configPath, []byte(content), 0o600))
	return configPath
}
