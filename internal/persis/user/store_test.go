// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package user_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	"github.com/dagucloud/dagu/internal/persis/user"
)

func newStore(t *testing.T) *user.Store {
	t.Helper()
	col := testutil.NewMemoryBackend().Collection("users")
	s, err := user.New(col)
	require.NoError(t, err)
	return s
}

func newUser(username string) *auth.User {
	now := time.Now().UTC()
	return &auth.User{
		ID:           "id-" + username,
		Username:     username,
		PasswordHash: "hash-" + username,
		Role:         auth.RoleAdmin,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func TestCreate(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := newUser("alice")

	require.NoError(t, s.Create(ctx, u))

	got, err := s.GetByID(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)
	assert.Equal(t, u.Username, got.Username)
	assert.Equal(t, u.PasswordHash, got.PasswordHash)
}

func TestCreate_DuplicateUsername(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	require.NoError(t, s.Create(ctx, newUser("alice")))

	dup := newUser("alice")
	dup.ID = "other-id"
	assert.ErrorIs(t, s.Create(ctx, dup), auth.ErrUserAlreadyExists)
}

func TestCreate_DuplicateOIDCIdentity(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	u1 := newUser("alice")
	u1.OIDCIssuer = "https://issuer.example"
	u1.OIDCSubject = "sub-1"
	require.NoError(t, s.Create(ctx, u1))

	u2 := newUser("bob")
	u2.OIDCIssuer = "https://issuer.example"
	u2.OIDCSubject = "sub-1"
	assert.ErrorIs(t, s.Create(ctx, u2), auth.ErrOIDCIdentityAlreadyExists)
}

func TestGetByID_NotFound(t *testing.T) {
	ctx := context.Background()
	_, err := newStore(t).GetByID(ctx, "missing")
	assert.ErrorIs(t, err, auth.ErrUserNotFound)
}

func TestGetByUsername(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := newUser("bob")
	require.NoError(t, s.Create(ctx, u))

	got, err := s.GetByUsername(ctx, "bob")
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)
}

func TestGetByUsername_NotFound(t *testing.T) {
	ctx := context.Background()
	_, err := newStore(t).GetByUsername(ctx, "nobody")
	assert.ErrorIs(t, err, auth.ErrUserNotFound)
}

func TestGetByOIDCIdentity(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := newUser("carol")
	u.OIDCIssuer = "https://accounts.example.com"
	u.OIDCSubject = "sub-carol"
	require.NoError(t, s.Create(ctx, u))

	got, err := s.GetByOIDCIdentity(ctx, "https://accounts.example.com", "sub-carol")
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)
}

func TestGetByOIDCIdentity_NotFound(t *testing.T) {
	ctx := context.Background()
	_, err := newStore(t).GetByOIDCIdentity(ctx, "https://x.example", "unknown")
	assert.ErrorIs(t, err, auth.ErrOIDCIdentityNotFound)
}

func TestList(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	for _, name := range []string{"u1", "u2", "u3"} {
		require.NoError(t, s.Create(ctx, newUser(name)))
	}
	list, err := s.List(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 3)
}

func TestUpdate(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := newUser("dave")
	require.NoError(t, s.Create(ctx, u))

	u.PasswordHash = "new-hash"
	require.NoError(t, s.Update(ctx, u))

	got, err := s.GetByID(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, "new-hash", got.PasswordHash)
}

func TestUpdate_NotFound(t *testing.T) {
	ctx := context.Background()
	assert.ErrorIs(t, newStore(t).Update(ctx, newUser("ghost")), auth.ErrUserNotFound)
}

func TestUpdate_UsernameChange(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := newUser("eve")
	require.NoError(t, s.Create(ctx, u))

	u.Username = "eve-renamed"
	require.NoError(t, s.Update(ctx, u))

	_, err := s.GetByUsername(ctx, "eve")
	assert.ErrorIs(t, err, auth.ErrUserNotFound)

	got, err := s.GetByUsername(ctx, "eve-renamed")
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)
}

func TestUpdate_UsernameConflict(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	require.NoError(t, s.Create(ctx, newUser("frank")))
	g := newUser("grace")
	require.NoError(t, s.Create(ctx, g))

	g.Username = "frank"
	assert.ErrorIs(t, s.Update(ctx, g), auth.ErrUserAlreadyExists)
}

func TestUpdate_OIDCIdentityChange(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := newUser("heidi")
	u.OIDCIssuer = "https://a.example"
	u.OIDCSubject = "old-sub"
	require.NoError(t, s.Create(ctx, u))

	u.OIDCIssuer = "https://a.example"
	u.OIDCSubject = "new-sub"
	require.NoError(t, s.Update(ctx, u))

	_, err := s.GetByOIDCIdentity(ctx, "https://a.example", "old-sub")
	assert.ErrorIs(t, err, auth.ErrOIDCIdentityNotFound)

	got, err := s.GetByOIDCIdentity(ctx, "https://a.example", "new-sub")
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := newUser("ivan")
	u.OIDCIssuer = "https://b.example"
	u.OIDCSubject = "ivan-sub"
	require.NoError(t, s.Create(ctx, u))

	require.NoError(t, s.Delete(ctx, u.ID))

	_, err := s.GetByID(ctx, u.ID)
	assert.ErrorIs(t, err, auth.ErrUserNotFound)

	_, err = s.GetByUsername(ctx, "ivan")
	assert.ErrorIs(t, err, auth.ErrUserNotFound)

	_, err = s.GetByOIDCIdentity(ctx, "https://b.example", "ivan-sub")
	assert.ErrorIs(t, err, auth.ErrOIDCIdentityNotFound)
}

func TestDelete_NotFound(t *testing.T) {
	ctx := context.Background()
	assert.ErrorIs(t, newStore(t).Delete(ctx, "nope"), auth.ErrUserNotFound)
}

func TestCount(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	n, err := s.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)

	require.NoError(t, s.Create(ctx, newUser("j1")))
	require.NoError(t, s.Create(ctx, newUser("j2")))

	n, err = s.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)

	require.NoError(t, s.Delete(ctx, "id-j1"))
	n, err = s.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
}

func TestIndexRebuiltOnStartup(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("users")

	s1, err := user.New(col)
	require.NoError(t, err)
	require.NoError(t, s1.Create(ctx, newUser("kate")))
	require.NoError(t, s1.Create(ctx, newUser("leo")))

	s2, err := user.New(col)
	require.NoError(t, err)

	got, err := s2.GetByUsername(ctx, "kate")
	require.NoError(t, err)
	assert.Equal(t, "id-kate", got.ID)

	n, err := s2.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
}
