// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package mtls makes a certificate authority and the certificates of a server
// and of clients for tests of mutual TLS. It writes each certificate and key as
// a PEM file.
package mtls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Authority is a certificate authority for one test.
type Authority struct {
	// CAFile is the path of the PEM file of the certificate of the authority.
	CAFile string

	dir  string
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

// Pair is the paths of a certificate and of its key.
type Pair struct {
	CertFile string
	KeyFile  string
}

var serial atomic.Int64

// NewAuthority makes an authority with the name and writes its certificate in
// dir as <name>.crt.
func NewAuthority(t testing.TB, dir, name string) *Authority {
	t.Helper()
	key := newKey(t)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(serial.Add(1)),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create the certificate of the authority: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse the certificate of the authority: %v", err)
	}
	a := &Authority{dir: dir, cert: cert, key: key, CAFile: filepath.Join(dir, name+".crt")}
	writePEM(t, a.CAFile, "CERTIFICATE", der)
	return a
}

// Server issues a certificate for 127.0.0.1 and localhost. The certificate
// is also a client certificate, as the certificate of a coordinator that the
// server and the scheduler of the same host use.
func (a *Authority) Server(t testing.TB, name string) Pair {
	t.Helper()
	return a.issue(t, name, func(c *x509.Certificate) {
		c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}
		c.DNSNames = []string{"localhost"}
		c.IPAddresses = []net.IP{net.IPv4(127, 0, 0, 1)}
	})
}

// Client issues a client certificate whose subject Common Name is cn. It writes
// <cn>.crt and <cn>.key in the folder of the authority.
func (a *Authority) Client(t testing.TB, cn string) Pair {
	t.Helper()
	return a.issue(t, cn, func(c *x509.Certificate) {
		c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	})
}

func (a *Authority) issue(t testing.TB, cn string, edit func(*x509.Certificate)) Pair {
	t.Helper()
	key := newKey(t)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial.Add(1)),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	edit(template)
	der, err := x509.CreateCertificate(rand.Reader, template, a.cert, &key.PublicKey, a.key)
	if err != nil {
		t.Fatalf("issue the certificate %q: %v", cn, err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("encode the key of %q: %v", cn, err)
	}
	base := filepath.Join(a.dir, cn)
	pair := Pair{CertFile: base + ".crt", KeyFile: base + ".key"}
	writePEM(t, pair.CertFile, "CERTIFICATE", der)
	writePEM(t, pair.KeyFile, "EC PRIVATE KEY", keyDER)
	return pair
}

func newKey(t testing.TB) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate a key: %v", err)
	}
	return key
}

func writePEM(t testing.TB, path, kind string, der []byte) {
	t.Helper()
	data := pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
