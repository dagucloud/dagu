// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/service/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkerCommand(t *testing.T) {
	t.Run("WorkerCommandExists", func(t *testing.T) {
		cli := cmd.CmdWorker()
		require.NotNil(t, cli)
		require.Equal(t, "worker [flags]", cli.Use)
		require.Equal(t, "Start a worker that polls the coordinator for tasks", cli.Short)
	})

	t.Run("WorkerCommandHasExpectedFlags", func(t *testing.T) {
		cli := cmd.CmdWorker()
		require.NotNil(t, cli)

		// Verify expected flags are registered
		flags := cli.Flags()
		require.NotNil(t, flags)

		// Check worker-specific flags exist (note: they may be prefixed)
		// The actual flag names depend on how they're registered
		assert.NotEmpty(t, cli.Long, "Long description should be set")
		require.NotNil(t, flags.Lookup("worker.health-port"))
		coordinatorsFlag := flags.Lookup("worker.coordinators")
		require.NotNil(t, coordinatorsFlag)
		assert.Contains(t, coordinatorsFlag.Usage, "Required")
	})

	t.Run("WorkerCommandLongDescriptionContainsUsageInfo", func(t *testing.T) {
		cli := cmd.CmdWorker()
		require.NotNil(t, cli)

		// Verify the long description contains important usage info
		assert.Contains(t, cli.Long, "worker ID")
		assert.Contains(t, cli.Long, "coordinator")
		assert.Contains(t, cli.Long, "TLS")
		assert.Contains(t, cli.Long, "labels")
		assert.Contains(t, cli.Long, "health")
	})

	t.Run("WorkerCommandExamples", func(t *testing.T) {
		cli := cmd.CmdWorker()
		require.NotNil(t, cli)

		// Verify examples are present in long description
		assert.Contains(t, cli.Long, "Example:")
		assert.Contains(t, cli.Long, "dagu worker")
	})
}

func TestWorkerContextSkipsLocalPersistence(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	blockedProcDir := filepath.Join(home, "blocked-proc")
	require.NoError(t, os.WriteFile(blockedProcDir, []byte("not a directory"), 0o600))
	configPath := filepath.Join(home, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, fmt.Appendf(nil, `
paths:
  proc_dir: %q
`, blockedProcDir), 0o600))

	command := cmd.CmdWorker()
	command.SetContext(t.Context())
	require.NoError(t, command.Flags().Set("dagu-home", home))
	require.NoError(t, command.Flags().Set("config", configPath))

	ctx, err := cmd.NewContext(command, nil)
	require.NoError(t, err)
	assert.Zero(t, ctx.Persistence)
}

func TestWorkerCoordinatorClientRequiresAddress(t *testing.T) {
	t.Parallel()

	client, err := worker.NewCoordinatorClient(t.Context(), &config.Config{})

	require.ErrorContains(t, err, "worker.coordinators is required")
	assert.Nil(t, client)
}
