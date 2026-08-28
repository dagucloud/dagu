// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseContextTimeout_Strict(t *testing.T) {
	t.Parallel()

	command := &cobra.Command{Use: "context"}
	initFlags(command, contextTimeoutFlag)
	ctx := &Context{Command: command}

	require.NoError(t, command.Flags().Set("timeout", "15"))
	timeout, err := parseContextTimeout(ctx)
	require.NoError(t, err)
	assert.Equal(t, 15, timeout)

	require.NoError(t, command.Flags().Set("timeout", "abc"))
	_, err = parseContextTimeout(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be an integer")
}

func TestReadContextInput_RequiresServerAndAPIKey(t *testing.T) {
	t.Parallel()

	command := &cobra.Command{Use: "context"}
	initFlags(command, contextManageFlags...)
	ctx := &Context{
		Command:      command,
		ContextStore: mustTestContextStore(t),
	}

	_, err := readContextInput(ctx, "prod", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--server is required")

	require.NoError(t, command.Flags().Set("server", "https://example.com"))
	_, err = readContextInput(ctx, "prod", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--api-key is required")
}

func TestContextServerFlagDescribesAPIBaseURL(t *testing.T) {
	t.Parallel()

	command := contextAddCommand()
	flag := command.Flags().Lookup("server")
	require.NotNil(t, flag)
	assert.Contains(t, flag.Usage, "API base URL")
}

func TestContextUpdate_CanClearDescriptionWithoutOverwritingOtherFields(t *testing.T) {
	t.Parallel()

	store := mustTestContextStore(t)
	require.NoError(t, store.Create(t.Context(), &cliContext{
		Name:           "prod",
		ServerURL:      "https://example.com",
		APIKey:         "dagu_test_123",
		Description:    "original",
		TimeoutSeconds: 30,
	}))

	command := &cobra.Command{Use: "context"}
	initFlags(command, contextManageFlags...)
	require.NoError(t, command.Flags().Set("description", ""))

	current, err := store.Get(t.Context(), "prod")
	require.NoError(t, err)

	ctx := &Context{
		Command:      command,
		ContextStore: store,
	}
	item, err := readContextInput(ctx, "prod", true)
	require.NoError(t, err)

	if !ctx.Command.Flags().Changed("server") {
		item.ServerURL = current.ServerURL
	}
	if !ctx.Command.Flags().Changed("api-key") {
		item.APIKey = current.APIKey
	}
	if !ctx.Command.Flags().Changed("description") {
		item.Description = current.Description
	}
	if !ctx.Command.Flags().Changed("timeout") {
		item.TimeoutSeconds = current.TimeoutSeconds
	}

	require.NoError(t, store.Update(t.Context(), item))
	updated, err := store.Get(t.Context(), "prod")
	require.NoError(t, err)
	assert.Equal(t, "", updated.Description)
	assert.Equal(t, current.ServerURL, updated.ServerURL)
	assert.Equal(t, current.APIKey, updated.APIKey)
	assert.Equal(t, current.TimeoutSeconds, updated.TimeoutSeconds)
}

func mustTestContextStore(t *testing.T) *cliContextStore {
	t.Helper()
	store, err := newCLIContextStore(t.TempDir(), t.TempDir())
	require.NoError(t, err)
	return store
}
