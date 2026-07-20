// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/auth"
	persiststore "github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func mustTokenSecret(s string) auth.TokenSecret {
	ts, err := auth.NewTokenSecretFromString(s)
	if err != nil {
		panic(err)
	}
	return ts
}

func setupTestService(t *testing.T) (*Service, func()) {
	t.Helper()
	store, err := persiststore.NewUserStore(testutil.NewMemoryBackend().Collection("users"))
	require.NoError(t, err)
	config := Config{
		TokenSecret: mustTokenSecret("test-secret-key-for-jwt-signing"),
		TokenTTL:    time.Hour,
		BcryptCost:  4, // Low cost for faster tests
	}
	return New(store, config), func() {}
}

type pauseAfterGetUserStore struct {
	auth.UserStore
	afterGet chan struct{}
	resume   chan struct{}
	once     sync.Once
}

func (s *pauseAfterGetUserStore) GetByID(ctx context.Context, id string) (*auth.User, error) {
	user, err := s.UserStore.GetByID(ctx, id)
	if err == nil {
		s.once.Do(func() {
			close(s.afterGet)
			<-s.resume
		})
	}
	return user, err
}

func TestService_CreateUser(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	user, err := svc.CreateUser(ctx, CreateUserInput{
		Username: "testuser",
		Password: "password123",
		Role:     auth.RoleManager,
	})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	if user.Username != "testuser" {
		t.Errorf("CreateUser() username = %v, want %v", user.Username, "testuser")
	}
	if user.Role != auth.RoleManager {
		t.Errorf("CreateUser() role = %v, want %v", user.Role, auth.RoleManager)
	}
	if user.PasswordHash == "" {
		t.Error("CreateUser() password hash should not be empty")
	}
	if user.PasswordHash == "password123" {
		t.Error("CreateUser() password should be hashed")
	}
}

func TestService_CreateUser_WeakPassword(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	_, err := svc.CreateUser(ctx, CreateUserInput{
		Username: "testuser",
		Password: "short", // Too short
		Role:     auth.RoleViewer,
	})
	if err == nil {
		t.Error("CreateUser() with weak password should return error")
	}
}

func TestService_Authenticate(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	// Create user
	_, err := svc.CreateUser(ctx, CreateUserInput{
		Username: "testuser",
		Password: "password123",
		Role:     auth.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	// Test successful authentication
	user, err := svc.Authenticate(ctx, "testuser", "password123")
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if user.Username != "testuser" {
		t.Errorf("Authenticate() username = %v, want %v", user.Username, "testuser")
	}

	// Test wrong password
	_, err = svc.Authenticate(ctx, "testuser", "wrongpassword")
	if err != ErrInvalidCredentials {
		t.Errorf("Authenticate() with wrong password error = %v, want %v", err, ErrInvalidCredentials)
	}

	// Test non-existent user
	_, err = svc.Authenticate(ctx, "nonexistent", "password123")
	if err != ErrInvalidCredentials {
		t.Errorf("Authenticate() with non-existent user error = %v, want %v", err, ErrInvalidCredentials)
	}
}

func TestService_AuthenticateRejectsOIDCUserWithPasswordHash(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("password123"), svc.config.BcryptCost)
	require.NoError(t, err)
	user := auth.NewUser("oidc-user", string(passwordHash), auth.RoleViewer)
	user.AuthProvider = "oidc"
	require.NoError(t, svc.store.Create(ctx, user))

	authenticated, err := svc.Authenticate(ctx, user.Username, "password123")
	assert.Nil(t, authenticated)
	assert.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestService_AuthenticateRejectsEveryExternalProvider(t *testing.T) {
	for _, provider := range []string{"oidc", "proxy", "future_provider"} {
		t.Run(provider, func(t *testing.T) {
			svc, cleanup := setupTestService(t)
			defer cleanup()

			ctx := context.Background()
			passwordHash, err := bcrypt.GenerateFromPassword([]byte("password123"), svc.config.BcryptCost)
			require.NoError(t, err)
			user := auth.NewUser(provider+"-user", string(passwordHash), auth.RoleViewer)
			user.AuthProvider = provider
			if provider == auth.AuthProviderProxy {
				user.TrustedProxyUser = provider + "-identity"
			}
			require.NoError(t, svc.store.Create(ctx, user))

			authenticated, err := svc.Authenticate(ctx, user.Username, "password123")
			assert.Nil(t, authenticated)
			assert.ErrorIs(t, err, ErrInvalidCredentials)
		})
	}
}

func TestService_PasswordManagementRejectsEveryExternalProvider(t *testing.T) {
	for _, provider := range []string{"oidc", "proxy", "future_provider"} {
		t.Run(provider, func(t *testing.T) {
			svc, cleanup := setupTestService(t)
			defer cleanup()

			ctx := context.Background()
			user := auth.NewUser(provider+"-user", "legacy-password-hash", auth.RoleViewer)
			user.AuthProvider = provider
			if provider == auth.AuthProviderProxy {
				user.TrustedProxyUser = provider + "-identity"
			}
			require.NoError(t, svc.store.Create(ctx, user))

			newPassword := "newpassword1"
			updated, err := svc.UpdateUser(ctx, user.ID, UpdateUserInput{Password: &newPassword})
			assert.Nil(t, updated)
			assert.ErrorIs(t, err, ErrExternalAuthPasswordManagement)
			assert.ErrorIs(t, svc.ChangePassword(ctx, user.ID, "oldpassword", newPassword), ErrExternalAuthPasswordManagement)
			assert.ErrorIs(t, svc.ResetPassword(ctx, user.ID, newPassword), ErrExternalAuthPasswordManagement)

			stored, err := svc.store.GetByID(ctx, user.ID)
			require.NoError(t, err)
			assert.Equal(t, "legacy-password-hash", stored.PasswordHash)
			assert.Nil(t, stored.PasswordChangedAt)
		})
	}
}

func TestService_GenerateAndValidateToken(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	// Create user
	user, err := svc.CreateUser(ctx, CreateUserInput{
		Username: "testuser",
		Password: "password123",
		Role:     auth.RoleManager,
	})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	// Generate token
	tokenResult, err := svc.GenerateToken(user)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}
	if tokenResult.Token == "" {
		t.Error("GenerateToken() returned empty token")
	}
	if tokenResult.ExpiresAt.IsZero() {
		t.Error("GenerateToken() returned zero expiry time")
	}

	// Validate token
	claims, err := svc.ValidateToken(tokenResult.Token)
	if err != nil {
		t.Fatalf("ValidateToken() error = %v", err)
	}
	if claims.UserID != user.ID {
		t.Errorf("ValidateToken() userID = %v, want %v", claims.UserID, user.ID)
	}
	if claims.Username != user.Username {
		t.Errorf("ValidateToken() username = %v, want %v", claims.Username, user.Username)
	}
	if claims.Role != user.Role {
		t.Errorf("ValidateToken() role = %v, want %v", claims.Role, user.Role)
	}
}

func TestService_ValidateToken_Invalid(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	// Test invalid token
	_, err := svc.ValidateToken("invalid-token")
	if err != ErrInvalidToken {
		t.Errorf("ValidateToken() with invalid token error = %v, want %v", err, ErrInvalidToken)
	}
}

func TestService_GetUserFromToken(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	// Create user
	user, err := svc.CreateUser(ctx, CreateUserInput{
		Username: "testuser",
		Password: "password123",
		Role:     auth.RoleViewer,
	})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	// Generate token
	tokenResult, err := svc.GenerateToken(user)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	// Get user from token
	retrieved, err := svc.GetUserFromToken(ctx, tokenResult.Token)
	if err != nil {
		t.Fatalf("GetUserFromToken() error = %v", err)
	}
	if retrieved.ID != user.ID {
		t.Errorf("GetUserFromToken() ID = %v, want %v", retrieved.ID, user.ID)
	}
}

func TestService_GetUserFromTokenReturnsCurrentAuthorization(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	user, err := svc.CreateUser(ctx, CreateUserInput{
		Username:        "authorization-refresh",
		Password:        "password123",
		Role:            auth.RoleDeveloper,
		WorkspaceAccess: auth.AllWorkspaceAccess(),
	})
	require.NoError(t, err)

	tokenResult, err := svc.GenerateToken(user)
	require.NoError(t, err)

	role := auth.RoleViewer
	workspaceAccess := &auth.WorkspaceAccess{
		Grants: []auth.WorkspaceGrant{{Workspace: "payments", Role: auth.RoleOperator}},
	}
	_, err = svc.UpdateUser(ctx, user.ID, UpdateUserInput{
		Role:            &role,
		WorkspaceAccess: workspaceAccess,
	})
	require.NoError(t, err)

	retrieved, err := svc.GetUserFromToken(ctx, tokenResult.Token)
	require.NoError(t, err)
	assert.Equal(t, auth.RoleViewer, retrieved.Role)
	assert.Equal(t, workspaceAccess, retrieved.WorkspaceAccess)
}

func TestService_ChangePassword(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	// Create user
	user, err := svc.CreateUser(ctx, CreateUserInput{
		Username: "testuser",
		Password: "oldpassword1",
		Role:     auth.RoleManager,
	})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	// Change password
	err = svc.ChangePassword(ctx, user.ID, "oldpassword1", "newpassword1")
	if err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}

	// Verify old password no longer works
	_, err = svc.Authenticate(ctx, "testuser", "oldpassword1")
	if err != ErrInvalidCredentials {
		t.Errorf("Authenticate() with old password should fail")
	}

	// Verify new password works
	_, err = svc.Authenticate(ctx, "testuser", "newpassword1")
	if err != nil {
		t.Errorf("Authenticate() with new password error = %v", err)
	}
}

func TestService_ChangePassword_WrongOldPassword(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	// Create user
	user, err := svc.CreateUser(ctx, CreateUserInput{
		Username: "testuser",
		Password: "password123",
		Role:     auth.RoleViewer,
	})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	// Try to change with wrong old password
	err = svc.ChangePassword(ctx, user.ID, "wrongpassword", "newpassword1")
	if err != ErrPasswordMismatch {
		t.Errorf("ChangePassword() with wrong old password error = %v, want %v", err, ErrPasswordMismatch)
	}
}

func TestService_ChangePasswordRejectsOIDCUser(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	user := auth.NewUser("oidc-user", "legacy-password-hash", auth.RoleViewer)
	user.AuthProvider = "oidc"
	require.NoError(t, svc.store.Create(ctx, user))

	err := svc.ChangePassword(ctx, user.ID, "oldpassword", "newpassword1")
	assert.ErrorIs(t, err, ErrOIDCPasswordManagement)

	stored, getErr := svc.store.GetByID(ctx, user.ID)
	require.NoError(t, getErr)
	assert.Equal(t, "legacy-password-hash", stored.PasswordHash)
	assert.Nil(t, stored.PasswordChangedAt)
}

func TestService_DeleteUser(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	// Create user
	user, err := svc.CreateUser(ctx, CreateUserInput{
		Username: "testuser",
		Password: "password123",
		Role:     auth.RoleManager,
	})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	// Delete user
	err = svc.DeleteUser(ctx, user.ID, "other-user-id")
	if err != nil {
		t.Fatalf("DeleteUser() error = %v", err)
	}

	// Verify user is deleted
	_, err = svc.GetUser(ctx, user.ID)
	if err != auth.ErrUserNotFound {
		t.Errorf("GetUser() after delete error = %v, want %v", err, auth.ErrUserNotFound)
	}
}

func TestService_DeleteUser_CannotDeleteSelf(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	// Create user
	user, err := svc.CreateUser(ctx, CreateUserInput{
		Username: "testuser",
		Password: "password123",
		Role:     auth.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	// Try to delete self
	err = svc.DeleteUser(ctx, user.ID, user.ID)
	if err != ErrCannotDeleteSelf {
		t.Errorf("DeleteUser() self error = %v, want %v", err, ErrCannotDeleteSelf)
	}
}

func TestService_UpdateUser(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	// Create user
	user, err := svc.CreateUser(ctx, CreateUserInput{
		Username: "testuser",
		Password: "password123",
		Role:     auth.RoleViewer,
	})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	// Update role
	newRole := auth.RoleAdmin
	updated, err := svc.UpdateUser(ctx, user.ID, UpdateUserInput{
		Role: &newRole,
	})
	if err != nil {
		t.Fatalf("UpdateUser() error = %v", err)
	}
	if updated.Role != auth.RoleAdmin {
		t.Errorf("UpdateUser() role = %v, want %v", updated.Role, auth.RoleAdmin)
	}

	// Update username
	newUsername := "newusername"
	updated, err = svc.UpdateUser(ctx, user.ID, UpdateUserInput{
		Username: &newUsername,
	})
	if err != nil {
		t.Fatalf("UpdateUser() error = %v", err)
	}
	if updated.Username != "newusername" {
		t.Errorf("UpdateUser() username = %v, want %v", updated.Username, "newusername")
	}
}

func TestService_UpdateUserPreservesOIDCManagedEmptyWorkspaceAccess(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	user := auth.NewUser("oidc-user", "", auth.RoleViewer)
	user.AuthProvider = "oidc"
	user.OIDCIssuer = "https://idp.example.com"
	user.OIDCSubject = "subject-1"
	user.WorkspaceAccess = &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{}}
	require.NoError(t, svc.store.Create(ctx, user))

	username := "renamed-oidc-user"
	updated, err := svc.UpdateUser(ctx, user.ID, UpdateUserInput{Username: &username})
	require.NoError(t, err)
	assert.Equal(t, username, updated.Username)
	assert.False(t, updated.WorkspaceAccess.All)
	assert.Empty(t, updated.WorkspaceAccess.Grants)

	disabled := true
	updated, err = svc.UpdateUser(ctx, user.ID, UpdateUserInput{IsDisabled: &disabled})
	require.NoError(t, err)
	assert.True(t, updated.IsDisabled)

	disabled = false
	updated, err = svc.UpdateUser(ctx, user.ID, UpdateUserInput{IsDisabled: &disabled})
	require.NoError(t, err)
	assert.False(t, updated.IsDisabled)

	stored, err := svc.GetUser(ctx, user.ID)
	require.NoError(t, err)
	assert.Equal(t, username, stored.Username)
	assert.False(t, stored.IsDisabled)
	assert.False(t, stored.WorkspaceAccess.All)
	assert.Empty(t, stored.WorkspaceAccess.Grants)
}

func TestService_UpdateUserPreservesAuthorizationSynchronizedAfterRead(t *testing.T) {
	ctx := context.Background()
	baseStore, err := persiststore.NewUserStore(testutil.NewMemoryBackend().Collection("users"))
	require.NoError(t, err)
	user := auth.NewUser("proxy-user", "", auth.RoleAdmin)
	user.AuthProvider = auth.AuthProviderProxy
	user.TrustedProxyUser = "stable-identity"
	require.NoError(t, baseStore.Create(ctx, user))

	pausingStore := &pauseAfterGetUserStore{
		UserStore: baseStore,
		afterGet:  make(chan struct{}),
		resume:    make(chan struct{}),
	}
	svc := New(pausingStore, Config{
		TokenSecret: mustTokenSecret("test-secret-key-for-jwt-signing"),
		TokenTTL:    time.Hour,
		BcryptCost:  4,
	})

	type updateResult struct {
		user *auth.User
		err  error
	}
	result := make(chan updateResult, 1)
	username := "proxy-renamed"
	go func() {
		updated, updateErr := svc.UpdateUser(ctx, user.ID, UpdateUserInput{Username: &username})
		result <- updateResult{user: updated, err: updateErr}
	}()

	select {
	case <-pausingStore.afterGet:
	case <-time.After(time.Second):
		t.Fatal("UpdateUser did not reach the store read")
	}
	_, syncErr := baseStore.SyncAuthorization(ctx, user.ID, auth.RoleViewer, &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{}})
	close(pausingStore.resume)
	require.NoError(t, syncErr)

	var got updateResult
	select {
	case got = <-result:
	case <-time.After(time.Second):
		t.Fatal("UpdateUser did not complete")
	}
	require.NoError(t, got.err)
	require.NotNil(t, got.user)
	assert.Equal(t, username, got.user.Username)
	assert.Equal(t, auth.RoleViewer, got.user.Role)
	assert.False(t, got.user.WorkspaceAccess.All)
	assert.Empty(t, got.user.WorkspaceAccess.Grants)
}

func TestService_UpdateUserRejectsExplicitEmptyWorkspaceAccess(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	user, err := svc.CreateUser(ctx, CreateUserInput{
		Username: "testuser",
		Password: "password123",
		Role:     auth.RoleViewer,
	})
	require.NoError(t, err)

	_, err = svc.UpdateUser(ctx, user.ID, UpdateUserInput{
		WorkspaceAccess: &auth.WorkspaceAccess{},
	})
	require.ErrorIs(t, err, auth.ErrInvalidWorkspaceAccess)
}

func TestService_ListUsers(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	// Create multiple users
	for i := range 3 {
		_, err := svc.CreateUser(ctx, CreateUserInput{
			Username: fmt.Sprintf("user%d", i),
			Password: "password123",
			Role:     auth.RoleViewer,
		})
		if err != nil {
			t.Fatalf("CreateUser() error = %v", err)
		}
	}

	// List users
	users, err := svc.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
	if len(users) != 3 {
		t.Errorf("ListUsers() returned %d users, want 3", len(users))
	}
}

func TestService_ResetPassword(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	// Create user
	user, err := svc.CreateUser(ctx, CreateUserInput{
		Username: "testuser",
		Password: "oldpassword1",
		Role:     auth.RoleManager,
	})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	// Reset password (admin action, doesn't require old password)
	err = svc.ResetPassword(ctx, user.ID, "newpassword1")
	if err != nil {
		t.Fatalf("ResetPassword() error = %v", err)
	}

	// Verify old password no longer works
	_, err = svc.Authenticate(ctx, "testuser", "oldpassword1")
	if err != ErrInvalidCredentials {
		t.Errorf("Authenticate() with old password should fail")
	}

	// Verify new password works
	_, err = svc.Authenticate(ctx, "testuser", "newpassword1")
	if err != nil {
		t.Errorf("Authenticate() with new password error = %v", err)
	}
}

func TestService_ResetPassword_WeakPassword(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	// Create user
	user, err := svc.CreateUser(ctx, CreateUserInput{
		Username: "testuser",
		Password: "password123",
		Role:     auth.RoleViewer,
	})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	// Try to reset with weak password
	err = svc.ResetPassword(ctx, user.ID, "weak")
	if err == nil {
		t.Error("ResetPassword() with weak password should return error")
	}
}

func TestService_ResetPasswordRejectsOIDCUser(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	user := auth.NewUser("oidc-user", "legacy-password-hash", auth.RoleViewer)
	user.AuthProvider = "oidc"
	require.NoError(t, svc.store.Create(ctx, user))

	err := svc.ResetPassword(ctx, user.ID, "newpassword1")
	assert.ErrorIs(t, err, ErrOIDCPasswordManagement)

	stored, getErr := svc.store.GetByID(ctx, user.ID)
	require.NoError(t, getErr)
	assert.Equal(t, "legacy-password-hash", stored.PasswordHash)
	assert.Nil(t, stored.PasswordChangedAt)
}

func TestService_UpdateUser_WithPassword(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	// Create user
	user, err := svc.CreateUser(ctx, CreateUserInput{
		Username: "testuser",
		Password: "oldpassword1",
		Role:     auth.RoleViewer,
	})
	require.NoError(t, err)

	// Update user with new password via Password field
	newPassword := "newpassword1"
	_, err = svc.UpdateUser(ctx, user.ID, UpdateUserInput{
		Password: &newPassword,
	})
	require.NoError(t, err)

	// Verify old password no longer works
	_, err = svc.Authenticate(ctx, "testuser", "oldpassword1")
	assert.ErrorIs(t, err, ErrInvalidCredentials, "old password should not work")

	// Verify new password works
	_, err = svc.Authenticate(ctx, "testuser", "newpassword1")
	assert.NoError(t, err, "new password should work")
}

func TestService_UpdateUserRejectsPasswordForOIDCUser(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	user := auth.NewUser("oidc-user", "legacy-password-hash", auth.RoleViewer)
	user.AuthProvider = "oidc"
	require.NoError(t, svc.store.Create(ctx, user))

	newPassword := "newpassword1"
	updated, err := svc.UpdateUser(ctx, user.ID, UpdateUserInput{Password: &newPassword})
	assert.Nil(t, updated)
	assert.ErrorIs(t, err, ErrOIDCPasswordManagement)

	stored, getErr := svc.store.GetByID(ctx, user.ID)
	require.NoError(t, getErr)
	assert.Equal(t, "legacy-password-hash", stored.PasswordHash)
	assert.Nil(t, stored.PasswordChangedAt)
}

func TestService_UpdateUser_WeakPassword(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	// Create user
	user, err := svc.CreateUser(ctx, CreateUserInput{
		Username: "testuser",
		Password: "password123",
		Role:     auth.RoleViewer,
	})
	require.NoError(t, err)

	// Try to update with weak password
	weakPassword := "weak"
	_, err = svc.UpdateUser(ctx, user.ID, UpdateUserInput{
		Password: &weakPassword,
	})
	assert.Error(t, err, "UpdateUser() with weak password should return error")
}

func TestService_UpdateUser_InvalidRole(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	// Create user
	user, err := svc.CreateUser(ctx, CreateUserInput{
		Username: "testuser",
		Password: "password123",
		Role:     auth.RoleViewer,
	})
	require.NoError(t, err)

	// Try to update with invalid role
	invalidRole := auth.Role("invalid-role")
	_, err = svc.UpdateUser(ctx, user.ID, UpdateUserInput{
		Role: &invalidRole,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid role")
}

func TestService_GetUserFromToken_UserDeleted(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	// Create user
	user, err := svc.CreateUser(ctx, CreateUserInput{
		Username: "testuser",
		Password: "password123",
		Role:     auth.RoleViewer,
	})
	require.NoError(t, err)

	// Generate token for user
	tokenResult, err := svc.GenerateToken(user)
	require.NoError(t, err)

	// Delete the user
	err = svc.DeleteUser(ctx, user.ID, "other-user-id")
	require.NoError(t, err)

	// Try to get user from token - should return ErrInvalidToken since user was deleted
	_, err = svc.GetUserFromToken(ctx, tokenResult.Token)
	assert.ErrorIs(t, err, ErrInvalidToken, "GetUserFromToken should return ErrInvalidToken when user is deleted")
}

func TestService_ValidateToken_MalformedToken(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	tests := []struct {
		name  string
		token string
	}{
		{"empty token", ""},
		{"random string", "not-a-jwt-token"},
		{"invalid base64", "header.payload.signature"},
		{"missing parts", "onlyonepart"},
		{"jwt-like but invalid", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.invalid.signature"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.ValidateToken(tt.token)
			assert.ErrorIs(t, err, ErrInvalidToken)
		})
	}
}

func TestService_ValidateToken_WrongSecret(t *testing.T) {
	// Create service with one secret
	store, err := persiststore.NewUserStore(testutil.NewMemoryBackend().Collection("users"))
	require.NoError(t, err)

	config1 := Config{
		TokenSecret: mustTokenSecret("secret-one"),
		TokenTTL:    time.Hour,
		BcryptCost:  4,
	}
	svc1 := New(store, config1)

	// Create user and generate token with svc1
	ctx := context.Background()
	user, err := svc1.CreateUser(ctx, CreateUserInput{
		Username: "testuser",
		Password: "password123",
		Role:     auth.RoleViewer,
	})
	require.NoError(t, err)

	tokenResult, err := svc1.GenerateToken(user)
	require.NoError(t, err)

	// Create service with different secret
	config2 := Config{
		TokenSecret: mustTokenSecret("secret-two"),
		TokenTTL:    time.Hour,
		BcryptCost:  4,
	}
	svc2 := New(store, config2)

	// Try to validate token with wrong secret
	_, err = svc2.ValidateToken(tokenResult.Token)
	assert.ErrorIs(t, err, ErrInvalidToken, "ValidateToken should reject token signed with different secret")
}

func TestService_ValidateToken_MissingSecret(t *testing.T) {
	store, err := persiststore.NewUserStore(testutil.NewMemoryBackend().Collection("users"))
	require.NoError(t, err)

	// Create service without secret
	config := Config{
		TokenSecret: auth.TokenSecret{},
		TokenTTL:    time.Hour,
		BcryptCost:  4,
	}
	svc := New(store, config)

	_, err = svc.ValidateToken("some-token")
	assert.ErrorIs(t, err, ErrMissingSecret)
}

func TestService_CreateUser_InvalidRole(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	_, err := svc.CreateUser(ctx, CreateUserInput{
		Username: "testuser",
		Password: "password123",
		Role:     auth.Role("invalid-role"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid role")
}

func TestService_GetUserFromToken_PasswordChangeInvalidatesToken(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	user, err := svc.CreateUser(ctx, CreateUserInput{
		Username: "testuser",
		Password: "password123",
		Role:     auth.RoleAdmin,
	})
	require.NoError(t, err)

	// Issue token before password change.
	tokenResult, err := svc.GenerateToken(user)
	require.NoError(t, err)

	// Token is valid before password change.
	_, err = svc.GetUserFromToken(ctx, tokenResult.Token)
	require.NoError(t, err, "token should be valid before password change")

	// Change password — stamps PasswordChangedAt on the user record.
	err = svc.ChangePassword(ctx, user.ID, "password123", "newpassword456")
	require.NoError(t, err)

	// Old token must now be rejected.
	_, err = svc.GetUserFromToken(ctx, tokenResult.Token)
	assert.ErrorIs(t, err, ErrInvalidToken, "old token must be rejected after password change")
}

func TestService_GetUserFromToken_NewTokenValidAfterPasswordChange(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	user, err := svc.CreateUser(ctx, CreateUserInput{
		Username: "testuser",
		Password: "password123",
		Role:     auth.RoleAdmin,
	})
	require.NoError(t, err)

	err = svc.ChangePassword(ctx, user.ID, "password123", "newpassword456")
	require.NoError(t, err)

	// Fetch updated user (has PasswordChangedAt set) and issue fresh token.
	updatedUser, err := svc.GetUser(ctx, user.ID)
	require.NoError(t, err)

	newToken, err := svc.GenerateToken(updatedUser)
	require.NoError(t, err)

	// New token must be accepted.
	_, err = svc.GetUserFromToken(ctx, newToken.Token)
	require.NoError(t, err, "token issued after password change should be valid")
}

func TestService_GetUserFromToken_ResetPasswordInvalidatesToken(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	user, err := svc.CreateUser(ctx, CreateUserInput{
		Username: "victim",
		Password: "password123",
		Role:     auth.RoleViewer,
	})
	require.NoError(t, err)

	tokenResult, err := svc.GenerateToken(user)
	require.NoError(t, err)

	// Admin resets password.
	err = svc.ResetPassword(ctx, user.ID, "adminreset789")
	require.NoError(t, err)

	// Old token must be rejected.
	_, err = svc.GetUserFromToken(ctx, tokenResult.Token)
	assert.ErrorIs(t, err, ErrInvalidToken, "token must be rejected after admin password reset")
}

func TestService_GetUserFromToken_NoPasswordChangeTokenStillValid(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	// User never changes password — PasswordChangedAt stays nil.
	user, err := svc.CreateUser(ctx, CreateUserInput{
		Username: "testuser",
		Password: "password123",
		Role:     auth.RoleViewer,
	})
	require.NoError(t, err)

	tokenResult, err := svc.GenerateToken(user)
	require.NoError(t, err)

	// Token must remain valid when password never changed.
	_, err = svc.GetUserFromToken(ctx, tokenResult.Token)
	require.NoError(t, err, "token should remain valid when user never changed password")
}

func setupTestServiceWithAPIKeys(t *testing.T) (*Service, func()) {
	t.Helper()
	backend := testutil.NewMemoryBackend()
	apiKeyStore, err := persiststore.NewAPIKeyStore(backend.Collection("apikeys"))
	require.NoError(t, err, "failed to create API key store")
	return newTestServiceWithAPIKeyStore(t, apiKeyStore), func() {}
}

func newTestServiceWithAPIKeyStore(tb testing.TB, apiKeyStore auth.APIKeyStore) *Service {
	tb.Helper()
	userStore, err := persiststore.NewUserStore(testutil.NewMemoryBackend().Collection("users"))
	require.NoError(tb, err, "failed to create user store")
	config := Config{
		TokenSecret: mustTokenSecret("test-secret-key-for-jwt-signing"),
		TokenTTL:    time.Hour,
		BcryptCost:  4, // Low cost for faster tests
	}
	return New(userStore, config, WithAPIKeyStore(apiKeyStore))
}

type observedAPIKeyStore struct {
	auth.APIKeyStore

	mu                  sync.Mutex
	promoteCalls        int
	updateLastUsedCalls int
	promoteErr          error
	updateLastUsedErr   error
}

func (s *observedAPIKeyStore) PromoteDigest(ctx context.Context, id, digest string) error {
	s.mu.Lock()
	s.promoteCalls++
	err := s.promoteErr
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.APIKeyStore.PromoteDigest(ctx, id, digest)
}

func (s *observedAPIKeyStore) UpdateLastUsed(ctx context.Context, id string) error {
	s.mu.Lock()
	s.updateLastUsedCalls++
	err := s.updateLastUsedErr
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.APIKeyStore.UpdateLastUsed(ctx, id)
}

func (s *observedAPIKeyStore) counts() (promote, updateLastUsed int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.promoteCalls, s.updateLastUsedCalls
}

func createLegacyAPIKey(tb testing.TB, store auth.APIKeyStore, name string) (*auth.APIKey, *apiKeyParts) {
	tb.Helper()
	parts, err := generateAPIKey(4)
	require.NoError(tb, err)
	key, err := auth.NewAPIKey(name, "", auth.RoleViewer, parts.keyHash, parts.keyPrefix, "creator-id")
	require.NoError(tb, err)
	require.NoError(tb, store.Create(context.Background(), key))
	return key, parts
}

func TestService_CreateAPIKey(t *testing.T) {
	svc, cleanup := setupTestServiceWithAPIKeys(t)
	defer cleanup()

	ctx := context.Background()

	result, err := svc.CreateAPIKey(ctx, CreateAPIKeyInput{
		Name:        "test-key",
		Description: "Test API key",
		Role:        auth.RoleManager,
	}, "creator-id")
	require.NoError(t, err)
	require.NotNil(t, result.APIKey)

	assert.Equal(t, "test-key", result.APIKey.Name)
	assert.Equal(t, "Test API key", result.APIKey.Description)
	assert.Equal(t, auth.RoleManager, result.APIKey.Role)
	assert.Equal(t, "creator-id", result.APIKey.CreatedBy)
	assert.NotEmpty(t, result.FullKey)
	assert.True(t, strings.HasPrefix(result.FullKey, "dagu_"), "full key should start with 'dagu_'")
	assert.NotEmpty(t, result.APIKey.KeyPrefix)
	assert.NotEmpty(t, result.APIKey.KeyHash)
	assert.Equal(t, apiKeyDigest(result.FullKey), result.APIKey.KeyDigest)
}

func TestService_CreateAPIKey_EmptyName(t *testing.T) {
	svc, cleanup := setupTestServiceWithAPIKeys(t)
	defer cleanup()

	ctx := context.Background()

	_, err := svc.CreateAPIKey(ctx, CreateAPIKeyInput{
		Name: "",
		Role: auth.RoleViewer,
	}, "creator-id")
	require.ErrorIs(t, err, auth.ErrInvalidAPIKeyName)
}

func TestService_CreateAPIKey_InvalidRole(t *testing.T) {
	svc, cleanup := setupTestServiceWithAPIKeys(t)
	defer cleanup()

	ctx := context.Background()

	_, err := svc.CreateAPIKey(ctx, CreateAPIKeyInput{
		Name: "test-key",
		Role: auth.Role("invalid"),
	}, "creator-id")
	require.Error(t, err, "CreateAPIKey() with invalid role should return error")
}

func TestService_CreateAPIKey_NotConfigured(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	_, err := svc.CreateAPIKey(ctx, CreateAPIKeyInput{
		Name: "test-key",
		Role: auth.RoleViewer,
	}, "creator-id")
	require.ErrorIs(t, err, ErrAPIKeyNotConfigured)
}

func TestService_GetAPIKey(t *testing.T) {
	svc, cleanup := setupTestServiceWithAPIKeys(t)
	defer cleanup()

	ctx := context.Background()

	// Create an API key
	result, err := svc.CreateAPIKey(ctx, CreateAPIKeyInput{
		Name:        "test-key",
		Description: "Test API key",
		Role:        auth.RoleManager,
	}, "creator-id")
	require.NoError(t, err)

	// Get the API key
	apiKey, err := svc.GetAPIKey(ctx, result.APIKey.ID)
	require.NoError(t, err)

	assert.Equal(t, result.APIKey.ID, apiKey.ID)
	assert.Equal(t, "test-key", apiKey.Name)
}

func TestService_GetAPIKey_NotFound(t *testing.T) {
	svc, cleanup := setupTestServiceWithAPIKeys(t)
	defer cleanup()

	ctx := context.Background()

	_, err := svc.GetAPIKey(ctx, "non-existent-id")
	require.ErrorIs(t, err, auth.ErrAPIKeyNotFound)
}

func TestService_GetAPIKey_NotConfigured(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	_, err := svc.GetAPIKey(ctx, "some-id")
	require.ErrorIs(t, err, ErrAPIKeyNotConfigured)
}

func TestService_ListAPIKeys(t *testing.T) {
	svc, cleanup := setupTestServiceWithAPIKeys(t)
	defer cleanup()

	ctx := context.Background()

	// Create multiple API keys
	for i := range 3 {
		_, err := svc.CreateAPIKey(ctx, CreateAPIKeyInput{
			Name: fmt.Sprintf("key-%d", i),
			Role: auth.RoleViewer,
		}, "creator-id")
		require.NoError(t, err)
	}

	// List API keys
	keys, err := svc.ListAPIKeys(ctx)
	require.NoError(t, err)
	assert.Len(t, keys, 3)
}

func TestService_ListAPIKeys_Empty(t *testing.T) {
	svc, cleanup := setupTestServiceWithAPIKeys(t)
	defer cleanup()

	ctx := context.Background()

	keys, err := svc.ListAPIKeys(ctx)
	require.NoError(t, err)
	assert.Empty(t, keys)
}

func TestService_ListAPIKeys_NotConfigured(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	_, err := svc.ListAPIKeys(ctx)
	require.ErrorIs(t, err, ErrAPIKeyNotConfigured)
}

func TestService_UpdateAPIKey(t *testing.T) {
	svc, cleanup := setupTestServiceWithAPIKeys(t)
	defer cleanup()

	ctx := context.Background()

	// Create an API key
	result, err := svc.CreateAPIKey(ctx, CreateAPIKeyInput{
		Name:        "original-name",
		Description: "Original description",
		Role:        auth.RoleViewer,
	}, "creator-id")
	require.NoError(t, err)

	// Update the API key
	newName := "updated-name"
	newDesc := "Updated description"
	newRole := auth.RoleAdmin
	updated, err := svc.UpdateAPIKey(ctx, result.APIKey.ID, UpdateAPIKeyInput{
		Name:        &newName,
		Description: &newDesc,
		Role:        &newRole,
	})
	require.NoError(t, err)

	assert.Equal(t, "updated-name", updated.Name)
	assert.Equal(t, "Updated description", updated.Description)
	assert.Equal(t, auth.RoleAdmin, updated.Role)
}

func TestService_UpdateAPIKey_PartialUpdate(t *testing.T) {
	svc, cleanup := setupTestServiceWithAPIKeys(t)
	defer cleanup()

	ctx := context.Background()

	// Create an API key
	result, err := svc.CreateAPIKey(ctx, CreateAPIKeyInput{
		Name:        "original-name",
		Description: "Original description",
		Role:        auth.RoleViewer,
	}, "creator-id")
	require.NoError(t, err)

	// Update only the name
	newName := "updated-name"
	updated, err := svc.UpdateAPIKey(ctx, result.APIKey.ID, UpdateAPIKeyInput{
		Name: &newName,
	})
	require.NoError(t, err)

	assert.Equal(t, "updated-name", updated.Name)
	// Other fields should remain unchanged
	assert.Equal(t, "Original description", updated.Description)
	assert.Equal(t, auth.RoleViewer, updated.Role)
}

func TestService_UpdateAPIKey_InvalidRole(t *testing.T) {
	svc, cleanup := setupTestServiceWithAPIKeys(t)
	defer cleanup()

	ctx := context.Background()

	// Create an API key
	result, err := svc.CreateAPIKey(ctx, CreateAPIKeyInput{
		Name: "test-key",
		Role: auth.RoleViewer,
	}, "creator-id")
	require.NoError(t, err)

	// Try to update with invalid role
	invalidRole := auth.Role("invalid")
	_, err = svc.UpdateAPIKey(ctx, result.APIKey.ID, UpdateAPIKeyInput{
		Role: &invalidRole,
	})
	require.Error(t, err, "UpdateAPIKey() with invalid role should return error")
}

func TestService_UpdateAPIKey_NotFound(t *testing.T) {
	svc, cleanup := setupTestServiceWithAPIKeys(t)
	defer cleanup()

	ctx := context.Background()

	newName := "updated-name"
	_, err := svc.UpdateAPIKey(ctx, "non-existent-id", UpdateAPIKeyInput{
		Name: &newName,
	})
	require.ErrorIs(t, err, auth.ErrAPIKeyNotFound)
}

func TestService_UpdateAPIKey_NotConfigured(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	newName := "updated-name"
	_, err := svc.UpdateAPIKey(ctx, "some-id", UpdateAPIKeyInput{
		Name: &newName,
	})
	require.ErrorIs(t, err, ErrAPIKeyNotConfigured)
}

func TestService_DeleteAPIKey(t *testing.T) {
	svc, cleanup := setupTestServiceWithAPIKeys(t)
	defer cleanup()

	ctx := context.Background()

	// Create an API key
	result, err := svc.CreateAPIKey(ctx, CreateAPIKeyInput{
		Name: "test-key",
		Role: auth.RoleViewer,
	}, "creator-id")
	require.NoError(t, err)

	// Delete the API key
	err = svc.DeleteAPIKey(ctx, result.APIKey.ID)
	require.NoError(t, err)

	// Verify it's deleted
	_, err = svc.GetAPIKey(ctx, result.APIKey.ID)
	require.ErrorIs(t, err, auth.ErrAPIKeyNotFound)
}

func TestService_DeleteAPIKey_NotFound(t *testing.T) {
	svc, cleanup := setupTestServiceWithAPIKeys(t)
	defer cleanup()

	ctx := context.Background()

	err := svc.DeleteAPIKey(ctx, "non-existent-id")
	require.ErrorIs(t, err, auth.ErrAPIKeyNotFound)
}

func TestService_DeleteAPIKey_NotConfigured(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	err := svc.DeleteAPIKey(ctx, "some-id")
	require.ErrorIs(t, err, ErrAPIKeyNotConfigured)
}

func TestService_ValidateAPIKey(t *testing.T) {
	svc, cleanup := setupTestServiceWithAPIKeys(t)
	defer cleanup()

	ctx := context.Background()

	// Create an API key
	result, err := svc.CreateAPIKey(ctx, CreateAPIKeyInput{
		Name: "test-key",
		Role: auth.RoleManager,
	}, "creator-id")
	require.NoError(t, err)

	// Validate the API key
	apiKey, err := svc.ValidateAPIKey(ctx, result.FullKey)
	require.NoError(t, err)

	assert.Equal(t, result.APIKey.ID, apiKey.ID)
	assert.Equal(t, "test-key", apiKey.Name)
	assert.Equal(t, auth.RoleManager, apiKey.Role)
}

func TestService_ValidateAPIKey_LazilyPersistsLegacyDigest(t *testing.T) {
	backend := testutil.NewMemoryBackend()
	store, err := persiststore.NewAPIKeyStore(backend.Collection("apikeys"))
	require.NoError(t, err)
	observed := &observedAPIKeyStore{APIKeyStore: store}
	svc := newTestServiceWithAPIKeyStore(t, observed)
	legacy, parts := createLegacyAPIKey(t, store, "legacy-key")

	validated, err := svc.ValidateAPIKey(context.Background(), parts.fullKey)
	require.NoError(t, err)
	assert.Equal(t, legacy.ID, validated.ID)

	digest := apiKeyDigest(parts.fullKey)
	persisted, err := store.GetByID(context.Background(), legacy.ID)
	require.NoError(t, err)
	assert.Equal(t, digest, persisted.KeyDigest)
	indexed, err := store.GetByDigest(context.Background(), digest)
	require.NoError(t, err)
	assert.Equal(t, legacy.ID, indexed.ID)
	restartedStore, err := persiststore.NewAPIKeyStore(backend.Collection("apikeys"))
	require.NoError(t, err)
	restartedSvc := newTestServiceWithAPIKeyStore(t, restartedStore)
	restartedKey, err := restartedSvc.ValidateAPIKey(context.Background(), parts.fullKey)
	require.NoError(t, err)
	assert.Equal(t, legacy.ID, restartedKey.ID)

	_, err = svc.ValidateAPIKey(context.Background(), parts.fullKey)
	require.NoError(t, err)
	promoteCalls, _ := observed.counts()
	assert.Equal(t, 1, promoteCalls)
}

func TestService_ValidateAPIKey_SerializesLegacyMigration(t *testing.T) {
	backend := testutil.NewMemoryBackend()
	store, err := persiststore.NewAPIKeyStore(backend.Collection("apikeys"))
	require.NoError(t, err)
	observed := &observedAPIKeyStore{APIKeyStore: store}
	svc := newTestServiceWithAPIKeyStore(t, observed)
	legacy, parts := createLegacyAPIKey(t, store, "concurrent-legacy-key")

	const workers = 24
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			<-start
			key, err := svc.ValidateAPIKey(context.Background(), parts.fullKey)
			if err == nil && key.ID != legacy.ID {
				err = fmt.Errorf("validated key ID %q, want %q", key.ID, legacy.ID)
			}
			errs <- err
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	promoteCalls, _ := observed.counts()
	assert.Equal(t, 1, promoteCalls)
}

func TestService_ValidateAPIKey_PromotionFailureDoesNotRejectLegacyKey(t *testing.T) {
	backend := testutil.NewMemoryBackend()
	store, err := persiststore.NewAPIKeyStore(backend.Collection("apikeys"))
	require.NoError(t, err)
	observed := &observedAPIKeyStore{
		APIKeyStore: store,
		promoteErr:  errors.New("promotion failed"),
	}
	svc := newTestServiceWithAPIKeyStore(t, observed)
	legacy, parts := createLegacyAPIKey(t, store, "promotion-failure-key")

	validated, err := svc.ValidateAPIKey(context.Background(), parts.fullKey)
	require.NoError(t, err)
	assert.Equal(t, legacy.ID, validated.ID)

	persisted, err := store.GetByID(context.Background(), legacy.ID)
	require.NoError(t, err)
	assert.Empty(t, persisted.KeyDigest)
}

func TestService_ValidateAPIKey_DoesNotPromoteLegacyKeyForWrongSecret(t *testing.T) {
	backend := testutil.NewMemoryBackend()
	store, err := persiststore.NewAPIKeyStore(backend.Collection("apikeys"))
	require.NoError(t, err)
	observed := &observedAPIKeyStore{APIKeyStore: store}
	svc := newTestServiceWithAPIKeyStore(t, observed)
	legacy, parts := createLegacyAPIKey(t, store, "wrong-secret-legacy-key")
	wrongSecret := parts.keyPrefix + "different-secret"

	_, err = svc.ValidateAPIKey(context.Background(), wrongSecret)
	require.ErrorIs(t, err, ErrInvalidAPIKey)

	persisted, err := store.GetByID(context.Background(), legacy.ID)
	require.NoError(t, err)
	assert.Empty(t, persisted.KeyDigest)
	promoteCalls, _ := observed.counts()
	assert.Zero(t, promoteCalls)
}

func TestService_ValidateAPIKey_PromotesLegacyKeyBeforeRejectingDisabledOwner(t *testing.T) {
	backend := testutil.NewMemoryBackend()
	store, err := persiststore.NewAPIKeyStore(backend.Collection("apikeys"))
	require.NoError(t, err)
	svc := newTestServiceWithAPIKeyStore(t, store)
	owner := auth.NewUser("disabled-owner", "unused-password-hash", auth.RoleViewer)
	owner.IsDisabled = true
	require.NoError(t, svc.store.Create(context.Background(), owner))

	legacy, parts := createLegacyAPIKey(t, store, "disabled-owner-legacy-key")
	legacy.AttributionClass = auth.APIKeyAttributionUserOwned
	legacy.OwnerUserID = owner.ID
	require.NoError(t, store.Update(context.Background(), legacy))

	_, err = svc.ValidateAPIKey(context.Background(), parts.fullKey)
	require.ErrorIs(t, err, ErrInvalidAPIKey)
	persisted, err := store.GetByID(context.Background(), legacy.ID)
	require.NoError(t, err)
	assert.Equal(t, apiKeyDigest(parts.fullKey), persisted.KeyDigest)
}

func TestService_ValidateAPIKey_InvalidPrefix(t *testing.T) {
	svc, cleanup := setupTestServiceWithAPIKeys(t)
	defer cleanup()

	ctx := context.Background()

	_, err := svc.ValidateAPIKey(ctx, "invalid_prefix_key")
	require.ErrorIs(t, err, ErrInvalidAPIKey)

	_, err = svc.ValidateAPIKey(ctx, apiKeyPrefix)
	require.ErrorIs(t, err, ErrInvalidAPIKey)
}

func TestService_ValidateAPIKey_WrongKey(t *testing.T) {
	svc, cleanup := setupTestServiceWithAPIKeys(t)
	defer cleanup()

	ctx := context.Background()

	// Create an API key
	_, err := svc.CreateAPIKey(ctx, CreateAPIKeyInput{
		Name: "test-key",
		Role: auth.RoleViewer,
	}, "creator-id")
	require.NoError(t, err)

	// Try to validate with wrong key (correct prefix but wrong value)
	_, err = svc.ValidateAPIKey(ctx, "dagu_wrongkeywrongkeywrongkeywrongkey")
	require.ErrorIs(t, err, ErrInvalidAPIKey)
}

func TestService_ValidateAPIKey_NotConfigured(t *testing.T) {
	svc, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()

	_, err := svc.ValidateAPIKey(ctx, "dagu_somekey")
	require.ErrorIs(t, err, ErrAPIKeyNotConfigured)
}

func TestService_HasAPIKeyStore(t *testing.T) {
	// Test with API key store
	svcWithStore, cleanup1 := setupTestServiceWithAPIKeys(t)
	defer cleanup1()

	assert.True(t, svcWithStore.HasAPIKeyStore(), "HasAPIKeyStore() should return true when configured")

	// Test without API key store
	svcWithoutStore, cleanup2 := setupTestService(t)
	defer cleanup2()

	assert.False(t, svcWithoutStore.HasAPIKeyStore(), "HasAPIKeyStore() should return false when not configured")
}

func TestService_CreateAPIKey_EmptyCreatorID(t *testing.T) {
	svc, cleanup := setupTestServiceWithAPIKeys(t)
	defer cleanup()

	ctx := context.Background()

	_, err := svc.CreateAPIKey(ctx, CreateAPIKeyInput{
		Name: "test-key",
		Role: auth.RoleViewer,
	}, "") // Empty creator ID
	require.ErrorIs(t, err, ErrInvalidCreatorID)
}

func TestService_ValidateAPIKey_UpdatesLastUsed(t *testing.T) {
	svc, cleanup := setupTestServiceWithAPIKeys(t)
	defer cleanup()

	ctx := context.Background()

	// Create an API key
	result, err := svc.CreateAPIKey(ctx, CreateAPIKeyInput{
		Name: "lastused-key",
		Role: auth.RoleViewer,
	}, "creator-id")
	require.NoError(t, err)

	// Verify LastUsedAt is nil initially
	apiKey, err := svc.GetAPIKey(ctx, result.APIKey.ID)
	require.NoError(t, err)
	assert.Nil(t, apiKey.LastUsedAt, "LastUsedAt should be nil initially")

	// Validate the API key (this updates LastUsedAt synchronously)
	_, err = svc.ValidateAPIKey(ctx, result.FullKey)
	require.NoError(t, err)

	// Verify LastUsedAt is now populated
	apiKey2, err := svc.GetAPIKey(ctx, result.APIKey.ID)
	require.NoError(t, err)
	require.NotNil(t, apiKey2.LastUsedAt, "LastUsedAt should be populated after validation")
}

func TestService_ValidateAPIKey_ThrottlesLastUsedUpdates(t *testing.T) {
	backend := testutil.NewMemoryBackend()
	store, err := persiststore.NewAPIKeyStore(backend.Collection("apikeys"))
	require.NoError(t, err)
	observed := &observedAPIKeyStore{APIKeyStore: store}
	svc := newTestServiceWithAPIKeyStore(t, observed)

	result, err := svc.CreateAPIKey(context.Background(), CreateAPIKeyInput{
		Name: "last-used-throttle-key",
		Role: auth.RoleViewer,
	}, "creator-id")
	require.NoError(t, err)

	_, err = svc.ValidateAPIKey(context.Background(), result.FullKey)
	require.NoError(t, err)
	_, err = svc.ValidateAPIKey(context.Background(), result.FullKey)
	require.NoError(t, err)
	_, updateCalls := observed.counts()
	assert.Equal(t, 1, updateCalls)
}

func TestService_ValidateAPIKey_LastUsedFailureDoesNotRejectKey(t *testing.T) {
	backend := testutil.NewMemoryBackend()
	store, err := persiststore.NewAPIKeyStore(backend.Collection("apikeys"))
	require.NoError(t, err)
	observed := &observedAPIKeyStore{
		APIKeyStore:       store,
		updateLastUsedErr: errors.New("last-used update failed"),
	}
	svc := newTestServiceWithAPIKeyStore(t, observed)

	result, err := svc.CreateAPIKey(context.Background(), CreateAPIKeyInput{
		Name: "last-used-failure-key",
		Role: auth.RoleViewer,
	}, "creator-id")
	require.NoError(t, err)
	validated, err := svc.ValidateAPIKey(context.Background(), result.FullKey)
	require.NoError(t, err)
	assert.Equal(t, result.APIKey.ID, validated.ID)
}

func TestGenerateAPIKey(t *testing.T) {
	// Test API key generation
	keyParts, err := generateAPIKey(4) // Low cost for fast tests
	require.NoError(t, err)

	// Verify full key has correct prefix
	assert.True(t, strings.HasPrefix(keyParts.fullKey, "dagu_"), "Full key should start with 'dagu_'")

	// Verify key prefix is correct length
	assert.Len(t, keyParts.keyPrefix, apiKeyPrefixLength, "Key prefix should be %d characters", apiKeyPrefixLength)

	// Verify key prefix matches start of full key
	assert.Equal(t, keyParts.fullKey[:apiKeyPrefixLength], keyParts.keyPrefix, "Key prefix should match start of full key")

	// Verify hash is valid bcrypt hash
	assert.NotEmpty(t, keyParts.keyHash, "Key hash should not be empty")
	assert.True(t, strings.HasPrefix(keyParts.keyHash, "$2"), "Key hash should be bcrypt format")
	assert.Equal(t, apiKeyDigest(keyParts.fullKey), keyParts.keyDigest)

	// Verify full key is long enough (should be at least 40 chars: 5 prefix + 32 bytes base58)
	assert.GreaterOrEqual(t, len(keyParts.fullKey), 40, "Full key should be at least 40 characters")
}

func TestAPIKeyDigest(t *testing.T) {
	assert.Equal(t,
		"sha256:v1:dffc41464f28bdb8ce95d13ecf9acd5ee6bf07a4b56860688431d8e803200514",
		apiKeyDigest("dagu_example"),
	)
}

func TestGenerateAPIKey_UniqueKeys(t *testing.T) {
	// Generate multiple keys and verify they are unique
	keys := make(map[string]bool)
	for range 100 {
		keyParts, err := generateAPIKey(4)
		require.NoError(t, err)
		assert.False(t, keys[keyParts.fullKey], "Generated key should be unique")
		keys[keyParts.fullKey] = true
	}
}

func BenchmarkService_ValidateAPIKey_DigestPath(b *testing.B) {
	backend := testutil.NewMemoryBackend()
	apiKeyStore, err := persiststore.NewAPIKeyStore(backend.Collection("apikeys"))
	require.NoError(b, err)
	svc := newTestServiceWithAPIKeyStore(b, apiKeyStore)
	const fullKey = "dagu_benchmark_digest_path_secret"
	key, err := auth.NewAPIKey(
		"benchmark-key",
		"",
		auth.RoleViewer,
		"unused-bcrypt-hash",
		fullKey[:apiKeyPrefixLength],
		"creator-id",
	)
	require.NoError(b, err)
	key.KeyDigest = apiKeyDigest(fullKey)
	require.NoError(b, apiKeyStore.Create(context.Background(), key))
	_, err = svc.ValidateAPIKey(context.Background(), fullKey)
	require.NoError(b, err)

	var firstErr error
	var errOnce sync.Once
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := svc.ValidateAPIKey(context.Background(), fullKey); err != nil {
				errOnce.Do(func() { firstErr = err })
				return
			}
		}
	})
	if firstErr != nil {
		b.Fatal(firstErr)
	}
}
