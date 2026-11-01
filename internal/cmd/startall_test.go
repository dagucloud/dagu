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

func TestStartAllCommand(t *testing.T) {
	t.Run("StartAll", func(t *testing.T) {
		th := test.SetupCommand(t, test.WithCoordinatorEnabled())
		cancelWhenLogContains(t, th, "Scheduler initialization", "Coordinator initialization")
		th.RunCommand(t, cmd.StartAll(), test.CmdTest{
			Args: []string{
				"start-all",
				fmt.Sprintf("--port=%s", findPort(t)),
				"--coordinator.host=0.0.0.0",
				fmt.Sprintf("--coordinator.port=%s", findPort(t)),
			},
			ExpectedOut: []string{"Server initialization", "Scheduler initialization", "Coordinator initialization", "Scheduler stopped"},
		})

	})
	t.Run("StartAllWithConfig", func(t *testing.T) {
		th := test.SetupCommand(t)
		cancelWhenLogContains(t, th, "Scheduler initialization", "Coordinator initialization")
		th.RunCommand(t, cmd.StartAll(), test.CmdTest{
			Args: []string{
				"start-all",
				"--config", test.TestdataPath(t, "cli/config_startall.yaml"),
				fmt.Sprintf("--coordinator.port=%s", findPort(t)),
			},
			ExpectedOut: []string{"54322", "dagu_test", "Coordinator initialization"},
		})
	})
	t.Run("WebPortInUse", func(t *testing.T) {
		th := test.SetupCommand(t)
		_, port := test.ReserveServerListener(t)
		err := commandError(t, th, cmd.StartAll(), "start-all", "--host=127.0.0.1", "--port="+port)
		require.ErrorContains(t, err, "--port")
		require.ErrorContains(t, err, "DAGU_PORT")
	})
	t.Run("CoordinatorPortInUse", func(t *testing.T) {
		th := test.SetupCommand(t, test.WithCoordinatorEnabled())
		_, coordinatorPort := test.ReserveServerListener(t)
		err := commandError(t, th, cmd.StartAll(),
			"start-all",
			"--host=127.0.0.1",
			"--port="+findPort(t),
			"--coordinator.host=127.0.0.1",
			"--coordinator.port="+coordinatorPort,
		)
		require.ErrorContains(t, err, "DAGU_COORDINATOR_ENABLED=false")
	})
}

func TestStartAllSecondInterruptTerminatesBlockedCleanup(t *testing.T) {
	assertSecondInterruptTerminatesBlockedCleanup(t, "start-all", "All services stopped gracefully")
}
