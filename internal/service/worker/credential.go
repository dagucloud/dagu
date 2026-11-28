// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
)

const (
	// credentialDirName is the directory, under the data directory, that
	// holds an invite worker's credential and its lock.
	credentialDirName = "worker"
	// credentialFileName is the credential file in that directory.
	credentialFileName = "credential.json"
	// credentialFileMode and credentialDirMode keep the credential readable
	// by its user only.
	credentialFileMode = 0o600
	credentialDirMode  = 0o700
	// writeProbeName is the file that tests whether the directory is
	// writable.
	writeProbeName = ".write-probe"
)

// credential is the key and worker certificate of an invite worker, the CA
// certificate that enrollment returned, and the invite it enrolled with.
type credential struct {
	cert tls.Certificate
	ca   *x509.Certificate
	// inviteSum is the SHA-256 of the invite it enrolled with, so a later
	// start can tell that invite from another without keeping it.
	inviteSum string
}

// credentialFile is how a credential is kept in its file.
type credentialFile struct {
	Key           string `json:"key"`
	Certificate   string `json:"certificate"`
	CACertificate string `json:"caCertificate"`
	InviteSHA256  string `json:"inviteSha256"`
}

// credentialStore keeps a worker's credential in its data directory.
type credentialStore struct {
	dir string
}

func newCredentialStore(dataDir string) credentialStore {
	return credentialStore{dir: filepath.Join(dataDir, credentialDirName)}
}

func (s credentialStore) path() string {
	return filepath.Join(s.dir, credentialFileName)
}

// exists reports whether the store holds a credential file.
func (s credentialStore) exists() bool {
	_, err := fileutil.Stat(s.path())
	return err == nil
}

// load returns the stored credential, or nil when there is none.
func (s credentialStore) load() (*credential, error) {
	data, err := fileutil.ReadFile(s.path())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the worker certificate: %w", err)
	}
	var file credentialFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("read the worker certificate: %w", err)
	}
	cert, err := tls.X509KeyPair([]byte(file.Certificate), []byte(file.Key))
	if err != nil {
		return nil, fmt.Errorf("read the worker certificate: %w", err)
	}
	ca, err := parseCertificatePEM(file.CACertificate)
	if err != nil {
		return nil, fmt.Errorf("read the worker certificate's CA: %w", err)
	}
	return &credential{cert: cert, ca: ca, inviteSum: file.InviteSHA256}, nil
}

// save replaces the stored credential with c, readable by its user only.
func (s credentialStore) save(c *credential) error {
	key, err := x509.MarshalPKCS8PrivateKey(c.cert.PrivateKey)
	if err != nil {
		return fmt.Errorf("encode the worker key: %w", err)
	}
	data, err := json.Marshal(credentialFile{
		Key:           string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})),
		Certificate:   string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.cert.Certificate[0]})),
		CACertificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.ca.Raw})),
		InviteSHA256:  c.inviteSum,
	})
	if err != nil {
		return err
	}
	if err := fileutil.MkdirAll(s.dir, credentialDirMode); err != nil {
		return fmt.Errorf("keep the worker certificate: %w", err)
	}
	if err := fileutil.WriteFileAtomic(s.path(), data, credentialFileMode); err != nil {
		return fmt.Errorf("keep the worker certificate: %w", err)
	}
	return nil
}

// writable reports whether the worker can keep files in the store.
func (s credentialStore) writable() bool {
	if err := fileutil.MkdirAll(s.dir, credentialDirMode); err != nil {
		return false
	}
	probe := filepath.Join(s.dir, writeProbeName)
	if err := fileutil.WriteFileAtomic(probe, nil, credentialFileMode); err != nil {
		return false
	}
	_ = fileutil.Remove(probe)
	return true
}

// pin returns the pin an invite carries for the CA that signed c.
func (c *credential) pin() string {
	return coordinator.CAPin(c.ca)
}

// newWorkerKey returns a new key for enrollment and the DER certificate
// signing request for it. The coordinator takes only the key from it.
func newWorkerKey() (*ecdsa.PrivateKey, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate the worker key: %w", err)
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	if err != nil {
		return nil, nil, fmt.Errorf("create the certificate signing request: %w", err)
	}
	return key, csr, nil
}

// enrolledCredential returns the credential of key from the DER worker
// certificate and CA certificate that enrollment with invite returned.
func enrolledCredential(key *ecdsa.PrivateKey, certDER, caDER []byte, invite string) (*credential, error) {
	leaf, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, fmt.Errorf("read the worker certificate: %w", err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, fmt.Errorf("read the CA certificate: %w", err)
	}
	return &credential{
		cert:      tls.Certificate{Certificate: [][]byte{certDER}, PrivateKey: key, Leaf: leaf},
		ca:        ca,
		inviteSum: inviteSum(invite),
	}, nil
}

func inviteSum(invite string) string {
	sum := sha256.Sum256([]byte(invite))
	return hex.EncodeToString(sum[:])
}

func parseCertificatePEM(data string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(data))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("no PEM certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}
