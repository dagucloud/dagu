// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/dagucloud/dagu/v2/internal/test/peercert"
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

// The coordinator checks its own certificates at start: a key that does not
// match fails, and a certificate clients would soon fail to verify warns.
func TestCoordinatorOwnCertificate(t *testing.T) {
	const certName = "coordinator.test"

	t.Run("KeyMismatch", func(t *testing.T) {
		th := test.SetupCommand(t)
		dir := t.TempDir()
		certFile, _ := peercert.Write(t, dir, peercert.Options{Name: "server"})
		_, otherKey := peercert.Write(t, dir, peercert.Options{Name: "other"})
		err := commandError(t, th, cmd.CmdCoordinator(), coordinatorTLSArgs(t, certFile, otherKey, certName)...)
		require.ErrorContains(t, err, "peer.key_file "+otherKey+" is not the key of the certificate in peer.cert_file "+certFile)
		require.ErrorContains(t, err, "set peer.key_file to the private key of that certificate")
	})

	t.Run("Warnings", func(t *testing.T) {
		th := test.SetupCommand(t)
		certFile, keyFile := peercert.Write(t, t.TempDir(), peercert.Options{
			Name:     "server",
			DNSNames: []string{certName},
			NotAfter: time.Now().Add(7 * 24 * time.Hour),
		})
		cancelWhenLogContains(t, th, coordinatorServing)
		th.RunCommand(t, cmd.CmdCoordinator(), test.CmdTest{
			Args: coordinatorTLSArgs(t, certFile, keyFile, "other.test"),
			ExpectedOut: []string{
				"Coordinator certificate does not name the advertised address",
				"Coordinator certificate expires within 14 days",
			},
		})
	})

	t.Run("Expired", func(t *testing.T) {
		th := test.SetupCommand(t)
		certFile, keyFile := peercert.Write(t, t.TempDir(), peercert.Options{
			Name:     "server",
			DNSNames: []string{certName},
			NotAfter: time.Now().Add(-time.Minute),
		})
		cancelWhenLogContains(t, th, coordinatorServing)
		th.RunCommand(t, cmd.CmdCoordinator(), test.CmdTest{
			Args:        coordinatorTLSArgs(t, certFile, keyFile, certName),
			ExpectedOut: []string{"Coordinator certificate has expired"},
		})
	})

	t.Run("Valid", func(t *testing.T) {
		th := test.SetupCommand(t)
		certFile, keyFile := peercert.Write(t, t.TempDir(), peercert.Options{Name: "server", DNSNames: []string{certName}})
		cancelWhenLogContains(t, th, coordinatorServing)
		th.RunCommand(t, cmd.CmdCoordinator(), test.CmdTest{Args: coordinatorTLSArgs(t, certFile, keyFile, certName)})
		require.NotContains(t, th.LoggingOutput.String(), "Coordinator certificate")
	})
}

// coordinatorServing is logged once the coordinator has passed its start
// checks and serves.
const coordinatorServing = "Starting to serve on coordinator service"

// coordinatorTLSArgs returns the arguments of a coordinator that serves TLS
// with certFile and keyFile and advertises advertise.
func coordinatorTLSArgs(t *testing.T, certFile, keyFile, advertise string) []string {
	t.Helper()
	return []string{
		"coordinator",
		"--coordinator.port=" + findPort(t),
		"--coordinator.advertise=" + advertise,
		"--peer.cert-file=" + certFile,
		"--peer.key-file=" + keyFile,
	}
}
