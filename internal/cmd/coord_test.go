// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

func TestCoordinatorCommand(t *testing.T) {
	t.Run("CoordinatorCommandHasHealthPortFlag", func(t *testing.T) {
		cli := CmdCoordinator()
		require.NotNil(t, cli.Flags().Lookup("coordinator.health-port"))
	})

	t.Run("StartCoordinator", func(t *testing.T) {
		th := test.SetupCommand(t)
		cancelCoordinatorWhenLogContains(t, th, "Coordinator initialization")
		listener, port := reserveCoordinatorListener(t)
		th.RunCommand(t, cmdCoordinator(listener), test.CmdTest{
			Args:        []string{"coordinator", fmt.Sprintf("--coordinator.port=%s", port)},
			ExpectedOut: []string{"Coordinator initialization", port},
		})
	})

	t.Run("StartCoordinatorWithHost", func(t *testing.T) {
		th := test.SetupCommand(t)
		cancelCoordinatorWhenLogContains(t, th, "Coordinator initialization")
		listener, port := reserveCoordinatorListener(t)
		th.RunCommand(t, cmdCoordinator(listener), test.CmdTest{
			Args:        []string{"coordinator", "--coordinator.host=0.0.0.0", fmt.Sprintf("--coordinator.port=%s", port)},
			ExpectedOut: []string{"Coordinator initialization", "0.0.0.0", port},
		})
	})

	t.Run("StartCoordinatorWithConfig", func(t *testing.T) {
		th := test.SetupCommand(t)
		cancelCoordinatorWhenLogContains(t, th, "Coordinator initialization")
		listener, port := reserveCoordinatorListener(t)
		configFile := th.TempFile(t, "coordinator-config.yaml", fmt.Appendf(nil, "coordinator:\n  host: 127.0.0.1\n  port: %s\n", port))
		th.RunCommand(t, cmdCoordinator(listener), test.CmdTest{
			Args:        []string{"coordinator", "--config", configFile},
			ExpectedOut: []string{"Coordinator initialization", port},
		})
	})

	t.Run("StartCoordinatorWithTLS", func(t *testing.T) {
		th := test.SetupCommand(t)
		cancelCoordinatorWhenLogContains(t, th, "Coordinator initialization")
		listener, port := reserveCoordinatorListener(t)
		th.RunCommand(t, cmdCoordinator(listener), test.CmdTest{
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
		cancelCoordinatorWhenLogContains(t, th, "Coordinator initialization")
		listener, port := reserveCoordinatorListener(t)
		th.RunCommand(t, cmdCoordinator(listener), test.CmdTest{
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
		cancelCoordinatorWhenLogContains(t, th, "Coordinator initialization")
		listener, port := reserveCoordinatorListener(t)
		th.RunCommand(t, cmdCoordinator(listener), test.CmdTest{
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

func reserveCoordinatorListener(t *testing.T) (net.Listener, string) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = listener.Close()
	})
	return listener, fmt.Sprintf("%d", listener.Addr().(*net.TCPAddr).Port)
}

func cancelCoordinatorWhenLogContains(t *testing.T, th test.Command, want string) {
	t.Helper()

	done := make(chan bool, 1)
	go func() {
		deadline := time.Now().Add(coordinatorLogWaitTimeout())
		for time.Now().Before(deadline) {
			if strings.Contains(th.LoggingOutput.String(), want) {
				th.Cancel()
				done <- true
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		th.Cancel()
		done <- false
	}()

	t.Cleanup(func() {
		require.True(t, <-done, "startup log never appeared: %s", want)
	})
}

func coordinatorLogWaitTimeout() time.Duration {
	if runtime.GOOS == "windows" {
		return 30 * time.Second
	}
	return 10 * time.Second
}
