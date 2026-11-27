// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
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
		// A generated CA that cannot be read gives no CA and no error, so the
		// coordinator still starts.
		{name: "GeneratedUnreadable", peer: config.Peer{GeneratedCAFile: filepath.Join(notADir, "coordinator", "peer-ca.pem")}},
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

// A coordinator that cannot create the generated CA in a read-only data
// directory gets no CA and no error, so it still starts.
func TestCoordinatorPeerCAReadOnlyDataDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not stop writes on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes to read-only directories")
	}
	dataDir := t.TempDir()
	require.NoError(t, os.Chmod(dataDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dataDir, 0o700) })
	generated := filepath.Join(dataDir, "coordinator", "peer-ca.pem")

	ca, err := coordinatorPeerCA(context.Background(), config.Peer{GeneratedCAFile: generated})
	require.NoError(t, err)
	assert.Nil(t, ca)
	assert.NoDirExists(t, filepath.Dir(generated))
}
