// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package peercert writes self-signed peer certificates for tests.
package peercert

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
	"testing"
	"time"
)

// Options describe the certificate Write creates.
type Options struct {
	// Name is the base name of the written files.
	Name string
	// DNSNames are the names the certificate covers.
	DNSNames []string
	// IPAddresses are the addresses the certificate covers.
	IPAddresses []net.IP
	// NotAfter is the expiry. Zero means one year from now.
	NotAfter time.Time
	// ExtKeyUsage lists the extended key usages. Empty means none, which
	// allows any usage.
	ExtKeyUsage []x509.ExtKeyUsage
}

// Write writes a self-signed certificate and its private key in PEM to dir
// and returns their paths.
func Write(t testing.TB, dir string, opts Options) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	notAfter := opts.NotAfter
	if notAfter.IsZero() {
		notAfter = time.Now().AddDate(1, 0, 0)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: opts.Name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  opts.ExtKeyUsage,
		DNSNames:     opts.DNSNames,
		IPAddresses:  opts.IPAddresses,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	certFile = filepath.Join(dir, opts.Name+".crt")
	keyFile = filepath.Join(dir, opts.Name+".key")
	writePEM(t, certFile, "CERTIFICATE", der)
	writePEM(t, keyFile, "PRIVATE KEY", keyDER)
	return certFile, keyFile
}

func writePEM(t testing.TB, path, blockType string, der []byte) {
	t.Helper()
	data := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
