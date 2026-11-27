// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"path/filepath"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const (
	peerCACommonName = "Dagu peer CA"
	// certBackdate moves a certificate's notBefore into the past, so a peer
	// whose clock runs behind the coordinator's still accepts it.
	certBackdate = time.Hour
	// certSerialBits is the size of a certificate's random serial number.
	certSerialBits = 128
	// peerFileMode and peerDirMode keep CA files, peer tokens, and their
	// directories readable by their user only.
	peerFileMode = 0o600
	peerDirMode  = 0o700

	peerInitCAFile    = "ca.crt"
	peerInitKeyFile   = "ca.key"
	peerInitTokenFile = "token"
)

// peerCANoExpiry is the notAfter that RFC 5280 gives a certificate with no
// expiry date, 99991231235959Z.
var peerCANoExpiry = time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC)

// PeerCA is the CA that signs coordinator and worker certificates.
type PeerCA struct {
	cert *x509.Certificate
	key  crypto.Signer
}

// LoadOrCreatePeerCA returns the CA stored in path, generating and storing
// one first when the file does not exist. Coordinators that share the file
// agree on one CA. A file that exists but cannot be read is an error, and the
// CA in it is never replaced.
func LoadOrCreatePeerCA(path string) (*PeerCA, error) {
	ca, err := readPeerCA(path)
	if !errors.Is(err, fs.ErrNotExist) {
		return ca, err
	}

	certPEM, keyPEM, err := newPeerCAPEM()
	if err != nil {
		return nil, err
	}
	if err := fileutil.MkdirAll(filepath.Dir(path), peerDirMode); err != nil {
		return nil, fmt.Errorf("create peer CA directory: %w", err)
	}
	err = fileutil.WriteFileAtomicExclusive(path, append(certPEM, keyPEM...), peerFileMode)
	if errors.Is(err, fs.ErrExist) {
		return readPeerCA(path)
	}
	if err != nil {
		return nil, fmt.Errorf("write peer CA: %w", err)
	}
	return parsePeerCA(certPEM, keyPEM)
}

// LoadPeerCA returns the CA in the PEM files certFile and keyFile. It fails,
// naming the files, when the certificate is not a CA certificate or the key
// does not match it.
func LoadPeerCA(certFile, keyFile string) (*PeerCA, error) {
	certPEM, err := fileutil.ReadFile(certFile)
	if err != nil {
		return nil, fmt.Errorf("read CA certificate: %w", err)
	}
	keyPEM, err := fileutil.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("read CA key: %w", err)
	}
	ca, err := parsePeerCA(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("%s and %s: %w", certFile, keyFile, err)
	}
	return ca, nil
}

// WritePeerInit writes a new CA certificate, its key, and a peer token into
// dir as ca.crt, ca.key, and token, readable by their user only, and returns
// their paths. dir is created when missing. When any of the files exists, it
// writes none and returns an *fs.PathError for that file that satisfies
// fs.ErrExist.
func WritePeerInit(dir string) ([]string, error) {
	paths := []string{
		filepath.Join(dir, peerInitCAFile),
		filepath.Join(dir, peerInitKeyFile),
		filepath.Join(dir, peerInitTokenFile),
	}
	for _, path := range paths {
		if _, err := fileutil.Stat(path); err == nil {
			return nil, existingPeerFile(path)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}

	certPEM, keyPEM, err := newPeerCAPEM()
	if err != nil {
		return nil, err
	}
	token, err := newPeerToken()
	if err != nil {
		return nil, err
	}
	if err := fileutil.MkdirAll(dir, peerDirMode); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	contents := map[string][]byte{
		paths[0]: certPEM,
		paths[1]: keyPEM,
		paths[2]: []byte(token + "\n"),
	}
	var attempted []string
	for _, path := range paths {
		attempted = append(attempted, path)
		err := writePeerFile(path, contents[path], peerFileMode)
		if err == nil {
			continue
		}
		// A write can fail before or after it installs its file, so remove
		// every file that still holds what this run wrote. The contents are
		// random, so a file another writer created stays.
		for _, written := range attempted {
			removeOwnPeerFile(written, contents[written])
		}
		if errors.Is(err, fs.ErrExist) {
			return nil, existingPeerFile(path)
		}
		return nil, err
	}
	return paths, nil
}

// writePeerFile creates a peer init file. Tests replace it to fail a write.
var writePeerFile = fileutil.WriteFileAtomicExclusive

func removeOwnPeerFile(path string, content []byte) {
	if data, err := fileutil.ReadFile(path); err == nil && bytes.Equal(data, content) {
		_ = fileutil.Remove(path)
	}
}

func existingPeerFile(path string) error {
	return &fs.PathError{Op: "create", Path: path, Err: fs.ErrExist}
}

func readPeerCA(path string) (*PeerCA, error) {
	data, err := fileutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	ca, err := parsePeerCA(data, data)
	if err != nil {
		return nil, fmt.Errorf("peer CA file %s: %w", path, err)
	}
	return ca, nil
}

// parsePeerCA reads the first certificate in certPEM and the first private
// key in keyPEM. Both may be the same bundle.
func parsePeerCA(certPEM, keyPEM []byte) (*PeerCA, error) {
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	cert := pair.Leaf
	if !cert.BasicConstraintsValid || !cert.IsCA {
		return nil, errors.New("the certificate is not a CA certificate")
	}
	// RFC 5280 lets a certificate without the key usage extension sign
	// certificates, and one with it only when it names keyCertSign.
	if cert.KeyUsage != 0 && cert.KeyUsage&x509.KeyUsageCertSign == 0 {
		return nil, errors.New("the CA certificate's key usage does not allow signing certificates")
	}
	key, ok := pair.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, errors.New("the key cannot sign certificates")
	}
	return &PeerCA{cert: cert, key: key}, nil
}

// newPeerCAPEM generates an ECDSA P-256 CA with no expiry date and returns
// its certificate and PKCS #8 private key as PEM.
func newPeerCAPEM() (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate peer CA key: %w", err)
	}
	serial, err := newCertSerial()
	if err != nil {
		return nil, nil, fmt.Errorf("generate peer CA serial number: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: peerCACommonName},
		NotBefore:             time.Now().Add(-certBackdate),
		NotAfter:              peerCANoExpiry,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		// The CA signs leaf certificates only.
		MaxPathLenZero: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		return nil, nil, fmt.Errorf("create peer CA certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("encode peer CA key: %w", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

// newCertSerial returns a random serial number for a certificate.
func newCertSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), certSerialBits))
}
