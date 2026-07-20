// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package trustedproxyprovision

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	authservice "github.com/dagucloud/dagu/internal/service/auth"
	"github.com/dagucloud/dagu/internal/service/authmapping"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestUserStore(t *testing.T) *store.UserStore {
	t.Helper()
	userStore, err := store.NewUserStore(testutil.NewMemoryBackend().Collection("users"))
	require.NoError(t, err)
	return userStore
}

func seedUser(t *testing.T, userStore auth.UserStore, username string) *auth.User {
	t.Helper()
	user := auth.NewUser(username, "hash", auth.RoleAdmin)
	user.AuthProvider = auth.AuthProviderBuiltin
	require.NoError(t, userStore.Create(context.Background(), user))
	return user
}

func newTestService(t *testing.T, userStore auth.UserStore, mutate func(*Config)) *Service {
	t.Helper()
	config := Config{
		UsersDir:   t.TempDir(),
		AutoSignup: true,
		RoleMapping: authmapping.Config{
			DefaultRole: auth.RoleViewer,
		},
	}
	if mutate != nil {
		mutate(&config)
	}
	service, err := New(userStore, config)
	require.NoError(t, err)
	return service
}

func TestProcessLoginRequiresInitialSetup(t *testing.T) {
	service := newTestService(t, newTestUserStore(t), nil)
	_, _, err := service.ProcessLogin(context.Background(), "opaque-user", nil)
	assert.ErrorIs(t, err, ErrInitialSetupRequired)
}

func TestProcessLoginCreatesTrustedProxyUser(t *testing.T) {
	userStore := newTestUserStore(t)
	seedUser(t, userStore, "admin")
	service := newTestService(t, userStore, func(config *Config) {
		config.Source = "edge-a"
	})

	user, created, err := service.ProcessLogin(context.Background(), "Alice@example.com", nil)
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, "alice_example_com", user.Username)
	assert.Equal(t, auth.AuthProviderProxy, user.AuthProvider)
	assert.Equal(t, "edge-a", user.TrustedProxySource)
	assert.Equal(t, "Alice@example.com", user.TrustedProxyUser)
	assert.Empty(t, user.PasswordHash)
	assert.Equal(t, auth.RoleViewer, user.Role)
	assert.True(t, auth.NormalizeWorkspaceAccess(user.WorkspaceAccess).All)

	persisted, err := userStore.GetByTrustedProxyIdentity(context.Background(), "edge-a", "Alice@example.com")
	require.NoError(t, err)
	assert.Equal(t, user.ID, persisted.ID)
}

func TestProcessLoginSeparatesIdenticalUsersBySource(t *testing.T) {
	ctx := context.Background()
	userStore := newTestUserStore(t)
	seedUser(t, userStore, "admin")
	edgeA := newTestService(t, userStore, func(config *Config) {
		config.Source = "edge-a"
	})
	edgeB := newTestService(t, userStore, func(config *Config) {
		config.Source = "edge-b"
	})

	first, created, err := edgeA.ProcessLogin(ctx, "same-user", nil)
	require.NoError(t, err)
	assert.True(t, created)
	second, created, err := edgeB.ProcessLogin(ctx, "same-user", nil)
	require.NoError(t, err)
	assert.True(t, created)

	assert.NotEqual(t, first.ID, second.ID)
	assert.NotEqual(t, first.Username, second.Username)
	assert.Equal(t, "edge-a", first.TrustedProxySource)
	assert.Equal(t, "edge-b", second.TrustedProxySource)
	assert.Equal(t, "same-user", first.TrustedProxyUser)
	assert.Equal(t, "same-user", second.TrustedProxyUser)

	resolved, err := userStore.GetByTrustedProxyIdentity(ctx, "edge-a", "same-user")
	require.NoError(t, err)
	assert.Equal(t, first.ID, resolved.ID)
	resolved, err = userStore.GetByTrustedProxyIdentity(ctx, "edge-b", "same-user")
	require.NoError(t, err)
	assert.Equal(t, second.ID, resolved.ID)
}

func TestProcessLoginReprovisionsDeletedIdentity(t *testing.T) {
	ctx := context.Background()
	userStore := newTestUserStore(t)
	seedUser(t, userStore, "admin")
	service := newTestService(t, userStore, nil)

	first, created, err := service.ProcessLogin(ctx, "stable-identity", nil)
	require.NoError(t, err)
	assert.True(t, created)
	require.NoError(t, userStore.Delete(ctx, first.ID))

	second, created, err := service.ProcessLogin(ctx, "stable-identity", nil)
	require.NoError(t, err)
	assert.True(t, created)
	assert.NotEqual(t, first.ID, second.ID)
	assert.Equal(t, first.Username, second.Username)
	assert.Equal(t, "stable-identity", second.TrustedProxyUser)
}

func TestProcessLoginUsesZeroGrantFallback(t *testing.T) {
	userStore := newTestUserStore(t)
	seedUser(t, userStore, "admin")
	service := newTestService(t, userStore, func(config *Config) {
		config.RoleMapping.DefaultWorkspaceAccess = authmapping.DefaultWorkspaceAccessNone
	})

	user, _, err := service.ProcessLogin(context.Background(), "scoped-user", nil)
	require.NoError(t, err)
	assert.Equal(t, auth.RoleViewer, user.Role)
	assert.Equal(t, &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{}}, user.WorkspaceAccess)
}

func TestProcessLoginChecksDormantWorkspaceGrantsWithoutDenyingLogin(t *testing.T) {
	userStore := newTestUserStore(t)
	seedUser(t, userStore, "admin")
	var lookedUp []string
	service := newTestService(t, userStore, func(config *Config) {
		config.RoleMapping.WorkspaceMappings = map[string][]authmapping.WorkspaceGrantConfig{
			"team": {
				{Workspace: "payments", Role: auth.RoleDeveloper},
				{Workspace: "infra", Role: auth.RoleViewer},
			},
		}
		config.WorkspaceExists = func(_ context.Context, workspaceName string) (bool, error) {
			lookedUp = append(lookedUp, workspaceName)
			if workspaceName == "infra" {
				return false, errors.New("lookup failed")
			}
			return false, nil
		}
	})

	user, created, err := service.ProcessLogin(context.Background(), "scoped-user", []string{"team"})
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, []string{"infra", "payments"}, lookedUp)
	assert.Equal(t, &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{
		{Workspace: "infra", Role: auth.RoleViewer},
		{Workspace: "payments", Role: auth.RoleDeveloper},
	}}, user.WorkspaceAccess)
}

func TestProcessLoginRejectsUnknownIdentityWhenAutoSignupDisabled(t *testing.T) {
	userStore := newTestUserStore(t)
	seedUser(t, userStore, "admin")
	service := newTestService(t, userStore, func(config *Config) { config.AutoSignup = false })

	_, _, err := service.ProcessLogin(context.Background(), "unknown", nil)
	assert.ErrorIs(t, err, ErrAutoSignupDisabled)
}

func TestProcessLoginRejectsStrictMappingMiss(t *testing.T) {
	userStore := newTestUserStore(t)
	seedUser(t, userStore, "admin")
	service := newTestService(t, userStore, func(config *Config) {
		config.RoleMapping.Strict = true
		config.RoleMapping.GroupMappings = map[string]auth.Role{"dagu-admins": auth.RoleAdmin}
	})

	_, _, err := service.ProcessLogin(context.Background(), "unknown", []string{"other"})
	assert.ErrorIs(t, err, ErrAuthorizationMapping)
}

func TestProcessLoginSynchronizesFullAuthorization(t *testing.T) {
	ctx := context.Background()
	userStore := newTestUserStore(t)
	seedUser(t, userStore, "admin")
	existing := auth.NewUser("renamed-user", "", auth.RoleDeveloper)
	existing.AuthProvider = auth.AuthProviderProxy
	existing.TrustedProxyUser = "opaque-user"
	existing.WorkspaceAccess = auth.AllWorkspaceAccess()
	require.NoError(t, userStore.Create(ctx, existing))

	service := newTestService(t, userStore, func(config *Config) {
		config.RoleMapping.DefaultWorkspaceAccess = authmapping.DefaultWorkspaceAccessNone
	})
	user, created, err := service.ProcessLogin(ctx, "opaque-user", nil)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, "renamed-user", user.Username)
	assert.Equal(t, auth.RoleViewer, user.Role)
	assert.Equal(t, &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{}}, user.WorkspaceAccess)

	persisted, err := userStore.GetByID(ctx, existing.ID)
	require.NoError(t, err)
	assert.Equal(t, user.Role, persisted.Role)
	assert.Equal(t, user.WorkspaceAccess, persisted.WorkspaceAccess)
}

func TestProcessLoginRejectsExistingUserOnStrictMappingMiss(t *testing.T) {
	ctx := context.Background()
	userStore := newTestUserStore(t)
	existing := auth.NewUser("trusted", "", auth.RoleManager)
	existing.AuthProvider = auth.AuthProviderProxy
	existing.TrustedProxyUser = "opaque-user"
	require.NoError(t, userStore.Create(ctx, existing))
	service := newTestService(t, userStore, func(config *Config) {
		config.RoleMapping.Strict = true
		config.RoleMapping.GroupMappings = map[string]auth.Role{"members": auth.RoleViewer}
	})

	_, _, err := service.ProcessLogin(ctx, "opaque-user", []string{"former-member"})
	assert.ErrorIs(t, err, ErrAuthorizationMapping)
}

func TestProcessLoginRejectsDisabledUser(t *testing.T) {
	ctx := context.Background()
	userStore := newTestUserStore(t)
	existing := auth.NewUser("trusted", "", auth.RoleViewer)
	existing.AuthProvider = auth.AuthProviderProxy
	existing.TrustedProxyUser = "opaque-user"
	existing.IsDisabled = true
	require.NoError(t, userStore.Create(ctx, existing))
	service := newTestService(t, userStore, nil)

	_, _, err := service.ProcessLogin(ctx, "opaque-user", nil)
	assert.ErrorIs(t, err, authservice.ErrUserDisabled)
}

func TestProcessLoginDoesNotLinkUsernameCollision(t *testing.T) {
	ctx := context.Background()
	userStore := newTestUserStore(t)
	builtin := seedUser(t, userStore, "alice")
	service := newTestService(t, userStore, nil)

	user, created, err := service.ProcessLogin(ctx, "alice", nil)
	require.NoError(t, err)
	assert.True(t, created)
	assert.NotEqual(t, builtin.ID, user.ID)
	assert.NotEqual(t, builtin.Username, user.Username)
	assert.Regexp(t, `^alice_[0-9a-f]{12}$`, user.Username)
}

func TestProcessLoginConcurrentRequestsConverge(t *testing.T) {
	ctx := context.Background()
	userStore := newTestUserStore(t)
	seedUser(t, userStore, "admin")
	service := newTestService(t, userStore, nil)

	const requests = 12
	ids := make(chan string, requests)
	errs := make(chan error, requests)
	var createdCount atomic.Int32
	var wg sync.WaitGroup
	for range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			user, created, err := service.ProcessLogin(ctx, "same-identity", nil)
			if err != nil {
				errs <- err
				return
			}
			if created {
				createdCount.Add(1)
			}
			ids <- user.ID
		}()
	}
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		require.NoError(t, err)
	}
	var firstID string
	for id := range ids {
		if firstID == "" {
			firstID = id
		}
		assert.Equal(t, firstID, id)
	}
	assert.Equal(t, int32(1), createdCount.Load())
	count, err := userStore.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)
}

type failingAuthorizationSyncStore struct {
	auth.AuthorizationSyncUserStore
}

func (f failingAuthorizationSyncStore) SyncAuthorization(
	context.Context,
	string,
	auth.Role,
	*auth.WorkspaceAccess,
) (*auth.User, error) {
	return nil, errors.New("write failed")
}

func TestProcessLoginDoesNotExposeUnpersistedAuthorization(t *testing.T) {
	ctx := context.Background()
	userStore := newTestUserStore(t)
	existing := auth.NewUser("trusted", "", auth.RoleManager)
	existing.AuthProvider = auth.AuthProviderProxy
	existing.TrustedProxyUser = "opaque-user"
	require.NoError(t, userStore.Create(ctx, existing))
	service := newTestService(t, failingAuthorizationSyncStore{AuthorizationSyncUserStore: userStore}, nil)

	user, created, err := service.ProcessLogin(ctx, "opaque-user", nil)
	assert.Error(t, err)
	assert.Nil(t, user)
	assert.False(t, created)
	persisted, getErr := userStore.GetByID(ctx, existing.ID)
	require.NoError(t, getErr)
	assert.Equal(t, auth.RoleManager, persisted.Role)
}

type disableBeforeAuthorizationSyncStore struct {
	auth.AuthorizationSyncUserStore
	once sync.Once
}

func (s *disableBeforeAuthorizationSyncStore) SyncAuthorization(
	ctx context.Context,
	id string,
	role auth.Role,
	workspaceAccess *auth.WorkspaceAccess,
) (*auth.User, error) {
	var disableErr error
	s.once.Do(func() {
		user, err := s.GetByID(ctx, id)
		if err != nil {
			disableErr = err
			return
		}
		user.IsDisabled = true
		disableErr = s.Update(ctx, user)
	})
	if disableErr != nil {
		return nil, disableErr
	}
	return s.AuthorizationSyncUserStore.SyncAuthorization(ctx, id, role, workspaceAccess)
}

func TestProcessLoginPreservesConcurrentDisable(t *testing.T) {
	ctx := context.Background()
	userStore := newTestUserStore(t)
	existing := auth.NewUser("trusted", "", auth.RoleManager)
	existing.AuthProvider = auth.AuthProviderProxy
	existing.TrustedProxyUser = "opaque-user"
	require.NoError(t, userStore.Create(ctx, existing))
	concurrentStore := &disableBeforeAuthorizationSyncStore{AuthorizationSyncUserStore: userStore}
	service := newTestService(t, concurrentStore, nil)

	user, created, err := service.ProcessLogin(ctx, "opaque-user", nil)
	assert.Nil(t, user)
	assert.False(t, created)
	assert.ErrorIs(t, err, authservice.ErrUserDisabled)

	persisted, err := userStore.GetByID(ctx, existing.ID)
	require.NoError(t, err)
	assert.True(t, persisted.IsDisabled)
	assert.Equal(t, auth.RoleManager, persisted.Role)
}

func TestUsernameCandidatesAreDeterministicAndBounded(t *testing.T) {
	first := usernameCandidates("", "Hello, 世界")
	second := usernameCandidates("", "Hello, 世界")
	assert.Equal(t, first, second)
	assert.Len(t, first, maxUsernameTries)
	assert.Equal(t, "hello", first[0])
	for _, candidate := range first {
		assert.LessOrEqual(t, len(candidate), maxUsernameLength)
	}

	emptyBase := usernameCandidates("", "世界")
	assert.Len(t, emptyBase, maxUsernameTries)
	for _, candidate := range emptyBase {
		assert.Regexp(t, `^user_[0-9a-f]{12}$`, candidate)
	}

	edgeA := usernameCandidates("edge-a", "same-user")
	edgeB := usernameCandidates("edge-b", "same-user")
	assert.Equal(t, edgeA[0], edgeB[0])
	assert.NotEqual(t, edgeA[1], edgeB[1])
}
