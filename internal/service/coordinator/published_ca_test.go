// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
)

// A coordinator writes the certificate of the CA it uses at start when it is
// missing or differs, writes it again on refresh only when it is missing, and
// a coordinator with own certificates removes it (Spec 108 §Published CA
// Certificate).
func TestPublishedCertificate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "coordinator", "peer-ca.crt")
	first, err := coordinator.LoadOrCreatePeerCA(filepath.Join(dir, "first.pem"))
	require.NoError(t, err)
	second, err := coordinator.LoadOrCreatePeerCA(filepath.Join(dir, "second.pem"))
	require.NoError(t, err)

	require.NoError(t, first.PublishCertificate(path))
	published, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(published), "BEGIN CERTIFICATE")
	assert.NotContains(t, string(published), "PRIVATE KEY")

	// A refresh keeps a certificate that is in place.
	require.NoError(t, second.RepublishCertificate(path))
	current, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, published, current)

	// A start with another CA replaces it.
	require.NoError(t, second.PublishCertificate(path))
	current, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.NotEqual(t, published, current)

	require.NoError(t, coordinator.RemovePublishedCertificate(path))
	assert.NoFileExists(t, path)
	require.NoError(t, coordinator.RemovePublishedCertificate(path), "removing a missing file")

	require.NoError(t, first.RepublishCertificate(path))
	current, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, published, current)
}
