// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

const (
	// credentialDirName is the directory, under the data directory, that
	// holds an invite worker's credential and its lock.
	credentialDirName = "worker"
	// credentialFileName is the credential file in that directory.
	credentialFileName = "credential.json"
	// credentialFileMode keeps the credential readable by its user only.
	credentialFileMode = 0o600
	// writeProbeName is the file that tests whether the directory is
	// writable.
	writeProbeName = ".write-probe"
)

// credential is the key and worker certificate of an invite worker, the CA
// certificate that enrollment returned, and the invite it enrolled with.
type credential struct {
	cert tls.Certificate
	ca   *x509.Certificate
	// inviteDigest identifies the invite it enrolled with, without its
	// secret.
	inviteDigest string
}

// credentialFile is how a credential is kept in its file.
type credentialFile struct {
	Key           string `json:"key"`
	Certificate   string `json:"certificate"`
	CACertificate string `json:"caCertificate"`
	InviteDigest  string `json:"inviteDigest"`
}

// credentialStore keeps a worker's credential in its data directory, in a
// directory its user only may use.
type credentialStore struct {
	dir string
}

func newCredentialStore(dataDir string) credentialStore {
	return credentialStore{dir: filepath.Join(dataDir, credentialDirName)}
}

func (s credentialStore) path() string {
	return filepath.Join(s.dir, credentialFileName)
}

// kept reports whether the store holds a credential. An error other than
// its absence, such as a denied access, is returned, never read as none.
func (s credentialStore) kept() (bool, error) {
	_, err := fileutil.Stat(s.path())
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	}
	return false, err
}

// load returns the stored credential, or nil when there is none.
func (s credentialStore) load() (*credential, error) {
	data, err := fileutil.ReadFile(s.path())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var file credentialFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	cert, err := tls.X509KeyPair([]byte(file.Certificate), []byte(file.Key))
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode([]byte(file.CACertificate))
	if block == nil {
		return nil, errors.New("no CA certificate")
	}
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	return &credential{cert: cert, ca: ca, inviteDigest: file.InviteDigest}, nil
}

// save replaces the stored credential with c. The file is written whole
// and then renamed into place, so an interrupted save leaves the previous
// credential or none.
func (s credentialStore) save(c *credential) error {
	key, err := x509.MarshalPKCS8PrivateKey(c.cert.PrivateKey)
	if err != nil {
		return fmt.Errorf("encode the worker key: %w", err)
	}
	data, err := json.Marshal(credentialFile{
		Key:           string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})),
		Certificate:   string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.cert.Certificate[0]})),
		CACertificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.ca.Raw})),
		InviteDigest:  c.inviteDigest,
	})
	if err != nil {
		return err
	}
	if err := fileutil.MkdirPrivate(s.dir); err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(s.path(), data, credentialFileMode)
}

// removeLeftovers removes the partial files of saves that a stop
// interrupted. The caller holds the lock, so no save is under way.
func (s credentialStore) removeLeftovers() {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), credentialFileName+".tmp.") {
			_ = fileutil.Remove(filepath.Join(s.dir, entry.Name()))
		}
	}
}

// writable reports whether the worker can keep files in the store.
func (s credentialStore) writable() bool {
	if err := fileutil.MkdirPrivate(s.dir); err != nil {
		return false
	}
	probe := filepath.Join(s.dir, writeProbeName)
	if err := fileutil.WriteFileAtomic(probe, nil, credentialFileMode); err != nil {
		return false
	}
	_ = fileutil.Remove(probe)
	return true
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

// enrolledCredential returns the credential of key from resp, the reply
// that Enroll checked, to enrollment with invite.
func enrolledCredential(key *ecdsa.PrivateKey, resp *coordinatorv1.EnrollResponse, invite coordinator.Invite) (*credential, error) {
	leaf, err := x509.ParseCertificate(resp.Certificate)
	if err != nil {
		return nil, err
	}
	ca, err := x509.ParseCertificate(resp.CaCertificate)
	if err != nil {
		return nil, err
	}
	return &credential{
		cert:         tls.Certificate{Certificate: [][]byte{resp.Certificate}, PrivateKey: key, Leaf: leaf},
		ca:           ca,
		inviteDigest: invite.Digest(),
	}, nil
}

// readCertificateFile returns the first PEM certificate in path.
func readCertificateFile(path string) (*x509.Certificate, error) {
	data, err := fileutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("no PEM certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}
