// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadOrCreatePeerCA(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coordinator", "peer-ca.pem")

	first, err := LoadOrCreatePeerCA(path)
	require.NoError(t, err)
	requireGeneratedCA(t, first)
	requireOwnerOnly(t, path)

	// A later start reuses the stored CA.
	second, err := LoadOrCreatePeerCA(path)
	require.NoError(t, err)
	assert.Equal(t, first.cert.Raw, second.cert.Raw)
}

// Coordinators that start together on one data directory end with one CA.
func TestLoadOrCreatePeerCAShared(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coordinator", "peer-ca.pem")
	const coordinators = 8

	cas := make([]*PeerCA, coordinators)
	errs := make([]error, coordinators)
	var wg sync.WaitGroup
	for i := range coordinators {
		wg.Go(func() {
			cas[i], errs[i] = LoadOrCreatePeerCA(path)
		})
	}
	wg.Wait()

	for i := range coordinators {
		require.NoError(t, errs[i])
		assert.Equal(t, cas[0].cert.Raw, cas[i].cert.Raw)
	}
}

// A CA file that cannot be read fails and stays in place, so a passing
// problem never replaces the CA that peers trust.
func TestLoadOrCreatePeerCAUnreadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peer-ca.pem")
	require.NoError(t, os.WriteFile(path, []byte("not a CA"), 0o600))

	_, err := LoadOrCreatePeerCA(path)
	require.ErrorContains(t, err, path)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "not a CA", string(data))
}

func TestLoadPeerCA(t *testing.T) {
	dir := t.TempDir()
	paths, err := WritePeerInit(filepath.Join(dir, "a"))
	require.NoError(t, err)
	other, err := WritePeerInit(filepath.Join(dir, "b"))
	require.NoError(t, err)
	leafCert, leafKey := writeLeafCertificate(t, dir)

	ca, err := LoadPeerCA(paths[0], paths[1])
	require.NoError(t, err)
	requireGeneratedCA(t, ca)

	tests := []struct {
		name     string
		certFile string
		keyFile  string
	}{
		{name: "KeyOfAnotherCA", certFile: paths[0], keyFile: other[1]},
		{name: "NotACA", certFile: leafCert, keyFile: leafKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadPeerCA(tt.certFile, tt.keyFile)
			require.ErrorContains(t, err, tt.certFile)
			require.ErrorContains(t, err, tt.keyFile)
		})
	}
}

func TestWritePeerInit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "peer")

	paths, err := WritePeerInit(dir)
	require.NoError(t, err)
	require.Equal(t, []string{
		filepath.Join(dir, "ca.crt"),
		filepath.Join(dir, "ca.key"),
		filepath.Join(dir, "token"),
	}, paths)
	for _, path := range paths {
		requireOwnerOnly(t, path)
	}

	ca, err := LoadPeerCA(paths[0], paths[1])
	require.NoError(t, err)
	requireGeneratedCA(t, ca)
	token, err := os.ReadFile(paths[2])
	require.NoError(t, err)
	assert.Regexp(t, regexp.MustCompile(`^[0-9a-f]{64}\n$`), string(token))
	_, err = readPeerToken(paths[2])
	require.NoError(t, err)
}

// peer init refuses to run when any of its files exists, and writes none.
func TestWritePeerInitExisting(t *testing.T) {
	for _, name := range []string{"ca.crt", "ca.key", "token"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			existing := filepath.Join(dir, name)
			require.NoError(t, os.WriteFile(existing, []byte("keep"), 0o600))

			_, err := WritePeerInit(dir)
			require.ErrorIs(t, err, fs.ErrExist)
			var pathErr *fs.PathError
			require.ErrorAs(t, err, &pathErr)
			assert.Equal(t, existing, pathErr.Path)

			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			data, err := os.ReadFile(existing)
			require.NoError(t, err)
			assert.Equal(t, "keep", string(data))
		})
	}
}

// A failed write leaves none of the files this run wrote, whether it failed
// before or after installing its file, and keeps a file another writer
// created in the meantime.
func TestWritePeerInitFailedWrite(t *testing.T) {
	errWrite := errors.New("write failed")
	tests := []struct {
		name string
		// write replaces the write of ca.key.
		write     func(path string, data []byte, perm os.FileMode) error
		wantFiles map[string]string
	}{
		{
			name: "AfterInstall",
			write: func(path string, data []byte, perm os.FileMode) error {
				if err := fileutil.WriteFileAtomicExclusive(path, data, perm); err != nil {
					return err
				}
				return errWrite
			},
			wantFiles: map[string]string{},
		},
		{
			name: "BeforeInstallWhileAnotherWriterCreates",
			write: func(path string, _ []byte, _ os.FileMode) error {
				if err := os.WriteFile(path, []byte("other"), 0o600); err != nil {
					return err
				}
				return errWrite
			},
			wantFiles: map[string]string{"ca.key": "other"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writePeerFile = func(path string, data []byte, perm os.FileMode) error {
				if filepath.Base(path) == "ca.key" {
					return tt.write(path, data, perm)
				}
				return fileutil.WriteFileAtomicExclusive(path, data, perm)
			}
			t.Cleanup(func() { writePeerFile = fileutil.WriteFileAtomicExclusive })

			_, err := WritePeerInit(dir)
			require.ErrorIs(t, err, errWrite)

			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			files := make(map[string]string, len(entries))
			for _, entry := range entries {
				data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
				require.NoError(t, err)
				files[entry.Name()] = string(data)
			}
			assert.Equal(t, tt.wantFiles, files)
		})
	}
}

// requireGeneratedCA checks the CA a coordinator or peer init generates: an
// ECDSA P-256 CA with no expiry date whose key matches its certificate.
func requireGeneratedCA(t *testing.T, ca *PeerCA) {
	t.Helper()
	require.True(t, ca.cert.IsCA)
	assert.Equal(t, time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC), ca.cert.NotAfter)
	pub, ok := ca.cert.PublicKey.(*ecdsa.PublicKey)
	require.True(t, ok)
	assert.Equal(t, elliptic.P256(), pub.Curve)
	assert.True(t, pub.Equal(ca.key.Public()))
}

func requireOwnerOnly(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		assert.Equal(t, fs.FileMode(0o600), info.Mode().Perm())
	}
}

// writeLeafCertificate writes a self-signed certificate that is not a CA and
// its key, and returns their paths.
func writeLeafCertificate(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "leaf"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)

	certFile = filepath.Join(dir, "leaf.crt")
	keyFile = filepath.Join(dir, "leaf.key")
	require.NoError(t, os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600))
	return certFile, keyFile
}
