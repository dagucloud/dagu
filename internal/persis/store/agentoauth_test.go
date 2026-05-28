// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/agentoauth"
	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
)

func newTestEncryptor(t *testing.T) *crypto.Encryptor {
	t.Helper()
	raw := make([]byte, 32)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	enc, err := crypto.NewEncryptor(base64.StdEncoding.EncodeToString(raw))
	require.NoError(t, err)
	return enc
}

func newMemoryAgentOAuthStore(t *testing.T) *store.AgentOAuthStore {
	t.Helper()
	s, err := store.NewAgentOAuthStore(testutil.NewMemoryBackend().Collection("oauth"), newTestEncryptor(t))
	require.NoError(t, err)
	return s
}

func newFileAgentOAuthStore(t *testing.T, dir string) (*store.AgentOAuthStore, *crypto.Encryptor) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o750))
	enc := newTestEncryptor(t)
	s, err := store.NewAgentOAuthStore(file.NewCollection(dir, file.WithIndentedJSON()), enc)
	require.NoError(t, err)
	return s, enc
}

func sampleCredential(provider string) *agentoauth.Credential {
	return &agentoauth.Credential{
		Provider:     provider,
		AccessToken:  "access-token-value",
		RefreshToken: "refresh-token-value",
		ExpiresAt:    time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
		AccountID:    "account-123",
		UpdatedAt:    time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC),
	}
}

func TestAgentOAuthStore_SetGet_RoundTrip(t *testing.T) {
	t.Parallel()
	s := newMemoryAgentOAuthStore(t)
	ctx := context.Background()
	cred := sampleCredential("anthropic")
	require.NoError(t, s.Set(ctx, cred))

	got, err := s.Get(ctx, "anthropic")
	require.NoError(t, err)
	assert.Equal(t, cred.AccessToken, got.AccessToken)
	assert.Equal(t, cred.RefreshToken, got.RefreshToken)
	assert.Equal(t, cred.AccountID, got.AccountID)
	assert.True(t, got.ExpiresAt.Equal(cred.ExpiresAt))
	assert.True(t, got.UpdatedAt.Equal(cred.UpdatedAt))
}

func TestAgentOAuthStore_Get_NotFound(t *testing.T) {
	t.Parallel()
	s := newMemoryAgentOAuthStore(t)
	_, err := s.Get(context.Background(), "missing")
	assert.ErrorIs(t, err, agentoauth.ErrCredentialNotFound)
}

func TestAgentOAuthStore_Delete(t *testing.T) {
	t.Parallel()
	s := newMemoryAgentOAuthStore(t)
	ctx := context.Background()
	require.NoError(t, s.Set(ctx, sampleCredential("anthropic")))
	require.NoError(t, s.Delete(ctx, "anthropic"))
	_, err := s.Get(ctx, "anthropic")
	assert.ErrorIs(t, err, agentoauth.ErrCredentialNotFound)
}

func TestAgentOAuthStore_List_SortedByProvider(t *testing.T) {
	t.Parallel()
	s := newMemoryAgentOAuthStore(t)
	ctx := context.Background()
	require.NoError(t, s.Set(ctx, sampleCredential("zeta")))
	require.NoError(t, s.Set(ctx, sampleCredential("alpha")))
	require.NoError(t, s.Set(ctx, sampleCredential("mu")))

	got, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, "alpha", got[0].Provider)
	assert.Equal(t, "mu", got[1].Provider)
	assert.Equal(t, "zeta", got[2].Provider)
}

func TestAgentOAuthStore_InvalidProvider(t *testing.T) {
	t.Parallel()
	s := newMemoryAgentOAuthStore(t)
	ctx := context.Background()

	_, err := s.Get(ctx, "Bad-Provider")
	assert.Error(t, err)

	err = s.Set(ctx, sampleCredential(""))
	assert.Error(t, err)
}

// Encrypted tokens never appear as plaintext in the on-disk file.
func TestAgentOAuthStore_File_OnDiskIsEncrypted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s, _ := newFileAgentOAuthStore(t, dir)
	cred := sampleCredential("anthropic")
	require.NoError(t, s.Set(context.Background(), cred))

	raw, err := os.ReadFile(filepath.Join(dir, "anthropic.json"))
	require.NoError(t, err)
	content := string(raw)
	assert.NotContains(t, content, cred.AccessToken)
	assert.NotContains(t, content, cred.RefreshToken)
}

// Credential round-trips through reopened store (encryptor reused, file on disk).
func TestAgentOAuthStore_File_RebuildsOnReopen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(dir, 0o750))
	enc := newTestEncryptor(t)

	s1, err := store.NewAgentOAuthStore(file.NewCollection(dir, file.WithIndentedJSON()), enc)
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, s1.Set(ctx, sampleCredential("anthropic")))

	s2, err := store.NewAgentOAuthStore(file.NewCollection(dir, file.WithIndentedJSON()), enc)
	require.NoError(t, err)
	got, err := s2.Get(ctx, "anthropic")
	require.NoError(t, err)
	assert.Equal(t, "access-token-value", got.AccessToken)
}
