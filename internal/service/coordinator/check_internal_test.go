// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A certificate that a CA of the same name signed does not sign itself,
// though its issuer and subject match.
func TestSignsItself(t *testing.T) {
	t.Parallel()

	name := pkix.Name{CommonName: "Dagu"}
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	ca := createCertificate(t, name, caKey, nil, nil)
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leaf := createCertificate(t, name, leafKey, ca, caKey)

	assert.True(t, signsItself(ca))
	assert.False(t, signsItself(leaf))
}

// createCertificate returns a certificate named name for key, signed by
// parent and parentKey, or by itself when parent is nil.
func createCertificate(t *testing.T, name pkix.Name, key *ecdsa.PrivateKey, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               name,
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  parent == nil,
		BasicConstraintsValid: true,
	}
	if parent == nil {
		parent, parentKey = template, key
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, parentKey)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert
}
