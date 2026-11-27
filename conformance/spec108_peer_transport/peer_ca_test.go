// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec108_peer_transport_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var peerFiles = []string{"ca.crt", "ca.key", "token"}

func TestPeerInit(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)

	result := dagu.Run("peer", "init", "--out", "peer")
	result.ExpectExitCode(0)
	var want string
	for _, name := range peerFiles {
		want += filepath.Join("peer", name) + "\n"
	}
	result.ExpectStdout(want)
	token, err := os.ReadFile(dagu.ProjectPath("peer/token"))
	require.NoError(t, err)
	assert.Regexp(t, regexp.MustCompile(`^[0-9a-f]{64}\n$`), string(token))

	// A second run writes no file over the first one's.
	before := readPeerFiles(t, dagu)
	result = dagu.Run("peer", "init", "--out", "peer")
	result.ExpectExitCode(1)
	result.ExpectStderrContains(filepath.Join("peer", "ca.crt") + " exists; dagu peer init writes no file over an existing one")
	assert.Equal(t, before, readPeerFiles(t, dagu))
}

// peer init writes none of its files when any one of them exists.
func TestPeerInitOneFileExists(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.WriteFile("peer/token", "keep\n")

	result := dagu.Run("peer", "init", "--out", "peer")
	result.ExpectExitCode(1)
	result.ExpectStderrContains(filepath.Join("peer", "token") + " exists")
	dagu.ExpectNoFile("peer/ca.crt")
	dagu.ExpectNoFile("peer/ca.key")
	dagu.ExpectFileContent("peer/token", "keep\n")
}

func TestPeerCAConflicts(t *testing.T) {
	t.Parallel()
	dagu := harness.NewRunner(t)
	dagu.Run("peer", "init", "--out", "peer").ExpectExitCode(0)

	tests := []struct {
		name string
		env  []string
		args []string
		want string
	}{
		{
			name: "CAWithOwnCertificates",
			args: []string{"coordinator", "--peer.ca-cert-file=peer/ca.crt", "--peer.ca-key-file=peer/ca.key", "--peer.cert-file=server.crt", "--peer.key-file=server.key"},
			want: "use the built-in CA (peer.ca_cert_file, peer.ca_key_file) or your own certificates (peer.cert_file, peer.key_file, peer.client_ca_file), not both",
		},
		{
			name: "CAWithOwnCertificatesInConfig",
			args: []string{"coordinator", "--config", "both_cas.yaml"},
			want: "use the built-in CA (peer.ca_cert_file, peer.ca_key_file) or your own certificates (peer.cert_file, peer.key_file, peer.client_ca_file), not both",
		},
		{
			// The conflict stops every process, not only a coordinator.
			name: "CACertWithSkipVerify",
			env:  []string{"DAGU_PEER_CA_CERT_FILE=peer/ca.crt", "DAGU_PEER_SKIP_TLS_VERIFY=true"},
			args: []string{"worker", "--worker.coordinators=127.0.0.1:1"},
			want: "peer.skip_tls_verify turns off the check that peer.ca_cert_file makes: remove one",
		},
		{
			name: "CAKeyWithoutCert",
			args: []string{"coordinator", "--peer.ca-key-file=peer/ca.key"},
			want: "peer.ca_key_file needs peer.ca_cert_file",
		},
		{
			name: "CACertWithoutKey",
			env:  []string{"DAGU_PEER_CA_CERT_FILE=peer/ca.crt"},
			args: []string{"coordinator"},
			want: "peer.ca_cert_file needs peer.ca_key_file on a coordinator",
		},
		{
			name: "CACertNotACertificate",
			args: []string{"coordinator", "--peer.ca-cert-file=peer/token", "--peer.ca-key-file=peer/ca.key"},
			want: "peer/token",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// A free port keeps a coordinator that wrongly starts from
			// taking another test's port.
			env := append([]string{fmt.Sprintf("DAGU_COORDINATOR_PORT=%d", harness.FreePort(t))}, tt.env...)
			result := dagu.RunWithEnv(env, tt.args...)
			result.ExpectExitCode(1)
			result.ExpectStderrContains(tt.want)
		})
	}
}

func readPeerFiles(t *testing.T, dagu *harness.Runner) map[string]string {
	t.Helper()
	contents := make(map[string]string, len(peerFiles))
	for _, name := range peerFiles {
		data, err := os.ReadFile(dagu.ProjectPath(filepath.Join("peer", name)))
		require.NoError(t, err)
		contents[name] = string(data)
	}
	return contents
}
