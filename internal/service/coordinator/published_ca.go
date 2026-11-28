// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

// publishedCAFileMode lets every user that shares the data directory read
// the published CA certificate, which holds no secret.
const publishedCAFileMode = 0o644

// PublishCertificate writes the CA certificate to path, the published CA
// certificate, unless path already holds it. The returned function, for a
// coordinator that does not start after all, removes the file while it
// holds this certificate; each running coordinator with a peer CA writes its
// own again on its next registry refresh.
func (ca *PeerCA) PublishCertificate(path string) (func(), error) {
	certPEM := ca.certificatePEM()
	if current, err := fileutil.ReadFile(path); err == nil && bytes.Equal(current, certPEM) {
		return func() {}, nil
	}
	if err := fileutil.MkdirAll(filepath.Dir(path), peerDirMode); err != nil {
		return nil, fmt.Errorf("create published CA certificate directory: %w", err)
	}
	if err := fileutil.WriteFileAtomic(path, certPEM, publishedCAFileMode); err != nil {
		return nil, fmt.Errorf("write published CA certificate: %w", err)
	}
	return func() {
		// Removing, not writing back, lets a coordinator that published in
		// between repair the file, since a refresh writes a missing one.
		if current, err := fileutil.ReadFile(path); err == nil && bytes.Equal(current, certPEM) {
			_ = fileutil.Remove(path)
		}
	}, nil
}

// republishCertificate writes the CA certificate to path when the file is
// missing. It only creates the file, so it never replaces a certificate
// that another coordinator published since the file went missing.
func (ca *PeerCA) republishCertificate(path string) error {
	// A refresh runs every 10 seconds, so it writes nothing while the file
	// is in place.
	if _, err := fileutil.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err := fileutil.MkdirAll(filepath.Dir(path), peerDirMode); err != nil {
		return fmt.Errorf("create published CA certificate directory: %w", err)
	}
	err := fileutil.WriteFileAtomicExclusive(path, ca.certificatePEM(), publishedCAFileMode)
	if err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("write published CA certificate: %w", err)
	}
	return nil
}

// retryCertificate retries a write of the CA certificate to path that
// failed at start, when path held previous, nil for no file. It writes only
// over previous or a missing file, so it never replaces a certificate that
// another coordinator published since, and reports whether no retry is left:
// path holds this certificate, or another coordinator's.
func (ca *PeerCA) retryCertificate(path string, previous []byte) (bool, error) {
	current, err := fileutil.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return true, ca.republishCertificate(path)
	case err != nil:
		return false, err
	case !bytes.Equal(current, previous):
		return true, nil
	}
	if err := fileutil.WriteFileAtomic(path, ca.certificatePEM(), publishedCAFileMode); err != nil {
		return false, fmt.Errorf("write published CA certificate: %w", err)
	}
	return true, nil
}

func (ca *PeerCA) certificatePEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw})
}

// RemovePublishedCertificate removes the published CA certificate at path,
// so clients that share the data directory stop using built-in TLS.
func RemovePublishedCertificate(path string) error {
	if err := fileutil.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove published CA certificate: %w", err)
	}
	return nil
}

// readCertPool returns the certificates in the PEM file at path.
func readCertPool(path string) (*x509.CertPool, error) {
	data, err := fileutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("%s holds no PEM certificate", path)
	}
	return pool, nil
}
