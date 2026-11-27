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
// certificate, unless path already holds it. The returned function puts back
// what path held before, while path still holds this certificate, for a
// coordinator that does not start after all.
func (ca *PeerCA) PublishCertificate(path string) (func(), error) {
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw})
	previous, readErr := fileutil.ReadFile(path)
	if readErr == nil && bytes.Equal(previous, certPEM) {
		return func() {}, nil
	}
	if err := fileutil.MkdirAll(filepath.Dir(path), peerDirMode); err != nil {
		return nil, fmt.Errorf("create published CA certificate directory: %w", err)
	}
	if err := fileutil.WriteFileAtomic(path, certPEM, publishedCAFileMode); err != nil {
		return nil, fmt.Errorf("write published CA certificate: %w", err)
	}
	return func() {
		// Best effort: a coordinator that still publishes writes its own
		// certificate again on its next registry refresh.
		if current, err := fileutil.ReadFile(path); err != nil || !bytes.Equal(current, certPEM) {
			return
		}
		switch {
		case readErr == nil:
			_ = fileutil.WriteFileAtomic(path, previous, publishedCAFileMode)
		case errors.Is(readErr, fs.ErrNotExist):
			_ = fileutil.Remove(path)
		}
	}, nil
}

// republishCertificate writes the CA certificate to path when the file is
// missing.
func (ca *PeerCA) republishCertificate(path string) error {
	if _, err := fileutil.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	_, err := ca.PublishCertificate(path)
	return err
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
