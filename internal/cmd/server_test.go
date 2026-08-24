// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"fmt"
	"net"
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
		cancelWhenLogContains(t, th, port)
		th.RunCommand(t, cmd.Server(frontend.WithListener(listener)), test.CmdTest{
			Args:        []string{"server", "--config", configFile},
			ExpectedOut: []string{port},
		})
	})
}

func TestServerSecondInterruptTerminatesBlockedCleanup(t *testing.T) {
	assertSecondInterruptTerminatesBlockedCleanup(t, "server", "Resource monitoring service stopped")
}

// findPort finds an available port.
func findPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return fmt.Sprintf("%d", port)
}
