// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A coordinator writes the certificate of the CA it uses at start when it is
// missing or differs, writes it again on refresh only when it is missing, and
// a coordinator with own certificates removes it (Spec 108 §Published CA
// Certificate).
func TestPublishedCertificate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coordinator", "peer-ca.crt")
	first, second := newTestCA(t), newTestCA(t)

	require.NoError(t, first.PublishCertificate(path))
	published := readPublished(t, path)
	assert.Contains(t, string(published), "BEGIN CERTIFICATE")
	assert.NotContains(t, string(published), "PRIVATE KEY")

	// A refresh keeps a certificate that is in place.
	require.NoError(t, second.republishCertificate(path))
	assert.Equal(t, published, readPublished(t, path))

	// A start with another CA replaces it.
	require.NoError(t, second.PublishCertificate(path))
	assert.NotEqual(t, published, readPublished(t, path))

	require.NoError(t, RemovePublishedCertificate(path))
	assert.NoFileExists(t, path)
	require.NoError(t, RemovePublishedCertificate(path), "removing a missing file")

	require.NoError(t, first.republishCertificate(path))
	assert.Equal(t, published, readPublished(t, path))
}

// A coordinator whose publication failed at start replaces, on its next
// registry refresh, the certificate another CA left in the file.
func TestRepublishAfterFailedStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peer-ca.crt")
	previous, current := newTestCA(t), newTestCA(t)
	require.NoError(t, previous.PublishCertificate(path))

	srv := &Service{}
	srv.ServeBuiltinTLS(current, path, false)
	srv.republishCA(t.Context())

	assert.Equal(t, readPublishedOf(t, current), readPublished(t, path))
}

func readPublished(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}

// readPublishedOf returns the published certificate of ca.
func readPublishedOf(t *testing.T, ca *PeerCA) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "peer-ca.crt")
	require.NoError(t, ca.PublishCertificate(path))
	return readPublished(t, path)
}
