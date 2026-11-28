// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"fmt"
	"math/rand/v2"
	"net"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/service/frontend"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/require"
)

func TestServerCommand(t *testing.T) {
	t.Run("StartServer", func(t *testing.T) {
		th := test.SetupCommand(t)
		cancelWhenLogContains(t, th, "Server is starting")
		listener, port := test.ReserveServerListener(t)
		th.RunCommand(t, cmd.Server(frontend.WithListener(listener)), test.CmdTest{
			Args:        []string{"server", fmt.Sprintf("--port=%s", port)},
			ExpectedOut: []string{"Server is starting", port},
		})

	})
	t.Run("StartServerWithConfig", func(t *testing.T) {
		th := test.SetupCommand(t)
		listener, port := test.ReserveServerListener(t)
		configFile := th.TempFile(t, "server-config.yaml", fmt.Appendf(nil, "host: 127.0.0.1\nport: %s\n", port))
		cancelWhenLogContains(t, th, "Server is starting")
		th.RunCommand(t, cmd.Server(frontend.WithListener(listener)), test.CmdTest{
			Args:        []string{"server", "--config", configFile, "--dagu-home", filepath.Dir(th.Config.Paths.DataDir)},
			ExpectedOut: []string{port},
		})
	})
	t.Run("WarnTestNamedDAG", func(t *testing.T) {
		th := test.SetupCommand(t)
		th.CreateDAGFile(t, "smoke.test.yaml", testNamedDAG)
		cancelWhenLogContains(t, th, "Server is starting")
		listener, port := test.ReserveServerListener(t)
		th.RunCommand(t, cmd.Server(frontend.WithListener(listener)), test.CmdTest{
			Args:        []string{"server", fmt.Sprintf("--port=%s", port)},
			ExpectedOut: []string{"smoke.test.yaml reads as a DAG"},
		})
	})
	t.Run("PortInUse", func(t *testing.T) {
		th := test.SetupCommand(t)
		_, port := test.ReserveServerListener(t)
		err := commandError(t, th, cmd.Server(), "server", "--host=127.0.0.1", "--port="+port)
		require.ErrorContains(t, err, "--port")
		require.ErrorContains(t, err, "DAGU_PORT")
	})
}

func TestServerSecondInterruptTerminatesBlockedCleanup(t *testing.T) {
	assertSecondInterruptTerminatesBlockedCleanup(t, "server", "Resource monitoring service stopped")
}

// Test service ports come from below the ephemeral port ranges of Linux
// (32768 and up), macOS, and Windows (49152 and up). A port the kernel hands
// out to a port-0 bind is in that range, where an outgoing connection of a
// parallel test can take it before the service binds it.
const (
	testPortMin      = 10000
	testPortMax      = 32000
	testPortAttempts = 100
)

// findPort finds an available port.
func findPort(t *testing.T) string {
	t.Helper()
	for range testPortAttempts {
		port := testPortMin + rand.IntN(testPortMax-testPortMin) //nolint:gosec // Spreads test ports; needs no unpredictability.
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			continue
		}
		require.NoError(t, ln.Close())
		return fmt.Sprintf("%d", port)
	}
	t.Fatalf("no free TCP port between %d and %d", testPortMin, testPortMax)
	return ""
}
