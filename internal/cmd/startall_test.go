// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"crypto/x509"
	"fmt"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/dagucloud/dagu/v2/internal/test/peercert"
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
	// The scheduler and the server share one process, which warns once.
	t.Run("WarnTestNamedDAG", func(t *testing.T) {
		th := test.SetupCommand(t)
		th.CreateDAGFile(t, "smoke.test.yaml", testNamedDAG)
		cancelWhenLogContains(t, th, "Scheduler started", "Server is starting")
		th.RunCommand(t, cmd.StartAll(), test.CmdTest{
			Args: []string{"start-all", fmt.Sprintf("--port=%s", findPort(t))},
		})
		require.Equal(t, 1, strings.Count(th.LoggingOutput.String(), "smoke.test.yaml reads as a DAG"))
	})
	// start-all dials its own coordinator with peer.cert_file, which mTLS
	// refuses when the certificate lacks client authentication. It warns and
	// still starts, since local runs do not dial.
	t.Run("WarnServerOnlyCertificate", func(t *testing.T) {
		th := test.SetupCommand(t, test.WithCoordinatorEnabled())
		certFile, keyFile := peercert.Write(t, t.TempDir(), peercert.Options{
			Name:        "server",
			DNSNames:    []string{"localhost"},
			ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		})
		cancelWhenLogContains(t, th, "Scheduler started", coordinatorServing)
		th.RunCommand(t, cmd.StartAll(), test.CmdTest{
			Args: []string{
				"start-all",
				fmt.Sprintf("--port=%s", findPort(t)),
				fmt.Sprintf("--coordinator.port=%s", findPort(t)),
				"--coordinator.advertise=localhost",
				"--peer.insecure=false",
				"--peer.cert-file=" + certFile,
				"--peer.key-file=" + keyFile,
				"--peer.client-ca-file=" + certFile,
			},
			ExpectedOut: []string{"Peer certificate does not allow client authentication"},
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
