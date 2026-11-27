// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCoordinatorPeerCA(t *testing.T) {
	dir := t.TempDir()
	supplied, err := coordinator.WritePeerInit(filepath.Join(dir, "supplied"))
	require.NoError(t, err)
	caCert, caKey := supplied[0], supplied[1]
	notADir := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(notADir, nil, 0o600))

	tests := []struct {
		name   string
		peer   config.Peer
		wantCA bool
		// generates reports whether the coordinator writes a generated CA.
		generates bool
		wantErr   string
	}{
		{name: "Generated", peer: config.Peer{}, wantCA: true, generates: true},
		{name: "Supplied", peer: config.Peer{CACertFile: caCert, CAKeyFile: caKey}, wantCA: true},
		{name: "OwnCertificates", peer: config.Peer{CertFile: "server.crt", KeyFile: "server.key"}},
		{name: "KeyWithoutCert", peer: config.Peer{CAKeyFile: caKey}, wantErr: "peer.ca_key_file needs peer.ca_cert_file"},
		{name: "CertWithoutKey", peer: config.Peer{CACertFile: caCert}, wantErr: "peer.ca_cert_file needs peer.ca_key_file on a coordinator"},
		{name: "SuppliedNotACertificate", peer: config.Peer{CACertFile: supplied[2], CAKeyFile: caKey}, wantErr: supplied[2]},
		// A coordinator that cannot write its data directory starts without
		// a CA, as it did before the peer CA.
		{name: "GeneratedUnwritable", peer: config.Peer{GeneratedCAFile: filepath.Join(notADir, "coordinator", "peer-ca.pem")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			generated := filepath.Join(t.TempDir(), "coordinator", "peer-ca.pem")
			if tt.peer.GeneratedCAFile == "" {
				tt.peer.GeneratedCAFile = generated
			}

			ca, err := coordinatorPeerCA(context.Background(), tt.peer)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantCA, ca != nil)
			_, statErr := os.Stat(generated)
			assert.Equal(t, tt.generates, statErr == nil)
		})
	}
}
