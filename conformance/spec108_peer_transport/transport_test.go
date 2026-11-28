// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec108_peer_transport_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

const (
	h2cNoSource   = "Peer transport: h2c (from no TLS source)"
	h2cInsecure   = "Peer transport: h2c (from peer.insecure: true)"
	ignoredFileCA = "Peer TLS setting ignored: peer.client_ca_file (/etc/dagu/ca.crt) has no effect while peer.insecure is not false; set peer.insecure: false to use it"
)

// A worker logs the transport it uses, and what selected it, at start
// (Spec 108 §Start Line).
func TestWorkerStartLine(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "NoSetting", want: h2cNoSource},
		{name: "InsecureTrue", args: []string{"--peer.insecure"}, want: h2cInsecure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dagu := harness.NewRunner(t)
			worker := startWorker(t, dagu, tc.args...)
			waitForOutput(t, worker, tc.want)
		})
	}
}

// A worker takes --peer.ca-cert-file and uses built-in TLS from it (Spec 108
// §Keys, §Start Line).
func TestWorkerCACertFileFlag(t *testing.T) {
	dagu := harness.NewRunner(t)
	dagu.Run("peer", "init", "--out", "peer").ExpectExitCode(0)
	worker := startWorker(t, dagu, "--peer.ca-cert-file=peer/ca.crt")
	waitForOutput(t, worker, "Peer transport: built-in TLS (from peer.ca_cert_file)")
}

// A TLS file without peer.insecure: false selects no TLS, as before Spec 108,
// and the worker says how to use it (Spec 108 §Ignored File).
func TestWorkerIgnoredFile(t *testing.T) {
	dagu := harness.NewRunner(t)
	worker := startWorker(t, dagu, "--config", dagu.ProjectPath("client-ca.yaml"))
	output := waitForOutput(t, worker, h2cNoSource)
	warning := strings.Index(output, ignoredFileCA)
	assert.GreaterOrEqual(t, warning, 0, output)
	assert.Less(t, warning, strings.Index(output, h2cNoSource), "the warning comes before the start line")
}

// peer.insecure: false without a TLS setting still fails at start (Spec 108
// §Client Transport).
func TestWorkerInsecureFalseWithoutTLS(t *testing.T) {
	dagu := harness.NewRunner(t)
	result := dagu.Run("worker", "--worker.health-port=0",
		fmt.Sprintf("--worker.coordinators=127.0.0.1:%d", harness.FreePort(t)),
		"--peer.insecure=false",
	)
	result.ExpectExitCode(1)
	assert.Contains(t, result.Stderr(), "TLS enabled but no certificates provided")
	assert.NotContains(t, result.Stderr(), "Peer transport:")
}

// startWorker starts a worker whose coordinator does not answer, which is
// enough for its start line.
func startWorker(t *testing.T, dagu *harness.Runner, args ...string) *harness.Process {
	t.Helper()
	return dagu.StartWithEnv(nil, append([]string{
		"worker",
		"--worker.health-port=0",
		fmt.Sprintf("--worker.coordinators=127.0.0.1:%d", harness.FreePort(t)),
	}, args...)...)
}
