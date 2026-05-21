// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmd"
	cmdprocess "github.com/dagucloud/dagu/internal/cmd/process"
	"github.com/dagucloud/dagu/internal/cmn/config"
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

func TestBuildCoordinatorClientConfig(t *testing.T) {
	t.Parallel()

	t.Run("EmptyCoordinatorsReturnsNil", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			Worker: config.Worker{
				Coordinators: []string{},
			},
		}
		result, useRemote, err := cmdprocess.BuildWorkerCoordinatorClientConfig(cfg)
		assert.NoError(t, err)
		assert.Nil(t, result)
		assert.False(t, useRemote)
	})

	t.Run("NilCoordinatorsReturnsNil", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			Worker: config.Worker{
				Coordinators: nil,
			},
		}
		result, useRemote, err := cmdprocess.BuildWorkerCoordinatorClientConfig(cfg)
		assert.NoError(t, err)
		assert.Nil(t, result)
		assert.False(t, useRemote)
	})

	t.Run("StaticCoordinatorsReturnsConfig", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			Worker: config.Worker{
				Coordinators: []string{"localhost:50055"},
			},
			Core: config.Core{
				Peer: config.Peer{
					Insecure: true,
				},
			},
		}
		result, useRemote, err := cmdprocess.BuildWorkerCoordinatorClientConfig(cfg)
		assert.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, useRemote)
		assert.True(t, result.Insecure)
	})

	t.Run("StaticCoordinatorsPreservePeerRetryConfig", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			Worker: config.Worker{
				Coordinators: []string{"localhost:50055"},
			},
			Core: config.Core{
				Peer: config.Peer{
					Insecure:      true,
					MaxRetries:    7,
					RetryInterval: 3 * time.Second,
				},
			},
		}
		result, useRemote, err := cmdprocess.BuildWorkerCoordinatorClientConfig(cfg)
		assert.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, useRemote)
		assert.Equal(t, 7, result.MaxRetries)
		assert.Equal(t, 3*time.Second, result.RetryInterval)
	})

	t.Run("TLSValidationFailure", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			Worker: config.Worker{
				Coordinators: []string{"localhost:50055"},
			},
			Core: config.Core{
				Peer: config.Peer{
					Insecure: false,
					// Missing CertFile and KeyFile - should fail validation
				},
			},
		}
		_, _, err := cmdprocess.BuildWorkerCoordinatorClientConfig(cfg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid coordinator client configuration")
	})

	t.Run("ValidTLSConfig", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			Worker: config.Worker{
				Coordinators: []string{"localhost:50055"},
			},
			Core: config.Core{
				Peer: config.Peer{
					Insecure:     false,
					CertFile:     "/path/to/cert.pem",
					KeyFile:      "/path/to/key.pem",
					ClientCaFile: "/path/to/ca.pem",
				},
			},
		}
		result, useRemote, err := cmdprocess.BuildWorkerCoordinatorClientConfig(cfg)
		assert.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, useRemote)
		assert.Equal(t, "/path/to/cert.pem", result.CertFile)
		assert.Equal(t, "/path/to/key.pem", result.KeyFile)
		assert.Equal(t, "/path/to/ca.pem", result.CAFile)
	})

	t.Run("SkipTLSVerifyConfig", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			Worker: config.Worker{
				Coordinators: []string{"localhost:50055"},
			},
			Core: config.Core{
				Peer: config.Peer{
					Insecure:      false,
					CertFile:      "/path/to/cert.pem",
					KeyFile:       "/path/to/key.pem",
					SkipTLSVerify: true,
				},
			},
		}
		result, useRemote, err := cmdprocess.BuildWorkerCoordinatorClientConfig(cfg)
		assert.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, useRemote)
		assert.True(t, result.SkipTLSVerify)
	})

	t.Run("MultipleCoordinators", func(t *testing.T) {
		t.Parallel()

		cfg := &config.Config{
			Worker: config.Worker{
				Coordinators: []string{"coord1:50055", "coord2:50055", "coord3:50055"},
			},
			Core: config.Core{
				Peer: config.Peer{
					Insecure: true,
				},
			},
		}
		result, useRemote, err := cmdprocess.BuildWorkerCoordinatorClientConfig(cfg)
		assert.NoError(t, err)
		require.NotNil(t, result)
		assert.True(t, useRemote)
	})
}
