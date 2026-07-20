// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/persis"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
)

func newUserStore(t *testing.T) *store.UserStore {
	t.Helper()
	col := testutil.NewMemoryBackend().Collection("users")
	s, err := store.NewUserStore(col)
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

func TestUserCreate(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)
	u := newUser("alice")

	require.NoError(t, s.Create(ctx, u))

	got, err := s.GetByID(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)
	assert.Equal(t, u.Username, got.Username)
	assert.Equal(t, u.PasswordHash, got.PasswordHash)
}

func TestUserCreate_DuplicateUsername(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)

	require.NoError(t, s.Create(ctx, newUser("alice")))

	dup := newUser("alice")
	dup.ID = "other-id"
	assert.ErrorIs(t, s.Create(ctx, dup), auth.ErrUserAlreadyExists)
}

func TestUserCreate_DuplicateOIDCIdentity(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)

	u1 := newUser("alice")
	u1.OIDCIssuer = "https://issuer.example"
	u1.OIDCSubject = "sub-1"
	require.NoError(t, s.Create(ctx, u1))

	u2 := newUser("bob")
	u2.OIDCIssuer = "https://issuer.example"
	u2.OIDCSubject = "sub-1"
	assert.ErrorIs(t, s.Create(ctx, u2), auth.ErrOIDCIdentityAlreadyExists)
}

func TestUserCreate_DuplicateTrustedProxyIdentity(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)

	u1 := newUser("alice")
	u1.AuthProvider = auth.AuthProviderProxy
	u1.TrustedProxySource = "edge-a"
	u1.TrustedProxyUser = "opaque-user"
	require.NoError(t, s.Create(ctx, u1))

	u2 := newUser("bob")
	u2.AuthProvider = auth.AuthProviderProxy
	u2.TrustedProxySource = "edge-a"
	u2.TrustedProxyUser = "opaque-user"
	assert.ErrorIs(t, s.Create(ctx, u2), auth.ErrTrustedProxyIdentityAlreadyExists)
}

func TestUserCreate_AllowsSameTrustedProxyUserFromDifferentSources(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)

	first := newUser("alice")
	first.AuthProvider = auth.AuthProviderProxy
	first.TrustedProxySource = "edge-a"
	first.TrustedProxyUser = "opaque-user"
	require.NoError(t, s.Create(ctx, first))

	second := newUser("bob")
	second.AuthProvider = auth.AuthProviderProxy
	second.TrustedProxySource = "edge-b"
	second.TrustedProxyUser = "opaque-user"
	require.NoError(t, s.Create(ctx, second))

	got, err := s.GetByTrustedProxyIdentity(ctx, "edge-a", "opaque-user")
	require.NoError(t, err)
	assert.Equal(t, first.ID, got.ID)
	got, err = s.GetByTrustedProxyIdentity(ctx, "edge-b", "opaque-user")
	require.NoError(t, err)
	assert.Equal(t, second.ID, got.ID)
	_, err = s.GetByTrustedProxyIdentity(ctx, "", "opaque-user")
	assert.ErrorIs(t, err, auth.ErrTrustedProxyIdentityNotFound)
}

func TestUserCreate_ReportsTrustedIdentityConflictBeforeUsernameConflict(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)
	u1 := newUser("same-name")
	u1.AuthProvider = auth.AuthProviderProxy
	u1.TrustedProxyUser = "same-identity"
	require.NoError(t, s.Create(ctx, u1))
	u2 := newUser("same-name")
	u2.ID = "different-id"
	u2.AuthProvider = auth.AuthProviderProxy
	u2.TrustedProxyUser = "same-identity"

	assert.ErrorIs(t, s.Create(ctx, u2), auth.ErrTrustedProxyIdentityAlreadyExists)
}

func TestUserGetByID_NotFound(t *testing.T) {
	ctx := context.Background()
	_, err := newUserStore(t).GetByID(ctx, "missing")
	assert.ErrorIs(t, err, auth.ErrUserNotFound)
}

func TestUserGetByUsername(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)
	u := newUser("bob")
	require.NoError(t, s.Create(ctx, u))

	got, err := s.GetByUsername(ctx, "bob")
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)
}

func TestUserGetByUsername_NotFound(t *testing.T) {
	ctx := context.Background()
	_, err := newUserStore(t).GetByUsername(ctx, "nobody")
	assert.ErrorIs(t, err, auth.ErrUserNotFound)
}

func TestUserGetByOIDCIdentity(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)
	u := newUser("carol")
	u.OIDCIssuer = "https://accounts.example.com"
	u.OIDCSubject = "sub-carol"
	require.NoError(t, s.Create(ctx, u))

	got, err := s.GetByOIDCIdentity(ctx, "https://accounts.example.com", "sub-carol")
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)
}

func TestUserGetByOIDCIdentity_NotFound(t *testing.T) {
	ctx := context.Background()
	_, err := newUserStore(t).GetByOIDCIdentity(ctx, "https://x.example", "unknown")
	assert.ErrorIs(t, err, auth.ErrOIDCIdentityNotFound)
}

func TestUserGetByTrustedProxyIdentity(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)
	u := newUser("proxy-user")
	u.AuthProvider = auth.AuthProviderProxy
	u.TrustedProxySource = "edge-a"
	u.TrustedProxyUser = "opaque-proxy-id"
	require.NoError(t, s.Create(ctx, u))

	got, err := s.GetByTrustedProxyIdentity(ctx, "edge-a", "opaque-proxy-id")
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)
	assert.Equal(t, "edge-a", got.TrustedProxySource)
	_, err = s.GetByTrustedProxyIdentity(ctx, "edge-b", "opaque-proxy-id")
	assert.ErrorIs(t, err, auth.ErrTrustedProxyIdentityNotFound)
}

func TestUserGetByTrustedProxyIdentity_NotFound(t *testing.T) {
	ctx := context.Background()
	_, err := newUserStore(t).GetByTrustedProxyIdentity(ctx, "", "unknown")
	assert.ErrorIs(t, err, auth.ErrTrustedProxyIdentityNotFound)
}

func TestUserList(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)
	for _, name := range []string{"u1", "u2", "u3"} {
		require.NoError(t, s.Create(ctx, newUser(name)))
	}
	list, err := s.List(ctx)
	require.NoError(t, err)
	assert.Len(t, list, 3)
}

func TestUserUpdate(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)
	u := newUser("dave")
	require.NoError(t, s.Create(ctx, u))

	u.PasswordHash = "new-hash"
	require.NoError(t, s.Update(ctx, u))

	got, err := s.GetByID(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, "new-hash", got.PasswordHash)
}

func TestUserUpdate_NotFound(t *testing.T) {
	ctx := context.Background()
	assert.ErrorIs(t, newUserStore(t).Update(ctx, newUser("ghost")), auth.ErrUserNotFound)
}

func TestUserUpdate_UsernameChange(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)
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

func TestUserUpdate_UsernameConflict(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)
	require.NoError(t, s.Create(ctx, newUser("frank")))
	g := newUser("grace")
	require.NoError(t, s.Create(ctx, g))

	g.Username = "frank"
	assert.ErrorIs(t, s.Update(ctx, g), auth.ErrUserAlreadyExists)
}

func TestUserUpdate_OIDCIdentityChange(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)
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

func TestUserUpdate_TrustedProxyIdentityIsImmutable(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)
	u := newUser("proxy-renamed")
	u.AuthProvider = auth.AuthProviderProxy
	u.TrustedProxyUser = "old-identity"
	require.NoError(t, s.Create(ctx, u))

	u.TrustedProxyUser = "new-identity"
	assert.ErrorIs(t, s.Update(ctx, u), auth.ErrTrustedProxyIdentityImmutable)

	got, err := s.GetByTrustedProxyIdentity(ctx, "", "old-identity")
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)
	_, err = s.GetByTrustedProxyIdentity(ctx, "", "new-identity")
	assert.ErrorIs(t, err, auth.ErrTrustedProxyIdentityNotFound)
}

func TestUserUpdate_TrustedProxySourceIsImmutable(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)
	u := newUser("proxy-source")
	u.AuthProvider = auth.AuthProviderProxy
	u.TrustedProxySource = "edge-a"
	u.TrustedProxyUser = "stable-identity"
	require.NoError(t, s.Create(ctx, u))

	u.TrustedProxySource = "edge-b"
	assert.ErrorIs(t, s.Update(ctx, u), auth.ErrTrustedProxyIdentityImmutable)

	got, err := s.GetByTrustedProxyIdentity(ctx, "edge-a", "stable-identity")
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)
	_, err = s.GetByTrustedProxyIdentity(ctx, "edge-b", "stable-identity")
	assert.ErrorIs(t, err, auth.ErrTrustedProxyIdentityNotFound)
}

func TestUserUpdate_TrustedProxyProviderCannotBeRemoved(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)
	u := newUser("proxy-provider")
	u.AuthProvider = auth.AuthProviderProxy
	u.TrustedProxyUser = "stable-identity"
	require.NoError(t, s.Create(ctx, u))

	u.AuthProvider = auth.AuthProviderBuiltin
	u.TrustedProxyUser = ""
	assert.ErrorIs(t, s.Update(ctx, u), auth.ErrTrustedProxyIdentityImmutable)

	got, err := s.GetByTrustedProxyIdentity(ctx, "", "stable-identity")
	require.NoError(t, err)
	assert.Equal(t, auth.AuthProviderProxy, got.AuthProvider)
}

func TestUserPatchPreservesUnspecifiedAuthorization(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)
	u := newUser("proxy-patch")
	u.AuthProvider = auth.AuthProviderProxy
	u.TrustedProxySource = "edge-a"
	u.TrustedProxyUser = "patch-identity"
	require.NoError(t, s.Create(ctx, u))

	_, err := s.SyncAuthorization(ctx, u.ID, auth.RoleViewer, &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{}})
	require.NoError(t, err)
	username := "proxy-renamed"
	patched, err := s.Patch(ctx, u.ID, auth.UserPatch{Username: &username})
	require.NoError(t, err)
	assert.Equal(t, username, patched.Username)
	assert.Equal(t, auth.RoleViewer, patched.Role)
	assert.False(t, patched.WorkspaceAccess.All)
	assert.Empty(t, patched.WorkspaceAccess.Grants)
	assert.Equal(t, "edge-a", patched.TrustedProxySource)
	assert.Equal(t, "patch-identity", patched.TrustedProxyUser)
	assert.Equal(t, u.PasswordHash, patched.PasswordHash)
}

func TestUserSyncAuthorizationPreservesAdministrativeFields(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)
	u := newUser("proxy-original")
	u.AuthProvider = auth.AuthProviderProxy
	u.TrustedProxySource = "edge-a"
	u.TrustedProxyUser = "stable-identity"
	u.Role = auth.RoleViewer
	require.NoError(t, s.Create(ctx, u))

	u.Username = "proxy-renamed"
	require.NoError(t, s.Update(ctx, u))
	updated, err := s.SyncAuthorization(ctx, u.ID, auth.RoleOperator, &auth.WorkspaceAccess{
		Grants: []auth.WorkspaceGrant{{Workspace: "payments", Role: auth.RoleDeveloper}},
	})
	require.NoError(t, err)
	assert.Equal(t, "proxy-renamed", updated.Username)
	assert.Equal(t, "edge-a", updated.TrustedProxySource)
	assert.Equal(t, "stable-identity", updated.TrustedProxyUser)
	assert.Equal(t, auth.RoleOperator, updated.Role)
	assert.Equal(t, &auth.WorkspaceAccess{
		Grants: []auth.WorkspaceGrant{{Workspace: "payments", Role: auth.RoleDeveloper}},
	}, updated.WorkspaceAccess)
}

func TestUserSyncAuthorizationPreservesWorkspaceAccessWhenUnmanaged(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)
	u := newUser("oidc-role-only")
	u.Role = auth.RoleViewer
	u.WorkspaceAccess = &auth.WorkspaceAccess{
		Grants: []auth.WorkspaceGrant{{Workspace: "payments", Role: auth.RoleDeveloper}},
	}
	require.NoError(t, s.Create(ctx, u))

	updated, err := s.SyncAuthorization(ctx, u.ID, auth.RoleViewer, nil)
	require.NoError(t, err)
	assert.Equal(t, u.WorkspaceAccess, updated.WorkspaceAccess)
}

func TestUserSyncAuthorizationRejectsDisabledUser(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)
	u := newUser("proxy-disabled")
	u.AuthProvider = auth.AuthProviderProxy
	u.TrustedProxyUser = "disabled-identity"
	u.IsDisabled = true
	require.NoError(t, s.Create(ctx, u))

	updated, err := s.SyncAuthorization(ctx, u.ID, auth.RoleViewer, auth.AllWorkspaceAccess())
	assert.Nil(t, updated)
	assert.ErrorIs(t, err, auth.ErrUserDisabled)

	stored, err := s.GetByID(ctx, u.ID)
	require.NoError(t, err)
	assert.True(t, stored.IsDisabled)
	assert.Equal(t, auth.RoleAdmin, stored.Role)
}

func TestUserCreate_ValidatesTrustedProxyProviderAndIdentity(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)

	missingIdentity := newUser("missing-identity")
	missingIdentity.AuthProvider = auth.AuthProviderProxy
	assert.ErrorIs(t, s.Create(ctx, missingIdentity), auth.ErrInvalidTrustedProxyIdentity)

	wrongProvider := newUser("wrong-provider")
	wrongProvider.TrustedProxyUser = "opaque-id"
	assert.ErrorIs(t, s.Create(ctx, wrongProvider), auth.ErrInvalidTrustedProxyIdentity)

	wrongProviderSource := newUser("wrong-provider-source")
	wrongProviderSource.TrustedProxySource = "edge-a"
	assert.ErrorIs(t, s.Create(ctx, wrongProviderSource), auth.ErrInvalidTrustedProxyIdentity)

	mixedIdentity := newUser("mixed-identity")
	mixedIdentity.AuthProvider = auth.AuthProviderProxy
	mixedIdentity.TrustedProxyUser = "opaque-id"
	mixedIdentity.OIDCIssuer = "https://issuer.example"
	assert.ErrorIs(t, s.Create(ctx, mixedIdentity), auth.ErrInvalidTrustedProxyIdentity)
}

func TestUserDelete(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)
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

func TestUserDelete_TrustedProxyIdentity(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)
	u := newUser("proxy-delete")
	u.AuthProvider = auth.AuthProviderProxy
	u.TrustedProxySource = "edge-a"
	u.TrustedProxyUser = "delete-identity"
	require.NoError(t, s.Create(ctx, u))

	require.NoError(t, s.Delete(ctx, u.ID))
	_, err := s.GetByTrustedProxyIdentity(ctx, "edge-a", "delete-identity")
	assert.ErrorIs(t, err, auth.ErrTrustedProxyIdentityNotFound)
}

func TestUserDelete_NotFound(t *testing.T) {
	ctx := context.Background()
	assert.ErrorIs(t, newUserStore(t).Delete(ctx, "nope"), auth.ErrUserNotFound)
}

func TestUserCount(t *testing.T) {
	ctx := context.Background()
	s := newUserStore(t)

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

func TestUserIndexRebuiltOnStartup(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("users")

	s1, err := store.NewUserStore(col)
	require.NoError(t, err)
	require.NoError(t, s1.Create(ctx, newUser("kate")))
	leo := newUser("leo")
	leo.AuthProvider = auth.AuthProviderProxy
	leo.TrustedProxySource = "edge-a"
	leo.TrustedProxyUser = "leo-proxy-id"
	require.NoError(t, s1.Create(ctx, leo))
	maya := newUser("maya")
	maya.AuthProvider = auth.AuthProviderProxy
	maya.TrustedProxySource = "edge-b"
	maya.TrustedProxyUser = "leo-proxy-id"
	require.NoError(t, s1.Create(ctx, maya))

	s2, err := store.NewUserStore(col)
	require.NoError(t, err)

	got, err := s2.GetByUsername(ctx, "kate")
	require.NoError(t, err)
	assert.Equal(t, "id-kate", got.ID)
	got, err = s2.GetByTrustedProxyIdentity(ctx, "edge-a", "leo-proxy-id")
	require.NoError(t, err)
	assert.Equal(t, "id-leo", got.ID)
	got, err = s2.GetByTrustedProxyIdentity(ctx, "edge-b", "leo-proxy-id")
	require.NoError(t, err)
	assert.Equal(t, "id-maya", got.ID)

	n, err := s2.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(3), n)
}

func TestUserIndexRebuildRejectsDuplicateTrustedProxyIdentity(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("users")
	now := time.Now().UTC()
	for _, username := range []string{"first", "second"} {
		user := newUser(username)
		user.AuthProvider = auth.AuthProviderProxy
		user.TrustedProxySource = "edge-a"
		user.TrustedProxyUser = "duplicate-identity"
		data, err := persis.Encode(user.ToStorage())
		require.NoError(t, err)
		require.NoError(t, col.Put(ctx, &persis.Record{
			ID: user.ID, Data: data, CreatedAt: now, UpdatedAt: now,
		}))
	}

	_, err := store.NewUserStore(col)
	assert.ErrorIs(t, err, auth.ErrTrustedProxyIdentityAlreadyExists)
}

type failingUserCollection struct {
	persis.Collection
	putErr    error
	deleteErr error
}

func (c *failingUserCollection) Put(ctx context.Context, record *persis.Record) error {
	if c.putErr != nil {
		return c.putErr
	}
	return c.Collection.Put(ctx, record)
}

func (c *failingUserCollection) Delete(ctx context.Context, id string) error {
	if c.deleteErr != nil {
		return c.deleteErr
	}
	return c.Collection.Delete(ctx, id)
}

func TestUserTrustedProxyIndexChangesOnlyAfterPersistence(t *testing.T) {
	ctx := context.Background()
	collection := &failingUserCollection{
		Collection: testutil.NewMemoryBackend().Collection("users"),
	}
	userStore, err := store.NewUserStore(collection)
	require.NoError(t, err)
	user := newUser("proxy-index")
	user.AuthProvider = auth.AuthProviderProxy
	user.TrustedProxySource = "edge-a"
	user.TrustedProxyUser = "proxy-index-identity"

	collection.putErr = errors.New("put failed")
	assert.Error(t, userStore.Create(ctx, user))
	_, err = userStore.GetByTrustedProxyIdentity(ctx, user.TrustedProxySource, user.TrustedProxyUser)
	assert.ErrorIs(t, err, auth.ErrTrustedProxyIdentityNotFound)

	collection.putErr = nil
	require.NoError(t, userStore.Create(ctx, user))
	collection.deleteErr = errors.New("delete failed")
	assert.Error(t, userStore.Delete(ctx, user.ID))
	got, err := userStore.GetByTrustedProxyIdentity(ctx, user.TrustedProxySource, user.TrustedProxyUser)
	require.NoError(t, err)
	assert.Equal(t, user.ID, got.ID)
}
