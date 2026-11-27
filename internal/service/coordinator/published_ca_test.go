// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

// A coordinator writes the certificate of the CA it uses at start when it is
// missing or differs, writes it again on refresh only when it is missing, and
// a coordinator with own certificates removes it (Spec 108 §Published CA
// Certificate).
func TestPublishedCertificate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coordinator", "peer-ca.crt")
	first, second := newTestCA(t), newTestCA(t)

	publish(t, first, path)
	published := readPublished(t, path)
	assert.Contains(t, string(published), "BEGIN CERTIFICATE")
	assert.NotContains(t, string(published), "PRIVATE KEY")

	// A refresh keeps a certificate that is in place.
	require.NoError(t, second.republishCertificate(path))
	assert.Equal(t, published, readPublished(t, path))

	// A start with another CA replaces it.
	publish(t, second, path)
	assert.NotEqual(t, published, readPublished(t, path))

	require.NoError(t, RemovePublishedCertificate(path))
	assert.NoFileExists(t, path)
	require.NoError(t, RemovePublishedCertificate(path), "removing a missing file")

	require.NoError(t, first.republishCertificate(path))
	assert.Equal(t, published, readPublished(t, path))
}

// A coordinator that does not start puts back the certificate it replaced,
// but leaves one that another coordinator wrote since.
func TestUnpublishCertificate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peer-ca.crt")
	running, failed, later := newTestCA(t), newTestCA(t), newTestCA(t)
	publish(t, running, path)
	before := readPublished(t, path)

	restore, err := failed.PublishCertificate(path)
	require.NoError(t, err)
	restore()
	assert.Equal(t, before, readPublished(t, path))

	restore, err = failed.PublishCertificate(path)
	require.NoError(t, err)
	publish(t, later, path)
	restore()
	assert.Equal(t, readPublishedOf(t, later), readPublished(t, path))

	missing := filepath.Join(t.TempDir(), "peer-ca.crt")
	restore, err = failed.PublishCertificate(missing)
	require.NoError(t, err)
	restore()
	assert.NoFileExists(t, missing)
}

// A coordinator whose publication failed at start replaces, on its next
// registry refresh, the certificate another CA left in the file.
func TestRepublishAfterFailedStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peer-ca.crt")
	previous, current := newTestCA(t), newTestCA(t)
	publish(t, previous, path)

	srv := &Service{}
	srv.ServeBuiltinTLS(current, path, false)
	srv.republishCA(t.Context())

	assert.Equal(t, readPublishedOf(t, current), readPublished(t, path))
}

func publish(t *testing.T, ca *PeerCA, path string) {
	t.Helper()
	_, err := ca.PublishCertificate(path)
	require.NoError(t, err)
}

func readPublished(t *testing.T, path string) []byte {
	t.Helper()
	data, err := fileutil.ReadFile(path)
	require.NoError(t, err)
	return data
}

// readPublishedOf returns the published certificate of ca.
func readPublishedOf(t *testing.T, ca *PeerCA) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "peer-ca.crt")
	publish(t, ca, path)
	return readPublished(t, path)
}
