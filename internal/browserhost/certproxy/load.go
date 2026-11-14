// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package certproxy

import (
	"bytes"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	cmnconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/hostpattern"
	"software.sslmate.com/src/go-pkcs12"
)

const (
	pemCertificate          = "CERTIFICATE"
	pemPrivateKey           = "PRIVATE KEY"
	pemEncryptedPrivateKey  = "ENCRYPTED PRIVATE KEY"
	pemProcTypeHeader       = "Proc-Type"
	pemProcTypeEncryptedTag = "ENCRYPTED"
	// pemCertificateBegin starts a PEM certificate block.
	pemCertificateBegin = "-----BEGIN CERTIFICATE-----"
	// pemPrivateKeyMarker ends the BEGIN and END lines of every kind of PEM
	// private key.
	pemPrivateKeyMarker = "PRIVATE KEY-----"
	// validityDateLayout formats the dates of validity warnings.
	validityDateLayout = "2006-01-02"
)

// Load reads one certificate. Relative files resolve against baseDir.
// Warnings report a certificate that is expired or not yet valid at now.
// Errors name label and the file involved, never the passphrase.
func Load(label string, c cmnconfig.BrowserClientCertificate, baseDir string, now time.Time) (Entry, []string, error) {
	if err := c.Validate(); err != nil {
		return Entry{}, nil, fmt.Errorf("%s: %w", label, err)
	}
	for _, host := range c.Hosts {
		if err := hostpattern.Validate(host); err != nil {
			return Entry{}, nil, fmt.Errorf("%s: %w", label, err)
		}
	}
	var (
		chain []*x509.Certificate
		key   crypto.PrivateKey
		err   error
	)
	if c.PFXFile != "" {
		chain, key, err = loadPFX(c, baseDir)
	} else {
		chain, key, err = loadPEMPair(c, baseDir)
	}
	if err != nil {
		return Entry{}, nil, fmt.Errorf("%s: %w", label, err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return Entry{}, nil, fmt.Errorf("%s: unsupported private key: %w", label, err)
	}
	entry := Entry{
		Label:    label,
		Hosts:    slices.Clone(c.Hosts),
		ChainPEM: encodeCertificates(chain),
		KeyPEM:   string(pem.EncodeToMemory(&pem.Block{Type: pemPrivateKey, Bytes: keyDER})),
	}
	if c.CAFile != "" {
		roots, err := loadCAFile(resolvePath(baseDir, c.CAFile))
		if err != nil {
			return Entry{}, nil, fmt.Errorf("%s: %w", label, err)
		}
		entry.CAPEM = encodeCertificates(roots)
	}
	return entry, validityWarnings(label, chain[0], now), nil
}

// LoadTrustedCA reads a scoped server authority without a client identity.
func LoadTrustedCA(label string, c cmnconfig.BrowserTrustedCA) (TrustedCA, error) {
	if err := c.Validate(); err != nil {
		return TrustedCA{}, fmt.Errorf("%s: %w", label, err)
	}
	data, err := os.ReadFile(c.CAFile) //nolint:gosec // the authority file is the configured input
	if err != nil {
		return TrustedCA{}, fmt.Errorf("%s: %w", label, err)
	}
	certs, err := trustedCertificates(data)
	if err != nil {
		return TrustedCA{}, fmt.Errorf("%s: %w", label, err)
	}
	return TrustedCA{Label: label, Hosts: slices.Clone(c.Hosts), CAPEM: encodeCertificates(certs)}, nil
}

// trustedCertificates returns the trust anchors of a ca_file: certificate
// authorities and self-signed certificates. Text around the PEM blocks, as
// openssl writes it, is ignored.
func trustedCertificates(data []byte) ([]*x509.Certificate, error) {
	if bytes.Contains(data, []byte(pemPrivateKeyMarker)) {
		return nil, errors.New("ca_file holds a private key; it needs only the authority's certificate")
	}
	var certs []*x509.Certificate
	for rest := data; ; {
		var block *pem.Block
		if block, rest = pem.Decode(rest); block == nil {
			break
		}
		if block.Type != pemCertificate {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("ca_file: %w", err)
		}
		if !trustAnchor(cert) {
			return nil, fmt.Errorf("ca_file holds %s, which is neither a certificate authority nor self-signed; name the authority that issued it", cert.Subject)
		}
		certs = append(certs, cert)
	}
	// pem.Decode skips a block it cannot decode, which would drop a
	// damaged certificate silently.
	if len(certs) < bytes.Count(data, []byte(pemCertificateBegin)) {
		return nil, errors.New("ca_file holds a malformed PEM certificate")
	}
	if len(certs) == 0 {
		return nil, errors.New("ca_file holds no PEM certificate")
	}
	return certs, nil
}

// trustAnchor reports whether cert can anchor server trust: a certificate
// authority, or a self-signed certificate such as an appliance's.
func trustAnchor(cert *x509.Certificate) bool {
	if cert.BasicConstraintsValid && cert.IsCA && (cert.KeyUsage == 0 || cert.KeyUsage&x509.KeyUsageCertSign != 0) {
		return true
	}
	return bytes.Equal(cert.RawIssuer, cert.RawSubject) &&
		cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature) == nil
}

// loadPFX reads a PKCS#12 file and returns its chain, leaf first, and key.
func loadPFX(c cmnconfig.BrowserClientCertificate, baseDir string) ([]*x509.Certificate, crypto.PrivateKey, error) {
	path := resolvePath(baseDir, c.PFXFile)
	data, err := os.ReadFile(path) //nolint:gosec // the certificate file is the configured input
	if err != nil {
		return nil, nil, fmt.Errorf("read pfx_file: %w", err)
	}
	passphrase := c.Passphrase
	if c.PassphraseFile != "" {
		passphraseFile := resolvePath(baseDir, c.PassphraseFile)
		text, err := os.ReadFile(passphraseFile) //nolint:gosec // the passphrase file is the configured input
		if err != nil {
			return nil, nil, fmt.Errorf("read passphrase_file: %w", err)
		}
		passphrase = strings.TrimRight(string(text), "\r\n")
	}
	key, first, rest, err := pkcs12.DecodeChain(data, passphrase)
	switch {
	case errors.Is(err, pkcs12.ErrIncorrectPassword), errors.Is(err, pkcs12.ErrDecryption):
		return nil, nil, fmt.Errorf("pfx_file %s: the passphrase does not unlock it", path)
	case err != nil:
		return nil, nil, fmt.Errorf("pfx_file %s: %w", path, err)
	}
	chain, err := leafFirst(append([]*x509.Certificate{first}, rest...), key)
	if err != nil {
		return nil, nil, fmt.Errorf("pfx_file %s: %w", path, err)
	}
	return chain, key, nil
}

// leafFirst orders certs with the one holding key's public key first.
// Exported files do not always put it first.
func leafFirst(certs []*x509.Certificate, key crypto.PrivateKey) ([]*x509.Certificate, error) {
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("unsupported private key type %T", key)
	}
	public, ok := signer.Public().(interface{ Equal(crypto.PublicKey) bool })
	if !ok {
		return nil, fmt.Errorf("unsupported private key type %T", key)
	}
	for i, cert := range certs {
		if public.Equal(cert.PublicKey) {
			return append([]*x509.Certificate{cert}, slices.Delete(slices.Clone(certs), i, i+1)...), nil
		}
	}
	return nil, errors.New("no certificate in the file matches its private key")
}

// loadPEMPair reads a PEM certificate chain and its unencrypted key.
func loadPEMPair(c cmnconfig.BrowserClientCertificate, baseDir string) ([]*x509.Certificate, crypto.PrivateKey, error) {
	certPath, keyPath := resolvePath(baseDir, c.CertFile), resolvePath(baseDir, c.KeyFile)
	certPEM, err := os.ReadFile(certPath) //nolint:gosec // the certificate file is the configured input
	if err != nil {
		return nil, nil, fmt.Errorf("read cert_file: %w", err)
	}
	keyPEM, err := os.ReadFile(keyPath) //nolint:gosec // the key file is the configured input
	if err != nil {
		return nil, nil, fmt.Errorf("read key_file: %w", err)
	}
	if encryptedPEM(keyPEM) {
		return nil, nil, fmt.Errorf("key_file %s holds an encrypted key, which is not supported; "+
			"save it unencrypted, as with openssl pkey -in key.pem -out key-plain.pem, or use pfx_file with a passphrase", keyPath)
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("cert_file %s and key_file %s: %w", certPath, keyPath, err)
	}
	chain := make([]*x509.Certificate, 0, len(pair.Certificate))
	for _, der := range pair.Certificate {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, nil, fmt.Errorf("cert_file %s: %w", certPath, err)
		}
		chain = append(chain, cert)
	}
	return chain, pair.PrivateKey, nil
}

// encryptedPEM reports whether data holds a key encrypted as PKCS#8 or in
// the traditional OpenSSL form.
func encryptedPEM(data []byte) bool {
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			return false
		}
		if block.Type == pemEncryptedPrivateKey || strings.Contains(block.Headers[pemProcTypeHeader], pemProcTypeEncryptedTag) {
			return true
		}
	}
}

// loadCAFile reads the PEM certificates of a ca_file.
func loadCAFile(path string) ([]*x509.Certificate, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the CA file is the configured input
	if err != nil {
		return nil, fmt.Errorf("read ca_file: %w", err)
	}
	var certs []*x509.Certificate
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type != pemCertificate {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("ca_file %s: %w", path, err)
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		return nil, fmt.Errorf("ca_file %s holds no PEM certificate", path)
	}
	return certs, nil
}

// validityWarnings reports a leaf that is not valid at now.
func validityWarnings(label string, leaf *x509.Certificate, now time.Time) []string {
	switch {
	case now.After(leaf.NotAfter):
		return []string{fmt.Sprintf("%s: the client certificate expired on %s", label, leaf.NotAfter.UTC().Format(validityDateLayout))}
	case now.Before(leaf.NotBefore):
		return []string{fmt.Sprintf("%s: the client certificate is not valid until %s", label, leaf.NotBefore.UTC().Format(validityDateLayout))}
	}
	return nil
}

func encodeCertificates(certs []*x509.Certificate) string {
	var b strings.Builder
	for _, cert := range certs {
		_ = pem.Encode(&b, &pem.Block{Type: pemCertificate, Bytes: cert.Raw})
	}
	return b.String()
}

func resolvePath(baseDir, path string) string {
	if baseDir == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(baseDir, path)
}
