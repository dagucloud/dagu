// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"fmt"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

func TestCoordinatorCommand(t *testing.T) {
	t.Run("CoordinatorCommandHasHealthPortFlag", func(t *testing.T) {
		cli := cmd.CmdCoordinator()
		require.NotNil(t, cli.Flags().Lookup("coordinator.health-port"))
	})

	t.Run("StartCoordinator", func(t *testing.T) {
		th := test.SetupCommand(t)
		cancelWhenLogContains(t, th, "Coordinator initialization")
		port := findPort(t)
		th.RunCommand(t, cmd.CmdCoordinator(), test.CmdTest{
			Args:        []string{"coordinator", fmt.Sprintf("--coordinator.port=%s", port)},
			ExpectedOut: []string{"Coordinator initialization", port},
		})
	})

	t.Run("StartCoordinatorWithHost", func(t *testing.T) {
		th := test.SetupCommand(t)
		cancelWhenLogContains(t, th, "Coordinator initialization")
		port := findPort(t)
		th.RunCommand(t, cmd.CmdCoordinator(), test.CmdTest{
			Args:        []string{"coordinator", "--coordinator.host=0.0.0.0", fmt.Sprintf("--coordinator.port=%s", port)},
			ExpectedOut: []string{"Coordinator initialization", "0.0.0.0", port},
		})
	})

	t.Run("StartCoordinatorWithConfig", func(t *testing.T) {
		th := test.SetupCommand(t)
		cancelWhenLogContains(t, th, "Coordinator initialization")
		th.RunCommand(t, cmd.CmdCoordinator(), test.CmdTest{
			Args:        []string{"coordinator", "--config", test.TestdataPath(t, "cli/config_test.yaml")},
			ExpectedOut: []string{"Coordinator initialization", "9876"},
		})
	})

	t.Run("StartCoordinatorWithTLS", func(t *testing.T) {
		th := test.SetupCommand(t)
		cancelWhenLogContains(t, th, "Coordinator initialization")
		port := findPort(t)
		th.RunCommand(t, cmd.CmdCoordinator(), test.CmdTest{
			Args: []string{
				"coordinator",
				fmt.Sprintf("--coordinator.port=%s", port),
				fmt.Sprintf("--peer.cert-file=%s", test.TestdataPath(t, "certs/cert.pem")),
				fmt.Sprintf("--peer.key-file=%s", test.TestdataPath(t, "certs/key.pem")),
			},
			ExpectedOut: []string{"Coordinator initialization", port},
		})
	})

	t.Run("StartCoordinatorWithMutualTLS", func(t *testing.T) {
		th := test.SetupCommand(t)
		cancelWhenLogContains(t, th, "Coordinator initialization")
		port := findPort(t)
		th.RunCommand(t, cmd.CmdCoordinator(), test.CmdTest{
			Args: []string{
				"coordinator",
				fmt.Sprintf("--coordinator.port=%s", port),
				fmt.Sprintf("--peer.cert-file=%s", test.TestdataPath(t, "certs/cert.pem")),
				fmt.Sprintf("--peer.key-file=%s", test.TestdataPath(t, "certs/key.pem")),
				fmt.Sprintf("--peer.client-ca-file=%s", test.TestdataPath(t, "certs/ca.pem")),
			},
			ExpectedOut: []string{"Coordinator initialization", port},
		})
	})

	t.Run("StartCoordinatorWithAdvertiseAddress", func(t *testing.T) {
		th := test.SetupCommand(t)
		cancelWhenLogContains(t, th, "Coordinator initialization")
		port := findPort(t)
		th.RunCommand(t, cmd.CmdCoordinator(), test.CmdTest{
			Args: []string{
				"coordinator",
				"--coordinator.host=0.0.0.0",
				"--coordinator.advertise=dagu-server",
				fmt.Sprintf("--coordinator.port=%s", port),
			},
			ExpectedOut: []string{"Coordinator initialization", "bind-address=0.0.0.0", "advertise-address=dagu-server", port},
		})
	})
}
