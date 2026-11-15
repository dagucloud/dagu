// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
)

func newHostKeyStore(t *testing.T, backend persis.Backend) *store.HostKeyStore {
	t.Helper()
	s, err := store.NewHostKeyStore(backend.Collection(persis.CollectionHostKeys))
	require.NoError(t, err)
	return s
}

func newPublicKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	key, err := ssh.NewPublicKey(pub)
	require.NoError(t, err)
	return key
}

func TestHostKeyStore_Trust(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newHostKeyStore(t, testutil.NewMemoryBackend())
	key := newPublicKey(t)

	trusted, err := s.Trust(ctx, "Tokyo-Web01", key, "alice")
	require.NoError(t, err)
	assert.Equal(t, ssh.FingerprintSHA256(key), trusted.Fingerprint)
	assert.WithinDuration(t, time.Now(), trusted.TrustedAt, time.Minute)

	// A node name is compared without case.
	got, err := s.Get(ctx, "tokyo-web01")
	require.NoError(t, err)
	assert.Equal(t, trusted, got)
	parsed, err := got.PublicKey()
	require.NoError(t, err)
	assert.Equal(t, key.Marshal(), parsed.Marshal())
	assert.Equal(t, "Tokyo-Web01", got.Node)
	assert.Equal(t, "alice", got.TrustedBy)
}

func TestHostKeyStore_TrustReplaces(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newHostKeyStore(t, testutil.NewMemoryBackend())
	_, err := s.Trust(ctx, "web01", newPublicKey(t), "alice")
	require.NoError(t, err)
	rotated := newPublicKey(t)

	_, err = s.Trust(ctx, "WEB01", rotated, "bob")
	require.NoError(t, err)

	got, err := s.Get(ctx, "web01")
	require.NoError(t, err)
	assert.Equal(t, ssh.FingerprintSHA256(rotated), got.Fingerprint)
	assert.Equal(t, "bob", got.TrustedBy)
}

func TestHostKeyStore_GetUntrusted(t *testing.T) {
	t.Parallel()
	s := newHostKeyStore(t, testutil.NewMemoryBackend())

	_, err := s.Get(context.Background(), "web01")
	assert.ErrorIs(t, err, inventory.ErrNoTrustedHostKey)
}

func TestHostKeyStore_TrustRefusesInvalidNodeName(t *testing.T) {
	t.Parallel()
	s := newHostKeyStore(t, testutil.NewMemoryBackend())

	_, err := s.Trust(context.Background(), "../web01", newPublicKey(t), "alice")
	assert.ErrorIs(t, err, inventory.ErrInvalid)
}

func TestHostKeyStore_NewerSchemaRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := testutil.NewMemoryBackend()
	require.NoError(t, backend.Collection(persis.CollectionHostKeys).Put(ctx, &persis.Record{
		ID:   "web01",
		Data: []byte(`{"schema_version":2,"node":"web01","keys":[]}`),
	}))
	s := newHostKeyStore(t, backend)

	_, err := s.Get(ctx, "web01")
	assert.ErrorContains(t, err, "schema version 2")
}
