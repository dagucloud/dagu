// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package certproxy

import (
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	cmnconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"software.sslmate.com/src/go-pkcs12"
)

const (
	testLabel      = "with.browser.client_certificates[0]"
	testPassphrase = "s3cret-passphrase"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func encodePFX(t *testing.T, encoder *pkcs12.Encoder, key crypto.Signer, first *x509.Certificate, rest []*x509.Certificate) []byte {
	t.Helper()
	data, err := encoder.Encode(key, first, rest, testPassphrase)
	require.NoError(t, err)
	return data
}

func parseChain(t *testing.T, chainPEM string) []*x509.Certificate {
	t.Helper()
	var certs []*x509.Certificate
	data := []byte(chainPEM)
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			return certs
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		require.NoError(t, err)
		certs = append(certs, cert)
	}
}

// assertEntryHolds checks that entry carries leaf first, then ca, and the
// leaf's key.
func assertEntryHolds(t *testing.T, entry Entry, leaf, ca *x509.Certificate) {
	t.Helper()
	chain := parseChain(t, entry.ChainPEM)
	require.Len(t, chain, 2)
	assert.True(t, chain[0].Equal(leaf), "the leaf comes first")
	assert.True(t, chain[1].Equal(ca))
	block, _ := pem.Decode([]byte(entry.KeyPEM))
	require.NotNil(t, block)
	assert.Equal(t, "PRIVATE KEY", block.Type)
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	require.NoError(t, err)
	assert.True(t, key.(crypto.Signer).Public().(interface{ Equal(crypto.PublicKey) bool }).Equal(leaf.PublicKey))
}

func TestLoadPFX(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "client CA")
	encoders := map[string]*pkcs12.Encoder{"Modern": pkcs12.Modern, "LegacyRC2": pkcs12.LegacyRC2, "LegacyDES": pkcs12.LegacyDES}
	for _, kind := range []struct {
		name string
		key  keyKind
	}{{"RSA", rsaKey}, {"ECDSA", ecdsaKey}} {
		leaf, key := ca.issue(t, certOptions{name: "client", key: kind.key, client: true})
		for name, encoder := range encoders {
			t.Run(kind.name+"/"+name, func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				writeFile(t, dir, "client.pfx", string(encodePFX(t, encoder, key, leaf, []*x509.Certificate{ca.cert})))
				entry, warnings, err := Load(testLabel, cmnconfig.BrowserClientCertificate{
					Hosts: []string{"portal.example.com"}, PFXFile: "client.pfx", Passphrase: testPassphrase,
				}, dir, time.Now())
				require.NoError(t, err)
				assert.Empty(t, warnings)
				assert.Equal(t, testLabel, entry.Label)
				assert.Equal(t, []string{"portal.example.com"}, entry.Hosts)
				assertEntryHolds(t, entry, leaf, ca.cert)
			})
		}
	}
}

func TestLoadPFXPutsLeafFirst(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "client CA")
	leaf, key := ca.issue(t, certOptions{name: "client", client: true})
	dir := t.TempDir()
	// The CA certificate is stored first, as some Windows exports do.
	path := writeFile(t, dir, "client.pfx", string(encodePFX(t, pkcs12.Modern, key, ca.cert, []*x509.Certificate{leaf})))

	entry, _, err := Load(testLabel, cmnconfig.BrowserClientCertificate{
		Hosts: []string{"*"}, PFXFile: path, Passphrase: testPassphrase,
	}, "", time.Now())
	require.NoError(t, err)
	assertEntryHolds(t, entry, leaf, ca.cert)
}

func TestLoadPFXWrongPassphrase(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "client CA")
	leaf, key := ca.issue(t, certOptions{name: "client", client: true})
	dir := t.TempDir()
	path := writeFile(t, dir, "client.pfx", string(encodePFX(t, pkcs12.Modern, key, leaf, nil)))

	const wrong = "not-the-passphrase"
	_, _, err := Load(testLabel, cmnconfig.BrowserClientCertificate{
		Hosts: []string{"*"}, PFXFile: path, Passphrase: wrong,
	}, "", time.Now())
	require.Error(t, err)
	assert.Contains(t, err.Error(), testLabel)
	assert.Contains(t, err.Error(), path)
	assert.Contains(t, err.Error(), "passphrase does not unlock")
	assert.NotContains(t, err.Error(), wrong)
	assert.NotContains(t, err.Error(), testPassphrase)
}

func TestLoadPFXPassphraseFile(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "client CA")
	leaf, key := ca.issue(t, certOptions{name: "client", client: true})
	dir := t.TempDir()
	writeFile(t, dir, "client.pfx", string(encodePFX(t, pkcs12.Modern, key, leaf, []*x509.Certificate{ca.cert})))
	writeFile(t, dir, "passphrase.txt", testPassphrase+"\r\n")

	entry, _, err := Load(testLabel, cmnconfig.BrowserClientCertificate{
		Hosts: []string{"*"}, PFXFile: "client.pfx", PassphraseFile: "passphrase.txt",
	}, dir, time.Now())
	require.NoError(t, err)
	assertEntryHolds(t, entry, leaf, ca.cert)
}

func TestLoadPEM(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "client CA")
	leaf, key := ca.issue(t, certOptions{name: "client", key: rsaKey, client: true})
	other, _ := ca.issue(t, certOptions{name: "other", client: true})

	t.Run("Pair", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeFile(t, dir, "client.crt", certPEM(leaf, ca.cert))
		writeFile(t, dir, "client.key", keyPEM(t, key))
		entry, warnings, err := Load(testLabel, cmnconfig.BrowserClientCertificate{
			Hosts: []string{"*.example.com"}, CertFile: "client.crt", KeyFile: "client.key",
		}, dir, time.Now())
		require.NoError(t, err)
		assert.Empty(t, warnings)
		assertEntryHolds(t, entry, leaf, ca.cert)
	})

	t.Run("Mismatch", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		certPath := writeFile(t, dir, "client.crt", certPEM(other))
		keyPath := writeFile(t, dir, "client.key", keyPEM(t, key))
		_, _, err := Load(testLabel, cmnconfig.BrowserClientCertificate{
			Hosts: []string{"*"}, CertFile: certPath, KeyFile: keyPath,
		}, "", time.Now())
		require.Error(t, err)
		assert.Contains(t, err.Error(), testLabel)
		assert.Contains(t, err.Error(), certPath)
		assert.Contains(t, err.Error(), keyPath)
	})

	t.Run("EncryptedKey", func(t *testing.T) {
		t.Parallel()
		for name, block := range map[string]*pem.Block{
			"PKCS8":       {Type: "ENCRYPTED PRIVATE KEY", Bytes: []byte("sealed")},
			"Traditional": {Type: "EC PRIVATE KEY", Headers: map[string]string{"Proc-Type": "4,ENCRYPTED", "DEK-Info": "AES-256-CBC,00"}, Bytes: []byte("sealed")},
		} {
			dir := t.TempDir()
			writeFile(t, dir, "client.crt", certPEM(other))
			keyPath := writeFile(t, dir, "client.key", string(pem.EncodeToMemory(block)))
			_, _, err := Load(testLabel, cmnconfig.BrowserClientCertificate{
				Hosts: []string{"*"}, CertFile: "client.crt", KeyFile: "client.key",
			}, dir, time.Now())
			require.Error(t, err, name)
			assert.Contains(t, err.Error(), keyPath, name)
			assert.Contains(t, err.Error(), "openssl pkey", name)
		}
	})
}

func TestLoadWarnsOutsideValidity(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "client CA")
	now := time.Now()
	for name, tt := range map[string]struct {
		opts certOptions
		want string
	}{
		"Expired": {
			opts: certOptions{notBefore: now.AddDate(-2, 0, 0), notAfter: time.Date(2025, 3, 31, 0, 0, 0, 0, time.UTC)},
			want: testLabel + ": the client certificate expired on 2025-03-31",
		},
		"NotYetValid": {
			opts: certOptions{notBefore: time.Date(2099, 1, 2, 0, 0, 0, 0, time.UTC), notAfter: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)},
			want: testLabel + ": the client certificate is not valid until 2099-01-02",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tt.opts.client = true
			leaf, key := ca.issue(t, tt.opts)
			dir := t.TempDir()
			writeFile(t, dir, "client.crt", certPEM(leaf))
			writeFile(t, dir, "client.key", keyPEM(t, key))
			_, warnings, err := Load(testLabel, cmnconfig.BrowserClientCertificate{
				Hosts: []string{"*"}, CertFile: "client.crt", KeyFile: "client.key",
			}, dir, now)
			require.NoError(t, err)
			assert.Equal(t, []string{tt.want}, warnings)
		})
	}
}

func TestLoadCAFile(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "client CA")
	serverCA := newTestCA(t, "server CA")
	leaf, key := ca.issue(t, certOptions{name: "client", client: true})
	dir := t.TempDir()
	writeFile(t, dir, "client.crt", certPEM(leaf))
	writeFile(t, dir, "client.key", keyPEM(t, key))
	writeFile(t, dir, "server-ca.pem", serverCA.pem())
	writeFile(t, dir, "empty.pem", "not a certificate\n")
	cfg := cmnconfig.BrowserClientCertificate{Hosts: []string{"*"}, CertFile: "client.crt", KeyFile: "client.key", CAFile: "server-ca.pem"}

	entry, _, err := Load(testLabel, cfg, dir, time.Now())
	require.NoError(t, err)
	roots := parseChain(t, entry.CAPEM)
	require.Len(t, roots, 1)
	assert.True(t, roots[0].Equal(serverCA.cert))

	cfg.CAFile = "empty.pem"
	_, _, err = Load(testLabel, cfg, dir, time.Now())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "holds no PEM certificate")
}

// A ca_file holds trust anchors: authorities, or self-signed certificates
// such as an appliance's. Text around the certificates, as openssl writes
// it, is ignored; a private key or a damaged certificate is an error.
func TestLoadTrustedCA(t *testing.T) {
	t.Parallel()
	ca := newTestCA(t, "server CA")
	leaf, key := ca.issue(t, certOptions{name: siteHost})
	appliance, _ := newSelfSigned(t, siteHost)
	for name, tt := range map[string]struct {
		data    string
		want    []*x509.Certificate
		wantErr string
	}{
		"Authority":            {data: ca.pem(), want: []*x509.Certificate{ca.cert}},
		"SelfSigned":           {data: certPEM(appliance), want: []*x509.Certificate{appliance}},
		"Bundle":               {data: ca.pem() + certPEM(appliance), want: []*x509.Certificate{ca.cert, appliance}},
		"OpenSSLText":          {data: "subject=CN = server CA\nissuer=CN = server CA\n" + ca.pem() + "\n# end of bundle\n", want: []*x509.Certificate{ca.cert}},
		"IssuedLeaf":           {data: ca.pem() + certPEM(leaf), wantErr: "neither a certificate authority nor self-signed"},
		"PrivateKey":           {data: ca.pem() + keyPEM(t, key), wantErr: "private key"},
		"MalformedPrivateKey":  {data: "-----BEGIN PRIVATE KEY-----\ninvalid\n" + ca.pem(), wantErr: "private key"},
		"MalformedCertificate": {data: "-----BEGIN CERTIFICATE-----\ninvalid\n" + ca.pem(), wantErr: "malformed"},
		"NoCertificate":        {data: "not a certificate", wantErr: "holds no PEM certificate"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeFile(t, dir, "ca.pem", tt.data)
			entry, err := LoadTrustedCA(testLabel, cmnconfig.BrowserTrustedCA{Hosts: []string{siteHost}, CAFile: filepath.Join(dir, "ca.pem")})
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, []string{siteHost}, entry.Hosts)
			got := parseChain(t, entry.CAPEM)
			require.Len(t, got, len(tt.want))
			for i, cert := range tt.want {
				assert.True(t, got[i].Equal(cert))
			}
		})
	}
}

func TestLoadRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	for name, cfg := range map[string]cmnconfig.BrowserClientCertificate{
		"NoHosts":     {PFXFile: "client.pfx"},
		"BadHost":     {Hosts: []string{"https://portal.example.com"}, PFXFile: "client.pfx"},
		"Unresolved":  {Hosts: []string{"${HOST}"}, PFXFile: "client.pfx"},
		"NoFile":      {Hosts: []string{"*"}},
		"MissingFile": {Hosts: []string{"*"}, PFXFile: "missing.pfx"},
	} {
		_, _, err := Load(testLabel, cfg, t.TempDir(), time.Now())
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), testLabel, name)
	}
}
